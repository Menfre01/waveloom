package permission

import (
	"fmt"
	"strings"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// DangerousCommandPattern — 危险命令模式
// ---------------------------------------------------------------------------

// DangerousCommandPattern 描述一个危险命令的匹配模式。
// 迁移自 pkg/tool/shell.go 的 dangerousPattern，升级为导出类型。
type DangerousCommandPattern struct {
	Keywords  []string // AND 关系，全部匹配才触发
	Pipewords []string // 可选：匹配管道后的危险命令名
	Label     string   // 人类可读的描述
}

// Matches 检查命令是否匹配此危险模式。
func (d *DangerousCommandPattern) Matches(command string) bool {
	// AND 关系：所有 Keywords 必须出现
	for _, kw := range d.Keywords {
		if !strings.Contains(command, kw) {
			return false
		}
	}
	// 如果设置了 Pipewords：Keywords 全匹配后，检查管道后是否跟了危险命令
	if len(d.Pipewords) > 0 {
		return d.checkPipe(command)
	}
	return true
}

// checkPipe 检查命令中管道后是否跟了危险命令。
func (d *DangerousCommandPattern) checkPipe(command string) bool {
	idx := strings.LastIndex(command, "|")
	if idx < 0 {
		return false
	}
	after := command[idx+1:]
	for _, pw := range d.Pipewords {
		if strings.Contains(after, pw) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// DangerousPatterns — 完整的危险命令模式列表
// ---------------------------------------------------------------------------

// DangerousPatterns 从 shell.go 迁移，完全保持原数据。
// Wave 2 中为软限制（仅警告），Wave 3 升级为硬拦截。
var DangerousPatterns = []DangerousCommandPattern{
	// 文件销毁
	{Keywords: []string{"rm", "-rf", "/"}, Label: "rm -rf / (recursive root deletion)"},
	{Keywords: []string{"rm", "-rf", "*"}, Label: "rm -rf * (recursive wildcard)"},
	{Keywords: []string{"sudo", "rm"}, Label: "sudo rm (privileged deletion)"},
	{Keywords: []string{">", "/dev/sd"}, Label: "overwrite block device"},
	{Keywords: []string{"dd", "if="}, Label: "dd disk copy"},
	{Keywords: []string{"mkfs"}, Label: "mkfs (format filesystem)"},

	// 权限修改
	{Keywords: []string{"chmod", "777"}, Label: "chmod 777 (world-writable)"},
	{Keywords: []string{"chmod", "u+s"}, Label: "chmod u+s (setuid)"},

	// 网络下载 + 管道执行
	{Keywords: []string{"curl"}, Pipewords: []string{"sh", "bash"}, Label: "curl piped to shell"},
	{Keywords: []string{"wget"}, Pipewords: []string{"sh", "bash"}, Label: "wget piped to shell"},

	// Fork bomb
	{Keywords: []string{":(){", ":|:&"}, Label: "fork bomb pattern"},

	// 外部脚本执行
	{Keywords: []string{"python", "-c", "import os"}, Label: "python -c with os import"},
	{Keywords: []string{"python", "-c", "import subprocess"}, Label: "python -c with subprocess import"},
	{Keywords: []string{"perl", "-e"}, Label: "perl -e inline execution"},

	// find -exec 危险组合
	{Keywords: []string{"find", "-exec", "chmod"}, Label: "find -exec chmod"},
	{Keywords: []string{"find", "-exec", "rm"}, Label: "find -exec rm"},
	{Keywords: []string{"xargs", "rm"}, Label: "xargs rm"},
}

// ---------------------------------------------------------------------------
// knownSafeCommands — 已知安全命令
// ---------------------------------------------------------------------------

// knownSafeCommands 首命令级别的白名单。
// 匹配逻辑为取命令行的第一个 token 进行检查。
// 即使首命令安全，后续参数仍可能构成危险，交由 DangerousPatterns 兜底。
var knownSafeCommands = map[string]bool{
	// 版本控制
	"git": true,
	// 文件查看
	"ls":       true,
	"cat":      true,
	"head":     true,
	"tail":     true,
	"less":     true,
	"more":     true,
	// 基础信息
	"echo":     true,
	"pwd":      true,
	"which":    true,
	"where":    true,
	"env":      true,
	"printenv": true,
	"whoami":   true,
	"hostname": true,
	"date":     true,
	"uname":    true,
	// 磁盘信息
	"df": true,
	"du": true,
	// 文本处理（只读）
	"wc":   true,
	"sort": true,
	"uniq": true,
	"diff": true,
	"test": true,
	// 编程语言（通常用于测试/构建）
	"go":     true,
	"cargo":  true,
	"python": true,
	"node":   true,
	// 包管理（读操作为主）
	"npm":   true,
	"yarn":  true,
	"pnpm":  true,
	"pip":   true,
}

// ---------------------------------------------------------------------------
// CommandCheckResult — 命令安全检查结果
// ---------------------------------------------------------------------------

// CommandCheckResult 包含命令安全检查的结果。
type CommandCheckResult struct {
	Level   CommandRiskLevel
	Pattern string // 匹配的危险模式 Label（如有）
	Message string
}

// ---------------------------------------------------------------------------
// CommandSafetyCheck — 命令安全检查入口
// ---------------------------------------------------------------------------

// CommandSafetyCheck 检查命令的安全性，返回风险等级。
// 入参 command 会先做 cd 前缀归一化，确保提取的 first token 是实际命令而非 cd。
func CommandSafetyCheck(command string) CommandCheckResult {
	command = strings.TrimSpace(command)
	if command == "" {
		return CommandCheckResult{Level: RiskLow, Message: "empty command"}
	}

	// 0. 归一化：剥离 "cd <path> &&" 前缀，使 first token 反映实际命令
	normalized, _ := pathutil.NormalizeShellCommand(command)
	if normalized != "" {
		command = normalized
	}

	// 1. 危险模式匹配（最高优先级）
	for _, dp := range DangerousPatterns {
		if dp.Matches(command) {
			return CommandCheckResult{
				Level:   RiskHigh,
				Pattern: dp.Label,
				Message: fmt.Sprintf("dangerous command pattern: %s", dp.Label),
			}
		}
	}

	// 2. 已知安全命令快速通道
	firstToken := extractFirstToken(command)
	if knownSafeCommands[firstToken] {
		return CommandCheckResult{Level: RiskLow, Message: "known safe command: " + firstToken}
	}

	// 3. 默认中等风险
	return CommandCheckResult{Level: RiskMedium, Message: "unclassified command"}
}

// extractFirstToken 取命令行第一个 token（空格分隔）。
func extractFirstToken(command string) string {
	// 去掉前导环境变量赋值（如 CC=gcc make ...）
	for strings.Contains(command, "=") {
		idx := strings.Index(command, " ")
		if idx < 0 {
			break
		}
		prefix := command[:idx]
		if !strings.Contains(prefix, "=") {
			break
		}
		command = strings.TrimSpace(command[idx+1:])
	}

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return ""
	}

	// 取路径的最后一部分（如 /usr/bin/git → git）
	token := parts[0]
	if idx := strings.LastIndex(token, "/"); idx >= 0 {
		token = token[idx+1:]
	}

	return token
}
