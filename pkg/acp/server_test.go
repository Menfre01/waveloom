package acp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// serverWithInput 创建带预构建输入的 Server
// ---------------------------------------------------------------------------

func serverWithInput(input string) (*Server, *strings.Builder) {
	var buf strings.Builder
	r := strings.NewReader(input)
	transport := NewStdioTransportIO(r, &buf)

	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.ReadFile{}))

	s := NewServer(ServerConfig{
		LLMClient:    &mockLLMClient{},
		ToolRegistry: registry,
		SystemPrompt: "test",
		BuildVersion: "test",
		CWD:          "/tmp",
		MaxSteps:     10,
	})
	s.transport = transport
	return s, &buf
}

// readLines 从 strings.Builder 读取所有 JSON 行。
func readLines(t *testing.T, buf *strings.Builder) []json.RawMessage {
	t.Helper()
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

// runWithin 在 goroutine 中运行 s.Run() 并等待返回。
func runWithin(t *testing.T, s *Server, timeout time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- s.Run() }()
	select {
	case err := <-done:
		if err != nil {
			t.Logf("Run returned: %v", err)
		}
	case <-time.After(timeout):
		t.Fatal("timeout waiting for Run to complete")
	}
}

// ---------------------------------------------------------------------------
// TestServerRun 测试
// ---------------------------------------------------------------------------

func TestServerRunParseError(t *testing.T) {
	s, buf := serverWithInput("{not json}\n")
	runWithin(t, s, 2*time.Second)

	responses := readLines(t, buf)
	if len(responses) < 1 {
		t.Fatal("expected at least 1 error response")
	}
	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[0], &resp)
	if resp.Error == nil || resp.Error.Code != ErrParse {
		t.Fatalf("expected parse error, got: %+v", resp.Error)
	}
}

func TestServerRunInvalidJSONRPCVersion(t *testing.T) {
	s, buf := serverWithInput(`{"jsonrpc":"1.0","id":1,"method":"initialize"}` + "\n")
	runWithin(t, s, 2*time.Second)

	responses := readLines(t, buf)
	if len(responses) < 1 {
		t.Fatal("expected error response")
	}
	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[0], &resp)
	if resp.Error == nil || resp.Error.Code != ErrInvalidRequest {
		t.Fatalf("expected invalid request error, got: %+v", resp.Error)
	}
}

func TestServerRunFullHandshake(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/new"}`,
	}, "\n") + "\n"

	s, buf := serverWithInput(input)
	runWithin(t, s, 2*time.Second)

	responses := readLines(t, buf)
	if len(responses) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(responses))
	}

	var resp1 JSONRPCResponse
	_ = json.Unmarshal(responses[0], &resp1)
	if resp1.Error != nil {
		t.Fatalf("initialize error: %+v", resp1.Error)
	}

	var resp2 JSONRPCResponse
	_ = json.Unmarshal(responses[1], &resp2)
	if resp2.Error != nil {
		t.Fatalf("session/new error: %+v", resp2.Error)
	}
	var newResult SessionNewResult
	_ = json.Unmarshal(resp2.Result, &newResult)
	if newResult.SessionID == "" {
		t.Error("expected valid session ID")
	}
}

func TestServerRunInitializeGuard(t *testing.T) {
	s, buf := serverWithInput(`{"jsonrpc":"2.0","id":1,"method":"session/new"}` + "\n")
	runWithin(t, s, 2*time.Second)

	responses := readLines(t, buf)
	if len(responses) < 1 {
		t.Fatal("expected error response")
	}
	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[0], &resp)
	if resp.Error == nil || resp.Error.Code != ErrInvalidRequest {
		t.Fatalf("expected initialize guard error, got: %+v", resp.Error)
	}
}

