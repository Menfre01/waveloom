package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/pathutil"
	"github.com/Menfre01/waveloom/pkg/todo"
	"github.com/Menfre01/waveloom/pkg/tool"

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
	paraSubagent                       // ◆ 子 agent 容器
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
	ToolName      string
	ToolArgs      string // 格式化后的参数摘要
	ToolResult    string // 完整输出（展开时显示）
	ToolError     string // 错误信息
	ToolErrorKind string // 错误分类（如 timeout、command_failed 等）
	ToolDurMs     int64  // 执行耗时（毫秒）
	ToolDenied    bool   // 权限被拒
	ToolFatal     bool   // 错误是否致命，TUI 据此区分红（fatal）/金（recoverable）样式
	DiffHunks  []tool.DiffHunk // edit_file 结构化 diff（nil = 不适用或纯文本回退）

	// Thought 专用字段
	ThoughtTokens int // 完成后的 token 数

	// System 专用字段
	NotifKind systemNotifKind // 通知类型（仅 paraSystem 有效）

	// Subagent 专用字段
	SubagentType      string // "fork" | "general-purpose" | "Explore"
	SubagentPrompt    string // 委派任务描述
	SubagentTurns     int    // 总轮次
	SubagentPromptTok int    // ↑ 输入 token
	SubagentComplTok  int    // ↓ 输出 token

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
	case "bash":
		cmd := extractField(argsJSON, "command")
		// 归一化：剥离 "cd <path> &&" 前缀，避免 turn log 中显示冗长的 cd 前缀
		if normalized, _ := pathutil.NormalizeShellCommand(cmd); normalized != "" {
			return normalized
		}
		return cmd
	case "web_fetch":
		u := extractField(argsJSON, "url")
		if u != "" {
			return u
		}
		return truncateStr(argsJSON, 50)
	case "skill":
		name := extractField(argsJSON, "name")
		args := extractField(argsJSON, "arguments")
		if args != "" {
			return name + " " + args
		}
		return name
	case "agent":
		if desc := extractField(argsJSON, "description"); desc != "" {
			return desc
		}
		if t := extractField(argsJSON, "subagent_type"); t != "" {
			return t + " · " + extractField(argsJSON, "description")
		}
		return "fork · " + extractField(argsJSON, "description")
	case "ask_user_question":
		return formatQuestionArgs(argsJSON)
	case "enter_plan_mode", "exit_plan_mode":
		return "" // 无参数工具，不显示 {}
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

