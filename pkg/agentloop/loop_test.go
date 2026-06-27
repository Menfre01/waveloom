package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"waveloom/pkg/compaction"
	"waveloom/pkg/llm"
	"waveloom/pkg/permission"
	"waveloom/pkg/tool"
)

// ============================================================================
// Mock LLM Client
// ============================================================================

// mockLLMClient 实现 llm.Client，返回预编程的响应序列。
type mockLLMClient struct {
	mu           sync.Mutex
	responses    []*llm.Response
	errors       []error     // SendMessage / SendMessageStream 首帧错误
	streamErrors []error     // 流中错误（ev.Err != nil），nil 表示无流错误
	callCount    int
}

func (m *mockLLMClient) SendMessage(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (*llm.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := m.callCount
	m.callCount++

	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}
	if idx < len(m.responses) {
		return m.responses[idx], nil
	}
	// 默认：返回空文本（无工具调用）
	return &llm.Response{Content: "done"}, nil
}

func (m *mockLLMClient) SendMessageStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (<-chan llm.StreamingEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := m.callCount
	m.callCount++

	if idx < len(m.errors) && m.errors[idx] != nil {
		return nil, m.errors[idx]
	}

	ch := make(chan llm.StreamingEvent, 16)
	go func() {
		defer close(ch)

		// 流中错误注入（模拟网络中断）
		if idx < len(m.streamErrors) && m.streamErrors[idx] != nil {
			ch <- llm.StreamingEvent{Err: m.streamErrors[idx]}
			return
		}

		var resp *llm.Response
		if idx < len(m.responses) {
			resp = m.responses[idx]
		} else {
			resp = &llm.Response{Content: "done"}
		}

		if resp.Content != "" {
			ch <- llm.StreamingEvent{Delta: resp.Content}
		}
		if resp.ReasoningContent != "" {
			ch <- llm.StreamingEvent{ReasoningDelta: resp.ReasoningContent}
		}

		finishReason := resp.FinishReason
		if finishReason == "" {
			if len(resp.ToolCalls) > 0 {
				finishReason = "tool_calls"
			} else {
				finishReason = "stop"
			}
		}
		ch <- llm.StreamingEvent{
			Done:         true,
			FinishReason: finishReason,
			ToolCalls:    resp.ToolCalls,
			Usage:        resp.Usage,
		}
	}()
	return ch, nil
}

func (m *mockLLMClient) GetBalance(ctx context.Context) (*llm.BalanceInfo, error) {
	return nil, nil
}

func (m *mockLLMClient) SupportsBalance() bool { return false }

// drainEvents 消费 channel 直到关闭，返回最后一个 LoopDone 事件。
func drainEvents(ch <-chan TurnEvent) LoopDone {
	var last LoopDone
	for ev := range ch {
		if done, ok := ev.(LoopDone); ok {
			last = done
		}
	}
	return last
}

// ============================================================================
// Mock Tool（基础版）
// ============================================================================

// mockTool 直接实现 tool.Tool 接口，用于大多数测试场景。
type mockTool struct {
	name           string
	desc           string
	schema         json.RawMessage
	concurrentSafe bool
	result         *tool.ToolResult // 执行返回值
	execErr        error            // Execute 直接返回的 error（模拟 registry 级错误）
	execCount      *int32           // 执行计数（并发安全）
	execOrder      *[]string        // 执行顺序记录（需外部加锁）
	orderMu        *sync.Mutex      // execOrder 的锁
	execDelay      time.Duration    // 执行前等待
}

func (m *mockTool) Name() string             { return m.name }
func (m *mockTool) Description() string      { return m.desc }
func (m *mockTool) Schema() json.RawMessage  { return m.schema }
func (m *mockTool) ConcurrentSafe() bool     { return m.concurrentSafe }

func (m *mockTool) Execute(ctx context.Context, raw json.RawMessage) (*tool.ToolResult, error) {
	if m.execCount != nil {
		atomic.AddInt32(m.execCount, 1)
	}
	if m.execDelay > 0 {
		select {
		case <-time.After(m.execDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if m.execErr != nil {
		return nil, m.execErr
	}
	if m.execOrder != nil && m.orderMu != nil {
		m.orderMu.Lock()
		*m.execOrder = append(*m.execOrder, m.name)
		m.orderMu.Unlock()
	}
	return m.result, nil
}

// ============================================================================
// Barrier Mock Tool（用于并发测试）
// ============================================================================

// barrierTool 通过 barrier 机制验证并发执行。
// Execute 开始时调用 startBarrier.Done()，然后阻塞在 proceedCh 上。
// 这样测试可以验证所有并发工具同一时间进入了 Execute。
type barrierTool struct {
	name           string
	schema         json.RawMessage
	concurrentSafe bool
	result         *tool.ToolResult
	startBarrier   *sync.WaitGroup // 所有工具启动后 Done
	proceedCh      <-chan struct{} // 关闭后工具继续执行
	execOrder      *[]string
	orderMu        *sync.Mutex
}

func (b *barrierTool) Name() string             { return b.name }
func (b *barrierTool) Description() string      { return "barrier tool: " + b.name }
func (b *barrierTool) Schema() json.RawMessage  { return b.schema }
func (b *barrierTool) ConcurrentSafe() bool     { return b.concurrentSafe }

func (b *barrierTool) Execute(ctx context.Context, raw json.RawMessage) (*tool.ToolResult, error) {
	b.startBarrier.Done()
	<-b.proceedCh
	if b.execOrder != nil && b.orderMu != nil {
		b.orderMu.Lock()
		*b.execOrder = append(*b.execOrder, b.name)
		b.orderMu.Unlock()
	}
	return b.result, nil
}

// ============================================================================
// 测试辅助函数
// ============================================================================

// newMockTool 创建一个返回成功结果的 mock 工具。
func newSuccessTool(name string, concurrentSafe bool, content string) *mockTool {
	return &mockTool{
		name:           name,
		desc:           "mock tool: " + name,
		schema:         json.RawMessage(`{"type":"object","properties":{}}`),
		concurrentSafe: concurrentSafe,
		result:         &tool.ToolResult{Content: content},
	}
}

// newErrorTool 创建一个返回错误的 mock 工具。
func newErrorTool(name string, concurrentSafe bool, toolErr *tool.ToolError) *mockTool {
	return &mockTool{
		name:           name,
		desc:           "mock tool: " + name,
		schema:         json.RawMessage(`{"type":"object","properties":{}}`),
		concurrentSafe: concurrentSafe,
		result: &tool.ToolResult{
			Error: toolErr,
		},
	}
}

// newTestRegistry 用给定的工具创建测试 Registry。
func newTestRegistry(tools ...tool.Tool) tool.Registry {
	r := tool.NewRegistry()
	for _, t := range tools {
		r.Register(t)
	}
	return r
}

// makeToolCall 创建单个 llm.ToolCall。
func makeToolCall(id, name, args string) llm.ToolCall {
	return llm.ToolCall{
		ID:        id,
		Name:      name,
		Arguments: args,
	}
}

// toolCallIDs 返回消息中 tool_calls 的 ID 列表，用于测试诊断。
func toolCallIDs(msg llm.Message) []string {
	ids := make([]string, len(msg.ToolCalls))
	for i, tc := range msg.ToolCalls {
		ids[i] = tc.ID
	}
	return ids
}

// makeTextResponse 创建纯文本 LLM 响应。
func makeTextResponse(content string) *llm.Response {
	return &llm.Response{Content: content}
}

// makeToolCallResponse 创建含工具调用的 LLM 响应。
func makeToolCallResponse(content string, calls ...llm.ToolCall) *llm.Response {
	return &llm.Response{
		Content:   content,
		ToolCalls: calls,
	}
}

func makeToolCallResponseWithUsage(content string, promptTokens int, calls ...llm.ToolCall) *llm.Response {
	return &llm.Response{
		Content:   content,
		ToolCalls: calls,
		Usage:     &llm.UsageInfo{PromptTokens: promptTokens},
	}
}

// ============================================================================
// Mock Guard + UserResponder
// ============================================================================

// mockGuard 实现 permission.Guard，返回预编程的决策结果。
type mockGuard struct {
	mu            sync.Mutex
	results       map[string]permission.DecisionResult // key = toolName
	defaultResult permission.DecisionResult
	rules         []permission.RuleEntry
	addRuleCalls  int
	persistCalls  int
	sessionAllowCalls int
	sessionDenyCalls  int
}

func (g *mockGuard) Check(ctx context.Context, toolName string, input json.RawMessage) permission.DecisionResult {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.results != nil {
		if r, ok := g.results[toolName]; ok {
			return r
		}
	}
	if g.defaultResult.Decision != "" {
		return g.defaultResult
	}
	return permission.DecisionResult{Decision: permission.DecisionAllow, Reason: permission.ReasonDefault}
}

func (g *mockGuard) AddRule(rule permission.Rule, scope permission.RuleScope) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.addRuleCalls++
	g.rules = append(g.rules, permission.RuleEntry{Rule: rule, Source: permission.SourceSession, Scope: scope})
	return nil
}

func (g *mockGuard) RemoveRule(rule permission.Rule, scope permission.RuleScope) error { return nil }

func (g *mockGuard) ListRules() []permission.RuleEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rules
}

func (g *mockGuard) PersistRule(rule permission.Rule) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.persistCalls++
	return nil
}

func (g *mockGuard) SessionAllow(toolName string, input json.RawMessage) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sessionAllowCalls++
}

func (g *mockGuard) SessionDeny(toolName string, input json.RawMessage) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sessionDenyCalls++
}

func (g *mockGuard) ClearSession() {}

func (g *mockGuard) SessionMemoryLen() int { return 0 }

// mockUserResponder 实现 permission.UserResponder，返回预编程的用户选择。
type mockUserResponder struct {
	mu       sync.Mutex
	choices  map[string]permission.UserChoice
	askCount int
}

func (u *mockUserResponder) AskUser(ctx context.Context, toolName string, input json.RawMessage, result permission.DecisionResult) permission.UserChoice {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.askCount++
	if u.choices != nil {
		if c, ok := u.choices[toolName]; ok {
			return c
		}
	}
	return permission.UserChoice{Decision: permission.DecisionDeny}
}

// ============================================================================
// 1. 基础流程测试
// ============================================================================

func TestRunCompletesImmediately(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeTextResponse("Hello, I can help with that."),
		},
	}
	registry := newTestRegistry()
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "say hello"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
	if finalEv.Turn != 1 {
		t.Errorf("expected 1 turn (1 LLM call), got %d", finalEv.Turn)
	}
	if len(finalEv.Messages) != 2 {
		t.Errorf("expected 2 messages (user + assistant), got %d", len(finalEv.Messages))
	}
	// 验证 assistant 消息
	lastMsg := finalEv.Messages[len(finalEv.Messages)-1]
	if lastMsg.Role != llm.RoleAssistant {
		t.Errorf("expected assistant role, got %s", lastMsg.Role)
	}
	if lastMsg.Content != "Hello, I can help with that." {
		t.Errorf("unexpected content: %s", lastMsg.Content)
	}
}

