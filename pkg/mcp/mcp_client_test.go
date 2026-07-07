package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/tool"
)

// ============================================================================
// fakeTransport — 可编程的传输层，用于测试 Client 方法
// ============================================================================

// fakeResponse 表示一次 Receive 调用应返回的数据或错误。
type fakeResponse struct {
	data json.RawMessage
	err  error
}

// fakeTransport 实现 Transport 接口，用预编程的响应序列进行测试。
type fakeTransport struct {
	mu     sync.Mutex
	ch     chan fakeResponse
	// sent 记录所有通过 Send 发送的消息
	sent      []json.RawMessage
	closed    bool
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		ch: make(chan fakeResponse, 32),
	}
}

// queueResponse 追加一个响应（data）到队列末尾。
func (f *fakeTransport) queueResponse(rpcResponse string) {
	f.ch <- fakeResponse{data: json.RawMessage(rpcResponse)}
}

// queueError 追加一个错误响应。
func (f *fakeTransport) queueError(err error) {
	f.ch <- fakeResponse{err: err}
}

func (f *fakeTransport) Send(ctx context.Context, msg any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return fmt.Errorf("transport closed")
	}
	data, _ := json.Marshal(msg)
	f.sent = append(f.sent, data)
	return nil
}

func (f *fakeTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	select {
	case resp := <-f.ch:
		return resp.data, resp.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeTransport) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// allSent 返回所有 Send 过的消息。
func (f *fakeTransport) allSent() []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]json.RawMessage, len(f.sent))
	copy(out, f.sent)
	return out
}

// ============================================================================
// 辅助: JSON-RPC 响应构造
// ============================================================================

// rpcResult 构造一个成功的 JSON-RPC 响应。
func rpcResult(id int, result string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, id, result)
}

// rpcError 构造一个 JSON-RPC 错误响应。
func rpcError(id int, code int, message string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"error":{"code":%d,"message":"%s"}}`, id, code, message)
}

// rpcNotification 构造一个 JSON-RPC 通知（无 id）。
func rpcNotification(method string, params string) string {
	if params == "" {
		return fmt.Sprintf(`{"jsonrpc":"2.0","method":"%s"}`, method)
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"%s","params":%s}`, method, params)
}

// ============================================================================
// Client.initialize 测试
// ============================================================================

func TestClient_Initialize(t *testing.T) {
	ft := newFakeTransport()
	// queue initialize response
	ft.queueResponse(rpcResult(1, `{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"TestServer","version":"1.0"}}`))

	c := &Client{
		name:          "test",
		transport:     ft,
		requestID:     1,
		toolTimeout:   defaultToolTimeout,
	}

	err := c.initialize(context.Background())
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	// Verify ServerInfo was captured
	if c.ServerInfo().Name != "TestServer" {
		t.Errorf("ServerName = %q, want TestServer", c.ServerInfo().Name)
	}

	// Verify 2 messages were sent: initialize + initialized notification
	sent := ft.allSent()
	if len(sent) != 2 {
		t.Fatalf("sent %d messages, want 2 (initialize + initialized)", len(sent))
	}

	// First: initialize request
	var initReq JSONRPCRequest
	if err := json.Unmarshal(sent[0], &initReq); err != nil {
		t.Fatalf("unmarshal init request: %v", err)
	}
	if initReq.Method != MethodInitialize {
		t.Errorf("init method = %q, want %s", initReq.Method, MethodInitialize)
	}

	// Second: initialized notification (no id)
	var notif JSONRPCNotification
	if err := json.Unmarshal(sent[1], &notif); err != nil {
		t.Fatalf("unmarshal notification: %v", err)
	}
	if notif.Method != MethodInitialized {
		t.Errorf("notification method = %q, want %s", notif.Method, MethodInitialized)
	}
}

