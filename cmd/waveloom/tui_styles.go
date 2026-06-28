package main

import (
	"image/color"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
)

// ---------------------------------------------------------------------------
// palette — 语义色集合
// ---------------------------------------------------------------------------

// palette 是主题颜色的完整定义。所有颜色变量和 style 变量都由主题初始化。
type palette struct {
	// 语义色
	User  color.Color // > 用户前缀蓝
	OK    color.Color // 完成态绿
	Error color.Color // 失败态红
	Gray  color.Color // 次要文字/thought 灰
	Muted color.Color // 更暗一级/折叠摘要

	// 工具输出内颜色
	DiffAdd   color.Color
	DiffDel   color.Color
	DiffAddBg color.Color // + 行背景
	DiffDelBg color.Color // - 行背景

	// 布局专用色
	HeaderFg     color.Color
	HeaderAccent color.Color
	FooterFg     color.Color
	FooterValue  color.Color // HUD 数值色（比 HeaderFg 对比度更高）

	// 强调色（系统提示、中间态警示共用）
	AccentGold color.Color

	// 工具输出代码
	ToolCode   color.Color
	ToolCodeBg color.Color

	// Logo 渐变色（6 行 ASCII art）
	LogoGradient [6]color.Color

	// Glamour markdown 风格名（"dark" / "light"）
	GlamourStyle string
}

// darkPalette 深色主题色值。
var darkPalette = palette{
	User:  lipgloss.Color("#5fafff"),
	OK:    lipgloss.Color("#5faf5f"),
	Error: lipgloss.Color("#d75f5f"),
	Gray:  lipgloss.Color("#777777"),
	Muted: lipgloss.Color("#6a6a6a"),

	DiffAdd:   lipgloss.Color("#8cc250"),
	DiffDel:   lipgloss.Color("#fa5555"),
	DiffAddBg: lipgloss.Color("#1a240a"),
	DiffDelBg: lipgloss.Color("#240a0a"),

	HeaderFg:     lipgloss.Color("#e0e0e0"),
	HeaderAccent: lipgloss.Color("#5fafd7"),
	FooterFg:     lipgloss.Color("#a0a0a0"),
	FooterValue:  lipgloss.Color("#f2f2f2"),

	AccentGold: lipgloss.Color("#d7af5f"),

	ToolCode:   lipgloss.Color("#d7875f"),
	ToolCodeBg: lipgloss.Color("#3a3a3a"),

	LogoGradient: [6]color.Color{
		lipgloss.Color("#6366f1"),
		lipgloss.Color("#818cf8"),
		lipgloss.Color("#06b6d4"),
		lipgloss.Color("#22d3ee"),
		lipgloss.Color("#a78bfa"),
		lipgloss.Color("#a0a0c0"),
	},

	GlamourStyle: "dark",
}

// lightPalette 浅色主题色值。
var lightPalette = palette{
	User:  lipgloss.Color("#0066cc"),
	OK:    lipgloss.Color("#2d7d2d"),
	Error: lipgloss.Color("#c0392b"),
	Gray:  lipgloss.Color("#666666"),
	Muted: lipgloss.Color("#777777"),

	DiffAdd:   lipgloss.Color("#3a7a00"),
	DiffDel:   lipgloss.Color("#d1242f"),
	DiffAddBg: lipgloss.Color("#d0f0c8"),
	DiffDelBg: lipgloss.Color("#ffebe9"),

	HeaderFg:     lipgloss.Color("#1a1a1a"),
	HeaderAccent: lipgloss.Color("#2d6fa0"),
	FooterFg:     lipgloss.Color("#666666"),
	FooterValue:  lipgloss.Color("#0d0d0d"),

	AccentGold: lipgloss.Color("#b8860b"),

	ToolCode:   lipgloss.Color("#a0522d"),
	ToolCodeBg: lipgloss.Color("#e8e8e8"),

	LogoGradient: [6]color.Color{
		lipgloss.Color("#4f46e5"),
		lipgloss.Color("#6366f1"),
		lipgloss.Color("#0891b2"),
		lipgloss.Color("#06b6d4"),
		lipgloss.Color("#7c3aed"),
		lipgloss.Color("#6b7280"),
	},

	GlamourStyle: "light",
}

