// Package acp 实现 Agent Client Protocol (ACP) v1 Agent 端。
//
// ACP 是标准化 JSON-RPC over stdio 协议，使 Waveloom 能以 Agent 身份
// 与 ACP Client（cc-connect、Zed、JetBrains）通信，进而接入飞书、钉钉等 IM 平台。
//
// 协议版本：v1
// 协议基础：JSON-RPC 2.0
// 传输方式：stdio（行分隔消息）
//
// 对齐标准：agent-client-protocol schema/v1/schema.json
package acp

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
// JSON-RPC 错误码
// ---------------------------------------------------------------------------

const (
	ErrParse          = -32700 // 无效 JSON
	ErrInvalidRequest = -32600 // 无效请求
	ErrMethodNotFound = -32601 // 未知 method
	ErrInvalidParams  = -32602 // 无效 params
	ErrInternal       = -32603 // 内部错误

	// ACP 自定义错误码(对齐官方 schema/v1/error.rs):
	// -32000 AuthRequired / -32002 ResourceNotFound / -32800 RequestCancelled;
	// -32001 未被官方占用,保留给 Waveloom 的 session busy 语义。
	ErrAuthRequired    = -32000 // 需要认证(初版 authMethods 为空,不触发)
	ErrSessionBusy     = -32001 // 同一 session 重复 prompt(Waveloom 自定义)
	ErrSessionNotFound = -32002 // session 不存在(官方 ResourceNotFound 语义)
)

// ---------------------------------------------------------------------------
// ACP 方法名常量
// ---------------------------------------------------------------------------

const (
	// Agent 端方法（Waveloom 实现，Client 调用）
	MethodInitialize    = "initialize"
	MethodSessionNew    = "session/new"
	MethodSessionPrompt = "session/prompt"
	MethodSessionLoad   = "session/load"
	MethodSessionResume = "session/resume"
	MethodSessionCancel = "session/cancel"
	MethodSessionClose  = "session/close"
	MethodSessionList   = "session/list"
	MethodSessionDelete = "session/delete"
	MethodCancelRequest = "$/cancel_request" // LSP 风格按 requestId 取消(官方 protocol side)

	// Client 端方法(Waveloom 调用,Client 实现)
	MethodSessionRequestPermission = "session/request_permission"

	// 通知（Waveloom → Client）
	MethodSessionUpdate = "session/update"
)

// ---------------------------------------------------------------------------
// Initialize 握手
// ---------------------------------------------------------------------------

// InitializeParams 是 initialize 请求的参数。
type InitializeParams struct {
	ProtocolVersion    int                 `json:"protocolVersion"`
	ClientCapabilities *ClientCapabilities `json:"clientCapabilities,omitempty"`
	ClientInfo         *ImplementationInfo `json:"clientInfo,omitempty"`
}

// ClientCapabilities 客户端能力声明。
type ClientCapabilities struct {
	McpCapabilities *McpCapabilities `json:"mcpCapabilities,omitempty"`
	FsCapabilities  *FsCapabilities  `json:"fsCapabilities,omitempty"`
}

// McpCapabilities 客户端 MCP 能力。
type McpCapabilities struct {
	HTTP bool `json:"http,omitempty"`
	SSE  bool `json:"sse,omitempty"`
}

// FsCapabilities 客户端文件系统能力。
type FsCapabilities struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

// ImplementationInfo 描述客户端或 Agent 的实现信息。
type ImplementationInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// AgentAuthCapabilities Agent 认证能力(默认为空对象)。
type AgentAuthCapabilities struct{}

