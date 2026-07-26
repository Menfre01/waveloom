package lsp
import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// Client — LSP JSON-RPC stdio 客户端
// ---------------------------------------------------------------------------

// Client 与一个 Language Server 进程通过 stdin/stdout JSON-RPC 通信。
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	mu       sync.Mutex          // 保护 stdin 写入串行化
	nextID   atomic.Int32        // 自增请求 ID
	pending  map[int]chan *rawMessage // id → 等待响应
	pendMu   sync.Mutex
	notify   map[string][]func(json.RawMessage) // method → 通知处理器
	notifyMu sync.RWMutex

	done chan struct{}
}

// rawMessage 是读取原始 JSON 消息结果的中间表示。
type rawMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`   // nil 表示 Notification
	Method  string          `json:"method"`
	Result  json.RawMessage `json:"result"`
	Error   *ResponseError  `json:"error"`
	Params  json.RawMessage `json:"params"`
}

// ---------------------------------------------------------------------------
// 构造函数
// ---------------------------------------------------------------------------

// NewClient 启动指定的 Language Server 命令并完成 initialize 握手。
// stderrPath 为空时 stderr 被丢弃；非空时写入指定文件。
func NewClient(command string, args []string, rootURI string, stderrPath string) (*Client, error) {
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
		notify: make(map[string][]func(json.RawMessage)),
		done:   make(chan struct{}),
	}
	c.nextID.Store(1)

	// 后台消费 stderr
	go c.consumeStderr(stderrPipe, stderrPath)

	// 后台读取 stdout
	go c.readLoop()

	// initialize 握手
	if err := c.initialize(rootURI); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("lsp: initialize: %w", err)
	}

	return c, nil
}

// ---------------------------------------------------------------------------
// 公开方法
// ---------------------------------------------------------------------------

// Call 发送 JSON-RPC 请求并等待响应，支持 context 取消。
// result 必须是一个指针，用于 JSON 反序列化。
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
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
		if result != nil && raw.Result != nil {
			if err := json.Unmarshal(raw.Result, result); err != nil {
				return fmt.Errorf("lsp: unmarshal %s result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return fmt.Errorf("lsp: client closed")
	}
}

// Notify 发送 JSON-RPC 通知 (no response expected)。
func (c *Client) Notify(method string, params any) error {
	paramsJSON, err := marshal(params)
	if err != nil {
		return fmt.Errorf("lsp: marshal notify params: %w", err)
	}

	notif := Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
	}

	data, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("lsp: marshal notification: %w", err)
	}

	return c.write(data)
}

// OnNotification 注册指定 method 的通知处理器。
// 多次调用会追加 handler，按注册顺序调用。
func (c *Client) OnNotification(method string, handler func(json.RawMessage)) {
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	c.notify[method] = append(c.notify[method], handler)
}

// Close 发送 shutdown 请求 → exit 通知 → 关闭 stdin → 等待进程退出。
// Waits up to 5s for graceful exit, then kills the process.
func (c *Client) Close() error {
	// Send shutdown (give server a chance to clean up)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = c.Call(shutdownCtx, "shutdown", nil, nil)
	shutdownCancel()
	// Send exit
	_ = c.Notify("exit", nil)
	// Close stdin to trigger readLoop exit
	_ = c.stdin.Close()

	// Wait for graceful exit with timeout
	done := make(chan struct{})
	go func() {
		_ = c.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = c.cmd.Process.Kill()
	}
	return nil
}

// ---------------------------------------------------------------------------
// 内部方法
// ---------------------------------------------------------------------------

// initialize 发送 initialize 请求 → initialized 通知，完成 LSP 握手。
func (c *Client) initialize(rootURI string) error {
	params := InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   rootURI,
		Capabilities: ClientCapabilities{
			TextDocument: &TextDocumentClientCapabilities{
				Diagnostic: &DiagnosticClientCapabilities{
					DynamicRegistration: false,
				},
			},
		},
	}

	var result InitializeResult
	if err := c.Call(context.Background(), "initialize", params, &result); err != nil {
		return err
	}

	// 发送 initialized 通知（LSP 规范要求 initialize 之后必须发送）
	return c.Notify("initialized", struct{}{})
}

// write 写入一行 JSON-RPC 消息到 stdin (Content-Length header + body)。
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

// readLoop 持续读取 stdout 的 JSON-RPC 消息,分发到 pending 请求或 notify 处理器。
// Handles multi-header responses (Content-Length + optional Content-Type per LSP 3.17).
func (c *Client) readLoop() {
	defer close(c.done)

	reader := bufio.NewReader(c.stdout)
	for {
		// Read headers line by line until empty line.
		var contentLength int
		hasContentLength := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break // end of headers
			}
			if strings.HasPrefix(line, "Content-Length:") {
				if _, err := fmt.Sscanf(line, "Content-Length: %d", &contentLength); err == nil {
					hasContentLength = true
				}
			}
			// Content-Type and other headers are silently ignored
		}
		if !hasContentLength || contentLength < 0 {
			return
		}

		// Read body
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(reader, body); err != nil {
			return
		}

		var raw rawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			continue
		}

		// Notification (id == nil)
		if raw.ID == nil {
			c.notifyMu.RLock()
			handlers := c.notify[raw.Method]
			c.notifyMu.RUnlock()
			for _, h := range handlers {
				h(raw.Params)
			}
			continue
		}

		// Response / Request — 分发到等待的 channel
		c.pendMu.Lock()
		ch := c.pending[*raw.ID]
		c.pendMu.Unlock()

		if ch != nil {
			ch <- &raw
		}
	}
}

// consumeStderr 消费 LSP server 的 stderr。
// stderrPath 为空时丢弃，非空时写入文件。
func (c *Client) consumeStderr(r io.Reader, stderrPath string) {
	if stderrPath == "" {
		_, _ = io.Copy(io.Discard, r)
		return
	}
	f, err := os.Create(stderrPath)
	if err != nil {
		_, _ = io.Copy(io.Discard, r)
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = io.Copy(f, r)
}

// marshal 序列化为 JSON，nil → "null"。
func marshal(v any) (json.RawMessage, error) {
	if v == nil {
		return json.RawMessage("null"), nil
	}
	return json.Marshal(v)
}
