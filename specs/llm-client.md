# LLM Client 组件规格书

## 组件定位

LLM Client 是 Waveloom Code Agent 与 LLM 服务之间的**通信层**，负责封装所有与 LLM API 交互的细节。它是 Agent Loop 唯一的 LLM 依赖，Loop 通过 `Client` 接口使用它，对底层 Provider、重试策略、HTTP 细节等完全无感知。

**核心设计决策：统一使用 OpenAI Chat Completions 协议作为对外请求格式。**
- 所有 Provider 的请求/响应均基于 OpenAI Chat Completions 格式
- 各 Provider 的特殊参数、行为差异通过内部 `providerAdapter` 接口处理
- `Client` 接口对外不变——Loop 不感知底层是哪个 Provider
- 首个实现：DeepSeek Adapter（已完成），扩展：OpenAI Adapter（已完成）

核心职责：
1. 请求构造与序列化 — 将内部类型转为 OpenAI Chat Completions API 格式
2. Provider 差异适配 — 通过 adapter 处理各 Provider 的特殊参数
3. 响应解析 — 将 Provider 响应转回内部类型（Message, ToolCall 等）
4. 错误处理 — 指数退避重试 + jitter，区分可重试与不可重试错误

## 参考来源

- Claude Code: `api/claude.ts` — `queryModelWithStreaming`
- Codex CLI: `core/src/client.rs` — `ModelClient` + `ModelClientSession`
- OpenAI Chat Completions API: `https://api.openai.com/v1/chat/completions`
- DeepSeek API: `https://api.deepseek.com/v1/chat/completions`

两者的共同模式：
- `SendMessage` 接受消息历史和工具列表，返回含文本+工具调用的 Response
- 重试逻辑封装在 Client 内部，调用方不感知
- 错误区分为 Retryable / NonRetryable，Retryable 自动重试
- API Key 和 Provider 配置在 Client 构造时完成

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 请求格式 | OpenAI Chat Completions | 行业事实标准，所有 adapter 对外输出的 HTTP 请求格式 |
| Provider 差异 | providerAdapter 接口 | 不同 Provider 的特殊参数、原生协议差异由 adapter 内部处理，Client 接口不感知 |
| 接口粒度 | `SendMessage` + `SendMessageStream` 双方法 | 非流式用于可靠性兜底，流式（SSE）用于主路径实时输出 |
| 响应模式 | 流式优先，非流式回退 | Agent Loop 优先用 `SendMessageStream`，流式出错自动回退 `SendMessage` |
| Provider 实现 | DeepSeek + OpenAI | 两个 adapter 均已实现，OpenAI 作为默认兜底 |
| 重试位置 | Client 内部 | Loop 不应关心重试细节，保持编排逻辑纯粹 |
| 退避算法 | 指数退避 + jitter | 标准实践，避免 thundering herd |
| 错误分类 | 白名单分类 | Retryable 列表显式定义，未匹配的默认不可重试（安全第一） |

## 组件边界

### 输入
- `context.Context` — 取消/超时信号
- `[]Message` — 消息历史（含 system prompt）
- `[]ToolSpec` — 可用工具列表

### 输出
- `*Response` — 文本内容 + 工具调用列表 + Token 用量
- `error` — 仅在不可恢复错误时返回（含底层原因，RetryPolicy 耗尽）

### 依赖（接口，非具体实现）
- 无内部依赖 — Client 是最底层组件
- 仅依赖标准库 `net/http` 和 `encoding/json`

### 不纳入本组件
- Token 计数与上下文窗口管理（属于 Context Manager 职责）
- 消息历史管理（属于 Agent Loop 职责）
- 多 Provider 自动切换/降级（后续 Wave）

---

## 接口定义

### Client 接口（对外，稳定）

```go
// Client 向 LLM 发送消息并返回响应。
// 重试策略、指数退避、错误分类等均在实现内部处理。
// Loop 通过此接口使用 LLM，对 Provider 差异完全无感知。
type Client interface {
    // SendMessage 非流式调用 LLM，返回完整 Response。
    // 内部含指数退避重试（默认 3 次）。
    SendMessage(ctx context.Context, messages []Message, tools []ToolSpec) (*Response, error)

    // SendMessageStream 流式调用 LLM，返回 StreamingEvent channel。
    // 数据通过 SSE（Server-Sent Events）增量到达。
    // channel 在流结束或出错时关闭。
    SendMessageStream(ctx context.Context, messages []Message, tools []ToolSpec) (<-chan StreamingEvent, error)

    // GetBalance 查询账户余额。部分 Provider 不支持，此时返回 nil, nil。
    GetBalance(ctx context.Context) (*BalanceInfo, error)

    // SupportsBalance 返回当前 Provider 是否支持余额查询。
    SupportsBalance() bool
}
```

### providerAdapter 接口（内部，仅供 Client 实现使用）

