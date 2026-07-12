package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/todo"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ============================================================================
// executeTodoWrite 单元测试
// ============================================================================

func TestExecuteTodoWrite_NilTodoState(t *testing.T) {
	// REGRESSION: executeTodoWrite 在 TodoState == nil 时应返回错误消息，
	// 不能 panic（nil pointer dereference）。
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	loop := New(nil, registry, Config{
		TodoState: nil, // 显式 nil
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
	if result.Content == "" {
		t.Error("expected non-empty content for nil TodoState")
	}
	if result.Content != "todo_write is not available (TodoState not configured)." {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func TestExecuteTodoWrite_InvalidJSON(t *testing.T) {
	// REGRESSION: executeTodoWrite 在收到非法 JSON 时应返回 Recoverable 错误，
	// 供 LLM 自行修正。
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	ts := todo.NewTodoState()
	loop := New(nil, registry, Config{
		TodoState: ts,
	})

	ch := make(chan TurnEvent, 1)
	result := loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:        "call_1",
		Name:      "todo_write",
		Arguments: `{invalid json}`,
	}, nil, ch)

	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Error == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if result.Error.Class != tool.ErrorClassRecoverable {
		t.Errorf("ErrorClass = %v, want Recoverable", result.Error.Class)
	}
	if result.Error.Kind != tool.ErrKindInvalidArgs {
		t.Errorf("ErrorKind = %q, want %q", result.Error.Kind, tool.ErrKindInvalidArgs)
	}
}

func TestExecuteTodoWrite_Success(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	ts := todo.NewTodoState()
	loop := New(nil, registry, Config{
		TodoState: ts,
	})

	ch := make(chan TurnEvent, 2)
	result := loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_1",
		Name: "todo_write",
		Arguments: `{"todos": [
			{"content": "Task A", "status": "in_progress", "activeForm": "Doing A"},
			{"content": "Task B", "status": "pending", "activeForm": "Doing B"}
		]}`,
	}, nil, ch)

	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Content == "" {
		t.Error("expected non-empty result content")
	}

	// Verify TodoUpdateEvent was pushed
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
		t.Error("expected TodoUpdateEvent to be pushed to channel")
	}

	// Verify TodoState was updated
	snapshot := ts.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 items in state, got %d", len(snapshot))
	}
	if snapshot[0].Status != "in_progress" {
		t.Errorf("item 0 status = %s, want in_progress", snapshot[0].Status)
	}
}

func TestExecuteTodoWrite_AllDoneClearsState(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	ts := todo.NewTodoState()
	loop := New(nil, registry, Config{
		TodoState: ts,
	})

	ch := make(chan TurnEvent, 2)

	// Create 2 items
	_ = loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_1",
		Name: "todo_write",
		Arguments: `{"todos": [
			{"content": "Task A", "status": "in_progress", "activeForm": "Doing A"},
			{"content": "Task B", "status": "pending", "activeForm": "Doing B"}
		]}`,
	}, nil, ch)
	<-ch // drain TodoUpdateEvent

	// Mark both completed → should trigger allDone clear
	result := loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_2",
		Name: "todo_write",
		Arguments: `{"todos": [
			{"id": "1", "content": "Task A", "status": "completed", "activeForm": "Did A"},
			{"id": "2", "content": "Task B", "status": "completed", "activeForm": "Did B"}
		], "merge": true}`,
	}, nil, ch)

	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	// Result should indicate all cleared
	if result.Content != "All todos completed and cleared." {
		t.Errorf("unexpected result: %s", result.Content)
	}

	// State should be empty
	snapshot := ts.Snapshot()
	if len(snapshot) != 0 {
		t.Errorf("expected empty state after allDone, got %d items", len(snapshot))
	}

	// Event should have empty items
	select {
	case ev := <-ch:
		tue, ok := ev.(TodoUpdateEvent)
		if !ok {
			t.Fatalf("expected TodoUpdateEvent, got %T", ev)
		}
		if len(tue.Items) != 0 {
			t.Errorf("expected empty items in event, got %d", len(tue.Items))
		}
	default:
		t.Error("expected TodoUpdateEvent for allDone clear")
	}
}

