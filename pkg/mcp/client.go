package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// Client — 单个 MCP Server 的连接
// ---------------------------------------------------------------------------

// 默认工具调用超时。
// 仅对底层传输的每次读取操作设置超时。30 分钟足够覆盖 Pencil 等设计工具的复杂操作。
// 可通过 ServerConfig.Timeout 覆盖（单位：毫秒）。
const defaultToolTimeout = 30 * time.Minute

// Client 表示与一个 MCP Server 的 1:1 连接。
// 通过 Connect() 创建并完成初始化握手。
type Client struct {
	name       string              // 配置中的 server 名称
	transport  Transport           // 传输层
	serverInfo ImplementationInfo  // initialize 返回的 server 信息
	tools      []ToolDef           // 已发现的工具列表

	httpTransport *HTTPTransport // 非 nil 表示使用 HTTP transport
	requestID     int            // JSON-RPC 请求 ID 自增
	toolTimeout   time.Duration  // 单次工具调用超时
	mu            sync.Mutex

	// logger 用于内部日志输出（如 protocol 级别警告）。
	// 默认为 io.Discard；Manager 连接完成后注入。
	logger *log.Logger

	// OnToolsChanged 在收到 tools/list_changed 通知时被调用。
	// Manager 设置此回调以触发工具列表刷新和重新注册。
	OnToolsChanged func()
}

// Connect 创建 Client 并完成 MCP 初始化握手。
// config 决定使用的传输类型。
func Connect(ctx context.Context, name string, config ServerConfig) (*Client, error) {
	var transport Transport
	var httpTransport *HTTPTransport

	switch config.Type {
	case ServerTypeStdio, "": // 默认 stdio
		t, err := NewStdioTransport(config.Command, config.Args, config.Env)
		if err != nil {
			return nil, fmt.Errorf("stdio transport for %q: %w", name, err)
		}
		transport = t

	case ServerTypeHTTP, ServerTypeSSE:
		ht := NewHTTPTransport(config.URL, config.Headers)
		transport = ht
		httpTransport = ht

	default:
		return nil, fmt.Errorf("unsupported server type %q for %q", config.Type, name)
	}

	toolTimeout := defaultToolTimeout
	if config.Timeout > 0 {
		toolTimeout = time.Duration(config.Timeout) * time.Millisecond
	}

	c := &Client{
		name:          name,
		transport:     transport,
		httpTransport: httpTransport,
		requestID:     1,
		toolTimeout:   toolTimeout,
	}

	if err := c.initialize(ctx); err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("initialize %q: %w", name, err)
	}

	return c, nil
}

// initialize 完成 MCP 初始化握手：initialize → initialized notification。
func (c *Client) initialize(ctx context.Context) error {
	params := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities: ClientCapabilities{
			// 声明客户端不提供 roots 和 sampling
		},
		ClientInfo: ImplementationInfo{
			Name:    "waveloom",
			Title:   "Waveloom",
			Version: "0.1.0",
		},
	}

	result, err := c.sendRequest(ctx, MethodInitialize, params)
	if err != nil {
		return fmt.Errorf("initialize request: %w", err)
	}

	initResult, rpcErr, err := ParseResponse[InitializeResult](result)
	if err != nil {
		return fmt.Errorf("parse initialize response: %w", err)
	}
	if rpcErr != nil {
		return fmt.Errorf("initialize error: %s (code %d)", rpcErr.Message, rpcErr.Code)
	}

	c.serverInfo = initResult.ServerInfo

	// 协议版本协商：检查 server 返回的版本是否兼容
	if initResult.ProtocolVersion != "" && initResult.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported protocol version: server=%q client=%q", initResult.ProtocolVersion, ProtocolVersion)
	}

	// 发送 initialized 通知
	if err := c.sendNotification(ctx, MethodInitialized, nil); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}

	return nil
}

// ListTools 发现 MCP Server 提供的工具列表。
func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error) {
	result, err := c.sendRequest(ctx, MethodToolsList, nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}

	listResult, rpcErr, err := ParseResponse[ListToolsResult](result)
	if err != nil {
		return nil, fmt.Errorf("parse tools/list response: %w", err)
	}
	if rpcErr != nil {
		return nil, fmt.Errorf("tools/list error: %s (code %d)", rpcErr.Message, rpcErr.Code)
	}

	c.mu.Lock()
	c.tools = listResult.Tools
	c.mu.Unlock()

	return listResult.Tools, nil
}

