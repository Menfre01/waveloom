package tool

import (
	"context"
	"strings"
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
