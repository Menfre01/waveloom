package main

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/slashcommand"
)

// ---------------------------------------------------------------------------
// 类型定义
// ---------------------------------------------------------------------------

// themeItem 是主题选择器列表项。
type themeItem struct {
	label string
	mode  string
}

func (i themeItem) Title() string       { return i.label }
func (i themeItem) Description() string { return "" }
func (i themeItem) FilterValue() string { return i.label }

// modelPickerItem 是模型选择器列表项。
type modelPickerItem struct {
	modelID string
	ownedBy string
}

func (i modelPickerItem) Title() string       { return i.modelID }
func (i modelPickerItem) Description() string { return i.ownedBy }
func (i modelPickerItem) FilterValue() string { return i.modelID }

// commandPickerItem 是 slash 命令选择器列表项。
type commandPickerItem struct {
	name        string
	aliases     []string
	description string
	args        string // 参数占位符，如 "model"；无参数时为空
}

func (i commandPickerItem) Title() string {
	label := i.name
	if i.args != "" {
		label = i.name + " [" + i.args + "]"
	}
	if len(i.aliases) > 0 {
		label += " / " + strings.Join(i.aliases, " / ")
	}
	return "/" + label + " " + i.description
}
func (i commandPickerItem) Description() string { return "" }
func (i commandPickerItem) FilterValue() string { return i.name }

// localeItem 表示语言选择器的选项。
type localeItem struct {
	label  string
	locale Locale
}

func (i localeItem) Title() string       { return i.label }
func (i localeItem) Description() string { return "" }
func (i localeItem) FilterValue() string { return i.label }

// ---------------------------------------------------------------------------
// 命令选择器（/ 触发）
// ---------------------------------------------------------------------------

// activateCommandPicker 首次激活命令选择器。
func (m *model) activateCommandPicker() {
	m.commandPickerVisible = true
	m.commandPickerFilter = extractFilterAfterSlash(m.input.Value())
	m.commandPickerLastValue = m.input.Value()

	// 从 registry 获取命令列表
	m.commandPickerItems = m.slashRegistry.List()

	// 立即过滤
	m.updateCommandPickerFilter()
}

// closeCommandPicker 关闭命令选择器。
func (m *model) closeCommandPicker() {
	m.commandPickerVisible = false
	m.commandPickerDismissValue = m.input.Value()
	m.commandPickerLastValue = ""
	m.commandPickerItems = nil
}

// extractFilterAfterSlash 提取 / 之后的文本作为命令过滤条件。
func extractFilterAfterSlash(value string) string {
	if !strings.HasPrefix(value, "/") {
		return ""
	}
	return value[1:]
}

// updateCommandPickerFilter 根据当前输入过滤命令列表。
func (m *model) updateCommandPickerFilter() {
	m.commandPickerFilter = extractFilterAfterSlash(m.input.Value())

	filter := strings.ToLower(m.commandPickerFilter)
	if filter == "" {
		m.buildCommandPickerList(m.commandPickerItems)
		return
	}

	// 为每个命令计算最佳匹配：prefix 优先，然后按匹配位置（越左越优先）+ 字母序
	var matches []cmdMatch
	for _, cmd := range m.commandPickerItems {
		match := bestCommandMatch(filter, cmd)
		if match.position >= 0 {
			matches = append(matches, match)
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].isPrefix != matches[j].isPrefix {
			return matches[i].isPrefix
		}
		if matches[i].position != matches[j].position {
			return matches[i].position < matches[j].position
		}
		return matches[i].cmd.Name < matches[j].cmd.Name
	})

	filtered := make([]slashcommand.CommandInfo, len(matches))
	for i, m := range matches {
		filtered[i] = m.cmd
	}

	m.buildCommandPickerList(filtered)
}

// cmdMatch 是命令匹配的中间结果。
type cmdMatch struct {
	cmd      slashcommand.CommandInfo
	isPrefix bool
	position int
}

// bestCommandMatch 计算命令与 filter 的最佳匹配。
// position = -1 表示不匹配。
func bestCommandMatch(filter string, cmd slashcommand.CommandInfo) cmdMatch {
	m := cmdMatch{cmd: cmd, position: -1}

	check := func(s string) {
		sl := strings.ToLower(s)
		idx := strings.Index(sl, filter)
		if idx < 0 {
			return
		}
		isPrefix := idx == 0 && strings.HasPrefix(sl, filter)
		better := false
		if m.position < 0 {
			better = true
		} else if isPrefix && !m.isPrefix {
			better = true
		} else if isPrefix == m.isPrefix && idx < m.position {
			better = true
		}
		if better {
			m.isPrefix = isPrefix
			m.position = idx
		}
	}

	check(cmd.Name)
	for _, alias := range cmd.Aliases {
		check(alias)
	}
	return m
}

