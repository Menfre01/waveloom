package agentloop

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/todo"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ============================================================================
// executeTodoWrite 单元测试 — 增量合并
// ============================================================================

func TestExecuteTodoWrite_NilTodoState(t *testing.T) {
	// REGRESSION: executeTodoWrite 在 TodoState == nil 时应返回错误消息，
	// 不能 panic（nil pointer dereference）。
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	loop := New(nil, registry, Config{
		TodoState: nil,
	})

	ch := make(chan TurnEvent, 1)
	result := loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_1",
		Name: "todo_write",
		Arguments: `{"todos": [{"content": "Test", "status": "pending", "activeForm": "Testing"}]}`,
	}, nil, ch)

	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Content != "todo_write is not available (TodoState not configured)." {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func TestExecuteTodoWrite_InvalidJSON(t *testing.T) {
	// REGRESSION: executeTodoWrite 在收到非法 JSON 时应返回 Recoverable 错误。
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	ts := todo.NewTodoState()
	loop := New(nil, registry, Config{TodoState: ts})

	ch := make(chan TurnEvent, 1)
	result := loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:        "call_1",
		Name:      "todo_write",
		Arguments: `{invalid json}`,
	}, nil, ch)

	if result == nil || result.Error == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if result.Error.Class != tool.ErrorClassRecoverable {
		t.Errorf("ErrorClass = %v, want Recoverable", result.Error.Class)
	}
	if result.Error.Kind != tool.ErrKindInvalidArgs {
		t.Errorf("ErrorKind = %q, want %q", result.Error.Kind, tool.ErrKindInvalidArgs)
	}
}

func TestExecuteTodoWrite_CreateNew(t *testing.T) {
	// 首次调用创建新项 — content 无匹配 → CREATE。
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	ts := todo.NewTodoState()
	loop := New(nil, registry, Config{TodoState: ts})

	ch := make(chan TurnEvent, 2)
	result := loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_1",
		Name: "todo_write",
		Arguments: `{"todos": [
			{"content": "Task A", "status": "in_progress", "activeForm": "Doing A"},
			{"content": "Task B", "status": "pending", "activeForm": "Doing B"}
		]}`,
	}, nil, ch)

	if result == nil || result.Error != nil {
		t.Fatalf("todo_write failed: %v", result)
	}
	if !contains(result.Content, "2 created") {
		t.Errorf("expected '2 created' in result, got: %s", result.Content)
	}
	if !contains(result.Content, "0 updated") {
		t.Errorf("expected '0 updated' in result, got: %s", result.Content)
	}

	// Verify TodoUpdateEvent
	select {
	case ev := <-ch:
		tue, ok := ev.(TodoUpdateEvent)
		if !ok {
			t.Fatalf("expected TodoUpdateEvent, got %T", ev)
		}
		if len(tue.Items) != 2 {
			t.Errorf("expected 2 items in event, got %d", len(tue.Items))
		}
	default:
		t.Error("expected TodoUpdateEvent to be pushed")
	}

	// Verify state
	snapshot := ts.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 items in state, got %d", len(snapshot))
	}
	if snapshot[0].Status != "in_progress" {
		t.Errorf("item 0 status = %s, want in_progress", snapshot[0].Status)
	}
}

func TestExecuteTodoWrite_UpdateByContent(t *testing.T) {
	// content 匹配 → UPDATE，content 不变。
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	ts := todo.NewTodoState()
	loop := New(nil, registry, Config{TodoState: ts})

	ch := make(chan TurnEvent, 4)

	// Call 1: create items
	_ = loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_1",
		Name: "todo_write",
		Arguments: `{"todos": [
			{"content": "Task A", "status": "in_progress", "activeForm": "Doing A"},
			{"content": "Task B", "status": "pending", "activeForm": "Doing B"}
		]}`,
	}, nil, ch)
	<-ch

	// Call 2: update by content — only send the items that changed
	result := loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_2",
		Name: "todo_write",
		Arguments: `{"todos": [
			{"content": "Task A", "status": "completed", "activeForm": "Did A"}
		]}`,
	}, nil, ch)

	if result == nil || result.Error != nil {
		t.Fatalf("todo_write failed: %v", result)
	}
	if !contains(result.Content, "1 updated") {
		t.Errorf("expected '1 updated' in result, got: %s", result.Content)
	}
	if !contains(result.Content, "1 unchanged") {
		t.Errorf("expected '1 unchanged' in result, got: %s", result.Content)
	}

	// Verify state: Task A completed, Task B still pending
	snapshot := ts.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 items, got %d", len(snapshot))
	}
	if snapshot[0].Content != "Task A" || snapshot[0].Status != "completed" {
		t.Errorf("Task A: content=%s status=%s, want completed", snapshot[0].Content, snapshot[0].Status)
	}
	if snapshot[1].Content != "Task B" || snapshot[1].Status != "pending" {
		t.Errorf("Task B: content=%s status=%s, want pending (unchanged)", snapshot[1].Content, snapshot[1].Status)
	}
}

