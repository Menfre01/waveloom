package permission

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// 敏感/危险文件和目录定义
// ---------------------------------------------------------------------------

// sensitiveFiles 是敏感配置文件列表。
// 即使在工作目录内，操作这些文件也需要额外确认。
var sensitiveFiles = map[string]bool{
	// Shell / 环境配置
	".gitconfig":    true,
	".gitmodules":   true,
	".bashrc":       true,
	".bash_profile": true,
	".zshrc":        true,
	".zprofile":     true,
	".profile":      true,
	".ripgreprc":    true,
	// 工具配置
	".mcp.json":     true,
	".claude.json":  true,
	// 环境变量 / 密钥
	".env":          true,
	".env.local":    true,
	".env.development": true,
	".env.production":  true,
	".env.staging":  true,
	// 云服务凭证
	".npmrc":        true,
	".yarnrc":       true,
	".yarnrc.yml":   true,
	".netrc":        true,
	".terraformrc":  true,
	".pgpass":       true,
	".my.cnf":       true,
}

// sensitiveDirs 是敏感目录名列表。
// 路径中包含这些目录名时标记为 PathSensitive。
var sensitiveDirs = map[string]bool{
	".git":      true,
	".claude":   true,
	".waveloom": true,
	".vscode":   true,
	".idea":     true,
	// 云服务凭证目录
	".aws":      true,
	".docker":   true,
	".kube":     true,
	".gnupg":    true,
	// SSH 密钥
	".ssh":      true,
	// Terraform / 基础设施
	".terraform": true,
	// 通用密钥目录
	"credentials": true,
	"secrets":     true,
	"tokens":      true,
}

// dangerousDirPrefixes 是危险目录前缀列表。
// 即使在工作目录内，路径以此开头也标记为 PathDangerous。
var dangerousDirPrefixes = []string{
	filepath.Join(string(filepath.Separator), "etc") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "System") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "boot") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "dev") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "proc") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "sys") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "bin") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "sbin") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "usr", "bin") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "usr", "sbin") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "Library") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "root") + string(filepath.Separator),
	// Windows 系统目录（Unix 上永不匹配，无影响）
	filepath.Join(string(filepath.Separator), "Windows", "System32") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "Windows", "SysWOW64") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "Program Files") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), "Program Files (x86)") + string(filepath.Separator),
}

// dangerousFilePrefixes 是危险文件路径前缀。
var dangerousFilePrefixes = []string{
	filepath.Join(string(filepath.Separator), ".ssh") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), ".gnupg") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), ".aws") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), ".docker") + string(filepath.Separator),
	filepath.Join(string(filepath.Separator), ".kube") + string(filepath.Separator),
}

// ---------------------------------------------------------------------------
// PathCheckResult — 路径安全检查结果
// ---------------------------------------------------------------------------

// PathCheckResult 包含路径安全检查的结果。
type PathCheckResult struct {
	Level          PathSafetyLevel
	Message        string
	ClassifierSafe bool // 未来 AI 分类器可自动批准
}

// ---------------------------------------------------------------------------
// PathSafetyCheck — 路径安全检查入口
// ---------------------------------------------------------------------------

// PathSafetyCheck 检查路径安全性。
// workingDirs 是允许的工作目录列表（通常为项目根目录）。
func PathSafetyCheck(path string, workingDirs []string) PathCheckResult {
	// 1. 路径标准化（复用 pathutil.ResolvePath）
	resolved, err := pathutil.ResolvePath(path)
	if err != nil {
		return PathCheckResult{
			Level:          PathDangerous,
			Message:        fmt.Sprintf("cannot resolve path: %v", err),
			ClassifierSafe: false,
		}
	}

	// 解析符号链接
	// 注意：macOS 上 /var 是 /private/var 的符号链接，必须对 path 和 workingDir
	// 都做 EvalSymlinks 才能正确计算相对关系。
	evalPath, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		// 文件不存在时 EvalSymlinks 会失败，但工作目录存在
		// 此时对路径的已存在部分做 EvalSymlinks
		evalPath = evalExistingPrefix(resolved)
	}

	// 2. 危险目录检查（无论是否在工作目录内）
	// Windows 上需先剥离盘符（如 C:），否则 prefix 匹配会因盘符前缀而漏检。
	checkPath := evalPath
	if runtime.GOOS == "windows" {
		if vol := filepath.VolumeName(evalPath); vol != "" {
			checkPath = evalPath[len(vol):]
		}
	}
	for _, prefix := range dangerousDirPrefixes {
		if strings.HasPrefix(checkPath, prefix) {
			return PathCheckResult{
				Level:          PathDangerous,
				Message:        "dangerous system path: " + prefix,
				ClassifierSafe: false,
			}
		}
	}

	// 3. 危险文件前缀检查
	for _, prefix := range dangerousFilePrefixes {
		if strings.HasPrefix(checkPath, prefix) || strings.Contains(checkPath, prefix) {
			return PathCheckResult{
				Level:          PathDangerous,
				Message:        "dangerous path: .ssh directory",
				ClassifierSafe: false,
			}
		}
	}

	// 4. 工作目录检查
	withinWorkDir := false
	for _, dir := range workingDirs {
		resolvedDir, err := pathutil.ResolvePath(dir)
		if err != nil {
			continue
		}
		// 解析工作目录的符号链接
		evalDir, err := filepath.EvalSymlinks(resolvedDir)
		if err != nil {
			evalDir = resolvedDir
		}
		if isWithinDir(evalPath, evalDir) {
			withinWorkDir = true
			break
		}
	}

	if !withinWorkDir {
		return PathCheckResult{
			Level:          PathDangerous,
			Message:        "path outside working directory",
			ClassifierSafe: false,
		}
	}

	// 5. 敏感文件检查（在工作目录内）
	base := filepath.Base(evalPath)
	if sensitiveFiles[base] {
		return PathCheckResult{
			Level:          PathSensitive,
			Message:        fmt.Sprintf("sensitive config file: %s", base),
			ClassifierSafe: true,
		}
	}

	// 6. 敏感目录检查（在工作目录内）
	for _, part := range splitPathParts(evalPath) {
		if sensitiveDirs[part] {
			return PathCheckResult{
				Level:          PathSensitive,
				Message:        fmt.Sprintf("sensitive directory: %s", part),
				ClassifierSafe: true,
			}
		}
	}

	// 7. 在工作目录内且非敏感
	return PathCheckResult{
		Level:          PathSafe,
		Message:        "within working directory",
		ClassifierSafe: true,
	}
}


