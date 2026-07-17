package hook

import "strings"

// Match 检查 toolName 是否匹配 matcher 模式。
//
// 规则（对齐 Claude Code matcher 语法）：
//   - 空字符串：匹配所有
//   - 精确名称：完全匹配（如 "Bash"）
//   - 前缀通配：以 * 结尾时匹配前缀（如 "Read*"）
//   - 多模式：| 分隔，匹配任一
func Match(matcher, toolName string) bool {
	if matcher == "" {
		return true
	}

	for _, pattern := range strings.Split(matcher, "|") {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if strings.HasSuffix(pattern, "*") {
			prefix := strings.TrimSuffix(pattern, "*")
			if strings.HasPrefix(toolName, prefix) {
				return true
			}
		} else if pattern == toolName {
			return true
		}
	}
	return false
}
