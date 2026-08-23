package pricing

import (
	"testing"
	"time"
)

func TestLookup_ExactMatch(t *testing.T) {
	// Lookup 委托 LookupCurrency → time.Now();用 LookupCurrencyAt 显式高峰时刻断言表内价
	p := LookupCurrencyAt("deepseek", "deepseek-v4-flash", CNY, beijingTime(10, 0))
	if p.CacheHit != 0.10 || p.CacheMiss != 3.0 || p.Output != 9.0 {
		t.Errorf("deepseek-v4-flash: got %+v", p)
	}
}

func TestLookup_ProviderWildcard(t *testing.T) {
	p := LookupCurrencyAt("deepseek", "deepseek-unknown-model", CNY, beijingTime(10, 0))
	if p.CacheHit != 0.10 || p.CacheMiss != 3.0 || p.Output != 9.0 {
		t.Errorf("deepseek wildcard: got %+v, expected V4 Flash fallback", p)
	}
}

func TestLookup_GlobalWildcard(t *testing.T) {
	p := Lookup("unknown", "unknown-model")
	if p.Prompt != 7.0 || p.Output != 14.0 {
		t.Errorf("global wildcard: got %+v", p)
	}
}

func TestLookup_OpenAI(t *testing.T) {
	p := Lookup("openai", "gpt-4o")
	if p.Prompt != 17.5 || p.Output != 70.0 {
		t.Errorf("gpt-4o: got %+v", p)
	}
}

func TestLookup_Kimi(t *testing.T) {
	p := Lookup("kimi", "kimi-k3")
	if p.CacheHit != 2.0 || p.CacheMiss != 20.0 || p.Output != 100.0 {
		t.Errorf("kimi-k3: got %+v", p)
	}
}

func TestLookupCurrency_CNY(t *testing.T) {
	p := LookupCurrencyAt("deepseek", "deepseek-v4-pro", CNY, beijingTime(10, 0))
	if p.CacheHit != 0.30 || p.CacheMiss != 9.0 || p.Output != 27.0 {
		t.Errorf("CNY v4-pro: got %+v", p)
	}
}

func TestLookupCurrency_USD(t *testing.T) {
	p := LookupCurrencyAt("deepseek", "deepseek-v4-pro", USD, beijingTime(10, 0))
	if p.CacheHit != 0.044 || p.CacheMiss != 1.32 || p.Output != 3.96 {
		t.Errorf("USD v4-pro: got %+v", p)
	}
}

func TestLookupCurrency_USD_Kimi(t *testing.T) {
	p := LookupCurrencyAt("kimi", "kimi-k3", USD, beijingTime(10, 0))
	if p.CacheHit != 0.30 || p.CacheMiss != 3.0 || p.Output != 15.0 {
		t.Errorf("USD kimi-k3: got %+v", p)
	}
}

func TestLookupCurrency_USD_OpenAI(t *testing.T) {
	p := LookupCurrencyAt("openai", "gpt-4o", USD, beijingTime(10, 0))
	if p.Prompt != 2.50 || p.Output != 10.00 {
		t.Errorf("USD gpt-4o: got %+v", p)
	}
}

func TestLookupCurrency_FallsBackToGlobal(t *testing.T) {
	p := LookupCurrencyAt("unknown", "unknown-model", USD, beijingTime(10, 0))
	if p.Prompt != 1.00 || p.Output != 2.00 {
		t.Errorf("USD global fallback: got %+v", p)
	}
}

// TestLookupCurrency_DelegatesToNow 验证 LookupCurrency 委托 LookupCurrencyAt(time.Now())。
func TestLookupCurrency_DelegatesToNow(t *testing.T) {
	now := time.Now()
	got := LookupCurrency("deepseek", "deepseek-v4-flash", CNY)
	want := LookupCurrencyAt("deepseek", "deepseek-v4-flash", CNY, now)
	if got != want {
		t.Errorf("LookupCurrency = %+v, want LookupCurrencyAt(now) = %+v", got, want)
	}
}

func TestCurrencySymbol(t *testing.T) {
	tests := []struct {
		c    Currency
		want string
	}{
		{CNY, "¥"},
		{USD, "$"},
		{Currency(""), "¥"}, // default
	}
	for _, tt := range tests {
		got := CurrencySymbol(tt.c)
		if got != tt.want {
			t.Errorf("CurrencySymbol(%q) = %q, want %q", tt.c, got, tt.want)
		}
	}
}

