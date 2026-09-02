package subagent

import (
	"fmt"
	"context"
	"encoding/json"
	"errors"
	"reflect"
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
	// 尾部是纯文本 assistant(无 tool_calls):前缀零改写,原样保留 + directive。
	// 缓存要求:不改写中段任何消息;纯文本 assistant 保留不影响消息序列合法性。
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi there"},
	}
	result := buildForkMessages(msgs, "test", "do it")
	if len(result) != 4 {
		t.Fatalf("expected 4 messages (prefix preserved + directive), got %d", len(result))
	}
	if !reflect.DeepEqual(result[:3], msgs) {
		t.Errorf("prefix must be preserved byte-for-byte, got %+v", result[:3])
	}
	if result[3].Role != llm.RoleUser || !strings.Contains(result[3].Content, forkBoilerplateTag) {
		t.Errorf("fork directive should be last user message with boilerplate: %+v", result[3])
	}
}

func TestBuildForkMessages_OpenToolCallRoundTruncated(t *testing.T) {
	// 尾部 assistant 含 tool_calls 且无结果(发起 fork 的开放轮次)→ 整体截断,
	// 不残留孤儿 tool_calls(627e50c 回归保护);前缀 = sys+user 原样。
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "let me check", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "agent", Arguments: `{"description":"x","prompt":"y"}`},
			{ID: "call_2", Name: "read", Arguments: `{"file_path":"/f.go"}`},
		}},
	}
	result := buildForkMessages(msgs, "fork-desc", "do something")
	if len(result) != 3 {
		t.Fatalf("expected 3 messages (open round truncated), got %d", len(result))
	}
	if !reflect.DeepEqual(result[:2], msgs[:2]) {
		t.Errorf("prefix must be preserved byte-for-byte, got %+v", result[:2])
	}
	if result[2].Role != llm.RoleUser || !strings.Contains(result[2].Content, forkBoilerplateTag) {
		t.Error("last message should be fork directive")
	}
	// 无孤儿 tool_calls:任何保留的 assistant 不得携带 ToolCalls
	for i, m := range result {
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0 {
			t.Errorf("message %d has orphaned tool_calls", i)
		}
	}
}

func TestBuildForkMessages_PrefixPreservedWithToolMessages(t *testing.T) {
	// 核心缓存断言:中段 tool 消息与 assistant tool_calls 原样保留(tool 角色
	// 不再被删除),仅截断到发起 fork 的最后一条 assistant 之前 →
	// 剩余前缀 == 父上一请求负载 P_k,可与父缓存线逐字节对齐。
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "inspect repo"},
		{Role: llm.RoleAssistant, Content: "let me look", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "bash", Arguments: `{"command":"ls"}`},
		}},
		{Role: llm.RoleTool, Content: "main.go", ToolCallID: "c1"},
		{Role: llm.RoleUser, Content: "summarize findings"},
		{Role: llm.RoleAssistant, Content: "found it", ToolCalls: []llm.ToolCall{
			{ID: "c2", Name: "agent", Arguments: `{"description":"x","prompt":"y"}`},
			{ID: "c3", Name: "read", Arguments: `{"file_path":"main.go"}`},
		}},
	}
	result := buildForkMessages(msgs, "fork-desc", "do something")
	// 配对轮次 assistant(索引 2,其后 tool 结果完整)必须原样保留 ToolCalls,
	// 防御层只清理孤儿轮次——误剥会让首请求与父缓存线从首个已完成轮次分叉。
	if len(result[2].ToolCalls) != 1 {
		t.Fatalf("paired round assistant tool_calls must be preserved, got %+v", result[2])
	}
	// 父消息历史不得被就地篡改(buildForkMessages 曾复用输入 backing 过滤,
	// 把 state.Messages 中配对 assistant 的 ToolCalls 原地置 nil)。
	if len(msgs[2].ToolCalls) != 1 {
		t.Fatalf("parent history mutated: msgs[2].ToolCalls = %+v", msgs[2].ToolCalls)
	}
	// 截断到最后一条含 tool_calls 的 assistant 之前(其兄弟工具结果 c3 未
	// 出现在历史中——fork 在工具执行阶段快照,结果尚未 append)。
	wantPrefix := msgs[:5]
	if len(result) != len(wantPrefix)+1 {
		t.Fatalf("expected %d messages, got %d", len(wantPrefix)+1, len(result))
	}
	if !reflect.DeepEqual(result[:len(wantPrefix)], wantPrefix) {
		t.Errorf("prefix must be preserved byte-for-byte (incl. tool role + tool_calls), got:\n%+v", result[:len(wantPrefix)])
	}
	if result[len(wantPrefix)].Role != llm.RoleUser || !strings.Contains(result[len(wantPrefix)].Content, forkBoilerplateTag) {
		t.Error("last message should be fork directive")
	}
}

