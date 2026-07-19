package tool

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Menfre01/waveloom/pkg/filehistory"
	"github.com/Menfre01/waveloom/pkg/hashline"
)

//go:embed edit_hashline_prompt.md
var editHashlinePrompt string

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
func (t *EditFileHashline) Prompt() string { return editHashlinePrompt }


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
			allHunks = append(allHunks, convertHunk(h, r.Path))
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
	var tagLines []string
	for _, r := range results {
		if r.Error != nil {
			fmt.Fprintf(&b, "✗ %s: %s\n", r.Path, r.Error.Message)
			continue
		}
		if r.Warning != "" {
			fmt.Fprintf(&b, "⚠ %s — TAG: %s — %s: %s (%+d lines)\n", r.Path, r.NewTAG, r.Op, r.Warning, r.LinesDelta)
			tagLines = append(tagLines, fmt.Sprintf("%s#%s", r.Path, r.NewTAG))
			continue
		}
		switch r.Op {
		case "update":
			fmt.Fprintf(&b, "✓ %s — TAG: %s — (%+d lines)\n", r.Path, r.NewTAG, r.LinesDelta)
			tagLines = append(tagLines, fmt.Sprintf("%s#%s", r.Path, r.NewTAG))
			if len(r.DiffHunks) > 0 {
				b.WriteString(formatLocalDiffExcerpt(r.DiffHunks, 12))
			}
		case "delete":
			fmt.Fprintf(&b, "✓ %s deleted (was TAG: %s)\n", r.Path, r.OldTAG)
		case "rename":
			fmt.Fprintf(&b, "✓ %s renamed (TAG: %s)\n", r.Path, r.NewTAG)
			tagLines = append(tagLines, fmt.Sprintf("%s#%s", r.Path, r.NewTAG))
		default:
			fmt.Fprintf(&b, "✓ %s — TAG: %s — %s\n", r.Path, r.NewTAG, r.Op)
			tagLines = append(tagLines, fmt.Sprintf("%s#%s", r.Path, r.NewTAG))
		}
	}
	if len(tagLines) > 0 {
		fmt.Fprintf(&b, "\n— Next TAGs: %s\n", strings.Join(tagLines, " | "))
	}
	return b.String()
}

// formatLocalDiffExcerpt 从 DiffHunks 生成精简的变更摘要（含行号），
// 供 LLM 在不 re-read 整个文件的情况下快速确认变更内容。
// 行号来自 hunks 的 OldNum/NewNum，与下方 post-edit context 中的行号一致，
// LLM 可交叉对照确认编辑边界正确。
func formatLocalDiffExcerpt(hunks []hashline.EditHunk, maxLines int) string {
	var b strings.Builder
	b.WriteString("--- edit delta ---\n")
	written := 0
	for _, h := range hunks {
		if written >= maxLines {
			b.WriteString("...\n")
			break
		}
		for _, l := range h.Lines {
			if written >= maxLines {
				b.WriteString("...\n")
				break
			}
			switch l.Kind {
			case hashline.LineDel:
				fmt.Fprintf(&b, "-%d:%s\n", l.OldNum, l.Content)
				written++
			case hashline.LineAdd:
				fmt.Fprintf(&b, "+%d:%s\n", l.NewNum, l.Content)
				written++
			case hashline.LineCtx:
				fmt.Fprintf(&b, " %d:%s\n", l.OldNum, l.Content)
				written++
			default:
				// LineHeader 等不计入行数，但也不消耗 maxLines 配额
				fmt.Fprintf(&b, "%s %s\n", l.Kind, l.Content)
			}
		}
	}
	return b.String()
}

func convertHunk(h hashline.EditHunk, filePath string) DiffHunk {
	out := DiffHunk{
		FilePath:       filePath,
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
const editContextLines = 5

// maxEditDisplay 编辑区域本身最多显示的行数（超出则截断）。
const maxEditDisplay = 20

// smallFileThreshold 全量显示的文件行数上限（≤此值时无需 re-read 即可编辑任意位置）。
const smallFileThreshold = 200

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

		// 小文件（≤200 行）：全量显示，LLM 可编辑任意位置无需 re-read
		if totalLines <= smallFileThreshold {
			b.WriteString("\n--- post-edit context (full file) ---\n")
			for i, line := range lines {
				fmt.Fprintf(&b, "%d:%s\n", i+1, line)
			}
			fmt.Fprintf(&b, "→ Full file shown (lines 1-%d). Reuse TAG for next edit anywhere.\n", totalLines)
			continue
		}
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

		b.WriteString("\n--- post-edit context ---\n")

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

		// 大文件：附加结构索引，便于导航到 post-edit context 未覆盖的区域
		b.WriteString(formatFileIndex(lines))

		// 末尾完整性检查：大文件编辑中段时，末尾不可见，追加最后 3 行作为结构哨兵
		if showEnd < totalLines-2 {
			b.WriteString("--- tail ---\n")
			tailStart := totalLines - 3
			if tailStart < showEnd+1 {
				tailStart = showEnd + 1
			}
			for i := tailStart; i < totalLines; i++ {
				fmt.Fprintf(&b, "%d:%s\n", i+1, lines[i])
			}
		}

		// 显式 re-read 指引：告诉 LLM 上下文覆盖范围，避免其自行判断
		if truncated {
			fmt.Fprintf(&b, "→ Context covers lines %d-%d (edit region truncated). Use file index to locate targets outside → re-read first.\n", showStart, showEnd)
		} else {
			fmt.Fprintf(&b, "→ Context covers lines %d-%d. Edit within this range → reuse TAG. Outside → re-read first.\n", showStart, showEnd)
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
// formatFileIndex 为大文件生成结构索引（段落首行），
// 帮助 LLM 在 post-edit context 未覆盖的区域定位编辑目标行号。
// 基于段落（连续非空行 = 一个段落）提取首行，上限 30 条；
// 超过上限则退化为每 25 行取样。
func formatFileIndex(lines []string) string {
	const maxEntries = 30
	type entry struct {
		line int
		text string
	}
	var entries []entry
	inParagraph := false
	for i, line := range lines {
		isBlank := strings.TrimSpace(line) == ""
		if !isBlank && !inParagraph {
			entries = append(entries, entry{line: i + 1, text: line})
			inParagraph = true
		} else if isBlank {
			inParagraph = false
		}
	}

	var b strings.Builder
	if len(entries) <= maxEntries {
		b.WriteString("--- file index ---\n")
		for _, e := range entries {
			fmt.Fprintf(&b, "%d:%s\n", e.line, e.text)
		}
	} else {
		b.WriteString("--- file index (sampled every 25 lines) ---\n")
		step := 25
		for i := 0; i < len(lines); i += step {
			for j := i; j < len(lines) && j < i+step; j++ {
				if strings.TrimSpace(lines[j]) != "" {
					fmt.Fprintf(&b, "%d:%s\n", j+1, lines[j])
					break
				}
			}
		}
	}
	return b.String()
}
