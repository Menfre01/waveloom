package tool

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/sandbox"
)

// testSandboxMgr 构造带 fake 后端的可用沙箱管理器(escapeHatchHint 测试用)。
func testSandboxMgr(t *testing.T, allowUnsandboxed bool) *sandbox.SandboxManager {
	t.Helper()
	cfg := sandbox.DefaultConfig()
	allow := allowUnsandboxed
	cfg.AllowUnsandboxedCommands = &allow
	mgr := sandbox.NewManager(cfg, t.TempDir())
	mgr.SetBackend(&fakeSandboxBackend{})
	return mgr
}

// fakeSandboxBackend 实现 sandbox.Backend(tool 包测试用)。
type fakeSandboxBackend struct{}

func (f *fakeSandboxBackend) Name() string { return "fake" }
func (f *fakeSandboxBackend) Probe() error { return nil }
func (f *fakeSandboxBackend) Transform(shellBin string, args []string, cfg *sandbox.Config, workspace string) ([]string, error) {
	return append([]string{"bwrap"}, args...), nil
}

func sandboxedTestCtx() context.Context {
	return sandbox.WithSandboxStatus(context.Background(), sandbox.SandboxStatus{Active: true})
}

func TestEscapeHatchHint_AllowedAndSandboxed(t *testing.T) {
	s := &Shell{SandboxMgr: testSandboxMgr(t, true)}
	out := s.escapeHatchHint(sandboxedTestCtx(), errors.New("exit status 1"), "base output")
	if out != "base output\n[sandbox] command failed inside sandbox — retry by adding it to sandbox.excludedCommands, or use a different approach" {
		t.Errorf("unexpected hint output: %q", out)
	}
}

func TestEscapeHatchHint_SuccessNoHint(t *testing.T) {
	s := &Shell{SandboxMgr: testSandboxMgr(t, true)}
	if out := s.escapeHatchHint(sandboxedTestCtx(), nil, "ok"); out != "ok" {
		t.Errorf("success should not add hint: %q", out)
	}
}

func TestEscapeHatchHint_NotAllowed(t *testing.T) {
	s := &Shell{SandboxMgr: testSandboxMgr(t, false)}
	out := s.escapeHatchHint(sandboxedTestCtx(), errors.New("boom"), "base")
	if out != "base" {
		t.Errorf("allowUnsandboxed=false should not add hint: %q", out)
	}
}

func TestEscapeHatchHint_NotSandboxed(t *testing.T) {
	s := &Shell{SandboxMgr: testSandboxMgr(t, true)}
	out := s.escapeHatchHint(context.Background(), errors.New("boom"), "base")
	if out != "base" {
		t.Errorf("non-sandboxed command should not add hint: %q", out)
	}
}

func TestEscapeHatchHint_NoManager(t *testing.T) {
	s := &Shell{}
	out := s.escapeHatchHint(sandboxedTestCtx(), errors.New("boom"), "base")
	if out != "base" {
		t.Errorf("no manager should not add hint: %q", out)
	}
}

// TestEscapeHatchHint_SemanticExitNoHint 五审 M3:普通退出码错误
// (ExitError,如 grep 无匹配)不提示——避免每次失败都噪。
func TestEscapeHatchHint_SemanticExitNoHint(t *testing.T) {
	s := &Shell{SandboxMgr: testSandboxMgr(t, true)}
	out := s.escapeHatchHint(sandboxedTestCtx(), &exec.ExitError{}, "grep: no match")
	if out != "grep: no match" {
		t.Errorf("semantic exit error should not add hint: %q", out)
	}
}

// TestEscapeHatchHint_ViolationStillHints 违规注解存在时仍提示
// (沙箱原因,即使命令退出码非 0)。
func TestEscapeHatchHint_ViolationStillHints(t *testing.T) {
	s := &Shell{SandboxMgr: testSandboxMgr(t, true)}
	content := "touch: /etc/x: Operation not permitted\n<sandbox_violations>\nwrite blocked: /etc/x (read-only filesystem)\n</sandbox_violations>"
	out := s.escapeHatchHint(sandboxedTestCtx(), &exec.ExitError{}, content)
	if !strings.Contains(out, "[sandbox]") {
		t.Errorf("violation should still hint: %q", out)
	}
}
