package tool

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Menfre01/waveloom/pkg/hashline"
	"github.com/Menfre01/waveloom/pkg/pathutil"
)

//go:embed read_hashline_prompt.md
var readHashlinePrompt string

// ---------------------------------------------------------------------------
// ReadFileHashline — 读取文件内容，返回 hashline 格式（TAG + N:CONTENT）
// ---------------------------------------------------------------------------


type ReadFileHashlineParams struct {
	FilePath     string `json:"file_path"`     // 与 read_file 一致
	Offset       int    `json:"offset"`        // 0-based: 0 = 文件第一行
	Limit        int    `json:"limit"`         // 读取行数(0 = 不限)
	Pattern      string `json:"pattern"`       // 可选:在文件中定位子串,窗口显示第一个匹配 ±context_lines
	ContextLines int    `json:"context_lines"` // 匹配行上下各显示的行数(默认 5,最大 50)
	WorkingDir   string `json:"working_dir"`   // 工作目录(可选)
}

type ReadFileHashline struct{}

func (t *ReadFileHashline) Name() string { return "read" }

func (t *ReadFileHashline) Description() string {
	return "Read a file with TAG and line numbers for hash-anchored editing. " +
		"Use with edit — the TAG certifies the file snapshot, " +
		"line numbers are used directly in SWAP/INS/DEL operations."
}

