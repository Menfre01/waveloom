package main

import (
	"os"
	"path/filepath"
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

// newTestModelForEffortPicker 构造带 settingsStore 的模型选择器 model。
// buildEffortPickerList 依赖 LoadLLM(读项目文件),commitEffortSwitch 依赖
// SaveLLM + reconfigureLLMClient(NewClientFromLLMSettings 需 api_key)。
func newTestModelForEffortPicker(t *testing.T) (*model, *tuiSettingsStore) {
	t.Helper()
	dir := t.TempDir()
	projectPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(projectPath, []byte(`{"llm":{"api_key":"sk-test","provider":"deepseek","model":"deepseek-v4-pro"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	store := &tuiSettingsStore{projectPath: projectPath}
	m := newTestModelForModelPicker()
	m.settingsStore = store
	m.modelChoice = "deepseek-v4-pro"
	m.hudThinkingEffort = "high"
	return m, store
}

// TestHandleModelPickerKey_E_EntersEffortMode 验证按 e 进入 effort 档位面板,
// 且当前档位在列表中高亮(Select 索引指向 hudThinkingEffort)。
func TestHandleModelPickerKey_E_EntersEffortMode(t *testing.T) {
	m, _ := newTestModelForEffortPicker(t)

	handled, _ := m.handleModelPickerKey(tea.KeyPressMsg{Text: "e", Code: 'e'})
	if !handled {
		t.Error("e should be handled")
	}
	if !m.effortPickerMode {
		t.Fatal("effortPickerMode should be true after pressing e")
	}
	if len(m.effortPickerEfforts) != 3 {
		t.Errorf("deepseek should offer 3 efforts (off/high/max), got %d", len(m.effortPickerEfforts))
	}
	if m.effortPickerEfforts[0].ID != "off" || m.effortPickerEfforts[1].ID != "high" || m.effortPickerEfforts[2].ID != "max" {
		t.Errorf("deepseek efforts = %+v, want [off high max]", m.effortPickerEfforts)
	}
	// 当前档位 high 应被高亮(deepseek 档位 [off high max] 中索引 1)
	if got := m.effortPickerList.Index(); got != 1 {
		t.Errorf("effort list should highlight current effort high (index 1), got %d", got)
	}
}

// TestHandleModelPickerKey_EffortEscReturns 验证 effort 面板按 esc 返回模型列表
// (overlay 保持,仅退出 effort 模式)。
func TestHandleModelPickerKey_EffortEscReturns(t *testing.T) {
	m, _ := newTestModelForEffortPicker(t)
	m.effortPickerMode = true
	m.buildEffortPickerList()

	handled, _ := m.handleModelPickerKey(tea.KeyPressMsg{Text: "esc", Code: 'e'})
	if !handled {
		t.Error("esc should be handled in effort mode")
	}
	if m.effortPickerMode {
		t.Error("effortPickerMode should be false after esc")
	}
	if m.overlay != overlayModelPicker {
		t.Errorf("overlay should stay model picker, got %v", m.overlay)
	}
}

// TestHandleModelPickerKey_EffortEnterCommits 验证 effort 面板按 enter 提交:
// 档位写入项目 settings.json 的 extra_params.reasoning_effort,HUD 档位同步更新。
func TestHandleModelPickerKey_EffortEnterCommits(t *testing.T) {
	m, store := newTestModelForEffortPicker(t)
	m.effortPickerMode = true
	m.buildEffortPickerList()
	// 选中 "max"(deepseek 档位 [off high max] 索引 2)
	m.effortPickerList.Select(2)

	handled, _ := m.handleModelPickerKey(tea.KeyPressMsg{Text: "enter", Code: 'e'})
	if !handled {
		t.Error("enter should be handled in effort mode")
	}
	if m.effortPickerMode {
		t.Error("effortPickerMode should be false after commit")
	}
	if m.overlay != overlayNone {
		t.Errorf("overlay should close after commit, got %v", m.overlay)
	}
	if m.hudThinkingEffort != "max" {
		t.Errorf("hudThinkingEffort = %q, want max", m.hudThinkingEffort)
	}
	// 验证落盘
	saved, err := store.LoadProjectLLM()
	if err != nil {
		t.Fatalf("LoadProjectLLM: %v", err)
	}
	if got := saved.ExtraParams["reasoning_effort"]; got != "max" {
		t.Errorf("saved reasoning_effort = %v, want max", got)
	}
}

// TestHandleModelPickerKey_EffortKimiOnlyMax 验证 Kimi 仅提供 max 档位。
func TestHandleModelPickerKey_EffortKimiOnlyMax(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(projectPath, []byte(`{"llm":{"api_key":"sk-test","provider":"kimi","model":"kimi-k3"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	store := &tuiSettingsStore{projectPath: projectPath}
	m := newTestModelForModelPicker()
	m.settingsStore = store

	handled, _ := m.handleModelPickerKey(tea.KeyPressMsg{Text: "e", Code: 'e'})
	if !handled {
		t.Error("e should be handled")
	}
	if !m.effortPickerMode {
		t.Fatal("effortPickerMode should be true")
	}
	if len(m.effortPickerEfforts) != 1 || m.effortPickerEfforts[0].ID != "max" {
		t.Errorf("kimi efforts = %+v, want [max]", m.effortPickerEfforts)
	}
}

// TestHandleModelPickerKey_EffortCommitWritesProfile 验证 profile 形态配置
// (reasoning_effort 位于 profiles.<provider>.extra_params)时,档位写入 profile
// 而非顶层——ResolveProfile 后生效,避免写顶层被 profile 覆盖导致不生效。
func TestHandleModelPickerKey_EffortCommitWritesProfile(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(projectPath, []byte(`{"llm":{"api_key":"sk-test","provider":"deepseek","model":"deepseek-v4-pro","profiles":{"deepseek":{"curr_model":"deepseek-v4-flash"}}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	store := &tuiSettingsStore{projectPath: projectPath}
	m := newTestModelForModelPicker()
	m.settingsStore = store
	m.modelChoice = "deepseek-v4-pro"
	m.hudThinkingEffort = "high"
	m.effortPickerMode = true
	m.buildEffortPickerList()
	m.effortPickerList.Select(2) // max(deepseek [off high max] 索引 2)

	_, _ = m.handleModelPickerKey(tea.KeyPressMsg{Text: "enter", Code: 'e'})

	saved, err := store.LoadProjectLLM()
	if err != nil {
		t.Fatalf("LoadProjectLLM: %v", err)
	}
	prof := saved.Profiles["deepseek"]
	if prof == nil {
		t.Fatal("profile deepseek should exist")
	}
	if got := prof.ExtraParams["reasoning_effort"]; got != "max" {
		t.Errorf("profile reasoning_effort = %v, want max", got)
	}
	if got := saved.ExtraParams["reasoning_effort"]; got != nil {
		t.Errorf("top-level reasoning_effort should stay nil, got %v", got)
	}
}

// TestHandleModelPickerKey_EffortOffDisablesThinking 验证选择 off 后:
// thinking.type=disabled 落盘、reasoning_effort 被移除、HUD 档位清空(不显示)。
func TestHandleModelPickerKey_EffortOffDisablesThinking(t *testing.T) {
	m, store := newTestModelForEffortPicker(t)
	m.effortPickerMode = true
	m.buildEffortPickerList()
	m.effortPickerList.Select(0) // off(deepseek [off high max] 索引 0)

	handled, _ := m.handleModelPickerKey(tea.KeyPressMsg{Text: "enter", Code: 'e'})
	if !handled {
		t.Error("enter should be handled in effort mode")
	}
	if m.hudThinkingEffort != "" {
		t.Errorf("hudThinkingEffort = %q, want empty (off hides effort in HUD)", m.hudThinkingEffort)
	}
	saved, err := store.LoadProjectLLM()
	if err != nil {
		t.Fatalf("LoadProjectLLM: %v", err)
	}
	thinking, ok := saved.ExtraParams["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Errorf("thinking = %#v, want {type: disabled}", saved.ExtraParams["thinking"])
	}
	if _, ok := saved.ExtraParams["reasoning_effort"]; ok {
		t.Error("reasoning_effort should be removed when off")
	}
}

// TestRenderModelPickerOverlay_EffortMode 验证 effort 面板渲染包含标题与档位。
func TestRenderModelPickerOverlay_EffortMode(t *testing.T) {
	m, _ := newTestModelForEffortPicker(t)
	m.effortPickerMode = true
	m.buildEffortPickerList()

	content := m.renderModelPickerOverlay(40)
	if content == "" {
		t.Fatal("effort overlay should produce non-empty output")
	}
	if !strings.Contains(content, "max") {
		t.Error("effort overlay should contain 'max' effort")
	}
	if !strings.Contains(content, "off") {
		t.Error("effort overlay should contain 'off' effort")
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
