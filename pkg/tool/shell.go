package tool

import (
	_ "embed"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"unicode/utf8"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Menfre01/waveloom/pkg/pathutil"
	"github.com/Menfre01/waveloom/pkg/sandbox"
	"github.com/Menfre01/waveloom/pkg/shellutil"
	"github.com/Menfre01/waveloom/pkg/task"
)
//go:embed shell_prompt.md
var shellPrompt string

// ---------------------------------------------------------------------------
// Shell — 执行 Shell 命令
// ---------------------------------------------------------------------------

type ShellParams struct {
	Command         string `json:"command"`
	WorkingDir      string `json:"working_dir"`
	TimeoutMs       int    `json:"timeout_ms"`
	RunInBackground bool   `json:"run_in_background"` // 显式请求后台执行
}

type Shell struct {
	AllowBg     bool // true for "bash" (main agent), false for "bash_subagent"
	lastCommand string
	// lastWorkingDirIgnored 标记最近一次命令的 working_dir 被沙箱忽略
	// (setupCommand 设置,formatResult 读取后清除)——模型传了 working_dir
	// 但沙箱 chdir 钉在 workspace,若不让模型感知会在错误目录执行命令。
	lastWorkingDirIgnored bool

	// SandboxMgr 沙箱管理器(可选)。注入后,按 context 中 per-command
	// SandboxStatus 决定是否用 bwrap 包装命令;nil 或不可用 → 原样执行。
	SandboxMgr *sandbox.SandboxManager
}

func (t *Shell) Name() string {
	if t.AllowBg {
		return "bash"
	}
	return "bash_subagent"
}

var shellSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {
      "type": "string",
      "description": "Shell command to execute. Unix/macOS uses bash -c (sh fallback), Windows uses Git Bash (bash -c)."
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    },
    "timeout_ms": {
      "type": "integer",
      "description": "Timeout in milliseconds (default: 300000, max: 1800000)"
    },
    "run_in_background": {
      "type": "boolean",
      "description": "Set to true to run this command in the background. The tool returns immediately with a task ID and log path. Use read to check progress. The next turn will receive a completion notification.",
      "default": false
    }
  },
  "required": ["command"]
}`)

var shellSchemaNoBackground = json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {
      "type": "string",
      "description": "Shell command to execute. Unix/macOS uses bash -c (sh fallback), Windows uses Git Bash (bash -c)."
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    },
    "timeout_ms": {
      "type": "integer",
      "description": "Timeout in milliseconds (default: 300000, max: 1800000)"
    }
  },
  "required": ["command"]
}`)

func (t *Shell) Schema() json.RawMessage {
	if t.AllowBg {
		return shellSchema
	}
	return shellSchemaNoBackground
}

func (t *Shell) ConcurrentSafe() bool { return false }

// ToolTimeout 声明 Shell 工具的最大超时（30 分钟）。
// 这样 effectiveTimeout 会使用此值而非 Loop 默认值（5 min），// 确保用户传入的 timeout_ms 参数在 (5 min, 30 min] 区间内能正常生效。
func (t *Shell) ToolTimeout() time.Duration {
	return time.Duration(MaxShellTimeoutMs) * time.Millisecond
}

// Description 仅描述 API 契约。行为约束(使用规则、策略)见 Prompt(),
// 由 Registry.FormatToolPrompts() 注入 C1 system prompt。
func (t *Shell) Description() string {
	return "Execute a shell command in a subprocess. Rules: see system prompt ## Tool Selection."
}

// Prompt 返回 shell 使用行为约束，由 Registry.FormatToolPrompts() 注入 C1 system prompt。
// Prompt 返回使用指南，由 Registry.FormatToolPrompts() 注入 system prompt。
func (t *Shell) Prompt() string { return shellPrompt }

// ── 超时常量 ──

const (
	DefaultShellTimeoutMs = 300000
	MaxShellTimeoutMs     = 1800000
)

// ── shellInterpreter ──

