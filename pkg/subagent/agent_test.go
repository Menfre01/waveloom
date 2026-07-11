package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// mockClient — 完整的 mock LLM Client
// ---------------------------------------------------------------------------

// mockClient implements llm.Client with minimal methods.
// Embed in specific test types and override SendMessage/SendMessageStream.
type mockClient struct{}

func (m *mockClient) GetBalance(ctx context.Context) (*llm.BalanceInfo, error) { return nil, nil }
func (m *mockClient) SupportsBalance() bool                                    { return false }
func (m *mockClient) ListModels(ctx context.Context) ([]llm.ModelInfo, error)  { return nil, nil }

// stubLLM returns "ok" for any request.
type stubLLM struct {
	mockClient
}

func (s *stubLLM) SendMessage(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (*llm.Response, error) {
	return &llm.Response{Content: "ok"}, nil
}

func (s *stubLLM) SendMessageStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (<-chan llm.StreamingEvent, error) {
	ch := make(chan llm.StreamingEvent, 1)
	go func() {
		defer close(ch)
		ch <- llm.StreamingEvent{Delta: "ok", Done: true}
	}()
	return ch, nil
}

// errorLLM always returns an error.
type errorLLM struct {
	mockClient
}

func (e *errorLLM) SendMessage(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (*llm.Response, error) {
	return nil, errors.New("LLM unavailable")
}

func (e *errorLLM) SendMessageStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (<-chan llm.StreamingEvent, error) {
	return nil, errors.New("LLM unavailable")
}

// captureLLM records the messages received by SendMessageStream for inspection.
type captureLLM struct {
	mockClient
	CapturedMessages []llm.Message
}

func (c *captureLLM) SendMessage(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (*llm.Response, error) {
	c.CapturedMessages = messages
	return &llm.Response{Content: "ok"}, nil
}

func (c *captureLLM) SendMessageStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (<-chan llm.StreamingEvent, error) {
	c.CapturedMessages = messages
	ch := make(chan llm.StreamingEvent, 1)
	go func() {
		defer close(ch)
		ch <- llm.StreamingEvent{Delta: "ok", Done: true}
	}()
	return ch, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestAgentTool_Name(t *testing.T) {
	a := &AgentTool{}
	if a.Name() != "agent" {
		t.Errorf("Name() = %q, want %q", a.Name(), "agent")
	}
}

func TestAgentTool_ConcurrentSafe(t *testing.T) {
	a := &AgentTool{}
	if !a.ConcurrentSafe() {
		t.Error("AgentTool should be concurrent-safe")
	}
}

func TestAgentTool_Schema(t *testing.T) {
	a := &AgentTool{}
	raw := a.Schema()
	// 验证是合法 JSON
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Errorf("Schema is not valid JSON: %v", err)
	}
	if _, ok := m["properties"]; !ok {
		t.Error("Schema missing properties")
	}
}

func TestAgentTool_Description(t *testing.T) {
	a := &AgentTool{}
	desc := a.Description()
	for _, keyword := range []string{"subagent", "fork", "evaluate", "Explore", "verification"} {
		if !strings.Contains(desc, keyword) {
			t.Errorf("Description missing keyword %q", keyword)
		}
	}
}

// ---------------------------------------------------------------------------
// Cold agent tests
// ---------------------------------------------------------------------------

func TestAgentTool_ExecuteCold_Evaluate(t *testing.T) {
	ctx := context.Background()

	a := &AgentTool{LLMClient: &stubLLM{}}
	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "evaluate",
		Description:  "test",
		Prompt:       "review something",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "evaluate") {
		t.Errorf("result should mention agent type: %s", result.Content)
	}
	if !strings.Contains(result.Content, "ok") {
		t.Errorf("result should contain LLM output: %s", result.Content)
	}
}

func TestAgentTool_ExecuteCold_Explore(t *testing.T) {
	ctx := context.Background()

	a := &AgentTool{LLMClient: &stubLLM{}}
	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "Explore",
		Description:  "test",
		Prompt:       "find something",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "Explore") {
		t.Errorf("result should mention agent type: %s", result.Content)
	}
}

func TestAgentTool_ExecuteCold_UnknownTypeDefaultsToEvaluate(t *testing.T) {
	ctx := context.Background()

	a := &AgentTool{LLMClient: &stubLLM{}}
	// Unknown type falls back to evaluate system prompt & tools, but the type
	// label in the result preserves the original name (for TUI display).
	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "nonexistent",
		Description:  "test",
		Prompt:       "test",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	// Verify it completed successfully with the fallback
	if !strings.Contains(result.Content, "completed") && !strings.Contains(result.Content, "ok") {
		t.Errorf("unknown type should succeed with fallback: %s", result.Content)
	}
}

func TestAgentTool_ExecuteCold_SubagentStartEvent(t *testing.T) {
	ctx := context.Background()
	ctx = agentloop.WithEventCallback(ctx, func(ev agentloop.TurnEvent) {
		// 验证事件类型
		if _, ok := ev.(SubagentStart); !ok {
			if subEnd, ok := ev.(SubagentEnd); ok {
				if subEnd.Error != "" {
					t.Errorf("unexpected subagent error: %s", subEnd.Error)
				}
			}
		}
	})

	a := &AgentTool{LLMClient: &stubLLM{}}
	_, err := a.Execute(ctx, AgentParams{
		SubagentType: "Explore",
		Description:  "event-test",
		Prompt:       "test",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
}

func TestAgentTool_ExecuteCold_SubagentEndError(t *testing.T) {
	errLLM := &errorLLM{}
	ctx := context.Background()
	var gotError bool
	ctx = agentloop.WithEventCallback(ctx, func(ev agentloop.TurnEvent) {
		if subEnd, ok := ev.(SubagentEnd); ok && subEnd.Error != "" {
			gotError = true
		}
	})

	a := &AgentTool{LLMClient: errLLM}
	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "evaluate",
		Description:  "error-test",
		Prompt:       "test",
	})
	if err != nil {
		t.Fatalf("Execute() should not return error (returns it in result): %v", err)
	}
	if !strings.Contains(result.Content, "failed") {
		t.Errorf("result should indicate failure: %s", result.Content)
	}
	if !gotError {
		t.Error("expected SubagentEnd with Error to be sent")
	}
}

// ---------------------------------------------------------------------------
// Cold registry tests
// ---------------------------------------------------------------------------

func TestBuildColdRegistry_Evaluate_IsReadOnly(t *testing.T) {
	r := buildColdRegistry(evaluateDisallowed)
	names := toolNames(r)
	for _, name := range []string{"read_file", "web_fetch", "bash_subagent"} {
		if !contains(names, name) {
			t.Errorf("evaluate registry missing %q", name)
		}
	}
	for _, name := range []string{"write_file", "edit_file"} {
		if contains(names, name) {
			t.Errorf("evaluate registry should NOT have %q", name)
		}
	}
	// bash (main agent) should NOT be available
	if contains(names, "bash") {
		t.Error("evaluate registry should NOT have bash")
	}
}

func TestBuildColdRegistry_Explore_IsReadOnly(t *testing.T) {
	r := buildColdRegistry(exploreDisallowed)
	names := toolNames(r)
	for _, name := range []string{"read_file", "web_fetch", "bash_subagent"} {
		if !contains(names, name) {
			t.Errorf("Explore registry missing %q", name)
		}
	}
	for _, name := range []string{"write_file", "edit_file"} {
		if contains(names, name) {
			t.Errorf("Explore registry should NOT have %q", name)
		}
	}
}

func TestBuildColdRegistry_NoAgentTool(t *testing.T) {
	r := buildColdRegistry(nil)
	names := toolNames(r)
	for _, name := range []string{"agent", "enter_plan_mode", "exit_plan_mode", "ask_user_question", "kill_background_task"} {
		if contains(names, name) {
			t.Errorf("registry should NOT have %q", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Event forwarding tests
// ---------------------------------------------------------------------------

func TestForwardEvents_TextAggregation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan agentloop.TurnEvent, 10)
	go func() {
		ch <- agentloop.StreamDelta{ContentDelta: "hello "}
		ch <- agentloop.StreamDelta{ContentDelta: "world"}
		ch <- agentloop.LoopDone{Turn: 1}
		close(ch)
	}()

	aggregated, turns, promptTok, complTok, _, _, _, err := forwardEvents(ctx, ch, nil, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}
	if aggregated != "hello world" {
		t.Errorf("aggregated = %q, want %q", aggregated, "hello world")
	}
	if turns != 1 {
		t.Errorf("turns = %d, want 1", turns)
	}
	if promptTok != 0 || complTok != 0 {
		t.Errorf("promptTokens = %d, complTokens = %d, want 0, 0", promptTok, complTok)
	}
}

// REGRESSION: ReasoningDelta must be forwarded as SubagentThought event (Phase 2).
// Without this, the TUI thought-dimmed rendering silently breaks.
func TestForwardEvents_ReasoningDelta_SubagentThought(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var events []SubagentEvent
	cb := func(ev agentloop.TurnEvent) {
		if se, ok := ev.(SubagentEvent); ok {
			events = append(events, se)
		}
	}

	ch := make(chan agentloop.TurnEvent, 10)
	go func() {
		ch <- agentloop.StreamDelta{ReasoningDelta: "let me think about this..."}
		ch <- agentloop.StreamDelta{ContentDelta: "the answer is 42"}
		ch <- agentloop.StreamDelta{ReasoningDelta: "actually, double-checking..."}
		ch <- agentloop.StreamDelta{ContentDelta: " yes, 42"}
		ch <- agentloop.LoopDone{Turn: 1}
		close(ch)
	}()

	aggregated, _, _, _, _, _, _, err := forwardEvents(ctx, ch, cb, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}
	// Aggregated text should only contain content, not reasoning.
	if aggregated != "the answer is 42 yes, 42" {
		t.Errorf("aggregated = %q, want only content deltas (no reasoning)", aggregated)
	}

	// Verify SubagentThought events were emitted.
	var thoughtCount, textCount int
	for _, ev := range events {
		switch ev.Kind {
		case SubagentThought:
			thoughtCount++
		case SubagentText:
			textCount++
		}
	}
	if thoughtCount != 2 {
		t.Errorf("thought events = %d, want 2", thoughtCount)
	}
	if textCount != 2 {
		t.Errorf("text events = %d, want 2", textCount)
	}
}

func TestForwardEvents_ToolEventsProduceCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var events []SubagentEvent
	cb := func(ev agentloop.TurnEvent) {
		if se, ok := ev.(SubagentEvent); ok {
			events = append(events, se)
		}
	}

	ch := make(chan agentloop.TurnEvent, 10)
	go func() {
		ch <- agentloop.StreamDelta{ContentDelta: "thinking..."}
		ch <- agentloop.ToolCallStart{ToolCallName: "read_file", Arguments: `{"file_path":"x.go"}`}
		ch <- agentloop.ToolCallResult{ToolCallName: "read_file", Result: "file content", DurationMs: 42}
		ch <- agentloop.LoopDone{Turn: 1}
		close(ch)
	}()

	_, _, _, _, _, _, _, err := forwardEvents(ctx, ch, cb, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Kind != SubagentText || events[0].TextDelta != "thinking..." {
		t.Errorf("event[0] wrong: %+v", events[0])
	}
	if events[1].Kind != SubagentToolStart || events[1].ToolName != "read_file" {
		t.Errorf("event[1] wrong: %+v", events[1])
	}
	if events[2].Kind != SubagentToolResult || events[2].ToolDurMs != 42 {
		t.Errorf("event[2] wrong: %+v", events[2])
	}
}

func TestForwardEvents_WriteOperationsTracking(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan agentloop.TurnEvent, 10)
	go func() {
		ch <- agentloop.StreamDelta{ContentDelta: "done."}
		ch <- agentloop.ToolCallResult{
			ToolCallName: "write_file",
			Result:       "Wrote 42 bytes to /tmp/test.go",
		}
		ch <- agentloop.ToolCallResult{
			ToolCallName: "edit_file",
			Result:       "@@ -1,0 +1,2 @@\n+added line\n+another\n",
		}
		ch <- agentloop.LoopDone{Turn: 1}
		close(ch)
	}()

	aggregated, _, _, _, _, _, _, err := forwardEvents(ctx, ch, nil, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}

	if !strings.Contains(aggregated, "<subagent_write_operations>") {
		t.Error("aggregated output should contain write operations block")
	}
	if !strings.Contains(aggregated, "write_file") {
		t.Error("write operations should list write_file")
	}
	if !strings.Contains(aggregated, "edit_file") {
		t.Error("write operations should list edit_file")
	}
}

func TestForwardEvents_TurnStatsAccumulation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan agentloop.TurnEvent, 10)
	go func() {
		ch <- agentloop.TurnStats{PromptTokens: 100, CompletionTokens: 50, CacheHitTokens: 60, CacheMissTokens: 40}
		ch <- agentloop.TurnStats{PromptTokens: 200, CompletionTokens: 75, CacheHitTokens: 120, CacheMissTokens: 80}
		ch <- agentloop.LoopDone{Turn: 2}
		close(ch)
	}()

	_, turns, promptTok, complTok, cacheHitTok, cacheMissTok, _, err := forwardEvents(ctx, ch, nil, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}
	if turns != 2 {
		t.Errorf("turns = %d, want 2", turns)
	}
	if promptTok != 300 || complTok != 125 {
		t.Errorf("promptTokens = %d, complTokens = %d, want 300, 125", promptTok, complTok)
	}
	if cacheHitTok != 180 || cacheMissTok != 120 {
		t.Errorf("cacheHitTokens = %d, cacheMissTokens = %d, want 180, 120", cacheHitTok, cacheMissTok)
	}
}

