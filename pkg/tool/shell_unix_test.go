//go:build !windows

package tool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestPollOutputFile_ContextCancel 覆盖 pollOutputFile 中 context 取消分支，
// 即 select 中 cmdCtx.Done() 在 done 之前触发的路径。
func TestPollOutputFile_ContextCancel(t *testing.T) {
	cmd := exec.Command("sleep", "100")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	outputPath := filepath.Join(t.TempDir(), "poll-cancel.log")
	outputFile, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer func() { _ = outputFile.Close() }()
	cmd.Stdout = outputFile
	cmd.Stderr = outputFile

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// 使用极短超时确保 cmdCtx.Done() 在进程退出前触发
	cmdCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	err = pollOutputFile(cmd, cmdCtx, done, outputFile, outputPath, func(s string) {})
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
	// sleep 进程应该被 killProcessGroup 杀死
	_ = cmd.Wait()
}

// TestShell_ReadPipesStreaming_Timeout 覆盖 pipe 模式超时 kill 路径。
func TestShell_ReadPipesStreaming_Timeout(t *testing.T) {
	cmd := exec.Command("sleep", "100")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	err := readPipesStreaming(cmd, ctx, done, stdoutPipe, stderrPipe, func(s string) {})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}
