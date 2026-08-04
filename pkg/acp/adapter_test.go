package acp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// adapter_test — consumeEvents 行为测试
// ---------------------------------------------------------------------------

// captureNotifs 返回一个 sendFn 用于捕获发送的通知。
func captureNotifs(t *testing.T) (func(msg any) error, func() []json.RawMessage) {
	t.Helper()
	var mu sync.Mutex
	var captured []json.RawMessage
	sendFn := func(msg any) error {
		mu.Lock()
		defer mu.Unlock()
		data, _ := json.Marshal(msg)
		captured = append(captured, data)
		return nil
	}
	getter := func() []json.RawMessage {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]json.RawMessage, len(captured))
		copy(cp, captured)
		return cp
	}
	return sendFn, getter
}

// parseSessionUpdate extracts the update content from a session/update notification JSON.
// Returns the update JSON as a raw message.
func parseSessionUpdate(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var notif JSONRPCNotification
	if err := json.Unmarshal(raw, &notif); err != nil {
		t.Fatalf("unmarshal notification: %v", err)
	}
	if notif.Method != MethodSessionUpdate {
		t.Fatalf("expected session/update, got %q", notif.Method)
	}
	var params SessionUpdateParams
	if err := json.Unmarshal(notif.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	return params.Update
}

func TestConsumeEventsStreamDelta(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 10)
	ctx := context.Background()

	// Send a StreamDelta with content
	ch <- agentloop.StreamDelta{
		Turn:         1,
		ContentDelta: "Hello, ",
	}
	ch <- agentloop.StreamDelta{
		Turn:         1,
		ContentDelta: "world!",
	}
	// Send a StreamDelta with reasoning
	ch <- agentloop.StreamDelta{
		Turn:           1,
		ReasoningDelta: "thinking...",
	}
	// Send LoopDone to terminate
	ch <- agentloop.LoopDone{
		Reason: agentloop.ReasonCompleted,
	}
	close(ch)

	loopDone, ok := ad.consumeEvents(ctx, ch)
	if !ok {
		t.Fatal("expected ok=true from consumeEvents")
	}
	if loopDone.Reason != agentloop.ReasonCompleted {
		t.Errorf("reason = %q, want %q", loopDone.Reason, agentloop.ReasonCompleted)
	}

	notifs := getNotifs()
	if len(notifs) != 3 {
		t.Fatalf("expected 3 notifications, got %d", len(notifs))
	}

	// Verify first notification: text "Hello, "
	update1 := parseSessionUpdate(t, notifs[0])
	var chunk1 ContentChunk
	if err := json.Unmarshal(update1, &chunk1); err != nil {
		t.Fatalf("unmarshal chunk1: %v", err)
	}
	if chunk1.Content.Type != "text" || chunk1.Content.Text != "Hello, " {
		t.Errorf("chunk1: type=%q text=%q", chunk1.Content.Type, chunk1.Content.Text)
	}

	// Verify second notification: text "world!"
	update2 := parseSessionUpdate(t, notifs[1])
	var chunk2 ContentChunk
	_ = json.Unmarshal(update2, &chunk2)
	if chunk2.Content.Text != "world!" {
		t.Errorf("chunk2 text = %q, want %q", chunk2.Content.Text, "world!")
	}

	// Verify third notification: thought
	update3 := parseSessionUpdate(t, notifs[2])
	var chunk3 ContentChunk
	_ = json.Unmarshal(update3, &chunk3)
	if chunk3.SessionUpdate != "agent_thought_chunk" {
		t.Errorf("chunk3 sessionUpdate = %q, want agent_thought_chunk", chunk3.SessionUpdate)
	}
	if chunk3.Content.Text != "thinking..." {
		t.Errorf("chunk3 text = %q, want %q", chunk3.Content.Text, "thinking...")
	}
	if chunk3.MessageID == chunk1.MessageID {
		t.Error("thought chunk must use an independent messageId (avoid mixing into message body)")
	}
}

func TestConsumeEventsLoopDone(t *testing.T) {
	sendFn, _ := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 1)
	ctx := context.Background()

	ch <- agentloop.LoopDone{
		Turn:   3,
		Reason: agentloop.ReasonCompleted,
	}
	close(ch)

	loopDone, ok := ad.consumeEvents(ctx, ch)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if loopDone.Reason != agentloop.ReasonCompleted {
		t.Errorf("reason = %q, want %q", loopDone.Reason, agentloop.ReasonCompleted)
	}
	if loopDone.Turn != 3 {
		t.Errorf("turn = %d, want 3", loopDone.Turn)
	}
}

