package acp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/mcp"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// mockLLMClient — 最小 mock，handler 测试不需要真实的 LLM 调用
// ---------------------------------------------------------------------------

type mockLLMClient struct{}

func (m *mockLLMClient) SendMessage(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (*llm.Response, error) {
	return &llm.Response{Content: "mock response"}, nil
}
func (m *mockLLMClient) SendMessageStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (<-chan llm.StreamingEvent, error) {
	// 最小流:单事件 "mock response" 后结束——Loop 正常流式完成路径
	// (原 panic 实现导致 Loop 走 panic 恢复 → ReasonToolFatal → 假绿 end_turn)
	ch := make(chan llm.StreamingEvent, 2)
	ch <- llm.StreamingEvent{Delta: "mock response", Done: true}
	close(ch)
	return ch, nil
}
func (m *mockLLMClient) GetBalance(ctx context.Context) (*llm.BalanceInfo, error) {
	return nil, nil
}
func (m *mockLLMClient) SupportsBalance() bool { return false }
func (m *mockLLMClient) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// testServer — 测试用 Server 构造器
// ---------------------------------------------------------------------------

func newTestServer() *Server {
	registry := tool.NewRegistry()
	// 注册最小工具集（handleSessionNew 需要 Registry 非空以创建 Loop）
	registry.Register(tool.Wrap(&tool.ReadFile{}))

	return NewServer(ServerConfig{
		LLMClient:    &mockLLMClient{},
		ToolRegistry: registry,
		SystemPrompt: "You are a helpful assistant.",
		BuildVersion: "test",
		CWD:          "/tmp",
		MaxTurns:     10,
	})
}

// captureTransport 返回一个写入 strings.Builder 的 transport 替代方案。
// Server 使用 transport.Send 写响应，通过替换 transport 为捕获版本实现可观测。
func withCaptureTransport(s *Server) (responses func() []json.RawMessage) {
	var buf strings.Builder
	capTransport := NewStdioTransportIO(strings.NewReader(""), &buf)
	s.transport = capTransport

	return func() []json.RawMessage {
		output := buf.String()
		lines := strings.Split(strings.TrimSpace(output), "\n")
		var result []json.RawMessage
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				result = append(result, json.RawMessage(line))
			}
		}
		return result
	}
}

// ---------------------------------------------------------------------------
// handleInitialize 测试
// ---------------------------------------------------------------------------

func TestHandleInitialize(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  MethodInitialize,
	}
	s.handleInitialize(req)

	if !s.initialized {
		t.Error("initialized flag should be set after handleInitialize")
	}

	responses := getResp()
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(responses[0], &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if result.ProtocolVersion != 1 {
		t.Errorf("protocolVersion = %d, want 1", result.ProtocolVersion)
	}
	if result.AgentInfo.Name != "waveloom" {
		t.Errorf("agent name = %q, want %q", result.AgentInfo.Name, "waveloom")
	}
	if !result.AgentCapabilities.LoadSession {
		t.Error("LoadSession should be true (session persistence implemented)")
	}
	if !result.AgentCapabilities.PromptCapabilities.EmbeddedContext {
		t.Error("EmbeddedContext should be true")
	}
	if result.AgentCapabilities.SessionCapabilities == nil {
		t.Fatal("SessionCapabilities should be declared (resume/close/list/delete)")
	}
	if result.AgentCapabilities.SessionCapabilities.Resume == nil ||
		result.AgentCapabilities.SessionCapabilities.Close == nil ||
		result.AgentCapabilities.SessionCapabilities.List == nil ||
		result.AgentCapabilities.SessionCapabilities.Delete == nil {
		t.Error("SessionCapabilities should include resume/close/list/delete")
	}
	if result.AgentCapabilities.McpCapabilities == nil ||
		!result.AgentCapabilities.McpCapabilities.HTTP ||
		!result.AgentCapabilities.McpCapabilities.SSE {
		t.Error("McpCapabilities should declare http + sse (MCP support implemented)")
	}
	// Terminal 认证声明(注册表 CI auth-check 要求 authMethods 非空,
	// 且至少一个方法 type 为 "agent"/"terminal")
	if len(result.AuthMethods) != 1 {
		t.Fatalf("authMethods: expected 1 terminal method, got %d", len(result.AuthMethods))
	}
	m := result.AuthMethods[0]
	if m.Type != "terminal" || m.ID == "" || m.Name == "" {
		t.Errorf("auth method: type=%q id=%q name=%q, want terminal auth with id/name", m.Type, m.ID, m.Name)
	}
	if len(m.Args) != 1 || m.Args[0] != "setup" {
		t.Errorf("auth method args = %v, want [setup] (waveloom acp setup)", m.Args)
	}
	// Zed 兼容扩展:stable Zed(acp-beta flag 关闭)点击登录按钮只解析
	// _meta.terminal-auth {label, command, args, env} 构造登录终端。
	ta, ok := m.Meta["terminal-auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth method meta: want _meta.terminal-auth for Zed compatibility, got %#v", m.Meta)
	}
	if ta["label"] != "waveloom acp setup" || ta["command"] != "waveloom" {
		t.Errorf("meta.terminal-auth label/command = %q/%q, want waveloom acp setup/waveloom",
			ta["label"], ta["command"])
	}
	// 经 JSON round-trip 后 args 为 []any
	if args, ok := ta["args"].([]any); !ok || len(args) != 1 || args[0] != "setup" {
		t.Errorf("meta.terminal-auth args = %#v, want [setup] (waveloom setup)", ta["args"])
	}
}

