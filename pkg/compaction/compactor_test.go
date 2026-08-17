package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
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
	mu          sync.Mutex
	result      string
	err         error
	calls       int
	chains      [][]string      // 每次调用收到的摘要链
	deltas      [][]llm.Message // 每次调用收到的 delta 消息(验证游标/摘要范围)
}

func (m *mockSummarizer) Summarize(_ context.Context, existing []string, delta []llm.Message) (string, error) {
	m.mu.Lock()
	m.calls++
	m.chains = append(m.chains, append([]string(nil), existing...))
	m.deltas = append(m.deltas, append([]llm.Message(nil), delta...))
	m.mu.Unlock()
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
		ToolCalls:       []llm.ToolCall{{ID: "tc1", Name: "read_file"}}, // 红线防线:配对 tool 消息
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
	messages = appendToolRun(messages, 80)

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
	messages = appendToolRun(messages, 80)

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

// TestRegression_Tier3_PreservesProtectionZone 回归防护:
// applyTier3 重建消息数组时容量预留了保护区空间(oldLen-scanEnd)
// 但从未 append——95% 压缩静默删除最近 8000 tokens(保护区),
// 违反 findProtectionStartIdx "最近消息不参与压缩"语义。
func TestRegression_Tier3_PreservesProtectionZone(t *testing.T) {
	summaryJSON := `{"progress":{"summary":"test","files":[]},"pending":[],"pitfalls":[],"constraints":""}`
	ms := &mockSummarizer{result: summaryJSON}
	c := NewCompactor(DefaultCompactionConfig(), ms)

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "start"},
	}
	// delta 消息(索引 2 起,可被摘要)
	for i := 0; i < 10; i++ {
		messages = append(messages, llm.Message{
			Role: llm.RoleUser, Content: fmt.Sprintf("delta message %d", i),
		})
	}
	// 配对完整的 tool run(填充中间)
	messages = appendToolRun(messages, 20)
	// 保护区标记消息:放在最后一条,必然落在保护区内(不被摘要/删除)
	marker := strings.Repeat("protection-zone-marker-", 2000) // 40K 字符 ≈ 12K tokens
	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: marker})

	oldLen := len(messages)

	tick := c.Compact(context.Background(), &messages, int(float64(DefaultContextLimit)*0.96))
	if tick.Tier != 3 {
		t.Fatalf("expected Tier 3 at 96%%, got Tier %d", tick.Tier)
	}
	if !tick.Tier3SummaryDone {
		t.Fatal("expected Tier3SummaryDone=true")
	}

	// 保护区标记消息必须保留
	found := false
	for _, m := range messages {
		if m.Content == marker {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("REGRESSION: Tier 3 压缩后保护区内消息被删除(最近 8000 tokens 丢失)")
	}

	// 消息仍应收缩(delta 删除 + 摘要插入,但保留保护区)
	if len(messages) >= oldLen {
		t.Fatalf("expected messages to shrink, old=%d new=%d", oldLen, len(messages))
	}
}

