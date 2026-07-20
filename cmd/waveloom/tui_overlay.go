package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// ---------------------------------------------------------------------------
// Overlay 类型
// ---------------------------------------------------------------------------

// Overlay 表示当前活跃的覆盖层。
type Overlay int

const (
	overlayNone           Overlay = iota // 无覆盖层
	overlayPermission                    // 权限确认框（阻断式）
	overlayQuestion                      // AskUserQuestion 选择题（阻断式）
	overlayThemePicker                   // /theme 触发：主题选择列表
	overlayModelPicker                   // /model 无参触发：模型选择列表
	overlayLocalePicker                  // /locale 触发：语言选择列表
	overlayCommandPicker                 // / 命令补全（预留）
	overlayPlanEnter                     // 进入 plan 模式确认（阻断式）
	overlayPlanExit                      // plan 审批（阻断式，展示 plan 内容）
	overlayHelp                          // ? 快捷键帮助
	overlayRewindSelect                  // rewind 消息选择
	overlayRewindConfirm                 // rewind 确认
	overlayProviderPicker                // /provider 无参触发：provider 选择列表
)

// renderOverlayBox 渲染覆盖层的外框。
// animFrame: 0=刚弹出（muted 边框），1+= 正常色。
func renderOverlayBox(boxWidth int, animFrame int, content string) string {
	borderColor := colorHeaderAccent
	if animFrame == 0 {
		borderColor = colorMuted
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(boxWidth).
		Render(content)
}

// renderOverlayHint 渲染覆盖层底部的快捷键提示。
func renderOverlayHint(helpModel *help.Model, innerWidth int, bindings []key.Binding) string {
	helpModel.SetWidth(innerWidth)
	return styleOverlayHint.Width(innerWidth).Render(helpModel.ShortHelpView(bindings))
}

// ---------------------------------------------------------------------------
// 权限确认框状态
// ---------------------------------------------------------------------------

// permChoice 用户在权限确认框中的选择索引。
type permChoice int

const (
	permAllow    permChoice = iota // Allow（本次放行）
	permAllowAll                   // Allow All（记住并始终放行）
	permDeny                       // Deny（本次拒绝）
)

// ---------------------------------------------------------------------------
// 权限确认框渲染（基于 bubbles/list）
// ---------------------------------------------------------------------------

// renderPermOverlay 渲染权限确认覆盖层。
// boxWidth 是面板宽度（已自适应裁剪为 ≤70）。
func (m *model) renderPermOverlay(boxWidth int) string {
	// box 内部可用宽度 = boxWidth - 左右 border(2) - 左右 padding(4)。
	innerWidth := boxWidth - 2 - 4

	// 同步 list 宽度到当前面板（对齐内容区宽度 innerWidth）
	m.permList.SetSize(innerWidth, 3) // 3 项，无间距

	overlayContentStyle := lipgloss.NewStyle().Width(innerWidth)

	title := styleOverlayTitle.Width(innerWidth).Render(m.msg().PermRequired)

	// 合并 Tool + Args 为一行： "shell: git push origin main"
	toolArgsLine := m.permReq.toolName
	if m.permReq.args != "" {
		toolArgsLine += ": " + m.permReq.args
	}
	wrappedToolArgs := wrapLine(toolArgsLine, innerWidth)
	if len(wrappedToolArgs) > 6 {
		wrappedToolArgs = wrappedToolArgs[:6]
		wrappedToolArgs[5] += "…"
	}

	// 构建内容行列表
	contentLines := []string{title, ""}
	for _, wl := range wrappedToolArgs {
		contentLines = append(contentLines, styleOverlayBody.Width(innerWidth).Render(wl))
	}

	// reason 仅在非默认原因时展示（安全检查、规则匹配等）
	if m.permReq.reason != "" {
		contentLines = append(contentLines, "")
		wrapWidth := innerWidth - len(m.msg().PermReason) // "Reason: " 前缀宽度
		if wrapWidth < 20 {
			wrapWidth = 20
		}
		wrappedReason := wrapLine(m.permReq.reason, wrapWidth)
		if len(wrappedReason) > 8 {
			wrappedReason = wrappedReason[:8]
			wrappedReason[7] += "…"
		}
		for i, wl := range wrappedReason {
			if i == 0 {
				contentLines = append(contentLines, styleOverlayBody.Width(innerWidth).Render(m.msg().PermReason+wl))
			} else {
				contentLines = append(contentLines, styleOverlayBody.Width(innerWidth).Render("        "+wl))
			}
		}
	}
	contentLines = append(contentLines, "")

	// 拼接 list 组件，包裹 overlay 背景避免 list viewport 露底
	contentLines = append(contentLines, overlayContentStyle.Render(m.permList.View()))
	contentLines = append(contentLines, "")

	hint := renderOverlayHint(&m.help, innerWidth, permKeyBindings(m.msg()))
	contentLines = append(contentLines, hint)

	return renderOverlayBox(boxWidth, m.overlayAnimFrame, strings.Join(contentLines, "\n"))
}

// ---------------------------------------------------------------------------
// AskUserQuestion 选择题覆盖层渲染（huh 表单）
// ---------------------------------------------------------------------------

// renderQuestionOverlay 渲染选择题覆盖层（huh 表单或 Other 文本输入）。
func (m *model) renderQuestionOverlay(boxWidth int) string {
	if m.questionReq == nil || m.questionIdx >= len(m.questionReq.questions) {
		return ""
	}

	q := m.questionReq.questions[m.questionIdx]
	totalQuestions := len(m.questionReq.questions)
	innerWidth := boxWidth - 2 - 4

	// 标题行：header chip + 问题编号
	titleText := fmt.Sprintf("▲ %s", q.Header)
	if totalQuestions > 1 {
		titleText += fmt.Sprintf(" (%d/%d)", m.questionIdx+1, totalQuestions)
	}
	title := styleOverlayTitle.Width(innerWidth).Render(titleText)

	var body string
	var hintKeys []key.Binding
	if m.questionFormIsOther {
		body = m.otherInput.View()
		hintKeys = questionOtherKeyBindings(m.msg())
	} else if m.questionForm != nil {
		body = m.questionForm.View()
		if q.MultiSelect {
			hintKeys = questionMultiKeyBindings(m.msg())
		} else {
			hintKeys = questionSingleKeyBindings(m.msg())
		}
	} else {
		return ""
	}

	// 底部快捷键提示（与权限面板一致）
	hint := renderOverlayHint(&m.help, innerWidth, hintKeys)

	content := strings.Join([]string{title, "", body, "", hint}, "\n")
	return renderOverlayBox(boxWidth, m.overlayAnimFrame, content)
}

// ---------------------------------------------------------------------------
// 主题选择器覆盖层渲染
// ---------------------------------------------------------------------------

// permKeyBindings 返回权限覆盖层的快捷键帮助。
func permKeyBindings(lc *Messages) []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("↑/↓"), key.WithHelp("↑/↓", lc.KeyNav)),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", lc.KeyConfirm)),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", lc.KeyDeny)),
	}
}

