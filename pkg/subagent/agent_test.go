package subagent

import (
	"fmt"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/tool"
)

func TestAgentTool_NewCompactor(t *testing.T) {
	// REGRESSION: 子代理 loop 曾无 Compactor——50 turn 上限不限制每轮 token
	// 增量(大文件/大输出),长任务撞窗口导致 API 400 失败。修复后子代理
	// 持有独立压缩器(Tier 1/2 零成本生效,Tier 3 nil summarizer 跳过)。
	a := &AgentTool{}
	if c := a.newCompactor(); c == nil {
		t.Fatal("newCompactor must return non-nil (zero config → normalize)")
	}

	// 配置与父级同源(入口 resolveCompactionConfig 传入)
	a2 := &AgentTool{CompactionConfig: compaction.CompactionConfig{ContextLimit: 128_000}}
	tc, ok := a2.newCompactor().(*compaction.TieredCompactor)
	if !ok {
		t.Fatalf("newCompactor type = %T, want *TieredCompactor", a2.newCompactor())
	}
	if got := tc.ContextLimit(); got != 128_000 {
		t.Errorf("ContextLimit = %d, want 128000", got)
	}
}

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
	for _, keyword := range []string{"subagent", "Agent Tool"} {
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
	if !strings.Contains(result.Content, "explore") {
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
	ctx = agentloop.WithEventCallback(ctx, func(ev agentloop.StepEvent) {
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
	ctx = agentloop.WithEventCallback(ctx, func(ev agentloop.StepEvent) {
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
	a := &AgentTool{}
	r := a.buildColdRegistry(coldDisallowed)
	names := toolNames(r)
	for _, name := range []string{"read", "web_fetch", "bash_subagent"} {
		if !contains(names, name) {
			t.Errorf("evaluate registry missing %q", name)
		}
	}
	for _, name := range []string{"write", "edit"} {
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
	a := &AgentTool{}
	r := a.buildColdRegistry(coldDisallowed)
	names := toolNames(r)
	for _, name := range []string{"read", "web_fetch", "bash_subagent"} {
		if !contains(names, name) {
			t.Errorf("Explore registry missing %q", name)
		}
	}
	for _, name := range []string{"write", "edit"} {
		if contains(names, name) {
			t.Errorf("Explore registry should NOT have %q", name)
		}
	}
}

func TestBuildColdRegistry_NoAgentTool(t *testing.T) {
	a := &AgentTool{}
	r := a.buildColdRegistry(nil)
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

	ch := make(chan agentloop.StepEvent, 10)
	go func() {
		ch <- agentloop.StreamDelta{ContentDelta: "hello "}
		ch <- agentloop.StreamDelta{ContentDelta: "world"}
		ch <- agentloop.TurnDone{Step: 1}
		close(ch)
	}()

	aggregated, steps, promptTok, complTok, _, _, _, err := forwardEvents(ctx, ch, nil, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}
	if aggregated != "hello world" {
		t.Errorf("aggregated = %q, want %q", aggregated, "hello world")
	}
	if steps != 1 {
		t.Errorf("steps = %d, want 1", steps)
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
	cb := func(ev agentloop.StepEvent) {
		if se, ok := ev.(SubagentEvent); ok {
			events = append(events, se)
		}
	}

	ch := make(chan agentloop.StepEvent, 10)
	go func() {
		ch <- agentloop.StreamDelta{ReasoningDelta: "let me think about this..."}
		ch <- agentloop.StreamDelta{ContentDelta: "the answer is 42"}
		ch <- agentloop.StreamDelta{ReasoningDelta: "actually, double-checking..."}
		ch <- agentloop.StreamDelta{ContentDelta: " yes, 42"}
		ch <- agentloop.TurnDone{Step: 1}
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
	cb := func(ev agentloop.StepEvent) {
		if se, ok := ev.(SubagentEvent); ok {
			events = append(events, se)
		}
	}

	ch := make(chan agentloop.StepEvent, 10)
	go func() {
		ch <- agentloop.StreamDelta{ContentDelta: "thinking..."}
		ch <- agentloop.ToolCallStart{ToolCallName: "read", Arguments: `{"file_path":"x.go"}`}
		ch <- agentloop.ToolCallResult{ToolCallName: "read", Result: "file content", DurationMs: 42}
		ch <- agentloop.TurnDone{Step: 1}
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
	if events[1].Kind != SubagentToolStart || events[1].ToolName != "read" {
		t.Errorf("event[1] wrong: %+v", events[1])
	}
	if events[2].Kind != SubagentToolResult || events[2].ToolDurMs != 42 {
		t.Errorf("event[2] wrong: %+v", events[2])
	}
}

func TestForwardEvents_WriteOperationsTracking(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan agentloop.StepEvent, 10)
	go func() {
		ch <- agentloop.StreamDelta{ContentDelta: "done."}
		ch <- agentloop.ToolCallResult{
			ToolCallName: "write",
			Result:       "Wrote 42 bytes to /tmp/test.go",
		}
		ch <- agentloop.ToolCallResult{
			ToolCallName: "edit",
			Result:       "@@ -1,0 +1,2 @@\n+added line\n+another\n",
		}
		ch <- agentloop.TurnDone{Step: 1}
		close(ch)
	}()

	aggregated, _, _, _, _, _, _, err := forwardEvents(ctx, ch, nil, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}

	if !strings.Contains(aggregated, "<subagent_write_operations>") {
		t.Error("aggregated output should contain write operations block")
	}
	if !strings.Contains(aggregated, "write") {
		t.Error("write operations should list write_file")
	}
	if !strings.Contains(aggregated, "edit") {
		t.Error("write operations should list edit_file")
	}
}

func TestForwardEvents_StepStatsAccumulation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan agentloop.StepEvent, 10)
	go func() {
		ch <- agentloop.StepStats{PromptTokens: 100, CompletionTokens: 50, CacheHitTokens: 60, CacheMissTokens: 40}
		ch <- agentloop.StepStats{PromptTokens: 200, CompletionTokens: 75, CacheHitTokens: 120, CacheMissTokens: 80}
		ch <- agentloop.TurnDone{Step: 2}
		close(ch)
	}()

	_, steps, promptTok, complTok, cacheHitTok, cacheMissTok, _, err := forwardEvents(ctx, ch, nil, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}
	if steps != 2 {
		t.Errorf("steps = %d, want 2", steps)
	}
	if promptTok != 300 || complTok != 125 {
		t.Errorf("promptTokens = %d, complTokens = %d, want 300, 125", promptTok, complTok)
	}
	if cacheHitTok != 180 || cacheMissTok != 120 {
		t.Errorf("cacheHitTokens = %d, cacheMissTokens = %d, want 180, 120", cacheHitTok, cacheMissTok)
	}
}

func TestForwardEvents_TurnDoneError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	expectedErr := errors.New("subagent crashed")
	ch := make(chan agentloop.StepEvent, 10)
	go func() {
		ch <- agentloop.TurnDone{Step: 0, Err: expectedErr}
		close(ch)
	}()

	_, _, _, _, _, _, _, err := forwardEvents(ctx, ch, nil, "")
	if err == nil {
		t.Fatal("expected error from TurnDone")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("err = %v, want %v", err, expectedErr)
	}
}

func TestForwardEvents_EmptyStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan agentloop.StepEvent, 1)
	go func() {
		ch <- agentloop.TurnDone{Step: 0}
		close(ch)
	}()

	aggregated, _, _, _, _, _, _, err := forwardEvents(ctx, ch, nil, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}
	// 空流(无任何文本输出)应返回兜底文本,而非空字符串,
	// 防止 tool_result 内容为空导致父 agent 误解。
	if aggregated == "" {
		t.Errorf("aggregated is empty, want non-empty fallback")
	}
}

func TestForwardEvents_ToolCallStreamEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var events []SubagentEvent
	cb := func(ev agentloop.StepEvent) {
		if se, ok := ev.(SubagentEvent); ok {
			events = append(events, se)
		}
	}

	ch := make(chan agentloop.StepEvent, 10)
	go func() {
		ch <- agentloop.ToolCallStream{ToolCallName: "bash_subagent", Chunk: "line1\n"}
		ch <- agentloop.ToolCallStream{ToolCallName: "bash_subagent", Chunk: "line2\n"}
		ch <- agentloop.TurnDone{Step: 1}
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
	cb := func(ev agentloop.StepEvent) {
		if se, ok := ev.(SubagentEvent); ok {
			events = append(events, se)
		}
	}

	ch := make(chan agentloop.StepEvent, 10)
	go func() {
		ch <- agentloop.ToolCallResult{
			ToolCallName: "read",
			Result:       "",
			DurationMs:   15,
			Error:        "file_not_found: /nonexistent.go",
		}
		ch <- agentloop.TurnDone{Step: 1}
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

func TestForwardEvents_ChannelCloseWithoutTurnDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan agentloop.StepEvent, 10)
	go func() {
		ch <- agentloop.StreamDelta{ContentDelta: "partial"}
		close(ch)
	}()

	aggregated, steps, _, _, _, _, _, err := forwardEvents(ctx, ch, nil, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}
	if aggregated != "partial" {
		t.Errorf("aggregated = %q, want %q", aggregated, "partial")
	}
	if steps != 0 {
		t.Errorf("steps = %d, want 0 (no TurnDone)", steps)
	}
}

// REGRESSION: forwardEvents 只返回最后一个 turn 的文本,丢弃中间推理过程,
// 节省主 agent 的 token 消耗。
func TestRegression_ForwardEvents_OnlyLastStepText(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan agentloop.StepEvent, 10)
	go func() {
		// Turn 1(中间推理,应被丢弃)
		ch <- agentloop.StreamDelta{Step: 1, ContentDelta: "step 1 thinking..."}
		ch <- agentloop.StreamDelta{Step: 1, ContentDelta: " more step 1"}
		ch <- agentloop.ToolCallStart{Step: 1, ToolCallName: "read", Arguments: `{"file_path":"a.go"}`}
		ch <- agentloop.ToolCallResult{Step: 1, ToolCallName: "read", Result: "content", DurationMs: 10}
		// Turn 2(最终结论,应保留)
		ch <- agentloop.StreamDelta{Step: 2, ContentDelta: "conclusion"}
		ch <- agentloop.StreamDelta{Step: 2, ContentDelta: " finalized"}
		ch <- agentloop.TurnDone{Step: 2}
		close(ch)
	}()

	lastStepText, steps, _, _, _, _, _, err := forwardEvents(ctx, ch, nil, "")
	if err != nil {
		t.Fatalf("forwardEvents error: %v", err)
	}
	if lastStepText != "conclusion finalized" {
		t.Errorf("lastStepText = %q, want %q", lastStepText, "conclusion finalized")
	}
	if steps != 2 {
		t.Errorf("steps = %d, want 2", steps)
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
	// fork 仅继承到最后一个 user 消息(不含 assistant),然后追加 fork directive
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi there"},
	}
	result := buildForkMessages(msgs, "test", "do it")
	// sys + user + fork directive = 3(assistant 被排除)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages (assistant excluded), got %d", len(result))
	}
	if result[0].Role != llm.RoleSystem || result[0].Content != "sys" {
		t.Error("system message should be preserved")
	}
	if result[1].Role != llm.RoleUser || result[1].Content != "hello" {
		t.Error("user message should be preserved")
	}
	if result[2].Role != llm.RoleUser || !strings.Contains(result[2].Content, forkBoilerplateTag) {
		t.Errorf("fork directive should be last user message with boilerplate: %+v", result[2])
	}
	// assistant 不应出现
	for _, m := range result {
		if m.Role == llm.RoleAssistant {
			t.Error("assistant message should be excluded from fork context")
		}
	}
}

func TestBuildForkMessages_NoAssistantInContext(t *testing.T) {
	// 最后一条 assistant 含 tool_calls → fork 不应看到(避免 agent 占位符混淆)
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "let me check", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "agent", Arguments: `{"description":"x","prompt":"y"}`},
			{ID: "call_2", Name: "read", Arguments: `{"file_path":"/f.go"}`},
		}},
	}
	result := buildForkMessages(msgs, "fork-desc", "do something")
	// sys + user + fork directive = 3(assistant + tool_calls 全部排除)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
	if result[2].Role != llm.RoleUser || !strings.Contains(result[2].Content, forkBoilerplateTag) {
		t.Error("last message should be fork directive")
	}
	// 不应有任何 tool 角色消息和 assistant 消息
	for _, m := range result {
		if m.Role == llm.RoleTool || m.Role == llm.RoleAssistant {
			t.Errorf("unexpected %s message in fork context", m.Role)
		}
	}
}
func TestBuildForkMessages_AgentToolCallPreserved(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "let me check", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "agent", Arguments: `{"description":"x","prompt":"y"}`},
			{ID: "call_2", Name: "bash", Arguments: `{"command":"ls"}`},
		}},
		{Role: llm.RoleTool, Content: "agent result", ToolCallID: "call_1"},
		{Role: llm.RoleTool, Content: "file list", ToolCallID: "call_2"},
	}
	result := buildForkMessages(msgs, "fork-desc", "do something")
	// sys + user + user(fork directive) = 3(assistant + tool_calls 全部排除)
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
	// fork directive 作为最后一条 user 消息
	if result[2].Role != llm.RoleUser || !strings.Contains(result[2].Content, forkBoilerplateTag) {
		t.Error("last message should be fork directive")
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
		return
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
	// 构造包含 fork-boilerplate 的父消息历史(模拟已在 fork 中的场景)
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
	// 正常父 agent(无 fork-boilerplate)可以自由 fork
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

// REGRESSION: Explore agents use exploreMaxSteps (25), not coldMaxSteps (50).
// This is verified indirectly: the stub LLM always returns "ok" immediately,
// so the agent completes in 1 turn regardless of limit. The limit is a safety
// ceiling, not a minimum. We verify the constant value is lower.
func TestExploreMaxSteps_LowerThanCold(t *testing.T) {
	if exploreMaxSteps >= coldMaxSteps {
		t.Errorf("exploreMaxSteps (%d) should be lower than coldMaxSteps (%d)", exploreMaxSteps, coldMaxSteps)
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
	a := &AgentTool{}
	r := a.buildColdRegistry(coldDisallowed)
	names := toolNames(r)
	for _, name := range []string{"read", "web_fetch", "bash_subagent"} {
		if !contains(names, name) {
			t.Errorf("verification registry missing %q", name)
		}
	}
	for _, name := range []string{"write", "edit"} {
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
	if !strings.Contains(got, "read") {
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

	a := &AgentTool{}
	r := a.buildColdRegistry(coldDisallowed)

	got := formatSubagentEnvironment(ctx, r)
	// Explore 只有 read, web_fetch, bash_subagent
	if !strings.Contains(got, "  read") {
		t.Error("should list read")
	}
	if !strings.Contains(got, "web_fetch") {
		t.Error("should list web_fetch")
	}
	// 不应包含 write 和 edit
	if strings.Contains(got, "  write") {
		t.Error("Explore should NOT list write")
	}
	if strings.Contains(got, "  edit") {
		t.Error("Explore should NOT list edit")
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
		ch := make(chan agentloop.StepEvent, 100)
		go func() {
			for i := 0; i < 50; i++ {
				ch <- agentloop.StreamDelta{ContentDelta: "some text content"}
			}
			ch <- agentloop.ToolCallStart{ToolCallName: "read", Arguments: `{"file_path":"/path/to/file.go"}`}
			ch <- agentloop.ToolCallResult{ToolCallName: "read", Result: "content", DurationMs: 42}
			ch <- agentloop.TurnDone{Step: 3}
			close(ch)
		}()
		_, _, _, _, _, _, _, _ = forwardEvents(ctx, ch, nil, "")
		cancel()
	}
}

// ---------------------------------------------------------------------------
// Model resolution tests (pro/flash enum -> Default{Sub}Model on AgentTool)
// ---------------------------------------------------------------------------

type mockSettingsProvider struct {
	model string
	err   error
}

func (m *mockSettingsProvider) LoadLLM() (*llm.LLMSettings, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.LLMSettings{Model: m.model}, nil
}

func TestResolveModel_Pro(t *testing.T) {
	a := &AgentTool{
		Settings:     &mockSettingsProvider{model: "gpt-4o"},
		DefaultModel: "gpt-4o",
	}
	if got := a.resolveModel("pro"); got != "gpt-4o" {
		t.Errorf("resolveModel('pro') = %q, want 'gpt-4o'", got)
	}
}

func TestResolveModel_FlashFallsBackToDefault(t *testing.T) {
	// DefaultSubModel removed — flash now uses DefaultModel.
	a := &AgentTool{
		DefaultModel: "gpt-4o",
	}
	if got := a.resolveModel("flash"); got != "gpt-4o" {
		t.Errorf("resolveModel('flash') = %q, want 'gpt-4o' (fallback to DefaultModel)", got)
	}
	if got := a.resolveModel("pro"); got != "gpt-4o" {
		t.Errorf("resolveModel('pro') = %q, want 'gpt-4o'", got)
	}
}

func TestResolveModel_FallbackToDefaultModel(t *testing.T) {
	a := &AgentTool{
		Settings:     nil,
		DefaultModel: "fallback-pro",
	}
	if got := a.resolveModel("pro"); got != "fallback-pro" {
		t.Errorf("resolveModel('pro') with nil settings = %q, want 'fallback-pro'", got)
	}
	if got := a.resolveModel("flash"); got != "fallback-pro" {
		t.Errorf("resolveModel('flash') = %q, want 'fallback-pro' (uses DefaultModel)", got)
	}
}

func TestResolveModel_SettingsError(t *testing.T) {
	a := &AgentTool{
		Settings:     &mockSettingsProvider{err: fmt.Errorf("disk error")},
		DefaultModel: "fallback-pro",
	}
	if got := a.resolveModel("pro"); got != "fallback-pro" {
		t.Errorf("resolveModel('pro') on error = %q, want 'fallback-pro'", got)
	}
}
