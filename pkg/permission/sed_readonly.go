// Package permission — sed 只读表达式检测（ / sedEditParser.ts）
//
// 解析 sed 表达式判断命令是否为纯只读操作（无 -i/--in-place 标志且仅使用只读命令），// 使只读 sed 能被 Plan Mode 自动批准，并被 pathValidation 降级为 read 操作类型。
package permission

import (
	"regexp"
	"strings"
)

// sedWriteCommands 是 sed 中会修改文件的命令集合。
var sedWriteCommands = map[byte]bool{
	'a': true, 'i': true, 'c': true, 's': true, 'y': true,
}

// sedReadOnlyCommands 是纯只读的 sed 命令。
var sedReadOnlyCommands = map[byte]bool{
	'p': true, 'P': true, 'd': true, 'D': true, 'l': true,
	'n': true, 'N': true, '=': true, 'q': true, 'Q': true,
	'r': true, 'w': true, 'W': true,
}

// IsSedReadOnly 检查 sed 命令是否为纯只读操作。
func IsSedReadOnly(cmd string) bool {
	ci := parseSedCommand(cmd)
	if ci == nil {
		return false
	}
	if ci.hasInPlace || ci.hasScriptFile || len(ci.expressions) == 0 {
		return false
	}
	for _, expr := range ci.expressions {
		if !isReadOnlySedExpression(expr) {
			return false
		}
	}
	return true
}

type sedCommandInfo struct {
	hasInPlace    bool
	hasScriptFile bool
	hasQuiet      bool
	expressions   []string
}

func parseSedCommand(cmd string) *sedCommandInfo {
	info := &sedCommandInfo{}
	rest := strings.TrimSpace(cmd)
	if strings.HasPrefix(rest, "sed") {
		rest = strings.TrimSpace(rest[3:])
	}
	tokens := tokenizeSed(rest)
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		switch {
		case t == "-i" || strings.HasPrefix(t, "-i") && len(t) > 2:
			info.hasInPlace = true
		case t == "--in-place":
			info.hasInPlace = true
		case t == "-n" || t == "--quiet" || t == "--silent":
			info.hasQuiet = true
		case t == "-e" || t == "--expression":
			i++
			if i < len(tokens) {
				info.expressions = append(info.expressions, tokens[i])
			}
		case t == "-f" || t == "--file":
			info.hasScriptFile = true
			i++
		case strings.HasPrefix(t, "-"):
			if len(t) > 2 && t[1] == 'e' {
				info.expressions = append(info.expressions, t[2:])
			}
			for _, ch := range t[1:] {
				if ch == 'n' {
					info.hasQuiet = true
				}
				if ch == 'f' {
					info.hasScriptFile = true
				}
			}
		default:
			if len(info.expressions) == 0 {
				info.expressions = append(info.expressions, t)
			}
		}
	}
	return info
}

func tokenizeSed(cmd string) []string {
	var tokens []string
	var current strings.Builder
	inSQ, inDQ, escaped := false, false, false
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if escaped {
			current.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' && !inSQ {
			escaped = true
			continue
		}
		if ch == '\'' && !inDQ {
			inSQ = !inSQ
			continue
		}
		if ch == '"' && !inSQ {
			inDQ = !inDQ
			continue
		}
		if !inSQ && !inDQ && (ch == ' ' || ch == '\t') {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteByte(ch)
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// reSedCommand 匹配 sed 命令字符。支持数字地址和模式地址 /regex/。
var reSedCommand = regexp.MustCompile(`(?:[0-9$]+(?:,[0-9$]*)?|/[^/]*/)?\s*([a-zA-Z])`)

func isReadOnlySedExpression(expr string) bool {
	if expr == "" {
		return false
	}
	clean := strings.Trim(expr, "'\"")
	matches := reSedCommand.FindAllStringSubmatch(clean, -1)
	if len(matches) == 0 {
		return true // 无命令 → 默认只读（print）
	}
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		cmd := m[1]
		if len(cmd) == 0 {
			continue
		}
		ch := cmd[0]
		if sedWriteCommands[ch] {
			return false
		}
		if sedReadOnlyCommands[ch] {
			continue
		}
		return false // 未知命令 → 保守拒绝
	}
	return true
}