func TestForwardEvents_LoopDoneError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	expectedErr := errors.New("subagent crashed")
	ch := make(chan agentloop.TurnEvent, 10)
	go func() {
		ch <- agentloop.LoopDone{Turn: 0, Err: expectedErr}
		close(ch)
	}()

	_, _, _, _, _, _, _, err := forwardEvents(ctx, ch, nil, "")
	if err == nil {
		t.Fatal("expected error from LoopDone")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("err = %v, want %v", err, expectedErr)
	}
}

func TestForwardEvents_EmptyStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan agentloop.TurnEvent, 1)
	go func() {
		ch <- agentloop.LoopDone{Turn: 0}
		close(ch)
	}()

	aggregated, _, _, _, _, _, _, err := forwardEvents(ctx, ch, nil, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}
	// 空流（无任何文本输出）应返回兜底文本，而非空字符串，
	// 防止 tool_result 内容为空导致父 agent 误解。
	if aggregated == "" {
		t.Errorf("aggregated is empty, want non-empty fallback")
	}
}

func TestForwardEvents_ToolCallStreamEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var events []SubagentEvent
	cb := func(ev agentloop.TurnEvent) {
		if se, ok := ev.(SubagentEvent); ok {
			events = append(events, se)
		}
	}

	ch := make(chan agentloop.TurnEvent, 10)
	go func() {
		ch <- agentloop.ToolCallStream{ToolCallName: "bash_subagent", Chunk: "line1\n"}
		ch <- agentloop.ToolCallStream{ToolCallName: "bash_subagent", Chunk: "line2\n"}
		ch <- agentloop.LoopDone{Turn: 1}
		close(ch)
	}()

	_, _, _, _, _, _, _, err := forwardEvents(ctx, ch, cb, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 ToolCallStream events, got %d", len(events))
	}
	for i, ev := range events {
		if ev.Kind != SubagentToolStream {
			t.Errorf("event[%d].Kind = %v, want SubagentToolStream", i, ev.Kind)
		}
		if ev.ToolName != "bash_subagent" {
			t.Errorf("event[%d].ToolName = %q, want bash_subagent", i, ev.ToolName)
		}
	}
}