func TestHandleInitializeInvalidParams(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  MethodInitialize,
		Params:  json.RawMessage(`{"protocolVersion": "not-a-number"}`),
	}
	s.handleInitialize(req)

	responses := getResp()
	if len(responses) != 1 {
		t.Fatalf("expected 1 error response, got %d", len(responses))
	}
	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[0], &resp)
	if resp.Error == nil {
		t.Fatal("expected error response for invalid params")
	}
	if resp.Error.Code != ErrInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, ErrInvalidParams)
	}
}

// ---------------------------------------------------------------------------
// handleSessionNew 测试
// ---------------------------------------------------------------------------

func TestHandleSessionNew(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  MethodSessionNew,
	}
	s.handleSessionNew(req)

	responses := getResp()
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}

	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[0], &resp)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var result SessionNewResult
	_ = json.Unmarshal(resp.Result, &result)
	if result.SessionID == "" {
		t.Error("sessionId should not be empty")
	}

	// Verify session is in registry
	s.mu.RLock()
	state, ok := s.sessions[result.SessionID]
	s.mu.RUnlock()
	if !ok {
		t.Fatal("session not found in registry")
	}
	if state.CM == nil {
		t.Error("ContextManager should not be nil")
	}
	if state.Loop == nil {
		t.Error("Loop should not be nil")
	}
	if state.Guard == nil {
		t.Error("Guard should not be nil")
	}
}

func TestHandleSessionNewInjectedGuard(t *testing.T) {
	// ACP 入口注入的 guard(已 EnableAutoAllow 二元决策)必须被 session 复用,
	// 而不是每个 session 新建——保证全局+项目规则与 autoAllow 对所有 session 生效。
	injected := permission.NewGuard(permission.WithBypassMode(false))
	s := newTestServer()
	s.guard = injected

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  MethodSessionNew,
	}
	s.handleSessionNew(req)

	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(s.sessions))
	}
	for _, state := range s.sessions {
		if state.Guard != injected {
			t.Error("session guard should be the injected instance (shared)")
		}
	}
}

func TestHandleSessionNewFallbackGuard(t *testing.T) {
	// 无注入(ServerConfig.Guard == nil)→ fallback 裸 Guard,保持 fail-closed
	// 行为(非二元决策:write/execute 默认 ASK → 无 responder → deny)。
	s := newTestServer() // ServerConfig 不含 Guard

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  MethodSessionNew,
	}
	s.handleSessionNew(req)

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, state := range s.sessions {
		if state.Guard == nil {
			t.Fatal("fallback guard must not be nil")
		}
		if state.Guard == s.guard {
			t.Error("fallback guard must be a fresh instance, not s.guard (nil)")
		}
	}
}

func TestHandleSessionNewTodoState(t *testing.T) {
	// REGRESSION: ACP Loop 必须配置 session 级 TodoState,否则
	// todo_create/todo_update 返回 "not available"(execute.go executeTodoMutate)
	// 且每轮 todo 摘要注入失效——todo 工具注册但必挂。
	s := newTestServer()
	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, state := range s.sessions {
		if state.Loop == nil {
			t.Fatal("Loop should not be nil")
		}
		if state.Loop.TodoState() == nil {
			t.Error("Loop must have a session-level TodoState configured")
		}
	}
}

func TestHandleSessionNewCompactor(t *testing.T) {
	// REGRESSION: ACP Loop 曾未配置 Compactor——上下文压缩被跳过,长会话
	// 无硬限,usage_update.size 上报值无人执行。修复后 Loop 必须持有压缩器,
	// 且窗口容量与入口解析值一致。
	s := newTestServer()
	s.compactionConfig = compaction.DefaultCompactionConfig()
	s.compactionConfig.ContextLimit = 200_000
	s.contextLimit = 200_000

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, state := range s.sessions {
		if state.Loop == nil || state.Loop.Compactor() == nil {
			t.Fatal("Loop must have a Compactor configured (compaction enabled)")
		}
		if state.CM.Compactor() == nil {
			t.Fatal("ContextManager must have a compactor")
		}
	}
}

// fakeCommandRunner 是 CommandRunner 的测试替身。
type fakeCommandRunner struct {
	resultText string
	injected   string
	handled    bool
	cmds       []AvailableCommand
}

func (f *fakeCommandRunner) Run(ctx context.Context, input string) (string, string, bool) {
	return f.resultText, f.injected, f.handled
}

func (f *fakeCommandRunner) AvailableCommands() []AvailableCommand {
	return f.cmds
}

func TestHandleSessionNewSendsAvailableCommands(t *testing.T) {
	// session/new 后发送 available_commands_update(客户端命令面板)
	s := newTestServer()
	getResp := withCaptureTransport(s)
	s.commandRunner = &fakeCommandRunner{cmds: []AvailableCommand{
		{Name: "help", Description: "Show help"},
		{Name: "model", Description: "Switch model", Input: &AvailableCommandInput{Kind: "unstructured"}},
	}}

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})

	responses := getResp()
	var found bool
	for _, raw := range responses {
		var notif JSONRPCNotification
		if json.Unmarshal(raw, &notif) != nil || notif.Method != MethodSessionUpdate {
			continue
		}
		if strings.Contains(string(notif.Params), "available_commands_update") &&
			strings.Contains(string(notif.Params), `"help"`) {
			found = true
		}
	}
	if !found {
		t.Error("session/new should send available_commands_update with commands")
	}
}

