// Package session 提供跨 Agent Loop 调用的消息历史累积，// 使 DeepSeek 前缀缓存系统能够跨轮次命中。
//
// 核心机制:
// - PrepareRun 追加 user 消息并返回完整历史副本
// - CompleteRun 用 Loop 完成后的完整消息替换内部状态
// - System Prompt 固定为 messages[0]，确保它是最长公共前缀的起点
// - 四级水位线上下文压缩（Tier 0/1/2/3）在 CompleteRun 中自动执行
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/filehistory"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/task"
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

	// jsonlMessageCount 记录已写入 JSONL 的消息数，	// 用于增量追加（避免重复写入已持久化的消息）。
	jsonlMessageCount int

	// 四级水位线上下文压缩（委托给 Compactor）
	compactor compaction.Compactor

	// AGENTS.md 注入标记（防止重复注入）
	instructionsInjected bool

	// 后台任务上次检查时间（用于跨 turn 通知）
	lastBackgroundCheck time.Time

	// Todo 列表持久化
	todoItems []json.RawMessage

	// Plan mode 状态（用于 resume 恢复）
	planModeActive bool
	planModeFile   string

	// FileHistory 状态（用于 resume 恢复）
	fhData *filehistory.SnapshotData
}

// compactorState 是 compactor 内部使用的扩展接口，// 提供 Snapshot/Restore/Reset 等持久化方法。
// TieredCompactor 同时满足 Compactor 和此接口。
type compactorState interface {
	compaction.Compactor
	Snapshot() compaction.CompactionData
	Restore(compaction.CompactionData)
	Reset()
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
func (cm *ContextManager) Compactor() compaction.Compactor {
	return cm.compactor
}

// PrepareRun 追加一条 user 消息到历史，返回完整消息切片供 Loop 使用。
//
// 在追加用户输入前检查已完成的后台任务并注入通知，// 确保 agent 能感知上一 turn 启动的后台命令的执行结果。
//
// 返回的切片是内部状态的副本——Loop 对返回值的 append/modify 不影响
// ContextManager 的内部状态。只有通过 CompleteRun 才能更新内部状态。
//
// 返回值: 完整消息切片, 本条 user 消息的 UUID（供 filehistory 追踪和 rewind 使用）
func (cm *ContextManager) PrepareRun(userInput string) ([]llm.Message, string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// ── 注入后台任务完成通知 ──
	if notification := cm.checkBackgroundTasksLocked(); notification != "" {
		cm.messages = append(cm.messages, llm.Message{
			ID:      newMessageID(),
			Role:    llm.RoleUser,
			Content: notification,
		})
	}

	messageID := newMessageID()
	cm.messages = append(cm.messages, llm.Message{
		ID:      messageID,
		Role:    llm.RoleUser,
		Content: userInput,
	})
	// 推进会话级 turn 计数（与 TUI HUD 的 Loop 计数一致）
	cm.compactor.AdvanceTurn()

	snapshot := make([]llm.Message, len(cm.messages))
	copy(snapshot, cm.messages)
	return snapshot, messageID
}

// RemoveLastUserMessage 移除 cm.messages 尾部连续 user 消息(含后台通知)。
// 用于 doTurn 中断路径:取消旧 loop 后,撤销上一次 PrepareRun 追加的 user 消息,
// 避免两条连续 user 消息进入下一次 PrepareRun 的快照。
func (cm *ContextManager) RemoveLastUserMessage() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 从尾部向前移除所有连续 user 消息(含 PrepareRun 注入的后台通知)
	for len(cm.messages) > 0 && cm.messages[len(cm.messages)-1].Role == llm.RoleUser {
		cm.messages = cm.messages[:len(cm.messages)-1]
	}
}

// mustReadRandom 包装 crypto/rand.Read，失败时 panic。
func mustReadRandom(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
	}
}

// newMessageID 生成 8 字节随机十六进制消息标识符。
// 格式：16 个十六进制字符，如 "a1b2c3d4e5f6a7b8"。
// 足以为每个消息提供唯一标识（64 位随机空间，冲突概率可忽略），// 比 UUID v4 更紧凑，在 JSONL 序列化中节省空间。
func newMessageID() string {
	b := make([]byte, 8)
	mustReadRandom(b)
	return hex.EncodeToString(b)
}

