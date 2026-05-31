package llm

import (
	"testing"
	"time"
)

func TestExponentialBackoff(t *testing.T) {
	policy := DefaultRetryPolicy()

	tests := []struct {
		attempt   int
		minFactor float64 // base * minFactor ≤ result
		maxFactor float64 // result ≤ base * maxFactor
	}{
		{0, 0.5, 1.5}, // 1s × [0.5, 1.5) = [0.5s, 1.5s)
		{1, 0.5, 1.5}, // 2s × [0.5, 1.5) = [1.0s, 3.0s)
		{2, 0.5, 1.5}, // 4s × [0.5, 1.5) = [2.0s, 6.0s)
	}

	for _, tt := range tests {
		base := time.Duration(float64(policy.InitialBackoff) * powFloat(policy.Multiplier, float64(tt.attempt)))
		minDur := time.Duration(float64(base) * tt.minFactor)
		maxDur := time.Duration(float64(base) * tt.maxFactor)

		// 运行多次以覆盖 jitter 范围
		for i := 0; i < 50; i++ {
			got := policy.ComputeBackoff(tt.attempt, nil)
			if got < minDur || got >= maxDur {
				t.Errorf("attempt %d: ComputeBackoff = %v, want in [%v, %v)", tt.attempt, got, minDur, maxDur)
				break
			}
		}
	}
}

func TestRetryAfterHeader(t *testing.T) {
	policy := DefaultRetryPolicy()
	retryAfter := 5 * time.Second
	err := &RetryableError{
		Message:    "rate limited",
		StatusCode: 429,
		RetryAfter: retryAfter,
	}

	got := policy.ComputeBackoff(0, err)
	if got != retryAfter {
		t.Errorf("ComputeBackoff with Retry-After = %v, want %v", got, retryAfter)
	}

	// Retry-After 应优先于 attempt 参数
	got2 := policy.ComputeBackoff(5, err)
	if got2 != retryAfter {
		t.Errorf("ComputeBackoff with Retry-After at attempt 5 = %v, want %v", got2, retryAfter)
	}
}

func TestMaxBackoffCap(t *testing.T) {
	policy := DefaultRetryPolicy()
	// attempt=5: base = 1s * 2^5 = 32s > MaxBackoff=30s，应被截断
	// jitter 范围: [15s, 45s)，但 base 被截断为 30s，实际范围 [15s, 45s)
	// 等等，base 被截断为 30s，jitter = 15s + rand(30s) = [15s, 45s)
	// 但我们期望 base 被截断后的 jitter 范围是 [15s, 45s)

	for i := 0; i < 100; i++ {
		got := policy.ComputeBackoff(10, nil) // 高 attempt，base 远超 MaxBackoff
		minExpected := policy.MaxBackoff / 2   // 15s
		maxExpected := policy.MaxBackoff * 3 / 2 // 45s
		if got < minExpected || got >= maxExpected {
			t.Errorf("ComputeBackoff at high attempt = %v, want in [%v, %v)", got, minExpected, maxExpected)
			break
		}
	}
}

func TestBackoffJitter(t *testing.T) {
	policy := DefaultRetryPolicy()
	seen := make(map[time.Duration]bool)

	for i := 0; i < 100; i++ {
		got := policy.ComputeBackoff(0, nil)
		seen[got] = true
	}

	// 100 次 jitter 应产生多个不同值
	if len(seen) < 10 {
		t.Errorf("expected at least 10 distinct backoff values from 100 calls, got %d", len(seen))
	}
}

func TestComputeBackoffZeroInitialBackoff(t *testing.T) {
	p := RetryPolicy{
		MaxRetries:     3,
		InitialBackoff: 0,
		MaxBackoff:     30 * time.Second,
		Multiplier:     2.0,
	}

	// base = 0 * 2^0 = 0, which triggers the base <= 0 guard → base = 1ms
	// jitter range: [0.5ms, 1.5ms)
	for i := 0; i < 50; i++ {
		got := p.ComputeBackoff(0, nil)
		if got < 500*time.Microsecond || got >= 1500*time.Microsecond {
			t.Errorf("ComputeBackoff with zero InitialBackoff = %v, want in [500us, 1.5ms)", got)
			break
		}
	}
}

func TestComputeBackoffNegativeInitialBackoff(t *testing.T) {
	p := RetryPolicy{
		MaxRetries:     3,
		InitialBackoff: -1 * time.Second,
		MaxBackoff:     30 * time.Second,
		Multiplier:     2.0,
	}

	// base = -1s * 2^0 = -1s, which triggers the base <= 0 guard → base = 1ms
	for i := 0; i < 50; i++ {
		got := p.ComputeBackoff(0, nil)
		if got < 500*time.Microsecond || got >= 1500*time.Microsecond {
			t.Errorf("ComputeBackoff with negative InitialBackoff = %v, want in [500us, 1.5ms)", got)
			break
		}
	}
}

func TestComputeBackoffZeroMultiplier(t *testing.T) {
	p := RetryPolicy{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		Multiplier:     0,
	}

	// base = 1s * 0^0 = 1s * 1 = 1s (first attempt at attempt=0)
	// For attempt=2: base = 1s * 0^2 = 0 → base <= 0 guard → base = 1ms
	for i := 0; i < 50; i++ {
		got := p.ComputeBackoff(2, nil)
		if got < 500*time.Microsecond || got >= 1500*time.Microsecond {
			t.Errorf("ComputeBackoff with zero Multiplier at attempt 2 = %v, want in [500us, 1.5ms)", got)
			break
		}
	}
}

// powFloat 计算 base^exp，避免引入 math.Pow 的测试依赖问题。
func powFloat(base, exp float64) float64 {
	result := 1.0
	for i := 0; i < int(exp); i++ {
		result *= base
	}
	return result
}