// shellInterpreter 返回当前平台的 shell 解释器及其参数。
// 委托到 shellutil.ShellInterpreter，结果在首次调用时缓存。
func shellInterpreter() (binary string, args []string) {
	return shellutil.ShellInterpreter()
}

// ── Execute ──

func (t *Shell) Execute(ctx context.Context, p ShellParams) (*ToolResult, error) {
	t.lastCommand = p.Command
	// ── Step 0: 父 context 已取消 → 提前返回 ──
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── 后台命令检测与预处理 ──
	bgLogFile, isBackground := prepareBackgroundCommand(&p, t.AllowBg)
	if isBackground && !t.AllowBg {
		return &ToolResult{
			Error: &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    ErrKindInvalidArgs,
				Message: "background execution is not supported in this context",
			},
		}, nil
	}

	// ── 共享前置逻辑（文件 fd 输出） ──
	cmd, cmdCtx, cancel, timeout, outputFile, outputPath := t.setupCommand(ctx, &p)
	defer cancel()

	// 文件创建失败时 fallback 到内存 buffer
	var stdout, stderr bytes.Buffer
	useFileFD := outputFile != nil
	if useFileFD {
		defer func() { _ = outputFile.Close() }()
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}

	// ── 启动命令 ──
	start := time.Now()
	if err := cmd.Start(); err != nil {
		if useFileFD {
			_ = os.Remove(outputPath)
		}
		res := formatShellError("Command start failed", -1, 0, timeout, err.Error(), true)
		res.Content = t.appendWorkingDirHint(res.Content)
		return res, nil
	}

	// ── 后台路径：run_in_background 或单行 & 命令 ──
	if isBackground {
		taskID := newTaskID()
		logPath := outputPath
		if !useFileFD {
			logPath = bgLogFile
		}
		task.DefaultRegistry.Register(taskID, &task.TaskInfo{
			ID: taskID, PID: cmd.Process.Pid,
			Command: p.Command, LogPath: logPath,
			Status: task.TaskRunning, StartTime: time.Now(),
		})
		// 异步等待进程退出后更新状态
		go func() {
			defer func() {
				if r := recover(); r != nil {
					// 极端情况(如 outputFile.Close() panic)确保 task 不永久卡 running
					task.DefaultRegistry.Update(taskID, task.TaskFailed, -1)
				}
				if useFileFD {
					_ = outputFile.Close()
				}
			}()
			// REGRESSION: 使用 channel 解耦 cmd.Wait(),避免永不退出的进程(如
			// python -m http.server)导致 goroutine 永久泄漏。通过 select 同时监听
			// 进程退出和 shutdown 信号,确保 TUI 退出时 goroutine 能及时回收。
			type waitOutcome struct {
				exitErr error
			}
			waitCh := make(chan waitOutcome, 1)
			go func() {
				err := cmd.Wait()
				waitCh <- waitOutcome{exitErr: err}
			}()

			var werr error
			select {
			case wo := <-waitCh:
				werr = wo.exitErr
			case <-task.DefaultRegistry.Done():
				// 全局 shutdown(如 TUI 退出):杀进程后等 Wait 返回以释放 OS 资源
				KillProcessGroup(cmd)
				wo := <-waitCh
				werr = wo.exitErr
			}

			exitCode := 0
			if werr != nil {
				if exitErr, ok := werr.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					exitCode = -1
				}
			}
			status := task.TaskCompleted
			if exitCode != 0 {
				status = task.TaskFailed
			}
			task.DefaultRegistry.Update(taskID, status, exitCode)
		}()
		content := fmt.Sprintf("Command started in background.\nTask ID: %s\nLog: %s",
			taskID, logPath)
		if bgLogFile != "" {
			content += fmt.Sprintf("\n[background] log: %s", bgLogFile)
		}
		content = t.appendWorkingDirHint(content)
		return &ToolResult{
			Content: content,
			Meta: ToolMeta{
				Duration:         time.Since(start),
				ExitCode:         0,
				BackgroundTaskID: taskID,
				LogPath:          logPath,
			},
		}, nil
	}

	// ── 前台路径：等待进程退出 ──
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

	// 读取输出
	var output []byte
	if useFileFD {
		output, _ = os.ReadFile(outputPath)
		_ = os.Remove(outputPath)
	} else {
		output = append(stdout.Bytes(), stderr.Bytes()...)
	}
	output = t.annotateSandboxViolations(ctx, output)

	result, _ := t.formatResult(execErr, cmdCtx, output, duration, timeout, outputPath, "")
	result.Content = t.escapeHatchHint(ctx, execErr, result.Content)
	if bgLogFile != "" {
		result.Content += fmt.Sprintf("\n[background] log: %s", bgLogFile)
	}
	return result, nil
}

