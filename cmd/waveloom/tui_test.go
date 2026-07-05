package main

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	ctxpkg "github.com/Menfre01/waveloom/pkg/context"
	"github.com/Menfre01/waveloom/pkg/llm"
)

// newTestCM 创建一个用于测试的 ContextManager（无 hard limit）。
func newTestCM() *ctxpkg.ContextManager {
	return ctxpkg.New("system")
}

// newTestCMWithHardLimit 通过 Compactor.Compact 注入高 contextTokens 触发硬临界值。
func newTestCMWithHardLimit() *ctxpkg.ContextManager {
	cm := ctxpkg.New("system")
	// 先 PrepareRun 注入消息
	cm.PrepareRun("hello")
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}
	// contextTokens=980_000 >= 98% * 1_000_000 → 触发 HardLimitReached
	_ = cm.Compactor().Compact(context.Background(), &messages, 980_000)
	// 同时写入消息到 ContextManager
	cm.CompleteRun(messages, 980_000, 980_000, 50, 0, 0, 0, "", 0, "")
	return cm
}

// ---------------------------------------------------------------------------
// truncateToolResult
// ---------------------------------------------------------------------------

func TestTruncateToolResult_UnderLimit(t *testing.T) {
	result := truncateToolResult("short output")
	if result != "short output" {
		t.Errorf("expected unchanged, got %q", result)
	}
}

func TestTruncateToolResult_OverLimit(t *testing.T) {
	// Build a string just over the limit
	base := make([]byte, maxToolResultBytes+100)
	for i := range base {
		base[i] = 'x'
	}
	input := string(base)
	result := truncateToolResult(input)

	if len(result) >= len(input) {
		t.Errorf("expected truncation, got same length %d", len(result))
	}
	if len(result) != maxToolResultBytes+len("\n... (output truncated)") {
		t.Errorf("expected length %d, got %d", maxToolResultBytes+len("\n... (output truncated)"), len(result))
	}
}

func TestTruncateToolResult_ExactlyAtLimit(t *testing.T) {
	base := make([]byte, maxToolResultBytes)
	for i := range base {
		base[i] = 'y'
	}
	input := string(base)
	result := truncateToolResult(input)
	if result != input {
		t.Errorf("expected unchanged at exactly limit, got truncated")
	}
}

// ---------------------------------------------------------------------------
// hasStreamingPara — tail-3 only
// ---------------------------------------------------------------------------

func TestHasStreamingPara_Empty(t *testing.T) {
	m := &model{}
	if m.hasStreamingPara() {
		t.Error("expected false for empty model")
	}
}

func TestHasStreamingPara_StreamingAtTail(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraUser, State: stateDone, Text: "old"},
			{Type: paraAssistant, State: stateDone, Text: "old"},
			{Type: paraAssistant, State: stateDone, Text: "old"},
			{Type: paraAssistant, State: stateStreaming, Text: "streaming"},
		},
	}
	if !m.hasStreamingPara() {
		t.Error("expected true when last para is streaming")
	}
}

func TestHasStreamingPara_StreamingBeyondTail3(t *testing.T) {
	// Paragraphs beyond the last 3 should NOT be scanned
	m := &model{
		paras: []Paragraph{
			{Type: paraAssistant, State: stateStreaming, Text: "very old streaming"},
			{Type: paraUser, State: stateDone, Text: "user"},
			{Type: paraAssistant, State: stateDone, Text: "assistant"},
			{Type: paraAssistant, State: stateDone, Text: "assistant"},
			{Type: paraAssistant, State: stateDone, Text: "assistant"},
		},
	}
	if m.hasStreamingPara() {
		t.Error("expected false — streaming paragraph is beyond last 3")
	}
}

func TestHasStreamingPara_StreamingAtPosition3FromEnd(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraAssistant, State: stateStreaming, Text: "should be found"},
			{Type: paraAssistant, State: stateDone, Text: "x"},
			{Type: paraAssistant, State: stateDone, Text: "x"},
		},
	}
	if !m.hasStreamingPara() {
		t.Error("expected true — streaming paragraph is at position 0, which is within last 3 (total 3)")
	}
}

// ---------------------------------------------------------------------------
// streamingIsLastOnly
// ---------------------------------------------------------------------------

