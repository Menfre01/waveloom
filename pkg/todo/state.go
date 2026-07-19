// Package todo 提供 session 级 Todo 任务列表状态管理。
//
// TodoState 是线程安全的内存持有者。LLM 每次传入需要创建或更新的项，
// State 严格按 ID 区分：带 ID = 修改已有项，不带 ID = 创建新项。
package todo

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
)


// ---------------------------------------------------------------------------
// TodoItem — 单条 todo 项
// ---------------------------------------------------------------------------

// TodoItem 描述一个待办任务。
// ID 是稳定引用：系统自动分配，LLM 通过 ID 精确更新状态。
// Content 不可变：创建后不随 UPDATE 改变。
type TodoItem struct {
	ID          string `json:"id,omitempty"`          // 系统自动分配的唯一标识（如 "1", "2", …）
	Content     string `json:"content"`               // 祈使句：要完成的事项
	Status      string `json:"status"`                // pending | in_progress | completed
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

// TodoWriteParams 是 todo_create / todo_update 工具的输入参数。
// LLM 仅发送需要创建或更新的项，State 严格按 ID 区分新增和修改。
type TodoWriteParams struct {
	Todos []TodoItem `json:"todos"`
}

// ---------------------------------------------------------------------------
// MergeResult — Apply 返回值
// ---------------------------------------------------------------------------

// MergeResult 描述增量合并的结果。
type MergeResult struct {
	Items           []TodoItem // 合并后的完整列表（allDone 清空时为空）
	Created         int        // 新创建的项数
	Updated         int        // 原地更新的项数
	Unchanged       int        // 未在 params 中出现、保持原样的项数
	UnmatchedIDs    []string   // 传入的 ID 中未匹配到已有项的 ID 列表
	InProgressCount int        // 合并后 in_progress 项数量（供调用方决定是否警告）
	Deduplicated    int        // 同调用中被去重跳过的项数
}

// ---------------------------------------------------------------------------
// TodoState — 线程安全的状态持有者
// ---------------------------------------------------------------------------

// TodoState 持有当前 session 的 todo 列表，支持跨 Loop 持久。
type TodoState struct {
	mu     sync.RWMutex
	items  []TodoItem
	nextID int // 下一个可用的自增 ID
}

// NewTodoState 创建一个新的空 TodoState。
func NewTodoState() *TodoState {
	return &TodoState{nextID: 1}
}

// Apply 应用一次 todo_create / todo_update 操作，严格按 ID 区分新增和修改。
//
// 匹配规则：
//  1. incoming 带 ID → 查找已有 item → 找到 → UPDATE
//  2. incoming 带 ID → 查找已有 item → 未找到 → 记录 UnmatchedIDs，跳过
//  3. incoming 无 ID → CREATE（无条件，不复用已有 item）
//
// 未在 params 中出现的项 → 保持原样（无隐式删除）
// 全部 completed → allDone 自动清空列表
func (s *TodoState) Apply(params TodoWriteParams) MergeResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 构建现有项的 id→index 映射
	idIndex := make(map[string]int, len(s.items))
	for i, item := range s.items {
		if item.ID != "" {
			idIndex[item.ID] = i
		}
	}
	originalLen := len(s.items)

	touched := make(map[int]bool) // 被本次 params 触碰到（含 no-op）的原始索引
	var unmatchedIDs []string     // 传入的 ID 未匹配到已有项的 ID
	var created, updated, deduplicated int
	// 同调用中去重：按 id（优先）或 content 去重
	processed := make(map[string]bool)

	for _, incoming := range params.Todos {
		// 去重 key：优先 ID，无 ID 时用 content
		dedupKey := incoming.Content
		if incoming.ID != "" {
			dedupKey = "id:" + incoming.ID
		}
		if processed[dedupKey] {
			deduplicated++
			continue
		}
		processed[dedupKey] = true

		// 1. 带 ID → UPDATE（仅当 ID 匹配到已有 item）
		if incoming.ID != "" {
			if idx, ok := idIndex[incoming.ID]; ok {
				touched[idx] = true
				if s.updateItem(idx, incoming) {
					updated++
				}
				continue
			}
			// ID 未命中 → 记录，跳过（不创建新项）
			unmatchedIDs = append(unmatchedIDs, incoming.ID)
			continue
		}
		// 2. 无 ID → CREATE
		if incoming.Content == "" {
			continue // 防御：创建必须有 content
		}
		status := incoming.Status
		if status == "" {
			status = "pending" // 自动默认 pending
		}
		newID := s.allocateID()
		s.items = append(s.items, TodoItem{
			ID:          newID,
			Content:     incoming.Content,
			Status:      status,
			Description: incoming.Description,
		})
		idIndex[newID] = len(s.items) - 1
		created++
	}
	unchanged := originalLen - len(touched)

	// 统计 in_progress 数量（在 allDone 清空前）
	inProgressCount := 0
	for _, item := range s.items {
		if item.Status == "in_progress" {
			inProgressCount++
		}
	}

	// allDone：全部 completed → 清空（必须在 unchanged 计算之后）
	if s.allDoneLocked() {
		s.items = nil
	}

	return MergeResult{
		Items:           cloneItems(s.items),
		Created:         created,
		Updated:         updated,
		Unchanged:       unchanged,
		UnmatchedIDs:    unmatchedIDs,
		InProgressCount: inProgressCount,
		Deduplicated:    deduplicated,
	}
}

