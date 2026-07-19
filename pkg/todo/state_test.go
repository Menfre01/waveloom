package todo

import (
	"sync"
	"testing"
)

func TestTodoState_CreateNew(t *testing.T) {
	s := NewTodoState()

	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress"},
			{Content: "Task B", Status: "pending"},
			{Content: "Task C", Status: "pending"},
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

	result1 := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress"},
			{Content: "Task B", Status: "pending"},
		},
	})

	// Capture IDs from initial creation
	idA := ""
	for _, item := range result1.Items {
		if item.Content == "Task A" {
			idA = item.ID
		}
	}

	// Update by ID + add new task — only send items that change
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{ID: idA, Content: "Task A", Status: "completed"},
			{Content: "Task C", Status: "pending"},
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
		if item.ID == idA && item.Status != "completed" {
			t.Errorf("Task A status = %s, want completed", item.Status)
		}
	}
}

func TestTodoState_UnmentionedKept(t *testing.T) {
	// 未在 params 中出现的项保持原样（无隐式删除）
	s := NewTodoState()

	result1 := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending"},
			{Content: "Task B", Status: "pending"},
			{Content: "Task C", Status: "pending"},
		},
	})

	// Capture IDs
	idA, idC := "", ""
	for _, item := range result1.Items {
		if item.Content == "Task A" {
			idA = item.ID
		}
		if item.Content == "Task C" {
			idC = item.ID
		}
	}

	// Only send Task A and Task C — Task B should remain
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{ID: idA, Content: "Task A", Status: "completed"},
			{ID: idC, Content: "Task C", Status: "in_progress"},
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
			{Content: "Task A", Status: "completed"},
			{Content: "Task B", Status: "completed"},
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
			{ID: "1", Content: "Task A", Status: "completed"},
			{ID: "2", Content: "Task B", Status: "in_progress"},
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
			{Content: "Task A", Status: "in_progress"},
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
			{Content: "Base", Status: "pending"},
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
					{Content: "Base", Status: "completed"},
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
			{Content: "Custom task", Status: "pending"},
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
	// 无 ID = CREATE（无条件），即使 content 相同也创建新项
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending"},
			{Content: "Task B", Status: "pending"},
		},
	})

	// Send Task A again without ID — creates NEW item, not update
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "completed"},
		},
	})

	if result.Created != 1 {
		t.Errorf("Created = %d, want 1 (no-ID always creates)", result.Created)
	}
	if result.Updated != 0 {
		t.Errorf("Updated = %d, want 0 (no content matching)", result.Updated)
	}
	if len(result.Items) != 3 {
		t.Errorf("len = %d, want 3 (duplicate allowed, use ID to update)", len(result.Items))
	}
}

func TestTodoState_AllDoneCount(t *testing.T) {
	s := NewTodoState()

	if s.AllDone() {
		t.Error("AllDone() should be false for empty state")
	}

	result1 := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress"},
		},
	})

	if s.AllDone() {
		t.Error("AllDone() should be false with 1 in_progress item")
	}

	idA := result1.Items[0].ID

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{ID: idA, Content: "Task A", Status: "completed"},
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
			{Content: "Task A", Status: "completed", Description: "Long description here"},
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
			{ID: "1", Content: "Task A", Status: "completed"},
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
		{Content: "Task A", Status: "completed"},
		{Content: "Task B", Status: "in_progress"},
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
	// Sending the same values with valid ID should not count as updated
	s := NewTodoState()

	result1 := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress"},
		},
	})

	idA := result1.Items[0].ID

	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{ID: idA, Content: "Task A", Status: "in_progress"},
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
			{Content: "Task A", Status: "pending"},
			{Content: "Task B", Status: "pending"},
		},
	})

	// Update by ID with different content — should match by ID, not content
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{ID: "1", Content: "Task A (renamed)", Status: "completed"},
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
			{Content: "Task A", Status: "pending"},
			{Content: "Task B", Status: "pending"},
		},
	})

	// incoming: id=1 but content matches item 2's content → ID match takes priority
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{ID: "1", Content: "Task B", Status: "completed"},
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

// TestTodoState_NoContentFallback verifies that sending content without ID
// ALWAYS creates a new item (no content-based matching).
func TestTodoState_NoContentFallback(t *testing.T) {
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending"},
		},
	})

	// Send identical content WITHOUT ID → creates NEW item (duplicate)
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "completed"},
		},
	})

	if result.Updated != 0 {
		t.Errorf("Updated = %d, want 0 (no content-based update)", result.Updated)
	}
	if result.Created != 1 {
		t.Errorf("Created = %d, want 1 (no-ID always creates new item)", result.Created)
	}

	// Should have 2 items now
	if len(result.Items) != 2 {
		t.Errorf("len = %d, want 2", len(result.Items))
	}
}

