package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Transport 接口
// ---------------------------------------------------------------------------

// Transport 抽象 MCP 传输层，支持 stdio 和 Streamable HTTP 两种实现。
type Transport interface {
	// Send 发送 JSON-RPC 消息。msg 必须可 JSON 序列化。
	Send(ctx context.Context, msg any) error

	// Receive 接收下一条 JSON-RPC 消息，返回原始 JSON。
	Receive(ctx context.Context) (json.RawMessage, error)

	// Close 关闭传输，释放相关资源。
	Close() error
}

// ---------------------------------------------------------------------------
// StdioTransport — 基于子进程 stdin/stdout 的传输
// ---------------------------------------------------------------------------

// StdioTransport 通过子进程的标准输入输出与 MCP Server 通信。
// 每条 JSON-RPC 消息独占一行（换行符分隔），消息内不含嵌入换行。
type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr io.ReadCloser

	readCh   chan readResult // stdout 行读取通道（后台 goroutine 填充）
	stdoutDone chan struct{}  // stdout 读取 goroutine 完成信号
	stderrDone chan struct{}  // stderr 消费 goroutine 完成信号
	stderrBuf  bytes.Buffer   // stderr 日志缓冲区

	closeOnce sync.Once
	writeMu   sync.Mutex // 保护 stdin 写入
	stderrMu  sync.Mutex // 保护 stderrBuf
}

type readResult struct {
	data json.RawMessage
	err  error
}

// NewStdioTransport 创建 stdio 传输。
// command 是要执行的程序路径，args 是参数，env 是额外环境变量。
func NewStdioTransport(command string, args []string, env map[string]string) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)

	// 设置环境变量
	if len(env) > 0 {
		cmd.Env = cmd.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start command %q: %w", command, err)
	}

	scanner := bufio.NewScanner(stdout)
	// MCP 响应可能很大（如包含 base64 图片的 get_screenshot），增大缓冲区到 10MB
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	t := &StdioTransport{
		cmd:        cmd,
		stdin:      stdin,
		stderr:     stderr,
		readCh:     make(chan readResult),
		stdoutDone: make(chan struct{}),
		stderrDone: make(chan struct{}),
	}

	// 后台持续读取 stdout，发送到 readCh
	go t.readStdout(scanner)
	// 持续消费 stderr 防止管道缓冲区满导致子进程阻塞
	go t.drainStderr()

	return t, nil
}

// Send 将 JSON-RPC 消息写入子进程 stdin。
func (t *StdioTransport) Send(ctx context.Context, msg any) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	if _, err := io.WriteString(t.stdin, string(data)+"\n"); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return nil
}

// readStdout 在后台 goroutine 中持续从 scanner 读取行并发送到 readCh。
// 当 scanner 结束（EOF/错误）时关闭 readCh 并通知 stdoutDone。
func (t *StdioTransport) readStdout(scanner *bufio.Scanner) {
	defer close(t.stdoutDone)
	defer close(t.readCh)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		select {
		case t.readCh <- readResult{data: json.RawMessage(line)}:
		case <-t.stdoutDone:
			return
		}
	}
	if err := scanner.Err(); err != nil {
		select {
		case t.readCh <- readResult{err: fmt.Errorf("read stdout: %w", err)}:
		case <-t.stdoutDone:
		}
	}
}