// ---------------------------------------------------------------------------
// 可设置的全局变量（由 applyTheme 初始化）
// ---------------------------------------------------------------------------

var (
	colorUser  color.Color
	colorOK    color.Color
	colorErr   color.Color
	colorGray  color.Color
	colorMuted color.Color

	colorDiffAdd   color.Color
	colorDiffDel   color.Color
	colorDiffAddBg color.Color
	colorDiffDelBg color.Color

	colorHeaderFg     color.Color
	colorHeaderAccent color.Color
	colorFooterFg     color.Color
	colorFooterValue  color.Color

	colorAccentGold color.Color

	colorToolCode   color.Color
	colorToolCodeBg color.Color

	// Logo 渐变色（6 行，由 palette 注入）
	colorLogoGradient [6]color.Color
)

var (
	styleUserPrefix        lipgloss.Style
	styleThoughtStreaming  lipgloss.Style
	styleThoughtCollapsed  lipgloss.Style
	styleThoughtContent    lipgloss.Style
	styleThoughtExpandHint lipgloss.Style
	styleToolPrefixDone    lipgloss.Style
	styleToolPrefixErr     lipgloss.Style
	styleHeader            lipgloss.Style
	styleHeaderAccent      lipgloss.Style
	styleFooter            lipgloss.Style
	styleApp               lipgloss.Style
	styleInput             lipgloss.Style
	styleOverlayTitle      lipgloss.Style
	styleOverlayHint       lipgloss.Style
	styleMDCode            lipgloss.Style
	styleDiffAdd           lipgloss.Style
	styleDiffDel           lipgloss.Style
	styleDiffAddBG         lipgloss.Style // + 行背景
	styleDiffDelBG         lipgloss.Style // - 行背景
	styleDiffCtx           lipgloss.Style // diff 上下文行
	styleDiffHeader        lipgloss.Style // hunk 头 @@ ... @@
	styleLineNum           lipgloss.Style // 行号列
	styleToolPreview       lipgloss.Style
	styleToolPreviewHint   lipgloss.Style
	styleToolExpanded      lipgloss.Style
	styleMuted             lipgloss.Style
	styleFooterModel       lipgloss.Style
	styleFooterLabel       lipgloss.Style
	styleFooterValue       lipgloss.Style
	styleFooterValueMuted  lipgloss.Style
	styleFooterLatGold     lipgloss.Style
	styleFooterLatRed      lipgloss.Style
	styleCtxBarGreenFg     lipgloss.Style
	styleCtxBarGoldFg      lipgloss.Style
	styleCtxBarRedFg       lipgloss.Style
	styleCacheGreen        lipgloss.Style
	styleCacheGold         lipgloss.Style
	styleSystemInfo   lipgloss.Style // 系统通知：完成/中断
	styleSystemWarn   lipgloss.Style // 系统通知：警告
	styleSystemError  lipgloss.Style // 系统通知：错误
	styleSystemPrefixInfo  lipgloss.Style
	styleSystemPrefixWarn  lipgloss.Style
	styleSystemPrefixError lipgloss.Style
	styleToolArgs          lipgloss.Style
	styleAsstPrefixDone     lipgloss.Style
	styleThoughtPrefixDone  lipgloss.Style
	styleFocusIndicator     lipgloss.Style
)

// ---------------------------------------------------------------------------
// applyTheme 初始化所有全局颜色和样式变量
// ---------------------------------------------------------------------------

