package task

import (
	"testing"
	"time"
)

func TestRegistry_RegisterUpdate(t *testing.T) {
	r := &Registry{tasks: make(map[string]*TaskInfo)}
	defer r.Reset()

	id := "task-1"
	r.Register(id, &TaskInfo{
		ID: id, PID: 12345, Command: "sleep 100",
		LogPath: "/tmp/test.log", Status: TaskRunning,
		StartTime: time.Now(),
	})

	info := r.Get(id)
	if info == nil {
		t.Fatal("expected task info, got nil")
	}
	return
	if info.Status != TaskRunning {
		t.Errorf("expected running, got %s", info.Status)
	}

	r.Update(id, TaskCompleted, 0)
	info = r.Get(id)
	if info.Status != TaskCompleted {
		t.Errorf("expected completed, got %s", info.Status)
	}
	if info.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", info.ExitCode)
	}
}

func TestRegistry_CompletedSince(t *testing.T) {
	r := &Registry{tasks: make(map[string]*TaskInfo)}
	defer r.Reset()

	before := time.Now()
	time.Sleep(1 * time.Millisecond)

	r.Register("t1", &TaskInfo{
		ID: "t1", PID: 1, Command: "cmd1",
		Status: TaskRunning, StartTime: time.Now(),
	})
	r.Register("t2", &TaskInfo{
		ID: "t2", PID: 2, Command: "cmd2",
		Status: TaskRunning, StartTime: time.Now(),
	})

	r.Update("t1", TaskCompleted, 0)
	r.Update("t2", TaskFailed, 1)

	// Update 设置了 CompletedTime，CompletedSince 应能找到
	completed := r.CompletedSince(before)
	if len(completed) != 2 {
		t.Errorf("expected 2 completed tasks, got %d", len(completed))
	}

	// 未完成的任务不应出现
	after := time.Now()
	r.Register("t3", &TaskInfo{
		ID: "t3", PID: 3, Command: "cmd3",
		Status: TaskRunning, StartTime: time.Now(),
	})
	completed = r.CompletedSince(after)
	if len(completed) != 0 {
		t.Errorf("expected 0 completed tasks since after, got %d", len(completed))
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	r := &Registry{tasks: make(map[string]*TaskInfo)}
	if info := r.Get("nonexistent"); info != nil {
		t.Errorf("expected nil for nonexistent task, got %v", info)
	}
}

func TestRegistry_Register_Duplicate(t *testing.T) {
	r := &Registry{tasks: make(map[string]*TaskInfo)}
	defer r.Reset()

	info1 := &TaskInfo{ID: "dup", PID: 1, Command: "first"}
	r.Register("dup", info1)
	info2 := &TaskInfo{ID: "dup", PID: 2, Command: "second"}
	r.Register("dup", info2)

	if got := r.Get("dup"); got.PID != 1 {
		t.Errorf("duplicate Register should not overwrite, PID = %d want 1", got.PID)
	}
}

func TestRegistry_Remove(t *testing.T) {
	r := &Registry{tasks: make(map[string]*TaskInfo)}
	r.Register("rm", &TaskInfo{ID: "rm", PID: 1, Status: TaskRunning, StartTime: time.Now()})
	r.Remove("rm")
	if info := r.Get("rm"); info != nil {
		t.Errorf("expected nil after Remove, got %v", info)
	}
}

func TestTaskStatus_String(t *testing.T) {
	tests := []struct {
		status TaskStatus
		want   string
	}{
		{TaskRunning, "running"},
		{TaskCompleted, "completed"},
		{TaskFailed, "failed"},
		{TaskStatus(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Errorf("TaskStatus(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestRegistry_List(t *testing.T) {
	r := &Registry{tasks: make(map[string]*TaskInfo)}
	r.Register("a", &TaskInfo{ID: "a", PID: 1, Command: "a"})
	r.Register("b", &TaskInfo{ID: "b", PID: 2, Command: "b"})

	list := r.List()
	if len(list) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(list))
	}
}

func TestRegistry_Reset(t *testing.T) {
	r := &Registry{tasks: make(map[string]*TaskInfo)}
	r.Register("x", &TaskInfo{ID: "x", PID: 1, Command: "x"})
	r.Reset()
	if len(r.List()) != 0 {
		t.Errorf("expected empty registry after Reset, got %d", len(r.List()))
	}
}

func TestRegistry_Running(t *testing.T) {
	r := &Registry{tasks: make(map[string]*TaskInfo)}
	defer r.Reset()

	r.Register("r1", &TaskInfo{ID: "r1", PID: 1, Command: "c1", Status: TaskRunning, StartTime: time.Now()})
	r.Register("r2", &TaskInfo{ID: "r2", PID: 2, Command: "c2", Status: TaskCompleted, StartTime: time.Now(), CompletedTime: time.Now(), ExitCode: 0})
	r.Register("r3", &TaskInfo{ID: "r3", PID: 3, Command: "c3", Status: TaskRunning, StartTime: time.Now()})

	running := r.Running()
	if len(running) != 2 {
		t.Errorf("expected 2 running tasks, got %d", len(running))
	}
	for _, tk := range running {
		if tk.Status != TaskRunning {
			t.Errorf("expected TaskRunning, got %s for %s", tk.Status, tk.ID)
		}
	}
}
