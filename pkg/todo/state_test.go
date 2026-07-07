package todo

import (
	"sync"
	"testing"
)

func TestTodoState_Replace(t *testing.T) {
	s := NewTodoState()

	// First write
	old, new := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
			{Content: "Task C", Status: "pending", ActiveForm: "Doing C"},
		},
	})

	if len(old) != 0 {
		t.Errorf("first Apply old len = %d, want 0", len(old))
	}
	if len(new) != 3 {
		t.Fatalf("first Apply new len = %d, want 3", len(new))
	}

	// Second write — full replacement
	old2, new2 := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task X", Status: "pending", ActiveForm: "Doing X"},
		},
	})

	if len(old2) != 3 {
		t.Errorf("second Apply old len = %d, want 3", len(old2))
	}
	if len(new2) != 1 {
		t.Fatalf("second Apply new len = %d, want 1", len(new2))
	}
}

func TestTodoState_UpdateStatus(t *testing.T) {
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
		},
	})

	// Update status + add new task
	_, new := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "completed", ActiveForm: "Did A"},
			{Content: "Task B", Status: "in_progress", ActiveForm: "Doing B"},
			{Content: "Task C", Status: "pending", ActiveForm: "Doing C"},
		},
	})

	if len(new) != 3 {
		t.Fatalf("len = %d, want 3", len(new))
	}
	if new[0].Status != "completed" {
		t.Errorf("Task A status = %s, want completed", new[0].Status)
	}
	if new[1].Status != "in_progress" {
		t.Errorf("Task B status = %s, want in_progress", new[1].Status)
	}
	if new[2].Content != "Task C" {
		t.Errorf("new item = %s, want Task C", new[2].Content)
	}
}

func TestTodoState_RemoveItem(t *testing.T) {
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
			{Content: "Task C", Status: "pending", ActiveForm: "Doing C"},
		},
	})

	// Remove Task B by not including it
	_, new := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "completed", ActiveForm: "Did A"},
			{Content: "Task C", Status: "in_progress", ActiveForm: "Doing C"},
		},
	})

	if len(new) != 2 {
		t.Fatalf("after remove len = %d, want 2", len(new))
	}
	if new[0].Content != "Task A" {
		t.Errorf("first item = %s, want Task A", new[0].Content)
	}
	if new[1].Content != "Task C" {
		t.Errorf("second item = %s, want Task C", new[1].Content)
	}
}

func TestTodoState_AllDone(t *testing.T) {
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "completed", ActiveForm: "Did A"},
			{Content: "Task B", Status: "completed", ActiveForm: "Did B"},
		},
	})

	snapshot := s.Snapshot()
	if len(snapshot) != 0 {
		t.Errorf("allDone snapshot len = %d, want 0", len(snapshot))
	}

	if s.AllDone() {
		t.Error("AllDone() should be false after clear (empty list)")
	}
}

func TestTodoState_Description(t *testing.T) {
	s := NewTodoState()

	_, new := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{
				Content:     "Add dark mode",
				Status:      "in_progress",
				ActiveForm:  "Adding dark mode",
				Description: "Add a toggle button in the settings page",
			},
		},
	})

	if new[0].Description != "Add a toggle button in the settings page" {
		t.Errorf("description not preserved: %s", new[0].Description)
	}
}

func TestTodoState_EmptyNotAllDone(t *testing.T) {
	s := NewTodoState()
	if s.AllDone() {
		t.Error("AllDone() should be false for fresh empty state")
	}
}

func TestTodoState_FormatResult(t *testing.T) {
	items := []TodoItem{
		{Content: "Task A", Status: "completed", ActiveForm: "Did A"},
		{Content: "Task B", Status: "in_progress", ActiveForm: "Doing B"},
	}

	result := FormatResult(items)
	if result == "" {
		t.Error("FormatResult should not be empty")
	}
	if !contains(result, "[completed] Task A") {
		t.Errorf("FormatResult missing Task A: %s", result)
	}
	if !contains(result, "[in_progress] Task B") {
		t.Errorf("FormatResult missing Task B: %s", result)
	}
}