func applyTheme(p palette) {
	// 颜色
	colorUser = p.User
	colorOK = p.OK
	colorErr = p.Error
	colorGray = p.Gray
	colorMuted = p.Muted
	colorDiffAdd = p.DiffAdd
	colorDiffDel = p.DiffDel
	colorDiffAddBg = p.DiffAddBg
	colorDiffDelBg = p.DiffDelBg
	colorHeaderFg = p.HeaderFg
	colorHeaderAccent = p.HeaderAccent
	colorFooterFg = p.FooterFg
	colorFooterValue = p.FooterValue
	colorAccentGold = p.AccentGold
	colorToolCode = p.ToolCode
	colorToolCodeBg = p.ToolCodeBg

	// Logo 渐变色
	colorLogoGradient = p.LogoGradient

	// 前缀样式
	styleUserPrefix = lipgloss.NewStyle().Foreground(colorUser).Bold(true)
	styleThoughtStreaming = lipgloss.NewStyle().Foreground(colorGray).Italic(true)
	styleThoughtCollapsed = lipgloss.NewStyle().Foreground(colorGray).Italic(true)
	styleThoughtContent = lipgloss.NewStyle().Foreground(colorMuted).Italic(true)
	styleThoughtExpandHint = lipgloss.NewStyle().Foreground(colorGray).Italic(true)
	styleToolPrefixDone = lipgloss.NewStyle().Foreground(colorOK).Bold(true)
	styleToolPrefixErr = lipgloss.NewStyle().Foreground(colorErr).Bold(true)

	// 布局样式
	styleHeader = lipgloss.NewStyle().Foreground(colorHeaderFg).Width(0)
	styleHeaderAccent = lipgloss.NewStyle().Foreground(colorHeaderAccent).Bold(true)
	styleFooter = lipgloss.NewStyle().Foreground(colorFooterFg).Width(0)
	styleApp = lipgloss.NewStyle().Padding(1, 2, 0, 2) // top(1) right(2) bottom(0) left(2)
	styleInput = lipgloss.NewStyle().Width(0)

	// 覆盖层样式
	styleOverlayTitle = lipgloss.NewStyle().Foreground(colorHeaderAccent).Bold(true)
	styleOverlayHint = lipgloss.NewStyle().Foreground(colorMuted)

	// 工具输出代码样式
	styleMDCode = lipgloss.NewStyle().Foreground(colorToolCode).Background(colorToolCodeBg)

	// diff 着色样式（预定义，避免热路径重复 NewStyle）
	styleDiffAdd = lipgloss.NewStyle().Foreground(colorDiffAdd)
	styleDiffDel = lipgloss.NewStyle().Foreground(colorDiffDel)
	styleDiffAddBG = lipgloss.NewStyle().Foreground(colorDiffAdd).Background(colorDiffAddBg)
	styleDiffDelBG = lipgloss.NewStyle().Foreground(colorDiffDel).Background(colorDiffDelBg)
	styleDiffCtx = lipgloss.NewStyle().Foreground(colorMuted)
	styleDiffHeader = lipgloss.NewStyle().Foreground(colorHeaderAccent)
	styleLineNum = lipgloss.NewStyle().Foreground(colorGray)

	// 工具输出预览样式
	styleToolPreview = lipgloss.NewStyle().Foreground(colorMuted)
	styleToolPreviewHint = lipgloss.NewStyle().Foreground(colorFooterFg).Italic(true)
	styleToolExpanded = lipgloss.NewStyle().Foreground(colorMuted)
	styleMuted = lipgloss.NewStyle().Foreground(colorMuted)

	// Footer HUD 子样式
	styleFooterModel = lipgloss.NewStyle().Foreground(colorAccentGold).Bold(true)
	styleFooterLabel = lipgloss.NewStyle().Foreground(colorGray)
	styleFooterValue = lipgloss.NewStyle().Foreground(colorFooterValue)
	styleFooterValueMuted = lipgloss.NewStyle().Foreground(colorGray)
	styleFooterLatGold = lipgloss.NewStyle().Foreground(colorAccentGold)
	styleFooterLatRed = lipgloss.NewStyle().Foreground(colorErr)

	// 上下文进度条百分比文字（仅前景色，无背景）
	styleCtxBarGreenFg = lipgloss.NewStyle().Foreground(colorOK)
	styleCtxBarGoldFg = lipgloss.NewStyle().Foreground(colorAccentGold)
	styleCtxBarRedFg = lipgloss.NewStyle().Foreground(colorErr)

	// 缓存着色
	styleCacheGreen = lipgloss.NewStyle().Foreground(colorOK)
	styleCacheGold = lipgloss.NewStyle().Foreground(colorAccentGold)

	// 系统提示段落 — 按通知类型着色
	styleSystemInfo  = lipgloss.NewStyle().Foreground(colorGray)
	styleSystemWarn  = lipgloss.NewStyle().Foreground(colorAccentGold)
	styleSystemError = lipgloss.NewStyle().Foreground(colorErr)

	styleSystemPrefixInfo  = lipgloss.NewStyle().Foreground(colorGray).Bold(true)
	styleSystemPrefixWarn  = lipgloss.NewStyle().Foreground(colorAccentGold).Bold(true)
	styleSystemPrefixError = lipgloss.NewStyle().Foreground(colorErr).Bold(true)

	// 工具参数代码色（仅前景，行内使用不设背景）
	styleToolArgs = lipgloss.NewStyle().Foreground(colorToolCode)

	// 前缀符号预定义样式（热路径，避免每次渲染 NewStyle）
	styleAsstPrefixDone = lipgloss.NewStyle().Foreground(colorGray)
	styleThoughtPrefixDone = lipgloss.NewStyle().Foreground(colorGray)
	styleFocusIndicator = lipgloss.NewStyle().Foreground(colorAccentGold).Bold(true)
}