func TestExecuteTodoWrite_UnmentionedKept(t *testing.T) {
	// 未在 params 中出现的项保持原样（无隐式删除）。
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
			{Content: "Task C", Status: "pending", ActiveForm: "Doing C"},
		},
	})

	// Only update Task A — Tasks B and C should remain
	result := ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "completed", ActiveForm: "Did A"},
		},
	})

	if result.Updated != 1 {
		t.Errorf("Updated = %d, want 1", result.Updated)
	}
	if result.Created != 0 {
		t.Errorf("Created = %d, want 0", result.Created)
	}
	if result.Unchanged != 2 {
		t.Errorf("Unchanged = %d, want 2", result.Unchanged)
	}

	snapshot := ts.Snapshot()
	if len(snapshot) != 3 {
		t.Fatalf("expected 3 items, got %d", len(snapshot))
	}
	// All three should still exist
	contents := make(map[string]string)
	for _, item := range snapshot {
		contents[item.Content] = item.Status
	}
	if contents["Task A"] != "completed" {
		t.Errorf("Task A should be completed, got %s", contents["Task A"])
	}
	if contents["Task B"] != "pending" {
		t.Errorf("Task B should be pending (unchanged), got %s", contents["Task B"])
	}
	if contents["Task C"] != "pending" {
		t.Errorf("Task C should be pending (unchanged), got %s", contents["Task C"])
	}
}

func TestExecuteTodoWrite_ContentImmutable(t *testing.T) {
	// content 创建后不可修改 — 即使 UPDATE 传入不同 status，content 不变。
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Original", Status: "pending", ActiveForm: "Waiting"},
		},
	})

	// Try to "update" with same content — it should UPDATE, not create a duplicate
	result := ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Original", Status: "in_progress", ActiveForm: "Working"},
		},
	})

	if result.Updated != 1 {
		t.Errorf("Updated = %d, want 1", result.Updated)
	}
	if result.Created != 0 {
		t.Errorf("Created = %d, want 0 (same content should UPDATE, not CREATE duplicate)", result.Created)
	}

	snapshot := ts.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 item, got %d (content immutable — no duplicates)", len(snapshot))
	}
	if snapshot[0].Status != "in_progress" {
		t.Errorf("status = %s, want in_progress", snapshot[0].Status)
	}
}

func TestExecuteTodoWrite_DuplicateContentInSameCall(t *testing.T) {
	// 同调用中重复 content → 首次生效，后续跳过。
	ts := todo.NewTodoState()

	result := ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task", Status: "pending", ActiveForm: "First"},
			{Content: "Task", Status: "in_progress", ActiveForm: "Second (should be ignored)"},
		},
	})

	if result.Created != 1 {
		t.Errorf("Created = %d, want 1 (duplicate content should be deduplicated)", result.Created)
	}
	if result.Updated != 0 {
		t.Errorf("Updated = %d, want 0", result.Updated)
	}

	snapshot := ts.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 item, got %d", len(snapshot))
	}
	// First occurrence wins
	if snapshot[0].Status != "pending" {
		t.Errorf("status = %s, want pending (first occurrence wins)", snapshot[0].Status)
	}
}

