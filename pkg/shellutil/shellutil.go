// Package shellutil 提供 shell 命令处理相关的共享实用函数，
// 供 pkg/tool 和 pkg/skill 等包共同使用，避免循环依赖。
package shellutil

import "strings"

// IsBackgroundCommand 检测命令是否包含后台执行标志（&）。
// 检查整个命令尾部以及每一行的尾部，处理多行命令中某一行以 & 结尾的场景。
// 不处理同行内部的 &（如 echo foo & echo bar）。
func IsBackgroundCommand(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	if strings.HasSuffix(trimmed, "&") {
		return true
	}
	for _, line := range strings.Split(cmd, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), "&") {
			return true
		}
	}
	return false
}