// Receive 从后台读取通道获取下一条 JSON-RPC 消息，支持 context 取消。
func (t *StdioTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	select {
	case r, ok := <-t.readCh:
		if !ok {
			return nil, io.EOF
		}
		return r.data, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close 关闭 stdin 并等待子进程退出。
func (t *StdioTransport) Close() error {
	var err error
	t.closeOnce.Do(func() {
		_ = t.stdin.Close()  // 通知子进程结束
		_ = t.stderr.Close() // 触发 drainStderr 退出
		<-t.stderrDone
		<-t.stdoutDone
		err = t.cmd.Wait()
	})
	return err
}

// drainStderr 持续读取 stderr 并缓冲，防止管道缓冲区满阻塞子进程。
func (t *StdioTransport) drainStderr() {
	defer close(t.stderrDone)
	buf := make([]byte, 4096)
	for {
		n, err := t.stderr.Read(buf)
		if n > 0 {
			t.stderrMu.Lock()
			t.stderrBuf.Write(buf[:n])
			t.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// Stderr 返回 stderr 输出缓冲区的副本，用于日志记录。
func (t *StdioTransport) Stderr() string {
	t.stderrMu.Lock()
	defer t.stderrMu.Unlock()
	return t.stderrBuf.String()
}

// ---------------------------------------------------------------------------
// HTTPTransport — 基于 Streamable HTTP 的传输
// ---------------------------------------------------------------------------

// HTTPTransport 通过 HTTP POST + SSE 与远程 MCP Server 通信。
// 协议版本：2025-11-25 (Streamable HTTP)
type HTTPTransport struct {
	url        string
	headers    map[string]string
	sessionID  string
	httpClient *http.Client
	closed     bool
	mu         sync.Mutex
}

// NewHTTPTransport 创建 HTTP 传输。
func NewHTTPTransport(url string, headers map[string]string) *HTTPTransport {
	return &HTTPTransport{
		url:     url,
		headers: headers,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// SetSessionID 设置会话 ID（从 InitializeResult 的 MCP-Session-Id 头获取）。
func (t *HTTPTransport) SetSessionID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionID = id
}

// Send 发送 JSON-RPC 消息到 MCP endpoint。
func (t *HTTPTransport) Send(ctx context.Context, msg any) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return fmt.Errorf("transport closed")
	}
	t.mu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion)

	t.mu.Lock()
	if t.sessionID != "" {
		req.Header.Set("MCP-Session-Id", t.sessionID)
	}
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	t.mu.Unlock()

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 捕获 session ID
	if sid := resp.Header.Get("MCP-Session-Id"); sid != "" {
		t.SetSessionID(sid)
	}

	if resp.StatusCode == 202 {
		return nil // Accepted, no body (notification/response)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Receive 发送 POST 请求并读取响应（或 SSE 流）。
// HTTP transport 中 Send 和 Receive 是耦合的——每次 Send 后紧跟一次 Receive。
// 此方法在 POST 请求后读取响应体。
func (t *HTTPTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	// HTTP transport 的 Receive 依赖前一个 Send 的响应
	// 简化处理：重新发起一个 POST 获取 SSE 流（用于服务端推送）
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion)

	t.mu.Lock()
	if t.sessionID != "" {
		req.Header.Set("MCP-Session-Id", t.sessionID)
	}
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	t.mu.Unlock()

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == 405 {
		return nil, fmt.Errorf("server does not support SSE GET")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return t.readSSE(ctx, resp.Body)
}

// SendAndReceive 发送请求并等待 JSON 响应（非流式路径）。
// 这是 HTTP transport 的主要使用方式：POST 请求，直接读取 JSON 响应。
func (t *HTTPTransport) SendAndReceive(ctx context.Context, msg any) (json.RawMessage, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, fmt.Errorf("transport closed")
	}
	t.mu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion)

	t.mu.Lock()
	if t.sessionID != "" {
		req.Header.Set("MCP-Session-Id", t.sessionID)
	}
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	t.mu.Unlock()

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 捕获 session ID
	if sid := resp.Header.Get("MCP-Session-Id"); sid != "" {
		t.SetSessionID(sid)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		// SSE 流响应
		return t.readSSE(ctx, resp.Body)
	}

	// JSON 响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return json.RawMessage(body), nil
}

// readSSE 从 SSE 流中读取并提取 JSON-RPC 响应。
// 支持 SSE 标准字段：data、event、id、retry，以及多行 data 拼接。
func (t *HTTPTransport) readSSE(ctx context.Context, body io.Reader) (json.RawMessage, error) {
	scanner := bufio.NewScanner(body)
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()

		// 空行表示事件结束
		if line == "" {
			if len(dataLines) > 0 {
				// 多行 data 用换行符拼接（SSE 标准）
				return json.RawMessage(strings.Join(dataLines, "\n")), nil
			}
			continue
		}

		// 收集 data 行（跳过 event/id/retry/注释）
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimPrefix(data, " ") // 去掉冒号后的可选空格
			dataLines = append(dataLines, data)
		}
		// 忽略 event:、id:、retry: 和注释行 (:)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SSE: %w", err)
	}

	// 流结束时有未完成的数据
	if len(dataLines) > 0 {
		return json.RawMessage(strings.Join(dataLines, "\n")), nil
	}

	return nil, io.EOF
}

// Close 关闭传输。
func (t *HTTPTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	return nil
}

// ensure interfaces are satisfied
var (
	_ Transport = (*StdioTransport)(nil)
	_ Transport = (*HTTPTransport)(nil)
)