```go
// providerAdapter 将内部类型适配为特定 Provider 的 HTTP 请求。
// 每个 Provider 实现一个 adapter，处理特殊参数、请求头、错误分类等差异。
// 新增 Provider 只需实现此接口并在 NewClient 中注册。
type providerAdapter interface {
    // BuildRequest 将内部类型转为 Provider 期望的 HTTP 请求（OpenAI 兼容格式为基准，
    // 各 adapter 可在 body 和 headers 上添加 Provider 特有字段）
    BuildRequest(messages []Message, tools []ToolSpec) (*http.Request, error)

    // ParseResponse 将 HTTP 响应解析为内部 Response 类型
    ParseResponse(body []byte) (*Response, error)

    // BuildStreamRequest 同 BuildRequest，但 body 中 stream=true。
    BuildStreamRequest(messages []Message, tools []ToolSpec) (*http.Request, error)

    // ParseStreamEvent 解析单行 SSE data JSON，返回增量事件。
    // 入参为去除 "data: " 前缀后的原始 JSON 字节。
    ParseStreamEvent(data []byte) (StreamingEvent, error)

    // ClassifyError 判断 Provider 返回的错误是否可重试
    // 入参 err 为 doRequest 返回的原始错误，adapter 根据 Provider 语义分类
    ClassifyError(err error) ErrorClass

    // BaseURL 返回 Provider 的 API 端点（如 https://api.deepseek.com/v1）
    BaseURL() string

    // AuthHeader 返回认证头键值对（如 "Authorization", "Bearer sk-xxx"）
    AuthHeader() (key, value string)

    // GetBalance 查询账户余额。如 Provider 不支持，返回 nil, nil。
    GetBalance(ctx context.Context, httpClient *http.Client) (*BalanceInfo, error)

    // SupportsBalance 返回 Provider 是否支持余额查询。
    SupportsBalance() bool
}
```

### 核心类型

```go
// --- 消息角色 ---

type Role string

const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
    RoleTool      Role = "tool"
)

// --- Message ---

// Message 表示对话中的一条消息，直接映射 OpenAI 协议的 message 对象。
type Message struct {
    Role             Role        // system / user / assistant / tool
    Content          string      // 文本内容（tool 角色时为工具执行结果）
    ReasoningContent string      // 思考链内容（assistant 角色时可选，DeepSeek 思考模式下出现）
    ToolCallID       string      // tool 角色时关联的工具调用 ID
    ToolCalls        []ToolCall  // assistant 角色时可能包含的工具调用
    Name             string      // 可选，工具名（tool 角色时）
}
```

```go
// --- ToolSpec ---

// ToolSpec 定义一个可供 LLM 调用的工具。
type ToolSpec struct {
    Name        string      // 函数名，须匹配 ^[a-zA-Z0-9_-]{1,64}$
    Description string      // 函数描述
    Parameters  interface{} // JSON Schema 格式的参数定义
}
```

```go
// --- ToolCall ---

// ToolCall 表示 LLM 发起的一次工具调用请求。
type ToolCall struct {
    Index     int    // 工具调用序号（流式模式下用于分片累积，非流式时为 0）；序列化时排除
    ID        string // 工具调用唯一 ID
    Name      string // 工具名
    Arguments string // JSON 编码的调用参数
}
```

```go
// --- Response ---

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
// 此数据是 Context Manager 进行上下文窗口管理的输入源，
// Client 作为 LLM 调用的唯一出口，必须保留并传递此信息。
type UsageInfo struct {
    PromptTokens         int // 输入 Token 数
    CompletionTokens     int // 输出 Token 数
    TotalTokens          int // 总 Token 数
    // 以下为 Prompt Cache 相关字段（Provider 不支持时为 0）
    CacheHitTokens  int // 命中前缀缓存的 Token 数
    CacheMissTokens int // 未命中前缀缓存的 Token 数
    // 思考模式相关（DeepSeek 独有）
    ReasoningTokens int // 思考链消耗的 Token 数
}
```

```go
// --- StreamingEvent ---

// StreamingEvent 流式响应中的一个增量块。
// 通过 SendMessageStream 返回的 channel 逐帧推送。
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
```

```go
// --- RetryPolicy ---

// RetryPolicy 定义重试行为参数
type RetryPolicy struct {
    MaxRetries     int           // 最大重试次数，默认 3
    InitialBackoff time.Duration // 初始等待时间，默认 1s
    MaxBackoff     time.Duration // 最大等待时间，默认 30s
    Multiplier     float64       // 退避乘数，默认 2.0
}

// DefaultRetryPolicy 返回推荐的重试配置
func DefaultRetryPolicy() RetryPolicy {
    return RetryPolicy{
        MaxRetries:     3,
        InitialBackoff: 1 * time.Second,
        MaxBackoff:     30 * time.Second,
        Multiplier:     2.0,
    }
}

// RetryEvent 记录一次重试尝试的元数据，并发安全（值类型）。
// ObserverBus 可将此类型包装为通用 Event 的 Payload。
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
```

### Balance 类型

```go
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
```

```go
// --- 错误类型 ---

// ErrorClass 区分错误的可恢复性
type ErrorClass int

const (
    ErrorClassRetryable    ErrorClass = iota // 可重试（网络超时、429、5xx）
    ErrorClassNonRetryable                   // 不可重试（401、403、400、模型不存在）
)

// RetryableError 标记一个 error 为可重试。
type RetryableError struct {
    Message    string
    StatusCode int
    RetryAfter time.Duration // 可选，来自 Retry-After 头
    Cause      error
}

func (e *RetryableError) Error() string { return e.Message }
func (e *RetryableError) Unwrap() error { return e.Cause }

// NonRetryableError 标记一个 error 为不可重试。
type NonRetryableError struct {
    Message    string
    StatusCode int
    Cause      error
}

func (e *NonRetryableError) Error() string { return e.Message }
func (e *NonRetryableError) Unwrap() error { return e.Cause }
```

