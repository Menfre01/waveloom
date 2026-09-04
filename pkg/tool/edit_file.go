package tool

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

//go:embed edit_file_prompt.md
var editFilePrompt string

// ---------------------------------------------------------------------------
// EditFile — diff hunk edit tool
// ---------------------------------------------------------------------------

type EditFileParams struct {
	FilePath string `json:"file_path"`
	Hunk     string `json:"hunk"`
}

type EditFile struct{}

func (t *EditFile) Name() string        { return "edit" }
func (t *EditFile) Description() string {
	return "Edit files using unified diff hunks. Supports multi-file, multi-hunk patches."
}
func (t *EditFile) Prompt() string       { return editFilePrompt }
func (t *EditFile) ConcurrentSafe() bool { return false }

var editFileSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "Default target file. For single-file edits, this is the file being edited. For multi-file patches, relative paths in *** Update File: headers resolve against this file's directory."
    },
    "hunk": {
      "type": "string",
      "description": "Unified diff hunk(s). Format: @@ header\\n context\\n-old\\n+new\\n context. For multi-file, wrap each file section with *** Update File: <path>."
    }
  },
  "required": ["file_path", "hunk"]
}`)

func (t *EditFile) Schema() json.RawMessage { return editFileSchema }

func (t *EditFile) Execute(ctx context.Context, p EditFileParams) (*ToolResult, error) {
	if p.Hunk == "" {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			"hunk is required", nil), nil
	}

	// 解析为绝对路径,与 read/write 对齐,确保 LSP 诊断能定位文件
	resolvedPath, resolveErr := pathutil.ResolvePathWithDir(p.FilePath, "")
	if resolveErr != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("invalid path: %v", resolveErr), resolveErr), nil
	}

	results, err := ApplyHunk(ctx, resolvedPath, p.Hunk, ReadStateFromContext(ctx))
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("hunk error: %v", err), err), nil
	}

	if len(results) == 0 {
		return &ToolResult{Content: "✓ no changes needed", Meta: ToolMeta{FilePath: resolvedPath}}, nil
	}

	var succeeded, failed int
	var buf strings.Builder
	var diffHunks []DiffHunk

	// 行号校正基准:跨 hunk 跟踪同一文件的净行数变化。
	// LLM 提供的 @@ header 行号不可信(seekHunk 按内容匹配,不校验 header);
	// 非标准 header(如 "@@ func name" / 裸 "@@")行号从 1 开始,与实际
	// 匹配位置无关——TUI diff view 的行号列会显示错误。
	var lastFile string
	var offset int
	var hintedNotReadFile string // 已提示过"未 read"的文件(多 hunk 只提示一次)

	for _, r := range results {
		if r.Error != "" {
			failed++
			fmt.Fprintf(&buf, "✗ %s: @@ %s — %s\n", r.File, r.Header, r.Error)
			// REGRESSION: not-been-read 的高发根因是 hunk header 路径被错误
			// 解析(双重嵌套,已容错)或根本没 read。给出可操作的恢复指引。
			if strings.Contains(r.Error, "file has not been read yet") && r.File != hintedNotReadFile {
				hintedNotReadFile = r.File
				buf.WriteString("  hint: 目标文件尚未 read——edit 前必须 read(部分读取亦可);若已 read 仍失败,检查 hunk 是否带了 `*** Update File:` 头(单文件编辑请省略)。若该文件是本进程重启前创建/修改的(read 记录不跨进程),read 一次即可消除本提示\n")
			}
			if len(r.OldLines) > 0 {
				buf.WriteString("  pattern:\n")
				for _, l := range r.OldLines {
					fmt.Fprintf(&buf, "   - %s\n", l)
				}
				for _, l := range r.NewLines {
					fmt.Fprintf(&buf, "   + %s\n", l)
				}
			}
			if r.FileSnippet != "" {
				buf.WriteString("  file content:\n")
				buf.WriteString(r.FileSnippet)
			}
			if r.ClosestMatch != "" {
				buf.WriteString(r.ClosestMatch)
			}
		} else {
			succeeded++
			fmt.Fprintf(&buf, "✓ %s: @@ %s — applied at line %d\n", r.File, r.Header, r.Line)
			if dh := parseDiffHunk(r.Header, r.RawBody, r.File); dh != nil {
				if r.File != lastFile {
					lastFile = r.File
					offset = 0
				}
				// r.Line 是 seekHunk 在已应用前面 hunk 的内容中的匹配位置
				// (新文件行号);旧文件行号 = r.Line - offset。
				renumberDiffHunk(dh, r.Line, offset)
				diffHunks = append(diffHunks, *dh)
			}
			offset += len(r.NewLines) - len(r.OldLines)
		}
	}

	total := succeeded + failed
	fmt.Fprintf(&buf, "\n%d/%d hunks succeeded", succeeded, total)
	if failed > 0 {
		fmt.Fprintf(&buf, " — re-read failed files and retry. Do NOT fall back to bash/python/sed — edit is the ONLY file modification tool. The only fix for a failed hunk is: re-read the target area → construct a better hunk → retry edit.")
	}
	buf.WriteByte('\n')

	if succeeded == 0 && failed > 0 {
		return &ToolResult{
			Content: buf.String(),
			Error: &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    ErrKindInvalidArgs,
				// Message 渲染在 Content 之前(模型第一眼位置):直接给恢复指引,
				// 避免模型在长诊断输出里找不到"先 read"的提示而盲目重试
				// (实测同文件三连败:指引埋在详情尾部被忽略)。
				Message: fmt.Sprintf("all %d hunks failed — 先 read 目标文件再重试,勿盲目重试(连续失败请停止并 read)", failed),
			},
		}, nil
	}

	result := &ToolResult{Content: buf.String()}
	// Set FilePath and stats from first successful file
	for _, r := range results {
		if r.Error == "" && r.File != "" {
			result.Meta.FilePath = r.File
			if data, err := os.ReadFile(r.File); err == nil {
				result.Meta.ByteCount = len(data)
				result.Meta.LineCount = countLinesInContent(string(data))
			}
			break
		}
	}
	if len(diffHunks) > 0 {
		result.Meta.DiffHunks = diffHunks
	}
	return result, nil
}

// parseDiffHunk parses a unified diff hunk header and body into a DiffHunk.
// Returns nil if parsing fails.
func parseDiffHunk(header, body, filePath string) *DiffHunk {
	if body == "" {
		return nil
	}
	oldStart, oldCount, newStart, newCount, heading := parseHunkHeader(header)

	// Non-standard header (e.g. @@ func name or bare @@): start line
	// numbering at 1 and infer counts from the body after parsing.
	inferFromBody := oldStart == 0 && newStart == 0
	if inferFromBody {
		oldStart, newStart = 1, 1
	}

	// Normalize \r\n → \n in body before parsing
	body = strings.ReplaceAll(body, "\r\n", "\n")

	var lines []DiffLine
	oldNum := oldStart
	newNum := newStart

	for _, line := range strings.Split(body, "\n") {
		if line == "" || line == "\r" {
			continue
		}
		switch {
		case strings.HasPrefix(line, " "):
			lines = append(lines, DiffLine{
				Kind:    DiffCtx,
				Content: line[1:],
				OldNum:  oldNum,
				NewNum:  newNum,
			})
			oldNum++
			newNum++
		case strings.HasPrefix(line, "-"):
			lines = append(lines, DiffLine{
				Kind:    DiffDel,
				Content: line[1:],
				OldNum:  oldNum,
			})
			oldNum++
		case strings.HasPrefix(line, "+"):
			lines = append(lines, DiffLine{
				Kind:    DiffAdd,
				Content: line[1:],
				NewNum:  newNum,
			})
			newNum++
		default:
			// No prefix — treat as context
			lines = append(lines, DiffLine{
				Kind:    DiffCtx,
				Content: line,
				OldNum:  oldNum,
				NewNum:  newNum,
			})
			oldNum++
			newNum++
		}
	}

	if len(lines) == 0 {
		return nil
	}

	if inferFromBody {
		oldCount = oldNum - 1
		newCount = newNum - 1
		if heading == "" {
			heading = strings.TrimSpace(strings.ReplaceAll(header, "@@", ""))
		}
	} else if oldCount == 0 && newCount == 0 {
		// Standard header with omitted counts (e.g. @@ -1 +1 @@)
		oldCount = oldNum - oldStart
		newCount = newNum - newStart
	}

	return &DiffHunk{
		FilePath: filePath,
		OldStart: oldStart,
		OldCount: oldCount,
		NewStart: newStart,
		NewCount: newCount,
		Heading:  heading,
		Lines:    lines,
	}
}

// renumberDiffHunk 以 hunk 的实际匹配位置为基准重算行号与 header 范围。
//
// REGRESSION: parseDiffHunk 的行号来自 LLM 提供的 @@ header——标准 header
// 用的是 LLM 猜测的行号(applyFileHunks 的 seekHunk 按内容匹配,不校验
// header 行号),非标准 header(如 "@@ func name" / 裸 "@@")一律从 1 开始,
// 两者都与文件中的实际匹配位置无关,导致 TUI diff view 的行号列错误。
//
// newFileStart 是 seekHunk 在"已应用前面 hunk 的内容"中的匹配位置
// (HunkResult.Line,新文件 1-based 行号);offset 是同一文件此前 hunk 的
// 净行数变化,故旧文件行号 = newFileStart - offset。
func renumberDiffHunk(dh *DiffHunk, newFileStart, offset int) {
	oldNum := newFileStart - offset
	newNum := newFileStart
	for i := range dh.Lines {
		switch dh.Lines[i].Kind {
		case DiffCtx:
			dh.Lines[i].OldNum = oldNum
			dh.Lines[i].NewNum = newNum
			oldNum++
			newNum++
		case DiffDel:
			dh.Lines[i].OldNum = oldNum
			oldNum++
		case DiffAdd:
			dh.Lines[i].NewNum = newNum
			newNum++
		}
	}
	dh.OldStart = newFileStart - offset
	dh.OldCount = oldNum - dh.OldStart
	dh.NewStart = newFileStart
	dh.NewCount = newNum - dh.NewStart
}

// parseHunkHeader parses "@@ -oldStart[,oldCount] +newStart[,newCount] @@ [heading]".
func parseHunkHeader(header string) (oldStart, oldCount, newStart, newCount int, heading string) {
	// Find the @@ markers
	parts := strings.SplitN(header, "@@", 3)
	if len(parts) < 3 {
		return 0, 0, 0, 0, ""
	}
	rangePart := strings.TrimSpace(parts[1])
	// rangePart looks like "-1,7 +1,7" or "-1 +1"
	spaceIdx := strings.Index(rangePart, " ")
	if spaceIdx < 0 {
		return 0, 0, 0, 0, ""
	}
	oldPart := strings.TrimPrefix(rangePart[:spaceIdx], "-")
	newPart := strings.TrimPrefix(rangePart[spaceIdx+1:], "+")

	oldStart, oldCount = parseRange(oldPart)
	newStart, newCount = parseRange(newPart)

	if len(parts) > 2 {
		heading = strings.TrimSpace(parts[2])
	}
	return
}

// parseRange parses "start" or "start,count".
func parseRange(s string) (start, count int) {
	commaIdx := strings.Index(s, ",")
	if commaIdx < 0 {
		start, _ = strconv.Atoi(s)
		count = 1
		return
	}
	start, _ = strconv.Atoi(s[:commaIdx])
	count, _ = strconv.Atoi(s[commaIdx+1:])
	if count == 0 {
		count = 1
	}
	return
}

// ParseEditPreview 解析 edit 工具 hunk 参数为结构化 diff(不应用文件),
// 用于权限审批框的改动预览。解析逻辑与 ApplyHunk 共用 parsePatchFiles /
// parseDiffHunk,保证预览展示的文件路径与行号和应用时一致。
// 返回 nil 表示无可预览内容(空 hunk 或全部解析失败)。
func ParseEditPreview(defaultPath, hunkText string) []DiffHunk {
	files := parsePatchFiles(hunkText, defaultPath)
	var out []DiffHunk
	for _, f := range files {
		for _, h := range f.hunks {
			if dh := parseDiffHunk(h.header, h.rawBody, f.path); dh != nil {
				out = append(out, *dh)
			}
		}
	}
	return out
}
