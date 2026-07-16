package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Menfre01/waveloom/pkg/filehistory"
	"github.com/Menfre01/waveloom/pkg/hashline"
)

// ---------------------------------------------------------------------------
// EditFileHashline — 基于 hashline 的精确编辑
// ---------------------------------------------------------------------------

type EditFileHashlineParams struct {
	Patch      string `json:"patch"`       // Hashline 格式的 patch 文本
	WorkingDir string `json:"working_dir"` // 可选工作目录
}

type EditFileHashline struct{}

func (t *EditFileHashline) Name() string { return "edit" }

func (t *EditFileHashline) Description() string {
	return "Edit files using hash-anchored patches. " +
		"Use read to get TAGs and line numbers, " +
		"then specify operations (SWAP/INS/DEL/REM/MV) by TAG and line number. " +
		"No need to reproduce old code — just the TAG, line numbers, and new content."
}

// Prompt 返回 hashline 使用指南，由 Registry.FormatToolPrompts() 注入 C1 system prompt。
func (t *EditFileHashline) Prompt() string {
	return `## Edit File (Hashline) — Recommended

Use edit to modify existing files. read gives you
TAGs and line numbers; edit applies changes by referencing them. Never
reproduce old code — only the TAG, line numbers, and new content.

### Operations

SWAP N.=M:     Replace lines N through M (inclusive) with body lines below
DEL N.=M       Delete lines N through M. DEL N for single line.
INS.PRE N:     Insert body lines BEFORE line N
INS.POST N:    Insert body lines AFTER line N
INS.HEAD:      Insert body lines at the very start of the file
INS.TAIL:      Insert body lines at the very end of the file
REM            Delete the entire file (no body, no line numbers)
MV DEST        Move/rename the file to DEST

### Body lines

Every body line starts with + followed by the actual content (including leading whitespace).
+ alone adds a blank line. The body is ONLY the new content — old lines are deleted
implicitly by the range in SWAP/DEL.

Blank lines between body lines are silently skipped by the parser. To insert
an intentional blank line, use a standalone + line (no content after the +).

### Line numbers

Line numbers come directly from read output (N:CONTENT format).
Ranges are INCLUSIVE: SWAP 2.=3: covers lines 2 and 3.
A range of N.=N: replaces a single line with any number of body lines.
Note: files without a trailing newline may acquire one after editing — normal.

### Rules

- Use the TAG from your most recent read output.
  After every edit, the response contains a new TAG — use it for the next edit.
- Touch only lines that change. For pure additions, use INS.PRE / INS.POST — never
  widen a SWAP to include unchanged lines.
- Operations are applied in declaration order. After each operation, the system
  automatically computes the line offset and adjusts subsequent operations' line
  numbers accordingly. All line numbers refer to the original file — you do NOT
  need to manually calculate offsets. This allows editing multiple places in
  a single edit call.
- Do NOT create overlapping operations on the same lines (e.g., SWAP 5.=6: and
  DEL 5 in the same patch; or INS.PRE 4: and INS.POST 4: on the same reference
  line). Note: INS.PRE N followed by SWAP N (or DEL N) is safe — the system
  automatically offsets the SWAP/DEL line number after the insertion.
  Overlapping ops will be rejected with an error — split them into
  separate edit calls.
- On tag_mismatch error: the file was modified since your last read — re-read to
  get a fresh TAG and line numbers before editing again.
- A patch may contain multiple [PATH#TAG] sections for different files, or
  multiple sections for the same file — each section independently validates
  its TAG and applies its operations. If an earlier section modifies a line's
  content that a later section targets, the later section will fail with a
  specific reason (e.g. "line N modified in current version"). Earlier sections
  are already applied to disk; use the post-edit context returned in the result
  to construct a new edit for the remaining changes without re-reading.
  Cross-section conflicts only affect the conflicting file — edits to other
  files in the same patch still execute normally. REM/MV cannot be combined
  with line-range operations on the same file in one patch; split them.
*** Begin Patch
[src/pkg/foo.go#A1B2]       ← first file
OP1
+BODY

[src/pkg/bar.go#C3D4]       ← second file
OP2
+BODY
*** End Patch

Example — replace line 2, insert after line 4:

*** Begin Patch
[src/main.go#A1B2]
SWAP 2.=2:
+    fmt.Println("hello, world")
INS.POST 4:
+    // cleanup on exit
+    defer os.Remove(tmpFile)
*** End Patch

### When NOT to use

- Creating a new file → use write (then read to get a TAG)
- Reading a file → use read
- Very simple single-word replacements on short files → use edit with a single SWAP line.`
}


var editFileHashlineSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "patch": {
      "type": "string",
      "description": "Hashline format patch text. Must start with *** Begin Patch and end with *** End Patch."
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    }
  },
  "required": ["patch"]
}`)

func (t *EditFileHashline) Schema() json.RawMessage { return editFileHashlineSchema }

func (t *EditFileHashline) ConcurrentSafe() bool { return false }

func (t *EditFileHashline) Execute(ctx context.Context, p EditFileHashlineParams) (*ToolResult, error) {
	// ── Step 0: 获取 Store ──
	store := hashline.StoreFromContext(ctx)
	if store == nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			"hashline not available, read first to initialize the snapshot store", nil), nil
	}

	// ── Step 1: 解析 patch ──
	patch, err := hashline.ParsePatch(p.Patch)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("patch parse error: %v", err), err), nil
	}


	// ── Step 3: FileHistory 追踪 ──
	if fh := filehistory.FromContext(ctx); fh != nil {
		if msgID := filehistory.MessageIDFromContext(ctx); msgID != "" {
			if sd := filehistory.SessionDirFromContext(ctx); sd != "" {
				for _, sec := range patch.Sections {
					fh.TrackEdit(sec.Path, msgID, sd)
				}
			}
		}
	}

	// ── Step 4: 应用 patch ──
	fs := &hashline.OSFS{WorkingDir: p.WorkingDir}
	results := hashline.ApplyPatch(patch, fs, store)

	// ── Step 5: 构造返回结果（含编辑后上下文，LLM 可链式编辑无需 re-read）──
	content := formatSectionResults(results)
	content += formatPostEditContext(fs, results)

	// 收集第一个错误（如有）
	var firstError *ToolError
	for _, r := range results {
		if r.Error != nil {
			class := ErrorClassRecoverable
			if r.Error.Fatal {
				class = ErrorClassFatal
			}
			firstError = &ToolError{
				Class:   class,
				Kind:    r.Error.Kind,
				Message: r.Error.Message,
			}
			break
		}
	}

	if firstError != nil {
		return &ToolResult{
			Content: content,
			Error:   firstError,
		}, nil
	}

	// ── 构造 DiffHunks ──
	var allHunks []DiffHunk
	for _, r := range results {
		for _, h := range r.DiffHunks {
			allHunks = append(allHunks, convertHunk(h))
		}
	}

	// ── Step 6: 构造 Meta ──
	meta := ToolMeta{
		DiffHunks: allHunks,
	}
	if len(results) > 0 {
		meta.FilePath = results[0].Path
		meta.LineCount = len(strings.Split(content, "\n"))
		meta.ByteCount = len(content)
	}

	return &ToolResult{
		Content: content,
		Meta:    meta,
	}, nil
}

func formatSectionResults(results []hashline.SectionResult) string {
	var b strings.Builder
	for _, r := range results {
		if r.Error != nil {
			fmt.Fprintf(&b, "[%s] ✗ %s\n", r.Path, r.Error.Message)
			continue
		}
		if r.Warning != "" {
			fmt.Fprintf(&b, "[%s#%s] ⚠ %s: %s (%+d lines)\n", r.Path, r.NewTAG, r.Op, r.Warning, r.LinesDelta)
			continue
		}
		switch r.Op {
		case "update":
			fmt.Fprintf(&b, "[%s#%s] ✓ %s (%+d lines)\n", r.Path, r.NewTAG, r.Op, r.LinesDelta)
		case "delete":
			fmt.Fprintf(&b, "[%s#%s] ✓ deleted\n", r.Path, r.OldTAG)
		case "rename":
			fmt.Fprintf(&b, "[%s#%s] → %s ✓ renamed\n", r.Path, r.NewTAG, r.Path)
		default:
			fmt.Fprintf(&b, "[%s#%s] ✓ %s\n", r.Path, r.NewTAG, r.Op)
		}
	}
	return b.String()
}

func convertHunk(h hashline.EditHunk) DiffHunk {
	out := DiffHunk{
		OldStart:       h.OldStart,
		OldCount:       h.OldCount,
		NewStart:       h.NewStart,
		NewCount:       h.NewCount,
		Heading:        h.Heading,
		NoNewlineAtEOF: h.NoNewlineAtEOF,
	}
	for _, l := range h.Lines {
		out.Lines = append(out.Lines, DiffLine{
			Kind:    DiffLineKind(l.Kind),
			Content: l.Content,
			OldNum:  l.OldNum,
			NewNum:  l.NewNum,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// 编辑后上下文 — LLM 无需 re-read 即可链式编辑
// ---------------------------------------------------------------------------

// editContextLines 编辑后上下文中变更区域前后各显示的行数。
const editContextLines = 3

// maxEditDisplay 编辑区域本身最多显示的行数（超出则截断）。
const maxEditDisplay = 20

// formatPostEditContext 为每个成功的 update 操作追加编辑后文件上下文。
// 显示变更区域 ±editContextLines 行的实际内容及行号，
// 编辑区域过大时自动截断，让 LLM 无需重新 read 即可构造下一个 edit。
// 注意：TAG 头已由 formatSectionResults 输出，此处不重复。
func formatPostEditContext(fs hashline.FileSystem, results []hashline.SectionResult) string {
	var b strings.Builder

	for _, r := range results {
		if r.Error != nil || r.Op != "update" || len(r.DiffHunks) == 0 {
			continue
		}

		fileContent, err := fs.ReadFile(r.Path)
		if err != nil {
			continue
		}

		lines := splitFileLines(fileContent)
		if len(lines) == 0 {
			continue
		}
		totalLines := len(lines)

		// 确定所有 hunk 覆盖的变更区域
		firstChanged, lastChanged := totalLines+1, 0
		for _, h := range r.DiffHunks {
			if h.NewCount > 0 {
				start := h.NewStart
				end := h.NewStart + h.NewCount - 1
				if start < firstChanged {
					firstChanged = start
				}
				if end > lastChanged {
					lastChanged = end
				}
			} else {
				if h.NewStart < firstChanged {
					firstChanged = h.NewStart
				}
				if h.NewStart > lastChanged {
					lastChanged = h.NewStart
				}
			}
		}
		if lastChanged == 0 {
			continue
		}

		editLen := lastChanged - firstChanged + 1
		truncated := editLen > maxEditDisplay

		// 计算显示范围（扩展上下文）
		showStart := firstChanged - editContextLines
		showEnd := lastChanged + editContextLines
		if showStart < 1 {
			showStart = 1
		}
		if showEnd > totalLines {
			showEnd = totalLines
		}

		b.WriteByte('\n')

		// 文件头部省略
		if showStart > 1 {
			fmt.Fprintf(&b, "... [%d lines above omitted]\n", showStart-1)
		}

		if truncated {
			// 大编辑区域：显示前 5 行 + 截断提示 + 后 5 行
			prefixEnd := firstChanged + 4
			suffixStart := lastChanged - 4
			if prefixEnd >= suffixStart {
				// 编辑区域不够大，退化为全量显示
				truncated = false
			}
		}

		if !truncated {
			for i := showStart - 1; i < showEnd; i++ {
				fmt.Fprintf(&b, "%d:%s\n", i+1, lines[i])
			}
		} else {
			prefixEnd := firstChanged + 4
			suffixStart := lastChanged - 4

			// 上文 + 编辑区前 5 行
			for i := showStart - 1; i < prefixEnd; i++ {
				fmt.Fprintf(&b, "%d:%s\n", i+1, lines[i])
			}

			omitted := suffixStart - prefixEnd
			fmt.Fprintf(&b, "... [%d lines in edit region omitted]\n", omitted)

			// 编辑区后 5 行 + 下文
			for i := suffixStart; i < showEnd; i++ {
				fmt.Fprintf(&b, "%d:%s\n", i+1, lines[i])
			}
		}

		// 文件尾部省略
		if showEnd < totalLines {
			fmt.Fprintf(&b, "... [%d lines below omitted]\n", totalLines-showEnd)
		}
	}

	return b.String()
}

// splitFileLines 将文件内容按行分割，去除尾部空行。
func splitFileLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSuffix(content, "\r")
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
