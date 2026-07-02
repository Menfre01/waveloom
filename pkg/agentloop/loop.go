// Package agentloop 实现 Waveloom Code Agent 的 Think-Act-Observe 循环。
//
// Loop 是连接 LLM Client 和 Tool System 的编排器，在每个 turn 中：
//  1. 组装上下文，调用 LLM（Think）
//  2. 解析响应，执行工具（Act）
//  3. 收集结果，更新状态（Observe）
//  4. 判断是否继续或终止
package agentloop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// Config — 不可变配置
// ---------------------------------------------------------------------------

// Config 保存 Loop 的不可变配置。
// 构造时传入，运行期不变。
type Config struct {
	// MaxTurns 最大 turn 数，0 表示无限制。
	// 每次调用 LLM 后 TurnCount 加 1，达到上限时循环终止。
	MaxTurns int

	// SystemPrompt 系统提示词，Run 启动时作为 Messages 的第一条 system 消息注入。
	// 为空时不注入，调用方需自行在 messages 中包含 system 消息。
	SystemPrompt string

	// Guard 权限守门人，在工具执行前做权限检查。
	// nil → 跳过权限检查，所有操作允许（向后兼容）。
	Guard permission.Guard

	// UserResponder 处理 ask 决策的用户交互。
	// nil → ask 自动降级为 deny。
	UserResponder permission.UserResponder

	// VerboseWriter 非 nil 时，Loop 在每一步输出明细到该 Writer。
	// 典型使用: os.Stderr（CLI）、log.Writer()、或 ioutil.Discard。
	// 输出格式为 "→ LLM Turn N …" / "← Response …" / "→ tool …" 前缀行。
	VerboseWriter io.Writer

	// Compactor 每轮 LLM 调用后执行上下文压缩。
	// nil → 跳过（向后兼容，由 CompleteRun 兜底）。
	Compactor compaction.Compactor

	// ToolTimeout 单个工具执行的最大时长。
	// 0 → 无超时限制（向后兼容）。
	// 设为正值时，每个工具执行会在独立的超时 context 中运行，
	// 防止工具因未正确处理 ctx 取消而永久阻塞 loop。
	ToolTimeout time.Duration

	// PlanFile plan 文件路径（首次进入 plan 时自动生成 slug 文件名）。
	// 仅在 plan 模式下有效。
	PlanFile string
}

// DefaultToolTimeout 是单个工具执行的推荐超时时间（10 分钟）。
// 覆盖 shell（最长 600s）和 web_fetch 等所有工具类型。
const DefaultToolTimeout = 10 * time.Minute

// DefaultConfig 返回带推荐默认值的 Config。
func DefaultConfig() Config {
	return Config{
		ToolTimeout: DefaultToolTimeout,
	}
}

// ---------------------------------------------------------------------------
// LoopState — 迭代间可变状态
// ---------------------------------------------------------------------------

// LoopState 持有迭代间可变的状态。
type LoopState struct {
	Messages  []llm.Message
	TurnCount int

	// ConsecutiveEmpty 记录连续收到空响应的次数。
	// 当 LLM 连续返回无 content 且无 tool_calls 的推理专用响应时递增，
	// 达到上限后循环终止以防止死循环。
	ConsecutiveEmpty int

	// LastErrorKind 记录上一轮工具错误的 Kind。
	// 用于检测同类错误连续重试。
	LastErrorKind string

	// ConsecutiveSameError 记录同一 ErrorKind 连续出现的次数。
	// 达到上限（3）后 loop 强制终止，防止探测死循环。
	ConsecutiveSameError int

	// AnyToolSucceeded 标记本轮是否有任何工具成功执行。
	// 成功时重置 ConsecutiveSameError 计数。
	AnyToolSucceeded bool
}

// maxConsecutiveSameError 是同类工具错误的容忍上限。
// 达到后 loop 强制终止，避免 LLM 陷入无限重试探测。
// 阈值设为 5 轮：给 LLM 充分的自主纠错空间，同时保留兜底防护，
// 防止 LLM 在不可恢复的错误上无限重试。
const maxConsecutiveSameError = 5



// ---------------------------------------------------------------------------
// TerminalReason — 终止原因
// ---------------------------------------------------------------------------

// TerminalReason 描述 Loop 终止的原因。
type TerminalReason string

