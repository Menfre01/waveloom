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
		"Find-and-replace on an existing file by exact string match. The system auto-corrects minor whitespace and Unicode differences.",
		"Set replace_all=true to replace every occurrence.",
		"Include 1-2 surrounding context lines if the match would otherwise be ambiguous.",
		"When NOT to use: creating new files → use write_file. Reading files → use read_file. Large rewrites → use write_file.",
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

	// ── Step 2.5: 目录检查 ──
	if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		return dirToListing(path), nil
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
		// Autofix 0：行号前缀 → 自动剥离 + 重新精确匹配
		if looksLikeLineNumberPrefix(p.OldString) {
			cleaned := stripLineNumberPrefixes(p.OldString)
			cleanedCount := strings.Count(original, cleaned)
			if cleanedCount == 1 || (cleanedCount > 1 && p.ReplaceAll) {
				cleanedParams := p
				cleanedParams.OldString = cleaned
				matchStart := lineForOffset(buildLineStarts(original), strings.Index(original, cleaned))
				return t.applyAutoFix(ctx, original, cleanedParams, path, matchStart, "line number prefixes")
			}
		}

		// 降级1：空白归一化匹配 → 自动修复
		if matchStart := findNormalizedMatchPosition(original, p.OldString, !p.ReplaceAll); matchStart >= 0 {
			return t.applyAutoFix(ctx, original, p, path, matchStart, "whitespace")
		}

		// 降级2：跳过空行 + 空白归一化匹配 → 自动修复
		if matchStart := findMatchSkippingBlankLines(original, p.OldString, !p.ReplaceAll); matchStart >= 0 {
			return t.applyAutoFix(ctx, original, p, path, matchStart, "blank lines / whitespace")
		}

		// 降级3：Unicode 标点归一化 + 空白归一化匹配 → 自动修复
		if matchStart := findNormalizedMatchPositionWithUnicode(original, p.OldString, !p.ReplaceAll); matchStart >= 0 {
			return t.applyAutoFix(ctx, original, p, path, matchStart, "unicode / whitespace")
		}

		// 降级4：空白归一化匹配不唯一 → 返回 hint 让 LLM 精确重试
		if hint := tryNormalizedMatch(original, p.OldString); hint != "" {
			return toolError(ErrorClassRecoverable, ErrKindNoMatch,
				fmt.Sprintf("no exact match for old_string in %s\n\n%s\n\n%s",
					path, hint, renderSearchHint(p.OldString, original)), nil), nil
		}
		return toolError(ErrorClassRecoverable, ErrKindNoMatch,
			fmt.Sprintf("no match found for old_string in %s\n%s",
				path, renderSearchHint(p.OldString, original)), nil), nil
	}

	if count > 1 && !p.ReplaceAll {
		result := buildMultipleMatchError(original, p.OldString, path, false)
		return result, nil
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
	fmt.Fprintf(&summary, "Edited file: %s\n", path)
	if p.ReplaceAll {
		fmt.Fprintf(&summary, "   Replaced %d occurrence(s)\n", count)
	} else {
		summary.WriteString("   Replaced 1 occurrence\n")
	}
	fmt.Fprintf(&summary, "   +%d -%d lines", added, removed)

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

// findNormalizedMatchPosition 在空白归一化后查找 oldStr 的匹配位置。
// 将原文每行和 oldStr 每行的连续空白压缩为单空格后逐行比较。
// requireUnique 为 true 时要求匹配唯一，否则只返回首次匹配。
// 返回 0-based 起始行号；若未找到或（requireUnique 时）匹配不唯一返回 -1。
func findNormalizedMatchPosition(original, oldStr string, requireUnique bool) int {
	origLines := strings.Split(original, "\n")
	oldLines := strings.Split(oldStr, "\n")

	if len(oldLines) == 0 {
		return -1
	}

	origNormalized := make([]string, len(origLines))
	for i, line := range origLines {
		origNormalized[i] = normalizeLine(line)
	}
	oldNormalized := make([]string, len(oldLines))
	for i, line := range oldLines {
		oldNormalized[i] = normalizeLine(line)
	}

	matchStart := findLineSequence(origNormalized, oldNormalized)
	if matchStart < 0 {
		return -1
	}

	// 确认唯一性（仅当 requireUnique 时）
	if requireUnique && findLineSequence(origNormalized[matchStart+1:], oldNormalized) >= 0 {
		return -1
	}

	return matchStart
}

// tryNormalizedMatch 在精确匹配失败后，尝试空白符归一化匹配。
// 将原文每行和 old_string 每行的连续空白压缩为单空格，然后进行逐行匹配。
// 若找到唯一匹配，返回实际匹配文本的提示（含行号和内容）。
// 若找不到匹配或多匹配，返回空字符串（继续走 renderSearchHint）。
func tryNormalizedMatch(original, oldStr string) string {
	matchStart := findNormalizedMatchPosition(original, oldStr, /*requireUnique*/ true)
	if matchStart < 0 {
		return ""
	}

	origLines := strings.Split(original, "\n")
	oldLines := strings.Split(oldStr, "\n")

	// 构造提示
	ctxStart := matchStart - 2
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxEnd := matchStart + len(oldLines) + 2
	if ctxEnd > len(origLines) {
		ctxEnd = len(origLines)
	}

	var buf strings.Builder
	buf.WriteString("⚠️  Whitespace mismatch detected. The content exists but with different whitespace:\n")
	buf.WriteString("\n  Did you mean this?\n")
	for i := ctxStart; i < ctxEnd; i++ {
		marker := "  "
		if i >= matchStart && i < matchStart+len(oldLines) {
			marker = "→ "
		}
		fmt.Fprintf(&buf, "%sLine %d: %s\n", marker, i+1, origLines[i])
	}
	buf.WriteString("\n  Copy the exact text (including indentation) from the lines marked → above.")

	return buf.String()
}

// findLineSequence 在 haystack 中查找 needle 序列，返回起始索引。
// 未找到返回 -1。
func findLineSequence(haystack, needle []string) int {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return -1
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j, n := range needle {
			if haystack[i+j] != n {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// normalizeLine 将一行内的连续空白符（空格、制表符）压缩为单空格，
// 并去除首尾空白。用于逐行容错匹配。
func normalizeLine(s string) string {
	var buf strings.Builder
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !inSpace {
				buf.WriteByte(' ')
				inSpace = true
			}
		} else {
			buf.WriteRune(r)
			inSpace = false
		}
	}
	return strings.TrimSpace(buf.String())
}

// normalizeLineWithUnicode 在 normalizeLine 基础上增加 Unicode 标点 → ASCII 归一化。
// 将排版级 Unicode 标点（弯引号、破折号、特殊空格等）映射为对应 ASCII 字符。
func normalizeLineWithUnicode(s string) string {
	var buf strings.Builder
	for _, r := range s {
		if mapped, ok := unicodePunctToASCII[r]; ok {
			buf.WriteRune(mapped)
		} else {
			buf.WriteRune(r)
		}
	}
	return normalizeLine(buf.String())
}

// unicodePunctToASCII 将常见的 Unicode 排版标点映射为 ASCII 等价物。
var unicodePunctToASCII = map[rune]rune{
	// 破折号 → ASCII '-'
	'\u2010': '-', '\u2011': '-', '\u2012': '-', '\u2013': '-',
	'\u2014': '-', '\u2015': '-', '\u2212': '-',
	// 弯双引号 → ASCII '"'
	'\u201C': '"', '\u201D': '"', '\u201E': '"', '\u201F': '"',
	// 弯单引号 → ASCII '\''
	'\u2018': '\'', '\u2019': '\'', '\u201A': '\'', '\u201B': '\'',
	// 特殊空白 → ASCII ' '（normalizeLine 仍会压缩）
	'\u00A0': ' ', '\u2002': ' ', '\u2003': ' ', '\u2004': ' ',
	'\u2005': ' ', '\u2006': ' ', '\u2007': ' ', '\u2008': ' ',
	'\u2009': ' ', '\u200A': ' ', '\u202F': ' ', '\u205F': ' ',
	'\u3000': ' ',
}

// findNormalizedMatchPositionWithUnicode 在 Unicode 归一化 + 空白归一化后查找 oldStr 的匹配位置。
// 将两边的 Unicode 标点映射为 ASCII、空白压缩后逐行比较。
// requireUnique 为 true 时要求匹配唯一。
// 返回 0-based 起始行号；若未找到或（requireUnique 时）匹配不唯一返回 -1。
func findNormalizedMatchPositionWithUnicode(original, oldStr string, requireUnique bool) int {
	origLines := strings.Split(original, "\n")
	oldLines := strings.Split(oldStr, "\n")

	if len(oldLines) == 0 {
		return -1
	}

	origNormalized := make([]string, len(origLines))
	for i, line := range origLines {
		origNormalized[i] = normalizeLineWithUnicode(line)
	}
	oldNormalized := make([]string, len(oldLines))
	for i, line := range oldLines {
		oldNormalized[i] = normalizeLineWithUnicode(line)
	}

	matchStart := findLineSequence(origNormalized, oldNormalized)
	if matchStart < 0 {
		return -1
	}

	// 确认唯一性（仅当 requireUnique 时）
	if requireUnique && findLineSequence(origNormalized[matchStart+1:], oldNormalized) >= 0 {
		return -1
	}

	return matchStart
}

// stripLineNumberPrefixes 移除每行开头的 read_file 行号前缀（[N] 或 [N] 后跟空格/制表符）。
// 返回去除前缀后的文本。
func stripLineNumberPrefixes(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if len(trimmed) >= 4 && trimmed[0] == '[' {
			end := strings.IndexByte(trimmed, ']')
			if end > 0 && end < 8 {
				numPart := trimmed[1:end]
				isNum := true
				for _, c := range numPart {
					if c < '0' || c > '9' {
						isNum = false
						break
					}
				}
				if isNum {
					rest := trimmed[end+1:]
					if len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
						rest = rest[1:]
					}
					// 保留原始行的前导空白 + 剩余内容
					leading := ""
					for _, c := range line {
						if c == ' ' || c == '\t' {
							leading += string(c)
						} else {
							break
						}
					}
					if len(rest) > 0 {
						lines[i] = leading + rest
					} else {
						lines[i] = leading // 空内容 → 只保留前导空白（通常为空字符串）
					}
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}

// renderSearchHint 当 old_string 未匹配时，在原文中寻找与 target 特征行编辑距离最小的若干行，
// 连同其上下各 1 行上下文一起返回，帮助 LLM 一眼看出 old_string 与原文的精确差异。
func renderSearchHint(target, content string) string {
	if target == "" || content == "" {
		return ""
	}

	// 选择最特征行：多行 old_string 时用最长非空行而非首行，提高匹配精度
	query := pickBestQueryLine(target)
	queryRunes := []rune(query)
	if len(queryRunes) < 4 {
		return ""
	}

	// 检测是否误复制了 read_file 的行号前缀 [N] ...
	if looksLikeLineNumberPrefix(target) {
		hint := "   Hint: old_string appears to include line number prefixes like [N] from read_file output.\n"
		hint += "   The prefixes were automatically stripped but the cleaned content still did not match.\n"
		hint += "   Re-read the file with read_file and copy the raw content without line numbers."
		return hint
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
			fmt.Fprintf(&buf, "   Line %d: %s\n", c.index, fileLines[c.index-1])
			seen[c.index-1] = true
		}
		// 相似行（高亮差异）
		fmt.Fprintf(&buf, "→  Line %d: %s  (distance=%d)\n", c.index+1, fileLines[c.index], c.distance)
		// 后一行上下文
		if c.index+1 < len(fileLines) && !seen[c.index+1] {
			fmt.Fprintf(&buf, "   Line %d: %s\n", c.index+2, fileLines[c.index+1])
			seen[c.index+1] = true
		}

		// 对最佳匹配（第一个打印的），若距离很小，附加字符级 diff
		if printed == 0 && c.distance > 0 && c.distance <= 3 {
			buf.WriteString(formatCharDiffHint(query, fileLines[c.index]))
		}
		printed++
	}

	if printed == 0 {
		return ""
	}
	return fmt.Sprintf("   Hint: closest matches to old_string:\n%s", buf.String())
}

// pickBestQueryLine 从多行 old_string 中选择最佳查询行。
// 优先选择最长的非空行（最具区分度），避免用 `}` 或空行等通用模式做模糊匹配。
func pickBestQueryLine(target string) string {
	lines := strings.Split(target, "\n")
	if len(lines) <= 1 {
		return target
	}
	best := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > len(best) {
			best = trimmed
		}
	}
	if best == "" {
		// 所有行都是空白 → 用第一行
		return strings.SplitN(target, "\n", 2)[0]
	}
	return best
}

// looksLikeLineNumberPrefix 检测 old_string 是否疑似包含 read_file 输出的行号前缀。
// read_file 输出格式为 "[N] content"，如果 old_string 中包含此模式则返回 true。
func looksLikeLineNumberPrefix(s string) bool {
	// 检测是否以 [数字] 开头，且后面跟空格
	lines := strings.Split(s, "\n")
	matchCount := 0
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if len(trimmed) >= 4 && trimmed[0] == '[' {
			end := strings.IndexByte(trimmed, ']')
			if end > 1 && end < 8 {
				// 检查括号内是否为数字
				numPart := trimmed[1:end]
				isNum := true
				for _, c := range numPart {
					if c < '0' || c > '9' {
						isNum = false
						break
					}
				}
				if isNum && end+1 < len(trimmed) && trimmed[end+1] == ' ' {
					matchCount++
				}
			}
		}
	}
	// 如果超过一半的行匹配行号前缀模式，判定为误复制
	return matchCount > 0 && matchCount*2 >= len(lines)
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

// formatCharDiffHint 为两个相似字符串生成字符级差异提示。
// 找出首个不同字符的位置，用 ^ 标记，方便 LLM 一眼定位差异。
// 若两串差异在末尾（长度不同），标记截断/多余位置。
func formatCharDiffHint(query, fileLine string) string {
	qr := []rune(query)
	fr := []rune(fileLine)

	// 找最长公共前缀
	prefixLen := 0
	for prefixLen < len(qr) && prefixLen < len(fr) && qr[prefixLen] == fr[prefixLen] {
		prefixLen++
	}

	// 找最长公共后缀（从前缀之后开始）
	suffixLen := 0
	for suffixLen < len(qr)-prefixLen && suffixLen < len(fr)-prefixLen &&
		qr[len(qr)-1-suffixLen] == fr[len(fr)-1-suffixLen] {
		suffixLen++
	}

	// 完全相同时不生成 diff（调用方保证 distance > 0，此处防御）
	if prefixLen == len(qr) && prefixLen == len(fr) {
		return ""
	}

	var buf strings.Builder
	buf.WriteString("\n   Character diff:\n")

	// 截断显示：前缀过长时省略
	const maxPrefix = 30
	displayPrefix := prefixLen
	prefixOmitted := ""
	if displayPrefix > maxPrefix {
		displayPrefix = maxPrefix
		prefixOmitted = "..."
	}

	// 文件中的行
	buf.WriteString("   File:  ")
	buf.WriteString(prefixOmitted)
	buf.WriteString(string(fr[displayPrefix:]))
	buf.WriteString("\n")

	// LLM 提供的行
	buf.WriteString("   Yours: ")
	buf.WriteString(prefixOmitted)
	buf.WriteString(string(qr[displayPrefix:]))
	buf.WriteString("\n")

	// 差异标记 ^
	markerPos := len(prefixOmitted) + (prefixLen - displayPrefix)
	buf.WriteString("          ")
	for i := 0; i < markerPos; i++ {
		buf.WriteByte(' ')
	}

	// 标记差异区域长度
	diffLen := len(fr) - prefixLen - suffixLen
	if alt := len(qr) - prefixLen - suffixLen; alt > diffLen {
		diffLen = alt
	}
	if diffLen <= 0 {
		diffLen = 1
	}
	if diffLen > 20 {
		diffLen = 20
	}
	for i := 0; i < diffLen; i++ {
		buf.WriteByte('^')
	}
	buf.WriteString(" ← differs here")

	return buf.String()
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

// ---------------------------------------------------------------------------
// applyAutoFix — 抽象公共的自动修复 + 替换 + 写回逻辑
// ---------------------------------------------------------------------------

// applyAutoFix 在降级匹配成功后执行实际的替换和写回。
// matchStart 是原文中的 0-based 起始行号；fixKind 用于日志描述（"whitespace" / "blank lines / whitespace"）。
func (t *EditFile) applyAutoFix(ctx context.Context, original string, p EditFileParams, path string, matchStart int, fixKind string) (*ToolResult, error) {
	origLines := strings.Split(original, "\n")
	oldLines := strings.Split(p.OldString, "\n")
	realOld := strings.Join(origLines[matchStart:matchStart+len(oldLines)], "\n")

	// 检查实际原文是否也多次出现
	realCount := strings.Count(original, realOld)
	if realCount > 1 && !p.ReplaceAll {
		// 用 realOld（文件中实际匹配的文本）而非 p.OldString（可能有空白差异）
		result := buildMultipleMatchError(original, realOld, path, true)
		return result, nil
	}

	matchPositions := findAllMatches(original, realOld)
	if !p.ReplaceAll && len(matchPositions) > 1 {
		matchPositions = matchPositions[:1]
	}
	hunks := buildDiffHunks(original, matchPositions, realOld, p.NewString, 3)

	// 执行替换
	var result string
	if p.ReplaceAll {
		result = strings.ReplaceAll(original, realOld, p.NewString)
	} else {
		result = strings.Replace(original, realOld, p.NewString, 1)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(result), 0o644); err != nil {
		return toolError(ErrorClassFatal, ErrKindPermissionDenied,
			fmt.Sprintf("cannot write file: %s", path), err), nil
	}

	added, removed := diffStats(hunks)
	effCount := len(matchPositions)
	var summary strings.Builder
	fmt.Fprintf(&summary, "Edited file: %s\n", path)
	fmt.Fprintf(&summary, "   ⚠️  Auto-corrected %s: old_string did not exactly match the file.\n", fixKind)
	fmt.Fprintf(&summary, "   Matched lines %d-%d by content.\n",
		matchStart+1, matchStart+len(oldLines))
	if p.ReplaceAll {
		fmt.Fprintf(&summary, "   Replaced %d occurrence(s)\n", effCount)
	} else {
		summary.WriteString("   Replaced 1 occurrence\n")
	}
	fmt.Fprintf(&summary, "   +%d -%d lines", added, removed)

	return &ToolResult{
		Content: summary.String(),
		Meta: ToolMeta{
			FilePath:  path,
			DiffHunks: hunks,
		},
	}, nil
}

// ---------------------------------------------------------------------------
// findMatchSkippingBlankLines — 跳过空行的后备匹配
// ---------------------------------------------------------------------------

// findMatchSkippingBlankLines 在忽略空行差异的前提下进行空白归一化序列匹配。
// 将原文和 oldStr 中的空行（含纯空白行）全部移除后，对剩余非空行做 normalizeLine 序列匹配。
// requireUnique 为 true 时要求匹配唯一。
// 返回原文中的 0-based 起始行号；未找到或（requireUnique 时）匹配不唯一返回 -1。
func findMatchSkippingBlankLines(original, oldStr string, requireUnique bool) int {
	origLines := strings.Split(original, "\n")
	oldLines := strings.Split(oldStr, "\n")

	origNonBlank, origMap := extractNonBlankNormalized(origLines)
	oldNonBlank, _ := extractNonBlankNormalized(oldLines)

	if len(oldNonBlank) == 0 {
		return -1
	}

	// 在 origNonBlank 中查找 oldNonBlank 的连续子序列
	matchIdx := findLineSequence(origNonBlank, oldNonBlank)
	if matchIdx < 0 {
		return -1
	}

	// 确认唯一性
	if requireUnique && findLineSequence(origNonBlank[matchIdx+1:], oldNonBlank) >= 0 {
		return -1
	}

	// 映射回原文行号：匹配的第一个非空行在原文中的位置
	return origMap[matchIdx]
}

// extractNonBlankNormalized 提取所有非空行并做空白归一化。
// 返回归一化后的非空行列表和每行在原始列表中的索引。
func extractNonBlankNormalized(lines []string) (nonBlank []string, lineMap []int) {
	for i, line := range lines {
		n := normalizeLine(line)
		if n != "" {
			nonBlank = append(nonBlank, n)
			lineMap = append(lineMap, i)
		}
	}
	return
}

// ---------------------------------------------------------------------------
// buildMultipleMatchError — 多匹配时生成带上下文的错误
// ---------------------------------------------------------------------------

// buildMultipleMatchError 当 old_string 在文件中多处匹配时，为每个匹配位置生成
// 带 ±2 行上下文的错误消息，让 LLM 可以直接看到各位置的差异并构造唯一的 old_string。
// autoFix 为 true 表示来自自动修复路径（空白归一化后仍不唯一），此时提示中会额外说明。
func buildMultipleMatchError(original, oldStr, path string, autoFix bool) *ToolResult {
	positions := findAllMatches(original, oldStr)
	origLines := strings.Split(original, "\n")
	oldLines := strings.Split(oldStr, "\n")
	oldLineCount := len(oldLines)

	// 限制最多展示的匹配位置数
	const maxDisplay = 5
	displayCount := len(positions)
	truncated := false
	if displayCount > maxDisplay {
		displayCount = maxDisplay
		truncated = true
	}

	var buf strings.Builder
	if autoFix {
		fmt.Fprintf(&buf, "auto-corrected old_string still matches %d locations in %s.\n", len(positions), path)
	} else {
		fmt.Fprintf(&buf, "old_string matches %d locations in %s.\n", len(positions), path)
	}
	buf.WriteString("Include more surrounding context to make it unique:\n")

	for i := 0; i < displayCount; i++ {
		pos := positions[i]
		matchLine := lineForOffset(buildLineStarts(original), pos)

		ctxStart := matchLine - 2
		if ctxStart < 0 {
			ctxStart = 0
		}
		ctxEnd := matchLine + oldLineCount + 2
		if ctxEnd > len(origLines) {
			ctxEnd = len(origLines)
		}

		fmt.Fprintf(&buf, "\n  Occurrence %d (line %d):\n", i+1, matchLine+1)
		for j := ctxStart; j < ctxEnd; j++ {
			marker := "   "
			if j >= matchLine && j < matchLine+oldLineCount {
				marker = " → "
			}
			fmt.Fprintf(&buf, "%s%4d  %s\n", marker, j+1, origLines[j])
		}
	}

	if truncated {
		fmt.Fprintf(&buf, "\n  ... and %d more occurrences (not shown).\n", len(positions)-maxDisplay)
	}

	buf.WriteString("\nPick one location and include 1-2 unique surrounding lines in old_string.")

	return toolError(ErrorClassRecoverable, ErrKindMultipleMatch, buf.String(), nil)
}

// dirToListing returns a recoverable error with the directory contents listed,
// so the LLM can immediately pick the correct file path without a separate ls call.
func dirToListing(path string) *ToolResult {
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
			fmt.Fprintf(&listing, "Showing first %d of %d entries:\n", maxDisplay, total)
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
		return toolError(ErrorClassRecoverable, ErrKindNotDir, listing.String(), nil)
	}
	return toolError(ErrorClassRecoverable, ErrKindNotDir,
		fmt.Sprintf("path is a directory, not a file: %s", path), nil)
}