func TestRunSingleToolCall(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("", makeToolCall("tc1", "read_file", `{"file_path":"/tmp/a.txt"}`)),
			makeTextResponse("File contents: hello"),
		},
	}
	readTool := newSuccessTool("read_file", true, "hello")
	registry := newTestRegistry(readTool)
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "read /tmp/a.txt"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
	if finalEv.Turn != 2 {
		t.Errorf("expected 2 turns (2 LLM calls), got %d", finalEv.Turn)
	}
	// 消息序列: user → assistant(tool call) → tool(result) → assistant(text)
	if len(finalEv.Messages) != 4 {
		t.Errorf("expected 4 messages, got %d", len(finalEv.Messages))
	}
	// 验证 tool 消息
	toolMsg := finalEv.Messages[2]
	if toolMsg.Role != llm.RoleTool {
		t.Errorf("expected tool role, got %s", toolMsg.Role)
	}
	if toolMsg.Content != "hello" {
		t.Errorf("expected tool result 'hello', got %s", toolMsg.Content)
	}
	if toolMsg.ToolCallID != "tc1" {
		t.Errorf("expected ToolCallID 'tc1', got %s", toolMsg.ToolCallID)
	}
}

func TestRunMultipleToolCalls(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("",
				makeToolCall("tc1", "read_file", `{"file_path":"/tmp/a.txt"}`),
				makeToolCall("tc2", "read_file", `{"file_path":"/tmp/b.txt"}`),
				makeToolCall("tc3", "grep", `{"pattern":"func"}`),
			),
			makeTextResponse("Found 3 functions"),
		},
	}
	readTool := newSuccessTool("read_file", true, "content-a")
	grepTool := newSuccessTool("grep", true, "3 matches")
	registry := newTestRegistry(readTool, grepTool)
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "find functions"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
	if finalEv.Turn != 2 {
		t.Errorf("expected 2 turns (2 LLM calls), got %d", finalEv.Turn)
	}
	// 消息: user → assistant(3 tool calls) → tool(tc1) → tool(tc2) → tool(tc3) → assistant(text)
	if len(finalEv.Messages) != 6 {
		t.Errorf("expected 6 messages, got %d", len(finalEv.Messages))
	}
	// 验证 tool 消息按原始顺序
	for i, expected := range []struct{ id, content string }{
		{"tc1", "content-a"},
		{"tc2", "content-a"},
		{"tc3", "3 matches"},
	} {
		msg := finalEv.Messages[2+i]
		if msg.Role != llm.RoleTool {
			t.Errorf("msg %d: expected tool role, got %s", i, msg.Role)
		}
		if msg.ToolCallID != expected.id {
			t.Errorf("msg %d: expected ToolCallID %s, got %s", i, expected.id, msg.ToolCallID)
		}
		if msg.Content != expected.content {
			t.Errorf("msg %d: expected content %s, got %s", i, expected.content, msg.Content)
		}
	}
}

func TestRunMultipleTurns(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("", makeToolCall("tc1", "read_file", `{"file_path":"/tmp/a.txt"}`)),
			makeToolCallResponse("", makeToolCall("tc2", "grep", `{"pattern":"func"}`)),
			makeTextResponse("Final answer"),
		},
	}
	readTool := newSuccessTool("read_file", true, "hello")
	grepTool := newSuccessTool("grep", true, "2 matches")
	registry := newTestRegistry(readTool, grepTool)
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "analyze code"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
	if finalEv.Turn != 3 {
		t.Errorf("expected 3 turns (3 LLM calls), got %d", finalEv.Turn)
	}
	// 消息: user → asst(tc1) → tool(tc1) → asst(tc2) → tool(tc2) → asst(text) = 6
	if len(finalEv.Messages) != 6 {
		t.Errorf("expected 6 messages, got %d", len(finalEv.Messages))
	}
}

// ============================================================================
// 2. 终止条件测试
// ============================================================================

func TestRunMaxTurns(t *testing.T) {
	// 每轮都返回 tool call，设置 MaxTurns=2
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("", makeToolCall("tc1", "read_file", `{"file_path":"/tmp/a.txt"}`)),
			makeToolCallResponse("", makeToolCall("tc2", "read_file", `{"file_path":"/tmp/b.txt"}`)),
			makeToolCallResponse("", makeToolCall("tc3", "read_file", `{"file_path":"/tmp/c.txt"}`)),
		},
	}
	readTool := newSuccessTool("read_file", true, "ok")
	registry := newTestRegistry(readTool)
	loop := New(client, registry, Config{MaxTurns: 2, })

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "read files"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonMaxTurns {
		t.Errorf("expected ReasonMaxTurns, got %s", finalEv.Reason)
	}
	if finalEv.Turn != 2 {
		t.Errorf("expected 2 turns, got %d", finalEv.Turn)
	}
}

func TestRunZeroMaxTurns(t *testing.T) {
	// MaxTurns=0 表示无限制，LLM 无工具调用时正常完成
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeTextResponse("Here is the answer."),
		},
	}
	registry := newTestRegistry()
	loop := New(client, registry, Config{MaxTurns: 0, })

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
	if finalEv.Turn != 1 {
		t.Errorf("expected 1 turn (1 LLM call), got %d", finalEv.Turn)
	}
}

func TestRunContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	client := &mockLLMClient{
		responses: []*llm.Response{
			makeTextResponse("should not be called"),
		},
	}
	registry := newTestRegistry()
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(ctx, []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	}))

	// Context 取消：Reason 应为 Aborted，Err 非 nil（ctx.Err()）
	if finalEv.Reason != ReasonAborted {
		t.Errorf("expected ReasonAborted, got %s", finalEv.Reason)
	}
}

func TestRunModelError(t *testing.T) {
	client := &mockLLMClient{
		errors: []error{
			&llm.NonRetryableError{Message: "unauthorized", StatusCode: 401},
		},
	}
	registry := newTestRegistry()
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	}))

	if finalEv.Err == nil {
		t.Fatal("expected error, got nil")
	}
	if finalEv.Reason != ReasonModelError {
		t.Errorf("expected ReasonModelError, got %s", finalEv.Reason)
	}
}

// ============================================================================
// 3. 错误处理测试
// ============================================================================

func TestRunToolFatalError(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("", makeToolCall("tc1", "write_file", `{"file_path":"/tmp/x","content":"data"}`)),
		},
	}
	fatalTool := newErrorTool("write_file", false, &tool.ToolError{
		Class:   tool.ErrorClassFatal,
		Kind:    tool.ErrKindPermissionDenied,
		Message: "permission denied: /tmp/x",
	})
	registry := newTestRegistry(fatalTool)
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "write to /tmp/x"},
	}))

	if finalEv.Err == nil {
		t.Fatal("expected error, got nil")
	}
	if finalEv.Reason != ReasonToolFatal {
		t.Errorf("expected ReasonToolFatal, got %s", finalEv.Reason)
	}
	// 即使终止，消息历史应保留完整的 assistant(tool_call) ↔ tool 配对
	// user → assistant(tc1) → tool(tc1 error) = 3
	if len(finalEv.Messages) != 3 {
		t.Errorf("expected 3 messages (user + assistant + tool error), got %d", len(finalEv.Messages))
		for i, m := range finalEv.Messages {
			t.Logf("  msg[%d]: role=%s content=%.40s toolCallIDs=%v", i, m.Role, m.Content, toolCallIDs(m))
		}
	}
	// 验证 assistant 消息保留了 ToolCalls（因为后面有配对 tool 消息）
	assistantMsg := finalEv.Messages[1]
	if assistantMsg.Role != llm.RoleAssistant {
		t.Errorf("expected assistant role, got %s", assistantMsg.Role)
	}
	if len(assistantMsg.ToolCalls) != 1 {
		t.Errorf("expected 1 tool_call in assistant message, got %d", len(assistantMsg.ToolCalls))
	}
	// 验证 tool 消息存在且包含错误
	toolMsg := finalEv.Messages[2]
	if toolMsg.Role != llm.RoleTool {
		t.Errorf("expected tool role, got %s", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "tc1" {
		t.Errorf("expected ToolCallID tc1, got %s", toolMsg.ToolCallID)
	}
	if !strings.Contains(toolMsg.Content, "permission_denied") {
		t.Errorf("expected permission_denied in tool message, got: %s", toolMsg.Content)
	}
}

func TestRunRecoverableError(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{
			// Turn 1: tool call 返回 recoverable 错误
			makeToolCallResponse("", makeToolCall("tc1", "read_file", `{"file_path":"/tmp/missing.txt"}`)),
			// Turn 2: LLM 看到错误后修正，返回最终答案
			makeTextResponse("The file does not exist. Can I help with something else?"),
		},
	}
	errorTool := newErrorTool("read_file", true, &tool.ToolError{
		Class:   tool.ErrorClassRecoverable,
		Kind:    tool.ErrKindFileNotFound,
		Message: "file not found: /tmp/missing.txt",
	})
	registry := newTestRegistry(errorTool)
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "read /tmp/missing.txt"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
	if finalEv.Turn != 2 {
		t.Errorf("expected 2 turns (2 LLM calls), got %d", finalEv.Turn)
	}
	// 验证错误消息作为 tool 消息内容返回
	toolMsg := finalEv.Messages[2]
	if toolMsg.Role != llm.RoleTool {
		t.Errorf("expected tool role, got %s", toolMsg.Role)
	}
	if toolMsg.Content != "Error [file_not_found]: file not found: /tmp/missing.txt" {
		t.Errorf("expected error content, got: %s", toolMsg.Content)
	}
}

func TestRunRecoverableErrorsDoNotTerminate(t *testing.T) {
	// 同类 Recoverable 错误重复 2 次不应终止 —— LLM 仍有修正机会。
	// 第 3 次才触发退避保护（见 TestRunConsecutiveSameErrorsTerminate）。
	fileNotFoundErr := &tool.ToolError{
		Class:   tool.ErrorClassRecoverable,
		Kind:    tool.ErrKindFileNotFound,
		Message: "file not found: /tmp/x",
	}

	client := &mockLLMClient{
		responses: []*llm.Response{
			// Turn 1-2: 重复返回相同的 file_not_found tool call（2 次不触发退避）
			makeToolCallResponse("", makeToolCall("tc1", "read_file", `{"file_path":"/tmp/x"}`)),
			makeToolCallResponse("", makeToolCall("tc2", "read_file", `{"file_path":"/tmp/x"}`)),
			// Turn 3: LLM 看到错误后主动停止
			makeTextResponse("The file does not exist after multiple attempts."),
		},
	}
	errorTool := newErrorTool("read_file", true, fileNotFoundErr)
	registry := newTestRegistry(errorTool)
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "find the file"},
	}))

	// 不应因同类错误超限而终止，应正常完成
	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
	// 3 轮 LLM 调用（2 次 tool return + 1 次 final answer）
	if finalEv.Turn != 3 {
		t.Errorf("expected 3 turns, got %d", finalEv.Turn)
	}
}

