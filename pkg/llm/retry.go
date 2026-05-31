package llm

import (
	"errors"
	"math"
	"math/rand"
	"time"
)

// ComputeBackoff 计算给定重试次数的退避等待时间。
// 优先使用 RetryableError 中的 Retry-After 值，
// 否则按指数退避公式计算并添加 jitter。
func (p RetryPolicy) ComputeBackoff(attempt int, err error) time.Duration {
	// 429 Retry-After 优先
	var re *RetryableError
	if errors.As(err, &re) && re.RetryAfter > 0 {
		return re.RetryAfter
	}

	base := time.Duration(float64(p.InitialBackoff) * math.Pow(p.Multiplier, float64(attempt)))
	if base > p.MaxBackoff {
		base = p.MaxBackoff
	}
	if base <= 0 {
		base = time.Millisecond
	}

	// jitter: [0.5 × base, 1.5 × base)
	return base/2 + time.Duration(rand.Int63n(int64(base)))
}
