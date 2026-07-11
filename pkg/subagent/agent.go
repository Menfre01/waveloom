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
	Model        string `json:"model,omitempty"`          // 可选模型覆盖，空/无效 = 继承主模型
}

// AgentTool 实现 tool.TypedTool[AgentParams]，将任务委派给子 agent 执行。
type AgentTool struct {
	LLMClient       llm.Client
	ValidModels     []string // 可用模型列表，空 = 不限制
	DefaultModel    string   // 主模型名，advisor subagent 锁定使用（也用于 TUI suffix 显示）
	DefaultSubModel string   // Explore 等轻量 agent 的默认模型
	WorkspaceDir    string   // 工作目录，用于分类器路径检查
}

func (a *AgentTool) Name() string              { return "agent" }
func (a *AgentTool) ConcurrentSafe() bool      { return true }

// isValidModel 校验模型名称是否在允许列表中。
// 空字符串视为有效（继承默认）；空列表视为不限制。
func (a *AgentTool) isValidModel(m string) bool {
	if m == "" {
		return true
	}
	if len(a.ValidModels) == 0 {
		return true
	}
	for _, v := range a.ValidModels {
		if v == m {
			return true
		}
	}
	return false
}

func (a *AgentTool) Description() string {
	return strings.Join([]string{
		"Launch a subagent to handle complex, multi-step tasks autonomously.",
		"",
		"subagent_type parameter (omit to fork — the DEFAULT):",
		"  (omit / empty)  → fork: inherits your full conversation context. Tools: all except agent/bash/enter_plan_mode/exit_plan_mode/ask_user_question/kill_background_task.",
		"  \"Explore\"       → cold, read-only discovery. Tools: read_file, bash_subagent, web_fetch. Use for finding code patterns and locations.",
		"  \"evaluate\"      → cold, read-only assessment. Tools: read_file, bash_subagent, web_fetch. Use for code review, security audit, second opinion.",
		"  \"verification\"  → cold, read-only testing. Tools: read_file, bash_subagent, web_fetch. Use to run builds/tests and try to BREAK an implementation.",
		"  \"advisor\"       → fork, read-only analysis with FULL context. Tools: read_file, bash_subagent, web_fetch. Use for deep analysis, trade-off evaluation, and decision support when you need the primary model's reasoning.",
		"",
		"Fork vs cold — affects how you write the prompt:",
		"  Fork: the subagent already knows the conversation background. Write a directive — no need to re-explain context.",
		"  Cold: the subagent starts with NO prior context. Provide a self-contained prompt with full background.",
		"",
		"Refer to the system prompt for detailed rules on when to use (or not use) each agent type.",
	}, "\n")
}

func (a *AgentTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "subagent_type": {
      "type": "string",
      "description": "Omit to fork (DEFAULT, cheap, shares cache). Set to 'evaluate' for code review / security audit, 'Explore' for finding code patterns, 'verification' for post-implementation testing, or 'advisor' for deep read-only analysis with full context. Cold agents are expensive — they cannot reuse your prompt cache."
    },
    "description": {
      "type": "string",
      "description": "A short (3-5 word) description of the task"
    },
    "prompt": {
      "type": "string",
      "description": "The task for the subagent to perform"
    },
    "model": {
      "type": "string",
      "description": "Optional model override. Available values are listed in the system prompt under 'Subagent Model Selection'. Omit or leave blank to use the default. Invalid values are ignored."
    }
  },
  "required": ["description", "prompt"]
}`)
}

// ---------------------------------------------------------------------------
// constants
// ---------------------------------------------------------------------------

const (
	forkMaxTurns  = 200
	coldMaxTurns  = 50
	exploreMaxTurns = 25 // 只读搜索任务通常更快完成

	// forkBoilerplateTag 是 fork 身份边界的 XML 标签，用于：
	// 1. 告知 fork 子 agent 它是 fork 而非主 agent（身份识别）
	// 2. 检测递归 fork（isInForkChild 通过扫描此标签判断）
	forkBoilerplateTag = "fork-boilerplate"

	// forkPlaceholderResult 是所有 fork 子 agent tool_result 的统一占位文本。
	// 必须对所有并行 fork 字节级一致，确保 DeepSeek 前缀缓存共享最大化。
	forkPlaceholderResult = "Fork started — processing in background"
)

// ---------------------------------------------------------------------------
// agent system prompts
// ---------------------------------------------------------------------------

func exploreSystemPrompt() string {
	return `You are a read-only file exploration agent. Search, read, and locate patterns in existing code.