func TestExecuteTodoWrite_TwoCallsInSameTurn(t *testing.T) {
	// REGRESSION: 同一 turn 内连续两次 todo_write，第二次应看到第一次的结果。
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	ts := todo.NewTodoState()
	loop := New(nil, registry, Config{
		TodoState: ts,
	})

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
	<-ch // drain event

	// Call 2: update #1 to completed (using ID from call 1 result)
	result := loop.executeTodoWrite(context.Background(), llm.ToolCall{
		ID:   "call_2",
		Name: "todo_write",
		Arguments: func() string {
			args, _ := json.Marshal(map[string]interface{}{
				"todos": []map[string]interface{}{
					{"content": "Task A", "status": "completed", "activeForm": "Did A"},
					{"content": "Task B", "status": "in_progress", "activeForm": "Doing B"},
					{"content": "Task C", "status": "pending", "activeForm": "Doing C"},
				},
			})
			return string(args)
		}(),
	}, nil, ch)

	if result == nil || result.Error != nil {
		t.Fatalf("second call failed: %v", result)
	}

	// Verify state reflects both calls
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
		t.Errorf("Task C: content=%s status=%s, want pending", snapshot[2].Content, snapshot[2].Status)
	}
}

func TestExecuteTodoWrite_ContextCancelled(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	ts := todo.NewTodoState()
	loop := New(nil, registry, Config{
		TodoState: ts,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	// 无缓冲 channel → sendEvent 在 ctx 取消时确定性返回 false
	ch := make(chan TurnEvent)
	result := loop.executeTodoWrite(ctx, llm.ToolCall{
		ID:   "call_1",
		Name: "todo_write",
		Arguments: `{"todos": [{"content": "Task", "status": "pending", "activeForm": "Testing"}]}`,
	}, nil, ch)

	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Error == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
	// State may or may not have been applied (depends on timing)
	// The key assertion: context cancellation → error, not panic
}

// ============================================================================
// 并发安全 + 数据完整性
// ============================================================================

func TestTodoState_ConcurrencyDataIntegrity(t *testing.T) {
	// REGRESSION: 并发 Apply + Snapshot 后应保证数据完整性：
	// 无重复 ID，所有项 content 非空，总数一致。
	ts := todo.NewTodoState()

	// 初始化 10 个基础项
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Base 1", Status: "pending", ActiveForm: "b1"},
			{Content: "Base 2", Status: "pending", ActiveForm: "b2"},
			{Content: "Base 3", Status: "pending", ActiveForm: "b3"},
			{Content: "Base 4", Status: "pending", ActiveForm: "b4"},
			{Content: "Base 5", Status: "pending", ActiveForm: "b5"},
			{Content: "Base 6", Status: "pending", ActiveForm: "b6"},
			{Content: "Base 7", Status: "pending", ActiveForm: "b7"},
			{Content: "Base 8", Status: "pending", ActiveForm: "b8"},
			{Content: "Base 9", Status: "pending", ActiveForm: "b9"},
			{Content: "Base 10", Status: "pending", ActiveForm: "b10"},
		},
	})

	var wg sync.WaitGroup
	// 50 readers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				snap := ts.Snapshot()
				// 不变式：所有项 content 非空
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
		go func(idx int) {
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
		}(i)
	}

	wg.Wait()

	// 最终状态：要么全部清空（allDone），要么全 completed
	snap := ts.Snapshot()
	if len(snap) == 0 {
		// allDone 触发，合法
		return
	}

	// 验证所有项 content 非空
	for _, item := range snap {
		if item.Content == "" {
			t.Errorf("item has empty content")
		}
	}

	// 所有项应为 completed
	for _, item := range snap {
		if item.Status != "completed" {
			t.Errorf("item %s status = %s, want completed", item.Content, item.Status)
		}
	}
}

