package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// context helpers
// ---------------------------------------------------------------------------

// ParentSystemPromptFromContext 从 ctx 提取父 Loop 注入的 system prompt。
// 委托到 agentloop.ParentSystemPromptFromContext（key 定义在 agentloop/context.go）。
func ParentSystemPromptFromContext(ctx context.Context) string {
	return agentloop.ParentSystemPromptFromContext(ctx)
}

// ---------------------------------------------------------------------------
// AgentTool
// ---------------------------------------------------------------------------

// AgentParams 是 agent 工具的参数结构体。
type AgentParams struct {
	SubagentType string `json:"subagent_type,omitempty"` // 可选。省略 = fork 模式
	Description  string `json:"description"`              // 简短描述
	Prompt       string `json:"prompt"`                   // 委派任务
}

// AgentTool 实现 tool.TypedTool[AgentParams]，将任务委派给子 agent 执行。
type AgentTool struct {
	LLMClient llm.Client
}

func (a *AgentTool) Name() string              { return "agent" }
func (a *AgentTool) ConcurrentSafe() bool      { return true }

func (a *AgentTool) Description() string {
	return strings.Join([]string{
		"Launch a subagent to handle complex, multi-step tasks autonomously.",
		"",
		"Available subagent types and the tools they have access to:",
		"- general-purpose: read_file, write_file, edit_file, bash_subagent, web_fetch",
		"- Explore: read-only exploration (read_file, bash_subagent, web_fetch)",
		"",
		"Omit subagent_type to fork yourself — the fork inherits your conversation context (minus the agent call itself).",
		"Specify subagent_type for a cold agent that starts with fresh context and filtered tools.",
		"",
		"Usage: for forks, write a directive (context is inherited); for cold agents, provide a self-contained prompt with full background.",
		"You will receive the subagent's final output as the tool result.",
		"",
		"Do NOT use the agent tool for: reading a known file path (use read_file),",
		"searching within 1-3 files (use read_file), or simple file pattern matching (use shell).",
		"Explore agent should be used proactively for codebase exploration without the user having to ask.",
	}, "\n")
}

func (a *AgentTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "subagent_type": {
      "type": "string",
      "description": "Type of subagent: 'general-purpose' or 'Explore'. Omit to fork yourself."
    },
    "description": {
      "type": "string",
      "description": "A short (3-5 word) description of the task"
    },
    "prompt": {
      "type": "string",
      "description": "The task for the subagent to perform"
    }
  },
  "required": ["description", "prompt"]
}`)
}

// ---------------------------------------------------------------------------
// constants
// ---------------------------------------------------------------------------

const (
	forkMaxTurns = 200
	coldMaxTurns = 50
)

// ---------------------------------------------------------------------------
// agent system prompts
// ---------------------------------------------------------------------------

func exploreSystemPrompt() string {
	return `You are a read-only file exploration agent. Your role is to search, read, and analyze existing code.
You CAN use: read_file, bash_subagent, web_fetch.
bash_subagent is for read-only operations ONLY: ls, cat, head, tail, find, grep, file, wc, sort, uniq, diff, git log, git status, git diff, du, df, which, pwd, date, uname.
You CANNOT use: write_file, edit_file.
You MUST NOT use bash_subagent for: mkdir, touch, rm, cp, mv, chmod, chown, echo > (redirect), tee, git add, git commit, npm install, pip install, or any command that writes to the filesystem.
Complete the search request efficiently and report your findings clearly.`
}

func generalPurposeSystemPrompt() string {
	return `You are a general-purpose coding agent. Complete the task using the tools available to you.
