package harness

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/tool"
)

// makeFakeVenv 创建带 activate 脚本的假 venv(activate 设置 TESTBED_ACTIVE=1)。
func makeFakeVenv(t *testing.T) string {
	t.Helper()
	venv := t.TempDir()
	bin := filepath.Join(venv, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// 假 activate:设置环境变量标记激活
	activate := `#!/bin/sh
export TESTBED_ACTIVE=1
export VIRTUAL_ENV="` + venv + `"
`
	if err := os.WriteFile(filepath.Join(bin, "activate"), []byte(activate), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "python"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(bin, "python")
}

func TestTestbedShell_ExecuteInjectsActivate(t *testing.T) {
	// testbed 激活注入依赖 bash/sh 执行语义,Windows 无对应环境。
	if runtime.GOOS == "windows" {
		t.Skip("评测基建(testbed shell)仅支持 Linux/macOS")
	}
	venvPython := makeFakeVenv(t)
	inner := &tool.Shell{AllowBg: true}
	ts := NewTestbedShell(inner, venvPython)

	// 激活脚本路径由 venv python 推导
	wantActivate := filepath.Join(filepath.Dir(venvPython), "activate")
	if ts.activate != wantActivate {
		t.Fatalf("activate=%s, want %s", ts.activate, wantActivate)
	}

	// 执行命令应命中注入前缀(source activate && cmd)
	// 通过环境变量确认激活真实生效
	res, err := ts.Execute(context.Background(), tool.ShellParams{
		Command: "echo testbed_active=$TESTBED_ACTIVE venv=$VIRTUAL_ENV",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "testbed_active=1") {
		t.Fatalf("activate not injected, got: %q", res.Content)
	}
	if !strings.Contains(res.Content, "venv=") {
		t.Fatalf("venv not set, got: %q", res.Content)
	}
}

func TestTestbedShell_NoActivateWhenEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("评测基建(testbed shell)仅支持 Linux/macOS")
	}
	// registerEvalTools 空 testbedPython 时注册普通 Shell(不注入)
	r := tool.NewRegistry()
	registerEvalTools(r, nil, "")
	sh, ok := r.Get("bash")
	if !ok {
		t.Fatal("bash not registered")
	}
	// 普通 Shell 直接执行,无前缀
	res, err := sh.Execute(context.Background(), []byte(`{"command":"echo env=$TESTBED_ACTIVE"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Content, "1") {
		t.Fatalf("unexpected testbed injection, got: %q", res.Content)
	}
}

func TestTestbedShell_RegisterWithTestbed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("评测基建(testbed shell)仅支持 Linux/macOS")
	}
	venvPython := makeFakeVenv(t)
	r := tool.NewRegistry()
	registerEvalTools(r, nil, venvPython)
	sh, ok := r.Get("bash")
	if !ok {
		t.Fatal("bash not registered")
	}
	res, err := sh.Execute(context.Background(), []byte(`{"command":"echo active=$TESTBED_ACTIVE"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "active=1") {
		t.Fatalf("testbed injection missing via registry, got: %q", res.Content)
	}
}

// TestTestbedShell_ExecuteStreaming 流式路径同样注入激活前缀。
func TestTestbedShell_ExecuteStreaming(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("评测基建(testbed shell)仅支持 Linux/macOS")
	}
	venvPython := makeFakeVenv(t)
	inner := &tool.Shell{AllowBg: true}
	ts := NewTestbedShell(inner, venvPython)

	var chunks []string
	res, err := ts.ExecuteStreaming(context.Background(), tool.ShellParams{
		Command: "echo streaming_active=$TESTBED_ACTIVE",
	}, func(chunk string) { chunks = append(chunks, chunk) })
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(chunks, "") + res.Content
	if !strings.Contains(all, "streaming_active=1") {
		t.Fatalf("streaming activate not injected, got: %q", all)
	}
}
