package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Menfre01/waveloom/pkg/hashline"
	"github.com/Menfre01/waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// ReadFileHashline — 读取文件内容，返回 hashline 格式（TAG + N:CONTENT）
// ---------------------------------------------------------------------------

type ReadFileHashlineParams struct {
	FilePath   string `json:"file_path"`   // 与 read_file 一致
	Offset     int    `json:"offset"`      // 0-based: 0 = 文件第一行
	Limit      int    `json:"limit"`       // 读取行数（0 = 不限）
	WorkingDir string `json:"working_dir"` // 工作目录（可选）
}

type ReadFileHashline struct{}

func (t *ReadFileHashline) Name() string { return "read" }

func (t *ReadFileHashline) Description() string {
	return "Read a file with TAG and line numbers for hash-anchored editing. " +
		"Use with edit — the TAG certifies the file snapshot, " +
		"line numbers are used directly in SWAP/INS/DEL operations."
}

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

	// ── Step 4: 二进制检测 ──
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

	// ── Step 5: 读取文件内容 ──
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	fullContent, _, totalLines, err := readFullFile(ctx, path)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindCommandFailed,
			fmt.Sprintf("error reading file: %v", err), err), nil
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

	// ── Step 7: 格式化输出 ──
	content := hashline.FormatContent(path, tag, fullContent, p.Offset, p.Limit)

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