func TestExecuteTodoWrite_AllDoneClearsState(t *testing.T) {
	// 全部 completed → allDone 清空列表。
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	ts := todo.NewTodoState()
	loop := New(nil, registry, Config{TodoState: ts})

	ch := make(chan TurnEvent, 4)

	// Call 1: create 2 items
	_ = loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_1",
		Name: "todo_write",
		Arguments: `{"todos": [
			{"content": "Task A", "status": "in_progress", "activeForm": "Doing A"},
			{"content": "Task B", "status": "pending", "activeForm": "Doing B"}
		]}`,
	}, nil, ch)
	<-ch

	// Call 2: mark both completed — only need to send the items changing status
	result := loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_2",
		Name: "todo_write",
		Arguments: `{"todos": [
			{"content": "Task A", "status": "completed", "activeForm": "Did A"},
			{"content": "Task B", "status": "completed", "activeForm": "Did B"}
		]}`,
	}, nil, ch)

	if result == nil || result.Error != nil {
		t.Fatalf("todo_write failed: %v", result)
	}
	if result.Content != "All todos completed and cleared." {
		t.Errorf("unexpected result: %s", result.Content)
	}

	// State should be empty
	if len(ts.Snapshot()) != 0 {
		t.Errorf("expected empty state after allDone, got %d items", len(ts.Snapshot()))
	}
}

func TestExecuteTodoWrite_TwoCallsInSameTurn(t *testing.T) {
	// REGRESSION: 同一 turn 内连续两次 todo_write，第二次应看到第一次的结果。
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	ts := todo.NewTodoState()
	loop := New(nil, registry, Config{TodoState: ts})

	ch := make(chan TurnEvent, 4)

	// Call 1: create 3 items
	_ = loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_1",
		Name: "todo_write",
		Arguments: `{"todos": [
			{"content": "Task A", "status": "in_progress", "activeForm": "Doing A"},
			{"content": "Task B", "status": "pending", "activeForm": "Doing B"},
			{"content": "Task C", "status": "pending", "activeForm": "Doing C"}
		]}`,
	}, nil, ch)
	<-ch

	// Call 2: update Task A to completed, Task B to in_progress
	result := loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_2",
		Name: "todo_write",
		Arguments: `{"todos": [
			{"content": "Task A", "status": "completed", "activeForm": "Did A"},
			{"content": "Task B", "status": "in_progress", "activeForm": "Doing B"}
		]}`,
	}, nil, ch)

	if result == nil || result.Error != nil {
		t.Fatalf("second call failed: %v", result)
	}

	snapshot := ts.Snapshot()
	if len(snapshot) != 3 {
		t.Fatalf("expected 3 items, got %d", len(snapshot))
	}
	if snapshot[0].Content != "Task A" || snapshot[0].Status != "completed" {
		t.Errorf("Task A: content=%s status=%s, want completed", snapshot[0].Content, snapshot[0].Status)
	}
	if snapshot[1].Content != "Task B" || snapshot[1].Status != "in_progress" {
		t.Errorf("Task B: content=%s status=%s, want in_progress", snapshot[1].Content, snapshot[1].Status)
	}
	if snapshot[2].Content != "Task C" || snapshot[2].Status != "pending" {
		t.Errorf("Task C: content=%s status=%s, want pending (unchanged)", snapshot[2].Content, snapshot[2].Status)
	}
}

func TestExecuteTodoWrite_ContextCancelled(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	ts := todo.NewTodoState()
	loop := New(nil, registry, Config{TodoState: ts})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch := make(chan TurnEvent)
	result := loop.executeTodoWrite(ctx, llm.ToolCall{
		ID:   "call_1",
		Name: "todo_write",
		Arguments: `{"todos": [{"content": "Task", "status": "pending", "activeForm": "Testing"}]}`,
	}, nil, ch)

	if result == nil || result.Error == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestExecuteTodoWrite_NoOpDetection(t *testing.T) {
	// 无创建且无更新时追加 no-op 警告。
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})

	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))
	loop := New(nil, registry, Config{TodoState: ts})

	ch := make(chan TurnEvent, 2)

	// Send the same item with same status → no-op
	result := loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_1",
		Name: "todo_write",
		Arguments: `{"todos": [
			{"content": "Task A", "status": "in_progress", "activeForm": "Doing A"}
		]}`,
	}, nil, ch)

	if result == nil || result.Error != nil {
		t.Fatalf("todo_write failed: %v", result)
	}
	if !contains(result.Content, "0 created, 0 updated") {
		t.Errorf("expected '0 created, 0 updated', got: %s", result.Content)
	}
	if !contains(result.Content, "No status changes detected") {
		t.Errorf("expected no-op warning, got: %s", result.Content)
	}
}

