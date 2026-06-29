package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/tool"
)

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
				defer func() {
					if r := recover(); r != nil {
						resultsCh <- execResult{
							tc: tc,
							result: &tool.ToolResult{
								Error: &tool.ToolError{
									Class:   tool.ErrorClassFatal,
									Kind:    tool.ErrKindUnknownTool,
									Message: fmt.Sprintf("panic: %v", r),
								},
							},
							err:   fmt.Errorf("panic in tool %q: %v", tc.Name, r),
							start: time.Now(),
						}
					}
				}()
				start := time.Now()
				execCtx := ctx
				if l.config.ToolTimeout > 0 {
					var cancel context.CancelFunc
					execCtx, cancel = context.WithTimeout(ctx, l.config.ToolTimeout)
					defer cancel()
				}
				result, execErr := l.toolRegistry.Execute(execCtx, tc.Name, json.RawMessage(tc.Arguments))
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
		execCtx := ctx
		if l.config.ToolTimeout > 0 {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(ctx, l.config.ToolTimeout)
			defer cancel()
		}
		result, execErr := l.toolRegistry.Execute(execCtx, tc.Name, json.RawMessage(tc.Arguments))
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