// TestRegression_Tier3_CursorSkipsSummary 回归防护:
// newCursor 曾为 tier3Cursor+1——布局 [:tier3Cursor] + notification + summary
// 中 summary 占据 tier3Cursor+1,游标指向摘要消息本身,下一轮 delta
// 重新包含摘要内容(已在 existingSummaries 链中)→ 链膨胀。
func TestRegression_Tier3_CursorSkipsSummary(t *testing.T) {
	summaryJSON := `{"progress":{"summary":"first round","files":[]},"pending":[],"pitfalls":[],"constraints":""}`
	ms := &mockSummarizer{result: summaryJSON}
	c := NewCompactor(DefaultCompactionConfig(), ms)

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "start"},
	}
	for i := 0; i < 10; i++ {
		messages = append(messages, llm.Message{
			Role: llm.RoleUser, Content: fmt.Sprintf("delta msg %d", i),
		})
	}
	messages = appendToolRun(messages, 20)
	// 保护区标记消息(单条 ≥ 8000 tokens,保证第二轮 delta 非空)
	marker := strings.Repeat("protection-zone-marker-", 2000)
	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: marker})

	// round 1:96% → Tier 3
	tick1 := c.Compact(context.Background(), &messages, int(float64(DefaultContextLimit)*0.96))
	if tick1.Tier != 3 || !tick1.Tier3SummaryDone {
		t.Fatalf("round 1: expected Tier 3 success, got %+v", tick1)
	}
	// 游标应指向摘要之后:前置 2 条 + notification + summary → 4
	if w := c.Watermark(); w.Tier3Cursor != 4 {
		t.Fatalf("Tier3Cursor 应指向摘要之后(=4),实际 %d", w.Tier3Cursor)
	}

	// 模拟新对话轮次:摘要后追加足够大的消息(单条 ≥8000 tokens,
	// 使保护区从它开始,摘要后的 marker 成为下一轮 delta)
	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: strings.Repeat("new turn ", 5000)})

	// round 2:再次 96% → Tier 3(新消息在保护区外)
	tick2 := c.Compact(context.Background(), &messages, int(float64(DefaultContextLimit)*0.96))
	if tick2.Tier != 3 || !tick2.Tier3SummaryDone {
		t.Fatalf("round 2: expected Tier 3 success, got %+v", tick2)
	}

	ms.mu.Lock()
	defer ms.mu.Unlock()
	if ms.calls < 2 {
		t.Fatalf("expected ≥2 Summarize calls, got %d", ms.calls)
	}
	for _, m := range ms.deltas[1] {
		if m.Content == summaryJSON {
			t.Fatal("REGRESSION: 第二轮 delta 包含第一轮摘要消息(游标未跳过摘要)")
		}
	}
	// 第二轮 delta 应包含 marker(增量语义:摘要后未覆盖的消息)
	foundNew := false
	for _, m := range ms.deltas[1] {
		if strings.HasPrefix(m.Content, "protection-zone-marker-") {
			foundNew = true
			break
		}
	}
	if !foundNew {
		t.Fatal("第二轮 delta 应包含摘要后未覆盖的消息(marker)")
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
		msgs = appendToolRun(msgs, 80)
		return msgs
	}
	usage := int(float64(DefaultContextLimit) * 0.96)

	// Round 1: Tier 3 失败,failures 0→1
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

	// Round 2: Tier 3 再次失败,failures 1→2(本轮仍不触发硬限)
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
	// 红线防线:targets 是 tool 消息,须在其前补配对 assistant
	// (收集所有 ToolCallID 挂到 assistant 的 tool_calls 上)
	if len(targets) > 0 {
		asst := llm.Message{Role: llm.RoleAssistant, Content: "tool dispatch"}
		for _, t := range targets {
			if t.Role == llm.RoleTool && t.ToolCallID != "" {
				asst.ToolCalls = append(asst.ToolCalls, llm.ToolCall{ID: t.ToolCallID, Name: t.Name})
			}
		}
		if len(asst.ToolCalls) > 0 {
			messages = append(messages, asst)
		}
	}
	targetIdx := len(messages)
	messages = append(messages, targets...)
	// 在末尾追加填充消息,形成保护区(target 消息在保护区之前)
	// 红线防线:测试数据必须配对完整(assistant + tool),否则 ValidateMessages 清理孤儿
	messages = appendToolRun(messages, 80)
	return messages, targetIdx
}