const (
	// ReasonCompleted LLM 给出最终答案，无 tool call。
	ReasonCompleted TerminalReason = "completed"

	// ReasonMaxTurns 达到 MaxTurns 限制。
	ReasonMaxTurns TerminalReason = "max_turns"

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
	planPairID  string // START/END 配对 ID（4 位 hex，如 "a3f7"）
	approvedPlan string // 审批通过的 plan 内容（用于 executeToolCalls 在 tool 消息后注入 [plan:end]）
}

// New 创建一个新的 Loop 实例。
func New(llmClient llm.Client, toolRegistry tool.Registry, config Config) *Loop {
	return &Loop{
		llmClient:    llmClient,
		toolRegistry: toolRegistry,
		config:       config,
	}
}

// SetPlanFile 设置 plan 文件路径（用户快捷键进入 plan 模式时由 TUI 调用）。
func (l *Loop) SetPlanFile(planFile string) {
	l.config.PlanFile = planFile
}

// SetPlanMode 启用 plan 模式并注入 START 消息（用户快捷键进入 plan 模式时由 TUI 调用）。
// 返回 [plan:start #xxxx] user 消息，调用方需将其注入 messages。
func (l *Loop) SetPlanMode(planFile string) (planPairID string, startMessage llm.Message) {
	l.plan = true
	l.planPairID = generatePairID()
	l.config.PlanFile = planFile
	l.config.Guard.EnterPlanMode(planFile)
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

// ResetPlanMode 由 TUI 调用，在用户快捷键退出 plan 模式时清除 Loop 内部 plan 状态。
// 仅重置 l.plan / l.planPairID / l.config.PlanFile，不操作 Guard（Guard 由 TUI 层统一管理）。
func (l *Loop) ResetPlanMode() {
	l.plan = false
	l.planPairID = ""
	l.config.PlanFile = ""
	l.approvedPlan = ""
}

// Run 执行主循环，逐轮推送 TurnEvent 到返回的 channel。
// channel 在 loop 终止后关闭，最后一个事件为 PhaseDone。
//
// 不变量：
//  1. 消息顺序：System → User → Assistant → Tool → Assistant → ... 严格遵守
//  2. Turn 计数：每次调用 LLM 后 +1，表示已完成的轮次数（无论工具执行结果如何）
//  3. 终止互斥：每个 Run 有且仅有一个 PhaseDone 事件
//  4. 错误不丢上下文：即使因错误终止，PhaseDone.Messages 仍包含已执行的操作历史
//  5. Context 优先：每次迭代开始先检查 ctx.Err()
//  6. 并发安全：ConcurrentSafe 工具并行执行，非安全工具串行执行
func (l *Loop) Run(ctx context.Context, messages []llm.Message) <-chan TurnEvent {
	ch := make(chan TurnEvent, 32)

	go func() {
		defer close(ch)

		// panic 防御：捕获 loop 内任何未预期 panic，转为 LoopDone 事件后关闭 channel，
		// 确保消费者（TUI/runner）不会因 channel 关闭而无 LoopDone 导致永久等待。
		var state *LoopState
		defer func() {
			if r := recover(); r != nil {
				msgs := messages
				turn := 0
				if state != nil {
					msgs = state.Messages
					turn = state.TurnCount
				}
				ch <- LoopDone{
					Turn:     turn,
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

		state = &LoopState{
			Messages: messages,
		}

		for l.shouldContinue(state) {
			// 1. Context 取消检查
			if err := ctx.Err(); err != nil {
				ch <- LoopDone{
					Turn:     state.TurnCount,
					Reason:   ReasonAborted,
					Err:      err,
					Messages: state.Messages,
				}
				return
			}

			// 2. THINK: 流式调用 LLM
			l.verbose("→ LLM call #%d  (messages=%d, tools=%d)\n",
				state.TurnCount+1, len(state.Messages), len(l.toolRegistry.List()))

			var lastPromptTokens int      // 本轮 API 返回的 prompt_tokens
			var lastUsage       *llm.UsageInfo // 暂存 usage，压缩后统一推送 TurnStats
			var lastModel       string         // 暂存 model
			streamCh, err := l.llmClient.SendMessageStream(ctx, state.Messages, toLLMToolSpecs(l.toolRegistry.List()))
			if err != nil {
				l.verbose("  ← ERROR: %v\n", err)
				ch <- LoopDone{
					Turn:     state.TurnCount,
					Reason:   ReasonModelError,
					Err:      fmt.Errorf("llm call: %w", err),
					Messages: state.Messages,
				}
				return
			}

			// 消费流式事件
			var contentBuf, reasoningBuf string
			var toolCalls []llm.ToolCall
			var streamModel string
			for ev := range streamCh {
				if ev.Err != nil {
					l.verbose("  ← STREAM ERROR: %v\n", ev.Err)

					// Context 取消 → 不回退，立即终止
					if errors.Is(ev.Err, context.Canceled) || errors.Is(ev.Err, context.DeadlineExceeded) {
						ch <- LoopDone{
							Turn:     state.TurnCount,
							Reason:   ReasonAborted,
							Err:      ev.Err,
							Messages: state.Messages,
						}
						return
					}

					// 回退到非流式调用（自带重试）
					l.verbose("  ← falling back to non-streaming\n")
					resp, fallbackErr := l.llmClient.SendMessage(ctx, state.Messages, toLLMToolSpecs(l.toolRegistry.List()))
					if fallbackErr != nil {
						l.verbose("  ← FALLBACK ERROR: %v\n", fallbackErr)

						// 若 ctx 在重试期间过期，用 Aborted 覆盖 ModelError
						reason := ReasonModelError
						if ctx.Err() != nil {
							reason = ReasonAborted
						}

						ch <- LoopDone{
							Turn:     state.TurnCount,
							Reason:   reason,
							Err:      fmt.Errorf("stream error: %w (fallback: %v)", ev.Err, fallbackErr),
							Messages: state.Messages,
						}
						return
					}

					// 替换为完整响应（已推送的增量仅影响 TUI 显示，不影响 state.Messages）
					contentBuf = resp.Content
					reasoningBuf = resp.ReasoningContent
					toolCalls = resp.ToolCalls
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
						Turn:           state.TurnCount + 1,
						ContentDelta:   ev.Delta,
						ReasoningDelta: ev.ReasoningDelta,
					}) {
						// ctx 已取消，跳出流消费循环，由下方的 ctx.Err() 检测统一终止
						break
					}
				}

				if ev.Done {
					toolCalls = ev.ToolCalls
					if ev.Usage != nil {
						lastPromptTokens = ev.Usage.PromptTokens
						lastUsage = ev.Usage
						lastModel = streamModel
					}
					break
				}
			}

			// 流消费循环可能因 ctx 取消而中断，在此统一检测
			if err := ctx.Err(); err != nil {
				ch <- LoopDone{
					Turn:     state.TurnCount,
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
			//    注入最小占位内容避免后续 API 400，累加连续空响应计数器。
			//    注意：空响应时不应保留 reasoning_content —— DeepSeek 要求 tool_calls
			//    场景下回传，但空响应时回传会污染下一轮上下文，导致模型持续空输出。
			emptyResponse := contentBuf == "" && len(toolCalls) == 0
			if emptyResponse {
				contentBuf = "(empty response)"
				state.ConsecutiveEmpty++
			} else {
				state.ConsecutiveEmpty = 0
			}

			// 5. 追加 assistant 消息。
			//    reasoning_content 仅在 tool_calls 场景保留（跨轮延续 DeepSeek 协议要求）。
			//    空响应时注入的占位消息不含 reasoning_content，使模型从干净上下文重新推理。
			assistantMsg := llm.Message{
				Role:      llm.RoleAssistant,
				Content:   contentBuf,
				ToolCalls: toolCalls,
			}
			if len(toolCalls) > 0 {
				assistantMsg.ReasoningContent = reasoningBuf
			}
			state.Messages = append(state.Messages, assistantMsg)
			state.TurnCount++

			// 6. 无工具调用且有实际内容 → 完成；空响应继续下一轮
			if len(toolCalls) == 0 {
				if emptyResponse {
					if state.ConsecutiveEmpty > 3 {
						l.verbose("    → too many consecutive empty responses (%d), aborting\n", state.ConsecutiveEmpty)
						ch <- LoopDone{
							Turn:     state.TurnCount,
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
				// 压缩检查（如有 Compactor）→ TurnStats
				var compacted bool
				if l.config.Compactor != nil && lastPromptTokens > 0 {
					tick := l.config.Compactor.Compact(ctx, &state.Messages, lastPromptTokens)
					compacted = true
					if lastUsage != nil {
						ch <- TurnStats{
							Turn:             state.TurnCount,
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
						ch <- LoopDone{
							Turn:     state.TurnCount,
							Reason:   ReasonModelError,
							Err:      fmt.Errorf("%s", tick.HardLimitReason),
							Messages: state.Messages,
						}
						return
					}
				}
				if !compacted && lastUsage != nil {
					ch <- TurnStats{
						Turn:             state.TurnCount,
						Model:            lastModel,
						PromptTokens:     lastPromptTokens,
						CompletionTokens: lastUsage.CompletionTokens,
						CacheHitTokens:   lastUsage.CacheHitTokens,
						CacheMissTokens:  lastUsage.CacheMissTokens,
						ReasoningTokens:  lastUsage.ReasoningTokens,
						MessageCount:     len(state.Messages) - 1,
					}
				}

				ch <- LoopDone{
					Turn:     state.TurnCount,
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

			if execErr != nil {
				l.verbose("  ← ERROR: %v\n", execErr)

				// 若无 tool 消息（执行前已中断），清除 assistant 的 tool_calls 并注入占位内容，
				// 避免空 content + 空 tool_calls 导致后续 API 400。
				if len(toolMessages) == 0 {
					lastIdx := len(state.Messages) - 1
					state.Messages[lastIdx].ToolCalls = nil
					if state.Messages[lastIdx].Content == "" {
						state.Messages[lastIdx].Content = "(tool execution error)"
					}
				}

				ch <- LoopDone{
					Turn:     state.TurnCount,
					Reason:   reason,
					Err:      execErr,
					Messages: state.Messages,
				}
				return
			}

			// 8. 压缩检查 + 推送本轮 TurnStats（合并压缩结果）
			var compacted bool
			if l.config.Compactor != nil && lastPromptTokens > 0 {
				tick := l.config.Compactor.Compact(ctx, &state.Messages, lastPromptTokens)
				compacted = true

				// 推送合并后的 TurnStats（含压缩字段）
				if lastUsage != nil {
					ch <- TurnStats{
						Turn:             state.TurnCount,
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
					ch <- LoopDone{
						Turn:     state.TurnCount,
						Reason:   ReasonModelError,
						Err:      fmt.Errorf("%s", tick.HardLimitReason),
						Messages: state.Messages,
					}
					return
				}
			}
			// 无压缩器时仍推送 TurnStats
			if !compacted && lastUsage != nil {
				ch <- TurnStats{
					Turn:             state.TurnCount,
					Model:            lastModel,
					PromptTokens:     lastPromptTokens,
					CompletionTokens: lastUsage.CompletionTokens,
					CacheHitTokens:   lastUsage.CacheHitTokens,
					CacheMissTokens:  lastUsage.CacheMissTokens,
					ReasoningTokens:  lastUsage.ReasoningTokens,
					MessageCount:     len(state.Messages),
				}
			}
		}

		l.verbose("  ⚠ stopped: max turns reached (%d)\n", l.config.MaxTurns)
		ch <- LoopDone{
			Turn:     state.TurnCount,
			Reason:   ReasonMaxTurns,
			Messages: state.Messages,
		}
	}()

	return ch
}

// shouldContinue 判断循环是否应继续。
// MaxTurns=0 表示无限制，始终继续。
func (l *Loop) shouldContinue(state *LoopState) bool {
	if l.config.MaxTurns == 0 {
		return true
	}
	return state.TurnCount < l.config.MaxTurns
}

// toLLMToolSpecs 将 tool.ToolSpec 切片转换为 llm.ToolSpec 切片。
//
// tool.ToolSpec.Parameters 是 json.RawMessage，赋给 llm.ToolSpec.Parameters
// (interface{}) 是安全的 — json.RawMessage 实现了 json.Marshaler，
// 在 LLM adapter 序列化时输出原始 JSON Schema 字节。
func toLLMToolSpecs(specs []tool.ToolSpec) []llm.ToolSpec {
	result := make([]llm.ToolSpec, len(specs))
	for i, s := range specs {
		result[i] = llm.ToolSpec{
			Name:        s.Name,
			Description: s.Description,
			Parameters:  s.Parameters,
		}
	}
	return result
}

// verbose 打印调试行到 VerboseWriter（如已配置）。
func (l *Loop) verbose(format string, args ...any) {
	if l.config.VerboseWriter == nil {
		return
	}
	_, _ = fmt.Fprintf(l.config.VerboseWriter, format, args...)
}

// truncateText 截断字符串到 maxLen，追加 "…"。
func truncateText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "…"
}

// sendEvent 发送事件到 channel，若 ctx 已取消则跳过发送并返回 false。
func sendEvent(ctx context.Context, ch chan<- TurnEvent, ev TurnEvent) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}