// SupportsStreaming 报告 bash 工具支持增量输出推送。
func (t *Shell) SupportsStreaming() bool { return true }

// ExecuteStreaming 执行 shell 命令并将增量输出通过 chunkCb 实时推送。
// 使用文件 polling 替代管道读取：每 500ms 读取输出文件的新增内容，// 逐行 push ToolCallStream 到 TUI。后台命令同样支持。
func (t *Shell) ExecuteStreaming(ctx context.Context, p ShellParams, chunkCb func(string)) (*ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// ── 后台命令检测与预处理 ──
	bgLogFile, isBackground := prepareBackgroundCommand(&p, t.AllowBg)
	if isBackground && !t.AllowBg {
		return &ToolResult{
			Error: &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    ErrKindInvalidArgs,
				Message: "background execution is not supported in this context",
			},
		}, nil
	}

	// ── 共享前置逻辑（文件 fd 输出） ──
	cmd, cmdCtx, cancel, timeout, outputFile, outputPath := t.setupCommand(ctx, &p)
	defer cancel()

	useFileFD := outputFile != nil
	if useFileFD {
		defer func() { _ = outputFile.Close() }()
	}

	// ── 启动命令 ──
	start := time.Now()

	// fallback pipe 模式:需在 Start 前设置 io.Pipe(cmd.Stdout/Stderr)。
	// 不用 cmd.StdoutPipe():其读端在 Wait 返回时被 os/exec 关闭,读 goroutine
	// 调度延迟时管道缓冲数据随读端关闭丢失(REGRESSION: TestShell_ReadPipesStreaming
	// 在 ubuntu-24.04 CI 稳定失败)。io.Pipe 由 os/exec 内部 copy goroutine 写入,
	// Wait 返回即数据已全部进入管道,无竞态。
	var stdoutPipe, stderrPipe io.ReadCloser
	var stdoutW, stderrW io.WriteCloser
	if !useFileFD {
		stdoutPipe, stdoutW = io.Pipe()
		stderrPipe, stderrW = io.Pipe()
		cmd.Stdout = stdoutW
		cmd.Stderr = stderrW
	}

	if err := cmd.Start(); err != nil {
		if useFileFD {
			_ = os.Remove(outputPath)
		}
		return formatShellError("Command start failed", -1, 0, timeout, err.Error(), true), nil
	}

	// ── 后台路径 ──
	if isBackground {
		taskID := newTaskID()
		logPath := outputPath
		if !useFileFD {
			logPath = bgLogFile
		}
		task.DefaultRegistry.Register(taskID, &task.TaskInfo{
			ID: taskID, PID: cmd.Process.Pid,
			Command: p.Command, LogPath: logPath,
			Status: task.TaskRunning, StartTime: time.Now(),
		})
		go func() {
			defer func() {
				if r := recover(); r != nil {
					task.DefaultRegistry.Update(taskID, task.TaskFailed, -1)
				}
				if useFileFD {
					_ = outputFile.Close()
				}
			}()
			// REGRESSION: 使用 channel 解耦 cmd.Wait(),避免永不退出的进程导致
			// goroutine 永久泄漏。通过 select 同时监听进程退出和 shutdown 信号。
			type waitOutcome struct {
				exitErr error
			}
			waitCh := make(chan waitOutcome, 1)
			go func() {
				err := cmd.Wait()
				waitCh <- waitOutcome{exitErr: err}
			}()

			var werr error
			select {
			case wo := <-waitCh:
				werr = wo.exitErr
			case <-task.DefaultRegistry.Done():
				KillProcessGroup(cmd)
				wo := <-waitCh
				werr = wo.exitErr
			}

			exitCode := 0
			if werr != nil {
				if exitErr, ok := werr.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					exitCode = -1
				}
			}
			status := task.TaskCompleted
			if exitCode != 0 {
				status = task.TaskFailed
			}
			task.DefaultRegistry.Update(taskID, status, exitCode)
		}()
		content := fmt.Sprintf("Command started in background.\nTask ID: %s\nLog: %s",
			taskID, logPath)
		if bgLogFile != "" {
			content += fmt.Sprintf("\n[background] log: %s", bgLogFile)
		}
		content = t.appendWorkingDirHint(content)
		return &ToolResult{
			Content: content,
			Meta: ToolMeta{
				Duration:         time.Since(start),
				ExitCode:         0,
				BackgroundTaskID: taskID,
				LogPath:          logPath,
			},
		}, nil
	}

	// ── 前台流式路径：文件 polling ──
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var outputBuf bytes.Buffer
	var mu sync.Mutex

	emitChunk := func(s string) {
		mu.Lock()
		chunkCb(s)
		outputBuf.WriteString(s)
		mu.Unlock()
	}

	var execErr error
	if useFileFD {
		// 文件 polling 模式：每 500ms 读取增量
		execErr = pollOutputFile(cmd, cmdCtx, done, outputFile, outputPath, emitChunk)
	} else {
		// fallback pipe 模式
		execErr = readPipesStreaming(cmd, cmdCtx, done, stdoutPipe, stderrPipe, stdoutW, stderrW, emitChunk)
	}

	duration := time.Since(start)

	var output []byte
	if useFileFD {
		output, _ = os.ReadFile(outputPath)
		_ = os.Remove(outputPath)
	} else {
		output = outputBuf.Bytes()
	}
	output = t.annotateSandboxViolations(ctx, output)

	result, _ := t.formatResult(execErr, cmdCtx, output, duration, timeout, outputPath, "")
	result.Content = t.escapeHatchHint(ctx, execErr, result.Content)
	if bgLogFile != "" {
		result.Content += fmt.Sprintf("\n[background] log: %s", bgLogFile)
	}
	return result, nil
}