func TestExecuteTodoWrite_MixedCreateAndUpdate(t *testing.T) {
	// 同时创建新项和更新已有项。
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
		},
	})

	result := ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "completed", ActiveForm: "Did A"},        // UPDATE
			{Content: "Task C", Status: "in_progress", ActiveForm: "Doing C"},     // CREATE
		},
	})

	if result.Updated != 1 {
		t.Errorf("Updated = %d, want 1", result.Updated)
	}
	if result.Created != 1 {
		t.Errorf("Created = %d, want 1", result.Created)
	}
	if result.Unchanged != 1 {
		t.Errorf("Unchanged = %d, want 1 (Task B)", result.Unchanged)
	}

	snapshot := ts.Snapshot()
	if len(snapshot) != 3 {
		t.Fatalf("expected 3 items, got %d", len(snapshot))
	}
}

// ============================================================================
// Apply 并发安全
// ============================================================================

func TestTodoState_ConcurrencyDataIntegrity(t *testing.T) {
	// REGRESSION: 并发 Apply + Snapshot 后应保证数据完整性。
	ts := todo.NewTodoState()

	// 初始化 10 个基础项
	todos := make([]todo.TodoItem, 10)
	for i := 0; i < 10; i++ {
		todos[i] = todo.TodoItem{
			Content:    fmt.Sprintf("Base %d", i+1),
			Status:     "pending",
			ActiveForm: fmt.Sprintf("b%d", i+1),
		}
	}
	ts.Apply(todo.TodoWriteParams{Todos: todos})

	var wg sync.WaitGroup
	// 50 readers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				snap := ts.Snapshot()
				for _, item := range snap {
					if item.Content == "" {
						t.Error("item has empty content")
					}
				}
			}
		}()
	}

	// 10 writers: 标记所有项为 completed
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			todos := make([]todo.TodoItem, 10)
			for j := 0; j < 10; j++ {
				todos[j] = todo.TodoItem{
					Content:    fmt.Sprintf("Base %d", j+1),
					Status:     "completed",
					ActiveForm: "done",
				}
			}
			ts.Apply(todo.TodoWriteParams{Todos: todos})
		}()
	}

	wg.Wait()

	snap := ts.Snapshot()
	if len(snap) == 0 {
		// allDone triggered, valid
		return
	}
	for _, item := range snap {
		if item.Content == "" {
			t.Errorf("item has empty content")
		}
		if item.Status != "completed" {
			t.Errorf("item %s status = %s, want completed", item.Content, item.Status)
		}
	}
}

// ============================================================================
// todoReminderText 单元测试
// ============================================================================

func TestTodoReminderText_ContainsStalenessCount(t *testing.T) {
	summary := "## Current Todo Status\n→ Verify status accuracy before taking action.\n[pending] Task A\n"
	text := todoReminderText(summary, 4)

	if !contains(text, "4 turns since last todo_write") {
		t.Errorf("todoReminderText missing staleness count, got: %s", text)
	}
	if contains(text, "Ignore if not applicable") {
		t.Errorf("todoReminderText should NOT contain escape hatch 'Ignore if not applicable'")
	}
	if !contains(text, "todo_write NOW") {
		t.Errorf("todoReminderText should contain urgency signal 'NOW'")
	}
}

func TestTodoReminderText_DifferentStalenessValues(t *testing.T) {
	summary := "## Current Todo Status\n[pending] Task A\n"
	for _, n := range []int{2, 5, 10} {
		text := todoReminderText(summary, n)
		expected := fmt.Sprintf("%d turns since last todo_write", n)
		if !contains(text, expected) {
			t.Errorf("staleness=%d: expected %q in text, got: %s", n, expected, text)
		}
	}
}

