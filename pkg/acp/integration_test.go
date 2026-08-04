//go:build integration

package acp_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// acpJSONRPCRequest 与 pkg/acp.JSONRPCRequest 保持一致（避免循环导入）
type acpJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// acpJSONRPCResponse 与 pkg/acp.JSONRPCResponse 一致
type acpJSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// acpNotification 与 pkg/acp.JSONRPCNotification 一致
type acpNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// startACP 启动 waveloom acp 子进程，返回 stdin writer / stdout scanner / cleanup 函数。
func startACP(t *testing.T, settingsPath string) (io.WriteCloser, *bufio.Scanner, func()) {
	t.Helper()

	// 找到 waveloom 二进制
	bin := filepath.Join("..", "..", "bin", "waveloom")
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		// 尝试从源码构建
		bin = filepath.Join("..", "..", "build", "waveloom")
	}
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("waveloom binary not found at %s — run 'make build' first", bin)
	}

	args := []string{"acp", "--log-level", "error"}
	if settingsPath != "" {
		abs, _ := filepath.Abs(settingsPath)
		args = append(args, "--settings", abs)
	}

	cmd := exec.Command(bin, args...)
	cmd.Stderr = os.Stderr // log to stderr for debugging
	// 隔离 HOME:子进程的全局配置路径(~/.waveloom/settings.json)退化为
	// "不存在"(LoadSettingsIfExists 对 os.IsNotExist 容错,返回 nil),避免
	// 依赖/写入用户真实配置;--settings 指定的项目级配置不受影响。
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		t.Fatalf("stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start waveloom acp: %v", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	cleanup := func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}

	return stdin, scanner, cleanup
}

// sendRequest 向 acp 进程发送 JSON-RPC 请求。
func sendRequest(t *testing.T, stdin io.Writer, id int, method string, params string) {
	t.Helper()
	var rawParams json.RawMessage
	if params != "" {
		rawParams = json.RawMessage(params)
	}

	req := acpJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  rawParams,
	}

	data, _ := json.Marshal(req)
	_, err := fmt.Fprintf(stdin, "%s\n", data)
	if err != nil {
		t.Fatalf("write request: %v", err)
	}
}

// readLine 从 scanner 读取一行 JSON。
func readLine(t *testing.T, scanner *bufio.Scanner) (json.RawMessage, bool) {
	t.Helper()
	if !scanner.Scan() {
		return nil, false
	}
	line := strings.TrimSpace(scanner.Text())
	if line == "" {
		return readLine(t, scanner)
	}
	return json.RawMessage(line), true
}

// readResponse 读取下一条 JSON-RPC 响应,跳过 session/update 通知
// (session/new 后会收到 available_commands_update 等通知)。
func readResponse(t *testing.T, scanner *bufio.Scanner) (json.RawMessage, bool) {
	t.Helper()
	for {
		raw, ok := readLine(t, scanner)
		if !ok {
			return nil, false
		}
		var notif acpNotification
		if json.Unmarshal(raw, &notif) == nil && notif.Method == "session/update" {
			continue
		}
		return raw, true
	}
}

// ---------------------------------------------------------------------------
// 集成测试
// ---------------------------------------------------------------------------

func TestIntegrationInitialize(t *testing.T) {
	settingsPath := filepath.Join("testdata", "settings.json")
	stdin, scanner, cleanup := startACP(t, settingsPath)
	defer cleanup()

	// 1. Send initialize
	sendRequest(t, stdin, 1, "initialize", `{"protocolVersion":1}`)

	raw, ok := readLine(t, scanner)
	if !ok {
		t.Fatal("no response from acp")
	}

	var resp acpJSONRPCResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	if resp.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", resp.JSONRPC)
	}
	if resp.ID != 1 {
		t.Errorf("id = %d, want 1", resp.ID)
	}

	// Verify result contains agent capabilities
	var result struct {
		ProtocolVersion int `json:"protocolVersion"`
		AgentInfo       struct {
			Name string `json:"name"`
		} `json:"agentInfo"`
	}
	json.Unmarshal(resp.Result, &result)
	if result.ProtocolVersion != 1 {
		t.Errorf("protocolVersion = %d, want 1", result.ProtocolVersion)
	}
	if result.AgentInfo.Name != "waveloom" {
		t.Errorf("agent name = %q, want waveloom", result.AgentInfo.Name)
	}

	t.Log("✓ initialize")
}