func TestExecutePromptSlashCommandTextReply(t *testing.T) {
	// 纯命令(/help 等)→ 文本回复 + end_turn,不调用 LLM
	s := newTestServer()
	getResp := withCaptureTransport(s)
	s.commandRunner = &fakeCommandRunner{resultText: "Available commands: /help /model", handled: true}

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])

	s.handleSessionPrompt(context.Background(), JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionPrompt,
		Params: json.RawMessage(`{"sessionId":"` + sid + `","prompt":[{"type":"text","text":"/help"}]}`)})

	s.wg.Wait()

	responses := getResp()
	var msgChunk, promptResp bool
	for _, raw := range responses {
		var notif JSONRPCNotification
		if json.Unmarshal(raw, &notif) == nil && notif.Method == MethodSessionUpdate {
			if strings.Contains(string(notif.Params), "agent_message_chunk") &&
				strings.Contains(string(notif.Params), "Available commands") {
				msgChunk = true
			}
			continue
		}
		var resp JSONRPCResponse
		if json.Unmarshal(raw, &resp) == nil && resp.ID == float64(2) && resp.Error == nil {
			var result SessionPromptResult
			if json.Unmarshal(resp.Result, &result) == nil && result.StopReason == "end_turn" {
				promptResp = true
			}
		}
	}
	if !msgChunk {
		t.Error("slash command should reply with agent_message_chunk text")
	}
	if !promptResp {
		t.Error("slash command should respond end_turn without LLM")
	}
}

func TestExecutePromptSlashCommandSkillInjection(t *testing.T) {
	// skill 命令 → 注入指令文本 → 走 LLM(不是直接回复)
	s := newTestServer()
	getResp := withCaptureTransport(s)
	s.commandRunner = &fakeCommandRunner{injected: "SKILL BODY: run tests\n\nTask", handled: true}

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])

	s.handleSessionPrompt(context.Background(), JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionPrompt,
		Params: json.RawMessage(`{"sessionId":"` + sid + `","prompt":[{"type":"text","text":"/test-acp"}]}`)})

	s.wg.Wait()

	// 注入路径走 LLM:mock 返回 "mock response" → agent_message_chunk 文本
	responses := getResp()
	var found bool
	for _, raw := range responses {
		var notif JSONRPCNotification
		if json.Unmarshal(raw, &notif) == nil && notif.Method == MethodSessionUpdate &&
			strings.Contains(string(notif.Params), "agent_message_chunk") &&
			strings.Contains(string(notif.Params), "mock response") {
			found = true
		}
	}
	if !found {
		t.Error("skill-injected prompt should go through LLM (mock response expected)")
	}
}

func TestExecutePromptUserMessageEchoAndTitle(t *testing.T) {
	// 回显 user_message_chunk(实际处理的 prompt)+ 首次 prompt 设置 session 标题
	s := newTestServer()
	getResp := withCaptureTransport(s)

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])

	s.handleSessionPrompt(context.Background(), JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionPrompt,
		Params: json.RawMessage(`{"sessionId":"` + sid + `","prompt":[{"type":"text","text":"explain this code"}]}`)})
	s.wg.Wait()

	responses := getResp()
	var userEcho, titleSet bool
	var titleNotifs int
	for _, raw := range responses {
		var notif JSONRPCNotification
		if json.Unmarshal(raw, &notif) != nil || notif.Method != MethodSessionUpdate {
			continue
		}
		params := string(notif.Params)
		if strings.Contains(params, "user_message_chunk") && strings.Contains(params, "explain this code") {
			userEcho = true
		}
		if strings.Contains(params, "session_info_update") {
			titleNotifs++
			if strings.Contains(params, "explain this code") {
				titleSet = true
			}
		}
	}
	if !userEcho {
		t.Error("prompt should echo user_message_chunk with actual text")
	}
	if !titleSet {
		t.Error("first prompt should set session title (session_info_update)")
	}

	// 第二次 prompt 不再重复设置标题
	s.handleSessionPrompt(context.Background(), JSONRPCRequest{JSONRPC: "2.0", ID: 3, Method: MethodSessionPrompt,
		Params: json.RawMessage(`{"sessionId":"` + sid + `","prompt":[{"type":"text","text":"second prompt"}]}`)})
	s.wg.Wait()
	totalAfter := 0
	for _, raw := range getResp() {
		var notif JSONRPCNotification
		if json.Unmarshal(raw, &notif) == nil && notif.Method == MethodSessionUpdate &&
			strings.Contains(string(notif.Params), "session_info_update") {
			totalAfter++
		}
	}
	if totalAfter != titleNotifs {
		t.Errorf("session title should be set only once, total = %d, want %d", totalAfter, titleNotifs)
	}
}

func TestExecutePromptAuthRequired(t *testing.T) {
	// 未配置 LLM(终端认证场景):session/prompt 返回 -32000 AUTH_REQUIRED,
	// 不启动 Loop;斜杠命令等无需 LLM 的路径不受影响。
	s := newTestServer()
	s.llmClient = nil
	getResp := withCaptureTransport(s)

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])

	s.handleSessionPrompt(context.Background(), JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionPrompt,
		Params: json.RawMessage(`{"sessionId":"` + sid + `","prompt":[{"type":"text","text":"explain this code"}]}`)})
	s.wg.Wait()

	var authErr *JSONRPCError
	for _, raw := range getResp() {
		var resp JSONRPCResponse
		if json.Unmarshal(raw, &resp) == nil && resp.ID == float64(2) {
			authErr = resp.Error
		}
	}
	if authErr == nil {
		t.Fatal("expected AUTH_REQUIRED error response for unconfigured LLM")
	}
	if authErr.Code != ErrAuthRequired {
		t.Errorf("error code = %d, want %d (AUTH_REQUIRED)", authErr.Code, ErrAuthRequired)
	}
}

func TestTruncateTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"short", "short"},
		{"first line\nsecond line", "first line"},
		{"a" + strings.Repeat("b", 70), "a" + strings.Repeat("b", 59) + "…"},
	}
	for _, tc := range cases {
		if got := truncateTitle(tc.in); got != tc.want {
			t.Errorf("truncateTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHandleSessionNewCreatesUniqueIDs(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)

	req := JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew}
	s.handleSessionNew(req)
	req2 := JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionNew}
	s.handleSessionNew(req2)

	responses := getResp()
	if len(responses) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(responses))
	}

	var r1, r2 SessionNewResult
	_ = json.Unmarshal(getRawResult(t, responses[0]), &r1)
	_ = json.Unmarshal(getRawResult(t, responses[1]), &r2)

	if r1.SessionID == r2.SessionID {
		t.Error("session IDs should be unique")
	}
}

// ---------------------------------------------------------------------------
// handleSessionPrompt 测试（错误路径）
// ---------------------------------------------------------------------------

func TestHandleSessionPromptSessionNotFound(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  MethodSessionPrompt,
		Params:  json.RawMessage(`{"sessionId":"nonexistent","prompt":[{"type":"text","text":"hello"}]}`),
	}
	s.handleSessionPrompt(context.Background(), req)

	responses := getResp()
	if len(responses) != 1 {
		t.Fatalf("expected 1 error response, got %d", len(responses))
	}
	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[0], &resp)
	if resp.Error == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if resp.Error.Code != ErrSessionNotFound {
		t.Errorf("error code = %d, want %d (ResourceNotFound)", resp.Error.Code, ErrSessionNotFound)
	}
	if !strings.Contains(resp.Error.Message, "not found") {
		t.Errorf("error message should mention 'not found', got: %s", resp.Error.Message)
	}
}

func TestHandleSessionPromptInvalidParams(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  MethodSessionPrompt,
		Params:  json.RawMessage(`{invalid`),
	}
	s.handleSessionPrompt(context.Background(), req)

	responses := getResp()
	if len(responses) != 1 {
		t.Fatalf("expected 1 error response, got %d", len(responses))
	}
	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[0], &resp)
	if resp.Error == nil {
		t.Fatal("expected parse error")
	}
}

func TestHandleSessionPromptBusySession(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)

	// Create a session first
	reqNew := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  MethodSessionNew,
	}
	s.handleSessionNew(reqNew)
	responses := getResp()
	sessionID := getSessionID(t, responses[0])

	// Lock the session's promptMu to simulate busy state
	s.mu.RLock()
	state := s.sessions[sessionID]
	s.mu.RUnlock()
	state.promptMu.Lock() // manually lock to simulate prompt in progress
	defer state.promptMu.Unlock()

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  MethodSessionPrompt,
		Params:  json.RawMessage(`{"sessionId":"` + sessionID + `","prompt":[{"type":"text","text":"hello"}]}`),
	}
	s.handleSessionPrompt(context.Background(), req)

	// Wait a bit for the goroutine
	allResp := getResp()
	// Should have the error response for busy session
	var foundBusy bool
	for _, raw := range allResp {
		var resp JSONRPCResponse
		_ = json.Unmarshal(raw, &resp)
		if resp.Error != nil && resp.Error.Code == ErrSessionBusy {
			foundBusy = true
			break
		}
	}
	if !foundBusy {
		t.Error("expected session busy error")
	}
}

func TestExecutePromptEmpty(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)

	// Create session first
	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	initResp := getResp()
	sid := getSessionID(t, initResp[0])

	// Lock promptMu to simulate ownership
	s.mu.RLock()
	state := s.sessions[sid]
	s.mu.RUnlock()
	state.promptMu.Lock()

	// executePrompt 内 defer s.wg.Done(),需提前 Add
	s.wg.Add(1)

	// Call executePrompt with empty prompt — should return error without touching Loop
	params := SessionPromptParams{
		SessionID: sid,
		Prompt:    []ContentBlock{}, // empty
	}
	s.executePrompt(context.Background(), 2, state, params)

	// executePrompt sends error response for empty prompt
	responses := getResp()
	var found bool
	for _, raw := range responses {
		var resp JSONRPCResponse
		_ = json.Unmarshal(raw, &resp)
		if resp.Error != nil && resp.Error.Code == ErrInvalidParams &&
			strings.Contains(resp.Error.Message, "empty") {
			found = true
		}
	}
	if !found {
		t.Error("expected 'prompt is empty' error response")
	}
}

// ---------------------------------------------------------------------------
// dispatch 路由测试
// ---------------------------------------------------------------------------

func TestDispatchInitializeGuard(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)

	// Try session/new before initialize
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  MethodSessionNew,
	}
	s.dispatch(context.Background(), req)

	responses := getResp()
	if len(responses) != 1 {
		t.Fatalf("expected 1 error response, got %d", len(responses))
	}
	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[0], &resp)
	if resp.Error == nil {
		t.Fatal("expected error for pre-initialize request")
	}
	if resp.Error.Code != ErrInvalidRequest {
		t.Errorf("error code = %d, want %d", resp.Error.Code, ErrInvalidRequest)
	}

	// Now initialize
	getResp2 := withCaptureTransport(s)
	initReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  MethodInitialize,
	}
	s.dispatch(context.Background(), initReq)
	if !s.initialized {
		t.Error("should be initialized after handleInitialize")
	}

	// After initialize, session/new should work
	responses2 := getResp2()
	_ = responses2 // initialized response
}

func TestDispatchUnknownMethod(t *testing.T) {
	s := newTestServer()
	// Manual initialized=true
	s.initialized = true

	getResp := withCaptureTransport(s)

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "nonexistent/method",
	}
	s.dispatch(context.Background(), req)

	responses := getResp()
	if len(responses) != 1 {
		t.Fatalf("expected 1 error response, got %d", len(responses))
	}
	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[0], &resp)
	if resp.Error == nil || resp.Error.Code != ErrMethodNotFound {
		t.Fatal("expected method not found error")
	}
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

