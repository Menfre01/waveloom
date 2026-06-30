package permission

import (
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// SessionMemory — 会话记忆
// ---------------------------------------------------------------------------

// sessionKey 是会话记忆的查找键。
type sessionKey struct {
	ToolName string
	Pattern  string
}

// SessionMemory 存储会话级权限决策记忆。
// 当用户选择 "don't ask again" 时，决策被记入此处，
// 后续同类操作在当前会话内自动通过。
type SessionMemory struct {
	mu    sync.RWMutex
	store map[sessionKey]Decision
}

// NewSessionMemory 创建一个空的会话记忆存储。
func NewSessionMemory() *SessionMemory {
	return &SessionMemory{
		store: make(map[sessionKey]Decision),
	}
}

// Remember 记录一个会话级权限决策。
func (sm *SessionMemory) Remember(toolName, pattern string, decision Decision) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.store[sessionKey{ToolName: toolName, Pattern: pattern}] = decision
}

// Lookup 查找会话记忆。
// 查找策略：
//  1. 内容级记忆（精确匹配 pattern）
//  2. 对 shell 工具：prefix 匹配（"make build" ↔ "make build 2>&1" 互通）
//  3. 工具级记忆（pattern 为空）
//
// 返回 (decision, found)。
func (sm *SessionMemory) Lookup(toolName, pattern string) (Decision, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// 1. 内容级记忆（精确匹配）
	if pattern != "" {
		if d, ok := sm.store[sessionKey{ToolName: toolName, Pattern: pattern}]; ok {
			return d, true
		}
	}

	// 2. 对 bash 工具：prefix 模糊匹配（命令的参数/重定向变化不应破坏 session 记忆）
	if toolName == "bash" && pattern != "" {
		for k, d := range sm.store {
			if k.ToolName != toolName || k.Pattern == "" {
				continue
			}
			if shellPrefixFuzzyMatch(k.Pattern, pattern) {
				return d, true
			}
		}
	}

	// 3. 工具级记忆（宽泛匹配）
	if d, ok := sm.store[sessionKey{ToolName: toolName, Pattern: ""}]; ok {
		return d, true
	}

	return "", false
}

// shellPrefixFuzzyMatch 检查两个 shell 命令是否"同类"。
// 若较短的字符串是较长的字符串的空格边界前缀，则认为匹配。
// 例: "make build" 与 "make build 2>&1" → match（"make build" 是前缀）
//
//	"git status" 与 "git push"      → no match
//	"make" 与 "make build"          → match
func shellPrefixFuzzyMatch(a, b string) bool {
	if a == b {
		return true
	}
	shorter, longer := a, b
	if len(a) > len(b) {
		shorter, longer = b, a
	}
	// shorter 必须是 longer 的空格边界前缀
	return longer == shorter || strings.HasPrefix(longer, shorter+" ")
}

// Len 返回记忆条目数。
func (sm *SessionMemory) Len() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.store)
}

// Clear 清空所有记忆。
func (sm *SessionMemory) Clear() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.store = make(map[sessionKey]Decision)
}

// Entries 返回所有记忆条目（用于 ListRules）。
func (sm *SessionMemory) Entries() []RuleEntry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	entries := make([]RuleEntry, 0, len(sm.store))
	for k, v := range sm.store {
		entries = append(entries, RuleEntry{
			Rule: Rule{
				Behavior: behaviorFromDecision(v),
				ToolName: k.ToolName,
				Pattern:  k.Pattern,
			},
			Source: SourceSession,
			Scope:  ScopeSession,
		})
	}
	return entries
}

// behaviorFromDecision 将 Decision 映射为 RuleBehavior。
func behaviorFromDecision(d Decision) RuleBehavior {
	switch d {
	case DecisionAllow:
		return RuleAllow
	case DecisionDeny:
		return RuleDeny
	default:
		return RuleAsk
	}
}

// ---------------------------------------------------------------------------
// 序列化 — 用于 session 落盘 / 恢复
// ---------------------------------------------------------------------------

// MemoryEntry 是 SessionMemory 中单条记录的序列化形式。
type MemoryEntry struct {
	ToolName string   `json:"tool_name"`
	Pattern  string   `json:"pattern"`
	Decision Decision `json:"decision"`
}

// Snapshot 返回当前所有记忆条目的快照。
func (sm *SessionMemory) Snapshot() []MemoryEntry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	entries := make([]MemoryEntry, 0, len(sm.store))
	for k, v := range sm.store {
		entries = append(entries, MemoryEntry{
			ToolName: k.ToolName,
			Pattern:  k.Pattern,
			Decision: v,
		})
	}
	return entries
}

// Load 从序列化数据恢复 SessionMemory（追加，不清空现有条目）。
func (sm *SessionMemory) Load(entries []MemoryEntry) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for _, e := range entries {
		sm.store[sessionKey{ToolName: e.ToolName, Pattern: e.Pattern}] = e.Decision
	}
}