// questionSingleKeyBindings 单选问题快捷键提示。
func questionSingleKeyBindings(lc *Messages) []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("↑/↓"), key.WithHelp("↑/↓", lc.KeyNav)),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", lc.KeyConfirm)),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", lc.KeyDeny)),
	}
}

// questionMultiKeyBindings 多选问题快捷键提示。
func questionMultiKeyBindings(lc *Messages) []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("↑/↓"), key.WithHelp("↑/↓", lc.KeyNav)),
		key.NewBinding(key.WithKeys("space"), key.WithHelp("Space", lc.KeyToggle)),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", lc.KeyConfirm)),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", lc.KeyDeny)),
	}
}

// questionOtherKeyBindings Other 文本输入快捷键提示。
func questionOtherKeyBindings(lc *Messages) []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", lc.KeyConfirm)),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", lc.KeyCancel)),
	}
}

// themePickerKeyBindings 主题选择器快捷键。
func themePickerKeyBindings(lc *Messages) []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("↑/↓"), key.WithHelp("↑/↓", lc.KeyNav)),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", lc.KeyConfirm)),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", lc.KeyCancel)),
	}
}

// localePickerKeyBindings 语言选择器快捷键。
func localePickerKeyBindings(lc *Messages) []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("↑/↓"), key.WithHelp("↑/↓", lc.KeyNav)),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", lc.KeyConfirm)),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", lc.KeyCancel)),
	}
}

