package tool

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/sandbox"
)

// wrapBackend 模拟真实后端:Transform 返回完整 argv(含 argv[0]=shellBin)。
type wrapBackend struct{}

func (f *wrapBackend) Name() string { return "fake" }
func (f *wrapBackend) Probe() error { return nil }
func (f *wrapBackend) Transform(shellBin string, args []string, cfg *sandbox.Config, workspace string) ([]string, error) {
	return append([]string{shellBin}, args...), nil
}

// TestRegression_ShellSandboxWrap_NoDuplicateArgv0 回归防护:
// setupCommand 的沙箱包装必须用 wrapped[1:](wrapped 含 argv[0]),
// 否则 exec.Command(shellBin, args...) 重复 argv[0]:
// sandbox-exec 收到 [sandbox-exec, sandbox-exec, -p, ...] → usage 错误,
// 两个平台全部命令失败。
func TestRegression_ShellSandboxWrap_NoDuplicateArgv0(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows 无沙箱后端(windowsStubBackend 恒不可用),且真实 bash 执行
		// 在 Windows 上未被既有测试覆盖(见 shell_test.go 的 18 处 windows skip);
		// 沙箱包装路径在 Windows 无实际使用场景。
		t.Skip("sandbox wrapping is Unix-only (windows stub never activates)")
	}
	mgr := sandbox.NewManager(sandbox.DefaultConfig(), "/tmp")
	mgr.SetBackend(&wrapBackend{})
	s := &Shell{SandboxMgr: mgr}
	ctx := sandbox.WithSandboxStatus(context.Background(), sandbox.SandboxStatus{Active: true})

	result, err := s.Execute(ctx, ShellParams{Command: "echo WRAP_OK"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(result.Content, "WRAP_OK") {
		t.Errorf("wrapped command failed (argv[0] duplication?): %s", result.Content)
	}
}

// TestRegression_ShellSandboxWrap_NonSandboxedUnchanged 对照组:
// 未注入沙箱状态 → 裸命令正常执行(行为不变)。
func TestRegression_ShellSandboxWrap_NonSandboxedUnchanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sandbox wrapping is Unix-only (windows stub never activates)")
	}
	mgr := sandbox.NewManager(sandbox.DefaultConfig(), "/tmp")
	mgr.SetBackend(&wrapBackend{})
	s := &Shell{SandboxMgr: mgr}

	result, err := s.Execute(context.Background(), ShellParams{Command: "echo BARE_OK"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(result.Content, "BARE_OK") {
		t.Errorf("bare command failed: %s", result.Content)
	}
}
