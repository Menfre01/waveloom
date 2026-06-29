package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// EditFile — 基于文本匹配的精确编辑
// ---------------------------------------------------------------------------

type EditFileParams struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
	WorkingDir string `json:"working_dir"` // 工作目录（可选），相对路径基于此解析
}

type EditFile struct{}

func (t *EditFile) Name() string            { return "edit_file" }
func (t *EditFile) Description() string {
	return strings.Join([]string{
		"Find-and-replace based on exact string match. old_string must be unique.",
		"",
		"For partial edits of existing files. Prefer over write_file:",
		"  - Small changes (≤50 lines) → use edit_file",
		"  - New files or full overwrites → use write_file",
		"  - Always confirm old_string content with read_file before editing (including indentation and whitespace)",
		"  - If old_string is not unique, expand the context to make it unique",
	}, "\n")
}
func (t *EditFile) Schema() json.RawMessage { return editFileSchema }
func (t *EditFile) ConcurrentSafe() bool    { return false }

func (t *EditFile) Execute(ctx context.Context, p EditFileParams) (*ToolResult, error) {
	// ── Step 1: 参数验证（读取文件前的纯逻辑检查）──
	if p.OldString == "" {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			"old_string cannot be empty", nil), nil
	}

	// ── Step 2: 路径解析 ──
	path, err := pathutil.ResolvePathWithDir(p.FilePath, p.WorkingDir)
	if err != nil {
		return toolError(ErrorClassRecoverable, ErrKindInvalidArgs,
			fmt.Sprintf("invalid path: %v", err), err), nil
	}

	// ── Step 3: 读取原文 ──
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return toolError(ErrorClassRecoverable, ErrKindFileNotFound,
				fmt.Sprintf("file not found: %s", path), err), nil
		}
		return toolError(ErrorClassFatal, ErrKindPermissionDenied,
			fmt.Sprintf("cannot read file: %s", path), err), nil
	}

	original := string(raw)

	// ── Step 4: 匹配 old_string ──
	count := strings.Count(original, p.OldString)

	if count == 0 {
		return toolError(ErrorClassRecoverable, ErrKindNoMatch,
			fmt.Sprintf("no match found for old_string in %s\n%s",
				path, renderSearchHint(p.OldString, original)), nil), nil
	}

	if count > 1 && !p.ReplaceAll {
		return toolError(ErrorClassRecoverable, ErrKindMultipleMatch,
			fmt.Sprintf("found %d matches for old_string in %s; provide more context or set replace_all=true",
				count, path), nil), nil
	}

	// ── Step 5: 生成 diff hunks（替换前，使用原文位置）──
	matchPositions := findAllMatches(original, p.OldString)
	if !p.ReplaceAll && len(matchPositions) > 1 {
		matchPositions = matchPositions[:1]
	}
	hunks := buildDiffHunks(original, matchPositions, p.OldString, p.NewString, 3)

	// ── Step 6: 执行替换 ──
	var result string
	if p.ReplaceAll {
		result = strings.ReplaceAll(original, p.OldString, p.NewString)
	} else {
		result = strings.Replace(original, p.OldString, p.NewString, 1)
	}

	// ── Step 7: 写入 ──
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(result), 0o644); err != nil {
		return toolError(ErrorClassFatal, ErrKindPermissionDenied,
			fmt.Sprintf("cannot write file: %s", path), err), nil
	}

	// ── Step 8: 构造精简 Content（给 LLM）+ 结构化 Meta（给 TUI）──
	added, removed := diffStats(hunks)
	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("Edited file: %s\n", path))
	if p.ReplaceAll {
		summary.WriteString(fmt.Sprintf("   Replaced %d occurrence(s)\n", count))
	} else {
		summary.WriteString("   Replaced 1 occurrence\n")
	}
	summary.WriteString(fmt.Sprintf("   +%d -%d lines", added, removed))

	return &ToolResult{
		Content: summary.String(),
		Meta: ToolMeta{
			FilePath:  path,
			DiffHunks: hunks,
		},
	}, nil
}