func TestStreamingIsLastOnly_Empty(t *testing.T) {
	m := &model{}
	if m.streamingIsLastOnly() {
		t.Error("expected false for empty model")
	}
}

func TestStreamingIsLastOnly_True(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraUser, State: stateDone, Text: "user"},
			{Type: paraAssistant, State: stateStreaming, Text: "streaming"},
		},
	}
	if !m.streamingIsLastOnly() {
		t.Error("expected true when only last para is streaming")
	}
}

func TestStreamingIsLastOnly_MiddleStreaming(t *testing.T) {
	// Middle paragraph streaming, last is not — should return false
	m := &model{
		paras: []Paragraph{
			{Type: paraAssistant, State: stateStreaming, Text: "streaming"},
			{Type: paraAssistant, State: stateDone, Text: "done"},
		},
	}
	if m.streamingIsLastOnly() {
		t.Error("expected false when last para is not streaming")
	}
}

// ---------------------------------------------------------------------------
// trimParas
// ---------------------------------------------------------------------------

func TestTrimParas_UnderLimit(t *testing.T) {
	paras := make([]Paragraph, 10)
	m := &model{paras: paras}
	m.trimParas()
	if len(m.paras) != 10 {
		t.Errorf("expected 10 paras, got %d", len(m.paras))
	}
}

func TestTrimParas_OverLimit(t *testing.T) {
	n := maxParas + 50
	paras := make([]Paragraph, n)
	m := &model{paras: paras}
	m.trimParas()
	if len(m.paras) != maxParas {
		t.Errorf("expected %d paras after trim, got %d", maxParas, len(m.paras))
	}
}

func TestTrimParas_TranscriptWrittenSync(t *testing.T) {
	n := maxParas + 10
	paras := make([]Paragraph, n)
	m := &model{
		paras:            paras,
		transcriptWritten: n,
	}
	m.trimParas()
	if m.transcriptWritten != maxParas {
		t.Errorf("expected transcriptWritten=%d, got %d", maxParas, m.transcriptWritten)
	}
}

// ---------------------------------------------------------------------------
// displayWidth
// ---------------------------------------------------------------------------

func TestDisplayWidth_AllASCII(t *testing.T) {
	w := displayWidth("hello world")
	if w != 11 {
		t.Errorf("expected 11, got %d", w)
	}
}

func TestDisplayWidth_MixedCJK(t *testing.T) {
	w := displayWidth("你好wo")
	// "你好" = 4, "wo" = 2 → 6
	if w != 6 {
		t.Errorf("expected 6, got %d", w)
	}
}

func TestDisplayWidth_Empty(t *testing.T) {
	w := displayWidth("")
	if w != 0 {
		t.Errorf("expected 0, got %d", w)
	}
}

func TestDisplayWidth_AnsiCodes(t *testing.T) {
	// ANSI 转义序列应计为 0 宽度
	// "  ## 标题"："标题" 是 CJK 字符，每个宽度 2，总宽度 = 2+2+1+2+2 = 9
	line := "\x1b[38;2;175;135;255m  ## 标题\x1b[0m"
	w := displayWidth(line)
	expected := 9
	if w != expected {
		t.Errorf("expected %d, got %d (ANSI codes should have zero width)", expected, w)
	}
}

func TestDisplayWidth_MultipleAnsiCodes(t *testing.T) {
	// 多个 ANSI 码 + 文本
	line := "\x1b[38;2;215;135;95mfunc\x1b[0m \x1b[38;2;130;170;255mmain\x1b[0m() {"
	w := displayWidth(line)
	expected := 13 // "func main() {" = 13 ASCII runes
	if w != expected {
		t.Errorf("expected %d, got %d", expected, w)
	}
}

func TestDisplayWidth_AnsiWithoutText(t *testing.T) {
	w := displayWidth("\x1b[1m\x1b[0m")
	if w != 0 {
		t.Errorf("expected 0 for pure ANSI, got %d", w)
	}
}