You are a discovery tool — find where things are, not whether they are correct.
Tools: read_file, bash_subagent, web_fetch.
bash_subagent is for READ-ONLY operations: inspecting files, searching code, checking git history — anything that does not modify the filesystem.
NEVER use bash_subagent for: mkdir, touch, rm, cp, mv, chmod, chown, echo > (redirect), tee, git add, git commit, npm install, pip install, or any filesystem modification.
NEVER: write_file, edit_file, mkdir, rm, cp, mv, chmod, git add, git commit, or any filesystem write.

CRITICAL: Your final message MUST contain a non-empty summary. Never end with a silent response — even if you only ran searches, describe what you found.

OUTPUT RULES (output tokens are the most expensive resource — 
240x the cost of cached input, 2x the cost of uncached input):
- Be concise, but not at the expense of correctness. Include details when they matter.
- Do NOT echo back file contents verbatim — reference paths and line numbers instead. Short code snippets that ARE the answer are fine.
- Aim for under 200 words unless the findings genuinely demand more detail.
- No conversational filler, no "let me summarize", no meta-commentary.
- Preferred format (adapt as needed):
  Scope: <one sentence>
  Findings: <key facts or answers>
  Key files: <paths, line ranges>
  Issues: <only if something is wrong>`
}

func verificationSystemPrompt() string {
	return `You are a verification specialist. Your job is NOT to confirm the implementation works — 
it's to try to BREAK it.

You are a READ-ONLY agent for the project directory. You CANNOT use write_file or edit_file.
bash_subagent is for READ-ONLY operations: running tests, compiling, checking git history —
anything that does not modify project files.
NEVER use bash_subagent for: mkdir, touch, rm, cp, mv, chmod, chown, echo > (redirect),
tee, sed -i, git add, git commit, npm install, pip install, or any filesystem modification
inside the project directory.
However, you MAY create ephemeral test scripts in /tmp via bash_subagent when inline commands
aren't sufficient (e.g., a multi-step test harness). Clean up /tmp files when done.

=== WHAT YOU RECEIVE ===
The caller will describe: the original task, what was changed, the approach taken,
and optionally the relevant file paths.

=== VERIFICATION STRATEGY ===
1. Read the changed files — understand what was modified
2. Run the build (if applicable). A broken build is an automatic FAIL.
3. Run the project's test suite (if it has one). Failing tests are an automatic FAIL.
4. Exercise the changed code. Reading is not verification — execute it.
5. Try adversarial inputs: boundary values, empty inputs, malformed data, concurrency edge cases.
6. Check for regressions in related code.

=== OUTPUT FORMAT (REQUIRED) ===
Every check MUST include the exact command run and the observed output:

### Check: <what you verified>
**Command run:**
  <exact command>
**Output observed:**
  <actual output — copy-paste, not paraphrased. Truncate if very long.>
**Result: PASS** (or FAIL — with Expected vs Actual)

End with exactly:
VERDICT: PASS
or
VERDICT: FAIL

=== BEFORE ISSUING FAIL ===
Before reporting FAIL, verify:
- Is there defensive code elsewhere that prevents this?
- Is the behavior intentional (check commit messages, comments)?
- Is it a real limitation that can't be fixed without breaking an external contract?

=== OUTPUT RULES ===
- Evidence over narration. Every claim must be backed by a command run and its output.
- If you catch yourself writing an explanation instead of a command, stop. Run the command.
- No conversational filler. Output the checks, then the verdict.
- CRITICAL: Your final message MUST contain the check results and a VERDICT line. Never end with a silent/empty response.`
}

func evaluateSystemPrompt() string {
	return `You are an independent evaluation agent. Your role is to assess correctness, quality, and security — 
