// Package bash 基于 mvdan.cc/sh 的 AST 提供 shell 命令分析能力。
//
// 基于 AST 实现命令结构提取和 parser differential 攻击检测。
package bash

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// CommandInfo 从 shell 命令 AST 中提取的安全分析所需的结构化信息。
type CommandInfo struct {
	BaseCommand   string
	Args          []string
	Flags         []string
	Redirs        []string
	HasPipes      bool
	HasCommandSub bool
	Stmts         int
	Raw           string
}

// Parse 解析 shell 命令字符串，返回提取的结构化信息。
func Parse(raw string) (*CommandInfo, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("empty command")
	}

	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(raw), "")
	if err != nil {
		return nil, fmt.Errorf("parse shell command: %w", err)
	}

	info := &CommandInfo{Raw: raw}
	extractCommandInfo(prog, info)
	return info, nil
}

// ParseLenient 尝试解析命令，失败时退化提取第一个非赋值 token。
func ParseLenient(raw string) *CommandInfo {
	info, err := Parse(raw)
	if err != nil {
		info = &CommandInfo{Raw: raw}
		tokens := strings.Fields(raw)
		for _, t := range tokens {
			if !strings.Contains(t, "=") {
				info.BaseCommand = t
				break
			}
		}
	}
	return info
}

// extractCommandInfo 遍历 AST 提取结构化信息。
func extractCommandInfo(prog *syntax.File, info *CommandInfo) {
	// 收集所有顶层语句
	for _, stmt := range prog.Stmts {
		if stmt != nil {
			info.Stmts++
		}
	}

	// 命令替换检测
	syntax.Walk(prog, func(node syntax.Node) bool {
		if _, ok := node.(*syntax.CmdSubst); ok {
			info.HasCommandSub = true
			return false
		}
		return true
	})

	if info.Stmts == 0 {
		return
	}

	// 检测管道或连接符
	if info.Stmts > 1 {
		info.HasPipes = true
	}

	// 从第一个语句提取命令信息
	firstStmt := prog.Stmts[0]
	info.HasPipes = info.HasPipes || extractStmtPipes(firstStmt)
	extractStmtArgs(firstStmt, info)
}

// extractStmtPipes 检测语句中是否包含管道或连接符。
// 仅 BinaryCmd 算作管道；CallExpr 的重定向不改变 HasPipes。
func extractStmtPipes(stmt *syntax.Stmt) bool {
	if stmt.Cmd == nil {
		return false
	}
	switch stmt.Cmd.(type) {
	case *syntax.BinaryCmd:
		return true
	}
	return false
}
// extractStmtArgs 从语句中提取参数。
func extractStmtArgs(stmt *syntax.Stmt, info *CommandInfo) {
	// 处理重定向
	for _, redir := range stmt.Redirs {
		if redir != nil && redir.Word != nil {
			info.Redirs = append(info.Redirs, redir.Word.Lit())
		}
	}

	if stmt.Cmd == nil {
		return
	}
	extractArgs(stmt.Cmd, info)
}

// extractArgs 递归提取命令参数。
func extractArgs(cmd syntax.Command, info *CommandInfo) {
	switch c := cmd.(type) {
	case *syntax.CallExpr:
		extractCallArgs(c, info)
	case *syntax.BinaryCmd:
		if c.X != nil && c.X.Cmd != nil {
			extractArgs(c.X.Cmd, info)
		}
	case *syntax.Subshell:
		for _, stmt := range c.Stmts {
			if stmt != nil && stmt.Cmd != nil {
				extractArgs(stmt.Cmd, info)
				return
			}
		}
	}
}

func extractCallArgs(call *syntax.CallExpr, info *CommandInfo) {
	args := call.Args
	if len(args) == 0 {
		return
	}

	var argList []string
	for _, word := range args {
		if word != nil {
			argList = append(argList, word.Lit())
		}
	}
	if len(argList) == 0 {
		return
	}

	// 跳过赋值（VAR=val）
	idx := 0
	for idx < len(argList) && strings.Contains(argList[idx], "=") {
		idx++
	}

	// 跳过内置修饰符
	modifiers := map[string]bool{
		"command": true, "builtin": true, "exec": true,
		"noglob": true, "nocorrect": true,
	}
	for idx < len(argList) && modifiers[argList[idx]] {
		idx++
	}
	if idx < len(argList) && argList[idx] == "sudo" {
		idx++
		// 跳过 sudo 自身的标志（-u, -E, -n, -g, -- 等）
		for idx < len(argList) {
			arg := argList[idx]
			if arg == "--" {
				idx++
				break // -- 之后全是命令
			}
			if !strings.HasPrefix(arg, "-") {
				break // 不再以 - 开头 → 这是命令本身
			}
			// 跳过标志
			idx++
			// -u/-g/-U 等需要参数值的标志 → 多跳一个
			if isSudoFlagWithValue(arg) && idx < len(argList) {
				idx++
			}
		}
		// sudo 之后可能还有修饰符
		for idx < len(argList) && modifiers[argList[idx]] {
			idx++
		}
	}

	if idx < len(argList) {
		info.BaseCommand = argList[idx]
		idx++
	}

	for ; idx < len(argList); idx++ {
		arg := argList[idx]
		info.Args = append(info.Args, arg)
		if strings.HasPrefix(arg, "-") {
			info.Flags = append(info.Flags, arg)
		}
	}
}

// isSudoFlagWithValue 判断 sudo 标志是否需要参数值。
func isSudoFlagWithValue(flag string) bool {
	switch flag {
	case "-u", "-g", "-U", "-C", "--close-from":
		return true
	default:
		return false
	}
}

// Match 检查 deny/allow 规则是否匹配命令。
func (ci *CommandInfo) Match(pattern string) bool {
	if ci == nil {
		return false
	}
	parts := strings.SplitN(pattern, ":", 2)
	if ci.BaseCommand != parts[0] && !strings.HasSuffix(ci.BaseCommand, "/"+parts[0]) {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	sub := parts[1]
	if sub == "*" {
		return true
	}
	for _, arg := range ci.Args {
		if arg == sub {
			return true
		}
	}
	for _, flag := range ci.Flags {
		if flag == sub {
			return true
		}
	}
	return false
}