// ============================================================================
// todoReminderText 单元测试
// ============================================================================

func TestTodoReminderText_ContainsStalenessCount(t *testing.T) {
	// REGRESSION: todoReminderText 应包含 staleness 计数，不包含忽略出口
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
	// staleness 计数应随传入值变化
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
	// 无活跃任务时计数器应归零
	ts := todo.NewTodoState()
	loop := New(nil, nil, Config{TodoState: ts})

	// 手动设置非零计数器
	loop.turnsSinceLastTodoWrite = 5
	loop.turnsSinceLastTodoReminder = 3

	loop.updateTodoCounters(nil)

	if loop.turnsSinceLastTodoWrite != 0 {
		t.Errorf("turnsSinceLastTodoWrite = %d, want 0 (no active tasks)", loop.turnsSinceLastTodoWrite)
	}
	if loop.turnsSinceLastTodoReminder != 0 {
		t.Errorf("turnsSinceLastTodoReminder = %d, want 0 (no active tasks)", loop.turnsSinceLastTodoReminder)
	}
}

func TestUpdateTodoCounters_WithActiveTasksIncrements(t *testing.T) {
	// 有活跃任务时两个计数器都应递增
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
	// 距上次 todo_write < idleTodoWrite → 不注入提醒
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})

	loop := New(nil, nil, Config{TodoState: ts})
	loop.turnsSinceLastTodoWrite = 1 // < idleTodoWrite(2)
	loop.turnsSinceLastTodoReminder = 0

	state := &LoopState{Messages: []llm.Message{}}
	loop.maybeInjectTodoReminder(state)

	// 不应注入任何 todo-status 消息（初始为空，未触发注入）
	if len(state.Messages) != 0 {
		t.Error("should NOT inject reminder when below idleTodoWrite threshold")
	}
	// 提醒计数器不应被重置（未触发注入）
	if loop.turnsSinceLastTodoReminder != 0 {
		t.Errorf("turnsSinceLastTodoReminder = %d, want 0 (no injection occurred)", loop.turnsSinceLastTodoReminder)
	}
}

func TestMaybeInjectTodoReminder_AtThresholdInjects(t *testing.T) {
	// 距上次 todo_write >= idleTodoWrite 且 reminder >= idleTodoReminder → 注入提醒
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})

	loop := New(nil, nil, Config{TodoState: ts})
	loop.turnsSinceLastTodoWrite = 2 // >= idleTodoWrite(2)
	loop.turnsSinceLastTodoReminder = 2 // >= idleTodoReminder(2)

	state := &LoopState{Messages: []llm.Message{}}
	loop.maybeInjectTodoReminder(state)

	// 消息应以 "## Current Todo Status" 开头
	if len(state.Messages) != 1 {
		t.Fatalf("expected 1 message after reminder injection, got %d", len(state.Messages))
	}
	if !strings.HasPrefix(state.Messages[0].Content, "## Current Todo Status") {
		t.Error("injected reminder should start with '## Current Todo Status'")
	}
	content := state.Messages[0].Content
	if !contains(content, "2 turns since last todo_write") {
		t.Errorf("reminder should contain staleness count, got: %s", content)
	}
	if !contains(content, "todo_write NOW") {
		t.Errorf("reminder should contain urgency signal 'NOW', got: %s", content)
	}
	// 提醒计数器应被重置
	if loop.turnsSinceLastTodoReminder != 0 {
		t.Errorf("turnsSinceLastTodoReminder = %d, want 0 (reset after injection)", loop.turnsSinceLastTodoReminder)
	}
	// todo_write 计数器不应被重置（提醒不能替代真正的 todo_write）
	if loop.turnsSinceLastTodoWrite != 2 {
		t.Errorf("turnsSinceLastTodoWrite = %d, want 2 (reminder does NOT reset write counter)", loop.turnsSinceLastTodoWrite)
	}
}

