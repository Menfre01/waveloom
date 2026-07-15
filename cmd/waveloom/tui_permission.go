package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/Menfre01/waveloom/pkg/permission"
)

// ---------------------------------------------------------------------------
// 权限/问题消息类型
// ---------------------------------------------------------------------------

// permissionReqMsg 权限确认请求。
type permissionReqMsg struct {
	toolName   string
	args       string
	reason     string
	reasonKind permission.DecisionReason
	reply      chan<- permission.UserChoice
}

// questionReqMsg AskUserQuestion 请求。
type questionReqMsg struct {
	questions []permission.QuestionPrompt
	reply     chan<- []permission.QuestionResponse
}

// planEnterReqMsg 进入 plan 模式确认请求。
type planEnterReqMsg struct {
	reply chan<- bool
}

// planExitReqMsg 退出 plan 模式审批请求（含 plan 内容）。
type planExitReqMsg struct {
	plan  string
	reply chan<- permission.PlanApproval
}

// enterPlanModeByUserMsg 用户通过 Shift+Tab 主动进入 plan 模式的消息。
type enterPlanModeByUserMsg struct{}

// exitPlanModeByUserMsg 用户通过审批界面批准退出 plan 模式的消息。
type exitPlanModeByUserMsg struct{}

// overlayAnimTickMsg 覆盖层动画帧推进（~50ms tick），用于淡入效果。
type overlayAnimTickMsg struct{}

// ---------------------------------------------------------------------------
// 权限面板
// ---------------------------------------------------------------------------

// permItem 是权限面板列表项，实现 list.DefaultItem 接口。
type permItem struct {
	title       string
	description string
	choice      permChoice
}

func (i permItem) Title() string       { return i.title }
func (i permItem) Description() string { return i.description }
func (i permItem) FilterValue() string { return i.title }

// handlePermKey 处理权限确认框内的按键。返回 (handled, cmd)。
// ↑/↓ 由 list 组件内部处理；Enter / Esc 在此拦截。
func (m *model) handlePermKey(key string) (bool, tea.Cmd) {
	switch key {
	case "enter":
		if m.permReq != nil {
			m.permReq.reply <- m.permListChoice()
		}
		m.overlay = overlayNone
		m.permReq = nil
		m.input.Focus()
		return true, nil

	case "esc":
		// Esc = Deny
		if m.permReq != nil {
			m.permReq.reply <- permission.UserChoice{Decision: permission.DecisionDeny}
		}
		m.overlay = overlayNone
		m.permReq = nil
		m.input.Focus()
		return true, nil
	}

	// 未处理的按键忽略
	return false, nil
}