func TestRunConsecutiveSameErrorsTerminate(t *testing.T) {
	// 同类 Recoverable 错误连续出现 5 次 → 强制终止，避免无限重试。
	fileNotFoundErr := &tool.ToolError{
		Class:   tool.ErrorClassRecoverable,
		Kind:    tool.ErrKindFileNotFound,
		Message: "file not found: /tmp/x",
	}

	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("", makeToolCall("tc1", "read_file", `{"file_path":"/tmp/x"}`)),
			makeToolCallResponse("", makeToolCall("tc2", "read_file", `{"file_path":"/tmp/x"}`)),
			makeToolCallResponse("", makeToolCall("tc3", "read_file", `{"file_path":"/tmp/x"}`)),
			makeToolCallResponse("", makeToolCall("tc4", "read_file", `{"file_path":"/tmp/x"}`)),
			makeToolCallResponse("", makeToolCall("tc5", "read_file", `{"file_path":"/tmp/x"}`)),
			// 不应到达这里——第 5 次后 loop 已终止
			makeTextResponse("unreachable"),
		},
	}
	errorTool := newErrorTool("read_file", true, fileNotFoundErr)
	registry := newTestRegistry(errorTool)
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "find the file"},
	}))

	if finalEv.Err == nil {
		t.Fatalf("expected error after 5 consecutive same errors, got nil")
	}
	if finalEv.Reason != ReasonToolFatal {
		t.Errorf("expected ReasonToolFatal, got %s", finalEv.Reason)
	}
	// 5 轮 LLM 调用后终止
	if finalEv.Turn != 5 {
		t.Errorf("expected 5 turns, got %d", finalEv.Turn)
	}
}

func TestRunDifferentRecoverableErrors(t *testing.T) {
	// 不同 Kind 的错误各自计数，互不影响
	client := &mockLLMClient{
		responses: []*llm.Response{
			// file_not_found × 2
			makeToolCallResponse("", makeToolCall("tc1", "read_file", `{"file_path":"/tmp/a"}`)),
			makeToolCallResponse("", makeToolCall("tc2", "read_file", `{"file_path":"/tmp/b"}`)),
			// invalid_args × 2
			makeToolCallResponse("", makeToolCall("tc3", "grep", `{"pattern":"["}`)),
			makeToolCallResponse("", makeToolCall("tc4", "grep", `{"pattern":"[["}`)),
			// 最终完成
			makeTextResponse("All done"),
		},
	}

	var readCount int32
	readTool := &mockTool{
		name:           "read_file",
		desc:           "read file tool",
		schema:         json.RawMessage(`{"type":"object","properties":{}}`),
		concurrentSafe: true,
		result:         nil, // 在 Execute 中动态设置
		execCount:      &readCount,
	}
	// 重写 Execute 来返回 file_not_found
	// 这里使用 execCount 来在 result 中设置错误
	readTool.result = &tool.ToolResult{
		Error: &tool.ToolError{
			Class:   tool.ErrorClassRecoverable,
			Kind:    tool.ErrKindFileNotFound,
			Message: "file not found",
		},
	}

	var grepCount int32
	grepTool := &mockTool{
		name:           "grep",
		desc:           "grep tool",
		schema:         json.RawMessage(`{"type":"object","properties":{}}`),
		concurrentSafe: true,
		result: &tool.ToolResult{
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    tool.ErrKindInvalidArgs,
				Message: "invalid regex",
			},
		},
		execCount: &grepCount,
	}

	registry := newTestRegistry(readTool, grepTool)
	loop := New(client, registry, Config{})

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "test"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
	// 每种错误出现 2 次，都没有超过 3 次限制
	if finalEv.Turn != 5 {
		t.Errorf("expected 5 turns (5 LLM calls), got %d", finalEv.Turn)
	}
}

func TestRunBackoffResetBySuccess(t *testing.T) {
	// 同类错误连续出现，中间夹一个成功的工具调用 → 计数器归零。
	// find_file(error)×2 → list_files(success) → find_file(error)×5 → 退避触发
	fileNotFoundErr := &tool.ToolError{
		Class:   tool.ErrorClassRecoverable,
		Kind:    tool.ErrKindFileNotFound,
		Message: "file not found: /tmp/x",
	}

	client := &mockLLMClient{
		responses: []*llm.Response{
			// Turn 1-2: find_file 错误 × 2
			makeToolCallResponse("", makeToolCall("tc1", "find_file", `{"path":"/tmp/x"}`)),
			makeToolCallResponse("", makeToolCall("tc2", "find_file", `{"path":"/tmp/x"}`)),
			// Turn 3: list_files 成功（不同工具，anySuccess=true → 计数器归零）
			makeToolCallResponse("", makeToolCall("tc3", "list_files", `{"path":"/tmp"}`)),
			// Turn 4-8: find_file 错误 × 5 → 退避触发
			makeToolCallResponse("", makeToolCall("tc4", "find_file", `{"path":"/tmp/x"}`)),
			makeToolCallResponse("", makeToolCall("tc5", "find_file", `{"path":"/tmp/x"}`)),
			makeToolCallResponse("", makeToolCall("tc6", "find_file", `{"path":"/tmp/x"}`)),
			makeToolCallResponse("", makeToolCall("tc7", "find_file", `{"path":"/tmp/x"}`)),
			makeToolCallResponse("", makeToolCall("tc8", "find_file", `{"path":"/tmp/x"}`)),
			// 不应到达
			makeTextResponse("unreachable"),
		},
	}

	registry := newTestRegistry(
		newErrorTool("find_file", true, fileNotFoundErr),
		newSuccessTool("list_files", true, "/tmp:\n  a.txt\n  b.txt"),
	)
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "find the file"},
	}))

	// 成功重置后的 5 次同类错误触发退避
	if finalEv.Err == nil {
		t.Fatalf("expected error after 5 consecutive same errors following a success reset")
	}
	if finalEv.Reason != ReasonToolFatal {
		t.Errorf("expected ReasonToolFatal, got %s", finalEv.Reason)
	}
	// 8 轮 LLM 调用后终止（2 error + 1 success + 5 error）
	if finalEv.Turn != 8 {
		t.Errorf("expected 8 turns, got %d", finalEv.Turn)
	}
}

func TestRunMixedErrorsSameBatchNoBackoff(t *testing.T) {
	// 同一轮中有不同 Kind 的错误 → 不触发退避。
	fileNotFoundErr := &tool.ToolError{
		Class:   tool.ErrorClassRecoverable,
		Kind:    tool.ErrKindFileNotFound,
		Message: "file not found: /tmp/a",
	}
	invalidArgsErr := &tool.ToolError{
		Class:   tool.ErrorClassRecoverable,
		Kind:    tool.ErrKindInvalidArgs,
		Message: "invalid regex",
	}

	client := &mockLLMClient{
		responses: []*llm.Response{
			// Turn 1: 同轮两个工具调用，不同 Kind 错误
			makeToolCallResponse("",
				makeToolCall("tc1", "read_file", `{"file_path":"/tmp/a"}`),
				makeToolCall("tc2", "grep", `{"pattern":"["}`),
			),
			// Turn 2: 同上
			makeToolCallResponse("",
				makeToolCall("tc3", "read_file", `{"file_path":"/tmp/a"}`),
				makeToolCall("tc4", "grep", `{"pattern":"["}`),
			),
			// Turn 3: 同上 — 仍不触发退避（因为每轮都是混合 Kind）
			makeToolCallResponse("",
				makeToolCall("tc5", "read_file", `{"file_path":"/tmp/a"}`),
				makeToolCall("tc6", "grep", `{"pattern":"["}`),
			),
			// Turn 4: LLM 停止
			makeTextResponse("Both tools consistently failed with different errors."),
		},
	}

	registry := newTestRegistry(
		newErrorTool("read_file", true, fileNotFoundErr),
		newErrorTool("grep", true, invalidArgsErr),
	)
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "find the file and search"},
	}))

	// 同轮混合错误不触发退避，loop 应正常完成
	if finalEv.Err != nil {
		t.Fatalf("unexpected error: mixed errors in same batch should not trigger backoff: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
	if finalEv.Turn != 4 {
		t.Errorf("expected 4 turns, got %d", finalEv.Turn)
	}
}

func TestRunPreservesMessageHistoryOnError(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{
			// Turn 1: 成功
			makeToolCallResponse("", makeToolCall("tc1", "read_file", `{"file_path":"/tmp/ok.txt"}`)),
			// Turn 2: 致命错误
			makeToolCallResponse("", makeToolCall("tc2", "write_file", `{"file_path":"/etc/x","content":"x"}`)),
		},
	}
	readTool := newSuccessTool("read_file", true, "file content ok")
	fatalTool := newErrorTool("write_file", false, &tool.ToolError{
		Class:   tool.ErrorClassFatal,
		Kind:    tool.ErrKindPermissionDenied,
		Message: "permission denied: /etc/x",
	})
	registry := newTestRegistry(readTool, fatalTool)
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "read then write"},
	}))

	if finalEv.Err == nil {
		t.Fatal("expected error, got nil")
	}
	if finalEv.Reason != ReasonToolFatal {
		t.Errorf("expected ReasonToolFatal, got %s", finalEv.Reason)
	}
	// 消息历史应包含 Turn 1 和 Turn 2 的所有消息（含错误 tool 消息）
	// user → asst(tc1) → tool(ok) → asst(tc2) → tool(tc2 error) = 5
	if len(finalEv.Messages) != 5 {
		t.Errorf("expected 5 messages (user + asst1 + tool1 + asst2 + tool2_error), got %d", len(finalEv.Messages))
		for i, m := range finalEv.Messages {
			t.Logf("  msg[%d]: role=%s content=%s", i, m.Role, m.Content)
		}
	}
	// 验证 Turn 1 的 tool 结果在历史中
	hasToolResult := false
	for _, m := range finalEv.Messages {
		if m.Role == llm.RoleTool && m.Content == "file content ok" {
			hasToolResult = true
		}
	}
	if !hasToolResult {
		t.Error("expected tool result 'file content ok' in message history")
	}
	// 验证 Turn 2 的错误 tool 消息也在历史中
	hasErrorMsg := false
	for _, m := range finalEv.Messages {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "permission_denied") {
			hasErrorMsg = true
		}
	}
	if !hasErrorMsg {
		t.Error("expected permission_denied error in tool message history")
	}
}

// ============================================================================
// 4. 工具并发测试
// ============================================================================

