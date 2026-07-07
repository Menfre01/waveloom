package llm

import (
	"context"
	"testing"
)

func TestWithModelOverride_SetsValue(t *testing.T) {
	ctx := context.Background()
	ctx = WithModelOverride(ctx, "test-model")
	got := ModelOverrideFromContext(ctx)
	if got != "test-model" {
		t.Errorf("ModelOverrideFromContext = %q, want %q", got, "test-model")
	}
}

func TestWithModelOverride_EmptyStringReturnsSameCtx(t *testing.T) {
	ctx := context.Background()
	newCtx := WithModelOverride(ctx, "")
	if newCtx != ctx {
		t.Error("WithModelOverride with empty string should return same context")
	}
	got := ModelOverrideFromContext(newCtx)
	if got != "" {
		t.Errorf("ModelOverrideFromContext should return empty, got %q", got)
	}
}

func TestModelOverrideFromContext_NoValue(t *testing.T) {
	ctx := context.Background()
	got := ModelOverrideFromContext(ctx)
	if got != "" {
		t.Errorf("ModelOverrideFromContext should return empty for clean ctx, got %q", got)
	}
}
