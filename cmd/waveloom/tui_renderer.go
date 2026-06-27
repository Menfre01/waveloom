package main

import (
	"fmt"
	"strings"

	"waveloom/pkg/llm"
	"waveloom/pkg/tool"

	"charm.land/bubbles/v2/spinner"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

// ---------------------------------------------------------------------------
// 段落类型
// ---------------------------------------------------------------------------

// ParagraphType 标识段落的角色。
type ParagraphType int

const (
	paraUser      ParagraphType = iota // > 用户消息
	paraAssistant                      // * assistant 回复（含 markdown）
	paraThought                        // ~ 思考过程
	paraTool                           // ● 工具调用
	paraSystem                         // ◼ 系统提示（终止原因、状态通知等）
)

// paraStateEnum 标识段落的渲染状态。
type paraStateEnum int

const (
	stateStreaming  paraStateEnum = iota // 流式进行中 → 呼吸动画
	stateDone                            // 完成 → 静态
	stateCollapsed                       // 折叠态（thought 收敛为一行 / tool 默认摘要+预览）
	stateExpanded                        // 展开态（thought 完整内容外框 / tool 完整输出）
	stateError                           // 错误态（tool 红色）
)

// systemNotifKind 区分系统通知的类型，用于着色。
type systemNotifKind int

const (
	notifInfo  systemNotifKind = iota // 完成/中断 — gray
	notifWarn                         // 警告 — amber gold
	notifError                        // 错误 — red
)

// ---------------------------------------------------------------------------
// Paragraph 结构体
// ---------------------------------------------------------------------------

// Paragraph 是 TUI 中一条可渲染的对话段落。
type Paragraph struct {
	Type  ParagraphType
	State paraStateEnum
	Text  string // 文本内容（assistant / thought / user 消息正文）

	// Tool 专用字段
	ToolName   string
	ToolArgs   string // 格式化后的参数摘要
	ToolResult string // 完整输出（展开时显示）
	ToolError  string // 错误信息
	ToolDurMs  int64  // 执行耗时（毫秒）
	ToolDenied bool   // 权限被拒
	DiffHunks  []tool.DiffHunk // edit_file 结构化 diff（nil = 不适用或纯文本回退）

	// Thought 专用字段
	ThoughtTokens int // 完成后的 token 数

	// System 专用字段
	NotifKind systemNotifKind // 通知类型（仅 paraSystem 有效）

	// 渲染缓存（避免每次 buildViewportContent 时重复 Glamour 渲染）
	renderedCache string
	cacheWidth    int // 缓存时的 viewport 宽度，宽度变化时失效

	// 段落级渲染缓存：缓存完整的渲染后行数组（含前缀/缩进），避免全量重建。
	// renderDirty=true 表示段落内容或状态已变更，需要重新渲染。
	renderDirty    bool
	cachedLines    []string // 缓存的渲染后行（含前缀/缩进/换行）
	cachedWidth    int      // 缓存时的 viewport 宽度
	cachedFocused  bool     // 缓存时的焦点状态
}

// ---------------------------------------------------------------------------
// 段落列表操作
// ---------------------------------------------------------------------------

// lastPara 返回段落列表的最后一个元素指针，nil 表示列表为空。
func lastPara(paras []Paragraph) *Paragraph {
	if len(paras) == 0 {
		return nil
	}
	return &paras[len(paras)-1]
}

// ---------------------------------------------------------------------------
// 工具参数摘要格式化
// ---------------------------------------------------------------------------

// stripCWDPrefix 切掉 cwd 前缀（含尾部 /），使路径相对 cwd 显示。
// 若字段不以 cwd 为前缀则原样返回。
func stripCWDPrefix(field, cwd string) string {
	if cwd == "" || field == "" {
		return field
	}
	prefix := cwd + "/"
	if strings.HasPrefix(field, prefix) {
		return field[len(prefix):]
	}
	// 容忍 cwd 不以 / 结尾的情况
	if strings.HasPrefix(field, cwd+"/") {
		return field[len(cwd)+1:]
	}
	return field
}

// formatToolArgs 将工具名和 JSON 参数格式化为一行可读摘要。
func formatToolArgs(toolName string, argsJSON string, cwd string) string {
	switch toolName {
	case "read_file":
		return stripCWDPrefix(extractField(argsJSON, "file_path"), cwd)
	case "write_file":
		return stripCWDPrefix(extractField(argsJSON, "file_path"), cwd)
	case "edit_file":
		return stripCWDPrefix(extractField(argsJSON, "file_path"), cwd)
	case "shell":
		return extractField(argsJSON, "command")
	case "grep":
		pattern := extractField(argsJSON, "pattern")
		dir := extractField(argsJSON, "working_dir")
		if dir != "" {
			return fmt.Sprintf(`"%s" in %s`, pattern, stripCWDPrefix(dir, cwd))
		}
		return fmt.Sprintf(`"%s"`, pattern)
	case "search_file":
		pattern := extractField(argsJSON, "pattern")
		dir := extractField(argsJSON, "working_dir")
		if dir != "" {
			return fmt.Sprintf("%s in %s", pattern, stripCWDPrefix(dir, cwd))
		}
		return pattern
	case "ls":
		return stripCWDPrefix(extractField(argsJSON, "path"), cwd)
	case "lsp_diagnostic", "lsp_definition", "lsp_references", "lsp_hover":
		fp := extractField(argsJSON, "file_path")
		line := extractField(argsJSON, "line")
		col := extractField(argsJSON, "character")
		if line != "" && col != "" {
			return fmt.Sprintf("%s:%s:%s", stripCWDPrefix(fp, cwd), line, col)
		}
		return stripCWDPrefix(fp, cwd)
	case "web_fetch":
		u := extractField(argsJSON, "url")
		if u != "" {
			return u
		}
		return truncateStr(argsJSON, 50)
	default:
		return truncateStr(argsJSON, 50)
	}
}

// extractField 从 JSON 字符串中提取指定 key 的字符串值（纯字符串操作，零分配热路径）。
func extractField(jsonStr, key string) string {
	search := `"` + key + `"`
	idx := strings.Index(jsonStr, search)
	if idx < 0 {
		return ""
	}
	rest := jsonStr[idx+len(search):]

	// 跳过空白和冒号
	colonIdx := strings.Index(rest, ":")
	if colonIdx < 0 {
		return ""
	}
	rest = strings.TrimLeft(rest[colonIdx+1:], " \t")

	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:] // 跳过开引号

	endIdx := strings.Index(rest, `"`)
	if endIdx < 0 {
		return ""
	}
	return rest[:endIdx]
}

// truncateStr 截断字符串到 maxLen。
func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}

// ---------------------------------------------------------------------------
// 工具摘要后缀格式化
// ---------------------------------------------------------------------------