```go
// --- ClientConfig ---

// ClientConfig 在构造 Client 时传入，运行期不可变
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

// ProviderType 标识 LLM 提供商
type ProviderType string

const (
    ProviderOpenAI   ProviderType = "openai"    // 标准 OpenAI 协议，也是默认兜底
    ProviderDeepSeek ProviderType = "deepseek"
    // 后续扩展:
    // ProviderAnthropic ProviderType = "anthropic"
    // ProviderOllama    ProviderType = "ollama"
)
```

---

## Provider 适配

### 架构

```
Agent Loop
    │
    ├─ SendMessageStream(ctx, messages, tools)     ← 主路径（流式，SSE）
    │  SendMessage(ctx, messages, tools)            ← 回退（非流式）
    ▼
┌─────────────────────────────────────┐
│            LLM Client               │
│                                     │
│  1. 工具名校验 + 清理                 │
│  2. adapter.BuildStreamRequest()    │──→ *http.Request (stream=true)
│     或 adapter.BuildRequest()       │──→ *http.Request (stream=false)
│  3. adapter.AuthHeader()            │──→ 设置认证头
│  4. 流式: readStream(SSE)           │──→ <-chan StreamingEvent
│     非流式: sendWithRetry + 退避    │──→ *Response
│  5. adapter.ParseStreamEvent()      │──→ StreamingEvent
│     或 adapter.ParseResponse()      │──→ *Response
│  6. adapter.ClassifyError()         │──→ ErrorClass
│                                     │
│  ┌───────────────────────────────┐  │
│  │      providerAdapter          │  │
│  │                               │  │
│  │  BuildRequest()               │  │
│  │  BuildStreamRequest()         │  │
│  │  ParseResponse()              │  │
│  │  ParseStreamEvent()           │  │
│  │  ClassifyError()              │  │
│  │  BaseURL()                    │  │
│  │  AuthHeader()                 │  │
│  └───────────────────────────────┘  │
└─────────────────────────────────────┘
```

### 新增 Provider 步骤

```
1. 实现 providerAdapter 接口
2. 在 ProviderType 中添加常量
3. 在 NewClient 的 switch 中注册新 adapter

不需要修改 Client 接口、Agent Loop、Tool System 等任何上层代码。
```

### 默认 Provider（兜底策略）

`NewClient` 中 Provider 选择逻辑：

```go
func NewClient(cfg ClientConfig) (*Client, error) {
    var adapter providerAdapter
    switch cfg.Provider {
    case ProviderDeepSeek:
        adapter = newDeepSeekAdapter(cfg)
    case ProviderOpenAI:
        adapter = newOpenAIAdapter(cfg)
    case "": // 空字符串默认走 DeepSeek adapter
        adapter = newDeepSeekAdapter(cfg)
    default:
        // 未识别的 ProviderType → 走默认 OpenAI adapter
        adapter = newOpenAIAdapter(cfg)
    }
    return newClientWithAdapter(cfg, adapter)
}
```

**兜底规则**：`ProviderType` 为空字符串时默认使用 DeepSeek adapter，未知 Provider 走标准 OpenAI adapter。这确保：
- 零配置场景下默认对接 DeepSeek API（Waveloom 首选 Provider）
- 对接任何 OpenAI 兼容端点（如 Azure OpenAI、vLLM、本地模型）只需显式设置 `ProviderType`
- 用户只需设置 `BaseURL` + `APIKey` 即可使用

### Settings 配置加载

Client 的构造参数通过 `LLMSettings` 结构从 JSON 配置文件加载，支持全局 + 项目双层配置合并。

```go
// LLMSettings 对应 settings.json 中的顶层 llm 配置块。
type LLMSettings struct {
    APIKey      string            `json:"api_key"`      // API Key；为空时回退到 LLM_API_KEY 环境变量
    Provider    string            `json:"provider"`     // "openai" / "deepseek"，默认 "deepseek"
    Model       string            `json:"model"`        // 模型名称
    BaseURL     string            `json:"base_url"`     // API 端点，留空使用默认
    Timeout     string            `json:"timeout"`      // 单次请求超时，Go Duration 格式（如 "600s"），默认 600s
    Retry       *RetrySettings    `json:"retry"`        // 重试策略，留空使用默认
    Headers     map[string]string `json:"headers"`      // 自定义 HTTP 请求头
    ExtraParams map[string]any    `json:"extra_params"` // Provider 特有参数，支持任意嵌套
}

type RetrySettings struct {
    MaxRetries     int     `json:"max_retries"`     // 最大重试次数
    InitialBackoff string  `json:"initial_backoff"` // 初始退避时间，Go Duration 格式
    MaxBackoff     string  `json:"max_backoff"`     // 最大退避时间，Go Duration 格式
    Multiplier     float64 `json:"multiplier"`      // 退避乘数
}
```

