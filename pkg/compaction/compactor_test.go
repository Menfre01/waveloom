package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"waveloom/pkg/llm"
)

// ---------------------------------------------------------------------------
// NewCompactor
// ---------------------------------------------------------------------------

func TestNewCompactor_DefaultConfig(t *testing.T) {
	c := NewCompactor(DefaultCompactionConfig(), nil)

	if c.ContextLimit() != DefaultContextLimit {
		t.Fatalf("expected context limit %d, got %d", DefaultContextLimit, c.ContextLimit())
	}
	w := c.Watermark()
	if w.Tier1Cursor != 2 {
		t.Fatalf("expected Tier1Cursor=2, got %d", w.Tier1Cursor)
	}
	lr := c.LastResult()
	if lr.Tier != 0 {
		t.Fatalf("expected initial Tier=0, got %d", lr.Tier)
	}
}

// ---------------------------------------------------------------------------
// Compact
// ---------------------------------------------------------------------------

func TestCompact_Tier0_Below60(t *testing.T) {
	c := NewCompactor(DefaultCompactionConfig(), nil)
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}

	tick := c.Compact(context.Background(), &messages, int(float64(DefaultContextLimit)*0.30))
	if tick.Tier != 0 {
		t.Fatalf("expected Tier 0 at 30%%, got Tier %d", tick.Tier)
	}
	if tick.HardLimitReached {
		t.Fatal("should not reach hard limit at 30%")
	}
}

func TestCompact_Tier1_Snip(t *testing.T) {
	c := NewCompactor(DefaultCompactionConfig(), nil)
	lines := make([]string, 500)
	for i := range lines {
		lines[i] = "line content for tool output that exceeds threshold"
	}
	content := strings.Join(lines, "\n")
	messages, _ := buildMessagesOutsideProtection(
		llm.Message{Role: llm.RoleTool, Content: content, Name: "read_file", ToolCallID: "tc1"},
	)

	// 65% → Tier 1
	promptTokens := int(float64(DefaultContextLimit) * 0.65)
	tick := c.Compact(context.Background(), &messages, promptTokens)
	if tick.Tier < 1 {
		t.Fatalf("expected Tier >= 1 at 65%%, got Tier %d", tick.Tier)
	}
	if tick.MessagesSnipped == 0 {
		t.Fatal("expected at least 1 snipped message")
	}

	// Decisions 不应为空
	decisions := c.Decisions()
	if len(decisions) == 0 {
		t.Fatal("expected non-empty decisions after Tier 1")
	}
}

func TestCompact_HardLimit_Usage(t *testing.T) {
	c := NewCompactor(DefaultCompactionConfig(), nil)
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}

	// 99% → Hard limit
	promptTokens := int(float64(DefaultContextLimit) * 0.99)
	tick := c.Compact(context.Background(), &messages, promptTokens)
	if !tick.HardLimitReached {
		t.Fatal("expected HardLimitReached=true at 99% usage")
	}
	if tick.HardLimitReason != "usage" {
		t.Fatalf("expected HardLimitReason=usage, got %q", tick.HardLimitReason)
	}
}

func TestCompact_MonotonicDecisions(t *testing.T) {
	c := NewCompactor(DefaultCompactionConfig(), nil)
	lines := make([]string, 500)
	for i := range lines {
		lines[i] = "line content"
	}
	content := strings.Join(lines, "\n")

	messages, _ := buildMessagesOutsideProtection(
		llm.Message{Role: llm.RoleTool, Content: content, Name: "read_file", ToolCallID: "tc1"},
	)

	// Round 1: Tier 1 at 65%
	tick1 := c.Compact(context.Background(), &messages, int(float64(DefaultContextLimit)*0.65))
	if tick1.Tier < 1 {
		t.Fatalf("round 1: expected Tier >= 1, got %d", tick1.Tier)
	}
	d1 := c.Decisions()

	// Round 2: Tier 2 at 85%
	tick2 := c.Compact(context.Background(), &messages, int(float64(DefaultContextLimit)*0.85))
	if tick2.Tier < 2 {
		t.Fatalf("round 2: expected Tier >= 2, got %d", tick2.Tier)
	}
	d2 := c.Decisions()

	// 决策应单调递增（不降级）
	for _, dec1 := range d1 {
		for _, dec2 := range d2 {
			if dec2.MsgIndex == dec1.MsgIndex {
				if dec1.Action == "prune" && dec2.Action == "snip" {
					t.Fatalf("decision for index %d downgraded from prune to snip", dec1.MsgIndex)
				}
				break
			}
		}
	}
}

