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

	aggregated, turns, promptTok, complTok, err := forwardEvents(ctx, ch, nil, "")
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

	_, _, _, _, err := forwardEvents(ctx, ch, cb, "")
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

	aggregated, _, _, _, err := forwardEvents(ctx, ch, nil, "")
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

	_, turns, promptTok, complTok, err := forwardEvents(ctx, ch, nil, "")
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

	_, _, _, _, err := forwardEvents(ctx, ch, nil, "")
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

	aggregated, _, _, _, err := forwardEvents(ctx, ch, nil, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}
	if aggregated != "" {
		t.Errorf("aggregated = %q, want empty", aggregated)
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

	_, _, _, _, err := forwardEvents(ctx, ch, cb, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 ToolCallStream events, got %d", len(events))
	}
	for i, ev := range events {
		if ev.Kind != SubagentToolResult {
			t.Errorf("event[%d].Kind = %v, want SubagentToolResult", i, ev.Kind)
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

	_, _, _, _, err := forwardEvents(ctx, ch, cb, "")
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

	aggregated, turns, _, _, err := forwardEvents(ctx, ch, nil, "")
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

	lastTurnText, turns, _, _, err := forwardEvents(ctx, ch, nil, "")
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
	// Verify AGENTS.md content was injected as a user message
	foundAgentsMD := false
	for _, msg := range capture.CapturedMessages {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "# Project Rules") {
			foundAgentsMD = true
			break
		}
	}
	if !foundAgentsMD {
		t.Error("AGENTS.md content should be injected as a user message")
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
	// edit_file "✅ Edit applied to /path"
	got := extractPath("✅ Edit applied to /src/main.go (+5 -2 lines)")
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
	if !strings.Contains(result[1].Content, "Fork task") {
		t.Error("second message should contain fork directive")
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
	// Fork directive should be appended
	if !strings.Contains(result[3].Content, "Fork task") {
		t.Error("fork directive should be appended as last message")
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

func TestAppendParentContext_WithWorkspace(t *testing.T) {
	agentSP := "You are a general-purpose agent."
	parentSP := "# System\n\n## Workspace\n\nWorking directory: /project\n\n## Environment\n\n- go 1.25\n"

	ctx := context.Background()
	ctx = agentloop.WithParentSystemPrompt(ctx, parentSP)

	got := appendParentContext(agentSP, ctx)
	if !strings.Contains(got, "You are a general-purpose agent.") {
		t.Error("result should still contain the agent-specific prompt")
	}
	if !strings.Contains(got, "## Workspace") {
		t.Error("result should contain the Workspace section from parent")
	}
	if !strings.Contains(got, "## Environment") {
		t.Error("result should contain the Environment section from parent")
	}
	if !strings.Contains(got, "go 1.25") {
		t.Error("result should contain parent environment details")
	}
}

func TestAppendParentContext_NoWorkspaceHeader(t *testing.T) {
	agentSP := "You are a general-purpose agent."
	parentSP := "# System\n\nJust some intro text without workspace section.\n"

	ctx := context.Background()
	ctx = agentloop.WithParentSystemPrompt(ctx, parentSP)

	got := appendParentContext(agentSP, ctx)
	// Should return agentSP unchanged (no "## Workspace" found)
	if got != agentSP {
		t.Errorf("appendParentContext without workspace should return agentSP unchanged: %q", got)
	}
}

func TestAppendParentContext_EmptyParentSP(t *testing.T) {
	agentSP := "You are a general-purpose agent."

	ctx := context.Background()
	// No parent system prompt in context

	got := appendParentContext(agentSP, ctx)
	if got != agentSP {
		t.Errorf("appendParentContext with empty parent should return agentSP unchanged: %q", got)
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
		_, _, _, _, _ = forwardEvents(ctx, ch, nil, "")
		cancel()
	}
}
