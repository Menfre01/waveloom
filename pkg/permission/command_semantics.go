package permission

import "strings"
// Package permission — 命令退出码语义解释（对标 Claude Code commandSemantics.ts）
//
// 许多命令使用退出码传达非错误信息。例如：
//   - grep 返回 1 表示无匹配（非错误）
//   - diff 返回 1 表示文件有差异（非错误）
//   - test/[ 返回 1 表示条件为假（非错误）

// ============================================================================
// CommandSemantic — 命令语义解释器
// ============================================================================

// CommandSemantic 定义命令退出码的解释逻辑。
type CommandSemantic func(exitCode int, stdout, stderr string) SemanticResult

// SemanticResult 包含语义解释的结果。
type SemanticResult struct {
	IsError bool   // true=真正的错误
	Message string // 人类可读的解释
}

// ============================================================================
// commandSemantics — 命令级语义表
// ============================================================================

var commandSemantics = map[string]CommandSemantic{
	// grep: 0=匹配找到, 1=无匹配, 2+=错误
	"grep": func(exitCode int, _, _ string) SemanticResult {
		return SemanticResult{
			IsError: exitCode >= 2,
			Message: grepMessage(exitCode),
		}
	},

	// rg (ripgrep): 同 grep
	"rg": func(exitCode int, _, _ string) SemanticResult {
		return SemanticResult{
			IsError: exitCode >= 2,
			Message: grepMessage(exitCode),
		}
	},

	// find: 0=成功, 1=部分成功（某些目录不可访问）, 2+=错误
	"find": func(exitCode int, _, _ string) SemanticResult {
		return SemanticResult{
			IsError: exitCode >= 2,
			Message: findMessage(exitCode),
		}
	},

	// diff: 0=无差异, 1=有差异, 2+=错误
	"diff": func(exitCode int, _, _ string) SemanticResult {
		return SemanticResult{
			IsError: exitCode >= 2,
			Message: diffMessage(exitCode),
		}
	},

	// test / [: 0=条件为真, 1=条件为假, 2+=错误
	"test": func(exitCode int, _, _ string) SemanticResult {
		return SemanticResult{
			IsError: exitCode >= 2,
			Message: testMessage(exitCode),
		}
	},
	"[": func(exitCode int, _, _ string) SemanticResult {
		return SemanticResult{
			IsError: exitCode >= 2,
			Message: testMessage(exitCode),
		}
	},

	// git: 根据子命令分发语义
	"git": func(exitCode int, stdout, stderr string) SemanticResult {
		return gitSemantic(stdout+stderr, exitCode)
	},
}

// ============================================================================
// defaultSemantic — 默认语义
// ============================================================================

func defaultSemantic(exitCode int) SemanticResult {
	if exitCode == 0 {
		return SemanticResult{IsError: false}
	}
	return SemanticResult{
		IsError: true,
		Message: "command exited with code " + itoa(exitCode),
	}
}

// ============================================================================
// 各命令的消息生成
// ============================================================================

func grepMessage(exitCode int) string {
	switch exitCode {
	case 0:
		return "matches found"
	case 1:
		return "no matches found"
	default:
		return "command failed with exit code " + itoa(exitCode)
	}
}

func findMessage(exitCode int) string {
	switch exitCode {
	case 0:
		return "find completed successfully"
	case 1:
		return "some directories were inaccessible"
	default:
		return "find failed with exit code " + itoa(exitCode)
	}
}

func diffMessage(exitCode int) string {
	switch exitCode {
	case 0:
		return "files are identical"
	case 1:
		return "files differ"
	default:
		return "diff failed with exit code " + itoa(exitCode)
	}
}

func testMessage(exitCode int) string {
	switch exitCode {
	case 0:
		return "condition is true"
	case 1:
		return "condition is false"
	default:
		return "test failed with exit code " + itoa(exitCode)
	}
}

// ============================================================================
// InterpretExitCode — 退出码解释入口
// ============================================================================

// InterpretExitCode 根据命令语义解释退出码。
//
// 参数：
//   - command: 完整命令字符串
//   - exitCode: 命令退出码
//   - stdout: 标准输出（可选）
//   - stderr: 标准错误（可选）
//
// 返回语义解释结果。
func InterpretExitCode(command string, exitCode int, stdout, stderr string) SemanticResult {
	baseCmd := extractBaseCmd(command)
	if semantic, ok := commandSemantics[baseCmd]; ok {
		return semantic(exitCode, stdout, stderr)
	}
	return defaultSemantic(exitCode)
}

// extractBaseCmd 从命令字符串中提取基础命令名。
func extractBaseCmd(cmd string) string {
	// 跳过前导空白和环境变量赋值
	for {
		cmd = trimLeft(cmd)
		if eq := indexByte(cmd, '='); eq > 0 && eq < indexByteOrLen(cmd, ' ') {
			sp := indexByteOrLen(cmd, ' ')
			cmd = cmd[sp:]
			continue
		}
		break
	}
	// 取第一个空格分隔的 token
	sp := indexByteOrLen(cmd, ' ')
	base := cmd[:sp]
	// 去掉路径前缀（/usr/bin/git → git）
	if slash := lastIndexByte(base, '/'); slash >= 0 {
		base = base[slash+1:]
	}
	return base
}

// ============================================================================
// 零分配字符串辅助函数
// ============================================================================

func trimLeft(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[i:]
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func indexByteOrLen(s string, c byte) int {
	if i := indexByte(s, c); i >= 0 {
		return i
	}
	return len(s)
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
func gitSemantic(output string, exitCode int) SemanticResult {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "diff"):
		return SemanticResult{
			IsError: exitCode >= 2,
			Message: diffMessage(exitCode),
		}
	case strings.Contains(lower, "grep"):
		return SemanticResult{
			IsError: exitCode >= 2,
			Message: grepMessage(exitCode),
		}
	default:
		return defaultSemantic(exitCode)
	}
}