// buildPermList 构建权限确认选项列表。
func (m *model) buildPermList() list.Model {
	items := []list.Item{
		permItem{title: m.msg().PermAllow, choice: permAllow},
		permItem{title: m.msg().PermAllowAll, choice: permAllowAll},
		permItem{title: m.msg().PermDeny, choice: permDeny},
	}

	delegate := list.NewDefaultDelegate()
	// 单行列表，不显示 description，无行距
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	delegate.Styles = listItemStyles()
	m.permDelegate = &delegate

	l := list.New(items, m.permDelegate, 0, 3)
	l.SetShowTitle(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	// 禁用 list 内置的 enter/esc 处理，由 TUI 层统一拦截
	l.KeyMap.Quit = key.NewBinding()
	l.KeyMap.ForceQuit = key.NewBinding()

	return l
}

// permListChoice 将 list 当前选中项转换为 UserChoice。
func (m *model) permListChoice() permission.UserChoice {
	item, ok := m.permList.SelectedItem().(permItem)
	if !ok {
		return permission.UserChoice{Decision: permission.DecisionDeny}
	}
	switch item.choice {
	case permAllow:
		return permission.UserChoice{Decision: permission.DecisionAllow}
	case permAllowAll:
		return permission.UserChoice{
			Decision:      permission.DecisionAllow,
			RememberScope: permission.ScopeConfig,
		}
	case permDeny:
		return permission.UserChoice{Decision: permission.DecisionDeny}
	default:
		return permission.UserChoice{Decision: permission.DecisionDeny}
	}
}

// ---------------------------------------------------------------------------
// AskUserQuestion 面板处理（huh 表单）
// ---------------------------------------------------------------------------

const otherOptionKey = "___other___"

func (m *model) overlayInnerWidth() int {
	boxWidth := max(m.width-4, 20)
	return boxWidth - 2 - 4 // border(左右各1) + padding(左右各2)
}

// overlayMaxFormHeight 返回 huh 表单在 overlay 内的自适应最大高度。
// 选项少时紧凑显示全部，选项多时撑开到合理上限，超出由 huh 内部滚动。
func (m *model) overlayMaxFormHeight(optionCount int) int {
	// 固定外壳开销 = styleApp padding(1) + header(8) + footer(2) + input(2) + separator(1) + overlay chrome(8) = 22
	// 简化：header + 底部固定 + overlay box 开销
	const (
		overlayFormTopOverhead    = 1 // styleApp padding
		overlayFormHeaderOverhead = 8 // header 区域
		overlayFormBottomOverhead = 5 // 底部区域（footer + input + separator）
		overlayFormChromeOverhead = 8 // overlay 边框 + padding + title + hint
	)
	fixedOverhead := overlayFormTopOverhead + overlayFormHeaderOverhead + overlayFormBottomOverhead + overlayFormChromeOverhead
	maxAvailable := m.height - fixedOverhead
	if maxAvailable < 5 {
		maxAvailable = 5
	}
	// 所需高度：每个选项约 1 行 + 标题 2 行 + 过滤栏 1 行
	needed := optionCount + 3
	if needed < 5 {
		needed = 5
	}
	// 取 needed 和 maxAvailable 的较小值，但不超过 20 行（约 15+ 选项可见，不侵占聊天区）
	const overlayFormAbsoluteMax = 20
	h := min(needed, maxAvailable)
	if h > overlayFormAbsoluteMax {
		h = overlayFormAbsoluteMax
	}
	return h
}
func themeWaveloom() huh.Theme {
	return huh.ThemeFunc(func(isDark bool) *huh.Styles {
		t := huh.ThemeBase(isDark)
		// 聚焦字段：与权限面板统一 —— 普通左边框 + colorOK 前景，无 ">" 选择符
		t.Focused.Title = t.Focused.Title.Foreground(colorHeaderAccent)
		t.Focused.Description = t.Focused.Description.Foreground(colorFooterFg)
		t.Focused.Base = lipgloss.NewStyle().
			PaddingLeft(1).
			BorderStyle(lipgloss.NormalBorder()).
			BorderLeft(true).
			BorderForeground(colorHeaderAccent)
		// 单选：使用 "▌" 选择符，与权限面板左边框视觉一致
		t.Focused.SelectSelector = lipgloss.NewStyle().Foreground(colorOK).SetString("▌ ")
		t.Blurred.SelectSelector = lipgloss.NewStyle().Foreground(colorMuted).SetString("  ")
		// 多选：光标指示器与单选统一 "▌"，选择状态用 [✓] / [ ]
		t.Focused.MultiSelectSelector = lipgloss.NewStyle().Foreground(colorOK).SetString("▌ ")
		t.Blurred.MultiSelectSelector = lipgloss.NewStyle().Foreground(colorMuted).SetString("  ")
		t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(colorOK).SetString("[✓] ")
		t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(colorMuted).SetString("[ ] ")
		t.Blurred.SelectedPrefix = lipgloss.NewStyle().Foreground(colorMuted).SetString("[✓] ")
		t.Blurred.UnselectedPrefix = lipgloss.NewStyle().Foreground(colorMuted).SetString("[ ] ")
		t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(colorOK)
		t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(colorOK)
		// 未聚焦字段
		t.Blurred.Title = t.Blurred.Title.Foreground(colorHeaderFg)
		t.Blurred.Description = t.Blurred.Description.Foreground(colorFooterFg)
		t.Blurred.Base = t.Blurred.Base.BorderForeground(colorMuted)
		return t
	})
}

// buildQuestionForm 使用 huh 构建当前问题的表单。
func (m *model) buildQuestionForm() {
	if m.questionReq == nil || m.questionIdx >= len(m.questionReq.questions) {
		return
	}
	q := m.questionReq.questions[m.questionIdx]

	// 构建选项列表（含 "Other..."）
	opts := make([]huh.Option[string], len(q.Options)+1)
	for i, opt := range q.Options {
		key := opt.Label
		if strings.HasSuffix(opt.Label, "(Recommended)") {
			key = "★ " + opt.Label
		}
		opts[i] = huh.NewOption(key, opt.Label)
	}
	opts[len(q.Options)] = huh.NewOption(m.msg().QuestionOtherOption, otherOptionKey)

	theme := themeWaveloom()
	formWidth := m.overlayInnerWidth()
	optionCount := len(opts)
	formMaxHeight := m.overlayMaxFormHeight(optionCount)
	m.questionFormMaxHeight = formMaxHeight

	if q.MultiSelect {
		var selected []string
		field := huh.NewMultiSelect[string]().
			Key("answer").
			Title(q.Question).
			Options(opts...).
			Value(&selected).
			WithTheme(theme).
			WithHeight(formMaxHeight)

		f := huh.NewForm(huh.NewGroup(field)).
			WithTheme(theme).
			WithWidth(formWidth).
			WithShowHelp(false)

		m.questionForm = f
		// 表单在 WindowSizeMsg 之后动态创建，需手动注入尺寸让其计算视口高度。
		// 使用 overlayMaxFormHeight 而非终端全高 m.height，避免视口计算过大导致选项被裁剪。
		m.questionFormInitCmd = tea.Batch(
			f.Init(),
			func() tea.Msg { return tea.WindowSizeMsg{Width: formWidth, Height: formMaxHeight} },
		)
	} else {
		var selected string
		field := huh.NewSelect[string]().
			Key("answer").
			Title(q.Question).
			Options(opts...).
			Value(&selected).
			WithTheme(theme).
			WithHeight(formMaxHeight)

		f := huh.NewForm(huh.NewGroup(field)).
			WithTheme(theme).
			WithWidth(formWidth).
			WithShowHelp(false)

		m.questionForm = f
		m.questionFormInitCmd = tea.Batch(
			f.Init(),
			func() tea.Msg { return tea.WindowSizeMsg{Width: formWidth, Height: formMaxHeight} },
		)
	}
}

// buildOtherForm 构建 "Other" 自定义文本输入（使用 real cursor textinput，与主输入框一致）。
func (m *model) buildOtherForm() {
	m.questionFormIsOther = true
	m.questionForm = nil
	m.questionFormInitCmd = nil
	m.otherInputVisStart = 0
	m.otherInputLastValue = ""
	m.otherInput.SetValue("")
	// 宽度对齐盒子内部可用宽度：innerWidth - prompt（与 huh 表单宽度一致）
	inputWidth := m.overlayInnerWidth() - lipgloss.Width(m.otherInput.Prompt)
	if inputWidth < 10 {
		inputWidth = 10
	}
	m.otherInput.SetWidth(inputWidth)
	// 延迟 Focus：Init 命令在下一帧执行，避免同一帧内 Focus + SetValue 状态不一致
	m.questionFormInitCmd = m.otherInput.Focus()
}

// handleQuestionFormComplete 在 huh 表单完成时调用，提取答案并推进流程。
func (m *model) handleQuestionFormComplete() {
	if m.questionReq == nil || m.questionIdx >= len(m.questionReq.questions) {
		return
	}
	q := m.questionReq.questions[m.questionIdx]

	answerValue := m.questionForm.Get("answer")

	if q.MultiSelect {
		// 多选结果
		selected := answerValue.([]string)
		var hasOther bool
		var finalAnswers []string
		for _, v := range selected {
			if v == otherOptionKey {
				hasOther = true
			} else {
				finalAnswers = append(finalAnswers, v)
			}
		}
		if hasOther {
			m.questionPendingOther = true
			m.questionPendingAnswers = finalAnswers
			m.buildOtherForm()
			return
		}
		m.recordQuestionAnswer(finalAnswers)
	} else {
		// 单选结果
		selected := answerValue.(string)
		if selected == otherOptionKey {
			m.questionPendingOther = true
			m.questionPendingAnswers = nil
			m.buildOtherForm()
			return
		}
		m.recordQuestionAnswer([]string{selected})
	}
	m.advanceQuestion()
}

// handleQuestionFormAborted 在用户取消表单（Esc）时调用。
func (m *model) handleQuestionFormAborted() {
	// 拒绝回答
	if m.questionReq != nil && m.questionReq.reply != nil {
		m.questionReq.reply <- nil
	}
	m.closeQuestionOverlay()
}

// handleOtherInputSubmit 在用户按 Enter 提交 Other 自定义文本时调用。
func (m *model) handleOtherInputSubmit() {
	text := m.otherInput.Value()
	m.questionFormIsOther = false
	m.otherInput.Blur()

	// 将 Other 文本合并到 pending answers，由 advanceQuestion 统一记录，避免重复响应覆盖
	if m.questionPendingAnswers == nil {
		m.questionPendingAnswers = []string{"Other: " + text}
	} else {
		m.questionPendingAnswers = append(m.questionPendingAnswers, "Other: "+text)
	}
	m.advanceQuestion()
}

// handleOtherInputCancel 在用户按 Esc 取消 Other 自定义文本时调用，回退到选项列表。
func (m *model) handleOtherInputCancel() {
	m.questionFormIsOther = false
	m.questionPendingOther = false
	m.questionPendingAnswers = nil
	m.otherInput.Blur()
	m.buildQuestionForm()
}

// recordQuestionAnswer 记录当前问题的答案。
func (m *model) recordQuestionAnswer(answers []string) {
	if m.questionReq == nil || m.questionIdx >= len(m.questionReq.questions) {
		return
	}
	q := m.questionReq.questions[m.questionIdx]
	m.questionAnswers = append(m.questionAnswers, permission.QuestionResponse{
		Question: q.Question,
		Answers:  answers,
	})
}

// advanceQuestion 前进到下一个问题或提交全部答案。
func (m *model) advanceQuestion() {
	// 如果之前有待合并的 Other 答案，合并之
	if m.questionPendingOther {
		m.questionPendingOther = false
		m.recordQuestionAnswer(m.questionPendingAnswers)
		m.questionPendingAnswers = nil
	}

	m.questionIdx++
	if m.questionReq == nil || m.questionIdx >= len(m.questionReq.questions) {
		// 所有问题已回答，提交答案
		if m.questionReq.reply != nil {
			m.questionReq.reply <- m.questionAnswers
		}
		m.closeQuestionOverlay()
		return
	}
	// 显示下一个问题
	m.buildQuestionForm()
}

// closeQuestionOverlay 关闭选择题面板。
func (m *model) closeQuestionOverlay() {
	m.overlay = overlayNone
	m.questionReq = nil
	m.questionAnswers = nil
	m.questionForm = nil
	m.questionFormIsOther = false
	m.questionPendingOther = false
	m.questionPendingAnswers = nil
	m.otherInputVisStart = 0
	m.otherInputLastValue = ""
	m.otherInput.Blur()
	m.input.Focus()
}
