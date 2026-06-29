// Package compaction 定义上下文压缩的接口与事件类型。
// agentloop（使用方）和 context（实现方）均依赖此包，
// 避免 context → agentloop 的反向依赖。
package compaction

import (
	"context"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// Compactor 执行上下文压缩。Loop 在每轮 LLM 调用 + tool 执行完毕后调用。
// 实现方持有压缩状态（watermark、decisions、summaries），原地修改 messages。
type Compactor interface {
	Compact(ctx context.Context, messages *[]llm.Message, contextTokens int) Tick
	// AdvanceTurn 推进会话级累计 turn 计数并返回新值。
	// 由 ContextManager 在每轮 PrepareRun 中调用，确保与 TUI HUD Loop 计数一致。
	AdvanceTurn() int
}

// Tick 是单轮压缩结果，作为 TurnEvent 推送 TUI 实时更新 HUD。
type Tick struct {
	Tier                     int     // 触发 tier (0/1/2/3)
	HardLimitReached         bool    // 硬临界值触发（≥98% 或 Tier3 连续失败）
	HardLimitReason          string  // "usage" | "tier3_failures"
	MessagesPruned           int     // 本轮 prune 数
	MessagesSnipped          int     // 本轮 snip 数
	TokensSaved              int     // 本轮估算节省 token 数
	Tier3SummaryDone         bool    // Tier 3 摘要是否成功
	UsageRatio               float64 // 当前利用率
	ContextTokens            int     // 本轮 prompt_tokens
	ContextLimit             int     // 窗口上限
	MessageCount             int     // 当前消息总数
	Tier3ConsecutiveFailures int     // Tier 3 连续失败计数
}