// ---------------------------------------------------------------------------
// mock Summarizer
// ---------------------------------------------------------------------------

// mockSummarizer 是一个可注入的 Summarizer 实现，用于 Tier 3 单测。
type mockSummarizer struct {
	result string
	err    error
}

func (m *mockSummarizer) Summarize(_ context.Context, _ []string, _ []llm.Message) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.result, nil
}

// ---------------------------------------------------------------------------
// Tier 2: Prune
// ---------------------------------------------------------------------------

func TestCompact_Tier2_Prune(t *testing.T) {
	c := NewCompactor(DefaultCompactionConfig(), nil)

	// 构造可被扫描的消息：assistant(reasoning) + tool(read_file) + user(code fence >50行)
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "start"},
	}
	asstIdx := len(messages)
	messages = append(messages, llm.Message{
		Role:            llm.RoleAssistant,
		Content:         "response",
		ReasoningContent: strings.Repeat("thinking deeply about the problem ", 200),
	})
	toolIdx := len(messages)
	messages = append(messages, llm.Message{
		Role: llm.RoleTool, Content: strings.Repeat("result line\n", 300), Name: "read_file", ToolCallID: "tc1",
	})
	userIdx := len(messages)
	messages = append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: "Here is the code:\n```go\n" + strings.Repeat("  fmt.Println(\"hello\")\n", 80) + "```\n",
	})
	// 填充保护区
	for i := 0; i < 80; i++ {
		messages = append(messages, llm.Message{
			Role: llm.RoleTool, Content: strings.Repeat("x", 400), Name: "ls", ToolCallID: fmt.Sprintf("pad%d", i),
		})
	}

	// 85% → Tier 2
	tick := c.Compact(context.Background(), &messages, int(float64(DefaultContextLimit)*0.85))
	if tick.Tier < 2 {
		t.Fatalf("expected Tier >= 2 at 85%%, got Tier %d", tick.Tier)
	}
	if tick.MessagesPruned == 0 {
		t.Fatal("expected at least 1 pruned message")
	}

	// 验证 reasoning 被清空
	if messages[asstIdx].ReasoningContent != "" {
		t.Fatal("expected assistant reasoning to be cleared")
	}

	// 验证 tool 结果被替换为占位符
	if !strings.Contains(messages[toolIdx].Content, "compressed") {
		t.Fatalf("expected tool placeholder, got: %s", messages[toolIdx].Content)
	}

	// 验证 user code block 被压缩
	if !strings.Contains(messages[userIdx].Content, ">50 lines") {
		t.Fatalf("expected user code block placeholder, got: %s", messages[userIdx].Content)
	}
}

// ---------------------------------------------------------------------------
// Tier 3: Summarize
// ---------------------------------------------------------------------------

func TestCompact_Tier3_Success(t *testing.T) {
	summaryJSON := `{"progress":{"summary":"test","files":[]},"pending":[],"pitfalls":[],"constraints":""}`
	ms := &mockSummarizer{result: summaryJSON}
	c := NewCompactor(DefaultCompactionConfig(), ms)

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "start"},
	}
	// 放入一些可被摘要的消息（索引 2 起，不在保护区）
	for i := 0; i < 10; i++ {
		messages = append(messages, llm.Message{
			Role: llm.RoleUser, Content: fmt.Sprintf("message %d content", i),
		})
	}
	// 填充保护区
	for i := 0; i < 80; i++ {
		messages = append(messages, llm.Message{
			Role: llm.RoleTool, Content: strings.Repeat("x", 400), Name: "ls", ToolCallID: fmt.Sprintf("pad%d", i),
		})
	}

	oldLen := len(messages)

	// 96% → Tier 3
	tick := c.Compact(context.Background(), &messages, int(float64(DefaultContextLimit)*0.96))
	if tick.Tier != 3 {
		t.Fatalf("expected Tier 3 at 96%%, got Tier %d", tick.Tier)
	}
	if !tick.Tier3SummaryDone {
		t.Fatal("expected Tier3SummaryDone=true")
	}

	// 消息数组应收缩（delta 删除 + 摘要插入）
	if len(messages) >= oldLen {
		t.Fatalf("expected messages to shrink after Tier 3, old=%d new=%d", oldLen, len(messages))
	}

	// decisions 应被清空
	if len(c.Decisions()) != 0 {
		t.Fatal("expected decisions to be cleared after Tier 3")
	}

	// cursor 应重置
	w := c.Watermark()
	if w.Tier1Cursor != w.Tier3Cursor || w.Tier2Cursor != w.Tier3Cursor {
		t.Fatal("expected all cursors to be equal after Tier 3")
	}
}