not to implement changes.

You are READ-ONLY for the project directory. You CANNOT use write_file or edit_file.
bash_subagent is for READ-ONLY operations: running tests, compiling, checking git history —
anything that does not modify project files.
NEVER use bash_subagent for: mkdir, touch, rm, cp, mv, chmod, chown, echo > (redirect),
tee, sed -i, git add, git commit, npm install, pip install, or any filesystem modification
inside the project directory.
You MAY create ephemeral test scripts in /tmp via bash_subagent when you need to test behavior.
Clean up /tmp files when done.

Approach:
- Read the relevant code thoroughly before forming an opinion
- If a test suite exists, run it — but don't trust it blindly (the implementer is an LLM too)
- Think about edge cases, error paths, race conditions, and security implications
- Distinguish between "this is wrong" (must fix) and "this could be improved" (nice to have)

OUTPUT RULES (output tokens are the most expensive — 240x cached input, 2x uncached):
- CRITICAL: Your final message MUST contain a non-empty assessment. Never end with a silent response.
- Aim for under 300 words unless the assessment genuinely demands more detail.
- Do not echo back code you just read — reference paths and line numbers.
- No conversational filler: no "great!", no "I reviewed the code and here's what I found".
- Preferred format (adapt as needed):
  Scope: <one sentence>
  Assessment: <PASS / NEEDS WORK / FAIL — with specific findings>
  Issues: <each with severity: CRITICAL / WARNING / NOTE, file:line, and explanation>
  Suggestions: <optional improvements, only if substantive>`
}

func advisorSystemPrompt() string {
	return `You are a read-only analysis agent. You inherit the FULL conversation context from your parent — 
you see the same message history, codebase, and tool results they saw.

Your role is to explore the codebase, analyze trade-offs, and provide a recommendation — 
NOT to implement changes. You MUST NOT write code or edit files.

You are READ-ONLY for the project directory. You CANNOT use write_file or edit_file.
bash_subagent is for READ-ONLY operations: reading files, searching code, checking git history —
anything that does not modify project files.
NEVER use bash_subagent for: mkdir, touch, rm, cp, mv, chmod, chown, echo > (redirect),
tee, sed -i, git add, git commit, npm install, pip install, or any filesystem modification
inside the project directory.