// modelPickerKeyBindings 模型选择器快捷键。
func modelPickerKeyBindings(lc *Messages) []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("↑/↓"), key.WithHelp("↑/↓", lc.KeyNav)),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", lc.KeyConfirm)),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", lc.KeyCancel)),
	}
}

func (m *model) renderThemePickerOverlay(boxWidth int) string {
	innerWidth := boxWidth - 2 - 4
	m.themeList.SetSize(innerWidth, len(themeItems))

	title := styleOverlayTitle.Width(innerWidth).Render(m.msg().PickerSelectTheme)
	contentLines := []string{title, ""}

	contentLines = append(contentLines, styleOverlayBody.Width(innerWidth).Render(m.themeList.View()))
	contentLines = append(contentLines, "")

	hint := renderOverlayHint(&m.help, innerWidth, themePickerKeyBindings(m.msg()))
	contentLines = append(contentLines, hint)

	return renderOverlayBox(boxWidth, m.overlayAnimFrame, strings.Join(contentLines, "\n"))
}

// ---------------------------------------------------------------------------
// 模型选择器覆盖层渲染
// ---------------------------------------------------------------------------

// modelPickerKeyBindings 在 tui_overlay.go 中定义。
// 主题和模型选择器的所有 key bindings 已合并为接受 *Messages 的函数。

func (m *model) renderModelPickerOverlay(boxWidth int) string {
	innerWidth := boxWidth - 2 - 4
	height := len(m.modelPickerItems)
	if height > 5 {
		height = 5
	}
	if height < 1 {
		height = 1
	}
	m.modelPickerList.SetSize(innerWidth, height)

	title := styleOverlayTitle.Width(innerWidth).Render(m.msg().PickerSelectModel)
	contentLines := []string{title, ""}

	contentLines = append(contentLines, styleOverlayBody.Width(innerWidth).Render(m.modelPickerList.View()))
	contentLines = append(contentLines, "")

	hint := renderOverlayHint(&m.help, innerWidth, modelPickerKeyBindings(m.msg()))
	contentLines = append(contentLines, hint)

	return renderOverlayBox(boxWidth, m.overlayAnimFrame, strings.Join(contentLines, "\n"))
}

// ---------------------------------------------------------------------------
// 语言选择器覆盖层渲染
// ---------------------------------------------------------------------------

func (m *model) renderLocalePickerOverlay(boxWidth int) string {
	innerWidth := boxWidth - 2 - 4
	m.localeList.SetSize(innerWidth, 2)

	title := styleOverlayTitle.Width(innerWidth).Render(m.msg().PickerSelectLocale)
	contentLines := []string{title, ""}

	contentLines = append(contentLines, styleOverlayBody.Width(innerWidth).Render(m.localeList.View()))
	contentLines = append(contentLines, "")

	hint := renderOverlayHint(&m.help, innerWidth, localePickerKeyBindings(m.msg()))
	contentLines = append(contentLines, hint)

	return renderOverlayBox(boxWidth, m.overlayAnimFrame, strings.Join(contentLines, "\n"))
}

// ---------------------------------------------------------------------------
// Provider 选择器覆盖层渲染
// ---------------------------------------------------------------------------

// providerPickerKeyBindings provider 选择器快捷键。
func providerPickerKeyBindings(lc *Messages) []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("↑/↓"), key.WithHelp("↑/↓", lc.KeyNav)),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", lc.KeyConfirm)),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", lc.KeyCancel)),
	}
}

func (m *model) renderProviderPickerOverlay(boxWidth int) string {
	innerWidth := boxWidth - 2 - 4
	height := len(m.providerPickerItems)
	if height > 5 {
		height = 5
	}
	if height < 1 {
		height = 1
	}
	m.providerPickerList.SetSize(innerWidth, height)

	title := styleOverlayTitle.Width(innerWidth).Render(m.msg().PickerSelectProvider)
	contentLines := []string{title, ""}

	contentLines = append(contentLines, styleOverlayBody.Width(innerWidth).Render(m.providerPickerList.View()))
	contentLines = append(contentLines, "")

	hint := renderOverlayHint(&m.help, innerWidth, providerPickerKeyBindings(m.msg()))
	contentLines = append(contentLines, hint)

	return renderOverlayBox(boxWidth, m.overlayAnimFrame, strings.Join(contentLines, "\n"))
}

