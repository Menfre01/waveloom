package agentloop

import (
	"context"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/sandbox"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// fakeSandboxBackend 实现 sandbox.Backend,供 agentloop 测试注入。
type fakeSandboxBackend struct{}

func (f *fakeSandboxBackend) Name() string { return "fake" }
func (f *fakeSandboxBackend) Probe() error { return nil }
func (f *fakeSandboxBackend) Transform(shellBin string, args []string, cfg *sandbox.Config, workspace string) ([]string, error) {
	return append([]string{"bwrap"}, args...), nil
}

// newTestLoopWithSandbox 构造带沙箱管理器的 Loop(不可用 → 返回 nil mgr)。
func newTestLoopWithSandbox(mgr *sandbox.SandboxManager) *Loop {
	registry := tool.NewRegistry()
	loop := New(&mockLLMClient{}, registry, Config{
		SandboxMgr: mgr,
	})
	return loop
}

func TestWithSandboxStatus_NoManager(t *testing.T) {
	l := newTestLoopWithSandbox(nil)
	ctx := l.withSandboxStatus(context.Background(), llm.ToolCall{Name: "bash", Arguments: `{"command": "ls"}`})
	s := sandbox.SandboxStatusFrom(ctx)
	if s.Active {
		t.Error("Active = true, want false (no manager)")
	}
	if s.Reason != "sandbox unavailable" {
		t.Errorf("Reason = %q, want %q", s.Reason, "sandbox unavailable")
	}
}

func TestWithSandboxStatus_ManagerNotSelected(t *testing.T) {
	// manager 未 Select → Available()=false → inactive
	mgr := sandbox.NewManager(sandbox.DefaultConfig(), "/tmp")
	l := newTestLoopWithSandbox(mgr)
	ctx := l.withSandboxStatus(context.Background(), llm.ToolCall{Name: "bash", Arguments: `{"command": "ls"}`})
	if sandbox.SandboxStatusFrom(ctx).Active {
		t.Error("Active = true, want false (manager unavailable)")
	}
}

func TestWithSandboxStatus_ActiveBash(t *testing.T) {
	mgr := sandbox.NewManager(sandbox.DefaultConfig(), "/tmp")
	mgr.SetBackend(&fakeSandboxBackend{})
	l := newTestLoopWithSandbox(mgr)

	ctx := l.withSandboxStatus(context.Background(), llm.ToolCall{Name: "bash", Arguments: `{"command": "ls"}`})
	s := sandbox.SandboxStatusFrom(ctx)
	if !s.Active {
		t.Errorf("Active = false, want true (reason: %s)", s.Reason)
	}
}

func TestWithSandboxStatus_ExcludedCommand(t *testing.T) {
	cfg := sandbox.DefaultConfig()
	cfg.ExcludedCommands = []string{"docker *"}
	mgr := sandbox.NewManager(cfg, "/tmp")
	mgr.SetBackend(&fakeSandboxBackend{})
	l := newTestLoopWithSandbox(mgr)

	ctx := l.withSandboxStatus(context.Background(), llm.ToolCall{Name: "bash", Arguments: `{"command": "docker ps"}`})
	s := sandbox.SandboxStatusFrom(ctx)
	if s.Active {
		t.Error("Active = true, want false (excluded command)")
	}
	if s.Reason != "excluded command (escapes sandbox)" {
		t.Errorf("Reason = %q", s.Reason)
	}

	// 对照组:非逃逸命令 → active
	ctx2 := l.withSandboxStatus(context.Background(), llm.ToolCall{Name: "bash", Arguments: `{"command": "git status"}`})
	if !sandbox.SandboxStatusFrom(ctx2).Active {
		t.Error("git status should be sandboxed")
	}
}

func TestWithSandboxStatus_NonBashTool(t *testing.T) {
	mgr := sandbox.NewManager(sandbox.DefaultConfig(), "/tmp")
	mgr.SetBackend(&fakeSandboxBackend{})
	l := newTestLoopWithSandbox(mgr)

	// 非 bash 工具 → active=false(仅 bash 有 OS 级包装)
	ctx := l.withSandboxStatus(context.Background(), llm.ToolCall{Name: "write_file", Arguments: `{"file_path": "a.go"}`})
	if sandbox.SandboxStatusFrom(ctx).Active {
		t.Error("non-bash tool should not be sandboxed")
	}
}

func TestWithSandboxStatus_InvalidInput(t *testing.T) {
	mgr := sandbox.NewManager(sandbox.DefaultConfig(), "/tmp")
	mgr.SetBackend(&fakeSandboxBackend{})
	l := newTestLoopWithSandbox(mgr)

	// JSON 解析失败 → fail-closed:inactive + 原因说明
	ctx := l.withSandboxStatus(context.Background(), llm.ToolCall{Name: "bash", Arguments: `{bad json`})
	s := sandbox.SandboxStatusFrom(ctx)
	if s.Active {
		t.Error("Active = true, want false (invalid input)")
	}
	if s.Reason == "" {
		t.Error("Reason should explain the failure")
	}
}