// buildCommandPickerList 从 CommandInfo 列表构建 bubbles/list 组件。
func (m *model) buildCommandPickerList(items []slashcommand.CommandInfo) {
	listItems := make([]list.Item, len(items))
	for i, cmd := range items {
		listItems[i] = commandPickerItem{name: cmd.Name, aliases: cmd.Aliases, description: cmd.Description, args: cmd.Args}
	}

	height := len(listItems)
	if height > 5 {
		height = 5
	}
	if height < 1 {
		height = 1
	}

	// 复用已有 list，仅更新 items + height
	if m.commandPickerList.Items() != nil {
		m.commandPickerList.SetItems(listItems)
		m.commandPickerList.SetSize(0, height)
		return
	}

	// 首次创建
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	delegate.Styles = listItemStyles()

	l := list.New(listItems, delegate, 0, height)
	l.SetShowTitle(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.KeyMap.Quit = key.NewBinding()
	l.KeyMap.ForceQuit = key.NewBinding()

	m.commandPickerList = l
	m.commandPickerDelegate = &delegate
}

// handleCommandPickerKey 处理命令选择器中的按键。
func (m *model) handleCommandPickerKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	keyStr := msg.String()

	switch keyStr {
	case "up", "down":
		var cmd tea.Cmd
		m.commandPickerList, cmd = m.commandPickerList.Update(msg)
		return true, cmd

	case "esc":
		m.closeCommandPicker()
		return true, nil

	case "enter":
		idx := m.commandPickerList.Index()
		if idx >= 0 && idx < len(m.commandPickerList.Items()) {
			item, ok := m.commandPickerList.Items()[idx].(commandPickerItem)
			if ok && item.args == "" {
				// 无参数命令：直接执行
				m.closeCommandPicker()
				return true, m.handleSlashCommand("/" + item.name)
			}
			m.commitCommandPickerSelection(idx)
			m.closeCommandPicker()
			return true, nil
		}
		// 无匹配项（如别名 /clear）：将当前输入作为 slash 命令执行
		m.closeCommandPicker()
		return true, m.handleSlashCommand(m.input.Value())

	case "tab":
		idx := m.commandPickerList.Index()
		if idx >= 0 && idx < len(m.commandPickerList.Items()) {
			m.completeCommandPickerFilter(idx)
		}
		return true, nil

	default:
		// 可打印字符 → 传给 input，Update() 中会触发 re-filter
		return false, nil
	}
}

// commitCommandPickerSelection 将选中命令回填到 textinput，关闭选择器。
func (m *model) commitCommandPickerSelection(idx int) {
	items := m.commandPickerList.Items()
	if idx < 0 || idx >= len(items) {
		return
	}
	item, ok := items[idx].(commandPickerItem)
	if !ok {
		return
	}
	// 替换 / 及其后的内容为 /{commandName} （保留空格以便用户输入参数）
	newValue := "/" + item.name + " "
	m.input.SetValue(newValue)
	m.input.CursorEnd()
}

// completeCommandPickerFilter 将选中命令名补全到 / 过滤器，保持选择器打开。
func (m *model) completeCommandPickerFilter(idx int) {
	items := m.commandPickerList.Items()
	if idx < 0 || idx >= len(items) {
		return
	}
	item, ok := items[idx].(commandPickerItem)
	if !ok {
		return
	}
	newValue := "/" + item.name
	m.input.SetValue(newValue)
	m.input.CursorEnd()
	// 更新过滤，保持选择器打开但只显示匹配项
	m.commandPickerFilter = item.name
	m.updateCommandPickerFilter()
	m.commandPickerLastValue = m.input.Value()
}

// renderCommandPickerDropdown 渲染命令选择器下拉列表。
func (m *model) renderCommandPickerDropdown(contentWidth int) string {
	if m.commandPickerList.Items() == nil || len(m.commandPickerList.Items()) == 0 {
		return ""
	}

	// 同步 list 宽度
	m.commandPickerList.SetSize(contentWidth-4, m.commandPickerList.Height())

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorHeaderAccent).
		Padding(1, 1).
		Width(contentWidth)

	return boxStyle.Render(m.commandPickerList.View())
}

