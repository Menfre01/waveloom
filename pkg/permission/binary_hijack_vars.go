// Package permission — BINARY_HIJACK_VARS 环境变量剥离（
//
// 在权限规则匹配前剥离可用于劫持二进制行为的环境变量，// 防止通过 LD_PRELOAD / DYLD_INSERT_LIBRARIES 等绕过命令白名单。
package permission

import (
	"strings"
)

// ============================================================================
// BINARY_HIJACK_VARS — 可劫持二进制行为的危险环境变量
// ============================================================================

// binaryHijackVars 是硬拦截的危险环境变量集合(命中直接 DENY)。
// 这些变量可在命令执行前注入共享库或修改运行时行为,用于绕过权限白名单。
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

}

// buildArgVars 是构建/编译参数变量:合法场景常见(如 CFLAGS=-O2 make build),
// 仅从检查命令中剥离(避免被用于改写构建行为绕过白名单),不硬拦截。
// 二审(五审 M2):硬拦截误伤合法用法且无逃生通道(Step 4 allow 规则在
// Step 3 DENY 后不可达,TUI 对 DENY 无交互覆盖)。
var buildArgVars = map[string]bool{
	// 通用编译器环境
	"CFLAGS":    true,
	"CXXFLAGS":  true,
	"LDFLAGS":   true,
	"CPPFLAGS":  true,
	// 条件编译 / Make
	"MAKEFLAGS": true,
	"MAKELEVEL": true,
	"MFLAGS":    true,
}

// ============================================================================
// StripBinaryHijackVars — 剥离危险环境变量
// ============================================================================

// StripBinaryHijackVars 从命令字符串中剥离危险的 BINARY_HIJACK_VARS 赋值。
//
// "LD_PRELOAD=/tmp/evil.so FOO=bar git status" → "FOO=bar git status"
//
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
		if binaryHijackVars[varName] || buildArgVars[varName] {
			// 危险变量 → 剥离(不写入 result)
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

// HasBinaryHijackVars 报告命令是否以危险劫持变量赋值开头
// (LD_PRELOAD=... / DYLD_INSERT_LIBRARIES=... 等),含 `env VAR=x cmd` 前缀形式。
// 命中必须 DENY(硬拦截,不受二元决策影响):剥离后检查、原样执行会造成
// "检查洗白、执行被劫持"——ASK 弹窗是旧的人工兜底,二元决策下已无兜底
// (五审 High-5;二审 M1 提升到 Check() 顶部防 ask 规则短路)。
// 复合命令按分隔符(; && || | 换行)分段后逐段检查(终审 W1:bash.Audit
// 无劫持变量检测器,原注释"由 parser differential 覆盖"失实);引号感知
// 拆分,引号内分隔符不切分。
func HasBinaryHijackVars(cmd string) bool {
	for _, seg := range splitCommandSegments(cmd) {
		if hasHijackPrefix(seg) {
			return true
		}
	}
	return false
}

// splitCommandSegments 按 shell 命令分隔符(; && || | 换行)拆分命令,
// 引号内的分隔符不切分。空段过滤。
func splitCommandSegments(cmd string) []string {
	var segs []string
	var cur strings.Builder
	inSQ, inDQ, escaped := false, false, false
	for _, ch := range cmd {
		if escaped {
			escaped = false
			cur.WriteRune(ch)
			continue
		}
		if ch == '\\' && !inSQ {
			escaped = true
			cur.WriteRune(ch)
			continue
		}
		if ch == '\'' && !inDQ {
			inSQ = !inSQ
			cur.WriteRune(ch)
			continue
		}
		if ch == '"' && !inSQ {
			inDQ = !inDQ
			cur.WriteRune(ch)
			continue
		}
		if !inSQ && !inDQ && (ch == ';' || ch == '&' || ch == '|' || ch == '\n') {
			if s := strings.TrimSpace(cur.String()); s != "" {
				segs = append(segs, s)
			}
			cur.Reset()
			continue
		}
		cur.WriteRune(ch)
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		segs = append(segs, s)
	}
	return segs
}

// hasHijackPrefix 检查单个命令段是否以危险 env 赋值开头
// (VAR=x ... 或 env [options] VAR=x ... 形式)。
func hasHijackPrefix(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	remaining := trimmed

	for {
		remaining = strings.TrimLeft(remaining, " \t")
		if remaining == "" {
			return false
		}

		// env 前缀形式:`env LD_PRELOAD=x cmd`(显式环境注入,常见攻击形式)
		if strings.HasPrefix(remaining, "env ") || remaining == "env" {
			remaining = strings.TrimSpace(remaining[3:])
			// 跳过 env 选项(-i / -u / --ignore-environment 等)
			for strings.HasPrefix(remaining, "-") {
				sp := strings.IndexByte(remaining, ' ')
				if sp < 0 {
					return false // 只有选项没有命令
				}
				remaining = strings.TrimLeft(remaining[sp:], " \t")
			}
			continue
		}

		eqIdx := strings.IndexByte(remaining, '=')
		if eqIdx < 0 {
			// 没有更多赋值 → 剩余部分是命令本身
			return false
		}

		varName := remaining[:eqIdx]
		if !isValidEnvVarName(varName) || strings.ContainsAny(varName, " \t") {
			return false
		}
		if binaryHijackVars[varName] {
			return true
		}

		// 跳过该赋值的值部分,继续检查下一个赋值
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
		remaining = remaining[valEnd:]
	}
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
