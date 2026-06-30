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

	// 前缀匹配: xxx* → 先尝试前缀，失败则回退为包含匹配
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSpace(strings.TrimSuffix(pattern, "*"))
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
		// 回退：命令不以 prefix 开头时，尝试包含匹配
		// 场景：pattern "gstack-update-check *" 匹配 "~/.claude/.../gstack-update-check"
		return strings.Contains(trimmed, prefix)
	}

	return trimmed == pattern
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