func TestCompact_Tier3_ConsecutiveFailures(t *testing.T) {
	ms := &mockSummarizer{err: errors.New("summarizer unavailable")}
	c := NewCompactor(DefaultCompactionConfig(), ms)

	buildMessages := func() []llm.Message {
		msgs := []llm.Message{
			{Role: llm.RoleSystem, Content: "system"},
			{Role: llm.RoleUser, Content: "start"},
		}
		for i := 0; i < 10; i++ {
			msgs = append(msgs, llm.Message{
				Role: llm.RoleUser, Content: fmt.Sprintf("msg %d", i),
			})
		}
		for i := 0; i < 80; i++ {
			msgs = append(msgs, llm.Message{
				Role: llm.RoleTool, Content: strings.Repeat("x", 400), Name: "ls", ToolCallID: fmt.Sprintf("pad%d", i),
			})
		}
		return msgs
	}
	usage := int(float64(DefaultContextLimit) * 0.96)

	// Round 1: Tier 3 失败，failures 0→1
	messages1 := buildMessages()
	c.AdvanceTurn()
	tick1 := c.Compact(context.Background(), &messages1, usage)
	if tick1.Tier != 3 {
		t.Fatalf("round 1: expected Tier 3, got %d", tick1.Tier)
	}
	if tick1.Tier3SummaryDone {
		t.Fatal("round 1: Tier3SummaryDone should be false on failure")
	}
	if tick1.HardLimitReached {
		t.Fatal("round 1: hard limit should not trigger on first failure")
	}

	// Round 2: Tier 3 再次失败，failures 1→2（本轮仍不触发硬限）
	messages2 := buildMessages()
	c.AdvanceTurn()
	tick2 := c.Compact(context.Background(), &messages2, usage)
	if tick2.Tier != 3 {
		t.Fatalf("round 2: expected Tier 3, got %d", tick2.Tier)
	}
	if tick2.HardLimitReached {
		t.Fatal("round 2: hard limit should not trigger until check sees failures >= 2")
	}
	w2 := c.Watermark()
	if w2.Tier3ConsecutiveFailures != 2 {
		t.Fatalf("round 2: expected 2 consecutive failures, got %d", w2.Tier3ConsecutiveFailures)
	}

	// Round 3: checkHardLimit 看到 failures=2 → 硬限
	messages3 := buildMessages()
	c.AdvanceTurn()
	tick3 := c.Compact(context.Background(), &messages3, usage)
	if !tick3.HardLimitReached {
		t.Fatal("round 3: expected HardLimitReached after 2 consecutive failures")
	}
	if tick3.HardLimitReason != "tier3_failures" {
		t.Fatalf("round 3: expected HardLimitReason=tier3_failures, got %q", tick3.HardLimitReason)
	}
}

func TestSnapshot_Restore(t *testing.T) {
	c1 := NewCompactor(DefaultCompactionConfig(), nil)

	// Apply some compaction to generate state
	lines := make([]string, 500)
	for i := range lines {
		lines[i] = "line content"
	}
	content := strings.Join(lines, "\n")
	messages, _ := buildMessagesOutsideProtection(
		llm.Message{Role: llm.RoleTool, Content: content, Name: "read_file", ToolCallID: "tc1"},
	)
	_ = c1.Compact(context.Background(), &messages, int(float64(DefaultContextLimit)*0.65))

	// Snapshot
	data := c1.Snapshot()
	if len(data.Decisions) == 0 {
		t.Fatal("expected non-empty decisions in snapshot")
	}

	// Restore into a fresh compactor
	c2 := NewCompactor(DefaultCompactionConfig(), nil)
	c2.Restore(data)

	// Verify restored state
	if len(c2.Decisions()) != len(c1.Decisions()) {
		t.Fatalf("decisions mismatch: %d vs %d", len(c2.Decisions()), len(c1.Decisions()))
	}
	w1 := c1.Watermark()
	w2 := c2.Watermark()
	if w1.UsageRatio != w2.UsageRatio {
		t.Fatalf("watermark mismatch: ratio %f vs %f", w1.UsageRatio, w2.UsageRatio)
	}
	if w1.Tier1Cursor != w2.Tier1Cursor {
		t.Fatalf("cursor mismatch: Tier1Cursor %d vs %d", w1.Tier1Cursor, w2.Tier1Cursor)
	}
}

// ---------------------------------------------------------------------------
// Reset
// ---------------------------------------------------------------------------

