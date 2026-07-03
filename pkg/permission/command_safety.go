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

// checkPipe 检查命令中所有管道段后是否跟了危险命令。
func (d *DangerousCommandPattern) checkPipe(command string) bool {
	segments := splitPipeSegments(command)
	// 对每个非首段（管道下游），检查是否包含危险命令
	for i := 1; i < len(segments); i++ {
		for _, pw := range d.Pipewords {
			if strings.Contains(strings.TrimSpace(segments[i]), pw) {
				return true
			}
		}
	}
	return false
}

// splitPipeSegments 按管道符 | 分割命令，返回各段。
// 不处理引号内转义——对于 LLM 生成的命令已足够。
func splitPipeSegments(command string) []string {
	var segments []string
	start := 0
	for i := 0; i < len(command); i++ {
		if command[i] == '|' {
			segments = append(segments, command[start:i])
			start = i + 1
		}
	}
	segments = append(segments, command[start:])
	return segments
}

// ---------------------------------------------------------------------------
// DangerousPatterns — 完整的危险命令模式列表
// ---------------------------------------------------------------------------

// DangerousPatterns 从 shell.go 迁移，完全保持原数据。
// Wave 2 中为软限制（仅警告），Wave 3 升级为硬拦截。
var DangerousPatterns = []DangerousCommandPattern{
	// ── 文件/文件系统销毁 ──
	{Keywords: []string{"rm", "-rf", "/"}, Label: "rm -rf / (recursive root deletion)"},
	{Keywords: []string{"rm", "-rf", "~"}, Label: "rm -rf ~ (home directory deletion)"},
	{Keywords: []string{"rm", "-rf", "*"}, Label: "rm -rf * (recursive wildcard)"},
	{Keywords: []string{"sudo", "rm"}, Label: "sudo rm (privileged deletion)"},
	{Keywords: []string{">", "/dev/sd"}, Label: "overwrite block device"},
	{Keywords: []string{"dd", "if="}, Label: "dd disk copy"},
	{Keywords: []string{"mkfs"}, Label: "mkfs (format filesystem)"},
	{Keywords: []string{"shred"}, Label: "shred (secure file deletion)"},

	// ── 权限 / 所有权修改 ──
	{Keywords: []string{"chmod", "777"}, Label: "chmod 777 (world-writable)"},
	{Keywords: []string{"chmod", "u+s"}, Label: "chmod u+s (setuid)"},
	{Keywords: []string{"chmod", "-R"}, Label: "chmod -R (recursive permission change)"},
	{Keywords: []string{"chown"}, Label: "chown (ownership change)"},

	// ── 系统操作 ──
	{Keywords: []string{"shutdown"}, Label: "shutdown (system shutdown)"},
	{Keywords: []string{"reboot"}, Label: "reboot (system reboot)"},
	{Keywords: []string{"halt"}, Label: "halt (system halt)"},
	{Keywords: []string{"poweroff"}, Label: "poweroff (system power off)"},
	{Keywords: []string{"mount"}, Label: "mount (filesystem mount)"},
	{Keywords: []string{"umount"}, Label: "umount (filesystem unmount)"},

	// ── 进程终止 ──
	{Keywords: []string{"kill", "-9"}, Label: "kill -9 (forced process termination)"},
	{Keywords: []string{"killall"}, Label: "killall (terminate by name)"},
	{Keywords: []string{"pkill"}, Label: "pkill (terminate by pattern)"},

	// ── 网络下载 + 管道执行 ──
	{Keywords: []string{"curl"}, Pipewords: []string{"sh", "bash", "python", "perl", "ruby"}, Label: "curl piped to interpreter"},
	{Keywords: []string{"wget"}, Pipewords: []string{"sh", "bash", "python", "perl", "ruby"}, Label: "wget piped to interpreter"},
	{Keywords: []string{"cat"}, Pipewords: []string{"sh", "bash"}, Label: "cat piped to shell"},

	// ── Fork bomb ──
	{Keywords: []string{":(){", ":|:&"}, Label: "fork bomb pattern"},

	// ── 外部脚本 / 内联执行 ──
	{Keywords: []string{"python", "-c", "import os"}, Label: "python -c with os import"},
	{Keywords: []string{"python", "-c", "import subprocess"}, Label: "python -c with subprocess import"},
	{Keywords: []string{"python3", "-c", "import os"}, Label: "python3 -c with os import"},
	{Keywords: []string{"python3", "-c", "import subprocess"}, Label: "python3 -c with subprocess import"},
	{Keywords: []string{"node", "-e"}, Label: "node -e inline execution"},
	{Keywords: []string{"perl", "-e"}, Label: "perl -e inline execution"},
	{Keywords: []string{"ruby", "-e"}, Label: "ruby -e inline execution"},
	{Keywords: []string{"bash", "-c"}, Label: "bash -c inline execution"},
	{Keywords: []string{"sh", "-c"}, Label: "sh -c inline execution"},

	// ── Shell 内建危险 ──
	{Keywords: []string{"eval"}, Label: "eval (arbitrary code execution)"},
	{Keywords: []string{"sudo"}, Label: "sudo (privilege escalation)"},
	{Keywords: []string{"source", "/dev/"}, Label: "source from /dev"},
	{Keywords: []string{"exec"}, Label: "exec (replace shell process)"},

	// ── 网络工具 ──
	{Keywords: []string{"nc", "-e"}, Label: "nc -e (netcat execute)"},
	{Keywords: []string{"nc", "-l"}, Pipewords: []string{"sh", "bash"}, Label: "nc listener piped to shell"},
	{Keywords: []string{"iptables"}, Label: "iptables (firewall modification)"},
	{Keywords: []string{"pfctl"}, Label: "pfctl (macOS firewall modification)"},

	// ── find -exec / xargs 危险组合 ──
	{Keywords: []string{"find", "-exec", "chmod"}, Label: "find -exec chmod"},
	{Keywords: []string{"find", "-exec", "rm"}, Label: "find -exec rm"},
	{Keywords: []string{"find", "-delete"}, Label: "find -delete"},
	{Keywords: []string{"xargs", "rm"}, Label: "xargs rm"},
	{Keywords: []string{"xargs", "sh"}, Label: "xargs sh"},
	{Keywords: []string{"xargs", "bash"}, Label: "xargs bash"},

	// ── 系统配置修改 ──
	{Keywords: []string{"sysctl", "-w"}, Label: "sysctl -w (kernel parameter write)"},
	{Keywords: []string{"crontab"}, Label: "crontab (schedule tasks)"},
	{Keywords: []string{"tee", "/etc/"}, Label: "tee to /etc (system config overwrite)"},
	{Keywords: []string{"tee", "/dev/"}, Label: "tee to /dev (device write)"},

	// ── SSH / 远程执行 ──
	{Keywords: []string{"ssh", "root@"}, Label: "ssh to root (remote privileged access)"},
	{Keywords: []string{"scp"}, Label: "scp (remote file transfer)"},

	// ── Git 破坏性操作 ──
	{Keywords: []string{"git", "push", "--force"}, Label: "git push --force (force push)"},
	{Keywords: []string{"git", "reset", "--hard"}, Label: "git reset --hard (discard all changes)"},
	{Keywords: []string{"git", "clean", "-fdx"}, Label: "git clean -fdx (remove untracked files)"},
}

