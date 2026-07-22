package permission

import (
	"strings"
	"sync"

	"github.com/Menfre01/waveloom/pkg/bash"
)
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

	// 2. 对 bash 工具：AST baseCommand+firstArg 匹配
	// 使用 "baseCommand:firstArg" 签名确保子命令不串扰：
	//   "make build" ↔ "make build 2>&1" → match (同命令+子命令)
	//   "make build" ↔ "make test"    → no match (子命令不同)
	if toolName == "bash" && pattern != "" {
		patternCI, _ := bash.Parse(pattern)
		for k, d := range sm.store {
			if k.ToolName != toolName || k.Pattern == "" {
				continue
			}
			memCI, _ := bash.Parse(k.Pattern)
			if patternCI != nil && memCI != nil {
				if commandSignature(patternCI) == commandSignature(memCI) {
					return d, true
				}
			}
			// 退化：前缀模糊匹配
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


// commandSignature 返回命令的语义签名（baseCommand + firstArg），
// 用于 session 记忆匹配时区分同命令的不同子命令。
// "make build" 和 "make build 2>&1" 签名相同，
// 但 "make build" 和 "make test" 签名不同。
func commandSignature(ci *bash.CommandInfo) string {
	sig := ci.BaseCommand
	if len(ci.Args) > 0 {
		sig += "\x00" + ci.Args[0]
	}
	return sig
}

// Len 返回记忆条目数。
func (sm *SessionMemory) Len() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.store)
}

// shellPrefixFuzzyMatch 退化路径：前缀模糊匹配。
// 当 AST 解析失败时保证向后兼容。
func shellPrefixFuzzyMatch(a, b string) bool {
	if a == b {
		return true
	}
	shorter, longer := a, b
	if len(a) > len(b) {
		shorter, longer = b, a
	}
	return longer == shorter || strings.HasPrefix(longer, shorter+" ")
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
