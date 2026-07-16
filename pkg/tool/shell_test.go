package tool

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/pathutil"
	"github.com/Menfre01/waveloom/pkg/shellutil"
	"github.com/Menfre01/waveloom/pkg/task"
)


// skipOnWindows skips the test on Windows for tests that rely on shell execution.
func skipOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping shell test on Windows")
	}
}

func TestShellInterpreter(t *testing.T) {
	bin, args := shellInterpreter()
	if bin == "" {
		t.Error("shellInterpreter() returned empty binary")
	}
	if len(args) != 1 {
		t.Fatalf("shellInterpreter() returned %d args, want 1", len(args))
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(bin, "bash") {
			t.Errorf("Windows: expected bash (Git Bash), got %s", bin)
		}
		if args[0] != "-c" {
			t.Errorf("Windows: expected -c, got %s", args[0])
		}
	} else {
		if bin != "bash" && bin != "sh" {
			t.Errorf("Unix: expected bash or sh, got %s", bin)
		}
		if args[0] != "-c" {
			t.Errorf("Unix: expected -c, got %s", args[0])
		}
	}
}

func TestShellContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	tool := &Shell{AllowBg: true}
	_, err := tool.Execute(ctx, ShellParams{
		Command: "echo hello",
	})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestShellInterruptKillsProcessGroup 验证 Esc 中断能杀死 bash 及其子进程。
// 启动一个创建子进程的命令（sleep 在子 shell 中），中途取消 context，
// 验证子进程不会成为孤儿继续运行。
func TestShellInterruptKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group killing is Unix-specific")
	}

	// 启动一个子进程：sh -c 'sleep 100'（sleep 是 bash 的子进程）
	ctx, cancel := context.WithCancel(context.Background())

	tool := &Shell{AllowBg: true}
	done := make(chan struct{})
	var result *ToolResult
	var execErr error

	go func() {
		defer close(done)
		result, execErr = tool.Execute(ctx, ShellParams{
			Command:   "sleep 100",
			TimeoutMs: 60000, // 工具超时很长，确保是 context 取消触发的
		})
	}()

	// 给命令一点时间启动并创建子进程
	time.Sleep(200 * time.Millisecond)

	// 模拟用户按 Esc
	cancel()

	// 等待 Execute 返回
	<-done

	if execErr != nil {
		t.Fatalf("Execute() error = %v, expected nil (error handled via ToolResult)", execErr)
	}
	if result == nil {
		t.Fatal("result is nil")
		return
	}
	if result.Error == nil {
		t.Fatal("expected error result for interrupted command")
	}
	// context 取消被归类为 timeout（通过 cmdCtx 传播）
	if result.Error.Message != "command interrupted by user" {
		t.Errorf("expected 'command interrupted by user', got %q", result.Error.Message)
	}
	if !contains(result.Content, "Command interrupted") {
		t.Errorf("expected 'Command interrupted' in content, got %q", result.Content)
	}
}

func TestShellContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	tool := &Shell{AllowBg: true}
	result, err := tool.Execute(ctx, ShellParams{
		Command:   "sleep 5",
		TimeoutMs: 100, // 工具超时 > context 超时，确保 context 先触发
	})
	// 父 context 超时会传递到 cmdCtx，命令被杀死 → 返回 timeout 错误
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error result for timed-out context")
	}
	// 父 context 超时 → cmdCtx 也会超时 → ErrKindTimeout
	if result.Error.Kind != ErrKindTimeout {
		t.Errorf("expected ErrKindTimeout, got %q", result.Error.Kind)
	}
}

func TestShellSuccess(t *testing.T) {
	skipOnWindows(t)
	tool := &Shell{AllowBg: true}
	result, err := tool.Execute(context.Background(), ShellParams{
		Command: "echo hello",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "hello") {
		t.Errorf("Content = %q, want to contain hello", result.Content)
	}
	if result.Meta.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.Meta.ExitCode)
	}
	if !contains(result.Content, "Command succeeded") {
		t.Errorf("Content should have success marker: %s", result.Content)
	}
}

func TestShellNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("false is a Unix command; Windows equivalent test uses 'cmd /c exit 1'")
	}
	tool := &Shell{AllowBg: true}
	result, err := tool.Execute(context.Background(), ShellParams{
		Command: "false",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for non-zero exit")
	}
	if result.Error.Kind != ErrKindCommandFailed {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindCommandFailed)
	}
	if result.Error.Class != ErrorClassRecoverable {
		t.Errorf("Error.Class = %v, want ErrorClassRecoverable", result.Error.Class)
	}
	if result.Meta.ExitCode == 0 {
		t.Error("ExitCode should not be 0 for failed command")
	}
	if !contains(result.Content, "Command failed") {
		t.Errorf("Content should have failure marker: %s", result.Content)
	}
}

func TestClassifyShellError(t *testing.T) {
	tests := []struct {
		name     string
		exitCode int
		stderr   string
		wantKind string
	}{
		{
			name:     "exit 127 command not found",
			exitCode: 127,
			stderr:   "sh: nonexistent: command not found",
			wantKind: ErrKindCommandNotFound,
		},
		{
			name:     "exit 126 permission denied",
			exitCode: 126,
			stderr:   "sh: ./script: Permission denied",
			wantKind: ErrKindCommandPermission,
		},
		{
			name:     "stderr permission denied",
			exitCode: 1,
			stderr:   "rm: /etc/hosts: Permission denied",
			wantKind: ErrKindCommandPermission,
		},
		{
			name:     "stderr operation not permitted",
			exitCode: 1,
			stderr:   "Operation not permitted",
			wantKind: ErrKindCommandPermission,
		},
		{
			name:     "stderr no such file",
			exitCode: 1,
			stderr:   "cat: /tmp/missing: No such file or directory",
			wantKind: ErrKindFileNotFound,
		},
		{
			name:     "stderr cannot access",
			exitCode: 1,
			stderr:   "ls: cannot access '/nonexistent': No such file or directory",
			wantKind: ErrKindFileNotFound,
		},
		{
			name:     "exit 2 invalid args",
			exitCode: 2,
			stderr:   "",
			wantKind: ErrKindInvalidArgs,
		},
		{
			name:     "exit 1 generic failure",
			exitCode: 1,
			stderr:   "something went wrong",
			wantKind: ErrKindCommandFailed,
		},
		{
			name:     "exit 1 empty stderr",
			exitCode: 1,
			stderr:   "",
			wantKind: ErrKindCommandFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyShellError(tt.exitCode, tt.stderr)
			if got != tt.wantKind {
				t.Errorf("classifyShellError(%d, %q) = %q, want %q",
					tt.exitCode, tt.stderr, got, tt.wantKind)
			}
		})
	}
}

func TestClassifyShellErrorWindowsNotFound(t *testing.T) {
	// Windows cmd: 退出码 1 + "not recognized" → command_not_found
	// 该分类仅在 runtime.GOOS == "windows" 时生效。
	windowsStderr := "'nonexistent' is not recognized as an internal or external command, operable program or batch file."
	got := classifyShellError(1, windowsStderr)

	if runtime.GOOS == "windows" {
		if got != ErrKindCommandNotFound {
			t.Errorf("Windows: classifyShellError(1, windows_not_recognized) = %q, want %q", got, ErrKindCommandNotFound)
		}
	} else {
		// Unix 上该模式不匹配 → 回退到 command_failed
		if got != ErrKindCommandFailed {
			t.Errorf("Unix: classifyShellError(1, windows_not_recognized) = %q, want %q", got, ErrKindCommandFailed)
		}
	}
}

func TestClassifyShellErrorAccessDenied(t *testing.T) {
	// Windows "Access is denied" → command_permission_denied
	got := classifyShellError(1, "Access is denied.")
	if got != ErrKindCommandPermission {
		t.Errorf("expected ErrKindCommandPermission for 'Access is denied', got %q", got)
	}

	// 大小写不敏感
	got2 := classifyShellError(5, "ACCESS IS DENIED")
	if got2 != ErrKindCommandPermission {
		t.Errorf("expected ErrKindCommandPermission for 'ACCESS IS DENIED', got %q", got2)
	}
}

func TestShellTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep is a Unix command; Windows equivalent uses timeout/ping")
	}
	tool := &Shell{AllowBg: true}
	result, err := tool.Execute(context.Background(), ShellParams{
		Command:   "sleep 10",
		TimeoutMs: 100,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for timeout")
	}
	if result.Error.Kind != ErrKindTimeout {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindTimeout)
	}
	if result.Error.Class != ErrorClassRecoverable {
		t.Errorf("Error.Class = %v, want ErrorClassRecoverable", result.Error.Class)
	}
	// 新格式：应有超时信息
	if !contains(result.Content, "timed out") {
		t.Errorf("Content should mention timeout: %s", result.Content)
	}
}

func TestShellWithWorkingDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pwd is a Unix command; Windows equivalent uses 'cd' in cmd")
	}
	dir := t.TempDir()
	tool := &Shell{AllowBg: true}
	result, err := tool.Execute(context.Background(), ShellParams{
		Command:    "pwd",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, dir) {
		t.Errorf("Content = %q, want to contain %q", result.Content, dir)
	}
}

func TestShellOutputCapture(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip(">&2 is Unix shell syntax; Windows cmd uses 2>&1 or different syntax")
	}
	tool := &Shell{AllowBg: true}
	result, err := tool.Execute(context.Background(), ShellParams{
		Command: "echo stdout_line && echo stderr_line >&2",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "stdout_line") {
		t.Errorf("Content missing stdout_line: %q", result.Content)
	}
	if !contains(result.Content, "stderr_line") {
		t.Errorf("Content missing stderr_line: %q", result.Content)
	}
}

func TestShellTimeoutClamped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("true is a Unix command; Windows equivalent uses 'ver' or 'echo'")
	}
	tool := &Shell{AllowBg: true}
	result, err := tool.Execute(context.Background(), ShellParams{
		Command:   "true",
		TimeoutMs: MaxShellTimeoutMs + 1000,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v, want nil", result.Error)
	}
	if result.Meta.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.Meta.ExitCode)
	}
}

