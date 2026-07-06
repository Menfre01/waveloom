package todo

import (
	"sync"
	"testing"
)

func TestTodoState_Replace(t *testing.T) {
	s := NewTodoState()

	// First write — creates all new items
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
	if new[0].ID != "1" || new[1].ID != "2" || new[2].ID != "3" {
		t.Errorf("IDs = %s,%s,%s, want 1,2,3", new[0].ID, new[1].ID, new[2].ID)
	}

	// Second write with merge=false — full replacement, IDs reset
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
	if new2[0].ID != "1" {
		t.Errorf("after replace ID = %s, want 1 (reset)", new2[0].ID)
	}
}

func TestTodoState_Merge(t *testing.T) {
	s := NewTodoState()

	// Create initial items
	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
		},
	})

	// Merge: update status of #2, add new #3
	_, new := s.Apply(TodoWriteParams{
		Merge: true,
		Todos: []TodoItem{
			{ID: "1", Content: "Task A", Status: "completed", ActiveForm: "Did A"},
			{ID: "2", Content: "Task B", Status: "in_progress", ActiveForm: "Doing B"},
			{Content: "Task C", Status: "pending", ActiveForm: "Doing C"},
		},
	})

	if len(new) != 3 {
		t.Fatalf("merge len = %d, want 3", len(new))
	}
	if new[0].Status != "completed" {
		t.Errorf("#1 status = %s, want completed", new[0].Status)
	}
	if new[1].Status != "in_progress" {
		t.Errorf("#2 status = %s, want in_progress", new[1].Status)
	}
	if new[2].ID != "3" {
		t.Errorf("new item ID = %s, want 3", new[2].ID)
	}
}

func TestTodoState_MergeKeepUnmentioned(t *testing.T) {
	// REGRESSION: merge=true 不应删除未传入的项。只更新传入的项，其他保持不变。
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
			{Content: "Task C", Status: "pending", ActiveForm: "Doing C"},
		},
	})

	// Merge: only pass #1 and #3 — #2 should be KEPT (not deleted)
	_, new := s.Apply(TodoWriteParams{
		Merge: true,
		Todos: []TodoItem{
			{ID: "1", Content: "Task A", Status: "completed", ActiveForm: "Did A"},
			{ID: "3", Content: "Task C", Status: "in_progress", ActiveForm: "Doing C"},
		},
	})

	if len(new) != 3 {
		t.Fatalf("after merge len = %d, want 3 (#2 kept)", len(new))
	}
	if new[0].ID != "1" {
		t.Errorf("first item ID = %s, want 1", new[0].ID)
	}
	if new[1].ID != "2" {
		t.Errorf("second item ID = %s, want 2 (unmentioned, kept)", new[1].ID)
	}
	if new[2].ID != "3" {
		t.Errorf("third item ID = %s, want 3", new[2].ID)
	}
}

func TestTodoState_AllDone(t *testing.T) {
	s := NewTodoState()

	// Before clearing: should detect all-done
	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "completed", ActiveForm: "Did A"},
			{Content: "Task B", Status: "completed", ActiveForm: "Did B"},
		},
	})

	// After all-done clear, snapshot should be empty
	snapshot := s.Snapshot()
	if len(snapshot) != 0 {
		t.Errorf("allDone snapshot len = %d, want 0", len(snapshot))
	}

	// After clear, AllDone returns false（空列表不是"全部完成"状态）
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
				Description: "Add a toggle button in the settings page for switching between light and dark themes",
			},
		},
	})

	if new[0].Description != "Add a toggle button in the settings page for switching between light and dark themes" {
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
		{ID: "1", Content: "Task A", Status: "completed"},
		{ID: "2", Content: "Task B", Status: "in_progress"},
	}

	result := FormatResult(items)
	if result == "" {
		t.Error("FormatResult should not be empty")
	}
	// Should contain IDs
	if !contains(result, "#1") || !contains(result, "#2") {
		t.Errorf("FormatResult missing IDs: %s", result)
	}
}

func TestTodoState_StatusSummary(t *testing.T) {
	s := NewTodoState()

	// Empty
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
	if !contains(summary, "#1") {
		t.Error("StatusSummary missing ID")
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
	// 100 concurrent readers
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Snapshot()
			_ = s.StatusSummary()
			_ = s.AllDone()
		}()
	}

	// 10 concurrent writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s.Apply(TodoWriteParams{
				Merge: true,
				Todos: []TodoItem{
					{ID: "1", Content: "Base", Status: "completed", ActiveForm: "Did base"},
				},
			})
		}(i)
	}

	wg.Wait()
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestTodoState_ReplaceWithCustomID(t *testing.T) {
	s := NewTodoState()

	// merge=false 时，指定了 ID 但列表中不存在 → 应作为新项追加并保留 ID
	_, new := s.Apply(TodoWriteParams{
		Merge: false,
		Todos: []TodoItem{
			{ID: "42", Content: "Custom ID task", Status: "pending", ActiveForm: "Doing custom"},
		},
	})

	if len(new) != 1 {
		t.Fatalf("len = %d, want 1", len(new))
	}
	if new[0].ID != "42" {
		t.Errorf("ID = %s, want 42 (custom ID preserved)", new[0].ID)
	}
}

func TestTodoState_MergeUpdateNotFound(t *testing.T) {
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
		},
	})

	// merge=true 时，指定了不存在的 ID → 应该被忽略（不 panic）
	// #1 保持 pending（不触发 allDone），#2 保持 pending，#999 忽略
	_, new := s.Apply(TodoWriteParams{
		Merge: true,
		Todos: []TodoItem{
			{ID: "1", Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
			{ID: "2", Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
			{ID: "999", Content: "Ghost", Status: "pending", ActiveForm: "Ghosting"},
		},
	})

	if len(new) != 2 {
		t.Fatalf("len = %d, want 2 (ghost ID silently dropped)", len(new))
	}
	if new[0].ID != "1" {
		t.Errorf("ID = %s, want 1", new[0].ID)
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
		{ID: "1", Content: "Task A", Status: "completed", Description: "Long description here"},
	}
	result := FormatResult(items)
	if !contains(result, " — Long description here") {
		t.Errorf("FormatResult missing description: %s", result)
	}
}

func TestFormatResult_WithoutDescription(t *testing.T) {
	items := []TodoItem{
		{ID: "1", Content: "Task A", Status: "completed"},
	}
	result := FormatResult(items)
	// Should not contain " — " since no description
	if contains(result, " — ") {
		t.Errorf("FormatResult should not contain ' — ' when no description: %s", result)
	}
	if !contains(result, "#1") {
		t.Error("FormatResult missing ID")
	}
}

func TestTodoState_MergeEmptyListNoOp(t *testing.T) {
	// REGRESSION: merge=true 空列表应该是无操作，不应删除已有项。
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
		},
	})

	// Merge with empty list → no-op, existing items kept
	_, new := s.Apply(TodoWriteParams{
		Merge: true,
		Todos:  []TodoItem{},
	})

	if len(new) != 2 {
		t.Errorf("after empty merge, len = %d, want 2 (items kept)", len(new))
	}
}

func TestTodoState_AllDoneCount(t *testing.T) {
	s := NewTodoState()

	// Before: not all done
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
		Merge: true,
		Todos: []TodoItem{
			{ID: "1", Content: "Task A", Status: "completed", ActiveForm: "Did A"},
		},
	})

	// After allDone clear, AllDone() should be false (empty != all-done)
	if s.AllDone() {
		t.Error("AllDone() should be false after clear")
	}
}