**构造方法**：

```go
// NewClientFromSettings — 从单个 settings.json 文件构造 Client
func NewClientFromSettings(path string) (Client, error)

// NewClientFromMergedSettings — 合并全局和项目配置文件构造 Client
// 合并规则：global → project，project 字段覆盖 global
func NewClientFromMergedSettings(globalPath, projectPath string) (Client, error)

// NewClientFromLLMSettings — 从 LLMSettings 结构构造 Client
// API Key 优先使用 settings.api_key，为空时回退到 LLM_API_KEY 环境变量
func NewClientFromLLMSettings(settings *LLMSettings) (Client, ClientConfig, error)

// DefaultSettings — 返回推荐的默认 LLM 配置（DeepSeek + 思考模式）
func DefaultSettings() *LLMSettings

// WriteDefaultSettings — 将默认配置文件写入指定路径
// 自动创建父目录。如果文件已存在则不做任何操作。
func WriteDefaultSettings(path string) error
```

---

## DeepSeek Adapter（Wave 1 唯一实现）

DeepSeek API 使用 OpenAI 兼容格式，但有若干特殊参数和响应字段。

### 端点与认证

```
BaseURL: https://api.deepseek.com
Auth:    Authorization: Bearer {APIKey}
```

> 注意：DeepSeek 同时提供 OpenAI 兼容端点（`https://api.deepseek.com`，路径 `/v1/chat/completions`）和 Anthropic 兼容端点（`https://api.deepseek.com/anthropic`）。adapter 使用 OpenAI 兼容端点，请求路径为 `{BaseURL}/v1/chat/completions`。

### 模型

| 模型 | 说明 |
|------|------|
| `deepseek-v4-flash` | 快速推理 |
| `deepseek-v4-pro` | 增强推理能力 |

> `deepseek-chat` 和 `deepseek-reasoner` 已标记为即将废弃，adapter 不硬编码模型名，通过 `Config.Model` 指定。

### 特殊参数（与 OpenAI 的差异）

#### thinking（DeepSeek 独有）

控制思考模式开关。OpenAI 不存在此参数，是 DeepSeek 的核心差异化功能。注意 `thinking` 在 OpenAI SDK 中需要通过 `extra_body` 传入，但在直接 HTTP 调用中作为请求 body 的顶层字段：

```json
{
  "thinking": {"type": "enabled"}
}
```

| 参数 | 位置 | 取值 | 说明 |
|------|------|------|------|
| `thinking.type` | body 顶层（SDK 中使用 `extra_body`） | `"enabled"` / `"disabled"` | 默认 `"enabled"` |

#### reasoning_effort

控制思考强度。**这是 body 顶层参数**，与 `model`、`messages` 同级，**不在** `thinking` 对象内部：

```json
{
  "reasoning_effort": "high"
}
```

| 参数 | 位置 | 取值 | 说明 |
|------|------|------|------|
| `reasoning_effort` | body 顶层 | `"high"` / `"max"` | 默认 `"high"`，对复杂 Agent 请求自动设为 `"max"` |

**与 OpenAI 的兼容映射**：`"low"` / `"medium"` → `"high"`，`"xhigh"` → `"max"`。传入不报错，但实际效果同上。

> 思考模式下 `temperature`、`top_p`、`presence_penalty`、`frequency_penalty` 传参不报错但无效。

> 启用思考模式后，响应中会出现 `reasoning_content` 字段。`reasoning_content` 的回传规则取决于是否涉及工具调用：
> - **无工具调用**：两个 user 消息之间的 assistant 消息，其 `reasoning_content` **无需**回传，传入 API 会被忽略。
> - **有工具调用**：两个 user 消息之间的 assistant 消息包含工具调用时，其 `reasoning_content` **必须**回传给 API，否则 API 返回 400 错误。

#### Context Caching（上下文硬盘缓存）

DeepSeek 实现了**基于磁盘的上下文缓存（Disk-based KV Cache）**，默认对所有用户自动启用，**无需任何代码或参数变更**。

**工作原理**：
- 每次请求都会触发硬盘缓存的构建；若后续请求与已有缓存前缀重复，则重复部分从缓存拉取，计入"缓存命中"
- 受 Sliding Window Attention 影响，缓存前缀以**独立的完整单元**落盘，后续请求必须完整匹配某个单元才算命中，部分重叠不算
- 缓存仅匹配用户输入的前缀部分，输出仍通过完整计算推理生成，效果与不使用缓存相同

**缓存前缀落盘时机**（三种）：
1. **请求结束位置落盘** — 每次请求的"用户输入结束位置"与"模型输出结束位置"各产生一个缓存前缀单元
2. **公共前缀检测落盘** — 系统检测到多次请求间存在公共前缀时，将其作为独立单元落盘
3. **按固定 token 间隔落盘** — 长输入/长输出场景下，系统按固定 token 数量间隔截取缓存前缀单元，防止长前缀因未达结束位置而无法被缓存

**命中示例**：
- 请求1 `A+B`，请求2 `A+B+C` → 命中 `A+B`
- 请求1 `A+B`，请求2 `A+C` → 无法命中（不完整匹配 `A+B`），但系统识别公共前缀 `A` 并落盘；请求3 `A+D` 可命中 `A`