func TestWrapLine_AnsiNotBroken(t *testing.T) {
	// 含 ANSI 的行在 displayWidth ≤ maxWidth 时不应被拆分
	line := "\x1b[38;2;175;135;255m  ## 标题\x1b[0m"
	// displayWidth = 7, maxWidth = 80 → 不应换行
	result := wrapLine(line, 80)
	if len(result) != 1 {
		t.Errorf("expected 1 line, got %d — ANSI line should NOT be wrapped", len(result))
	}
	if result[0] != line {
		t.Errorf("expected line unchanged, got %q", result[0])
	}
}

func TestWrapLine_AnsiKeptIntact(t *testing.T) {
	// 含 ANSI 的行需要换行时，ANSI 序列不应被撕裂在中间。
	// ANSI 码在行首和行尾，换行后首段保留开头 ANSI，末段保留结尾 ANSI。
	line := "\x1b[38;2;175;135;255m" + strings.Repeat("x", 100) + "\x1b[0m"
	result := wrapLine(line, 50)
	if len(result) < 2 {
		t.Fatalf("expected at least 2 wrapped lines, got %d", len(result))
	}
	// 第一段应包含开头 ANSI
	if !strings.HasPrefix(result[0], "\x1b[38;2;175;135;255m") {
		t.Errorf("first segment missing opening ANSI: %q", result[0])
	}
	// 最后一段应包含结尾 ANSI
	if !strings.HasSuffix(result[len(result)-1], "\x1b[0m") {
		t.Errorf("last segment missing closing ANSI: %q", result[len(result)-1])
	}
	// 所有段的可见文本拼接后应与原文一致（去掉 ANSI 码）
	var visible strings.Builder
	for _, seg := range result {
		s := seg
		s = strings.TrimPrefix(s, "\x1b[38;2;175;135;255m")
		s = strings.TrimSuffix(s, "\x1b[0m")
		visible.WriteString(s)
	}
	if visible.String() != strings.Repeat("x", 100) {
		t.Errorf("visible content mismatch: got %d chars, expected 100", len(visible.String()))
	}
}

// newTestInput 创建测试用的 textarea，与 newTUIModel 中的初始化一致。
func newTestInput() textarea.Model {
	ti := textarea.New()
	ti.CharLimit = 2048
	ti.ShowLineNumbers = false
	ti.MaxHeight = 2
	ti.EndOfBufferCharacter = ' '
	ti.SetPromptFunc(2, func(_ textarea.PromptInfo) string {
		return "  "
	})
	ti.SetHeight(2)
	ti.SetVirtualCursor(false)
	ti.Focus()
	return ti
}

// ---------------------------------------------------------------------------
// hard limit guard
// ---------------------------------------------------------------------------

