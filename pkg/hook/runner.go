package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ── HookRunner ──

// Runner 管理 hook 配置并执行 hook 脚本。
// 通过 NewRunner 创建，注入从 settings.json 解析的 hook 配置。
type Runner struct {
	hooks          map[EventType][]HookConfig
	transcriptPath string
	sessionID      string
	runtimeWarns   []string // 运行时累计的 hook 错误（每次 PreToolUse/PostToolUse 后通过 FlushWarnings 提取并清空）
	mu             sync.Mutex
}

// NewRunner 创建 HookRunner。
// configs 为从 settings.json 解析的所有 hook 配置（按事件类型 key）。
func NewRunner(configs map[EventType][]HookConfig, sessionID, transcriptPath string) *Runner {
	if configs == nil {
		configs = make(map[EventType][]HookConfig)
	}
	return &Runner{
		hooks:          configs,
		sessionID:      sessionID,
		transcriptPath: transcriptPath,
	}
}

// SetSessionInfo 设置 session ID 和 transcript 路径，供 hook 脚本使用。
// 在 session 初始化后调用。
func (r *Runner) SetSessionInfo(sessionID, transcriptPath string) {
	r.sessionID = sessionID
	r.transcriptPath = transcriptPath
}

// FlushWarnings 返回并清空自上次调用以来积累的运行时警告。
// 调用方应将其作为系统消息展示给用户。
func (r *Runner) FlushWarnings() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	warns := r.runtimeWarns
	r.runtimeWarns = nil
	return warns
}

func (r *Runner) addRuntimeWarn(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runtimeWarns = append(r.runtimeWarns, msg)
}


// Validate 检查所有 hook 命令是否可达，返回警告信息列表。
// 启动时调用，用户可感知 hook 配置错误。
func (r *Runner) Validate() []string {
	var warnings []string
	for event, configs := range r.hooks {
		for _, cfg := range configs {
			for _, item := range cfg.Hooks {
				if item.Type != "" && item.Type != "command" {
					warnings = append(warnings, fmt.Sprintf("hook event=%s: unsupported type %q (only 'command' supported)", event, item.Type))
					continue
				}
				if item.Command == "" {
					warnings = append(warnings, fmt.Sprintf("hook event=%s: empty command", event))
					continue
				}
				// 仅对单词命令（脚本路径/可执行文件）做 PATH 可达性检查。
				// 多词命令（如 "echo '...'"）依赖 shell 解析，不检查。
				if !strings.ContainsRune(item.Command, ' ') {
					if _, err := exec.LookPath(item.Command); err != nil {
						warnings = append(warnings, fmt.Sprintf("hook event=%s command=%q: not found in PATH", event, item.Command))
					}
				}
			}
		}
	}
	return warnings
}
// ── 公开方法 ──
// RunPreToolUse 执行 PreToolUse hooks。
// toolUseID 来自 LLM 返回的 ToolCall.ID，传给 hook 脚本。
func (r *Runner) RunPreToolUse(ctx context.Context, toolName, toolUseID string, toolInput json.RawMessage) (*HookResult, error) {
	return r.runHooks(ctx, EventPreToolUse, toolName, toolUseID, toolInput, "")
}

// RunPostToolUse 执行 PostToolUse hooks。
// exitCode 会包含在 tool_response 中传给 hook 脚本。
func (r *Runner) RunPostToolUse(ctx context.Context, toolName, toolUseID string, toolInput json.RawMessage, toolResult string, exitCode int) (*HookResult, error) {
	return r.runHooks(ctx, EventPostToolUse, toolName, toolUseID, toolInput, toolResult, exitCode)
}

