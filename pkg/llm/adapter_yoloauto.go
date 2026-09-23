package llm

// yoloAutoAdapter 实现 providerAdapter,复用 openAIAdapter 的全部协议逻辑。
// Yolo-Auto(https://yolo-auto.com)提供标准 OpenAI Chat Completion 兼容端点
// (https://yolo-auto.com/v1,请求路径 {base}/chat/completions),
// 流式 SSE / tool calls / 错误码均与 OpenAI 一致,无 Provider 特有参数,
// 因此通过结构体嵌入直接继承 openAIAdapter 的方法集。
// 模型清单不内置:公开别名为 yolo / yolo-small,完整目录由
// GET {base}/models 自动发现(继承 openAIAdapter.ListModels)。
type yoloAutoAdapter struct {
	*openAIAdapter
}

// newYoloAutoAdapter 构造 Yolo-Auto adapter。BaseURL 为空时使用 Yolo-Auto
// OpenAI 兼容端点(接入文档:https://yolo-auto.com/docs)。
func newYoloAutoAdapter(cfg ClientConfig) *yoloAutoAdapter {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://yolo-auto.com/v1"
	}
	return &yoloAutoAdapter{openAIAdapter: newOpenAIAdapter(cfg)}
}

// 确保 yoloAutoAdapter 实现 providerAdapter(嵌入方法集变更时编译期报错)。
var _ providerAdapter = (*yoloAutoAdapter)(nil)
