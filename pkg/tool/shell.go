package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
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

func (t *Shell) Name() string    { return "bash" }
func (t *Shell) Schema() json.RawMessage { return shellSchema }
func (t *Shell) ConcurrentSafe() bool    { return false }

// Description 引导 LLM 优先使用专用工具，仅在必要时使用 shell。
func (t *Shell) Description() string {
	return strings.Join([]string{
		"Execute a shell command in a subprocess. Configurable timeout (default 120s, max 600s), captures stdout and stderr.",
		"",
		"Unix/macOS uses bash -c (sh fallback), Windows uses cmd /c.",
		"Command syntax must target the correct platform (Windows does not support ; for multi-command, use &&).",
		"",
		"Prefer dedicated tools over shell:",
		"  - Read files: read_file (not cat/head/tail)",
		"  - Write files: write_file (not echo >/cat <<EOF)",
		"  - Edit files: edit_file (not sed/awk)",
		"",
		"Keep commands to a SINGLE LINE. Chain dependent commands with && — do NOT use newlines or \\ line continuation.",
		"If you absolutely must split, escape newlines as \\\\\\n in JSON (three backslashes + n).",
		"",
		"Launch multiple independent commands as parallel shell calls in a single response.",
		"Chain dependent commands with &&, not newlines.",
		"",
		"Commands already run in the workspace directory.",
		"To operate in a different directory, use the working_dir parameter.",
		"",
		"For throwaway verification scripts: prefer python, write to /tmp, and clean up after.",
		`Example: {"command":"python /tmp/check.py && rm /tmp/check.py"}`,
		"",
		"Examples:",
		`  {"command":"make build"}                                     — runs in workspace`,
		`  {"command":"ls", "working_dir":"/tmp"}                       — runs in /tmp, clean`,
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
// Unix/macOS: bash -c（优先 bash 兼容脚本中的 pipefail / local 等特性；
//             bash 不可用时回退到 sh）
// Windows:    cmd /c（始终可用，无需额外安装）
func shellInterpreter() (binary string, args []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c"}
	}
	if _, err := exec.LookPath("bash"); err == nil {
		return "bash", []string{"-c"}
	}
	return "sh", []string{"-c"}
}

// ── Execute ──

func (t *Shell) Execute(ctx context.Context, p ShellParams) (*ToolResult, error) {
	// ── Step 0: 父 context 已取消 → 提前返回 ──
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── 共享前置逻辑 ──
	cmd, cmdCtx, cancel, timeout := t.setupCommand(ctx, &p)
	defer cancel()

	// ── 缓冲区模式：提前捕获 stdout/stderr，确保进程被杀后仍可读取部分输出 ──
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// ── 启动命令并监听 context 取消 ──
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return formatShellError("Command start failed", -1, 0, timeout, err.Error(), true), nil
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var execErr error

	select {
	case <-cmdCtx.Done():
		killProcessGroup(cmd)
		<-done
		execErr = cmdCtx.Err()
	case execErr = <-done:
	}

	duration := time.Since(start)
	output := append(stdout.Bytes(), stderr.Bytes()...)

	return t.formatResult(execErr, cmdCtx, output, duration, timeout)
}

// SupportsStreaming 报告 bash 工具支持增量输出推送。
func (t *Shell) SupportsStreaming() bool { return true }

// ExecuteStreaming 执行 shell 命令并将增量输出通过 chunkCb 实时推送。
// 前置逻辑（超时、命令归一化、进程组）与 Execute 共享。
func (t *Shell) ExecuteStreaming(ctx context.Context, p ShellParams, chunkCb func(string)) (*ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── 共享前置逻辑 ──
	cmd, cmdCtx, cancel, timeout := t.setupCommand(ctx, &p)
	defer cancel()

	// ── 管道模式：使用 StdoutPipe + StderrPipe 逐行读取 ──
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return formatShellError("Command start failed", -1, 0, timeout, err.Error(), true), nil
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return formatShellError("Command start failed", -1, 0, timeout, err.Error(), true), nil
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return formatShellError("Command start failed", -1, 0, timeout, err.Error(), true), nil
	}

	// 累积完整输出用于最终 ToolResult 格式化
	var outputBuf bytes.Buffer
	var mu sync.Mutex

	// 合并 stdout + stderr 到一个 chunkCb，用 mutex 保护顺序
	emitChunk := func(s string) {
		mu.Lock()
		chunkCb(s)
		outputBuf.WriteString(s)
		mu.Unlock()
	}

	// goroutine 读取 pipe
	readPipe := func(reader io.Reader) {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024) // 1MB per line max
		for scanner.Scan() {
			emitChunk(scanner.Text() + "\n")
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); readPipe(stdoutPipe) }()
	go func() { defer wg.Done(); readPipe(stderrPipe) }()

	// 等待命令完成
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var execErr error
	select {
	case <-cmdCtx.Done():
		killProcessGroup(cmd)
		<-done
		execErr = cmdCtx.Err()
	case execErr = <-done:
	}

	// 等 pipe 读完
	wg.Wait()
	duration := time.Since(start)

	// ── 结果格式化（与 Execute 共享） ──
	return t.formatResult(execErr, cmdCtx, outputBuf.Bytes(), duration, timeout)
}