// findAllMatches 返回 old 在 s 中所有出现位置的字节偏移量。
func findAllMatches(s, old string) []int {
	var positions []int
	offset := 0
	for {
		idx := strings.Index(s[offset:], old)
		if idx < 0 {
			break
		}
		positions = append(positions, offset+idx)
		offset += idx + len(old)
	}
	return positions
}

// buildDiffHunks 基于原文、匹配位置和 old/new 字符串，生成带上下文的统一 diff 块列表。
//
// 对于 replace_all 场景，会合并重叠的 hunk 窗口并正确计算累积行号偏移，
// 确保生成与标准 unified diff 语义一致的输出。
func buildDiffHunks(original string, positions []int, oldStr, newStr string, contextLines int) []DiffHunk {
	if len(positions) == 0 {
		return nil
	}

	origLines := splitLines(original)
	oldLines := splitLines(oldStr)
	newLines := splitLines(newStr)
	oldLineCount := len(oldLines)
	newLineCount := len(newLines)
	totalLines := len(origLines)

	lineStarts := buildLineStarts(original)

	// ── Step 1: 将所有匹配位置转为行号，计算 hunk 窗口（1-based）──
	type matchSpan struct {
		matchLine int // 0-based，匹配起始行
		ctxStart  int // 1-based，含上下文窗口起始
		ctxEnd    int // 1-based，含上下文窗口结束（inclusive）
	}
	spans := make([]matchSpan, len(positions))
	for i, pos := range positions {
		ml := lineForOffset(lineStarts, pos)
		cs := ml - contextLines + 1
		if cs < 1 {
			cs = 1
		}
		ce := ml + oldLineCount + contextLines
		if ce > totalLines {
			ce = totalLines
		}
		spans[i] = matchSpan{matchLine: ml, ctxStart: cs, ctxEnd: ce}
	}

	// ── Step 2: 合并重叠的 hunk 窗口 ──
	merged := make([]matchSpan, 0, len(spans))
	for _, s := range spans {
		if len(merged) == 0 {
			merged = append(merged, s)
			continue
		}
		last := &merged[len(merged)-1]
		if s.ctxStart <= last.ctxEnd {
			// 窗口重叠或相邻 → 合并
			if s.ctxEnd > last.ctxEnd {
				last.ctxEnd = s.ctxEnd
			}
			// 保留第一个匹配行作为 heading 定位
		} else {
			merged = append(merged, s)
		}
	}

	// ── Step 3: 对每个合并后的窗口生成 DiffHunk ──
	hunks := make([]DiffHunk, 0, len(merged))
	cumulativeShift := 0 // 之前所有 hunk 产生的行数净变化（删除 − 新增）

	for _, mw := range merged {
		// 收集当前窗口内所有的匹配起始行
		var windowMatches []int
		for _, s := range spans {
			if s.matchLine >= mw.ctxStart-1 && s.matchLine+oldLineCount-1 < mw.ctxEnd {
				windowMatches = append(windowMatches, s.matchLine)
			}
		}

		hunk := DiffHunk{
			OldStart: mw.ctxStart,
			OldCount: mw.ctxEnd - mw.ctxStart + 1,
			NewStart: mw.ctxStart - cumulativeShift,
			Heading:  extractHeading(origLines, mw.matchLine),
		}

		hunkAdded := 0
		hunkDeleted := 0
		matchIdx := 0 // windowMatches 的游标

		lineIdx := mw.ctxStart - 1 // 0-based
		for lineIdx < mw.ctxEnd {
			if matchIdx < len(windowMatches) && lineIdx == windowMatches[matchIdx] {
				// ── 变更区域：先输出删除行，再输出新增行 ──
				for i := 0; i < oldLineCount; i++ {
					oldNum := lineIdx + i + 1
					hunk.Lines = append(hunk.Lines, DiffLine{
						Kind:    DiffDel,
						Content: oldLines[i],
						OldNum:  oldNum,
						NewNum:  0,
					})
				}
				hunkDeleted += oldLineCount

				newBase := lineIdx + 1 - cumulativeShift
				for i := 0; i < newLineCount; i++ {
					hunk.Lines = append(hunk.Lines, DiffLine{
						Kind:    DiffAdd,
						Content: newLines[i],
						OldNum:  0,
						NewNum:  newBase + i,
					})
				}
				hunkAdded += newLineCount

				lineIdx += oldLineCount
				matchIdx++
			} else {
				// ── 上下文行 ──
				hunk.Lines = append(hunk.Lines, DiffLine{
					Kind:    DiffCtx,
					Content: origLines[lineIdx],
					OldNum:  lineIdx + 1,
					NewNum:  lineIdx + 1 - cumulativeShift,
				})
				lineIdx++
			}
		}

		hunk.NewCount = hunk.OldCount - hunkDeleted + hunkAdded

		// 检测 NoNewlineAtEOF：若 hunk 覆盖文件末尾且原文不以 \n 结尾，
		// 或替换内容不以 \n 结尾，标记之（符合 POSIX unified diff）。
		if (mw.ctxEnd >= totalLines && !strings.HasSuffix(original, "\n")) ||
			!strings.HasSuffix(newStr, "\n") {
			hunk.NoNewlineAtEOF = true
		}

		hunks = append(hunks, hunk)

		// 累加此 hunk 产生的行号偏移（删除行数 − 新增行数）
		cumulativeShift += hunkDeleted - hunkAdded
	}

	return hunks
}