// CallTool 调用 MCP Server 上的指定工具。
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	params := CallToolParams{
		Name:      name,
		Arguments: args,
	}

	result, err := c.sendRequest(ctx, MethodToolsCall, params)
	if err != nil {
		return nil, fmt.Errorf("tools/call %q: %w", name, err)
	}

	callResult, rpcErr, err := ParseResponse[CallToolResult](result)
	if err != nil {
		return nil, fmt.Errorf("parse tools/call response: %w", err)
	}
	if rpcErr != nil {
		return nil, fmt.Errorf("tools/call %q error: %s (code %d)", name, rpcErr.Message, rpcErr.Code)
	}

	return callResult, nil
}

// Tools 返回已缓存的工具列表（不发起网络请求）。
func (c *Client) Tools() []ToolDef {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tools
}

// ServerName 返回配置中的 server 名称。
func (c *Client) ServerName() string {
	return c.name
}

// ServerInfo 返回 initialize 响应中的 server 信息。
func (c *Client) ServerInfo() ImplementationInfo {
	return c.serverInfo
}

// Close 关闭与 MCP Server 的连接。
func (c *Client) Close() error {
	return c.transport.Close()
}

// ---------------------------------------------------------------------------
// JSON-RPC 消息收发
// ---------------------------------------------------------------------------

// nextID 返回下一个请求 ID。
func (c *Client) nextID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.requestID
	c.requestID++
	return id
}

// sendRequest 发送 JSON-RPC 请求并等待响应。
func (c *Client) sendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.httpTransport != nil {
		return c.sendRequestHTTP(ctx, method, params)
	}
	return c.sendRequestStdio(ctx, method, params)
}

// sendRequestStdio 通过 stdio transport 发送请求。
// 循环读取直到收到匹配 ID 的响应，中间的通知被处理并跳过。
func (c *Client) sendRequestStdio(ctx context.Context, method string, params any) (json.RawMessage, error) {
	req, err := NewRequest(c.nextID(), method, params)
	if err != nil {
		return nil, err
	}

	if err := c.transport.Send(ctx, req); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	// 循环读取，跳过通知，直到拿到匹配 ID 的响应
	timeout := c.toolTimeout
	reqID := req.ID
	for {
		recvCtx, cancel := context.WithTimeout(ctx, timeout)
		data, recvErr := c.transport.Receive(recvCtx)
		cancel()
		if recvErr != nil {
			return nil, recvErr
		}

		// 尝试解析为 JSON-RPC 消息
		var raw struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *JSONRPCError  `json:"error"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse message: %w", err)
		}

		// 通知（无 id）→ 处理并继续等待响应
		if raw.ID == nil {
			c.handleNotification(data, raw.Method)
			continue
		}

		// 检查是否匹配请求 ID（用字符串比较避免 JSON 数字类型差异）
		if idEqual(raw.ID, reqID) {
			return data, nil
		}

		// 不匹配的 ID — 极不常见（单线程使用），记录并继续
		if c.logger != nil {
			c.logger.Printf("%s: received response with unexpected id %v (expected %v)", c.name, raw.ID, reqID)
		}
	}
}

// sendRequestHTTP 通过 HTTP transport 发送请求（SendAndReceive）。
func (c *Client) sendRequestHTTP(ctx context.Context, method string, params any) (json.RawMessage, error) {
	req, err := NewRequest(c.nextID(), method, params)
	if err != nil {
		return nil, err
	}
	return c.httpTransport.SendAndReceive(ctx, req)
}

// sendNotification 发送 JSON-RPC 通知（无响应）。
func (c *Client) sendNotification(ctx context.Context, method string, params any) error {
	notif, err := NewNotification(method, params)
	if err != nil {
		return err
	}

	if c.httpTransport != nil {
		return c.httpTransport.Send(ctx, notif)
	}

	return c.transport.Send(ctx, notif)
}

// handleNotification 处理收到的 JSON-RPC 通知。
func (c *Client) handleNotification(data json.RawMessage, method string) {
	switch method {
	case MethodToolsListChanged:
		// 通知到达时触发回调，由 Manager 重新发现并注册工具
		if c.OnToolsChanged != nil {
			go c.OnToolsChanged()
		}
	default:
		// 未知通知类型，静默忽略
	}
}

// idEqual 比较两个 JSON-RPC ID，处理 float64/int/string 等类型差异。
func idEqual(a, b any) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// ---------------------------------------------------------------------------
// toToolResult 将 MCP CallToolResult 转换为 Waveloom ToolResult
// ---------------------------------------------------------------------------

// toToolResult 将 MCP 工具调用结果转换为 Waveloom 的 ToolResult。
func toToolResult(mcpResult *CallToolResult) *tool.ToolResult {
	content := TextContent(mcpResult.Content)

	if mcpResult.IsError {
		return &tool.ToolResult{
			Content: content,
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    tool.ErrKindCommandFailed,
				Message: content,
			},
		}
	}

	return &tool.ToolResult{
		Content: content,
	}
}
