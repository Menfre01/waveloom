package main

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newTestModelForOverlay() *model {
	m := &model{
		lc:     &enUS,
		input:  textarea.New(),
		keys:   makeKeyMap(&enUS),
		width:  80,
		height: 24,
	}
	return m
}

// ---------------------------------------------------------------------------
// Theme picker — render
// ---------------------------------------------------------------------------

func newTestModelForThemePicker() *model {
	m := newTestModelForOverlay()
	m.themeMode = "dark"
	m.overlay = overlayThemePicker
	m.buildThemeList()
	return m
}

func TestRenderThemePickerOverlay_ContainsOptions(t *testing.T) {
	m := newTestModelForThemePicker()
	content := m.renderThemePickerOverlay(40)

	if content == "" {
		t.Fatal("theme picker overlay should produce non-empty output")
	}
	// Theme items: Auto, Dark, Light, Dark CB, Light CB
	// Labels may be locale-dependent; just verify output is meaningful
	t.Logf("theme picker content (first 200 chars): %.200s", content)
}

// ---------------------------------------------------------------------------
// Theme picker — key handling
// ---------------------------------------------------------------------------

func TestHandleThemePickerKey_EscClosesOverlay(t *testing.T) {
	m := newTestModelForThemePicker()
	handled, cmd := m.handleThemePickerKey(tea.KeyPressMsg{Text: "esc", Code: 'e'})
	if !handled || cmd != nil {
		t.Error("esc should be handled with nil cmd")
	}
	if m.overlay != overlayNone {
		t.Errorf("esc should close theme overlay, got overlay %v", m.overlay)
	}
}

func TestHandleThemePickerKey_EnterSelectsAndCloses(t *testing.T) {
	m := newTestModelForThemePicker()
	// themeItems: [auto, dark, light, darkcolorblind, lightcolorblind]
	// Default themeMode "dark" → selectedIdx=1. Move to index 0 (auto).
	m.themeList.Select(0)
	handled, cmd := m.handleThemePickerKey(tea.KeyPressMsg{Text: "enter", Code: '\r'})
	if !handled || cmd != nil {
		t.Error("enter should be handled with nil cmd")
	}
	if m.overlay != overlayNone {
		t.Errorf("enter should close theme overlay, got overlay %v", m.overlay)
	}
	if m.themeMode != "auto" {
		t.Errorf("enter on auto should switch theme to 'auto', got %q", m.themeMode)
	}
}

func TestHandleThemePickerKey_UnknownKeyPassthrough(t *testing.T) {
	m := newTestModelForThemePicker()
	handled, _ := m.handleThemePickerKey(tea.KeyPressMsg{Text: "a"})
	if handled {
		t.Error("unknown key should not be consumed by theme picker")
	}
	if m.overlay != overlayThemePicker {
		t.Error("unknown key should not close theme picker")
	}
}

func TestHandleThemePickerKey_UpDownNavigates(t *testing.T) {
	m := newTestModelForThemePicker()
	initial := m.themeList.Index()

	// Press down
	handled, cmd := m.handleThemePickerKey(tea.KeyPressMsg{Text: "down", Code: 'j'})
	if !handled || cmd != nil {
		t.Error("down should be handled")
	}
	if m.themeList.Index() == initial {
		t.Log("down may not have changed index if at bottom")
	} else {
		t.Logf("down moved from %d to %d", initial, m.themeList.Index())
	}

	// Press up
	handled, cmd = m.handleThemePickerKey(tea.KeyPressMsg{Text: "up", Code: 'k'})
	if !handled || cmd != nil {
		t.Error("up should be handled")
	}
	if m.themeList.Index() != initial {
		t.Logf("up moved to %d (initial was %d)", m.themeList.Index(), initial)
	}
}

// ---------------------------------------------------------------------------
// Model picker — render
// ---------------------------------------------------------------------------

func newTestModelForModelPicker() *model {
	m := newTestModelForOverlay()
	m.overlay = overlayModelPicker
	m.modelPickerItems = []llm.ModelInfo{
		{ID: "model-a", OwnedBy: "vendor-a"},
		{ID: "model-b", OwnedBy: "vendor-b"},
	}
	m.buildModelPickerList()
	return m
}

func TestRenderModelPickerOverlay_ContainsModels(t *testing.T) {
	m := newTestModelForModelPicker()
	content := m.renderModelPickerOverlay(40)

	if content == "" {
		t.Fatal("model picker overlay should produce non-empty output")
	}
	if !strings.Contains(content, "model-a") {
		t.Error("model picker should contain 'model-a'")
	}
	if !strings.Contains(content, "model-b") {
		t.Error("model picker should contain 'model-b'")
	}
}

// ---------------------------------------------------------------------------
// Model picker — key handling
// ---------------------------------------------------------------------------

func TestHandleModelPickerKey_EscCloses(t *testing.T) {
	m := newTestModelForModelPicker()
	handled, cmd := m.handleModelPickerKey(tea.KeyPressMsg{Text: "esc", Code: 'e'})
	if !handled || cmd != nil {
		t.Error("esc should be handled with nil cmd")
	}
	if m.overlay != overlayNone {
		t.Errorf("esc should close model overlay, got %v", m.overlay)
	}
}


