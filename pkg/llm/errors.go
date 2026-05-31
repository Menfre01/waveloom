package llm

import "time"

// RetryableError 标记一个 error 为可重试。
type RetryableError struct {
	Message    string
	StatusCode int
	RetryAfter time.Duration // 可选，来自 Retry-After 头
	Cause      error
}

func (e *RetryableError) Error() string { return e.Message }
func (e *RetryableError) Unwrap() error { return e.Cause }

// NonRetryableError 标记一个 error 为不可重试。
type NonRetryableError struct {
	Message    string
	StatusCode int
	Cause      error
}

func (e *NonRetryableError) Error() string { return e.Message }
func (e *NonRetryableError) Unwrap() error { return e.Cause }
