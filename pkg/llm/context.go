package llm

import "context"

// modelOverrideKey 是用于传递 per-request model 覆盖的 context key。
type modelOverrideKey struct{}

// WithModelOverride 将 model 名称注入 ctx，adapter 读取后将替换请求中的 model 字段。
// model 为空字符串时返回原始 ctx（不覆盖）。
func WithModelOverride(ctx context.Context, model string) context.Context {
	if model == "" {
		return ctx
	}
	return context.WithValue(ctx, modelOverrideKey{}, model)
}

// ModelOverrideFromContext 从 ctx 提取 model 覆盖名称,无覆盖时返回空字符串。
func ModelOverrideFromContext(ctx context.Context) string {
	m, _ := ctx.Value(modelOverrideKey{}).(string)
	return m
}

// maxTokensKey 是用于传递 per-request max_tokens 覆盖的 context key。
// 部分场景(如压缩摘要)需要显式指定输出 token 上限:
// 不指定时服务端默认上限可能截断长 JSON 输出(响应不完整 → 校验失败)。
type maxTokensKey struct{}

// WithMaxTokens 将 max_tokens 注入 ctx,adapter 读取后将写入请求 body。
// n <= 0 时返回原始 ctx(不覆盖)。
func WithMaxTokens(ctx context.Context, n int) context.Context {
	if n <= 0 {
		return ctx
	}
	return context.WithValue(ctx, maxTokensKey{}, n)
}

// MaxTokensFromContext 从 ctx 提取 max_tokens 覆盖值,无覆盖时 ok=false。
func MaxTokensFromContext(ctx context.Context) (int, bool) {
	n, ok := ctx.Value(maxTokensKey{}).(int)
	return n, ok
}