// ---------------------------------------------------------------------------
// Plan 进入确认覆盖层渲染
// ---------------------------------------------------------------------------

func (m *model) renderPlanEnterOverlay(boxWidth int) string {
	innerWidth := boxWidth - 2 - 4

	msg := m.msg()
	title := styleOverlayTitle.Width(innerWidth).Render(msg.PlanEnterTitle)
	contentLines := []string{title, ""}
	contentLines = append(contentLines, styleOverlayBody.Width(innerWidth).Render(msg.PlanEnterDesc1))
	contentLines = append(contentLines, styleOverlayBody.Width(innerWidth).Render(msg.PlanEnterDesc2))
	contentLines = append(contentLines, "")

	hint := renderOverlayHint(&m.help, innerWidth, []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", msg.PlanEnterConfirm)),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", msg.PlanEnterCancel)),
	})
	contentLines = append(contentLines, hint)

	return renderOverlayBox(boxWidth, m.overlayAnimFrame, strings.Join(contentLines, "\n"))
}

// ---------------------------------------------------------------------------
// Plan 退出审批覆盖层渲染
// ---------------------------------------------------------------------------

func (m *model) renderPlanExitOverlay(boxWidth int) string {
	innerWidth := boxWidth - 2 - 4

	msg := m.msg()
	title := styleOverlayTitle.Width(innerWidth).Render(msg.PlanExitTitle)
	contentLines := []string{title}

	hint := renderOverlayHint(&m.help, innerWidth, []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", msg.PlanExitApprove)),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", msg.PlanExitReject)),
	})
	contentLines = append(contentLines, "")
	contentLines = append(contentLines, hint)

	return renderOverlayBox(boxWidth, m.overlayAnimFrame, strings.Join(contentLines, "\n"))
}

// ---------------------------------------------------------------------------
// 快捷键帮助覆盖层渲染
// ---------------------------------------------------------------------------

func (m *model) renderHelpOverlay(boxWidth int) string {
	innerWidth := boxWidth - 2 - 4

	msg := m.msg()
	title := styleOverlayTitle.Width(innerWidth).Render(msg.KeyHelpTitle)
	m.help.SetWidth(innerWidth)

	// 纵向渲染各组快捷键，避免 FullHelpView 列布局在窄终端下截断末尾组。
	var groups []string
	for _, g := range keyMapToGroups(m.keys) {
		groups = append(groups, m.help.ShortHelpView(g))
	}
	contentLines := []string{title, "", strings.Join(groups, "\n\n")}

	hint := renderOverlayHint(&m.help, innerWidth, []key.Binding{
		key.NewBinding(key.WithKeys("?, esc"), key.WithHelp("?/Esc", msg.KeyCancel)),
	})
	contentLines = append(contentLines, "", hint)

	return renderOverlayBox(boxWidth, m.overlayAnimFrame, strings.Join(contentLines, "\n"))
}

// keyMapToGroups 将 keyMap 转换为 help.ShortHelpView 所需的 [][]key.Binding 格式。
func keyMapToGroups(km keyMap) [][]key.Binding {
	return [][]key.Binding{
		{km.Enter, km.Interrupt, km.Quit},
		{km.FocusNext, km.FocusPrev, km.Picker, km.Paste, km.ToggleTheme, km.Help},
		{km.Up, km.Down, km.PageUp, km.PageDown, km.JumpBottom},
	}
}

// ---------------------------------------------------------------------------
// Rewind 覆盖层渲染
// ---------------------------------------------------------------------------

// rewindOption 是 rewind 确认界面的选项。
type rewindOption int

const (
	rewindBoth     rewindOption = iota // 回退代码和对话
	rewindConvOnly                     // 仅回退对话
	rewindCodeOnly                     // 仅回退代码
	rewindCancel                       // 取消
)

