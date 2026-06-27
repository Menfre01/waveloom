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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"waveloom/pkg/compaction"
	"waveloom/pkg/llm"
	"waveloom/pkg/permission"
	"waveloom/pkg/tool"
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
}

// DefaultConfig 返回带推荐默认值的 Config。
func DefaultConfig() Config {
	return Config{}
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
}

// New 创建一个新的 Loop 实例。
func New(llmClient llm.Client, toolRegistry tool.Registry, config Config) *Loop {
	return &Loop{
		llmClient:    llmClient,
		toolRegistry: toolRegistry,
		config:       config,
	}
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

		// 注入 SystemPrompt（如已配置且 messages 中尚无 system 消息）
		if l.config.SystemPrompt != "" {
			hasSystem := len(messages) > 0 && messages[0].Role == llm.RoleSystem
			if !hasSystem {
				messages = append([]llm.Message{
					{Role: llm.RoleSystem, Content: l.config.SystemPrompt},
				}, messages...)
			}
		}

		state := &LoopState{
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
					ch <- StreamDelta{
						Turn:           state.TurnCount + 1,
						ContentDelta:   ev.Delta,
						ReasoningDelta: ev.ReasoningDelta,
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
			// 4. 防御：LLM 返回空响应（无 content 无 tool_calls）会导致后续 API 400，
			//    此时注入最小占位内容，并累加连续空响应计数器。
			emptyResponse := contentBuf == "" && len(toolCalls) == 0
			if emptyResponse {
				contentBuf = "(empty response)"
				state.ConsecutiveEmpty++
			} else {
				state.ConsecutiveEmpty = 0
			}

			// 5. 追加 assistant 消息。
			//    reasoning_content 保留条件：有 tool_calls（跨轮延续 DeepSeek 协议要求），
			//    或空响应（注入占位后继续下一轮，reasoning 需带入后续调用）。
			assistantMsg := llm.Message{
				Role:      llm.RoleAssistant,
				Content:   contentBuf,
				ToolCalls: toolCalls,
			}
			if len(toolCalls) > 0 || emptyResponse {
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
							Compaction: CompactionInfo{
								TokensSaved:              tick.TokensSaved,
								Tier:                     tick.Tier,
								SummaryDone:              tick.Tier3SummaryDone,
								HardLimitReached:          tick.HardLimitReached,
								HardLimitReason:           tick.HardLimitReason,
								UsageRatio:                tick.UsageRatio,
								Tier3ConsecutiveFailures:  tick.Tier3ConsecutiveFailures,
							},
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
						Compaction: CompactionInfo{
							TokensSaved:              tick.TokensSaved,
							Tier:                     tick.Tier,
							SummaryDone:              tick.Tier3SummaryDone,
							HardLimitReached:          tick.HardLimitReached,
							HardLimitReason:           tick.HardLimitReason,
							UsageRatio:                tick.UsageRatio,
							Tier3ConsecutiveFailures:  tick.Tier3ConsecutiveFailures,
						},
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

// executeToolCalls 按并发安全性分区执行工具调用。
//
// 执行流程：
//  1. 按 ConcurrentSafe() 分区：并发安全组 + 串行组
//  2. 并发组：逐工具推送 ToolCallStart → 权限检查 → 放行则收集；被拒则立即推送 ToolCallResult{Denied:true}
//  3. 并行执行放行的并发工具，完成后推送 ToolCallResult
//  4. 串行组：逐工具推送 ToolCallStart → 权限检查 → 被拒立即推送 ToolCallResult{Denied:true}；放行则执行并推送 ToolCallResult
//  5. 按原始 ToolCall 顺序构造 tool 消息
//
// 权限检查：
//   - allow → 正常执行
//   - deny → 构造拒绝消息，作为 Recoverable error 返回给 LLM
//   - ask → 调用 UserResponder，allow 则执行，deny 则拒绝
//
// 错误处理：
//   - Fatal → 直接返回 TerminalReason
//   - Recoverable → 作为 tool 消息内容返回给 LLM，由 LLM 根据错误反馈自行修正
func (l *Loop) executeToolCalls(ctx context.Context, calls []llm.ToolCall, state *LoopState, ch chan<- TurnEvent) (msgs []llm.Message, termReason TerminalReason, execErr error) {
	// 1. 按 ConcurrentSafe 分区
	var concurrent, serial []llm.ToolCall
	for _, tc := range calls {
		t, ok := l.toolRegistry.Get(tc.Name)
		if !ok {
			return nil, ReasonToolFatal, fmt.Errorf("unknown tool %q", tc.Name)
		}
		if t.ConcurrentSafe() {
			concurrent = append(concurrent, tc)
		} else {
			serial = append(serial, tc)
		}
	}

	// 结果存储，key = ToolCall.ID
	results := make(map[string]*tool.ToolResult, len(calls))
	durations := make(map[string]time.Duration, len(calls))
	skip := make(map[string]bool, len(calls))

	// defer: 确保所有 tool call 都有对应的 tool 消息，
	// 即使中途因 context 取消或执行错误提前返回，也不破坏消息配对完整性。
	defer func() {
		if msgs == nil {
			msgs, _, _ = l.buildToolMessages(calls, results, skip, state)
		}
	}()

	// 2. 并发组：逐工具推送 Start → 权限检查 → 放行则收集（每个工具独立判断）
	var toExec []llm.ToolCall

	for _, tc := range concurrent {
		if !sendEvent(ctx, ch, ToolCallStart{
			Turn:         state.TurnCount,
			ToolCallID:   tc.ID,
			ToolCallName: tc.Name,
			Arguments:    tc.Arguments,
		}) {
			return nil, ReasonAborted, ctx.Err()
		}

		if l.checkPermission(ctx, tc, results, skip) {
			r := results[tc.ID]
			if !sendEvent(ctx, ch, ToolCallResult{
				Turn:         state.TurnCount,
				ToolCallID:   tc.ID,
				ToolCallName: tc.Name,
				Result:       r.Content,
				Error:        r.Error.Message,
				ErrorKind:    r.Error.Kind,
				Denied:       true,
				DiffHunks:    r.Meta.DiffHunks,
			}) {
				return nil, ReasonAborted, ctx.Err()
			}
		} else {
			toExec = append(toExec, tc)
		}
	}

	// 3. 并行执行放行的并发工具
	if len(toExec) > 0 {
		var mu sync.Mutex
		var wg sync.WaitGroup
		var firstExecErr error
		type execResult struct {
			tc     llm.ToolCall
			result *tool.ToolResult
			err    error
			start  time.Time
		}
		resultsCh := make(chan execResult, len(toExec))

		for _, tc := range toExec {
			wg.Add(1)
			go func(tc llm.ToolCall) {
				defer wg.Done()
				start := time.Now()
				result, execErr := l.toolRegistry.Execute(ctx, tc.Name, json.RawMessage(tc.Arguments))
				resultsCh <- execResult{tc: tc, result: result, err: execErr, start: start}
			}(tc)
		}
		wg.Wait()
		close(resultsCh)

		for er := range resultsCh {
			mu.Lock()
			if er.err != nil {
				results[er.tc.ID] = &tool.ToolResult{
					Error: &tool.ToolError{
						Class:   tool.ErrorClassFatal,
						Kind:    tool.ErrKindUnknownTool,
						Message: er.err.Error(),
					},
				}
				if firstExecErr == nil {
					firstExecErr = er.err
				}
			} else if er.result == nil {
				results[er.tc.ID] = &tool.ToolResult{
					Error: &tool.ToolError{
						Class:   tool.ErrorClassFatal,
						Kind:    tool.ErrKindUnknownTool,
						Message: fmt.Sprintf("tool %q returned nil result", er.tc.Name),
					},
				}
				if firstExecErr == nil {
					firstExecErr = fmt.Errorf("tool %q returned nil result", er.tc.Name)
				}
			} else {
				results[er.tc.ID] = er.result
				dur := time.Since(er.start)
				durations[er.tc.ID] = dur
				l.verbose("    ✔ %s  (%s | %d bytes | %s)\n",
					er.tc.Name, dur.Round(time.Millisecond),
					len(er.result.Content), truncateText(er.result.Content, 60),
				)
			}
			mu.Unlock()
		}

		if firstExecErr != nil {
			return nil, ReasonToolFatal, fmt.Errorf("concurrent tool execution: %w", firstExecErr)
		}
	}

	// 推送已执行并发工具的 ToolCallResult
	for _, tc := range toExec {
		r := results[tc.ID]
		ev := ToolCallResult{
			Turn:         state.TurnCount,
			ToolCallID:   tc.ID,
			ToolCallName: tc.Name,
			DurationMs:   durations[tc.ID].Milliseconds(),
			Result:       r.Content,
			DiffHunks:    r.Meta.DiffHunks,
		}
		if r.IsError() {
			ev.Error = r.Error.Message
			ev.ErrorKind = r.Error.Kind
		}
		if !sendEvent(ctx, ch, ev) {
			return nil, ReasonAborted, ctx.Err()
		}
	}

	// 4. 串行组（每个工具独立判断）
	for _, tc := range serial {
		if !sendEvent(ctx, ch, ToolCallStart{
			Turn:         state.TurnCount,
			ToolCallID:   tc.ID,
			ToolCallName: tc.Name,
			Arguments:    tc.Arguments,
		}) {
			return nil, ReasonAborted, ctx.Err()
		}

		if l.checkPermission(ctx, tc, results, skip) {
			r := results[tc.ID]
			if !sendEvent(ctx, ch, ToolCallResult{
				Turn:         state.TurnCount,
				ToolCallID:   tc.ID,
				ToolCallName: tc.Name,
				Result:       r.Content,
				Error:        r.Error.Message,
				ErrorKind:    r.Error.Kind,
				Denied:       true,
				DiffHunks:    r.Meta.DiffHunks,
			}) {
				return nil, ReasonAborted, ctx.Err()
			}
			continue
		}

		if err := ctx.Err(); err != nil {
			return nil, ReasonAborted, err
		}

		start := time.Now()
		result, execErr := l.toolRegistry.Execute(ctx, tc.Name, json.RawMessage(tc.Arguments))
		if execErr != nil {
			return nil, ReasonToolFatal, fmt.Errorf("serial tool execution: %w", execErr)
		}
		if result == nil {
			return nil, ReasonToolFatal, fmt.Errorf("tool %q returned nil result", tc.Name)
		}
		results[tc.ID] = result
		dur := time.Since(start)
		durations[tc.ID] = dur
		l.verbose("    ✔ %s  (%s | %d bytes | %s)\n",
			tc.Name, dur.Round(time.Millisecond),
			len(result.Content), truncateText(result.Content, 60),
		)

		ev := ToolCallResult{
			Turn:         state.TurnCount,
			ToolCallID:   tc.ID,
			ToolCallName: tc.Name,
			DurationMs:   durations[tc.ID].Milliseconds(),
			Result:       result.Content,
			DiffHunks:    result.Meta.DiffHunks,
		}
		if result.IsError() {
			ev.Error = result.Error.Message
			ev.ErrorKind = result.Error.Kind
		}
		if !sendEvent(ctx, ch, ev) {
			return nil, ReasonAborted, ctx.Err()
		}
	}

	// 5. 构造 tool 消息 + 错误分类检查
	//
	// 即使检测到 Fatal 错误，仍为所有 tool call 生成对应的 tool 消息。
	// 这保证 assistant(tool_calls) 和 tool 消息的配对完整性，避免后续 LLM 调用时
	// 因孤儿 tool_calls 导致 API 400。
	messages, reason, execErr := l.buildToolMessages(calls, results, skip, state)
	return messages, reason, execErr
}

// buildToolMessages 基于执行结果构造 tool 消息，并进行错误分类检查。
//
// 所有 tool call 都会生成对应的 tool 消息，即使：
//   - 结果不存在（执行被中断）→ 生成占位错误消息
//   - 存在 Fatal 错误 → 仍为后续 call 生成消息，保证配对完整
//
// Recoverable 错误始终包装为 "Error [kind]: message" 返回给 LLM，
// LLM 可以根据错误反馈自行修正，无需 loop 层面限制重试次数。
//
// 返回: tool 消息切片, 终止原因（如有 Fatal 错误）, error（如有 Fatal 错误）
func (l *Loop) buildToolMessages(
	calls []llm.ToolCall,
	results map[string]*tool.ToolResult,
	skip map[string]bool,
	state *LoopState,
) ([]llm.Message, TerminalReason, error) {
	messages := make([]llm.Message, 0, len(calls))
	var fatalReason TerminalReason
	var fatalErr error

	// 退避追踪：本轮首个 Recoverable 错误的 Kind 和是否全部同 Kind。
	var firstRecoverableKind string
	allRecoverableSameKind := true
	anySuccess := false

	for _, tc := range calls {
		result, exists := results[tc.ID]
		if !exists {
			// 工具未执行（如执行阶段出错中断）
			result = &tool.ToolResult{
				Content: fmt.Sprintf("Error: tool %q was not executed (loop terminated)", tc.Name),
				Error: &tool.ToolError{
					Class:   tool.ErrorClassFatal,
					Kind:    tool.ErrKindUnknownTool,
					Message: "execution interrupted",
				},
			}
		}

		content := result.Content
		if result.IsError() && !skip[tc.ID] {
			toolErr := result.Error

			if toolErr.Class == tool.ErrorClassFatal {
				// 记录第一个 Fatal 错误，但继续构造后续消息
				if fatalErr == nil {
					fatalReason = ReasonToolFatal
					fatalErr = fmt.Errorf("fatal tool error (%s): %s", toolErr.Kind, toolErr.Message)
				}
			} else {
				// Recoverable 错误 → 退避追踪
				if firstRecoverableKind == "" {
					firstRecoverableKind = toolErr.Kind
				} else if toolErr.Kind != firstRecoverableKind {
					allRecoverableSameKind = false
				}
			}

			content = fmt.Sprintf("Error [%s]: %s", toolErr.Kind, toolErr.Message)
		} else if !result.IsError() {
			anySuccess = true
		}

		messages = append(messages, llm.Message{
			Role:       llm.RoleTool,
			Content:    content,
			ToolCallID: tc.ID,
			Name:       tc.Name,
		})
	}

	// 退避检查：同类 Recoverable 错误连续出现达到上限 → 升级为 Fatal。
	// 若本轮有任意工具成功，重置计数（LLM 在推进任务，不是死循环）。
	if anySuccess {
		state.LastErrorKind = ""
		state.ConsecutiveSameError = 0
	} else if firstRecoverableKind != "" && allRecoverableSameKind {
		if state.LastErrorKind == firstRecoverableKind {
			state.ConsecutiveSameError++
		} else {
			state.LastErrorKind = firstRecoverableKind
			state.ConsecutiveSameError = 1
		}

		if state.ConsecutiveSameError >= maxConsecutiveSameError {
			if fatalErr == nil {
				fatalReason = ReasonToolFatal
				fatalErr = fmt.Errorf(
					"tool error %q repeated %d times consecutively — stopping to avoid infinite retry loop",
					firstRecoverableKind, state.ConsecutiveSameError,
				)
			}
		}
	} else {
		// 混合错误（不同 Kind）或本轮无 Recoverable 错误 → 不触发退避
		state.LastErrorKind = ""
		state.ConsecutiveSameError = 0
	}

	return messages, fatalReason, fatalErr
}

// permissionDeniedResult 构造权限拒绝的工具结果。
func permissionDeniedResult(result permission.DecisionResult) *tool.ToolResult {
	return &tool.ToolResult{
		Content: fmt.Sprintf("Permission denied: %s", result.Message),
		Error: &tool.ToolError{
			Class:   tool.ErrorClassRecoverable,
			Kind:    tool.ErrKindSecurityViolation,
			Message: result.Message,
		},
	}
}

// skippedDueToDenyResult 构造因其他工具被拒而跳过的工具结果。
func skippedDueToDenyResult(deniedName string) *tool.ToolResult {
	return &tool.ToolResult{
		Content: fmt.Sprintf("Skipped: '%s' was denied permission", deniedName),
	}
}

// checkPermission 对单个 tool call 进行权限检查。
// 返回 true 表示该工具被拒绝，结果已写入 results 和 skip。
func (l *Loop) checkPermission(ctx context.Context, tc llm.ToolCall, results map[string]*tool.ToolResult, skip map[string]bool) bool {
	if l.config.Guard == nil {
		return false
	}
	rawArgs := json.RawMessage(tc.Arguments)
	guardResult := l.config.Guard.Check(ctx, tc.Name, rawArgs)

	switch guardResult.Decision {
	case permission.DecisionDeny:
		results[tc.ID] = permissionDeniedResult(guardResult)
		skip[tc.ID] = true
		return true

	case permission.DecisionAsk:
		if l.config.UserResponder == nil {
			results[tc.ID] = permissionDeniedResult(guardResult)
			skip[tc.ID] = true
			return true
		}
		choice := l.config.UserResponder.AskUser(ctx, tc.Name, rawArgs, guardResult)
		if choice.Decision != permission.DecisionAllow {
			results[tc.ID] = permissionDeniedResult(guardResult)
			skip[tc.ID] = true
		}
		if choice.RememberScope != "" {
			// 提取 pattern：优先 Guard 建议，否则自行提取
			pattern := guardResult.SuggestedPattern
			if pattern == "" {
				pattern = permission.ExtractPattern(tc.Name, rawArgs)
			}
			behavior := permission.RuleAllow
			if choice.Decision != permission.DecisionAllow {
				behavior = permission.RuleDeny
			}
			rule := permission.Rule{
				Behavior: behavior,
				ToolName: tc.Name,
				Pattern:  pattern,
			}

			switch choice.RememberScope {
			case permission.ScopeConfig:
				// 三写：RuleEngine（当前 session）+ SessionMemory（当前 session）+ 落盘 settings.json（跨 session）
				l.config.Guard.AddRule(rule, permission.ScopeConfig)
				if choice.Decision == permission.DecisionAllow {
					l.config.Guard.SessionAllow(tc.Name, rawArgs)
				} else {
					l.config.Guard.SessionDeny(tc.Name, rawArgs)
				}
				l.config.Guard.PersistRule(rule)
			case permission.ScopeSession:
				// 仅 SessionMemory（当前 session），不落盘
				if choice.Decision == permission.DecisionAllow {
					l.config.Guard.SessionAllow(tc.Name, rawArgs)
				} else {
					l.config.Guard.SessionDeny(tc.Name, rawArgs)
				}
			}
		}
		return skip[tc.ID]
	}
	return false
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
	fmt.Fprintf(l.config.VerboseWriter, format, args...)
}

// truncateText 截断字符串到 maxLen，追加 "…"。
func truncateText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "…"
}

// truncateArgs 截断工具调用参数字符串（JSON），保持紧凑。
func truncateArgs(args string, maxLen int) string {
	if args == "" || args == "{}" {
		return ""
	}
	// 尝试压缩 JSON 为 key=value 摘要
	var raw map[string]any
	if err := json.Unmarshal([]byte(args), &raw); err == nil {
		parts := make([]string, 0, len(raw))
		for k, v := range raw {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
		short := joinStrings(parts, ", ")
		if len([]rune(short)) <= maxLen {
			return short
		}
	}
	return truncateText(args, maxLen)
}

// joinStrings 用 sep 连接字符串切片。
func joinStrings(elems []string, sep string) string {
	if len(elems) == 0 {
		return ""
	}
	n := len(sep) * (len(elems) - 1)
	for _, e := range elems {
		n += len(e)
	}
	b := make([]byte, 0, n)
	for i, e := range elems {
		if i > 0 {
			b = append(b, sep...)
		}
		b = append(b, e...)
	}
	return string(b)
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