// Prompt 返回 read 工具使用指南，由 Registry.FormatToolPrompts() 注入 system prompt。
func (t *ReadFileHashline) Prompt() string { return readHashlinePrompt }
var readFileHashlineSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "File path (absolute, or relative to working_dir / workspace root). Must be a file, not a directory — use shell('ls') first to explore directories. Paths without a file extension are likely directories."
    },
    "offset": {
      "type": "integer",
      "description": "Without pattern: starting line number (0-based, 0 = first line, optional). With pattern: match index (0-based) to page through matches."
    },
    "limit": {
      "type": "integer",
      "description": "Number of lines to read (optional, default: all)"
    },
    "pattern": {
      "type": "string",
      "description": "Optional substring to locate in the file. When present, the output centers on the first match ±context_lines, eliminating a separate grep call. Use offset/limit to page through additional matches."
    },
    "context_lines": {
      "type": "integer",
      "description": "Lines of context above and below each match (default: 5, max: 50). 0 is treated as default. Only meaningful with pattern. For match-line-only, use limit=1."
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    }
  },
  "required": ["file_path"]
}`)

func (t *ReadFileHashline) Schema() json.RawMessage { return readFileHashlineSchema }

func (t *ReadFileHashline) ConcurrentSafe() bool { return true }

func (t *ReadFileHashline) Execute(ctx context.Context, p ReadFileHashlineParams) (*ToolResult, error) {
	// ── Step 1: 路径解析 ──
	path, err := pathutil.ResolvePathWithDir(p.FilePath, p.WorkingDir)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("invalid path: %v", err), err), nil
	}

	// ── Step 2: 设备文件拦截 ──
	if IsBlockedDevicePath(path) {
		return toolError(ErrorClassFatal, ErrKindSecurityViolation,
			fmt.Sprintf("cannot read device file: %s", path), nil), nil
	}

	// ── Step 3: 文件检查 ──
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileNotFoundError(path), nil
		}
		return toolError(ErrorClassFatal, ErrKindPermissionDenied,
			fmt.Sprintf("cannot access file: %s", path), err), nil
	}

	if info.IsDir() {
		entries, readErr := os.ReadDir(path)
		if readErr == nil {
			sortDirEntries(entries)
			var listing strings.Builder
			fmt.Fprintf(&listing, "Path is a directory, not a file: %s\n\n", path)
			if suggestion := suggestFileInDir(path, entries); suggestion != "" {
				fmt.Fprintf(&listing, "Did you mean %s?\n\n", suggestion)
			}
			const maxDisplay = 50
			total := len(entries)
			if total > maxDisplay {
				fmt.Fprintf(&listing, "Showing first %d of %d entries (use ls for more):\n", maxDisplay, total)
			} else {
				listing.WriteString("Contents:\n")
			}
			for i, entry := range entries {
				if i >= maxDisplay {
					fmt.Fprintf(&listing, "  ... and %d more entries\n", total-maxDisplay)
					break
				}
				name := entry.Name()
				if entry.IsDir() {
					name += "/"
				}
				fmt.Fprintf(&listing, "  %s\n", name)
			}
			return toolError(ErrorClassRecoverable, ErrKindNotDir, listing.String(), nil), nil
		}
		return toolError(ErrorClassRecoverable, ErrKindNotDir,
			fmt.Sprintf("path is a directory, not a file: %s", path), nil), nil
	}

	// ── Step 4: 大文件门限 ──
	// 在读取任何内容之前先检查文件大小。hashline read 需要完整文件内容
	// 计算 TAG + 存入 snapshot store，超大文件（>10MB）可能导致 OOM。
	const maxReadBytes = 10 * 1024 * 1024 // 10MB
	if info.Size() > maxReadBytes {
		s := fmt.Sprintf("%.1fMB", float64(info.Size())/(1024*1024))
		return toolError(ErrorClassRecoverable, ErrKindLargeFile,
			fmt.Sprintf("file too large for hashline read (%s > 10MB): %s. Use shell tools to both read (head/tail/grep) and edit (sed/awk) large files.", s, path), nil), nil
	}

	// ── Step 5: 二进制检测 ──
	if HasBinaryExtension(path) {
		return toolError(ErrorClassRecoverable, ErrKindBinaryFile,
			fmt.Sprintf("file appears to be a binary %s file: %s",
				strings.ToLower(strings.TrimPrefix(fileExtension(path), ".")), path), nil), nil
	}

	isBinary, err := IsBinaryByContent(path)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("cannot check file type: %v", err), err), nil
	}
	if isBinary {
		return toolError(ErrorClassRecoverable, ErrKindBinaryFile,
			fmt.Sprintf("file appears to be binary: %s", path), nil), nil
	}

	// ── Step 6: 读取文件内容 ──

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	fullContent, _, totalLines, err := readFullFile(ctx, path)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("failed to read file: %v", err), err), nil
	}

	// ── Step 6: 生成 TAG（无论截断与否，TAG 对应完整文件内容）──
	var tag string
	if store := hashline.StoreFromContext(ctx); store != nil {
		tag, err = store.Record(path, fullContent)
		if err != nil {
			return toolError(ErrorClassRecoverable, ErrKindCommandFailed,
				fmt.Sprintf("failed to generate TAG: %v", err), err), nil
		}
	} else {
		// 无 Store 时用临时 TAG（仍可读但不可编辑）
		tag = "0000"
	}

	// ── Step 7: pattern 匹配(可选)──
	// pattern 选择显示窗口:匹配行 ±ContextLines。TAG 始终对应完整文件,不受 pattern 影响。
	var matchFooter string
	if p.Pattern != "" && totalLines > 0 {
		lines := strings.Split(fullContent, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}

		var matches []int
		for i, line := range lines {
			if strings.Contains(line, p.Pattern) {
				matches = append(matches, i) // 0-based
			}
		}
		// ContextLines 为 0 时使用默认值 5(Go int 零值无法区分"未传"和"传 0")。
		// 想要仅显示匹配行本身:传入 limit=1。
		ctxLines := p.ContextLines
		if ctxLines <= 0 {
			ctxLines = 5
		}
		if ctxLines > 50 {
			ctxLines = 50
		}

		if len(matches) == 0 {
			matchFooter = fmt.Sprintf("\n<system-reminder>Pattern %q not found in file. Check pattern spelling. Showing full file.</system-reminder>", p.Pattern)
		} else {
			// offset 复用为匹配索引(0-based),limit 约束显示行数
			requestedIdx := p.Offset // 保存原始值,用于后续钳位提示
			matchIdx := requestedIdx
			if matchIdx < 0 {
				matchIdx = 0
			}
			if matchIdx >= len(matches) {
				matchIdx = len(matches) - 1
			}

			matchLine := matches[matchIdx]
			start := matchLine - ctxLines
			if start < 0 {
				start = 0
			}
			end := matchLine + ctxLines + 1
			if end > totalLines {
				end = totalLines
			}
			if p.Limit > 0 && start+p.Limit < end {
				end = start + p.Limit
			}

			p.Offset = start
			p.Limit = end - start

			matchFooter = fmt.Sprintf("\n<system-reminder>Match %d of %d for %q at line %d (%d lines shown).",
				matchIdx+1, len(matches), p.Pattern, matchLine+1, p.Limit)
			if requestedIdx > 0 && requestedIdx >= len(matches) {
				matchFooter += fmt.Sprintf(" Requested match %d, clamped to %d.", requestedIdx+1, matchIdx+1)
			}
			if len(matches) > 1 {
				matchFooter += " Use offset=N to page through matches."
			}
			matchFooter += "</system-reminder>"
		}
	}

	// ── Step 8: 格式化输出 ──
	content := hashline.FormatContent(path, tag, fullContent, p.Offset, p.Limit)
	if matchFooter != "" {
		content += matchFooter
	}

	if totalLines == 0 {
		content = fmt.Sprintf("[%s#%s]\n<system-reminder>Warning: the file exists but the contents are empty.</system-reminder>", path, tag)
	}

	lineCount := totalLines
	if p.Limit > 0 && p.Limit < totalLines {
		lineCount = p.Limit
	}
	if p.Offset > 0 && p.Offset < totalLines {
		remaining := totalLines - p.Offset
		if p.Limit == 0 || p.Limit > remaining {
			lineCount = remaining
		}
	}

	return &ToolResult{
		Content: content,
		Meta: ToolMeta{
			FilePath:  path,
			LineCount: lineCount,
			ByteCount: len(content),
		},
	}, nil
}

// readFullFile 读取完整文件内容，不做截断。
// 返回：文件文本、实际行数、总行数、错误。
func readFullFile(ctx context.Context, path string) (content string, lineCount int, totalLines int, err error) {
	raw, err := readFileWithContext(ctx, path)
	if err != nil {
		return "", 0, 0, err
	}

	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\r")
	lines := splitLines(text)
	totalLines = len(lines)

	return text, totalLines, totalLines, nil
}
