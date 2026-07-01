package main

import (
	"fmt"
	"strings"

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
)

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
	overlayFgStyle := lipgloss.NewStyle().Foreground(colorFooterFg).Width(innerWidth)

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
		contentLines = append(contentLines, overlayFgStyle.Render(wl))
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
				contentLines = append(contentLines, overlayFgStyle.Render(m.msg().PermReason+wl))
			} else {
				contentLines = append(contentLines, overlayFgStyle.Render("        "+wl))
			}
		}
	}
	contentLines = append(contentLines, "")

	// 拼接 list 组件，包裹 overlay 背景避免 list viewport 露底
	contentLines = append(contentLines, overlayContentStyle.Render(m.permList.View()))
	contentLines = append(contentLines, "")

	m.help.SetWidth(innerWidth) // 对齐内容区宽度
	hintWrapper := lipgloss.NewStyle().Foreground(colorMuted).Width(innerWidth)
	hint := hintWrapper.Render(m.help.ShortHelpView(permKeyBindings(m.msg())))
	contentLines = append(contentLines, hint)

	// 动态宽度：不超出可用空间
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorHeaderAccent).
		Padding(1, 2).
		Width(boxWidth)

	return boxStyle.Render(strings.Join(contentLines, "\n"))
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
	m.help.SetWidth(innerWidth)
	hintWrapper := lipgloss.NewStyle().Foreground(colorMuted).Width(innerWidth)
	hint := hintWrapper.Render(m.help.ShortHelpView(hintKeys))

	content := strings.Join([]string{title, "", body, "", hint}, "\n")

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorHeaderAccent).
		Padding(1, 2).
		Width(boxWidth)

	return boxStyle.Render(content)
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
	m.themeList.SetSize(innerWidth, 3)
	overlayFgStyle := lipgloss.NewStyle().Foreground(colorFooterFg).Width(innerWidth)

	title := styleOverlayTitle.Width(innerWidth).Render(m.msg().PickerSelectTheme)
	contentLines := []string{title, ""}

	// 高亮当前选择
	contentLines = append(contentLines, overlayFgStyle.Render(m.themeList.View()))
	contentLines = append(contentLines, "")

	m.help.SetWidth(innerWidth)
	hintWrapper := lipgloss.NewStyle().Foreground(colorMuted).Width(innerWidth)
	hint := hintWrapper.Render(m.help.ShortHelpView(themePickerKeyBindings(m.msg())))
	contentLines = append(contentLines, hint)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorHeaderAccent).
		Padding(1, 2).
		Width(boxWidth)

	return boxStyle.Render(strings.Join(contentLines, "\n"))
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
	overlayFgStyle := lipgloss.NewStyle().Foreground(colorFooterFg).Width(innerWidth)

	title := styleOverlayTitle.Width(innerWidth).Render(m.msg().PickerSelectModel)
	contentLines := []string{title, ""}

	contentLines = append(contentLines, overlayFgStyle.Render(m.modelPickerList.View()))
	contentLines = append(contentLines, "")

	m.help.SetWidth(innerWidth)
	hintWrapper := lipgloss.NewStyle().Foreground(colorMuted).Width(innerWidth)
	hint := hintWrapper.Render(m.help.ShortHelpView(modelPickerKeyBindings(m.msg())))
	contentLines = append(contentLines, hint)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorHeaderAccent).
		Padding(1, 2).
		Width(boxWidth)

	return boxStyle.Render(strings.Join(contentLines, "\n"))
}

// ---------------------------------------------------------------------------
// 语言选择器覆盖层渲染
// ---------------------------------------------------------------------------

func (m *model) renderLocalePickerOverlay(boxWidth int) string {
	innerWidth := boxWidth - 2 - 4
	m.localeList.SetSize(innerWidth, 2)
	overlayFgStyle := lipgloss.NewStyle().Foreground(colorFooterFg).Width(innerWidth)

	title := styleOverlayTitle.Width(innerWidth).Render(m.msg().PickerSelectLocale)
	contentLines := []string{title, ""}

	contentLines = append(contentLines, overlayFgStyle.Render(m.localeList.View()))
	contentLines = append(contentLines, "")

	m.help.SetWidth(innerWidth)
	hintWrapper := lipgloss.NewStyle().Foreground(colorMuted).Width(innerWidth)
	hint := hintWrapper.Render(m.help.ShortHelpView(localePickerKeyBindings(m.msg())))
	contentLines = append(contentLines, hint)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorHeaderAccent).
		Padding(1, 2).
		Width(boxWidth)

	return boxStyle.Render(strings.Join(contentLines, "\n"))
}