// toolSuffix 根据工具类型和结果生成成功/失败的后缀字符串。
func toolSuffix(p *Paragraph) string {
	// 工具仍在执行中，尚无结果
	if p.State == stateStreaming {
		return ""
	}

	if p.ToolError != "" {
		return fmt.Sprintf("(%s)", p.ToolError)
	}
	if p.ToolDenied {
		return "(permission denied)"
	}

	switch p.ToolName {
	case "read_file", "write_file":
		size := formatBytes(len(p.ToolResult))
		dur := formatDuration(p.ToolDurMs)
		return fmt.Sprintf("(%s, %s)", size, dur)
	case "edit_file":
		dur := formatDuration(p.ToolDurMs)
		var added, removed int
		if p.DiffHunks != nil {
			for _, h := range p.DiffHunks {
				a, r := h.Stats()
				added += a
				removed += r
			}
		} else {
			added, removed = countDiffLines(p.ToolResult)
		}
		return fmt.Sprintf("(+%d -%d lines, %s)", added, removed, dur)
	case "shell":
		code := parseExitCode(p.ToolResult)
		dur := formatDuration(p.ToolDurMs)
		if code >= 0 {
			return fmt.Sprintf("(exit=%d, %s)", code, dur)
		}
		return fmt.Sprintf("(%s)", dur)
	case "grep":
		matches := countMatches(p.ToolResult)
		return fmt.Sprintf("(%d matches)", matches)
	case "search_file":
		files := countLines(p.ToolResult)
		return fmt.Sprintf("(%d files)", files)
	case "ls":
		entries := countLines(p.ToolResult)
		return fmt.Sprintf("(%d entries)", entries)
	case "lsp_diagnostic":
		n, e, w, i, h := parseDiagnosticSummary(p.ToolResult)
		if n < 0 {
			return "✓"
		}
		dur := formatDuration(p.ToolDurMs)
		return fmt.Sprintf("(%d 条: %dE %dW %dI %dH, %s)", n, e, w, i, h, dur)
	case "lsp_definition":
		n := parseLocationCount(p.ToolResult, "定义")
		if n == 0 {
			return "(未找到)"
		}
		dur := formatDuration(p.ToolDurMs)
		return fmt.Sprintf("(%d 个定义, %s)", n, dur)
	case "lsp_references":
		n := parseLocationCount(p.ToolResult, "引用")
		if n == 0 {
			return "(未找到)"
		}
		dur := formatDuration(p.ToolDurMs)
		return fmt.Sprintf("(%d 个引用, %s)", n, dur)
	case "lsp_hover":
		if strings.TrimSpace(p.ToolResult) == "" || strings.TrimSpace(p.ToolResult) == "无悬浮信息" {
			return "(无信息)"
		}
		size := formatBytes(len(p.ToolResult))
		dur := formatDuration(p.ToolDurMs)
		return fmt.Sprintf("(%s, %s)", size, dur)
	case "web_fetch":
		size := formatBytes(len(p.ToolResult))
		dur := formatDuration(p.ToolDurMs)
		return fmt.Sprintf("(%s, %s)", size, dur)
	default:
		dur := formatDuration(p.ToolDurMs)
		return fmt.Sprintf("(%s)", dur)
	}
}

// formatBytes 将字节数格式化为人类可读的字符串。
func formatBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// formatDuration 将毫秒格式化为人类可读的字符串。
func formatDuration(ms int64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	case ms < 3_600_000:
		s := (ms / 1000) % 60
		m := ms / 60_000
		return fmt.Sprintf("%dm%ds", m, s)
	default:
		m := (ms / 60_000) % 60
		h := ms / 3_600_000
		return fmt.Sprintf("%dh%dm", h, m)
	}
}

// formatTokens 将 token 数格式化为紧凑的人类可读形式。
//   0 → "0", 512 → "512", 3860 → "3.9K", 38600 → "38.6K", 1000000 → "1.0M"
func formatTokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 10_000:
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	case n < 1_000_000:
		v := float64(n) / 1000
		if v >= 100 {
			return fmt.Sprintf("%.0fK", v)
		}
		return fmt.Sprintf("%.1fK", v)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

// formatBalance 将余额信息格式化为单行紧凑显示。
// 优先展示 CNY 余额，若无则取首个币种；不支持时返回空字符串。
func formatBalance(balance *llm.BalanceInfo) string {
	if balance == nil || len(balance.BalanceInfos) == 0 {
		return ""
	}
	// 优先取 CNY
	var cb *llm.CurrencyBalance
	for i := range balance.BalanceInfos {
		if balance.BalanceInfos[i].Currency == "CNY" {
			cb = &balance.BalanceInfos[i]
			break
		}
	}
	if cb == nil {
		cb = &balance.BalanceInfos[0]
	}
	return fmt.Sprintf("%s %s", cb.Currency, cb.TotalBalance)
}

// countDiffLines 从工具输出中估算增删行数。
func countDiffLines(output string) (added, removed int) {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "+") && !strings.HasPrefix(trimmed, "+++") {
			added++
		} else if strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "---") {
			removed++
		}
	}
	return
}

// countMatches 从 grep 输出首行提取匹配数。
func countMatches(output string) int {
	n := parseHeaderCount(output)
	if n < 0 {
		return 0
	}
	return n
}

// countLines 统计输出中的条目数（从 header 提取，用于 search_file / ls）。
func countLines(output string) int {
	n := parseHeaderCount(output)
	if n < 0 {
		return 0
	}
	return n
}

// parseHeaderCount 从工具输出的 header 行中提取计数。
// 支持格式: "Found N match(es)/file(s) ..." 或 "Listed ... (N entries, ...)"
func parseHeaderCount(output string) int {
	firstLine := strings.SplitN(output, "\n", 2)[0]

	if strings.HasPrefix(firstLine, "Found ") {
		after := strings.TrimPrefix(firstLine, "Found ")
		numStr := takeDigits(after)
		if numStr != "" {
			return atoi(numStr)
		}
		return -1
	}

	if strings.HasPrefix(firstLine, "Listed ") {
		// "Listed /path (N entries, 1ms):"
		idx := strings.Index(firstLine, " entries")
		if idx < 0 {
			return -1
		}
		// 向左找 '('
		left := strings.LastIndex(firstLine[:idx], "(")
		if left < 0 {
			return -1
		}
		numStr := strings.TrimSpace(firstLine[left+1 : idx])
		return atoi(numStr)
	}

	return -1
}

// takeDigits 提取字符串开头的连续数字。
func takeDigits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// atoi 简易字符串转整数。
func atoi(s string) int {
	n := 0
	for _, ch := range s {
		n = n*10 + int(ch-'0')
	}
	return n
}

// parseExitCode 从 shell 输出首行提取退出码。
// 格式: "✅ Command succeeded (exit=0)  123ms" 或 "❌ Command failed (exit=1)  456ms"
func parseExitCode(output string) int {
	firstLine := strings.SplitN(output, "\n", 2)[0]
	idx := strings.Index(firstLine, "(exit=")
	if idx < 0 {
		return -1
	}
	rest := firstLine[idx+len("(exit="):]
	numStr := takeDigits(rest)
	if numStr == "" {
		return -1
	}
	return atoi(numStr)
}