func getRawResult(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var resp JSONRPCResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp.Result
}

func getSessionID(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var result SessionNewResult
	if err := json.Unmarshal(getRawResult(t, raw), &result); err != nil {
		t.Fatalf("unmarshal session new result: %v", err)
	}
	return result.SessionID
}

// ---------------------------------------------------------------------------
// Session 生命周期测试(B2)
// ---------------------------------------------------------------------------

func TestHandleSessionCloseRemovesSession(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)
	sessionDir := t.TempDir()
	s.sessionDir = sessionDir

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])

	// close → 从注册表移除
	s.handleSessionClose(JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionClose,
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`)})
	s.mu.RLock()
	_, ok := s.sessions[sid]
	s.mu.RUnlock()
	if ok {
		t.Fatal("session should be removed after close")
	}

	// close 后 prompt → -32002 ResourceNotFound(原实现 -32602)
	s.handleSessionPrompt(context.Background(), JSONRPCRequest{JSONRPC: "2.0", ID: 3, Method: MethodSessionPrompt,
		Params: json.RawMessage(`{"sessionId":"` + sid + `","prompt":[{"type":"text","text":"hi"}]}`)})
	responses := getResp()
	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[len(responses)-1], &resp)
	if resp.Error == nil || resp.Error.Code != ErrSessionNotFound {
		t.Errorf("prompt after close: error code = %v, want %d", resp.Error, ErrSessionNotFound)
	}
}

func TestHandleSessionList(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)
	sessionDir := t.TempDir()
	s.sessionDir = sessionDir

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])

	// 磁盘上放一个跨进程 session 文件
	diskID := "disk-session-0001"
	if err := os.WriteFile(filepath.Join(sessionDir, diskID+".json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write disk session: %v", err)
	}

	s.handleSessionList(JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionList})
	responses := getResp()
	var listResp JSONRPCResponse
	_ = json.Unmarshal(responses[len(responses)-1], &listResp)
	var result SessionListResult
	if err := json.Unmarshal(listResp.Result, &result); err != nil {
		t.Fatalf("unmarshal list result: %v", err)
	}

	found := map[string]bool{}
	for _, item := range result.Sessions {
		found[item.SessionID] = true
	}
	if !found[sid] {
		t.Errorf("in-memory session %q missing from list", sid)
	}
	if !found[diskID] {
		t.Errorf("disk session %q missing from list", diskID)
	}
}

func TestHandleSessionLoadRestoresHistory(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)
	sessionDir := t.TempDir()
	s.sessionDir = sessionDir

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])

	// 触发落盘(SetSessionPath 已在 session/new 配置,CompleteRun 自动保存)
	s.mu.RLock()
	state := s.sessions[sid]
	s.mu.RUnlock()
	_ = state.CM.CompleteRun(
		[]llm.Message{{Role: llm.RoleUser, Content: "persisted hello"}},
		10, 0, 5, 0, 0, 0, "", 0, "end_turn",
	)
	if _, err := os.Stat(filepath.Join(sessionDir, sid+".json")); err != nil {
		t.Fatalf("session file should be persisted: %v", err)
	}

	// close 后 load → 消息历史恢复
	s.handleSessionClose(JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionClose,
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`)})
	s.handleSessionLoad(JSONRPCRequest{JSONRPC: "2.0", ID: 3, Method: MethodSessionLoad,
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`)})

	s.mu.RLock()
	loaded, ok := s.sessions[sid]
	s.mu.RUnlock()
	if !ok {
		t.Fatal("session should be re-registered after load")
	}
	found := false
	for _, m := range loaded.CM.Messages() {
		if m.Role == llm.RoleUser && m.Content == "persisted hello" {
			found = true
		}
	}
	if !found {
		t.Error("loaded session should contain persisted message history")
	}

	// load 回放历史为 session/update 通知(user_message_chunk)
	responses := getResp()
	var replayed bool
	for _, raw := range responses {
		var notif JSONRPCNotification
		if json.Unmarshal(raw, &notif) != nil || notif.Method != MethodSessionUpdate {
			continue
		}
		if strings.Contains(string(notif.Params), "user_message_chunk") {
			replayed = true
		}
	}
	if !replayed {
		t.Error("session/load should replay history as session/update notifications")
	}
}

func TestHandleSessionLoadReappliesContextLimit(t *testing.T) {
	// REGRESSION: LoadFromFile 恢复磁盘持久化的 watermark.ContextLimit 后,
	// 曾未重放当前配置——用户改 --context-limit/settings 后恢复会话的压缩
	// 阈值仍是旧值。修复:load 后 SetContextLimit 重放。
	s := newTestServer()
	getResp := withCaptureTransport(s)
	sessionDir := t.TempDir()
	s.sessionDir = sessionDir
	s.compactionConfig = compaction.DefaultCompactionConfig()
	s.compactionConfig.ContextLimit = 200_000
	s.contextLimit = 200_000

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])

	// 落盘(旧阈值 200k 写入 watermark)
	s.mu.RLock()
	state := s.sessions[sid]
	s.mu.RUnlock()
	_ = state.CM.CompleteRun(
		[]llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		1, 0, 0, 0, 0, 0, "", 0, "end_turn",
	)

	// 配置改为 128k 后 load → 恢复的 compactor 阈值必须是新值
	s.contextLimit = 128_000
	s.handleSessionClose(JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionClose,
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`)})
	s.handleSessionLoad(JSONRPCRequest{JSONRPC: "2.0", ID: 3, Method: MethodSessionLoad,
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`)})

	s.mu.RLock()
	loaded := s.sessions[sid]
	s.mu.RUnlock()
	tc, ok := loaded.CM.Compactor().(*compaction.TieredCompactor)
	if !ok {
		t.Fatalf("compactor type = %T, want *TieredCompactor", loaded.CM.Compactor())
	}
	if got := tc.ContextLimit(); got != 128_000 {
		t.Errorf("restored ContextLimit = %d, want 128000 (reapplied)", got)
	}
}

func TestHandleSessionResumeNoReplay(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)
	sessionDir := t.TempDir()
	s.sessionDir = sessionDir

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])
	s.mu.RLock()
	state := s.sessions[sid]
	s.mu.RUnlock()
	_ = state.CM.CompleteRun(
		[]llm.Message{{Role: llm.RoleUser, Content: "resume me"}},
		1, 0, 0, 0, 0, 0, "", 0, "end_turn",
	)
	s.handleSessionClose(JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionClose,
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`)})
	before := len(getResp())

	s.handleSessionResume(JSONRPCRequest{JSONRPC: "2.0", ID: 3, Method: MethodSessionResume,
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`)})

	// resume 不回放:close 后不应出现新的 session/update 通知
	for _, raw := range getResp()[before:] {
		var notif JSONRPCNotification
		if json.Unmarshal(raw, &notif) == nil && notif.Method == MethodSessionUpdate {
			t.Error("session/resume must not replay history notifications")
		}
	}
}

// ---------------------------------------------------------------------------
// embeddedContext 提取测试(C1)
// ---------------------------------------------------------------------------

func TestExtractPromptTextEmbeddedContext(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(filePath, []byte("file content"), 0o644); err != nil {
		t.Fatalf("write note file: %v", err)
	}

	blocks := []ContentBlock{
		{Type: "text", Text: "prefix"},
		{Type: "resource", Resource: &EmbeddedResource{Text: "embedded text", URI: "res://1"}},
		{Type: "resource", Resource: &EmbeddedResource{Blob: base64.StdEncoding.EncodeToString([]byte("blob text")), URI: "res://2"}},
		{Type: "resource_link", URI: "file://" + filePath},
	}

	got := extractPromptText(blocks, dir)
	want := "prefix\nembedded text\nblob text\nfile content"
	if got != want {
		t.Errorf("extractPromptText = %q, want %q", got, want)
	}
}

func TestExtractPromptTextResourceLinkMissing(t *testing.T) {
	// resource_link 读取失败 → 错误说明追加,不阻断 prompt(embeddedContext 承诺)
	got := extractPromptText([]ContentBlock{
		{Type: "resource_link", URI: "file:///nonexistent/acp-test-file"},
	}, t.TempDir())
	if !strings.Contains(got, "resource_link") || !strings.Contains(got, "nonexistent") {
		t.Errorf("expected error note for missing resource_link, got %q", got)
	}
}

func TestExtractPromptTextResourceLinkOutsideWorkspace(t *testing.T) {
	// 安全边界:resource_link 只能读工作区内文件(防任意文件读入 LLM 上下文)
	got := extractPromptText([]ContentBlock{
		{Type: "resource_link", URI: "file:///etc/passwd"},
	}, t.TempDir())
	if !strings.Contains(got, "outside workspace") {
		t.Errorf("expected workspace boundary error, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// cancel 竞态测试(D2)
// ---------------------------------------------------------------------------

func TestHandleSessionCancelBeforePrompt(t *testing.T) {
	// REGRESSION: prompt goroutine 已启动但 cancelFn 未设置的窗口期到达的
	// cancel 曾被静默丢弃。修复:cancelPending 置位,executePrompt 设置
	// cancelFn 后消费并立即取消。
	s := newTestServer()
	getResp := withCaptureTransport(s)

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])
	s.mu.RLock()
	state := s.sessions[sid]
	s.mu.RUnlock()

	// 1. 空闲期(无活跃 prompt)cancel → 忽略,不置位(避免误取消下一个 prompt)
	s.handleSessionCancel(JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionCancel,
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`)})
	state.cancelMu.Lock()
	if state.cancelPending {
		t.Fatal("idle-period cancel must not set cancelPending (would cancel the next prompt)")
	}
	state.cancelMu.Unlock()

	// 2. 启动窗口期(promptStarted=true 但 cancelFn 未设置)→ cancelPending 置位
	state.cancelMu.Lock()
	state.promptStarted = true
	state.cancelMu.Unlock()
	s.handleSessionCancel(JSONRPCRequest{JSONRPC: "2.0", ID: 3, Method: MethodSessionCancel,
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`)})
	state.cancelMu.Lock()
	pending := state.cancelPending
	state.cancelMu.Unlock()
	if !pending {
		t.Fatal("cancelPending should be set when cancel arrives in the startup window")
	}

	// 3. executePrompt 启动(空 prompt 提前返回路径)→ cancelPending 被消费清除
	state.promptMu.Lock() // executePrompt 的 defer 会 Unlock,调用前必须持有
	s.wg.Add(1)
	s.executePrompt(context.Background(), 99, state, SessionPromptParams{SessionID: sid, Prompt: []ContentBlock{}})
	state.cancelMu.Lock()
	pending = state.cancelPending
	state.cancelMu.Unlock()
	if pending {
		t.Error("cancelPending should be consumed by executePrompt")
	}
	if state.cancelFn != nil {
		t.Error("cancelFn should be cleared after prompt ends")
	}
	state.cancelMu.Lock()
	if state.promptStarted {
		t.Error("promptStarted should be cleared after prompt ends")
	}
	state.cancelMu.Unlock()
}

func TestCancelRequestWindowPeriod(t *testing.T) {
	// $/cancel_request(LSP 风格按 requestId)在 prompt 启动窗口期到达 →
	// 与 session/cancel 同机制:cancelPending 置位;通知无响应。
	s := newTestServer()
	getResp := withCaptureTransport(s)

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])
	s.mu.RLock()
	state := s.sessions[sid]
	s.mu.RUnlock()

	// 模拟活跃 prompt:注册 requestId → session + promptStarted
	s.registerRequest(float64(2), sid) // JSON 数字 id 解析为 float64,与真实链路一致
	state.cancelMu.Lock()
	state.promptStarted = true
	state.cancelMu.Unlock()
	before := len(getResp())

	s.handleCancelRequest(JSONRPCRequest{JSONRPC: "2.0", Method: MethodCancelRequest,
		Params: json.RawMessage(`{"requestId":2}`)})

	state.cancelMu.Lock()
	pending := state.cancelPending
	state.cancelMu.Unlock()
	if !pending {
		t.Error("cancelPending should be set for matching requestId")
	}
	if resp := getResp(); len(resp) != before {
		t.Errorf("notification must not receive response, got %d new", len(resp)-before)
	}
}

func TestCancelRequestUnknownID(t *testing.T) {
	// 未知 requestId(未注册)→ 静默忽略,不置位
	s := newTestServer()
	getResp := withCaptureTransport(s)

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])
	s.mu.RLock()
	state := s.sessions[sid]
	s.mu.RUnlock()
	state.cancelMu.Lock()
	state.promptStarted = true
	state.cancelMu.Unlock()
	before := len(getResp())

	s.handleCancelRequest(JSONRPCRequest{JSONRPC: "2.0", Method: MethodCancelRequest,
		Params: json.RawMessage(`{"requestId":999}`)})

	state.cancelMu.Lock()
	pending := state.cancelPending
	state.cancelMu.Unlock()
	if pending {
		t.Error("unknown requestId must be ignored")
	}
	if resp := getResp(); len(resp) != before {
		t.Errorf("unknown requestId must not produce response, got %d new", len(resp)-before)
	}
}

func TestCancelRequestUnregisteredAfterCompletion(t *testing.T) {
	// prompt 完成后映射注销 → $/cancel_request 到达被忽略(空闲期)
	s := newTestServer()
	getResp := withCaptureTransport(s)

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])
	s.mu.RLock()
	state := s.sessions[sid]
	s.mu.RUnlock()

	// 模拟 prompt 完成:注册后注销
	s.registerRequest(2, sid)
	s.unregisterRequest(2)
	s.handleCancelRequest(JSONRPCRequest{JSONRPC: "2.0", Method: MethodCancelRequest,
		Params: json.RawMessage(`{"requestId":2}`)})

	state.cancelMu.Lock()
	pending := state.cancelPending
	state.cancelMu.Unlock()
	if pending {
		t.Error("cancel after completion must be ignored (mapping removed)")
	}
}

// TestRegression_SessionIDPathTraversal:sessionId 直接 filepath.Join 曾可
// `../` 穿越任意读/删/覆写 sessionDir 下文件。validSessionID 校验后拒绝。
func TestRegression_SessionIDPathTraversal(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)
	s.sessionDir = t.TempDir()

	// load/delete 拒绝非法 sessionId(含路径分隔与编码穿越)
	for _, tc := range []struct {
		method string
		id     string
	}{
		{MethodSessionLoad, "../evil"},
		{MethodSessionLoad, "..%2F..%2Fetc"},
		{MethodSessionDelete, "../../etc/passwd"},
		{MethodSessionClose, "a/b"},
	} {
		s.handleSessionLoadOrDelete(tc.method, tc.id)
		responses := getResp()
		var resp JSONRPCResponse
		_ = json.Unmarshal(responses[len(responses)-1], &resp)
		if resp.Error == nil || resp.Error.Code != ErrInvalidParams {
			t.Errorf("%s with %q: error = %+v, want ErrInvalidParams", tc.method, tc.id, resp.Error)
		}
	}
}

// handleSessionLoadOrDelete 按 method 分发到 load/delete/close(路径穿越测试用)。
func (s *Server) handleSessionLoadOrDelete(method, id string) {
	req := JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: method,
		Params: json.RawMessage(`{"sessionId":"` + id + `"}`)}
	switch method {
	case MethodSessionLoad:
		s.handleSessionLoad(req)
	case MethodSessionDelete:
		s.handleSessionDelete(req)
	case MethodSessionClose:
		s.handleSessionClose(req)
	}
}

// ---------------------------------------------------------------------------
// MCP 支持测试(session/new mcpServers)
// ---------------------------------------------------------------------------

// TestHandleSessionNewMcpServers:session/new 带 mcpServers →
// per-session MCPManager 创建 + child registry(内置工具仍可见)+ fake connect 被调。
func TestHandleSessionNewMcpServers(t *testing.T) {
	s := newTestServer()
	getResp := withCaptureTransport(s)

	connectCalled := make(chan string, 1)
	s.mcpConnect = func(ctx context.Context, name string, config mcp.ServerConfig) (*mcp.Client, error) {
		connectCalled <- name
		return nil, fmt.Errorf("fake connect failure (test)")
	}

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew,
		Params: json.RawMessage(`{"mcpServers":[{"name":"fs","command":"/bin/true"}]}`)})
	if resp := getResp(); len(resp) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resp))
	}
	sid := getSessionID(t, getResp()[0])

	s.mu.RLock()
	state := s.sessions[sid]
	s.mu.RUnlock()
	if state.MCPManager == nil {
		t.Fatal("MCPManager should be created when mcpServers provided")
	}
	if state.Registry == nil {
		t.Fatal("Registry should be the child registry")
	}
	// child registry 可见父级内置工具
	if _, ok := state.Registry.Get("read"); !ok {
		t.Error("child registry should expose parent builtin tools")
	}

	// fake connect 被调用(异步 goroutine,等待)
	select {
	case name := <-connectCalled:
		if name != "fs" {
			t.Errorf("connect called with %q, want fs", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mcp connect should be invoked")
	}

	// close 不 panic(Manager.Stop 安全路径)
	s.handleSessionClose(JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionClose,
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`)})
}