func TestConcurrentToolsRunInParallel(t *testing.T) {
	// 使用 barrier 机制验证并发：所有工具必须同时进入 Execute，
	// 在 startBarrier.Wait() 返回前全部阻塞在 proceedCh 上。
	var startBarrier sync.WaitGroup
	proceedCh := make(chan struct{})
	var orderMu sync.Mutex
	var execOrder []string

	tools := []tool.Tool{
		&barrierTool{
			name: "t1", schema: json.RawMessage(`{}`), concurrentSafe: true,
			result: &tool.ToolResult{Content: "ok1"},
			startBarrier: &startBarrier, proceedCh: proceedCh,
			execOrder: &execOrder, orderMu: &orderMu,
		},
		&barrierTool{
			name: "t2", schema: json.RawMessage(`{}`), concurrentSafe: true,
			result: &tool.ToolResult{Content: "ok2"},
			startBarrier: &startBarrier, proceedCh: proceedCh,
			execOrder: &execOrder, orderMu: &orderMu,
		},
		&barrierTool{
			name: "t3", schema: json.RawMessage(`{}`), concurrentSafe: true,
			result: &tool.ToolResult{Content: "ok3"},
			startBarrier: &startBarrier, proceedCh: proceedCh,
			execOrder: &execOrder, orderMu: &orderMu,
		},
	}
	startBarrier.Add(3)

	registry := newTestRegistry(tools...)

	calls := []llm.ToolCall{
		makeToolCall("c1", "t1", `{}`),
		makeToolCall("c2", "t2", `{}`),
		makeToolCall("c3", "t3", `{}`),
	}

	loop := New(nil, registry, DefaultConfig())
	state := &LoopState{
		Messages: nil,
	}

	// 在 goroutine 中执行，因为 executeToolCalls 会阻塞等待所有工具完成
	type execResult struct {
		msgs   []llm.Message
		reason TerminalReason
		err    error
	}
	resultCh := make(chan execResult, 1)
	go func() {
		ch := make(chan TurnEvent, 32)
		msgs, reason, err := loop.executeToolCalls(context.Background(), calls, state, ch)
		go func() { for range ch {} }()
		resultCh <- execResult{msgs, reason, err}
	}()

	// 等待所有 3 个工具都已启动（进入 Execute 并调用 startBarrier.Done()）
	startBarrier.Wait()

	// 此时所有工具都在阻塞等待 proceedCh，证明它们并发进入了 Execute
	close(proceedCh)

	// 等待执行完成
	res := <-resultCh
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}
	if res.reason != "" {
		t.Errorf("expected empty reason, got %s", res.reason)
	}
	if len(res.msgs) != 3 {
		t.Errorf("expected 3 tool messages, got %d", len(res.msgs))
	}

	// 所有 3 个工具都已完成（不关心具体顺序）
	if len(execOrder) != 3 {
		t.Errorf("expected 3 tools executed, got %d: %v", len(execOrder), execOrder)
	}
}

func TestSerialToolsRunSequentially(t *testing.T) {
	var orderMu sync.Mutex
	var execOrder []string

	// 串行工具：ConcurrentSafe=false
	t1 := &mockTool{
		name:           "t1",
		desc:           "serial tool 1",
		schema:         json.RawMessage(`{}`),
		concurrentSafe: false,
		result:         &tool.ToolResult{Content: "ok1"},
		execOrder:      &execOrder,
		orderMu:        &orderMu,
		execDelay:      10 * time.Millisecond,
	}
	t2 := &mockTool{
		name:           "t2",
		desc:           "serial tool 2",
		schema:         json.RawMessage(`{}`),
		concurrentSafe: false,
		result:         &tool.ToolResult{Content: "ok2"},
		execOrder:      &execOrder,
		orderMu:        &orderMu,
		execDelay:      10 * time.Millisecond,
	}
	t3 := &mockTool{
		name:           "t3",
		desc:           "serial tool 3",
		schema:         json.RawMessage(`{}`),
		concurrentSafe: false,
		result:         &tool.ToolResult{Content: "ok3"},
		execOrder:      &execOrder,
		orderMu:        &orderMu,
		execDelay:      10 * time.Millisecond,
	}

	registry := newTestRegistry(t1, t2, t3)
	loop := New(nil, registry, DefaultConfig())
	state := &LoopState{}

	calls := []llm.ToolCall{
		makeToolCall("c1", "t1", `{}`),
		makeToolCall("c2", "t2", `{}`),
		makeToolCall("c3", "t3", `{}`),
	}

	ch := make(chan TurnEvent, 32)
	msgs, reason, err := loop.executeToolCalls(context.Background(), calls, state, ch)
	go func() { for range ch {} }()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %s", reason)
	}
	if len(msgs) != 3 {
		t.Errorf("expected 3 messages, got %d", len(msgs))
	}

	// 串行执行应按调用顺序记录
	if len(execOrder) != 3 {
		t.Fatalf("expected 3 executions, got %d", len(execOrder))
	}
	if execOrder[0] != "t1" || execOrder[1] != "t2" || execOrder[2] != "t3" {
		t.Errorf("expected sequential order [t1, t2, t3], got %v", execOrder)
	}
}

func TestMixedConcurrentAndSerialTools(t *testing.T) {
	var orderMu sync.Mutex
	var execOrder []string
	var serialOrder []string

	// 并发组
	c1 := &mockTool{
		name: "c1", desc: "concurrent 1", schema: json.RawMessage(`{}`),
		concurrentSafe: true, result: &tool.ToolResult{Content: "c1"},
		execOrder: &execOrder, orderMu: &orderMu, execDelay: 30 * time.Millisecond,
	}
	c2 := &mockTool{
		name: "c2", desc: "concurrent 2", schema: json.RawMessage(`{}`),
		concurrentSafe: true, result: &tool.ToolResult{Content: "c2"},
		execOrder: &execOrder, orderMu: &orderMu, execDelay: 30 * time.Millisecond,
	}

	// 串行组
	s1 := &mockTool{
		name: "s1", desc: "serial 1", schema: json.RawMessage(`{}`),
		concurrentSafe: false, result: &tool.ToolResult{Content: "s1"},
		execOrder: &serialOrder, orderMu: &orderMu, execDelay: 10 * time.Millisecond,
	}
	s2 := &mockTool{
		name: "s2", desc: "serial 2", schema: json.RawMessage(`{}`),
		concurrentSafe: false, result: &tool.ToolResult{Content: "s2"},
		execOrder: &serialOrder, orderMu: &orderMu, execDelay: 10 * time.Millisecond,
	}

	registry := newTestRegistry(c1, c2, s1, s2)
	loop := New(nil, registry, DefaultConfig())
	state := &LoopState{}

	calls := []llm.ToolCall{
		makeToolCall("cc1", "c1", `{}`),
		makeToolCall("cc2", "c2", `{}`),
		makeToolCall("sc1", "s1", `{}`),
		makeToolCall("sc2", "s2", `{}`),
	}

	start := time.Now()
	ch := make(chan TurnEvent, 32)
	msgs, reason, err := loop.executeToolCalls(context.Background(), calls, state, ch)
	go func() { for range ch {} }()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %s", reason)
	}
	if len(msgs) != 4 {
		t.Errorf("expected 4 messages, got %d", len(msgs))
	}

	// 验证消息按原始顺序：c1, c2, s1, s2
	expectedOrder := []string{"cc1", "cc2", "sc1", "sc2"}
	for i, tcID := range expectedOrder {
		if msgs[i].ToolCallID != tcID {
			t.Errorf("msg[%d]: expected ToolCallID %s, got %s", i, tcID, msgs[i].ToolCallID)
		}
	}

	// 串行工具应按顺序执行
	if len(serialOrder) != 2 {
		t.Fatalf("expected 2 serial executions, got %d", len(serialOrder))
	}
	if serialOrder[0] != "s1" || serialOrder[1] != "s2" {
		t.Errorf("expected serial order [s1, s2], got %v", serialOrder)
	}

	// 性能验证：并发组（30ms）+ 串行组（10ms+10ms）≈ 50ms，不应超过 200ms
	if elapsed > 200*time.Millisecond {
		t.Errorf("execution too slow for mixed concurrent+serial: %v", elapsed)
	}
}

// ============================================================================
// 5. 边界条件测试
// ============================================================================

func TestConfigDefaultValues(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxTurns != 0 {
		t.Errorf("expected MaxTurns=0 (unlimited), got %d", cfg.MaxTurns)
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	loop := New(nil, nil, Config{})
	if loop.config.MaxTurns != 0 {
		t.Errorf("expected MaxTurns=0 (unlimited), got %d", loop.config.MaxTurns)
	}
}

func TestNewPreservesExplicitValues(t *testing.T) {
	loop := New(nil, nil, Config{MaxTurns: 10})
	if loop.config.MaxTurns != 10 {
		t.Errorf("expected MaxTurns=10, got %d", loop.config.MaxTurns)
	}
}

func TestToLLMToolSpecs(t *testing.T) {
	specs := []tool.ToolSpec{
		{Name: "read_file", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "grep", Description: "Search text", Parameters: json.RawMessage(`{"type":"object"}`)},
	}
	result := toLLMToolSpecs(specs)
	if len(result) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(result))
	}
	if result[0].Name != "read_file" {
		t.Errorf("expected name 'read_file', got %s", result[0].Name)
	}
	if result[0].Description != "Read a file" {
		t.Errorf("expected description 'Read a file', got %s", result[0].Description)
	}
	// Parameters 作为 json.RawMessage 传递给 interface{}，序列化时应保持原始 JSON
	params, ok := result[0].Parameters.(json.RawMessage)
	if !ok {
		t.Errorf("expected Parameters to be json.RawMessage, got %T", result[0].Parameters)
	}
	if string(params) != `{"type":"object"}` {
		t.Errorf("unexpected params: %s", string(params))
	}
}

func TestRunUnknownTool(t *testing.T) {
	// LLM 请求一个不在 Registry 中的工具 → filter 在追加 messages 前拦截，
	// assistant 消息的 tool_calls 被清空，循环正常完成（ReasonCompleted），不崩溃。
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("", makeToolCall("tc1", "nonexistent_tool", `{}`)),
		},
	}
	registry := newTestRegistry() // 空的 Registry
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "do something"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted (filtered tool calls = no tools to execute), got %s", finalEv.Reason)
	}

	// 验证消息历史无孤儿 tool_calls
	for _, msg := range finalEv.Messages {
		if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) > 0 {
			t.Error("messages should not contain orphaned assistant tool_calls after filtering")
		}
	}
}

// TestRunFiltersInvalidToolCalls 验证 LLM 返回的 tool_calls 中无效项（空 ID、空 Name、不存在工具）
// 在追加到 assistant 消息前被过滤，不污染对话历史。
func TestRunFiltersInvalidToolCalls(t *testing.T) {
	// LLM 返回混合 tool_calls：一个有效 + 一个空 ID + 一个不存在工具
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("", []llm.ToolCall{
				{ID: "tc1", Name: "read_file", Arguments: `{"file_path":"/tmp/x"}`},
				{ID: "", Name: "read_file", Arguments: `{}`},          // 空 ID → 剔除
				{ID: "tc3", Name: "nonexistent", Arguments: `{}`},     // 不存在工具 → 剔除
			}...),
			// Turn 2: LLM 收到过滤后只剩 tc1 的结果，正常完成
			makeTextResponse("done"),
		},
	}
	readTool := newSuccessTool("read_file", true, "content here")
	registry := newTestRegistry(readTool)
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "read file"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}

	// assistant 消息只应有 tc1
	for _, msg := range finalEv.Messages {
		if msg.Role == llm.RoleAssistant && len(msg.ToolCalls) > 0 {
			if len(msg.ToolCalls) != 1 {
				t.Errorf("expected 1 valid tool_call, got %d", len(msg.ToolCalls))
			}
			if msg.ToolCalls[0].ID != "tc1" {
				t.Errorf("expected tool_call id=tc1, got id=%s", msg.ToolCalls[0].ID)
			}
		}
	}
}

func TestConcurrentToolExecutionError(t *testing.T) {
	// 并发工具返回 registry 级 Go error → ReasonToolFatal
	var execErr error = fmt.Errorf("simulated execution error")
	errorTool := &mockTool{
		name:           "failing_tool",
		desc:           "always fails",
		schema:         json.RawMessage(`{}`),
		concurrentSafe: true,
		execErr:        execErr,
	}
	registry := newTestRegistry(errorTool)
	loop := New(nil, registry, DefaultConfig())
	state := &LoopState{}

	calls := []llm.ToolCall{
		makeToolCall("tc1", "failing_tool", `{}`),
	}

	ch := make(chan TurnEvent, 32)
	_, reason, err := loop.executeToolCalls(context.Background(), calls, state, ch)
	go func() { for range ch {} }()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if reason != ReasonToolFatal {
		t.Errorf("expected ReasonToolFatal, got %s", reason)
	}
}