=== OUTPUT RULES ===
- Your final message MUST contain a non-empty analysis. Never end with a silent response.
- Aim for under 300 words unless the analysis genuinely demands more detail.
- Reference paths and line numbers — do not echo file contents verbatim.
- No conversational filler, no "let me analyze", no meta-commentary.
- Preferred format:
  Scope: <one sentence>
  Analysis: <key findings, trade-offs, constraints>
  Recommendation: <preferred approach with rationale>
  Alternatives: <other approaches considered, with pros/cons>
  Key files: <paths, line ranges>`
}

// ---------------------------------------------------------------------------
// Execute
// ---------------------------------------------------------------------------

// Execute
// ---------------------------------------------------------------------------

func (a *AgentTool) Execute(ctx context.Context, p AgentParams) (*tool.ToolResult, error) {
	if p.SubagentType == "" {
		// Guard against recursive forking: detect fork-boilerplate tag in parent
		// message history and reject the fork attempt at call time.
		if parentRaw := agentloop.ParentMessagesFromContext(ctx); parentRaw != nil {
			if msgs, ok := parentRaw.([]llm.Message); ok && isInForkChild(msgs) {
				return &tool.ToolResult{
					Content: "Error: You are already a fork child. Recursive forking is forbidden — execute the task directly instead of delegating.",
					Error: &tool.ToolError{
						Class:   tool.ErrorClassRecoverable,
						Kind:    tool.ErrKindSecurityViolation,
						Message: "recursive fork detected: fork child attempted to spawn another fork",
					},
				}, nil
			}
		}
		return a.executeFork(ctx, p)
	}
	// advisor is a fork variant: hot-start, read-only tools, primary model
	if p.SubagentType == "advisor" {
		return a.executeFork(ctx, p)
	}
	return a.executeCold(ctx, p)
}

// ---------------------------------------------------------------------------
// Fork execution
// ---------------------------------------------------------------------------

func (a *AgentTool) executeFork(ctx context.Context, p AgentParams) (*tool.ToolResult, error) {
	cb := agentloop.EventCallbackFromContext(ctx)

	// 模型安全兜底：无效/空 → 继承主模型
	model := p.Model
	if !a.isValidModel(model) {
		model = ""
	}

	// 从 context 获取父消息历史；buildForkMessages 会保留最后一条 assistant
	// 并注入占位 tool_result 以保证缓存友好的 fork 构造。
	parentRaw := agentloop.ParentMessagesFromContext(ctx)
	messages := buildForkMessages(parentRaw, p.Description, p.Prompt)

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	// Advisor specialization: read-only tools, primary model, advisor system prompt
	registry := a.buildForkRegistry()
	if p.SubagentType == "advisor" {
		// 始终锁定主模型（不参与次模型降级，忽略 LLM 传入的 model 参数）
		model = a.DefaultModel
		// 替换为只读 registry
		registry = buildColdRegistry(exploreDisallowed)
		// 注入 advisor system prompt 到 fork 消息中
		messages = injectAdvisorGuidance(messages)
	}

	subLoop := agentloop.New(a.LLMClient, registry, agentloop.Config{
		MaxTurns:      forkMaxTurns,
		SystemPrompt:  "", // messages already contain system prompt
		Guard:         permission.NewGuard(permission.WithBypassMode(true)),
		UserResponder: nil,
		ToolTimeout:   agentloop.DefaultToolTimeout,
		Model:         model,
	})

	startTime := time.Now()
	toolCallID := agentloop.ToolCallIDFromContext(ctx)

	agentType := "fork"
	if p.SubagentType == "advisor" {
		agentType = "advisor"
	}

	if cb != nil {
		cb(SubagentStart{Prompt: p.Description, AgentType: agentType, InheritCtx: true, ToolCallID: toolCallID, Model: model})
	}

	lastTurnText, totalTurns, promptTok, complTok, cacheHitTok, cacheMissTok, events, err := forwardEvents(subCtx, subLoop.Run(subCtx, messages), cb, toolCallID)
	if err != nil {
		if cb != nil {
			cb(SubagentEnd{ToolCallID: toolCallID, DurationMs: time.Since(startTime).Milliseconds(), Error: err.Error()})
		}
		return &tool.ToolResult{
			Content: fmt.Sprintf("Fork subagent failed: %s", err),
			Meta:    tool.ToolMeta{Duration: time.Since(startTime)},
		}, nil
	}

	// Phase 2: Layer 3 事后分类器
	classified := classify(events, a.WorkspaceDir)

	if cb != nil {
		cb(SubagentEnd{ToolCallID: toolCallID, TotalTurns: totalTurns, PromptTokens: promptTok, CompletionTokens: complTok, CacheHitTokens: cacheHitTok, CacheMissTokens: cacheMissTok, DurationMs: time.Since(startTime).Milliseconds()})
	}

	return &tool.ToolResult{
		Content: fmt.Sprintf("(fork subagent completed, %d turns, %d+%d tokens)\n\n%s%s", totalTurns, promptTok, complTok, lastTurnText, formatFindings(classified)),
		Meta:    tool.ToolMeta{Duration: time.Since(startTime)},
	}, nil
}

// ---------------------------------------------------------------------------
// Cold execution
// ---------------------------------------------------------------------------

func (a *AgentTool) executeCold(ctx context.Context, p AgentParams) (*tool.ToolResult, error) {
	cb := agentloop.EventCallbackFromContext(ctx)

	// 模型锁定：每种子代理类型绑定固定模型，忽略 LLM 传入的 model 参数。
	// 防止 LLM 误传 model 导致审查/验证质量降级或搜索成本不必要升高。
	model := p.Model
	switch p.SubagentType {
	case "Explore":
		if a.DefaultSubModel != "" {
			model = a.DefaultSubModel
		}
	case "evaluate", "verification":
		// 始终锁定主模型：DefaultModel 非空则用，否则清空走 client 默认（即主模型）
		model = a.DefaultModel
	}
	if !a.isValidModel(model) {
		model = ""
	}

	sp, extraDisallowed := agentConfig(p.SubagentType)
	subRegistry := buildColdRegistry(extraDisallowed)

	// Build tailored environment section: only include OS/Shell/CWD, not the full
	// system tool list. The subagent's own tool registry defines what it can use;
	// listing unavailable tools wastes prompt tokens and misleads the LLM.
	sp += formatSubagentEnvironment(ctx, subRegistry)

	// All cold agents are read-only on project files — they don't need AGENTS.md
	// coding standards. Dropping it saves prompt tokens.
	maxTurns := coldMaxTurns
	if p.SubagentType == "Explore" {
		maxTurns = exploreMaxTurns // 搜索任务更快完成
	}

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	subLoop := agentloop.New(a.LLMClient, subRegistry, agentloop.Config{
		MaxTurns:      maxTurns,
		SystemPrompt:  sp,
		Guard:         permission.NewGuard(permission.WithBypassMode(true)),
		UserResponder: nil,
		ToolTimeout:   agentloop.DefaultToolTimeout,
		Model:         model,
	})

	startTime := time.Now()
	toolCallID := agentloop.ToolCallIDFromContext(ctx)

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: sp},
	}
	messages = append(messages, llm.Message{
		Role: llm.RoleUser, Content: fmt.Sprintf("Task: %s\n\n%s", p.Description, p.Prompt),
	})

	if cb != nil {
		cb(SubagentStart{Prompt: p.Description, AgentType: p.SubagentType, InheritCtx: false, ToolCallID: toolCallID, Model: model})
	}

	lastTurnText, totalTurns, promptTok, complTok, cacheHitTok, cacheMissTok, events, err := forwardEvents(subCtx, subLoop.Run(subCtx, messages), cb, toolCallID)
	if err != nil {
		if cb != nil {
			cb(SubagentEnd{ToolCallID: toolCallID, DurationMs: time.Since(startTime).Milliseconds(), Error: err.Error()})
		}
		return &tool.ToolResult{
			Content: fmt.Sprintf("Subagent [%s] failed: %s", p.SubagentType, err),
			Meta:    tool.ToolMeta{Duration: time.Since(startTime)},
		}, nil
	}

	// Phase 2: Layer 3 事后分类器
	classified := classify(events, a.WorkspaceDir)

	if cb != nil {
		cb(SubagentEnd{ToolCallID: toolCallID, TotalTurns: totalTurns, PromptTokens: promptTok, CompletionTokens: complTok, CacheHitTokens: cacheHitTok, CacheMissTokens: cacheMissTok, DurationMs: time.Since(startTime).Milliseconds()})
	}

	return &tool.ToolResult{
		Content: fmt.Sprintf("(subagent [%s] completed, %d turns, %d+%d tokens)\n\n%s%s", p.SubagentType, totalTurns, promptTok, complTok, lastTurnText, formatFindings(classified)),
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

// verificationDisallowed 与 exploreDisallowed 相同：审查 agent 只读项目文件，
// 但可通过 bash_subagent 在 /tmp 创建临时脚本。
var verificationDisallowed = map[string]bool{
	"write_file": true,
	"edit_file":  true,
}

// evaluateDisallowed 与 explore/verification 相同：评估 agent 只读项目文件，
// 但可通过 bash_subagent 在 /tmp 创建临时脚本来测试行为。
var evaluateDisallowed = map[string]bool{
	"write_file": true,
	"edit_file":  true,
}

func agentConfig(agentType string) (systemPrompt string, extraDisallowed map[string]bool) {
	switch agentType {
	case "Explore":
		return exploreSystemPrompt(), exploreDisallowed
	case "evaluate":
		return evaluateSystemPrompt(), evaluateDisallowed
	case "verification":
		return verificationSystemPrompt(), verificationDisallowed
	default:
		return evaluateSystemPrompt(), evaluateDisallowed
	}
}

// formatSubagentEnvironment 为冷启动子 agent 构建精简的 ## Environment 节。
//
// 与父 agent 的完整工具列表不同，子 agent 只需要：
//   - OS / Shell 信息（来自父 system prompt）
//   - 自身 registry 中的工具列表（父 prompt 的工具列表对子 agent 无效且误导）
//
// 这避免了向 Explore agent 列出 cargo、docker 等无法直接使用的工具。
func formatSubagentEnvironment(ctx context.Context, registry tool.Registry) string {
	parentSP := agentloop.ParentSystemPromptFromContext(ctx)
	if parentSP == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n## Environment\n\n")

	// 从父 system prompt 提取 OS / Shell 行
	for _, line := range strings.Split(parentSP, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- OS:") || strings.HasPrefix(trimmed, "- Shell:") {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	// 从父 system prompt 提取 Workspace（CWD）信息
	if idx := strings.Index(parentSP, "## Workspace"); idx >= 0 {
		wsSection := parentSP[idx:]
		if end := strings.Index(wsSection, "## Environment"); end >= 0 {
			wsSection = wsSection[:end]
		}
		// 只保留 "Working directory" 行
		for _, line := range strings.Split(wsSection, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "Working directory") || strings.Contains(trimmed, "Current working") {
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
	}

	// 仅列出子 agent registry 中可用的工具
	tools := registry.List()
	if len(tools) > 0 {
		b.WriteString("\nAvailable tools:\n")
		for _, t := range tools {
			fmt.Fprintf(&b, "  %-16s %s\n", t.Name, truncateTo(t.Description, 80))
		}
	}

	return b.String()
}

// truncateTo 截断字符串到 maxLen 字符，超出部分用 "..." 替代。
func truncateTo(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
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

func forwardEvents(ctx context.Context, subCh <-chan agentloop.TurnEvent, cb func(agentloop.TurnEvent), toolCallID string) (lastTurnText string, totalTurns int, promptTokens int, completionTokens int, cacheHitTokens int, cacheMissTokens int, events []SubagentEvent, finalErr error) {
	var sb strings.Builder
	var writeOps []writeOp
	var currentTurn int
	var lastToolCalls []string // 最后一个 turn 的工具调用名列表（用于空文本兜底）

	// 缓冲扇出通道：解耦 subagent 事件消费与 TUI 投递。
	// 若不隔离，pushEvent → m.program.Send() 可能因 TUI 消息通道拥塞而阻塞，
	// 进而阻塞 forwardEvents → 阻塞 subLoop goroutine → 级联死锁。
	// 此 channel 由专用 goroutine 消费，保证事件投递顺序且不丢事件。
	//
	// Buffer 容量选取：subagent 事件总量受 MaxTurns（fork=200, cold=50）约束，
	// 不存在无界增长风险。16384 ≈ 典型场景（20 turns × 500 tokens）的 ~1.6 倍余量，
	// 在 100 events/s 流式速率下可吸收 ~164 秒的 TUI 拥塞，远超任何合理卡顿时长。
	fanout := make(chan agentloop.TurnEvent, 16384)
	fanoutDone := make(chan struct{})
	go func() {
		defer close(fanoutDone)
		for ev := range fanout {
			if cb != nil {
				cb(ev)
			}
		}
	}()

	// defer 在函数返回前关闭 fanout 并等待所有事件投递完成，
	// 确保 SubagentEnd 之前的全部 SubagentEvent 已被 TUI 消费。
	defer func() {
		close(fanout)
		<-fanoutDone
	}()

	for ev := range subCh {
		switch e := ev.(type) {
		case agentloop.StreamDelta:
			if e.Turn > currentTurn {
				// 进入新 turn：只保留最后一个 turn 的文本，丢弃中间推理过程
				currentTurn = e.Turn
				sb.Reset()
				lastToolCalls = lastToolCalls[:0]
			}
			// Phase 2: 转发思考过程（dimmed 渲染）
			if e.ReasoningDelta != "" {
				ev := SubagentEvent{ToolCallID: toolCallID, Kind: SubagentThought, TextDelta: e.ReasoningDelta}
				fanout <- ev
				events = append(events, ev)
			}
			if e.ContentDelta != "" {
				sb.WriteString(e.ContentDelta)
				ev := SubagentEvent{ToolCallID: toolCallID, Kind: SubagentText, TextDelta: e.ContentDelta}
				fanout <- ev
				events = append(events, ev)
			}

		case agentloop.ToolCallStart:
			lastToolCalls = append(lastToolCalls, e.ToolCallName)
			args := formatArgs(e.ToolCallName, e.Arguments)
			ev := SubagentEvent{ToolCallID: toolCallID, Kind: SubagentToolStart, ToolName: e.ToolCallName, ToolArgs: args}
			fanout <- ev
			events = append(events, ev)

		case agentloop.ToolCallStream:
			ev := SubagentEvent{ToolCallID: toolCallID, Kind: SubagentToolStream, ToolName: e.ToolCallName, ToolResult: e.Chunk}
			fanout <- ev
			events = append(events, ev)

		case agentloop.ToolCallResult:
			ev := SubagentEvent{ToolCallID: toolCallID, Kind: SubagentToolResult, ToolName: e.ToolCallName, ToolResult: e.Result, ToolDurMs: e.DurationMs, ToolError: e.Error}
			fanout <- ev
			events = append(events, ev)
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
			cacheHitTokens += e.CacheHitTokens
			cacheMissTokens += e.CacheMissTokens

		case agentloop.LoopDone:
			totalTurns = e.Turn
			if e.Err != nil {
				finalErr = e.Err
			}
			// 兜底：子 agent 最后一个 turn 无文本输出时，
			// 合成非空 fallback 防止 tool_result 内容为空，避免父 agent 因空结果而误解。
			ensureNonEmpty(&sb, lastToolCalls)
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
			return sb.String(), totalTurns, promptTokens, completionTokens, cacheHitTokens, cacheMissTokens, events, finalErr
		}
	}
	// Channel 关闭但未收到 LoopDone（跨包防御：当前 agentloop.Run 总是会发送 LoopDone，
	// 但此处做兜底防止未来引入的不发送 LoopDone 的路径导致空文本传播）。
	ensureNonEmpty(&sb, lastToolCalls)
	return sb.String(), totalTurns, promptTokens, completionTokens, cacheHitTokens, cacheMissTokens, events, nil
}

// ensureNonEmpty 在 sb 为空时合成非空 fallback 文本。
// 覆盖三种场景：
//   - 全程无文本输出（!anyText 的情况由调用方保证，此处仅检查 sb）
//   - 前序 turn 有文本但最后 turn 被重置为空，且无工具调用
//   - 前序 turn 有文本但最后 turn 被重置为空，有工具调用
func ensureNonEmpty(sb *strings.Builder, lastToolCalls []string) {
	if sb.Len() > 0 {
		return
	}
	if len(lastToolCalls) > 0 {
		fmt.Fprintf(sb, "(completed via: %s)", strings.Join(lastToolCalls, ", "))
	} else {
		sb.WriteString("(no summary text produced)")
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
	// edit_file: "Edited file: /path\n"
	if idx := strings.Index(result, "Edited file: "); idx >= 0 {
		path := strings.TrimSpace(result[idx+len("Edited file: "):])
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
//
// 策略：
//  1. 保留完整最后一条 assistant（含所有 tool_use），不剥离 —— 保持与父 agent
//     前缀连续性，最大化 DeepSeek 硬盘缓存命中率
//  2. 为 assistant 中每个 tool_call 注入 tool 角色占位消息，文本统一使用
//     forkPlaceholderResult —— 所有并行 fork 的 tool_result 前缀字节完全一致，
//     进一步合并缓存
//  3. 追加一条 user 消息，包含 <fork-boilerplate> 身份注入 + 任务指令
//
// 结果：[...parent, assistant(tool_calls), tool(id1, placeholder), tool(id2, placeholder),
//
//	user(<fork-boilerplate> + task directive)]
//
// 若父消息不存在则创建新的干净消息（兜底）。
func buildForkMessages(parentRaw interface{}, description, prompt string) []llm.Message {
	if parentRaw == nil {
		return []llm.Message{
			{Role: llm.RoleSystem, Content: "You are a coding agent. Complete the task using the tools available to you."},
			{Role: llm.RoleUser, Content: buildForkDirective(description, prompt)},
		}
	}
	msgs, ok := parentRaw.([]llm.Message)
	if !ok || len(msgs) == 0 {
		return []llm.Message{
			{Role: llm.RoleSystem, Content: "You are a coding agent. Complete the task using the tools available to you."},
			{Role: llm.RoleUser, Content: buildForkDirective(description, prompt)},
		}
	}

	// 克隆全部父消息
	cloned := make([]llm.Message, len(msgs))
	copy(cloned, msgs)

	// 找到最后一条 assistant 消息，为其 tool_calls 注入占位 tool_result
	lastAssistant := findLastAssistant(cloned)
	if lastAssistant != nil && len(lastAssistant.ToolCalls) > 0 {
		for _, tc := range lastAssistant.ToolCalls {
			cloned = append(cloned, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Content:    forkPlaceholderResult,
			})
		}
	}

	// 追加 fork 身份注入 + 任务指令
	cloned = append(cloned, llm.Message{
		Role:    llm.RoleUser,
		Content: buildForkDirective(description, prompt),
	})
	return cloned
}

// findLastAssistant 返回消息列表中最后一条 assistant 消息，无则返回 nil。
func findLastAssistant(msgs []llm.Message) *llm.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant {
			return &msgs[i]
		}
	}
	return nil
}

// buildForkDirective 构造 fork 子 agent 的身份注入提示词。
//
// 设计要点：
//   - <fork-boilerplate> 身份边界标记（用于 isInForkChild 递归检测）
//   - 极简规则：输出 token 成本意识 + 结构化格式 + 省略空字段
//   - English 标签（DeepSeek tokenizer 下比中文标签省 ~50% token）
func buildForkDirective(description, prompt string) string {
	return fmt.Sprintf(`<%s>