func TestCompactor_Reset(t *testing.T) {
	c := NewCompactor(DefaultCompactionConfig(), nil)

	// Apply compaction
	lines := make([]string, 500)
	for i := range lines {
		lines[i] = "line content"
	}
	content := strings.Join(lines, "\n")
	messages, _ := buildMessagesOutsideProtection(
		llm.Message{Role: llm.RoleTool, Content: content, Name: "read_file", ToolCallID: "tc1"},
	)
	_ = c.Compact(context.Background(), &messages, int(float64(DefaultContextLimit)*0.65))

	if len(c.Decisions()) == 0 {
		t.Fatal("expected non-empty decisions before reset")
	}

	c.Reset()

	if len(c.Decisions()) != 0 {
		t.Fatal("decisions should be empty after reset")
	}
	w := c.Watermark()
	if w.Tier1Cursor != 2 {
		t.Fatalf("expected cursor reset to 2, got %d", w.Tier1Cursor)
	}
	lr := c.LastResult()
	if lr.Tier != 0 {
		t.Fatalf("expected Tier=0 after reset, got %d", lr.Tier)
	}
}

// ---------------------------------------------------------------------------
// SetContextLimit
// ---------------------------------------------------------------------------

func TestCompactor_SetContextLimit(t *testing.T) {
	c := NewCompactor(DefaultCompactionConfig(), nil)
	c.SetContextLimit(500000)
	if c.ContextLimit() != 500000 {
		t.Fatalf("expected 500000, got %d", c.ContextLimit())
	}
}

// ---------------------------------------------------------------------------
// ProtectionZone
// ---------------------------------------------------------------------------

func TestCompact_ProtectionZone(t *testing.T) {
	c := NewCompactor(DefaultCompactionConfig(), nil)

	// 构造消息：target 在保护区外，填充消息在保护区内
	var linesBuilder strings.Builder
	for i := 0; i < 500; i++ {
		linesBuilder.WriteString("long line of text that takes up space for compaction testing purposes\n")
	}
	content := linesBuilder.String()

	messages, targetIdx := buildMessagesOutsideProtection(
		llm.Message{Role: llm.RoleTool, Content: content, Name: "read_file", ToolCallID: "tc1"},
	)

	// 65% → Tier 1
	tick := c.Compact(context.Background(), &messages, int(float64(DefaultContextLimit)*0.65))

	// target 消息在保护区外，应被决策
	decisions := c.Decisions()
	found := false
	for _, dec := range decisions {
		if dec.MsgIndex == targetIdx {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("target message at index %d (outside protection zone) should have a snip decision", targetIdx)
	}

	// 验证至少有一些 snipped 消息
	if tick.MessagesSnipped == 0 {
		t.Fatal("expected at least 1 snipped message")
	}
}

// ---------------------------------------------------------------------------
// 测试辅助
// ---------------------------------------------------------------------------

// buildMessagesOutsideProtection 构造一个 messages 切片，其中前两条为 system + user，
// 然后插入 target 消息，之后再追加足够的填充消息使尾部形成 8000 token 保护区。
// target 消息位于保护区之前，确保可被扫描。
// 返回完整 messages 切片和 target 消息的起始索引。
func buildMessagesOutsideProtection(targets ...llm.Message) ([]llm.Message, int) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "user"},
	}
	targetIdx := len(messages)
	messages = append(messages, targets...)
	// 在末尾追加填充消息，形成保护区（target 消息在保护区之前）
	for i := 0; i < 80; i++ {
		messages = append(messages, llm.Message{
			Role: llm.RoleTool, Content: strings.Repeat("x", 400), Name: "ls", ToolCallID: "pad",
		})
	}
	return messages, targetIdx
}

// ---------------------------------------------------------------------------
// compressUserCodeBlocks
// ---------------------------------------------------------------------------

func TestCompressUserCodeBlocks_NoFence(t *testing.T) {
	content := "plain text without any code block"
	result, did := compressUserCodeBlocks(content)
	if did {
		t.Fatal("expected no compression for fence-free content")
	}
	if result != content {
		t.Fatalf("content should be unchanged, got: %s", result)
	}
}

func TestCompressUserCodeBlocks_SmallFence(t *testing.T) {
	// ≤50 行的 fence 不应被压缩
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "  some code"
	}
	content := "before\n```go\n" + strings.Join(lines, "\n") + "\n```\nafter"
	result, did := compressUserCodeBlocks(content)
	if did {
		t.Fatal("expected no compression for fence with ≤50 lines")
	}
	if result != content {
		t.Fatalf("content should be unchanged, got: %s", result)
	}
}

