// Package permission — BINARY_HIJACK_VARS 环境变量剥离（对标 Claude Code bashPermissions.ts）
//
// 在权限规则匹配前剥离可用于劫持二进制行为的环境变量，
// 防止通过 LD_PRELOAD / DYLD_INSERT_LIBRARIES 等绕过命令白名单。
package permission

import (
	"strings"
)

// ============================================================================
// BINARY_HIJACK_VARS — 可劫持二进制行为的危险环境变量
// ============================================================================

// binaryHijackVars 是对标 Claude Code BINARY_HIJACK_VARS 的危险环境变量集合。
// 这些变量可在命令执行前注入共享库或修改运行时行为，用于绕过权限白名单。
//
// 攻击示例:
//
//	LD_PRELOAD=/tmp/evil.so git status
//	→ 权限检查看到 baseCommand="git" → 匹配 Bash(git:*) → ALLOW
//	→ 实际执行：git 被 LD_PRELOAD 注入 → RCE
var binaryHijackVars = map[string]bool{
	// Linux/Unix 动态链接器劫持
	"LD_PRELOAD":          true,
	"LD_LIBRARY_PATH":     true,
	"LD_AUDIT":            true,
	"LD_ORIGIN_PATH":      true,
	"LD_DEBUG":            true,
	"LD_DEBUG_OUTPUT":     true,
	"LD_PROFILE":          true,
	"LD_PROFILE_OUTPUT":   true,
	"LD_USE_LOAD_BIAS":    true,
	"LD_DYNAMIC_WEAK":     true,
	"LD_SHOW_AUXV":        true,
	"LD_BIND_NOW":         true,
	"LD_BIND_NOT":         true,

	// macOS 动态链接器劫持
	"DYLD_INSERT_LIBRARIES": true,
	"DYLD_LIBRARY_PATH":     true,
	"DYLD_FRAMEWORK_PATH":   true,
	"DYLD_FALLBACK_LIBRARY_PATH": true,
	"DYLD_FALLBACK_FRAMEWORK_PATH": true,
	"DYLD_ROOT_PATH":        true,
	"DYLD_SHARED_REGION":    true,
	"DYLD_IMAGE_SUFFIX":     true,

	// 通用运行时劫持
	"GCONV_PATH":            true,
	"GLIBC_TUNABLES":        true,  // glibc 2.34+ malloc 等调优，可触发代码执行
	"LOCPATH":               true,
	"MALLOC_CHECK_":         true,
	"MALLOC_PERTURB_":       true,
	"MALLOC_TRACE":          true,

	// Python
	"PYTHONPATH":            true,
	"PYTHONHOME":            true,
	"PYTHONSTARTUP":         true,
	"PYTHONINSPECT":         true,

	// Ruby
	"RUBYLIB":               true,
	"RUBYOPT":               true,
	"RUBYPATH":              true,
	"GEM_PATH":              true,
	"GEM_HOME":              true,

	// Node.js
	"NODE_PATH":             true,
	"NODE_OPTIONS":          true,
	"NODE_DEBUG":            true,

	// Perl
	"PERL5LIB":              true,
	"PERLLIB":               true,
	"PERL5OPT":              true,

	// 通用编译器环境劫持
	"CFLAGS":                true,
	"CXXFLAGS":              true,
	"LDFLAGS":               true,
	"CPPFLAGS":              true,

	// 条件编译 / Make
	"MAKEFLAGS":             true,
	"MAKELEVEL":             true,
	"MFLAGS":                true,
}

// ============================================================================
// StripBinaryHijackVars — 剥离危险环境变量
// ============================================================================

// StripBinaryHijackVars 从命令字符串中剥离危险的 BINARY_HIJACK_VARS 赋值。
//
// "LD_PRELOAD=/tmp/evil.so FOO=bar git status" → "FOO=bar git status"
// 对标 Claude Code stripAllLeadingEnvVars。
func StripBinaryHijackVars(cmd string) string {
	if !strings.Contains(cmd, "=") {
		return cmd
	}

	trimmed := strings.TrimSpace(cmd)
	var result strings.Builder
	remaining := trimmed

	for {
		remaining = strings.TrimLeft(remaining, " \t")
		if remaining == "" {
			break
		}

		// 查找下一个 = 之前的内容（像 VAR=val 的形式）
		eqIdx := strings.IndexByte(remaining, '=')
		if eqIdx < 0 {
			// 没有更多赋值 → 剩余部分是命令本身
			if result.Len() > 0 {
				result.WriteByte(' ')
			}
			result.WriteString(remaining)
			break
		}

		// 提取 VAR 名（= 之前）
		varName := remaining[:eqIdx]

		// 检查是否为有效的环境变量名（[A-Za-z_][A-Za-z0-9_]*）
		if !isValidEnvVarName(varName) || strings.ContainsAny(varName, " \t") {
			// 不是有效 VAR 赋值 → 剩余部分是命令本身
			if result.Len() > 0 {
				result.WriteByte(' ')
			}
			result.WriteString(remaining)
			break
		}

		// 查找值的结束位置（下一个空格或行尾）
		valEnd := eqIdx + 1
		inSQ, inDQ, escaped := false, false, false
		for valEnd < len(remaining) {
			ch := remaining[valEnd]
			if escaped {
				escaped = false
				valEnd++
				continue
			}
			if ch == '\\' && !inSQ {
				escaped = true
				valEnd++
				continue
			}
			if ch == '\'' && !inDQ {
				inSQ = !inSQ
				valEnd++
				continue
			}
			if ch == '"' && !inSQ {
				inDQ = !inDQ
				valEnd++
				continue
			}
			if !inSQ && !inDQ && (ch == ' ' || ch == '\t') {
				break
			}
			valEnd++
		}

		assignment := remaining[:valEnd]

		// 决定是剥离还是保留
		if binaryHijackVars[varName] {
			// 危险变量 → 剥离（不写入 result）
		} else {
			// 安全变量 → 保留
			if result.Len() > 0 {
				result.WriteByte(' ')
			}
			result.WriteString(assignment)
		}

		remaining = remaining[valEnd:]
	}

	res := result.String()
	if res == "" {
		// 所有 env vars 都被剥离 → 无命令
		return ""
	}
	return res
}

func isValidEnvVarName(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !isAlpha(r) && r != '_' {
				return false
			}
		} else {
			if !isAlphaNum(r) && r != '_' {
				return false
			}
		}
	}
	return true
}

func isAlpha(r rune) bool  { return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') }
func isAlphaNum(r rune) bool { return isAlpha(r) || (r >= '0' && r <= '9') }
