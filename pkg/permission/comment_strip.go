// Package permission — 注释行剥离（对标 Claude Code stripCommentLines）
package permission

import "strings"

// StripCommentLines 以引号感知方式剥离 # 开头的注释行。
func StripCommentLines(cmd string) string {
	if !strings.ContainsRune(cmd, '#') {
		return cmd
	}
	lines := strings.Split(cmd, "\n")
	var result []string
	inSQ, inDQ := false, false
	for _, line := range lines {
		// 保存处理前的状态用于注释判断
		preSQ, preDQ := inSQ, inDQ
		// 更新引号状态（所有行都需要）
		trackQuoteState(line, &inSQ, &inDQ)
		// 用处理前的状态判断此行 # 是否在引号外
		if isCommentLineWithPreState(line, preSQ, preDQ) {
			continue
		}
		result = append(result, line)
	}
	if len(result) == 0 {
		return ""
	}
	return strings.Join(result, "\n")
}

func trackQuoteState(line string, inSQ, inDQ *bool) {
	escaped := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !*inSQ {
			escaped = true
			continue
		}
		if ch == '\'' && !*inDQ {
			*inSQ = !*inSQ
			continue
		}
		if ch == '"' && !*inSQ {
			*inDQ = !*inDQ
			continue
		}
	}
}

func isCommentLineWithPreState(line string, inSQ, inDQ bool) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) == 0 {
		return false
	}
	if trimmed[0] != '#' {
		return false
	}
	// 用给定的初始引号状态扫描此行判断 # 位置
	sq, dq, escaped := inSQ, inDQ, false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !sq {
			escaped = true
			continue
		}
		if ch == '\'' && !dq {
			sq = !sq
			continue
		}
		if ch == '"' && !sq {
			dq = !dq
			continue
		}
		if ch == '#' && !sq && !dq {
			return true
		}
	}
	return false
}