// ============================================================================
// updateTodoCounters 单元测试
// ============================================================================

func TestUpdateTodoCounters_NoActiveTasksResetsCounters(t *testing.T) {
	ts := todo.NewTodoState()
	loop := New(nil, nil, Config{TodoState: ts})
	loop.turnsSinceLastTodoWrite = 5
	loop.turnsSinceLastTodoReminder = 3

	loop.updateTodoCounters(nil)

	if loop.turnsSinceLastTodoWrite != 0 {
		t.Errorf("turnsSinceLastTodoWrite = %d, want 0", loop.turnsSinceLastTodoWrite)
	}
	if loop.turnsSinceLastTodoReminder != 0 {
		t.Errorf("turnsSinceLastTodoReminder = %d, want 0", loop.turnsSinceLastTodoReminder)
	}
}

func TestUpdateTodoCounters_WithActiveTasksIncrements(t *testing.T) {
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})
	loop := New(nil, nil, Config{TodoState: ts})
	loop.turnsSinceLastTodoWrite = 1
	loop.turnsSinceLastTodoReminder = 1

	loop.updateTodoCounters(nil)

	if loop.turnsSinceLastTodoWrite != 2 {
		t.Errorf("turnsSinceLastTodoWrite = %d, want 2", loop.turnsSinceLastTodoWrite)
	}
	if loop.turnsSinceLastTodoReminder != 2 {
		t.Errorf("turnsSinceLastTodoReminder = %d, want 2", loop.turnsSinceLastTodoReminder)
	}
}

// ============================================================================
// maybeInjectTodoReminder 单元测试
// ============================================================================

func TestMaybeInjectTodoReminder_BelowThresholdNoInject(t *testing.T) {
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})
	loop := New(nil, nil, Config{TodoState: ts})
	loop.turnsSinceLastTodoWrite = 1
	loop.turnsSinceLastTodoReminder = 0

	state := &LoopState{Messages: []llm.Message{}}
	loop.maybeInjectTodoReminder(state)

	if len(state.Messages) != 0 {
		t.Error("should NOT inject reminder when below idleTodoWrite threshold")
	}
	if loop.turnsSinceLastTodoReminder != 0 {
		t.Errorf("turnsSinceLastTodoReminder = %d, want 0", loop.turnsSinceLastTodoReminder)
	}
}

func TestMaybeInjectTodoReminder_AtThresholdInjects(t *testing.T) {
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})
	loop := New(nil, nil, Config{TodoState: ts})
	loop.turnsSinceLastTodoWrite = 2
	loop.turnsSinceLastTodoReminder = 2

	state := &LoopState{Messages: []llm.Message{}}
	loop.maybeInjectTodoReminder(state)

	if len(state.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(state.Messages))
	}
	if !strings.HasPrefix(state.Messages[0].Content, "## Current Todo Status") {
		t.Error("injected reminder should start with '## Current Todo Status'")
	}
	if !contains(state.Messages[0].Content, "2 turns since last todo_write") {
		t.Error("reminder should contain staleness count")
	}
	if loop.turnsSinceLastTodoReminder != 0 {
		t.Errorf("turnsSinceLastTodoReminder = %d, want 0", loop.turnsSinceLastTodoReminder)
	}
	if loop.turnsSinceLastTodoWrite != 2 {
		t.Errorf("turnsSinceLastTodoWrite = %d, want 2", loop.turnsSinceLastTodoWrite)
	}
}

func TestMaybeInjectTodoReminder_ReminderIntervalEnforced(t *testing.T) {
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})
	loop := New(nil, nil, Config{TodoState: ts})
	loop.turnsSinceLastTodoWrite = 3
	loop.turnsSinceLastTodoReminder = 1

	state := &LoopState{Messages: []llm.Message{}}
	loop.maybeInjectTodoReminder(state)

	if len(state.Messages) != 0 {
		t.Error("should NOT inject reminder when reminder interval not yet reached")
	}
}

