package permission

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// DangerousCommandPattern — 危险命令模式
// ---------------------------------------------------------------------------

// DangerousCommandPattern 描述一个危险命令的匹配模式。
// 迁移自 pkg/tool/shell.go 的 dangerousPattern，升级为导出类型。
type DangerousCommandPattern struct {
	Keywords      []string // AND 关系，全部匹配才触发
	Pipewords     []string // 可选：匹配管道后的危险命令名
	Label         string   // 人类可读的描述
	FirstTokenOnly bool    // 仅对首 token 做精确匹配，避免路径/参数中的子串误伤
}

// Matches 检查命令是否匹配此危险模式。
func (d *DangerousCommandPattern) Matches(command string) bool {
	// AND 关系：所有 Keywords 必须出现
	for i, kw := range d.Keywords {
		if i == 0 && d.FirstTokenOnly {
			// 首 keyword 需匹配任一子命令的首 token（精确或 prefix），
			// 而非全命令的首 token。拆链后逐段检查，防止 "echo && shutdown" 漏检。
			if !anyFirstTokenMatches(command, kw) {
				return false
			}
		} else {
			if !strings.Contains(command, kw) {
				return false
			}
		}
	}
	// 如果设置了 Pipewords：Keywords 全匹配后，检查管道后是否跟了危险命令
	if len(d.Pipewords) > 0 {
		return d.checkPipe(command)
	}
	return true
}

// anyFirstTokenMatches 检查命令链中任一子命令的首 token 是否匹配 keyword。
func anyFirstTokenMatches(command, kw string) bool {
	segments := splitCommandChain(command)
	if segments == nil {
		// 单命令，直接检查首 token
		return firstTokenMatches(extractFirstToken(command), kw)
	}
	for _, seg := range segments {
		if firstTokenMatches(extractFirstToken(seg), kw) {
			return true
		}
	}
	return false
}

// firstTokenMatches 检查首 token 是否等于 keyword，或以 keyword. 开头
// （匹配子命令变体如 mkfs.ext4）。注意：不匹配 kw-（如 scp-wrapper），
// iptables-* 子命令由独立的 DangerousPatterns 覆盖。
func firstTokenMatches(firstToken, kw string) bool {
	if firstToken == kw {
		return true
	}
	if strings.HasPrefix(firstToken, kw+".") {
		return true
	}
	return false
}

