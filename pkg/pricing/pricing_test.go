package pricing

import (
	"testing"
)

func TestLookup_ExactMatch(t *testing.T) {
	p := Lookup("deepseek", "deepseek-v4-flash")
	if p.CacheHit != 0.02 || p.CacheMiss != 1.0 || p.Output != 2.0 {
		t.Errorf("deepseek-v4-flash: got %+v", p)
	}
}

func TestLookup_ProviderWildcard(t *testing.T) {
	p := Lookup("deepseek", "deepseek-unknown-model")
	if p.CacheHit != 0.02 || p.CacheMiss != 1.0 || p.Output != 2.0 {
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
	p := LookupCurrency("deepseek", "deepseek-v4-pro", CNY)
	if p.CacheHit != 0.025 || p.CacheMiss != 3.0 || p.Output != 6.0 {
		t.Errorf("CNY v4-pro: got %+v", p)
	}
}

func TestLookupCurrency_USD(t *testing.T) {
	p := LookupCurrency("deepseek", "deepseek-v4-pro", USD)
	if p.CacheHit != 0.003625 || p.CacheMiss != 0.435 || p.Output != 0.87 {
		t.Errorf("USD v4-pro: got %+v", p)
	}
}

func TestLookupCurrency_USD_Kimi(t *testing.T) {
	p := LookupCurrency("kimi", "kimi-k3", USD)
	if p.CacheHit != 0.30 || p.CacheMiss != 3.0 || p.Output != 15.0 {
		t.Errorf("USD kimi-k3: got %+v", p)
	}
}

func TestLookupCurrency_USD_OpenAI(t *testing.T) {
	p := LookupCurrency("openai", "gpt-4o", USD)
	if p.Prompt != 2.50 || p.Output != 10.00 {
		t.Errorf("USD gpt-4o: got %+v", p)
	}
}

func TestLookupCurrency_FallsBackToGlobal(t *testing.T) {
	p := LookupCurrency("unknown", "unknown-model", USD)
	if p.Prompt != 1.00 || p.Output != 2.00 {
		t.Errorf("USD global fallback: got %+v", p)
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
	if cnyT["deepseek/deepseek-v4-flash"].CacheHit != 0.02 {
		t.Error("CNY table should have deepseek-v4-flash at 0.02")
	}

	usdT := tableFor(USD)
	if usdT["deepseek/deepseek-v4-flash"].CacheHit != 0.0028 {
		t.Error("USD table should have deepseek-v4-flash at 0.0028")
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
