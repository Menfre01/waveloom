// Package todo 提供 session 级 Todo 任务列表状态管理。
//
// TodoState 是线程安全的内存持有者。LLM 每次传入需要创建或更新的项，
// State 按 ID 匹配优先、content 回退合并——ID 是稳定引用，content 是回退 key。
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
// Content 是回退 key：无 ID 时按 Content 精确匹配定位任务。
type TodoItem struct {
	ID          string `json:"id,omitempty"`          // 系统自动分配的唯一标识（如 "1", "2", …）
	Content     string `json:"content"`               // 祈使句：要完成的事项（回退 key）
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
// LLM 仅发送需要创建或更新的项，State 按 ID 优先、content 回退匹配合并。
type TodoWriteParams struct {
	Todos []TodoItem `json:"todos"`
}

// ---------------------------------------------------------------------------
// MergeResult — Apply 返回值
// ---------------------------------------------------------------------------

// MergeResult 描述增量合并的结果。
type MergeResult struct {
	Items        []TodoItem // 合并后的完整列表（allDone 清空时为空）
	Created      int        // 新创建的项数
	Updated      int        // 原地更新的项数
	Unchanged    int        // 未在 params 中出现、保持原样的项数
	UnmatchedIDs []string   // 传入的 ID 中未匹配到已有项的 ID 列表（供 FormatResult 生成反馈）
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

// Apply 应用一次 todo_write 操作，按 ID 优先、content 回退匹配合并。
//
// 匹配优先级：
//  1. ID 匹配（精确）→ incoming 带 id 且匹配到已有 item → UPDATE
//  2. Content 匹配（回退）→ 无 ID 或 ID 未命中时，按 content 精确匹配 → UPDATE
//  3. 无匹配 → CREATE（系统自动分配递增 ID）
//
// 未在 params 中出现的项 → 保持原样（无隐式删除）
// 全部 completed → allDone 自动清空列表
func (s *TodoState) Apply(params TodoWriteParams) MergeResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 构建现有项的 id→index 和 content→index 映射
	idIndex := make(map[string]int, len(s.items))
	contentIndex := make(map[string]int, len(s.items))
	for i, item := range s.items {
		if item.ID != "" {
			idIndex[item.ID] = i
		}
		contentIndex[item.Content] = i
	}
	originalLen := len(s.items)

	touched := make(map[int]bool)       // 被本次 params 触碰到（含 no-op）的原始索引
	var unmatchedIDs []string           // 传入的 ID 未匹配到已有项的 ID
	var created, updated int
	// 同调用中去重：按 id（优先）或 content 去重
	processed := make(map[string]bool)

	for _, incoming := range params.Todos {
		// 去重 key：优先 ID，无 ID 时用 content
		dedupKey := incoming.Content
		if incoming.ID != "" {
			dedupKey = "id:" + incoming.ID
		}
		if processed[dedupKey] {
			continue
		}
		processed[dedupKey] = true

		// 1. ID 匹配
		if incoming.ID != "" {
			if idx, ok := idIndex[incoming.ID]; ok {
				touched[idx] = true
				if s.updateItem(idx, incoming) {
					updated++
				}
				continue
			}
			// ID 未命中 → 记录
			unmatchedIDs = append(unmatchedIDs, incoming.ID)
		}

		// 2. Content 匹配（回退）
		if idx, ok := contentIndex[incoming.Content]; ok {
			existing := &s.items[idx]
			// 如果已有 item 无 ID，自动补分配
			if existing.ID == "" {
				existing.ID = s.allocateID()
				idIndex[existing.ID] = idx
			}
			touched[idx] = true
			if s.updateItem(idx, incoming) {
				updated++
			}
			continue
		}

		// 3. CREATE：自动分配 ID
		newID := s.allocateID()
		s.items = append(s.items, TodoItem{
			ID:          newID,
			Content:     incoming.Content,
			Status:      incoming.Status,
			ActiveForm:  incoming.ActiveForm,
			Description: incoming.Description,
		})
		idx := len(s.items) - 1
		idIndex[newID] = idx
		contentIndex[incoming.Content] = idx
		created++
	}

	unchanged := originalLen - len(touched)

	// allDone：全部 completed → 清空（必须在 unchanged 计算之后）
	if s.allDoneLocked() {
		s.items = nil
	}

	return MergeResult{
		Items:        cloneItems(s.items),
		Created:      created,
		Updated:      updated,
		Unchanged:    unchanged,
		UnmatchedIDs: unmatchedIDs,
	}
}
// Snapshot 返回当前列表的线程安全副本。
func (s *TodoState) Snapshot() []TodoItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneItems(s.items)
}

// Restore 从持久化数据恢复 todo 列表（用于 session resume）。
// 扫描已有 items 的最大 ID，初始化 nextID 计数器。
// 若发现非数字 ID → 全部重新编号（从 1 开始），避免碰撞。
func (s *TodoState) Restore(items []TodoItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = cloneItems(items)

	hasNonNumeric := false
	maxID := 0
	for _, item := range s.items {
		if item.ID != "" {
			if n, err := strconv.Atoi(item.ID); err == nil {
				if n > maxID {
					maxID = n
				}
			} else {
				hasNonNumeric = true
				log.Printf("todo.Restore: non-numeric ID %q found, renumbering all items", item.ID)
			}
		}
	}

	if hasNonNumeric {
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

	// 未匹配 ID 反馈：LLM 传入的 ID 未命中已有项
	if len(result.UnmatchedIDs) > 0 {
		fmt.Fprintf(&b, "Note: ID(s) %s were not found — tasks matched by content fallback or created as new.\n",
			strings.Join(result.UnmatchedIDs, ", "))
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

// allocateID 返回下一个可用 ID 并自增计数器。调用方必须持有 mu.Lock。
func (s *TodoState) allocateID() string {
	id := strconv.Itoa(s.nextID)
	s.nextID++
	return id
}

// updateItem 原地更新指定索引的项（仅 status/activeForm/description，content 和 ID 不变）。
// 返回 true 表示有实际变化。调用方必须持有 mu.Lock。
func (s *TodoState) updateItem(idx int, incoming TodoItem) bool {
	existing := &s.items[idx]
	if existing.Status != incoming.Status || existing.ActiveForm != incoming.ActiveForm || existing.Description != incoming.Description {
		existing.Status = incoming.Status
		existing.ActiveForm = incoming.ActiveForm
		existing.Description = incoming.Description
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