func TestForwardEvents_ToolCallResultError(t *testing.T) {
	// REGRESSION: ToolError field was not forwarded to SubagentEvent, causing
	// TUI error display to miss tool-level failures (e.g. file_not_found).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var events []SubagentEvent
	cb := func(ev agentloop.TurnEvent) {
		if se, ok := ev.(SubagentEvent); ok {
			events = append(events, se)
		}
	}

	ch := make(chan agentloop.TurnEvent, 10)
	go func() {
		ch <- agentloop.ToolCallResult{
			ToolCallName: "read_file",
			Result:       "",
			DurationMs:   15,
			Error:        "file_not_found: /nonexistent.go",
		}
		ch <- agentloop.LoopDone{Turn: 1}
		close(ch)
	}()

	_, _, _, _, _, _, _, err := forwardEvents(ctx, ch, cb, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Kind != SubagentToolResult {
		t.Errorf("event kind = %v, want SubagentToolResult", events[0].Kind)
	}
	if events[0].ToolError != "file_not_found: /nonexistent.go" {
		t.Errorf("ToolError = %q, want %q", events[0].ToolError, "file_not_found: /nonexistent.go")
	}
	if events[0].ToolDurMs != 15 {
		t.Errorf("ToolDurMs = %d, want 15", events[0].ToolDurMs)
	}
}

func TestForwardEvents_ChannelCloseWithoutLoopDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan agentloop.TurnEvent, 10)
	go func() {
		ch <- agentloop.StreamDelta{ContentDelta: "partial"}
		close(ch)
	}()

	aggregated, turns, _, _, _, _, _, err := forwardEvents(ctx, ch, nil, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}
	if aggregated != "partial" {
		t.Errorf("aggregated = %q, want %q", aggregated, "partial")
	}
	if turns != 0 {
		t.Errorf("turns = %d, want 0 (no LoopDone)", turns)
	}
}

// REGRESSION: forwardEvents 只返回最后一个 turn 的文本，丢弃中间推理过程，
// 节省主 agent 的 token 消耗。
func TestRegression_ForwardEvents_OnlyLastTurnText(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan agentloop.TurnEvent, 10)
	go func() {
		// Turn 1（中间推理，应被丢弃）
		ch <- agentloop.StreamDelta{Turn: 1, ContentDelta: "turn 1 thinking..."}
		ch <- agentloop.StreamDelta{Turn: 1, ContentDelta: " more turn 1"}
		ch <- agentloop.ToolCallStart{Turn: 1, ToolCallName: "read_file", Arguments: `{"file_path":"a.go"}`}
		ch <- agentloop.ToolCallResult{Turn: 1, ToolCallName: "read_file", Result: "content", DurationMs: 10}
		// Turn 2（最终结论，应保留）
		ch <- agentloop.StreamDelta{Turn: 2, ContentDelta: "conclusion"}
		ch <- agentloop.StreamDelta{Turn: 2, ContentDelta: " finalized"}
		ch <- agentloop.LoopDone{Turn: 2}
		close(ch)
	}()

	lastTurnText, turns, _, _, _, _, _, err := forwardEvents(ctx, ch, nil, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}
	if lastTurnText != "conclusion finalized" {
		t.Errorf("lastTurnText = %q, want %q", lastTurnText, "conclusion finalized")
	}
	if turns != 2 {
		t.Errorf("turns = %d, want 2", turns)
	}
}

// ---------------------------------------------------------------------------
// Fork tests (need parent context)
// ---------------------------------------------------------------------------

func TestAgentTool_ExecuteFork_WorksWithoutParentMessages(t *testing.T) {
	ctx := context.Background()
	// Fork works even without parent messages (clean start fallback)
	a := &AgentTool{LLMClient: &stubLLM{}}
	result, err := a.Execute(ctx, AgentParams{
		Description: "fork-test",
		Prompt:      "do something",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "fork subagent completed") {
		t.Errorf("result should indicate fork completion: %s", result.Content)
	}
}

func TestBuildForkMessages(t *testing.T) {
	// 有父消息 → 保留最后 assistant + 注入占位 tool_result + fork 指令
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi there"}, // ← 无 tool_calls，保留不剥离
	}
	result := buildForkMessages(msgs, "test", "do it")
	if len(result) != 4 { // sys + user + assistant(kept) + fork directive
		t.Fatalf("expected 4 messages, got %d", len(result))
	}
	if result[0].Role != llm.RoleSystem || result[0].Content != "sys" {
		t.Error("system message should be preserved")
	}
	if result[1].Role != llm.RoleUser || result[1].Content != "hello" {
		t.Error("user message should be preserved")
	}
	if result[2].Role != llm.RoleAssistant || result[2].Content != "hi there" {
		t.Error("assistant message should be preserved (not stripped)")
	}
	if result[3].Role != llm.RoleUser || !strings.Contains(result[3].Content, forkBoilerplateTag) {
		t.Errorf("fork directive should be last user message with boilerplate: %+v", result[3])
	}
}

func TestBuildForkMessages_NilParent(t *testing.T) {
	result := buildForkMessages(nil, "test", "do it")
	if len(result) != 2 { // sys + fork directive
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
}

func TestAgentTool_ExecuteFork_EventCallback(t *testing.T) {
	ctx := context.Background()
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "system prompt"},
		{Role: llm.RoleUser, Content: "hello"},
	}
	ctx = agentloop.WithParentMessages(ctx, msgs)
	ctx = agentloop.WithEventCallback(ctx, func(agentloop.TurnEvent) {})

	a := &AgentTool{LLMClient: &stubLLM{}}
	result, err := a.Execute(ctx, AgentParams{
		Description: "fork-test",
		Prompt:      "do something",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "fork subagent completed") {
		t.Errorf("result should indicate fork completion: %s", result.Content)
	}
	if !strings.Contains(result.Content, "ok") {
		t.Errorf("result should contain LLM output: %s", result.Content)
	}
}

func TestAgentTool_ExecuteFork_SubagentStartEvent(t *testing.T) {
	ctx := context.Background()
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hi"},
	}
	ctx = agentloop.WithParentMessages(ctx, msgs)

	var gotStart bool
	ctx = agentloop.WithEventCallback(ctx, func(ev agentloop.TurnEvent) {
		if ss, ok := ev.(SubagentStart); ok {
			gotStart = true
			if ss.AgentType != "fork" {
				t.Errorf("fork AgentType = %q, want %q", ss.AgentType, "fork")
			}
			if !ss.InheritCtx {
				t.Error("fork InheritCtx should be true")
			}
		}
	})

	a := &AgentTool{LLMClient: &stubLLM{}}
	_, err := a.Execute(ctx, AgentParams{Description: "fork-test", Prompt: "test"})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !gotStart {
		t.Error("expected SubagentStart event for fork")
	}
}

