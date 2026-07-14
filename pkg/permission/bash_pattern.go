package permission

import "strings"

// MatchBashPattern 检查 command 是否匹配 Bash 白名单模式。
//
// 支持以下通配符模式（按顺序检查）：
//   - "*" / ""             → 匹配任意命令
//   - "*xxx*"              → 包含匹配（包含 xxx）
//   - "*xxx"               → 后缀匹配（以 xxx 结尾）
//   - "xxx*"               → 前缀匹配优先，失败则回退为包含匹配
//   - "xxx"                → 精确匹配
func MatchBashPattern(command, pattern string) bool {
	if pattern == "*" || pattern == "" {
		return true
	}
	pattern = strings.TrimSpace(pattern)
	trimmed := strings.TrimSpace(command)

	// 包含匹配: *xxx*
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") && len(pattern) > 1 {
		sub := strings.TrimSpace(pattern[1 : len(pattern)-1])
		if sub == "" {
			return true // "**" 等价于 "*"
		}
		return strings.Contains(trimmed, sub)
	}

	// 后缀匹配: *xxx
	if strings.HasPrefix(pattern, "*") {
		suffix := strings.TrimSpace(pattern[1:])
		return strings.HasSuffix(trimmed, suffix)
	}

	// 前缀匹配: xxx* → 先尝试前缀（保留空格边界），失败则回退为包含匹配
	// 例: "git *" → prefix="git " → 匹配 "git status" 和 "git"
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		word := strings.TrimRight(prefix, " ")

		// 空格边界前缀匹配（精确或后跟空格）
		if strings.HasSuffix(prefix, " ") {
			if trimmed == word {
				return true // 精确匹配，无参数，不涉及命令链
			}
			if strings.HasPrefix(trimmed, word+" ") {
				// 拒绝命令链：检测 && / || / ; / | 操作符
				// 例: Bash(echo *) 不应匹配 "echo hello && rm -rf /"
				suffix := trimmed[len(word)+1:]
				if !hasChainOperator(suffix) {
					return true
				}
				// 有命令链 → 继续尝试包含匹配（回退）
			}
			// 回退：词边界包含匹配 — 防止 "git *" 误匹配 "gitfoo"
			// 词边界 = 字符串首尾 或 相邻字符为 空格/路径分隔符
			if idx := strings.Index(trimmed, word); idx >= 0 {
				beforeOK := idx == 0 || isBoundary(trimmed[idx-1])
				afterOK := idx+len(word) == len(trimmed) || isBoundary(trimmed[idx+len(word)])
				if beforeOK && afterOK {
					return true
				}
			}
			return false
		}

		// 无空格 suffix（如 "gstack*"）：先前缀，再简单包含
		if strings.HasPrefix(trimmed, word) {
			return true
		}
		return strings.Contains(trimmed, word)
	}

	return trimmed == pattern
}

// isBoundary 检查字符是否为词边界（空格或路径分隔符）。
func isBoundary(c byte) bool {
	return c == ' ' || c == '\t' || c == '/' || c == '\\'
}

// hasChainOperator 检查字符串中是否包含 shell 命令链操作符（&& || ; |）。
// 用于防止 Bash 白名单前缀匹配绕过命令链检测。
func hasChainOperator(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ';':
			return true
		case '|':
			if i+1 < len(s) && s[i+1] == '|' {
				return true
			}
			// 单独的 | 也是管道操作符
			return true
		case '&':
			if i+1 < len(s) && s[i+1] == '&' {
				return true
			}
		}
	}
	return false
}

// ParseAllowedBashPatterns 从 allowed-tools 列表中提取 Bash 白名单模式。
// 格式: "Bash(git *)", "Bash(go test *)" → 提取括号内的 pattern。
// "Bash" 无括号 → 全放行（返回 "*"）。
// 不匹配 "Bash(...)" 格式的条目被忽略。
func ParseAllowedBashPatterns(raw []string) []string {
	var patterns []string
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if !strings.HasPrefix(entry, "Bash(") || !strings.HasSuffix(entry, ")") {
			if entry == "Bash" {
				patterns = append(patterns, "*") // 无 pattern 的 "Bash" = 全部放行
			}
			continue
		}
		// 提取括号内的 pattern
		inner := entry[5 : len(entry)-1] // 去掉 "Bash(" 和 ")"
		inner = strings.TrimSpace(inner)
		if inner != "" {
			patterns = append(patterns, inner)
		}
	}
	return patterns
}