// AuthMethod 描述一种 ACP 认证方式(initialize 的 authMethods 数组元素)。
// v1 支持两种类型:
//   - "agent":Agent 自管 OAuth 流程(本地 HTTP 回调 + 浏览器)
//   - "terminal":客户端以 base 启动配置追加 args 在终端启动交互式登录流,
//     退出码 0 表示成功,随后客户端重连并重新 initialize(Waveloom 采用此方式)
type AuthMethod struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type"` // "agent" | "terminal"
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	// Meta 客户端特定扩展(ACP 规范 _meta 约定,见 Extensibility 章节)。
	// Waveloom 填充 "terminal-auth" 供 Zed 使用:Zed 对标准 terminal 认证
	// 有 acp-beta feature flag 门控(普通用户默认关闭),点击登录按钮时
	// 只解析 _meta.terminal-auth {label, command, args, env} 构造登录终端
	// (crates/agent_servers/src/acp.rs 的 meta_terminal_auth_task);
	// acp-beta 放开后标准路径优先,此字段仍可保留作为兼容层。
	Meta map[string]any `json:"_meta,omitempty"`
}

// InitializeResult 是 initialize 请求的成功响应。
type InitializeResult struct {
	ProtocolVersion   int                  `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities    `json:"agentCapabilities"`
	AgentInfo         *ImplementationInfo  `json:"agentInfo,omitempty"`
	AuthMethods       []AuthMethod         `json:"authMethods"`
}

// AgentCapabilities Agent 能力声明。
type AgentCapabilities struct {
	LoadSession         bool                 `json:"loadSession"`
	PromptCapabilities  PromptCapabilities   `json:"promptCapabilities"`
	McpCapabilities     *McpCapabilities     `json:"mcpCapabilities,omitempty"`
	SessionCapabilities *SessionCapabilities `json:"sessionCapabilities,omitempty"`
	Auth                AgentAuthCapabilities `json:"auth"`
}

// PromptCapabilities prompt 输入能力。
type PromptCapabilities struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}

// SessionCapabilities session 管理能力。
type SessionCapabilities struct {
	Resume *struct{} `json:"resume,omitempty"`
	Close  *struct{} `json:"close,omitempty"`
	List   *struct{} `json:"list,omitempty"`
	Delete *struct{} `json:"delete,omitempty"`
}

// ---------------------------------------------------------------------------
// Session 管理
// ---------------------------------------------------------------------------

// SessionNewParams 是 session/new 请求的参数。
type SessionNewParams struct {
	Cwd        string          `json:"cwd,omitempty"`
	McpServers json.RawMessage `json:"mcpServers,omitempty"`
}

// AcpNameValue 是 ACP McpServer 中 env/headers 的 {name, value} 条目。
type AcpNameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// AcpMcpServer 是 ACP v1 McpServer(discriminated union):
//   - 无 type → stdio 变体{name, command, args[], env[]}
//   - type:"http" → {name, url, headers[]}
//   - type:"sse" → {name, url, headers[]}(Waveloom 映射为 http)
type AcpMcpServer struct {
	Type    string         `json:"type,omitempty"` // "" | "http" | "sse"
	Name    string         `json:"name"`
	Command string         `json:"command,omitempty"`
	Args    []string       `json:"args,omitempty"`
	URL     string         `json:"url,omitempty"`
	Headers []AcpNameValue `json:"headers,omitempty"`
	Env     []AcpNameValue `json:"env,omitempty"`
}

// SessionNewResult 是 session/new 的成功响应。
type SessionNewResult struct {
	SessionID string `json:"sessionId"`
}

// SessionLoadParams 是 session/load 与 session/resume 的请求参数。
type SessionLoadParams struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd,omitempty"`
}

// SessionListItem 是 session/list 返回的单条 session。
// 对齐官方 schema SessionInfo:{sessionId, cwd} 均为必填。
type SessionListItem struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
}

// SessionListResult 是 session/list 的成功响应。
type SessionListResult struct {
	Sessions []SessionListItem `json:"sessions"`
}

// AvailableCommand 是 available_commands_update 中的单条命令。
// 对齐官方 schema AvailableCommand{name, description, input}。
type AvailableCommand struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Input       *AvailableCommandInput `json:"input,omitempty"`
}

// AvailableCommandInput 命令输入规格(v1 仅 unstructured)。
type AvailableCommandInput struct {
	Kind string `json:"kind"` // "unstructured"
}