// parseDiagnosticSummary 从 lsp_diagnostic 输出首行提取诊断计数。
// 格式: "N diagnostics (E errors, W warnings, I info, H hints)"
// 无诊断时为 "✓ No diagnostics"，返回 -1。
func parseDiagnosticSummary(output string) (total, errors, warnings, infos, hints int) {
	firstLine := strings.SplitN(output, "\n", 2)[0]
	if strings.Contains(firstLine, "No diagnostics") {
		return -1, 0, 0, 0, 0
	}
	// 提取总数
	total = -1
	if idx := strings.Index(firstLine, " diagnostics"); idx >= 0 {
		totalStr := takeDigits(strings.TrimSpace(firstLine[:idx]))
		if totalStr != "" {
			total = atoi(totalStr)
		}
	}
	// 提取分类计数
	errors = extractParenInt(firstLine, "errors")
	warnings = extractParenInt(firstLine, "warnings")
	infos = extractParenInt(firstLine, "info")
	hints = extractParenInt(firstLine, "hints")
	return
}

// extractParenInt 从字符串中提取 "N key" 格式中的数字。
func extractParenInt(s, key string) int {
	idx := strings.Index(s, key)
	if idx < 1 {
		return 0
	}
	// 向左找数字
	start := idx - 1
	for start >= 0 && s[start] >= '0' && s[start] <= '9' {
		start--
	}
	if start+1 < idx {
		return atoi(s[start+1 : idx])
	}
	return 0
}

// parseLocationCount 从 lsp_definition/lsp_references 输出首行提取位置数。
// 格式: "找到 N 个定义:\n" 或 "找到 N 个引用（仅显示前 100 条）:\n"
func parseLocationCount(output, kind string) int {
	firstLine := strings.SplitN(output, "\n", 2)[0]
	if strings.Contains(firstLine, "未找到") {
		return 0
	}
	prefix := "找到 "
	after := strings.TrimPrefix(firstLine, prefix)
	if after == firstLine {
		return 0
	}
	numStr := takeDigits(strings.TrimSpace(after))
	if numStr != "" {
		return atoi(numStr)
	}
	return 0
}

// parseWebFetchBody 从 web_fetch 输出中剥离元数据头部，返回纯正文。
// 头部格式: "Fetched <url>  HTTP <code>  <duration>\nContent-Type: ...\nSize: ...\n\n<body>"
func parseWebFetchBody(output string) string {
	idx := strings.Index(output, "\n\n")
	if idx < 0 {
		return output
	}
	return strings.TrimLeft(output[idx+2:], "\n")
}

// ---------------------------------------------------------------------------
// LSP / web_fetch 专用完整渲染
// ---------------------------------------------------------------------------

// renderLSPDiagnosticFull 渲染 lsp_diagnostic 的展开态输出。
// 首行摘要着色，后续条目按严重级别着色（error=红, warning=金, info=灰, hint=弱）。
func renderLSPDiagnosticFull(sb *strings.Builder, result string, textWidth int, indent string) {
	wrapped := 0
	lines := strings.Split(result, "\n")
	for i, line := range lines {
		styled := line
		if i == 0 {
			styled = styleHeaderAccent.Render(line)
		} else if strings.Contains(line, ": error:") {
			styled = styleToolPrefixErr.Render(line)
		} else if strings.Contains(line, ": warning:") {
			styled = styleFooterLatGold.Render(line)
		} else if strings.Contains(line, ": info:") {
			styled = styleMuted.Render(line)
		} else if strings.Contains(line, ": hint:") {
			styled = styleToolPreview.Render(line)
		}
		for _, wl := range wrapLine(styled, textWidth) {
			if wrapped >= maxExpandedWrapped {
				sb.WriteString(indent)
				sb.WriteString(styleToolPreviewHint.Render(fmt.Sprintf("... (truncated to %d lines)", maxExpandedWrapped)))
				sb.WriteString("\n")
				return
			}
			sb.WriteString(indent)
			sb.WriteString(wl)
			sb.WriteString("\n")
			wrapped++
		}
	}
}

// renderLSPLocationFull 渲染 lsp_definition / lsp_references 的展开态输出。
// 首行总结着色，后续条目中等亮度。
func renderLSPLocationFull(sb *strings.Builder, result string, textWidth int, indent string) {
	wrapped := 0
	lines := strings.Split(result, "\n")
	for i, line := range lines {
		styled := line
		if i == 0 {
			styled = styleHeaderAccent.Render(line)
		}
		for _, wl := range wrapLine(styled, textWidth) {
			if wrapped >= maxExpandedWrapped {
				sb.WriteString(indent)
				sb.WriteString(styleToolPreviewHint.Render(fmt.Sprintf("... (truncated to %d lines)", maxExpandedWrapped)))
				sb.WriteString("\n")
				return
			}
			sb.WriteString(indent)
			sb.WriteString(wl)
			sb.WriteString("\n")
			wrapped++
		}
	}
}

// renderLSPHoverFull 渲染 lsp_hover 的展开态输出（Markdown 类型签名 + 文档）。
func renderLSPHoverFull(sb *strings.Builder, result string, textWidth int, indent string) {
	wrapped := 0
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		for _, wl := range wrapLine(line, textWidth) {
			if wrapped >= maxExpandedWrapped {
				sb.WriteString(indent)
				sb.WriteString(styleToolPreviewHint.Render(fmt.Sprintf("... (truncated to %d lines)", maxExpandedWrapped)))
				sb.WriteString("\n")
				return
			}
			sb.WriteString(indent)
			sb.WriteString(styleToolExpanded.Render(wl))
			sb.WriteString("\n")
			wrapped++
		}
	}
}

// renderWebFetchFull 渲染 web_fetch 的展开态输出。头部元数据着色，正文默认样式。
func renderWebFetchFull(sb *strings.Builder, result string, textWidth int, indent string) {
	wrapped := 0

	// 分离头部与正文
	parts := strings.SplitN(result, "\n\n", 2)
	headerLines := strings.Split(parts[0], "\n")
	for _, line := range headerLines {
		styled := styleHeaderAccent.Render(line)
		for _, wl := range wrapLine(styled, textWidth) {
			if wrapped >= maxExpandedWrapped {
				goto truncate
			}
			sb.WriteString(indent)
			sb.WriteString(wl)
			sb.WriteString("\n")
			wrapped++
		}
	}
	// 空行分隔
	sb.WriteString(indent)
	sb.WriteString("\n")
	// 正文
	if len(parts) > 1 {
		body := strings.TrimSpace(parts[1])
		bodyLines := strings.Split(body, "\n")
		for _, line := range bodyLines {
			for _, wl := range wrapLine(line, textWidth) {
				if wrapped >= maxExpandedWrapped {
					goto truncate
				}
				sb.WriteString(indent)
				sb.WriteString(wl)
				sb.WriteString("\n")
				wrapped++
			}
		}
	}
	return

truncate:
	sb.WriteString(indent)
	sb.WriteString(styleToolPreviewHint.Render(fmt.Sprintf("... (truncated to %d lines)", maxExpandedWrapped)))
	sb.WriteString("\n")
}