func TestTodoState_AutoAssignID(t *testing.T) {
	s := NewTodoState()

	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending"},
			{Content: "Task B", Status: "in_progress"},
			{Content: "Task C", Status: "completed"},
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

	// Simulate restoring old data without IDs — Restore auto-assigns IDs
	s.Restore([]TodoItem{
		{Content: "Task A", Status: "pending"},
		{Content: "Task B", Status: "in_progress"},
	})

	// Verify Restore assigned IDs
	snapshot := s.Snapshot()
	for _, item := range snapshot {
		if item.ID == "" {
			t.Errorf("item %q should have auto-assigned ID after Restore", item.Content)
		}
	}

	// Find Task A's ID
	idA := ""
	for _, item := range snapshot {
		if item.Content == "Task A" {
			idA = item.ID
		}
	}

	// Update by ID
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{ID: idA, Content: "Task A", Status: "completed"},
		},
	})

	if result.Updated != 1 {
		t.Errorf("Updated = %d, want 1", result.Updated)
	}

	// Task A should be completed
	for _, item := range result.Items {
		if item.Content == "Task A" {
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
			{Content: "Task A", Status: "pending"},
			{Content: "Task B", Status: "in_progress"},
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

func TestTodoState_IDNotFoundSkipped(t *testing.T) {
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending"},
		},
	})

	// Send non-existent ID — should record in UnmatchedIDs, not create
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{ID: "999", Content: "Ghost", Status: "completed"},
		},
	})

	if len(result.UnmatchedIDs) != 1 || result.UnmatchedIDs[0] != "999" {
		t.Errorf("UnmatchedIDs = %v, want [999]", result.UnmatchedIDs)
	}
	if result.Created != 0 {
		t.Errorf("Created = %d, want 0", result.Created)
	}
	if result.Updated != 0 {
		t.Errorf("Updated = %d, want 0", result.Updated)
	}
	if len(result.Items) != 1 {
		t.Errorf("len = %d, want 1 (original item unchanged)", len(result.Items))
	}
}

func TestTodoState_EmptyIDEqualsOmit(t *testing.T) {
	s := NewTodoState()

	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{ID: "", Content: "Task A", Status: "pending"},
		},
	})

	if result.Created != 1 {
		t.Errorf("Created = %d, want 1 (empty ID treated as omit)", result.Created)
	}
	if result.Updated != 0 {
		t.Errorf("Updated = %d, want 0", result.Updated)
	}
}

func TestTodoState_MixedBatch(t *testing.T) {
	s := NewTodoState()

	result1 := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "pending"},
			{Content: "Task B", Status: "pending"},
		},
	})

	idA := ""
	for _, item := range result1.Items {
		if item.Content == "Task A" {
			idA = item.ID
		}
	}

	// Mixed batch: valid ID update, invalid ID, new create
	result2 := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{ID: idA, Content: "Task A", Status: "completed"},
			{ID: "999", Content: "Ghost", Status: "completed"},
			{Content: "Task C", Status: "in_progress"},
		},
	})

	if result2.Updated != 1 {
		t.Errorf("Updated = %d, want 1", result2.Updated)
	}
	if result2.Created != 1 {
		t.Errorf("Created = %d, want 1", result2.Created)
	}
	if len(result2.UnmatchedIDs) != 1 || result2.UnmatchedIDs[0] != "999" {
		t.Errorf("UnmatchedIDs = %v, want [999]", result2.UnmatchedIDs)
	}
	if len(result2.Items) != 3 {
		t.Errorf("len = %d, want 3 (2 original + 1 new)", len(result2.Items))
	}

	for _, item := range result2.Items {
		if item.ID == idA && item.Status != "completed" {
			t.Errorf("Task A status = %s, want completed", item.Status)
		}
	}
}

func TestTodoState_RestoreAutoAssignID(t *testing.T) {
	s := NewTodoState()

	s.Restore([]TodoItem{
		{Content: "Task A", Status: "pending"},
		{Content: "Task B", Status: "in_progress"},
	})

	snapshot := s.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("len = %d, want 2", len(snapshot))
	}

	for _, item := range snapshot {
		if item.ID == "" {
			t.Errorf("item %q has no ID after Restore", item.Content)
		}
	}

	if snapshot[0].ID != "1" || snapshot[1].ID != "2" {
		t.Errorf("IDs = %s, %s; want 1, 2", snapshot[0].ID, snapshot[1].ID)
	}
}

func TestTodoState_RestoreKeepsValidIDs(t *testing.T) {
	s := NewTodoState()

	s.Restore([]TodoItem{
		{ID: "5", Content: "Task A", Status: "pending"},
		{ID: "7", Content: "Task B", Status: "in_progress"},
	})

	snapshot := s.Snapshot()
	if snapshot[0].ID != "5" {
		t.Errorf("first ID = %s, want 5", snapshot[0].ID)
	}
	if snapshot[1].ID != "7" {
		t.Errorf("second ID = %s, want 7", snapshot[1].ID)
	}
}