// ---------------------------------------------------------------------------
// 列表组件样式（权限面板 / 文件选择器共用）
// ---------------------------------------------------------------------------

// listItemStyles 返回基于当前主题色的 list.DefaultItemStyles。
// 选中项使用 colorOK（绿）前景 + colorHeaderAccent 左侧边框，与覆盖层视觉统一。
func listItemStyles() list.DefaultItemStyles {
	return list.DefaultItemStyles{
		NormalTitle: lipgloss.NewStyle().
			Foreground(colorHeaderFg).
			Padding(0, 0, 0, 2),
		NormalDesc: lipgloss.NewStyle().
			Foreground(colorFooterFg).
			Padding(0, 0, 0, 2),
		SelectedTitle: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(colorHeaderAccent).
			Foreground(colorOK).
			Bold(true).
			Padding(0, 0, 0, 1),
		SelectedDesc: lipgloss.NewStyle().
			Foreground(colorFooterFg).
			Padding(0, 0, 0, 1),
		DimmedTitle: lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 0, 0, 2),
		DimmedDesc: lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 0, 0, 2),
		FilterMatch: lipgloss.NewStyle().Underline(true),
	}
}

// ---------------------------------------------------------------------------
// 前缀渲染辅助函数（spinner 驱动动画）
// ---------------------------------------------------------------------------

// systemPrefix 返回系统 ◼ 前缀，按通知类型着色。
func systemPrefix(kind systemNotifKind) string {
	switch kind {
	case notifWarn:
		return styleSystemPrefixWarn.Render("◼")
	case notifError:
		return styleSystemPrefixError.Render("◼")
	default:
		return styleSystemPrefixInfo.Render("◼")
	}
}

// systemTextStyle 返回系统通知文本的样式，按通知类型着色。
func systemTextStyle(kind systemNotifKind) lipgloss.Style {
	switch kind {
	case notifWarn:
		return styleSystemWarn
	case notifError:
		return styleSystemError
	default:
		return styleSystemInfo
	}
}

// userPrefix 返回蓝色的小右箭号前缀。
func userPrefix() string {
	return styleUserPrefix.Render("›")
}

// asstPrefix 返回 assistant 前缀。流式时返回 spinner 动画字符，完成后返回静态灰色 · 占位。
func asstPrefix(sp spinner.Model, streaming bool) string {
	if streaming {
		return sp.View()
	}
	return styleAsstPrefixDone.Render("·")
}

// thoughtPrefix 返回 thought 前缀。流式时返回 spinner 动画字符，完成后返回静态灰色 · 占位。
func thoughtPrefix(sp spinner.Model, streaming bool) string {
	if streaming {
		return sp.View()
	}
	return styleThoughtPrefixDone.Render("·")
}

// toolPrefix 返回 tool 前缀。执行中使用 spinner 动画，完成/失败为静态 `•`。
func toolPrefix(sp spinner.Model, state paraStateEnum) string {
	switch state {
	case stateStreaming:
		return sp.View()
	case stateDone:
		return styleToolPrefixDone.Render("•")
	case stateError:
		return styleToolPrefixErr.Render("•")
	default:
		return styleToolPrefixDone.Render("•")
	}
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