Work efficiently — don't gold-plate, but don't leave the task half-done.
When you finish, respond with a concise report of what was done and any key findings.`
}

// ---------------------------------------------------------------------------
// Execute
// ---------------------------------------------------------------------------

// Execute
// ---------------------------------------------------------------------------

func (a *AgentTool) Execute(ctx context.Context, p AgentParams) (*tool.ToolResult, error) {
	if p.SubagentType == "" {
		return a.executeFork(ctx, p)
	}
	return a.executeCold(ctx, p)
}

// ---------------------------------------------------------------------------
// Fork execution
// ---------------------------------------------------------------------------

func (a *AgentTool) executeFork(ctx context.Context, p AgentParams) (*tool.ToolResult, error) {
	cb := agentloop.EventCallbackFromContext(ctx)

	// 从 context 获取父消息历史，剥离最后一条 assistant（含未配对 agent tool_call）
	parentRaw := agentloop.ParentMessagesFromContext(ctx)
	messages := buildForkMessages(parentRaw, p.Description, p.Prompt)

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	subLoop := agentloop.New(a.LLMClient, a.buildForkRegistry(), agentloop.Config{
		MaxTurns:      forkMaxTurns,
		SystemPrompt:  "", // messages already contain system prompt
		Guard:         permission.NewGuard(permission.WithBypassMode(true)),
		UserResponder: nil,
		ToolTimeout:   agentloop.DefaultToolTimeout,
	})

	startTime := time.Now()
	toolCallID := agentloop.ToolCallIDFromContext(ctx)

	if cb != nil {
		cb(SubagentStart{Prompt: p.Description, AgentType: "fork", InheritCtx: true, ToolCallID: toolCallID})
	}

	lastTurnText, totalTurns, promptTok, complTok, err := forwardEvents(subCtx, subLoop.Run(subCtx, messages), cb, toolCallID)
	if err != nil {
		if cb != nil {
			cb(SubagentEnd{ToolCallID: toolCallID, DurationMs: time.Since(startTime).Milliseconds(), Error: err.Error()})
		}
		return &tool.ToolResult{
			Content: fmt.Sprintf("Fork subagent failed: %s", err),
			Meta:    tool.ToolMeta{Duration: time.Since(startTime)},
		}, nil
	}

	if cb != nil {
		cb(SubagentEnd{ToolCallID: toolCallID, TotalTurns: totalTurns, PromptTokens: promptTok, CompletionTokens: complTok, DurationMs: time.Since(startTime).Milliseconds()})
	}

	return &tool.ToolResult{
		Content: fmt.Sprintf("(fork subagent completed, %d turns, %d+%d tokens)\n\n%s", totalTurns, promptTok, complTok, lastTurnText),
		Meta:    tool.ToolMeta{Duration: time.Since(startTime)},
	}, nil
}

// ---------------------------------------------------------------------------
// Cold execution
// ---------------------------------------------------------------------------

func (a *AgentTool) executeCold(ctx context.Context, p AgentParams) (*tool.ToolResult, error) {
	cb := agentloop.EventCallbackFromContext(ctx)

	sp, extraDisallowed := agentConfig(p.SubagentType)
	subRegistry := buildColdRegistry(extraDisallowed)

	// Build complete system prompt: agent-specific prompt + workspace/environment from parent.
	sp = appendParentContext(sp, ctx)

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	subLoop := agentloop.New(a.LLMClient, subRegistry, agentloop.Config{
		MaxTurns:      coldMaxTurns,
		SystemPrompt:  sp,
		Guard:         permission.NewGuard(permission.WithBypassMode(true)),
		UserResponder: nil,
		ToolTimeout:   agentloop.DefaultToolTimeout,
	})

	startTime := time.Now()
	toolCallID := agentloop.ToolCallIDFromContext(ctx)

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: sp},
	}
	// Inject AGENTS.md as user message, matching the parent's context setup.
	if agentsMD := agentloop.AgentsMDFromContext(ctx); agentsMD != "" {
		messages = append(messages, llm.Message{
			Role: llm.RoleUser, Content: agentsMD,
		})
	}
	messages = append(messages, llm.Message{
		Role: llm.RoleUser, Content: fmt.Sprintf("Task: %s\n\n%s", p.Description, p.Prompt),
	})

	if cb != nil {
		cb(SubagentStart{Prompt: p.Description, AgentType: p.SubagentType, InheritCtx: false, ToolCallID: toolCallID})
	}

	lastTurnText, totalTurns, promptTok, complTok, err := forwardEvents(subCtx, subLoop.Run(subCtx, messages), cb, toolCallID)
	if err != nil {
		if cb != nil {
			cb(SubagentEnd{ToolCallID: toolCallID, DurationMs: time.Since(startTime).Milliseconds(), Error: err.Error()})
		}
		return &tool.ToolResult{
			Content: fmt.Sprintf("Subagent [%s] failed: %s", p.SubagentType, err),
			Meta:    tool.ToolMeta{Duration: time.Since(startTime)},
		}, nil
	}

	if cb != nil {
		cb(SubagentEnd{ToolCallID: toolCallID, TotalTurns: totalTurns, PromptTokens: promptTok, CompletionTokens: complTok, DurationMs: time.Since(startTime).Milliseconds()})
	}

	return &tool.ToolResult{
		Content: fmt.Sprintf("(subagent [%s] completed, %d turns, %d+%d tokens)\n\n%s", p.SubagentType, totalTurns, promptTok, complTok, lastTurnText),
		Meta:    tool.ToolMeta{Duration: time.Since(startTime)},
	}, nil
}

// ---------------------------------------------------------------------------
// registry builders
// ---------------------------------------------------------------------------

func (a *AgentTool) buildForkRegistry() tool.Registry {
	r := tool.NewRegistry()
	for _, t := range allTools() {
		if !allAgentDisallowed[t.Name()] {
			r.Register(t)
		}
	}
	return r
}

func buildColdRegistry(extraDisallowed map[string]bool) tool.Registry {
	r := tool.NewRegistry()
	for _, t := range allTools() {
		name := t.Name()
		if allAgentDisallowed[name] || extraDisallowed[name] {
			continue
		}
		r.Register(t)
	}
	return r
}

func allTools() []tool.Tool {
	return []tool.Tool{
		tool.Wrap(&tool.ReadFile{}),
		tool.Wrap(&tool.WriteFile{}),
		tool.Wrap(&tool.EditFile{}),
		tool.Wrap(&tool.WebFetch{}),
		tool.Wrap(&tool.Shell{AllowBg: false}), // bash_subagent
	}
}

// ---------------------------------------------------------------------------
// tool filter maps
// ---------------------------------------------------------------------------

var allAgentDisallowed = map[string]bool{
	"agent":                true,
	"bash":                 true,
	"enter_plan_mode":      true,
	"exit_plan_mode":       true,
	"ask_user_question":    true,
	"kill_background_task": true,
}

var exploreDisallowed = map[string]bool{
	"write_file": true,
	"edit_file":  true,
}

func agentConfig(agentType string) (systemPrompt string, extraDisallowed map[string]bool) {
	switch agentType {
	case "Explore":
		return exploreSystemPrompt(), exploreDisallowed
	case "general-purpose":
		return generalPurposeSystemPrompt(), nil
	default:
		return generalPurposeSystemPrompt(), nil
	}
}

// appendParentContext appends workspace and environment context from the parent
// system prompt to the agent-specific prompt, so cold agents know CWD and toolchain.
func appendParentContext(agentSP string, ctx context.Context) string {
	parentSP := agentloop.ParentSystemPromptFromContext(ctx)
	if parentSP == "" {
		return agentSP
	}
	// Extract "## Workspace" and everything after it (includes "## Environment").
	if idx := strings.Index(parentSP, "## Workspace"); idx >= 0 {
		agentSP += "\n\n" + parentSP[idx:]
	}
	return agentSP
}

// ---------------------------------------------------------------------------
// event forwarding
// ---------------------------------------------------------------------------

// writeOp records a write operation performed by the subagent.
type writeOp struct {
	ToolName string
	FilePath string
	BytesIn  int
	LinesAdd int
	LinesDel int
}

func forwardEvents(ctx context.Context, subCh <-chan agentloop.TurnEvent, cb func(agentloop.TurnEvent), toolCallID string) (lastTurnText string, totalTurns int, promptTokens int, completionTokens int, finalErr error) {
	var sb strings.Builder
	var writeOps []writeOp
	var currentTurn int

	for ev := range subCh {
		switch e := ev.(type) {
		case agentloop.StreamDelta:
			if e.Turn > currentTurn {
				// 进入新 turn：只保留最后一个 turn 的文本，丢弃中间推理过程
				currentTurn = e.Turn
				sb.Reset()
			}
			if e.ContentDelta != "" {
				sb.WriteString(e.ContentDelta)
				pushEvent(cb, SubagentEvent{ToolCallID: toolCallID, Kind: SubagentText, TextDelta: e.ContentDelta})
			}

		case agentloop.ToolCallStart:
			args := formatArgs(e.ToolCallName, e.Arguments)
			pushEvent(cb, SubagentEvent{ToolCallID: toolCallID, Kind: SubagentToolStart, ToolName: e.ToolCallName, ToolArgs: args})

		case agentloop.ToolCallStream:
			pushEvent(cb, SubagentEvent{ToolCallID: toolCallID, Kind: SubagentToolResult, ToolName: e.ToolCallName, ToolResult: e.Chunk})

		case agentloop.ToolCallResult:
			pushEvent(cb, SubagentEvent{ToolCallID: toolCallID, Kind: SubagentToolResult, ToolName: e.ToolCallName, ToolResult: e.Result, ToolDurMs: e.DurationMs, ToolError: e.Error})
			if e.ToolCallName == "write_file" || e.ToolCallName == "edit_file" {
				op := writeOp{ToolName: e.ToolCallName, FilePath: extractPath(e.Result), BytesIn: len(e.Result)}
				if e.ToolCallName == "edit_file" {
					op.LinesAdd, op.LinesDel = countDiff(e.Result)
				}
				writeOps = append(writeOps, op)
			}

		case agentloop.TurnStats:
			promptTokens += e.PromptTokens
			completionTokens += e.CompletionTokens

		case agentloop.LoopDone:
			totalTurns = e.Turn
			if e.Err != nil {
				finalErr = e.Err
			}
			if len(writeOps) > 0 {
				sb.WriteString("\n\n<subagent_write_operations>\n")
				for _, op := range writeOps {
					switch op.ToolName {
					case "write_file":
						fmt.Fprintf(&sb, "- write_file: %s (%s)\n", op.FilePath, fmtBytes(op.BytesIn))
					case "edit_file":
						fmt.Fprintf(&sb, "- edit_file: %s (+%d -%d lines)\n", op.FilePath, op.LinesAdd, op.LinesDel)
					}
				}
				sb.WriteString("</subagent_write_operations>")
			}
			return sb.String(), totalTurns, promptTokens, completionTokens, finalErr
		}
	}
	return sb.String(), totalTurns, promptTokens, completionTokens, nil
}

func pushEvent(cb func(agentloop.TurnEvent), ev SubagentEvent) {
	if cb != nil {
		cb(ev)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func formatArgs(toolName, argsJSON string) string {
	switch toolName {
	case "read_file", "write_file", "edit_file":
		return extractField(argsJSON, "file_path")
	case "bash_subagent", "bash":
		return extractField(argsJSON, "command")
	case "web_fetch":
		if u := extractField(argsJSON, "url"); u != "" {
			return u
		}
	}
	return argsJSON
}

func extractField(jsonStr, key string) string {
	search := `"` + key + `"`
	idx := strings.Index(jsonStr, search)
	if idx < 0 {
		return ""
	}
	rest := jsonStr[idx+len(search):]
	colonIdx := strings.Index(rest, ":")
	if colonIdx < 0 {
		return ""
	}
	rest = strings.TrimLeft(rest[colonIdx+1:], " \t")
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	if endIdx := strings.Index(rest, `"`); endIdx >= 0 {
		return rest[:endIdx]
	}
	return ""
}

