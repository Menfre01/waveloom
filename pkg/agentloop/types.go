package agentloop

import (
	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/todo"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// StepEvent — 事件接口
// ---------------------------------------------------------------------------

// StepEvent 是 Loop 逐 step 推送给上层的事件接口。
// 术语对齐 ACP/Claude Code 生态:一次 Loop.Run() 执行 = 一个 turn(一次用户消息
// 的完整回应);turn 内每次 LLM 调用 + 工具执行 = 一个 step。
// 所有事件类型通过 channel 发送,消费方用 type switch 路由。
type StepEvent interface {
	StepEvent()
}

// ---------------------------------------------------------------------------
// StreamDelta — 流式响应增量
// ---------------------------------------------------------------------------

// StreamDelta 表示 LLM 流式响应中的一次文本增量。
// 每个 delta 独立发送,TUI 可逐字渲染。
// ContentDelta 为普通回复文本,ReasoningDelta 为思考链(DeepSeek 思考模式)。
type StreamDelta struct {
	Step           int    // 当前 step 序号(1-based)
	ContentDelta   string // 增量回复文本
	ReasoningDelta string // 增量思考链(DeepSeek 思考模式,通常为空)
}

func (StreamDelta) StepEvent() {}

// ---------------------------------------------------------------------------
// ToolCallStart — 工具调用开始
// ---------------------------------------------------------------------------

// ToolCallStart 表示 LLM 请求执行一个工具,Loop 即将执行。
type ToolCallStart struct {
	Step         int    // 当前 step 序号
	ToolCallID   string // 工具调用唯一 ID
	ToolCallName string // 工具名
	Arguments    string // JSON 编码的调用参数
	// ServerSide 标记该事件为服务端自动执行的虚拟事件(Responses API 的
	// web_search_call),非本地工具调用;TUI 据此区分渲染样式。
	ServerSide bool
}

func (ToolCallStart) StepEvent() {}

// ---------------------------------------------------------------------------
// ToolCallStream — 工具执行增量输出
// ---------------------------------------------------------------------------

// ToolCallStream 表示支持流式输出的工具执行中的一次增量文本块。
// Shell 等长时间运行的工具在执行过程中通过此事件实时推送 stdout/stderr,
// TUI 将输出逐行追加到对应工具段落,替代空转等待。
type ToolCallStream struct {
	Step         int    // 当前 step 序号
	ToolCallID   string // 工具调用唯一 ID
	ToolCallName string // 工具名
	Chunk        string // 增量文本
}

func (ToolCallStream) StepEvent() {}

// ---------------------------------------------------------------------------
// ToolCallResult — 工具执行结果
// ---------------------------------------------------------------------------

// ToolCallResult 表示一个工具执行完毕(成功、失败或权限被拒)。
// 成功时 Result 非空、Error 为空;
// 执行错误时 Error 非空、Result 可为空;
// 权限被拒时 Error 非空、Denied=true、Result 含拒绝消息。
type ToolCallResult struct {
	Step         int    // 当前 step 序号
	ToolCallID   string // 工具调用唯一 ID
	ToolCallName string // 工具名
	Result       string // 输出文本
	Error        string // 失败时的错误信息
	ErrorKind    string // 失败时的错误分类(如 file_not_found)
	DurationMs   int64  // 执行耗时(毫秒)
	Denied       bool   // 工具因权限检查被拒(未实际执行)
	Fatal        bool   // 错误是否致命(ErrorClassFatal),TUI 据此区分红/金色样式
	// ServerSide 标记该结果为服务端自动执行的虚拟事件(Responses API 的
	// web_search_call),非本地工具执行结果;TUI 据此区分渲染样式。
	ServerSide bool

	// DiffHunks 为 edit_file 等工具提供的结构化 diff,供 TUI 渲染带行号的统一 diff 视图。
	// nil 表示不适用。
	DiffHunks []tool.DiffHunk
}

func (ToolCallResult) StepEvent() {}

// IsError 返回该结果是否为错误。
func (r ToolCallResult) IsError() bool { return r.Error != "" }

// ---------------------------------------------------------------------------
// StepStats — 本 step token 统计
// ---------------------------------------------------------------------------

// CompactionInfo 携带单次压缩操作的结果。
// 嵌入 StepStats 作为二级结构,通过 HasCompaction() 判断是否有压缩发生。
type CompactionInfo struct {
	TokensSaved              int     // 估算节省 token 数
	Tier                     int     // 触发 tier(0/1/2/3)
	SummaryDone              bool    // Tier 3 摘要是否成功
	HardLimitReached         bool    // 硬临界值触发
	HardLimitReason          string  // "usage" | "tier3_failures"
	UsageRatio               float64 // 上下文利用率
	Tier3ConsecutiveFailures int     // Tier 3 连续失败计数
}

// HasCompaction 返回是否有实际压缩发生(Tier 1+ 且节省 > 0)。
func (c CompactionInfo) HasCompaction() bool {
	return c.Tier > 0 && c.TokensSaved > 0
}

// compactionInfoFromTick 从 compaction.Tick 构造 CompactionInfo。
// 集中管理 Tick → CompactionInfo 的字段映射,避免多处手工拷贝。
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

// StepStats 在每个 step(一次 LLM 调用 + 工具执行 + 压缩)完成后推送,
// 一次性携带本 step token 用量和压缩结果。TUI 可累加到 HUD 中实时展示。
type StepStats struct {
	Step             int    // 当前 step 序号
	Model            string // API 返回的实际模型名
	PromptTokens     int    // 本 step 输入 token(API 真实值,压缩前)
	CompletionTokens int    // 本 step 输出 token
	CacheHitTokens   int    // 本 step 缓存命中 token
	CacheMissTokens  int    // 本 step 缓存未命中 token
	ReasoningTokens  int    // 本 step 思考链 token(DeepSeek 思考模式)
	MessageCount     int    // 调用 LLM 时的消息数(不含本 step assistant 回复)

	// 压缩结果(每个 step LLM 后必定执行,无压缩时各字段为零值)
	Compaction CompactionInfo
}

func (StepStats) StepEvent() {}

// ---------------------------------------------------------------------------
// BalanceUpdate — 余额更新
// ---------------------------------------------------------------------------

// BalanceUpdate 在 agent loop 启动时推送,携带最新的账户余额信息。
// 仅在 Provider 支持余额查询时发送,整个 turn 生命周期仅发送一次。
// Step 固定为 0(表示 turn 启动阶段的查询)。
type BalanceUpdate struct {
	Step    int              // 固定为 0
	Balance *llm.BalanceInfo // 余额信息;nil 表示查询失败
}

func (BalanceUpdate) StepEvent() {}

// ---------------------------------------------------------------------------
// AskUserQuestionEvent — 用户选择题交互通知
// ---------------------------------------------------------------------------

// QuestionPrompt / QuestionOptionPrompt / QuestionResponse 的类型别名,
// 实际定义在 pkg/permission/types.go 中。
type (
	QuestionPrompt       = permission.QuestionPrompt
	QuestionOptionPrompt = permission.QuestionOptionPrompt
	QuestionResponse     = permission.QuestionResponse
)

// AskUserQuestionEvent 通知 TUI 即将展示选择题界面。
// 实际的阻塞式交互通过 UserResponder.AnswerQuestion() 完成,
// 此事件用于 TUI 在渲染前做准备工作(如清空状态)。
type AskUserQuestionEvent struct {
	Step       int
	ToolCallID string
	Questions  []QuestionPrompt
}

func (AskUserQuestionEvent) StepEvent() {}

// ---------------------------------------------------------------------------
// PlanModeEnter / PlanModeExit — plan 模式事件
// ---------------------------------------------------------------------------

// PlanModeEnter 在进入 plan 模式时推送。
type PlanModeEnter struct {
	Step     int
	PlanFile string
	PairID   string // START/END 配对 ID,TUI 用于用户手动退出时注入 [plan:end]
}

func (PlanModeEnter) StepEvent() {}

// PlanModeExit 在退出 plan 模式时推送(无论 approve 或 reject)。
type PlanModeExit struct {
	Step     int
	Plan     string
	FilePath string
	Approved bool
	Feedback string
}

func (PlanModeExit) StepEvent() {}

// ---------------------------------------------------------------------------
// TodoUpdateEvent — todo 列表更新
// ---------------------------------------------------------------------------

// TodoUpdateEvent 在 todo_write 工具执行后推送,
// 通知 TUI 刷新 todo 面板显示。
type TodoUpdateEvent struct {
	Items []todo.TodoItem
}

func (TodoUpdateEvent) StepEvent() {}

// ---------------------------------------------------------------------------
// TurnDone — turn 终止
// ---------------------------------------------------------------------------

// TurnDone 是 Run 返回的最后一个事件,表示一个 turn(一次 Run 执行)已终止。
// 此后 channel 关闭。
type TurnDone struct {
	Step     int              // 总 step 数
	Reason   TerminalReason   // 终止原因
	Err      error            // 非 nil 表示异常终止
	Messages []llm.Message    // 完整消息历史
}

func (TurnDone) StepEvent() {}

// ---------------------------------------------------------------------------
// TurnDoneWithGen — 带代数标记的 TurnDone
// ---------------------------------------------------------------------------

// TurnDoneWithGen 包装 TurnDone 并携带 runGeneration。
// 用于 TUI 层判断 TurnDone 是否属于已被取代的旧 turn,防止旧事件覆盖新 turn 状态。
type TurnDoneWithGen struct {
	TurnDone
	Generation int
}

func (TurnDoneWithGen) StepEvent() {}