// RunNotification 异步发送 Notification hook。
// 不阻塞主流程，失败仅记录日志。
func (r *Runner) RunNotification(notificationType, message string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(DefaultHookTimeoutMs)*time.Millisecond)
		defer cancel()
		cwd, _ := os.Getwd()
		eventCtx := &EventContext{
			HookEventName:    string(EventNotification),
			NotificationType: notificationType,
			Message:          message,
			SessionID:        r.sessionID,
			TranscriptPath:   r.transcriptPath,
			Cwd:              cwd,
		}
		configs := r.hooks[EventNotification]
		for _, cfg := range configs {
			if !Match(cfg.Matcher, notificationType) {
				continue
			}
			for _, item := range cfg.Hooks {
				_, _ = r.executeHook(ctx, item, eventCtx)
			}
		}
	}()
}

// RunStop 执行 Stop hooks。
// 同步执行，在 loop 终止前调用。返回 true 表示被 hook 阻止终止。
func (r *Runner) RunStop(ctx context.Context, message string) (blocked bool) {
	cwd, _ := os.Getwd()
	active := false

	configs := r.hooks[EventStop]
	for _, cfg := range configs {
		if !Match(cfg.Matcher, "") {
			continue
		}
		for _, item := range cfg.Hooks {
			eventCtx := &EventContext{
				SessionID:      r.sessionID,
				TranscriptPath: r.transcriptPath,
				Cwd:            cwd,
				HookEventName:  string(EventStop),
				Message:        message,
				StopHookActive: &active,
			}

			output, err := r.executeHook(ctx, item, eventCtx)
			if err != nil || output == nil {
				continue
			}

			// 处理 Stop hook 输出：block → 阻止终止
			parsed := r.parseHookOutput(output)
			if parsed.Denied {
				blocked = true
			}

			// 第一个激活的 Stop hook → 后续 hook 的 stop_hook_active=true
			active = true
		}
	}
	return blocked
}
// ── 内部逻辑 ──
func (r *Runner) runHooks(ctx context.Context, event EventType, toolName, toolUseID string, toolInput json.RawMessage, toolResult string, exitCode ...int) (*HookResult, error) {
	configs := r.hooks[event]
	if len(configs) == 0 {
		return &HookResult{}, nil
	}

	// 获取当前工作目录
	cwd, _ := os.Getwd()

	// 构造 PostToolUse 的 tool_response 对象
	var toolResponse json.RawMessage
	if event == EventPostToolUse && toolResult != "" {
		ec := 0
		if len(exitCode) > 0 {
			ec = exitCode[0]
		}
		tr := ToolResponseContent{Content: toolResult, ExitCode: ec}
		toolResponse, _ = json.Marshal(tr)
	}

	// 构造初始事件上下文
	eventCtx := &EventContext{
		SessionID:      r.sessionID,
		TranscriptPath: r.transcriptPath,
		Cwd:            cwd,
		HookEventName:  string(event),
		ToolName:       toolName,
		ToolUseID:      toolUseID,
		ToolInput:      toolInput,
		ToolResponse:   toolResponse,
	}

	var lastResult *HookResult
	currentInput := toolInput

	for _, cfg := range configs {
		if !Match(cfg.Matcher, toolName) {
			continue
		}

		for _, item := range cfg.Hooks {
			hookCtx := *eventCtx
			if currentInput != nil {
				hookCtx.ToolInput = currentInput
			}

			output, err := r.executeHook(ctx, item, &hookCtx)
			if err != nil {
				slog.Warn("hook execution failed, continuing with original parameters",
					"event", string(event),
					"command", item.Command,
					"error", err,
				)
				continue
			}

			if output == nil {
				continue
			}

			parsed := r.parseHookOutput(output)
			if parsed.Denied {
				return parsed, nil
			}

			// 应用改写
			if parsed.ModifiedInput != nil {
				currentInput = parsed.ModifiedInput
			}

			lastResult = parsed
		}
	}

	if lastResult != nil {
		return lastResult, nil
	}
	return &HookResult{}, nil
}