func extractPath(result string) string {
	// write_file: "Created new file: /path\n" or "Updated file: /path\n"
	for _, prefix := range []string{"Created new file: ", "Updated file: "} {
		if idx := strings.Index(result, prefix); idx >= 0 {
			path := strings.TrimSpace(result[idx+len(prefix):])
			if end := strings.IndexAny(path, "\n "); end >= 0 {
				path = path[:end]
			}
			return path
		}
	}
	// edit_file: "✅ Edit applied to /path ..."
	if idx := strings.Index(result, " to "); idx >= 0 {
		path := strings.TrimSpace(result[idx+4:])
		if end := strings.IndexAny(path, "\n "); end >= 0 {
			path = path[:end]
		}
		return path
	}
	return ""
}

func fmtBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// buildForkMessages 从父消息构建 fork 子 agent 的消息历史。
// 继承父消息，但剥离最后一条 assistant（含发起 fork 的未配对 agent tool_call）。
// 若父消息不存在则创建新的干净消息（兜底）。
func buildForkMessages(parentRaw interface{}, description, prompt string) []llm.Message {
	if parentRaw == nil {
		return []llm.Message{
			{Role: llm.RoleSystem, Content: generalPurposeSystemPrompt()},
			{Role: llm.RoleUser, Content: fmt.Sprintf("Fork task: %s\n\n%s", description, prompt)},
		}
	}
	msgs, ok := parentRaw.([]llm.Message)
	if !ok || len(msgs) == 0 {
		return []llm.Message{
			{Role: llm.RoleSystem, Content: generalPurposeSystemPrompt()},
			{Role: llm.RoleUser, Content: fmt.Sprintf("Fork task: %s\n\n%s", description, prompt)},
		}
	}

	// 克隆并剥离最后一条 assistant 消息
	cloned := make([]llm.Message, len(msgs))
	copy(cloned, msgs)
	for i := len(cloned) - 1; i >= 0; i-- {
		if cloned[i].Role == llm.RoleAssistant {
			cloned = append(cloned[:i], cloned[i+1:]...)
			break
		}
	}

	// 追加 fork 指令
	cloned = append(cloned, llm.Message{
		Role:    llm.RoleUser,
		Content: fmt.Sprintf("Fork task: %s\n\n%s", description, prompt),
	})
	return cloned
}

func countDiff(output string) (added, removed int) {
	for _, line := range strings.Split(output, "\n") {
		t := strings.TrimLeft(line, " ")
		if strings.HasPrefix(t, "+") && !strings.HasPrefix(t, "+++") {
			added++
		} else if strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "---") {
			removed++
		}
	}
	return
}

