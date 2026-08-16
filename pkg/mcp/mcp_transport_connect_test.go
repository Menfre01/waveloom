package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ============================================================================
// Connect — HTTP 握手(httptest mock server)
// ============================================================================

// mcpHandshakeHandler 返回完成 initialize 握手的 HTTP handler。
func mcpHandshakeHandler(initializeResult string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var msg map[string]any
		_ = json.NewDecoder(r.Body).Decode(&msg)
		if method, _ := msg["method"].(string); method == MethodInitialize {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%v,"result":%s}`, msg["id"], initializeResult)
			return
		}
		// initialized 通知等其余消息
		w.WriteHeader(http.StatusAccepted)
	}
}

func TestConnect_HTTP_Success(t *testing.T) {
	srv := httptest.NewServer(mcpHandshakeHandler(
		`{"protocolVersion":"2025-11-25","capabilities":{},"serverInfo":{"name":"TestServer","version":"1.0"}}`))
	defer srv.Close()

	client, err := Connect(context.Background(), "mysrv", ServerConfig{Type: ServerTypeHTTP, URL: srv.URL})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	if client.ServerName() != "mysrv" {
		t.Errorf("ServerName = %q, want mysrv", client.ServerName())
	}
	if client.ServerInfo().Name != "TestServer" {
		t.Errorf("ServerInfo.Name = %q, want TestServer", client.ServerInfo().Name)
	}
	if client.toolTimeout != defaultToolTimeout {
		t.Errorf("toolTimeout = %v, want default", client.toolTimeout)
	}
	if client.httpTransport == nil {
		t.Error("httpTransport = nil, want non-nil")
	}
}

func TestConnect_HTTP_TimeoutOverride(t *testing.T) {
	srv := httptest.NewServer(mcpHandshakeHandler(
		`{"protocolVersion":"2025-11-25","capabilities":{},"serverInfo":{"name":"S","version":"1"}}`))
	defer srv.Close()

	client, err := Connect(context.Background(), "mysrv",
		ServerConfig{Type: ServerTypeHTTP, URL: srv.URL, Timeout: 5000})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	if client.toolTimeout != 5*time.Second {
		t.Errorf("toolTimeout = %v, want 5s", client.toolTimeout)
	}
}

func TestConnect_HTTP_HandshakeRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"rejected"}}`)
	}))
	defer srv.Close()

	_, err := Connect(context.Background(), "x", ServerConfig{Type: ServerTypeHTTP, URL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "initialize error") {
		t.Errorf("err = %v, want containing 'initialize error'", err)
	}
}

func TestConnect_HTTP_ProtocolVersionMismatch(t *testing.T) {
	srv := httptest.NewServer(mcpHandshakeHandler(
		`{"protocolVersion":"2024-06-01","capabilities":{},"serverInfo":{"name":"S","version":"1"}}`))
	defer srv.Close()

	_, err := Connect(context.Background(), "x", ServerConfig{Type: ServerTypeHTTP, URL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "unsupported protocol version") {
		t.Errorf("err = %v, want containing 'unsupported protocol version'", err)
	}
}

func TestConnect_HTTP_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	_, err := Connect(context.Background(), "x", ServerConfig{Type: ServerTypeHTTP, URL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("err = %v, want containing 'HTTP 500'", err)
	}
}

func TestConnect_HTTP_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "not-json")
	}))
	defer srv.Close()

	_, err := Connect(context.Background(), "x", ServerConfig{Type: ServerTypeHTTP, URL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "parse initialize response") {
		t.Errorf("err = %v, want containing 'parse initialize response'", err)
	}
}

func TestConnect_UnsupportedType(t *testing.T) {
	_, err := Connect(context.Background(), "x", ServerConfig{Type: ServerType("grpc")})
	if err == nil || !strings.Contains(err.Error(), `unsupported server type "grpc"`) {
		t.Errorf("err = %v, want unsupported type", err)
	}
}

func TestConnect_Stdio_BadCommand(t *testing.T) {
	_, err := Connect(context.Background(), "x", ServerConfig{
		Type:    ServerTypeStdio,
		Command: filepath.Join(t.TempDir(), "no-such-binary"),
	})
	if err == nil || !strings.Contains(err.Error(), "stdio transport") {
		t.Errorf("err = %v, want containing 'stdio transport'", err)
	}
}

