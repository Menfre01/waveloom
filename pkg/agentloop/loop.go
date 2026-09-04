// Package agentloop 实现 Waveloom Code Agent 的 Think-Act-Observe 循环。
//
// 术语对齐 ACP/Claude Code 生态:一次 Loop.Run() 执行 = 一个 turn(一次用户消息
// 的完整回应);turn 内每次 LLM 调用 + 工具执行 = 一个 step。
// Loop 是连接 LLM Client 和 Tool System 的编排器,在每个 step 中:
// 1. 组装上下文，调用 LLM（Think）
// 2. 解析响应，执行工具（Act）
// 3. 收集结果，更新状态（Observe）
// 4. 判断是否继续或终止
package agentloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/lsp"
	"github.com/Menfre01/waveloom/pkg/hook"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/sandbox"
	"github.com/Menfre01/waveloom/pkg/todo"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// Config — 不可变配置
// ---------------------------------------------------------------------------

// Config 保存 Loop 的不可变配置。
// 构造时传入，运行期不变。
type Config struct {
	// MaxSteps 最大 step 数,0 表示无限制。
	// 每次调用 LLM 后 StepCount 加 1,达到上限时 turn 终止。
	MaxSteps int

	// SystemPrompt 系统提示词，Run 启动时作为 Messages 的第一条 system 消息注入。
	// 为空时不注入，调用方需自行在 messages 中包含 system 消息。
	SystemPrompt string

	// Guard 权限守门人，在工具执行前做权限检查。
	// nil → 跳过权限检查，所有操作允许（向后兼容）。
	Guard permission.Guard

	// UserResponder 处理 ask 决策的用户交互。
	// nil → ask 自动降级为 deny。
	UserResponder permission.UserResponder

	// SandboxMgr 沙箱管理器(可选)。注入后:
	// - execute.go 为每个工具调用注入 per-command SandboxStatus 到 context
	//   (Guard 二元决策与 Shell 工具包装共用)
	// - nil → 所有命令注入 active=false(安全默认,不触发二元决策)
	SandboxMgr *sandbox.SandboxManager

	// Compactor 每轮 LLM 调用后执行上下文压缩。
	// nil → 跳过（向后兼容，由 CompleteRun 兜底）。
	Compactor compaction.Compactor

	// ToolTimeout 单个工具执行的最大时长。
	// 0 → 无超时限制（向后兼容）。
	// 设为正值时，每个工具执行会在独立的超时 context 中运行，	// 防止工具因未正确处理 ctx 取消而永久阻塞 loop。
	ToolTimeout time.Duration

	// PlanFile plan 文件路径（首次进入 plan 时自动生成 slug 文件名）。
	// 仅在 plan 模式下有效。
	PlanFile string

	// EventCallback 在工具执行 ctx 中注入的回调,供 AgentTool 等嵌套工具向父通道推送事件。
	// nil → 不注入(不影响现有路径)。
	EventCallback func(StepEvent)

	// AgentsMD 项目 AGENTS.md 文本，注入到 cold subagent 的上下文。
	// 空字符串 → 不注入。
	AgentsMD string

	// TodoState session 级 todo 状态,跨 Loop 持久。
	// nil → todo_update 工具禁用。
	TodoState *todo.TodoState

	// BackgroundCompletions 返回 turn 内新完成的后台任务通知 XML
	// (每个工具 step 执行后调用一次;空串 = 无新完成)。nil → 不启用。
	// 由持有 session.ContextManager 的入口注入(与 PrepareRun 共享游标,
	// 天然去重);用于把后台任务完成信号在 turn 内送达模型,避免模型自建
	// sleep watcher 轮询。
	BackgroundCompletions func() string

	// Model 覆盖 LLM Client 的默认 model。空 = 使用 Client 默认。
	// 用于 subagent 按任务复杂度选择不同模型。
	Model string

	// SubModel / PlanModel 是 proplan 选择值(llm.ModelChoiceProPlan)的锚点:
	// Model == "proplan" 时,plan mode 用 PlanModel(pro),其余用 SubModel(flash)。
	// 两者均非空时由入口校验保证;Loop 层仍做互备兜底,绝不将 "proplan" 发到 API。
	SubModel  string
	PlanModel string

	// ToolsOverride 请求侧工具 schema 覆盖。非空时,每次 LLM 请求携带此列表
	// 而非 registry.List() 派生列表;工具执行/分发仍走 toolRegistry。
	// 用途:fork 子代理携带与父完全一致的 tools 数组,使首请求命中父缓存前缀
	// (DeepSeek 前缀缓存包含 tools schema,不一致则从头分叉)。
	ToolsOverride []llm.ToolSpec

	// LSPManager LSP diagnostic manager
	LSPManager *lsp.Manager

	// ThrottleStore 会话级 web 限流状态(web_fetch/web_search 共享)。
	// nil → Loop 自建一个(默认);fork 子代理应由父显式传入父 store,
	// 否则子代理会覆盖父 ctx 注入后对刚被限流的 host 继续轰炸。
	ThrottleStore *tool.ThrottleStore
}

// DefaultToolTimeout 是单个工具执行的推荐超时时间（5 分钟）。
// 工具可通过 ToolWithTimeout 接口声明更长的超时（如 agent 工具 30 min）。
const DefaultToolTimeout = 5 * time.Minute

// DefaultConfig 返回带推荐默认值的 Config。
func DefaultConfig() Config {
	return Config{
		ToolTimeout: DefaultToolTimeout,
	}
}

// ---------------------------------------------------------------------------
// TurnState — turn 内 step 间可变状态
// ---------------------------------------------------------------------------

// TurnState 持有一次 turn(Run 执行)内 step 间可变的状态。
type TurnState struct {
	Messages []llm.Message
	StepCount int

	// ConsecutiveEmpty 记录连续收到空响应的次数。
	// 当 LLM 连续返回无 content 且无 tool_calls 的推理专用响应时递增，	// 达到上限后循环终止以防止死循环。
	ConsecutiveEmpty int

// AnyToolSucceeded 标记本 step 是否有任何工具成功执行。
	// 成功时重置退避计数器。
	AnyToolSucceeded bool
}

