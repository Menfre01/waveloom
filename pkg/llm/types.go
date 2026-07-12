package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Role 表示对话中的消息角色，映射 OpenAI Chat Completions 协议。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 表示对话中的一条消息，直接映射 OpenAI 协议的 message 对象。
// JSON struct tags 配合 omitempty 实现零分配序列化，无需手动 buildMessages。
type Message struct {
	ID               string     `json:"id,omitempty"`                 // 不可变 UUID，创建时分配，用于 checkpoint/rewind 追踪
	Role             Role       `json:"role"`                         // system / user / assistant / tool
	Content          string     `json:"content,omitempty"`            // 文本内容（tool 角色时为工具执行结果）
	ReasoningContent string     `json:"reasoning_content"`            // 思考链内容（DeepSeek 要求有 tool_calls 时必须回传，即使是空字符串）
	ToolCallID       string     `json:"tool_call_id,omitempty"`       // tool 角色时关联的工具调用 ID
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`         // assistant 角色时可能包含的工具调用
	Name             string     `json:"name,omitempty"`               // 可选，工具名（tool 角色时）
}

// ToolCall 表示 LLM 发起的一次工具调用请求。
type ToolCall struct {
	Index     int    // 工具调用序号（流式模式下用于分片累积，非流式时为 0）；序列化时排除
	ID        string // 工具调用唯一 ID
	Name      string // 工具名
	Arguments string // JSON 编码的调用参数
}

// openaiToolCall 是 ToolCall 序列化/反序列化的中间结构，
// 映射 OpenAI 兼容格式 {id, type:"function", function:{name, arguments}}。
type openaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// MarshalJSON 输出 OpenAI 兼容格式 {id, type:"function", function:{name, arguments}}。
// Index（内部字段，用于流式分片累积）不出现在输出中。
func (tc ToolCall) MarshalJSON() ([]byte, error) {
	return json.Marshal(openaiToolCall{
		ID:   tc.ID,
		Type: "function",
		Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{
			Name:      tc.Name,
			Arguments: tc.Arguments,
		},
	})
}

// UnmarshalJSON 从 OpenAI 兼容格式 {id, type:"function", function:{name, arguments}} 反序列化。
// Index 不在输入格式中，保持零值。
func (tc *ToolCall) UnmarshalJSON(data []byte) error {
	var o openaiToolCall
	if err := json.Unmarshal(data, &o); err != nil {
		return err
	}
	tc.Index = 0
	tc.ID = o.ID
	tc.Name = o.Function.Name
	tc.Arguments = o.Function.Arguments
	return nil
}

// ToolSpec 定义一个可供 LLM 调用的工具。
type ToolSpec struct {
	Name        string      // 函数名，须匹配 ^[a-zA-Z0-9_-]{1,64}$
	Description string      // 函数描述
	Parameters  interface{} // JSON Schema 格式的参数定义
	Prompt      string      // 工具使用指南（由 agent loop 注入 system message，不进入 function description）
}

// Response 封装 LLM 返回的完整信息。
type Response struct {
	Content          string     // 文本回复内容
	ReasoningContent string     // 推理/思考链内容（思考模式下非空，DeepSeek 独有）
	FinishReason     string     // 完成原因：stop / length / tool_calls / content_filter / insufficient_system_resource
	ToolCalls        []ToolCall // 工具调用列表（LLM 请求执行工具时非空）
	Usage            *UsageInfo // Token 用量信息（Provider 未返回时为 nil）
	Model            string     // API 返回的实际模型名
}

// UsageInfo 记录单次 LLM 调用的 Token 消耗。
// 此数据是 Context Manager（Wave 4）进行上下文窗口管理的输入源，
// Client 作为 LLM 调用的唯一出口，必须保留并传递此信息。
type UsageInfo struct {
	PromptTokens     int // 输入 Token 数
	CompletionTokens int // 输出 Token 数
	TotalTokens      int // 总 Token 数
	// 以下为 Prompt Cache 相关字段（Provider 不支持时为 0）
	CacheHitTokens  int // 命中前缀缓存的 Token 数
	CacheMissTokens int // 未命中前缀缓存的 Token 数
	// 思考模式相关（DeepSeek 独有）
	ReasoningTokens int // 思考链消耗的 Token 数
}

// ErrorClass 区分错误的可恢复性。
type ErrorClass int

const (
	ErrorClassRetryable    ErrorClass = iota // 可重试（网络超时、429、5xx）
	ErrorClassNonRetryable                   // 不可重试（401、403、400、模型不存在）
)

// RetryPolicy 定义重试行为参数。
type RetryPolicy struct {
	MaxRetries     int           // 最大重试次数，默认 3
	InitialBackoff time.Duration // 初始等待时间，默认 1s
	MaxBackoff     time.Duration // 最大等待时间，默认 30s
	Multiplier     float64       // 退避乘数，默认 2.0
}

// RetryEvent 记录一次重试尝试的元数据，并发安全（值类型）。
// Wave 5 的 ObserverBus 可将此类型包装为通用 Event 的 Payload。
type RetryEvent struct {
	Attempt     int           // 当前尝试序号（0-based，0 = 首次请求）
	MaxAttempts int           // 最大尝试次数（含首次，= MaxRetries + 1）
	Error       error         // 导致重试的错误；成功时为 nil
	Backoff     time.Duration // 下次重试前的等待时间；不再重试时为 0
	WillRetry   bool          // 是否还会重试
	Timestamp   time.Time     // 事件触发时间
}

// RetryHook 是重试事件的回调签名。
// 实现者不应在回调中阻塞或 panic。
type RetryHook func(ctx context.Context, ev RetryEvent)

// DefaultRetryPolicy 返回推荐的重试配置。
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		Multiplier:     2.0,
	}
}

// ProviderType 标识 LLM 提供商。
type ProviderType string

const (
	ProviderOpenAI   ProviderType = "openai"    // 标准 OpenAI 协议，也是默认兜底
	ProviderDeepSeek ProviderType = "deepseek"
	// 后续扩展:
	// ProviderAnthropic ProviderType = "anthropic"
	// ProviderOllama    ProviderType = "ollama"
)

// ClientConfig 在构造 Client 时传入，运行期不可变。
type ClientConfig struct {
	Provider       ProviderType      // Provider 类型
	APIKey         string            // API Key
	Model          string            // 模型名称，如 "deepseek-chat"
	BaseURL        string            // 可选，留空使用 Provider 默认端点
	ResponseFormat string            // 可选，强制输出格式："json_object" 启用 JSON 模式，空字符串不设置
	ExtraParams    map[string]any    // 可选，注入 Provider 特殊参数（如 DeepSeek 的 frequency_penalty 等）
	RetryPolicy    RetryPolicy       // 重试策略配置
	Timeout        time.Duration     // 单次请求超时，默认 600s（对齐 DeepSeek 服务端保活超时）
	Headers        map[string]string // 可选，注入自定义 HTTP 请求头
	HTTPClient     *http.Client      // 可选，注入自定义 HTTP 传输层（代理、TLS 证书等），nil 使用默认
	OnRetry        RetryHook         // 可选，重试事件回调；nil 时不触发
}

// StreamingEvent 流式响应中的一个增量块。
type StreamingEvent struct {
	Delta          string     // 本次增量文本
	ReasoningDelta string     // 本次增量思考内容（DeepSeek 思考模式下出现）
	ToolCalls      []ToolCall // 流式累积完成后的工具调用列表（仅在 Done=true 时完整填充）
	FinishReason   string     // 完成原因（仅在 Done=true 时非空）
	Usage          *UsageInfo // Token 用量（仅在 Done=true 的最后一帧非空）
	Model          string     // API 返回的实际模型名（首帧或最后一帧携带）
	Done           bool       // 是否为流结束信号
	Err            error      // 流处理中的错误（仅在 Done=true 且出错时非 nil）
}

// RepairAction 描述校验/修复操作的类型。
type RepairAction string

const (
	RepairSkipInvalidRole    RepairAction = "skip_invalid_role"     // 消息 Role 为空或非法
	RepairSkipEmptyAssistant RepairAction = "skip_empty_assistant"  // assistant 消息无 content 且无 tool_calls
	RepairStripToolCall      RepairAction = "strip_tool_call"       // ToolCall 缺少 ID / Name
	RepairStripOrphanCall    RepairAction = "strip_orphan_call"     // ToolCall 无对应 tool 结果消息
	RepairSkipOrphanTool     RepairAction = "skip_orphan_tool"      // tool 消息无对应 assistant tool_call
)

// RepairEntry 描述单条校验/修复操作。
type RepairEntry struct {
	Index  int          // 原始消息索引（未修复前）
	Role   Role         // 消息角色
	Action RepairAction // 操作类型
	Detail string       // 可读描述
}

var validRoles = map[Role]bool{
	RoleSystem:    true,
	RoleUser:      true,
	RoleAssistant: true,
	RoleTool:      true,
}

// ValidateMessages 对消息历史执行完整性校验和修复。
//
// 检查项目：
//  1. Role 合法性 — 空或非法 Role 的消息直接跳过
//  2. ToolCall 字段 — 剔除 ID/Name 为空的 tool_calls
//  3. 空 assistant — 无 content 且无 tool_calls 的 assistant 消息跳过
//  4. tool_calls 配对 — 无对应 tool 结果的 tool_calls 剔除
//  5. tool 消息配对 — 无对应 assistant tool_call 的 tool 消息跳过
//
// 返回修复后的消息切片和修复报告。
// 调用方可通过 len(report) > 0 判断是否有数据被修复。
func ValidateMessages(msgs []Message) ([]Message, []RepairEntry) {
	if len(msgs) == 0 {
		return nil, nil
	}

	// --- Pass 1: 收集所有 assistant tool_call ID（用于配对检查）---
	validToolCallIDs := make(map[string]bool, len(msgs)/2)
	for _, msg := range msgs {
		if msg.Role == RoleAssistant {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" && tc.Name != "" {
					validToolCallIDs[tc.ID] = true
				}
			}
		}
	}

	// --- Pass 2: 逐条清洗 ---
	clean := make([]Message, 0, len(msgs))
	var report []RepairEntry
	origIdx := 0 // 原始消息索引（始终递增，因为可能跳过消息）

	for _, msg := range msgs {
		idx := origIdx
		origIdx++

		// 1. Role 合法性
		if !validRoles[msg.Role] {
			report = append(report, RepairEntry{
				Index: idx, Role: msg.Role, Action: RepairSkipInvalidRole,
				Detail: "invalid role",
			})
			continue
		}

		// 2. Tool 消息配对 — 无对应 assistant tool_call 则跳过
		if msg.Role == RoleTool {
			if msg.ToolCallID == "" || !validToolCallIDs[msg.ToolCallID] {
				report = append(report, RepairEntry{
					Index: idx, Role: msg.Role, Action: RepairSkipOrphanTool,
					Detail: "tool message without matching assistant tool_call",
				})
				continue
			}
			clean = append(clean, msg)
			continue
		}

		// 3. Assistant 消息：校验 tool_calls 字段完整性
		if msg.Role == RoleAssistant && len(msg.ToolCalls) > 0 {
			var valid []ToolCall
			for _, tc := range msg.ToolCalls {
				if tc.ID == "" || tc.Name == "" {
					report = append(report, RepairEntry{
						Index: idx, Role: msg.Role, Action: RepairStripToolCall,
						Detail: "tool_call missing ID or Name",
					})
					continue
				}
				// 配对检查：只在有 tool 消息集合可供比较时剔除孤儿
				if len(validToolCallIDs) > 0 {
					// 检查后续是否有对应的 tool 消息
					hasPair := false
					for j := origIdx; j < len(msgs); j++ {
						if msgs[j].Role == RoleTool && msgs[j].ToolCallID == tc.ID {
							hasPair = true
							break
						}
						if msgs[j].Role == RoleAssistant || msgs[j].Role == RoleUser {
							break
						}
					}
					if !hasPair {
						report = append(report, RepairEntry{
							Index: idx, Role: msg.Role, Action: RepairStripOrphanCall,
							Detail: "tool_call " + tc.ID + " has no matching tool result",
						})
						continue
					}
				}
				valid = append(valid, tc)
			}
			msg.ToolCalls = valid

			// 如果所有 tool_calls 被剔除且 content 为空 → 退化为空 assistant
			if len(msg.ToolCalls) == 0 && msg.Content == "" {
				report = append(report, RepairEntry{
					Index: idx, Role: msg.Role, Action: RepairSkipEmptyAssistant,
					Detail: "assistant message empty after stripping invalid tool_calls",
				})
				continue
			}
		}

		// 4. 空 assistant（无 content 且无 tool_calls）— 占位 "(empty response)" 或反序列化残留
		if msg.Role == RoleAssistant && msg.Content == "" && len(msg.ToolCalls) == 0 {
			report = append(report, RepairEntry{
				Index: idx, Role: msg.Role, Action: RepairSkipEmptyAssistant,
				Detail: "assistant message with no content and no tool_calls",
			})
			continue
		}

		clean = append(clean, msg)
	}

	if len(report) == 0 {
		return msgs, nil
	}
	return clean, report
}

// ---------------------------------------------------------------------------
// BalanceInfo — 账户余额
// ---------------------------------------------------------------------------

// BalanceInfo 表示账户余额查询结果。
// 部分 Provider（如 OpenAI）不支持余额查询，此时 IsAvailable 为 false。
type BalanceInfo struct {
	IsAvailable  bool              // 当前账户是否有余额可供 API 调用
	BalanceInfos []CurrencyBalance // 各币种余额明细
}

// CurrencyBalance 表示单个币种的余额明细。
type CurrencyBalance struct {
	Currency        string // 货币代码：CNY / USD
	TotalBalance    string // 总的可用余额（包括赠金和充值余额）
	GrantedBalance  string // 未过期的赠金余额
	ToppedUpBalance string // 充值余额
}

// FilterValidToolCalls 从 tool_calls 中剔除无效项：空 ID、空 Name、
// 或在 registry 中不存在的工具名。返回过滤后的切片。
// registry 为空时仅检查 ID/Name 非空。
func FilterValidToolCalls(calls []ToolCall, registry map[string]bool) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	valid := make([]ToolCall, 0, len(calls))
	for _, tc := range calls {
		if tc.ID == "" || tc.Name == "" {
			continue
		}
		if registry != nil && !registry[tc.Name] {
			continue
		}
		valid = append(valid, tc)
	}
	if len(valid) == 0 {
		return nil
	}
	return valid
}

// ModelInfo 表示从 Provider 的 GET /models 接口获取的模型基本信息。
type ModelInfo struct {
	ID      string `json:"id"`       // 模型标识符，如 "deepseek-v4-pro"
	Object  string `json:"object"`   // 对象类型，其值为 "model"
	OwnedBy string `json:"owned_by"` // 拥有该模型的组织
}

