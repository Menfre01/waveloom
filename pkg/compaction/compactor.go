package compaction

import (
	"context"
	"sync"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// TieredCompactor 执行四级水位线上下文压缩，实现 Compactor 接口。
// 所有公开方法受 Mutex 保护，并发安全。
type TieredCompactor struct {
	mu                sync.Mutex
	watermark         WatermarkState
	decisions         compactionDecisionSet
	existingSummaries []string
	summarizer        Summarizer
	config            CompactionConfig
	lastResult        CompactionResult
	totalTurns        int // 会话级累计 turn 数（由 ContextManager 通过 AdvanceTurn 推进）
}

// NewCompactor 创建一个新的 TieredCompactor。
// config 零值字段自动用默认值填充。
func NewCompactor(config CompactionConfig, summarizer Summarizer) *TieredCompactor {
	config = config.normalize()
	return &TieredCompactor{
		decisions:  compactionDecisionSet{},
		config:     config,
		summarizer: summarizer,
		watermark: WatermarkState{
			ContextLimit: config.ContextLimit,
			Tier1Cursor:  2,
			Tier2Cursor:  2,
			Tier3Cursor:  2,
		},
	}
}

// AdvanceTurn 递增会话级 turn 计数并返回新值。
// 由 ContextManager 在每轮 Agent Loop 开始前调用。
func (c *TieredCompactor) AdvanceTurn() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.totalTurns++
	return c.totalTurns
}

// Compact 实现 Compactor 接口。
// 每轮 LLM 调用 + tool 执行后由 Loop 调用，原地修改 messages。
func (c *TieredCompactor) Compact(ctx context.Context, messages *[]llm.Message, contextTokens int) Tick {
	c.mu.Lock()
	defer c.mu.Unlock()

	result := CompactMessages(
		ctx,
		messages,
		contextTokens,
		&c.watermark,
		&c.decisions,
		c.totalTurns,
		c.config,
		c.summarizer,
		&c.existingSummaries,
	)
	c.lastResult = result

	return Tick{
		Tier:                     result.Tier,
		HardLimitReached:         result.HardLimitReached,
		HardLimitReason:          result.HardLimitReason,
		MessagesPruned:           result.MessagesPruned,
		MessagesSnipped:          result.MessagesSnipped,
		TokensSaved:              result.TokensSaved,
		Tier3SummaryDone:         result.Tier3SummaryDone,
		UsageRatio:               c.watermark.UsageRatio,
		ContextTokens:            contextTokens,
		ContextLimit:             c.watermark.ContextLimit,
		MessageCount:             len(*messages),
		Tier3ConsecutiveFailures: c.watermark.Tier3ConsecutiveFailures,
	}
}

// Watermark 返回水位线状态快照。
func (c *TieredCompactor) Watermark() WatermarkState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.watermark
}

// LastResult 返回最近一次压缩结果。
func (c *TieredCompactor) LastResult() CompactionResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastResult
}

// Decisions 返回压缩决策表快照。
func (c *TieredCompactor) Decisions() compactionDecisionSet {
	c.mu.Lock()
	defer c.mu.Unlock()
	clone := make(compactionDecisionSet, len(c.decisions))
	copy(clone, c.decisions)
	return clone
}

// ExistingSummaries 返回已有摘要链快照。
func (c *TieredCompactor) ExistingSummaries() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	clone := make([]string, len(c.existingSummaries))
	copy(clone, c.existingSummaries)
	return clone
}

// ContextLimit 返回当前上下文窗口上限。
func (c *TieredCompactor) ContextLimit() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.watermark.ContextLimit
}

// SetContextLimit 设置上下文窗口上限。
func (c *TieredCompactor) SetContextLimit(limit int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.watermark.ContextLimit = limit
	c.config.ContextLimit = limit
}

// SetSummarizer 设置 Tier 3 摘要执行器。nil 时 Tier 3 降级跳过。
func (c *TieredCompactor) SetSummarizer(s Summarizer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.summarizer = s
}

// Snapshot 返回压缩系统的完整状态快照（用于持久化）。
func (c *TieredCompactor) Snapshot() CompactionData {
	c.mu.Lock()
	defer c.mu.Unlock()

	decisions := make(compactionDecisionSet, len(c.decisions))
	copy(decisions, c.decisions)
	summaries := make([]string, len(c.existingSummaries))
	copy(summaries, c.existingSummaries)

	return CompactionData{
		Decisions:  decisions,
		Watermark:  c.watermark,
		Summaries:  summaries,
		TotalTurns: c.totalTurns,
	}
}

// Restore 从 CompactionData 恢复压缩状态。
func (c *TieredCompactor) Restore(data CompactionData) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if data.Decisions != nil {
		c.decisions = data.Decisions
	} else {
		c.decisions = compactionDecisionSet{}
	}
	c.totalTurns = data.TotalTurns
	c.watermark = data.Watermark
	c.existingSummaries = data.Summaries
	if c.existingSummaries == nil {
		c.existingSummaries = nil
	}

	// 确保 watermark 有效
	if c.watermark.ContextLimit <= 0 {
		c.watermark.ContextLimit = c.config.ContextLimit
	}
	if c.watermark.Tier1Cursor < 2 {
		c.watermark.Tier1Cursor = 2
	}
	if c.watermark.Tier2Cursor < 2 {
		c.watermark.Tier2Cursor = 2
	}
	if c.watermark.Tier3Cursor < 2 {
		c.watermark.Tier3Cursor = 2
	}
}

// Reset 重置所有压缩状态。
func (c *TieredCompactor) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.decisions = compactionDecisionSet{}
	c.existingSummaries = nil
	c.lastResult = CompactionResult{}
	c.totalTurns = 0
	c.watermark = WatermarkState{
		ContextLimit: c.config.ContextLimit,
		Tier1Cursor:  2,
		Tier2Cursor:  2,
		Tier3Cursor:  2,
	}
}