func TestConsumeEventsChannelClose(t *testing.T) {
	sendFn, _ := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent)
	ctx := context.Background()
	close(ch) // channel closed without LoopDone

	loopDone, ok := ad.consumeEvents(ctx, ch)
	if ok {
		t.Error("expected ok=false when channel closes without LoopDone")
	}
	if loopDone.Reason != agentloop.ReasonCompleted {
		t.Errorf("reason = %q, want %q", loopDone.Reason, agentloop.ReasonCompleted)
	}
}

func TestConsumeEventsContextCancellation(t *testing.T) {
	sendFn, _ := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent)
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately; channel closes without LoopDone (loop contract violated)
	cancel()
	close(ch)

	// consumeEvents must not return a synthetic LoopDone as real: it drains
	// until channel close, then reports ok=false.
	loopDone, ok := ad.consumeEvents(ctx, ch)
	if ok {
		t.Error("expected ok=false on cancellation")
	}
	if loopDone.Reason != agentloop.ReasonCompleted {
		t.Errorf("reason = %q, want %q", loopDone.Reason, agentloop.ReasonCompleted)
	}
}

func TestConsumeEventsContextCancellationDrain(t *testing.T) {
	sendFn, _ := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	// Channel with buffered events that haven't been consumed yet
	ch := make(chan agentloop.TurnEvent, 3)
	ch <- agentloop.StreamDelta{Turn: 1, ContentDelta: "hello"}
	ch <- agentloop.StreamDelta{Turn: 1, ContentDelta: "world"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	close(ch)

	// consumeEvents drains buffered events until channel close (no LoopDone)
	loopDone, ok := ad.consumeEvents(ctx, ch)
	if ok {
		t.Error("expected ok=false on cancellation")
	}
	if loopDone.Reason != agentloop.ReasonCompleted {
		t.Errorf("reason = %q, want %q", loopDone.Reason, agentloop.ReasonCompleted)
	}
}

func TestRegression_CancelPreservesLoopDoneMessages(t *testing.T) {
	// REGRESSION: consumeEvents 曾在 ctx.Done() 分支立即返回合成 LoopDone
	// (Messages=nil),把 Loop 取消后发送的携带真实 Messages 的 LoopDone 排空
	// 丢弃;调用方对合成值无条件调用 CompleteRun(nil),用空历史整体替换会话
	// 上下文(数据损坏,每次 session/cancel 约 50% 概率触发)。
	// 根因修复:ctx.Done() 分支转入纯消费循环,等待真实 LoopDone。
	sendFn, _ := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 2)
	ctx, cancel := context.WithCancel(context.Background())

	// 模拟 Loop 契约:取消后仍先发送带真实 Messages 的 LoopDone 再关闭通道
	realMessages := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}
	go func() {
		cancel()
		ch <- agentloop.LoopDone{Reason: agentloop.ReasonAborted, Messages: realMessages}
		close(ch)
	}()

	loopDone, ok := ad.consumeEvents(ctx, ch)
	if !ok {
		t.Fatal("real LoopDone must not be lost on cancellation (ok=false)")
	}
	if len(loopDone.Messages) != len(realMessages) {
		t.Errorf("messages lost on cancel: got %d, want %d", len(loopDone.Messages), len(realMessages))
	}
	if loopDone.Reason != agentloop.ReasonAborted {
		t.Errorf("reason = %q, want %q", loopDone.Reason, agentloop.ReasonAborted)
	}
}