// Snapshot 返回当前列表的线程安全副本。
func (s *TodoState) Snapshot() []TodoItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneItems(s.items)
}

// Restore 从持久化数据恢复 todo 列表（用于 session resume）。
// 扫描已有 items：若发现空 ID 或非数字 ID → 全部重新编号（从 1 开始），
// 否则取最大数字 ID + 1 作为 nextID。
func (s *TodoState) Restore(items []TodoItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = cloneItems(items)

	hasEmptyID := false
	hasNonNumeric := false
	maxID := 0
	for _, item := range s.items {
		if item.ID == "" {
			hasEmptyID = true
			log.Printf("todo.Restore: empty ID found, renumbering all items")
			break
		}
		if n, err := strconv.Atoi(item.ID); err == nil {
			if n > maxID {
				maxID = n
			}
		} else {
			hasNonNumeric = true
			log.Printf("todo.Restore: non-numeric ID %q found, renumbering all items", item.ID)
		}
	}

	if hasEmptyID || hasNonNumeric {
		for i := range s.items {
			s.items[i].ID = strconv.Itoa(i + 1)
		}
		s.nextID = len(s.items) + 1
	} else {
		s.nextID = maxID + 1
	}
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
	b.WriteString("→ Verify status accuracy before taking action. Update via todo_create / todo_update if any status is wrong.\n")
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
	if result.Deduplicated > 0 {
		fmt.Fprintf(&b, "Note: %d duplicate item(s) in this call were skipped. Check for repeated content or IDs.\n", result.Deduplicated)
	}

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
		fmt.Fprintf(&b, "Next task to start: %q — call todo_update to set its status to in_progress before proceeding.\n", firstPending)
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

// allocateID 返回下一个可用 ID 并自增计数器。调用方必须持有 mu.Lock。
func (s *TodoState) allocateID() string {
	id := strconv.Itoa(s.nextID)
	s.nextID++
	return id
}

// updateItem 原地更新指定索引的项（仅 status，content/description/ID 不变）。
// 返回 true 表示有实际变化。调用方必须持有 mu.Lock。
func (s *TodoState) updateItem(idx int, incoming TodoItem) bool {
	existing := &s.items[idx]
	if existing.Status != incoming.Status {
		existing.Status = incoming.Status
		return true
	}
	return false
}
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
	if t.ID != "" {
		return fmt.Sprintf("[%s] %s %s", t.Status, t.ID, desc)
	}
	return fmt.Sprintf("[%s] %s", t.Status, desc)
}