func TestSerialToolExecutionError(t *testing.T) {
	// 串行工具返回 registry 级 Go error → ReasonToolFatal
	var execErr error = fmt.Errorf("serial execution failure")
	errorTool := &mockTool{
		name:           "serial_failing_tool",
		desc:           "always fails",
		schema:         json.RawMessage(`{}`),
		concurrentSafe: false, // 串行
		execErr:        execErr,
	}
	registry := newTestRegistry(errorTool)
	loop := New(nil, registry, DefaultConfig())
	state := &LoopState{}

	calls := []llm.ToolCall{
		makeToolCall("tc1", "serial_failing_tool", `{}`),
	}

	ch := make(chan TurnEvent, 32)
	_, reason, err := loop.executeToolCalls(context.Background(), calls, state, ch)
	go func() { for range ch {} }()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if reason != ReasonToolFatal {
		t.Errorf("expected ReasonToolFatal, got %s", reason)
	}
}

func TestRunSystemPromptInjection(t *testing.T) {
	// SystemPrompt 配置后，Run 应在 messages 头部注入 system 消息
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeTextResponse("done"),
		},
	}
	registry := newTestRegistry()
	cfg := Config{SystemPrompt: "You are a helpful assistant.", }
	loop := New(client, registry, cfg)

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if len(finalEv.Messages) < 3 {
		t.Fatalf("expected at least 3 messages, got %d", len(finalEv.Messages))
	}
	if finalEv.Messages[0].Role != llm.RoleSystem {
		t.Errorf("expected first message role=system, got %s", finalEv.Messages[0].Role)
	}
	if finalEv.Messages[0].Content != "You are a helpful assistant." {
		t.Errorf("unexpected system prompt: %s", finalEv.Messages[0].Content)
	}
}

func TestRunSystemPromptNotDuplicated(t *testing.T) {
	// 如果 messages 已包含 system 消息，SystemPrompt 不应重复注入
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeTextResponse("done"),
		},
	}
	registry := newTestRegistry()
	cfg := Config{SystemPrompt: "Override prompt", }
	loop := New(client, registry, cfg)

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleSystem, Content: "Original prompt"},
		{Role: llm.RoleUser, Content: "hello"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	// 不应注入第二条 system 消息
	systemCount := 0
	for _, m := range finalEv.Messages {
		if m.Role == llm.RoleSystem {
			systemCount++
		}
	}
	if systemCount != 1 {
		t.Errorf("expected exactly 1 system message, got %d", systemCount)
	}
	if finalEv.Messages[0].Content != "Original prompt" {
		t.Errorf("expected original system prompt preserved, got %s", finalEv.Messages[0].Content)
	}
}

func TestRunSystemPromptEmpty(t *testing.T) {
	// SystemPrompt 为空时不注入任何 system 消息
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeTextResponse("done"),
		},
	}
	registry := newTestRegistry()
	cfg := Config{SystemPrompt: "", }
	loop := New(client, registry, cfg)

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	// 不应有 system 消息
	if finalEv.Messages[0].Role == llm.RoleSystem {
		t.Errorf("unexpected system message injected when SystemPrompt is empty")
	}
}

// ============================================================================
// 6. 集成/组合场景测试
// ============================================================================

// --- 6a. Context 在工具执行中途取消 ---

func TestRunContextCancelledDuringToolExecution(t *testing.T) {
	// 工具执行过程中取消 ctx，工具应响应取消，Loop 应返回 ReasonAborted
	ctx, cancel := context.WithCancel(context.Background())

	// 延迟取消 ctx：在工具开始执行后 50ms 取消
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	slowTool := &mockTool{
		name:           "slow_tool",
		desc:           "slow tool",
		schema:         json.RawMessage(`{}`),
		concurrentSafe: false,
		execDelay:      5 * time.Second, // 足够长，确保 ctx 取消时工具仍在执行
		result:         &tool.ToolResult{Content: "should not reach"},
	}

	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("", makeToolCall("tc1", "slow_tool", `{}`)),
		},
	}
	registry := newTestRegistry(slowTool)
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(ctx, []llm.Message{
		{Role: llm.RoleUser, Content: "run slow tool"},
	}))

	// ctx 取消后，要么串行工具返回 ctx.Err()（registry 级错误 → ReasonToolFatal），
	// 要么下一轮循环开头检测到 ctx.Err()（→ ReasonAborted）。
	// 取决于取消时机，两种结果都合法。
	if finalEv.Reason != ReasonAborted && finalEv.Reason != ReasonToolFatal {
		t.Errorf("expected ReasonAborted or ReasonToolFatal, got %s", finalEv.Reason)
	}
}

// --- 6b. 并发组中部分成功 + 部分 Recoverable 错误 ---

func TestConcurrentPartialRecoverableError(t *testing.T) {
	// 3 个并发工具：2 个成功，1 个返回 Recoverable 错误
	// 所有工具都应在 results 中有条目，Recoverable 错误应作为 tool 消息返回
	success1 := newSuccessTool("tool_a", true, "result-a")
	success2 := newSuccessTool("tool_b", true, "result-b")
	errTool := newErrorTool("tool_c", true, &tool.ToolError{
		Class:   tool.ErrorClassRecoverable,
		Kind:    tool.ErrKindFileNotFound,
		Message: "file not found: /tmp/missing",
	})

	registry := newTestRegistry(success1, success2, errTool)
	loop := New(nil, registry, DefaultConfig())
	state := &LoopState{}

	calls := []llm.ToolCall{
		makeToolCall("tc1", "tool_a", `{}`),
		makeToolCall("tc2", "tool_b", `{}`),
		makeToolCall("tc3", "tool_c", `{}`),
	}

	ch := make(chan TurnEvent, 32)
	msgs, reason, err := loop.executeToolCalls(context.Background(), calls, state, ch)
	go func() { for range ch {} }()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reason != "" {
		t.Errorf("expected empty reason for recoverable errors, got %s", reason)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 tool messages, got %d", len(msgs))
	}

	// 验证成功工具的消息内容
	if msgs[0].ToolCallID != "tc1" || msgs[0].Content != "result-a" {
		t.Errorf("msg[0]: expected tc1/result-a, got %s/%s", msgs[0].ToolCallID, msgs[0].Content)
	}
	if msgs[1].ToolCallID != "tc2" || msgs[1].Content != "result-b" {
		t.Errorf("msg[1]: expected tc2/result-b, got %s/%s", msgs[1].ToolCallID, msgs[1].Content)
	}
	// 验证 Recoverable 错误的消息内容
	if msgs[2].ToolCallID != "tc3" {
		t.Errorf("msg[2]: expected ToolCallID tc3, got %s", msgs[2].ToolCallID)
	}
	if msgs[2].Content != "Error [file_not_found]: file not found: /tmp/missing" {
		t.Errorf("msg[2]: expected error content, got %s", msgs[2].Content)
	}
}

// --- 6c. 并发组中部分成功 + 部分 Fatal 错误 ---

func TestConcurrentPartialFatalError(t *testing.T) {
	// 2 个并发工具：1 个成功，1 个返回 Fatal 错误
	// Fatal 错误应在第 4 步（构造消息时）被捕获并终止
	successTool := newSuccessTool("tool_ok", true, "ok-result")
	fatalTool := newErrorTool("tool_bad", true, &tool.ToolError{
		Class:   tool.ErrorClassFatal,
		Kind:    tool.ErrKindPermissionDenied,
		Message: "permission denied",
	})

	registry := newTestRegistry(successTool, fatalTool)
	loop := New(nil, registry, DefaultConfig())
	state := &LoopState{}

	calls := []llm.ToolCall{
		makeToolCall("tc1", "tool_ok", `{}`),
		makeToolCall("tc2", "tool_bad", `{}`),
	}

	ch := make(chan TurnEvent, 32)
	_, reason, err := loop.executeToolCalls(context.Background(), calls, state, ch)
	go func() { for range ch {} }()
	if err == nil {
		t.Fatal("expected error for fatal tool error, got nil")
	}
	if reason != ReasonToolFatal {
		t.Errorf("expected ReasonToolFatal, got %s", reason)
	}
}

// --- 6d. 消息序列不变量端到端验证 ---

func TestRunMessageSequenceInvariant(t *testing.T) {
	// 验证完整消息链：system → user → assistant(tool) → tool → assistant(tool) → tool → assistant(text)
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("", makeToolCall("tc1", "read_file", `{}`)),
			makeToolCallResponse("", makeToolCall("tc2", "grep", `{}`)),
			makeTextResponse("Final answer"),
		},
	}
	readTool := newSuccessTool("read_file", true, "file content")
	grepTool := newSuccessTool("grep", true, "3 matches")
	registry := newTestRegistry(readTool, grepTool)
	cfg := Config{SystemPrompt: "You are a code assistant.", }
	loop := New(client, registry, cfg)

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "analyze"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}

	// 期望消息序列:
	// [0] system   (注入)
	// [1] user     (输入)
	// [2] assistant(tc1)
	// [3] tool(tc1)
	// [4] assistant(tc2)
	// [5] tool(tc2)
	// [6] assistant(text)
	expectedRoles := []llm.Role{
		llm.RoleSystem, llm.RoleUser,
		llm.RoleAssistant, llm.RoleTool,
		llm.RoleAssistant, llm.RoleTool,
		llm.RoleAssistant,
	}

	if len(finalEv.Messages) != len(expectedRoles) {
		t.Fatalf("expected %d messages, got %d", len(expectedRoles), len(finalEv.Messages))
	}
	for i, expected := range expectedRoles {
		if finalEv.Messages[i].Role != expected {
			t.Errorf("msg[%d]: expected role %s, got %s", i, expected, finalEv.Messages[i].Role)
		}
	}

	// 验证 tool 消息的 ToolCallID 关联正确
	if finalEv.Messages[3].ToolCallID != "tc1" {
		t.Errorf("msg[3]: expected ToolCallID tc1, got %s", finalEv.Messages[3].ToolCallID)
	}
	if finalEv.Messages[5].ToolCallID != "tc2" {
		t.Errorf("msg[5]: expected ToolCallID tc2, got %s", finalEv.Messages[5].ToolCallID)
	}

	// 验证 assistant 消息携带 ToolCalls
	if len(finalEv.Messages[2].ToolCalls) != 1 || finalEv.Messages[2].ToolCalls[0].Name != "read_file" {
		t.Errorf("msg[2]: expected assistant with read_file tool call")
	}
	if len(finalEv.Messages[4].ToolCalls) != 1 || finalEv.Messages[4].ToolCalls[0].Name != "grep" {
		t.Errorf("msg[4]: expected assistant with grep tool call")
	}
	// 最后一轮 assistant 不应有 tool calls
	if len(finalEv.Messages[6].ToolCalls) != 0 {
		t.Errorf("msg[6]: expected assistant without tool calls, got %d", len(finalEv.Messages[6].ToolCalls))
	}
}

// --- 6e. 同轮多工具中不同 Kind 的 Recoverable 错误各自计数 ---