**限制条件**：
- 必须**完全匹配**一个缓存前缀单元才算命中，部分重叠不算
- 缓存系统工作在"尽力而为（best-effort）"模式，不保证 100% 命中率
- 缓存构建耗时在秒级
- 缓存不再使用后自动清空，时间一般为"几个小时到几天"

**Usage 体现**：

```go
// 响应中 usage 字段包含：
usage := &UsageInfo{
    PromptTokens:     1000,  // = CacheHitTokens + CacheMissTokens
    CompletionTokens: 200,
    TotalTokens:      1200,
    CacheHitTokens:   800,   // 命中缓存，按低价计费
    CacheMissTokens:  200,   // 未命中缓存，按标准价计费
}
```

> **计费**：缓存命中 Token（`CacheHitTokens`）享有极低价格（约为未命中的 1/50 ~ 1/120），具体费率参见 DeepSeek 定价页。adapter 仅透传数值，不参与计费逻辑。

#### frequency_penalty / presence_penalty

**已废弃，不再生效。** 传入这两个参数不会产生任何效果。adapter 不注入这两个参数。

#### response_format

支持 JSON mode，由 `ClientConfig.ResponseFormat` 字段控制：
```go
cfg.ResponseFormat = "json_object" // 启用 JSON 模式，adapter 自动设置 {"response_format": {"type": "json_object"}}
```

#### logprobs / top_logprobs

标准 OpenAI 参数，DeepSeek 支持：`logprobs`（bool）和 `top_logprobs`（≤ 20）。

### 其他可通过 ExtraParams 注入的参数

| 参数 | 类型 | 说明 |
|------|------|------|
| `temperature` | number | ≤ 2，默认 1（思考模式下无效） |
| `top_p` | number | ≤ 1，默认 1（思考模式下无效） |
| `max_tokens` | integer | 最大生成 token 数 |
| `stop` | string/string[] | 最多 16 个停止序列 |
| `logprobs` | boolean | 是否返回 log 概率 |
| `top_logprobs` | integer | ≤ 20 |
| `thinking` | object | `{"type": "enabled"\|"disabled"}` |
| `reasoning_effort` | string | `"high"` / `"max"`，body 顶层参数 |
| `response_format` | object | `{"type": "json_object"}` |

### 请求格式（BuildRequest）

```go
// deepseekAdapter.BuildRequest 生成的 JSON body：
{
  "model": "deepseek-v4-pro",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello"},
    {"role": "assistant", "content": null, "tool_calls": [...]},
    {"role": "tool", "tool_call_id": "call_xxx", "content": "result"}
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "read_file",
        "description": "Read a file from disk",
        "parameters": {"type": "object", "properties": {...}}
      }
    }
  ],
  "stream": false,
  // 以下来自 Config.ExtraParams
  "thinking": {"type": "enabled"},
  "reasoning_effort": "high"
}
```

### 响应格式（ParseResponse）

DeepSeek 响应与 OpenAI 兼容，但有额外的字段：

```json
{
  "choices": [{
    "finish_reason": "stop",
    "message": {
      "role": "assistant",
      "content": "Hello!",
      "reasoning_content": "Let me think...",   // 思考模式下的推理链
      "tool_calls": [{
        "id": "call_xxx",
        "type": "function",
        "function": { "name": "read_file", "arguments": "{\"path\":\"/etc/hosts\"}" }
      }]
    }
  }],
  "usage": {
    "prompt_tokens": 100,
    "completion_tokens": 50,
    "total_tokens": 150,
    "prompt_cache_hit_tokens": 80,
    "prompt_cache_miss_tokens": 20,
    "completion_tokens_details": {
      "reasoning_tokens": 30
    }
  }
}
```

解析规则：
- `Response.Content` ← `choices[0].message.content`
- `Response.ReasoningContent` ← `choices[0].message.reasoning_content`（思考模式下非空）
- `Response.ToolCalls[]` ← `choices[0].message.tool_calls[]`，提取 `id`、`function.name`、`function.arguments`
- `Response.Usage` ← `usage`（含 `CacheHitTokens`、`CacheMissTokens`、`ReasoningTokens`）
- `Response.FinishReason` ← `choices[0].finish_reason`
- 多 choice 取第一个（`choices[0]`）
- 未知字段忽略

**finish_reason 特殊值**：DeepSeek 多一个 `"insufficient_system_resource"` 值，表示推理系统资源不足导致中断。adapter 在 `ParseResponse` 中将其转换为 `RetryableError` 返回。

**reasoning_content 回传约束**：`reasoning_content` 的回传规则取决于是否涉及工具调用：
- **无工具调用**：两个 user 消息之间的 assistant 消息，其 `reasoning_content` 无需回传，传入 API 会被忽略。
- **有工具调用**：两个 user 消息之间的 assistant 消息包含工具调用时，其 `reasoning_content` 必须回传给 API，否则 API 返回 400 错误。

响应解析时须将 `reasoning_content` 填入 `Message.ReasoningContent`，序列化请求时按上述规则决定是否输出该字段。



### 错误分类（ClassifyError）

