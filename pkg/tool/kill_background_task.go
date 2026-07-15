package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/Menfre01/waveloom/pkg/task"
)

// ---------------------------------------------------------------------------
// KillBackgroundTask — 终止后台任务
// ---------------------------------------------------------------------------

type KillBackgroundTaskParams struct {
	TaskID string `json:"task_id"`
}

type KillBackgroundTask struct{}

func (t *KillBackgroundTask) Name() string             { return "kill_background_task" }

var killBackgroundTaskSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "task_id": {
      "type": "string",
      "description": "The task ID of the background command to kill. Obtained from the bash tool response or background-task notifications."
    }
  },
  "required": ["task_id"]
}`)

func (t *KillBackgroundTask) Schema() json.RawMessage   { return killBackgroundTaskSchema }
func (t *KillBackgroundTask) ConcurrentSafe() bool       { return true }
func (t *KillBackgroundTask) SupportsStreaming() bool    { return false }

func (t *KillBackgroundTask) Description() string {
	return strings.Join([]string{
		"Kill a running background task by its task ID.",
		"Use this to stop long-running background commands (servers, watchers) started via bash(run_in_background=true).",
		"The task ID is shown in the background-task notifications (<background-task id=\"...\"/>) and in the original bash tool response (Task ID: xxx).",
		"Call with kill_background_task(task_id=\"<id>\").",
		"",
		"On Unix, kills the entire process group (SIGKILL). On Windows, kills the process.",
		"If the task is already completed or not found, returns an appropriate message.",
	}, "\n")
}

func (t *KillBackgroundTask) Execute(ctx context.Context, p KillBackgroundTaskParams) (*ToolResult, error) {
	info := task.DefaultRegistry.Get(p.TaskID)
	if info == nil {
		return &ToolResult{
			Content: fmt.Sprintf("Task %s not found in registry.", p.TaskID),
			Error: &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    ErrKindInvalidArgs,
				Message: fmt.Sprintf("task %s not found", p.TaskID),
			},
		}, nil
	}

	if info.Status != task.TaskRunning {
		return &ToolResult{
			Content: fmt.Sprintf("Task %s is already %s (exit code %d).", p.TaskID, info.Status, info.ExitCode),
		}, nil
	}

	pid := info.PID
	if pid <= 0 {
		return &ToolResult{
			Content: fmt.Sprintf("Task %s has no valid PID (%d).", p.TaskID, pid),
			Error: &ToolError{
				Class:   ErrorClassRecoverable,
				Kind:    ErrKindCommandFailed,
				Message: fmt.Sprintf("task %s has invalid PID %d", p.TaskID, pid),
			},
		}, nil
	}

	if runtime.GOOS == "windows" {
		proc, err := os.FindProcess(pid)
		if err != nil {
			return &ToolResult{
				Content: fmt.Sprintf("Cannot find process for task %s (PID %d): %v", p.TaskID, pid, err),
				Error: &ToolError{
					Class: ErrorClassRecoverable, Kind: ErrKindCommandFailed,
					Message: fmt.Sprintf("cannot find PID %d: %v", pid, err),
				},
			}, nil
		}
		_ = proc.Kill()
	} else {
		KillProcessGroupByPID(pid)
	}

	return &ToolResult{
		Content: fmt.Sprintf("Task %s (PID %d, command: %s) killed.", p.TaskID, pid, info.Command),
	}, nil
}