func TestBuildForkMessages_CompactedHistoryNoToolCalls(t *testing.T) {
	// 父历史被压缩改写(无 assistant 含 tool_calls)→ 无截断点,前缀全保留。
	// 该历史本身合法(压缩摘要),追加 directive 后序列有效。
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "earlier work"},
		{Role: llm.RoleAssistant, Content: "done, see summary"},
		{Role: llm.RoleUser, Content: "[summary] 完成了初步调研"},
	}
	result := buildForkMessages(msgs, "fork-desc", "do something")
	if len(result) != 5 {
		t.Fatalf("expected 5 messages (all preserved + directive), got %d", len(result))
	}
	if !reflect.DeepEqual(result[:4], msgs) {
		t.Errorf("prefix must be preserved byte-for-byte, got %+v", result[:4])
	}
}

func TestBuildForkMessages_NilParentFallback(t *testing.T) {
	r := buildForkMessages(nil, "desc", "prompt")
	if len(r) != 2 || r[0].Role != llm.RoleSystem || r[1].Role != llm.RoleUser {
		t.Fatalf("nil parent must fall back to clean messages, got %+v", r)
	}
	r2 := buildForkMessages([]llm.Message{}, "desc", "prompt")
	if len(r2) != 2 {
		t.Fatalf("empty parent must fall back to clean messages, got %+v", r2)
	}
}

// TestRegression_OrphanRoundsStrippedFromForkPrefix 覆盖 627e50c 回归保护在新
// 前缀零改写策略下的正确形态:防御层只清理孤儿轮次(assistant 声明了 tool_calls
// 但结果不在历史中,如压缩摘要拼接),配对的已完成轮次必须原样保留——无条件
// 剥离曾导致:① 前缀从首个已完成轮次起与父缓存线分叉,命中归零;② 原地过滤
// 篡改父 state.Messages(共享 backing);③ tool 消息失去前导声明,协议违规。
func TestRegression_OrphanRoundsStrippedFromForkPrefix(t *testing.T) {
	// 中段半配对孤儿:assistant 声明 c1+c2,历史中只有 c1 的结果(压缩损伤),
	// 其后还有完整配对轮次 → 截断保留孤儿在前缀内 → 保留文本、剥离 ToolCalls、
	// 跳过其 tool 段(t1 引用已剥离声明,不得残留)。
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "task 1"},
		{Role: llm.RoleAssistant, Content: "half", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "bash", Arguments: `{"command":"ls"}`},
			{ID: "c2", Name: "read", Arguments: `{"file_path":"a.go"}`},
		}},
		{Role: llm.RoleTool, Content: "out", ToolCallID: "c1"},
		{Role: llm.RoleUser, Content: "task 2"},
		{Role: llm.RoleAssistant, Content: "final", ToolCalls: []llm.ToolCall{
			{ID: "c3", Name: "bash", Arguments: `{"command":"git status"}`},
		}},
		{Role: llm.RoleTool, Content: "fresh", ToolCallID: "c3"},
	}
	result := buildForkMessages(msgs, "fork-desc", "do something")
	if len(result) != 5 {
		t.Fatalf("expected 4 messages (orphan calls stripped, tool seg skipped), got %d: %+v", len(result), result)
	}
	if result[1].Content != "task 1" || result[2].Content != "half" || result[3].Content != "task 2" {
		t.Fatalf("expected [task1, half, task2] in sequence, got %+v", result)
	}
	if result[2].Role != llm.RoleAssistant || len(result[2].ToolCalls) != 0 {
		t.Errorf("orphan assistant must keep text without ToolCalls, got %+v", result[2])
	}
	if len(msgs[2].ToolCalls) != 2 {
		t.Errorf("parent history mutated: msgs[2].ToolCalls = %+v", msgs[2].ToolCalls)
	}

	// 纯 tool_calls 孤儿(无文本,结果完全缺失)→ 整条删除;不阻塞其后
	// 配对轮次的保留。
	msgs2 := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "task 1"},
		{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "bash", Arguments: `{"command":"ls"}`},
		}},
		{Role: llm.RoleUser, Content: "task 2"},
		{Role: llm.RoleAssistant, Content: "paired", ToolCalls: []llm.ToolCall{
			{ID: "c2", Name: "read", Arguments: `{"file_path":"a.go"}`},
		}},
		{Role: llm.RoleTool, Content: "fresh", ToolCallID: "c2"},
	}
	result2 := buildForkMessages(msgs2, "fork-desc", "do something")
	if len(result2) != 4 {
		t.Fatalf("expected 4 messages (pure orphan round dropped), got %d: %+v", len(result2), result2)
	}
	if result2[1].Content != "task 1" || result2[2].Content != "task 2" {
		t.Errorf("orphan round must be dropped whole, got %+v", result2)
	}
	if len(msgs2[4].ToolCalls) != 1 {
		t.Errorf("parent history mutated: msgs2[4].ToolCalls = %+v", msgs2[4].ToolCalls)
	}

	// 悬空 tool 段(user 后无前导声明的 tool 结果,拼接残留)→ 整段删除;
	// 其后配对轮次不受影响。
	msgs3 := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "task 1"},
		{Role: llm.RoleTool, Content: "dangling", ToolCallID: "ghost1"},
		{Role: llm.RoleTool, Content: "dangling2", ToolCallID: "ghost2"},
		{Role: llm.RoleUser, Content: "task 2"},
		{Role: llm.RoleAssistant, Content: "paired", ToolCalls: []llm.ToolCall{
			{ID: "c2", Name: "read", Arguments: `{"file_path":"a.go"}`},
		}},
		{Role: llm.RoleTool, Content: "fresh", ToolCallID: "c2"},
	}
	result3 := buildForkMessages(msgs3, "fork-desc", "do something")
	if len(result3) != 4 {
		t.Fatalf("expected 4 messages (dangling tool segment dropped), got %d: %+v", len(result3), result3)
	}
	if result3[1].Content != "task 1" || result3[2].Content != "task 2" {
		t.Errorf("dangling segment must be dropped whole, got %+v", result3)
	}
	for i, m := range result3 {
		if m.Role == llm.RoleTool {
			t.Errorf("result %d must not retain dangling tool message: %+v", i, m)
		}
	}
}