func TestTodoState_InProgressCount(t *testing.T) {
	s := NewTodoState()

	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress"},
			{Content: "Task B", Status: "in_progress"},
			{Content: "Task C", Status: "pending"},
		},
	})

	if result.InProgressCount != 2 {
		t.Errorf("InProgressCount = %d, want 2", result.InProgressCount)
	}

	// Complete one by ID
	idA := ""
	for _, item := range result.Items {
		if item.Content == "Task A" {
			idA = item.ID
		}
	}

	result2 := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{ID: idA, Content: "Task A", Status: "completed"},
		},
	})

	if result2.InProgressCount != 1 {
		t.Errorf("InProgressCount = %d, want 1", result2.InProgressCount)
	}
}

func TestTodoState_EmptyTodos(t *testing.T) {
	// 空或 nil Todos → no-op，所有项保持原样
	s := NewTodoState()

	s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "in_progress"},
			{Content: "Task B", Status: "pending"},
		},
	})

	result := s.Apply(TodoWriteParams{
		Todos: nil,
	})

	if result.Created != 0 {
		t.Errorf("Created = %d, want 0", result.Created)
	}
	if result.Updated != 0 {
		t.Errorf("Updated = %d, want 0", result.Updated)
	}
	if result.Unchanged != 2 {
		t.Errorf("Unchanged = %d, want 2", result.Unchanged)
	}
	if len(result.Items) != 2 {
		t.Errorf("len = %d, want 2 (items preserved)", len(result.Items))
	}
}

func TestTodoState_RestoreMixedIDs(t *testing.T) {
	// 混合 valid + empty ID → 全部重新编号
	s := NewTodoState()

	s.Restore([]TodoItem{
		{ID: "5", Content: "Task A", Status: "pending"},
		{ID: "", Content: "Task B", Status: "in_progress"},
	})

	snapshot := s.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("len = %d, want 2", len(snapshot))
	}

	// 有 empty ID → 全部重新编号为 1, 2
	if snapshot[0].ID != "1" || snapshot[1].ID != "2" {
		t.Errorf("IDs = %s, %s; want 1, 2 (renumbered due to empty ID)", snapshot[0].ID, snapshot[1].ID)
	}

	// 验证可以通过 ID 更新
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{ID: "1", Content: "Task A", Status: "completed"},
		},
	})
	if result.Updated != 1 {
		t.Errorf("Updated = %d, want 1 (should match by reassigned ID)", result.Updated)
	}
}

func TestTodoState_RestoreNonNumeric(t *testing.T) {
	// 非数字 ID → 全部重新编号
	s := NewTodoState()

	s.Restore([]TodoItem{
		{ID: "abc", Content: "Task A", Status: "pending"},
		{ID: "xyz", Content: "Task B", Status: "in_progress"},
	})

	snapshot := s.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("len = %d, want 2", len(snapshot))
	}

	if snapshot[0].ID != "1" || snapshot[1].ID != "2" {
		t.Errorf("IDs = %s, %s; want 1, 2 (renumbered due to non-numeric)", snapshot[0].ID, snapshot[1].ID)
	}
}

func TestTodoState_RestoreOutOfOrder(t *testing.T) {
	// 乱序数字 ID → 保持原样，nextID = max+1
	s := NewTodoState()

	s.Restore([]TodoItem{
		{ID: "5", Content: "Task A", Status: "pending"},
		{ID: "3", Content: "Task B", Status: "in_progress"},
	})

	snapshot := s.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("len = %d, want 2", len(snapshot))
	}

	// 原始 ID 保持
	if snapshot[0].ID != "5" {
		t.Errorf("item 0 ID = %s, want 5", snapshot[0].ID)
	}
	if snapshot[1].ID != "3" {
		t.Errorf("item 1 ID = %s, want 3", snapshot[1].ID)
	}

	// 新创建的 item 应有 nextID = 6
	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task C", Status: "pending"},
		},
	})
	if len(result.Items) != 3 {
		t.Fatalf("len = %d, want 3", len(result.Items))
	}
	if result.Items[2].ID != "6" {
		t.Errorf("new item ID = %s, want 6 (max 5 + 1)", result.Items[2].ID)
	}
}

func TestTodoState_InProgressCountWithAllDone(t *testing.T) {
	// allDone 触发时 InProgressCount 应为 0
	s := NewTodoState()

	result := s.Apply(TodoWriteParams{
		Todos: []TodoItem{
			{Content: "Task A", Status: "completed"},
			{Content: "Task B", Status: "completed"},
		},
	})

	if result.InProgressCount != 0 {
		t.Errorf("InProgressCount = %d, want 0 (allDone cleared, no in_progress)", result.InProgressCount)
	}
	if len(result.Items) != 0 {
		t.Errorf("len = %d, want 0 (allDone cleared)", len(result.Items))
	}
}
