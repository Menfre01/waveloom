package main

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// ---------------------------------------------------------------------------
// rect / inputRectFromLayout
// ---------------------------------------------------------------------------

func TestRectContains_Boundaries(t *testing.T) {
	r := rect{X: 2, Y: 25, W: 80, H: 2}
	cases := []struct {
		x, y int
		want bool
	}{
		{2, 25, true},   // 左上角
		{81, 25, true},  // 右边界内(2+80-1)
		{82, 25, false}, // X 超右边界(不含)
		{2, 26, true},   // 第二行
		{2, 27, false},  // Y 超下边界(不含)
		{1, 25, false},  // X 超左边界
		{2, 24, false},  // Y 超上边界
		{41, 25, true},  // 内部
	}
	for _, c := range cases {
		if got := r.contains(c.x, c.y); got != c.want {
			t.Errorf("contains(%d,%d) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
}

func TestInputRectFromLayout_Basic(t *testing.T) {
	r := inputRectFromLayout(80, 2, 3, 20, 0, 0, 0, 0, 0)
	// Y = styleApp top(1) + header(3) + body(20) + separator(1) = 25
	if r != (rect{X: 2, Y: 25, W: 80, H: 2}) {
		t.Errorf("basic layout rect = %+v", r)
	}
}

func TestInputRectFromLayout_AllComponents(t *testing.T) {
	r := inputRectFromLayout(100, 3, 4, 15, 1, 5, 4, 3, 2)
	// Y = 1 + 4 + 15 + 1 + 5 + 4 + 3 + 2 + 1 = 36
	if r != (rect{X: 2, Y: 36, W: 100, H: 3}) {
		t.Errorf("full layout rect = %+v", r)
	}
}

// ---------------------------------------------------------------------------
// handleMouse — 拖拽选中
// ---------------------------------------------------------------------------

// newTestTextarea 构造与生产几何一致的 textarea:
// prompt 宽 2、无行号、内容宽 38(SetWidth(40) - prompt 2)。
func newTestTextarea() textarea.Model {
	ta := textarea.New()
	ta.SetPromptFunc(2, func(_ textarea.PromptInfo) string {
		return "  "
	})
	ta.ShowLineNumbers = false
	ta.SetWidth(40)
	return ta
}

func clickMsg(x, y int, mod tea.KeyMod) tea.MouseMsg {
	return tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft, Mod: mod})
}

func motionMsg(x, y int) tea.MouseMsg {
	return tea.MouseMotionMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})
}

func releaseMsg(x, y int) tea.MouseMsg {
	return tea.MouseReleaseMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})
}

func TestHandleMouse_DragSelect(t *testing.T) {
	m := &model{
		input:     newTestTextarea(),
		inputRect: rect{X: 2, Y: 10, W: 40, H: 2},
		overlay:   overlayNone,
	}
	m.input.SetValue("abcdefghij")

	// 按下:relX=3 → contentX=1 → col=1('b' 前)
	m.handleMouse(clickMsg(5, 10, 0))
	if !m.mouseDrag {
		t.Fatal("expected mouseDrag after click in input rect")
	}
	// 拖动:relX=6 → contentX=4 → col=4('e' 前)
	m.handleMouse(motionMsg(8, 10))
	// 释放
	m.handleMouse(releaseMsg(8, 10))
	if m.mouseDrag {
		t.Error("expected mouseDrag cleared after release")
	}
	if !m.input.HasSelection() {
		t.Fatal("expected selection after drag")
	}
	if got := m.input.SelectedText(); got != "bcd" {
		t.Errorf("SelectedText = %q, want %q", got, "bcd")
	}
}

func TestHandleMouse_SelectBackwards(t *testing.T) {
	// 反向拖动:anchor 在 head 之后,Selection 应归一化
	m := &model{
		input:     newTestTextarea(),
		inputRect: rect{X: 2, Y: 10, W: 40, H: 2},
		overlay:   overlayNone,
	}
	m.input.SetValue("abcdefghij")

	m.handleMouse(clickMsg(8, 10, 0)) // col=4
	m.handleMouse(motionMsg(5, 10))   // col=1
	m.handleMouse(releaseMsg(5, 10))
	if got := m.input.SelectedText(); got != "bcd" {
		t.Errorf("backward SelectedText = %q, want %q", got, "bcd")
	}
}

func TestHandleMouse_DragBeyondInputEdge(t *testing.T) {
	// 拖出输入框底部/右侧:PositionAt 收敛到缓冲区端点,仍选中全部
	m := &model{
		input:     newTestTextarea(),
		inputRect: rect{X: 2, Y: 10, W: 40, H: 2},
		overlay:   overlayNone,
	}
	m.input.SetValue("abcdefghij")

	m.handleMouse(clickMsg(5, 10, 0))      // col=1
	m.handleMouse(motionMsg(200, 100))     // 远超出 → 收敛到行尾
	m.handleMouse(releaseMsg(200, 100))
	if got := m.input.SelectedText(); got != "bcdefghij" {
		t.Errorf("overflow SelectedText = %q, want %q", got, "bcdefghij")
	}
}

