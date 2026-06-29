package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

// ---------------------------------------------------------------------------
// Shell — 执行 Shell 命令
// ---------------------------------------------------------------------------

type ShellParams struct {
	Command    string `json:"command"`
	WorkingDir string `json:"working_dir"`
	TimeoutMs  int    `json:"timeout_ms"`
}

type Shell struct{}

func (t *Shell) Name() string    { return "shell" }
func (t *Shell) Schema() json.RawMessage { return shellSchema }
func (t *Shell) ConcurrentSafe() bool    { return false }

// Description 引导 LLM 优先使用专用工具，仅在必要时使用 shell。
func (t *Shell) Description() string {
	return strings.Join([]string{
		"Execute a shell command in a subprocess. Configurable timeout (default 120s, max 600s), captures stdout and stderr.",
		"",
		"Unix/macOS uses sh -c, Windows uses cmd /c.",
		"Command syntax must target the correct platform (Windows does not support ; for multi-command, use &&).",
		"",
		"Prefer dedicated tools over shell:",
		"  - Read files: read_file (not cat/head/tail)",
		"  - Write files: write_file (not echo >/cat <<EOF)",
		"  - Edit files: edit_file (not sed/awk)",
		"  - Find files: search_file (not find)",
		"  - Search content: grep (not grep/rg)",
		"  - List directories: ls (not ls command)",
		"",
		"Launch multiple independent commands as parallel shell calls in a single response.",
		"Chain dependent commands with &&, not newlines.",
		"",
		"Commands already run in the workspace directory — cd to reach it is redundant.",
		"For a different directory, prefer the working_dir parameter (keeps commands clean).",
		"If you must use cd (e.g. chaining multiple directory changes), keep it minimal.",
		"",
		"For throwaway verification scripts: prefer python, write to /tmp, and clean up after.",
		`Example: {"command":"python /tmp/check.py && rm /tmp/check.py"}`,
		"",
		"Examples:",
		`  {"command":"make build"}                                     — runs in workspace`,
		`  {"command":"ls", "working_dir":"/tmp"}                       — runs in /tmp, clean`,
		`  {"command":"cd subdir && go test ./..."}                     — acceptable if needed`,
	}, "\n")
}

// ── 超时常量 ──

const (
	DefaultShellTimeoutMs = 120000
	MaxShellTimeoutMs     = 600000
)

// ── shellInterpreter ──

// shellInterpreter 根据当前 OS 返回用于执行 Shell 命令的解释器和参数。
//
// Unix/macOS: sh -c
// Windows:    cmd /c（始终可用，无需额外安装）
func shellInterpreter() (binary string, args []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c"}
	}
	return "sh", []string{"-c"}
}

// ── Execute ──

func (t *Shell) Execute(ctx context.Context, p ShellParams) (*ToolResult, error) {
	// ── Step 0: 父 context 已取消 → 提前返回 ──
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── Step 1: 超时设置 ──
	timeoutMs := p.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = DefaultShellTimeoutMs
	}
	if timeoutMs > MaxShellTimeoutMs {
		timeoutMs = MaxShellTimeoutMs
	}

	timeout := time.Duration(timeoutMs) * time.Millisecond
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// ── Step 2: 归一化命令（剥离 cd 前缀，提取工作目录） ──
	normalizedCmd, extractedDir := pathutil.NormalizeShellCommand(p.Command)
	if p.WorkingDir == "" && extractedDir != "" {
		p.WorkingDir = extractedDir
	}

	// ── Step 3: 构造并执行命令 ──
	shellBin, shellArgs := shellInterpreter()
	args := append(shellArgs, normalizedCmd)
	cmd := exec.CommandContext(cmdCtx, shellBin, args...)
	if p.WorkingDir != "" {
		cmd.Dir = p.WorkingDir
	}

	start := time.Now()
	output, err := cmd.CombinedOutput()
	duration := time.Since(start)

	// ── Step 4: 格式化输出 ──
	exitCode := -1
	if err == nil {
		exitCode = 0
	} else if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}

	var content string
	var toolErr *ToolError

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			partialOutput := truncateOutput(string(output), MaxShellLines)
			content = formatShellResult("Command timed out", exitCode, duration, timeout, partialOutput, false)
			toolErr = &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    ErrKindTimeout,
				Message: fmt.Sprintf("command timed out after %s", formatDuration(timeout)),
			}
		} else {
			stderrOutput := truncateOutput(string(output), MaxShellLines)
			content = formatShellResult("Command failed", exitCode, duration, timeout, stderrOutput, true)
			kind := classifyShellError(exitCode, stderrOutput)
			toolErr = &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    kind,
				Message: fmt.Sprintf("command exited with code %d", exitCode),
			}
		}
	} else {
		stdoutOutput := truncateOutput(string(output), MaxShellLines)
		content = formatShellResult("Command succeeded", exitCode, duration, timeout, stdoutOutput, false)
	}

	return &ToolResult{
		Content: content,
		Meta: ToolMeta{
			Duration: duration,
			ExitCode: exitCode,
		},
		Error: toolErr,
	}, nil
}