func TestCompressUserCodeBlocks_LargeFence(t *testing.T) {
	// >50 lines的 fence 应被压缩
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "  some code"
	}
	content := "before\n```go\n" + strings.Join(lines, "\n") + "\n```\nafter"
	result, did := compressUserCodeBlocks(content)
	if !did {
		t.Fatal("expected compression for fence with >50 lines")
	}
	if !strings.Contains(result, ">50 lines") {
		t.Fatalf("expected placeholder in compressed output, got: %s", result)
	}
	// 前 50 行保留
	for i := 0; i < 50; i++ {
		if !strings.Contains(result, "  some code") {
			t.Fatalf("first 50 lines should be preserved")
		}
		break
	}
	// 结尾 after 应保留
	if !strings.Contains(result, "after") {
		t.Fatal("expected 'after' to be preserved")
	}
}

func TestCompressUserCodeBlocks_NestedBacktickInFence(t *testing.T) {
	// 4 个反引号开启的 fence，内部的 3 反引号不应关闭
	content := "before\n````\nline1\n```\nline3\n````\nafter"
	result, did := compressUserCodeBlocks(content)
	if did {
		t.Fatal("expected no compression for small fence with embedded backticks")
	}
	if result != content {
		t.Fatalf("content should be unchanged, got: %s", result)
	}
}

func TestCompressUserCodeBlocks_UnterminatedFence(t *testing.T) {
	// 未闭合的 fence，内部 >50 lines
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = "code"
	}
	content := "before\n```go\n" + strings.Join(lines, "\n")
	result, did := compressUserCodeBlocks(content)
	if !did {
		t.Fatal("expected compression for unterminated fence with >50 lines")
	}
	if !strings.Contains(result, ">50 lines") {
		t.Fatalf("expected placeholder, got: %s", result)
	}
	// 结尾不应有 trailing fence line
	if strings.HasSuffix(strings.TrimSpace(result), "```") {
		t.Fatal("unterminated fence should not add closing backticks")
	}
}

func TestCompressUserCodeBlocks_TrailingNewline(t *testing.T) {
	content := "no fence here\n"
	result, _ := compressUserCodeBlocks(content)
	if len(result) == 0 || result[len(result)-1] != '\n' {
		t.Fatal("expected trailing newline to be preserved")
	}
}

func TestCompressUserCodeBlocks_MultipleFences(t *testing.T) {
	// 两个 fence，一个大一个小
	small := make([]string, 10)
	for i := range small {
		small[i] = "a"
	}
	large := make([]string, 80)
	for i := range large {
		large[i] = "b"
	}
	content := "start\n```\n" + strings.Join(small, "\n") + "\n```\nmiddle\n```\n" + strings.Join(large, "\n") + "\n```\nend"
	result, did := compressUserCodeBlocks(content)
	if !did {
		t.Fatal("expected compression for multiple fences with one >50")
	}
	if !strings.Contains(result, ">50 lines") {
		t.Fatal("expected placeholder for large fence")
	}
	if !strings.Contains(result, "start") || !strings.Contains(result, "middle") || !strings.Contains(result, "end") {
		t.Fatal("expected surrounding text preserved")
	}
	// 小型 fence 应完整保留
	if !strings.Contains(result, "a") {
		t.Fatal("small fence should be preserved intact")
	}
}

func TestCompressUserCodeBlocks_SingleLongLine(t *testing.T) {
	// fence 内只有一行但超过 2000 字符 → 应触发单行截断
	longLine := strings.Repeat("x", 5000)
	content := "before\n```\n" + longLine + "\n```\nafter"
	result, did := compressUserCodeBlocks(content)
	if !did {
		t.Fatal("expected compression for fence with single super-long line")
	}
	if !strings.Contains(result, "单行截断") {
		t.Fatalf("expected line truncation marker, got: %s", result[:200])
	}
	if strings.Contains(result, longLine) {
		t.Fatal("long line should be truncated")
	}
	if !strings.Contains(result, "before") || !strings.Contains(result, "after") {
		t.Fatal("expected surrounding text preserved")
	}
}

// ---------------------------------------------------------------------------
// countLeadingBackticks
// ---------------------------------------------------------------------------

func TestCountLeadingBackticks(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"```", 3},
		{"```go", 3},
		{"````", 4},
		{"`````", 5},
		{"  ```", 0},
		{"abc", 0},
		{"", 0},
		{"`", 1},
		{"``", 2},
	}
	for _, tc := range tests {
		got := countLeadingBackticks(tc.input)
		if got != tc.want {
			t.Errorf("countLeadingBackticks(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// countLeadingBackticks
// ---------------------------------------------------------------------------