func TestMaybeInjectTodoReminder_NoTasksSkips(t *testing.T) {
	ts := todo.NewTodoState()
	loop := New(nil, nil, Config{TodoState: ts})
	loop.turnsSinceLastTodoWrite = 10
	loop.turnsSinceLastTodoReminder = 10

	state := &LoopState{Messages: []llm.Message{}}
	loop.maybeInjectTodoReminder(state)

	if len(state.Messages) != 0 {
		t.Error("should NOT inject reminder when no active tasks")
	}
}

func TestMaybeInjectTodoReminder_UpdatesExistingSlot(t *testing.T) {
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})
	loop := New(nil, nil, Config{TodoState: ts})
	loop.turnsSinceLastTodoWrite = 2
	loop.turnsSinceLastTodoReminder = 2

	state := &LoopState{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "## Current Todo Status\n[pending] Old Task\n"},
	}}
	loop.maybeInjectTodoReminder(state)

	if len(state.Messages) != 2 {
		t.Errorf("expected 2 messages (append), got %d", len(state.Messages))
	}
	if !contains(state.Messages[0].Content, "Old Task") {
		t.Error("old todo-status should remain unchanged")
	}
	if !contains(state.Messages[1].Content, "todo_write NOW") {
		t.Error("appended reminder should contain 'todo_write NOW'")
	}
}

// ============================================================================
// injectTodoStatus 单元测试
// ============================================================================

func TestInjectTodoStatus_NoTasksSkips(t *testing.T) {
	ts := todo.NewTodoState()
	loop := New(nil, nil, Config{TodoState: ts})
	msgs := []llm.Message{}
	loop.injectTodoStatus(&msgs)
	if len(msgs) != 0 {
		t.Error("should not inject status when no active tasks")
	}
}

func TestInjectTodoStatus_AppendsWhenNoSlot(t *testing.T) {
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})
	loop := New(nil, nil, Config{TodoState: ts})
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "system prompt"},
	}
	loop.injectTodoStatus(&msgs)

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[1].Role != llm.RoleUser {
		t.Errorf("injected message role = %s, want user", msgs[1].Role)
	}
	if !contains(msgs[1].Content, "Current Todo Status") {
		t.Error("injected message should contain todo status header")
	}
}

func TestInjectTodoStatus_UpdatesExistingSlot(t *testing.T) {
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task B", Status: "in_progress", ActiveForm: "Doing B"},
		},
	})
	loop := New(nil, nil, Config{TodoState: ts})
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "system prompt"},
		{Role: llm.RoleUser, Content: "## Current Todo Status\n[pending] Task A\n"},
	}
	loop.injectTodoStatus(&msgs)

	if len(msgs) != 3 {
		t.Errorf("expected 3 messages (append), got %d", len(msgs))
	}
	if !contains(msgs[1].Content, "Task A") {
		t.Error("old todo-status should remain unchanged")
	}
	if !contains(msgs[2].Content, "Task B") {
		t.Error("appended status should contain Task B")
	}
}

func TestInjectTodoStatus_NoNilTodoState(t *testing.T) {
	loop := New(nil, nil, Config{TodoState: nil})
	msgs := []llm.Message{}
	loop.injectTodoStatus(&msgs)
	// 不 panic 即通过
}

// ============================================================================
// todo_write 执行后计数器重置
// ============================================================================

func TestExecuteTodoWrite_ResetsReminderCounters(t *testing.T) {
	ts := todo.NewTodoState()
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))
	loop := New(nil, registry, Config{TodoState: ts})
	loop.turnsSinceLastTodoWrite = 7
	loop.turnsSinceLastTodoReminder = 5
	loop.lastChanceTodoInjected = true

	ch := make(chan TurnEvent, 2)
	result := loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_1",
		Name: "todo_write",
		Arguments: `{"todos": [
			{"content": "Task A", "status": "in_progress", "activeForm": "Doing A"}
		]}`,
	}, nil, ch)

	if result == nil || result.Error != nil {
		t.Fatalf("todo_write failed: %v", result)
	}
	if loop.turnsSinceLastTodoWrite != 0 {
		t.Errorf("turnsSinceLastTodoWrite = %d, want 0", loop.turnsSinceLastTodoWrite)
	}
	if loop.turnsSinceLastTodoReminder != 0 {
		t.Errorf("turnsSinceLastTodoReminder = %d, want 0", loop.turnsSinceLastTodoReminder)
	}
	if loop.lastChanceTodoInjected {
		t.Error("lastChanceTodoInjected should be false after successful todo_write")
	}
}