func TestConsumeEventsPlanMode(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 10)
	ctx := context.Background()

	ch <- agentloop.PlanModeEnter{
		Turn:     1,
		PlanFile: "/tmp/plan.md",
	}
	ch <- agentloop.PlanModeExit{
		Turn:     2,
		Plan:     "do the thing",
		Approved: true,
	}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	loopDone, _ := ad.consumeEvents(ctx, ch)
	_ = loopDone

	notifs := getNotifs()
	if len(notifs) != 2 {
		t.Fatalf("expected 2 notifications (plan enter + plan exit), got %d", len(notifs))
	}

	// Verify PlanModeEnter notification
	update1 := parseSessionUpdate(t, notifs[0])
	var plan1 PlanUpdate
	if err := json.Unmarshal(update1, &plan1); err != nil {
		t.Fatalf("unmarshal plan1: %v", err)
	}
	if plan1.SessionUpdate != "plan" || plan1.Entries[0].Content != "/tmp/plan.md" {
		t.Errorf("plan enter: type=%q plan=%q", plan1.SessionUpdate, plan1.Entries[0].Content)
	}

	// Verify PlanModeExit notification (approved)
	update2 := parseSessionUpdate(t, notifs[1])
	var plan2 PlanUpdate
	_ = json.Unmarshal(update2, &plan2)
	if plan2.Entries[0].Content != "do the thing" {
		t.Errorf("plan exit: plan=%q, want %q", plan2.Entries[0].Content, "do the thing")
	}
}

func TestConsumeEventsPlanModeRejected(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 2)
	ctx := context.Background()

	ch <- agentloop.PlanModeExit{
		Turn:     2,
		Plan:     "bad plan",
		Approved: false,
	}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	_, _ = ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
	update := parseSessionUpdate(t, notifs[0])
	var plan PlanUpdate
	_ = json.Unmarshal(update, &plan)
	if plan.Entries[0].Content != "bad plan" {
		t.Errorf("plan exit: plan=%q, want %q", plan.Entries[0].Content, "bad plan")
	}
}

func TestConsumeEventsMessageIDConsistency(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	if ad.messageID == "" {
		t.Error("messageID should not be empty")
	}

	ch := make(chan agentloop.TurnEvent, 3)
	ctx := context.Background()

	ch <- agentloop.StreamDelta{Turn: 1, ContentDelta: "a"}
	ch <- agentloop.StreamDelta{Turn: 1, ContentDelta: "b"}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	// All agent_message_chunk should share the same messageID
	for i, raw := range notifs {
		update := parseSessionUpdate(t, raw)
		var chunk ContentChunk
		if err := json.Unmarshal(update, &chunk); err != nil {
			t.Fatalf("notif[%d] unmarshal: %v", i, err)
		}
		if chunk.MessageID != ad.messageID {
			t.Errorf("notif[%d] messageId = %q, want %q", i, chunk.MessageID, ad.messageID)
		}
	}
}

func TestConsumeEventsIgnoresEmptyDeltas(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 3)
	ctx := context.Background()

	// Empty delta should not trigger notification
	ch <- agentloop.StreamDelta{Turn: 1}
	ch <- agentloop.StreamDelta{Turn: 1, ContentDelta: "visible"}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification (empty delta skipped), got %d", len(notifs))
	}
	update := parseSessionUpdate(t, notifs[0])
	var chunk ContentChunk
	_ = json.Unmarshal(update, &chunk)
	if chunk.Content.Text != "visible" {
		t.Errorf("text = %q, want %q", chunk.Content.Text, "visible")
	}
}

// ---------------------------------------------------------------------------
// ToolCallStart / ToolCallStream / ToolCallResult 测试
// ---------------------------------------------------------------------------

func TestConsumeEventsToolCallStart(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 3)
	ctx := context.Background()

	ch <- agentloop.ToolCallStart{
		Turn:         1,
		ToolCallID:   "tc-001",
		ToolCallName: "bash",
		Arguments:    `{"command":"go test ./..."}`,
	}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) < 1 {
		t.Fatalf("expected at least 1 notification, got %d", len(notifs))
	}

	update1 := parseSessionUpdate(t, notifs[0])
	var tc ToolCallUpdate
	if err := json.Unmarshal(update1, &tc); err != nil {
		t.Fatalf("unmarshal tool_call: %v", err)
	}
	if tc.SessionUpdate != "tool_call" || tc.ToolCallID != "tc-001" || tc.Kind != "execute" || tc.Status != "pending" || tc.Title != "bash" {
		t.Errorf("tool_call: update=%s id=%s kind=%s status=%s title=%s", tc.SessionUpdate, tc.ToolCallID, tc.Kind, tc.Status, tc.Title)
	}
}

