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

func (t *EditFileHashline) Name() string { return "edit_file_hashline" }

func (t *EditFileHashline) Description() string {
	return "Edit files using hash-anchored patches. " +
		"Use read_file_hashline to get TAGs and line numbers, " +
		"then specify operations (SWAP/INS/DEL/REM/MV) by TAG and line number. " +
		"No need to reproduce old code — just the TAG, line numbers, and new content."
}

// Prompt 返回 hashline 使用指南，由 Registry.FormatToolPrompts() 注入 C1 system prompt。
func (t *EditFileHashline) Prompt() string {
	return `## Edit File (Hashline) — Recommended

Use edit_file_hashline to modify existing files. read_file_hashline gives you
TAGs and line numbers; edit_file_hashline applies changes by referencing them. Never
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

### Line numbers

Line numbers come directly from read_file_hashline output (N:CONTENT format).
Ranges are INCLUSIVE: SWAP 2.=3: covers lines 2 and 3.
A range of N.=N: replaces a single line with any number of body lines.

### Rules

- Use the TAG from your most recent read_file_hashline output.
  After every edit, the response contains a new TAG — use it for the next edit.
- Touch only lines that change. For pure additions, use INS.PRE / INS.POST — never
  widen a SWAP to include unchanged lines.
- Line numbers refer to the original file as shown in the read. The system handles
  ordering when a section has multiple operations.
- Never start or end a range mid-expression or mid-block.
- If TAG verification fails, re-read the file before editing again.

### Format

*** Begin Patch
[PATH#TAG]
OP1
+BODY

[PATH#TAG]
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

- Creating a new file → use write_file (then read_file_hashline to get a TAG)
- Reading a file → use read_file_hashline
- Very simple single-word replacements on short files → ordinary edit_file is fine`
}

func (t *EditFileHashline) Schema() json.RawMessage { return editFileHashlineSchema }

func (t *EditFileHashline) ConcurrentSafe() bool { return false }

func (t *EditFileHashline) Execute(ctx context.Context, p EditFileHashlineParams) (*ToolResult, error) {
	// ── Step 0: 获取 Store ──
	store := hashline.StoreFromContext(ctx)
	if store == nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			"hashline not available, read_file_hashline first to initialize the snapshot store", nil), nil
	}

	// ── Step 1: 解析 patch ──
	patch, err := hashline.ParsePatch(p.Patch)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("patch parse error: %v", err), err), nil
	}

	// ── Step 2: 检测同文件多 Section ──
	seenPaths := make(map[string]bool)
	for _, sec := range patch.Sections {
		if seenPaths[sec.Path] {
			return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
				fmt.Sprintf("duplicate section for %q: merge all operations into a single [PATH#TAG] section", sec.Path), nil), nil
		}
		seenPaths[sec.Path] = true
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

	// ── Step 5: 构造返回结果 ──
	content := formatSectionResults(results)

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
			fmt.Fprintf(&b, "[%s#%s] ⚠ %s: %s (+%d lines)\n", r.Path, r.NewTAG, r.Op, r.Warning, r.LinesDelta)
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