// ============================================================================
// initialize — 协议版本兼容性分支
// ============================================================================

func TestClient_Initialize_Accepts2024Version(t *testing.T) {
	ft := newFakeTransport()
	ft.queueResponse(rpcResult(1, `{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"S","version":"1"}}`))
	c := &Client{name: "t", transport: ft, requestID: 1, toolTimeout: defaultToolTimeout}

	if err := c.initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
}

func TestClient_Initialize_EmptyVersionAccepted(t *testing.T) {
	ft := newFakeTransport()
	ft.queueResponse(rpcResult(1, `{"capabilities":{},"serverInfo":{"name":"S","version":"1"}}`))
	c := &Client{name: "t", transport: ft, requestID: 1, toolTimeout: defaultToolTimeout}

	if err := c.initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
}

func TestClient_Initialize_RejectsUnknownVersion(t *testing.T) {
	ft := newFakeTransport()
	ft.queueResponse(rpcResult(1, `{"protocolVersion":"2024-06-01","capabilities":{},"serverInfo":{"name":"S","version":"1"}}`))
	c := &Client{name: "t", transport: ft, requestID: 1, toolTimeout: defaultToolTimeout}

	err := c.initialize(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unsupported protocol version") {
		t.Errorf("err = %v, want 'unsupported protocol version'", err)
	}
}

// ============================================================================
// sendRequestStdio — ID 不匹配与解析错误
// ============================================================================

func TestClient_SendRequestStdio_MismatchedID(t *testing.T) {
	ft := newFakeTransport()
	ft.queueResponse(rpcResult(99, `"wrong"`)) // 不匹配 ID
	ft.queueResponse(rpcResult(1, `"right"`))

	var logBuf bytes.Buffer
	c := &Client{
		name:        "t",
		transport:   ft,
		requestID:   1,
		toolTimeout: 5 * time.Second,
		logger:      log.New(&logBuf, "", 0),
	}

	result, err := c.sendRequest(context.Background(), "test/m", nil)
	if err != nil {
		t.Fatalf("sendRequest: %v", err)
	}
	got, rpcErr, err := ParseResponse[string](result)
	if err != nil || rpcErr != nil {
		t.Fatalf("ParseResponse: %v, rpcErr=%v", err, rpcErr)
	}
	if got == nil || *got != "right" {
		t.Errorf("result = %v, want right", got)
	}
	if !strings.Contains(logBuf.String(), "unexpected id") {
		t.Errorf("logger 未记录 unexpected id: %q", logBuf.String())
	}
}

func TestClient_SendRequestStdio_ParseError(t *testing.T) {
	ft := newFakeTransport()
	ft.ch <- fakeResponse{data: json.RawMessage(`{not-valid-json`)}

	c := &Client{name: "t", transport: ft, requestID: 1, toolTimeout: 5 * time.Second}

	_, err := c.sendRequest(context.Background(), "m", nil)
	if err == nil || !strings.Contains(err.Error(), "parse message") {
		t.Errorf("err = %v, want containing 'parse message'", err)
	}
}

func TestClient_SendRequestStdio_Timeout(t *testing.T) {
	ft := newFakeTransport() // 无响应入队
	c := &Client{name: "t", transport: ft, requestID: 1, toolTimeout: 50 * time.Millisecond}

	_, err := c.sendRequest(context.Background(), "m", nil)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("err = %v, want deadline exceeded", err)
	}
}

// ============================================================================
// StdioTransport — stderr 捕获与 Close
// ============================================================================

func TestStdioTransport_StderrCaptureAndClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c 在 Windows 不可用")
	}
	transport, err := NewStdioTransport("sh",
		[]string{"-c", "echo some-stderr-line >&2; cat >/dev/null"}, nil)
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}

	// drainStderr 后台持续读取,轮询等待内容到达
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(transport.Stderr(), "some-stderr-line") {
		if time.Now().After(deadline) {
			t.Fatal("stderr 未在期限内被捕获")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := transport.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close 幂等(closeOnce)
	if err := transport.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !strings.Contains(transport.Stderr(), "some-stderr-line") {
		t.Errorf("Close 后 stderr 缓冲丢失: %q", transport.Stderr())
	}
}

func TestStdioTransport_ReceiveAfterClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c 在 Windows 不可用")
	}
	transport, err := NewStdioTransport("sh", []string{"-c", "cat >/dev/null"}, nil)
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := transport.Receive(context.Background()); err != io.EOF {
		t.Errorf("Receive after Close = %v, want io.EOF", err)
	}
}