func TestConsumeEventsToolCallResultSuccess(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 2)
	ctx := context.Background()

	ch <- agentloop.ToolCallResult{
		ToolCallID: "tc-001", ToolCallName: "read",
		Result: "file content here", DurationMs: 42,
	}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
	update := parseSessionUpdate(t, notifs[0])
	var tcu ToolCallUpdate
	_ = json.Unmarshal(update, &tcu)
	if tcu.Status != "completed" {
		t.Errorf("status=%s, want completed", tcu.Status)
	}
}

func TestConsumeEventsToolCallResultError(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 2)
	ctx := context.Background()

	ch <- agentloop.ToolCallResult{
		ToolCallID: "tc-002", ToolCallName: "bash",
		Error: "command failed", ErrorKind: "command_failed",
	}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) != 1 {
		t.Fatalf("expected 1, got %d", len(notifs))
	}
	update := parseSessionUpdate(t, notifs[0])
	var tcu ToolCallUpdate
	_ = json.Unmarshal(update, &tcu)
	if tcu.Status != "failed" {
		t.Errorf("status = %q, want failed", tcu.Status)
	}
}

func TestConsumeEventsToolCallResultDiff(t *testing.T) {
	// DiffHunks → v1 diff 内容块(oldText 由 context+delete 重建,newText 由 context+add 重建)
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 2)
	ctx := context.Background()

	ch <- agentloop.ToolCallResult{
		ToolCallID: "tc-003", ToolCallName: "edit",
		Result: "done",
		DiffHunks: []tool.DiffHunk{{
			FilePath: "/tmp/x.go",
			Lines: []tool.DiffLine{
				{Kind: tool.DiffCtx, Content: "keep"},
				{Kind: tool.DiffDel, Content: "old-line"},
				{Kind: tool.DiffAdd, Content: "new-line"},
			},
		}},
	}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
	update := parseSessionUpdate(t, notifs[0])
	var tcu ToolCallUpdate
	_ = json.Unmarshal(update, &tcu)

	var diff *ToolCallContentItem
	for i := range tcu.Content {
		if tcu.Content[i].Type == "diff" {
			diff = &tcu.Content[i]
			break
		}
	}
	if diff == nil {
		t.Fatal("expected diff content item")
	}
	if diff.Path != "/tmp/x.go" {
		t.Errorf("diff path = %q, want /tmp/x.go", diff.Path)
	}
	if diff.OldText != "keep\nold-line" {
		t.Errorf("oldText = %q, want %q", diff.OldText, "keep\nold-line")
	}
	if diff.NewText != "keep\nnew-line" {
		t.Errorf("newText = %q, want %q", diff.NewText, "keep\nnew-line")
	}
}

// ---------------------------------------------------------------------------
// TurnStats 测试
// ---------------------------------------------------------------------------

func TestConsumeEventsTurnStats(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 2)
	ctx := context.Background()

	ch <- agentloop.TurnStats{
		Turn: 3, Model: "deepseek-chat",
		PromptTokens: 1500, CompletionTokens: 200,
		CacheHitTokens: 1000, CacheMissTokens: 500,
		ReasoningTokens: 50, MessageCount: 12,
	}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) != 1 {
		t.Fatalf("expected 1, got %d", len(notifs))
	}
	update := parseSessionUpdate(t, notifs[0])
	var usage UsageUpdateContent
	_ = json.Unmarshal(update, &usage)
	if usage.SessionUpdate != "usage_update" || usage.Size != 12 {
		t.Errorf("usage: type=%s prompt=%d", usage.SessionUpdate, usage.Used)
	}
}

func TestConsumeEventsTurnStatsAccumulated(t *testing.T) {
	// usage_update 的 used 取当轮上下文 token(与 TUI HUD 同源);
	// adapter.Stats() 供 handler 提交 CompleteRun 用(累积)。
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 3)
	ctx := context.Background()

	ch <- agentloop.TurnStats{PromptTokens: 100, CompletionTokens: 20, MessageCount: 5}
	ch <- agentloop.TurnStats{PromptTokens: 50, CompletionTokens: 10, MessageCount: 6}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	// 最后一次 usage_update 的 used = 当轮 PromptTokens(50),非累积
	notifs := getNotifs()
	if len(notifs) != 2 {
		t.Fatalf("expected 2 usage notifications, got %d", len(notifs))
	}
	update := parseSessionUpdate(t, notifs[1])
	var usage UsageUpdateContent
	_ = json.Unmarshal(update, &usage)
	if usage.Used != 50 {
		t.Errorf("used = %d, want 50 (current context tokens)", usage.Used)
	}

	// Stats() 累积值
	stats := ad.Stats()
	if stats.PromptTokens != 150 || stats.CompletionTokens != 30 {
		t.Errorf("Stats() = %+v, want Prompt=150 Compl=30", stats)
	}
}

