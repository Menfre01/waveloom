package agentloop

import (
	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// TurnEvent — 事件接口
// ---------------------------------------------------------------------------

// TurnEvent 是 Loop 逐轮推送给上层的事件接口。
// 所有事件类型通过 channel 发送，消费方用 type switch 路由。
type TurnEvent interface {
	turnEvent()
}

// ---------------------------------------------------------------------------
// StreamDelta — 流式响应增量
// ---------------------------------------------------------------------------

// StreamDelta 表示 LLM 流式响应中的一次文本增量。
// 每个 delta 独立发送，TUI 可逐字渲染。
// ContentDelta 为普通回复文本，ReasoningDelta 为思考链（DeepSeek 思考模式）。
type StreamDelta struct {
	Turn           int    // 当前 turn 序号（1-based）
	ContentDelta   string // 增量回复文本
	ReasoningDelta string // 增量思考链（DeepSeek 思考模式，通常为空）
}

func (StreamDelta) turnEvent() {}

// ---------------------------------------------------------------------------
// ToolCallStart — 工具调用开始
// ---------------------------------------------------------------------------

// ToolCallStart 表示 LLM 请求执行一个工具，Loop 即将执行。
type ToolCallStart struct {
	Turn         int    // 当前 turn 序号
	ToolCallID   string // 工具调用唯一 ID
	ToolCallName string // 工具名
	Arguments    string // JSON 编码的调用参数
}

func (ToolCallStart) turnEvent() {}

// ---------------------------------------------------------------------------
// ToolCallResult — 工具执行结果
// ---------------------------------------------------------------------------

// ToolCallResult 表示一个工具执行完毕（成功、失败或权限被拒）。
// 成功时 Result 非空、Error 为空；
// 执行错误时 Error 非空、Result 可为空；
// 权限被拒时 Error 非空、Denied=true、Result 含拒绝消息。
type ToolCallResult struct {
	Turn         int    // 当前 turn 序号
	ToolCallID   string // 工具调用唯一 ID
	ToolCallName string // 工具名
	Result       string // 输出文本
	Error        string // 失败时的错误信息
	ErrorKind    string // 失败时的错误分类（如 file_not_found）
	DurationMs   int64  // 执行耗时（毫秒）
	Denied       bool   // 工具因权限检查被拒（未实际执行）

	// DiffHunks 为 edit_file 等工具提供的结构化 diff，供 TUI 渲染带行号的统一 diff 视图。
	// nil 表示不适用。
	DiffHunks []tool.DiffHunk
}

func (ToolCallResult) turnEvent() {}

// IsError 返回该结果是否为错误。
func (r ToolCallResult) IsError() bool { return r.Error != "" }

// ---------------------------------------------------------------------------
// TurnStats — 本轮 token 统计
// ---------------------------------------------------------------------------

// CompactionInfo 携带单次压缩操作的结果。
// 嵌入 TurnStats 作为二级结构，通过 HasCompaction() 判断是否有压缩发生。
type CompactionInfo struct {
	TokensSaved              int     // 估算节省 token 数
	Tier                     int     // 触发 tier（0/1/2/3）
	SummaryDone              bool    // Tier 3 摘要是否成功
	HardLimitReached         bool    // 硬临界值触发
	HardLimitReason          string  // "usage" | "tier3_failures"
	UsageRatio               float64 // 上下文利用率
	Tier3ConsecutiveFailures int     // Tier 3 连续失败计数
}

// HasCompaction 返回是否有实际压缩发生（Tier 1+ 且节省 > 0）。
func (c CompactionInfo) HasCompaction() bool {
	return c.Tier > 0 && c.TokensSaved > 0
}

// compactionInfoFromTick 从 compaction.Tick 构造 CompactionInfo。
// 集中管理 Tick → CompactionInfo 的字段映射，避免多处手工拷贝。
func compactionInfoFromTick(tick compaction.Tick) CompactionInfo {
	return CompactionInfo{
		TokensSaved:              tick.TokensSaved,
		Tier:                     tick.Tier,
		SummaryDone:              tick.Tier3SummaryDone,
		HardLimitReached:         tick.HardLimitReached,
		HardLimitReason:          tick.HardLimitReason,
		UsageRatio:               tick.UsageRatio,
		Tier3ConsecutiveFailures: tick.Tier3ConsecutiveFailures,
	}
}

// TurnStats 在每轮 LLM 调用 + 工具执行 + 压缩完成后推送，
// 一次性携带本轮 token 用量和压缩结果。TUI 可累加到 HUD 中实时展示。
type TurnStats struct {
	Turn             int    // 当前 turn 序号
	Model            string // API 返回的实际模型名
	PromptTokens     int    // 本轮输入 token（API 真实值，压缩前）
	CompletionTokens int    // 本轮输出 token
	CacheHitTokens   int    // 本轮缓存命中 token
	CacheMissTokens  int    // 本轮缓存未命中 token
	ReasoningTokens  int    // 本轮思考链 token（DeepSeek 思考模式）
	MessageCount     int    // 调用 LLM 时的消息数（不含本轮 assistant 回复）

	// 压缩结果（每轮 LLM 后必定执行，无压缩时各字段为零值）
	Compaction CompactionInfo
}

func (TurnStats) turnEvent() {}

// ---------------------------------------------------------------------------
// BalanceUpdate — 余额更新
// ---------------------------------------------------------------------------

// BalanceUpdate 在 agent loop 启动时推送，携带最新的账户余额信息。
// 仅在 Provider 支持余额查询时发送，整个 loop 生命周期仅发送一次。
// Turn 固定为 0（表示 loop 启动阶段的查询）。
type BalanceUpdate struct {
	Turn    int              // 固定为 0
	Balance *llm.BalanceInfo // 余额信息；nil 表示查询失败
}

func (BalanceUpdate) turnEvent() {}

// ---------------------------------------------------------------------------
// AskUserQuestionEvent — 用户选择题交互通知
// ---------------------------------------------------------------------------

// QuestionPrompt / QuestionOptionPrompt / QuestionResponse 的类型别名，
// 实际定义在 pkg/permission/types.go 中。
type (
	QuestionPrompt       = permission.QuestionPrompt
	QuestionOptionPrompt = permission.QuestionOptionPrompt
	QuestionResponse     = permission.QuestionResponse
)

// AskUserQuestionEvent 通知 TUI 即将展示选择题界面。
// 实际的阻塞式交互通过 UserResponder.AnswerQuestion() 完成，
// 此事件用于 TUI 在渲染前做准备工作（如清空状态）。
type AskUserQuestionEvent struct {
	Turn       int
	ToolCallID string
	Questions  []QuestionPrompt
}

func (AskUserQuestionEvent) turnEvent() {}

// ---------------------------------------------------------------------------
// PlanModeEnter / PlanModeExit — plan 模式事件
// ---------------------------------------------------------------------------

// PlanModeEnter 在进入 plan 模式时推送。
type PlanModeEnter struct {
	Turn     int
	PlanFile string
	PairID   string // START/END 配对 ID，TUI 用于用户手动退出时注入 [plan:end]
}

func (PlanModeEnter) turnEvent() {}

// PlanModeExit 在退出 plan 模式时推送（无论 approve 或 reject）。
type PlanModeExit struct {
	Turn     int
	Plan     string
	FilePath string
	Approved bool
	Feedback string
}

func (PlanModeExit) turnEvent() {}

// ---------------------------------------------------------------------------
// LoopDone — 循环终止
// ---------------------------------------------------------------------------

// LoopDone 是 Run 返回的最后一个事件，表示 loop 已终止。
// 此后 channel 关闭。
type LoopDone struct {
	Turn     int              // 总 turn 数
	Reason   TerminalReason   // 终止原因
	Err      error            // 非 nil 表示异常终止
	Messages []llm.Message    // 完整消息历史
}

func (LoopDone) turnEvent() {}

// ---------------------------------------------------------------------------
// LoopDoneWithGen — 带代数标记的 LoopDone
// ---------------------------------------------------------------------------

// LoopDoneWithGen 包装 LoopDone 并携带 runGeneration。
// 用于 TUI 层判断 LoopDone 是否属于已被取代的旧 loop，防止旧事件覆盖新 loop 状态。
type LoopDoneWithGen struct {
	LoopDone
	Generation int
}

func (LoopDoneWithGen) turnEvent() {}