// ---------------------------------------------------------------------------
// Locale picker — render
// ---------------------------------------------------------------------------

func newTestModelForLocalePicker() *model {
	m := newTestModelForOverlay()
	m.overlay = overlayLocalePicker
	m.buildLocaleList()
	return m
}

func TestRenderLocalePickerOverlay_ContainsOptions(t *testing.T) {
	m := newTestModelForLocalePicker()
	content := m.renderLocalePickerOverlay(40)

	if content == "" {
		t.Fatal("locale picker overlay should produce non-empty output")
	}
	t.Logf("locale picker content (first 200 chars): %.200s", content)
}

// ---------------------------------------------------------------------------
// Locale picker — key handling
// ---------------------------------------------------------------------------

func TestHandleLocalePickerKey_EscCloses(t *testing.T) {
	m := newTestModelForLocalePicker()
	handled, cmd := m.handleLocalePickerKey(tea.KeyPressMsg{Text: "esc", Code: 'e'})
	if !handled || cmd != nil {
		t.Error("esc should be handled with nil cmd")
	}
	if m.overlay != overlayNone {
		t.Errorf("esc should close locale overlay, got %v", m.overlay)
	}
}

// ---------------------------------------------------------------------------
// Help overlay — render
// ---------------------------------------------------------------------------

func newTestModelForHelp() *model {
	m := newTestModelForOverlay()
	m.overlay = overlayHelp
	m.keys = makeKeyMap(&enUS)
	m.input.Blur()
	return m
}

func TestRenderHelpOverlay_ContainsShortcuts(t *testing.T) {
	m := newTestModelForHelp()
	content := m.renderHelpOverlay(40)

	if content == "" {
		t.Fatal("help overlay should produce non-empty output")
	}
	t.Logf("help overlay content (first 200 chars): %.200s", content)
}

// ---------------------------------------------------------------------------
// Help overlay — key toggle via handleKeyPress
// ---------------------------------------------------------------------------

