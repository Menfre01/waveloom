// Package context 提供跨 Agent Loop 调用的消息历史累积，
// 使 DeepSeek 前缀缓存系统能够跨轮次命中。
//
// 核心机制:
//   - PrepareRun 追加 user 消息并返回完整历史副本
//   - CompleteRun 用 Loop 完成后的完整消息替换内部状态
//   - System Prompt 固定为 messages[0]，确保它是最长公共前缀的起点
//   - 四级水位线上下文压缩（Tier 0/1/2/3）在 CompleteRun 中自动执行
package context

import (
	"strings"
	"sync"

	"waveloom/pkg/compaction"
	"waveloom/pkg/llm"
)

// Stats 记录跨轮次的累计统计。
type Stats struct {
	MessageCount          int   // 当前累积的消息数
	TotalTurns            int   // 累计完成的 loop 数
	TotalPromptTokens     int   // 累计输入 token
	TotalCompletionTokens int   // 累计输出 token
	TotalCacheHitTokens   int   // 累计缓存命中 token
	TotalCacheMissTokens  int   // 累计缓存未命中 token
	TotalReasoningTokens  int   // 累计思考链 token
	TotalDurationMs       int64 // 累计耗时（毫秒）
}

// ContextManager 跨 Agent Loop 调用累积消息历史。
// 所有公开方法受 RWMutex 保护，并发安全。
type ContextManager struct {
	mu          sync.RWMutex
	messages    []llm.Message
	stats       Stats
	sessionPath string // session 落盘路径（空表示不落盘）

	// 四级水位线上下文压缩（委托给 Compactor）
	compactor *compaction.TieredCompactor

	// AGENTS.md 注入标记（防止重复注入）
	instructionsInjected bool
}

// New 创建一个新的 ContextManager。
// systemPrompt 非空时作为 messages[0] 注入，确保它始终是公共前缀的起点。
func New(systemPrompt string) *ContextManager {
	cm := &ContextManager{
		compactor: compaction.NewCompactor(compaction.DefaultCompactionConfig(), nil),
	}
	if systemPrompt != "" {
		cm.messages = []llm.Message{
			{Role: llm.RoleSystem, Content: systemPrompt},
		}
	}
	return cm
}

// NewWithCompaction 创建一个带完整压缩配置的 ContextManager。
// 推荐用法：New(systemPrompt) + cm.Compactor() 手动配置，或直接使用 NewCompactor。
func NewWithCompaction(systemPrompt string, config compaction.CompactionConfig, summarizer compaction.Summarizer) *ContextManager {
	cm := &ContextManager{
		compactor: compaction.NewCompactor(config, summarizer),
	}
	if systemPrompt != "" {
		cm.messages = []llm.Message{
			{Role: llm.RoleSystem, Content: systemPrompt},
		}
	}
	return cm
}

// Compactor 返回内部 Compactor 引用（供 TUI 读取状态、设置 hook、持久化）。
func (cm *ContextManager) Compactor() *compaction.TieredCompactor {
	return cm.compactor
}

// PrepareRun 追加一条 user 消息到历史，返回完整消息切片供 Loop 使用。
//
// 返回的切片是内部状态的副本——Loop 对返回值的 append/modify 不影响
// ContextManager 的内部状态。只有通过 CompleteRun 才能更新内部状态。
func (cm *ContextManager) PrepareRun(userInput string) []llm.Message {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.messages = append(cm.messages, llm.Message{
		Role:    llm.RoleUser,
		Content: userInput,
	})
	// 推进会话级 turn 计数（与 TUI HUD 的 Loop 计数一致）
	cm.compactor.AdvanceTurn()

	snapshot := make([]llm.Message, len(cm.messages))
	copy(snapshot, cm.messages)
	return snapshot
}

// CompleteResult 由 CompleteRun 返回，供上层（TUI/runner）获取本轮状态。
type CompleteResult struct {
	MessageCount     int              // 当前消息总数
	Compaction       compaction.CompactionResult // 本轮压缩结果
	HardLimitReached bool             // 是否触发硬临界值（后续 LLM 调用应被阻止）
	HardLimitReason  string           // 触发原因："usage" 或 "tier3_failures"
}

// CompleteRun 用 Loop 完成后的完整消息历史替换内部状态，
// 并累加本轮 token 统计。如果设置了 sessionPath 则自动落盘。
//
// promptTokens 为跨轮累加值（用于 TotalPromptTokens 统计），
// contextTokens 为末轮 API 返回的 prompt_tokens（用于上下文利用率计算）。
// model 为实际使用的模型名，reason 为终止原因，durationMs 为本轮耗时。
// 典型用法：在 LoopDone 事件中调用，传入 ev.Messages、TurnStats 累积值和 LoopDone 元数据。
// 返回 CompleteResult 供上层更新 HUD/日志。
//
// 上下文压缩已在 Loop 内每轮执行，此处仅做状态持久化。
func (cm *ContextManager) CompleteRun(messages []llm.Message, promptTokens, contextTokens, completionTokens, cacheHit, cacheMiss, reasoningTokens int, model string, durationMs int64, reason string) CompleteResult {
	cm.mu.Lock()

	// 验证和存储消息（压缩已在 Loop 内完成）
	validated, ok := llm.ValidateMessages(messages)
	if !ok {
	}
	cm.messages = validated

	cm.stats.TotalTurns++
	cm.stats.TotalPromptTokens += promptTokens
	cm.stats.TotalCompletionTokens += completionTokens
	cm.stats.TotalCacheHitTokens += cacheHit
	cm.stats.TotalCacheMissTokens += cacheMiss
	cm.stats.TotalReasoningTokens += reasoningTokens
	cm.stats.TotalDurationMs += durationMs

	lastCompaction := cm.compactor.LastResult()
	cm.mu.Unlock()

	// 落盘
	if cm.sessionPath != "" {
		cm.saveToPath(cm.sessionPath)
	}

	return CompleteResult{
		MessageCount:     len(validated),
		Compaction:       lastCompaction,
		HardLimitReached: lastCompaction.HardLimitReached,
		HardLimitReason:  lastCompaction.HardLimitReason,
	}
}