func TestSameTurnMultipleRecoverableErrors(t *testing.T) {
	// 同一轮中 2 个工具分别返回不同 Kind 的 Recoverable 错误
	readErrTool := newErrorTool("read_file", true, &tool.ToolError{
		Class:   tool.ErrorClassRecoverable,
		Kind:    tool.ErrKindFileNotFound,
		Message: "not found",
	})
	grepErrTool := newErrorTool("grep", true, &tool.ToolError{
		Class:   tool.ErrorClassRecoverable,
		Kind:    tool.ErrKindInvalidArgs,
		Message: "bad regex",
	})

	registry := newTestRegistry(readErrTool, grepErrTool)
	loop := New(nil, registry, DefaultConfig())
	state := &LoopState{}

	calls := []llm.ToolCall{
		makeToolCall("tc1", "read_file", `{}`),
		makeToolCall("tc2", "grep", `{}`),
	}

	ch := make(chan TurnEvent, 32)
	msgs, reason, err := loop.executeToolCalls(context.Background(), calls, state, ch)
	go func() { for range ch {} }()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %s", reason)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
}

// --- 6f. 混合并发/串行 + Recoverable 错误 ---

func TestMixedConcurrentSerialWithRecoverableError(t *testing.T) {
	// 并发组成功，串行组返回 Recoverable 错误
	concurrentTool := newSuccessTool("concurrent_read", true, "data")
	serialErrTool := newErrorTool("serial_write", false, &tool.ToolError{
		Class:   tool.ErrorClassRecoverable,
		Kind:    tool.ErrKindCommandFailed,
		Message: "command exited with code 1",
	})

	registry := newTestRegistry(concurrentTool, serialErrTool)
	loop := New(nil, registry, DefaultConfig())
	state := &LoopState{}

	calls := []llm.ToolCall{
		makeToolCall("tc1", "concurrent_read", `{}`),
		makeToolCall("tc2", "serial_write", `{}`),
	}

	ch := make(chan TurnEvent, 32)
	msgs, reason, err := loop.executeToolCalls(context.Background(), calls, state, ch)
	go func() { for range ch {} }()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %s", reason)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	// 并发工具结果在前
	if msgs[0].ToolCallID != "tc1" || msgs[0].Content != "data" {
		t.Errorf("msg[0]: expected tc1/data, got %s/%s", msgs[0].ToolCallID, msgs[0].Content)
	}
	// 串行工具错误
	if msgs[1].ToolCallID != "tc2" {
		t.Errorf("msg[1]: expected ToolCallID tc2, got %s", msgs[1].ToolCallID)
	}
	if msgs[1].Content != "Error [command_failed]: command exited with code 1" {
		t.Errorf("msg[1]: expected error content, got %s", msgs[1].Content)
	}
}

// --- 6g. Loop 可复用性：同一实例多次调用 Run 互不影响 ---

func TestLoopReusableAcrossRuns(t *testing.T) {
	// 同一个 Loop 实例调用两次 Run，状态互不干扰
	client1 := &mockLLMClient{
		responses: []*llm.Response{
			makeTextResponse("first run done"),
		},
	}
	client2 := &mockLLMClient{
		responses: []*llm.Response{
			makeTextResponse("second run done"),
		},
	}

	registry := newTestRegistry()

	// 第一次 Run
	loop := New(client1, registry, DefaultConfig())
	finalEv1 := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "first"},
	}))
	if finalEv1.Err != nil {
		t.Fatalf("first run: unexpected error: %v", finalEv1.Err)
	}
	if finalEv1.Reason != ReasonCompleted {
		t.Errorf("first run: expected ReasonCompleted, got %s", finalEv1.Reason)
	}

	// 替换 client 模拟第二次调用
	loop2 := New(client2, registry, DefaultConfig())
	finalEv2 := drainEvents(loop2.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "second"},
	}))
	if finalEv2.Err != nil {
		t.Fatalf("second run: unexpected error: %v", finalEv2.Err)
	}
	if finalEv2.Reason != ReasonCompleted {
		t.Errorf("second run: expected ReasonCompleted, got %s", finalEv2.Reason)
	}

	// 两次运行的消息互不干扰
	if len(finalEv1.Messages) != 2 {
		t.Errorf("first run: expected 2 messages, got %d", len(finalEv1.Messages))
	}
	if len(finalEv2.Messages) != 2 {
		t.Errorf("second run: expected 2 messages, got %d", len(finalEv2.Messages))
	}
	if finalEv1.Messages[1].Content != "first run done" {
		t.Errorf("first run: unexpected content: %s", finalEv1.Messages[1].Content)
	}
	if finalEv2.Messages[1].Content != "second run done" {
		t.Errorf("second run: unexpected content: %s", finalEv2.Messages[1].Content)
	}
}

// --- 6h. Run 不修改调用方的原始 messages 切片 ---

func TestRunDoesNotMutateInputMessages(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeTextResponse("done"),
		},
	}
	registry := newTestRegistry()
	loop := New(client, registry, DefaultConfig())

	original := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	}
	// 记录原始切片的长度和容量
	origLen := len(original)
	origCap := cap(original)

	finalEv := drainEvents(loop.Run(context.Background(), original))
	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}

	// 调用方切片不应被修改
	if len(original) != origLen {
		t.Errorf("original slice length mutated: was %d, now %d", origLen, len(original))
	}
	if cap(original) != origCap {
		t.Errorf("original slice capacity mutated: was %d, now %d", origCap, cap(original))
	}
	if original[0].Content != "hello" {
		t.Errorf("original slice content mutated: %s", original[0].Content)
	}
}

// --- 6i. MaxTurns 达到时消息序列完整性 ---

func TestRunMaxTurnsMessageSequenceComplete(t *testing.T) {
	// MaxTurns=2，执行 2 轮后停止
	// 消息链应完整：user → asst(tool1) → tool1 → asst(tool2) → tool2
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("", makeToolCall("tc1", "read_file", `{}`)),
			makeToolCallResponse("", makeToolCall("tc2", "read_file", `{}`)),
			makeToolCallResponse("", makeToolCall("tc3", "read_file", `{}`)), // 不会被调用
		},
	}
	readTool := newSuccessTool("read_file", true, "ok")
	registry := newTestRegistry(readTool)
	loop := New(client, registry, Config{MaxTurns: 2, })

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "read files"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonMaxTurns {
		t.Errorf("expected ReasonMaxTurns, got %s", finalEv.Reason)
	}

	// 消息序列: user → asst(tc1) → tool(tc1) → asst(tc2) → tool(tc2) = 5 条
	// 注意：第 2 轮执行完 tool 后 TurnCount=2，shouldContinue 返回 false，
	// 但 assistant(tool2) 消息在 tool 执行前已追加，tool 消息也已追加
	expectedLen := 5 // user + asst1 + tool1 + asst2 + tool2
	if len(finalEv.Messages) != expectedLen {
		t.Errorf("expected %d messages, got %d", expectedLen, len(finalEv.Messages))
		for i, m := range finalEv.Messages {
			t.Logf("  msg[%d]: role=%s content=%.40s toolCallID=%s", i, m.Role, m.Content, m.ToolCallID)
		}
	}

	// 验证 role 序列完整合理
	expectedRoles := []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleAssistant, llm.RoleTool}
	for i, expected := range expectedRoles {
		if finalEv.Messages[i].Role != expected {
			t.Errorf("msg[%d]: expected role %s, got %s", i, expected, finalEv.Messages[i].Role)
		}
	}
}

// --- 6j. 空消息列表传给 Run ---

func TestRunEmptyMessages(t *testing.T) {
	// 传空消息列表，LLM Client 应拒绝（返回错误），Loop 应返回 ReasonModelError
	client := &mockLLMClient{
		errors: []error{
			&llm.NonRetryableError{Message: "messages must not be empty"},
		},
	}
	registry := newTestRegistry()
	loop := New(client, registry, DefaultConfig())

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{}))

	if finalEv.Err == nil {
		t.Fatal("expected error for empty messages, got nil")
	}
	if finalEv.Reason != ReasonModelError {
		t.Errorf("expected ReasonModelError, got %s", finalEv.Reason)
	}
}

// ============================================================================
// 7. Permission 集成测试
// ============================================================================

func TestRunGuardNilBackwardCompatible(t *testing.T) {
	// Guard 为 nil 时，所有操作正常执行（向后兼容）
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("executing", makeToolCall("c1", "tool_a", `{}`)),
			makeTextResponse("done"),
		},
	}
	registry := newTestRegistry(newSuccessTool("tool_a", true, "result"))
	loop := New(client, registry, Config{})

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "do something"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
}

func TestRunGuardAllow(t *testing.T) {
	// Guard 返回 allow → 正常执行
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("executing", makeToolCall("c1", "tool_a", `{}`)),
			makeTextResponse("done"),
		},
	}
	registry := newTestRegistry(newSuccessTool("tool_a", true, "result"))
	guard := &mockGuard{
		results: map[string]permission.DecisionResult{
			"tool_a": {Decision: permission.DecisionAllow, Reason: permission.ReasonDefault},
		},
	}
	loop := New(client, registry, Config{
		Guard: guard,
	})

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "do something"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
}

func TestRunGuardDeny(t *testing.T) {
	// Guard 返回 deny → LLM 收到拒绝消息，不执行工具
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("executing", makeToolCall("c1", "tool_a", `{}`)),
			makeTextResponse("I understand."),
		},
	}
	registry := newTestRegistry(newSuccessTool("tool_a", true, "should not be executed"))
	guard := &mockGuard{
		results: map[string]permission.DecisionResult{
			"tool_a": {Decision: permission.DecisionDeny, Reason: permission.ReasonRule, Message: "blocked by rule"},
		},
	}
	loop := New(client, registry, Config{
		Guard: guard,
	})

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "do something"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}

	// 验证拒绝消息被返回给 LLM
	foundRejectMsg := false
	for _, msg := range finalEv.Messages {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "c1" {
			if msg.Content != "" {
				foundRejectMsg = true
			}
		}
	}
	if !foundRejectMsg {
		t.Error("expected rejection message in tool response")
	}
}

func TestRunGuardAskUserAllows(t *testing.T) {
	// Guard 返回 ask + 用户 allow → 正常执行
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("executing", makeToolCall("c1", "tool_a", `{}`)),
			makeTextResponse("done"),
		},
	}
	execCount := int32(0)
	tool := newSuccessTool("tool_a", true, "result")
	tool.execCount = &execCount
	registry := newTestRegistry(tool)

	guard := &mockGuard{
		results: map[string]permission.DecisionResult{
			"tool_a": {Decision: permission.DecisionAsk, Reason: permission.ReasonDefault, Message: "need confirmation"},
		},
	}
	user := &mockUserResponder{
		choices: map[string]permission.UserChoice{
			"tool_a": {Decision: permission.DecisionAllow},
		},
	}
	loop := New(client, registry, Config{
		
		Guard:           guard,
		UserResponder:   user,
	})

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "do something"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if atomic.LoadInt32(&execCount) != 1 {
		t.Errorf("tool should be executed once, got %d", execCount)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
}