// maxConsecutiveSameError 是同类 (工具 + 错误) 连续失败的容忍上限。
// 达到后 loop 强制终止，避免 LLM 陷入无限重试探测。
// 阈值设为 8 轮:其中第 3、第 5 轮会注入提醒消息引导 LLM 改变策略,// 8 轮后仍未改变则判定为死循环强制终止。
const maxConsecutiveSameError = 8

// planProMaxContextTokens 是 plan mode 使用 PlanModel 的上下文上限(对齐
// Claude Code 的 exceeds200kTokens 守卫):plan 中上下文 ≥ 此值时降回 SubModel,
// 避免超长上下文使用 pro 模型烧钱。
const planProMaxContextTokens = 200_000

// warnThresholds 定义需要注入提醒消息的连续失败 step 数。
// 阶梯:3 → 5 → 8(终止)
var warnThresholds = map[int]bool{3: true, 5: true}

// todoReminderInterval 定义 todo 周期性提醒的间隔(assistant step 数)。
// 首次提醒在 idleTodoWrite（距上次 todo_update 达到此值）时触发，// 后续提醒至少间隔 idleTodoReminder 轮。
const (
	idleTodoWrite    = 2 // 超过此值无 todo_update → 注入提醒
	idleTodoReminder = 2 // 两次提醒之间的最小间隔
)

// ---------------------------------------------------------------------------
// TerminalReason — 终止原因
// ---------------------------------------------------------------------------

// TerminalReason 描述一个 turn(Loop.Run 执行)终止的原因。
type TerminalReason string

const (
	// ReasonCompleted LLM 给出最终答案，无 tool call。
	ReasonCompleted TerminalReason = "completed"

	// ReasonMaxSteps 达到 MaxSteps 限制。
	ReasonMaxSteps TerminalReason = "max_steps"

	// ReasonAborted ctx 被取消。
	ReasonAborted TerminalReason = "aborted"

	// ReasonModelError LLM 调用失败（重试已耗尽）。
	ReasonModelError TerminalReason = "model_error"

	// ReasonToolFatal 工具返回致命错误。
	ReasonToolFatal TerminalReason = "tool_fatal"
)

// ---------------------------------------------------------------------------
// Loop — 主循环
// ---------------------------------------------------------------------------

// Loop 编排 Think-Act-Observe 循环。
// llmClient 和 toolRegistry 通过接口注入，Loop 不绑定具体实现。
type Loop struct {
	llmClient    llm.Client
	toolRegistry tool.Registry
	config       Config

	// plan 模式状态（仅在 Run goroutine 内访问，无竞态）
	plan        bool   // 当前是否在 plan 模式
	prePlanMode bool   // 进入 plan 前的 bypassMode 状态
	planPairID  string // START/END 配对 ID(4 位 hex,如 "a3f7")
	approvedPlan string // 审批通过的 plan 内容(用于 executeToolCalls 在 tool 消息后注入 [plan:end])

	// lastPromptTokens 最近一轮 API 返回的 prompt tokens(200k 守卫用)。
	// 首轮为 0;resume 后由 estimateContextTokens 估算兜底。
	lastPromptTokens int

	// ── 退避追踪(会话级,跨 Run() 持久化)──

	// lastErrorKind 记录上一轮工具错误的 Kind。
	lastErrorKind string

	// lastErrorTool 记录上一轮发生错误的工具名。
	lastErrorTool string

	// consecutiveSameError 记录同一 (ErrorKind, Tool) 连续出现的次数。
	consecutiveSameError int

	// ── todo 周期性提醒（会话级，跨 Run() 持久化）──

	// stepsSinceLastTodoWrite 记录自上次 todo_update 调用以来的 assistant step 数。
	stepsSinceLastTodoWrite int

	// stepsSinceLastTodoReminder 记录自上次注入 todo 提醒以来的 assistant step 数。
	stepsSinceLastTodoReminder int
	// lastTodoStatusSummary 上次每-step 注入的 todo 状态快照;summary 未变化
	// 时跳过注入(状态变化才注入;周期可见性由 maybeInjectTodoReminder 兜底)。
	lastTodoStatusSummary string
	// lastChanceTodoInjected 在 loop 即将以 ReasonCompleted 终止时,	// 若检测到残留的非 completed todo 项,注入一次"最后机会"提醒后置为 true。
	// todo_update 成功执行时重置为 false。防止 LLM 忘记最后一次 todo 更新导致残留。
	lastChanceTodoInjected bool

	// previewWarned 标记本轮是否已注入过"预告文本无工具调用"提醒(每 Run 最多一次)。
	// 评测实测(deepseek-v4-flash):模型常输出"接下来:xxx"式预告文本后
	// 漏发工具调用,若直接终止会导致预告的动作从未执行、任务被迫中断。
	previewWarned bool

	// previewGraceStep 标记预告注入发生在 MaxSteps 最后一轮时,放行一轮
	// 补发工具调用。仅放行一次:注入后 continue 回到 for shouldContinue,
	// 若 StepCount 已满循环会立即退出,模型看不到 [system:continue] 提醒,
	// 预告的动作永远不执行(REGRESSION,见 shouldContinue)。
	previewGraceStep bool

	readStateStore    *tool.ReadStateStore

	throttleStore *tool.ThrottleStore

	// hookRunner 执行 hooks。nil → 跳过 hooks。
	// todoMultiInProgressMsg 由 executeTodoMutate 在检测到多个 in_progress 时设置，
	// 由 executeToolCalls 在 buildToolMessages 之后消费并注入 user 消息。
	// 确保 user 消息位于 tool 消息之后，满足 API 消息序列要求:
	// assistant(tool_calls) → tool(result) → user([system:todo])
	todoMultiInProgressMsg string

	hookRunner *hook.Runner
}

// New 创建一个新的 Loop 实例。
func New(llmClient llm.Client, toolRegistry tool.Registry, config Config) *Loop {
	ts := config.ThrottleStore
	if ts == nil {
		ts = tool.NewThrottleStore()
	}
	return &Loop{
		llmClient:    llmClient,
		toolRegistry: toolRegistry,
		config:       config,
		readStateStore: tool.NewReadStateStore(),
		throttleStore:  ts,
	}
}