// executeHook 执行单个 hook 脚本。
// stdin → 脚本 → stdout JSON，带超时保护。
func (r *Runner) executeHook(ctx context.Context, item HookItem, eventCtx *EventContext) (*HookOutput, error) {
	timeout := item.Timeout
	if timeout <= 0 {
		timeout = DefaultHookTimeoutMs
	}
	hookCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()

	// 序列化 stdin JSON
	stdinData, err := json.Marshal(eventCtx)
	if err != nil {
		return nil, fmt.Errorf("marshal event context: %w", err)
	}

	// 判断是否需要 shell 解释：脚本路径或简单命令可直执行，避免嵌套 bash -c
	var cmd *exec.Cmd
	if isShellCommand(item.Command) {
		cmd = exec.CommandContext(hookCtx, "bash", "-c", item.Command)
	} else {
		parts := strings.Fields(item.Command)
		cmd = exec.CommandContext(hookCtx, parts[0], parts[1:]...)
	}
	cmd.Stdin = bytes.NewReader(stdinData)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			slog.Warn("hook stderr", "command", item.Command, "stderr", stderr.String())
		}
		// exit code 1 按 Claude Code 协议表示"无改写需求"，透传
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			slog.Debug("hook returned exit code 1 (no rewrite needed)", "command", item.Command)
			return nil, nil
		}
		// exit code 2 按 Claude Code 协议表示"阻止执行"
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
			slog.Warn("hook blocked execution (exit code 2)", "command", item.Command, "stderr", stderr.String())
			return &HookOutput{
				Decision: "block",
				Reason:   strings.TrimSpace(stderr.String()),
			}, nil
		}
		// 其他非零退出码
		slog.Warn("hook command failed",
			"command", item.Command,
			"duration", time.Since(start).Round(time.Millisecond),
			"error", err,
		)
		r.addRuntimeWarn(fmt.Sprintf("hook %q failed with %v", item.Command, err))
		return nil, nil // 失败不阻断
	}
	if stdout.Len() == 0 {
		return nil, nil // 无输出，透传
	}

	var output HookOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		slog.Warn("hook output parse failed, continuing with original parameters",
			"command", item.Command,
			"stdout", stdout.String(),
			"error", err,
		)
		return nil, nil
	}

	return &output, nil
}

// isShellCommand 判断命令是否需要 shell 解释。
// 多词命令或包含 shell 特殊字符（管道、重定向、引号、变量等）返回 true，
// 单词路径或可执行文件名返回 false 以直执行避免嵌套 bash -c。
func isShellCommand(cmd string) bool {
	// 多词命令需要 shell 解析（如 "exit 1", "echo hello"）
	if strings.ContainsRune(cmd, ' ') {
		return true
	}
	for _, r := range cmd {
		switch r {
		case '|', '>', '<', '&', ';', '$', '`', '\'', '"', '*', '?', '[', ']', '(', ')', '{', '}', '~':
			return true
		}
	}
	return false
}

// parseHookOutput 解析 hook 输出，支持 hookSpecificOutput（新格式）
// 和 legacy decision/reason（旧格式）。
func (r *Runner) parseHookOutput(output *HookOutput) *HookResult {
	result := &HookResult{}

	// 通用字段
	if output.StopReason != "" {
		result.StopReason = output.StopReason
	}

	// 优先使用 hookSpecificOutput（新格式）
	if output.HookSpecificOutput.HookEventName != "" {
		ho := output.HookSpecificOutput
		switch ho.PermissionDecision {
		case "deny":
			result.Denied = true
			result.DenyReason = ho.PermissionDecisionReason
		case "ask":
			// ask 暂不支持 TUI 交互，降级为 deny
			result.Denied = true
			result.DenyReason = ho.PermissionDecisionReason
			slog.Warn("hook requested 'ask' permission but TUI interaction not supported, treated as deny",
				"reason", ho.PermissionDecisionReason)
		}
		if ho.UpdatedInput != nil {
			result.ModifiedInput = ho.UpdatedInput
		}
		if ho.UpdatedResult != "" {
			result.ModifiedResult = ho.UpdatedResult
		}
		return result
	}

	// Legacy format: decision/reason
	if output.Decision == "block" {
		result.Denied = true
		result.DenyReason = output.Reason
		return result
	}

	return result
}
