package llm

import (
	"context"
	"net/http"
)

// glmAdapter 实现 providerAdapter,复用 openAIAdapter 的全部协议逻辑。
// GLM Coding Plan 提供标准 OpenAI Chat Completion 兼容端点
// (https://open.bigmodel.cn/api/coding/paas/v4,请求路径 {base}/chat/completions),
// 流式 SSE / tool calls / 错误码均与 OpenAI 一致,无 Provider 特有参数,
// 因此通过结构体嵌入直接继承 openAIAdapter 的方法集。
type glmAdapter struct {
	*openAIAdapter
}

// newGLMAdapter 构造 GLM adapter。BaseURL 为空时使用 GLM Coding Plan
// OpenAI 兼容端点(官方接入指南:OpenAI Chat Completion 协议)。
func newGLMAdapter(cfg ClientConfig) *glmAdapter {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://open.bigmodel.cn/api/coding/paas/v4"
	}
	return &glmAdapter{openAIAdapter: newOpenAIAdapter(cfg)}
}

// BuildStreamRequest 覆写流式请求构造:在 OpenAI 兼容 body 基础上追加
// stream_options.include_usage。GLM 遵循 OpenAI 协议——流式响应默认不返回
// usage,显式要求后最后一帧(choices 为空)才携带 prompt/completion/cached_tokens
// 统计(TUI ctx 进度条与 cache 数值的来源)。非流式请求不设置该参数,
// OpenAI 规范仅允许其在 stream:true 时出现(严格校验的兼容端点会拒绝 400),
// 且非流式响应默认携带 usage,无需 include_usage。
func (a *glmAdapter) BuildStreamRequest(ctx context.Context, messages []Message, tools []ToolSpec) (*http.Request, error) {
	body := a.buildRequestBody(ctx, messages, tools, true)
	body["stream_options"] = map[string]any{"include_usage": true}
	return newJSONRequest(http.MethodPost, a.baseURL+"/chat/completions", body)
}

// 确保 glmAdapter 实现 providerAdapter(嵌入方法集变更时编译期报错)。
var _ providerAdapter = (*glmAdapter)(nil)