func TestTodoState_StatusSummary(t *testing.T) {
	s := NewTodoState()

	if summary := s.StatusSummary(); summary != "" {
		t.Errorf("empty summary should be '', got %q", summary)
	}

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})

	summary := s.StatusSummary()
	if !contains(summary, "Current Todo Status") {
		t.Error("StatusSummary missing header")
	}
	if !contains(summary, "[in_progress] Task A") {
		t.Error("StatusSummary missing task")
	}
}

func TestTodoState_Concurrency(t *testing.T) {
	s := NewTodoState()
	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Base", Status: "pending", ActiveForm: "Doing base"},
		},
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Snapshot()
			_ = s.StatusSummary()
			_ = s.AllDone()
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s.Apply(TodoWriteParams{
				Todos: []TodoItem{
					{Content: "Base", Status: "completed", ActiveForm: "Did base"},
				},
			})
		}(i)
	}

	wg.Wait()
}

func TestTodoState_ReplaceWithCustomContent(t *testing.T) {
	s := NewTodoState()

	_, new := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Custom task", Status: "pending", ActiveForm: "Doing custom"},
		},
	})

	if len(new) != 1 {
		t.Fatalf("len = %d, want 1", len(new))
	}
	if new[0].Content != "Custom task" {
		t.Errorf("Content = %s, want Custom task", new[0].Content)
	}
}

func TestTodoState_EmptyList(t *testing.T) {
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
		},
	})

	// Empty list → items cleared
	_, new := s.Apply(TodoWriteParams{
		Todos: []TodoItem{},
	})

	if len(new) != 0 {
		t.Errorf("after empty list, len = %d, want 0", len(new))
	}
}

func TestTodoState_AllDoneCount(t *testing.T) {
	s := NewTodoState()

	if s.AllDone() {
		t.Error("AllDone() should be false for empty state")
	}

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})

	if s.AllDone() {
		t.Error("AllDone() should be false with 1 in_progress item")
	}

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "completed", ActiveForm: "Did A"},
		},
	})

	if s.AllDone() {
		t.Error("AllDone() should be false after clear")
	}
}

func TestFormatResult_Empty(t *testing.T) {
	result := FormatResult(nil)
	if result != "All todos completed and cleared." {
		t.Errorf("FormatResult(nil) = %q, want 'All todos completed and cleared.'", result)
	}

	result2 := FormatResult([]TodoItem{})
	if result2 != "All todos completed and cleared." {
		t.Errorf("FormatResult([]) = %q, want 'All todos completed and cleared.'", result2)
	}
}

func TestFormatResult_WithDescription(t *testing.T) {
	items := []TodoItem{
		{Content: "Task A", Status: "completed", ActiveForm: "Did A", Description: "Long description here"},
	}
	result := FormatResult(items)
	if !contains(result, " — Long description here") {
		t.Errorf("FormatResult missing description: %s", result)
	}
}

func TestFormatResult_WithoutDescription(t *testing.T) {
	items := []TodoItem{
		{Content: "Task A", Status: "completed", ActiveForm: "Did A"},
	}
	result := FormatResult(items)
	if contains(result, " — ") {
		t.Errorf("FormatResult should not contain ' — ' when no description: %s", result)
	}
	if !contains(result, "[completed] Task A") {
		t.Error("FormatResult missing task")
	}
}

func TestTodoState_SessionResume(t *testing.T) {
	// Verify Restore() works for --resume scenario
	s := NewTodoState()
	s.Restore([]TodoItem{
		{Content: "Task A", Status: "completed", ActiveForm: "Did A"},
		{Content: "Task B", Status: "in_progress", ActiveForm: "Doing B"},
	})

	snapshot := s.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("restored len = %d, want 2", len(snapshot))
	}
	if snapshot[0].Content != "Task A" {
		t.Errorf("first item = %s", snapshot[0].Content)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
