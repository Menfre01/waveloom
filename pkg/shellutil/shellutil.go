// Package shellutil 提供 shell 命令处理相关的共享实用函数，
// 供 pkg/tool 和 pkg/skill 等包共同使用，避免循环依赖。
package shellutil

import (
	"fmt"
	"log/slog"
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
	if strings.HasSuffix(trimmed, "&") && !strings.HasSuffix(trimmed, "&&") {
		return true
	}
	for _, line := range strings.Split(cmd, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasSuffix(t, "&") && !strings.HasSuffix(t, "&&") {
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

// isWSLBashLauncher 判断路径是否为 WSL 的 bash 启动器。
//
// Windows 10+ 自带 %SystemRoot%\System32\bash.exe,它是 WSL 入口而非 Git Bash:
// 未安装 WSL 发行版时执行任何命令都返回 exit 1 且无输出。GitHub Actions 的
// windows-latest 镜像正是这种状态 —— 裸 LookPath("bash.exe") 会命中它,
// 导致所有 shell 执行静默失败。
func isWSLBashLauncher(path string) bool {
	if path == "" {
		return false
	}
	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	launcher := filepath.Clean(filepath.Join(sysRoot, "System32", "bash.exe"))
	return strings.EqualFold(filepath.Clean(path), launcher)
}

// findBashNearGit 从 git.exe 所在目录向上探测 Git Bash 的 bash.exe。
//
// Git for Windows 有两种布局,git.exe 位置不同:
//   - C:\Program Files\Git\cmd\git.exe  -> bash 在 ..\bin\bash.exe
//   - C:\Program Files\Git\bin\git.exe  -> bash 在同级 bash.exe
//
// REGRESSION: 原实现固定按 `..\..\bin\bash.exe` 推算,遇到 bin 布局会算到
// C:\Program Files\bin\bash.exe(不存在);GitHub windows-latest 正是 bin 布局,
// 推算失败后只能靠 PATH 兜底。现改为向上最多 3 层、每层试同级与 bin 子目录。
func findBashNearGit(gitPath string) string {
	dir := filepath.Dir(gitPath)
	for depth := 0; depth < 3; depth++ {
		for _, candidate := range []string{
			filepath.Join(dir, "bash.exe"),
			filepath.Join(dir, "bin", "bash.exe"),
		} {
			if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
				return candidate
			}
		}
		dir = filepath.Dir(dir)
	}
	return ""
}

// resolveWindowsShell 在 Windows 上定位 Git Bash 的 bash.exe。
// 探测顺序:
//  1. WAVELOOM_GIT_BASH_PATH 环境变量(用户显式指定,最高优先)
//  2. 从 git.exe 路径推算 ../../bin/bash.exe(最可靠的自动探测)
//  3. PATH 中的 bash / bash.exe,排除 WSL 启动器
//  4. 常见安装路径(C:\Program Files\Git\bin\bash.exe)
//  5. 都找不到 → 返回空字符串,由调用方报错
//
// REGRESSION: 原实现把裸 PATH 查找放第一位。GitHub windows-latest 的 PATH 里有
// %SystemRoot%\System32\bash.exe(WSL 启动器),被优先选中后 skill 动态注入
// `echo a done` 返回 "exit status 1" 且无输出,TestRegression_AllowedToolsScalar
// StringFeedsWhitelist 在 Windows CI 上失败。Git 推算优先 + 显式排除 WSL 启动器修复。
func resolveWindowsShell() (string, []string) {
	// 1. 环境变量(显式指定优先于一切自动探测)
	if envPath := os.Getenv("WAVELOOM_GIT_BASH_PATH"); envPath != "" {
		if fi, err := os.Stat(envPath); err == nil && !fi.IsDir() {
			return envPath, []string{"-c"}
		}
		slog.Warn("WAVELOOM_GIT_BASH_PATH set but path does not exist, falling back to auto-detection", "path", envPath)
	}

	// 2. 从 git.exe 推算(cmd 与 bin 两种布局都覆盖)
	if gitPath, err := exec.LookPath("git"); err == nil {
		if bashPath := findBashNearGit(gitPath); bashPath != "" {
			return bashPath, []string{"-c"}
		}
	}

	// 3. PATH 查找,跳过 WSL 启动器
	for _, name := range []string{"bash.exe", "bash"} {
		if bashPath, err := exec.LookPath(name); err == nil && !isWSLBashLauncher(bashPath) {
			return bashPath, []string{"-c"}
		}
	}

	// 4. 常见安装路径
	commonPaths := []string{
		`C:\Program Files\Git\bin\bash.exe`,
	}
	for _, p := range commonPaths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, []string{"-c"}
		}
	}

	// 5. 找不到 → 返回空,由 ShellInterpreter 调用方处理
	return "", nil
}