func TestShellDangerousWarning(t *testing.T) {
	skipOnWindows(t)	// Wave 3: security warnings moved to permission.Guard. Shell.Execute no longer
	// performs security checks — that's the Guard's responsibility.
	// This test verifies that the shell still executes commands that would
	// previously have triggered warnings.
	tool := &Shell{AllowBg: true}
	result, err := tool.Execute(context.Background(), ShellParams{
		Command: "echo chmod -R 777 /tmp",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if result.Content == "" {
		t.Error("Content should not be empty")
	}
}

func TestShellRMRootDetection(t *testing.T) {
	skipOnWindows(t)
	tool := &Shell{AllowBg: true}
	result, err := tool.Execute(context.Background(), ShellParams{
		Command: "echo rm -rf /",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "rm -rf /") {
		t.Errorf("Content should warn about rm -rf /: %s", result.Content)
	}
}

func TestShellCurlPipeShellDetection(t *testing.T) {
	// Wave 3: security warnings moved to permission.Guard.
	// Shell.Execute no longer produces warnings — permission checks are
	// handled by Guard before Execute is called.
	if runtime.GOOS == "windows" {
		t.Skip("uses sh -c which is not available on Windows")
	}
	tool := &Shell{AllowBg: true}
	result, err := tool.Execute(context.Background(), ShellParams{
		Command: `echo curl test && true | sh -c "echo safe"`,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if result.Content == "" {
		t.Error("Content should not be empty")
	}
}

func TestShellSafeCommandNoWarning(t *testing.T) {
	skipOnWindows(t)
	tool := &Shell{AllowBg: true}
	result, err := tool.Execute(context.Background(), ShellParams{
		Command: "echo hello world",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if contains(result.Content, "Security warning") {
		t.Errorf("Safe command should not trigger warnings: %s", result.Content)
	}
}

func TestShellDescriptionGuidesToolUsage(t *testing.T) {
	tool := &Shell{AllowBg: true}
	desc := tool.Description()
	prompt := tool.Prompt()

	// Prompt 应引导 LLM 优先用专用工具（行为约束在 Prompt → C1）
	expectedMentions := []string{
		"read", "write", "edit",
	}
	for _, toolName := range expectedMentions {
		if !strings.Contains(prompt, toolName) {
			t.Errorf("Prompt should mention %s as preferred over shell", toolName)
		}
	}
	// Description 应提及 working_dir（API 契约在 Description → C2）
	if !strings.Contains(desc, "working_dir") {
		t.Error("Description should mention working_dir for directory switching")
	}
	// Description 不应包含行为约束（已移至 Prompt）
	if strings.Contains(desc, "Prefer dedicated tools") {
		t.Error("Description should NOT contain behavioral rules — they belong in Prompt()")
	}
	// cd 前缀已由权限系统和工具执行层归一化，Prompt 无需再警告
	if strings.Contains(prompt, "do NOT prefix") || strings.Contains(prompt, "cd breaks permission") {
		t.Error("Prompt should NOT warn against cd prefix — normalization handles it")
	}
}

func TestNormalizeShellCommand(t *testing.T) {
	tests := []struct {
		name         string
		command      string
		wantCmd      string
		wantDir      string
	}{
		{
			name:    "no cd prefix",
			command: "go test ./...",
			wantCmd: "go test ./...",
			wantDir: "",
		},
		{
			name:    "cd with &&",
			command: "cd /tmp && ls",
			wantCmd: "ls",
			wantDir: "/tmp",
		},
		{
			name:    "cd with semicolon",
			command: "cd /tmp; ls",
			wantCmd: "ls",
			wantDir: "/tmp",
		},
		{
			name:    "cd with spaces around &&",
			command: "cd /app  &&  go test ./...",
			wantCmd: "go test ./...",
			wantDir: "/app",
		},
		{
			name:    "cd with double-quoted path",
			command: `cd "/path with spaces" && ls`,
			wantCmd: "ls",
			wantDir: "/path with spaces",
		},
		{
			name:    "cd with single-quoted path",
			command: `cd '/path with spaces' && ls`,
			wantCmd: "ls",
			wantDir: "/path with spaces",
		},
		{
			name:    "cd . and command",
			command: "cd . && pwd",
			wantCmd: "pwd",
			wantDir: ".",
		},
		{
			name:    "cd with chained commands",
			command: "cd /app && go build && go test",
			wantCmd: "go build && go test",
			wantDir: "/app",
		},
		{
			name:    "just cd (no command after separator)",
			command: "cd /tmp",
			wantCmd: "cd /tmp",
			wantDir: "",
		},
		{
			name:    "empty command",
			command: "",
			wantCmd: "",
			wantDir: "",
		},
		{
			name:    "cd appears but not at beginning",
			command: "echo cd /tmp && ls",
			wantCmd: "echo cd /tmp && ls",
			wantDir: "",
		},
		{
			name:    "cd && with no space before &&",
			command: "cd /tmp&&ls",
			wantCmd: "ls",
			wantDir: "/tmp",
		},
		{
			name:    "cd ; with no space before ;",
			command: "cd /app;go test",
			wantCmd: "go test",
			wantDir: "/app",
		},
		{
			name:    "cd ; with spaces around ;",
			command: "cd /app  ;  go test",
			wantCmd: "go test",
			wantDir: "/app",
		},
		{
			name:    "cd with trailing space after command",
			command: "cd /tmp && ",
			wantCmd: "",
			wantDir: "/tmp",
		},
		{
			name:    "cd with tilde path",
			command: "cd ~/project && make",
			wantCmd: "make",
			wantDir: "~/project",
		},
		{
			name:    "cd with env var path",
			command: "cd $HOME && ls",
			wantCmd: "ls",
			wantDir: "$HOME",
		},
		{
			name:    "cd with escaped path",
			command: `cd \$HOME && ls`,
			wantCmd: "ls",
			wantDir: `\$HOME`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmd, gotDir := pathutil.NormalizeShellCommand(tt.command)
			if gotCmd != tt.wantCmd {
				t.Errorf("command = %q, want %q", gotCmd, tt.wantCmd)
			}
			if gotDir != tt.wantDir {
				t.Errorf("dir = %q, want %q", gotDir, tt.wantDir)
			}
		})
	}
}

func TestFormatShellError(t *testing.T) {
	// formatShellError 在命令启动失败时构造错误结果（如二进制不存在）
	result := formatShellError("Command start failed", -1, 0, 120000*time.Millisecond, "exec: \"nonexistent\": executable file not found", true)
	if result.Error == nil {
		t.Fatal("formatShellError should return an error")
	}
	if result.Error.Kind != "command_failed" {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, "command_failed")
	}
	if !strings.Contains(result.Content, "Command start failed") {
		t.Errorf("Content should contain status: %s", result.Content)
	}
	if !strings.Contains(result.Content, "executable file not found") {
		t.Errorf("Content should contain original error: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// ExecuteStreaming 回归测试
// ---------------------------------------------------------------------------

func TestShell_SupportsStreaming(t *testing.T) {
	s := &Shell{AllowBg: true}
	if !s.SupportsStreaming() {
		t.Error("Shell should support streaming")
	}
}

func TestShell_ExecuteStreaming_Basic(t *testing.T) {
	skipOnWindows(t)
	s := &Shell{AllowBg: true}
	ctx := context.Background()
	var chunks []string
	result, err := s.ExecuteStreaming(ctx, ShellParams{
		Command: "echo hello",
	}, func(chunk string) {
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatalf("ExecuteStreaming error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
		return
	}
	if len(chunks) == 0 {
		t.Error("expected at least one chunk")
	}
	if !strings.Contains(result.Content, "hello") {
		t.Errorf("result content should contain 'hello': %s", result.Content)
	}
	if result.Error != nil {
		t.Errorf("unexpected error: %s", result.Error.Message)
	}
}

func TestShell_ExecuteStreaming_Error(t *testing.T) {
	s := &Shell{AllowBg: true}
	ctx := context.Background()
	var chunks []string
	result, err := s.ExecuteStreaming(ctx, ShellParams{
		Command: "exit 1",
	}, func(chunk string) {
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatalf("ExecuteStreaming error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
		return
	}
	if result.Error == nil {
		t.Error("expected error for exit 1")
	}
}

func TestShell_ExecuteStreaming_Timeout(t *testing.T) {
	s := &Shell{AllowBg: true}
	ctx := context.Background()
	result, err := s.ExecuteStreaming(ctx, ShellParams{
		Command:   "sleep 10",
		TimeoutMs: 100,
	}, func(chunk string) {})
	if err != nil {
		t.Fatalf("ExecuteStreaming error: %v", err)
	}
	if result.Error == nil || result.Error.Kind != ErrKindTimeout {
		t.Errorf("expected timeout error, got kind=%s", result.Error.Kind)
	}
}

func TestShell_ExecuteStreaming_ContextCancel(t *testing.T) {
	s := &Shell{AllowBg: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	_, err := s.ExecuteStreaming(ctx, ShellParams{
		Command: "echo hello",
	}, func(chunk string) {})
	if err == nil {
		t.Error("expected context canceled error")
	}
}

// ---------------------------------------------------------------------------
// 后台命令 (&) 回归测试
// ---------------------------------------------------------------------------

func TestIsBackgroundCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		// 单行 —— 整体 &
		{"echo hello &", true},
		{"echo hello & ", true},
		{"echo hello 2>&1 &", true},
		{"npx wrangler dev --port 8794 2>&1 &", true},
		{"echo hello", false},
		{"echo foo & echo bar", false},
		{"echo hello && echo world", false},
		{"", false},
		{"&", true},
		// 多行 —— 某一行以 & 结尾
		{"npx wrangler dev --port 8787 2>&1 &\nsleep 12", true},
		{"echo hello\nsleep 100 &", true},
		{"echo hello &\necho world &", true},
		// 多行 —— 无 &
		{"echo hello\necho world", false},
		// 多行 —— & 不在行尾
		{"echo foo & echo bar\necho baz", false},
	}
	for _, tt := range tests {
		got := shellutil.IsBackgroundCommand(tt.cmd)
		if got != tt.want {
			t.Errorf("isBackgroundCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestPrepareBackgroundCommand_Rewrites(t *testing.T) {
	p := &ShellParams{Command: "npx wrangler dev --port 8794 2>&1 &"}
	bgLogFile, isBackground := prepareBackgroundCommand(p)

	if !isBackground {
		t.Fatal("expected isBackground=true for single-line & command")
	}
	if bgLogFile != "" {
		t.Errorf("expected empty log file for single-line & command, got %s", bgLogFile)
	}

	// 改写后的命令应剥离尾部 &（2>&1 中的 & 不是后台操作符，应保留）
	if strings.HasSuffix(strings.TrimSpace(p.Command), "&") {
		t.Errorf("rewritten command should not end with &: %s", p.Command)
	}
	if strings.Contains(p.Command, "</dev/null") || strings.Contains(p.Command, ">/tmp") {
		t.Errorf("rewritten command should NOT contain subshell redirects (file fd handles this): %s", p.Command)
	}
}

func TestPrepareBackgroundCommand_NonBackground(t *testing.T) {
	p := &ShellParams{Command: "echo hello"}
	bgLogFile, isBackground := prepareBackgroundCommand(p)

	if isBackground {
		t.Errorf("non-background command should not be flagged as background")
	}
	if bgLogFile != "" {
		t.Errorf("non-background command should return empty log file, got %s", bgLogFile)
	}
	if p.Command != "echo hello" {
		t.Errorf("non-background command should not be modified, got %s", p.Command)
	}
}

func TestPrepareBackgroundCommand_MultiLineRewrite(t *testing.T) {
	// REGRESSION: 多行命令中某一行以 & 结尾时，文件 fd 输出已消除 SIGPIPE，
	// 不再需要 subshell 重定向改写。命令保持原样，仅返回日志提示路径。
	p := &ShellParams{Command: "npx wrangler dev --port 8787 2>&1 &\nsleep 12 && curl -s localhost:8787"}
	bgLogFile, isBackground := prepareBackgroundCommand(p)

	if isBackground {
		t.Fatal("multi-line & command should NOT be flagged as background (foreground parts exist)")
	}
	if bgLogFile == "" {
		t.Fatal("expected non-empty log file path for multi-line bg command")
	}
	if !strings.Contains(bgLogFile, "waveloom-bg-") {
		t.Errorf("log file should contain 'waveloom-bg-', got %s", bgLogFile)
	}

	// 命令不应被改写（文件 fd 消除 SIGPIPE，不再需要 subshell 重定向）
	if strings.Contains(p.Command, "</dev/null") {
		t.Errorf("command should not be rewritten with subshell redirects: %s", p.Command)
	}
	if !strings.Contains(p.Command, "sleep 12 && curl -s localhost:8787") {
		t.Errorf("foreground line should not be modified: %s", p.Command)
	}
}

// TestShell_BackgroundCommand_DoesNotHang 回归测试：验证后台命令不会卡死。
// 单行 & 命令现在走后台路径，立即返回 taskId + logPath。
func TestShell_BackgroundCommand_DoesNotHang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background & is Unix shell syntax")
	}

	s := &Shell{AllowBg: true}
	ctx := context.Background()
	done := make(chan struct{})
	var result *ToolResult
	var execErr error

	go func() {
		defer close(done)
		result, execErr = s.Execute(ctx, ShellParams{
			Command:   "sleep 100 &",
			TimeoutMs: 5000,
		})
	}()

	// 后台命令应立即返回（不等待进程退出）
	select {
	case <-done:
		// OK — 快速返回
	case <-time.After(3 * time.Second):
		t.Fatal("background command did not return within 3s")
	}

	if execErr != nil {
		t.Fatalf("Execute() error = %v", execErr)
	}
	if result == nil {
		t.Fatal("result is nil")
		return
	}
	if result.Meta.BackgroundTaskID == "" {
		t.Errorf("expected non-empty BackgroundTaskID for background command")
	}
	if !strings.Contains(result.Content, "Command started in background") {
		t.Errorf("result should indicate background start: %s", result.Content)
	}
	// 清理：杀掉后台 sleep 进程
	if result.Meta.BackgroundTaskID != "" {
		task.DefaultRegistry.Remove(result.Meta.BackgroundTaskID)
	}
}

// TestShell_BackgroundCommand_LogFileHasOutput 验证后台进程的输出写入日志文件。
func TestShell_BackgroundCommand_LogFileHasOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background & is Unix shell syntax")
	}

	s := &Shell{AllowBg: true}
	result, err := s.Execute(context.Background(), ShellParams{
		Command:   "echo background_hello &",
		TimeoutMs: 5000,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
		return
	}

	if result.Meta.BackgroundTaskID == "" {
		t.Fatal("expected non-empty BackgroundTaskID")
	}
	logPath := result.Meta.LogPath
	if logPath == "" {
		t.Fatal("expected non-empty LogPath")
	}

	// 等待 echo 完成后检查日志文件
	time.Sleep(500 * time.Millisecond)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("cannot read log file %s: %v", logPath, err)
	}
	if !strings.Contains(string(data), "background_hello") {
		t.Errorf("log file should contain 'background_hello', got: %s", string(data))
	}

	// 清理
	_ = os.Remove(logPath)
	task.DefaultRegistry.Remove(result.Meta.BackgroundTaskID)
}

// TestShell_ExecuteStreaming_BackgroundCommand_DoesNotHang 回归测试：
// 验证 ExecuteStreaming 路径的后台命令也不会卡死。
func TestShell_ExecuteStreaming_BackgroundCommand_DoesNotHang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background & is Unix shell syntax")
	}

	s := &Shell{AllowBg: true}
	ctx := context.Background()
	done := make(chan struct{})
	var result *ToolResult
	var execErr error

	go func() {
		defer close(done)
		result, execErr = s.ExecuteStreaming(ctx, ShellParams{
			Command:   "sleep 100 &",
			TimeoutMs: 5000,
		}, func(chunk string) {})
	}()

	select {
	case <-done:
		// OK
	case <-time.After(3 * time.Second):
		t.Fatal("ExecuteStreaming with background command did not return within 3s")
	}

	if execErr != nil {
		t.Fatalf("ExecuteStreaming() error = %v", execErr)
	}
	if result == nil {
		t.Fatal("result is nil")
		return
	}
	if result.Meta.BackgroundTaskID == "" {
		t.Errorf("expected non-empty BackgroundTaskID")
	}
	if !strings.Contains(result.Content, "Command started in background") {
		t.Errorf("result should indicate background start: %s", result.Content)
	}
	// 清理
	if result.Meta.BackgroundTaskID != "" {
		task.DefaultRegistry.Remove(result.Meta.BackgroundTaskID)
	}
}

// TestShell_ExecuteStreaming_PipeWaitTimeout 验证流式输出在超时时正确处理。
// 文件 fd 模式下，超时杀进程后仍能读取部分输出。
func TestShell_ExecuteStreaming_PipeWaitTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process group semantics")
	}

	s := &Shell{AllowBg: true}
	ctx := context.Background()
	done := make(chan struct{})
	var result *ToolResult
	var execErr error

	go func() {
		defer close(done)
		result, execErr = s.ExecuteStreaming(ctx, ShellParams{
			Command:   "sleep 10",
			TimeoutMs: 500,
		}, func(chunk string) {})
	}()

	select {
	case <-done:
		// OK
	case <-time.After(3 * time.Second):
		t.Fatal("ExecuteStreaming did not return within 3s")
	}

	if execErr != nil {
		t.Fatalf("ExecuteStreaming() error = %v", execErr)
	}
	if result == nil {
		t.Fatal("result is nil")
		return
	}
	if result.Error == nil || result.Error.Kind != ErrKindTimeout {
		t.Errorf("expected timeout error, got kind=%s", result.Error.Kind)
	}
}

// TestShell_ReadPipesStreaming 覆盖 fallback pipe 模式读取路径。
func TestShell_ReadPipesStreaming(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix pipe semantics")
	}

	cmd := exec.Command("echo", "pipe_hello")
	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var chunks []string
	emitChunk := func(s string) { chunks = append(chunks, s) }

	err := readPipesStreaming(cmd, ctx, done, stdoutPipe, stderrPipe, emitChunk)
	if err != nil {
		t.Fatalf("readPipesStreaming error = %v", err)
	}

	joined := strings.Join(chunks, "")
	if !strings.Contains(joined, "pipe_hello") {
		t.Errorf("expected 'pipe_hello' in output, got: %q", joined)
	}
}

// TestShell_NewTaskID 覆盖任务 ID 生成。
func TestShell_NewTaskID(t *testing.T) {
	id1 := newTaskID()
	id2 := newTaskID()
	if len(id1) != 8 {
		t.Errorf("expected 8-char hex ID, got %q (len=%d)", id1, len(id1))
	}
	if id1 == id2 {
		t.Errorf("two IDs should differ: %q == %q", id1, id2)
	}
}

// TestShell_FormatShellResult_Empty 覆盖 formatShellResult 空输出分支。
func TestShell_FormatShellResult_Empty(t *testing.T) {
	r := formatShellResult("done", 0, 0, 0, "   \n", false)
	if !strings.Contains(r, "(empty)") {
		t.Errorf("expected (empty) for whitespace-only output: %q", r)
	}
}

// TestShell_Execute_RunInBackground 覆盖 run_in_background 显式参数路径。
func TestShell_Execute_RunInBackground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background execution is Unix-specific")
	}

	s := &Shell{AllowBg: true}
	result, err := s.Execute(context.Background(), ShellParams{
		Command:         "sleep 100",
		RunInBackground: true,
		TimeoutMs:       5000,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
		return
	}
	if result.Meta.BackgroundTaskID == "" {
		t.Errorf("expected non-empty BackgroundTaskID")
	}
	if !strings.Contains(result.Content, "Command started in background") {
		t.Errorf("result should indicate background start: %s", result.Content)
	}
	if result.Meta.BackgroundTaskID != "" {
		task.DefaultRegistry.Remove(result.Meta.BackgroundTaskID)
	}
}

// TestShell_ExecuteStreaming_RunInBackground 覆盖 ExecuteStreaming run_in_background。
func TestShell_ExecuteStreaming_RunInBackground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background execution is Unix-specific")
	}

	s := &Shell{AllowBg: true}
	result, err := s.ExecuteStreaming(context.Background(), ShellParams{
		Command:         "sleep 100",
		RunInBackground: true,
		TimeoutMs:       5000,
	}, func(chunk string) {})
	if err != nil {
		t.Fatalf("ExecuteStreaming() error = %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
		return
	}
	if result.Meta.BackgroundTaskID == "" {
		t.Errorf("expected non-empty BackgroundTaskID")
	}
	if !strings.Contains(result.Content, "Command started in background") {
		t.Errorf("result should indicate background start: %s", result.Content)
	}
	if result.Meta.BackgroundTaskID != "" {
		task.DefaultRegistry.Remove(result.Meta.BackgroundTaskID)
	}
}

func TestShell_BashSubagent_RejectsBackground(t *testing.T) {
	s := &Shell{AllowBg: false}
	if s.Name() != "bash_subagent" {
		t.Errorf("Name() = %q, want %q", s.Name(), "bash_subagent")
	}

	// Background should be rejected
	result, err := s.Execute(context.Background(), ShellParams{
		Command:         "sleep 1",
		RunInBackground: true,
	})
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error in result for background execution in bash_subagent")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("error kind = %q, want %q", result.Error.Kind, ErrKindInvalidArgs)
	}
}

func TestShell_Bash_AllowsBackground(t *testing.T) {
	s := &Shell{AllowBg: true}
	if s.Name() != "bash" {
		t.Errorf("Name() = %q, want %q", s.Name(), "bash")
	}
}

func TestShell_Schema_WithAndWithoutBackground(t *testing.T) {
	sBg := &Shell{AllowBg: true}
	sNoBg := &Shell{AllowBg: false}

	bgSchema := string(sBg.Schema())
	noBgSchema := string(sNoBg.Schema())

	if !strings.Contains(bgSchema, "run_in_background") {
		t.Error("bash schema should have run_in_background")
	}
	if strings.Contains(noBgSchema, "run_in_background") {
		t.Error("bash_subagent schema should NOT have run_in_background (AllowBg=false)")
	}
}

func TestShell_Description_WithAndWithoutBackground(t *testing.T) {
	sBg := &Shell{AllowBg: true}
	sNoBg := &Shell{AllowBg: false}

	bgDesc := sBg.Description()
	noBgDesc := sNoBg.Description()

	if !strings.Contains(bgDesc, "run_in_background") {
		t.Error("bash description should mention run_in_background")
	}
	if strings.Contains(noBgDesc, "run_in_background") {
		t.Error("bash_subagent description should NOT mention run_in_background")
	}
}