func TestAgentTool_ExecuteFork_LLMError(t *testing.T) {
	errLLM := &errorLLM{}
	ctx := context.Background()
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hi"},
	}
	ctx = agentloop.WithParentMessages(ctx, msgs)

	var gotEndError bool
	ctx = agentloop.WithEventCallback(ctx, func(ev agentloop.TurnEvent) {
		if subEnd, ok := ev.(SubagentEnd); ok && subEnd.Error != "" {
			gotEndError = true
		}
	})

	a := &AgentTool{LLMClient: errLLM}
	result, err := a.Execute(ctx, AgentParams{Description: "fork-error", Prompt: "test"})
	if err != nil {
		t.Fatalf("Execute() should not return error (returns it in result): %v", err)
	}
	if !strings.Contains(result.Content, "Fork subagent failed") {
		t.Errorf("result should indicate fork failure: %s", result.Content)
	}
	if !gotEndError {
		t.Error("expected SubagentEnd with Error for fork")
	}
}

// ---------------------------------------------------------------------------
// Cold agent test: AGENTS.md injection
// ---------------------------------------------------------------------------

func TestAgentTool_ExecuteCold_WithAgentsMD(t *testing.T) {
	ctx := context.Background()
	capture := &captureLLM{}

	ctx = agentloop.WithAgentsMD(ctx, "# Project Rules\n\n- Use Go 1.25+\n")

	a := &AgentTool{LLMClient: capture}
	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "evaluate",
		Description:  "test",
		Prompt:       "say hello",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "evaluate") {
		t.Errorf("result should mention agent type: %s", result.Content)
	}
	// Verify AGENTS.md was NOT injected — all cold agents are read-only and skip it
	for _, msg := range capture.CapturedMessages {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "# Project Rules") {
			t.Error("evaluate agent should NOT receive AGENTS.md injection (all cold agents skip it)")
		}
	}
}


// ---------------------------------------------------------------------------
// Context helpers
// ---------------------------------------------------------------------------

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()

	// EventCallback
	var called bool
	ctx = agentloop.WithEventCallback(ctx, func(ev agentloop.TurnEvent) { called = true })
	cb := agentloop.EventCallbackFromContext(ctx)
	if cb == nil {
		t.Fatal("EventCallback should be non-nil")
	}
	cb(SubagentStart{})
	if !called {
		t.Error("callback should be called")
	}

	// ParentMessages
	type testMsg struct{ Text string }
	msgs := []testMsg{{Text: "hello"}}
	ctx = agentloop.WithParentMessages(ctx, msgs)
	got := agentloop.ParentMessagesFromContext(ctx)
	if got == nil {
		t.Fatal("ParentMessages should be non-nil")
	}
	gotMsgs, ok := got.([]testMsg)
	if !ok || len(gotMsgs) != 1 || gotMsgs[0].Text != "hello" {
		t.Error("ParentMessages round-trip failed")
	}
}

func TestSubagentStartEvent(t *testing.T) {
	ev := SubagentStart{
		AgentType:  "Explore",
		Prompt:     "test",
		InheritCtx: false,
	}
	if ev.AgentType != "Explore" {
		t.Errorf("AgentType = %q", ev.AgentType)
	}
	if ev.InheritCtx {
		t.Error("InheritCtx should be false")
	}
	// Compile-time: implements agentloop.TurnEvent
	var _ agentloop.TurnEvent = ev
}

func TestSubagentEventTypes(t *testing.T) {
	// Ensure constants are distinct
	if SubagentText == SubagentToolStart || SubagentText == SubagentToolResult || SubagentText == SubagentToolStream {
		t.Error("SubagentEventKind constants should be distinct")
	}
	if SubagentToolStart == SubagentToolResult || SubagentToolStart == SubagentToolStream {
		t.Error("SubagentEventKind constants should be distinct")
	}
	if SubagentToolResult == SubagentToolStream {
		t.Error("SubagentEventKind constants should be distinct")
	}
}

// ---------------------------------------------------------------------------
// Helper function unit tests
// ---------------------------------------------------------------------------

func TestParentSystemPromptFromContext(t *testing.T) {
	ctx := context.Background()
	ctx = agentloop.WithParentSystemPrompt(ctx, "test-system-prompt")

	got := ParentSystemPromptFromContext(ctx)
	if got != "test-system-prompt" {
		t.Errorf("ParentSystemPromptFromContext = %q, want %q", got, "test-system-prompt")
	}
}

func TestFormatArgs_Bash(t *testing.T) {
	// bash / bash_subagent should extract the "command" field
	got := formatArgs("bash_subagent", `{"command":"ls -la","working_dir":"/tmp"}`)
	if got != "ls -la" {
		t.Errorf("formatArgs(bash_subagent) = %q, want %q", got, "ls -la")
	}

	got = formatArgs("bash", `{"command":"echo hello"}`)
	if got != "echo hello" {
		t.Errorf("formatArgs(bash) = %q, want %q", got, "echo hello")
	}
}

func TestFormatArgs_WebFetch(t *testing.T) {
	got := formatArgs("web_fetch", `{"url":"https://example.com","max_size":1024}`)
	if got != "https://example.com" {
		t.Errorf("formatArgs(web_fetch) = %q, want %q", got, "https://example.com")
	}
}

func TestFormatArgs_WebFetchNoURL(t *testing.T) {
	// web_fetch without url → returns raw argsJSON
	got := formatArgs("web_fetch", `{"max_size":1024}`)
	if got != `{"max_size":1024}` {
		t.Errorf("formatArgs(web_fetch no url) = %q, want raw JSON", got)
	}
}

func TestFormatArgs_Fallback(t *testing.T) {
	// Unknown tool → returns raw argsJSON
	raw := `{"key":"value"}`
	got := formatArgs("unknown_tool", raw)
	if got != raw {
		t.Errorf("formatArgs(unknown) = %q, want %q", got, raw)
	}
}

func TestExtractField_NotFound(t *testing.T) {
	// key not found → ""
	got := extractField(`{"other":"value"}`, "file_path")
	if got != "" {
		t.Errorf("extractField(not found) = %q, want empty", got)
	}
}

func TestExtractField_NoColon(t *testing.T) {
	// key found but no colon → ""
	got := extractField(`{"file_path"}`, "file_path")
	if got != "" {
		t.Errorf("extractField(no colon) = %q, want empty", got)
	}
}

func TestExtractField_NoQuoteStart(t *testing.T) {
	// key:value exists but value doesn't start with "
	got := extractField(`{"file_path": 123}`, "file_path")
	if got != "" {
		t.Errorf("extractField(no quote start) = %q, want empty", got)
	}
}

func TestExtractField_NoEndQuote(t *testing.T) {
	// key:"value → no closing quote → ""
	got := extractField(`{"file_path":"abc}`, "file_path")
	if got != "" {
		t.Errorf("extractField(no end quote) = %q, want empty", got)
	}
}

