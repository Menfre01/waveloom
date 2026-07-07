// Package todo 提供 session 级 Todo 任务列表状态管理。
//
// TodoState 是线程安全的内存持有者。LLM 每次传入完整列表，State 直接替换。
// 无内部 ID，LLM 通过 content 引用任务。
package todo

import (
	"fmt"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// TodoItem — 单条 todo 项
// ---------------------------------------------------------------------------

// TodoItem 描述一个待办任务。
type TodoItem struct {
	Content     string `json:"content"`                 // 祈使句：要完成的事项
	Status      string `json:"status"`                  // pending | in_progress | completed
	ActiveForm  string `json:"activeForm"`              // 现在进行时描述
	Description string `json:"description,omitempty"`   // 可选：任务详情/备注
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
// LLM 传入完整的 todo 列表，State 直接替换。
type TodoWriteParams struct {
	Todos []TodoItem `json:"todos"`
}

// ---------------------------------------------------------------------------
// TodoState — 线程安全的状态持有者
// ---------------------------------------------------------------------------

// TodoState 持有当前 session 的 todo 列表，支持跨 Loop 持久。
type TodoState struct {
	mu    sync.RWMutex
	items []TodoItem

	// ReminderInjected 标记是否已向 LLM 注入过 todo 更新提醒。
	ReminderInjected bool
}

// NewTodoState 创建一个新的空 TodoState。
func NewTodoState() *TodoState {
	return &TodoState{}
}

// Apply 应用一次 todo_write 操作，直接替换整个列表。
func (s *TodoState) Apply(params TodoWriteParams) (oldItems, newItems []TodoItem) {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldItems = cloneItems(s.items)
	s.items = cloneItems(params.Todos)

	// allDone：全部 completed → 清空 + 重置提醒标记
	if s.allDoneLocked() {
		s.items = nil
		s.ReminderInjected = false
	}

	// 列表清空时重置提醒标记
	if len(s.items) == 0 {
		s.ReminderInjected = false
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
func (s *TodoState) Restore(items []TodoItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = cloneItems(items)
}

// StatusSummary 格式化为 LLM 上下文摘要。
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
func FormatResult(items []TodoItem) string {
	if len(items) == 0 {
		return "All todos completed and cleared."
	}

	var b strings.Builder
	b.WriteString("Todos updated. **Remember**: mark the next task in_progress BEFORE starting work, and mark completed immediately after finishing.\n")
	for _, t := range items {
		b.WriteString(formatTodoLine(t))
		b.WriteByte('\n')
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// 内部方法
// ---------------------------------------------------------------------------

func (s *TodoState) allDoneLocked() bool {
	if len(s.items) == 0 {
		return false
	}
	for _, t := range s.items {
		if t.Status != "completed" {
			return false
		}
	}
	return true
}

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
	return fmt.Sprintf("[%s] %s", t.Status, desc)
}