func TestTableFor(t *testing.T) {
	cnyT := tableFor(CNY)
	if cnyT["deepseek/deepseek-v4-flash"].CacheHit != 0.10 {
		t.Error("CNY table should have deepseek-v4-flash at 0.10 (peak)")
	}
	// 图像理解模型与 flash 同价(官方定价页)
	if cnyT["deepseek/deepseek-v4-flash-vision-exp"] != cnyT["deepseek/deepseek-v4-flash"] {
		t.Error("CNY vision-exp should match flash pricing")
	}

	usdT := tableFor(USD)
	if usdT["deepseek/deepseek-v4-flash"].CacheHit != 0.014 {
		t.Error("USD table should have deepseek-v4-flash at 0.014 (peak)")
	}
	if usdT["deepseek/deepseek-v4-flash-vision-exp"] != usdT["deepseek/deepseek-v4-flash"] {
		t.Error("USD vision-exp should match flash pricing")
	}
}

// beijingTime 构造北京时间(UTC+8)的指定时刻,用于峰谷定价边界测试。
func beijingTime(hour, min int) time.Time {
	return time.Date(2026, 8, 17, hour, min, 0, 0, time.FixedZone("CST", 8*3600))
}

func TestIsPeakTime(t *testing.T) {
	tests := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"peak start 09:00", beijingTime(9, 0), true},
		{"peak mid 11:59", beijingTime(11, 59), true},
		{"off-peak 08:59", beijingTime(8, 59), false},
		{"off-peak noon 12:00", beijingTime(12, 0), false},
		{"off-peak 13:59", beijingTime(13, 59), false},
		{"peak start 14:00", beijingTime(14, 0), true},
		{"peak mid 17:59", beijingTime(17, 59), true},
		{"off-peak 18:00", beijingTime(18, 0), false},
		{"off-peak night 23:00", beijingTime(23, 0), false},
		{"off-peak dawn 00:00", beijingTime(0, 0), false},
		// UTC 输入转换:2026-08-16 16:00 UTC = 北京 2026-08-17 00:00(空闲)
		{"utc input converted to beijing", time.Date(2026, 8, 16, 16, 0, 0, 0, time.UTC), false},
	}
	for _, tt := range tests {
		if got := IsPeakTime(tt.t); got != tt.want {
			t.Errorf("IsPeakTime(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestRegression_WeekendIsOffPeak 回归防护:IsPeakTime 曾遗漏「周一至周五」,
// 周末高峰窗口(9-12、14-18)被误判为高峰,四项价格均不打五折。
// 根因:只比较北京时间小时,未排除周六/周日。官方定价页明确高峰时段为
// 北京时间周一至周五 9:00-12:00、14:00-18:00,其余(含整个周末)为空闲时段。
func TestRegression_WeekendIsOffPeak(t *testing.T) {
	satPeakWindow := time.Date(2026, 8, 22, 10, 0, 0, 0, time.FixedZone("CST", 8*3600)) // 周六 10:00
	sunPeakWindow := time.Date(2026, 8, 23, 15, 0, 0, 0, time.FixedZone("CST", 8*3600)) // 周日 15:00
	friPeakWindow := time.Date(2026, 8, 21, 10, 0, 0, 0, time.FixedZone("CST", 8*3600)) // 周五 10:00

	if IsPeakTime(satPeakWindow) {
		t.Error("周六 10:00 应判空闲时段,实际判为高峰")
	}
	if IsPeakTime(sunPeakWindow) {
		t.Error("周日 15:00 应判空闲时段,实际判为高峰")
	}
	if !IsPeakTime(friPeakWindow) {
		t.Error("周五 10:00 应判高峰时段,实际判为空闲")
	}

	// 周末高峰窗口内应返回空闲半价:0.10/3.0/9.0 → 0.05/1.5/4.5
	p := LookupCurrencyAt("deepseek", "deepseek-v4-flash", CNY, satPeakWindow)
	if p.CacheHit != 0.05 || p.CacheMiss != 1.5 || p.Output != 4.5 {
		t.Errorf("周六 10:00 应返回空闲半价,got %+v", p)
	}
}

func TestLookupCurrencyAt_OffPeakHalf(t *testing.T) {
	// 高峰时段:表内价格原样
	peak := LookupCurrencyAt("deepseek", "deepseek-v4-flash", CNY, beijingTime(10, 0))
	if peak.CacheHit != 0.10 || peak.CacheMiss != 3.0 || peak.Output != 9.0 {
		t.Errorf("peak price: got %+v", peak)
	}

	// 空闲时段:高峰价 × 0.5
	off := LookupCurrencyAt("deepseek", "deepseek-v4-flash", CNY, beijingTime(13, 0))
	if off.CacheHit != 0.05 || off.CacheMiss != 1.5 || off.Output != 4.5 {
		t.Errorf("off-peak price: got %+v", off)
	}

	// pro 空闲:0.30/9.0/27.0 → 0.15/4.5/13.5
	proOff := LookupCurrencyAt("deepseek", "deepseek-v4-pro", CNY, beijingTime(13, 0))
	if proOff.CacheHit != 0.15 || proOff.CacheMiss != 4.5 || proOff.Output != 13.5 {
		t.Errorf("pro off-peak price: got %+v", proOff)
	}
}

func TestLookupCurrencyAt_NonDeepSeekUnaffected(t *testing.T) {
	// kimi/openai 无峰谷定价,空闲时段价格不变
	off := LookupCurrencyAt("kimi", "kimi-k3", CNY, beijingTime(13, 0))
	if off.CacheHit != 2.0 || off.CacheMiss != 20.0 || off.Output != 100.0 {
		t.Errorf("kimi off-peak should be unchanged: got %+v", off)
	}
}

func TestLookupCurrencyAt_USD_OffPeakHalf(t *testing.T) {
	peak := LookupCurrencyAt("deepseek", "deepseek-v4-pro", USD, beijingTime(10, 0))
	if peak.CacheHit != 0.044 || peak.CacheMiss != 1.32 || peak.Output != 3.96 {
		t.Errorf("USD peak price: got %+v", peak)
	}
	off := LookupCurrencyAt("deepseek", "deepseek-v4-pro", USD, beijingTime(13, 0))
	if off.CacheHit != 0.022 || off.CacheMiss != 0.66 || off.Output != 1.98 {
		t.Errorf("USD off-peak price: got %+v", off)
	}
}

func TestInferProvider(t *testing.T) {
	tests := []struct {
		model    string
		expected string
	}{
		{"deepseek-v4-flash", "deepseek"},
		{"deepseek-v4-pro", "deepseek"},
		{"deepseek-chat", "deepseek"},
		{"kimi-k3", "kimi"},
		{"kimi-k2.7", "kimi"},
		{"gpt-4o", "openai"},
		{"gpt-4.1", "openai"},
		{"o1-mini", "openai"},
		{"o3", "openai"},
		{"o4", "openai"},
		{"claude-sonnet", "openai"},
		{"unknown-model", "*"},
	}
	for _, tt := range tests {
		got := InferProvider(tt.model)
		if got != tt.expected {
			t.Errorf("InferProvider(%q) = %q, want %q", tt.model, got, tt.expected)
		}
	}
}

func TestCalculateCost_CachePricing(t *testing.T) {
	// 10 cache-hit input + 5 cache-miss input + 3 output tokens
	// kimi-k3 CNY: CacheHit=2.0, CacheMiss=20.0, Output=100.0
	price := Price{CacheHit: 2.0, CacheMiss: 20.0, Output: 100.0}
	cost := CalculateCost(price, 0, 10, 5, 3)
	expected := float64(10)*(2.0/1_000_000) + float64(5)*(20.0/1_000_000) + float64(3)*(100.0/1_000_000)
	if cost != expected {
		t.Errorf("cache pricing: got %v, want %v", cost, expected)
	}
}

func TestCalculateCost_PromptFallback(t *testing.T) {
	// No cache info → use Prompt price for all input tokens
	price := Price{Prompt: 17.5, Output: 70.0}
	cost := CalculateCost(price, 1000, 0, 0, 500)
	expected := float64(1000)*(17.5/1_000_000) + float64(500)*(70.0/1_000_000)
	if cost != expected {
		t.Errorf("prompt fallback: got %v, want %v", cost, expected)
	}
}

func TestCalculateCost_ZeroTokens(t *testing.T) {
	price := Price{CacheHit: 2.0, CacheMiss: 20.0, Output: 100.0}
	cost := CalculateCost(price, 0, 0, 0, 0)
	if cost != 0.0 {
		t.Errorf("zero tokens: got %v, want 0", cost)
	}
}

func TestCalculateCost_CacheHitOnly(t *testing.T) {
	price := Price{CacheHit: 0.02, CacheMiss: 1.0, Output: 2.0}
	cost := CalculateCost(price, 0, 1000000, 0, 0)
	expected := float64(1000000) * (0.02 / 1_000_000)
	if cost != expected {
		t.Errorf("cache hit only: got %v, want %v", cost, expected)
	}
}

func TestCalculateCost_CacheMissOnly(t *testing.T) {
	price := Price{CacheHit: 0.02, CacheMiss: 1.0, Output: 2.0}
	cost := CalculateCost(price, 0, 0, 1000000, 0)
	expected := float64(1000000) * (1.0 / 1_000_000)
	if cost != expected {
		t.Errorf("cache miss only: got %v, want %v", cost, expected)
	}
}

func TestCalculateCost_OutputOnly(t *testing.T) {
	price := Price{CacheHit: 2.0, CacheMiss: 20.0, Output: 100.0}
	cost := CalculateCost(price, 0, 0, 0, 1000000)
	expected := float64(1000000) * (100.0 / 1_000_000)
	if cost != expected {
		t.Errorf("output only: got %v, want %v", cost, expected)
	}
}