// checkBackgroundTasksLocked 检查后台任务状态，返回应注入的通知文本。
// 每轮报告两类信息：
// 1. 新完成/失败的任务（仅当有状态变更时）
// 2. 仍在运行的任务（让 LLM 知道有哪些待处理的后台工作）
// 调用方必须持有 cm.mu 写锁。
func (cm *ContextManager) checkBackgroundTasksLocked() string {
	completed := task.DefaultRegistry.CompletedSince(cm.lastBackgroundCheck)
	running := task.DefaultRegistry.Running()
	cm.lastBackgroundCheck = time.Now()

	if len(completed) == 0 && len(running) == 0 {
		return ""
	}

	var parts []string

	for _, t := range completed {
		status := "completed"
		switch t.Status {
		case task.TaskFailed:
			status = fmt.Sprintf("failed (exit code %d)", t.ExitCode)
		case task.TaskInterrupted:
			status = "interrupted (session was closed while this task was running)"
		}
		parts = append(parts, fmt.Sprintf(
			`<background-task id="%s" command="%s" exit_code="%d" log="%s">%s</background-task>`,
			t.ID, t.Command, t.ExitCode, t.LogPath, status,
		))
	}

	for _, t := range running {
		elapsed := time.Since(t.StartTime).Round(time.Second)
		parts = append(parts, fmt.Sprintf(
			`<background-task id="%s" command="%s" status="running" log="%s" elapsed="%s"/>`,
			t.ID, t.Command, t.LogPath, elapsed,
		))
	}

	return fmt.Sprintf("<background-notifications>\n%s\n</background-notifications>",
		strings.Join(parts, "\n"))
}

// CompleteResult 由 CompleteRun 返回，供上层（TUI/runner）获取本轮状态。
type CompleteResult struct {
	MessageCount     int              // 当前消息总数
	Compaction       compaction.CompactionResult // 本轮压缩结果
	HardLimitReached bool             // 是否触发硬临界值（后续 LLM 调用应被阻止）
	HardLimitReason  string           // 触发原因："usage" 或 "tier3_failures"
}