func TestExtractPath_UpdatedFile(t *testing.T) {
	// write_file "Updated file:" variant
	got := extractPath("Updated file: /home/user/test.txt\nDone.")
	if got != "/home/user/test.txt" {
		t.Errorf("extractPath(Updated file) = %q, want %q", got, "/home/user/test.txt")
	}
}

func TestExtractPath_CreatedFile(t *testing.T) {
	// write_file "Created new file:" variant
	got := extractPath("Created new file: /tmp/hello.go\nok")
	if got != "/tmp/hello.go" {
		t.Errorf("extractPath(Created new file) = %q, want %q", got, "/tmp/hello.go")
	}
}

func TestExtractPath_EditFile(t *testing.T) {
	// edit_file "Edited file: /path\n"
	got := extractPath("Edited file: /src/main.go\n   Replaced 1 occurrence\n   +5 -2 lines")
	if got != "/src/main.go" {
		t.Errorf("extractPath(edit_file) = %q, want %q", got, "/src/main.go")
	}
}

func TestExtractPath_NotFound(t *testing.T) {
	// No match at all
	got := extractPath("Some random output")
	if got != "" {
		t.Errorf("extractPath(no match) = %q, want empty", got)
	}
}

func TestFmtBytes_Bytes(t *testing.T) {
	got := fmtBytes(42)
	if got != "42B" {
		t.Errorf("fmtBytes(42) = %q, want %q", got, "42B")
	}
}

func TestFmtBytes_KB(t *testing.T) {
	got := fmtBytes(2048)
	if got != "2.0KB" {
		t.Errorf("fmtBytes(2048) = %q, want %q", got, "2.0KB")
	}
}

func TestFmtBytes_MB(t *testing.T) {
	got := fmtBytes(2*1024*1024 + 512*1024)
	if got != "2.5MB" {
		t.Errorf("fmtBytes(2.5MB) = %q, want %q", got, "2.5MB")
	}
}

func TestBuildForkMessages_NonMessageType(t *testing.T) {
	// Pass a string instead of []llm.Message → should fall back to clean messages
	result := buildForkMessages("not a message slice", "desc", "task")
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (fallback), got %d", len(result))
	}
	if result[0].Role != llm.RoleSystem {
		t.Error("first message should be system (fallback)")
	}
	if !strings.Contains(result[1].Content, forkBoilerplateTag) {
		t.Error("second message should contain fork-boilerplate directive")
	}
}

func TestBuildForkMessages_EmptySlice(t *testing.T) {
	// Pass empty []llm.Message → should fall back to clean messages
	result := buildForkMessages([]llm.Message{}, "desc", "task")
	if len(result) != 2 {
		t.Fatalf("expected 2 messages (fallback), got %d", len(result))
	}
	if result[0].Role != llm.RoleSystem {
		t.Error("first message should be system (fallback)")
	}
}

func TestBuildForkMessages_NoAssistant(t *testing.T) {
	// REGRESSION: when message list has no assistant role, buildForkMessages
	// should preserve all messages and append fork directive without stripping.
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleUser, Content: "another question"},
	}
	result := buildForkMessages(msgs, "desc", "task")
	if len(result) != 4 { // sys + user + user + fork directive
		t.Fatalf("expected 4 messages (all preserved + fork), got %d", len(result))
	}
	// All original messages should be preserved
	if result[0].Content != "sys" {
		t.Error("system message should be preserved")
	}
	if result[1].Content != "hello" {
		t.Error("first user message should be preserved")
	}
	if result[2].Content != "another question" {
		t.Error("second user message should be preserved")
	}
	// Fork directive should be appended with boilerplate tag
	if !strings.Contains(result[3].Content, forkBoilerplateTag) {
		t.Error("fork directive should be appended as last message with boilerplate tag")
	}
}

func TestBuildForkMessages_KeepsAssistantWithToolCalls(t *testing.T) {
	// 有父消息，最后一条 assistant 含 tool_calls → 保留 assistant + 注入占位 tool 消息
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "let me check", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "agent", Arguments: `{"description":"x","prompt":"y"}`},
			{ID: "call_2", Name: "read_file", Arguments: `{"file_path":"/f.go"}`},
		}},
	}
	result := buildForkMessages(msgs, "fork-desc", "do something")
	// sys + user + assistant + tool(call_1) + tool(call_2) + user(fork directive) = 6
	if len(result) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(result))
	}
	// assistant 保留
	if result[2].Role != llm.RoleAssistant {
		t.Error("assistant should be preserved")
	}
	if len(result[2].ToolCalls) != 2 {
		t.Errorf("assistant should keep 2 tool_calls, got %d", len(result[2].ToolCalls))
	}
	// tool 占位消息
	if result[3].Role != llm.RoleTool || result[3].ToolCallID != "call_1" {
		t.Errorf("message 3 should be tool for call_1: %+v", result[3])
	}
	if result[3].Content != forkPlaceholderResult {
		t.Errorf("tool placeholder should be %q, got %q", forkPlaceholderResult, result[3].Content)
	}
	if result[4].Role != llm.RoleTool || result[4].ToolCallID != "call_2" {
		t.Errorf("message 4 should be tool for call_2: %+v", result[4])
	}
	if result[4].Content != forkPlaceholderResult {
		t.Errorf("tool placeholder should be %q, got %q", forkPlaceholderResult, result[4].Content)
	}
	// fork directive 包含 boilerplate
	if !strings.Contains(result[5].Content, forkBoilerplateTag) {
		t.Error("fork directive should contain boilerplate tag")
	}
}

func TestBuildForkDirective_ContainsBoilerplate(t *testing.T) {
	directive := buildForkDirective("test task", "do something specific")
	for _, keyword := range []string{
		forkBoilerplateTag,
		"fork child process",
		"Scope:",
		"Result:",
		"Key files:",
		"Files changed:",
		"Issues:",
		"test task",
		"do something specific",
	} {
		if !strings.Contains(directive, keyword) {
			t.Errorf("fork directive missing %q", keyword)
		}
	}
}

func TestIsInForkChild_Positive(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "some context"},
		{Role: llm.RoleUser, Content: "<fork-boilerplate>\nYou are a fork child process...\n</fork-boilerplate>\n\nTask: x"},
	}
	if !isInForkChild(msgs) {
		t.Error("isInForkChild should return true when boilerplate tag is present")
	}
}

func TestIsInForkChild_Negative(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello world"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}
	if isInForkChild(msgs) {
		t.Error("isInForkChild should return false when boilerplate tag is absent")
	}
}

func TestIsInForkChild_EmptyMessages(t *testing.T) {
	if isInForkChild(nil) {
		t.Error("isInForkChild should return false for nil messages")
	}
	if isInForkChild([]llm.Message{}) {
		t.Error("isInForkChild should return false for empty messages")
	}
}

func TestFindLastAssistant(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "q1"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleUser, Content: "q2"},
		{Role: llm.RoleAssistant, Content: "a2", ToolCalls: []llm.ToolCall{
			{ID: "tc1", Name: "agent"},
		}},
	}
	last := findLastAssistant(msgs)
	if last == nil {
		t.Fatal("expected to find last assistant")
	}
	if last.Content != "a2" {
		t.Errorf("last assistant content = %q, want %q", last.Content, "a2")
	}
	if len(last.ToolCalls) != 1 || last.ToolCalls[0].Name != "agent" {
		t.Error("last assistant should have agent tool_call")
	}
}

func TestFindLastAssistant_None(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "q1"},
	}
	if last := findLastAssistant(msgs); last != nil {
		t.Errorf("expected nil, got %+v", last)
	}
}