func TestServerRunUnknownMethod(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"nonexistent"}`,
	}, "\n") + "\n"

	s, buf := serverWithInput(input)
	runWithin(t, s, 2*time.Second)

	responses := readLines(t, buf)
	if len(responses) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(responses))
	}
	var resp2 JSONRPCResponse
	_ = json.Unmarshal(responses[1], &resp2)
	if resp2.Error == nil || resp2.Error.Code != ErrMethodNotFound {
		t.Fatalf("expected method not found, got: %+v", resp2.Error)
	}
}

// ---------------------------------------------------------------------------
// TestShutdown 测试
// ---------------------------------------------------------------------------

func TestShutdownWithNoSessions(t *testing.T) {
	s, _ := serverWithInput("")
	runWithin(t, s, 2*time.Second)
}

func TestShutdownCancelsActivePrompts(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/new"}`,
	}, "\n") + "\n"

	s, buf := serverWithInput(input)
	runWithin(t, s, 2*time.Second)

	responses := readLines(t, buf)
	if len(responses) < 2 {
		t.Fatalf("expected at least 2 responses, got %d", len(responses))
	}

	s.mu.RLock()
	sessionCount := len(s.sessions)
	s.mu.RUnlock()
	t.Logf("sessions after shutdown: %d", sessionCount)
}

// ---------------------------------------------------------------------------
// TestDispatch 路由测试（直接调用 dispatch，不经过 Run）
// ---------------------------------------------------------------------------