func TestHandleMouse_ClickClearsSelection(t *testing.T) {
	// 零宽点击(anchor==head):EndSelection 丢弃
	m := &model{
		input:     newTestTextarea(),
		inputRect: rect{X: 2, Y: 10, W: 40, H: 2},
		overlay:   overlayNone,
	}
	m.input.SetValue("abcdefghij")

	m.handleMouse(clickMsg(5, 10, 0))
	m.handleMouse(releaseMsg(5, 10))
	if m.input.HasSelection() {
		t.Error("expected zero-width click to clear selection")
	}
	if m.mouseDrag {
		t.Error("expected mouseDrag false after release")
	}
}

func TestHandleMouse_OutsideClickIgnored(t *testing.T) {
	m := &model{
		input:     newTestTextarea(),
		inputRect: rect{X: 2, Y: 10, W: 40, H: 2},
		overlay:   overlayNone,
	}
	m.input.SetValue("abcdefghij")

	m.handleMouse(clickMsg(0, 0, 0)) // 输入框外
	if m.mouseDrag {
		t.Fatal("expected no drag for outside click")
	}
	if m.input.HasSelection() {
		t.Fatal("expected no selection for outside click")
	}
	// 后续 motion 不应进入拖动
	m.handleMouse(motionMsg(5, 10))
	if m.input.HasSelection() {
		t.Fatal("expected motion without drag to be ignored")
	}
}

func TestHandleMouse_OverlayActiveIgnored(t *testing.T) {
	m := &model{
		input:     newTestTextarea(),
		inputRect: rect{X: 2, Y: 10, W: 40, H: 2},
		overlay:   overlayHelp, // 覆盖层活跃时输入框不可交互
	}
	m.input.SetValue("abcdefghij")

	m.handleMouse(clickMsg(5, 10, 0))
	if m.mouseDrag {
		t.Fatal("expected no drag while overlay active")
	}
}

func TestHandleMouse_ShiftIgnored(t *testing.T) {
	// Shift+点击放行给终端原生选择,不触发 TUI 内选中
	m := &model{
		input:     newTestTextarea(),
		inputRect: rect{X: 2, Y: 10, W: 40, H: 2},
		overlay:   overlayNone,
	}
	m.input.SetValue("abcdefghij")

	m.handleMouse(clickMsg(5, 10, tea.ModShift))
	if m.mouseDrag || m.input.HasSelection() {
		t.Fatal("expected shift+click to be ignored")
	}
}

func TestHandleMouse_WheelScroll(t *testing.T) {
	m := &model{scrollTop: 10, pinnedToBottom: true}
	m.handleMouse(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
	if m.scrollTop != 13 {
		t.Errorf("scrollDown: scrollTop = %d, want 13", m.scrollTop)
	}
	m.handleMouse(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	if m.scrollTop != 10 {
		t.Errorf("scrollUp: scrollTop = %d, want 10", m.scrollTop)
	}
	if m.pinnedToBottom {
		t.Error("expected pinnedToBottom false after wheel scroll")
	}
}

// ---------------------------------------------------------------------------
// 键位冲突
// ---------------------------------------------------------------------------

func TestTextareaSelectAllKey_NoConflictWithToggleTheme(t *testing.T) {
	ti := textarea.New()
	configureTextareaKeyMap(&ti)

	selectKeys := ti.KeyMap.SelectAll.Keys()
	if len(selectKeys) != 1 || selectKeys[0] != "ctrl+shift+a" {
		t.Fatalf("SelectAll keys = %v, want [ctrl+shift+a]", selectKeys)
	}

	// 与全局 ToggleTheme(ctrl+g)无重叠
	themeKeys := defaultKeys.ToggleTheme.Keys()
	for _, sk := range selectKeys {
		for _, tk := range themeKeys {
			if sk == tk {
				t.Errorf("key conflict: SelectAll %q overlaps ToggleTheme %q", sk, tk)
			}
		}
	}

	// 默认绑定确实包含 ctrl+g(冲突来源,防止上游 KeyMap 变化后回归)
	def := textarea.New()
	found := false
	for _, k := range def.KeyMap.SelectAll.Keys() {
		if k == "ctrl+g" {
			found = true
		}
	}
	if !found {
		t.Error("upstream default SelectAll no longer uses ctrl+g; re-evaluate conflict handling")
	}
}

func TestTextareaKeyMap_PasteAndCopyPreserved(t *testing.T) {
	// v2.2.0 默认键:v2.2.0 默认 Paste=ctrl+v(与全局 paste 流程等效)、
	// CopySelection=ctrl+shift+c(未占用)——升级不应破坏
	ti := textarea.New()
	configureTextareaKeyMap(&ti)

	pasteKeys := strings.Join(ti.KeyMap.Paste.Keys(), ",")
	if !strings.Contains(pasteKeys, "ctrl+v") {
		t.Errorf("Paste keys = %q, want ctrl+v included", pasteKeys)
	}
	copyKeys := strings.Join(ti.KeyMap.CopySelection.Keys(), ",")
	if !strings.Contains(copyKeys, "ctrl+shift+c") {
		t.Errorf("CopySelection keys = %q, want ctrl+shift+c included", copyKeys)
	}
}