// checkPipe 检查命令的管道下游段是否以危险命令开头。
// 使用 first-token 精确匹配，而非子串匹配，避免 "grep sh" 中的 "sh" 被误判为 shell 命令。
func (d *DangerousCommandPattern) checkPipe(command string) bool {
	segments := splitPipeSegments(command)
	// 对每个非首段（管道下游），检查首 token 是否为危险命令
	for i := 1; i < len(segments); i++ {
		firstToken := extractFirstToken(segments[i])
		for _, pw := range d.Pipewords {
			if firstToken == pw {
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
	{Keywords: []string{"mkfs"}, Label: "mkfs (format filesystem)", FirstTokenOnly: true},
	{Keywords: []string{"shred"}, Label: "shred (secure file deletion)", FirstTokenOnly: true},

	// ── 权限 / 所有权修改 ──
	{Keywords: []string{"chmod", "777"}, Label: "chmod 777 (world-writable)"},
	{Keywords: []string{"chmod", "u+s"}, Label: "chmod u+s (setuid)"},
	{Keywords: []string{"chmod", "-R"}, Label: "chmod -R (recursive permission change)"},
	{Keywords: []string{"chown"}, Label: "chown (ownership change)", FirstTokenOnly: true},

	// ── 系统操作 ──
	{Keywords: []string{"shutdown"}, Label: "shutdown (system shutdown)", FirstTokenOnly: true},
	{Keywords: []string{"reboot"}, Label: "reboot (system reboot)", FirstTokenOnly: true},
	{Keywords: []string{"halt"}, Label: "halt (system halt)", FirstTokenOnly: true},
	{Keywords: []string{"poweroff"}, Label: "poweroff (system power off)", FirstTokenOnly: true},
	{Keywords: []string{"mount"}, Label: "mount (filesystem mount)", FirstTokenOnly: true},
	{Keywords: []string{"umount"}, Label: "umount (filesystem unmount)", FirstTokenOnly: true},

	// ── 进程终止 ──
	{Keywords: []string{"kill", "-9"}, Label: "kill -9 (forced process termination)"},
	{Keywords: []string{"killall"}, Label: "killall (terminate by name)", FirstTokenOnly: true},
	{Keywords: []string{"pkill"}, Label: "pkill (terminate by pattern)", FirstTokenOnly: true},

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
	{Keywords: []string{"node", "-e"}, Label: "node -e inline execution", FirstTokenOnly: true},
	{Keywords: []string{"perl", "-e"}, Label: "perl -e inline execution", FirstTokenOnly: true},
	{Keywords: []string{"ruby", "-e"}, Label: "ruby -e inline execution", FirstTokenOnly: true},
	{Keywords: []string{"bash", "-c"}, Label: "bash -c inline execution", FirstTokenOnly: true},
	{Keywords: []string{"sh", "-c"}, Label: "sh -c inline execution", FirstTokenOnly: true},

	// ── Shell 内建危险 ──
	{Keywords: []string{"eval"}, Label: "eval (arbitrary code execution)", FirstTokenOnly: true},
	{Keywords: []string{"sudo"}, Label: "sudo (privilege escalation)", FirstTokenOnly: true},
	// source /dev/stdin 等：用单 keyword 强制 source + /dev/ 邻接，
	// 避免 "claude-source/ 2>/dev/null" 误中（空格归一化后 source 在路径中，/dev/ 在重定向中）。
	{Keywords: []string{"source /dev/"}, Label: "source from /dev/stdin"},
	{Keywords: []string{". /dev/"}, Label: ". (source) from /dev/stdin"},
	{Keywords: []string{"exec"}, Label: "exec (replace shell process)", FirstTokenOnly: true},

	// ── 网络工具 ──
	{Keywords: []string{"nc", "-e"}, Label: "nc -e (netcat execute)", FirstTokenOnly: true},
	{Keywords: []string{"nc", "-l"}, Pipewords: []string{"sh", "bash"}, Label: "nc listener piped to shell", FirstTokenOnly: true},
	{Keywords: []string{"iptables"}, Label: "iptables (firewall modification)", FirstTokenOnly: true},
	{Keywords: []string{"iptables-restore"}, Label: "iptables-restore (firewall rules restore)", FirstTokenOnly: true},
	{Keywords: []string{"iptables-save"}, Label: "iptables-save (firewall rules save)", FirstTokenOnly: true},
	{Keywords: []string{"pfctl"}, Label: "pfctl (macOS firewall modification)", FirstTokenOnly: true},

	// ── find -exec / xargs 危险组合 ──
	{Keywords: []string{"find", "-exec chmod"}, Label: "find -exec chmod"},
	{Keywords: []string{"find", "-exec rm"}, Label: "find -exec rm"},
	{Keywords: []string{"find", "-delete"}, Label: "find -delete"},
	{Keywords: []string{"xargs", "rm"}, Label: "xargs rm", FirstTokenOnly: true},
	{Keywords: []string{"xargs", "sh"}, Label: "xargs sh", FirstTokenOnly: true},
	{Keywords: []string{"xargs", "bash"}, Label: "xargs bash", FirstTokenOnly: true},

	// ── 系统配置修改 ──
	{Keywords: []string{"sysctl", "-w"}, Label: "sysctl -w (kernel parameter write)"},
	{Keywords: []string{"crontab"}, Label: "crontab (schedule tasks)", FirstTokenOnly: true},
	{Keywords: []string{"tee", "/etc/"}, Label: "tee to /etc (system config overwrite)", FirstTokenOnly: true},
	{Keywords: []string{"tee", "/dev/"}, Label: "tee to /dev (device write)", FirstTokenOnly: true},

	// ── SSH / 远程执行 ──
	{Keywords: []string{"ssh", "root@"}, Label: "ssh to root (remote privileged access)", FirstTokenOnly: true},
	{Keywords: []string{"scp"}, Label: "scp (remote file transfer)", FirstTokenOnly: true},

	// ── Git 破坏性操作 ──
	{Keywords: []string{"git", "push", "--force"}, Label: "git push --force (force push)"},
	{Keywords: []string{"git", "reset", "--hard"}, Label: "git reset --hard (discard all changes)"},
	{Keywords: []string{"git", "clean", "-fdx"}, Label: "git clean -fdx (remove untracked files)"},
	// ── Windows 系统工具 ──
	{Keywords: []string{"diskpart"}, Label: "diskpart (disk partition tool)", FirstTokenOnly: true},
	{Keywords: []string{"format"}, Label: "format (disk format)", FirstTokenOnly: true},
	{Keywords: []string{"reg", "delete"}, Label: "reg delete (registry deletion)"},
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
	// Windows 等价命令（Unix 上不存在或同样安全）
	"dir":  true,
	"type": true,
}

// commandsWithDangerousArgs 是 trulySafeCommands 中某些参数组合有危险的命令。
// 这些命令仍需要走危险模式检查（如 find -exec rm / find -delete），
// 不能在 safe-command 快速路径中直接放行。
var commandsWithDangerousArgs = map[string]bool{
	"find": true,
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

	// 0.5 水平空格归一化：collapse 连续空格/tab 为单空格（保留换行），
	// 防止多余空白导致邻接 keyword（如 "source /dev/"）漏检。
	// 注意：不能 collapse 换行——会破坏 heredoc 边界，导致 heredoc 体内
	// 的 | sh / | bash 等内容被误判为 shell 管道。
	command = collapseHorizontalWhitespace(command)

	// 0.6 剥离 heredoc 体：防止 heredoc 体内的 | sh / | bash 等字符串
	// 被误判为 shell 管道。仅保留 heredoc 起始标记，剔除 body 和结束标记。
	command = stripHeredocs(command)

	// 1. 单条安全命令快速路径：首 token 在 trulySafeCommands、无危险参数、非链命令，
	//    直接返回 RiskNone，避免危险模式误伤参数中的关键词（如 echo "reboot"）。
	//    find 等虽有危险子命令，仍走完整流程（如 find -exec rm）。
	firstToken := extractFirstToken(command)
	if trulySafeCommands[firstToken] && !commandsWithDangerousArgs[firstToken] && splitCommandChain(command) == nil {
		return CommandCheckResult{Level: RiskNone, Message: "safe read-only command: " + firstToken}
	}

	// 2. 危险模式匹配（最高优先级，先于已知安全命令检查）
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

	// 3. 命令链分割：对 && / ; / || / 换行 / 管道 连接的每个子命令独立评估
	segments := splitCommandChain(command)
	if len(segments) > 1 {
		highestLevel := RiskNone
		var highestMsg string
		for _, seg := range segments {
		segResult := singleCommandRisk(strings.TrimSpace(seg))
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

	// 4. 单命令评估
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

// stripHeredocs 移除命令中的 heredoc 体（<< 和 <<-），防止 heredoc 体内的
// 字符串被误判为 shell 管道或危险模式。保留 << 起始行但删除 body 和结束标记。
func stripHeredocs(command string) string {
	// heredoc 模式：<< 或 <<- 后跟可选空格，然后是定界符（可被引号包围）
	re := regexp.MustCompile(`<<-?\s*('[^']*'|"[^"]*"|\S+)`)
	for {
		loc := re.FindStringIndex(command)
		if loc == nil {
			break
		}
		// 提取定界符（去引号）
		delimMatch := re.FindStringSubmatch(command[loc[0]:])
		if len(delimMatch) < 2 {
			break
		}
		rawDelim := delimMatch[1]
		// 去引号
		delim := strings.Trim(rawDelim, "'\"")
		// 找到定界符后的第一个换行，body 从此开始
		bodyStart := strings.Index(command[loc[1]:], "\n")
		if bodyStart < 0 {
			break
		}
		bodyStart += loc[1] + 1 // 跳过换行符
		// 从 bodyStart 开始，找单独成行的结束定界符
		rest := command[bodyStart:]
		// 构建查找模式：行首 + 定界符 + 行尾
		endPattern := "(?m)^" + regexp.QuoteMeta(delim) + `\s*$`
		endRe := regexp.MustCompile(endPattern)
		endLoc := endRe.FindStringIndex(rest)
		if endLoc == nil {
			break
		}
		endPos := bodyStart + endLoc[1]
		// 替换：保留 << 起始行，删除 body + 结束定界符
		command = command[:loc[1]] + command[endPos:]
	}
	return command
}

// collapseHorizontalWhitespace 将连续的水平空白符（空格、tab）折叠为单个空格，
// 保留换行符不动。这样既修复了 "source  /dev/" 漏检问题，又不会破坏 heredoc 边界。
func collapseHorizontalWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if r == '\n' || r == '\r' {
			b.WriteRune(r)
			inSpace = false
			continue
		}
		if r == ' ' || r == '\t' {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		b.WriteRune(r)
		inSpace = false
	}
	return b.String()
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