// appendToolRun 追加一条 assistant(带 n 个 tool_calls)后接 n 条配对 tool 消息。
// 红线防线(压缩后 ValidateMessages)会清理孤儿 tool 消息——测试数据必须
// 构造完整配对,否则断言基于消息数/索引会崩。
func appendToolRun(msgs []llm.Message, n int) []llm.Message {
	msgs = append(msgs, llm.Message{
		Role: llm.RoleAssistant, Content: "tool dispatch",
	})
	// 在 assistant 上挂 tool_calls
	idx := len(msgs) - 1
	msgs[idx].ToolCalls = make([]llm.ToolCall, n)
	for i := 0; i < n; i++ {
		msgs[idx].ToolCalls[i] = llm.ToolCall{ID: fmt.Sprintf("pad%d", i), Name: "ls"}
	}
	for i := 0; i < n; i++ {
		msgs = append(msgs, llm.Message{
			Role: llm.RoleTool, Content: strings.Repeat("x", 400), Name: "ls", ToolCallID: fmt.Sprintf("pad%d", i),
		})
	}
	return msgs
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
	if !strings.Contains(result, "  some code") {
		t.Fatalf("first 50 lines should be preserved")
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
	if !strings.Contains(result, "truncated") {
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

// TestRegression_AlignPairBoundary 回归防护(三审 Critical-1):
// tier3 删除边界必须对齐 assistant↔tool 配对,切割产生孤儿消息 → API 400。
func TestRegression_AlignPairBoundary(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "u0"},
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a", ToolCalls: []llm.ToolCall{{ID: "tc1"}}},
		{Role: llm.RoleTool, Content: "t1", ToolCallID: "tc1"},
		{Role: llm.RoleUser, Content: "u2"},
		{Role: llm.RoleAssistant, Content: "b", ToolCalls: []llm.ToolCall{{ID: "tc2"}}},
		{Role: llm.RoleTool, Content: "t2", ToolCallID: "tc2"},
		{Role: llm.RoleUser, Content: "u3"},
	}

	// 边界落在配对中间(tool 消息在 start,assistant 在 start-1)
	start1, _ := alignPairBoundary(msgs, 3, 5)
	if start1 != 2 {
		t.Errorf("forward align: start = %d, want 2 (include assistant)", start1)
	}
	// end 落在 assistant(tc2) 与其 tool 之间 → 向后包含 tool
	_, end2 := alignPairBoundary(msgs, 3, 6)
	if end2 != 7 {
		t.Errorf("backward align: end = %d, want 7 (include tool result)", end2)
	}
	// 无配对干扰的边界不变
	start3, end3 := alignPairBoundary(msgs, 4, 5)
	if start3 != 4 || end3 != 5 {
		t.Errorf("clean boundary moved: start=%d end=%d, want 4,5", start3, end3)
	}
}

// TestRegression_HardLimitRetriesTier3 回归防护(三审 Critical-2):
// 硬限分支(tier3_failures)此前从不重试 tier3 → 失败计数永不复位 → 会话永久终止。
// 修复后:硬限路径仍调用 applyTier3;摘要成功 → 计数清零 → 会话恢复。
func TestRegression_HardLimitRetriesTier3(t *testing.T) {
	ms := &mockSummarizer{}
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
		msgs = appendToolRun(msgs, 80)
		return msgs
	}
	usage := int(float64(DefaultContextLimit) * 0.96)

	// 模拟 2 次失败 → 硬限
	ms.err = errors.New("summarizer unavailable")
	messages1 := buildMessages()
	c.AdvanceTurn()
	_ = c.Compact(context.Background(), &messages1, usage)
	messages2 := buildMessages()
	c.AdvanceTurn()
	_ = c.Compact(context.Background(), &messages2, usage)

	// Round 3:硬限触发,但 summarizer 恢复可用 → 硬限路径重试 tier3 成功
	ms.err = nil
	messages3 := buildMessages()
	c.AdvanceTurn()
	tick3 := c.Compact(context.Background(), &messages3, usage)
	if !tick3.Tier3SummaryDone {
		t.Fatal("hard limit path should retry Tier 3 and succeed (Critical-2 regression)")
	}
	// 三审 High-2:摘要成功 → 硬限解除(否则调用层立即终止,恢复不可达)
	if tick3.HardLimitReached {
		t.Fatal("hard limit should be lifted after successful Tier 3 (High-2 regression)")
	}
	// 失败计数清零 → 下一轮不再硬限
	w := c.Watermark()
	if w.Tier3ConsecutiveFailures != 0 {
		t.Fatalf("expected failures reset after successful Tier 3, got %d", w.Tier3ConsecutiveFailures)
	}
	messages4 := buildMessages()
	c.AdvanceTurn()
	tick4 := c.Compact(context.Background(), &messages4, usage)
	if tick4.HardLimitReached {
		t.Fatal("session should recover after Tier 3 success (no permanent deadlock)")
	}
}

// TestRegression_AlignPairBoundary_BatchTools 回归防护(三审 High-1):
// 并行工具产生 A(c1,c2)→T1→T2 批量,向后对齐必须走完整个连续 tool run。
func TestRegression_AlignPairBoundary_BatchTools(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "u0"},
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a", ToolCalls: []llm.ToolCall{{ID: "c1"}, {ID: "c2"}}},
		{Role: llm.RoleTool, Content: "t1", ToolCallID: "c1"},
		{Role: llm.RoleTool, Content: "t2", ToolCallID: "c2"},
		{Role: llm.RoleUser, Content: "u2"},
	}
	// 边界在 T1 与 T2 之间(批量中间)→ 向后扩展到 T2(整个 run)
	_, end := alignPairBoundary(msgs, 2, 4)
	if end != 5 {
		t.Errorf("batch backward align: end = %d, want 5 (whole tool run)", end)
	}
	// 边界在 A 与 T1 之间 → 向后扩展到整个 run(T1+T2)
	_, end = alignPairBoundary(msgs, 2, 3)
	if end != 5 {
		t.Errorf("batch forward boundary: end = %d, want 5 (whole tool run)", end)
	}
}