// shouldActivateCommandPicker 检测输入框当前内容是否触发命令选择器。
// 条件: / 在行首，且 / 之后无空格（命令未完成）。
func shouldActivateCommandPicker(value string) bool {
	if !strings.HasPrefix(value, "/") {
		return false
	}
	afterSlash := value[1:]
	// / 之后不能已经包含空格（命令已完成，如 "/help" 整体提交或 "/model v4"）
	if strings.Contains(afterSlash, " ") {
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// 覆盖层 — 主题选择器
// ---------------------------------------------------------------------------

// themeItems 返回主题选择器的固定选项。label 为占位值，运行时由 buildThemeList 根据 locale 替换。
var themeItems = []themeItem{
	{label: "Auto", mode: "auto"},
	{label: "Dark", mode: "dark"},
	{label: "Light", mode: "light"},
	{label: "Dark CB", mode: "darkcolorblind"},
	{label: "Light CB", mode: "lightcolorblind"},
}

// buildThemeList 构建主题选择列表覆盖层。
func (m *model) buildThemeList() {
	items := make([]list.Item, len(themeItems))
	selectedIdx := 0
	for i, ti := range themeItems {
		label := ti.label
		if ti.mode == "auto" {
			label = m.msg().PickerThemeAuto
		}
		items[i] = themeItem{label: label, mode: ti.mode}
		if ti.mode == m.themeMode {
			selectedIdx = i
		}
	}

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	delegate.Styles = listItemStyles()
	m.themeDelegate = &delegate

	l := list.New(items, delegate, 0, len(items))
	l.SetShowTitle(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.KeyMap.Quit = key.NewBinding()
	l.KeyMap.ForceQuit = key.NewBinding()
	if selectedIdx < len(items) {
		l.Select(selectedIdx)
	}
	m.themeList = l
}

// handleThemePickerKey 处理主题选择器中的按键。
func (m *model) handleThemePickerKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	keyStr := msg.String()
	switch keyStr {
	case "up", "down":
		var cmd tea.Cmd
		m.themeList, cmd = m.themeList.Update(msg)
		return true, cmd
	case "enter":
		idx := m.themeList.Index()
		if idx >= 0 && idx < len(themeItems) {
			m.applyThemeMode(themeItems[idx].mode)
		}
		m.closeThemePicker()
		return true, nil
	case "esc":
		m.closeThemePicker()
		return true, nil
	}
	return false, nil
}

// applyThemeMode 应用指定主题模式并保存到 settings.json。
func (m *model) applyThemeMode(mode string) {
	m.themeMode = mode
	var p palette
	switch mode {
	case "dark":
		p = darkPalette
	case "light":
		p = lightPalette
	case "darkcolorblind":
		p = darkColorBlindPalette
	case "lightcolorblind":
		p = lightColorBlindPalette
	case "auto":
		if m.autoIsDark() {
			p = darkPalette
		} else {
			p = lightPalette
		}
	default:
		p = darkPalette
	}
	applyTheme(p)
	m.palette = p
	m.syncThemeComponents()
	// 落盘到 project settings.json
	if m.settingsStore != nil {
		if err := m.settingsStore.SaveTheme(mode); err != nil {
			slog.Warn("failed to save theme", "err", err)
		}
	}
}

func (m *model) closeThemePicker() {
	m.overlay = overlayNone
	m.input.Focus()
}

// ---------------------------------------------------------------------------
// 覆盖层 — 语言选择器
// ---------------------------------------------------------------------------

// buildLocaleList 构建语言选择列表覆盖层。
func (m *model) buildLocaleList() {
	lc := m.lc
	if lc == nil {
		lc = &enUS
	}
	items := []list.Item{
		localeItem{label: lc.SetupLocaleZhCNLabel, locale: LocaleZhCN},
		localeItem{label: lc.SetupLocaleEnUSLabel, locale: LocaleEnUS},
	}
	selectedIdx := 0
	currentLocale := LocaleEnUS
	if m.lc != nil {
		switch m.lc {
		case &zhCN:
			currentLocale = LocaleZhCN
		}
	}
	for i, li := range items {
		if li.(localeItem).locale == currentLocale {
			selectedIdx = i
		}
	}

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	delegate.Styles = listItemStyles()
	m.localeDelegate = &delegate

	l := list.New(items, delegate, 0, 2)
	l.SetShowTitle(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.KeyMap.Quit = key.NewBinding()
	l.KeyMap.ForceQuit = key.NewBinding()
	if selectedIdx < 2 {
		l.Select(selectedIdx)
	}
	m.localeList = l
}

// handleLocalePickerKey 处理语言选择器中的按键。
func (m *model) handleLocalePickerKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	keyStr := msg.String()
	switch keyStr {
	case "up", "down":
		var cmd tea.Cmd
		m.localeList, cmd = m.localeList.Update(msg)
		return true, cmd
	case "enter":
		if item, ok := m.localeList.SelectedItem().(localeItem); ok {
			m.applyLocale(item.locale)
		}
		m.closeLocalePicker()
		return true, nil
	case "esc":
		m.closeLocalePicker()
		return true, nil
	}
	return false, nil
}

// applyLocale 应用指定语言并保存到 settings.json。
func (m *model) applyLocale(loc Locale) {
	m.lc = messagesFor(loc)
	// 即时更新 input placeholder
	m.input.Placeholder = m.msg().InputPlaceholder
	m.otherInput.Placeholder = m.msg().InputOtherPlaceholder
	// 刷新 slash command 文案（共享 SlashMessages 指针原地更新）
	if m.slashMessages != nil {
		*m.slashMessages = *slashMessagesFrom(m.lc)
	}
	// 刷新 command picker 缓存（下次打开 / 时用新文案重建列表）
	m.commandPickerItems = nil
	if m.settingsStore != nil {
		if err := m.settingsStore.SaveLocale(string(loc)); err != nil {
			slog.Warn("failed to save locale", "err", err)
		}
	}
}

func (m *model) closeLocalePicker() {
	m.overlay = overlayNone
	m.input.Focus()
}

// ---------------------------------------------------------------------------
// 覆盖层 — 模型选择器
// ---------------------------------------------------------------------------

// buildModelPickerList 从 modelPickerItems 构建模型选择列表。
// 当前使用的模型（m.hudModel）在列表中高亮。
func (m *model) buildModelPickerList() {
	items := make([]list.Item, len(m.modelPickerItems))
	selectedIdx := 0
	for i, mi := range m.modelPickerItems {
		items[i] = modelPickerItem{modelID: mi.ID, ownedBy: mi.OwnedBy}
		if mi.ID == m.hudModel {
			selectedIdx = i
		}
	}

	height := len(items)
	if height > 5 {
		height = 5
	}
	if height < 1 {
		height = 1
	}

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	delegate.Styles = listItemStyles()
	m.modelPickerDelegate = &delegate

	l := list.New(items, delegate, 0, height)
	l.SetShowTitle(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.KeyMap.Quit = key.NewBinding()
	l.KeyMap.ForceQuit = key.NewBinding()
	if selectedIdx < height {
		l.Select(selectedIdx)
	}
	m.modelPickerList = l
}

// handleModelPickerKey 处理模型选择器中的按键。
func (m *model) handleModelPickerKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	keyStr := msg.String()
	switch keyStr {
	case "up", "down":
		var cmd tea.Cmd
		m.modelPickerList, cmd = m.modelPickerList.Update(msg)
		return true, cmd
	case "enter":
		idx := m.modelPickerList.Index()
		if idx >= 0 && idx < len(m.modelPickerItems) {
			m.commitModelSwitch(m.modelPickerItems[idx].ID)
		}
		m.closeModelPicker()
		return true, nil
	case "esc":
		m.closeModelPicker()
		return true, nil
	}
	return false, nil
}

// resolveThinkingEffort 从 LLM 配置中提取 thinking 档位。
// thinking 关闭时返回空字符串。
func resolveThinkingEffort(settings *llm.LLMSettings) string {
	if settings == nil || settings.ExtraParams == nil {
		return ""
	}
	if thinking, ok := settings.ExtraParams["thinking"].(map[string]any); ok {
		if t, ok := thinking["type"].(string); ok && t == "disabled" {
			return ""
		}
	}
	if effort, ok := settings.ExtraParams["reasoning_effort"].(string); ok {
		return effort
	}
	return "high"
}

// commitModelSwitch 确认模型切换：写 settings + 热替换。
func (m *model) commitModelSwitch(modelID string) {
	settings, err := m.settingsStore.LoadLLM()
	if err != nil {
		settings = &llm.LLMSettings{}
	}

	wasAdvisorMode := settings.IsAdvisorMode()

	settings.SetModel(modelID)
	if err := m.settingsStore.SaveLLM(settings); err != nil {
		slog.Warn("failed to save LLM settings", "err", err)
	}
	m.hudModel = normalizeWidth(modelID)
	m.hudThinkingEffort = resolveThinkingEffort(settings)
	m.reconfigureLLMClient(modelID)

	// 追加系统通知
	lc := m.msg()
	text := fmt.Sprintf(lc.SlashModelSwitched, modelID)
	if wasAdvisorMode {
		text += "\n" + lc.SlashModelAdvisorModeNotice
	}
	m.paras = append(m.paras, Paragraph{
		Type:      paraSystem,
		State:     stateDone,
		Text:      text,
		NotifKind: notifInfo,
	})
	m.trimParas()
	m.flushTranscript()
}

func (m *model) closeModelPicker() {
	m.overlay = overlayNone
	m.input.Focus()
}
