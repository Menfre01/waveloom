package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
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
