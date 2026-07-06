package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/tool"
)

// ============================================================================
// JSON-RPC 类型测试
// ============================================================================

func TestNewRequest(t *testing.T) {
	req, err := NewRequest(1, "test/method", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	if req.JSONRPC != "2.0" {
		t.Errorf("JSONRPC = %q, want 2.0", req.JSONRPC)
	}
	if req.Method != "test/method" {
		t.Errorf("Method = %q, want test/method", req.Method)
	}

	// Verify params round-trip
	var params map[string]string
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params["key"] != "value" {
		t.Errorf("params[key] = %q, want value", params["key"])
	}
}

func TestNewRequest_NilParams(t *testing.T) {
	req, err := NewRequest(2, "noop", nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	if req.Params != nil {
		t.Errorf("Params = %s, want nil", string(req.Params))
	}
}

func TestNewNotification(t *testing.T) {
	notif, err := NewNotification("notifications/test", map[string]int{"x": 42})
	if err != nil {
		t.Fatalf("NewNotification failed: %v", err)
	}
	if notif.JSONRPC != "2.0" {
		t.Errorf("JSONRPC = %q, want 2.0", notif.JSONRPC)
	}
	if notif.Method != "notifications/test" {
		t.Errorf("Method = %q", notif.Method)
	}
}

func TestParseResponse_Success(t *testing.T) {
	result := json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"name":"test","version":"1.0"}}`)
	info, rpcErr, err := ParseResponse[ImplementationInfo](result)
	if err != nil {
		t.Fatalf("ParseResponse failed: %v", err)
	}
	if rpcErr != nil {
		t.Fatalf("unexpected rpc error: %v", rpcErr)
	}
	if info.Name != "test" {
		t.Errorf("Name = %q, want test", info.Name)
	}
	if info.Version != "1.0" {
		t.Errorf("Version = %q, want 1.0", info.Version)
	}
}

func TestParseResponse_Error(t *testing.T) {
	result := json.RawMessage(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"Unknown tool"}}`)
	info, rpcErr, err := ParseResponse[ImplementationInfo](result)
	if err != nil {
		t.Fatalf("ParseResponse failed: %v", err)
	}
	if rpcErr == nil {
		t.Fatal("expected rpc error, got nil")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("Code = %d, want -32602", rpcErr.Code)
	}
	if info != nil {
		t.Errorf("info = %v, want nil", info)
	}
}

// ============================================================================
// TextContent 测试
// ============================================================================

func TestTextContent(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "text", Text: "Hello"},
		{Type: "image", Data: "base64..."},
		{Type: "text", Text: "World"},
	}
	result := TextContent(blocks)
	if result != "Hello\nWorld" {
		t.Errorf("TextContent = %q, want Hello\\nWorld", result)
	}
}

func TestTextContent_Empty(t *testing.T) {
	if result := TextContent(nil); result != "" {
		t.Errorf("TextContent(nil) = %q, want empty", result)
	}
	blocks := []ContentBlock{{Type: "image", Data: "x"}}
	if result := TextContent(blocks); result != "" {
		t.Errorf("TextContent = %q, want empty", result)
	}
}