// SetHookRunner 设置 hook runner（由调用方在 Loop 创建后注入）。
// 设置为 nil 则跳过所有 hook 处理。
func (l *Loop) SetHookRunner(runner *hook.Runner) {
	l.hookRunner = runner
}

// TodoState 返回 Loop 配置的 session 级 todo 状态(未配置时为 nil)。
func (l *Loop) TodoState() *todo.TodoState {
	return l.config.TodoState
}

// Compactor 返回 Loop 配置的上下文压缩器(未配置时为 nil)。
func (l *Loop) Compactor() compaction.Compactor {
	return l.config.Compactor
}

// SetPlanFile 设置 plan 文件路径(用户快捷键进入 plan 模式时由 TUI 调用)。
func (l *Loop) SetPlanFile(planFile string) {
	l.config.PlanFile = planFile
}

// SetPlanMode 启用 plan 模式并注入 START 消息（用户快捷键进入 plan 模式时由 TUI 调用）。
// 返回 [plan:start #xxxx] user 消息，调用方需将其注入 messages。
func (l *Loop) SetPlanMode(planFile string) (planPairID string, startMessage llm.Message) {
	l.plan = true
	l.planPairID = generatePairID()
	l.config.PlanFile = planFile

	if l.config.Guard != nil {
		l.config.Guard.EnterPlanMode(planFile)
	}
	startMessage = llm.Message{
		Role:    llm.RoleUser,
		Content: l.planModeStartMessage(),
	}
	return l.planPairID, startMessage
}

// InPlanMode 返回当前是否在 plan 模式。
func (l *Loop) InPlanMode() bool {
	return l.plan
}

// RestorePlanMode 从 session 恢复 plan 模式状态(resume 时由 TUI 调用)。
// 与 SetPlanMode 不同:不生成新的 [plan:start] 消息和 planPairID(已在消息历史中),
// 仅恢复 Loop 和 Guard 的 plan 模式状态。
func (l *Loop) RestorePlanMode(planFile string) {
	l.plan = true
	l.config.PlanFile = planFile

	if l.config.Guard != nil {
		l.config.Guard.EnterPlanMode(planFile)
	}
}

// ResetPlanMode 由 TUI 调用，在用户快捷键退出 plan 模式时清除 Loop 内部 plan 状态。
// 仅重置 l.plan / l.planPairID / l.config.PlanFile，不操作 Guard（Guard 由 TUI 层统一管理）。
func (l *Loop) ResetPlanMode() {
	l.plan = false
	l.planPairID = ""
	l.config.PlanFile = ""
	l.approvedPlan = ""
}

