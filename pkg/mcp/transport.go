package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
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
	stdout *bufio.Scanner
	stderr io.ReadCloser

	closeOnce sync.Once
	writeMu   sync.Mutex // 保护 stdin 写入
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
		stdin.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		stderr.Close()
		return nil, fmt.Errorf("start command %q: %w", command, err)
	}

	return &StdioTransport{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewScanner(stdout),
		stderr: stderr,
	}, nil
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

// Receive 从子进程 stdout 读取下一条 JSON-RPC 消息。
func (t *StdioTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	// 在 goroutine 中等待行读取，以便响应 ctx 取消
	type result struct {
		data json.RawMessage
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		if t.stdout.Scan() {
			line := strings.TrimSpace(t.stdout.Text())
			if line == "" {
				ch <- result{err: fmt.Errorf("empty line from server")}
				return
			}
			ch <- result{data: json.RawMessage(line)}
		} else {
			err := t.stdout.Err()
			if err == nil {
				err = io.EOF
			}
			ch <- result{err: fmt.Errorf("read stdout: %w", err)}
		}
	}()

	select {
	case r := <-ch:
		return r.data, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close 关闭 stdin 并等待子进程退出。
func (t *StdioTransport) Close() error {
	var err error
	t.closeOnce.Do(func() {
		_ = t.stdin.Close()
		_ = t.stderr.Close()
		err = t.cmd.Wait()
	})
	return err
}

// StderrReader 返回 stderr 的读取器，供外部日志记录。
func (t *StdioTransport) StderrReader() io.ReadCloser {
	return t.stderr
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
		url:        url,
		headers:    headers,
		httpClient: &http.Client{},
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
func (t *HTTPTransport) readSSE(ctx context.Context, body io.Reader) (json.RawMessage, error) {
	scanner := bufio.NewScanner(body)
	var lastData string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "data: ") {
			lastData = strings.TrimPrefix(line, "data: ")
		}

		// 空行表示事件结束
		if line == "" && lastData != "" {
			return json.RawMessage(lastData), nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SSE: %w", err)
	}

	if lastData != "" {
		return json.RawMessage(lastData), nil
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