// ── formatShellResult ──

func formatShellResult(
	status string,
	exitCode int,
	duration time.Duration,
	timeout time.Duration,
	output string,
	isError bool,
) string {
	var buf bytes.Buffer

	// 头行：状态 + exit code + 耗时
	buf.WriteString(status)
	if exitCode >= 0 {
		fmt.Fprintf(&buf, " (exit=%d)", exitCode)
	}
	fmt.Fprintf(&buf, "  %s\n", duration.Round(time.Millisecond))

	// 超时标记
	if exitCode == -1 && isError {
		fmt.Fprintf(&buf, "   Timeout: %s\n", formatDuration(timeout))
	}

	// 输出标题
	label := "   stdout:"
	if isError {
		label = "   stderr/stdout:"
	}
	buf.WriteString(label)

	// 输出内容（有缩进）
	if strings.TrimSpace(output) == "" {
		buf.WriteString(" (empty)\n")
	} else {
		buf.WriteByte('\n')
		for _, line := range strings.Split(output, "\n") {
			buf.WriteString("     ")
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}

	return buf.String()
}

// ── truncateOutput ──

func truncateOutput(output string, maxLines int) string {
	lines := strings.Split(output, "\n")

	// 单行截断：超长行截断为 MaxLineBytes，防止单行 HTML/JSON 淹没输出
	for i, line := range lines {
		if len(line) > MaxLineBytes {
			lines[i] = line[:MaxLineBytes] + fmt.Sprintf("... [line truncated at %d bytes]", MaxLineBytes)
		}
	}

	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}

	head := maxLines / 2
	tail := maxLines / 2
	var buf bytes.Buffer
	for i, line := range lines {
		if i < head || i >= len(lines)-tail {
			buf.WriteString(line)
			buf.WriteByte('\n')
		} else if i == head {
			fmt.Fprintf(&buf, "... [truncated: %d lines omitted]\n", len(lines)-head-tail)
		}
	}
	return buf.String()
}

// ── classifyShellError ──

// classifyShellError 基于退出码和 stderr 输出确定错误 Kind。
//
// 规则（按优先级）：
//  1. 退出码 127 → command_not_found（Unix shell 命令不存在）
//  2. 退出码 126 → command_permission_denied（命令不可执行）
//  3. stderr 匹配 "permission denied" / "operation not permitted" / "access is denied" → command_permission_denied
//  4. stderr 匹配 "no such file" / "not found" / "cannot access" / "cannot find" → file_not_found
//  5. Windows cmd: 退出码 1 + "not recognized" → command_not_found
//  6. 退出码 2 → invalid_args（语法/参数错误）
//  7. 其他 → command_failed（通用命令失败）
//
// 不同 Kind 在 agentloop 中各自独立计数，避免不同类型错误相互累积触发 retry_limit。
func classifyShellError(exitCode int, fallbackOutput string) string {
	// 退出码 127: 命令未找到（Unix shell）
	if exitCode == 127 {
		return ErrKindCommandNotFound
	}
	// 退出码 126: 命令不可执行（权限问题）
	if exitCode == 126 {
		return ErrKindCommandPermission
	}

	lower := strings.ToLower(fallbackOutput)

	// Windows cmd: "not recognized as an internal or external command"
	if runtime.GOOS == "windows" && exitCode == 1 &&
		strings.Contains(lower, "not recognized") {
		return ErrKindCommandNotFound
	}

	// stderr 模式匹配
	if strings.Contains(lower, "permission denied") ||
		strings.Contains(lower, "operation not permitted") ||
		strings.Contains(lower, "not permitted") ||
		strings.Contains(lower, "access is denied") {
		return ErrKindCommandPermission
	}
	if strings.Contains(lower, "no such file") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "cannot access") ||
		strings.Contains(lower, "cannot find") {
		return ErrKindFileNotFound
	}

	// 退出码 2: 语法/参数错误
	if exitCode == 2 {
		return ErrKindInvalidArgs
	}

	return ErrKindCommandFailed
}

// ── formatDuration ──

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fmin", d.Minutes())
}