func TestHardLimitGuard_BlocksEnterWhenReached(t *testing.T) {
	// 构造一个已触发硬临界值的 ContextManager
	cm := newTestCMWithHardLimit()

	m := &model{
		cm:    cm,
		keys:  defaultKeys,
		paras: []Paragraph{},
		input: newTestInput(),
	}
	// 模拟用户输入
	m.input.SetValue("hello")
	m.width = 120
	m.height = 40

	initialParaCount := len(m.paras)

	// 发送 Enter 键
	_, cmd := m.handleKeyPress(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	// 不应返回 tea.Cmd（不启动 loop）
	if cmd != nil {
		t.Error("expected nil cmd (no loop started) when hard limit reached")
	}
	// 不应进入 running 态
	if m.running {
		t.Error("expected running=false when hard limit reached")
	}
	// 应追加一条系统提示段落
	if len(m.paras) != initialParaCount+1 {
		t.Fatalf("expected %d paras (initial + 1), got %d", initialParaCount+1, len(m.paras))
	}
	lastPara := m.paras[len(m.paras)-1]
	if lastPara.Type != paraSystem || lastPara.State != stateDone {
		t.Errorf("expected system/done paragraph, got type=%v state=%v", lastPara.Type, lastPara.State)
	}
	if !strings.Contains(lastPara.Text, "/reset") {
		t.Errorf("expected block message, got %q", lastPara.Text)
	}
}

func TestHardLimitGuard_AllowsEnterWhenNotReached(t *testing.T) {
	cm := newTestCM()

	m := &model{
		cm:    cm,
		keys:  defaultKeys,
		paras: []Paragraph{},
		input: newTestInput(),
	}
	m.input.SetValue("hello")
	m.width = 120
	m.height = 40

	initialParaCount := len(m.paras)

	handled, cmd := m.handleKeyPress(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	if !handled {
		t.Error("expected handled=true for Enter")
	}
	if cmd == nil {
		t.Error("expected non-nil cmd (doTurn) when no hard limit")
	}
	// doTurn 同步追加 user 段落，验证区分于 hard limit 阻断
	if len(m.paras) != initialParaCount+1 {
		t.Errorf("expected %d para (initial + user), got %d", initialParaCount+1, len(m.paras))
	}
	if m.paras[len(m.paras)-1].Type != paraUser {
		t.Errorf("expected user paragraph, got type=%v", m.paras[len(m.paras)-1].Type)
	}
}

func TestEnter_EmptyInputWhenRunning_NoInterrupt(t *testing.T) {
	m := &model{
		cm:      newTestCM(),
		keys:    defaultKeys,
		paras:   []Paragraph{},
		running: true,
		width:   120,
		height:  40,
		input:   newTestInput(),
	}
	// 设置 cancelRun 可以取消
	cancelCalled := false
	m.cancelRun = func() { cancelCalled = true }
	// 输入框为空
	m.input.SetValue("")

	handled, cmd := m.handleKeyPress(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	if !handled {
		t.Error("expected handled=true for Enter with empty input when running")
	}
	if cmd != nil {
		t.Error("expected nil cmd (no doTurn) for empty input when running")
	}
	if cancelCalled {
		t.Error("cancelRun should NOT be called for empty input when running")
	}
}

// ── 段落焦点导航测试 ──

func TestIsExpandable(t *testing.T) {
	const contentWidth = 80

	tests := []struct {
		name string
		p    Paragraph
		want bool
	}{
		{"thought collapsed short", Paragraph{Type: paraThought, State: stateCollapsed, Text: "short"}, false},
		{"thought collapsed long", Paragraph{Type: paraThought, State: stateCollapsed, Text: strings.Repeat("a ", 100)}, true},
		{"thought expanded", Paragraph{Type: paraThought, State: stateExpanded, Text: "any"}, true},
		{"thought streaming", Paragraph{Type: paraThought, State: stateStreaming}, false},
		{"shell done short", Paragraph{Type: paraTool, State: stateDone, ToolName: "bash", ToolResult: "ok"}, false},
		{"shell done long", Paragraph{Type: paraTool, State: stateDone, ToolName: "bash", ToolResult: strings.Repeat("line\n", 10)}, true},
		{"shell expanded", Paragraph{Type: paraTool, State: stateExpanded, ToolName: "bash"}, true},
		{"web_fetch done short", Paragraph{Type: paraTool, State: stateDone, ToolName: "web_fetch", ToolResult: "short"}, false},
		{"web_fetch done long", Paragraph{Type: paraTool, State: stateDone, ToolName: "web_fetch", ToolResult: "Fetched url\n\n" + strings.Repeat("line\n", 10)}, true},
		{"web_fetch expanded", Paragraph{Type: paraTool, State: stateExpanded, ToolName: "web_fetch"}, true},
		{"tool streaming", Paragraph{Type: paraTool, State: stateStreaming}, false},
		{"tool error", Paragraph{Type: paraTool, State: stateError}, false},
		{"read_file done", Paragraph{Type: paraTool, State: stateDone, ToolName: "read_file"}, false},
		{"edit_file done", Paragraph{Type: paraTool, State: stateDone, ToolName: "edit_file"}, false},
		{"write_file done", Paragraph{Type: paraTool, State: stateDone, ToolName: "write_file"}, false},
		{"user any", Paragraph{Type: paraUser, State: stateDone}, false},
		{"assistant any", Paragraph{Type: paraAssistant, State: stateDone}, false},
		{"system any", Paragraph{Type: paraSystem, State: stateDone}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isExpandable(&tt.p, contentWidth); got != tt.want {
				t.Errorf("isExpandable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFocusNext_EmptyParas(t *testing.T) {
	m := &model{paras: nil, focusIndex: -1}
	_ = m.focusNext()
	if m.focusIndex != -1 {
		t.Errorf("expected focusIndex=-1 for empty paras, got %d", m.focusIndex)
	}
}

func TestFocusNext_NoExpandable(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraUser, State: stateDone},
			{Type: paraAssistant, State: stateDone},
			{Type: paraSystem, State: stateDone},
		},
		focusIndex: -1,
		width:      120,
	}
	_ = m.focusNext()
	if m.focusIndex != -1 {
		t.Errorf("expected focusIndex=-1 with no expandable, got %d", m.focusIndex)
	}
}

func TestFocusNext_CyclicRing(t *testing.T) {
	paras := []Paragraph{
		{Type: paraThought, State: stateCollapsed, ThoughtTokens: 100, Text: strings.Repeat("long text ", 50)},   // 可展开（溢出）
		{Type: paraAssistant, State: stateDone},
		{Type: paraTool, State: stateDone, ToolName: "bash", ToolResult: strings.Repeat("line\n", 10)}, // 可展开（溢出）
		{Type: paraThought, State: stateExpanded, ThoughtTokens: 200, Text: "expanded"},                // 可展开（已展开）
	}
	m := &model{paras: paras, focusIndex: -1, width: 120}

	// -1 → first expandable (index 0)
	_ = m.focusNext()
	if m.focusIndex != 0 {
		t.Errorf("first focus: expected 0, got %d", m.focusIndex)
	}
	// 0 → 2
	_ = m.focusNext()
	if m.focusIndex != 2 {
		t.Errorf("second focus: expected 2, got %d", m.focusIndex)
	}
	// 2 → 3
	_ = m.focusNext()
	if m.focusIndex != 3 {
		t.Errorf("third focus: expected 3, got %d", m.focusIndex)
	}
	// 3 → 0 (wrap)
	_ = m.focusNext()
	if m.focusIndex != 0 {
		t.Errorf("wrap focus: expected 0, got %d", m.focusIndex)
	}
}

func TestFocusPrev_CyclicRing(t *testing.T) {
	paras := []Paragraph{
		{Type: paraThought, State: stateCollapsed, ThoughtTokens: 100, Text: strings.Repeat("long text ", 50)},   // 可展开（溢出）
		{Type: paraAssistant, State: stateDone},
		{Type: paraTool, State: stateDone, ToolName: "bash", ToolResult: strings.Repeat("line\n", 10)}, // 可展开（溢出）
	}
	m := &model{paras: paras, focusIndex: -1, width: 120}

	// -1 → last expandable (index 2)
	_ = m.focusPrev()
	if m.focusIndex != 2 {
		t.Errorf("first focus: expected 2, got %d", m.focusIndex)
	}
	// 2 → 0
	_ = m.focusPrev()
	if m.focusIndex != 0 {
		t.Errorf("second focus: expected 0, got %d", m.focusIndex)
	}
	// 0 → 2 (wrap)
	_ = m.focusPrev()
	if m.focusIndex != 2 {
		t.Errorf("wrap focus: expected 2, got %d", m.focusIndex)
	}
}

func TestToggleParagraphFocus_Thought(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraThought, State: stateCollapsed, ThoughtTokens: 100},
		},
		focusIndex: 0,
	}

	// Collapsed → Expanded
	m.toggleParagraphFocus()
	if m.paras[0].State != stateExpanded {
		t.Errorf("expected expanded, got %v", m.paras[0].State)
	}
	if !m.paras[0].renderDirty {
		t.Error("expected renderDirty=true after toggle")
	}

	// Expanded → Collapsed
	m.toggleParagraphFocus()
	if m.paras[0].State != stateCollapsed {
		t.Errorf("expected collapsed, got %v", m.paras[0].State)
	}
}

func TestToggleParagraphFocus_Tool(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraTool, State: stateDone, ToolName: "bash"},
		},
		focusIndex: 0,
	}

	// Done → Expanded
	m.toggleParagraphFocus()
	if m.paras[0].State != stateExpanded {
		t.Errorf("expected expanded, got %v", m.paras[0].State)
	}

	// Expanded → Done
	m.toggleParagraphFocus()
	if m.paras[0].State != stateDone {
		t.Errorf("expected done, got %v", m.paras[0].State)
	}
}