// setupCommand 构造并配置 exec.Cmd，返回 prepared 命令、context、cancel 和超时值。
// Execute 和 ExecuteStreaming 共享此前置逻辑。
func (t *Shell) setupCommand(ctx context.Context, p *ShellParams) (*exec.Cmd, context.Context, context.CancelFunc, time.Duration) {
	timeoutMs := p.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = DefaultShellTimeoutMs
	}
	if timeoutMs > MaxShellTimeoutMs {
		timeoutMs = MaxShellTimeoutMs
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)

	normalizedCmd, extractedDir := pathutil.NormalizeShellCommand(p.Command)
	if p.WorkingDir == "" && extractedDir != "" {
		p.WorkingDir = extractedDir
	}

	shellBin, shellArgs := shellInterpreter()
	args := append(shellArgs, normalizedCmd)
	cmd := exec.Command(shellBin, args...)
	if p.WorkingDir != "" {
		cmd.Dir = p.WorkingDir
	}
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	return cmd, cmdCtx, cancel, timeout
}

// formatResult 基于执行结果格式化 ToolResult。Execute 和 ExecuteStreaming 共享。
func (t *Shell) formatResult(execErr error, cmdCtx context.Context, output []byte, duration, timeout time.Duration) (*ToolResult, error) {
	exitCode := -1
	if execErr == nil {
		exitCode = 0
	} else if exitErr, ok := execErr.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}

	var content string
	var toolErr *ToolError

	switch {
	case execErr == nil:
		stdoutOutput := truncateOutput(string(output), MaxShellLines)
		content = formatShellResult("Command succeeded", exitCode, duration, timeout, stdoutOutput, false)
	case cmdCtx.Err() == context.DeadlineExceeded:
		partialOutput := truncateOutput(string(output), MaxShellLines)
		content = formatShellResult("Command timed out", exitCode, duration, timeout, partialOutput, false)
		toolErr = &ToolError{
			Class:   ErrorClassRecoverable,
			Kind:    ErrKindTimeout,
			Message: fmt.Sprintf("command timed out after %s", formatDuration(timeout)),
		}
	case cmdCtx.Err() == context.Canceled:
		partialOutput := truncateOutput(string(output), MaxShellLines)
		content = formatShellResult("Command interrupted", exitCode, duration, timeout, partialOutput, false)
		toolErr = &ToolError{
			Class:   ErrorClassRecoverable,
			Kind:    ErrKindTimeout,
			Message: "command interrupted by user",
		}
	default:
		stderrOutput := truncateOutput(string(output), MaxShellLines)
		content = formatShellResult("Command failed", exitCode, duration, timeout, stderrOutput, true)
		kind := classifyShellError(exitCode, stderrOutput)
		toolErr = &ToolError{
			Class:   ErrorClassRecoverable,
			Kind:    kind,
			Message: fmt.Sprintf("command exited with code %d", exitCode),
		}
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

// killProcessGroup 向 cmd 及其所有子进程发送 SIGKILL。
// Unix: 使用负 PID 向整个进程组发送信号。
// Windows: 回退到 os.Process.Kill（无进程组支持）。
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = cmd.Process.Kill()
		return
	}
	// 负 PID → 杀整个进程组
	pgid := cmd.Process.Pid
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		_ = cmd.Process.Kill()
	}
}

// formatShellError 构造命令执行前的错误结果（如启动失败）。
func formatShellError(status string, exitCode int, duration time.Duration, timeout time.Duration, output string, isError bool) *ToolResult {
	return &ToolResult{
		Content: formatShellResult(status, exitCode, duration, timeout, output, isError),
		Meta: ToolMeta{
			Duration: duration,
			ExitCode: exitCode,
		},
		Error: &ToolError{
			Class:   ErrorClassRecoverable,
			Kind:    ErrKindCommandFailed,
			Message: status,
		},
	}
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