func TestExecute_RecursiveForkGuard(t *testing.T) {
	// fork 子 agent 尝试再次 fork → 返回 recoverable 错误
	ctx := context.Background()
	// 构造包含 fork-boilerplate 的父消息历史（模拟已在 fork 中的场景）
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "<fork-boilerplate>\nYou are a fork child process...\n</fork-boilerplate>\n\nTask: original fork"},
		{Role: llm.RoleAssistant, Content: "working...", ToolCalls: []llm.ToolCall{
			{ID: "call_nested", Name: "agent", Arguments: `{"description":"nested","prompt":"do nested fork"}`},
		}},
	}
	ctx = agentloop.WithParentMessages(ctx, msgs)

	a := &AgentTool{LLMClient: &stubLLM{}}
	result, err := a.Execute(ctx, AgentParams{
		Description: "nested fork attempt",
		Prompt:      "do nested fork",
	})
	if err != nil {
		t.Fatalf("Execute() should not return Go error: %v", err)
	}
	if !result.IsError() {
		t.Fatal("expected recoverable error for recursive fork")
	}
	if !strings.Contains(result.Content, "already a fork child") {
		t.Errorf("error should mention recursive fork prevention: %s", result.Content)
	}
	if !strings.Contains(result.Error.Message, "recursive fork") {
		t.Errorf("error message should mention recursive fork: %s", result.Error.Message)
	}
}

func TestExecute_RecursiveForkGuard_NonForkParent(t *testing.T) {
	// 正常父 agent（无 fork-boilerplate）可以自由 fork
	ctx := context.Background()
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
	}
	ctx = agentloop.WithParentMessages(ctx, msgs)

	a := &AgentTool{LLMClient: &stubLLM{}}
	result, err := a.Execute(ctx, AgentParams{
		Description: "valid fork",
		Prompt:      "do something",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "fork subagent completed") {
		t.Errorf("valid fork should succeed: %s", result.Content)
	}
}

