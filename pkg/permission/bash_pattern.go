package permission

import (
	"strings"

	"github.com/Menfre01/waveloom/pkg/bash"
)

// MatchBashPattern 检查 command 是否匹配 Bash 白名单模式。
//
// 优先使用 AST 精确匹配（baseCommand），退化到旧 glob 逻辑以保证向后兼容。
func MatchBashPattern(command, pattern string) bool {
	if pattern == "*" || pattern == "" {
		return true
	}

	// AST 优先：解析命令并精确匹配 baseCommand
	if ci, err := bash.Parse(command); err == nil && ci != nil {
		astPattern := convertToASTPattern(pattern)
		if ci.Match(astPattern) {
			return true
		}
	}

	// 退化：旧 glob 匹配
	return matchBashPatternGlob(command, pattern)
}

// matchBashPatternGlob 旧 glob 退化路径，保留作为 AST 无法解析时的备用。
func matchBashPatternGlob(command, pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	trimmed := strings.TrimSpace(command)

	// *xxx*
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") && len(pattern) > 1 {
		sub := strings.TrimSpace(pattern[1 : len(pattern)-1])
		if sub == "" {
			return true
		}
		return strings.Contains(trimmed, sub)
	}
	// *xxx
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(trimmed, strings.TrimSpace(pattern[1:]))
	}
	// xxx*
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		word := strings.TrimRight(prefix, " ")
		if strings.HasSuffix(prefix, " ") {
			// 空格边界："git *" 匹配 "git status" 但不匹配 "gitfoo"
			if trimmed == word || strings.HasPrefix(trimmed, word+" ") {
				return true
			}
			return false
		}
		if strings.HasPrefix(trimmed, word) {
			return true
		}
		return strings.Contains(trimmed, word)
	}
	return trimmed == pattern
}

// ParseAllowedBashPatterns 从 allowed-tools 列表中提取 Bash 白名单模式。
// 格式: "Bash(git *)", "Bash(go test *)" → 提取括号内的 pattern。
func ParseAllowedBashPatterns(raw []string) []string {
	var patterns []string
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if !strings.HasPrefix(entry, "Bash(") || !strings.HasSuffix(entry, ")") {
			if entry == "Bash" {
				patterns = append(patterns, "*")
			}
			continue
		}
		inner := entry[5 : len(entry)-1]
		inner = strings.TrimSpace(inner)
		if inner != "" {
			patterns = append(patterns, inner)
		}
	}
	return patterns
}