// Run 执行一次 turn(一次用户 prompt 的完整回应),逐 step 推送 StepEvent 到返回的 channel。
// channel 在 turn 终止后关闭,最后一个事件为 TurnDone。
//
// 不变量：
// 1. 消息顺序：System → User → Assistant → Tool → Assistant → ... 严格遵守
// 2. Step 计数:每次调用 LLM 后 +1,表示已完成的 step 数(无论工具执行结果如何)
// 3. 终止互斥:每个 Run 有且仅有一个 TurnDone 事件
// 4. 错误不丢上下文:即使因错误终止,TurnDone.Messages 仍包含已执行的操作历史
// 5. Context 优先：每次迭代开始先检查 ctx.Err()
// 6. 并发安全：ConcurrentSafe 工具并行执行，非安全工具串行执行
func (l *Loop) Run(ctx context.Context, messages []llm.Message) <-chan StepEvent {
	ch := make(chan StepEvent, 32)

	go func() {
		// REGRESSION: lastChanceTodoInjected 曾跨 prompt 残留——同 session
		// 第二个 prompt 的完成前提醒被跳过。Loop 是 session 级持久组件,
		// 每次 Run(单次 prompt)开始时重置,保证每轮都注入提醒。
		l.lastChanceTodoInjected = false
		// 同理:previewWarned 若跨 Run 残留,后续 prompt 的预告文本保护永久失效。
		l.previewWarned = false
		// previewGraceStep 同理:仅当轮注入时置位,shouldContinue 消费后复位,
		// 此处重置防御跨 Run 残留(注入后立即退出等边角路径)。
		l.previewGraceStep = false

		// goroutine 结束时触发 Stop/Notification hooks。
		// blocked 返回值在此场景无意义（goroutine 即将退出），仅记录日志。
		defer func() {
			if l.hookRunner != nil {
				_ = l.hookRunner.RunStop(context.Background(), "loop terminated")
			l.hookRunner.RunNotification("TurnDone", "turn terminated")
			}
		}()
		defer close(ch)

		// panic 防御:捕获 turn 内任何未预期 panic,转为 TurnDone 事件后关闭 channel,		// 确保消费者(TUI/runner)不会因 channel 关闭而无 TurnDone 导致永久等待。
		var state *TurnState
		defer func() {
			if r := recover(); r != nil {
				msgs := messages
				steps := 0
				if state != nil {
					msgs = state.Messages
					steps = state.StepCount
				}
				ch <- TurnDone{
					Step:     steps,
					Reason:   ReasonToolFatal,
					Err:      fmt.Errorf("panic: %v", r),
					Messages: msgs,
				}
			}
		}()

		// 注入 SystemPrompt（如已配置且 messages 中尚无 system 消息）
		if l.config.SystemPrompt != "" {
			hasSystem := len(messages) > 0 && messages[0].Role == llm.RoleSystem
			if !hasSystem {
				messages = append([]llm.Message{
					{Role: llm.RoleSystem, Content: l.config.SystemPrompt},
				}, messages...)
			}
		}

		state = &TurnState{
			Messages: messages,
		}

		for l.shouldContinue(state) {
			// 1. Context 取消检查
			if err := ctx.Err(); err != nil {
				ch <- TurnDone{
					Step:     state.StepCount,
					Reason:   ReasonAborted,
					Err:      err,
					Messages: state.Messages,
				}
				return
			}

		// 2. THINK: 流式调用 LLM
		l.verbose("→ LLM call #%d  (messages=%d, tools=%d)\n",
			state.StepCount+1, len(state.Messages), len(l.toolRegistry.List()))

		messagesForStep := state.Messages
			// messagesForStep 是每个 step 重建的临时切片。injectTodoStatus 通过 Append
			// 在末尾追加 todo-status 消息,仅影响 messagesForStep,不修改 state.Messages。
			// 追加使用 Append 策略以避免破坏前缀缓存（Update 的 API 费用是 Append 的 19x）。

			// 每轮注入当前 todo 状态，确保 LLM 始终可见活跃任务
			l.injectTodoStatus(&messagesForStep)

			var lastPromptTokens int      // 本 step API 返回的 prompt_tokens
			var lastUsage       *llm.UsageInfo // 暂存 usage,压缩后统一推送 StepStats
			var lastModel       string         // 暂存 model
			sendCtx := ctx
			model := l.resolveModel(messagesForStep)
			if model != "" {
				sendCtx = llm.WithModelOverride(ctx, model)
			}
			streamCh, err := l.llmClient.SendMessageStream(sendCtx, messagesForStep, l.requestTools())
			if err != nil {
				l.verbose("  ← ERROR: %v\n", err)
				ch <- TurnDone{
					Step:     state.StepCount,
					Reason:   ReasonModelError,
					Err:      fmt.Errorf("llm call: %w", err),
					Messages: state.Messages,
				}
				return
			}

			// 消费流式事件
			var contentBuf, reasoningBuf string
			var toolCalls []llm.ToolCall
			var webSearchCalls []llm.WebSearchCall // 服务端 web_search 输出 items(多轮回传)
			var streamModel string
			// 服务端 web_search 起始时间表(call_id → 起始时间),completed 时
			// 计算真实搜索耗时(替代虚拟事件的 0ms 假时长)
			webSearchStartedAt := make(map[string]time.Time)
			for ev := range streamCh {
				if ev.Err != nil {
					l.verbose("  ← STREAM ERROR: %v\n", ev.Err)

					// Context 取消 → 不回退，立即终止
					if errors.Is(ev.Err, context.Canceled) || errors.Is(ev.Err, context.DeadlineExceeded) {
						ch <- TurnDone{
							Step:     state.StepCount,
							Reason:   ReasonAborted,
							Err:      ev.Err,
							Messages: state.Messages,
						}
						return
					}

					// 回退到非流式调用（自带重试）
					l.verbose("  ← falling back to non-streaming\n")
					resp, fallbackErr := l.llmClient.SendMessage(sendCtx, messagesForStep, l.requestTools())
					if fallbackErr != nil {
						l.verbose("  ← FALLBACK ERROR: %v\n", fallbackErr)

						// 若 ctx 在重试期间过期，用 Aborted 覆盖 ModelError
						reason := ReasonModelError
						if ctx.Err() != nil {
							reason = ReasonAborted
						}

						ch <- TurnDone{
							Step:     state.StepCount,
							Reason:   reason,
							Err:      fmt.Errorf("stream error: %w (fallback: %v)", ev.Err, fallbackErr),
							Messages: state.Messages,
						}
						return
					}

					// 替换为完整响应(已推送的增量仅影响 TUI 显示,不影响 state.Messages)
					contentBuf = resp.Content
					reasoningBuf = resp.ReasoningContent
					toolCalls = resp.ToolCalls
					webSearchCalls = resp.WebSearchCalls
					if resp.Usage != nil {
						lastPromptTokens = resp.Usage.PromptTokens
						lastUsage = resp.Usage
						lastModel = resp.Model
					}
					break
				}

				// 捕获首帧的 model（API 仅在首帧携带）
				if ev.Model != "" && streamModel == "" {
					streamModel = ev.Model
				}

				// 流式增量 → StreamDelta
				if ev.Delta != "" || ev.ReasoningDelta != "" {
					contentBuf += ev.Delta
					reasoningBuf += ev.ReasoningDelta
					if !sendEvent(ctx, ch, StreamDelta{
						Step:           state.StepCount + 1,
						ContentDelta:   ev.Delta,
						ReasoningDelta: ev.ReasoningDelta,
					}) {
						// ctx 已取消,排空 streamCh 防止 LLM 流生产者 goroutine 泄漏
						go func() {
							for range streamCh {
								// drain
							}
						}()
						break
					}
				}

				// 服务端 web_search 状态 → 虚拟 ToolCall 事件(TUI 进度对齐)。
				// Responses API 模式下搜索由服务端自动执行并注入上下文,
				// 此事件仅用于 TUI 展示,不进入 toolCalls(无需本地执行)。
				if ev.WebSearchStatus != "" {
				if virtual := webSearchVirtualEvent(ev, state.StepCount+1, webSearchStartedAt); virtual != nil {
						if !sendEvent(ctx, ch, virtual) {
							// ctx 已取消,排空 streamCh 防止生产者 goroutine 泄漏
							go func() {
								for range streamCh {
									// drain
								}
							}()
							break
						}
					}
				}

				if ev.Done {
					toolCalls = ev.ToolCalls
					webSearchCalls = ev.WebSearchCalls
					if ev.Usage != nil {
						lastPromptTokens = ev.Usage.PromptTokens
						lastUsage = ev.Usage
						// Responses API 的终态事件(completed)携带 model;
						// chat 模式终态无 model,回退首帧捕获的 streamModel
						if ev.Model != "" {
							lastModel = ev.Model
						} else {
							lastModel = streamModel
						}
					}
					break
				}
			}

			// 流消费循环可能因 ctx 取消而中断，在此统一检测
			if err := ctx.Err(); err != nil {
				ch <- TurnDone{
					Step:     state.StepCount,
					Reason:   ReasonAborted,
					Err:      err,
					Messages: state.Messages,
				}
				return
			}

			// 3. 过滤无效 tool_calls（空 ID / 空 Name / 工具不存在），避免后续 API 400
			if len(toolCalls) > 0 {
				var valid []llm.ToolCall
				for _, tc := range toolCalls {
					if tc.ID == "" || tc.Name == "" {
						l.verbose("  ⚠ stripped invalid tool_call: id=%q name=%q\n", tc.ID, tc.Name)
						continue
					}
					if _, ok := l.toolRegistry.Get(tc.Name); !ok {
						l.verbose("  ⚠ stripped unknown tool: %s\n", tc.Name)
						continue
					}
					valid = append(valid, tc)
				}
				toolCalls = valid
			}
			// 4. 防御：LLM 返回空响应（无 content 无 tool_calls）。
			// 注入最小占位内容避免后续 API 400，累加连续空响应计数器。
			emptyResponse := contentBuf == "" && len(toolCalls) == 0
			if emptyResponse {
				contentBuf = "(empty response)"
				state.ConsecutiveEmpty++
			} else {
				state.ConsecutiveEmpty = 0
			}

			// 5. 追加 assistant 消息。
			// reasoning_content 仅在 tool_calls 场景保留（跨轮延续 DeepSeek 协议要求）。
			// 空响应时注入的占位消息不含 reasoning_content，使模型从干净上下文重新推理。
			assistantMsg := llm.Message{
				ID:             llm.NewMessageID(),
				Role:           llm.RoleAssistant,
				Content:        contentBuf,
				ToolCalls:      toolCalls,
				WebSearchCalls: webSearchCalls,
			}
			if !emptyResponse {
				if reasoningBuf != "" || len(toolCalls) > 0 {
					assistantMsg.ReasoningContent = reasoningBuf
				}
			}
			if lastModel != "" {
				assistantMsg.Model = lastModel
			}
			// 同步上一轮上下文用量到 Loop 字段(供下一轮请求前的 200k 守卫判断)
			l.lastPromptTokens = lastPromptTokens
			if len(toolCalls) > 0 {
				assistantMsg.FinishReason = "tool_calls"
			} else {
				assistantMsg.FinishReason = "stop"
			}
			if lastUsage != nil {
				assistantMsg.Usage = lastUsage
			}
			state.Messages = append(state.Messages, assistantMsg)
			// 5.5 空响应警告：以 user 角色注入，让 LLM 意识到自己行为异常。
			// 对标 buildToolMessages 中的退避警告注入模式（user 消息）。
			if emptyResponse && reasoningBuf != "" && state.ConsecutiveEmpty <= maxConsecutiveSameError {
				warnMsg := llm.Message{
					Role: llm.RoleUser,
					Content: fmt.Sprintf(
						"[system:empty] You have produced %d consecutive responses with thinking (reasoning) but no visible content or tool calls. Your last response was empty — the user cannot see your thoughts, only your output. On this turn, produce actual content or use a tool immediately.",
						state.ConsecutiveEmpty,
					),
				}
				state.Messages = append(state.Messages, warnMsg)
			}
			state.StepCount++

			// 6. 无工具调用且有实际内容 → 完成；空响应继续下一轮
			if len(toolCalls) == 0 {
				if emptyResponse {
					if state.ConsecutiveEmpty > 3 {
						l.verbose("    → too many consecutive empty responses (%d), aborting\n", state.ConsecutiveEmpty)
						ch <- TurnDone{
							Step:     state.StepCount,
							Reason:   ReasonModelError,
							Err:      fmt.Errorf("too many consecutive empty responses (%d)", state.ConsecutiveEmpty),
							Messages: state.Messages,
						}
						return
					}
					l.verbose("    → empty response (reasoning only, consecutive=%d), continuing\n", state.ConsecutiveEmpty)
					continue
				}
				l.verbose("    %s\n", truncateText(contentBuf, 120))

				// 无工具调用时 step 7-8（工具执行 + 压缩检查）不会执行，在此补发。
				// 压缩检查(如有 Compactor)→ StepStats
				var compacted bool
				if l.config.Compactor != nil && lastPromptTokens > 0 {
					tick := l.config.Compactor.Compact(ctx, &state.Messages, lastPromptTokens)
					compacted = true
					if lastUsage != nil {
						ch <- StepStats{
							Step:             state.StepCount,
							Model:            lastModel,
							PromptTokens:     lastPromptTokens,
							CompletionTokens: lastUsage.CompletionTokens,
							CacheHitTokens:   lastUsage.CacheHitTokens,
							CacheMissTokens:  lastUsage.CacheMissTokens,
							ReasoningTokens:  lastUsage.ReasoningTokens,
							MessageCount:     len(state.Messages) - 1,
							Compaction: compactionInfoFromTick(tick),
						}
					}
					if tick.HardLimitReached {
						ch <- TurnDone{
							Step:     state.StepCount,
							Reason:   ReasonModelError,
							Err:      fmt.Errorf("%s", tick.HardLimitReason),
							Messages: state.Messages,
						}
						return
					}
				}
			if !compacted && lastUsage != nil {
				ch <- StepStats{
					Step:             state.StepCount,
					Model:            lastModel,
					PromptTokens:     lastPromptTokens,
					CompletionTokens: lastUsage.CompletionTokens,
					CacheHitTokens:   lastUsage.CacheHitTokens,
					CacheMissTokens:  lastUsage.CacheMissTokens,
					ReasoningTokens:  lastUsage.ReasoningTokens,
					MessageCount:     len(state.Messages) - 1,
				}
			}

			// 最后机会:终止前检测残留的非 completed todo 项,			// 注入提醒并给 LLM 一次额外 step 调用 todo_update。
			if l.config.TodoState != nil && !l.lastChanceTodoInjected {
				snapshot := l.config.TodoState.Snapshot()
				hasIncomplete := false
				for _, t := range snapshot {
					if t.Status != "completed" {
						hasIncomplete = true
						break
					}
				}
				if hasIncomplete {
					l.lastChanceTodoInjected = true
					l.verbose("    → stale todo detected, injecting last-chance reminder\n")
					reminder := llm.Message{
						Role:    llm.RoleUser,
						Content: todoLastChanceText(l.config.TodoState.StatusSummary()),
					}
					state.Messages = append(state.Messages, reminder)
					continue
				}
			}

			// 预告文本保护:模型输出了"预告"式文本(以冒号/箭头等结尾,
			// 暗示接下来还要执行动作)但没有携带工具调用。直接终止会中断任务
			// (评测实测:模型常以 "启动 xxx:" 结尾后漏发工具调用)。
			// 注入 [system:continue] 提醒并继续一轮,给模型一次补发工具调用的机会;
			// 若下一轮仍无工具调用(或本 Run 已提醒过)则正常终止,防死循环。
			// plan mode 同样生效:计划文本虽常以冒号结尾,但预告式结尾同样可能
			// 意味着模型漏发了工具调用(如读完文件后未写 plan),给一次补发机会;
			// previewWarned 保证最多提醒一次,不会造成死循环。
			if !l.previewWarned && hasPreviewSuffix(contentBuf) {
				l.previewWarned = true
				l.verbose("    → preview-style text without tool calls, injecting continue reminder\n")
				// REGRESSION: 注入后 continue 回到 for l.shouldContinue(state),
				// 若 StepCount 已达 MaxSteps 会立即退出,模型看不到提醒、
				// 预告的动作永远不执行。放行一轮(仅一轮,shouldContinue 消费后复位),
				// 给模型补发工具调用的机会。
				if l.config.MaxSteps > 0 && state.StepCount >= l.config.MaxSteps {
					l.previewGraceStep = true
				}
				state.Messages = append(state.Messages, llm.Message{
					Role:    llm.RoleUser,
					Content: previewContinueText,
				})
				continue
			}

			ch <- TurnDone{
				Step:     state.StepCount,
				Reason:   ReasonCompleted,
				Messages: state.Messages,
			}
			return
			}

			l.verbose("    → %d tool calls\n", len(toolCalls))

			// 7. ACT + OBSERVE: 执行工具（含事件推送）
			toolMessages, reason, execErr := l.executeToolCalls(ctx, toolCalls, state, ch)

		// 追加已构造的 tool 消息（即使出错也追加，保证 assistant(tool_calls) ↔ tool 消息配对完整）
		if len(toolMessages) > 0 {
			state.Messages = append(state.Messages, toolMessages...)
		}

		// 更新 todo 周期性提醒计数器（检测本轮是否调用了 todo_update）
		l.updateTodoCounters(toolCalls)

			if execErr != nil {
				l.verbose("  ← ERROR: %v\n", execErr)

				// 若无 tool 消息（执行前已中断），清除 assistant 的 tool_calls 并注入占位内容，				// 避免空 content + 空 tool_calls 导致后续 API 400。
				if len(toolMessages) == 0 {
					lastIdx := len(state.Messages) - 1
					state.Messages[lastIdx].ToolCalls = nil
					if state.Messages[lastIdx].Content == "" {
						state.Messages[lastIdx].Content = "(tool execution error)"
					}
				}

				ch <- TurnDone{
					Step:     state.StepCount,
					Reason:   reason,
					Err:      execErr,
					Messages: state.Messages,
				}
				return
			}

			// 8. 压缩检查 + 推送本 step StepStats(合并压缩结果)
			var compacted bool
			if l.config.Compactor != nil && lastPromptTokens > 0 {
				tick := l.config.Compactor.Compact(ctx, &state.Messages, lastPromptTokens)
				compacted = true

				// 推送合并后的 StepStats(含压缩字段)
				if lastUsage != nil {
					ch <- StepStats{
						Step:             state.StepCount,
						Model:            lastModel,
						PromptTokens:     lastPromptTokens,
						CompletionTokens: lastUsage.CompletionTokens,
						CacheHitTokens:   lastUsage.CacheHitTokens,
						CacheMissTokens:  lastUsage.CacheMissTokens,
						ReasoningTokens:  lastUsage.ReasoningTokens,
						MessageCount:     len(state.Messages),
						Compaction: compactionInfoFromTick(tick),
					}
				}

					if tick.HardLimitReached {
						ch <- TurnDone{
							Step:     state.StepCount,
							Reason:   ReasonModelError,
						Err:      fmt.Errorf("%s", tick.HardLimitReason),
						Messages: state.Messages,
					}
					return
				}
			}
		// 无压缩器时仍推送 StepStats
			if !compacted && lastUsage != nil {
				ch <- StepStats{
					Step:             state.StepCount,
				Model:            lastModel,
				PromptTokens:     lastPromptTokens,
				CompletionTokens: lastUsage.CompletionTokens,
				CacheHitTokens:   lastUsage.CacheHitTokens,
				CacheMissTokens:  lastUsage.CacheMissTokens,
				ReasoningTokens:  lastUsage.ReasoningTokens,
				MessageCount:     len(state.Messages),
			}
		}

		// 周期性 todo 提醒：距上次 todo_update 超过阈值时注入当前状态快照
		l.maybeInjectTodoReminder(state)
		}

		l.verbose("  ⚠ stopped: max steps reached (%d)\n", l.config.MaxSteps)
		ch <- TurnDone{
			Step:     state.StepCount,
			Reason:   ReasonMaxSteps,
			Messages: state.Messages,
		}
	}()

	return ch
}