func TestToggleParagraphFocus_InvalidIndex(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraThought, State: stateCollapsed},
		},
		focusIndex: -1, // 未聚焦
	}
	// 不应 panic
	m.toggleParagraphFocus()
	if m.paras[0].State != stateCollapsed {
		t.Error("state should not change when focusIndex=-1")
	}

	m.focusIndex = 5 // 越界
	m.toggleParagraphFocus()
	if m.paras[0].State != stateCollapsed {
		t.Error("state should not change when focusIndex out of bounds")
	}
}

func TestFocusEsc_ReturnsToInput(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraThought, State: stateCollapsed},
		},
		focusIndex: 0,
		keys:       defaultKeys,
		width:      120,
		height:     40,
	}

	handled, _ := m.handleKeyPress(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	if !handled {
		t.Error("expected Esc handled when focused")
	}
	if m.focusIndex != -1 {
		t.Errorf("expected focusIndex=-1 after Esc, got %d", m.focusIndex)
	}
}

func TestFocusEnter_Toggle(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraThought, State: stateCollapsed, ThoughtTokens: 100},
		},
		focusIndex: 0,
		keys:       defaultKeys,
		width:      120,
		height:     40,
	}

	handled, _ := m.handleKeyPress(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !handled {
		t.Error("expected Enter handled when focused")
	}
	if m.paras[0].State != stateExpanded {
		t.Errorf("expected expanded after Enter, got %v", m.paras[0].State)
	}
}