// TestRegression_AlignPairBoundary_StartInRunMiddle 回归防护(复查发现):
// start 指向 tool run 中间(A→T1→T2,start=T2)时,向前扩展必须回退到
// run 开头的 assistant——否则 T2 被删而 A(c1,c2)/T1 保留 → 孤儿 tool_call → API 400。
func TestRegression_AlignPairBoundary_StartInRunMiddle(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "u0"},
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a", ToolCalls: []llm.ToolCall{{ID: "c1"}, {ID: "c2"}}},
		{Role: llm.RoleTool, Content: "t1", ToolCallID: "c1"},
		{Role: llm.RoleTool, Content: "t2", ToolCallID: "c2"},
		{Role: llm.RoleUser, Content: "u2"},
	}
	// start=4(T2, run 中间),end=6 → 向前回退到 assistant(index 2)
	start, end := alignPairBoundary(msgs, 4, 6)
	if start != 2 {
		t.Errorf("start should extend back to assistant (index 2), got %d (run middle cut → orphan tool_call)", start)
	}
	if end != 6 {
		t.Errorf("end = %d, want 6", end)
	}
}

// TestRegression_CursorClampAfterShrink 回归防护(三审 Medium-3):
// 消息数组被缩短(ValidateMessages)后游标越界 → 钳制而非永久失效。
func TestRegression_CursorClampAfterShrink(t *testing.T) {
	// 构造 watermark 游标越界(模拟 Restore 后数组被缩短)
	wm := WatermarkState{
		Tier1Cursor: 100,
		Tier2Cursor: 100,
		Tier3Cursor: 100,
		ContextLimit: DefaultContextLimit,
	}
	decisions := compactionDecisionSet{}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "s"},
		{Role: llm.RoleUser, Content: "u"},
		{Role: llm.RoleTool, Content: strings.Repeat("x", 1000), Name: "ls", ToolCallID: "tc1"},
	}

	// applyTier1 应钳制游标到 len(messages) 而非提前返回不推进
	snipped, _ := applyTier1(msgs, &decisions, &wm.Tier1Cursor, len(msgs), 1)
	if snipped != 0 {
		t.Errorf("snipped = %d, want 0", snipped)
	}
	if wm.Tier1Cursor != len(msgs) {
		t.Errorf("tier1 cursor not clamped: %d, want %d", wm.Tier1Cursor, len(msgs))
	}
	// applyTier2 同样钳制
	pruned, _ := applyTier2(msgs, &decisions, &wm.Tier2Cursor, wm.Tier1Cursor, 1)
	if pruned != 0 {
		t.Errorf("pruned = %d, want 0", pruned)
	}
	if wm.Tier2Cursor != len(msgs) {
		t.Errorf("tier2 cursor not clamped: %d, want %d", wm.Tier2Cursor, len(msgs))
	}
}

// TestRegression_HardLimitTier2Usage 回归防护(复查发现):
// tier3_failures 硬限时 usage 可能在 80-95%(tier=2)——硬限分支此前要求
// tier>=3 才重试 tier3,导致 tier=2 场景永不重试 → 死锁仍在。
// 修复:硬限分支无条件尝试 tier3。
func TestRegression_HardLimitTier2Usage(t *testing.T) {
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
		msgs = appendToolRun(msgs, 80)
		return msgs
	}

	// 2 次 tier3 失败(failures=2)在 96% 时产生
	usage96 := int(float64(DefaultContextLimit) * 0.96)
	messages1 := buildMessages()
	c.AdvanceTurn()
	_ = c.Compact(context.Background(), &messages1, usage96)
	messages2 := buildMessages()
	c.AdvanceTurn()
	_ = c.Compact(context.Background(), &messages2, usage96)

	// 现在 usage 降到 85%(tier=2)但 failures=2 → tier3_failures 硬限
	// 修复前:硬限分支 tier>=3 不满足 → 不重试 tier3 → 死锁
	// 修复后:无条件重试 tier3 → summarizer 恢复 → 计数清零 → 恢复
	ms.err = nil
	messages3 := buildMessages()
	c.AdvanceTurn()
	tick3 := c.Compact(context.Background(), &messages3, int(float64(DefaultContextLimit)*0.85))
	if tick3.HardLimitReached {
		t.Fatal("tier=2 + tier3_failures hard limit should recover via Tier 3 retry (deadlock regression)")
	}
	if !tick3.Tier3SummaryDone {
		t.Fatal("hard limit path should retry Tier 3 even at tier=2 (usage 80-95%)")
	}
	w := c.Watermark()
	if w.Tier3ConsecutiveFailures != 0 {
		t.Fatalf("failures should reset, got %d", w.Tier3ConsecutiveFailures)
	}
}