| 错误 | 分类 | 说明 |
|------|------|------|
| 429 rate_limit_error | Retryable | 速率限制 |
| 5xx server_error | Retryable | 服务端故障 |
| `insufficient_system_resource` (finish_reason) | Retryable | 资源不足，由 ParseResponse 转换为 RetryableError |
| 网络错误（超时、连接重置、DNS、TLS） | Retryable | 传输层错误 |
| 非 JSON 响应 | Retryable | 网关错误页面等 |
| 401 authentication_error | NonRetryable | API Key 无效 |
| 402 insufficient_balance | NonRetryable | 余额不足 |
| 400 invalid_request_error | NonRetryable | 请求格式错误 |
| 上下文超长 | NonRetryable | 输入超过窗口 |

---

## OpenAI Adapter（未知 Provider 兜底）

标准 OpenAI Chat Completions 协议实现，同时是所有未识别 `ProviderType` 的默认兜底 adapter（空字符串默认走 DeepSeek）。

### 端点与认证

```
BaseURL: https://api.openai.com/v1  （可通过 Config.BaseURL 覆盖，对接 Azure、vLLM 等兼容端点）
Auth:    Authorization: Bearer {APIKey}
```

### 请求格式

与 DeepSeek adapter 相同的基础 OpenAI 兼容结构，不含任何非标准字段：

```json
{
  "model": "gpt-4o",
  "messages": [...],
  "tools": [...],
  "stream": false
}
```

`Config.ExtraParams` 中的值同样合并到 body 顶层，用于支持 Azure OpenAI 的 `api-version` 等自定义字段。

### 错误分类

标准 OpenAI 错误码映射：

| 错误 | 分类 | 说明 |
|------|------|------|
| 429 rate_limit_exceeded | Retryable | 速率限制 |
| 5xx server_error | Retryable | 服务端故障 |
| 网络错误 | Retryable | 超时、连接重置、DNS、TLS |
| 非 JSON 响应 | Retryable | 网关错误页面等 |
| 401 invalid_authentication | NonRetryable | API Key 无效 |
| 403 permission_denied | NonRetryable | 无权访问 |
| 404 model_not_found | NonRetryable | 模型不存在 |
| 400 invalid_request | NonRetryable | 请求格式错误 |
| context_length_exceeded | NonRetryable | 输入超过窗口 |

---

## 请求/响应流程

```
  Agent Loop
     │
     │  SendMessage(ctx, messages, tools)
     ▼
┌─────────────────┐
│   LLM Client    │
│                 │
│ 1. 工具名校验     │──→ 清理 Name 非法字符 → 检查重名
│    ↓            │
│ 2. adapter      │──→ BuildRequest(messages, tools)
│    .BuildRequest│      ├── OpenAI 兼容 JSON 结构
│    ↓            │      ├── 合并 Config.ExtraParams
│ 3. 签名认证      │      └── stream=false
│    ↓            │──→ AuthHeader() → 设置请求头
│ 4. 发送+重试     │──→ sendWithRetry(req)
│    ↓            │      ├── 每次重试前检查 ctx.Err()
│ 5. 错误分类      │      ├── 指数退避 + jitter
│    ↓            │      └── adapter.ClassifyError() 判决
│ 6. 解析响应      │──→ adapter.ParseResponse(body)
│    ↓            │      ├── 提取 choices[0].message
│ 7. 返回结果      │      ├── 分离 content ↔ tool_calls
│                 │      └── 提取 usage
└─────────────────┘
     │
     │  *Response, error
     ▼
  Agent Loop
```

---

## 错误处理

### B1. 错误分类树

```
LLM 调用错误
├── Retryable → 指数退避重试（Client 内部自动处理）
│   ├── network_timeout       — 连接超时、读取超时
│   ├── connection_reset      — 连接被重置
│   ├── dns_failure           — DNS 解析失败
│   ├── tls_handshake_failure — TLS 握手失败
│   ├── connection_refused    — 连接被拒绝
│   ├── rate_limited (429)    — 速率限制
│   ├── server_error (5xx)    — 服务端临时故障
│   └── malformed_response    — 响应非 JSON（如网关错误页面）
│
├── NonRetryable → 直接返回错误给调用方
│   ├── unauthorized (401)    — API Key 无效
│   ├── forbidden (403)       — 无权访问
│   ├── bad_request (400)     — 请求格式错误
│   ├── model_not_found (404) — 模型不存在或无权访问
│   ├── context_too_long      — 输入超过上下文窗口
│   └── payment_required (402) — 余额不足
```

### B2. 指数退避策略

```
attempt 0: 初始请求
  ↓ 失败（Retryable）
attempt 1: 等待 1s × [0.5, 1.5]（jitter）
  ↓ 失败（Retryable）
attempt 2: 等待 2s × [0.5, 1.5]
  ↓ 失败（Retryable）
attempt 3: 等待 4s × [0.5, 1.5]
  ↓ 返回 NonRetryable 错误（重试耗尽）

参数:
  MaxRetries     = 3
  InitialBackoff = 1s
  MaxBackoff     = 30s（单次等待上限）
  Multiplier     = 2.0

特殊处理:
  - 429 响应含 Retry-After 头时，使用该值
  - ctx 被取消时立即停止重试，返回 NonRetryableError
  - 每次重试前检查 ctx.Err()
```