// ---------------------------------------------------------------------------
// Markdown 行类型
// ---------------------------------------------------------------------------
// Viewport 内容构建
// ---------------------------------------------------------------------------

// ViewportCtx 聚合 viewport 渲染所需的上下文。
type ViewportCtx struct {
	Asst     spinner.Model
	Thought  spinner.Model
	Tool     spinner.Model
	Glamour  *glamour.TermRenderer // nil 时回退到纯文本
	Width    int                   // viewport 内容宽度（终端宽度 - 4）
	Focused  bool                  // 当前段落是否处于焦点态
}

// renderSingleParagraph 渲染单个段落到行数组，用于 spliceLastParagraph 增量更新。
// 不写入段落级缓存（流式段落缓存无意义），调用方负责将结果拼接到 viewport 缓存。
func renderSingleParagraph(p *Paragraph, ctx ViewportCtx) []string {
	var sb strings.Builder
	switch p.Type {
	case paraUser:
		renderUserPara(&sb, p, ctx)
	case paraAssistant:
		renderAssistantPara(&sb, p, ctx)
	case paraThought:
		renderThoughtPara(&sb, p, ctx)
	case paraTool:
		renderToolPara(&sb, p, ctx)
	case paraSystem:
		renderSystemPara(&sb, p, ctx)
	}
	return strings.Split(sb.String(), "\n")
}

// buildViewportContent 从段落列表重建 viewport 的全部文本行，同时返回每段的起始行号。
// 对未变更（!renderDirty）且宽度匹配的段落复用缓存渲染，大幅降低流式刷新开销。
// 返回 []string 而非单一字符串，调用方应使用 SetContentLines 避免不必要的 Split。
// lineHint 用于预分配 lines 容量（通常传上次缓存的 len），避免 append 多次扩容。
func buildViewportContent(paras []Paragraph, ctx ViewportCtx, focusIndex int, lineHint int) (lines []string, lineStarts []int) {
	lineStarts = make([]int, len(paras))

	// 预分配：优先用 hint，否则用段落数 × 5 粗略估算
	capHint := lineHint
	if capHint < len(paras)*5 {
		capHint = len(paras) * 5
	}
	lines = make([]string, 0, capHint)

	currentLine := 0

	for i := range paras {
		p := &paras[i]
		lineStarts[i] = currentLine

		// 设置当前段落的焦点状态
		ctx.Focused = (i == focusIndex)

		// 尝试复用段落级渲染缓存（行数组形式，零分配）
		if !p.renderDirty && p.cachedLines != nil && p.cachedWidth == ctx.Width && p.cachedFocused == ctx.Focused {
			lines = append(lines, p.cachedLines...)
			currentLine += len(p.cachedLines)
			continue
		}

		// 渲染到临时 buffer；段落间保留一个空行作为间距
		var tmp strings.Builder
		switch p.Type {
		case paraUser:
			renderUserPara(&tmp, p, ctx)
		case paraAssistant:
			renderAssistantPara(&tmp, p, ctx)
		case paraThought:
			renderThoughtPara(&tmp, p, ctx)
		case paraTool:
			renderToolPara(&tmp, p, ctx)
		case paraSystem:
			renderSystemPara(&tmp, p, ctx)
		}

		rendered := tmp.String()
		renderedLines := strings.Split(rendered, "\n")

		// 仅对非流式段落写入缓存（流式段落内容频繁变化，缓存无意义）
		if p.State != stateStreaming {
			p.cachedLines = renderedLines
			p.cachedWidth = ctx.Width
			p.cachedFocused = ctx.Focused
			p.renderDirty = false
		}

		lines = append(lines, renderedLines...)
		currentLine += len(renderedLines)
	}

	return lines, lineStarts
}

// renderSystemPara 渲染系统提示段落。
// 前缀和文字按 NotifKind 着色：完成/中断 → gray，警告 → amber gold，错误 → red。
func renderSystemPara(sb *strings.Builder, p *Paragraph, ctx ViewportCtx) {
	prefixStr := systemPrefix(p.NotifKind) + " "
	prefixWidth := lipgloss.Width(prefixStr)
	indentStr := strings.Repeat(" ", prefixWidth)
	textWidth := ctx.Width - prefixWidth
	if textWidth < 1 {
		textWidth = 1
	}

	textStyle := systemTextStyle(p.NotifKind)

	lines := strings.Split(p.Text, "\n")
	for i, line := range lines {
		wrapped := wrapLine(line, textWidth)
		for j, wl := range wrapped {
			if i == 0 && j == 0 {
				sb.WriteString(prefixStr)
			} else {
				sb.WriteString(indentStr)
			}
			sb.WriteString(textStyle.Render(wl))
			sb.WriteString("\n")
		}
	}
}

// renderUserPara 渲染用户消息段落。
func renderUserPara(sb *strings.Builder, p *Paragraph, ctx ViewportCtx) {
	// 前缀对齐 thought/assistant/tool 模式：› + 空格，仅首行
	prefixStr := userPrefix() + " "
	prefixWidth := lipgloss.Width(prefixStr)
	indentStr := strings.Repeat(" ", prefixWidth)
	textWidth := ctx.Width - prefixWidth
	if textWidth < 1 {
		textWidth = 1
	}

	lines := strings.Split(p.Text, "\n")
	for _, line := range lines {
		wrapped := wrapLine(line, textWidth)
		for i, wl := range wrapped {
			if i == 0 {
				sb.WriteString(prefixStr)
			} else {
				sb.WriteString(indentStr)
			}
			sb.WriteString(wl)
			sb.WriteString("\n")
		}
	}
}