// TestRegression_PostCompactionValidation 回归防护(红线防线):
// 压缩后必须执行配对完整性验证(llm.ValidateMessages),修复孤儿消息——
// 即使 alignPairBoundary 有漏,防线也能兜住,孤儿 tool_calls/tool 不会发往 API。
func TestRegression_PostCompactionValidation(t *testing.T) {
	c := NewCompactor(DefaultCompactionConfig(), nil)

	// 构造:一条带 2 个 tool_calls 的 assistant,但只有 1 条 tool 结果
	// (人为破坏配对——模拟对齐逻辑遗漏的场景)
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "start"},
		{Role: llm.RoleAssistant, Content: "a",
			ReasoningContent: strings.Repeat("thinking ", 50), // 触发 tier2 prune 实际修改
			ToolCalls:        []llm.ToolCall{{ID: "c1", Name: "ls"}, {ID: "c2", Name: "cat"}}},
		{Role: llm.RoleTool, Content: strings.Repeat("x", 500), Name: "ls", ToolCallID: "c1"},
		// c2 无 tool 结果(孤儿 tool_call)
		{Role: llm.RoleUser, Content: "more"},
	}
	// 填充保护区(配对完整)
	messages = appendToolRun(messages, 80)

	// 85% → Tier 2(触发压缩)
	tick := c.Compact(context.Background(), &messages, int(float64(DefaultContextLimit)*0.85))
	if tick.Tier < 2 {
		t.Fatalf("expected Tier >= 2, got %d", tick.Tier)
	}

	// 红线防线:压缩后 ValidateMessages 应修复孤儿 tool_call(c2 被剔除)
	// 验证:assistant 的 tool_calls 只剩 c1(有配对),无孤儿
	for _, m := range messages {
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				if tc.ID == "c2" {
					t.Fatal("orphan tool_call c2 should be repaired by post-compaction validation (redline)")
				}
			}
		}
	}
	// 再跑一次 ValidateMessages 确认无残留修复(稳定态)
	_, report := llm.ValidateMessages(messages)
	if len(report) != 0 {
		t.Fatalf("expected stable state after redline repair, got %d repairs", len(report))
	}
}

// TestRegression_ValidationOnlyOnActualModification 回归防护(优化):
// 红线防线只在"实际发生修改"时执行——tier=1 但无消息可处理(removed=0)
// 的轮次跳过校验,避免每轮 O(n) ValidateMessages。
func TestRegression_ValidationOnlyOnActualModification(t *testing.T) {
	c := NewCompactor(DefaultCompactionConfig(), nil)

	// 构造:少量消息,全部在保护区内 → tier=1 触发但无可 snip 内容
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "start"},
		{Role: llm.RoleTool, Content: "short output", Name: "ls", ToolCallID: "tc1"},
	}
	// 补配对 assistant(红线防线要求测试数据配对完整)
	messages = append([]llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "start"},
		{Role: llm.RoleAssistant, Content: "a", ToolCalls: []llm.ToolCall{{ID: "tc1", Name: "ls"}}},
	}, messages[2:]...)

	// 60% 触发 tier1,但消息短且全在保护区 → snipped=0
	tick := c.Compact(context.Background(), &messages, int(float64(DefaultContextLimit)*0.61))
	if tick.Tier < 1 {
		t.Fatalf("expected Tier >= 1, got %d", tick.Tier)
	}
	// 无实际修改 → 不校验(RepairedMessages 保持 0,且无 WARN)
	if tick.MessagesSnipped != 0 {
		t.Fatalf("expected no snip (all in protection zone), got %d", tick.MessagesSnipped)
	}
}

// ---------------------------------------------------------------------------
// countLeadingBackticks
// ---------------------------------------------------------------------------
