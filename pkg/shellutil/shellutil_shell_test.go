package shellutil

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// resetShellCache 重置 ShellInterpreter 的进程级缓存,使每个测试分支都能独立验证。
func resetShellCache() {
	cachedShell.once = sync.Once{}
	cachedShell.bin = ""
	cachedShell.args = nil
}

func TestShellInterpreter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 分支依赖真实 Git Bash 环境,由 Windows CI 覆盖")
	}

	t.Run("bash 可用时解析到 bash", func(t *testing.T) {
		if _, err := exec.LookPath("bash"); err != nil {
			t.Skip("当前环境无 bash")
		}
		resetShellCache()
		bin, args := ShellInterpreter()
		if bin != "bash" || len(args) != 1 || args[0] != "-c" {
			t.Errorf("ShellInterpreter() = (%q, %v), want (\"bash\", [\"-c\"])", bin, args)
		}
	})

	t.Run("PATH 无 bash 时回退 sh", func(t *testing.T) {
		resetShellCache()
		t.Setenv("PATH", t.TempDir())
		bin, args := ShellInterpreter()
		if bin != "sh" || len(args) != 1 || args[0] != "-c" {
			t.Errorf("ShellInterpreter() = (%q, %v), want (\"sh\", [\"-c\"])", bin, args)
		}
	})
}

func TestResolveWindowsShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 平台 PATH 语义不同且常见安装路径真实存在,由 Windows CI 覆盖")
	}
	// 屏蔽 warn 日志输出,保持测试输出干净
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(oldDefault)

	writeExec := func(t *testing.T, path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("PATH 中 bash.exe 优先", func(t *testing.T) {
		dir := t.TempDir()
		exe := filepath.Join(dir, "bash.exe")
		writeExec(t, exe)
		t.Setenv("PATH", dir)
		bin, args := resolveWindowsShell()
		if bin != exe || len(args) != 1 || args[0] != "-c" {
			t.Errorf("resolveWindowsShell() = (%q, %v), want (%q, [\"-c\"])", bin, args, exe)
		}
	})

	t.Run("PATH 中无 bash.exe 但有 bash", func(t *testing.T) {
		dir := t.TempDir()
		bash := filepath.Join(dir, "bash")
		writeExec(t, bash)
		t.Setenv("PATH", dir)
		bin, args := resolveWindowsShell()
		if bin != bash || len(args) != 1 || args[0] != "-c" {
			t.Errorf("resolveWindowsShell() = (%q, %v), want (%q, [\"-c\"])", bin, args, bash)
		}
	})

	t.Run("WAVELOOM_GIT_BASH_PATH 指向存在的文件", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "bash.exe")
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", t.TempDir())
		t.Setenv("WAVELOOM_GIT_BASH_PATH", f)
		bin, args := resolveWindowsShell()
		if bin != f || len(args) != 1 || args[0] != "-c" {
			t.Errorf("resolveWindowsShell() = (%q, %v), want (%q, [\"-c\"])", bin, args, f)
		}
	})

	t.Run("WAVELOOM_GIT_BASH_PATH 无效时继续探测并失败", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		t.Setenv("WAVELOOM_GIT_BASH_PATH", filepath.Join(t.TempDir(), "missing.exe"))
		bin, args := resolveWindowsShell()
		if bin != "" || args != nil {
			t.Errorf("resolveWindowsShell() = (%q, %v), want (\"\", nil)", bin, args)
		}
	})

	t.Run("从 git.exe 路径推算 bash.exe", func(t *testing.T) {
		root := t.TempDir()
		// git.exe 在 <root>/<level>/cmd/ 时,源码推算 <root>/bin/bash.exe
		// (Join(gitDir, "..", "..", "bin") 上提两级),fixture 需匹配该层级
		levelDir := filepath.Join(root, "level")
		cmdDir := filepath.Join(levelDir, "cmd")
		if err := os.MkdirAll(cmdDir, 0o755); err != nil {
			t.Fatal(err)
		}
		gitExe := filepath.Join(cmdDir, "git")
		writeExec(t, gitExe)
		binDir := filepath.Join(root, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(binDir, "bash.exe")
		if err := os.WriteFile(want, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", cmdDir)
		bin, args := resolveWindowsShell()
		if bin != want || len(args) != 1 || args[0] != "-c" {
			t.Errorf("resolveWindowsShell() = (%q, %v), want (%q, [\"-c\"])", bin, args, want)
		}
	})

	t.Run("全部失败返回空", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		t.Setenv("WAVELOOM_GIT_BASH_PATH", "")
		bin, args := resolveWindowsShell()
		if bin != "" || args != nil {
			t.Errorf("resolveWindowsShell() = (%q, %v), want (\"\", nil)", bin, args)
		}
	})
}