func TestRunGuardAskUserDenies(t *testing.T) {
	// Guard 返回 ask + 用户 deny → LLM 收到拒绝消息
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("executing", makeToolCall("c1", "tool_a", `{}`)),
			makeTextResponse("ok"),
		},
	}
	execCount := int32(0)
	tool := newSuccessTool("tool_a", true, "result")
	tool.execCount = &execCount
	registry := newTestRegistry(tool)

	guard := &mockGuard{
		results: map[string]permission.DecisionResult{
			"tool_a": {Decision: permission.DecisionAsk, Reason: permission.ReasonDefault, Message: "need confirmation"},
		},
	}
	user := &mockUserResponder{
		choices: map[string]permission.UserChoice{
			"tool_a": {Decision: permission.DecisionDeny},
		},
	}
	loop := New(client, registry, Config{
		
		Guard:           guard,
		UserResponder:   user,
	})

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "do something"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	// 工具不应被执行
	if atomic.LoadInt32(&execCount) != 0 {
		t.Errorf("tool should NOT be executed, got count %d", execCount)
	}
	// 应该有 tool 拒绝消息
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
}

func TestRunGuardAskNoResponder(t *testing.T) {
	// Guard 返回 ask 但无 UserResponder → 自动 deny
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("executing", makeToolCall("c1", "tool_a", `{}`)),
			makeTextResponse("ok"),
		},
	}
	execCount := int32(0)
	tool := newSuccessTool("tool_a", true, "result")
	tool.execCount = &execCount
	registry := newTestRegistry(tool)

	guard := &mockGuard{
		results: map[string]permission.DecisionResult{
			"tool_a": {Decision: permission.DecisionAsk, Reason: permission.ReasonDefault},
		},
	}
	loop := New(client, registry, Config{
		
		Guard:           guard,
		// UserResponder 为 nil
	})

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "do something"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if atomic.LoadInt32(&execCount) != 0 {
		t.Errorf("tool should NOT be executed when no responder, got count %d", execCount)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
}

func TestRunGuardAskRemember(t *testing.T) {
	// 用户选择 allow + remember (ScopeSession) → SessionAllow 被调用
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("executing",
				makeToolCall("c1", "tool_a", `{}`),
			),
			makeTextResponse("done"),
		},
	}
	registry := newTestRegistry(newSuccessTool("tool_a", true, "result"))

	guard := &mockGuard{
		results: map[string]permission.DecisionResult{
			"tool_a": {Decision: permission.DecisionAsk, Reason: permission.ReasonDefault, Message: "need confirmation"},
		},
	}
	user := &mockUserResponder{
		choices: map[string]permission.UserChoice{
			"tool_a": {Decision: permission.DecisionAllow, RememberScope: permission.ScopeSession},
		},
	}
	loop := New(client, registry, Config{
		
		Guard:           guard,
		UserResponder:   user,
	})

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "do something"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if guard.sessionAllowCalls == 0 {
		t.Error("expected SessionAllow to be called when user chooses remember with ScopeSession")
	}
}

func TestRunGuardDenyAndRemember(t *testing.T) {
	// 用户选 deny + remember (ScopeSession) → SessionDeny 被调用
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("executing",
				makeToolCall("c1", "danger_tool", `{}`),
			),
			makeTextResponse("ok"),
		},
	}
	registry := newTestRegistry(newSuccessTool("danger_tool", true, "should not run"))

	guard := &mockGuard{
		results: map[string]permission.DecisionResult{
			"danger_tool": {Decision: permission.DecisionAsk, Reason: permission.ReasonDefault, Message: "confirm?"},
		},
	}
	user := &mockUserResponder{
		choices: map[string]permission.UserChoice{
			"danger_tool": {Decision: permission.DecisionDeny, RememberScope: permission.ScopeSession},
		},
	}
	loop := New(client, registry, Config{
		
		Guard:           guard,
		UserResponder:   user,
	})

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "do something"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if guard.sessionDenyCalls == 0 {
		t.Error("expected SessionDeny to be called when user denies with ScopeSession")
	}
}

func TestRunGuardRememberScopeConfig(t *testing.T) {
	// 用户选 allow + remember scope=config → SessionAllow + PersistRule 被调用
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("executing",
				makeToolCall("c1", "tool_a", `{}`),
			),
			makeTextResponse("done"),
		},
	}
	registry := newTestRegistry(newSuccessTool("tool_a", true, "result"))

	guard := &mockGuard{
		results: map[string]permission.DecisionResult{
			"tool_a": {Decision: permission.DecisionAsk, Reason: permission.ReasonDefault, Message: "need confirmation"},
		},
	}
	user := &mockUserResponder{
		choices: map[string]permission.UserChoice{
			"tool_a": {Decision: permission.DecisionAllow, RememberScope: permission.ScopeConfig},
		},
	}
	loop := New(client, registry, Config{
		
		Guard:           guard,
		UserResponder:   user,
	})

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "do something"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if guard.sessionAllowCalls == 0 {
		t.Error("expected SessionAllow to be called")
	}
	if guard.addRuleCalls == 0 {
		t.Error("expected AddRule to be called for ScopeConfig")
	}
	if guard.persistCalls == 0 {
		t.Error("expected PersistRule to be called for ScopeConfig")
	}
}

func TestRunGuardAskNoRemember(t *testing.T) {
	// 用户选 allow 但不记住 → 不调用 SessionAllow 或 PersistRule
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("executing",
				makeToolCall("c1", "tool_a", `{}`),
			),
			makeTextResponse("done"),
		},
	}
	registry := newTestRegistry(newSuccessTool("tool_a", true, "result"))

	guard := &mockGuard{
		results: map[string]permission.DecisionResult{
			"tool_a": {Decision: permission.DecisionAsk, Reason: permission.ReasonDefault, Message: "need confirmation"},
		},
	}
	user := &mockUserResponder{
		choices: map[string]permission.UserChoice{
			"tool_a": {Decision: permission.DecisionAllow, RememberScope: ""},
		},
	}
	loop := New(client, registry, Config{
		
		Guard:           guard,
		UserResponder:   user,
	})

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "do something"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	if guard.addRuleCalls != 0 {
		t.Error("expected AddRule NOT to be called when user doesn't choose remember")
	}
	if guard.sessionAllowCalls != 0 {
		t.Error("expected SessionAllow NOT to be called when user doesn't choose remember")
	}
	if guard.persistCalls != 0 {
		t.Error("expected PersistRule NOT to be called when user doesn't choose remember")
	}
}

func TestRunGuardConcurrentToolsPermission(t *testing.T) {
	// 并发工具 + 权限检查：一个 allow，一个 deny
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("executing",
				makeToolCall("c1", "reader", `{}`),
				makeToolCall("c2", "danger", `{}`),
			),
			makeTextResponse("ok"),
		},
	}
	execCount := int32(0)
	reader := newSuccessTool("reader", true, "data")
	reader.execCount = &execCount
	danger := newSuccessTool("danger", true, "bad")
	dangerCnt := int32(0)
	danger.execCount = &dangerCnt
	registry := newTestRegistry(reader, danger)

	guard := &mockGuard{
		results: map[string]permission.DecisionResult{
			"reader": {Decision: permission.DecisionAllow, Reason: permission.ReasonDefault},
			"danger": {Decision: permission.DecisionDeny, Reason: permission.ReasonRule, Message: "blocked"},
		},
	}
	loop := New(client, registry, Config{
		Guard: guard,
	})

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "do something"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}
	// reader 应执行
	if atomic.LoadInt32(&execCount) != 1 {
		t.Errorf("reader should be executed, got %d", execCount)
	}
	// danger 不应执行
	if atomic.LoadInt32(&dangerCnt) != 0 {
		t.Errorf("danger should NOT be executed, got %d", dangerCnt)
	}
	if finalEv.Reason != ReasonCompleted {
		t.Errorf("expected ReasonCompleted, got %s", finalEv.Reason)
	}
}

func TestRunGuardSkipToolPreservesMessageSequence(t *testing.T) {
	// 权限拒绝不应破坏消息顺序
	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("executing",
				makeToolCall("c1", "safe_tool", `{}`),
				makeToolCall("c2", "blocked_tool", `{}`),
			),
			makeTextResponse("done"),
		},
	}
	registry := newTestRegistry(
		newSuccessTool("safe_tool", false, "safe result"),
		newSuccessTool("blocked_tool", false, "should not see"),
	)
	guard := &mockGuard{
		results: map[string]permission.DecisionResult{
			"safe_tool":    {Decision: permission.DecisionAllow, Reason: permission.ReasonDefault},
			"blocked_tool": {Decision: permission.DecisionDeny, Reason: permission.ReasonRule, Message: "blocked"},
		},
	}
	loop := New(client, registry, Config{
		Guard: guard,
	})

	finalEv := drainEvents(loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "do something"},
	}))

	if finalEv.Err != nil {
		t.Fatalf("unexpected error: %v", finalEv.Err)
	}

	// 消息序列应有：user → assistant(tool_calls) → tool(safe_tool) → tool(blocked_tool denied) → assistant(done)
	// 类型顺序应为：user, assistant, tool, tool, assistant
	expectedRoles := []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleTool, llm.RoleAssistant}
	if len(finalEv.Messages) != 5 {
		t.Errorf("expected 5 messages, got %d", len(finalEv.Messages))
	}
	for i, role := range expectedRoles {
		if i < len(finalEv.Messages) && finalEv.Messages[i].Role != role {
			t.Errorf("message[%d].Role = %s, want %s", i, finalEv.Messages[i].Role, role)
		}
	}
}

func TestShouldContinue(t *testing.T) {
	tests := []struct {
		name      string
		maxTurns  int
		turnCount int
		want      bool
	}{
		{"unlimited", 0, 0, true},
		{"unlimited_many_turns", 0, 100, true},
		{"within_limit", 3, 0, true},
		{"within_limit_2", 3, 2, true},
		{"at_limit", 3, 3, false},
		{"exceeded", 3, 5, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loop := New(nil, nil, Config{MaxTurns: tt.maxTurns})
			state := &LoopState{TurnCount: tt.turnCount}
			got := loop.shouldContinue(state)
			if got != tt.want {
				t.Errorf("shouldContinue() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// mockCompactor — 用于测试 CompactionInfo 流动
// ---------------------------------------------------------------------------

type mockCompactor struct {
	tick compaction.Tick
}

func (m *mockCompactor) Compact(_ context.Context, _ *[]llm.Message, _ int) compaction.Tick {
	return m.tick
}

func (m *mockCompactor) AdvanceTurn() int { return 0 }

// ---------------------------------------------------------------------------
// 压缩集成测试
// ---------------------------------------------------------------------------

func TestRunWithCompactor_TurnStatsIncludesCompaction(t *testing.T) {
	comp := &mockCompactor{
		tick: compaction.Tick{
			Tier:               1,
			TokensSaved:        1500,
			Tier3SummaryDone:   false,
			HardLimitReached:   false,
			UsageRatio:         0.65,
			ContextLimit:       1000000,
			MessageCount:       3,
		},
	}

	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponseWithUsage("working", 650000, makeToolCall("c1", "test_tool", `{}`)),
			{
				Content: "done",
				Usage:   &llm.UsageInfo{PromptTokens: 650000},
			},
		},
	}

	loop := New(client, newTestRegistry(newSuccessTool("test_tool", true, "ok")), Config{MaxTurns: 3, Compactor: comp})
	events := loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleSystem, Content: "test"},
		{Role: llm.RoleUser, Content: "hello"},
	})

	var gotCompaction bool
	for ev := range events {
		if ts, ok := ev.(TurnStats); ok {
			if ts.Compaction.HasCompaction() {
				gotCompaction = true
				if ts.Compaction.Tier != 1 {
					t.Errorf("expected Tier 1, got %d", ts.Compaction.Tier)
				}
				if ts.Compaction.TokensSaved != 1500 {
					t.Errorf("expected TokensSaved 1500, got %d", ts.Compaction.TokensSaved)
				}
				if ts.PromptTokens != 650000 {
					t.Errorf("expected PromptTokens 650000, got %d", ts.PromptTokens)
				}
			}
		}
	}
	if !gotCompaction {
		t.Fatal("expected TurnStats with CompactionInfo.HasCompaction() == true")
	}
}

