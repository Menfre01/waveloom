// Package todo 提供 session 级 Todo 任务列表状态管理。
//
// TodoState 是线程安全的内存持有者。LLM 每次传入需要创建或更新的项，
// State 按 content 匹配合并——content 是不可变 key。
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
// Content 是不可变 key：创建后不可修改，Loop 按 Content 精确匹配定位任务。
type TodoItem struct {
	Content     string `json:"content"`                 // 不可变 key，祈使句：要完成的事项
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
// LLM 仅发送需要创建或更新的项，State 按 content 匹配合并。
type TodoWriteParams struct {
	Todos []TodoItem `json:"todos"`
}

// ---------------------------------------------------------------------------
// MergeResult — Apply 返回值
// ---------------------------------------------------------------------------

// MergeResult 描述增量合并的结果。
type MergeResult struct {
	Items     []TodoItem // 合并后的完整列表（allDone 清空时为空）
	Created   int        // 新创建的项数
	Updated   int        // 原地更新的项数
	Unchanged int        // 未在 params 中出现、保持原样的项数
}

// ---------------------------------------------------------------------------
// TodoState — 线程安全的状态持有者
// ---------------------------------------------------------------------------

// TodoState 持有当前 session 的 todo 列表，支持跨 Loop 持久。
type TodoState struct {
	mu    sync.RWMutex
	items []TodoItem
}

// NewTodoState 创建一个新的空 TodoState。
func NewTodoState() *TodoState {
	return &TodoState{}
}

// Apply 应用一次 todo_write 操作，按 content 匹配合并。
// content 匹配 → UPDATE（原地更新 status/activeForm/description，content 不变）
// content 无匹配 → CREATE（追加到列表末尾）
// 同调用中重复 content → 首次生效，后续跳过
// 未在 params 中出现的项 → 保持原样（无隐式删除）
// 全部 completed → allDone 自动清空列表
func (s *TodoState) Apply(params TodoWriteParams) MergeResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 构建现有项的 content → index 映射
	existingIdx := make(map[string]int, len(s.items))
	for i, item := range s.items {
		existingIdx[item.Content] = i
	}
	originalLen := len(s.items)

	var created, updated int
	processed := make(map[string]bool) // 同调用中去重

	for _, incoming := range params.Todos {
		if processed[incoming.Content] {
			continue // 重复 content：首次生效
		}
		processed[incoming.Content] = true

		if idx, ok := existingIdx[incoming.Content]; ok {
			// UPDATE：仅当字段有实际变化时才计数
			existing := &s.items[idx]
			if existing.Status != incoming.Status || existing.ActiveForm != incoming.ActiveForm || existing.Description != incoming.Description {
				existing.Status = incoming.Status
				existing.ActiveForm = incoming.ActiveForm
				existing.Description = incoming.Description
				updated++
			}
		} else {
			// CREATE：追加到列表末尾
			s.items = append(s.items, TodoItem{
				Content:     incoming.Content,
				Status:      incoming.Status,
				ActiveForm:  incoming.ActiveForm,
				Description: incoming.Description,
			})
			existingIdx[incoming.Content] = len(s.items) - 1
			created++
		}
	}

	unchanged := originalLen - updated

	// allDone：全部 completed → 清空
	if s.allDoneLocked() {
		s.items = nil
	}

	return MergeResult{
		Items:     cloneItems(s.items),
		Created:   created,
		Updated:   updated,
		Unchanged: unchanged,
	}
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
	b.WriteString("→ Verify status accuracy before taking action. Update via todo_write if any status is wrong.\n")
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
func FormatResult(result MergeResult) string {
	if len(result.Items) == 0 {
		return "All todos completed and cleared."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Todos updated. %d created, %d updated, %d unchanged.\n",
		result.Created, result.Updated, result.Unchanged)

	// 智能引导：如果有 pending 任务但没有 in_progress 任务，提示下一个应启动的任务
	hasInProgress := false
	var firstPending string
	for _, t := range result.Items {
		if t.Status == "in_progress" {
			hasInProgress = true
		}
		if t.Status == "pending" && firstPending == "" {
			firstPending = t.Content
		}
	}
	if !hasInProgress && firstPending != "" {
		fmt.Fprintf(&b, "Next task to start: %q — call todo_write to set its status to in_progress before proceeding.\n", firstPending)
	}

	for _, t := range result.Items {
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
