package todo

import (
	"sync"
	"testing"
)

func TestTodoState_CreateNew(t *testing.T) {
	s := NewTodoState()

	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
			{Content: "Task C", Status: "pending", ActiveForm: "Doing C"},
		},
	})

	if result.Created != 3 {
		t.Errorf("Created = %d, want 3", result.Created)
	}
	if result.Updated != 0 {
		t.Errorf("Updated = %d, want 0", result.Updated)
	}
	if len(result.Items) != 3 {
		t.Fatalf("len = %d, want 3", len(result.Items))
	}
}

func TestTodoState_UpdateByContent(t *testing.T) {
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
		},
	})

	// Update status + add new task — only send items that change
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "completed", ActiveForm: "Did A"},
			{Content: "Task C", Status: "pending", ActiveForm: "Doing C"},
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

	// Verify Task A was updated
	for _, item := range result.Items {
		if item.Content == "Task A" && item.Status != "completed" {
			t.Errorf("Task A status = %s, want completed", item.Status)
		}
	}
}

func TestTodoState_UnmentionedKept(t *testing.T) {
	// 未在 params 中出现的项保持原样（无隐式删除）
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
			{Content: "Task C", Status: "pending", ActiveForm: "Doing C"},
		},
	})

	// Only send Task A and Task C — Task B should remain
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "completed", ActiveForm: "Did A"},
			{Content: "Task C", Status: "in_progress", ActiveForm: "Doing C"},
		},
	})

	if len(result.Items) != 3 {
		t.Fatalf("after update len = %d, want 3 (Task B kept)", len(result.Items))
	}

	// Verify all three exist
	contents := make(map[string]string)
	for _, item := range result.Items {
		contents[item.Content] = item.Status
	}
	if contents["Task A"] != "completed" {
		t.Errorf("Task A = %s, want completed", contents["Task A"])
	}
	if contents["Task B"] != "pending" {
		t.Errorf("Task B = %s, want pending (unchanged)", contents["Task B"])
	}
	if contents["Task C"] != "in_progress" {
		t.Errorf("Task C = %s, want in_progress", contents["Task C"])
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

	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{
				Content:     "Add dark mode",
				Status:      "in_progress",
				ActiveForm:  "Adding dark mode",
				Description: "Add a toggle button in the settings page",
			},
		},
	})

	if result.Items[0].Description != "Add a toggle button in the settings page" {
		t.Errorf("description not preserved: %s", result.Items[0].Description)
	}
}

func TestTodoState_EmptyNotAllDone(t *testing.T) {
	s := NewTodoState()
	if s.AllDone() {
		t.Error("AllDone() should be false for fresh empty state")
	}
}

func TestTodoState_FormatResult(t *testing.T) {
	result := MergeResult{
		Items: []TodoItem{
			{ID: "1", Content: "Task A", Status: "completed", ActiveForm: "Did A"},
			{ID: "2", Content: "Task B", Status: "in_progress", ActiveForm: "Doing B"},
		},
		Created:   0,
		Updated:   1,
		Unchanged: 1,
	}

	text := FormatResult(result)
	if text == "" {
		t.Error("FormatResult should not be empty")
	}
	if !contains(text, "[completed] 1 Task A") {
		t.Errorf("FormatResult missing Task A: %s", text)
	}
	if !contains(text, "[in_progress] 2 Task B") {
		t.Errorf("FormatResult missing Task B: %s", text)
	}
	if !contains(text, "0 created") {
		t.Errorf("FormatResult missing created count: %s", text)
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
	if !contains(summary, "[in_progress] 1 Task A") {
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

func TestTodoState_SingleItem(t *testing.T) {
	s := NewTodoState()

	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Custom task", Status: "pending", ActiveForm: "Doing custom"},
		},
	})

	if len(result.Items) != 1 {
		t.Fatalf("len = %d, want 1", len(result.Items))
	}
	if result.Items[0].Content != "Custom task" {
		t.Errorf("Content = %s, want Custom task", result.Items[0].Content)
	}
}

func TestTodoState_ContentImmutable(t *testing.T) {
	// content 匹配 → UPDATE，不会创建重复项
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
		},
	})

	// Send Task A again — should UPDATE, not create duplicate
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "completed", ActiveForm: "Did A"},
		},
	})

	if result.Created != 0 {
		t.Errorf("Created = %d, want 0 (content match should UPDATE)", result.Created)
	}
	if result.Updated != 1 {
		t.Errorf("Updated = %d, want 1", result.Updated)
	}
	if len(result.Items) != 2 {
		t.Errorf("len = %d, want 2 (no duplicates)", len(result.Items))
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
		t.Error("AllDone() should be false after clear (single item completed → clear)")
	}
}

func TestFormatResult_Empty(t *testing.T) {
	result := FormatResult(MergeResult{})
	if result != "All todos completed and cleared." {
		t.Errorf("FormatResult(empty) = %q, want 'All todos completed and cleared.'", result)
	}

	result2 := FormatResult(MergeResult{Items: nil})
	if result2 != "All todos completed and cleared." {
		t.Errorf("FormatResult(nil items) = %q, want 'All todos completed and cleared.'", result2)
	}
}

func TestFormatResult_WithDescription(t *testing.T) {
	result := MergeResult{
		Items: []TodoItem{
			{Content: "Task A", Status: "completed", ActiveForm: "Did A", Description: "Long description here"},
		},
		Created:   1,
		Updated:   0,
		Unchanged: 0,
	}
	text := FormatResult(result)
	if !contains(text, " — Long description here") {
		t.Errorf("FormatResult missing description: %s", text)
	}
}