// renderRewindSelectOverlay 渲染 rewind 消息选择覆盖层。
func (m *model) renderRewindSelectOverlay(boxWidth int) string {
	minWidth := 40                              // 最小内部宽度
	maxInnerWidth := boxWidth - 2 - 4           // border(2) + padding(4)

	// 计算最大总行数（含 box 边框和内边距 = 4 行）
	maxTotalLines := m.height - 2 // 留 2 行 margin
	if maxTotalLines < 10 {
		maxTotalLines = 10
	}

	// 构建所有消息行（含间距），同时计算最大原始行宽
	type msgLine struct {
		text     string
		isBlank  bool
		selected bool
	}
	var allMsgLines []msgLine
	maxRawLen := 0

	if len(m.rewindMessages) == 0 {
		text := m.msg().RewindNothingToRestore
		allMsgLines = append(allMsgLines, msgLine{text: text})
		if len(text) > maxRawLen {
			maxRawLen = len(text)
		}
	} else {
		for i, msg := range m.rewindMessages {
			line := msg.Content
			if msg.FilesChanged > 0 {
				line += "  " + fmt.Sprintf(m.msg().RewindFilesChanged, msg.FilesChanged)
				if msg.FileSummary != "" {
					line += "  " + msg.FileSummary
				}
			} else {
				line += "  " + m.msg().RewindNoCodeChanges
			}
			// 前缀宽度预留 2 字符（选中 ▌  或 未选中   ）
			fullLine := "  " + line // 占位，最大宽度计算用
			allMsgLines = append(allMsgLines, msgLine{text: line, selected: i == m.rewindSelectedIdx})
			if len(fullLine) > maxRawLen {
				maxRawLen = len(fullLine)
			}
			// 消息之间添加空行（最后一条不加）
			if i < len(m.rewindMessages)-1 {
				allMsgLines = append(allMsgLines, msgLine{isBlank: true})
			}
		}
	}

	// 自适应宽度
	titleText := m.msg().RewindTitle
	promptText := m.msg().RewindPrompt
	if len(titleText) > maxRawLen {
		maxRawLen = len(titleText)
	}
	if len(promptText) > maxRawLen {
		maxRawLen = len(promptText)
	}

	innerWidth := maxRawLen
	if innerWidth < minWidth {
		innerWidth = minWidth
	}
	if innerWidth > maxInnerWidth {
		innerWidth = maxInnerWidth
	}
	actualBoxWidth := innerWidth + 6

	// 消息行渲染辅助
	renderMsgLine := func(ml msgLine) string {
		if ml.isBlank {
			return ""
		}
		if ml.selected {
			bar := styleRewindBar.Render("▌")
			return bar + " " + styleRewindSelected.MaxWidth(innerWidth-2).Render(ml.text)
		}
		return "  " + styleRewindNormal.MaxWidth(innerWidth-2).Render(ml.text)
	}

	// 计算固定区域行数
	// header: title(1) + blank(1) + prompt(1) + blank(1) = 4
	headerLines := 4
	// footer: blank(1) + (current)(1) + blank(1) + hint(1) = 4
	footerLines := 4
	availMsgLines := maxTotalLines - headerLines - footerLines - 4 // 4 = box border/padding
	if availMsgLines < 1 {
		availMsgLines = 1
	}

	// 将 rewindScrollOffset（消息索引）映射到 allMsgLines 行偏移
	// 每条消息在 allMsgLines 中占 2 行（内容 + 空行），最后一条占 1 行
	msgLineOffset := m.rewindScrollOffset * 2
	totalMsgLines := len(allMsgLines)
	if msgLineOffset > totalMsgLines-availMsgLines {
		msgLineOffset = max(0, totalMsgLines-availMsgLines)
	}
	end := msgLineOffset + availMsgLines
	if end > totalMsgLines {
		end = totalMsgLines
		msgLineOffset = max(0, totalMsgLines-availMsgLines)
	}
	visibleMsgLines := allMsgLines[msgLineOffset:end]

	// 组装 overlay 内容
	var lines []string
	lines = append(lines,
		styleOverlayTitle.Width(innerWidth).Render(titleText),
		"",
		styleOverlayBody.Width(innerWidth).Render(promptText),
		"",
	)

	for _, ml := range visibleMsgLines {
		lines = append(lines, renderMsgLine(ml))
	}

	// 滚动指示器 或 普通尾部
	if len(m.rewindMessages) > 0 {
		needsScroll := msgLineOffset > 0 || end < totalMsgLines
		if needsScroll {
			indicator := ""
			if msgLineOffset > 0 {
				indicator = "↑ "
			}
			// 计算当前可见范围内的消息数
			visibleCount := 0
			for _, ml := range visibleMsgLines {
				if !ml.isBlank {
					visibleCount++
				}
			}
			indicator += fmt.Sprintf("%d-%d / %d",
				m.rewindScrollOffset+1,
				min(m.rewindScrollOffset+visibleCount, len(m.rewindMessages)),
				len(m.rewindMessages))
			if end < totalMsgLines {
				indicator += " ↓"
			}
			lines = append(lines, styleOverlayBody.MaxWidth(innerWidth).Render(indicator))
		} else {
			lines = append(lines, "")
		}
		lines = append(lines, styleOverlayBody.MaxWidth(innerWidth).Render(m.msg().RewindCurrent))
	}

	lines = append(lines, "")

	hint := renderOverlayHint(&m.help, innerWidth, []key.Binding{
		key.NewBinding(key.WithKeys("↑/↓"), key.WithHelp("↑/↓", m.msg().KeyNav)),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", m.msg().KeyConfirm)),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", m.msg().KeyCancel)),
	})
	lines = append(lines, hint)

	return renderOverlayBox(actualBoxWidth, m.overlayAnimFrame, strings.Join(lines, "\n"))
}