func TestTodoLastChanceText_Format(t *testing.T) {
	summary := "## Current Todo Status\n[in_progress] Fix the bug\n"
	text := todoLastChanceText(summary)

	if !strings.Contains(text, summary) {
		t.Errorf("todoLastChanceText should contain the summary, got: %s", text)
	}
	if !strings.Contains(text, "You are about to finish") {
		t.Errorf("todoLastChanceText should contain 'You are about to finish'")
	}
	if !strings.Contains(text, "incomplete tasks") {
		t.Errorf("todoLastChanceText should contain 'incomplete tasks'")
	}
	if !strings.Contains(text, "last automatic reminder") {
		t.Errorf("todoLastChanceText should contain 'last automatic reminder'")
	}
}

func TestExecuteTodoWrite_ResetsLastChanceFlag(t *testing.T) {
	ts := todo.NewTodoState()
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))
	loop := New(nil, registry, Config{TodoState: ts})
	loop.lastChanceTodoInjected = true

	ch := make(chan TurnEvent, 2)
	result := loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_1",
		Name: "todo_write",
		Arguments: `{"todos": [
			{"content": "Task A", "status": "completed", "activeForm": "Did A"}
		]}`,
	}, nil, ch)

	if result == nil || result.Error != nil {
		t.Fatalf("todo_write failed: %v", result)
	}
	if loop.lastChanceTodoInjected {
		t.Error("lastChanceTodoInjected should be false after successful todo_write")
	}
}

func TestExecuteTodoWrite_UpdateByIDWithDifferentContent(t *testing.T) {
	// ID 优先匹配：即使 content 有差异，只要 ID 匹配就应该 UPDATE 而非 CREATE。
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	ts := todo.NewTodoState()
	loop := New(nil, registry, Config{TodoState: ts})

	ch := make(chan TurnEvent, 4)

	// Call 1: create 2 items — 第二个未完成任务防止 allDone 清空
	_ = loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_1",
		Name: "todo_write",
		Arguments: `{"todos": [
			{"content": "Fix the login bug", "status": "in_progress", "activeForm": "Fixing login"},
			{"content": "Keep this pending", "status": "pending", "activeForm": "Keeping pending"}
		]}`,
	}, nil, ch)

	// Drain event
	ev := <-ch
	tue, ok := ev.(TodoUpdateEvent)
	if !ok {
		t.Fatalf("expected TodoUpdateEvent, got %T", ev)
	}
	if len(tue.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(tue.Items))
	}
	assignedID := tue.Items[0].ID
	if assignedID == "" {
		t.Fatal("expected non-empty ID on created item")
	}

	// Call 2: update by ID — content slightly different, should still UPDATE
	result := loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_2",
		Name: "todo_write",
		Arguments: fmt.Sprintf(`{"todos": [
			{"id": %q, "content": "Fix login bug", "status": "completed", "activeForm": "Fixed login"}
		]}`, assignedID),
	}, nil, ch)

	if result == nil || result.Error != nil {
		t.Fatalf("todo_write failed: %v", result)
	}
	if !contains(result.Content, "1 updated") {
		t.Errorf("expected '1 updated' in result, got: %s", result.Content)
	}
	if !contains(result.Content, "0 created") {
		t.Errorf("expected '0 created' in result, got: %s", result.Content)
	}

	// Verify: still 2 items (no duplicate), first one completed
	snapshot := ts.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 items, got %d (no duplicate)", len(snapshot))
	}
	if snapshot[0].ID != assignedID {
		t.Errorf("ID = %s, want %s", snapshot[0].ID, assignedID)
	}
	if snapshot[0].Status != "completed" {
		t.Errorf("status = %s, want completed", snapshot[0].Status)
	}
	// Content should be immutable — original content preserved
	if snapshot[0].Content != "Fix the login bug" {
		t.Errorf("content = %s, want 'Fix the login bug' (original, immutable)", snapshot[0].Content)
	}
}
