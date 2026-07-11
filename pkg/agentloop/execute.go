package agentloop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/todo"
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
	// Inject EventCallback + ParentMessages into tool execution context.
	// Sub-tools like AgentTool read these to create nested loops and forward events.
	if l.config.EventCallback != nil {
		ctx = WithEventCallback(ctx, l.config.EventCallback)
	}
	ctx = WithParentMessages(ctx, state.Messages)
	// Inject system prompt for subagents. Prefer Config; fall back to extracting
	// from messages[0] (ContextManager-managed sessions use empty Config.SystemPrompt).
	systemPrompt := l.config.SystemPrompt
	if systemPrompt == "" && len(state.Messages) > 0 && state.Messages[0].Role == llm.RoleSystem {
		systemPrompt = state.Messages[0].Content
	}
	ctx = WithParentSystemPrompt(ctx, systemPrompt)
	// Inject AGENTS.md for cold subagents.
	if l.config.AgentsMD != "" {
		ctx = WithAgentsMD(ctx, l.config.AgentsMD)
	}

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
			msgs, _, _ = l.buildToolMessages(calls, results, skip)
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
				Fatal:        true,
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
					safeSend(resultsCh, execResult{
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
					})
				}
			}()
				start := time.Now()
				execCtx := ctx
				if l.config.ToolTimeout > 0 {
					var cancel context.CancelFunc
					execCtx, cancel = context.WithTimeout(ctx, l.config.ToolTimeout)
					defer cancel()
				}
				execCtx = WithToolCallID(execCtx, tc.ID)
				var result *tool.ToolResult
				var execErr error
				if l.toolRegistry.IsStreamable(tc.Name) {
					result, execErr = l.toolRegistry.ExecuteStreaming(execCtx, tc.Name, json.RawMessage(tc.Arguments), func(chunk string) {
						sendEvent(ctx, ch, ToolCallStream{
							Turn: state.TurnCount, ToolCallID: tc.ID,
							ToolCallName: tc.Name, Chunk: chunk,
						})
					})
				} else {
					result, execErr = l.toolRegistry.Execute(execCtx, tc.Name, json.RawMessage(tc.Arguments))
				}
				safeSend(resultsCh, execResult{tc: tc, result: result, err: execErr, start: start})
			}(tc)
		}
		// REGRESSION: wg.Wait() 无超时保护。每个 goroutine 有 ToolTimeout，
		// 但若工具忽略 context，TUI 会被阻塞长达 10 分钟。加 ctx.Done() +
		// 5s 宽限期作为双重保险，确保 TUI 永不卡死。
		wgDone := make(chan struct{})
		go func() {
			wg.Wait()
			close(wgDone)
		}()
		select {
		case <-wgDone:
			// 全部工具正常完成
		case <-ctx.Done():
			// 用户取消（Esc）或超时。等待 5s 宽限期让工具退出。
			select {
			case <-wgDone:
			case <-time.After(5 * time.Second):
				// 工具未在宽限期内退出。异步等待避免 goroutine 泄漏，
				// 继续处理已收集的结果。
				go func() { <-wgDone }()
			}
		}
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
			ev.Fatal = r.Error.Class == tool.ErrorClassFatal
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

		// 工具需要用户交互时（如 ask_user_question）：跳过权限检查 + 普通执行，
		// 改为通过 UserResponder 进行阻塞式交互
		if t, ok := l.toolRegistry.Get(tc.Name); ok {
			if uit, ok := t.(tool.UserInteractionTool); ok && uit.RequiresUserInteraction() {
				// plan 模式工具由 Loop 层面的特殊处理完成
				if tc.Name == "enter_plan_mode" {
					result, skipErr := l.executeEnterPlanMode(ctx, tc, state, ch)
					results[tc.ID] = result
					durations[tc.ID] = 0
					ev := ToolCallResult{
						Turn:         state.TurnCount,
						ToolCallID:   tc.ID,
						ToolCallName: tc.Name,
						DurationMs:   0,
					}
					if result.IsError() {
						ev.Error = result.Error.Message
						ev.ErrorKind = result.Error.Kind
						ev.Fatal = result.Error.Class == tool.ErrorClassFatal
					} else {
						ev.Result = result.Content
					}
					if !sendEvent(ctx, ch, ev) {
						return nil, ReasonAborted, ctx.Err()
					}
					if skipErr != nil {
						return nil, ReasonAborted, skipErr
					}
					continue
				}
				if tc.Name == "exit_plan_mode" {
					result, skipErr := l.executeExitPlanMode(ctx, tc, state, ch)
					results[tc.ID] = result
					durations[tc.ID] = 0
					ev := ToolCallResult{
						Turn:         state.TurnCount,
						ToolCallID:   tc.ID,
						ToolCallName: tc.Name,
						DurationMs:   0,
					}
					if result.IsError() {
						ev.Error = result.Error.Message
						ev.ErrorKind = result.Error.Kind
						ev.Fatal = result.Error.Class == tool.ErrorClassFatal
					} else {
						ev.Result = result.Content
					}
					if !sendEvent(ctx, ch, ev) {
						return nil, ReasonAborted, ctx.Err()
					}
					if skipErr != nil {
						return nil, ReasonAborted, skipErr
					}
					continue
				}
				result, skipErr := l.executeAskUserQuestion(ctx, tc, state, ch)
				results[tc.ID] = result
				durations[tc.ID] = 0

				ev := ToolCallResult{
					Turn:         state.TurnCount,
					ToolCallID:   tc.ID,
					ToolCallName: tc.Name,
					DurationMs:   0,
				}
				if result.IsError() {
					ev.Error = result.Error.Message
					ev.ErrorKind = result.Error.Kind
					ev.Fatal = result.Error.Class == tool.ErrorClassFatal
				} else {
					ev.Result = result.Content
				}
				if !sendEvent(ctx, ch, ev) {
					return nil, ReasonAborted, ctx.Err()
				}

				if skipErr != nil {
					return nil, ReasonAborted, skipErr
				}
				continue
			}
		}

		// todo_write 由 Loop 拦截处理（更新 TodoState + 推送事件）
		if tc.Name == "todo_write" {
			result := l.executeTodoWrite(ctx, tc, state, ch)
			results[tc.ID] = result
			durations[tc.ID] = 0

			ev := ToolCallResult{
				Turn:         state.TurnCount,
				ToolCallID:   tc.ID,
				ToolCallName: tc.Name,
				DurationMs:   0,
			}
			if result.IsError() {
				ev.Error = result.Error.Message
				ev.ErrorKind = result.Error.Kind
				ev.Fatal = result.Error.Class == tool.ErrorClassFatal
			} else {
				ev.Result = result.Content
			}
			if !sendEvent(ctx, ch, ev) {
				return nil, ReasonAborted, ctx.Err()
			}
			continue
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
				Fatal:        true,
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
		execCtx = WithToolCallID(execCtx, tc.ID)
		var result *tool.ToolResult
		var execErr error
		if l.toolRegistry.IsStreamable(tc.Name) {
			result, execErr = l.toolRegistry.ExecuteStreaming(execCtx, tc.Name, json.RawMessage(tc.Arguments), func(chunk string) {
				sendEvent(ctx, ch, ToolCallStream{
					Turn: state.TurnCount, ToolCallID: tc.ID,
					ToolCallName: tc.Name, Chunk: chunk,
				})
			})
		} else {
			result, execErr = l.toolRegistry.Execute(execCtx, tc.Name, json.RawMessage(tc.Arguments))
		}
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
			ev.Fatal = result.Error.Class == tool.ErrorClassFatal
		}
		if !sendEvent(ctx, ch, ev) {
			return nil, ReasonAborted, ctx.Err()
		}
	}

	// 5. 构造 tool 消息 + 错误分类检查 + [plan:start] 注入
	//
	// 即使检测到 Fatal 错误，仍为所有 tool call 生成对应的 tool 消息。
	// 这保证 assistant(tool_calls) 和 tool 消息的配对完整性，避免后续 LLM 调用时
	// 因孤儿 tool_calls 导致 API 400。
	//
	// [plan:start] 必须在 tool 消息之后注入，确保消息序列符合 API 要求：
	// assistant(tool_calls) → tool(result) → user([plan:start])
	messages, reason, execErr := l.buildToolMessages(calls, results, skip)

	// 若本轮执行了 enter_plan_mode 且成功，在 tool 消息之后注入 [plan:start]
	// 若本轮执行了 exit_plan_mode 且审批通过，在 tool 消息之后注入 [plan:end]
	for _, tc := range calls {
		switch tc.Name {
		case "enter_plan_mode":
			if r, ok := results[tc.ID]; ok && !r.IsError() && !skip[tc.ID] {
				messages = append(messages, llm.Message{
					Role:    llm.RoleUser,
					Content: l.planModeStartMessage(),
				})
			}
		case "exit_plan_mode":
			if r, ok := results[tc.ID]; ok && !r.IsError() && !skip[tc.ID] && l.approvedPlan != "" {
				messages = append(messages, llm.Message{
					Role:    llm.RoleUser,
					Content: l.planModeEndMessage(l.approvedPlan),
				})
				l.approvedPlan = "" // 消费后清除
			}
		}
	}

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
) ([]llm.Message, TerminalReason, error) {
	messages := make([]llm.Message, 0, len(calls))
	var fatalReason TerminalReason
	var fatalErr error

	// 退避追踪：本轮首个 Recoverable 错误的 Kind / Tool 和是否全部同 (Kind, Tool)。
	var firstRecoverableKind string
	var firstRecoverableTool string
	allRecoverableSameKind := true
	allRecoverableSameTool := true
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
				// Recoverable 错误 → 退避追踪（同时追踪 Kind 和 Tool）
				if firstRecoverableKind == "" {
					firstRecoverableKind = toolErr.Kind
					firstRecoverableTool = tc.Name
				} else {
					if toolErr.Kind != firstRecoverableKind {
						allRecoverableSameKind = false
					}
					if tc.Name != firstRecoverableTool {
						allRecoverableSameTool = false
					}
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

	// 退避检查：同类 (Kind, Tool) Recoverable 错误连续出现达到上限 → 升级为 Fatal。
	// 若本轮有任意工具成功，重置计数（LLM 在推进任务，不是死循环）。
	// Kind 或 Tool 任一变化即视为 LLM 改变了策略，重新计数。
	if anySuccess {
		l.lastErrorKind = ""
		l.lastErrorTool = ""
		l.consecutiveSameError = 0
	} else if firstRecoverableKind != "" && allRecoverableSameKind && allRecoverableSameTool {
		samePair := l.lastErrorKind == firstRecoverableKind && l.lastErrorTool == firstRecoverableTool
		if samePair {
			l.consecutiveSameError++
		} else {
			l.lastErrorKind = firstRecoverableKind
			l.lastErrorTool = firstRecoverableTool
			l.consecutiveSameError = 1
		}

		// 警告注入：连续失败达到阈值时，向 messages 末尾注入 system 提示，
		// 引导 LLM 意识到重复错误并改变策略。
		if warnThresholds[l.consecutiveSameError] {
			warnMsg := llm.Message{
				Role: llm.RoleUser,
				Content: fmt.Sprintf(
					"[system] 你已经连续 %d 轮对 %q 工具收到 %q 错误。请反思当前策略——尝试用不同的工具、不同的参数，或重新理解任务目标。不要重复相同的调用模式。",
					l.consecutiveSameError, firstRecoverableTool, firstRecoverableKind,
				),
			}
			messages = append(messages, warnMsg)
		}

		if l.consecutiveSameError >= maxConsecutiveSameError {
			if fatalErr == nil {
				fatalReason = ReasonToolFatal
				fatalErr = fmt.Errorf(
					"tool %q error %q repeated %d times consecutively — stopping to avoid infinite retry loop",
					firstRecoverableTool, firstRecoverableKind, l.consecutiveSameError,
				)
			}
		}
	} else {
		// 混合错误（不同 Kind 或不同 Tool）或本轮无 Recoverable 错误 → 不触发退避
		l.lastErrorKind = ""
		l.lastErrorTool = ""
		l.consecutiveSameError = 0
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
				_ = l.config.Guard.AddRule(rule, permission.ScopeConfig)
				if choice.Decision == permission.DecisionAllow {
					l.config.Guard.SessionAllow(tc.Name, rawArgs)
				} else {
					l.config.Guard.SessionDeny(tc.Name, rawArgs)
				}
				_ = l.config.Guard.PersistRule(rule)
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

// executeTodoWrite 处理 todo_write 工具调用。
// 解析参数 → 更新 TodoState → 推送 TodoUpdateEvent → 返回格式化结果给 LLM。
func (l *Loop) executeTodoWrite(ctx context.Context, tc llm.ToolCall, state *LoopState, ch chan<- TurnEvent) *tool.ToolResult {
	if l.config.TodoState == nil {
		return &tool.ToolResult{
			Content: "todo_write is not available (TodoState not configured).",
		}
	}

	var params todo.TodoWriteParams
	if err := json.Unmarshal([]byte(tc.Arguments), &params); err != nil {
		return &tool.ToolResult{
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    tool.ErrKindInvalidArgs,
				Message: "todo_write: failed to parse arguments: " + err.Error(),
			},
		}
	}

	oldItems, newItems := l.config.TodoState.Apply(params)

	// 成功更新后重置周期性提醒计数器
	l.turnsSinceLastTodoWrite = 0
	l.turnsSinceLastTodoReminder = 0

	// 推送更新事件给 TUI
	if !sendEvent(ctx, ch, TodoUpdateEvent{Items: newItems}) {
		return &tool.ToolResult{
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    "context_cancelled",
				Message: "todo_write: context cancelled",
			},
		}
	}

	result := todo.FormatResult(newItems)

	// 检测无状态变更的 no-op 调用：若新旧列表状态完全一致，追加提示
	if todoItemsEqual(oldItems, newItems) && len(newItems) > 0 {
		result += "\n⚠️ No status changes detected. Did you forget to update task statuses? " +
			"Mark completed tasks as 'completed' and start the next task by setting it to 'in_progress'."
	}

	return &tool.ToolResult{
		Content: result,
	}
}

// executeAskUserQuestion 处理 ask_user_question 工具调用。
// 发送通知事件后通过 UserResponder.AnswerQuestion() 阻塞等待用户回答。
func (l *Loop) executeAskUserQuestion(ctx context.Context, tc llm.ToolCall, state *LoopState, ch chan<- TurnEvent) (*tool.ToolResult, error) {
	// 解析问题参数
	var params struct {
		Questions []struct {
			Question    string `json:"question"`
			Header      string `json:"header"`
			MultiSelect bool   `json:"multiSelect"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(tc.Arguments), &params); err != nil {
		return &tool.ToolResult{
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    tool.ErrKindInvalidArgs,
				Message: fmt.Sprintf("failed to parse questions: %v", err),
			},
		}, nil
	}

	// 转换为 QuestionPrompt
	prompts := make([]QuestionPrompt, len(params.Questions))
	for i, q := range params.Questions {
		opts := make([]QuestionOptionPrompt, len(q.Options))
		for j, o := range q.Options {
			opts[j] = QuestionOptionPrompt{
				Label:       o.Label,
				Description: o.Description,
			}
		}
		prompts[i] = QuestionPrompt{
			Question:    q.Question,
			Header:      q.Header,
			Options:     opts,
			MultiSelect: q.MultiSelect,
		}
	}

	// 发送通知事件（非阻塞，TUI 可据此做渲染准备）
	if !sendEvent(ctx, ch, AskUserQuestionEvent{
		Turn:       state.TurnCount,
		ToolCallID: tc.ID,
		Questions:  prompts,
	}) {
		return nil, ctx.Err()
	}

	// 通过 UserResponder 阻塞等待用户回答
	if l.config.UserResponder == nil {
		return &tool.ToolResult{
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    "user_declined",
				Message: "no user responder available",
			},
		}, nil
	}

	responses, err := l.config.UserResponder.AnswerQuestion(ctx, prompts)
	if err != nil || responses == nil {
		msg := "user declined to answer"
		if err != nil {
			msg = err.Error()
		}
		return &tool.ToolResult{
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    "user_declined",
				Message: msg,
			},
		}, nil
	}

	// 格式化答案为 JSON（对齐 spec: {questions, answers}）
	answerMap := make(map[string]string, len(responses))
	for _, r := range responses {
		answerMap[r.Question] = strings.Join(r.Answers, ", ")
	}
	resultJSON, err := json.Marshal(map[string]interface{}{
		"questions": prompts,
		"answers":   answerMap,
	})
	if err != nil {
		return &tool.ToolResult{
			Content: formatAnswersAsText(prompts, responses),
		}, nil
	}
	return &tool.ToolResult{
		Content: string(resultJSON),
	}, nil
}

// formatAnswersAsText 将答案格式化为纯文本（JSON 序列化失败的兜底）。
func formatAnswersAsText(questions []QuestionPrompt, responses []QuestionResponse) string {
	var b strings.Builder
	for _, r := range responses {
		b.WriteString(r.Question)
		b.WriteString(": ")
		b.WriteString(strings.Join(r.Answers, ", "))
		b.WriteString("\n")
	}
	return b.String()
}

// executeEnterPlanMode 处理 enter_plan_mode 工具调用。
// 通过 UserResponder.EnterPlan() 阻塞等待用户确认，然后设置 plan 模式状态。
func (l *Loop) executeEnterPlanMode(ctx context.Context, tc llm.ToolCall, state *LoopState, ch chan<- TurnEvent) (*tool.ToolResult, error) {
	// 已在 plan 模式中（如用户通过 Shift+Tab 提前进入）→ 直接返回成功
	if l.plan {
		return &tool.ToolResult{
			Content: fmt.Sprintf(`Already in plan mode. Continue exploring and designing. Write your plan to %s.

Remember: DO NOT write or edit any source files — these operations will be blocked by the permission system.`, l.config.PlanFile),
		}, nil
	}

	if l.config.UserResponder == nil {
		return &tool.ToolResult{
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    "user_declined",
				Message: "no user responder available for plan mode",
			},
		}, nil
	}

	confirmed, err := l.config.UserResponder.EnterPlan(ctx)
	if err != nil || !confirmed {
		return &tool.ToolResult{
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    "user_declined",
				Message: "user declined to enter plan mode",
			},
		}, nil
	}

	// 保存进入 plan 前的状态
	l.prePlanMode = l.config.Guard != nil // 简化：记录是否有 Guard
	_ = l.prePlanMode

	// 生成 plan 文件路径（如未设置）
	if l.config.PlanFile == "" {
		l.config.PlanFile = l.generatePlanFilePath()
	}

	// 启用 plan 模式
	l.plan = true
	l.planPairID = generatePairID()

	// Advisor mode：进入 plan 时切到主模型（清空 Model override，回退到 Client 默认）
	if l.config.AdvisorMode {
		l.prePlanModel = l.config.Model
		l.config.Model = ""
	}

	if l.config.Guard != nil {
		l.config.Guard.EnterPlanMode(l.config.PlanFile)
	}

	emitPlanEnter(ctx, ch, state.TurnCount, l.config.PlanFile, l.planPairID)

	return &tool.ToolResult{
		Content: fmt.Sprintf(`Entered plan mode. You should now focus on exploring the codebase and designing an implementation approach.

In plan mode, you should:
1. Thoroughly explore the codebase to understand existing patterns
2. Identify similar features and architectural approaches
3. Consider multiple approaches and their trade-offs
4. Use ask_user_question if you need to clarify the approach
5. Design a concrete implementation strategy
6. Write your plan to %s
7. When ready, use exit_plan_mode to present your plan for approval

Remember: DO NOT write or edit any source files — these operations will be blocked by the permission system. Use write_file only for the plan file. Use shell for analysis commands (lint, test, version checks, git log/diff) — destructive commands will be blocked.`, l.config.PlanFile),
	}, nil
}

// executeExitPlanMode 处理 exit_plan_mode 工具调用。
// 读取 plan 文件内容，通过 UserResponder.ApprovePlan() 提交审批。
func (l *Loop) executeExitPlanMode(ctx context.Context, tc llm.ToolCall, state *LoopState, ch chan<- TurnEvent) (*tool.ToolResult, error) {
	// 校验：仅 plan 模式下有效
	if !l.plan {
		return &tool.ToolResult{
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    tool.ErrKindInvalidArgs,
				Message: "You are not in plan mode. This tool is only for exiting plan mode after writing a plan.",
			},
		}, nil
	}

	// 读取 plan 文件
	planContent, err := os.ReadFile(l.config.PlanFile)
	if err != nil {
		return &tool.ToolResult{
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    tool.ErrKindFileNotFound,
				Message: fmt.Sprintf("Plan file not found at %s. Write your plan to this file first using write_file.", l.config.PlanFile),
			},
		}, nil
	}

	if l.config.UserResponder == nil {
		return &tool.ToolResult{
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    "user_declined",
				Message: "no user responder available for plan approval",
			},
		}, nil
	}

	planStr := string(planContent)

	approval, err := l.config.UserResponder.ApprovePlan(ctx, planStr)
	if err != nil {
		return &tool.ToolResult{
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    "user_declined",
				Message: err.Error(),
			},
		}, nil
	}

	if !approval.Approved {
		// 拒绝：留在 plan 模式
		msg := "User rejected the plan"
		if approval.Feedback != "" {
			msg += ": " + approval.Feedback
		}
		return &tool.ToolResult{
			Error: &tool.ToolError{
				Class:   tool.ErrorClassRecoverable,
				Kind:    "user_declined",
				Message: msg,
			},
		}, nil
	}

	// 审批通过：退出 plan 模式
	l.plan = false
	l.approvedPlan = planStr // 暂存，由 executeToolCalls 在 tool 消息后注入 [plan:end]
	l.config.PlanFile = ""   // 清除，确保下次进入生成新文件

	// Advisor mode：退出 plan 时恢复次模型
	if l.config.AdvisorMode && l.prePlanModel != "" {
		l.config.Model = l.prePlanModel
		l.prePlanModel = ""
	}

	if l.config.Guard != nil {
		l.config.Guard.ExitPlanMode()
	}

	emitPlanExit(ctx, ch, state.TurnCount, planStr, l.config.PlanFile)

	return &tool.ToolResult{
		Content: "Plan approved. The approved plan is in the next user message ([plan:end #xxxx]).",
	}, nil
}

// planModeStartMessage 返回 [plan:start #xxxx] user 消息。
func (l *Loop) planModeStartMessage() string {
	return fmt.Sprintf(`[plan:start #%s] Plan file: %s

You are now in plan mode. Explore the codebase, analyze architectures,
use shell for analysis (lint, test, git log/diff), ask questions via
ask_user_question, and write your plan to the file above.

DO NOT write or edit any source files — those will be blocked.
Call exit_plan_mode when ready.

This message is paired with [plan:end #%s]. You remain in plan mode
as long as [plan:end #%s] has NOT appeared. Check the message history:
if you see [plan:end #%s], plan mode has ended. If you only see this
[plan:start #%s] without its matching end, you are still in plan mode.`, l.planPairID, l.config.PlanFile, l.planPairID, l.planPairID, l.planPairID, l.planPairID)
}

// planModeEndMessage 返回 [plan:end #xxxx] user 消息（含已审批 plan）。
func (l *Loop) planModeEndMessage(planContent string) string {
	return fmt.Sprintf(`[plan:end #%s] Plan approved. You are now in normal mode.
Start implementing according to the approved plan below.

This message is paired with [plan:start #%s]. Plan mode has ended.
Ignore any earlier [plan:start #%s] — it is now overridden by this
[plan:end #%s] marker.

Plan saved to: %s

## Approved Plan:
%s`, l.planPairID, l.planPairID, l.planPairID, l.planPairID, l.config.PlanFile, planContent)
}

// mustReadRandom 包装 crypto/rand.Read，失败时 panic。
// crypto/rand.Read 仅当系统熵池枯竭时才会失败，此时进程已处于退化状态，
// 继续使用全零 ID 会导致数据损坏，因此 fail-fast 是更安全的选择。
func mustReadRandom(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
	}
}

// generatePairID 生成 4 位 hex 随机配对 ID（如 "a3f7"）。
func generatePairID() string {
	b := make([]byte, 2)
	mustReadRandom(b)
	return hex.EncodeToString(b)
}

// plansDirectory 返回 plan 文件存储目录。
func (l *Loop) plansDirectory() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".waveloom", "plans")
}

// generatePlanFilePath 在 plans 目录下生成随机 word slug 文件路径。
func (l *Loop) generatePlanFilePath() string {
	plansDir := l.plansDirectory()
	_ = os.MkdirAll(plansDir, 0o755)

	for i := 0; i < 10; i++ {
		slug := generateWordSlug()
		path := filepath.Join(plansDir, slug+".md")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path
		}
	}
	// 兜底：用随机 hex
	b := make([]byte, 4)
	mustReadRandom(b)
	return filepath.Join(plansDir, hex.EncodeToString(b)+".md")
}

// generateWordSlug 生成 "adjective-noun" 格式的随机 slug。
func generateWordSlug() string {
	adj := adjectives[randInt(len(adjectives))]
	noun := nouns[randInt(len(nouns))]
	return adj + "-" + noun
}

// randInt 使用 crypto/rand 生成 [0, max) 范围内的随机整数。
func randInt(max int) int {
	b := make([]byte, 4)
	mustReadRandom(b)
	return int(uint32(b[0])|uint32(b[1])<<8|uint32(b[2])<<16|uint32(b[3])<<24) % max
}

// emitPlanEnter 发送 PlanModeEnter 事件到 channel。
func emitPlanEnter(ctx context.Context, ch chan<- TurnEvent, turn int, planFile, pairID string) {
	sendEvent(ctx, ch, PlanModeEnter{Turn: turn, PlanFile: planFile, PairID: pairID})
}

// emitPlanExit 发送 PlanModeExit 事件到 channel。
func emitPlanExit(ctx context.Context, ch chan<- TurnEvent, turn int, plan, filePath string) {
	sendEvent(ctx, ch, PlanModeExit{
		Turn:     turn,
		Plan:     plan,
		FilePath: filePath,
		Approved: true, // 仅在审批通过后调用
	})
}

// 词库：用于生成 plan 文件 slug
var adjectives = []string{
	"happy", "clever", "brave", "bright", "calm",
	"eager", "fancy", "fresh", "grand", "green",
	"jolly", "keen", "lucky", "merry", "noble",
	"proud", "quick", "sharp", "smart", "swift",
	"vivid", "warm", "wise", "bold", "cool",
}

var nouns = []string{
	"badger", "crane", "dolphin", "eagle", "falcon",
	"gecko", "heron", "ibis", "jackal", "koala",
	"lemur", "marlin", "newt", "otter", "puffin",
	"quokka", "raven", "salmon", "tapir", "urchin",
	"viper", "weasel", "xerus", "yak", "zebra",
}

// todoItemsEqual 比较两个 todo 列表的状态是否完全一致（content + status + activeForm）。
// 用于检测 no-op todo_write 调用（LLM 传入相同状态但未做任何变更）。
func todoItemsEqual(a, b []todo.TodoItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Content != b[i].Content || a[i].Status != b[i].Status || a[i].ActiveForm != b[i].ActiveForm {
			return false
		}
	}
	return true
}

// safeSend 向 channel 安全发送，channel 已关闭时静默丢弃（不 panic）。
// REGRESSION: 工具 goroutine 可能在 resultsCh 关闭后仍尝试发送（工具忽略 context 取消），
// 导致 panic-in-recover 的 double-panic 使进程崩溃。
func safeSend[T any](ch chan<- T, v T) {
	defer func() { _ = recover() }()
	ch <- v
}