### B3. 退避算法伪代码

**重试主循环**（`sendWithRetry`）：

```go
func (c *client) sendWithRetry(ctx context.Context, req *http.Request) (*Response, error) {
    var lastErr error
    maxAttempts := c.config.RetryPolicy.MaxRetries + 1

    for attempt := 0; attempt <= c.config.RetryPolicy.MaxRetries; attempt++ {
        if err := ctx.Err(); err != nil {
            return nil, &NonRetryableError{
                Message: "request cancelled",
                Cause:   err,
            }
        }

        reqClone := req.Clone(ctx)
        resp, err := c.doRequest(reqClone)
        if err == nil {
            // 成功时触发 OnRetry 回调（Err=nil）
            c.emitRetryEvent(ctx, RetryEvent{Attempt: attempt, MaxAttempts: maxAttempts, ...})
            return resp, nil
        }

        lastErr = err

        if c.adapter.ClassifyError(err) == ErrorClassNonRetryable {
            c.emitRetryEvent(ctx, RetryEvent{Attempt: attempt, MaxAttempts: maxAttempts, Error: err, WillRetry: false, ...})
            return nil, err
        }

        if attempt == c.config.RetryPolicy.MaxRetries {
            c.emitRetryEvent(ctx, RetryEvent{Attempt: attempt, MaxAttempts: maxAttempts, Error: err, WillRetry: false, ...})
            break
        }

        wait := c.config.RetryPolicy.ComputeBackoff(attempt, err)
        c.emitRetryEvent(ctx, RetryEvent{Attempt: attempt, MaxAttempts: maxAttempts, Error: err, Backoff: wait, WillRetry: true, ...})
        select {
        case <-ctx.Done():
            return nil, &NonRetryableError{
                Message: "request cancelled during backoff",
                Cause:   ctx.Err(),
            }
        case <-time.After(wait):
        }
    }

    return nil, &NonRetryableError{
        Message: fmt.Sprintf("retry exhausted after %d attempts", maxAttempts),
        Cause:   lastErr,
    }
}
```

**退避计算**（`RetryPolicy.ComputeBackoff`）：

```go
func (p RetryPolicy) ComputeBackoff(attempt int, err error) time.Duration {
    // 429 Retry-After 优先
    var re *RetryableError
    if errors.As(err, &re) && re.RetryAfter > 0 {
        return re.RetryAfter
    }

    base := time.Duration(float64(p.InitialBackoff) * math.Pow(p.Multiplier, float64(attempt)))
    if base > p.MaxBackoff {
        base = p.MaxBackoff
    }
    // jitter: [0.5 × base, 1.5 × base)
    return base/2 + time.Duration(rand.Int63n(int64(base)))
}
```

### B4. 责任边界

```
┌──────────────────────────────────┐
│           Agent Loop             │
│                                  │
│  只管收到最终结果: 成功 or 不可恢复错误
│  不关心重试了几次、等了多久        │
│         │                       │
│         ▼                       │
│  ┌──────────────────────┐       │
│  │     LLM Client        │       │
│  │  ┌──────────────────┐ │       │
│  │  │ RetryPolicy      │ │       │
│  │  │ computeBackoff() │ │       │
│  │  └──────────────────┘ │       │
│  │  ┌──────────────────┐ │       │
│  │  │ providerAdapter  │ │       │
│  │  │ BuildRequest()   │ │       │
│  │  │ ParseResponse()  │ │       │
│  │  │ ClassifyError()  │ │       │
│  │  └──────────────────┘ │       │
│  └──────────────────────┘       │
└──────────────────────────────────┘
```

---

## 不变量

1. **接口稳定**: `Client` 接口是 Loop 与 LLM 之间的唯一契约，Provider 变更不影响上层
2. **协议统一**: 所有 Provider 的请求格式基于 OpenAI Chat Completions，adapter 仅处理差异
3. **重试透明**: 调用方只看到最终成功或最终失败，看不到中间重试过程
4. **Context 优先**: 每次重试前检查 `ctx.Err()`，保证及时响应取消
5. **错误完整**: `RetryableError` / `NonRetryableError` 包含消息、状态码和原始错误
6. **配置不可变**: `ClientConfig` 在构造后不可修改，避免运行期竞态
7. **无状态**: Client 本身不持有任何会话状态（消息历史由 Loop 管理）
8. **并发安全**: Client 实例是 goroutine-safe 的，多个 goroutine 可并发调用 `SendMessage`
9. **Usage 传递**: Client 必须将 API 返回的 Token 用量填入 `Response.Usage`，作为 Context Manager 的数据源
10. **工具名唯一**: 工具名清理后若产生冲突，Client 返回错误拒绝发送请求
11. **ExtraParams 透传**: `Config.ExtraParams` 直接合并到请求 body 顶层，不做校验

---

## 测试计划

### 功能测试

1. **TestSendMessageSuccess** — 正常请求返回文本响应（含 Usage）
2. **TestSendMessageWithToolCalls** — LLM 返回工具调用 → 正确解析 ToolCalls
3. **TestSendMessageWithSystemPrompt** — System prompt 正确序列化
4. **TestSendMessageWithConversationHistory** — 多轮消息（含 tool call/tool result）正确序列化