// renderAssistantPara 渲染 assistant 回复段落（Glamour markdown 渲染）。
func renderAssistantPara(sb *strings.Builder, p *Paragraph, ctx ViewportCtx) {
	streaming := p.State == stateStreaming
	prefix := asstPrefix(ctx.Asst, streaming)

	// 计算缩进：前缀 + 空格，与 thought 逻辑统一
	prefixStr := ""
	if prefix != "" {
		prefixStr = prefix + " "
	}
	prefixWidth := lipgloss.Width(prefixStr)
	indent := strings.Repeat(" ", prefixWidth)

	if p.Text == "" {
		sb.WriteString(prefixStr)
		sb.WriteString("\n")
		return
	}

	// 流式输出中跳过 Glamour（markdown 结构不完整，渲染无意义且极慢）；
	// 仅在 done 时使用 Glamour，并缓存结果避免重复渲染。
	rendered := p.Text
	if !streaming && ctx.Glamour != nil {
		if p.renderedCache != "" && p.cacheWidth == ctx.Width {
			rendered = p.renderedCache
		} else {
			if out, err := ctx.Glamour.Render(p.Text); err == nil {
				rendered = out
				p.renderedCache = out
				p.cacheWidth = ctx.Width
			}
		}
	}

	// Glamour 输出前后可能带有多余换行，统一去除
	rendered = strings.Trim(rendered, "\n")

	// 归一化连续空行：Glamour 在不同 block 元素间输出数量不等的空行，
	// 直接透传会导致 viewport 中行距忽大忽小。压缩为最多 1 个空行。
	rendered = collapseBlankLines(rendered)

	lines := strings.Split(rendered, "\n")
	firstLine := true
	for _, line := range lines {
		if firstLine {
			sb.WriteString(prefixStr)
			firstLine = false
		} else {
			sb.WriteString(indent)
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}
}



// renderThoughtPara 渲染 thought 段落（斜体灰色，流式时有 spinner 前缀，通过字体样式与正文区分）。
func renderThoughtPara(sb *strings.Builder, p *Paragraph, ctx ViewportCtx) {
	// 前缀：流式时 spinner 动画，done 时静态灰色 ·，始终保持锚点宽度
	streaming := p.State == stateStreaming
	prefix := thoughtPrefix(ctx.Thought, streaming)
	if ctx.Focused {
		prefix = styleFocusIndicator.Render(prefix)
	}
	prefixStr := prefix + " "
	prefixWidth := lipgloss.Width(prefixStr)
	indentStr := strings.Repeat(" ", prefixWidth)
	textWidth := ctx.Width - prefixWidth
	if textWidth < 1 {
		textWidth = 1
	}

	switch p.State {
	case stateStreaming:
		// 固定 3 行高度刷新：从文本尾部反向采集足够原始行来产生 3 个换行行，
		// 避免对全文 split+wrap（文本随流式增长，此优化将开销从 O(n) 降为 O(1)）。
		const fixedLines = 3

		rawLines := strings.Split(p.Text, "\n")
		var visible []string
		// 从尾部反向收集，直到获得至少 fixedLines 个换行行
		for i := len(rawLines) - 1; i >= 0 && len(visible) < fixedLines; i-- {
			wrapped := wrapLine(rawLines[i], textWidth)
			// 反向插入：从后往前处理的原始行，其换行结果需要放在已收集行之前
			visible = append(wrapped, visible...)
		}
		// 超出 fixedLines 时从头部截断
		if len(visible) > fixedLines {
			visible = visible[len(visible)-fixedLines:]
		}

		// 输出固定 fixedLines 行
		for i := 0; i < fixedLines; i++ {
			if i < len(visible) {
				// 始终保留 spinner/前缀；截断提示 … 放在内容开头
				pfx := indentStr
				content := visible[i]
				if i == 0 {
					pfx = prefixStr
				}
				sb.WriteString(pfx)
				sb.WriteString(styleThoughtStreaming.Render(content))
			} else if i == 0 && len(visible) == 0 {
				// 尚无思考内容，首行显示提示
				sb.WriteString(prefixStr)
				sb.WriteString(styleThoughtStreaming.Render("思考中..."))
			} else {
				// 补齐空行，保持前缀缩进对齐
				if i == 0 {
					sb.WriteString(prefixStr)
				} else {
					sb.WriteString(indentStr)
				}
			}
			sb.WriteString("\n")
		}

	case stateCollapsed:
		// 截取前 2 行内容 + 展开按钮；仅首行显示 · 前缀
		lines := strings.Split(p.Text, "\n")
		var visible []string
		remaining := 2
		for _, line := range lines {
			wrapped := wrapLine(line, textWidth)
			for _, wl := range wrapped {
				if remaining <= 0 {
					break
				}
				visible = append(visible, wl)
				remaining--
			}
			if remaining <= 0 {
				break
			}
		}

		if len(visible) == 0 {
			sb.WriteString(prefixStr)
			sb.WriteString(styleThoughtCollapsed.Render(
				fmt.Sprintf("▶ 思考完成 (%d tokens) · Enter 展开", p.ThoughtTokens)))
			sb.WriteString("\n")
			return
		}

		for i, wl := range visible {
			if i == 0 {
				sb.WriteString(prefixStr)
			} else {
				sb.WriteString(indentStr)
			}
			sb.WriteString(styleThoughtCollapsed.Render(wl))
			sb.WriteString("\n")
		}

		// 如果总行数超过 2，显示展开按钮
		totalWrapped := 0
		for _, line := range lines {
			totalWrapped += len(wrapLine(line, textWidth))
		}
		if totalWrapped > 2 {
			sb.WriteString(indentStr)
			sb.WriteString(styleThoughtExpandHint.Render(
				fmt.Sprintf("··· Enter 展开 (%d tokens)", p.ThoughtTokens)))
			sb.WriteString("\n")
		}

	case stateExpanded:
		// 完整内容，斜体暗灰；仅首行显示 · 前缀
		first := true
		for _, line := range strings.Split(p.Text, "\n") {
			wrapped := wrapLine(line, textWidth)
			for _, wl := range wrapped {
				if first {
					sb.WriteString(prefixStr)
					first = false
				} else {
					sb.WriteString(indentStr)
				}
				sb.WriteString(styleThoughtContent.Render(wl))
				sb.WriteString("\n")
			}
		}
		// 折叠提示
		sb.WriteString(indentStr)
		sb.WriteString(styleThoughtExpandHint.Render("▼ Enter 折叠"))
		sb.WriteString("\n")

	default:
		// fallback: 完整内容渲染（兼容旧 stateDone 等状态）
		first := true
		for _, line := range strings.Split(p.Text, "\n") {
			wrapped := wrapLine(line, textWidth)
			for _, wl := range wrapped {
				if first {
					sb.WriteString(prefixStr)
					first = false
				} else {
					sb.WriteString(indentStr)
				}
				sb.WriteString(styleThoughtContent.Render(wl))
				sb.WriteString("\n")
			}
		}
	}
}



// wrapLine 按 maxWidth 列宽对单行文本智能换行，优先在空格处断行。
// 能正确处理 ANSI 转义序列（\x1b[...m），将其视为 0 宽度且不在中间撕裂。
func wrapLine(line string, maxWidth int) []string {
	if maxWidth <= 0 || displayWidth(line) <= maxWidth {
		return []string{line}
	}

	var result []string
	runes := []rune(line)

	for len(runes) > 0 {
		w := 0
		cut := 0
		lastSpace := -1

		// 先跳过行首的 ANSI 序列（宽度 0），避免它们在断行处被撕裂
		ansiHeadLen := skipAnsiSequence(runes)
		cut = ansiHeadLen

		for i := ansiHeadLen; i < len(runes); {
			r := runes[i]

			// 内嵌 ANSI 转义序列整体跳过，零宽度且不可断
			if r == 0x1b && i+1 < len(runes) && runes[i+1] == '[' {
				skipLen := skipAnsiSequence(runes[i:])
				i += skipLen
				continue
			}

			rw := 1
			if r >= 128 {
				rw = lipgloss.Width(string(r))
			}
			if w+rw > maxWidth {
				break
			}
			w += rw
			cut = i + 1
			if r == ' ' {
				lastSpace = i
			}
			i++
		}

		if cut == 0 {
			// 单字符也放不下 → 强制断在第一个 rune
			cut = 1
		}

		if lastSpace > 0 && lastSpace >= cut*3/4 {
			cut = lastSpace
		}

		result = append(result, string(runes[:cut]))

		for cut < len(runes) && runes[cut] == ' ' {
			cut++
		}
		runes = runes[cut:]
	}

	if len(result) == 0 {
		result = append(result, "")
	}
	return result
}

// skipAnsiSequence 返回从 runes[0] 开始的 ANSI 转义序列长度。
// 支持 CSI（ESC [）、OSC（ESC ]）、DCS（ESC P）等序列。
// 若不以 ESC 开头则返回 0。
func skipAnsiSequence(runes []rune) int {
	if len(runes) < 2 || runes[0] != 0x1b {
		return 0
	}

	switch runes[1] {
	case '[': // CSI: ESC [ 参数字节(0x30-0x3F)/中间字节(0x20-0x2F) 终字节(0x40-0x7E)
		end := 2
		for end < len(runes) && (runes[end] < 0x40 || runes[end] > 0x7E) {
			end++
		}
		if end < len(runes) {
			end++ // 包含终字节
		}
		return end

	case ']', 'P': // OSC (ESC ]) / DCS (ESC P): 终止于 BEL (0x07) 或 ST (ESC \)
		end := 2
		for end < len(runes) {
			if runes[end] == 0x07 { // BEL
				return end + 1
			}
			if runes[end] == 0x1b && end+1 < len(runes) && runes[end+1] == '\\' { // ST
				return end + 2
			}
			end++
		}
		return len(runes) // 未终止，吞掉剩余全部

	default: // 其他 ESC 序列（如 ESC 7, ESC c）：ESC + 1 字符
		return 2
	}
}

// displayWidth 计算字符串的终端显示宽度，ANSI 转义序列计为 0 宽度。
// 对于 ASCII 字符（<128）直接返回宽度 1 避免 string(r) 分配和 lipgloss.Width 调用；
// 仅非 ASCII 字符回退到 lipgloss.Width，覆盖 CJK 全角字符等场景。
func displayWidth(s string) int {
	w := 0
	runes := []rune(s)
	for i := 0; i < len(runes); {
		r := runes[i]

		// ANSI 转义序列 → 宽度 0
		if skipLen := skipAnsiSequence(runes[i:]); skipLen > 0 {
			i += skipLen
			continue
		}

		if r < 128 {
			w++
		} else {
			w += lipgloss.Width(string(r))
		}
		i++
	}
	return w
}

// stripToolStatusHeader 去除 tool 结果首行的状态标题（如 "✅ Command succeeded (exit=0) 123ms"），
// 并清理尾部空行。摘要行已有 toolName + toolSuffix，颜色已传达成功/失败，无需重复。
func stripToolStatusHeader(result string) string {
	lines := strings.Split(result, "\n")
	start := 0
	// 跳过首行中 "✅" 或 "❌" 开头的状态标题
	if len(lines) > 0 {
		first := strings.TrimSpace(lines[0])
		if strings.HasPrefix(first, "\u2705") || strings.HasPrefix(first, "\u274c") {
			start = 1
		}
	}
	// 去除尾部空行
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}

// renderToolPara 渲染 tool 段落。前缀对齐 thought/assistant 模式。
func renderToolPara(sb *strings.Builder, p *Paragraph, ctx ViewportCtx) {
	toolState := p.State
	if p.ToolError != "" || p.ToolDenied {
		toolState = stateError
	}

	prefix := toolPrefix(ctx.Tool, toolState)
	if ctx.Focused {
		prefix = styleFocusIndicator.Render(prefix)
	}
	prefixStr := prefix + " "
	prefixWidth := lipgloss.Width(prefixStr)
	indentStr := strings.Repeat(" ", prefixWidth)
	textWidth := ctx.Width - prefixWidth
	if textWidth < 1 {
		textWidth = 1
	}

	// 摘要行：仅首行有 prefixStr，后续内容行用 indentStr 对齐
	sb.WriteString(prefixStr)

	// tool 名颜色：成功绿色，失败红色
	toolNameStyle := styleToolPrefixDone
	if toolState == stateError {
		toolNameStyle = styleToolPrefixErr
	}

	// 构造摘要行并做宽度自适应截断
	toolNameRendered := toolNameStyle.Render(p.ToolName)
	suffixRendered := toolSuffix(p)
	fixedWidth := lipgloss.Width(toolNameRendered) + lipgloss.Width("  ") + lipgloss.Width("  ") + lipgloss.Width(suffixRendered)
	maxArgsWidth := textWidth - fixedWidth
	if maxArgsWidth < 4 {
		maxArgsWidth = 4
	}
	argsDisplay := p.ToolArgs
	argsRunes := []rune(argsDisplay)
	if len(argsRunes) > maxArgsWidth {
		argsDisplay = string(argsRunes[:maxArgsWidth-1]) + "…"
	}
	sb.WriteString(toolNameRendered)
	sb.WriteString("  ")
	sb.WriteString(styleToolArgs.Render(argsDisplay))
	sb.WriteString("  ")
	sb.WriteString(suffixRendered)
	sb.WriteString("\n")

	// 完成/错误态的折叠预览
	if p.State == stateDone || p.State == stateError {
		if p.State == stateCollapsed || p.State == stateDone {
			if p.DiffHunks != nil {
				renderDiffPreview(sb, p.DiffHunks, textWidth, indentStr)
			} else {
				renderToolPreview(sb, p, textWidth, indentStr)
			}
		}
	}

	// 展开态 —— 显示完整输出
	if p.State == stateExpanded {
		if p.DiffHunks != nil {
			renderDiffView(sb, p.DiffHunks, textWidth, indentStr)
		} else {
			renderToolFullOutput(sb, p, textWidth, indentStr)
		}
	}
}

// maxPreviewWrapped 是折叠预览的最大包装后行数。限制 wrapLine 膨胀后的实际显示行数，
// 防止单条超长行（如 100KB 无换行 JSON）撑满折叠预览。
const maxPreviewWrapped = 5

// renderToolPreview 渲染工具输出的默认预览行（折叠态）。indent 由上层传入以对齐摘要行前缀。
func renderToolPreview(sb *strings.Builder, p *Paragraph, textWidth int, indent string) {
	result := stripToolStatusHeader(p.ToolResult)
	if result == "" {
		return
	}

	// "│ " 前缀占 2 列
	contentWidth := textWidth - 2
	if contentWidth < 1 {
		contentWidth = 1
	}

	// writeWrappedPreview 将 line 包装后写入 sb，受 wrapped 计数器限制。
	// 返回是否因达到上限而截断。
	writeWrappedPreview := func(line string, lineStyle lipgloss.Style, wrapped *int) (truncated bool) {
		for _, wl := range wrapLine(line, contentWidth) {
			if *wrapped >= maxPreviewWrapped {
				return true
			}
			sb.WriteString(indent)
			sb.WriteString(lineStyle.Render("│ " + wl))
			sb.WriteString("\n")
			*wrapped++
		}
		return false
	}

	wrapped := 0
	truncated := false

	switch p.ToolName {
	case "write_file", "edit_file":
		lines := strings.Split(result, "\n")
		for _, line := range lines {
			lineStyle := styleToolPreview
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				lineStyle = styleDiffAdd
			} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				lineStyle = styleDiffDel
			}
			if writeWrappedPreview(line, lineStyle, &wrapped) {
				truncated = true
				break
			}
		}

	case "shell":
		lines := strings.Split(result, "\n")
		for _, line := range lines {
			line = strings.TrimLeft(line, " ")
			if writeWrappedPreview(line, styleToolPreview, &wrapped) {
				truncated = true
				break
			}
		}

	// 仅 read_file, grep, search_file, ls — 不显示预览
	case "lsp_diagnostic":
		lines := strings.Split(result, "\n")
		start := 1
		if len(lines) > 0 && strings.HasPrefix(lines[0], "诊断结果") {
			start = 1
		}
		for _, line := range lines[start:] {
			if line == "" {
				continue
			}
			if writeWrappedPreview(line, styleToolPreview, &wrapped) {
				truncated = true
				break
			}
		}
	case "lsp_definition", "lsp_references":
		lines := strings.Split(result, "\n")
		start := 0
		for i, line := range lines {
			if strings.HasPrefix(line, "找到 ") {
				start = i + 1
				continue
			}
			if i >= start && strings.TrimSpace(line) != "" {
				if writeWrappedPreview(line, styleToolPreview, &wrapped) {
					truncated = true
					break
				}
			}
		}
	case "lsp_hover":
		lines := strings.Split(result, "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			if writeWrappedPreview(line, styleToolPreview, &wrapped) {
				truncated = true
				break
			}
		}
	case "web_fetch":
		body := parseWebFetchBody(result)
		lines := strings.Split(body, "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			if writeWrappedPreview(line, styleToolPreview, &wrapped) {
				truncated = true
				break
			}
		}
	default:
		// 无预览
	}

	if truncated {
		sb.WriteString(indent)
		// 仅可段落聚焦的工具展示 Enter 提示（shell / web_fetch），
		// write_file / edit_file 等不可聚焦的工具仅显示截断标记。
		switch p.ToolName {
		case "shell", "web_fetch":
			sb.WriteString(styleToolPreviewHint.Render("··· Enter 展开全部"))
		default:
			sb.WriteString(styleToolPreviewHint.Render("··· (truncated)"))
		}
		sb.WriteString("\n")
	}
}


