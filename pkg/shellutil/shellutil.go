// Package shellutil 提供 shell 命令处理相关的共享实用函数，
// 供 pkg/tool 和 pkg/skill 等包共同使用，避免循环依赖。
package shellutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// IsBackgroundCommand 检测命令是否包含后台执行标志（&）。
// 检查整个命令尾部以及每一行的尾部，处理多行命令中某一行以 & 结尾的场景。
// 不处理同行内部的 &（如 echo foo & echo bar）。
func IsBackgroundCommand(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	if strings.HasSuffix(trimmed, "&") {
		return true
	}
	for _, line := range strings.Split(cmd, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), "&") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Shell 解释器探测
// ---------------------------------------------------------------------------

// cachedShell 缓存 ShellInterpreter 的探测结果，启动时通过 sync.Once 初始化一次，
// 后续调用零 I/O 开销。
var cachedShell struct {
	once sync.Once
	bin  string
	args []string
}

// ShellInterpreter 返回当前平台的 shell 解释器及其参数。
// 结果在首次调用时缓存，后续调用直接返回缓存值。
func ShellInterpreter() (binary string, args []string) {
	cachedShell.once.Do(func() {
		if runtime.GOOS == "windows" {
			cachedShell.bin, cachedShell.args = resolveWindowsShell()
			if cachedShell.bin == "" {
				fmt.Fprintln(os.Stderr, "Waveloom on Windows requires Git for Windows (https://git-scm.com/downloads/win).")
				fmt.Fprintln(os.Stderr, "If already installed, set WAVELOOM_GIT_BASH_PATH to your bash.exe location.")
			}
			return
		}
		if _, err := exec.LookPath("bash"); err == nil {
			cachedShell.bin, cachedShell.args = "bash", []string{"-c"}
			return
		}
		cachedShell.bin, cachedShell.args = "sh", []string{"-c"}
	})
	return cachedShell.bin, cachedShell.args
}

// resolveWindowsShell 在 Windows 上定位 Git Bash 的 bash.exe。
// 探测顺序：
//  1. PATH 中的 bash / bash.exe（运行在 Git Bash 中时最可靠）
//  2. WAVELOOM_GIT_BASH_PATH 环境变量
//  3. 从 git.exe 路径推算 ../../bin/bash.exe
//  4. 常见安装路径（C:\Program Files\Git\bin\bash.exe）
//  5. 都找不到 → 返回空字符串，由调用方报错
func resolveWindowsShell() (string, []string) {
	// 0. 直接查找 PATH（用户在 Git Bash 中运行 waveloom 时 bash.exe 就在 PATH 中）
	if bashPath, err := exec.LookPath("bash.exe"); err == nil {
		return bashPath, []string{"-c"}
	}
	if bashPath, err := exec.LookPath("bash"); err == nil {
		return bashPath, []string{"-c"}
	}

	// 1. 环境变量
	if envPath := os.Getenv("WAVELOOM_GIT_BASH_PATH"); envPath != "" {
		if fi, err := os.Stat(envPath); err == nil && !fi.IsDir() {
			return envPath, []string{"-c"}
		}
		fmt.Fprintf(os.Stderr, "WAVELOOM_GIT_BASH_PATH is set but file not found: %s\n", envPath)
	}

	// 2. 从 git.exe 推算
	if gitPath, err := exec.LookPath("git"); err == nil {
		// gitPath 形如 C:\Program Files\Git\cmd\git.exe
		// bash 在 ..\..\bin\bash.exe
		gitDir := filepath.Dir(gitPath)
		bashPath := filepath.Join(gitDir, "..", "..", "bin", "bash.exe")
		if fi, err := os.Stat(bashPath); err == nil && !fi.IsDir() {
			return bashPath, []string{"-c"}
		}
	}

	// 3. 常见安装路径
	commonPaths := []string{
		`C:\Program Files\Git\bin\bash.exe`,
	}
	for _, p := range commonPaths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, []string{"-c"}
		}
	}

	// 4. 找不到 → 返回空，由 ShellInterpreter 调用方处理
	return "", nil
}
