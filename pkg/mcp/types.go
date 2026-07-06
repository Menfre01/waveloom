// Package mcp 实现 Model Context Protocol (MCP) 客户端。
//
// MCP 是 Anthropic 推出的开放标准，标准化 AI 应用与外部工具/数据源的连接。
// 本包实现 MCP Client 角色：连接外部 MCP Server，发现其工具并注册到 Waveloom 工具系统。
//
// 协议版本：2025-11-25
// 协议基础：JSON-RPC 2.0
package mcp

import "encoding/json"

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 基础消息
// ---------------------------------------------------------------------------

// JSONRPCRequest 表示 JSON-RPC 2.0 请求。
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse 表示 JSON-RPC 2.0 响应（成功或错误）。
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCNotification 表示 JSON-RPC 2.0 通知（无 id，无响应）。
type JSONRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCError 表示 JSON-RPC 2.0 错误对象。
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ---------------------------------------------------------------------------
// MCP 协议常量
// ---------------------------------------------------------------------------

const (
	// ProtocolVersion 是本实现支持的 MCP 协议版本。
	ProtocolVersion = "2025-11-25"

	// JSONRPCVersion 是 JSON-RPC 版本。
	JSONRPCVersion = "2.0"
)

// MCP 方法名
const (
	MethodInitialize     = "initialize"
	MethodInitialized    = "notifications/initialized"
	MethodToolsList      = "tools/list"
	MethodToolsCall      = "tools/call"
	MethodToolsListChanged = "notifications/tools/list_changed"
	MethodPing           = "ping"
)

// ---------------------------------------------------------------------------
// Initialize 握手
// ---------------------------------------------------------------------------

// InitializeParams 是 initialize 请求的参数。
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      ImplementationInfo  `json:"clientInfo"`
}

// InitializeResult 是 initialize 请求的成功响应。
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ImplementationInfo  `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// ImplementationInfo 描述客户端或服务端的实现信息。
type ImplementationInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// ClientCapabilities 描述客户端支持的能力。
type ClientCapabilities struct {
	Roots    *RootsCapability    `json:"roots,omitempty"`
	Sampling *SamplingCapability `json:"sampling,omitempty"`
}

// ServerCapabilities 描述服务端支持的能力。
type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
	Logging   *struct{}            `json:"logging,omitempty"`
}

// RootsCapability 客户端 roots 能力。
type RootsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// SamplingCapability 客户端 sampling 能力。
type SamplingCapability struct{}

// ToolsCapability 服务端 tools 能力。
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability 服务端 resources 能力。
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptsCapability 服务端 prompts 能力。
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ---------------------------------------------------------------------------
// Tool 相关类型
// ---------------------------------------------------------------------------

// ToolDef 是 tools/list 返回的工具定义。
type ToolDef struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
}

// ListToolsResult 是 tools/list 的响应。
type ListToolsResult struct {
	Tools      []ToolDef `json:"tools"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

// CallToolParams 是 tools/call 的参数。
type CallToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// CallToolResult 是 tools/call 的响应。
type CallToolResult struct {
	Content           []ContentBlock `json:"content"`
	IsError           bool           `json:"isError,omitempty"`
	StructuredContent any            `json:"structuredContent,omitempty"`
}

// ContentBlock 是工具调用结果中的一个内容块。
type ContentBlock struct {
	Type     string `json:"type"`           // "text" | "image" | "audio" | "resource_link" | "resource"
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`      // base64（image/audio）
	MimeType string `json:"mimeType,omitempty"`
	URI      string `json:"uri,omitempty"`       // resource_link
	Name     string `json:"name,omitempty"`      // resource_link
}

// TextContent 从 ContentBlock 切片中提取所有 text 块并拼接。
func TextContent(blocks []ContentBlock) string {
	var result string
	for _, b := range blocks {
		if b.Type == "text" {
			if result != "" {
				result += "\n"
			}
			result += b.Text
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// ServerConfig — MCP Server 连接配置
// ---------------------------------------------------------------------------

// ServerType 表示 MCP Server 的传输类型。
type ServerType string

const (
	ServerTypeStdio ServerType = "stdio"
	ServerTypeHTTP  ServerType = "http"
	ServerTypeSSE   ServerType = "sse" // deprecated, 映射为 http
)

// ServerConfig 表示一个 MCP Server 的完整连接配置。
type ServerConfig struct {
	// Name 是 server 的唯一标识名（来自配置文件的 key）。
	// 不在 JSON 中序列化，由配置加载时填充。
	Name string `json:"-"`

	// Type 传输类型: "stdio" | "http" | "sse"
	Type ServerType `json:"type"`

	// stdio 专用
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`

	// HTTP 专用
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	// 通用
	Env     map[string]string `json:"env,omitempty"`
	Timeout int               `json:"timeout,omitempty"` // 工具调用超时（毫秒），0 = 默认
}

// ---------------------------------------------------------------------------
// MCP 配置文件顶层结构
// ---------------------------------------------------------------------------

// MCPConfigFile 是 .mcp.json 或 ~/.waveloom.json 的顶层结构。
type MCPConfigFile struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

// ClaudeJSONFile 是 ~/.claude.json 的顶层结构。
type ClaudeJSONFile struct {
	MCPServers map[string]ServerConfig            `json:"mcpServers,omitempty"`
	Projects   map[string]ClaudeJSONProjectEntry  `json:"projects,omitempty"`
}

// ClaudeJSONProjectEntry 是 ~/.claude.json 中 projects.<path> 的结构。
type ClaudeJSONProjectEntry struct {
	MCPServers map[string]ServerConfig `json:"mcpServers,omitempty"`
}

// ---------------------------------------------------------------------------
// JSON-RPC 消息构造与解析辅助
// ---------------------------------------------------------------------------

// NewRequest 构造一个 JSON-RPC 请求。
func NewRequest(id any, method string, params any) (*JSONRPCRequest, error) {
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = data
	}
	return &JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Method:  method,
		Params:  raw,
	}, nil
}

// NewNotification 构造一个 JSON-RPC 通知。
func NewNotification(method string, params any) (*JSONRPCNotification, error) {
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = data
	}
	return &JSONRPCNotification{
		JSONRPC: JSONRPCVersion,
		Method:  method,
		Params:  raw,
	}, nil
}

// ParseResponse 解析 JSON-RPC 响应，提取 result 到目标类型。
func ParseResponse[T any](data json.RawMessage) (*T, *JSONRPCError, error) {
	var resp JSONRPCResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error, nil
	}
	var result T
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, nil, err
	}
	return &result, nil, nil
}