// pollOutputFile 轮询输出文件的新增内容并逐行推送到 emitChunk。
// 返回命令执行的最终错误（nil 表示正常退出）。
func pollOutputFile(cmd *exec.Cmd, cmdCtx context.Context, done <-chan error, outputFile *os.File, outputPath string, emitChunk func(string)) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastOffset int64
	var execErr error
	readRemaining := func() {
		// 读取自上次偏移量以来的新增内容
		currentSize, statErr := os.Stat(outputPath)
		if statErr != nil {
			return
		}
		if currentSize.Size() <= lastOffset {
			return
		}
		f, err := os.Open(outputPath)
		if err != nil {
			return
		}
		defer func() { _ = f.Close() }()
		_, _ = f.Seek(lastOffset, io.SeekStart)
		data, _ := io.ReadAll(io.LimitReader(f, currentSize.Size()-lastOffset))
		lastOffset = currentSize.Size()
		if len(data) > 0 {
			for _, line := range strings.Split(string(data), "\n") {
				if line != "" {
					emitChunk(line + "\n")
				}
			}
		}
	}

loop:
	for {
		select {
		case <-ticker.C:
			readRemaining()
		case <-cmdCtx.Done():
			killProcessGroup(cmd)
			select {
			case <-done:
			default:
			}
			execErr = cmdCtx.Err()
			break loop
		case err := <-done:
			execErr = err
			break loop
		}
	}

	// 读取剩余输出
	readRemaining()
	return execErr
}