// ============================================================================
// sanitizeName 测试
// ============================================================================

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"my-server", "my-server"},
		{"hello world", "hello_world"},
		{"a.b.c", "a.b.c"}, // dots are allowed in MCP tool names
		{"spaces and symbols!!!", "spaces_and_symbols"},
		{"__double__", "double"},
		{"", "unknown"},
		{"___", "unknown"},
		{"already_clean", "already_clean"},
	}
	for _, tc := range tests {
		result := sanitizeName(tc.input)
		if result != tc.expected {
			t.Errorf("sanitizeName(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

// ============================================================================
// 环境变量展开测试
// ============================================================================

func TestExpandEnv(t *testing.T) {
	_ = os.Setenv("TEST_MCP_VAR", "hello")
	defer func() { _ = os.Unsetenv("TEST_MCP_VAR") }()

	tests := []struct {
		input    string
		expected string
	}{
		{"${TEST_MCP_VAR}", "hello"},
		{"prefix_${TEST_MCP_VAR}_suffix", "prefix_hello_suffix"},
		{"${NONEXISTENT:-default}", "default"},
		{"${NONEXISTENT}", ""},
		{"no variables", "no variables"},
		{"", ""},
	}
	for _, tc := range tests {
		result := expandEnv(tc.input)
		if result != tc.expected {
			t.Errorf("expandEnv(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

// ============================================================================
// 配置合并测试
// ============================================================================

func TestMergeConfigs(t *testing.T) {
	src1 := map[string]ServerConfig{
		"server-a": {Type: ServerTypeHTTP, URL: "http://a.com"},
		"server-b": {Type: ServerTypeStdio, Command: "b"},
	}
	src2 := map[string]ServerConfig{
		"server-a": {Type: ServerTypeHTTP, URL: "http://a-override.com"}, // override
		"server-c": {Type: ServerTypeHTTP, URL: "http://c.com"},            // new
	}

	merged := mergeConfigs([]map[string]ServerConfig{src1, src2})

	if len(merged) != 3 {
		t.Fatalf("len = %d, want 3", len(merged))
	}

	if merged["server-a"].URL != "http://a-override.com" {
		t.Errorf("server-a URL = %q, want override", merged["server-a"].URL)
	}
	if merged["server-a"].Name != "server-a" {
		t.Errorf("server-a Name = %q, want server-a", merged["server-a"].Name)
	}
	if merged["server-b"].Name != "server-b" {
		t.Errorf("server-b Name = %q, want server-b", merged["server-b"].Name)
	}
	if merged["server-c"].Name != "server-c" {
		t.Errorf("server-c Name = %q, want server-c", merged["server-c"].Name)
	}
}

func TestMergeConfigs_EnvExpansion(t *testing.T) {
	_ = os.Setenv("MCP_TEST_URL", "https://example.com/mcp")
	defer func() { _ = os.Unsetenv("MCP_TEST_URL") }()

	src := map[string]ServerConfig{
		"test": {Type: ServerTypeHTTP, URL: "${MCP_TEST_URL}"},
	}
	merged := mergeConfigs([]map[string]ServerConfig{src})
	if merged["test"].URL != "https://example.com/mcp" {
		t.Errorf("URL = %q, want expanded", merged["test"].URL)
	}
}

// ============================================================================
// toToolResult 测试
// ============================================================================

func TestToToolResult_Success(t *testing.T) {
	mcpResult := &CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: "result text"},
		},
		IsError: false,
	}
	result := toToolResult(mcpResult)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Content != "result text" {
		t.Errorf("Content = %q, want 'result text'", result.Content)
	}
}

func TestToToolResult_Error(t *testing.T) {
	mcpResult := &CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: "something went wrong"},
		},
		IsError: true,
	}
	result := toToolResult(mcpResult)
	if result.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if result.Error.Class != tool.ErrorClassRecoverable {
		t.Errorf("ErrorClass = %v, want recoverable", result.Error.Class)
	}
	if result.Content != "something went wrong" {
		t.Errorf("Content = %q", result.Content)
	}
}

// ============================================================================
// MCPToolProxy 测试
// ============================================================================

type mockTransport struct{}

func (m *mockTransport) Send(ctx context.Context, msg any) error  { return nil }
func (m *mockTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (m *mockTransport) Close() error { return nil }

func TestMCPToolProxy_Name(t *testing.T) {
	client := &Client{name: "test-server", transport: &mockTransport{}}
	toolDef := ToolDef{
		Name:        "get_data",
		Description: "Get some data",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
	proxy := NewMCPToolProxy(client, toolDef)

	expected := "mcp__test-server__get_data"
	if proxy.Name() != expected {
		t.Errorf("Name() = %q, want %q", proxy.Name(), expected)
	}
}

func TestMCPToolProxy_Description(t *testing.T) {
	client := &Client{name: "myserver", transport: &mockTransport{}}
	toolDef := ToolDef{
		Name:        "tool1",
		Description: "Does something useful",
		InputSchema: json.RawMessage(`{}`),
	}
	proxy := NewMCPToolProxy(client, toolDef)

	desc := proxy.Description()
	if !strings.Contains(desc, "[MCP:myserver]") {
		t.Errorf("Description missing server tag: %q", desc)
	}
	if !strings.Contains(desc, "Does something useful") {
		t.Errorf("Description missing tool description: %q", desc)
	}
}

func TestMCPToolProxy_Schema(t *testing.T) {
	client := &Client{name: "s", transport: &mockTransport{}}
	schema := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)
	toolDef := ToolDef{Name: "t", InputSchema: schema}
	proxy := NewMCPToolProxy(client, toolDef)

	if string(proxy.Schema()) != string(schema) {
		t.Errorf("Schema = %s, want %s", proxy.Schema(), schema)
	}
}

func TestMCPToolProxy_ConcurrentSafe(t *testing.T) {
	client := &Client{name: "s", transport: &mockTransport{}}
	proxy := NewMCPToolProxy(client, ToolDef{Name: "t", InputSchema: json.RawMessage(`{}`)})
	if !proxy.ConcurrentSafe() {
		t.Error("MCPToolProxy should be ConcurrentSafe")
	}
}

// ============================================================================
// StdioTransport 测试（真实子进程）
// ============================================================================

func TestStdioTransport_Echo(t *testing.T) {
	// 使用 echo 作为简单 MCP server 模拟 — 它把 stdout 当输出来处理
	// 这里用 cat 实现：读一行 → 回写一行
	transport, err := NewStdioTransport("cat", nil, nil)
	if err != nil {
		t.Fatalf("NewStdioTransport failed: %v", err)
	}
	defer func() { _ = transport.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 发送消息
	msg := map[string]string{"hello": "world"}
	if err := transport.Send(ctx, msg); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// 接收回声（cat 会将 stdin 复制到 stdout）
	data, err := transport.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive failed: %v", err)
	}

	var received map[string]string
	if err := json.Unmarshal(data, &received); err != nil {
		t.Fatalf("unmarshal received: %v", err)
	}
	if received["hello"] != "world" {
		t.Errorf("received[hello] = %q, want world", received["hello"])
	}
}

func TestStdioTransport_ReceiveTimeout(t *testing.T) {
	// 启动 sleep 进程 — 它不会输出任何东西
	transport, err := NewStdioTransport("sleep", []string{"10"}, nil)
	if err != nil {
		t.Fatalf("NewStdioTransport failed: %v", err)
	}
	defer func() { _ = transport.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = transport.Receive(ctx)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

// ============================================================================
// HTTPTransport 测试（本地 mock server）
// ============================================================================

func TestHTTPTransport_SendAndReceive(t *testing.T) {
	// 这个测试需要真实的 HTTP server
	// 用 net/http/httptest 模拟 MCP endpoint
	// 暂时跳过需要外部 server 的测试
	t.Skip("requires running MCP server")
}

// ============================================================================
// Client 测试
// ============================================================================

func TestClient_NextID(t *testing.T) {
	client := &Client{requestID: 1}
	id1 := client.nextID()
	id2 := client.nextID()
	if id1 != 1 {
		t.Errorf("first id = %d, want 1", id1)
	}
	if id2 != 2 {
		t.Errorf("second id = %d, want 2", id2)
	}
}

// ============================================================================
// Manager 测试
// ============================================================================

func TestManager_NewManager(t *testing.T) {
	registry := tool.NewRegistry()
	m := NewManager(registry)
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.ClientCount() != 0 {
		t.Errorf("ClientCount = %d, want 0", m.ClientCount())
	}
}

func TestManager_ClientCountAndNames(t *testing.T) {
	m := NewManager(tool.NewRegistry())

	m.mu.Lock()
	m.clients["a"] = &Client{name: "a", transport: &mockTransport{}}
	m.clients["b"] = &Client{name: "b", transport: &mockTransport{}}
	m.mu.Unlock()

	if m.ClientCount() != 2 {
		t.Errorf("ClientCount = %d, want 2", m.ClientCount())
	}

	names := m.ClientNames()
	if len(names) != 2 {
		t.Errorf("ClientNames len = %d, want 2", len(names))
	}
}

func TestManager_Stop(t *testing.T) {
	m := NewManager(tool.NewRegistry())
	m.mu.Lock()
	m.clients["a"] = &Client{name: "a", transport: &mockTransport{}}
	m.mu.Unlock()

	if err := m.Stop(); err != nil {
		t.Errorf("Stop failed: %v", err)
	}
	if m.ClientCount() != 0 {
		t.Errorf("after Stop, ClientCount = %d, want 0", m.ClientCount())
	}
}

// ============================================================================
// 配置加载测试（文件系统）
// ============================================================================

func TestLoadMCPJSON(t *testing.T) {
	dir := t.TempDir()
	content := `{"mcpServers":{"test-server":{"type":"http","url":"https://example.com/mcp"}}}`
	_ = os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(content), 0644)

	servers := loadMCPJSON(dir)
	if len(servers) != 1 {
		t.Fatalf("len = %d, want 1", len(servers))
	}
	srv := servers["test-server"]
	if srv.Type != ServerTypeHTTP {
		t.Errorf("Type = %q, want http", srv.Type)
	}
	if srv.URL != "https://example.com/mcp" {
		t.Errorf("URL = %q", srv.URL)
	}
}

func TestLoadMCPJSON_NotExist(t *testing.T) {
	dir := t.TempDir()
	servers := loadMCPJSON(dir)
	if len(servers) != 0 {
		t.Errorf("expected 0 servers for nonexistent file, got %d", len(servers))
	}
}

func TestLoadWaveloomJSON_UserScope(t *testing.T) {
	dir := t.TempDir()
	content := `{"mcpServers":{"global-srv":{"type":"stdio","command":"npx"}}}`
	_ = os.WriteFile(filepath.Join(dir, ".waveloom.json"), []byte(content), 0644)

	servers := loadWaveloomJSON(dir, "")
	if len(servers) != 1 {
		t.Fatalf("len = %d, want 1", len(servers))
	}
	if servers["global-srv"].Command != "npx" {
		t.Errorf("Command = %q, want npx", servers["global-srv"].Command)
	}
}

func TestLoadWaveloomJSON_LocalScope(t *testing.T) {
	dir := t.TempDir()
	cwd := "/some/project"
	content := `{"projects":{"/some/project":{"mcpServers":{"local-srv":{"type":"http","url":"http://localhost"}}}}}`
	_ = os.WriteFile(filepath.Join(dir, ".waveloom.json"), []byte(content), 0644)

	servers := loadWaveloomJSON(dir, cwd)
	if len(servers) != 1 {
		t.Fatalf("len = %d, want 1", len(servers))
	}
	if servers["local-srv"].URL != "http://localhost" {
		t.Errorf("URL = %q", servers["local-srv"].URL)
	}
}

func TestLoadClaudeJSON(t *testing.T) {
	dir := t.TempDir()
	content := `{"mcpServers":{"claude-srv":{"type":"stdio","command":"echo"}}}`
	_ = os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(content), 0644)

	servers := loadClaudeJSON(dir, "")
	if len(servers) != 1 {
		t.Fatalf("len = %d, want 1", len(servers))
	}
	if servers["claude-srv"].Command != "echo" {
		t.Errorf("Command = %q", servers["claude-srv"].Command)
	}
}

func TestLoadConfigs_MergeAndPriority(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()

	// Claude Code user scope (lowest)
	claudeContent := `{"mcpServers":{"shared":{"type":"http","url":"http://claude-user"}}}`
	_ = os.WriteFile(filepath.Join(homeDir, ".claude.json"), []byte(claudeContent), 0644)

	// Waveloom user scope (higher)
	waveloomContent := `{"mcpServers":{"shared":{"type":"http","url":"http://waveloom-user"}}}`
	_ = os.WriteFile(filepath.Join(homeDir, ".waveloom.json"), []byte(waveloomContent), 0644)

	// Project scope (highest)
	mcpContent := `{"mcpServers":{"shared":{"type":"http","url":"http://project"}}}`
	_ = os.WriteFile(filepath.Join(projectDir, ".mcp.json"), []byte(mcpContent), 0644)

	configs := LoadConfigs(projectDir, homeDir)

	if len(configs) != 1 {
		t.Fatalf("len = %d, want 1 (merged by name)", len(configs))
	}
	cfg := configs["shared"]
	if cfg.URL != "http://project" {
		t.Errorf("URL = %q, want project (highest priority)", cfg.URL)
	}
	if cfg.Name != "shared" {
		t.Errorf("Name = %q, want shared", cfg.Name)
	}
}

// ============================================================================
// ServerConfig JSON 测试
// ============================================================================

func TestParseHeaderFlags(t *testing.T) {
	headers := []string{"Authorization: Bearer token", "X-Key: value"}
	result := parseHeaderFlags(headers)
	if result["Authorization"] != "Bearer token" {
		t.Errorf("Authorization = %q", result["Authorization"])
	}
	if result["X-Key"] != "value" {
		t.Errorf("X-Key = %q", result["X-Key"])
	}
}

func TestParseEnvFlags(t *testing.T) {
	envVars := []string{"KEY=value", "ANOTHER=123"}
	result := parseEnvFlags(envVars)
	if result["KEY"] != "value" {
		t.Errorf("KEY = %q", result["KEY"])
	}
	if result["ANOTHER"] != "123" {
		t.Errorf("ANOTHER = %q", result["ANOTHER"])
	}
}

// ============================================================================
// AddServerToWaveloomJSON 测试
// ============================================================================

func TestAddServerToWaveloomJSON(t *testing.T) {
	homeDir := t.TempDir()
	cwd := "/test/project"

	config := ServerConfig{
		Type: ServerTypeHTTP,
		URL:  "https://example.com/mcp",
	}

	err := AddServerToWaveloomJSON(homeDir, cwd, "user", "my-srv", config)
	if err != nil {
		t.Fatalf("AddServerToWaveloomJSON failed: %v", err)
	}

	// Verify it was written
	servers := loadWaveloomJSON(homeDir, "")
	if len(servers) != 1 {
		t.Fatalf("len = %d, want 1", len(servers))
	}
	if servers["my-srv"].URL != "https://example.com/mcp" {
		t.Errorf("URL = %q", servers["my-srv"].URL)
	}
}

func TestAddServerToMCPJSON(t *testing.T) {
	projectDir := t.TempDir()

	config := ServerConfig{
		Type:    ServerTypeStdio,
		Command: "npx",
		Args:    []string{"-y", "mcp-server"},
	}

	err := AddServerToMCPJSON(projectDir, "test-srv", config)
	if err != nil {
		t.Fatalf("AddServerToMCPJSON failed: %v", err)
	}

	// Verify it was written
	servers := loadMCPJSON(projectDir)
	if len(servers) != 1 {
		t.Fatalf("len = %d, want 1", len(servers))
	}
	if servers["test-srv"].Command != "npx" {
		t.Errorf("Command = %q", servers["test-srv"].Command)
	}
}

// ============================================================================
// ServerConfig JSON 测试
// ============================================================================

func TestServerConfig_JSONRoundTrip(t *testing.T) {
	cfg := ServerConfig{
		Name: "test", // json:"-" so won't be serialized
		Type: ServerTypeHTTP,
		URL:  "https://example.com",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Name should not appear in JSON
	if strings.Contains(string(data), `"Name"`) || strings.Contains(string(data), `"name"`) {
		t.Errorf("Name field leaked into JSON: %s", data)
	}

	// Round-trip
	var restored ServerConfig
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if restored.Type != ServerTypeHTTP {
		t.Errorf("Type = %q", restored.Type)
	}
	if restored.Name != "" {
		t.Errorf("Name should be empty after unmarshal, got %q", restored.Name)
	}
}

// ============================================================================
// ParseResponse generic test
// ============================================================================

func TestParseResponse_ListTools(t *testing.T) {
	result := json.RawMessage(`{
		"jsonrpc":"2.0",
		"id":1,
		"result":{
			"tools":[
				{"name":"tool1","inputSchema":{"type":"object"}},
				{"name":"tool2","inputSchema":{"type":"object"}}
			]
		}
	}`)

	listResult, rpcErr, err := ParseResponse[ListToolsResult](result)
	if err != nil {
		t.Fatalf("ParseResponse failed: %v", err)
	}
	if rpcErr != nil {
		t.Fatalf("unexpected rpc error: %v", rpcErr)
	}
	if len(listResult.Tools) != 2 {
		t.Fatalf("len(Tools) = %d, want 2", len(listResult.Tools))
	}
	if listResult.Tools[0].Name != "tool1" {
		t.Errorf("Tools[0].Name = %q", listResult.Tools[0].Name)
	}
}
