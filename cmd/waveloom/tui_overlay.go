package main

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// ---------------------------------------------------------------------------
// Overlay 类型
// ---------------------------------------------------------------------------

// Overlay 表示当前活跃的覆盖层。
type Overlay int

const (
	overlayNone       Overlay = iota // 无覆盖层
	overlayPermission                // 权限确认框（阻断式）
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

	title := styleOverlayTitle.Width(innerWidth).Render("▲ Permission Required")

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
		wrapWidth := innerWidth - 8 // "Reason: " 前缀宽度
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
				contentLines = append(contentLines, overlayFgStyle.Render("Reason: "+wl))
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
	hint := hintWrapper.Render(m.help.ShortHelpView(permKeys))
	contentLines = append(contentLines, hint)

	// 动态宽度：不超出可用空间
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorHeaderAccent).
		Padding(1, 2).
		Width(boxWidth)

	return boxStyle.Render(strings.Join(contentLines, "\n"))
}
