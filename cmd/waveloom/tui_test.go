package main

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	ctxpkg "waveloom/pkg/context"
	"waveloom/pkg/llm"
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
	if !strings.Contains(lastPara.Text, "已被阻止") {
		t.Errorf("expected block message, got %q", lastPara.Text)
	}
}

func TestHardLimitGuard_AllowsEnterWhenNotReached(t *testing.T) {
	cm := newTestCM()

	m := &model{
		cm:    cm,
		keys:  defaultKeys,
		paras: []Paragraph{},
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
