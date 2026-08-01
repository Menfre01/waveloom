package sandbox

import (
	"context"
	"testing"
)

func TestSandboxStatusContextRoundTrip(t *testing.T) {
	ctx := WithSandboxStatus(context.Background(), SandboxStatus{Active: true, Reason: "bypass"})
	s := SandboxStatusFrom(ctx)
	if !s.Active {
		t.Error("Active = false, want true")
	}
	if s.Reason != "bypass" {
		t.Errorf("Reason = %q, want %q", s.Reason, "bypass")
	}
}

func TestSandboxStatusFrom_NoInjection(t *testing.T) {
	// 未注入 context → Active=false(安全默认,fail-closed)
	s := SandboxStatusFrom(context.Background())
	if s.Active {
		t.Error("Active = true, want false (default)")
	}
}

func TestSandboxStatusFrom_NilContext(t *testing.T) {
	// nil ctx 防御分支(SandboxStatusFrom 显式处理 nil → fail-closed)
	//nolint:staticcheck // 有意测试 nil context 的防御行为
	s := SandboxStatusFrom(nil)
	if s.Active {
		t.Error("Active = true, want false (nil ctx)")
	}
}

func TestSandboxStatusFrom_InactiveInjection(t *testing.T) {
	// 显式注入 inactive(如 excludedCommands 逃逸命令)→ Active=false
	ctx := WithSandboxStatus(context.Background(), SandboxStatus{Active: false, Reason: "excluded: docker *"})
	s := SandboxStatusFrom(ctx)
	if s.Active {
		t.Error("Active = true, want false")
	}
	if s.Reason != "excluded: docker *" {
		t.Errorf("Reason = %q, want %q", s.Reason, "excluded: docker *")
	}
}

func TestSandboxStatusContext_DoesNotLeak(t *testing.T) {
	// context 值不可跨 context 泄漏:子 context 派生后仍可读
	parent := WithSandboxStatus(context.Background(), SandboxStatus{Active: true})
	type testKey struct{}
	child := context.WithValue(parent, testKey{}, "v")
	if !SandboxStatusFrom(child).Active {
		t.Error("child ctx lost sandbox status")
	}
}