// maxExpandedWrapped 是展开态的最大包装后行数。防止单条超长行在展开时产生海量 viewport 行。
const maxExpandedWrapped = 2000

// renderToolFullOutput 渲染工具的完整输出（展开态）。indent 由上层传入以对齐摘要行前缀。
func renderToolFullOutput(sb *strings.Builder, p *Paragraph, textWidth int, indent string) {
	if textWidth < 1 {
		textWidth = 1
	}

	result := stripToolStatusHeader(p.ToolResult)
	if result == "" {
		return
	}

	wrapped := 0
	truncated := false

	switch p.ToolName {
	case "read_file":
		codeTextWidth := textWidth - 9
		if codeTextWidth < 1 {
			codeTextWidth = 1
		}
		lines := strings.Split(result, "\n")
		for i, line := range lines {
			lineNum := styleMuted.Render(fmt.Sprintf("%4d │", i+1))
			wlines := wrapLine(line, codeTextWidth)
			for j, wl := range wlines {
				if wrapped >= maxExpandedWrapped {
					truncated = true
					break
				}
				sb.WriteString(indent)
				if j == 0 {
					sb.WriteString(lineNum)
				} else {
					sb.WriteString(styleMuted.Render("     │"))
				}
				sb.WriteString(" ")
				sb.WriteString(styleMDCode.Render(wl))
				sb.WriteString("\n")
				wrapped++
			}
			if truncated {
				break
			}
		}

	case "write_file", "edit_file":
		for _, line := range strings.Split(result, "\n") {
			wlines := wrapLine(line, textWidth)
			for _, wl := range wlines {
				if wrapped >= maxExpandedWrapped {
					truncated = true
					break
				}
				sb.WriteString(indent)
				if strings.HasPrefix(wl, "@@") {
					sb.WriteString(styleHeaderAccent.Render(wl))
				} else if strings.HasPrefix(wl, "+") && !strings.HasPrefix(wl, "+++") {
					sb.WriteString(styleDiffAdd.Render(wl))
				} else if strings.HasPrefix(wl, "-") && !strings.HasPrefix(wl, "---") {
					sb.WriteString(styleDiffDel.Render(wl))
				} else {
					sb.WriteString(styleToolExpanded.Render(wl))
				}
				sb.WriteString("\n")
				wrapped++
			}
			if truncated {
				break
			}
		}

	case "shell":
		rawLines := strings.Split(result, "\n")
		for _, line := range rawLines {
			line = strings.TrimLeft(line, " ")
			wlines := wrapLine(line, textWidth)
			for _, wl := range wlines {
				if wrapped >= maxExpandedWrapped {
					truncated = true
					break
				}
				sb.WriteString(indent)
				sb.WriteString(wl)
				sb.WriteString("\n")
				wrapped++
			}
			if truncated {
				break
			}
		}

	case "grep", "search_file", "ls":

	case "lsp_diagnostic":
		renderLSPDiagnosticFull(sb, result, textWidth, indent)
		return
	case "lsp_definition", "lsp_references":
		renderLSPLocationFull(sb, result, textWidth, indent)
		return
	case "lsp_hover":
		renderLSPHoverFull(sb, result, textWidth, indent)
		return
	case "web_fetch":
		renderWebFetchFull(sb, result, textWidth, indent)
		return

	default:
		for _, line := range strings.Split(result, "\n") {
			wlines := wrapLine(line, textWidth)
			for _, wl := range wlines {
				if wrapped >= maxExpandedWrapped {
					truncated = true
					break
				}
				sb.WriteString(indent)
				sb.WriteString(wl)
				sb.WriteString("\n")
				wrapped++
			}
			if truncated {
				break
			}
		}
	}

	if truncated {
		sb.WriteString(indent)
		sb.WriteString(styleToolPreviewHint.Render(fmt.Sprintf("... (truncated to %d lines)", maxExpandedWrapped)))
		sb.WriteString("\n")
	}

	// 折叠提示 — 仅可段落聚焦的工具（shell / web_fetch）展示 Enter 提示
	switch p.ToolName {
	case "shell", "web_fetch":
		sb.WriteString(indent)
		sb.WriteString(styleToolPreviewHint.Render("▼ Enter 折叠"))
		sb.WriteString("\n")
	}
}