// readPipesStreaming 是 fallback 管道读取模式(文件创建失败时使用)。
// stdoutPipe/stderrPipe 为 io.Pipe 读端,stdoutW/stderrW 为对应写端:
// 调用方须在 Start 前将 cmd.Stdout/Stderr 设为写端(os/exec 内部 copy
// goroutine 负责写入),Wait 返回即输出已全部进入管道。
// 不使用 cmd.StdoutPipe():其读端在 Wait 返回时被 os/exec 关闭,
// 读 goroutine 调度延迟时管道缓冲数据会随读端关闭丢失。
func readPipesStreaming(cmd *exec.Cmd, cmdCtx context.Context, done <-chan error, stdoutPipe, stderrPipe io.Reader, stdoutW, stderrW io.Closer, emitChunk func(string)) error {

	var wg sync.WaitGroup
	readPipe := func(reader io.Reader) {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				emitChunk(string(buf[:n]))
			}
			if err != nil {
				return
			}
		}
	}
	wg.Add(2)
	go readPipe(stdoutPipe)
	go readPipe(stderrPipe)

	var execErr error
	select {
	case <-cmdCtx.Done():
		killProcessGroup(cmd)
		select {
		case <-done:
		default:
		}
		execErr = cmdCtx.Err()
	case execErr = <-done:
	}

	// Wait 返回 → 子进程输出已全部写入管道;关闭写端令读 goroutine EOF 退出
	_ = stdoutW.Close()
	_ = stderrW.Close()

	pipesDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(pipesDone)
	}()
	select {
	case <-pipesDone:
	case <-cmdCtx.Done():
	}

	return execErr
}

// setupCommand 构造并配置 exec.Cmd，返回 prepared 命令、context、cancel、超时值
// 以及输出文件句柄。所有 shell 命令的 stdout/stderr 合并写入同一个 O_APPEND 文件，// 消除管道 SIGPIPE 问题，并自然支持后台进程跨 turn 存活。
// Execute 和 ExecuteStreaming 共享此前置逻辑。
func (t *Shell) setupCommand(ctx context.Context, p *ShellParams) (*exec.Cmd, context.Context, context.CancelFunc, time.Duration, *os.File, string) {
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
	// ── 沙箱包装(ctx 中 per-command 状态 active 时)→ 改写为 bwrap argv ──
	var extraFiles []*os.File
	if t.SandboxMgr != nil && t.SandboxMgr.Available() && sandbox.SandboxStatusFrom(ctx).Active {
		wrapped, err := t.SandboxMgr.Transform(shellBin, args)
		if err != nil {
			// Transform 失败 → 警告 + 降级为裸命令(不静默失效)
			slog.Warn("sandbox transform failed, running unsandboxed", "error", err)
		} else {
			// 沙箱内工作目录固定为 workspace(bwrap --chdir),working_dir 参数被忽略
			if p.WorkingDir != "" {
				t.lastWorkingDirIgnored = true
				slog.Warn("sandbox: working_dir ignored inside sandbox (chdir pinned to workspace)", "working_dir", p.WorkingDir)
			}
			shellBin = wrapped[0]
			// REGRESSION: wrapped 是完整 argv(含 argv[0]=沙箱二进制),
			// exec.Command(shellBin, args...) 会重复 argv[0]——
			// sandbox-exec 收到 [sandbox-exec, sandbox-exec, -p, ...] → usage 错误,
			// bwrap 同样受影响(两个平台全部命令失败)
			args = wrapped[1:]
			extraFiles = t.SandboxMgr.ExtraFiles()
		}
	}
	cmd := exec.Command(shellBin, args...)
	if len(extraFiles) > 0 {
		cmd.ExtraFiles = append(cmd.ExtraFiles, extraFiles...)
	}
	if p.WorkingDir != "" {
		cmd.Dir = p.WorkingDir
	}
	SetSysProcAttr(cmd)

	// 创建临时输出文件（stdout/stderr 合并，O_APPEND 保证原子写入）
	outputPath := filepath.Join(pathutil.TempDir(), fmt.Sprintf("waveloom-out-%s.log", newTaskID()))
	outputFile, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		// 文件创建失败 → fallback 到内存 buffer（向后兼容）
		return cmd, cmdCtx, cancel, timeout, nil, ""
	}
	cmd.Stdout = outputFile
	cmd.Stderr = outputFile

	return cmd, cmdCtx, cancel, timeout, outputFile, outputPath
}