// formatQuestionArgs 从 ask_user_question 的 JSON 参数中提取问题 header 摘要。
// 解析失败或 header 过多时回退到截断原 JSON。
func formatQuestionArgs(argsJSON string) string {
	var params struct {
		Questions []struct {
			Header string `json:"header"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &params); err != nil || len(params.Questions) == 0 {
		return truncateStr(argsJSON, 50)
	}
	headers := make([]string, len(params.Questions))
	for i, q := range params.Questions {
		headers[i] = q.Header
	}
	return strings.Join(headers, ", ")
}

// parseQuestionResult 解析 ask_user_question 的 ToolResult JSON，
// 返回 question→answers 的映射和问题列表（用于渲染顺序）。
func parseQuestionResult(resultJSON string) (map[string]string, []string) {
	var data struct {
		Questions []struct {
			Question string `json:"question"`
			Header   string `json:"header"`
		} `json:"questions"`
		Answers map[string]string `json:"answers"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &data); err != nil {
		return nil, nil
	}
	m := make(map[string]string, len(data.Questions))
	order := make([]string, 0, len(data.Questions))
	for _, q := range data.Questions {
		answer := data.Answers[q.Question]
		m[q.Header] = answer
		order = append(order, q.Header)
	}
	return m, order
}

// formatQuestionPreview 将问答结果渲染为可读预览行（折叠态）。
func formatQuestionPreview(resultJSON string, textWidth int, indent string, lc *Messages) string {
	answers, order := parseQuestionResult(resultJSON)
	if len(order) == 0 {
		return ""
	}
	// 与其他工具预览的 "│ " 前缀保持一致，使用灰色
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	contentWidth := textWidth - 2 // "│ " 前缀占 2 列
	if contentWidth < 1 {
		contentWidth = 1
	}
	var sb strings.Builder
	wrapped := 0
	truncated := false
	for _, header := range order {
		answer := answers[header]
		if answer == "" {
			answer = lc.ToolQuestionDeclined
		}
		line := header + " → " + answer
		for _, wl := range wrapLine(line, contentWidth) {
			if wrapped >= maxPreviewWrapped {
				truncated = true
				break
			}
			sb.WriteString(indent)
			sb.WriteString(mutedStyle.Render("│ "))
			sb.WriteString(wl)
			sb.WriteString("\n")
			wrapped++
		}
		if truncated {
			break
		}
	}
	if truncated {
		sb.WriteString(indent)
		sb.WriteString(styleToolPreviewHint.Render(lc.ToolTruncated))
		sb.WriteString("\n")
	}
	return sb.String()
}

// formatQuestionExpanded 将问答结果渲染为展开态，与 shell output 对齐（│ header → answer）。
func formatQuestionExpanded(resultJSON string, indent string, textWidth int, lc *Messages) string {
	answers, order := parseQuestionResult(resultJSON)
	if len(order) == 0 {
		return ""
	}
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	contentWidth := textWidth - 2 // "│ " 前缀占 2 列
	if contentWidth < 1 {
		contentWidth = 1
	}
	var sb strings.Builder
	wrapped := 0
	truncated := false
	for _, header := range order {
		answer := answers[header]
		if answer == "" {
			answer = lc.ToolQuestionDeclined
		}
		line := header + " → " + answer
		for _, wl := range wrapLine(line, contentWidth) {
			if wrapped >= maxExpandedWrapped {
				truncated = true
				break
			}
			sb.WriteString(indent)
			sb.WriteString(mutedStyle.Render("│ "))
			sb.WriteString(wl)
			sb.WriteString("\n")
			wrapped++
		}
		if truncated {
			break
		}
	}
	if truncated {
		sb.WriteString(indent)
		sb.WriteString(styleToolPreviewHint.Render(fmt.Sprintf(lc.ToolTruncatedLines, maxExpandedWrapped)))
		sb.WriteString("\n")
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// 工具摘要后缀格式化
// ---------------------------------------------------------------------------

// toolSuffix 根据工具类型和结果生成成功/失败的后缀字符串。
func toolSuffix(p *Paragraph, lc *Messages) string {
	// 工具仍在执行中，尚无结果
	if p.State == stateStreaming {
		return ""
	}

	if p.ToolError != "" {
		// 工具专用简短错误后缀：摘要保持简洁，完整错误在预览区展示
		switch p.ToolName {
		case "bash":
			code := parseExitCode(p.ToolResult)
			if code >= 0 {
				return fmt.Sprintf("(exit=%d)", code)
			}
		case "web_fetch":
			return webFetchErrorSuffix(p.ToolErrorKind, p.ToolError)
		case "edit_file":
			return editFileErrorSuffix(p.ToolErrorKind, p.ToolError)
		case "exit_plan_mode":
			if p.ToolErrorKind == "user_declined" {
				return "(rejected)"
			}
		}
		// 通用回退：用错误分类作为后缀，避免路径等长信息与预览区重叠
		return fmt.Sprintf("(%s)", p.ToolErrorKind)
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
	case "bash":
		code := parseExitCode(p.ToolResult)
		dur := formatDuration(p.ToolDurMs)
		if code >= 0 {
			return fmt.Sprintf("(exit=%d, %s)", code, dur)
		}
		return fmt.Sprintf("(%s)", dur)
	case "web_fetch", "skill":
		size := formatBytes(len(p.ToolResult))
		dur := formatDuration(p.ToolDurMs)
		return fmt.Sprintf("(%s, %s)", size, dur)
	case "ask_user_question":
		n := parseQuestionCount(p.ToolResult)
		if n <= 0 {
			n = parseQuestionCount(p.ToolArgs)
		}
		return fmt.Sprintf(lc.ToolNQuestions, n)
	case "enter_plan_mode":
		return "" // 进入 plan 模式，无额外摘要
	case "exit_plan_mode":
		return "" // 审批通过，无额外摘要
	default:
		dur := formatDuration(p.ToolDurMs)
		return fmt.Sprintf("(%s)", dur)
	}
}

// webFetchErrorSuffix 返回 web_fetch 错误的简短后缀。
// 从 ToolError.Message 中提取 HTTP 状态码或使用错误分类。
func webFetchErrorSuffix(kind, msg string) string {
	switch kind {
	case "timeout":
		return "(timeout)"
	case "invalid_args":
		return "(invalid URL)"
	case "binary_file":
		return "(unsupported type)"
	case "command_failed":
		// HTTP 错误：提取状态码，如 "HTTP 404 Not Found" → "(HTTP 404)"
		if strings.HasPrefix(msg, "HTTP ") {
			parts := strings.SplitN(msg, " ", 3)
			if len(parts) >= 2 {
				return fmt.Sprintf("(HTTP %s)", parts[1])
			}
		}
		return "(request failed)"
	default:
		return fmt.Sprintf("(%s)", kind)
	}
}

// editFileErrorSuffix 为 edit_file 错误生成简短后缀，避免与预览区完整错误内容重叠。
func editFileErrorSuffix(kind, msg string) string {
	switch kind {
	case "multiple_matches":
		// "found N matches for ..." → "(N matches)"
		if n := parseMatchCount(msg); n > 0 {
			return fmt.Sprintf("(%d matches)", n)
		}
		return "(multiple_matches)"
	default:
		return fmt.Sprintf("(%s)", kind)
	}
}

// parseMatchCount 从 "found N matches" 中提取数字 N。
func parseMatchCount(msg string) int {
	// msg: "found 3 matches for old_string in /path; ..."
	if !strings.HasPrefix(msg, "found ") {
		return 0
	}
	rest := strings.TrimPrefix(msg, "found ")
	// 找到第一个空格或 "matches" 之前的部分
	idx := strings.Index(rest, " ")
	if idx < 0 {
		return 0
	}
	n, err := strconv.Atoi(rest[:idx])
	if err != nil {
		return 0
	}
	return n
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
// 优先展示 USD 余额，若无则取首个币种；不支持时返回空字符串。
func formatBalance(balance *llm.BalanceInfo) string {
	if balance == nil || len(balance.BalanceInfos) == 0 {
		return ""
	}
	// 优先取 USD
	var cb *llm.CurrencyBalance
	for i := range balance.BalanceInfos {
		if balance.BalanceInfos[i].Currency == "USD" {
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

// parseQuestionCount 从 ask_user_question 的 JSON 中解析问题数量。
func parseQuestionCount(jsonStr string) int {
	var params struct {
		Questions []struct{} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &params); err != nil {
		return 0
	}
	return len(params.Questions)
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
// web_fetch 专用完整渲染
// ---------------------------------------------------------------------------
func renderWebFetchFull(sb *strings.Builder, result string, textWidth int, indent string, lc *Messages) {
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
	sb.WriteString(styleToolPreviewHint.Render(fmt.Sprintf(lc.ToolTruncatedLines, maxExpandedWrapped)))
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
	Subagent spinner.Model
	Glamour  *glamour.TermRenderer // nil 时回退到纯文本
	Width    int                   // viewport 内容宽度（终端宽度 - 4）
	Focused  bool                  // 当前段落是否处于焦点态
	LC       *Messages             // 当前语言文案
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

		// 过滤 Todo StatusSummary 注入消息（仅为 LLM 上下文，不应在 TUI 中渲染）
		if (p.Type == paraUser || p.Type == paraSystem) && strings.HasPrefix(strings.TrimSpace(p.Text), "## Current Todo Status") {
			lineStarts[i] = currentLine
			continue
		}
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
		case paraSubagent:
			renderSubagentPara(&tmp, p, ctx)
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
	textWidth := ctx.Width - prefixWidth
	if textWidth < 1 {
		textWidth = 1
	}

	if p.Text == "" {
		sb.WriteString(prefixStr)
		sb.WriteString("\n")
		return
	}

	// 流式输出中跳过 Glamour（markdown 结构不完整，渲染无意义且极慢）；
	// 仅在 done 时使用 Glamour，并缓存结果避免重复渲染。
	rendered := p.Text
	glamourUsed := false
	if !streaming && ctx.Glamour != nil {
		if p.renderedCache != "" && p.cacheWidth == ctx.Width {
			rendered = p.renderedCache
			glamourUsed = true
		} else {
			if out, err := ctx.Glamour.Render(p.Text); err == nil {
				rendered = out
				p.renderedCache = out
				p.cacheWidth = ctx.Width
				glamourUsed = true
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
		wrapped := []string{line}
		if !glamourUsed {
			// 流式输出中用稳定截断（避免 word-wrap 断点漂移导致抖动）
			if streaming {
				wrapped = wrapLineStable(line, textWidth)
			} else {
				wrapped = wrapLine(line, textWidth)
			}
		}
		for _, wl := range wrapped {
			if firstLine {
				sb.WriteString(prefixStr)
				firstLine = false
			} else {
				sb.WriteString(indent)
			}
			sb.WriteString(wl)
			sb.WriteString("\n")
		}
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
			wrapped := wrapLineStable(rawLines[i], textWidth)
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
				sb.WriteString(styleThoughtStreaming.Render(ctx.LC.ThoughtThinking))
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
				fmt.Sprintf(ctx.LC.ThoughtComplete, p.ThoughtTokens)))
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
				fmt.Sprintf(ctx.LC.ThoughtExpandHint, p.ThoughtTokens)))
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
		sb.WriteString(styleThoughtExpandHint.Render(ctx.LC.ThoughtCollapseHint))
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



// wrapLineStable 按列宽硬截断，不做 word-wrap 优化。
// 流式输出中使用，保证换行位置仅由字符位置决定，不因后续词增长而漂移。
func wrapLineStable(line string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{""}
	}
	runes := []rune(line)
	var result []string
	for len(runes) > 0 {
		w := 0
		cut := 0
		ansiHeadLen := skipAnsiSequence(runes)
		cut = ansiHeadLen
		for i := ansiHeadLen; i < len(runes); {
			r := runes[i]
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
			i++
		}
		if cut == 0 {
			cut = 1
		}
		result = append(result, string(runes[:cut]))
		runes = runes[cut:]
	}
	if len(result) == 0 {
		result = append(result, "")
	}
	return result
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
// 同时剥离 shell 的 "Command succeeded/failed/timed out" 头部和 "stdout:" / "stderr/stdout:" 标签行，
// 让预览直接展示实际输出内容。
func stripToolStatusHeader(result string) string {
	lines := strings.Split(result, "\n")
	start := 0

	// 通用：跳过 emoji 状态标题（✅ / ❌ 开头）
	if len(lines) > 0 {
		first := strings.TrimSpace(lines[0])
		if strings.HasPrefix(first, "\u2705") || strings.HasPrefix(first, "\u274c") {
			start = 1
		}
	}

	// Shell：跳过 "Command succeeded / failed / timed out" 状态头
	if start == 0 && len(lines) > 0 {
		first := strings.TrimSpace(lines[0])
		if strings.HasPrefix(first, "Command succeeded") ||
			strings.HasPrefix(first, "Command failed") ||
			strings.HasPrefix(first, "Command timed out") {
			start = 1

			// 跳过 stdout/stderr 标签行和 timeout 提示行
			if len(lines) > 1 {
				second := strings.TrimSpace(lines[1])
				if strings.HasPrefix(second, "stdout:") ||
					strings.HasPrefix(second, "stderr/stdout:") ||
					strings.HasPrefix(second, "Timeout:") {
					start = 2
				}
			}
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

	prefix := toolPrefix(ctx.Tool, toolState, p.ToolFatal)
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

	// tool 名颜色：成功绿色，recoverable 错误金色，fatal 错误红色
	toolNameStyle := styleToolPrefixDone
	if toolState == stateError {
		if p.ToolFatal {
			toolNameStyle = styleToolPrefixErr
		} else {
			toolNameStyle = styleToolPrefixWarn
		}
	}

	// 构造摘要行并做宽度自适应截断
	toolNameRendered := toolNameStyle.Render(p.ToolName)
	suffixRendered := toolSuffix(p, ctx.LC)
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
		if p.State == stateCollapsed || p.State == stateDone || p.State == stateError {
			if p.DiffHunks != nil {
				renderDiffPreview(sb, p.DiffHunks, textWidth, indentStr, ctx.LC)
			} else {
				renderToolPreview(sb, p, textWidth, indentStr, ctx.LC)
			}
		}
	}

	// 流式输出 —— 实时渲染已有输出（stateStreaming + 有 ToolResult 时）
	if p.State == stateStreaming && p.ToolResult != "" {
		renderToolStreamOutput(sb, p, textWidth, indentStr, ctx.LC)
	}

	// 展开态 —— 显示完整输出
	if p.State == stateExpanded {
		if p.DiffHunks != nil {
			renderDiffView(sb, p.DiffHunks, textWidth, indentStr, ctx.LC)
		} else {
			renderToolFullOutput(sb, p, textWidth, indentStr, ctx.LC)
		}
	}
}

// maxPreviewWrapped 是折叠预览的最大包装后行数。限制 wrapLine 膨胀后的实际显示行数，
// 防止单条超长行（如 100KB 无换行 JSON）撑满折叠预览。
const maxPreviewWrapped = 5

// renderToolPreview 渲染工具输出的默认预览行（折叠态）。indent 由上层传入以对齐摘要行前缀。
//
// 错误态（ToolResult 为空但 ToolError 非空）：统一以红色 "│ " 前缀渲染错误信息，
// 与 shell 错误输出布局对齐，保证所有工具的失败信息在 TUI 中一致可见。
func renderToolPreview(sb *strings.Builder, p *Paragraph, textWidth int, indent string, lc *Messages) {
	// ask_user_question 定制渲染：显示可读的问答摘要
	if p.ToolName == "ask_user_question" && p.ToolResult != "" {
		preview := formatQuestionPreview(p.ToolResult, textWidth, indent, lc)
		if preview != "" {
			sb.WriteString(preview)
			return
		}
	}

	result := stripToolStatusHeader(p.ToolResult)
	isErrorOnly := false
	if result == "" && p.ToolError != "" {
		result = p.ToolError
		isErrorOnly = true
	}
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

	// ── 错误统一预览：所有工具的错误信息（ToolResult 为空、ToolError 非空）
	//    fatal 错误以红色样式渲染，recoverable 错误以金色样式渲染。
	if isErrorOnly {
		errStyle := styleToolPrefixErr
		if !p.ToolFatal {
			errStyle = styleToolPrefixWarn
		}
		lines := strings.Split(result, "\n")
		for _, line := range lines {
			if writeWrappedPreview(line, errStyle, &wrapped) {
				truncated = true
				break
			}
		}
		if truncated {
			sb.WriteString(indent)
			sb.WriteString(styleToolPreviewHint.Render(lc.ToolTruncated))
			sb.WriteString("\n")
		}
		return
	}

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

	case "bash", "skill":
		lineStyle := styleToolPreview
		if p.ToolError != "" {
			if p.ToolFatal {
				lineStyle = styleToolPrefixErr
			} else {
				lineStyle = styleToolPrefixWarn
			}
		}
		lines := strings.Split(result, "\n")
		for _, line := range lines {
			line = strings.TrimLeft(line, " ")
			if writeWrappedPreview(line, lineStyle, &wrapped) {
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
		// 无预览（read_file, grep, search_file, ls 等成功态不展示预览）
	}

	if truncated {
		sb.WriteString(indent)
		// 仅可段落聚焦的工具展示 Enter 提示（shell / web_fetch），
		// write_file / edit_file 等不可聚焦的工具仅显示截断标记。
		switch p.ToolName {
		case "bash", "web_fetch", "skill":
			sb.WriteString(styleToolPreviewHint.Render(lc.ToolExpandAllHint))
		default:
			sb.WriteString(styleToolPreviewHint.Render(lc.ToolTruncated))
		}
		sb.WriteString("\n")
	}
}

// renderToolStreamOutput 渲染流式工具输出，与 renderThoughtPara 的 fixedLines 反向收集逻辑一致。
// 从尾部反向采集直到获得 fixedLines 个 wrap 后可见行，不显示截断提示（截断是隐式的）。
func renderToolStreamOutput(sb *strings.Builder, p *Paragraph, textWidth int, indent string, lc *Messages) {
	result := strings.TrimRight(p.ToolResult, "\n")
	if result == "" {
		return
	}

	contentWidth := textWidth - 2 // "│ " 前缀占 2 列
	if contentWidth < 1 {
		contentWidth = 1
	}

	const fixedLines = 5

	rawLines := strings.Split(result, "\n")
	var visible []string
	// 从尾部反向收集，直到获得至少 fixedLines 个 wrap 后的可见行
	for i := len(rawLines) - 1; i >= 0 && len(visible) < fixedLines; i-- {
		if rawLines[i] == "" {
			continue
		}
		wrapped := wrapLineStable(rawLines[i], contentWidth)
		visible = append(wrapped, visible...)
	}
	// 超出 fixedLines 时从头部截断
	if len(visible) > fixedLines {
		visible = visible[len(visible)-fixedLines:]
	}

	for _, wl := range visible {
		sb.WriteString(indent)
		sb.WriteString(styleToolPreview.Render("│ "))
		sb.WriteString(wl)
		sb.WriteString("\n")
	}
}


// maxExpandedWrapped 是展开态的最大包装后行数。防止单条超长行在展开时产生海量 viewport 行。
const maxExpandedWrapped = 2000

// renderToolFullOutput 渲染工具的完整输出（展开态）。indent 由上层传入以对齐摘要行前缀。
// 当 ToolResult 为空但 ToolError 非空时（如 stateError → stateExpanded 展开），
// 回退到渲染错误信息。
func renderToolFullOutput(sb *strings.Builder, p *Paragraph, textWidth int, indent string, lc *Messages) {
	if textWidth < 1 {
		textWidth = 1
	}

	result := stripToolStatusHeader(p.ToolResult)
	if result == "" && p.ToolError != "" {
		result = p.ToolError
	}
	if result == "" {
		return
	}

	wrapped := 0
	truncated := false

	switch p.ToolName {
	case "ask_user_question":
		// 展开态：显示每个问题的完整信息
		sb.WriteString(formatQuestionExpanded(p.ToolResult, indent, textWidth, lc))
		return
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

	case "bash":
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

	case "web_fetch":
		renderWebFetchFull(sb, result, textWidth, indent, lc)
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
		sb.WriteString(styleToolPreviewHint.Render(fmt.Sprintf(lc.ToolTruncatedLines, maxExpandedWrapped)))
		sb.WriteString("\n")
	}

	// 折叠提示 — 仅可段落聚焦的工具（shell / web_fetch）展示 Enter 提示
	switch p.ToolName {
	case "bash", "web_fetch":
		sb.WriteString(indent)
		sb.WriteString(styleToolPreviewHint.Render(lc.ToolCollapseHint))
		sb.WriteString("\n")
	}
}

// renderDiffPreview 渲染 diff 的折叠预览。受 maxPreviewWrapped 约束，
// 防止单条超长行撑满预览。edit_file 不参与段落聚焦，截断时仅显示标记。
func renderDiffPreview(sb *strings.Builder, hunks []tool.DiffHunk, textWidth int, indent string, lc *Messages) {
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
		sb.WriteString(styleToolPreviewHint.Render(lc.ToolTruncated))
		sb.WriteString("\n")
	}
}

// renderDiffView 渲染完整的统一 diff 视图（展开态），遵循 POSIX unified diff 格式：
//   - 前缀为单字符（+ / - / 空格）
//   - hunk header 在 count=1 时省略 ",1"
//   - 附加虚行号列（灰色），不影响 diff 语义
//
// 受 maxExpandedWrapped 约束，防止超长行导致海量输出。
func renderDiffView(sb *strings.Builder, hunks []tool.DiffHunk, textWidth int, indent string, lc *Messages) {
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
		if w := digitCount(maxNum); w > numWidth {
			numWidth = w
		}
	}

	// 单字符前缀 + 行号列宽度 + 续行缩进（1 空格 = 前缀宽度）
	codeWidth := textWidth - numWidth - 2 // numStr("  5 ") + prefix(1)
	if codeWidth < 1 {
		codeWidth = 1
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

		// @@ header（count=1 时省略 ",1"）；heading 过长时截断，防止终端换行造成视觉混乱
		header := fmt.Sprintf("@@ -%s +%s @@", hunkRange(h.OldStart, h.OldCount), hunkRange(h.NewStart, h.NewCount))
		if h.Heading != "" {
			full := header + " " + h.Heading
			if len(full) > textWidth {
				avail := textWidth - len(header) - 1 // -1 for space
				if avail > 0 {
					heading := h.Heading
					if len(heading) > avail {
						heading = heading[:avail]
					}
					header += " " + heading
				}
			} else {
				header = full
			}
		}
		sb.WriteString(indent)
		sb.WriteString(styleDiffHeader.Render(header))
		sb.WriteString("\n")

		for _, l := range h.Lines {
			prefix, styleContent := diffLinePrefixAndStyle(l.Kind)
			// 行号：删除显示旧行号，新增显示新行号，上下文显示旧行号
			var num int
			switch l.Kind {
			case tool.DiffDel:
				num = l.OldNum
			case tool.DiffAdd:
				num = l.NewNum
			default:
				num = l.OldNum
			}
			numStr := fmt.Sprintf("%*d ", numWidth, num)

			wlines := wrapLine(l.Content, codeWidth)
			for wi, wl := range wlines {
				if wrapped >= maxExpandedWrapped {
					truncated = true
					break
				}
				sb.WriteString(indent)
				if wi == 0 {
					// 首行：行号 + 前缀 + 内容
					sb.WriteString(styleLineNum.Render(numStr))
					sb.WriteString(styleContent.Render(prefix + wl))
				} else {
					// 续行：空行号 + 单空格缩进
					sb.WriteString(styleLineNum.Render(fmt.Sprintf("%*s ", numWidth, "")))
					sb.WriteString(styleContent.Render(" " + wl))
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
		sb.WriteString(styleToolPreviewHint.Render(fmt.Sprintf(lc.ToolTruncatedLines, maxExpandedWrapped)))
		sb.WriteString("\n")
	}
}

// digitCount 返回 n 的十进制位数。
func digitCount(n int) int {
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

// hunkRange 返回 unified diff hunk header 中的 range 字符串。
// count=1 时省略 ",1"，与 git diff 行为一致。
func hunkRange(start, count int) string {
	if count == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, count)
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
// 严格遵循 unified diff 单字符前缀规范：+ 新增，- 删除，空格 上下文。
func linePrefix(kind tool.DiffLineKind) string {
	switch kind {
	case tool.DiffAdd:
		return "+"
	case tool.DiffDel:
		return "-"
	case tool.DiffHeader:
		return "@@"
	default:
		return " "
	}
}

// diffLinePrefixAndStyle 根据 DiffLineKind 返回首行前缀和续行样式。
// 严格遵循 unified diff 单字符前缀规范。
func diffLinePrefixAndStyle(kind tool.DiffLineKind) (prefix string, style lipgloss.Style) {
	switch kind {
	case tool.DiffAdd:
		return "+", styleDiffAddBG
	case tool.DiffDel:
		return "-", styleDiffDelBG
	case tool.DiffCtx:
		return " ", styleDiffCtx
	default:
		return " ", styleToolExpanded
	}
}



// ---------------------------------------------------------------------------
// 辅助：检测字符串内容类型
// ---------------------------------------------------------------------------

// collapseBlankLines 将 2 个以上连续换行压缩为最多 1 个空行（即 \n\n\n+ → \n\n）。
// 用于归一化 Glamour 在不同 block 元素间输出的不等量空行。
func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

// findFirstPromptPos 在可能含 ANSI 转义序列的字符串中查找第一个 "  "（2 空格）的位置。
// 返回下标（不含前导 ANSI），找不到返回 -1。
func findFirstPromptPos(s string) int {
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		if runes[i] == 0x1b {
			skip := skipAnsiSequence(runes[i:])
			i += skip
			continue
		}
		if runes[i] == ' ' && i+1 < len(runes) && runes[i+1] == ' ' {
			return i
		}
		i++
	}
	return -1
}

// ---------------------------------------------------------------------------
// Subagent 段落渲染
// ---------------------------------------------------------------------------

// renderSubagentPara 渲染子 agent 段落，完全对齐 renderToolPara 的样式。
// 摘要行格式：● agent  general-purpose · description  (2轮, 2.1s, ↑1.2K, ↓2.0K)
func renderSubagentPara(sb *strings.Builder, p *Paragraph, ctx ViewportCtx) {
	subState := p.State
	if p.ToolError != "" {
		subState = stateError
	}

	// ── 前缀：使用独立的 subagent spinner，区别于普通工具 ──
	prefix := toolPrefix(ctx.Subagent, subState, false)
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

	// ── tool 名颜色 ──
	toolNameStyle := styleToolPrefixDone
	if subState == stateError {
		if p.ToolFatal {
			toolNameStyle = styleToolPrefixErr
		} else {
			toolNameStyle = styleToolPrefixWarn
		}
	}
	toolNameRendered := toolNameStyle.Render("agent")

	// ── args：agent 类型 · description ──
	agentLabel := p.SubagentType
	if agentLabel == "" {
		agentLabel = "fork"
	}
	desc := p.SubagentPrompt
	if desc == "" {
		desc = p.Text
	}
	argsText := agentLabel + " · " + desc

	// ── suffix（对齐 bash 的 toolSuffix 格式） ──
	suffixRendered := subagentSuffix(p)

	// ── 摘要行宽度自适应 ──
	fixedWidth := lipgloss.Width(toolNameRendered) + lipgloss.Width("  ") + lipgloss.Width("  ") + lipgloss.Width(suffixRendered)
	maxArgsWidth := textWidth - fixedWidth
	if maxArgsWidth < 4 {
		maxArgsWidth = 4
	}
	argsRunes := []rune(argsText)
	if len(argsRunes) > maxArgsWidth {
		argsText = string(argsRunes[:maxArgsWidth-1]) + "…"
	}

	sb.WriteString(prefixStr)
	sb.WriteString(toolNameRendered)
	sb.WriteString("  ")
	sb.WriteString(styleToolArgs.Render(argsText))
	sb.WriteString("  ")
	sb.WriteString(suffixRendered)
	sb.WriteString("\n")

	// ── 流式输出：活跃中普通字体（对齐 shell 流式） ──
	if p.State == stateStreaming && p.Text != "" {
		renderSubagentStreamLines(sb, p.Text, textWidth, indentStr, 5, false)
		return
	}

	// ── 完成/折叠：灰色预览（对齐 shell 折叠预览） ──
	if p.State == stateDone || p.State == stateCollapsed {
		renderSubagentStreamLines(sb, p.Text, textWidth, indentStr, 5, true)
		sb.WriteString(indentStr)
		sb.WriteString(styleToolPreviewHint.Render(ctx.LC.ToolExpandAllHint))
		sb.WriteString("\n")
		return
	}

	// ── 错误态 ──
	if p.State == stateError {
		renderSubagentStreamLines(sb, p.Text, textWidth, indentStr, 5, true)
		return
	}

	// ── 展开态：完整输出 ──
	if p.State == stateExpanded {
		saved := p.ToolResult
		p.ToolResult = p.Text
		renderToolFullOutput(sb, p, textWidth, indentStr, ctx.LC)
		p.ToolResult = saved
	}
}

// renderSubagentStreamLines 渲染固定行数、尾部内容。
// muted=true 时全文灰色（折叠预览），false 时仅前缀灰色（流式，对齐 shell）。
func renderSubagentStreamLines(sb *strings.Builder, text string, textWidth int, indent string, fixedLines int, muted bool) {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}
	contentWidth := textWidth - 2
	if contentWidth < 1 {
		contentWidth = 1
	}
	rawLines := strings.Split(text, "\n")
	var visible []string
	for i := len(rawLines) - 1; i >= 0 && len(visible) < fixedLines; i-- {
		if rawLines[i] == "" {
			continue
		}
		wrapped := wrapLineStable(rawLines[i], contentWidth)
		visible = append(wrapped, visible...)
	}
	if len(visible) > fixedLines {
		visible = visible[len(visible)-fixedLines:]
	}
	for _, wl := range visible {
		sb.WriteString(indent)
		if muted {
			sb.WriteString(styleToolPreview.Render("│ " + wl))
		} else {
			sb.WriteString(styleToolPreview.Render("│ "))
			sb.WriteString(wl)
		}
		sb.WriteString("\n")
	}
}

// subagentSuffix 返回子 agent 摘要行后缀，对齐 bash 的 toolSuffix 格式。
func subagentSuffix(p *Paragraph) string {
	if p.State == stateStreaming {
		return ""
	}
	if p.ToolError != "" {
		return "(interrupted)"
	}
	suffix := ""
	if p.SubagentTurns > 0 {
		suffix = fmt.Sprintf("%d轮", p.SubagentTurns)
	}
	if p.ToolDurMs > 0 {
		if suffix != "" {
			suffix += ", "
		}
		suffix += formatDuration(p.ToolDurMs)
	}
	if p.SubagentPromptTok > 0 {
		suffix += fmt.Sprintf(", ↑%s", formatTokens(p.SubagentPromptTok))
	}
	if p.SubagentComplTok > 0 {
		suffix += fmt.Sprintf(", ↓%s", formatTokens(p.SubagentComplTok))
	}
	if suffix == "" {
		return ""
	}
	return "(" + suffix + ")"
}

// ---------------------------------------------------------------------------
// 格式辅助函数
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Todo 面板渲染
// ---------------------------------------------------------------------------

// renderTodoPanel 渲染固定在底部的 todo 面板。
// spinnerView 是 bubbletea spinner 的当前渲染帧（用于 in_progress 项）。
// 返回渲染后的字符串和面板所占行数。
func renderTodoPanel(lc *Messages, todos []todo.TodoItem, width int, expanded bool, focused bool, spinnerView string) (string, int) {
	if len(todos) == 0 {
		return "", 0
	}

	// 排序：in_progress > pending > completed
	sorted := make([]todo.TodoItem, len(todos))
	copy(sorted, todos)
	sortTodos(sorted)

	// 统计各状态数量
	var inProgressCount, pendingCount, completedCount int
	for _, t := range sorted {
		switch t.Status {
		case "in_progress":
			inProgressCount++
		case "pending":
			pendingCount++
		case "completed":
			completedCount++
		}
	}
	totalCount := len(sorted)

	// 缩略显示：默认 5 项
	maxVisible := 5
	if expanded {
		maxVisible = totalCount
	}
	visibleItems := sorted
	hidden := 0
	if totalCount > maxVisible {
		visibleItems = sorted[:maxVisible]
		hidden = totalCount - maxVisible
	}

	// 边框
	borderColor := colorFooterFg
	if focused {
		borderColor = colorHeaderAccent
	}
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(width)

	innerWidth := width - 2 - 2 // border(2) + padding(2)

	// ── 标题行 ──
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(colorHeaderAccent).Width(innerWidth)
	title := titleStyle.Render(fmt.Sprintf(lc.TodoTitle, totalCount, completedCount, totalCount))

	// ── 每项渲染 ──
	var itemLines []string
	for _, t := range visibleItems {
		itemLines = append(itemLines, renderTodoItem(t, innerWidth, spinnerView))
	}

	// ── 折叠提示（仅显示 hidden 项的类型分解） ──
	if hidden > 0 {
		hintStyle := lipgloss.NewStyle().Foreground(colorMuted).Width(innerWidth)
		hiddenItems := sorted[maxVisible:]
		hint := formatHiddenSummary(lc, hiddenItems, hidden)
		itemLines = append(itemLines, hintStyle.Render(hint))
	}

	// ── 组装 ──
	var parts []string
	parts = append(parts, title, "")
	parts = append(parts, itemLines...)

	content := strings.Join(parts, "\n")
	rendered := boxStyle.Render(content)

	return rendered, strings.Count(rendered, "\n") + 1
}

// renderTodoItem 渲染单条 todo 项。
func renderTodoItem(t todo.TodoItem, width int, spinnerView string) string {
	switch t.Status {
	case "in_progress":
		return renderTodoInProgress(t, width, spinnerView)
	case "completed":
		return renderTodoCompleted(t, width)
	default:
		return renderTodoPending(t, width)
	}
}

func renderTodoInProgress(t todo.TodoItem, width int, spinnerView string) string {
	// spinner + bold + colorAccentGold
	spinnerPart := lipgloss.NewStyle().Foreground(colorAccentGold).Render(spinnerView)
	textPart := lipgloss.NewStyle().Foreground(colorAccentGold).Bold(true).Render(t.ActiveForm)
	line := spinnerPart + " " + textPart
	return lipgloss.NewStyle().Width(width).Render(line)
}

func renderTodoPending(t todo.TodoItem, width int) string {
	// dim circle + muted
	marker := lipgloss.NewStyle().Foreground(colorMuted).Render("○")
	text := lipgloss.NewStyle().Foreground(colorMuted).Render(t.Content)
	line := marker + " " + text
	return lipgloss.NewStyle().Width(width).Render(line)
}

func renderTodoCompleted(t todo.TodoItem, width int) string {
	// green check + strikethrough
	marker := lipgloss.NewStyle().Foreground(colorOK).Render("✓")
	text := lipgloss.NewStyle().Foreground(colorOK).Strikethrough(true).Render(t.Content)
	line := marker + " " + text
	return lipgloss.NewStyle().Width(width).Render(line)
}

// formatHiddenSummary 格式化隐藏项提示，仅显示隐藏项的类型分解。
func formatHiddenSummary(lc *Messages, hiddenItems []todo.TodoItem, hiddenCount int) string {
	var doneCount, inProgCount, pendCount int
	for _, t := range hiddenItems {
		switch t.Status {
		case "completed":
			doneCount++
		case "in_progress":
			inProgCount++
		case "pending":
			pendCount++
		}
	}
	parts := []string{fmt.Sprintf(lc.TodoHiddenCount, hiddenCount)}
	if doneCount > 0 {
		parts = append(parts, fmt.Sprintf(lc.TodoDoneCount, doneCount))
	}
	if inProgCount > 0 {
		parts = append(parts, fmt.Sprintf(lc.TodoInProgCount, inProgCount))
	}
	if pendCount > 0 {
		parts = append(parts, fmt.Sprintf(lc.TodoPendingCount, pendCount))
	}
	return strings.Join(parts, " · ")
}

func sortTodos(todos []todo.TodoItem) {
	rank := func(s string) int {
		switch s {
		case "in_progress":
			return 0
		case "pending":
			return 1
		default:
			return 2
		}
	}
	for i := 0; i < len(todos); i++ {
		for j := i + 1; j < len(todos); j++ {
			if rank(todos[i].Status) > rank(todos[j].Status) {
				todos[i], todos[j] = todos[j], todos[i]
			}
		}
	}
}