// shouldContinue 判断循环是否应继续。
// MaxSteps=0 表示无限制,始终继续。
func (l *Loop) shouldContinue(state *TurnState) bool {
	if l.config.MaxSteps == 0 {
		return true
	}
	if state.StepCount < l.config.MaxSteps {
		return true
	}
	// REGRESSION: 预告注入发生在最后一轮(StepCount == MaxSteps)时,
	// 注入后 continue 会在此立即退出,模型看不到 [system:continue] 提醒、
	// 预告的动作从未执行、任务被误判中断。previewGraceStep 放行一轮
	// 补发工具调用;消费后立即复位,仅放行一次,不突破 MaxSteps 语义。
	if l.previewGraceStep {
		l.previewGraceStep = false
		return true
	}
	return false
}

// resolveModel 解析本 step 请求使用的模型。
// Model == llm.ModelChoiceProPlan 时启用 proplan 语义(对齐 Claude Code opusplan):
//   plan mode 且上下文 < planProMaxContextTokens → PlanModel(pro)
//   其余 → SubModel(flash)
// 锚点缺失时互备兜底,两者皆空 → 空(不注入 override,用 client 默认)。
// 不变量:返回值绝不等于 llm.ModelChoiceProPlan —— "proplan" 字符串不进 API。
func (l *Loop) resolveModel(messagesForStep []llm.Message) string {
	model := l.config.Model
	if model != llm.ModelChoiceProPlan {
		return model
	}
	ctxTokens := l.lastPromptTokens
	if ctxTokens == 0 && len(messagesForStep) > 0 {
		// resume 后 lastPromptTokens 为 0(API 用量未知),按消息内容估算兜底
		ctxTokens = estimateContextTokens(messagesForStep)
	}
	if l.plan && ctxTokens < planProMaxContextTokens {
		model = l.config.PlanModel
	} else {
		model = l.config.SubModel
	}
	// 锚点互备 + 自指防线:任一锚点缺失或为 proplan 自身时用另一锚点兜底;
	// 最终值绝不为 "proplan"(畸形手改配置的最终防线)
	if model == "" || model == llm.ModelChoiceProPlan {
		model = l.config.PlanModel
		if model == "" || model == llm.ModelChoiceProPlan {
			model = l.config.SubModel
		}
	}
	if model == llm.ModelChoiceProPlan {
		model = "" // 双锚点均自指:不注入 override,用 client 默认
	}
	return model
}