func TestConsumeEventsTurnStatsContextLimit(t *testing.T) {
	// REGRESSION: usage_update 的 size 曾错误传消息条数;官方语义为
	// "Total context window size in tokens"——配置窗口容量后必须用之。
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapterWithContextLimit("test-session", sendFn, 1_000_000)

	ch := make(chan agentloop.TurnEvent, 2)
	ctx := context.Background()

	ch <- agentloop.TurnStats{PromptTokens: 100, CompletionTokens: 20, MessageCount: 5}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) != 1 {
		t.Fatalf("expected 1 usage notification, got %d", len(notifs))
	}
	update := parseSessionUpdate(t, notifs[0])
	var usage UsageUpdateContent
	_ = json.Unmarshal(update, &usage)
	if usage.Size != 1_000_000 {
		t.Errorf("size = %d, want 1000000 (context window capacity)", usage.Size)
	}
	if usage.Used != 100 {
		t.Errorf("used = %d, want 100 (current context tokens)", usage.Used)
	}
}

func TestConsumeEventsTurnStatsCompactionAware(t *testing.T) {
	// used 压缩感知(与 TUI ctx bar 同逻辑):
	// 有压缩 → PromptTokens - TokensSaved;Tier 3 摘要后 → 0。
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 4)
	ctx := context.Background()

	ch <- agentloop.TurnStats{PromptTokens: 100, CompletionTokens: 20}
	ch <- agentloop.TurnStats{
		PromptTokens: 200, CompletionTokens: 30,
		Compaction: agentloop.CompactionInfo{Tier: 1, TokensSaved: 60},
	}
	ch <- agentloop.TurnStats{
		PromptTokens: 150, CompletionTokens: 10,
		Compaction: agentloop.CompactionInfo{Tier: 3, SummaryDone: true},
	}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) != 3 {
		t.Fatalf("expected 3 usage notifications, got %d", len(notifs))
	}
	expect := []uint64{100, 140, 0} // 无压缩 / 压缩后 200-60 / Tier3 摘要后清零
	for i, raw := range notifs {
		update := parseSessionUpdate(t, raw)
		var usage UsageUpdateContent
		_ = json.Unmarshal(update, &usage)
		if usage.Used != expect[i] {
			t.Errorf("notif[%d] used = %d, want %d", i, usage.Used, expect[i])
		}
	}
}

// ---------------------------------------------------------------------------
// AskUserQuestion 测试
// ---------------------------------------------------------------------------

func TestConsumeEventsAskUserQuestion(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 2)
	ctx := context.Background()

	ch <- agentloop.AskUserQuestionEvent{
		Turn: 2, ToolCallID: "tc-q",
		Questions: []agentloop.QuestionPrompt{{
			Question: "Which?", Header: "Pick",
			Options: []agentloop.QuestionOptionPrompt{
				{Label: "A", Description: "First"},
			},
		}},
	}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) != 1 {
		t.Fatalf("expected 1, got %d", len(notifs))
	}
	update := parseSessionUpdate(t, notifs[0])
	var q AskUserQuestionContent
	_ = json.Unmarshal(update, &q)
	if q.SessionUpdate != "_waveloom/ask_user_question" || q.ToolCallID != "tc-q" {
		t.Errorf("ask: type=%s id=%s", q.SessionUpdate, q.ToolCallID)
	}
}

// ---------------------------------------------------------------------------
// TodoUpdate 测试
// ---------------------------------------------------------------------------

func TestConsumeEventsTodoUpdate(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 2)
	ctx := context.Background()

	ch <- agentloop.TodoUpdateEvent{}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) != 1 {
		t.Fatalf("expected 1, got %d", len(notifs))
	}
	update := parseSessionUpdate(t, notifs[0])
	var td TodoUpdateContent
	_ = json.Unmarshal(update, &td)
	if td.SessionUpdate != "_waveloom/todo_update" {
		t.Errorf("type = %q", td.SessionUpdate)
	}
}