// renderDiffPreview 渲染 diff 的折叠预览。受 maxPreviewWrapped 约束，
// 防止单条超长行撑满预览。edit_file 不参与段落聚焦，截断时仅显示标记。
func renderDiffPreview(sb *strings.Builder, hunks []tool.DiffHunk, textWidth int, indent string) {
	if len(hunks) == 0 {
		return
	}

	contentWidth := textWidth - 2
	if contentWidth < 1 {
		contentWidth = 1
	}

	wrapped := 0
	truncated := false
	for _, h := range hunks {
		for _, l := range h.Lines {
			style := lineStyle(l.Kind)
			prefix := linePrefix(l.Kind)
			line := prefix + l.Content
			for _, wl := range wrapLine(line, contentWidth) {
				if wrapped >= maxPreviewWrapped {
					truncated = true
					break
				}
				sb.WriteString(indent)
				sb.WriteString(style.Render("│ " + wl))
				sb.WriteString("\n")
				wrapped++
			}
			if truncated {
				break
			}
		}
		if truncated {
			break
		}
	}
	if truncated {
		sb.WriteString(indent)
		sb.WriteString(styleToolPreviewHint.Render("··· (truncated)"))
		sb.WriteString("\n")
	}
}

// renderDiffView 渲染完整的统一 diff 视图（展开态），带行号列和背景着色。
// 受 maxExpandedWrapped 约束，防止超长行导致海量输出。
func renderDiffView(sb *strings.Builder, hunks []tool.DiffHunk, textWidth int, indent string) {
	if len(hunks) == 0 {
		return
	}

	// 计算行号列宽度（最多 4 位）
	numWidth := 4
	for _, h := range hunks {
		maxNum := h.OldStart + h.OldCount
		if n := h.NewStart + h.NewCount; n > maxNum {
			maxNum = n
		}
		if w := digits(maxNum); w > numWidth {
			numWidth = w
		}
	}

	wrapped := 0
	truncated := false

	for hi, h := range hunks {
		// hunk 之间空行分隔，第一个前不加空行
		if hi > 0 {
			sb.WriteString(indent)
			sb.WriteString(styleMuted.Render(strings.Repeat("─", textWidth)))
			sb.WriteString("\n")
		}

		// @@ header
		header := fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
		if h.Heading != "" {
			header += " " + h.Heading
		}
		sb.WriteString(indent)
		sb.WriteString(styleDiffHeader.Render(header))
		sb.WriteString("\n")

		for _, l := range h.Lines {
			prefix, styleContent := diffLinePrefixAndStyle(l.Kind)
			codeWidth := textWidth - numWidth - 4 // 行号 + "  " + 前缀（2）
			if codeWidth < 1 {
				codeWidth = 1
			}
			wlines := wrapLine(l.Content, codeWidth)
			for wi, wl := range wlines {
				if wrapped >= maxExpandedWrapped {
					truncated = true
					break
				}
				sb.WriteString(indent)
				if wi == 0 {
					// 首行：行号 + 前缀 + 内容
					numStr := ""
					switch l.Kind {
					case tool.DiffDel:
						numStr = fmt.Sprintf("%*d  ", numWidth, l.OldNum)
					case tool.DiffAdd:
						numStr = fmt.Sprintf("%*d  ", numWidth, l.NewNum)
					case tool.DiffCtx:
						numStr = fmt.Sprintf("%*d  ", numWidth, l.OldNum)
					default:
						numStr = fmt.Sprintf("%*s  ", numWidth, "")
					}
					sb.WriteString(styleLineNum.Render(numStr))
					sb.WriteString(styleContent.Render(prefix + wl))
				} else {
					// 续行：空行号 + 对齐空格 + 内容
					emptyNum := styleLineNum.Render(fmt.Sprintf("%*s  ", numWidth, ""))
					sb.WriteString(emptyNum)
					sb.WriteString(styleContent.Render("  " + wl))
				}
				sb.WriteString("\n")
				wrapped++
			}
			if truncated {
				break
			}
		}
		if truncated {
			break
		}
	}

	if truncated {
		sb.WriteString(indent)
		sb.WriteString(styleToolPreviewHint.Render(fmt.Sprintf("... (truncated to %d lines)", maxExpandedWrapped)))
		sb.WriteString("\n")
	}

}