func TestIntegrationInitializeAuthMethods(t *testing.T) {
	// 无配置启动(隔离 HOME,不带 --settings):agent 必须照常应答
	// initialize 并声明 terminal 认证方法(注册表 CI auth-check 要求)。
	stdin, scanner, cleanup := startACP(t, "")
	defer cleanup()

	sendRequest(t, stdin, 1, "initialize", `{"protocolVersion":1,"clientInfo":{"name":"ACP Registry Validator","version":"1.0.0"}}`)

	raw, ok := readLine(t, scanner)
	if !ok {
		t.Fatal("no response from acp")
	}
	var resp acpJSONRPCResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}

	var result struct {
		AuthMethods []struct {
			ID   string   `json:"id"`
			Type string   `json:"type"`
			Args []string `json:"args"`
		} `json:"authMethods"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.AuthMethods) != 1 {
		t.Fatalf("authMethods: expected 1, got %d", len(result.AuthMethods))
	}
	m := result.AuthMethods[0]
	if m.Type != "terminal" {
		t.Errorf("auth method type = %q, want terminal", m.Type)
	}
	if len(m.Args) != 1 || m.Args[0] != "setup" {
		t.Errorf("auth method args = %v, want [setup]", m.Args)
	}

	t.Logf("✓ initialize authMethods: type=%s args=%v", m.Type, m.Args)
}

func TestIntegrationAuthRequired(t *testing.T) {
	// 无配置:session/prompt 返回 -32000 AUTH_REQUIRED(终端认证未完成)。
	stdin, scanner, cleanup := startACP(t, "")
	defer cleanup()

	// 1. Initialize
	sendRequest(t, stdin, 1, "initialize", "")
	readLine(t, scanner) // consume response

	// 2. session/new
	sendRequest(t, stdin, 2, "session/new", "")
	raw, ok := readResponse(t, scanner)
	if !ok {
		t.Fatal("no response from acp")
	}
	var newResp acpJSONRPCResponse
	json.Unmarshal(raw, &newResp)
	if newResp.Error != nil {
		t.Fatalf("session/new error: %+v", newResp.Error)
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	json.Unmarshal(newResp.Result, &result)

	// 3. session/prompt → -32000 AUTH_REQUIRED
	sendRequest(t, stdin, 3, "session/prompt",
		`{"sessionId":"`+result.SessionID+`","prompt":[{"type":"text","text":"hello"}]}`)
	raw, ok = readResponse(t, scanner)
	if !ok {
		t.Fatal("no response from acp")
	}
	var promptResp acpJSONRPCResponse
	json.Unmarshal(raw, &promptResp)
	if promptResp.Error == nil {
		t.Fatal("expected AUTH_REQUIRED error for unconfigured LLM")
	}
	if promptResp.Error.Code != -32000 {
		t.Errorf("error code = %d, want -32000 (AUTH_REQUIRED)", promptResp.Error.Code)
	}

	t.Logf("✓ prompt → -32000 AUTH_REQUIRED: %s", promptResp.Error.Message)
}

func TestIntegrationSessionNew(t *testing.T) {
	settingsPath := filepath.Join("testdata", "settings.json")
	stdin, scanner, cleanup := startACP(t, settingsPath)
	defer cleanup()

	// 1. Initialize
	sendRequest(t, stdin, 1, "initialize", "")
	readLine(t, scanner) // consume response

	// 2. Create session
	sendRequest(t, stdin, 2, "session/new", "")

	raw, ok := readResponse(t, scanner)
	if !ok {
		t.Fatal("no response from acp")
	}

	var resp acpJSONRPCResponse
	json.Unmarshal(raw, &resp)
	if resp.Error != nil {
		t.Fatalf("session/new error: %+v", resp.Error)
	}

	var result struct {
		SessionID string `json:"sessionId"`
	}
	json.Unmarshal(resp.Result, &result)
	if result.SessionID == "" {
		t.Error("sessionId should not be empty")
	}

	t.Logf("✓ session/new → %s", result.SessionID)
}

func TestIntegrationSessionPromptError(t *testing.T) {
	settingsPath := filepath.Join("testdata", "settings.json")
	stdin, scanner, cleanup := startACP(t, settingsPath)
	defer cleanup()

	// 1. Initialize
	sendRequest(t, stdin, 1, "initialize", "")
	readLine(t, scanner)

	// 2. session/prompt without session/new → error
	sendRequest(t, stdin, 2, "session/prompt", `{"sessionId":"nonexistent","prompt":[{"type":"text","text":"hello"}]}`)

	raw, ok := readLine(t, scanner)
	if !ok {
		t.Fatal("no response from acp")
	}

	var resp acpJSONRPCResponse
	json.Unmarshal(raw, &resp)
	if resp.Error == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !strings.Contains(resp.Error.Message, "not found") {
		t.Errorf("error message = %q, want 'not found'", resp.Error.Message)
	}

	t.Log("✓ session/prompt with invalid session → error")
}

func TestIntegrationInitializeGuard(t *testing.T) {
	settingsPath := filepath.Join("testdata", "settings.json")
	stdin, scanner, cleanup := startACP(t, settingsPath)
	defer cleanup()

	// session/new before initialize → error
	sendRequest(t, stdin, 1, "session/new", "")

	raw, ok := readResponse(t, scanner)
	if !ok {
		t.Fatal("no response from acp")
	}

	var resp acpJSONRPCResponse
	json.Unmarshal(raw, &resp)
	if resp.Error == nil {
		t.Fatal("expected initialize guard error")
	}
	if resp.Error.Code != -32600 {
		t.Errorf("error code = %d, want -32600", resp.Error.Code)
	}

	t.Log("✓ initialize guard works")
}

func TestIntegrationParseError(t *testing.T) {
	settingsPath := filepath.Join("testdata", "settings.json")
	stdin, scanner, cleanup := startACP(t, settingsPath)
	defer cleanup()

	// Send invalid JSON
	_, _ = fmt.Fprintf(stdin, "{not json}\n")

	raw, ok := readLine(t, scanner)
	if !ok {
		t.Fatal("no response from acp")
	}

	var resp acpJSONRPCResponse
	json.Unmarshal(raw, &resp)
	if resp.Error == nil || resp.Error.Code != -32700 {
		t.Fatalf("expected parse error (-32700), got: %+v", resp.Error)
	}

	t.Log("✓ parse error handling")
}

func TestIntegrationGracefulShutdown(t *testing.T) {
	settingsPath := filepath.Join("testdata", "settings.json")
	stdin, scanner, cleanup := startACP(t, settingsPath)
	defer cleanup() // closes stdin, triggers shutdown

	// Just close stdin without sending anything — should exit cleanly
	_ = stdin.Close()

	// Give it time to shutdown
	time.Sleep(100 * time.Millisecond)

	// Scanner should see EOF
	if scanner.Scan() {
		t.Log("unexpected output after stdin close: ", scanner.Text())
	}

	t.Log("✓ graceful shutdown on stdin close")
}