// TestAlignForkRegistry_ParentToolsCovered 验证 ToolsOverride(父 tools)中 fork
// 未注册的工具在 align 后均可分发:bash 别名到沙箱 shell(可真实执行),其余
// 注册显式报错 stub——替代 loop 层静默剥离,模型能收到反馈自纠。
func TestAlignForkRegistry_ParentToolsCovered(t *testing.T) {
	a := &AgentTool{}
	reg := a.buildForkRegistry()
	parent := []llm.ToolSpec{
		{Name: "read", Description: "read files"}, // 已注册 → 不动
		{Name: "bash", Description: "run commands"},
		{Name: "agent", Description: "spawn subagent"},
		{Name: "todo_create", Description: "create todo"},
		{Name: "mcp__github__list_issues", Description: "mcp tool"},
	}
	alignForkRegistry(reg, parent)

	// 差集工具全部可分发(不再被 loop 静默剥离)
	for _, name := range []string{"bash", "agent", "todo_create", "mcp__github__list_issues"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("parent tool %q must be registered after align", name)
		}
	}
	// bash 别名真实执行(委托 fork 沙箱 shell),而非报不可用
	res, err := reg.Execute(context.Background(), "bash", json.RawMessage(`{"command":"echo hi"}`))
	if err != nil {
		t.Fatalf("bash alias execute error: %v", err)
	}
	if res.Error != nil || !strings.Contains(res.Content, "hi") {
		t.Fatalf("bash alias must execute through sandbox shell, got error=%+v content=%q", res.Error, res.Content)
	}
	// 父独占工具 → 显式不可用错误(可恢复),内容含替代指引
	res, err = reg.Execute(context.Background(), "agent", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("stub Execute must not return Go error: %v", err)
	}
	if res.Error == nil || res.Error.Kind != "tool_unavailable_in_fork" {
		t.Fatalf("agent stub must return unavailable error, got %+v", res)
	}
	if !strings.Contains(res.Content, "不可调用") {
		t.Errorf("stub content should guide model to available tools, got %q", res.Content)
	}
	// 已注册工具不受影响:read 仍是原实现(分发成功即不被 stub 遮蔽)
	if res, err := reg.Execute(context.Background(), "read", json.RawMessage(`{"file_path":"nonexistent-file-xyz"}`)); err == nil && res.Error == nil {
		t.Errorf("read must dispatch to the real tool (expect file-not-found error)")
	}
}