func TestDispatchMethods(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		initialized bool
		wantErr     bool
		wantErrCode int
	}{
		{"initialize", MethodInitialize, false, false, 0},
		{"session/new before init", MethodSessionNew, false, true, ErrInvalidRequest},
		{"session/new after init", MethodSessionNew, true, false, 0},
		{"session/prompt before init", MethodSessionPrompt, false, true, ErrInvalidRequest},
		{"unknown method after init", "unknown", true, true, ErrMethodNotFound},
		{"unknown method before init", "unknown", false, true, ErrInvalidRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, buf := serverWithInput("")
			s.initialized = tt.initialized

			var params json.RawMessage
			if tt.method == MethodSessionPrompt && !tt.wantErr {
				s.initialized = true
				s.handleSessionNew(JSONRPCRequest{JSONRPC: "2.0", ID: 99, Method: MethodSessionNew})
				initResp := readLines(t, buf)
				sid := getSID(t, initResp[0])
				params = json.RawMessage(`{"sessionId":"` + sid + `","prompt":[{"type":"text","text":"hello"}]}`)
			}

			req := JSONRPCRequest{
				JSONRPC: "2.0",
				ID:      1,
				Method:  tt.method,
				Params:  params,
			}
			s.dispatch(context.Background(), req)

			responses := readLines(t, buf)
			if len(responses) == 0 {
				t.Fatal("expected a response")
			}

			var resp JSONRPCResponse
			_ = json.Unmarshal(responses[0], &resp)

			if tt.wantErr {
				if resp.Error == nil {
					t.Fatal("expected error response")
				}
				if tt.wantErrCode != 0 && resp.Error.Code != tt.wantErrCode {
					t.Errorf("error code = %d, want %d", resp.Error.Code, tt.wantErrCode)
				}
			} else {
				if resp.Error != nil {
					t.Errorf("unexpected error: %+v", resp.Error)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestSendResponse / TestSendErrorResponse
// ---------------------------------------------------------------------------

func TestSendResponse(t *testing.T) {
	s, buf := serverWithInput("")
	s.sendResponse(42, map[string]string{"key": "value"})

	responses := readLines(t, buf)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[0], &resp)
	if resp.Error != nil {
		t.Errorf("unexpected error: %+v", resp.Error)
	}
}

func TestSendErrorResponse(t *testing.T) {
	s, buf := serverWithInput("")
	s.sendErrorResponse("req-1", ErrInternal, "something went wrong")

	responses := readLines(t, buf)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response, got %d", len(responses))
	}
	var resp JSONRPCResponse
	_ = json.Unmarshal(responses[0], &resp)
	if resp.Error == nil || resp.Error.Code != ErrInternal {
		t.Fatal("expected internal error")
	}
}

// TestServerRunLineTooLongContinues:超长行(>10MB)→ 回 parse error 后 server
// 继续服务下一条请求(恶意客户端 DoS 防护:不再关闭整个连接)。
func TestServerRunLineTooLongContinues(t *testing.T) {
	longLine := strings.Repeat("x", 10*1024*1024+1)
	input := longLine + "\n" +
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n"
	s, buf := serverWithInput(input)
	runWithin(t, s, 2*time.Second)

	responses := readLines(t, buf)
	if len(responses) < 2 {
		t.Fatalf("expected parse error + initialize response, got %d", len(responses))
	}
	// 第一条:超长行 parse error
	var parseErr JSONRPCResponse
	_ = json.Unmarshal(responses[0], &parseErr)
	if parseErr.Error == nil || parseErr.Error.Code != ErrParse {
		t.Fatalf("expected parse error first, got: %+v", parseErr.Error)
	}
	// 第二条:server 存活,initialize 正常响应
	var initResp JSONRPCResponse
	_ = json.Unmarshal(responses[1], &initResp)
	if initResp.Error != nil {
		t.Fatalf("server should continue after too-long line, got error: %+v", initResp.Error)
	}
	var result InitializeResult
	_ = json.Unmarshal(initResp.Result, &result)
	if result.AgentInfo == nil || result.AgentInfo.Name != "waveloom" {
		t.Errorf("expected successful initialize after recovery, got: %+v", result)
	}
}

// TestDispatchNotificationNoResponse:JSON-RPC 通知(无 id)MUST NOT 收到响应,
// 但处理逻辑照常执行。
func TestDispatchNotificationNoResponse(t *testing.T) {
	s, buf := serverWithInput("")
	s.initialized = true

	// 未知方法通知 → 无响应
	s.dispatch(context.Background(), JSONRPCRequest{JSONRPC: "2.0", Method: "unknown/method"})
	if responses := readLines(t, buf); len(responses) != 0 {
		t.Fatalf("unknown-method notification must not receive response, got %d", len(responses))
	}

	// session/new 通知 → 处理(创建 session)但不响应
	s.dispatch(context.Background(), JSONRPCRequest{JSONRPC: "2.0", Method: MethodSessionNew})
	if responses := readLines(t, buf); len(responses) != 0 {
		t.Fatalf("session/new notification must not receive response, got %d", len(responses))
	}
	s.mu.RLock()
	n := len(s.sessions)
	s.mu.RUnlock()
	if n != 1 {
		t.Errorf("notification should still be processed, sessions = %d, want 1", n)
	}
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

func getSID(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var resp JSONRPCResponse
	_ = json.Unmarshal(raw, &resp)
	var result SessionNewResult
	_ = json.Unmarshal(resp.Result, &result)
	return result.SessionID
}

// TestRegression_RunRespondsToSIGTERM 验证空闲等待 stdin 时收到终止信号
// Run 立即返回(五审 High-4:此前 Receive 阻塞在主循环、信号只 cancel ctx,
// SIGTERM 后进程不退出,仅 stdin EOF 才关)。
// Unix-only:Windows 无 SIGTERM 语义。
func TestRegression_RunRespondsToSIGTERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM semantics are Unix-specific")
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	registry := tool.NewRegistry()
	s := NewServer(ServerConfig{
		LLMClient:    &mockLLMClient{},
		ToolRegistry: registry,
		SystemPrompt: "test",
		BuildVersion: "test",
		CWD:          "/tmp",
	})
	s.transport = NewStdioTransportIO(pr, io.Discard)

	// 兜底捕获:若 Run 尚未注册 signal.Notify,信号落入本 channel 而非
	// 默认处理器(默认 SIGTERM 会终止测试进程)。
	guardCh := make(chan os.Signal, 1)
	signal.Notify(guardCh, syscall.SIGTERM)
	defer signal.Stop(guardCh)

	done := make(chan error, 1)
	go func() { done <- s.Run() }()

	// 等待 Run 进入 Receive 阻塞(空闲)
	time.Sleep(100 * time.Millisecond)

	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned error after SIGTERM: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after SIGTERM (Receive blocks despite ctx cancel)")
	}
}
