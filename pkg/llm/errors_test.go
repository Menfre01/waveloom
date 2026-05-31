package llm

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRetryableErrorImplementsError(t *testing.T) {
	cause := fmt.Errorf("root cause")
	re := &RetryableError{
		Message:    "rate limited",
		StatusCode: 429,
		Cause:      cause,
	}

	if re.Error() != "rate limited" {
		t.Errorf("Error() = %q, want %q", re.Error(), "rate limited")
	}

	if unwrapped := re.Unwrap(); unwrapped != cause {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, cause)
	}

	// 验证 errors.As 可用
	var target *RetryableError
	if !errors.As(re, &target) {
		t.Error("errors.As failed to match *RetryableError")
	}
}

func TestNonRetryableErrorImplementsError(t *testing.T) {
	cause := fmt.Errorf("root cause")
	nre := &NonRetryableError{
		Message:    "unauthorized",
		StatusCode: 401,
		Cause:      cause,
	}

	if nre.Error() != "unauthorized" {
		t.Errorf("Error() = %q, want %q", nre.Error(), "unauthorized")
	}

	if unwrapped := nre.Unwrap(); unwrapped != cause {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, cause)
	}

	var target *NonRetryableError
	if !errors.As(nre, &target) {
		t.Error("errors.As failed to match *NonRetryableError")
	}
}

func TestRetryableErrorWithRetryAfter(t *testing.T) {
	re := &RetryableError{
		Message:    "rate limited",
		StatusCode: 429,
		RetryAfter: 5 * time.Second,
	}

	if re.RetryAfter != 5*time.Second {
		t.Errorf("RetryAfter = %v, want %v", re.RetryAfter, 5*time.Second)
	}
}
