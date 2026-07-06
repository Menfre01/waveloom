// Package todo 提供 session 级 Todo 任务列表状态管理。
//
// TodoState 是线程安全的内存持有者，支持 ID-based CRUD：
//   - 有 ID → UPDATE
//   - 无 ID → CREATE（自动分配自增 ID）
//   - merge 模式下不在传入列表中的 → DELETE
//   - 全部 completed → 自动清空
package todo

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// TodoItem — 单条 todo 项
// ---------------------------------------------------------------------------

// TodoItem 描述一个待办任务。
type TodoItem struct {
	ID          string `json:"id,omitempty"`         // 服务端分配的 ID（CREATE 时为空，返回时填充）
	Content     string `json:"content"`               // 祈使句：要完成的事项
	Status      string `json:"status"`                // pending | in_progress | completed
	ActiveForm  string `json:"activeForm"`            // 现在进行时描述
	Description string `json:"description,omitempty"` // 可选：任务详情/备注
}

// ValidStatuses 是合法的 status 枚举值。
var ValidStatuses = map[string]bool{
	"pending":     true,
	"in_progress": true,
	"completed":   true,
}

// ---------------------------------------------------------------------------
// TodoWriteParams — 工具输入参数
// ---------------------------------------------------------------------------

// TodoWriteParams 是 todo_write 工具的输入参数。
type TodoWriteParams struct {
	Todos []TodoItem `json:"todos"`
	Merge bool       `json:"merge,omitempty"` // 默认 false = 全量替换
}

// ---------------------------------------------------------------------------
// TodoState — 线程安全的状态持有者
// ---------------------------------------------------------------------------

// TodoState 持有当前 session 的 todo 列表，支持跨 Loop 持久。
// 零值不可用，必须通过 NewTodoState() 创建。
type TodoState struct {
	mu     sync.RWMutex
	items  []TodoItem
	nextID int
}

// NewTodoState 创建一个新的空 TodoState。
func NewTodoState() *TodoState {
	return &TodoState{
		items:  nil,
		nextID: 1,
	}
}

// Apply 应用一次 todo_write 操作。
// 返回操作前的旧列表和操作后的新列表（用于构建 tool result）。
func (s *TodoState) Apply(params TodoWriteParams) (oldItems, newItems []TodoItem) {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldItems = cloneItems(s.items)

	if !params.Merge {
		// 全量替换
		s.items = nil
		s.nextID = 1
	}

	// 逐项处理
	for _, t := range params.Todos {
		if t.ID != "" {
			// UPDATE：找到并原地更新
			found := false
			for i := range s.items {
				if s.items[i].ID == t.ID {
					s.items[i] = TodoItem{
						ID:          t.ID, // ID 不可变
						Content:     t.Content,
						Status:      t.Status,
						ActiveForm:  t.ActiveForm,
						Description: t.Description,
					}
					found = true
					break
				}
			}
			// ID 不存在且非 merge 模式 → 作为新项追加（保留指定 ID）
			if !found && !params.Merge {
				s.items = append(s.items, t)
			}
		} else {
			// CREATE：分配 ID
			t.ID = strconv.Itoa(s.nextID)
			s.nextID++
			s.items = append(s.items, t)
		}
	}

	// allDone：全部 completed → 清空
	if s.allDoneLocked() {
		s.items = nil
	}

	newItems = cloneItems(s.items)
	return
}

// Snapshot 返回当前列表的线程安全副本。
func (s *TodoState) Snapshot() []TodoItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneItems(s.items)
}

// Restore 从持久化数据恢复 todo 列表（用于 session resume）。
// 恢复后自动推算 nextID 为 max(id)+1。
func (s *TodoState) Restore(items []TodoItem) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.items = cloneItems(items)

	// 推算 nextID
	maxID := 0
	for _, t := range s.items {
		if id, err := strconv.Atoi(t.ID); err == nil && id > maxID {
			maxID = id
		}
	}
	s.nextID = maxID + 1
}

// StatusSummary 格式化为 LLM 上下文摘要（单行一项）。
// 空列表返回空字符串。
func (s *TodoState) StatusSummary() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.items) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Current Todo Status\n")
	for _, t := range s.items {
		b.WriteString(formatTodoLine(t))
		b.WriteByte('\n')
	}
	return b.String()
}

// AllDone 返回是否所有项都是 completed。
func (s *TodoState) AllDone() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.allDoneLocked()
}

// FormatResult 格式化 Apply 结果，作为 tool result 返回给 LLM。
// 包含完整列表（含 ID），让 LLM 感知所有任务的 ID 和状态。
func FormatResult(items []TodoItem) string {
	if len(items) == 0 {
		return "All todos completed and cleared."
	}

	var b strings.Builder
	b.WriteString("Todos updated successfully. Ensure that you continue to use the todo list to track your progress.\n")
	for _, t := range items {
		b.WriteString(formatTodoLine(t))
		b.WriteByte('\n')
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// 内部方法（调用方已持有锁）
// ---------------------------------------------------------------------------

func (s *TodoState) allDoneLocked() bool {
	if len(s.items) == 0 {
		return false // 空列表不是 "all done"，而是已经清空
	}
	for _, t := range s.items {
		if t.Status != "completed" {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

func cloneItems(items []TodoItem) []TodoItem {
	if items == nil {
		return nil
	}
	out := make([]TodoItem, len(items))
	copy(out, items)
	return out
}

func formatTodoLine(t TodoItem) string {
	desc := t.Content
	if t.Description != "" {
		desc += " — " + t.Description
	}
	return fmt.Sprintf("#%s [%s] %s", t.ID, t.Status, desc)
}