func TestStdioTransport_ReceiveCancelledCtx(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c 在 Windows 不可用")
	}
	transport, err := NewStdioTransport("sh", []string{"-c", "cat >/dev/null"}, nil)
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}
	defer func() { _ = transport.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := transport.Receive(ctx); err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestStdioTransport_EnvPassedToChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c 在 Windows 不可用")
	}
	transport, err := NewStdioTransport("sh",
		[]string{"-c", "echo $MCP_TEST_ENV_VAR"},
		map[string]string{"MCP_TEST_ENV_VAR": "env-ok"})
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}
	defer func() { _ = transport.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := transport.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(data) != "env-ok" {
		t.Errorf("data = %q, want env-ok", string(data))
	}
}

func TestNewStdioTransport_BadCommand(t *testing.T) {
	_, err := NewStdioTransport(filepath.Join(t.TempDir(), "no-such-binary"), nil, nil)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

// ============================================================================
// HTTPTransport — Send / SendAndReceive
// ============================================================================

func TestNewHTTPTransport(t *testing.T) {
	tr := NewHTTPTransport("http://x.example", map[string]string{"K": "V"})
	if tr.url != "http://x.example" {
		t.Errorf("url = %q", tr.url)
	}
	if tr.headers["K"] != "V" {
		t.Errorf("headers = %v", tr.headers)
	}
	if tr.httpClient == nil {
		t.Error("httpClient = nil")
	}
}

func TestHTTPTransport_SetSessionID(t *testing.T) {
	tr := NewHTTPTransport("http://x", nil)
	tr.SetSessionID("abc")
	if tr.sessionID != "abc" {
		t.Errorf("sessionID = %q, want abc", tr.sessionID)
	}
}

func TestHTTPTransport_Close(t *testing.T) {
	tr := NewHTTPTransport("http://x", nil)
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !tr.closed {
		t.Error("closed = false after Close")
	}
}

func TestHTTPTransport_SendAndReceive_JSONAndSession(t *testing.T) {
	var mu sync.Mutex
	sawSession := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.Header.Get("MCP-Session-Id") == "sess-abc" {
			sawSession = true
		}
		mu.Unlock()
		w.Header().Set("MCP-Session-Id", "sess-abc")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL, map[string]string{"X-Custom": "yes"})
	data, err := tr.SendAndReceive(context.Background(), map[string]string{"m": "1"})
	if err != nil {
		t.Fatalf("SendAndReceive: %v", err)
	}
	var resp struct {
		Result map[string]bool `json:"result"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Result["ok"] {
		t.Errorf("result = %v, want ok=true", resp.Result)
	}

	// 第二次请求应携带捕获到的 session id
	if _, err := tr.SendAndReceive(context.Background(), map[string]string{"m": "2"}); err != nil {
		t.Fatalf("second SendAndReceive: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawSession {
		t.Error("第二次请求未携带 MCP-Session-Id")
	}
}

func TestHTTPTransport_SendAndReceive_SSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\n")
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL, nil)
	data, err := tr.SendAndReceive(context.Background(), map[string]string{})
	if err != nil {
		t.Fatalf("SendAndReceive: %v", err)
	}
	if !strings.Contains(string(data), `"ok":true`) {
		t.Errorf("data = %s", string(data))
	}
}

func TestHTTPTransport_SendAndReceive_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL, nil)
	_, err := tr.SendAndReceive(context.Background(), map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want HTTP 500 + boom", err)
	}
}

func TestHTTPTransport_SendAndReceive_Closed(t *testing.T) {
	tr := NewHTTPTransport("http://127.0.0.1:1", nil)
	_ = tr.Close()
	_, err := tr.SendAndReceive(context.Background(), map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "transport closed") {
		t.Errorf("err = %v, want 'transport closed'", err)
	}
}

func TestHTTPTransport_SendAndReceive_MarshalError(t *testing.T) {
	tr := NewHTTPTransport("http://127.0.0.1:1", nil)
	_, err := tr.SendAndReceive(context.Background(), make(chan int))
	if err == nil || !strings.Contains(err.Error(), "marshal") {
		t.Errorf("err = %v, want marshal error", err)
	}
}

func TestHTTPTransport_Send_AcceptedAndOK(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("MCP-Session-Id", "sid-1")
		if n.Add(1) == 1 {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL, nil)
	if err := tr.Send(context.Background(), map[string]string{}); err != nil {
		t.Fatalf("Send 200: %v", err)
	}
	if err := tr.Send(context.Background(), map[string]string{}); err != nil {
		t.Fatalf("Send 202: %v", err)
	}
	if tr.sessionID != "sid-1" {
		t.Errorf("sessionID = %q, want sid-1 (从响应头捕获)", tr.sessionID)
	}
}

func TestHTTPTransport_Send_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL, nil)
	err := tr.Send(context.Background(), map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("err = %v, want HTTP 404", err)
	}
}

func TestHTTPTransport_Send_Closed(t *testing.T) {
	tr := NewHTTPTransport("http://127.0.0.1:1", nil)
	_ = tr.Close()
	err := tr.Send(context.Background(), map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "transport closed") {
		t.Errorf("err = %v, want 'transport closed'", err)
	}
}

func TestHTTPTransport_Send_MarshalError(t *testing.T) {
	tr := NewHTTPTransport("http://127.0.0.1:1", nil)
	err := tr.Send(context.Background(), make(chan int))
	if err == nil || !strings.Contains(err.Error(), "marshal") {
		t.Errorf("err = %v, want marshal error", err)
	}
}

func TestHTTPTransport_Send_BadURL(t *testing.T) {
	tr := NewHTTPTransport("://bad-url", nil)
	err := tr.Send(context.Background(), map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "create request") {
		t.Errorf("err = %v, want 'create request'", err)
	}
}

// ============================================================================
// HTTPTransport — Receive(GET SSE 路径)
// ============================================================================

func TestHTTPTransport_Receive_SSEWithSession(t *testing.T) {
	var mu sync.Mutex
	sawSession := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		if r.Header.Get("MCP-Session-Id") == "sid-x" {
			sawSession = true
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n\n")
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL, nil)
	tr.SetSessionID("sid-x")
	data, err := tr.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !strings.Contains(string(data), `"ok":true`) {
		t.Errorf("data = %s", string(data))
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawSession {
		t.Error("GET 请求未携带 MCP-Session-Id")
	}
}

func TestHTTPTransport_Receive_405(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL, nil)
	_, err := tr.Receive(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not support SSE GET") {
		t.Errorf("err = %v, want 'does not support SSE GET'", err)
	}
}

func TestHTTPTransport_Receive_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL, nil)
	_, err := tr.Receive(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("err = %v, want HTTP 404", err)
	}
}

// ============================================================================
// readSSE — SSE 流解析
// ============================================================================

func TestReadSSE_Variants(t *testing.T) {
	tr := NewHTTPTransport("http://unused", nil)

	tests := []struct {
		name string
		body string
		want string
		err  bool
	}{
		{"basic", "data: hello\n\n", "hello", false},
		{"multiline-data", "data: a\ndata: b\n\n", "a\nb", false},
		{"skip-other-fields", "event: msg\nid: 1\nretry: 100\n: comment\ndata: payload\n\n", "payload", false},
		{"no-data", ": only comment\n\n", "", true},
		{"eof-with-data", "data: tail", "tail", false},
		{"empty-stream", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tr.readSSE(context.Background(), strings.NewReader(tc.body))
			if tc.err {
				if err == nil {
					t.Fatalf("want error, got data=%s", string(data))
				}
				return
			}
			if err != nil {
				t.Fatalf("readSSE: %v", err)
			}
			if string(data) != tc.want {
				t.Errorf("data = %q, want %q", string(data), tc.want)
			}
		})
	}
}

func TestReadSSE_CtxCancelled(t *testing.T) {
	tr := NewHTTPTransport("http://unused", nil)

	pr, pw := io.Pipe()
	defer func() { _ = pr.Close() }()
	go func() {
		_, _ = pw.Write([]byte("data: x\n"))
	}()
	defer func() { _ = pw.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tr.readSSE(ctx, pr)
	if err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
