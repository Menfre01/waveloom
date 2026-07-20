package hook

import "strings"

// 规则（对齐 Claude Code matcher 语法，精确匹配不区分大小写以兼容不同命名惯例）：
//   - 空字符串：匹配所有
//   - 精确名称：大小写不敏感匹配（如 "Bash" 匹配 "bash"）
//   - 前缀通配：以 * 结尾时匹配前缀（如 "Read*"）
//   - 多模式：| 分隔，匹配任一
// Match 检查 toolName 是否匹配 matcher 模式。
//
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
		} else if strings.EqualFold(pattern, toolName) {
			return true
		}
	}
	return false
}
