// Package pricing 提供 LLM token 计费。
// 支持 CNY/USD 双币种,根据 locale 自动选择价格表和货币符号。
package pricing

import (
	"strings"
	"time"
)

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
// 2026-08-17 00:00(北京时间)起 DeepSeek 采用峰谷定价:表内为高峰价,
// 空闲时段(北京时间周一至周五 9:00-12:00、14:00-18:00 之外,含整个周末)= 高峰价 × 0.5。
// 生效时刻之前为平价(无折半),由 peakPricingEffectiveAt 门槛控制。
var cnyTable = map[string]Price{
	// DeepSeek
	"deepseek/deepseek-v4-flash":  {CacheHit: 0.10, CacheMiss: 3.0, Prompt: 3.0, Output: 9.0},
	"deepseek/deepseek-v4-pro":    {CacheHit: 0.30, CacheMiss: 9.0, Prompt: 9.0, Output: 27.0},
	// 图像理解模型,价格与 flash 一致(官方定价页)
	"deepseek/deepseek-v4-flash-vision-exp": {CacheHit: 0.10, CacheMiss: 3.0, Prompt: 3.0, Output: 9.0},
	"deepseek/":                   {CacheHit: 0.10, CacheMiss: 3.0, Prompt: 3.0, Output: 9.0},

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
// 2026-08-16 16:00 UTC(2026-08-17 00:00 北京时间)起 DeepSeek 采用峰谷定价:
// 高峰时段为 UTC 周一至周五 01:00-04:00、06:00-10:00(北京时间 9:00-12:00、14:00-18:00),
// 空闲时段 = 高峰价 × 0.5。
// 生效时刻之前为平价(无折半),由 peakPricingEffectiveAt 门槛控制。
var usdTable = map[string]Price{
	// DeepSeek
	"deepseek/deepseek-v4-flash":  {CacheHit: 0.014, CacheMiss: 0.44, Prompt: 0.44, Output: 1.32},
	"deepseek/deepseek-v4-pro":    {CacheHit: 0.044, CacheMiss: 1.32, Prompt: 1.32, Output: 3.96},
	// 图像理解模型,价格与 flash 一致(官方定价页)
	"deepseek/deepseek-v4-flash-vision-exp": {CacheHit: 0.014, CacheMiss: 0.44, Prompt: 0.44, Output: 1.32},
	"deepseek/":                   {CacheHit: 0.014, CacheMiss: 0.44, Prompt: 0.44, Output: 1.32},

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
	return LookupCurrencyAt(provider, model, c, time.Now())
}

// LookupCurrencyAt 根据 provider、model、币种和指定时间查找价格。
// DeepSeek 自 peakPricingEffectiveAt(2026-08-16 16:00 UTC)起采用峰谷定价:
// 高峰时段(北京时间周一至周五 9:00-12:00、14:00-18:00)使用表内价格,
// 空闲时段为高峰价 × 0.5;生效时刻之前为平价,表内价格原样返回。
// 其他 provider(kimi/openai 等)不受峰谷影响,返回表内价格。
func LookupCurrencyAt(provider, model string, c Currency, at time.Time) Price {
	table := tableFor(c)
	p, ok := table[provider+"/"+model]
	if !ok {
		p, ok = table[provider+"/"]
	}
	if !ok {
		p = table["*/*"]
	}
	// 仅 DeepSeek 峰谷定价;生效时刻之前为平价(无折半),生效后空闲时段 = 高峰价 × 0.5
	if provider == "deepseek" && !at.Before(peakPricingEffectiveAt) && !IsPeakTime(at) {
		p.CacheHit *= 0.5
		p.CacheMiss *= 0.5
		p.Prompt *= 0.5
		p.Output *= 0.5
	}
	return p
}

// peakPricingEffectiveAt DeepSeek 峰谷定价生效时刻(UTC)。
// 2026-08-16 16:00 UTC = 2026-08-17 00:00 北京时间;此前的历史时间一律按平价计,
// 不适用峰谷折半(官方公告: Effective 00:00 Beijing Time on August 17, 2026)。
var peakPricingEffectiveAt = time.Date(2026, 8, 16, 16, 0, 0, 0, time.UTC)

// IsPeakTime 判断指定时间是否处于 DeepSeek 高峰时段。
// 高峰时段(北京时间): 周一至周五 9:00-12:00、14:00-18:00;其余(含整个周末)为空闲时段。
// 参考: https://api-docs.deepseek.com/zh-cn/quick_start/pricing
func IsPeakTime(t time.Time) bool {
	// 北京时间 = UTC+8(无夏令时);FixedZone 名称仅作显示用,不影响转换。
	// 星期属于厂商时钟,必须在北京时间上取 Weekday()。
	beijing := t.In(time.FixedZone("UTC+8", 8*3600))
	switch beijing.Weekday() {
	case time.Saturday, time.Sunday:
		return false
	}
	h := beijing.Hour()
	return (h >= 9 && h < 12) || (h >= 14 && h < 18)
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