// AvailableCommandsUpdate 是命令列表更新通知(session/update 变体)。
type AvailableCommandsUpdate struct {
	SessionUpdate     string             `json:"sessionUpdate"` // "available_commands_update"
	AvailableCommands []AvailableCommand `json:"availableCommands"`
}

// SessionInfoUpdate 是 session 元数据更新通知(session/update 变体)。
// 对齐官方 schema:title/updatedAt 均可选,支持部分更新。
type SessionInfoUpdate struct {
	SessionUpdate string  `json:"sessionUpdate"` // "session_info_update"
	Title         *string `json:"title,omitempty"`
	UpdatedAt     *string `json:"updatedAt,omitempty"`
}

// ---------------------------------------------------------------------------
// Content Block（对齐 ACP ContentBlock schema）
// ---------------------------------------------------------------------------

// ContentBlock 是 prompt / update 中的内容块。
// 对齐 ACP v1 ContentBlock discriminated union。
type ContentBlock struct {
	Type string `json:"type"` // "text" | "image" | "audio" | "resource_link" | "resource"

	// text
	Text string `json:"text,omitempty"`

	// image / audio / resource_link
	Data     string `json:"data,omitempty"`     // base64
	MimeType string `json:"mimeType,omitempty"` // MIME type
	URI      string `json:"uri,omitempty"`      // resource_link / resource

	// resource_link
	Name        string `json:"name,omitempty"`        // resource_link required
	Description string `json:"description,omitempty"`  // resource_link optional
	Size        int64  `json:"size,omitempty"`         // resource_link optional
	Title       string `json:"title,omitempty"`        // resource_link optional

	// resource (embedded) — nested {text, uri} | {blob, uri}
	Resource *EmbeddedResource `json:"resource,omitempty"`

	// annotations (e.g. audience for thought)
	Annotations *Annotations `json:"annotations,omitempty"`
}

// EmbeddedResource 表示嵌入的资源内容。
type EmbeddedResource struct {
	// TextResourceContents
	Text string `json:"text,omitempty"`
	// BlobResourceContents
	Blob string `json:"blob,omitempty"`
	// 共用
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
}

// Annotations 可选的内容注释（用于 audience 等标记）。
type Annotations struct {
	Audience []string `json:"audience,omitempty"` // "user" | "assistant"
}

// TextContent 从 ContentBlock 切片中提取所有 text 块文本并拼接。
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

// SessionPromptParams 是 session/prompt 请求的参数。
type SessionPromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

// SessionPromptResult 是 session/prompt 的成功响应。
type SessionPromptResult struct {
	StopReason string `json:"stopReason"`
}

// ---------------------------------------------------------------------------
// Session Update 通知（Agent → Client）
// ---------------------------------------------------------------------------

// SessionUpdateParams 是 session/update 通知的参数。
type SessionUpdateParams struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

// ---------------------------------------------------------------------------
// ContentChunk — 流式文本响应（agent_message_chunk）
// ---------------------------------------------------------------------------

// ContentChunk 表示流式响应中的一个文本块。
// 对齐 ACP v1: session/update 的 update 字段为 {sessionUpdate, content, messageId?}。
type ContentChunk struct {
	SessionUpdate string       `json:"sessionUpdate"`         // "agent_message_chunk"
	Content       ContentBlock `json:"content"`               // ContentBlock（type: "text"）
	MessageID     string       `json:"messageId,omitempty"`   // 稳定的消息 ID
}

// ---------------------------------------------------------------------------
// Plan Update
// ---------------------------------------------------------------------------

// PlanEntry 是计划中的单条条目。
type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"` // "high" | "medium" | "low"(v1 必填)
	Status   string `json:"status,omitempty"`   // "pending" | "in_progress" | "completed" | "rejected"
}

// PlanUpdate 表示 plan 模式更新通知。
type PlanUpdate struct {
	SessionUpdate string      `json:"sessionUpdate"` // "plan"
	Entries       []PlanEntry `json:"entries"`
}