You are a fork child process. The message history above is inherited from your parent — 
understand the context, then execute the task below.

Rules:
1. Your final message MUST contain a non-empty summary of what you did. Never end with a silent/empty response — even if all work was done via tool calls, summarize the outcome.
2. Output tokens are expensive (240x cached input, 2x uncached). Be concise. Aim for under 300 words unless findings genuinely demand more detail.
3. Do NOT call the agent tool (you ARE the fork — execute directly)
4. No conversation, no questions, no commentary. Use tools silently, report once at the end.
5. Stay within the task scope. Related observations outside scope deserve at most one sentence.
6. Preferred format (English labels; adapt as needed):

Scope: <one sentence echoing the task>
Result: <findings or work done — details when they matter>
Key files: <paths, line ranges>
Files changed: <paths, only if modified>
Issues: <only if something is wrong>

Task: %s
%s</%s>`, forkBoilerplateTag, description, prompt, forkBoilerplateTag)
}

// injectAdvisorGuidance 将 advisor system prompt 注入到 fork 消息中。
// 在 fork directive 之后追加一条 user 消息，包含 advisor 专用指导。
func injectAdvisorGuidance(messages []llm.Message) []llm.Message {
	guidance := advisorSystemPrompt()
	// 在最后一条 user 消息（fork directive）之后追加 advisor 指导
	return append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: fmt.Sprintf("<%s-advisor>\n%s\n</%s-advisor>", forkBoilerplateTag, guidance, forkBoilerplateTag),
	})
}

// isInForkChild 检测消息历史中是否已包含 fork-boilerplate 标记，
// 用于防止 fork 子 agent 递归创建孙子 fork。
func isInForkChild(messages []llm.Message) bool {
	tag := fmt.Sprintf("<%s>", forkBoilerplateTag)
	for _, m := range messages {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, tag) {
			return true
		}
	}
	return false
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