func TestFocusTab_FromInput(t *testing.T) {
	paras := []Paragraph{
		{Type: paraThought, State: stateCollapsed, ThoughtTokens: 100, Text: strings.Repeat("long thought content that overflows the collapsed preview ", 10)},
	}
	m := &model{
		paras:      paras,
		focusIndex: -1,
		keys:       defaultKeys,
		width:      120,
		height:     40,
	}

	handled, _ := m.handleKeyPress(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if !handled {
		t.Error("expected Tab handled")
	}
	if m.focusIndex != 0 {
		t.Errorf("expected focusIndex=0 after Tab, got %d", m.focusIndex)
	}
}

func TestFocusTab_NoOpWhenRunning(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraThought, State: stateCollapsed, Text: strings.Repeat("overflowing ", 50)},
		},
		focusIndex: -1,
		running:    true, // 运行中
		keys:       defaultKeys,
		width:      120,
		height:     40,
	}

	handled, _ := m.handleKeyPress(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if handled {
		t.Error("Tab should not be handled when running")
	}
}

func TestFocusUpDown_Navigation(t *testing.T) {
	paras := []Paragraph{
		{Type: paraThought, State: stateCollapsed, ThoughtTokens: 100, Text: strings.Repeat("overflowing thought content ", 30)},
		{Type: paraAssistant, State: stateDone},
		{Type: paraTool, State: stateDone, ToolName: "bash", ToolResult: strings.Repeat("line\n", 10)},
	}
	m := &model{
		paras:      paras,
		focusIndex: 0, // 聚焦第一个
		keys:       defaultKeys,
		width:      120,
		height:     40,
	}

	// ↓ → 下一个可交互段落到 focusIndex 2
	handled, _ := m.handleKeyPress(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if !handled {
		t.Error("expected Down handled when focused")
	}
	if m.focusIndex != 2 {
		t.Errorf("expected focusIndex=2 after Down, got %d", m.focusIndex)
	}

	// ↑ → 回到 focusIndex 0
	handled, _ = m.handleKeyPress(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	if !handled {
		t.Error("expected Up handled when focused")
	}
	if m.focusIndex != 0 {
		t.Errorf("expected focusIndex=0 after Up, got %d", m.focusIndex)
	}
}

func TestTrimParas_AdjustsFocusIndex(t *testing.T) {
	paras := make([]Paragraph, maxParas+5)
	for i := range paras {
		paras[i] = Paragraph{Type: paraThought, State: stateCollapsed, ThoughtTokens: i}
	}
	m := &model{paras: paras, focusIndex: 10} // 聚焦第 10 个

	m.trimParas()

	// 淘汰了前 5 个，focusIndex 应从 10 → 5
	if m.focusIndex != 5 {
		t.Errorf("expected focusIndex=5 after trim, got %d", m.focusIndex)
	}
	if len(m.paras) != maxParas {
		t.Errorf("expected %d paras after trim, got %d", maxParas, len(m.paras))
	}
}

func TestTrimParas_FocusBecomesNegative(t *testing.T) {
	paras := make([]Paragraph, maxParas+5)
	for i := range paras {
		paras[i] = Paragraph{Type: paraThought, State: stateCollapsed, ThoughtTokens: i}
	}
	m := &model{paras: paras, focusIndex: 2} // focusIndex < remove(5) → 归位

	m.trimParas()

	if m.focusIndex != -1 {
		t.Errorf("expected focusIndex=-1 after trim eviction, got %d", m.focusIndex)
	}
}

// ============================================================================
// isTimeoutError
// ============================================================================

func TestIsTimeoutError_DeadlineExceeded(t *testing.T) {
	if !isTimeoutError(context.DeadlineExceeded) {
		t.Error("expected true for context.DeadlineExceeded")
	}
}

func TestIsTimeoutError_WrappedDeadlineExceeded(t *testing.T) {
	// Go 的 %w 包装后，errors.Is 应能穿透
	err := context.DeadlineExceeded
	wrapped := &testError{msg: "tool execution: " + err.Error(), wrapped: err}
	if !isTimeoutError(wrapped) {
		t.Error("expected true for wrapped context.DeadlineExceeded via errors.Is")
	}
}

func TestIsTimeoutError_StringMatch(t *testing.T) {
	// 某些错误可能不包装 deadline exceeded，但字符串包含
	err := &testError{msg: "context deadline exceeded during tool execution"}
	if !isTimeoutError(err) {
		t.Error("expected true for string containing 'deadline exceeded'")
	}
}

func TestIsTimeoutError_Canceled(t *testing.T) {
	if isTimeoutError(context.Canceled) {
		t.Error("expected false for context.Canceled (user interrupt)")
	}
}

func TestIsTimeoutError_Nil(t *testing.T) {
	if isTimeoutError(nil) {
		t.Error("expected false for nil")
	}
}

func TestIsTimeoutError_OtherError(t *testing.T) {
	err := &testError{msg: "some random error"}
	if isTimeoutError(err) {
		t.Error("expected false for unrelated error")
	}
}

// testError 实现 error 和 Unwrap 接口。
type testError struct {
	msg     string
	wrapped error
}

func (e *testError) Error() string { return e.msg }
func (e *testError) Unwrap() error { return e.wrapped }

// ---------------------------------------------------------------------------
// syncInputVisibleStart 测试
// ---------------------------------------------------------------------------

// runeDisplayWidth 计算字符串中所有 rune 的 lipgloss 显示宽度之和。
func runeDisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += lipgloss.Width(string(r))
	}
	return w
}