// ---------------------------------------------------------------------------
// BalanceUpdate 测试
// ---------------------------------------------------------------------------

func TestConsumeEventsBalanceUpdate(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 2)
	ctx := context.Background()

	ch <- agentloop.BalanceUpdate{
		Turn: 0,
		Balance: &llm.BalanceInfo{
			IsAvailable: true,
			BalanceInfos: []llm.CurrencyBalance{
				{Currency: "CNY", TotalBalance: "100.50", GrantedBalance: "50.00", ToppedUpBalance: "50.50"},
			},
		},
	}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) != 1 {
		t.Fatalf("expected 1, got %d", len(notifs))
	}
	update := parseSessionUpdate(t, notifs[0])
	var bal BalanceUpdateContent
	_ = json.Unmarshal(update, &bal)
	if !bal.IsAvailable || len(bal.Balances) != 1 {
		t.Errorf("balance: available=%v count=%d", bal.IsAvailable, len(bal.Balances))
	}
}

func TestConsumeEventsBalanceUpdateNil(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 2)
	ctx := context.Background()

	ch <- agentloop.BalanceUpdate{Turn: 0, Balance: nil}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) != 1 {
		t.Fatalf("expected 1, got %d", len(notifs))
	}
	update := parseSessionUpdate(t, notifs[0])
	var bal BalanceUpdateContent
	_ = json.Unmarshal(update, &bal)
	if bal.IsAvailable {
		t.Error("IsAvailable should be false when Balance is nil")
	}
}

// ---------------------------------------------------------------------------
// ToolKind 映射测试
// ---------------------------------------------------------------------------

func TestToolKind(t *testing.T) {
	tests := []struct {
		tool string
		want string
	}{
		{"read", "read"}, {"edit", "edit"}, {"write", "edit"},
		{"bash", "execute"}, {"web_search", "search"}, {"web_fetch", "fetch"},
		{"agent", "think"}, {"ask_user_question", "other"}, {"skill", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			if got := ToolKind(tt.tool); got != tt.want {
				t.Errorf("ToolKind(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestConsumeEventsToolCallStream(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 3)
	ctx := context.Background()

	ch <- agentloop.ToolCallStream{
		ToolCallID: "tc-001", ToolCallName: "bash",
		Chunk: "Running...\n",
	}
	ch <- agentloop.ToolCallStream{
		ToolCallID: "tc-001", ToolCallName: "bash",
		Chunk: "OK\n",
	}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) != 2 {
		t.Fatalf("expected 2 stream notifications, got %d", len(notifs))
	}
	for i, raw := range notifs {
		update := parseSessionUpdate(t, raw)
		var tcu ToolCallUpdate
		_ = json.Unmarshal(update, &tcu)
		if tcu.SessionUpdate != "tool_call_update" || tcu.ToolCallID != "tc-001" || tcu.Status != "in_progress" {
			t.Errorf("notif[%d]: type=%s id=%s status=%s", i, tcu.SessionUpdate, tcu.ToolCallID, tcu.Status)
		}
		if len(tcu.Content) != 1 || tcu.Content[0].Type != "content" {
			t.Errorf("notif[%d]: content=%+v", i, tcu.Content)
		}
	}
}

func TestConsumeEventsTodoUpdateWithItems(t *testing.T) {
	sendFn, getNotifs := captureNotifs(t)
	ad := newAdapter("test-session", sendFn)

	ch := make(chan agentloop.TurnEvent, 2)
	ctx := context.Background()

	// agentloop.TodoUpdateEvent 的 Items 是 []todo.TodoItem 类型，
	// 在测试包中直接构造（跨包类型通过 agentloop 的别名访问）
	ch <- agentloop.TodoUpdateEvent{
		Items: nil, // 类型匹配即可，handleTodoUpdate 只读取字段值
	}
	ch <- agentloop.LoopDone{Reason: agentloop.ReasonCompleted}
	close(ch)

	ad.consumeEvents(ctx, ch)

	notifs := getNotifs()
	if len(notifs) != 1 {
		t.Fatalf("expected 1, got %d", len(notifs))
	}
	update := parseSessionUpdate(t, notifs[0])
	var td TodoUpdateContent
	_ = json.Unmarshal(update, &td)
	if td.SessionUpdate != "_waveloom/todo_update" {
		t.Errorf("type = %q", td.SessionUpdate)
	}
}