// REGRESSION: Explore agents should skip AGENTS.md injection — they are read-only
// searchers that don't need coding standards, saving prompt tokens.
func TestAgentTool_ExecuteCold_ExploreSkipsAgentsMD(t *testing.T) {
	ctx := context.Background()
	capture := &captureLLM{}

	ctx = agentloop.WithAgentsMD(ctx, "# Project Rules\n\n- Use Go 1.25+\n")

	a := &AgentTool{LLMClient: capture}
	_, err := a.Execute(ctx, AgentParams{
		SubagentType: "Explore",
		Description:  "search",
		Prompt:       "find config files",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	// Verify AGENTS.md was NOT injected for Explore
	for _, msg := range capture.CapturedMessages {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "# Project Rules") {
			t.Error("Explore agent should NOT receive AGENTS.md injection")
		}
	}
}

// REGRESSION: Verification agent should also skip AGENTS.md — it's read-only.
func TestAgentTool_ExecuteCold_VerificationSkipsAgentsMD(t *testing.T) {
	ctx := context.Background()
	capture := &captureLLM{}

	ctx = agentloop.WithAgentsMD(ctx, "# Project Rules\n\n- Use Go 1.25+\n")

	a := &AgentTool{LLMClient: capture}
	_, err := a.Execute(ctx, AgentParams{
		SubagentType: "verification",
		Description:  "verify auth fix",
		Prompt:       "verify the recent auth changes",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	// Verify AGENTS.md was NOT injected for verification
	for _, msg := range capture.CapturedMessages {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "# Project Rules") {
			t.Error("verification agent should NOT receive AGENTS.md injection")
		}
	}
}

// REGRESSION: All cold agents skip AGENTS.md — they are read-only on project files.
func TestAgentTool_ExecuteCold_EvaluateSkipsAgentsMD(t *testing.T) {
	ctx := context.Background()
	capture := &captureLLM{}

	ctx = agentloop.WithAgentsMD(ctx, "# Project Rules\n\n- Use Go 1.25+\n")

	a := &AgentTool{LLMClient: capture}
	_, err := a.Execute(ctx, AgentParams{
		SubagentType: "evaluate",
		Description:  "review auth",
		Prompt:       "review auth.go for security issues",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	for _, msg := range capture.CapturedMessages {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "# Project Rules") {
			t.Error("evaluate agent should NOT receive AGENTS.md injection")
		}
	}
}

func TestEvaluateSystemPrompt_ContainsAssessmentFormat(t *testing.T) {
	sp := evaluateSystemPrompt()
	for _, keyword := range []string{
		"assessment",
		"CRITICAL",
		"WARNING",
		"NOTE",
		"READ-ONLY",
	} {
		if !strings.Contains(sp, keyword) {
			t.Errorf("evaluate system prompt missing %q", keyword)
		}
	}
}

// REGRESSION: Explore agents use exploreMaxTurns (25), not coldMaxTurns (50).
// This is verified indirectly: the stub LLM always returns "ok" immediately,
// so the agent completes in 1 turn regardless of limit. The limit is a safety
// ceiling, not a minimum. We verify the constant value is lower.
func TestExploreMaxTurns_LowerThanCold(t *testing.T) {
	if exploreMaxTurns >= coldMaxTurns {
		t.Errorf("exploreMaxTurns (%d) should be lower than coldMaxTurns (%d)", exploreMaxTurns, coldMaxTurns)
	}
}

func TestAgentTool_ExecuteCold_Verification(t *testing.T) {
	ctx := context.Background()

	a := &AgentTool{LLMClient: &stubLLM{}}
	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "verification",
		Description:  "verify auth fix",
		Prompt:       "verify the recent auth changes in login.go",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "verification") {
		t.Errorf("result should mention agent type: %s", result.Content)
	}
	if !strings.Contains(result.Content, "ok") {
		t.Errorf("result should contain LLM output: %s", result.Content)
	}
}

func TestVerificationRegistry_IsReadOnly(t *testing.T) {
	r := buildColdRegistry(verificationDisallowed)
	names := toolNames(r)
	for _, name := range []string{"read_file", "web_fetch", "bash_subagent"} {
		if !contains(names, name) {
			t.Errorf("verification registry missing %q", name)
		}
	}
	for _, name := range []string{"write_file", "edit_file"} {
		if contains(names, name) {
			t.Errorf("verification registry should NOT have %q", name)
		}
	}
}

func TestVerificationSystemPrompt_ContainsVerdictFormat(t *testing.T) {
	sp := verificationSystemPrompt()
	for _, keyword := range []string{
		"VERDICT: PASS",
		"VERDICT: FAIL",
		"Command run:",
		"Output observed:",
		"Result: PASS",
		"try to BREAK",
		"/tmp",
		"adversarial",
	} {
		if !strings.Contains(sp, keyword) {
			t.Errorf("verification system prompt missing %q", keyword)
		}
	}
}

func TestCountDiff_AddedOnly(t *testing.T) {
	added, removed := countDiff("+line1\n+line2\n normal\n")
	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestCountDiff_RemovedLines(t *testing.T) {
	added, removed := countDiff("-line1\n-line2\n normal\n")
	if added != 0 {
		t.Errorf("added = %d, want 0", added)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
}

func TestCountDiff_ExcludesHeaderLines(t *testing.T) {
	// +++ and --- header lines should NOT be counted
	added, removed := countDiff("+++ b/file.go\n--- a/file.go\n+real_add\n-real_del\n")
	if added != 1 {
		t.Errorf("added = %d, want 1 (+++ excluded)", added)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (--- excluded)", removed)
	}
}

func TestCountDiff_Mixed(t *testing.T) {
	added, removed := countDiff("+new line\n-old line\n+another new\n-older\n unchanged\n")
	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
}

func TestFormatSubagentEnvironment_WithOSAndShell(t *testing.T) {
	parentSP := "# System\n\n## Workspace\n\nWorking directory: /project\n\n## Environment\n\n- OS: darwin\n- Shell: /bin/zsh\n\nAvailable tools:\n  go         go version go1.25.8\n  cargo      cargo 1.85.0\n"
	ctx := context.Background()
	ctx = agentloop.WithParentSystemPrompt(ctx, parentSP)

	r := tool.NewRegistry()
	r.Register(tool.Wrap(&tool.ReadFile{}))
	r.Register(tool.Wrap(&tool.Shell{AllowBg: false}))

	got := formatSubagentEnvironment(ctx, r)
	if !strings.Contains(got, "## Environment") {
		t.Error("result should contain ## Environment section")
	}
	if !strings.Contains(got, "- OS: darwin") {
		t.Error("should contain OS info from parent")
	}
	if !strings.Contains(got, "- Shell: /bin/zsh") {
		t.Error("should contain Shell info from parent")
	}
	if !strings.Contains(got, "read_file") {
		t.Error("should list read_file from registry")
	}
	if !strings.Contains(got, "bash_subagent") {
		t.Error("should list bash_subagent from registry")
	}
	// 不应包含子 agent 不可用的工具
	if strings.Contains(got, "go version") || strings.Contains(got, "cargo") {
		t.Error("should NOT contain tools that are not in the subagent registry")
	}
}

func TestFormatSubagentEnvironment_EmptyParent(t *testing.T) {
	ctx := context.Background()
	// No parent system prompt in context

	r := tool.NewRegistry()
	got := formatSubagentEnvironment(ctx, r)
	if got != "" {
		t.Errorf("formatSubagentEnvironment with empty parent should return empty: %q", got)
	}
}

func TestFormatSubagentEnvironment_ExploreRegistry(t *testing.T) {
	parentSP := "# System\n\n## Workspace\n\nWorking directory: /src\n\n## Environment\n\n- OS: linux\n- Shell: /bin/bash\n\nAvailable tools:\n  docker     Docker 29.4.0\n  node       v23.10.0\n  go         go1.25.8\n"
	ctx := context.Background()
	ctx = agentloop.WithParentSystemPrompt(ctx, parentSP)

	r := buildColdRegistry(exploreDisallowed)

	got := formatSubagentEnvironment(ctx, r)
	// Explore 只有 read_file, web_fetch, bash_subagent
	if !strings.Contains(got, "read_file") {
		t.Error("should list read_file")
	}
	if !strings.Contains(got, "web_fetch") {
		t.Error("should list web_fetch")
	}
	// 不应包含 write_file 和 edit_file
	if strings.Contains(got, "write_file") {
		t.Error("Explore should NOT list write_file")
	}
	if strings.Contains(got, "edit_file") {
		t.Error("Explore should NOT list edit_file")
	}
	// 不应包含 docker, node, go
	if strings.Contains(got, "docker") {
		t.Error("should NOT contain parent tool 'docker'")
	}
}


// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func toolNames(r tool.Registry) []string {
	var names []string
	for _, s := range r.List() {
		names = append(names, s.Name)
	}
	return names
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Benchmark
// ---------------------------------------------------------------------------

func BenchmarkForwardEvents(b *testing.B) {
	for b.Loop() {
		ctx, cancel := context.WithCancel(context.Background())
		ch := make(chan agentloop.TurnEvent, 100)
		go func() {
			for i := 0; i < 50; i++ {
				ch <- agentloop.StreamDelta{ContentDelta: "some text content"}
			}
			ch <- agentloop.ToolCallStart{ToolCallName: "read_file", Arguments: `{"file_path":"/path/to/file.go"}`}
			ch <- agentloop.ToolCallResult{ToolCallName: "read_file", Result: "content", DurationMs: 42}
			ch <- agentloop.LoopDone{Turn: 3}
			close(ch)
		}()
		_, _, _, _, _, _, _, _ = forwardEvents(ctx, ch, nil, "")
		cancel()
	}
}

// ---------------------------------------------------------------------------
// Model switching tests
// ---------------------------------------------------------------------------

func TestIsValidModel_EmptyString(t *testing.T) {
	a := &AgentTool{ValidModels: []string{"deepseek-v4-pro", "deepseek-v4-flash"}}
	if !a.isValidModel("") {
		t.Error("empty model should be valid (inherit default)")
	}
}

func TestIsValidModel_ValidValue(t *testing.T) {
	a := &AgentTool{ValidModels: []string{"deepseek-v4-pro", "deepseek-v4-flash"}}
	if !a.isValidModel("deepseek-v4-flash") {
		t.Error("valid model should be accepted")
	}
	if !a.isValidModel("deepseek-v4-pro") {
		t.Error("valid model should be accepted")
	}
}

func TestIsValidModel_InvalidValue(t *testing.T) {
	a := &AgentTool{ValidModels: []string{"deepseek-v4-pro", "deepseek-v4-flash"}}
	if a.isValidModel("garbage-model") {
		t.Error("invalid model should be rejected")
	}
}

func TestIsValidModel_EmptyList(t *testing.T) {
	a := &AgentTool{ValidModels: nil}
	if !a.isValidModel("anything") {
		t.Error("empty ValidModels should accept any model (no restriction)")
	}
}

func TestIsValidModel_EmptySlice(t *testing.T) {
	a := &AgentTool{ValidModels: []string{}}
	if !a.isValidModel("anything") {
		t.Error("empty ValidModels slice should accept any model")
	}
}

// REGRESSION: invalid model in AgentParams → sanitized to empty before sub-loop creation.
func TestAgentTool_ExecuteFork_InvalidModelFallsBack(t *testing.T) {
	ctx := context.Background()
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
	}
	ctx = agentloop.WithParentMessages(ctx, msgs)

	a := &AgentTool{
		LLMClient:   &stubLLM{},
		ValidModels: []string{"deepseek-v4-pro", "deepseek-v4-flash"},
	}
	result, err := a.Execute(ctx, AgentParams{
		Description: "fork-test",
		Prompt:      "do something",
		Model:       "garbage", // invalid → should be cleared
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "fork subagent completed") {
		t.Errorf("fork should succeed with invalid model sanitized: %s", result.Content)
	}
}

// REGRESSION: valid model in AgentParams → passed through to sub-loop.
func TestAgentTool_ExecuteCold_ValidModel(t *testing.T) {
	ctx := context.Background()
	a := &AgentTool{
		LLMClient:   &stubLLM{},
		ValidModels: []string{"deepseek-v4-pro", "deepseek-v4-flash"},
	}
	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "Explore",
		Description:  "search",
		Prompt:       "find it",
		Model:        "deepseek-v4-flash",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "Explore") {
		t.Errorf("result should succeed with valid model: %s", result.Content)
	}
}

// REGRESSION: SubagentStart carries Model field.
func TestSubagentStart_ModelField(t *testing.T) {
	ev := SubagentStart{
		AgentType: "Explore",
		Model:     "deepseek-v4-flash",
	}
	if ev.Model != "deepseek-v4-flash" {
		t.Errorf("SubagentStart.Model = %q, want %q", ev.Model, "deepseek-v4-flash")
	}
	// Model should be empty by default (zero value)
	ev2 := SubagentStart{}
	if ev2.Model != "" {
		t.Errorf("SubagentStart.Model zero value = %q, want empty", ev2.Model)
	}
}

// REGRESSION: Explore auto-model — when SubagentType is "Explore" and no model
// is specified, executeCold must automatically select DefaultSubModel (Phase 2).
func TestAgentTool_ExecuteCold_ExploreAutoModel(t *testing.T) {
	ctx := context.Background()
	a := &AgentTool{
		LLMClient:       &stubLLM{},
		ValidModels:     []string{"deepseek-v4-pro", "deepseek-v4-flash"},
		DefaultSubModel: "deepseek-v4-flash",
	}

	var capturedModel string
	cb := func(ev agentloop.TurnEvent) {
		if start, ok := ev.(SubagentStart); ok {
			capturedModel = start.Model
		}
	}
	ctx = agentloop.WithEventCallback(ctx, cb)
	ctx = agentloop.WithToolCallID(ctx, "call-explore-auto")

	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "Explore",
		Description:  "auto model test",
		Prompt:       "find it",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "Explore") {
		t.Errorf("result should succeed: %s", result.Content)
	}
	if capturedModel != "deepseek-v4-flash" {
		t.Errorf("SubagentStart.Model = %q, want %q (Explore auto flash)", capturedModel, "deepseek-v4-flash")
	}
}

// REGRESSION: Explore auto-model — empty DefaultSubModel means no auto-selection.
func TestAgentTool_ExecuteCold_ExploreAutoModel_EmptyDefault(t *testing.T) {
	ctx := context.Background()
	a := &AgentTool{
		LLMClient:       &stubLLM{},
		ValidModels:     []string{"deepseek-v4-pro"},
		DefaultSubModel: "",
	}

	var capturedModel string
	cb := func(ev agentloop.TurnEvent) {
		if start, ok := ev.(SubagentStart); ok {
			capturedModel = start.Model
		}
	}
	ctx = agentloop.WithEventCallback(ctx, cb)
	ctx = agentloop.WithToolCallID(ctx, "call-explore-no-sub")

	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "Explore",
		Description:  "no sub model",
		Prompt:       "find it",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "Explore") {
		t.Errorf("result should succeed: %s", result.Content)
	}
	if capturedModel != "" {
		t.Errorf("SubagentStart.Model = %q, want empty (no DefaultSubModel configured)", capturedModel)
	}
}

// ---------------------------------------------------------------------------
// Advisor mode tests
// ---------------------------------------------------------------------------

func TestAdvisorMode_ExploreAutoFlash(t *testing.T) {
	ctx := context.Background()
	a := &AgentTool{
		LLMClient:       &stubLLM{},
		DefaultSubModel: "sub-model",
	}

	var capturedModel string
	cb := func(ev agentloop.TurnEvent) {
		if start, ok := ev.(SubagentStart); ok {
			capturedModel = start.Model
		}
	}
	ctx = agentloop.WithEventCallback(ctx, cb)
	ctx = agentloop.WithToolCallID(ctx, "call-explore-auto")

	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "Explore",
		Description:  "auto flash test",
		Prompt:       "find something",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "Explore") {
		t.Errorf("result should mention agent type: %s", result.Content)
	}
	if capturedModel != "sub-model" {
		t.Errorf("SubagentStart.Model = %q, want %q (Explore auto uses DefaultSubModel)", capturedModel, "sub-model")
	}
}

