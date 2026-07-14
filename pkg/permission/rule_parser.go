package permission

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// ParseRule — 规则字符串解析
// ---------------------------------------------------------------------------

// ParseRule 解析规则字符串为 Rule 结构体。
//
// 格式：ToolName 或 ToolName(pattern)
//
// 示例：
//
//	"read_file"       → Rule{ToolName: "read_file", Pattern: ""}
//	"Bash(git *)"     → Rule{ToolName: "bash", Pattern: "git *"}
//	"write_file(src/**)" → Rule{ToolName: "write_file", Pattern: "src/**"}
//
// 兼容性：Bash(...) 自动映射为 shell(...)
func ParseRule(s string, behavior RuleBehavior) (Rule, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Rule{}, fmt.Errorf("empty rule string")
	}

	idx := strings.Index(s, "(")
	if idx < 0 {
		// 工具级规则: "read_file"
		return Rule{
			Behavior: behavior,
			ToolName: normalizeToolName(s),
			Pattern:  "",
		}, nil
	}

	// 内容级规则: "Bash(git *)"
	toolName := s[:idx]
	remaining := s[idx:]

	if len(remaining) < 2 || remaining[len(remaining)-1] != ')' {
		return Rule{}, fmt.Errorf("invalid rule format: missing closing parenthesis in %q", s)
	}

	pattern := remaining[1 : len(remaining)-1] // 去掉括号
	if pattern == "" {
		return Rule{}, fmt.Errorf("empty pattern in rule %q", s)
	}

	return Rule{
		Behavior: behavior,
		ToolName: normalizeToolName(toolName),
		Pattern:  pattern,
	}, nil
}

// normalizeToolName 兼容映射：Bash / Shell → "bash"；edit_file → "edit"；write_file → "write"。
// 用户配置中的 "Bash(git *)" 和 "Shell(git *)" 均自动归一化为 "bash(git *)"。
// "edit_file(cmd/**)" 归一化为 "edit(cmd/**)"，"write_file(cmd/**)" 归一化为 "write(cmd/**)"。
func normalizeToolName(name string) string {
	if strings.EqualFold(name, "Bash") || strings.EqualFold(name, "Shell") {
		return "bash"
	}
	if strings.EqualFold(name, "edit_file") {
		return "edit"
	}
	if strings.EqualFold(name, "write_file") {
		return "write"
	}
	return name
}

// FormatRule 将 Rule 格式化为可读字符串（不含 behavior 前缀）。
func FormatRule(r Rule) string {
	if r.Pattern != "" {
		return r.ToolName + "(" + r.Pattern + ")"
	}
	return r.ToolName
}