func TestRuneDisplayWidth_ASCII(t *testing.T) {
	if w := runeDisplayWidth("hello"); w != 5 {
		t.Errorf("expected width=5, got %d", w)
	}
}

func TestRuneDisplayWidth_CJK(t *testing.T) {
	if w := runeDisplayWidth("你好"); w != 4 {
		t.Errorf("expected width=4, got %d", w)
	}
}

func TestRuneDisplayWidth_Mixed(t *testing.T) {
	w := runeDisplayWidth("a你b好")
	if w != 6 {
		t.Errorf("expected width=6, got %d", w)
	}
}

// ---------------------------------------------------------------------------
// handleToolStream 回归测试
// ---------------------------------------------------------------------------

func TestHandleToolStream_AppendsChunk(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraTool, State: stateStreaming, ToolName: "bash", ToolArgs: "make build"},
		},
	}
	ev := agentloop.ToolCallStream{
		ToolCallID:   "call_1",
		ToolCallName: "bash",
		Chunk:        "hello\n",
	}
	m.handleToolStream(ev)
	p := &m.paras[0]
	if p.ToolResult != "hello\n" {
		t.Errorf("ToolResult = %q, want %q", p.ToolResult, "hello\n")
	}
	if !p.renderDirty {
		t.Error("renderDirty should be true")
	}
}