// estimateContextTokens 粗略估算消息列表的 token 消耗(200k 守卫的 resume 兜底)。
// 与 tool.EstimateTokens 同一估算口径(ASCII ≈ 0.3 token/字符,非 ASCII ≈ 0.6)。
func estimateContextTokens(msgs []llm.Message) int {
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(m.Content)
		sb.WriteString(m.ReasoningContent)
		for _, tc := range m.ToolCalls {
			sb.WriteString(tc.Arguments)
		}
	}
	// 安全系数 1.3:文本估算不含 role 标记、消息边界与 tool schema 的 token
	// 开销(二审审查建议),保守偏向降级,防 resume 首轮超窗仍误用 pro。
	return tool.EstimateTokens(sb.String()) * 13 / 10
}

// toLLMToolSpecs 将 tool.ToolSpec 切片转换为 llm.ToolSpec 切片。
//
// tool.ToolSpec.Parameters 是 json.RawMessage，赋给 llm.ToolSpec.Parameters
// (interface{}) 是安全的 — json.RawMessage 实现了 json.Marshaler，// 在 LLM adapter 序列化时输出原始 JSON Schema 字节。
func toLLMToolSpecs(specs []tool.ToolSpec) []llm.ToolSpec {
	result := make([]llm.ToolSpec, len(specs))
	for i, s := range specs {
		result[i] = llm.ToolSpec{
			Name:        s.Name,
			Description: s.Description,
			Parameters:  s.Parameters,
			Prompt:      s.Prompt,
		}
	}
	return result
}

