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

// matchBashPatternGlob 旧 glob 退化路径,保留作为 AST 无法解析时的备用。
func matchBashPatternGlob(command, pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	trimmed := strings.TrimSpace(command)

	// xxx:* — Claude Code 的前缀写法,含多词前缀:
	// "git branch:*" 匹配 "git branch --show-current"(词边界,不匹配 "git branchx")。
	// AST 路径只认单 token 的 "cmd:*",多词前缀必须在此处截获。
	// 以 "*" 开头的模式("*xxx:*" / "*:*")交给下面的通配分支,否则其"包含子串"语义会被截断。
	if strings.HasSuffix(pattern, ":*") && !strings.HasPrefix(pattern, "*") {
		word := strings.TrimSpace(strings.TrimSuffix(pattern, ":*"))
		if word == "" {
			return false // ":*" 无有效前缀,不放行任何命令
		}
		return trimmed == word || strings.HasPrefix(trimmed, word+" ")
	}

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
		// 单个条目内可能声明多条前缀模式(如 "Bash(git branch:*, git diff:*)"),
		// 先按顶层逗号拆开再逐个提取。
		for _, item := range SplitRuleList(entry) {
			if !strings.HasPrefix(item, "Bash(") || !strings.HasSuffix(item, ")") {
				if item == "Bash" {
					patterns = append(patterns, "*")
				}
				continue
			}
			inner := strings.TrimSpace(item[5 : len(item)-1])
			patterns = append(patterns, SplitRuleList(inner)...)
		}
	}
	return patterns
}

// SplitRuleList 按顶层逗号切分权限规则串,括号内与引号内的逗号不切分。
// 覆盖两种写法:
//
//	Bash(git log:*), Read            → ["Bash(git log:*)", "Read"]
//	Bash(git branch:*, git diff:*)   → ["Bash(git branch:*, git diff:*)"]
//
// 返回项已去除首尾空白,空项被丢弃。
func SplitRuleList(raw string) []string {
	var out []string
	depth := 0
	start := 0
	var quote byte // 0 = 不在引号内
	flush := func(end int) {
		if item := strings.TrimSpace(raw[start:end]); item != "" {
			out = append(out, item)
		}
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '\\' && i+1 < len(raw) {
			i++ // 跳过被转义字符(如引号内的 \"),避免误判引号结束
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				flush(i)
				start = i + 1
			}
		}
	}
	flush(len(raw))
	return out
}