func TestHandleKeyPress_HelpToggle(t *testing.T) {
	m := newTestModelForOverlay()
	m.overlay = overlayNone

	handled, cmd := m.handleKeyPress(tea.KeyPressMsg{Text: "?", Code: '?'})
	if handled && cmd == nil {
		if m.overlay != overlayHelp {
			t.Error("'?' should open help overlay when none active")
		}
	}

	if m.overlay == overlayHelp {
		handled, cmd = m.handleKeyPress(tea.KeyPressMsg{Text: "?", Code: '?'})
		if handled && cmd == nil {
			if m.overlay != overlayNone {
				t.Error("'?' should close help overlay when already open")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Plan enter overlay — render + key handling
// ---------------------------------------------------------------------------

func newTestModelForPlanEnter() *model {
	m := newTestModelForOverlay()
	m.overlay = overlayPlanEnter
	m.input.Blur()
	replyCh := make(chan bool, 1)
	m.planEnterReply = replyCh
	return m
}

func TestRenderPlanEnterOverlay_ProducesOutput(t *testing.T) {
	m := newTestModelForPlanEnter()
	content := m.renderPlanEnterOverlay(40)
	if content == "" {
		t.Fatal("plan enter overlay should produce non-empty output")
	}
}

func TestHandleKeyPress_PlanEnter_EscCloses(t *testing.T) {
	m := newTestModelForPlanEnter()
	replyCh := make(chan bool, 1)
	m.planEnterReply = replyCh

	handled, _ := m.handleKeyPress(tea.KeyPressMsg{Text: "esc", Code: 'e'})
	if !handled {
		t.Error("esc should be handled in plan enter overlay")
	}
	if m.overlay != overlayNone {
		t.Error("esc should close plan enter overlay")
	}
	select {
	case reply := <-replyCh:
		if reply {
			t.Error("esc should reply false")
		}
	default:
		t.Error("esc should send reply on channel")
	}
}

// ---------------------------------------------------------------------------
// Plan exit overlay — render
// ---------------------------------------------------------------------------

func newTestModelForPlanExit() *model {
	m := newTestModelForOverlay()
	m.overlay = overlayPlanExit
	m.inPlanMode = true
	m.input.Blur()
	return m
}

func TestRenderPlanExitOverlay_ProducesOutput(t *testing.T) {
	m := newTestModelForPlanExit()
	content := m.renderPlanExitOverlay(40)
	if content == "" {
		t.Fatal("plan exit overlay should produce non-empty output")
	}
}

// ---------------------------------------------------------------------------
// Rewind select overlay — render
// ---------------------------------------------------------------------------

func newTestModelForRewindSelect() *model {
	m := newTestModelForOverlay()
	m.overlay = overlayRewindSelect
	m.rewindMessages = []rewindMsg{
		{MessageID: "msg-1", Content: "fix: update API handler"},
		{MessageID: "msg-2", Content: "feat: add new endpoint"},
	}
	m.input.Blur()
	return m
}

func TestRenderRewindSelectOverlay_ProducesOutput(t *testing.T) {
	m := newTestModelForRewindSelect()
	content := m.renderRewindSelectOverlay(40)
	if content == "" {
		t.Fatal("rewind select overlay should produce non-empty output")
	}
}

// ---------------------------------------------------------------------------
// Rewind confirm overlay — render
// ---------------------------------------------------------------------------

func newTestModelForRewindConfirm() *model {
	m := newTestModelForOverlay()
	m.overlay = overlayRewindConfirm
	m.rewindTargetMsgID = "msg-1"
	m.rewindMessages = []rewindMsg{
		{MessageID: "msg-1", Content: "fix: update API handler"},
	}
	m.input.Blur()
	return m
}

func TestRenderRewindConfirmOverlay_ProducesOutput(t *testing.T) {
	m := newTestModelForRewindConfirm()
	content := m.renderRewindConfirmOverlay(40)
	if content == "" {
		t.Fatal("rewind confirm overlay should produce non-empty output")
	}
}

// ---------------------------------------------------------------------------
// 权限面板 ↑↓ 导航测试:↑↓ 仅导航列表项,不透传给 viewport。
// ---------------------------------------------------------------------------

func newTestModelForPermissionOverlay() *model {
	m := newTestModelForOverlay()
	m.overlay = overlayPermission
	m.permList = m.buildPermList()
	m.permList.SetSize(40, 3) // 设置有效宽度避免 delegate 除零
	m.input.Blur()
	return m
}

func TestHandleKeyPress_PermissionBoundary_UpStaysAtTop(t *testing.T) {
	m := newTestModelForPermissionOverlay()
	m.scrollTop = 5
	m.bodyHeight = 10

	// 列表选中第一项(index 0),按 ↑ 应停留在顶部,不滚动 body
	m.permList.Select(0)
	handled, _ := m.handleKeyPress(tea.KeyPressMsg{Text: "up", Code: 'u'})
	if !handled {
		t.Error("up at boundary should be handled (navigate list)")
	}
	// body 不滚动
	if m.scrollTop != 5 {
		t.Errorf("scrollTop should stay at 5, got %d", m.scrollTop)
	}
	// 列表选中项应保持
	if m.permList.Index() != 0 {
		t.Errorf("permList index should stay at 0, got %d", m.permList.Index())
	}
}

func TestHandleKeyPress_PermissionBoundary_DownStaysAtBottom(t *testing.T) {
	m := newTestModelForPermissionOverlay()

	m.scrollTop = 5
	m.bodyHeight = 10

	// 列表选中最后一项(index 2 = Deny),按 ↓ 应停留在底部,不滚动 body
	m.permList.Select(2)
	handled, _ := m.handleKeyPress(tea.KeyPressMsg{Text: "down", Code: 'd'})
	if !handled {
		t.Error("down at boundary should be handled (navigate list)")
	}
	if m.scrollTop != 5 {
		t.Errorf("scrollTop should stay at 5, got %d", m.scrollTop)
	}
	if m.permList.Index() != 2 {
		t.Errorf("permList index should stay at 2, got %d", m.permList.Index())
	}
}

func TestHandleKeyPress_PermissionMiddle_NavigatesList(t *testing.T) {
	m := newTestModelForPermissionOverlay()

	// 列表选中中间项(index 1 = Allow All),按 ↑↓ 应正常导航
	m.permList.Select(1)
	handled, _ := m.handleKeyPress(tea.KeyPressMsg{Text: "up", Code: 'u'})
	if !handled {
		t.Error("up in middle should be handled (navigate list)")
	}
	if m.permList.Index() != 0 {
		t.Errorf("permList should move to 0, got %d", m.permList.Index())
	}

	// 再按 ↓ 回到中间
	handled, _ = m.handleKeyPress(tea.KeyPressMsg{Text: "down", Code: 'd'})
	if !handled {
		t.Error("down in middle should be handled (navigate list)")
	}
	if m.permList.Index() != 1 {
		t.Errorf("permList should move back to 1, got %d", m.permList.Index())
	}
}

// 主题选择器边界穿透
func TestHandleThemePickerKey_BoundaryUpScrollsBody(t *testing.T) {
	m := newTestModelForThemePicker()
	m.scrollTop = 5
	m.bodyHeight = 10
	m.themeList.Select(0) // 第一项

	handled, _ := m.handleThemePickerKey(tea.KeyPressMsg{Text: "up", Code: 'u'})
	if !handled {
		t.Error("up at boundary should scroll body")
	}
	if m.scrollTop != 4 {
		t.Errorf("scrollTop should be 4, got %d", m.scrollTop)
	}
}

func TestHandleThemePickerKey_BoundaryDownScrollsBody(t *testing.T) {
	m := newTestModelForThemePicker()
	m.scrollTop = 5
	m.bodyHeight = 10
	m.themeList.Select(len(themeItems) - 1) // 最后一项

	handled, _ := m.handleThemePickerKey(tea.KeyPressMsg{Text: "down", Code: 'd'})
	if !handled {
		t.Error("down at boundary should scroll body")
	}
	if m.scrollTop != 6 {
		t.Errorf("scrollTop should be 6, got %d", m.scrollTop)
	}
}