// lineStyle 根据 DiffLineKind 返回对应的前景色样式。
func lineStyle(kind tool.DiffLineKind) lipgloss.Style {
	switch kind {
	case tool.DiffAdd:
		return styleDiffAdd
	case tool.DiffDel:
		return styleDiffDel
	case tool.DiffHeader:
		return styleDiffHeader
	default:
		return styleToolPreview
	}
}

// linePrefix 根据 DiffLineKind 返回对应的前缀字符。
func linePrefix(kind tool.DiffLineKind) string {
	switch kind {
	case tool.DiffAdd:
		return "+ "
	case tool.DiffDel:
		return "- "
	case tool.DiffHeader:
		return "@@ "
	default:
		return "  "
	}
}

// diffLinePrefixAndStyle 根据 DiffLineKind 返回首行前缀和续行样式。
func diffLinePrefixAndStyle(kind tool.DiffLineKind) (prefix string, style lipgloss.Style) {
	switch kind {
	case tool.DiffAdd:
		return "+ ", styleDiffAddBG
	case tool.DiffDel:
		return "- ", styleDiffDelBG
	case tool.DiffCtx:
		return "  ", styleDiffCtx
	default:
		return "  ", styleToolExpanded
	}
}

// digits 返回 n 的十进制位数。
func digits(n int) int {
	if n <= 0 {
		return 1
	}
	d := 0
	for n > 0 {
		n /= 10
		d++
	}
	return d
}

// ---------------------------------------------------------------------------
// 辅助：检测字符串内容类型
// ---------------------------------------------------------------------------

// isDiffContent 判断工具输出是否看起来像 unified diff。
func isDiffContent(s string) bool {
	return strings.Contains(s, "\n--- ") || strings.Contains(s, "\n+++ ") ||
		strings.Contains(s, "\n@@ ") || strings.Contains(s, "\ndiff --git ")
}

// indentStr 给多行文本的每一行加上缩进前缀。
func indentStr(s, indent string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

// collapseBlankLines 将 2 个以上连续换行压缩为最多 1 个空行（即 \n\n\n+ → \n\n）。
// 用于归一化 Glamour 在不同 block 元素间输出的不等量空行。
func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

// ---------------------------------------------------------------------------
// 格式辅助函数
// ---------------------------------------------------------------------------