func TestMaybeInjectTodoReminder_ReminderIntervalEnforced(t *testing.T) {
	// 两次提醒之间至少间隔 idleTodoReminder 轮
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})

	loop := New(nil, nil, Config{TodoState: ts})
	// todo_write 计数器已超阈值，但 reminder 计数器未达间隔
	loop.turnsSinceLastTodoWrite = 3 // >= idleTodoWrite(2)
	loop.turnsSinceLastTodoReminder = 1 // < idleTodoReminder(2)

	state := &LoopState{Messages: []llm.Message{}}
	loop.maybeInjectTodoReminder(state)

	if len(state.Messages) != 0 {
		t.Error("should NOT inject reminder when reminder interval not yet reached")
	}
}

func TestMaybeInjectTodoReminder_NoTasksSkips(t *testing.T) {
	// 无活跃任务时跳过
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
	// 已有 todo-status 消息时追加新消息（Append 策略，避免破坏前缀缓存）
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})

	loop := New(nil, nil, Config{TodoState: ts})
	loop.turnsSinceLastTodoWrite = 2
	loop.turnsSinceLastTodoReminder = 2

	// 预置一条旧的 todo-status 消息
	state := &LoopState{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "## Current Todo Status\n[pending] Old Task\n"},
	}}

	loop.maybeInjectTodoReminder(state)

	// 消息数量应增加（Append 策略，不原地更新）
	if len(state.Messages) != 2 {
		t.Errorf("expected 2 messages (append), got %d", len(state.Messages))
	}
	// 旧消息内容不变
	if !contains(state.Messages[0].Content, "Old Task") {
		t.Error("old todo-status should remain unchanged (Append strategy)")
	}
	// 新消息应包含提醒
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
	// 已有 todo-status 消息时追加新消息（Append 策略，避免破坏前缀缓存）
	ts := todo.NewTodoState()
	ts.Apply(todo.TodoWriteParams{
		Todos: []todo.TodoItem{
			{Content: "Task B", Status: "in_progress", ActiveForm: "Doing B"},
		},
	})

	loop := New(nil, nil, Config{TodoState: ts})

	// 预置旧 todo-status
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "system prompt"},
		{Role: llm.RoleUser, Content: "## Current Todo Status\n[pending] Task A\n"},
	}

	loop.injectTodoStatus(&msgs)

	// 消息数量应增加（Append 策略，不原地更新）
	if len(msgs) != 3 {
		t.Errorf("expected 3 messages (append), got %d", len(msgs))
	}
	// 旧消息内容不变
	if !contains(msgs[1].Content, "Task A") {
		t.Error("old todo-status should remain unchanged (Append strategy)")
	}
	// 新消息应包含最新状态
	if !contains(msgs[2].Content, "Task B") {
		t.Error("appended status should contain Task B")
	}
	if !contains(msgs[2].Content, "Verify status accuracy before taking action") {
		t.Error("appended status should contain directive text")
	}
}

func TestInjectTodoStatus_NoNilTodoState(t *testing.T) {
	// REGRESSION: nil TodoState 不应 panic
	loop := New(nil, nil, Config{TodoState: nil})
	msgs := []llm.Message{}
	loop.injectTodoStatus(&msgs)
	// 不 panic 即通过
}

// ============================================================================
// todo_write 执行后计数器重置
// ============================================================================

func TestExecuteTodoWrite_ResetsReminderCounters(t *testing.T) {
	// REGRESSION: todo_write 成功后应重置提醒计数器
	ts := todo.NewTodoState()
	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.TodoWrite{}))

	loop := New(nil, registry, Config{TodoState: ts})

	// 设置非零计数器
	loop.turnsSinceLastTodoWrite = 7
	loop.turnsSinceLastTodoReminder = 5

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
		t.Errorf("turnsSinceLastTodoWrite = %d, want 0 after todo_write", loop.turnsSinceLastTodoWrite)
	}
	if loop.turnsSinceLastTodoReminder != 0 {
		t.Errorf("turnsSinceLastTodoReminder = %d, want 0 after todo_write", loop.turnsSinceLastTodoReminder)
	}
}