// requestTools 返回本 loop 请求侧的工具 schema:
// ToolsOverride 非空(如 fork 子代理对齐父 tools)→ 使用覆盖列表;
// 否则由注册表派生。执行/分发始终走 toolRegistry,不受 override 影响。
func (l *Loop) requestTools() []llm.ToolSpec {
	if len(l.config.ToolsOverride) > 0 {
		return l.config.ToolsOverride
	}
	return toLLMToolSpecs(l.toolRegistry.List())
}

// webSearchVirtualEvent 将服务端 web_search 状态转为虚拟 ToolCall 事件。
// Responses API(deepseek-v4-flash)模式下搜索由服务端自动执行:
// - in_progress → ToolCallStart:创建 web_search 段落 + spinner(TUI 进度反馈)
// - completed → ToolCallResult:段落完成,展示搜索结果注入说明
// - searching 中间态无附加信息,忽略
// 虚拟事件不进入 toolCalls 列表,不会触发本地执行或产生 tool 消息。
func webSearchVirtualEvent(ev llm.StreamingEvent, step int, startedAt map[string]time.Time) StepEvent {
	switch ev.WebSearchStatus {
	case "in_progress":
		startedAt[ev.WebSearchCallID] = time.Now()
		return ToolCallStart{
			Step:         step,
			ToolCallID:   ev.WebSearchCallID,
			ToolCallName: "web_search",
			Arguments:    "{}",
			ServerSide:   true,
		}
	case "searching":
		// 兜底:in_progress 事件丢失时(服务端可能省略),用 searching 补记起点,
		// 确保 completed 时能计算真实耗时而非 0ms;同时补发 ToolCallStart
		// 创建段落,否则 completed 的 ToolCallResult 无匹配段落被 TUI 静默丢弃
		if _, ok := startedAt[ev.WebSearchCallID]; !ok {
			startedAt[ev.WebSearchCallID] = time.Now()
			return ToolCallStart{
				Step:         step,
				ToolCallID:   ev.WebSearchCallID,
				ToolCallName: "web_search",
				Arguments:    "{}",
				ServerSide:   true,
			}
		}
		return nil
	case "completed":
		var durationMs int64
		if start, ok := startedAt[ev.WebSearchCallID]; ok {
			durationMs = time.Since(start).Milliseconds()
			delete(startedAt, ev.WebSearchCallID)
		}
		// 服务端实际执行的搜索词(防御性解析;DeepSeek 文档未承诺该字段)
		query := strings.Join(ev.WebSearchQueries, ", ")
		result := "Server-side search completed"
		if query != "" {
			result += fmt.Sprintf(" — %q", query)
		}
		return ToolCallResult{
			Step:         step,
			ToolCallID:   ev.WebSearchCallID,
			ToolCallName: "web_search",
			DurationMs:   durationMs,
			ServerSide:   true,
			Result: result + "\n\n" +
				"Search results have been injected into context by DeepSeek Responses API.",
		}
	default:
		return nil
	}
}

// verbose 打印调试日志。仅 Debug 级别时才格式化,避免热路径浪费。
func (l *Loop) verbose(format string, args ...any) {
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	slog.Debug(fmt.Sprintf(format, args...))
}

// truncateText 截断字符串到 maxLen，追加 "…"。
func truncateText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "…"
}

// sendEvent 发送事件到 channel,若 ctx 已取消则跳过发送并返回 false。
func sendEvent(ctx context.Context, ch chan<- StepEvent, ev StepEvent) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// todoLastChanceText 构造最后机会提醒文本:告知 LLM 即将终止但有残留任务。
func todoLastChanceText(summary string) string {
	return summary + "\n\n" +
		"[system:todo] You are about to finish, but your todo list still has incomplete tasks. " +
		"If all work is actually done, call todo_update to mark them as 'completed' before giving your final answer. " +
		"If work remains, continue working. This is your last automatic reminder."
}

