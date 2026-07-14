// Package pathutil 提供跨包共享的路径标准化和命令归一化工具函数。
//
// 这些函数原本分散在 pkg/tool/ 中，被 pkg/permission/ 等包反向依赖。
// 提取到独立包消除依赖倒置，使 permission 不再依赖 tool。
package pathutil

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// TempDir 返回规范化的临时目录路径。
// 与 os.TempDir() 不同，TempDir 通过 filepath.EvalSymlinks 解析符号链接，
// 确保返回的路径在不同上下文（Go 进程、Shell 子进程、session 持久化）中一致。
// macOS 上 /var 是 /private/var 的符号链接，不解析会导致路径不一致。
func TempDir() string {
	dir := os.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return filepath.Clean(dir)
}

// cdPattern 匹配 Shell 命令中的 "cd <path> &&" 或 "cd <path> ;" 前缀。
// 支持单引号、双引号和无引号路径。
//
//	例: cd /foo && bar       → dir=/foo, cmd=bar
//	    cd "/foo bar" && baz → dir=/foo bar, cmd=baz
//	    cd /foo; bar        → dir=/foo, cmd=bar
var cdPattern = regexp.MustCompile(`^cd\s+(?:"([^"]*)"|'([^']*)'|([^\s;&]+))\s*(?:&&|;)\s*(.*)$`)

// NormalizeShellCommand 剥离命令中的 cd 前缀，返回归一化后的命令和提取的工作目录。
// 若命令不以 cd 开头，extractedDir 返回空字符串，原始命令原样返回。
func NormalizeShellCommand(command string) (normalized string, extractedDir string) {
	matches := cdPattern.FindStringSubmatch(command)
	if matches == nil {
		return command, ""
	}
	// matches[1]: 双引号路径, matches[2]: 单引号路径, matches[3]: 无引号路径, matches[4]: 剩余命令
	dir := matches[1]
	if dir == "" {
		dir = matches[2]
	}
	if dir == "" {
		dir = matches[3]
	}
	return matches[4], dir
}

// ResolvePath 将路径解析为标准化绝对路径。
func ResolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}
	return filepath.Clean(abs), nil
}

// FindProjectRoot 从 cwd 向上查找包含 .git 的目录，返回绝对路径。
// 未找到时返回空字符串。
func FindProjectRoot(cwd string) string {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	for {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		if err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ResolvePathWithDir 将路径基于指定工作目录解析为标准化绝对路径。
// workingDir 为空时回退到 ResolvePath（基于进程 CWD）。
func ResolvePathWithDir(path, workingDir string) (string, error) {
	if workingDir == "" {
		return ResolvePath(path)
	}
	absDir, err := filepath.Abs(workingDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve working_dir: %w", err)
	}
	absDir = filepath.Clean(absDir)

	var absPath string
	if filepath.IsAbs(path) {
		absPath = path
	} else {
		absPath = filepath.Join(absDir, path)
	}
	return filepath.Clean(absPath), nil
}