// CompleteRun 用 Loop 完成后的完整消息历史替换内部状态，// 并累加本轮 token 统计。如果设置了 sessionPath 则自动落盘。
//
// promptTokens 为跨轮累加值（用于 TotalPromptTokens 统计），// contextTokens 为末轮 API 返回的 prompt_tokens（用于上下文利用率计算）。
// model 为实际使用的模型名，reason 为终止原因，durationMs 为本轮耗时。
// 典型用法：在 LoopDone 事件中调用，传入 ev.Messages、TurnStats 累积值和 LoopDone 元数据。
// 返回 CompleteResult 供上层更新 HUD/日志。
//
// 上下文压缩已在 Loop 内每轮执行，此处仅做状态持久化。
func (cm *ContextManager) CompleteRun(messages []llm.Message, promptTokens, contextTokens, completionTokens, cacheHit, cacheMiss, reasoningTokens int, model string, durationMs int64, reason string) CompleteResult {
	cm.mu.Lock()

	// 验证和存储消息（压缩已在 Loop 内完成）
	validated, repairReport := llm.ValidateMessages(messages)
	if len(repairReport) > 0 {
		// 本轮产出了无效消息 → 打印修复日志并重置 JSONL 计数，		// 强制下次 saveToPath 全量重写 JSONL（而非增量追加），		// 避免已写入但被丢弃的消息残留在 JSONL 中。
		for _, entry := range repairReport {
			slog.Warn("turn repair", "index", entry.Index, "role", entry.Role, "action", entry.Action, "detail", entry.Detail)
		}
		cm.jsonlMessageCount = 0
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
	return strings.TrimSuffix(filepath.Base(path), ".json")
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
	todoItems := make([]json.RawMessage, len(cm.todoItems))
	copy(todoItems, cm.todoItems)
	jsonlWritten := cm.jsonlMessageCount
	lastCheck := cm.lastBackgroundCheck
	planActive := cm.planModeActive
	planFile := cm.planModeFile
	fhData := cm.fhData
	cm.mu.RUnlock()

	// 防御：过滤空 role 消息
	forceRewrite := false
	if dropped := filterInvalidMessages(messages); dropped > 0 {
		valid := make([]llm.Message, 0, len(messages))
		for i := range messages {
			if messages[i].Role != "" {
				valid = append(valid, messages[i])
			}
		}
		messages = valid
		forceRewrite = true
	}

	if path != "" {
		pm := &sessionPlanMode{Active: planActive, PlanFile: planFile}
		_ = SaveSessionToFile(path, messages, stats, &compaction, todoItems, pm, fhData, lastCheck)
	}
	n := len(messages)
	sid := strings.TrimSuffix(filepath.Base(path), ".json")
	jlPath := TranscriptPath(filepath.Dir(path), sid)
	sessionVersion := version()
	cwd, _ := os.Getwd()
	if forceRewrite || n < jsonlWritten {
		entries := MessagesToTranscriptEntries(messages, nil, sid, sessionVersion, cwd, "")
		if err := WriteTranscriptEntries(jlPath, entries); err != nil {
			slog.Warn("jsonl rewrite failed", "err", err)
		} else {
			cm.mu.Lock()
			cm.jsonlMessageCount = n
			cm.mu.Unlock()
		}
	} else if n > jsonlWritten {
		var parentUUID *string
		if jsonlWritten > 0 {
			parentUUID = &messages[jsonlWritten-1].ID
		}
		entries := MessagesToTranscriptEntries(messages[jsonlWritten:], parentUUID, sid, sessionVersion, cwd, "")
		if err := AppendTranscriptEntries(jlPath, entries); err != nil {
			slog.Warn("jsonl append failed", "err", err)
		} else {
			cm.mu.Lock()
			cm.jsonlMessageCount = n
			cm.mu.Unlock()
		}
	}
}

// saveToPath 内部方法：用当前状态覆盖写入指定 JSON 文件，// 并将新增消息追加写入 JSONL 文件。
// 若消息列表因 compaction/ValidateMessages 缩短或过滤了无效消息，则全量重写 JSONL。
func (cm *ContextManager) saveToPath(path string) {
	cm.mu.RLock()
	messages := make([]llm.Message, len(cm.messages))
	copy(messages, cm.messages)
	stats := cm.stats
	compaction := cm.compactionData()
	todoItems := make([]json.RawMessage, len(cm.todoItems))
	copy(todoItems, cm.todoItems)
	jsonlWritten := cm.jsonlMessageCount
	lastCheck := cm.lastBackgroundCheck
	planActive := cm.planModeActive
	planFile := cm.planModeFile
	fhData := cm.fhData
	cm.mu.RUnlock()

	// 防御：过滤空 role 消息（避免非法数据落盘）
	forceRewrite := false
	if dropped := filterInvalidMessages(messages); dropped > 0 {
		slog.Warn("saveToPath dropped invalid messages", "dropped", dropped, "total", len(messages))
		valid := make([]llm.Message, 0, len(messages))
		for i := range messages {
			if messages[i].Role != "" {
				valid = append(valid, messages[i])
			}
		}
		messages = valid
		forceRewrite = true
	}
	// 保存 JSON 元数据（stats / compaction / tasks / todo / plan / filehistory）
	pm := &sessionPlanMode{Active: planActive, PlanFile: planFile}
	_ = SaveSessionToFile(path, messages, stats, &compaction, todoItems, pm, fhData, lastCheck)

	n := len(messages)
	sid := strings.TrimSuffix(filepath.Base(path), ".json")
	jlPath := TranscriptPath(filepath.Dir(path), sid)
	sessionVersion := version()
	cwd, _ := os.Getwd()
	if forceRewrite || n < jsonlWritten {
		entries := MessagesToTranscriptEntries(messages, nil, sid, sessionVersion, cwd, "")
		if err := WriteTranscriptEntries(jlPath, entries); err != nil {
			slog.Warn("jsonl rewrite failed", "err", err)
		} else {
			cm.mu.Lock()
			cm.jsonlMessageCount = n
			cm.mu.Unlock()
		}
	} else if n > jsonlWritten {
		var parentUUID *string
		if jsonlWritten > 0 {
			parentUUID = &messages[jsonlWritten-1].ID
		}
		entries := MessagesToTranscriptEntries(messages[jsonlWritten:], parentUUID, sid, sessionVersion, cwd, "")
		if err := AppendTranscriptEntries(jlPath, entries); err != nil {
			slog.Warn("jsonl append failed", "err", err)
		} else {
			cm.mu.Lock()
			cm.jsonlMessageCount = n
			cm.mu.Unlock()
		}
	}
}

// stateful 返回内部 Compactor 的持久化扩展接口。
// 若 compactor 不支持持久化则 panic（编程错误：非 TieredCompactor 实现）。
func (cm *ContextManager) stateful() compactorState {
	cs, ok := cm.compactor.(compactorState)
	if !ok {
		panic("context: compactor does not implement stateful persistence — use compaction.NewCompactor")
	}
	return cs
}

// compactionData 返回压缩系统的完整状态快照（调用方需持有锁）。
func (cm *ContextManager) compactionData() compaction.CompactionData {
	return cm.stateful().Snapshot()
}

// LoadFromFile 从 session 文件恢复 ContextManager 的内部状态。
// 返回 true 表示成功恢复，false 表示文件不存在或格式无效。
// 恢复后自动将 sessionPath 设为该文件路径，后续 CompleteRun 自动落盘。
// 同时恢复压缩决策表和摘要链。
//
// 反序列化后执行完整性校验（llm.ValidateMessages），自动修复：
// - 非法 Role 的消息
// - 空 assistant 消息（无 content 且无 tool_calls）
// - 残缺 ToolCall（缺少 ID/Name）
// - 孤儿 tool_calls / tool 消息配对异常
//
// 修复详情通过 stderr 输出（静默修复不阻塞恢复流程）。
func (cm *ContextManager) LoadFromFile(path string) bool {
	messages, stats, compactionData, _, tasks, todoItems, planMode, fileHistory, lastCheck, err := LoadSessionFromFile(path)
	if err != nil || messages == nil {
		return false
	}

	// 恢复 Todo 列表
	cm.todoItems = todoItems

	// 恢复后台任务注册表
	for _, t := range tasks {
		taskInfo := t // copy
		task.DefaultRegistry.Register(t.ID, &taskInfo)
	}

	// 标记 session 关闭时仍在运行的任务为中断（原进程失去监控，无法确定最终状态）
	task.DefaultRegistry.InterruptRunning()

	// 恢复上次后台检查时间（避免 --resume 回放历史通知）
	if !lastCheck.IsZero() {
		cm.lastBackgroundCheck = lastCheck
	} else {
		cm.lastBackgroundCheck = time.Now()
	}

	// 恢复 Plan mode 状态
	if planMode != nil {
		cm.planModeActive = planMode.Active
		cm.planModeFile = planMode.PlanFile
	}

	// 恢复 FileHistory 状态
	if fileHistory != nil {
		cm.fhData = fileHistory
	}


	// 反序列化后完整性校验
	cleaned, report := llm.ValidateMessages(messages)
	if len(report) > 0 {
		for _, entry := range report {
			slog.Warn("session repair", "index", entry.Index, "role", entry.Role, "action", entry.Action, "detail", entry.Detail)
		}
		messages = cleaned

		// 立即回写清理后的数据，避免下次启动重复报 repair
		cwd, _ := os.Getwd()
		sid := strings.TrimSuffix(filepath.Base(path), ".json")
		jlPath := TranscriptPath(filepath.Dir(path), sid)
		entries := MessagesToTranscriptEntries(messages, nil, sid, version(), cwd, "")
		_ = WriteTranscriptEntries(jlPath, entries)
		// 同时更新 JSON 文件中的 messages 字段
		if sf, loadErr := loadSessionFile(path); loadErr == nil && sf != nil {
			sf.Messages = messages
			sf.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			if data, marshalErr := json.MarshalIndent(sf, "", "  "); marshalErr == nil {
				tmpPath := path + ".tmp"
				if writeErr := os.WriteFile(tmpPath, data, 0o644); writeErr == nil {
					_ = os.Rename(tmpPath, path)
				}
			}
		}
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.messages = messages
	cm.stats = stats
	cm.sessionPath = path
	cm.jsonlMessageCount = len(messages) // 标记全部已写入 JSONL

	// 恢复压缩状态
	if compactionData != nil {
		cm.stateful().Restore(*compactionData)
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
		_ = RemoveSessionFile(path)
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
	cm.stateful().Reset()
	cm.jsonlMessageCount = 0

	if len(cm.messages) > 0 && cm.messages[0].Role == llm.RoleSystem {
		cm.messages = cm.messages[:1]
	} else {
		cm.messages = nil
	}
}

// RewindConversationTo 截断消息历史到指定索引（不含），用于 rewind 功能。
// messageIndex 是保留的最后一条消息的索引+1（即 messages = messages[:messageIndex]）。
// 截断后生成新的 conversationID，重置 stats/compaction/todo 状态，// 并将截断后的消息写入新的 JSONL 文件。
// 返回新的 conversationID 和新 JSONL 文件路径。
func (cm *ContextManager) RewindConversationTo(messageIndex int, sessionDir string) (string, string, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if messageIndex < 0 || messageIndex > len(cm.messages) {
		return "", "", fmt.Errorf("invalid message index %d (have %d messages)", messageIndex, len(cm.messages))
	}

	// 截断消息
	cm.messages = cm.messages[:messageIndex]

	// 生成新 conversationID
	newID := NewSessionID()

	// 重置状态
	cm.stats = Stats{}
	cm.jsonlMessageCount = 0
	cm.instructionsInjected = len(cm.messages) > 1 && cm.messages[1].Role == llm.RoleUser && cm.messages[1].Content != ""
	cm.stateful().Reset()

	// 写入新 JSONL（统一 transcript 格式，同时）
	jsonlPath := TranscriptPath(sessionDir, newID)
	sessionVersion := version()
	cwd, _ := os.Getwd()
	entries := MessagesToTranscriptEntries(cm.messages, nil, newID, sessionVersion, cwd, "")
	if err := WriteTranscriptEntries(jsonlPath, entries); err != nil {
		return "", "", fmt.Errorf("write fork jsonl: %w", err)
	}
	cm.jsonlMessageCount = len(cm.messages)

	// 写入新 JSON 元数据（零值 plan mode + 空 filehistory）
	jsonPath := filepath.Join(sessionDir, newID+".json")
	compData := cm.compactionData()
	if err := SaveSessionToFile(jsonPath, cm.messages, cm.stats, &compData, nil, nil, nil, time.Time{}); err != nil {
		return "", "", fmt.Errorf("write fork json: %w", err)
	}

	// 更新 session path 到新文件
	cm.sessionPath = jsonPath

	return newID, jsonlPath, nil
}

// filterInvalidMessages 统计消息列表中 role 为空的消息数。
// 纯诊断函数，不修改切片（调用方负责过滤）。
func filterInvalidMessages(msgs []llm.Message) int {
	count := 0
	for i := range msgs {
		if msgs[i].Role == "" {
			if count == 0 {
				// 只在首次发现时打印详情，避免刷屏
				slog.Warn("corrupt message", "index", i, "role", msgs[i].Role, "content", truncStr(msgs[i].Content, 80))
			}
			count++
		}
	}
	return count
}

func truncStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// Messages 返回当前消息历史的副本（线程安全）。
func (cm *ContextManager) Messages() []llm.Message {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	msgs := make([]llm.Message, len(cm.messages))
	copy(msgs, cm.messages)
	return msgs
}

// MessageCount 返回当前消息数（线程安全）。
func (cm *ContextManager) MessageCount() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.messages)
}

// LastUserMessageID 返回最后一条 user 消息的 ID（线程安全）。
// 用于 FileHistory 在 CompleteRun 后关联 snapshot。
func (cm *ContextManager) LastUserMessageID() string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for i := len(cm.messages) - 1; i >= 0; i-- {
		if cm.messages[i].Role == llm.RoleUser && cm.messages[i].ID != "" {
			return cm.messages[i].ID
		}
	}
	return ""
}

// InjectUserInstructions 注入 AGENTS.md 内容作为第一条 user 消息。
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

// SetTodoItems 持久化 todo 列表（session 保存时序列化）。
func (cm *ContextManager) SetTodoItems(data []json.RawMessage) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.todoItems = data
}

// TodoItems 返回已持久化的 todo 列表（session 恢复时反序列化）。
func (cm *ContextManager) TodoItems() []json.RawMessage {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.todoItems
}

// SetPlanState 设置 plan mode 状态（session 保存时序列化）。
func (cm *ContextManager) SetPlanState(active bool, planFile string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.planModeActive = active
	cm.planModeFile = planFile
}

// PlanState 返回已持久化的 plan mode 状态（session 恢复时反序列化）。
func (cm *ContextManager) PlanState() (bool, string) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.planModeActive, cm.planModeFile
}

// SetFileHistory 设置文件历史快照数据（session 保存时序列化）。
func (cm *ContextManager) SetFileHistory(data *filehistory.SnapshotData) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.fhData = data
}

// FileHistory 返回已持久化的文件历史快照数据（session 恢复时反序列化）。
func (cm *ContextManager) FileHistory() *filehistory.SnapshotData {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.fhData
}