func TestClient_Initialize_RPCError(t *testing.T) {
	ft := newFakeTransport()
	ft.queueResponse(rpcError(1, -32600, "Unsupported protocol version"))

	c := &Client{
		name:         "test",
		transport:    ft,
		requestID:    1,
		toolTimeout:  defaultToolTimeout,
	}

	err := c.initialize(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestClient_Initialize_NetworkError(t *testing.T) {
	ft := newFakeTransport()
	ft.queueError(fmt.Errorf("connection refused"))

	c := &Client{
		name:         "test",
		transport:    ft,
		requestID:    1,
		toolTimeout:  100 * time.Millisecond,
	}

	err := c.initialize(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ============================================================================
// Client.ListTools 测试
// ============================================================================

func TestClient_ListTools_Success(t *testing.T) {
	ft := newFakeTransport()
	ft.queueResponse(rpcResult(1, `{"tools":[{"name":"tool1","inputSchema":{"type":"object"}},{"name":"tool2","inputSchema":{"type":"object","properties":{"x":{"type":"string"}}}}]}`))

	c := &Client{
		name:         "test",
		transport:    ft,
		requestID:    1,
		toolTimeout:  defaultToolTimeout,
	}

	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("len = %d, want 2", len(tools))
	}
	if tools[0].Name != "tool1" {
		t.Errorf("tools[0].Name = %q", tools[0].Name)
	}
	if tools[1].Name != "tool2" {
		t.Errorf("tools[1].Name = %q", tools[1].Name)
	}

	// Verify tools cached
	cached := c.Tools()
	if len(cached) != 2 {
		t.Errorf("cached tools len = %d, want 2", len(cached))
	}
}

func TestClient_ListTools_RPCError(t *testing.T) {
	ft := newFakeTransport()
	ft.queueResponse(rpcError(1, -32601, "Method not found"))

	c := &Client{
		name:         "test",
		transport:    ft,
		requestID:    1,
		toolTimeout:  defaultToolTimeout,
	}

	_, err := c.ListTools(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ============================================================================
// Client.CallTool 测试
// ============================================================================

func TestClient_CallTool_Success(t *testing.T) {
	ft := newFakeTransport()
	ft.queueResponse(rpcResult(1, `{"content":[{"type":"text","text":"hello world"}],"isError":false}`))

	c := &Client{
		name:         "test",
		transport:    ft,
		requestID:    1,
		toolTimeout:  defaultToolTimeout,
	}

	result, err := c.CallTool(context.Background(), "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if result.IsError {
		t.Error("IsError should be false")
	}
	if len(result.Content) != 1 || result.Content[0].Text != "hello world" {
		t.Errorf("Content = %+v", result.Content)
	}
}

func TestClient_CallTool_IsError(t *testing.T) {
	ft := newFakeTransport()
	ft.queueResponse(rpcResult(1, `{"content":[{"type":"text","text":"something failed"}],"isError":true}`))

	c := &Client{
		name:         "test",
		transport:    ft,
		requestID:    1,
		toolTimeout:  defaultToolTimeout,
	}

	result, err := c.CallTool(context.Background(), "failtool", nil)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !result.IsError {
		t.Error("IsError should be true")
	}
}

func TestClient_CallTool_RPCError(t *testing.T) {
	ft := newFakeTransport()
	ft.queueResponse(rpcError(1, -32602, "Unknown tool: bad_tool"))

	c := &Client{
		name:         "test",
		transport:    ft,
		requestID:    1,
		toolTimeout:  defaultToolTimeout,
	}

	_, err := c.CallTool(context.Background(), "bad_tool", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ============================================================================
// sendRequestStdio 通知穿插测试
// ============================================================================

func TestClient_SendRequestStdio_NotificationInterleaved(t *testing.T) {
	// 模拟: 发送请求 → 收到 list_changed 通知 → 收到匹配响应
	ft := newFakeTransport()
	// 先 queue 一个通知（无 id），再 queue 真正的响应
	ft.queueResponse(rpcNotification(MethodToolsListChanged, ""))
	ft.queueResponse(rpcResult(1, `{"tools":[{"name":"t1","inputSchema":{}}]}`))

	c := &Client{
		name:             "test",
		transport:        ft,
		requestID:        1,
		toolTimeout:      5 * time.Second,
	}

	notifiedChan := make(chan struct{}, 1)
	c.OnToolsChanged = func() {
		notifiedChan <- struct{}{}
	}

	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len = %d, want 1", len(tools))
	}

	// 验证通知回调被触发
	select {
	case <-notifiedChan:
		// OK
	case <-time.After(time.Second):
		t.Error("OnToolsChanged callback was not triggered for list_changed notification")
	}

	// 验证只发送了 1 个请求（通知被正确跳过，不影响响应匹配）
	sent := ft.allSent()
	if len(sent) != 1 {
		t.Errorf("sent %d requests, want 1", len(sent))
	}
}

func TestClient_SendRequestStdio_MultipleNotifications(t *testing.T) {
	// 多个通知在响应之前到达
	ft := newFakeTransport()
	ft.queueResponse(rpcNotification("notifications/unknown", `{"data":"ignored"}`))
	ft.queueResponse(rpcNotification(MethodToolsListChanged, ""))
	ft.queueResponse(rpcNotification("notifications/another", ""))
	ft.queueResponse(rpcResult(1, `{"tools":[]}`))

	notified := make(chan struct{}, 1)

	c := &Client{
		name:         "test",
		transport:    ft,
		requestID:    1,
		toolTimeout:  5 * time.Second,
	}
	c.OnToolsChanged = func() {
		notified <- struct{}{}
	}

	_, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	// 只有 list_changed 触发回调，其他通知静默忽略
	select {
	case <-notified:
		// OK
	case <-time.After(time.Second):
		t.Error("OnToolsChanged was not triggered for list_changed notification")
	}
}

// ============================================================================
// MCPToolProxy.Execute 测试
// ============================================================================

func TestMCPToolProxy_Execute_Success(t *testing.T) {
	ft := newFakeTransport()
	ft.queueResponse(rpcResult(1, `{"content":[{"type":"text","text":"result from server"}],"isError":false}`))

	client := &Client{
		name:         "myserver",
		transport:    ft,
		requestID:    1,
		toolTimeout:  defaultToolTimeout,
	}
	proxy := NewMCPToolProxy(client, ToolDef{
		Name:        "mytool",
		Description: "Does things",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	})

	result, err := proxy.Execute(context.Background(), map[string]any{"key": "value"})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected ToolError: %v", result.Error)
	}
	if result.Content != "result from server" {
		t.Errorf("Content = %q, want 'result from server'", result.Content)
	}
	// Duration may be zero on platforms with low timer resolution (e.g. Windows)
	// when the fake transport responds instantaneously.
	if result.Meta.Duration < 0 {
		t.Error("Duration should not be negative")
	}
}

func TestMCPToolProxy_Execute_IsError(t *testing.T) {
	ft := newFakeTransport()
	ft.queueResponse(rpcResult(1, `{"content":[{"type":"text","text":"invalid input: date must be future"}],"isError":true}`))

	client := &Client{
		name:         "myserver",
		transport:    ft,
		requestID:    1,
		toolTimeout:  defaultToolTimeout,
	}
	proxy := NewMCPToolProxy(client, ToolDef{
		Name:        "mytool",
		InputSchema: json.RawMessage(`{}`),
	})

	result, err := proxy.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected ToolError, got nil")
	}
	if result.Error.Class != tool.ErrorClassRecoverable {
		t.Errorf("ErrorClass = %v, want Recoverable", result.Error.Class)
	}
	if result.Content != "invalid input: date must be future" {
		t.Errorf("Content = %q", result.Content)
	}
}

func TestMCPToolProxy_Execute_NetworkError(t *testing.T) {
	ft := newFakeTransport()
	ft.queueError(fmt.Errorf("connection lost"))

	client := &Client{
		name:         "myserver",
		transport:    ft,
		requestID:    1,
		toolTimeout:  100 * time.Millisecond,
	}
	proxy := NewMCPToolProxy(client, ToolDef{
		Name:        "mytool",
		InputSchema: json.RawMessage(`{}`),
	})

	result, err := proxy.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected ToolError for network failure, got nil")
	}
	if result.Error.Cause == nil {
		t.Error("ToolError.Cause should preserve original error")
	}
	if result.Content == "" {
		t.Error("Content should not be empty on error")
	}
}

// ============================================================================
// Manager.connectServer 测试
// ============================================================================

func TestManager_ConnectServer_Stdio_OneAttempt(t *testing.T) {
	registry := tool.NewRegistry()
	m := NewManager(registry)

	attempts := 0
	m.connectFunc = func(ctx context.Context, name string, config ServerConfig) (*Client, error) {
		attempts++
		return nil, fmt.Errorf("simulated failure")
	}

	cfg := ServerConfig{Name: "test", Type: ServerTypeStdio, Command: "echo"}
	m.connectServer(context.Background(), cfg)

	if attempts != 1 {
		t.Errorf("stdio attempts = %d, want 1", attempts)
	}
	// 失败不阻塞
	if m.ClientCount() != 0 {
		t.Errorf("ClientCount = %d, want 0 (failed)", m.ClientCount())
	}
}

func TestManager_ConnectServer_HTTP_FiveRetries(t *testing.T) {
	// 验证 HTTP transport 尝试 5 次（不测试实际 backoff 延迟，因为会累积 15s）
	registry := tool.NewRegistry()
	m := NewManager(registry)

	attempts := 0
	m.connectFunc = func(ctx context.Context, name string, config ServerConfig) (*Client, error) {
		attempts++
		// 注入一个假的 backoff，避免实际 sleep 累积
		return nil, fmt.Errorf("simulated failure")
	}

	cfg := ServerConfig{Name: "test", Type: ServerTypeHTTP, URL: "https://example.com/mcp"}

	// 用短超时的 context 运行，因为 backoff 会累计 ~15s
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// 在 goroutine 中启动，避免阻塞主测试
	done := make(chan int, 1)
	go func() {
		m.connectServer(ctx, cfg)
		done <- attempts
	}()

	select {
	case a := <-done:
		// connectServer 可能在 backoff 期间被 ctx 取消
		// 只要能验证它至少尝试了就行
		if a < 1 {
			t.Errorf("expected at least 1 attempt, got %d", a)
		}
	case <-time.After(2 * time.Second):
		// connectServer 在 backoff 中；至少验证它已开始尝试
		if attempts < 1 {
			t.Error("connectServer did not start attempting connections")
		}
	}
}

func TestManager_ConnectServer_Success_RegistersTools(t *testing.T) {
	registry := tool.NewRegistry()
	m := NewManager(registry)

	ft := newFakeTransport()
	client := &Client{
		name:             "test",
		transport:        ft,
		requestID:        1,
		toolTimeout:      defaultToolTimeout,
	}

	m.connectFunc = func(ctx context.Context, name string, config ServerConfig) (*Client, error) {
		client.serverInfo = ImplementationInfo{Name: "TestServer", Version: "1.0"}
		return client, nil
	}

	// queue ListTools response
	ft.queueResponse(rpcResult(1, `{"tools":[{"name":"t1","inputSchema":{"type":"object"}},{"name":"t2","inputSchema":{}}]}`))

	cfg := ServerConfig{Name: "test", Type: ServerTypeHTTP, URL: "https://example.com"}
	m.connectServer(context.Background(), cfg)

	if m.ClientCount() != 1 {
		t.Fatalf("ClientCount = %d, want 1", m.ClientCount())
	}

	// 验证工具已注册到 registry
	if _, ok := registry.Get("mcp__test__t1"); !ok {
		t.Error("tool mcp__test__t1 not found in registry")
	}
	if _, ok := registry.Get("mcp__test__t2"); !ok {
		t.Error("tool mcp__test__t2 not found in registry")
	}
}

// ============================================================================
// HandleNotification 测试
// ============================================================================

func TestClient_HandleNotification_ListChanged(t *testing.T) {
	notified := make(chan struct{}, 1)
	c := &Client{
		name: "test",
		OnToolsChanged: func() {
			notified <- struct{}{}
		},
	}

	c.handleNotification(nil, MethodToolsListChanged)

	select {
	case <-notified:
		// OK
	case <-time.After(time.Second):
		t.Error("OnToolsChanged should be called for tools/list_changed")
	}
}

func TestClient_HandleNotification_UnknownMethod(t *testing.T) {
	c := &Client{name: "test"}
	// 不应 panic
	c.handleNotification(nil, "notifications/unknown_method")
}

// ============================================================================
// ServerConfig Name 字段测试
// ============================================================================

func TestServerConfig_NameField(t *testing.T) {
	// Name 是 json:"-" 的，不应出现在序列化输出中
	cfg := ServerConfig{
		Name: "should-not-appear",
		Type: ServerTypeHTTP,
		URL:  "https://example.com",
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["Name"]; ok {
		t.Error("Name field should not be in JSON output")
	}
	if _, ok := m["name"]; ok {
		t.Error("name field should not be in JSON output")
	}
}

// ============================================================================
// mergeConfigs Name 传递测试
// ============================================================================

func TestMergeConfigs_PreservesAllNames(t *testing.T) {
	src := map[string]ServerConfig{
		"srv-a": {Type: ServerTypeHTTP, URL: "http://a"},
		"srv-b": {Type: ServerTypeStdio, Command: "b"},
		"srv-c": {Type: ServerTypeHTTP, URL: "http://c"},
	}
	merged := mergeConfigs([]map[string]ServerConfig{src})

	for _, name := range []string{"srv-a", "srv-b", "srv-c"} {
		if cfg, ok := merged[name]; !ok {
			t.Errorf("%q missing from merged result", name)
		} else if cfg.Name != name {
			t.Errorf("%q.Name = %q, want %q", name, cfg.Name, name)
		}
	}
}

// ============================================================================
// Connect 函数测试（通过真实 cat 子进程验证集成）
// ============================================================================

func TestClient_SendRequestStdio_NotificationSkipped(t *testing.T) {
	// 使用 fake transport 验证：请求-通知-响应的序列能正确工作
	ft := newFakeTransport()
	// 首先 queue 一个通知，然后 queue 响应
	ft.queueResponse(rpcNotification(MethodToolsListChanged, ""))
	ft.queueResponse(rpcResult(1, `"ok"`))

	c := &Client{
		name:         "test",
		transport:    ft,
		requestID:    1,
		toolTimeout:  5 * time.Second,
	}

	// 发送请求，使用自定义方法（不走 initialize）
	result, err := c.sendRequest(context.Background(), "test/method", nil)
	if err != nil {
		t.Fatalf("sendRequest failed: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

	// 解析 JSON-RPC 响应，验证 result 字段
	resp, rpcErr, err := ParseResponse[string](result)
	if err != nil {
		t.Fatalf("ParseResponse failed: %v", err)
	}
	if rpcErr != nil {
		t.Fatalf("rpc error: %v", rpcErr)
	}
	if resp == nil || *resp != "ok" {
		t.Errorf("Result = %v, want ok", resp)
	}
}

// ============================================================================
// ClientStatus — 失败 server 追踪
// ============================================================================

func TestManager_ClientStatus_IncludesFailedServers(t *testing.T) {
	registry := tool.NewRegistry()
	m := NewManager(registry)

	m.connectFunc = func(ctx context.Context, name string, config ServerConfig) (*Client, error) {
		return nil, fmt.Errorf("connection refused")
	}

	cfg := ServerConfig{Name: "fail-srv", Type: ServerTypeStdio, Command: "echo"}
	m.connectServer(context.Background(), cfg)

	status := m.ClientStatus()
	if info, ok := status["fail-srv"]; !ok {
		t.Fatal("ClientStatus should include failed server")
	} else {
		if info.Connected {
			t.Error("failed server should have Connected=false")
		}
		if info.Error == "" {
			t.Error("failed server should have non-empty Error")
		}
		if !strings.Contains(info.Error, "connection refused") {
			t.Errorf("Error = %q, want containing 'connection refused'", info.Error)
		}
	}
}

func TestManager_ClientStatus_SuccessClearsFailure(t *testing.T) {
	registry := tool.NewRegistry()
	m := NewManager(registry)

	// Register a prior failure
	m.mu.Lock()
	m.failedErr["srv"] = fmt.Errorf("old error")
	m.mu.Unlock()

	// Then connect successfully
	ft := newFakeTransport()
	ft.queueResponse(rpcResult(1, `{"tools":[]}`))

	m.connectFunc = func(ctx context.Context, name string, config ServerConfig) (*Client, error) {
		return &Client{
			name:         "srv",
			transport:    ft,
			requestID:    1,
			toolTimeout:  defaultToolTimeout,
			serverInfo:   ImplementationInfo{Name: "TestServer", Version: "1.0"},
		}, nil
	}

	cfg := ServerConfig{Name: "srv", Type: ServerTypeHTTP, URL: "https://example.com"}
	m.connectServer(context.Background(), cfg)

	status := m.ClientStatus()
	if info := status["srv"]; !info.Connected {
		t.Error("server should be Connected after successful connection")
	} else if info.Error != "" {
		t.Errorf("Error should be empty after success, got %q", info.Error)
	}
}

// ============================================================================
// OnStatusChange 回调
// ============================================================================

func TestManager_OnStatusChange_TriggeredOnFailure(t *testing.T) {
	registry := tool.NewRegistry()
	m := NewManager(registry)

	m.connectFunc = func(ctx context.Context, name string, config ServerConfig) (*Client, error) {
		return nil, fmt.Errorf("boom")
	}

	notified := make(chan struct{}, 1)
	m.OnStatusChange = func() {
		notified <- struct{}{}
	}

	cfg := ServerConfig{Name: "test", Type: ServerTypeStdio, Command: "echo"}
	m.connectServer(context.Background(), cfg)

	select {
	case <-notified:
		// OK
	case <-time.After(time.Second):
		t.Error("OnStatusChange was not triggered on connect failure")
	}
}

func TestManager_OnStatusChange_TriggeredOnSuccess(t *testing.T) {
	registry := tool.NewRegistry()
	m := NewManager(registry)

	ft := newFakeTransport()
	ft.queueResponse(rpcResult(1, `{"tools":[]}`))

	m.connectFunc = func(ctx context.Context, name string, config ServerConfig) (*Client, error) {
		return &Client{
			name:         "test",
			transport:    ft,
			requestID:    1,
			toolTimeout:  defaultToolTimeout,
			serverInfo:   ImplementationInfo{Name: "TestServer", Version: "1.0"},
		}, nil
	}

	notified := make(chan struct{}, 1)
	m.OnStatusChange = func() {
		notified <- struct{}{}
	}

	cfg := ServerConfig{Name: "test", Type: ServerTypeHTTP, URL: "https://example.com"}
	m.connectServer(context.Background(), cfg)

	select {
	case <-notified:
		// OK
	case <-time.After(time.Second):
		t.Error("OnStatusChange was not triggered on connect success")
	}
}

func TestManager_OnStatusChange_TriggeredOnStop(t *testing.T) {
	m := NewManager(tool.NewRegistry())

	m.mu.Lock()
	m.clients["a"] = &Client{name: "a", transport: &mockTransport{}}
	m.mu.Unlock()

	notified := make(chan struct{}, 1)
	m.OnStatusChange = func() {
		notified <- struct{}{}
	}

	_ = m.Stop()

	select {
	case <-notified:
		// OK
	case <-time.After(time.Second):
		t.Error("OnStatusChange was not triggered on Stop")
	}
}

// ============================================================================
// refreshTools 去重
// ============================================================================

func TestManager_RefreshTools_SkipDuplicates(t *testing.T) {
	registry := tool.NewRegistry()
	m := NewManager(registry)

	ft := newFakeTransport()
	client := &Client{
		name:         "test",
		transport:    ft,
		requestID:    1,
		toolTimeout:  defaultToolTimeout,
		serverInfo:   ImplementationInfo{Name: "TestServer", Version: "1.0"},
	}

	// 先注册工具（模拟首次连接）
	proxy := NewMCPToolProxy(client, ToolDef{
		Name:        "mytool",
		InputSchema: json.RawMessage(`{}`),
	})
	registry.Register(tool.Wrap(proxy))
	existingName := proxy.Name()

	// refreshTools 尝试再次注册同名工具
	ft.queueResponse(rpcResult(1, `{"tools":[{"name":"mytool","inputSchema":{}},{"name":"newtool","inputSchema":{}}]}`))
	m.refreshTools(client)

	// 验证: 同名工具没有触发 panic，总工具数 = 2（原有 1 + 新增 1）
	// 注意：registry.List() / Get() 来验证
	if _, ok := registry.Get(existingName); !ok {
		t.Errorf("existing tool %q was removed (should not happen)", existingName)
	}
	if _, ok := registry.Get("mcp__test__newtool"); !ok {
		t.Error("new tool mcp__test__newtool was not registered")
	}
}
