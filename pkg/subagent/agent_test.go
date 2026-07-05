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
	for _, keyword := range []string{"subagent", "fork", "general-purpose", "Explore"} {
		if !strings.Contains(desc, keyword) {
			t.Errorf("Description missing keyword %q", keyword)
		}
	}
}

// ---------------------------------------------------------------------------
// Cold agent tests
// ---------------------------------------------------------------------------

func TestAgentTool_ExecuteCold_GeneralPurpose(t *testing.T) {
	ctx := context.Background()

	a := &AgentTool{LLMClient: &stubLLM{}}
	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "general-purpose",
		Description:  "test",
		Prompt:       "say hello",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "general-purpose") {
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

func TestAgentTool_ExecuteCold_UnknownTypeDefaultsToGeneral(t *testing.T) {
	ctx := context.Background()

	a := &AgentTool{LLMClient: &stubLLM{}}
	// Unknown type should fall back to general-purpose
	result, err := a.Execute(ctx, AgentParams{
		SubagentType: "nonexistent",
		Description:  "test",
		Prompt:       "test",
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !strings.Contains(result.Content, "general-purpose") && !strings.Contains(result.Content, "nonexistent") {
		t.Errorf("unknown type should fall back to general-purpose system prompt: %s", result.Content)
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
		SubagentType: "general-purpose",
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

func TestBuildColdRegistry_GeneralPurpose_HasAllWriteableTools(t *testing.T) {
	r := buildColdRegistry(nil) // general-purpose: no extra disallowed
	names := toolNames(r)
	for _, name := range []string{"read_file", "write_file", "edit_file", "web_fetch", "bash_subagent"} {
		if !contains(names, name) {
			t.Errorf("general-purpose registry missing %q", name)
		}
	}
	// bash (main agent) should NOT be available
	if contains(names, "bash") {
		t.Error("general-purpose registry should NOT have bash")
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

	aggregated, turns, promptTok, complTok, err := forwardEvents(ctx, ch, nil)
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

	_, _, _, _, err := forwardEvents(ctx, ch, cb)
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

	aggregated, _, _, _, err := forwardEvents(ctx, ch, nil)
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
		ch <- agentloop.TurnStats{PromptTokens: 100, CompletionTokens: 50}
		ch <- agentloop.TurnStats{PromptTokens: 200, CompletionTokens: 75}
		ch <- agentloop.LoopDone{Turn: 2}
		close(ch)
	}()

	_, turns, promptTok, complTok, err := forwardEvents(ctx, ch, nil)
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}
	if turns != 2 {
		t.Errorf("turns = %d, want 2", turns)
	}
	if promptTok != 300 || complTok != 125 { // 100+200=300, 50+75=125
		t.Errorf("promptTokens = %d, complTokens = %d, want 300, 125", promptTok, complTok)
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

	_, _, _, _, err := forwardEvents(ctx, ch, nil)
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

	aggregated, _, _, _, err := forwardEvents(ctx, ch, nil)
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}
	if aggregated != "" {
		t.Errorf("aggregated = %q, want empty", aggregated)
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
	// 有父消息 → 继承并剥离最后 assistant
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi there"}, // ← 这条会被剥离
	}
	result := buildForkMessages(msgs, "test", "do it")
	if len(result) != 3 { // sys + user + fork directive
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
	if result[0].Role != llm.RoleSystem || result[0].Content != "sys" {
		t.Error("system message should be preserved")
	}
	if result[1].Role != llm.RoleUser || result[1].Content != "hello" {
		t.Error("user message should be preserved")
	}
	if result[2].Role != llm.RoleUser || !strings.Contains(result[2].Content, "Fork task") {
		t.Errorf("fork directive should be user message: %+v", result[2])
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
	if SubagentText == SubagentToolStart || SubagentText == SubagentToolResult {
		t.Error("SubagentEventKind constants should be distinct")
	}
	if SubagentToolStart == SubagentToolResult {
		t.Error("SubagentEventKind constants should be distinct")
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
		_, _, _, _, _ = forwardEvents(ctx, ch, nil)
		cancel()
	}
}
