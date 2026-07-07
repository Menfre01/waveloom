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

// ModelOverrideFromContext 从 ctx 提取 model 覆盖名称，无覆盖时返回空字符串。
func ModelOverrideFromContext(ctx context.Context) string {
	m, _ := ctx.Value(modelOverrideKey{}).(string)
	return m
}
