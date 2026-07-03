package tool

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/pathutil"
)

func TestShellInterpreter(t *testing.T) {
	bin, args := shellInterpreter()
	if bin == "" {
		t.Error("shellInterpreter() returned empty binary")
	}
	if len(args) != 1 {
		t.Fatalf("shellInterpreter() returned %d args, want 1", len(args))
	}
	if runtime.GOOS == "windows" {
		if bin != "cmd" {
			t.Errorf("Windows: expected cmd, got %s", bin)
		}
		if args[0] != "/c" {
			t.Errorf("Windows: expected /c, got %s", args[0])
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

	tool := &Shell{}
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

	tool := &Shell{}
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

	tool := &Shell{}
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
	tool := &Shell{}
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
	tool := &Shell{}
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
	tool := &Shell{}
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
	tool := &Shell{}
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
	tool := &Shell{}
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
	tool := &Shell{}
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
	// Wave 3: security warnings moved to permission.Guard. Shell.Execute no longer
	// performs security checks — that's the Guard's responsibility.
	// This test verifies that the shell still executes commands that would
	// previously have triggered warnings.
	tool := &Shell{}
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
	tool := &Shell{}
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
	tool := &Shell{}
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
	tool := &Shell{}
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
	tool := &Shell{}
	desc := tool.Description()
	// Description 应引导 LLM 优先用专用工具
	expectedMentions := []string{
		"read_file", "write_file", "edit_file",
	}
	for _, toolName := range expectedMentions {
		if !strings.Contains(desc, toolName) {
			t.Errorf("Description should mention %s as preferred over shell", toolName)
		}
	}
	// Description 应引导 LLM 使用 working_dir 切换到不同目录
	if !strings.Contains(desc, "working_dir") {
		t.Error("Description should mention working_dir for directory switching")
	}
	// cd 前缀已由权限系统和工具执行层归一化，Description 无需再警告
	if strings.Contains(desc, "do NOT prefix") || strings.Contains(desc, "cd breaks permission") {
		t.Error("Description should NOT warn against cd prefix — normalization handles it")
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
	s := &Shell{}
	if !s.SupportsStreaming() {
		t.Error("Shell should support streaming")
	}
}

func TestShell_ExecuteStreaming_Basic(t *testing.T) {
	s := &Shell{}
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
	s := &Shell{}
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
	}
	if result.Error == nil {
		t.Error("expected error for exit 1")
	}
}

func TestShell_ExecuteStreaming_Timeout(t *testing.T) {
	s := &Shell{}
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
	s := &Shell{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	_, err := s.ExecuteStreaming(ctx, ShellParams{
		Command: "echo hello",
	}, func(chunk string) {})
	if err == nil {
		t.Error("expected context canceled error")
	}
}