func TestHandleToolStream_Accumulates(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraTool, State: stateStreaming, ToolName: "bash", ToolArgs: "make build"},
		},
	}
	m.handleToolStream(agentloop.ToolCallStream{ToolCallID: "call_1", ToolCallName: "bash", Chunk: "line1\n"})
	m.handleToolStream(agentloop.ToolCallStream{ToolCallID: "call_1", ToolCallName: "bash", Chunk: "line2\n"})
	p := &m.paras[0]
	if p.ToolResult != "line1\nline2\n" {
		t.Errorf("ToolResult = %q, want %q", p.ToolResult, "line1\nline2\n")
	}
}

func TestHandleToolStream_NoMatch(t *testing.T) {
	// 没有匹配的 streaming tool 段落时应 no-op
	m := &model{
		paras: []Paragraph{
			{Type: paraTool, State: stateDone, ToolName: "bash", ToolArgs: "make build"},
		},
	}
	m.handleToolStream(agentloop.ToolCallStream{ToolCallID: "call_1", ToolCallName: "bash", Chunk: "hello\n"})
	// stateDone 的段落不应被修改
	p := &m.paras[0]
	if p.ToolResult != "" {
		t.Errorf("ToolResult should remain empty for non-streaming paragraph, got %q", p.ToolResult)
	}
}

// ---------------------------------------------------------------------------
// relativizePaths
// ---------------------------------------------------------------------------

// platformAbsPath 返回跨平台兼容的假绝对路径（仅路径运算，不访问文件系统）。
// Windows: C:\<parts>; Unix: /<parts>。
func platformAbsPath(t *testing.T, parts ...string) string {
	t.Helper()
	joined := filepath.Join(parts...)
	if runtime.GOOS == "windows" {
		return "C:" + string(filepath.Separator) + joined
	}
	return string(filepath.Separator) + joined
}

func TestRelativizePaths_NormalRelative(t *testing.T) {
	cwd := platformAbsPath(t, "home", "user", "project")
	paths := []string{"main.go", filepath.Join("cmd", "waveloom", "tui.go")}
	result := relativizePaths(paths, cwd)
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
	if result[0] != "main.go" {
		t.Errorf("expected main.go, got %q", result[0])
	}
	if result[1] != filepath.Join("cmd", "waveloom", "tui.go") {
		t.Errorf("expected %q, got %q", filepath.Join("cmd", "waveloom", "tui.go"), result[1])
	}
}

func TestRelativizePaths_Absolute(t *testing.T) {
	cwd := platformAbsPath(t, "home", "user", "project")
	// 同卷绝对路径 → 转为 cwd 相对路径；兄弟目录绝对路径 → ../ 相对路径
	paths := []string{
		filepath.Join(cwd, "main.go"),
		platformAbsPath(t, "home", "user", "other", "lib.rs"),
	}
	result := relativizePaths(paths, cwd)
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
	if result[0] != "main.go" {
		t.Errorf("expected main.go, got %q", result[0])
	}
	if result[1] != filepath.Join("..", "other", "lib.rs") {
		t.Errorf("expected %q, got %q", filepath.Join("..", "other", "lib.rs"), result[1])
	}
}

// TestRegression_RelativizePathsParentDir 验证 @../ 扫描时
// walkFn 返回的 ../ 前缀路径能正确保留并转为 cwd 相对路径。
// 根因：修复前 walkFn 以 absRoot 为基准做 Rel，产生不含 ../ 的错误路径。
func TestRegression_RelativizePathsParentDir(t *testing.T) {
	cwd := platformAbsPath(t, "home", "user", "project")
	// 模拟 WalkDir 从父目录扫描发现的文件，
	// walkFn 修复后以 cwd 为基准做 Rel，路径正确携带 ../ 前缀。
	paths := []string{
		filepath.Join("..", "sibling", "file.go"),
		filepath.Join("..", "claude-code", "main.go"),
		filepath.Join("..", "..", "other", "src", "lib.rs"),
	}
	result := relativizePaths(paths, cwd)
	if len(result) != 3 {
		t.Fatalf("expected 3, got %d", len(result))
	}
	expect := []string{
		filepath.Join("..", "sibling", "file.go"),
		filepath.Join("..", "claude-code", "main.go"),
		filepath.Join("..", "..", "other", "src", "lib.rs"),
	}
	for i, exp := range expect {
		if result[i] != exp {
			t.Errorf("result[%d]: expected %q, got %q", i, exp, result[i])
		}
	}
}
