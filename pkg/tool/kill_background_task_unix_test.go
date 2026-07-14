//go:build !windows

package tool

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/task"
)

func TestKillBackgroundTask_Kill(t *testing.T) {
	task.DefaultRegistry.Reset()
	defer task.DefaultRegistry.Reset()

	// 启动一个 sleep 后台进程
	cmd := exec.Command("sleep", "100")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	task.DefaultRegistry.Register("kill-me", &task.TaskInfo{
		ID: "kill-me", PID: cmd.Process.Pid, Command: "sleep 100",
		Status: task.TaskRunning, StartTime: time.Now(),
	})

	// 模拟真实后台 goroutine：监听进程退出并更新状态
	go func() {
		_ = cmd.Wait()
		exitCode := 0
		if cmd.ProcessState != nil && !cmd.ProcessState.Success() {
			exitCode = -1
		}
		status := task.TaskCompleted
		if exitCode != 0 {
			status = task.TaskFailed
		}
		task.DefaultRegistry.Update("kill-me", status, exitCode)
	}()

	kt := &KillBackgroundTask{}
	result, err := kt.Execute(context.Background(), KillBackgroundTaskParams{TaskID: "kill-me"})
	if err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	if !strings.Contains(result.Content, "killed") {
		t.Errorf("expected 'killed' in result: %s", result.Content)
	}

	// 等待 goroutine 更新状态
	time.Sleep(200 * time.Millisecond)
	info := task.DefaultRegistry.Get("kill-me")
	if info == nil {
		t.Fatal("task not found after kill")
		return
	}
	if info.Status == task.TaskRunning {
		t.Errorf("expected task to be completed/failed after kill, got status=%s", info.Status)
	}
}
