package llm

import (
	"context"
	"net/http"
)

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

	// ListModels 获取 Provider 支持的模型列表。
	// 对应 GET /models（DeepSeek）或 GET /models（OpenAI）。
	ListModels(ctx context.Context, httpClient *http.Client) ([]ModelInfo, error)
}