// IsSensitiveFile 检查文件路径是否指向已知敏感配置文件（如 .env、.gitconfig 等）。
// 仅基于文件名进行匹配，与路径位置无关（workspace 内外均适用）。
func IsSensitiveFile(path string) bool {
	base := filepath.Base(path)
	return sensitiveFiles[base]
}
// ---------------------------------------------------------------------------
// PathSafetyDecision — 路径安全 + 操作类型 → 权限决策
// ---------------------------------------------------------------------------

// PathSafetyDecision 根据路径安全检查结果和操作类型计算权限决策。
//
// 决策矩阵：
//
//	路径等级    | read     | write
//	safe       | allow    | ask (默认需确认)
//	sensitive  | ask      | ask
//	dangerous  | ask      | deny
func PathSafetyDecision(pathCheck PathCheckResult, isWriteOp bool) DecisionResult {
	switch pathCheck.Level {
	case PathSafe:
		if isWriteOp {
			return DecisionResult{
				Decision: DecisionAsk,
				Reason:   ReasonDefault,
				Message:  "write operation requires confirmation",
			}
		}
		return DecisionResult{
			Decision: DecisionAllow,
			Reason:   ReasonDefault,
			Message:  "read within working directory",
		}

	case PathSensitive:
		return DecisionResult{
			Decision: DecisionAsk,
			Reason:   ReasonSafety,
			Message:  pathCheck.Message,
		}

	case PathDangerous:
		if isWriteOp {
			return DecisionResult{
				Decision: DecisionDeny,
				Reason:   ReasonSafety,
				Message:  pathCheck.Message,
			}
		}
		return DecisionResult{
			Decision: DecisionAsk,
			Reason:   ReasonSafety,
			Message:  pathCheck.Message,
		}
	}

	return DecisionResult{
		Decision: DecisionDeny,
		Reason:   ReasonDefault,
		Message:  "unknown path safety level",
	}
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// splitPathParts 将路径拆分为各个目录名部分。
// 例如 "/home/user/project/.git/refs" → ["home", "user", "project", ".git", "refs"]
func splitPathParts(path string) []string {
	var parts []string
	sep := string(filepath.Separator)
	for {
		dir, file := filepath.Split(path)
		if file != "" {
			parts = append(parts, file)
		}
		if dir == "" || dir == sep {
			break
		}
		// 去掉尾部分隔符继续。Windows 上 C: 没有分隔符，
		// TrimRight 无法去除任何字符，需要检测是否到达根。
		path = strings.TrimRight(dir, sep)
		if path == dir {
			// 根目录（如 Windows C: 或 \\server\share），无法继续分解
			break
		}
	}
	// 反转顺序（从根到叶）
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return parts
}

// isWithinDir 检查 path 是否在 dir 目录内。
// 与 tool.IsWithinDir 类似，但接受已解析的绝对路径，避免重复解析。
func isWithinDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	// rel 为 "." 表示 path == dir，不算"在目录内"
	// rel 以 ".." 开头表示在目录外
	return rel != "." && !strings.HasPrefix(rel, "..") && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// evalExistingPrefix 对路径中已存在的部分做 EvalSymlinks，
// 不存在的后缀保持不变。这对于 macOS 上 /var → /private/var 尤为重要。
func evalExistingPrefix(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	evalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return path // 无法解析，返回原路径
	}
	return filepath.Join(evalDir, base)
}