### DeepSeek Adapter

5. **TestDeepSeekBuildRequest** — 验证请求 JSON 结构符合 OpenAI 格式
6. **TestDeepSeekBuildRequestExtraParams** — Config.ExtraParams 正确合并到 body 顶层
7. **TestDeepSeekParseResponseText** — 纯文本响应解析正确
8. **TestDeepSeekParseResponseToolCalls** — 工具调用响应解析正确（id, name, arguments）
9. **TestDeepSeekParseResponseUsage** — Usage 正确提取（含 cache_hit/cache_miss/reasoning_tokens）
10. **TestDeepSeekAuthHeader** — Authorization 头正确设置
11. **TestDeepSeekClassifyError** — 各 HTTP 状态码和网络错误分类正确

### 错误处理

12. **TestRetryableErrorRetried** — 429/5xx 错误自动重试，最终成功
13. **TestRetryableErrorExhausted** — 重试耗尽 → 返回 NonRetryableError
14. **TestNonRetryableErrorReturned** — 401/400 直接返回错误，不重试
15. **TestContextCancelledDuringRetry** — ctx 取消后立即停止重试

### 退避算法

16. **TestExponentialBackoff** — 验证退避时间符合公式
17. **TestRetryAfterHeader** — 429 含 Retry-After 头时使用该值
18. **TestMaxBackoffCap** — 退避时间不超过 MaxBackoff
19. **TestBackoffJitter** — 多次退避时间不完全相同

### 边界条件

20. **TestSendMessageEmptyMessages** — messages 为 nil 或空切片时返回错误
21. **TestSendMessageEmptyTools** — tools 为空切片时正常发送（无 function calling 场景）
22. **TestSendMessageNilContext** — ctx 为 nil 时返回错误
23. **TestParseResponseUnknownFields** — 响应含未识别字段时忽略
24. **TestParseResponseMalformedJSON** — 非 JSON 响应归类为 Retryable
25. **TestParseResponseMultipleChoices** — 多 choice 响应只取第一个
26. **TestSendMessageHTTPTransportError** — DNS/TLS/连接拒绝等归类为 Retryable
27. **TestSendMessageTimeout** — 单次请求超时正确传播
28. **TestToolNameCollision** — 清理后重名返回错误

### Mock 组件

- `mockHTTPTransport` — 拦截 HTTP 请求，返回可编程控制的响应（status + body），支持模拟网络层错误
- `spyAdapter` — 记录 BuildRequest / ParseResponse / ClassifyError 调用，验证 adapter 行为
- `spyRetryPolicy` — 记录每次重试的等待时间和计数

---

## 文件清单

| 操作 | 文件 | 说明 |
|------|------|------|
| 新增 | `pkg/llm/types.go` | Message, ToolCall, ToolSpec, Response, UsageInfo, StreamingEvent, ErrorClass, RetryPolicy, ClientConfig, ProviderType, BalanceInfo, RetryEvent, RetryHook 核心类型 |
| 新增 | `pkg/llm/client.go` | Client 接口（含 SendMessageStream/GetBalance/SupportsBalance）+ NewClient + SSE readStream + streamAccumulator + sendWithRetry |
| 新增 | `pkg/llm/adapter.go` | providerAdapter 内部接口定义（含 BuildStreamRequest + ParseStreamEvent + GetBalance + SupportsBalance） |
| 新增 | `pkg/llm/adapter_openai.go` | 标准 OpenAI adapter（默认兜底） + buildToolsJSON + newJSONRequest |
| 新增 | `pkg/llm/adapter_deepseek.go` | DeepSeek adapter 实现（含余额查询 + reasoning_effort 映射） |
| 新增 | `pkg/llm/retry.go` | RetryPolicy.ComputeBackoff 退避计算（指数退避 + jitter） |
| 新增 | `pkg/llm/errors.go` | RetryableError + NonRetryableError |
| 新增 | `pkg/llm/settings.go` | LLMSettings + LoadSettings + MergeLLMSettings + NewClientFromMergedSettings + DefaultSettings + WriteDefaultSettings |
| 新增 | `pkg/llm/client_test.go` | Client + adapter 单元测试 |
| 新增 | `pkg/llm/adapter_openai_test.go` | OpenAI adapter 测试 |
| 新增 | `pkg/llm/adapter_deepseek_test.go` | DeepSeek adapter 测试 |
| 新增 | `pkg/llm/retry_test.go` | 重试逻辑测试 |
| 新增 | `pkg/llm/errors_test.go` | 错误类型测试 |
| 新增 | `pkg/llm/types_test.go` | 核心类型测试 |
| 新增 | `pkg/llm/settings_test.go` | Settings 测试 |
| 新增 | `pkg/llm/settings_gen_test.go` | Settings 生成的默认配置测试 |

## 集成点

- 被 Agent Loop 通过 `llm.Client` 接口调用
- 不依赖任何 Waveloom 内部组件（最底层组件）
- 仅依赖标准库 `net/http` 和 `encoding/json`
- Wave 1 对接 DeepSeek API（`https://api.deepseek.com/v1`）
