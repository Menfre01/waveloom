// Package pricing 提供 LLM token 计费。
// 支持 CNY/USD 双币种,根据 locale 自动选择价格表和货币符号。
package pricing

import "strings"

// Currency 表示计费币种。
type Currency string

const (
	CNY Currency = "CNY" // 人民币
	USD Currency = "USD" // 美元
)

// Price 记录各类型 token 的单价(CNY 或 USD / 1M tokens)。
type Price struct {
	CacheHit  float64 // 缓存命中输入
	CacheMiss float64 // 缓存未命中输入
	Prompt    float64 // 无缓存信息的输入兜底价
	Output    float64 // 输出
}

// cnyTable 中文价格(元/1M tokens,来自官方中文定价页)。
// 官方页面: https://api-docs.deepseek.com/zh-cn/quick_start/pricing
var cnyTable = map[string]Price{
	// DeepSeek
	"deepseek/deepseek-v4-flash":  {CacheHit: 0.02, CacheMiss: 1.0, Prompt: 1.0, Output: 2.0},
	"deepseek/deepseek-v4-pro":    {CacheHit: 0.025, CacheMiss: 3.0, Prompt: 3.0, Output: 6.0},
	"deepseek/":                   {CacheHit: 0.02, CacheMiss: 1.0, Prompt: 1.0, Output: 2.0},

	// Kimi (Moonshot)
	"kimi/kimi-k3": {CacheHit: 2.0, CacheMiss: 20.0, Prompt: 20.0, Output: 100.0},
	"kimi/":        {CacheHit: 2.0, CacheMiss: 20.0, Prompt: 20.0, Output: 100.0},

	// OpenAI
	"openai/gpt-4o":  {Prompt: 17.5, Output: 70.0},
	"openai/gpt-4.1": {Prompt: 14.0, Output: 56.0},
	"openai/":        {Prompt: 14.0, Output: 56.0},

	// 全局通配
	"*/*": {Prompt: 7.0, Output: 14.0},
}

// usdTable 英文价格($/1M tokens,来自官方英文定价页)。
// 官方页面: https://api-docs.deepseek.com/quick_start/pricing
var usdTable = map[string]Price{
	// DeepSeek
	"deepseek/deepseek-v4-flash":  {CacheHit: 0.0028, CacheMiss: 0.14, Prompt: 0.14, Output: 0.28},
	"deepseek/deepseek-v4-pro":    {CacheHit: 0.003625, CacheMiss: 0.435, Prompt: 0.435, Output: 0.87},
	"deepseek/":                   {CacheHit: 0.0028, CacheMiss: 0.14, Prompt: 0.14, Output: 0.28},

	// Kimi (Moonshot)
	"kimi/kimi-k3": {CacheHit: 0.30, CacheMiss: 3.0, Prompt: 3.0, Output: 15.0},
	"kimi/":        {CacheHit: 0.30, CacheMiss: 3.0, Prompt: 3.0, Output: 15.0},

	// OpenAI
	"openai/gpt-4o":  {Prompt: 2.50, Output: 10.00},
	"openai/gpt-4.1": {Prompt: 2.00, Output: 8.00},
	"openai/":        {Prompt: 2.00, Output: 8.00},

	// 全局通配
	"*/*": {Prompt: 1.00, Output: 2.00},
}

// Lookup 根据 provider 和 model 查找价格(默认 CNY)。保留此函数用于无需币种感知的调用方。
func Lookup(provider, model string) Price {
	return LookupCurrency(provider, model, CNY)
}

// LookupCurrency 根据 provider、model 和币种查找价格。
// 优先精确匹配 model,回退到 provider 通配,最后全局通配。
func LookupCurrency(provider, model string, c Currency) Price {
	t := tableFor(c)
	if p, ok := t[provider+"/"+model]; ok {
		return p
	}
	if p, ok := t[provider+"/"]; ok {
		return p
	}
	return t["*/*"]
}

// CurrencySymbol 返回币种的显示符号。
func CurrencySymbol(c Currency) string {
	switch c {
	case USD:
		return "$"
	default:
		return "¥"
	}
}

// tableFor 返回币种对应的价格表。
func tableFor(c Currency) map[string]Price {
	if c == USD {
		return usdTable
	}
	return cnyTable
}

// InferProvider 从 model 名推导 provider。用于 footer 计费等不需要精确 provider 的场景。
func InferProvider(model string) string {
	switch {
	case strings.HasPrefix(model, "deepseek"):
		return "deepseek"
	case strings.HasPrefix(model, "kimi"):
		return "kimi"
	case strings.HasPrefix(model, "gpt"),
		strings.HasPrefix(model, "o1"),
		strings.HasPrefix(model, "o3"),
		strings.HasPrefix(model, "o4"),
		strings.HasPrefix(model, "claude"):
		return "openai"
	default:
		return "*"
	}
}

// CalculateCost 根据 token 用量和价格计算总费用。
// cacheHit + cacheMiss > 0 时分别计费,否则用 prompt 兜底价。
func CalculateCost(price Price, prompt, cacheHit, cacheMiss, completion int) float64 {
	cost := 0.0

	if cacheHit+cacheMiss > 0 {
		cost += float64(cacheHit) * (price.CacheHit / 1_000_000)
		cost += float64(cacheMiss) * (price.CacheMiss / 1_000_000)
	} else {
		cost += float64(prompt) * (price.Prompt / 1_000_000)
	}

	cost += float64(completion) * (price.Output / 1_000_000)

	return cost
}