// ---------------------------------------------------------------------------
// trulySafeCommands — 纯只读命令，零副作用，直接 ALLOW 无需用户确认
// ---------------------------------------------------------------------------

// trulySafeCommands 是经过严格审查的纯只读命令。
// 条件：不修改文件系统、不派生进程、不执行代码、不产生网络副作用。
// 即使参数变化（如 ls -la / ls -R），命令本身始终安全。
//
// 被排除的候选及原因：
//   - env / printenv: 暴露环境变量中的 API 密钥等敏感信息
//   - less / more: 交互式 TTY 工具，非 TTY 环境下行为异常且无实用价值
var trulySafeCommands = map[string]bool{
	// 文件查看（纯读取）
	"ls":   true,
	"cat":  true,
	"head": true,
	"tail": true,
	// 搜索工具（纯读取，替代已删除的 grep/search_file/ls 工具）
	"grep": true,
	"find": true,
	"file": true,
	// 基础输出与目录操作（echo 重定向风险由 ASK 对话框可见命令来防范）
	"echo":  true,
	"mkdir": true,
	// 基础信息查询
	"pwd":      true,
	"which":    true,
	"where":    true,
	"whoami":   true,
	"hostname": true,
	"date":     true,
	"uname":    true,
	// 磁盘用量查询
	"df": true,
	"du": true,
	// 文本处理（只读管道过滤）
	"wc":   true,
	"sort": true,
	"uniq": true,
	// 差异比较（不修改文件）
	"diff": true,
	// 条件判断（无副作用，仅返回退出码）
	"test": true,
}