// annotateSandboxViolations 在命令确实被沙箱包装时,对输出追加
// <sandbox_violations> 注解(违规必须反馈,不静默失败)。
// 非沙箱命令或无违规时输出原样返回。
func (t *Shell) annotateSandboxViolations(ctx context.Context, output []byte) []byte {
	if t.SandboxMgr == nil || !sandbox.SandboxStatusFrom(ctx).Active {
		return output
	}
	return []byte(sandbox.AnnotateViolations(string(output)))
}

// escapeHatchHint 沙箱内命令失败时追加逃生舱提示
// (allowUnsandboxedCommands=true 时;提示模型可配置 excludedCommands 逃逸)。
// 五审 M3:仅在"疑似沙箱原因"时提示——输出含违规注解,或命令启动失败
// (非 ExitError,如 exec 找不到二进制);普通退出码错误(grep 无匹配、
// 语义失败)不提示,避免每次失败都噪。
func (t *Shell) escapeHatchHint(ctx context.Context, execErr error, content string) string {
	if execErr == nil || t.SandboxMgr == nil || !t.SandboxMgr.AllowUnsandboxed() {
		return content
	}
	if !sandbox.SandboxStatusFrom(ctx).Active {
		return content
	}
	if strings.Contains(content, "<sandbox_violations>") {
		return content + "\n[sandbox] command failed inside sandbox — retry by adding it to sandbox.excludedCommands, or use a different approach"
	}
	var exitErr *exec.ExitError
	if errors.As(execErr, &exitErr) {
		// 命令正常执行但退出码非 0(语义失败)→ 非沙箱原因,不提示
		return content
	}
	return content + "\n[sandbox] command failed inside sandbox — retry by adding it to sandbox.excludedCommands, or use a different approach"
}

// newTaskID 生成一个短的唯一任务 ID(8 字符 hex)。
func newTaskID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// formatResult 基于执行结果格式化 ToolResult。Execute 和 ExecuteStreaming 共享。
func (t *Shell) formatResult(execErr error, cmdCtx context.Context, output []byte, duration, timeout time.Duration, logPath string, bgTaskID string) (*ToolResult, error) {
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
			Message: fmt.Sprintf("command timed out after %s. Increase timeout_ms or simplify the command", formatDuration(timeout)),
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
		// 检查是否为语义上的"非错误"退出码（如 grep 无匹配）
		if semanticMsg := interpretShellExitCode(t.lastCommand, exitCode); semanticMsg != "" {
			// 有语义解释 → 非真实错误，不生成 ToolError
			content = formatShellResult("Command completed", exitCode, duration, timeout, stderrOutput, false)
			content += "\n" + semanticMsg
		} else {
			kind := classifyShellError(exitCode, stderrOutput)
			recovery := classifyShellRecovery(kind)
			msg := fmt.Sprintf("command exited with code %d", exitCode)
			if recovery != "" {
				msg += ". " + recovery
			}
			toolErr = &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    kind,
				Message: msg,
			}
		}
	}

	return &ToolResult{
		Content: content,
		Meta: ToolMeta{
			Duration:         duration,
			ExitCode:         exitCode,
			LogPath:          logPath,
			BackgroundTaskID: bgTaskID,
		},
		Error: toolErr,
	}, nil
}