// previewContinueText 是模型输出"预告文本"(以冒号/箭头等结尾)但未携带
// 工具调用时注入的 user 提醒,引导模型补发工具调用或明确收尾。
// 措辞为后缀形态的事实描述(与 hasPreviewSuffix 的后缀一致),避免误报时
// 模型困惑;补充"上一条消息已入历史"确认;总结选项提示勿再以预告后缀结尾,
// 防止再次触发检测。
const previewContinueText = "[system:continue] Your last message ended with a trailing preview marker (\":\", \"\uFF1A\", \"-\", \"\u2192\" or \"...\") but included no tool calls — that message is already part of the conversation history. If you still intend to perform the announced action, call the tool(s) now. If you are genuinely finished, reply with a final summary (do not end it with a trailing preview marker)."

// hasPreviewSuffix 检测文本是否以"预告后缀"结尾(冒号/中文冒号/连字符/箭头/省略号),
// 表明模型宣告了下一步动作但未附带工具调用。评测实测(deepseek-v4-flash):
// 模型常输出 "启动 xxx:" 之类的预告文本后漏发工具调用,导致任务被误判完成。
func hasPreviewSuffix(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	// REGRESSION: 此前第 5 个后缀是 U+2026(...),与 previewContinueText 文案
	// 声明的 ASCII "..." 不一致;英文模型输出 "checking files..."(三个 ASCII
	// 点)时预告防御失效,任务被误判完成。补上 ASCII 三连点,两者都匹配。
	// REGRESSION: 冒号类后缀仅覆盖 U+003A/U+FF1A,其余视觉形似冒号的变体
	// (小冒号 U+FE55、比号 U+2236、修饰符冒号 U+A789、竖排冒号 U+FE13、
	// 比例号 U+2237、亚美尼亚句号 U+0589、希伯来标点 U+05C3、语音学冒号
	// U+02D0/U+02F8、希腊问号 U+037E)结尾的预告文本被跳过,任务误判完成。
	// 全部纳入匹配;用 \uXXXX 转义锁定字节,防编辑工具 Unicode 归一化破坏。
	for _, suffix := range []string{
		":",      // U+003A COLON
		"\uFF1A", // FULLWIDTH COLON
		"\uFE55", // SMALL COLON
		"\u2236", // RATIO
		"\uA789", // MODIFIER LETTER COLON
		"\uFE13", // PRESENTATION FORM FOR VERTICAL COLON
		"\u2237", // PROPORTION
		"\u0589", // ARMENIAN FULL STOP
		"\u05C3", // HEBREW PUNCTUATION SOF PASUQ
		"\u02D0", // MODIFIER LETTER TRIANGULAR COLON
		"\u02F8", // MODIFIER LETTER RAISED COLON
		"\u037E", // GREEK QUESTION MARK
		"-",
		"\u2192", // RIGHTWARDS ARROW
		"...",
		"\u2026", // HORIZONTAL ELLIPSIS
	} {
		if strings.HasSuffix(t, suffix) {
			return true
		}
	}
	return false
}

// injectTodoStatus 在每轮 LLM 调用前将当前 todo 状态注入消息列表。
// 始终追加新消息（Append 策略），不更新已有消息以避免破坏前缀缓存。
// messagesForStep 是每个 step 重建的临时切片,追加的消息不会泄漏到 state.Messages。
func (l *Loop) injectTodoStatus(msgs *[]llm.Message) {
	if l.config.TodoState == nil {
		return
	}
	summary := l.config.TodoState.StatusSummary()
	if summary == "" {
		l.lastTodoStatusSummary = ""
		return
	}
	// 仅在状态变化时注入:重复快照无新增信息,却每步占用 cache-miss 区
	// token(实测 ~20 次/轮 ≈ 全部 miss 的 26%);周期可见性由
	// maybeInjectTodoReminder(持久化、前缀缓存友好)兜底,防遗忘不受损。
	if summary == l.lastTodoStatusSummary {
		return
	}
	l.lastTodoStatusSummary = summary
	*msgs = append(*msgs, llm.Message{
		Role:    llm.RoleUser,
		Content: summary,
	})
}

// todoReminderText 构造 todo 提醒消息文本:状态摘要 + 提醒引导。
func todoReminderText(summary string, stepsSince int) string {
	return summary + "\n\n" +
		fmt.Sprintf("[system:todo] %d steps since last todo_update. If you have completed a task or your focus changed, call todo_update to sync the list — otherwise ignore this reminder (do NOT call todo_update just to acknowledge it).", stepsSince)
}

// updateTodoCounters 在每个 step 工具执行后更新 todo 提醒计数器。
// 当无活跃任务时保持计数器归零（无需提醒）；否则递增。
// 注意：todo_create / todo_update 成功执行时计数器已在 executeTodoMutate 内重置，// 此处仅处理递增逻辑。
func (l *Loop) updateTodoCounters(toolCalls []llm.ToolCall) {
	// 无活跃任务时无需提醒,保持计数器归零
	if l.config.TodoState != nil && len(l.config.TodoState.Snapshot()) == 0 {
		l.stepsSinceLastTodoWrite = 0
		l.stepsSinceLastTodoReminder = 0
		return
	}

	l.stepsSinceLastTodoWrite++
	l.stepsSinceLastTodoReminder++
}

// maybeInjectTodoReminder 在距上次 todo_update 超过 idleTodoWrite 个 step 后,// 向 messages 追加当前 todo 状态快照 + 提醒文字。
// 使用 Append 策略避免破坏前缀缓存。
// 两次提醒之间至少间隔 idleTodoReminder 个 step。
func (l *Loop) maybeInjectTodoReminder(state *TurnState) {
	if l.config.TodoState == nil {
		return
	}

	snapshot := l.config.TodoState.Snapshot()
	if len(snapshot) == 0 {
		return
	}

	if l.stepsSinceLastTodoWrite < idleTodoWrite || l.stepsSinceLastTodoReminder < idleTodoReminder {
		return
	}

	// 注入提醒后重置提醒计数器（但保留 todo_update 计数器，	// 因为提醒不能替代真正的 todo_update 更新）
	l.stepsSinceLastTodoReminder = 0

	msg := todoReminderText(l.config.TodoState.StatusSummary(), l.stepsSinceLastTodoWrite)

	state.Messages = append(state.Messages, llm.Message{
		Role:    llm.RoleUser,
		Content: msg,
	})
}