// ---------------------------------------------------------------------------
// buildToolCommands — 构建/版本控制工具，需要子命令级白名单
// ---------------------------------------------------------------------------

// buildToolCommands 是构建工具和版本控制工具。
// RiskLow：不会被 Step 3 高危拦截，但仍走 Step 7 默认策略（normal 模式 ASK，plan 模式 ALLOW）。
var buildToolCommands = map[string]bool{
	"git":    true,
	"go":     true,
	"cargo":  true,
	"rustc":  true,
	"make":   true,
	"npm":    true,
	"npx":    true,
	"node":   true,
	"python": true,
	"python3": true,
	"pip":    true,
	"pip3":   true,
	"docker": true,
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
//
// 对于 && / ; / || / 换行 连接的命令链，每个子命令独立评估，
// 取最高风险等级作为整体结果——防止 "ls && rm -rf /" 仅凭首命令 "ls" 误判为安全。
func CommandSafetyCheck(command string) CommandCheckResult {
	command = strings.TrimSpace(command)
	if command == "" {
		return CommandCheckResult{Level: RiskNone, Message: "empty command"}
	}

	// 0. 归一化：剥离 "cd <path> &&" 前缀，使 first token 反映实际命令
	normalized, _ := pathutil.NormalizeShellCommand(command)
	if normalized != "" {
		command = normalized
	}

	// 1. 危险模式匹配（最高优先级，先于已知安全命令检查）
	//    即使首命令在安全列表中，危险模式仍可能命中（如 git + find -exec rm 的组合等）
	for _, dp := range DangerousPatterns {
		if dp.Matches(command) {
			return CommandCheckResult{
				Level:   RiskHigh,
				Pattern: dp.Label,
				Message: fmt.Sprintf("dangerous command pattern: %s", dp.Label),
			}
		}
	}

	// 2. 命令链分割：对 && / ; / || / 换行 / 管道 连接的每个子命令独立评估
	segments := splitCommandChain(command)
	if len(segments) > 1 {
		highestLevel := RiskNone
		var highestMsg string
		for _, seg := range segments {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			segResult := singleCommandRisk(seg)
			if riskOrder(segResult.Level) > riskOrder(highestLevel) {
				highestLevel = segResult.Level
				highestMsg = segResult.Message
			}
		}
		if highestLevel == RiskNone {
			return CommandCheckResult{Level: RiskNone, Message: "all chained commands are safe"}
		}
		return CommandCheckResult{Level: highestLevel, Message: highestMsg}
	}

	// 3. 单命令评估
	return singleCommandRisk(command)
}

// singleCommandRisk 评估单条命令（不含 && / ; / ||）的风险。
func singleCommandRisk(command string) CommandCheckResult {
	firstToken := extractFirstToken(command)
	if trulySafeCommands[firstToken] {
		return CommandCheckResult{Level: RiskNone, Message: "safe read-only command: " + firstToken}
	}

	if buildToolCommands[firstToken] {
		return CommandCheckResult{Level: RiskLow, Message: "build tool: " + firstToken}
	}

	return CommandCheckResult{Level: RiskMedium, Message: "unclassified command"}
}

// riskOrder 返回风险等级的顺序值（数值越大风险越高）。
func riskOrder(level CommandRiskLevel) int {
	switch level {
	case RiskNone:
		return 0
	case RiskLow:
		return 1
	case RiskMedium:
		return 2
	case RiskHigh:
		return 3
	}
	return 0
}

// splitCommandChain 将命令按 && / ; / || / 换行 / | 分割为子命令。
// 注意：管道 | 分割后，每段仍会被后续的危险模式检查覆盖。
func splitCommandChain(command string) []string {
	var segments []string
	start := 0
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if ch == '\n' {
			segments = append(segments, command[start:i])
			start = i + 1
			continue
		}
		if i+1 < len(command) {
			if (ch == '&' && command[i+1] == '&') || (ch == '|' && command[i+1] == '|') {
				segments = append(segments, command[start:i])
				start = i + 2
				i++ // skip second char
				continue
			}
		}
		if ch == ';' || ch == '|' {
			segments = append(segments, command[start:i])
			start = i + 1
		}
	}
	segments = append(segments, command[start:])

	// 过滤空白段
	var result []string
	for _, s := range segments {
		s = strings.TrimSpace(s)
		if s != "" {
			result = append(result, s)
		}
	}
	if len(result) <= 1 {
		return nil // 无链，单命令
	}
	return result
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