// ---------------------------------------------------------------------------
// Tool Call 通知（对齐 ACP ToolCallUpdate）
// ---------------------------------------------------------------------------

// ToolCallUpdate 表示工具调用状态变更通知。
// 对齐 ACP v1: 单一 ToolCallUpdate 类型，toolCallId 必填，其余可选。
type ToolCallUpdate struct {
	SessionUpdate string               `json:"sessionUpdate"`          // "tool_call"
	ToolCallID    string               `json:"toolCallId"`             // 工具调用唯一 ID
	Kind          string               `json:"kind,omitempty"`         // ToolKind
	Status        string               `json:"status,omitempty"`       // pending | in_progress | completed | failed
	Title         string               `json:"title,omitempty"`        // 工具名（显示标题）
	Content       []ToolCallContentItem `json:"content,omitempty"`     // 内容项列表
	Locations     []ToolCallLocation   `json:"locations,omitempty"`    // 文件位置
	RawInput      json.RawMessage      `json:"rawInput,omitempty"`     // 原始输入
	RawOutput     json.RawMessage      `json:"rawOutput,omitempty"`    // 原始输出
}

// ToolCallContentItem 是工具调用内容的单个项。
// 对齐 ACP v1 ToolCallContent: {type:"content"/"diff"/"terminal", ...}
type ToolCallContentItem struct {
	Type string `json:"type"` // "content" | "diff" | "terminal"

	// type = "content": 内嵌 ContentBlock
	Content *ContentBlock `json:"content,omitempty"`

	// type = "diff"
	Path    string `json:"path,omitempty"`
	OldText string `json:"oldText,omitempty"`
	NewText string `json:"newText,omitempty"`

	// type = "terminal"
	TerminalID string `json:"terminalId,omitempty"`
}

// ToolCallLocation 是工具操作的文件位置。
type ToolCallLocation struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
}

// ---------------------------------------------------------------------------
// Usage Update 通知（对齐 ACP UsageUpdate）
// ---------------------------------------------------------------------------

// Cost 是费用信息。
type Cost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// UsageUpdateContent 表示 token 用量更新通知。
// 对齐 ACP v1 UsageUpdate: {sessionUpdate, used, size, cost?}
type UsageUpdateContent struct {
	SessionUpdate string `json:"sessionUpdate"` // "usage_update"
	Used          uint64 `json:"used"`          // 已使用 token（含累积）
	Size          uint64 `json:"size"`          // 本轮上下文大小
	Cost          *Cost  `json:"cost,omitempty"`
}

// ---------------------------------------------------------------------------
// ToolKind 映射
// ---------------------------------------------------------------------------

// ToolKind 将 Waveloom 工具名映射为 ACP ToolKind。
// ACP v1 支持: read/edit/delete/move/search/execute/think/fetch/switch_mode/other
func ToolKind(toolName string) string {
	switch toolName {
	case "read":
		return "read"
	case "edit", "write":
		return "edit"
	case "bash":
		return "execute"
	case "web_search":
		return "search"
	case "web_fetch":
		return "fetch"
	case "agent":
		return "think"
	default:
		return "other"
	}
}

// ---------------------------------------------------------------------------
// 辅助函数
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
		JSONRPC: "2.0",
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
		JSONRPC: "2.0",
		Method:  method,
		Params:  raw,
	}, nil
}

// NewResponse 构造一个 JSON-RPC 成功响应。
func NewResponse(id any, result any) (*JSONRPCResponse, error) {
	var raw json.RawMessage
	if result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		raw = data
	}
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  raw,
	}, nil
}

// NewErrorResponse 构造一个 JSON-RPC 错误响应。
func NewErrorResponse(id any, code int, message string) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
}

// ACPStopReason 将 agentloop.TerminalReason 映射为 ACP stopReason 字符串。
func ACPStopReason(reason string) string {
	switch reason {
	case "completed":
		return "end_turn"
	case "aborted":
		return "cancelled"
	case "max_steps":
		return "max_turn_requests"
	case "model_error":
		return "refusal"
	default:
		return "end_turn"
	}
}