// renderRewindConfirmOverlay 渲染 rewind 确认覆盖层。
func (m *model) renderRewindConfirmOverlay(boxWidth int) string {
	minWidth := 40
	maxInnerWidth := boxWidth - 2 - 4

	// 找到目标消息的文本
	targetText := ""
	for _, msg := range m.rewindMessages {
		if msg.MessageID == m.rewindTargetMsgID {
			targetText = msg.Content
			break
		}
	}

	// 计算自适应宽度
	maxRawLen := len(m.msg().RewindConfirmTitle)
	if len(m.msg().RewindConfirmPrompt) > maxRawLen {
		maxRawLen = len(m.msg().RewindConfirmPrompt)
	}
	if len(targetText)+5 > maxRawLen { // "  │ " + targetText
		maxRawLen = len(targetText) + 5
	}
	options := []string{
		m.msg().RewindOptionBoth,
		m.msg().RewindOptionConv,
		m.msg().RewindOptionCode,
		m.msg().RewindOptionNeverMind,
	}
	for _, opt := range options {
		if len(opt)+2 > maxRawLen { // prefix 2 chars
			maxRawLen = len(opt) + 2
		}
	}
	warningText := "⚠ " + m.msg().RewindWarning
	if len(warningText) > maxRawLen {
		maxRawLen = len(warningText)
	}

	innerWidth := maxRawLen
	if innerWidth < minWidth {
		innerWidth = minWidth
	}
	if innerWidth > maxInnerWidth {
		innerWidth = maxInnerWidth
	}
	actualBoxWidth := innerWidth + 6

	title := styleOverlayTitle.Width(innerWidth).Render(m.msg().RewindConfirmTitle)

	var lines []string
	lines = append(lines, title, "")
	if targetText != "" {
		lines = append(lines, styleOverlayBody.Width(innerWidth).Render(m.msg().RewindConfirmPrompt))
		lines = append(lines, styleOverlayBody.MaxWidth(innerWidth).Render("  │ "+targetText))
		lines = append(lines, "")
	}

	for i, opt := range options {
		if i == int(m.rewindSelectedIdx) {
			bar := styleRewindBar.Render("▌")
			lines = append(lines, bar+" "+styleRewindSelected.MaxWidth(innerWidth-2).Render(opt))
		} else {
			lines = append(lines, "  "+styleRewindNormal.MaxWidth(innerWidth-2).Render(opt))
		}
	}

	lines = append(lines, "")
	lines = append(lines, styleOverlayBody.Width(innerWidth).Render(warningText))

	hint := renderOverlayHint(&m.help, innerWidth, []key.Binding{
		key.NewBinding(key.WithKeys("↑/↓"), key.WithHelp("↑/↓", m.msg().KeyNav)),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", m.msg().KeyConfirm)),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", m.msg().KeyCancel)),
	})
	lines = append(lines, "")
	lines = append(lines, hint)

	return renderOverlayBox(actualBoxWidth, m.overlayAnimFrame, strings.Join(lines, "\n"))
}