// SetSessionPath 设置 session 落盘路径。设置后每次 CompleteRun 自动保存。
// 传空字符串关闭自动落盘。
func (cm *ContextManager) SetSessionPath(path string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.sessionPath = path
}

// SessionPath 返回当前的 session 落盘路径。
func (cm *ContextManager) SessionPath() string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.sessionPath
}

// SessionID 从落盘路径提取 session 标识符。未设置路径时返回空字符串。
func (cm *ContextManager) SessionID() string {
	cm.mu.RLock()
	path := cm.sessionPath
	cm.mu.RUnlock()
	if path == "" {
		return ""
	}
	base := path[len(path)-1:]
	// 快速路径：直接扫文件名（避免 import filepath）
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			base = path[i+1:]
			break
		}
	}
	return strings.TrimSuffix(base, ".json")
}

// Save 手动将当前状态落盘到已设置的 sessionPath。
// 如果未设置路径则静默返回。
func (cm *ContextManager) Save() {
	cm.mu.RLock()
	path := cm.sessionPath
	messages := make([]llm.Message, len(cm.messages))
	copy(messages, cm.messages)
	stats := cm.stats
	compaction := cm.compactionData()
	cm.mu.RUnlock()

	if path != "" {
		SaveSessionToFile(path, messages, stats, &compaction)
	}
}

// saveToPath 内部方法：用当前状态覆盖写入指定文件。
func (cm *ContextManager) saveToPath(path string) {
	cm.mu.RLock()
	messages := make([]llm.Message, len(cm.messages))
	copy(messages, cm.messages)
	stats := cm.stats
	compaction := cm.compactionData()
	cm.mu.RUnlock()

	SaveSessionToFile(path, messages, stats, &compaction)
}

// compactionData 返回压缩系统的完整状态快照（调用方需持有锁）。
func (cm *ContextManager) compactionData() compaction.CompactionData {
	return cm.compactor.Snapshot()
}

// LoadFromFile 从 session 文件恢复 ContextManager 的内部状态。
// 返回 true 表示成功恢复，false 表示文件不存在或格式无效。
// 恢复后自动将 sessionPath 设为该文件路径，后续 CompleteRun 自动落盘。
// 同时恢复压缩决策表和摘要链。
func (cm *ContextManager) LoadFromFile(path string) bool {
	messages, stats, compactionData, _, err := LoadSessionFromFile(path)
	if err != nil || messages == nil {
		return false
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.messages = messages
	cm.stats = stats
	cm.sessionPath = path

	// 恢复压缩状态
	if compactionData != nil {
		cm.compactor.Restore(*compactionData)
	}

	// 推断 instructionsInjected：如果 messages[1] 是 user 消息且非空，认为已注入
	if len(messages) > 1 && messages[1].Role == llm.RoleUser && messages[1].Content != "" {
		cm.instructionsInjected = true
	}

	return true
}

// RemoveSession 删除当前的 session 落盘文件并关闭自动落盘。
func (cm *ContextManager) RemoveSession() {
	cm.mu.Lock()
	path := cm.sessionPath
	cm.sessionPath = ""
	cm.mu.Unlock()

	if path != "" {
		RemoveSessionFile(path)
	}
}

// Stats 返回当前累计统计的快照。
func (cm *ContextManager) Stats() Stats {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	s := cm.stats
	s.MessageCount = len(cm.messages)
	return s
}

// Reset 清空历史并归零统计，但保留 system prompt（messages[0]）。
// 同时重置压缩状态。
// 如果内部没有 system 消息则清空全部。
func (cm *ContextManager) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.stats = Stats{}
	cm.instructionsInjected = false
	cm.compactor.Reset()

	if len(cm.messages) > 0 && cm.messages[0].Role == llm.RoleSystem {
		cm.messages = cm.messages[:1]
	} else {
		cm.messages = nil
	}
}

// InjectUserInstructions 注入 AGENTS.md 内容作为第一条 user 消息。
// 对标 Codex 的 UserInstructions contextual user fragment。
// 在 system prompt（messages[0]）之后、用户实际输入之前插入。
// 若 AGENTS.md 内容为空则不做任何操作。
func (cm *ContextManager) InjectUserInstructions(text string) {
	if text == "" {
		return
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cm.instructionsInjected {
		return
	}
	if len(cm.messages) > 0 && cm.messages[0].Role == llm.RoleSystem {
		msg := llm.Message{Role: llm.RoleUser, Content: text}
		cm.messages = append(cm.messages[:1], append([]llm.Message{msg}, cm.messages[1:]...)...)
		cm.instructionsInjected = true
	}
}