func TestFormatResult_WithoutDescription(t *testing.T) {
	result := MergeResult{
		Items: []TodoItem{
			{ID: "1", Content: "Task A", Status: "completed", ActiveForm: "Did A"},
		},
		Created:   1,
		Updated:   0,
		Unchanged: 0,
	}
	text := FormatResult(result)
	if contains(text, " — ") {
		t.Errorf("FormatResult should not contain ' — ' when no description: %s", text)
	}
	if !contains(text, "[completed] 1 Task A") {
		t.Error("FormatResult missing task")
	}
}

func TestTodoState_SessionResume(t *testing.T) {
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

func TestTodoState_NoOpUpdate(t *testing.T) {
	// Sending the same values should not count as updated
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})

	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress", ActiveForm: "Doing A"},
		},
	})

	if result.Updated != 0 {
		t.Errorf("Updated = %d, want 0 (no actual change)", result.Updated)
	}
	if result.Created != 0 {
		t.Errorf("Created = %d, want 0", result.Created)
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

func TestTodoState_UpdateByID(t *testing.T) {
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
		},
	})

	// Update by ID with different content — should match by ID, not content
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{ID: "1", Content: "Task A (renamed)", Status: "completed", ActiveForm: "Did A"},
		},
	})

	if result.Updated != 1 {
		t.Errorf("Updated = %d, want 1", result.Updated)
	}
	if result.Created != 0 {
		t.Errorf("Created = %d, want 0 (match by ID)", result.Created)
	}

	// Verify item 1 was updated with new content preserved (but content is immutable via updateItem)
	for _, item := range result.Items {
		if item.ID == "1" && item.Status != "completed" {
			t.Errorf("item 1 status = %s, want completed", item.Status)
		}
	}
}

func TestTodoState_IDMatchPriority(t *testing.T) {
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "pending", ActiveForm: "Doing B"},
		},
	})

	// incoming: id=1 but content matches item 2's content → ID match takes priority
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{ID: "1", Content: "Task B", Status: "completed", ActiveForm: "Did B via ID1"},
		},
	})

	if result.Updated != 1 {
		t.Errorf("Updated = %d, want 1", result.Updated)
	}

	// Item 1 (ID="1") should be updated, NOT item 2
	for _, item := range result.Items {
		if item.ID == "1" && item.Status != "completed" {
			t.Errorf("item 1 should be completed by ID match, got status=%s", item.Status)
		}
		if item.ID == "2" && item.Status != "pending" {
			t.Errorf("item 2 should remain pending, got status=%s", item.Status)
		}
	}
}

func TestTodoState_ContentFallback(t *testing.T) {
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending", ActiveForm: "Doing A"},
		},
	})

	// Update without ID, matching by content → content fallback
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "completed", ActiveForm: "Did A"},
		},
	})

	if result.Updated != 1 {
		t.Errorf("Updated = %d, want 1 (content fallback)", result.Updated)
	}
	if result.Created != 0 {
		t.Errorf("Created = %d, want 0", result.Created)
	}

	for _, item := range result.Items {
		if item.Content == "Task A" && item.Status != "completed" {
			t.Errorf("Task A should be completed, got %s", item.Status)
		}
	}
}

func TestTodoState_AutoAssignID(t *testing.T) {
	s := NewTodoState()

	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "in_progress", ActiveForm: "Doing B"},
			{Content: "Task C", Status: "completed", ActiveForm: "Did C"},
		},
	})

	if result.Created != 3 {
		t.Fatalf("Created = %d, want 3", result.Created)
	}

	ids := make(map[string]string)
	for _, item := range result.Items {
		if item.ID == "" {
			t.Errorf("item %q has no ID", item.Content)
		}
		ids[item.ID] = item.Content
	}

	if ids["1"] != "Task A" || ids["2"] != "Task B" || ids["3"] != "Task C" {
		t.Errorf("ID assignment wrong: %v", ids)
	}
}

func TestTodoState_BackwardCompatNoID(t *testing.T) {
	s := NewTodoState()

	// Simulate restoring old data without IDs
	s.Restore([]TodoItem{
		{Content: "Task A", Status: "pending", ActiveForm: "Doing A"},
		{Content: "Task B", Status: "in_progress", ActiveForm: "Doing B"},
	})

	// Update by content — should match and auto-assign ID
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "completed", ActiveForm: "Did A"},
		},
	})

	if result.Updated != 1 {
		t.Errorf("Updated = %d, want 1", result.Updated)
	}

	// Task A should now have an ID
	for _, item := range result.Items {
		if item.Content == "Task A" {
			if item.ID == "" {
				t.Error("Task A should have an auto-assigned ID after update")
			}
			if item.Status != "completed" {
				t.Errorf("Task A status = %s, want completed", item.Status)
			}
		}
	}
}

func TestTodoState_IDPersistenceInSnapshot(t *testing.T) {
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending", ActiveForm: "Doing A"},
			{Content: "Task B", Status: "in_progress", ActiveForm: "Doing B"},
		},
	})

	snapshot := s.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(snapshot))
	}

	for _, item := range snapshot {
		if item.ID == "" {
			t.Errorf("snapshot item %q missing ID", item.Content)
		}
	}

	if snapshot[0].ID != "1" || snapshot[1].ID != "2" {
		t.Errorf("snapshot IDs = %s, %s; want 1, 2", snapshot[0].ID, snapshot[1].ID)
	}
}