// buildLineStarts 返回每行在 s 中的起始字节偏移。
func buildLineStarts(s string) []int {
	starts := []int{0} // 第 0 行始终从偏移 0 开始
	for i, r := range s {
		if r == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// lineForOffset 根据行起始偏移表返回 offset 所在的行号（0-based）。
func lineForOffset(starts []int, offset int) int {
	for i := len(starts) - 1; i >= 0; i-- {
		if starts[i] <= offset {
			return i
		}
	}
	return 0
}

// diffStats 汇总所有 hunk 的增删行数。
func diffStats(hunks []DiffHunk) (added, removed int) {
	for _, h := range hunks {
		a, r := h.Stats()
		added += a
		removed += r
	}
	return
}

// renderSearchHint 当 old_string 未匹配时，在原文中寻找与 target 首行编辑距离最小的若干行，
// 连同其上下各 1 行上下文一起返回，帮助 LLM 一眼看出 old_string 与原文的精确差异。
func renderSearchHint(target, content string) string {
	if target == "" || content == "" {
		return ""
	}

	query := strings.SplitN(target, "\n", 2)[0]
	queryRunes := []rune(query)
	if len(queryRunes) < 4 {
		return ""
	}

	fileLines := strings.Split(content, "\n")

	// 找编辑距离最小的 topN 行（排除完全匹配的行，因为如果完全匹配 count 不会是 0）
	type candidate struct {
		index    int
		distance int
	}
	const topN = 3
	best := make([]candidate, topN)
	for i := range best {
		best[i] = candidate{index: -1, distance: -1}
	}

	for i, line := range fileLines {
		if line == query {
			continue // 完全相同的行不会导致 no_match
		}
		lineRunes := []rune(line)
		dist := levenshteinDistance(queryRunes, lineRunes)
		if dist < 0 {
			continue
		}
		// 插入排序保持 topN
		for j := 0; j < topN; j++ {
			if best[j].index < 0 || dist < best[j].distance {
				// 向下推移
				for k := topN - 1; k > j; k-- {
					best[k] = best[k-1]
				}
				best[j] = candidate{index: i, distance: dist}
				break
			}
		}
	}

	// 收集 topN 结果（去重行号），附上下文
	var buf strings.Builder
	seen := make(map[int]bool)
	printed := 0
	for _, c := range best {
		if c.index < 0 || seen[c.index] {
			continue
		}
		seen[c.index] = true
		if printed >= topN {
			break
		}
		if printed > 0 {
			buf.WriteString("\n")
		}
		// 前一行上下文
		if c.index > 0 && !seen[c.index-1] {
			buf.WriteString(fmt.Sprintf("   Line %d: %s\n", c.index, fileLines[c.index-1]))
			seen[c.index-1] = true
		}
		// 相似行（高亮差异）
		buf.WriteString(fmt.Sprintf("→  Line %d: %s  (distance=%d)\n", c.index+1, fileLines[c.index], c.distance))
		// 后一行上下文
		if c.index+1 < len(fileLines) && !seen[c.index+1] {
			buf.WriteString(fmt.Sprintf("   Line %d: %s\n", c.index+2, fileLines[c.index+1]))
			seen[c.index+1] = true
		}
		printed++
	}

	if printed == 0 {
		return ""
	}
	return fmt.Sprintf("   Hint: closest matches to first line of old_string:\n%s", buf.String())
}

// levenshteinDistance 计算两个 rune 序列的编辑距离。
// 若任一长度超过 200 则返回 -1（跳过，避免长行性能开销），
// 若任一为空则返回另一方的长度。
func levenshteinDistance(a, b []rune) int {
	if len(a) > 200 || len(b) > 200 {
		return -1
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// 用单行滚动数组，O(min(m,n)) 空间
	if len(a) < len(b) {
		a, b = b, a
	}
	m, n := len(a), len(b)
	prev := make([]int, n+1)
	curr := make([]int, n+1)
	for j := 0; j <= n; j++ {
		prev[j] = j
	}
	for i := 1; i <= m; i++ {
		curr[0] = i
		for j := 1; j <= n; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			min := prev[j] + 1 // deletion
			if v := curr[j-1] + 1; v < min { // insertion
				min = v
			}
			if v := prev[j-1] + cost; v < min { // substitution
				min = v
			}
			curr[j] = min
		}
		prev, curr = curr, prev
	}
	return prev[n]
}

// extractHeading 从原文匹配行向前扫描，提取最接近的函数/方法/类型签名作为 hunk 头部上下文。
// 返回匹配行的前一行中看起来像声明的文本（去除前导空白后截断至 60 字符）。
func extractHeading(lines []string, matchLine int) string {
	// 从 matchLine-1 向前搜索，最多搜索 10 行
	start := matchLine - 1
	if start < 0 {
		return ""
	}
	end := start - 10
	if end < 0 {
		end = -1
	}
	for i := start; i > end; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		// 匹配常见声明关键字：func / def / class / function / pub fn / fn / private fn / protected fn
		if isDeclarationLine(trimmed) {
			if len(trimmed) > 60 {
				trimmed = trimmed[:60]
			}
			return trimmed
		}
	}
	return ""
}

// declarationPatterns 定义常见语言的声明行匹配模式。
var declarationPatterns = []string{
	"func ",      // Go
	"def ",       // Python / Ruby
	"class ",     // Python / Ruby / Java / C++ 等
	"function ",  // JavaScript / PHP
	"pub fn ",    // Rust
	"fn ",        // Rust
	"interface ", // Go / Java / TypeScript
	"type ",      // Go type 定义
	"struct ",    // Go / Rust / C
	"enum ",      // Rust / C++ / TypeScript
	"trait ",     // Rust
	"impl ",      // Rust
	"@Override",  // Java
	"@GetMapping", "@PostMapping", "@PutMapping", "@DeleteMapping", // Spring
}

// isDeclarationLine 判断一行是否为函数/类/类型声明。
func isDeclarationLine(line string) bool {
	for _, p := range declarationPatterns {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}