func TestHandleSessionNewNoMcpServers(t *testing.T) {
	// 无 mcpServers → 不创建 MCPManager
	s := newTestServer()
	getResp := withCaptureTransport(s)

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])

	s.mu.RLock()
	state := s.sessions[sid]
	s.mu.RUnlock()
	if state.MCPManager != nil {
		t.Error("MCPManager should be nil without mcpServers")
	}
	if state.Registry == nil {
		t.Error("Registry should still be a child registry")
	}
}

func TestHandleSessionNewInvalidMcpServers(t *testing.T) {
	// 非法 mcpServers(缺 command)→ fail-closed:整体拒绝 session 创建
	s := newTestServer()
	getResp := withCaptureTransport(s)

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew,
		Params: json.RawMessage(`{"mcpServers":[{"name":"broken"}]}`)})
	responses := getResp()
	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[len(responses)-1], &resp)
	if resp.Error == nil || resp.Error.Code != ErrInvalidParams {
		t.Errorf("invalid mcpServers: error = %+v, want ErrInvalidParams", resp.Error)
	}
	s.mu.RLock()
	n := len(s.sessions)
	s.mu.RUnlock()
	if n != 0 {
		t.Errorf("session should not be created on invalid mcpServers, got %d", n)
	}
}

func TestHandleSessionDeleteRemovesSessionAndFile(t *testing.T) {
	// delete 合法路径:移除注册表 + 删除磁盘 session 文件
	s := newTestServer()
	getResp := withCaptureTransport(s)
	sessionDir := t.TempDir()
	s.sessionDir = sessionDir

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])

	// 触发落盘
	s.mu.RLock()
	state := s.sessions[sid]
	s.mu.RUnlock()
	_ = state.CM.CompleteRun(
		[]llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		1, 0, 0, 0, 0, 0, "", 0, "end_turn",
	)
	filePath := filepath.Join(sessionDir, sid+".json")
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("session file should exist before delete: %v", err)
	}

	s.handleSessionDelete(JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionDelete,
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`)})

	s.mu.RLock()
	_, inMap := s.sessions[sid]
	s.mu.RUnlock()
	if inMap {
		t.Error("session should be removed from registry after delete")
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("session file should be deleted, stat err = %v", err)
	}

	// 删除不存在的 session 文件不报错(幂等)
	s.handleSessionDelete(JSONRPCRequest{JSONRPC: "2.0", ID: 3, Method: MethodSessionDelete,
		Params: json.RawMessage(`{"sessionId":"` + sid + `"}`)})
}

func TestHandleSessionResumeNotFound(t *testing.T) {
	// resume 不存在的 session → -32002(loadSessionFromDisk 报错分支)
	s := newTestServer()
	getResp := withCaptureTransport(s)
	s.sessionDir = t.TempDir()

	s.handleSessionResume(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionResume,
		Params: json.RawMessage(`{"sessionId":"00000000-0000-0000-0000-000000000000"}`)})
	responses := getResp()
	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[len(responses)-1], &resp)
	if resp.Error == nil || resp.Error.Code != ErrSessionNotFound {
		t.Errorf("resume missing session: error = %+v, want -32002", resp.Error)
	}
}

func TestHandleSessionCancelErrors(t *testing.T) {
	// cancel 错误分支:非法 params + session 不存在
	s := newTestServer()
	getResp := withCaptureTransport(s)

	s.handleSessionCancel(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionCancel,
		Params: json.RawMessage(`{not json`)})
	s.handleSessionCancel(JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionCancel,
		Params: json.RawMessage(`{"sessionId":"00000000-0000-0000-0000-000000000000"}`)})
	responses := getResp()
	if len(responses) != 2 {
		t.Fatalf("expected 2 error responses, got %d", len(responses))
	}
	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[0], &resp)
	if resp.Error == nil || resp.Error.Code != ErrInvalidParams {
		t.Errorf("invalid params: error = %+v, want -32602", resp.Error)
	}
	_ = json.Unmarshal(responses[1], &resp)
	if resp.Error == nil || resp.Error.Code != ErrSessionNotFound {
		t.Errorf("missing session: error = %+v, want -32002", resp.Error)
	}
}

func TestExecutePromptSuccess(t *testing.T) {
	// executePrompt 正常路径:mock LLM 一轮文本响应 → end_turn 响应
	s := newTestServer()
	getResp := withCaptureTransport(s)

	s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: MethodSessionNew})
	sid := getSessionID(t, getResp()[0])

	s.handleSessionPrompt(context.Background(), JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: MethodSessionPrompt,
		Params: json.RawMessage(`{"sessionId":"` + sid + `","prompt":[{"type":"text","text":"hello"}]}`)})

	// 等待 prompt goroutine 完成(wg 跟踪),再读取响应——避免轮询读与
	// goroutine 写 strings.Builder 的数据竞争
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("prompt goroutine did not finish within deadline")
	}

	var found bool
	for _, raw := range getResp() {
		var resp JSONRPCResponse
		if json.Unmarshal(raw, &resp) != nil || resp.ID != float64(2) {
			continue
		}
		if resp.Error != nil {
			t.Fatalf("prompt error: %+v", resp.Error)
		}
		var result SessionPromptResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if result.StopReason != "end_turn" {
			t.Errorf("stopReason = %q, want end_turn", result.StopReason)
		}
		found = true
	}
	if !found {
		t.Fatal("prompt response not found")
	}
}