func TestAdvisorMode_EvaluateStaysPro(t *testing.T) {
	ctx := context.Background()
	a := &AgentTool{
		LLMClient:       &stubLLM{},
		DefaultSubModel: "flash",
	}

	var capturedModel string
	cb := func(ev agentloop.TurnEvent) {
		if start, ok := ev.(SubagentStart); ok {
			capturedModel = start.Model
		}
	}
	ctx = agentloop.WithEventCallback(ctx, cb)
	ctx = agentloop.WithToolCallID(ctx, "call-evaluate-pro")

	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "evaluate",
		Description:  "stay pro test",
		Prompt:       "review something",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "evaluate") {
		t.Errorf("result should mention agent type: %s", result.Content)
	}
	if capturedModel != "" {
		t.Errorf("SubagentStart.Model = %q, want empty (evaluate should not downgrade)", capturedModel)
	}
}

func TestAdvisorMode_ColdAgentExplicitModel(t *testing.T) {
	// REGRESSION: 冷代理模型锁定后，LLM 传入的 model 参数被忽略。
	// evaluate 始终锁定 DefaultModel，缺少时为空（走 client 默认）。
	ctx := context.Background()
	a := &AgentTool{LLMClient: &stubLLM{}}

	var capturedModel string
	cb := func(ev agentloop.TurnEvent) {
		if start, ok := ev.(SubagentStart); ok {
			capturedModel = start.Model
		}
	}
	ctx = agentloop.WithEventCallback(ctx, cb)
	ctx = agentloop.WithToolCallID(ctx, "call-cold-explicit")

	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "evaluate",
		Description:  "explicit model test",
		Prompt:       "review something",
		Model:        "custom-model",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	// evaluate locks to DefaultModel ("" when unset), ignoring LLM's explicit model
	if capturedModel != "" {
		t.Errorf("SubagentStart.Model = %q, want %q (evaluate locks to DefaultModel, ignores explicit)", capturedModel, "")
	}
	_ = result
}

func TestAdvisorMode_AdvisorSubagent_UsesPrimaryModel(t *testing.T) {
	ctx := context.Background()
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
	}
	ctx = agentloop.WithParentMessages(ctx, msgs)

	var capturedModel string
	cb := func(ev agentloop.TurnEvent) {
		if start, ok := ev.(SubagentStart); ok {
			capturedModel = start.Model
		}
	}
	ctx = agentloop.WithEventCallback(ctx, cb)
	ctx = agentloop.WithToolCallID(ctx, "call-advisor-model")

	a := &AgentTool{
		LLMClient:       &stubLLM{},
		DefaultModel:    "deepseek-v4-pro",
		DefaultSubModel: "deepseek-v4-flash",
	}

	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "advisor",
		Description:  "advisor test",
		Prompt:       "analyze something",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	// Advisor routes to executeFork → fork format, not cold format
	if !strings.Contains(result.Content, "fork subagent completed") {
		t.Errorf("advisor should produce fork-style result, got: %s", result.Content)
	}
	if strings.Contains(result.Content, "subagent [advisor]") {
		t.Errorf("advisor should NOT produce cold-style result: %s", result.Content)
	}
	// Advisor must lock to DefaultModel regardless of AgentParams.Model
	if capturedModel != "deepseek-v4-pro" {
		t.Errorf("SubagentStart.Model = %q, want %q (advisor always locks to DefaultModel)", capturedModel, "deepseek-v4-pro")
	}
}

func TestAdvisorMode_AdvisorSubagent_ReadOnly(t *testing.T) {
	// Advisor uses buildColdRegistry(exploreDisallowed) — same as Explore
	// The advisor path in executeFork replaces the fork registry with this
	r := buildColdRegistry(exploreDisallowed)
	names := toolNames(r)
	for _, name := range []string{"write_file", "edit_file"} {
		if contains(names, name) {
			t.Errorf("advisor registry should NOT have %q", name)
		}
	}
	for _, name := range []string{"read_file", "web_fetch", "bash_subagent"} {
		if !contains(names, name) {
			t.Errorf("advisor registry missing %q", name)
		}
	}
}