func TestRunWithCompactor_NoCompaction(t *testing.T) {
	comp := &mockCompactor{
		tick: compaction.Tick{
			Tier:        0,
			TokensSaved: 0,
		},
	}

	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponse("ok", makeToolCall("c1", "test_tool", `{}`)),
			{
				Content: "done",
				Usage:   &llm.UsageInfo{PromptTokens: 300000},
			},
		},
	}

	loop := New(client, newTestRegistry(newSuccessTool("test_tool", true, "ok")), Config{MaxTurns: 3, Compactor: comp})
	events := loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleSystem, Content: "test"},
		{Role: llm.RoleUser, Content: "hello"},
	})

	for ev := range events {
		if ts, ok := ev.(TurnStats); ok {
			if ts.Compaction.HasCompaction() {
				t.Fatal("expected HasCompaction() == false for Tier 0 with 0 savings")
			}
			if ts.PromptTokens != 300000 {
				t.Errorf("expected PromptTokens 300000, got %d", ts.PromptTokens)
			}
		}
	}
}

func TestRunWithCompactor_HardLimitReached(t *testing.T) {
	comp := &mockCompactor{
		tick: compaction.Tick{
			HardLimitReached: true,
			HardLimitReason:  "usage",
			Tier:             0,
		},
	}

	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponseWithUsage("working", 650000, makeToolCall("c1", "test_tool", `{}`)),
		},
	}

	loop := New(client, newTestRegistry(newSuccessTool("test_tool", true, "ok")), Config{MaxTurns: 5, Compactor: comp})
	events := loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleSystem, Content: "test"},
		{Role: llm.RoleUser, Content: "hello"},
	})

	var loopDone *LoopDone
	for ev := range events {
		if ts, ok := ev.(TurnStats); ok {
			if !ts.Compaction.HardLimitReached {
				t.Fatal("expected HardLimitReached in TurnStats")
			}
		}
		if ld, ok := ev.(LoopDone); ok {
			loopDone = &ld
		}
	}
	if loopDone == nil {
		t.Fatal("expected LoopDone event")
	}
	if loopDone.Reason != ReasonModelError {
		t.Errorf("expected ReasonModelError, got %s", loopDone.Reason)
	}
	if loopDone.Err == nil || loopDone.Err.Error() != "usage" {
		t.Errorf("expected error 'usage', got %v", loopDone.Err)
	}
}

func TestRunWithCompactor_Tier3SummaryDone(t *testing.T) {
	comp := &mockCompactor{
		tick: compaction.Tick{
			Tier:             3,
			Tier3SummaryDone: true,
			TokensSaved:      100000,
			ContextLimit:     1000000,
		},
	}

	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponseWithUsage("working", 650000, makeToolCall("c1", "test_tool", `{}`)),
			{
				Content: "summarized",
				Usage:   &llm.UsageInfo{PromptTokens: 960000},
			},
		},
	}

	loop := New(client, newTestRegistry(newSuccessTool("test_tool", true, "ok")), Config{MaxTurns: 3, Compactor: comp})
	events := loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleSystem, Content: "test"},
		{Role: llm.RoleUser, Content: "hello"},
	})

	for ev := range events {
		if ts, ok := ev.(TurnStats); ok {
			if !ts.Compaction.SummaryDone {
				t.Fatal("expected SummaryDone true for Tier 3")
			}
			if ts.Compaction.Tier != 3 {
				t.Errorf("expected Tier 3, got %d", ts.Compaction.Tier)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 连续空响应 → abort
// ---------------------------------------------------------------------------

func TestRunEmptyResponseConsecutiveAbort(t *testing.T) {
	// 模拟 LLM 连续返回 4 次纯 reasoning（无 content、无 tool_calls）
	client := &mockLLMClient{
		responses: makeResponses(5, "", nil), // 5 次空响应
	}
	client.responses[0].Usage = &llm.UsageInfo{PromptTokens: 100}

	loop := New(client, newTestRegistry(), Config{MaxTurns: 0})
	events := loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleSystem, Content: "test"},
		{Role: llm.RoleUser, Content: "go"},
	})

	var done *LoopDone
	for ev := range events {
		if ld, ok := ev.(LoopDone); ok {
			done = &ld
		}
	}
	if done == nil {
		t.Fatal("expected LoopDone")
	}
	if done.Reason != ReasonModelError {
		t.Fatalf("expected ReasonModelError, got %s", done.Reason)
	}
}

func TestRunEmptyResponse_RecoversAfterNonEmpty(t *testing.T) {
	// 3 次空 → 1 次有内容 → 1 次空（计数器应被重置）
	client := &mockLLMClient{
		responses: []*llm.Response{
			{Content: "", Usage: &llm.UsageInfo{PromptTokens: 100}},           // empty 1
			{Content: "", Usage: &llm.UsageInfo{PromptTokens: 100}},           // empty 2
			{Content: "", Usage: &llm.UsageInfo{PromptTokens: 100}},           // empty 3
			makeToolCallResponse("working", makeToolCall("c1", "test_tool", `{}`)), // tool call → 有效
			{Content: "done", Usage: &llm.UsageInfo{PromptTokens: 100}},       // 完成
		},
	}
	client.responses[3].Usage = &llm.UsageInfo{PromptTokens: 100}

	loop := New(client, newTestRegistry(newSuccessTool("test_tool", true, "ok")), Config{MaxTurns: 0})
	events := loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleSystem, Content: "test"},
		{Role: llm.RoleUser, Content: "go"},
	})

	var done *LoopDone
	for ev := range events {
		if ld, ok := ev.(LoopDone); ok {
			done = &ld
		}
	}
	if done == nil {
		t.Fatal("expected LoopDone")
	}
	if done.Reason != ReasonCompleted {
		t.Fatalf("expected ReasonCompleted (counter reset), got %s", done.Reason)
	}
}

// ---------------------------------------------------------------------------
// 流错误 → 回退成功 / 回退失败
// ---------------------------------------------------------------------------

func TestRunStreamError_FallbackSuccess(t *testing.T) {
	// 流中注入非 cancel 错误，mock 的 SendMessage 返回正常响应
	streamErr := fmt.Errorf("connection reset")
	client := &mockLLMClient{
		responses: []*llm.Response{
			{Content: "fallback response", Usage: &llm.UsageInfo{PromptTokens: 500}},
		},
		streamErrors: []error{streamErr},
	}

	loop := New(client, newTestRegistry(), Config{MaxTurns: 2})
	events := loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleSystem, Content: "test"},
		{Role: llm.RoleUser, Content: "hello"},
	})

	var done *LoopDone
	for ev := range events {
		if ld, ok := ev.(LoopDone); ok {
			done = &ld
		}
	}
	if done == nil {
		t.Fatal("expected LoopDone")
	}
	if done.Reason != ReasonCompleted {
		t.Fatalf("expected ReasonCompleted after fallback success, got %s", done.Reason)
	}
}

func TestRunStreamError_FallbackFailure(t *testing.T) {
	// 流中错误 + SendMessage 也失败 → 终止
	streamErr := fmt.Errorf("connection reset")
	sendMsgErr := fmt.Errorf("fallback failed")
	client := &mockLLMClient{
		responses:    []*llm.Response{nil},
		errors:       []error{nil, sendMsgErr}, // SendMessageStream ok, SendMessage fails
		streamErrors: []error{streamErr},
	}

	loop := New(client, newTestRegistry(), Config{MaxTurns: 2})
	events := loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleSystem, Content: "test"},
		{Role: llm.RoleUser, Content: "hello"},
	})

	var done *LoopDone
	for ev := range events {
		if ld, ok := ev.(LoopDone); ok {
			done = &ld
		}
	}
	if done == nil {
		t.Fatal("expected LoopDone")
	}
	if done.Reason != ReasonModelError {
		t.Fatalf("expected ReasonModelError after fallback failure, got %s", done.Reason)
	}
}

// ---------------------------------------------------------------------------
// context.DeadlineExceeded 在流消费中触发
// ---------------------------------------------------------------------------

func TestRunStreamDeadlineExceeded(t *testing.T) {
	streamErr := context.DeadlineExceeded
	client := &mockLLMClient{
		streamErrors: []error{streamErr},
	}

	loop := New(client, newTestRegistry(), Config{MaxTurns: 2})
	events := loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleSystem, Content: "test"},
		{Role: llm.RoleUser, Content: "hello"},
	})

	var done *LoopDone
	for ev := range events {
		if ld, ok := ev.(LoopDone); ok {
			done = &ld
		}
	}
	if done == nil {
		t.Fatal("expected LoopDone")
	}
	if done.Reason != ReasonAborted {
		t.Fatalf("expected ReasonAborted for deadline exceeded, got %s", done.Reason)
	}
}

// ---------------------------------------------------------------------------
// Tool 返回 (nil, nil) → ReasonToolFatal
// ---------------------------------------------------------------------------

func TestRunToolNilResult(t *testing.T) {
	nilTool := &mockTool{
		name:           "nil_tool",
		concurrentSafe: true,
		result:         nil, // 返回 nil 结果
		execErr:        nil, // 无 Go error
	}

	client := &mockLLMClient{
		responses: []*llm.Response{
			makeToolCallResponseWithUsage("working", 500, makeToolCall("c1", "nil_tool", `{}`)),
		},
	}

	loop := New(client, newTestRegistry(nilTool), Config{MaxTurns: 2})
	events := loop.Run(context.Background(), []llm.Message{
		{Role: llm.RoleSystem, Content: "test"},
		{Role: llm.RoleUser, Content: "go"},
	})

	var done *LoopDone
	for ev := range events {
		if ld, ok := ev.(LoopDone); ok {
			done = &ld
		}
	}
	if done == nil {
		t.Fatal("expected LoopDone")
	}
	// nilTool 在 serial 路径执行（非 concurrentSafe？不，concurrentSafe=true）
	// 但 execute 返回 (nil, nil) → serial path 检查 result == nil → ReasonToolFatal
	if done.Reason != ReasonToolFatal {
		t.Fatalf("expected ReasonToolFatal for nil result, got %s", done.Reason)
	}
}

// makeResponses 生成 n 个内容相同的简单响应。
func makeResponses(n int, content string, calls []llm.ToolCall) []*llm.Response {
	resps := make([]*llm.Response, n)
	for i := range resps {
		resps[i] = &llm.Response{Content: content, ToolCalls: calls}
	}
	return resps
}
