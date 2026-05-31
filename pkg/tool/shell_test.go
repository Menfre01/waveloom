package tool

import (
	"context"
	"strings"
	"testing"
)

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

func TestShellTimeout(t *testing.T) {
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
		"search_file", "grep", "ls",
	}
	for _, toolName := range expectedMentions {
		if !strings.Contains(desc, toolName) {
			t.Errorf("Description should mention %s as preferred over shell", toolName)
		}
	}
	// Description 应引导 LLM 使用 working_dir 而非 cd 前缀
	if !strings.Contains(desc, "working_dir") {
		t.Error("Description should mention working_dir to discourage cd prefix")
	}
	if !strings.Contains(desc, "cd ") {
		t.Error("Description should explicitly warn against cd prefix")
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
			gotCmd, gotDir := NormalizeShellCommand(tt.command)
			if gotCmd != tt.wantCmd {
				t.Errorf("command = %q, want %q", gotCmd, tt.wantCmd)
			}
			if gotDir != tt.wantDir {
				t.Errorf("dir = %q, want %q", gotDir, tt.wantDir)
			}
		})
	}
}
