package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// Client — LSP JSON-RPC 客户端
// ---------------------------------------------------------------------------

// Client 与一个 Language Server 进程通过 stdin/stdout JSON-RPC 通信。
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu       sync.Mutex       // 保护 stdin 写入
	nextID   atomic.Int32     // 自增请求 ID
	pending  map[int]chan *rawMessage // id → 等待响应
	pendMu   sync.Mutex
	notify   map[string]func(json.RawMessage) // method → 通知处理器
	notifyMu sync.RWMutex

	done chan struct{}
}

// rawMessage 是读取原始 JSON 消息结果的中间表示。
type rawMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`  // nil 表示 Notification
	Method  string          `json:"method"`
	Result  json.RawMessage `json:"result"`
	Error   *ResponseError  `json:"error"`
	Params  json.RawMessage `json:"params"`
}

// ---------------------------------------------------------------------------
// 构造函数
// ---------------------------------------------------------------------------

// NewClient 启动指定的 Language Server 命令并完成 initialize 握手。
// command 和 args 如 "gopls" / ["gopls"]。
func NewClient(command string, args []string, rootURI string) (*Client, error) {
	cmd := exec.Command(command, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("lsp: start %s: %w", command, err)
	}

	c := &Client{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		pending: make(map[int]chan *rawMessage),
		notify: make(map[string]func(json.RawMessage)),
		done:   make(chan struct{}),
	}
	c.nextID.Store(1)

	// 后台读取 stdout
	go c.readLoop()
	// 后台消费 stderr（丢弃，避免管道阻塞）
	go func() { io.Copy(io.Discard, stderrPipe) }()

	// initialize 握手
	if err := c.initialize(rootURI); err != nil {
		c.Close()
		return nil, fmt.Errorf("lsp: initialize: %w", err)
	}

	return c, nil
}

// ---------------------------------------------------------------------------
// 公开方法
// ---------------------------------------------------------------------------

// Call 发送 JSON-RPC 请求并等待响应。
// result 必须是一个指针，用于 JSON 反序列化。
// Deprecated: 使用 CallContext 以支持 context 取消和超时。
func (c *Client) Call(method string, params, result any) error {
	return c.CallContext(context.Background(), method, params, result)
}

// CallContext 发送 JSON-RPC 请求并等待响应，支持 context 取消。
// result 必须是一个指针，用于 JSON 反序列化。
func (c *Client) CallContext(ctx context.Context, method string, params, result any) error {
	id := int(c.nextID.Add(1))

	paramsJSON, err := marshal(params)
	if err != nil {
		return fmt.Errorf("lsp: marshal params: %w", err)
	}

	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  paramsJSON,
	}

	ch := make(chan *rawMessage, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()

	defer func() {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
	}()

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("lsp: marshal request: %w", err)
	}

	if err := c.write(data); err != nil {
		return fmt.Errorf("lsp: write request: %w", err)
	}

	// 等待响应、context 取消或 client 关闭
	select {
	case raw := <-ch:
		if raw.Error != nil {
			return fmt.Errorf("lsp: %s error %d: %s", method, raw.Error.Code, raw.Error.Message)
		}
		if result != nil {
			if err := json.Unmarshal(raw.Result, result); err != nil {
				return fmt.Errorf("lsp: unmarshal result: %w", err)
			}
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("lsp: %s: %w", method, ctx.Err())
	case <-c.done:
		return fmt.Errorf("lsp: client closed")
	}
}

// Notify 发送 JSON-RPC 通知（不等待响应）。
func (c *Client) Notify(method string, params any) error {
	paramsJSON, err := marshal(params)
	if err != nil {
		return fmt.Errorf("lsp: marshal params: %w", err)
	}

	ntf := Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
	}

	data, err := json.Marshal(ntf)
	if err != nil {
		return fmt.Errorf("lsp: marshal notification: %w", err)
	}

	return c.write(data)
}

// OnNotification 注册通知处理器。method 如 "textDocument/publishDiagnostics"。
func (c *Client) OnNotification(method string, handler func(json.RawMessage)) {
	c.notifyMu.Lock()
	c.notify[method] = handler
	c.notifyMu.Unlock()
}

// Close 发送 shutdown/exit 并关闭进程。
func (c *Client) Close() error {
	// 尽力发送 shutdown 请求（忽略错误，进程可能已退出）
	_ = c.Call("shutdown", nil, nil)
	_ = c.Notify("exit", nil)

	close(c.done)

	// 等待进程退出（最多 5 秒）
	done := make(chan error, 1)
	go func() {
		done <- c.cmd.Wait()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		c.cmd.Process.Kill()
	}

	c.stdin.Close()
	c.stdout.Close()
	return nil
}

// CommandRunning 返回底层命令是否仍在运行。
func (c *Client) CommandRunning() bool {
	if c.cmd == nil || c.cmd.Process == nil {
		return false
	}
	return c.cmd.ProcessState == nil || !c.cmd.ProcessState.Exited()
}

// ---------------------------------------------------------------------------
// 内部方法
// ---------------------------------------------------------------------------

// initialize 完成 LSP 初始化握手。
func (c *Client) initialize(rootURI string) error {
	params := InitializeParams{
		ProcessID: 0, // 不关联父进程
		RootURI:   rootURI,
		Capabilities: ClientCapabilities{
			TextDocument: &TextDocumentClientCapabilities{
				Hover: &HoverClientCapabilities{
					ContentFormat: []string{"markdown", "plaintext"},
				},
			},
		},
	}

	var result InitializeResult
	if err := c.Call("initialize", params, &result); err != nil {
		return err
	}

	// 发送 initialized 通知
	if err := c.Notify("initialized", struct{}{}); err != nil {
		return err
	}

	return nil
}

// write 写入一条 JSON 消息（带 Content-Length 头）。
func (c *Client) write(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := c.stdin.Write([]byte(header)); err != nil {
		return err
	}
	_, err := c.stdin.Write(data)
	return err
}

// readLoop 持续从 stdout 读取 JSON-RPC 消息并分发。
func (c *Client) readLoop() {
	buf := bufio.NewReader(c.stdout)
	for {
		select {
		case <-c.done:
			return
		default:
		}

		msg, err := readMessage(buf)
		if err != nil {
			// 进程退出或管道错误，停止读取
			return
		}

		c.dispatch(msg)
	}
}

// dispatch 将消息路由到等待的请求处理器或通知处理器。
func (c *Client) dispatch(msg *rawMessage) {
	// 通知（无 id）
	if msg.ID == nil {
		c.notifyMu.RLock()
		handler := c.notify[msg.Method]
		c.notifyMu.RUnlock()
		if handler != nil {
			handler(msg.Params)
		}
		return
	}

	// 响应
	id := *msg.ID
	c.pendMu.Lock()
	ch := c.pending[id]
	c.pendMu.Unlock()

	if ch != nil {
		ch <- msg
	}
}

// ---------------------------------------------------------------------------
// 消息读取
// ---------------------------------------------------------------------------

// readMessage 从 reader 读取一条 Content-Length 头 + JSON body 组成的消息。
func readMessage(r *bufio.Reader) (*rawMessage, error) {
	var contentLength int

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			// 头部结束
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			contentLength, err = strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("lsp: bad Content-Length: %s", v)
			}
		}
		// 忽略其他头（Content-Type 等）
	}

	if contentLength <= 0 {
		return nil, fmt.Errorf("lsp: missing Content-Length header")
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("lsp: read body: %w", err)
	}

	var msg rawMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("lsp: unmarshal message: %w", err)
	}
	return &msg, nil
}

// marshal 将值序列化为 json.RawMessage。
func marshal(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}