// appendWorkingDirHint 在标志置位时向 content 追加沙箱 working_dir 忽略
// 提示并清除标志。覆盖所有返回路径(前台 formatResult / 后台立即返回 /
// 命令启动失败)——避免标志残留导致下一条命令收到陈旧提示。
func (t *Shell) appendWorkingDirHint(content string) string {
	if !t.lastWorkingDirIgnored {
		return content
	}
	t.lastWorkingDirIgnored = false
	return content + "\n<system-reminder>working_dir ignored inside sandbox — commands run pinned to the sandbox workspace root. Use absolute paths or cd within the command.</system-reminder>"
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
	KillProcessGroup(cmd)
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
	// 沿 rune 边界截断，避免在多字节字符中间切断
	for i, line := range lines {
		if len(line) > MaxLineBytes {
			truncateAt := 0
			for _, r := range line {
				next := truncateAt + utf8.RuneLen(r)
				if next > MaxLineBytes {
					break
				}
				truncateAt = next
			}
			if truncateAt == 0 {
				truncateAt = MaxLineBytes
			}
			lines[i] = line[:truncateAt] + fmt.Sprintf("... [line truncated at %d bytes]", MaxLineBytes)
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
// 1. 退出码 127 → command_not_found（Unix shell 命令不存在）
// 2. 退出码 126 → command_permission_denied（命令不可执行）
// 3. stderr 匹配 "permission denied" / "operation not permitted" / "access is denied" → command_permission_denied
// 4. stderr 匹配 "no such file" / "not found" / "cannot access" / "cannot find" → file_not_found
// 5. Windows cmd: 退出码 1 + "not recognized" → command_not_found
// 6. 退出码 2 → invalid_args（语法/参数错误）
// 7. 其他 → command_failed（通用命令失败）
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

// classifyShellRecovery 基于错误 Kind 返回恢复建议提示。
// 返回值作为 ToolError.Message 的后缀，帮助 LLM 在下一轮选择正确的排查工具。
func classifyShellRecovery(kind string) string {
	switch kind {
	case ErrKindCommandNotFound:
		return "Check the command name with 'which <cmd>' or look for the correct binary. Use 'ls' to explore project directories"
	case ErrKindFileNotFound:
		return "Verify the file path with 'ls' or 'read'. Check if the working_dir is correct"
	case ErrKindCommandPermission:
		return "Check file permissions with 'ls -la'. You may need 'chmod +x' or a different approach"
	case ErrKindInvalidArgs:
		return "Check the command syntax. Use '--help' or 'man' for usage"
	default:
		return ""
	}
}

// ── formatDuration ──

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fmin", d.Minutes())
}

// ── 后台命令处理 ──

// longSleepBackgroundThreshold 含 sleep N(N≥此值)的单行命令自动转后台执行,
// 避免模型同步阻塞等待(历史扫描:等 rate-limit 重置的 sleep 150-180 等
// 累计 8 分钟纯等待)。阈值 30s:服务器就绪探测等短等待(sleep 5-10)保持
// 前台同步,避免额外一轮往返。
const longSleepBackgroundThreshold = 30 // 秒

// sleepCmdRe 匹配"sleep 后跟后续命令"的独立 sleep(行首或 ; & | 分隔符后,
// 且 sleep 后带 && / ; / | 分隔符),带可选单位(s/m/h)。
// 要求有后续命令:纯 `sleep N`(无后续动作)保持前台同步——模型显式等待,
// 转后台无收益且破坏中断语义;`sleep N && 动作`(等待后必须执行,如
// rate-limit 重置后 curl)转后台可在等待期间并行其他步骤。
// 行首/分隔符约束避免误匹配字符串字面量(如 echo "sleep 30")与环境变量
// 前缀(VAR=x sleep 30)。
var sleepCmdRe = regexp.MustCompile(`(^|[;&|]\s*)sleep\s+(\d+)([smh]?)(\s*&&|\s*;|\s*\|)`)

// longSleepSeconds 提取命令中最大的 sleep 时长(秒)。无 sleep 命令返回 ok=false。
func longSleepSeconds(cmd string) (int, bool) {
	matches := sleepCmdRe.FindAllStringSubmatch(cmd, -1)
	maxSecs := 0
	for _, m := range matches {
		n, _ := strconv.Atoi(m[2])
		switch m[3] {
		case "m":
			n *= 60
		case "h":
			n *= 3600
		}
		if n > maxSecs {
			maxSecs = n
		}
	}
	if maxSecs == 0 {
		return 0, false
	}
	return maxSecs, true
}

// prepareBackgroundCommand 检测后台命令并决定执行模式。
// allowBg=false(bash_subagent)时不做 sleep 自动转后台——子代理无后台
// 能力,强制转后台会硬失败("background execution is not supported")。
//
// 文件 fd 输出消除了 SIGPIPE 风险，因此不再需要对命令进行 subshell 重定向改写。
// 取代策略：
// - run_in_background=true → 立即后台（返回 isBackground=true）
// - 单行命令以 & 结尾 → 剥离 & 后后台执行（返回 isBackground=true）
// - 多行命令含 & → 前台执行（bash 等待前景部分），仅标记 log 提示
//
// 返回值:bgLogFile(多行时的提示日志路径),isBackground(是否走后台路径)。
func prepareBackgroundCommand(p *ShellParams, allowBg bool) (bgLogFile string, isBackground bool) {
	trimmed := strings.TrimSpace(p.Command)

	// 显式参数优先
	if p.RunInBackground {
		return "", true
	}

	// 单行命令以 & 结尾 → 剥离 & 后作为后台命令执行
	if strings.HasSuffix(trimmed, "&") && !strings.Contains(p.Command, "\n") {
		p.Command = strings.TrimSpace(strings.TrimSuffix(trimmed, "&"))
		return "", true
	}

	// 单行命令含长 sleep(≥30s)→ 自动转后台,避免同步阻塞等待。
	// 后台路径返回 taskID + 完成通知,模型可并行执行其他步骤。
	if allowBg {
		if secs, ok := longSleepSeconds(p.Command); ok && secs >= longSleepBackgroundThreshold && !strings.Contains(p.Command, "\n") {
			p.RunInBackground = true
			return "", true
		}
	}

	// 多行命令含 & → 前景执行,但标记 log 文件路径供 agent 参考
	if shellutil.IsBackgroundCommand(p.Command) {
		logFile := filepath.Join(pathutil.TempDir(), fmt.Sprintf("waveloom-bg-%d.log", time.Now().UnixNano()))
		// 文件 fd 输出已消除 SIGPIPE，不需要 subshell 重定向改写
		return logFile, false
	}

	return "", false
}
// interpretShellExitCode 解释命令退出码的语义。
//
// 返回空字符串表示退出码是真正的错误。
func interpretShellExitCode(cmd string, exitCode int) string {
	base := extractBaseCmd(cmd)
	switch base {
	case "grep", "rg":
		if exitCode == 1 {
			return "No matches found"
		}
	case "find":
		if exitCode == 1 {
			return "Some directories were inaccessible"
		}
	case "diff":
		if exitCode == 1 {
			return "Files differ"
		}
	case "test", "[":
		if exitCode == 1 {
			return "Condition is false"
		}
	case "git":
		if exitCode == 1 {
			// git diff/grep 等子命令的 1 = 正常非零退出
			return ""
		}
	}
	return ""
}

// extractBaseCmd 从命令字符串中提取基础命令名。
func extractBaseCmd(cmd string) string {
	// 跳过空白和环境变量
	for {
		cmd = strings.TrimLeft(cmd, " \t")
		if eq := strings.IndexByte(cmd, '='); eq > 0 && eq < len(cmd) {
			if sp := strings.IndexByte(cmd[eq:], ' '); sp > 0 {
				cmd = strings.TrimLeft(cmd[eq+sp:], " \t")
				continue
			}
		}
		break
	}
	sp := strings.IndexByte(cmd, ' ')
	if sp < 0 {
		sp = len(cmd)
	}
	base := cmd[:sp]
	if slash := strings.LastIndexByte(base, '/'); slash >= 0 {
		base = base[slash+1:]
	}
	return base
}
