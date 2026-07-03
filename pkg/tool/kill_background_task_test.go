package tool

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/task"
)

func TestKillBackgroundTask_NotFound(t *testing.T) {
	kt := &KillBackgroundTask{}
	result, err := kt.Execute(context.Background(), KillBackgroundTaskParams{TaskID: "nonexistent"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for nonexistent task")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("expected invalid_args, got %q", result.Error.Kind)
	}
}

func TestKillBackgroundTask_AlreadyCompleted(t *testing.T) {
	task.DefaultRegistry.Reset()
	defer task.DefaultRegistry.Reset()

	now := time.Now()
	task.DefaultRegistry.Register("done-task", &task.TaskInfo{
		ID: "done-task", PID: 1, Command: "echo done",
		Status: task.TaskCompleted, StartTime: now,
		CompletedTime: now, ExitCode: 0,
	})

	kt := &KillBackgroundTask{}
	result, err := kt.Execute(context.Background(), KillBackgroundTaskParams{TaskID: "done-task"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Errorf("unexpected error for already completed task: %s", result.Error.Message)
	}
	if !strings.Contains(result.Content, "already completed") {
		t.Errorf("expected 'already completed' message, got: %s", result.Content)
	}
}

func TestKillBackgroundTask_InvalidPID(t *testing.T) {
	task.DefaultRegistry.Reset()
	defer task.DefaultRegistry.Reset()

	task.DefaultRegistry.Register("zero-pid", &task.TaskInfo{
		ID: "zero-pid", PID: 0, Command: "ghost",
		Status: task.TaskRunning, StartTime: time.Now(),
	})

	kt := &KillBackgroundTask{}
	result, err := kt.Execute(context.Background(), KillBackgroundTaskParams{TaskID: "zero-pid"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for invalid PID")
	}
}

func TestKillBackgroundTask_Kill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("kill via signal is Unix-specific")
	}

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
		cmd.Wait()
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
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
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
	}
	if info.Status == task.TaskRunning {
		t.Errorf("expected task to be completed/failed after kill, got status=%s", info.Status)
	}
}

func TestKillBackgroundTask_NameSchema(t *testing.T) {
	kt := &KillBackgroundTask{}
	if kt.Name() != "kill_background_task" {
		t.Errorf("Name() = %q, want 'kill_background_task'", kt.Name())
	}
	if kt.ConcurrentSafe() != true {
		t.Error("kill_background_task should be concurrent safe")
	}
	schema := kt.Schema()
	if !strings.Contains(string(schema), "task_id") {
		t.Errorf("schema should contain 'task_id': %s", string(schema))
	}
}