// forkBudgetFixture 构造多轮工具型历史(每轮 = assistant(tool_calls) + tool 结果)。
func forkBudgetFixture() []llm.Message {
	big := strings.Repeat("x", 400)
	return []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "task 1"},
		{Role: llm.RoleAssistant, Content: "running", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "bash", Arguments: `{"command":"ls"}`},
		}},
		{Role: llm.RoleTool, Content: big, ToolCallID: "c1"},
		{Role: llm.RoleUser, Content: "task 2"},
		{Role: llm.RoleAssistant, Content: "running again", ToolCalls: []llm.ToolCall{
			{ID: "c2", Name: "read", Arguments: `{"file_path":"a.go"}`},
		}},
		{Role: llm.RoleTool, Content: big, ToolCallID: "c2"},
		{Role: llm.RoleUser, Content: "task 3"},
	}
}

func hasOrphanToolCalls(msgs []llm.Message) bool {
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

func TestTrimForkContextToBudget_UnderBudgetUnchanged(t *testing.T) {
	msgs := forkBudgetFixture()
	out, ok := trimForkContextToBudget(msgs, estimateMessagesTokens(msgs), forkMinKeepMessages)
	if !ok {
		t.Fatal("budget equal to full estimate must succeed")
	}
	if !reflect.DeepEqual(out, msgs) {
		t.Error("messages must be unchanged when within budget")
	}
}

func TestTrimForkContextToBudget_TailRoundTruncation(t *testing.T) {
	// 预算只够保留到 task 2 的 user:尾部 round(u3 / a2+t2)必须整轮丢弃,
	// 绝不拆开 assistant(tool_calls) 与其 tool 结果;剩余前缀逐字节不变。
	msgs := forkBudgetFixture()
	budget := estimateMessagesTokens(msgs[:5]) // sys..u2(含 round1 完整)
	out, ok := trimForkContextToBudget(msgs, budget, forkMinKeepMessages)
	if !ok {
		t.Fatal("expected successful trim")
	}
	if len(out) != 5 {
		t.Fatalf("expected prefix len 5 (u3+a2+t2 dropped whole), got %d", len(out))
	}
	if !reflect.DeepEqual(out, msgs[:5]) {
		t.Errorf("prefix must be preserved byte-for-byte: %+v", out)
	}
}

func TestTrimForkContextToBudget_NeverSplitsToolPair(t *testing.T) {
	// 预算卡在 round1 的 assistant 与 tool 结果之间:允许的截断点要么在
	// 整轮之前(丢弃 a1+t1),要么在整轮之后;绝不产出孤儿 tool_calls。
	msgs := forkBudgetFixture()
	midRoundBudget := estimateMessagesTokens(msgs[:3]) // sys..a1(不含 t1)
	for _, budget := range []int{midRoundBudget, midRoundBudget - 1} {
		out, ok := trimForkContextToBudget(msgs, budget, forkMinKeepMessages)
		if !ok {
			t.Fatal("expected successful trim")
		}
		if hasOrphanToolCalls(out) {
			t.Fatalf("trim must never split tool pair, got orphan tool_calls in %+v", out)
		}
		if len(out) != 2 {
			t.Fatalf("budget below round1+t1 must cut to sys+u1 (len 2), got len %d", len(out))
		}
		if !reflect.DeepEqual(out, msgs[:2]) {
			t.Errorf("prefix must be preserved byte-for-byte: %+v", out)
		}
	}
}

func TestTrimForkContextToBudget_TooSmallBudget(t *testing.T) {
	msgs := forkBudgetFixture()
	out, ok := trimForkContextToBudget(msgs, 1, forkMinKeepMessages)
	if ok {
		t.Fatal("budget 1 must fail (cannot fit minKeep)")
	}
	if len(out) != forkMinKeepMessages {
		t.Errorf("failed trim must return msgs[:minKeep], got len %d", len(out))
	}
}

func TestForkRoundStart(t *testing.T) {
	msgs := forkBudgetFixture()
	// 尾部 user → 起点即它自己
	if got := forkRoundStart(msgs, len(msgs)); got != 7 {
		t.Errorf("tail user round start = %d, want 7", got)
	}
	// 尾部 tool 结果批 → 回溯到其 assistant(tool_calls) 起点(索引 5)
	trunc := msgs[:7]
	if got := forkRoundStart(trunc, len(trunc)); got != 5 {
		t.Errorf("tool batch round start = %d, want 5", got)
	}
	// 尾部 assistant(tool_calls) → 起点即它自己(整轮 = 它 + 后续结果,一并丢弃)
	if got := forkRoundStart(msgs[:6], 6); got != 5 {
		t.Errorf("open tool round start = %d, want 5", got)
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
