package subagent
import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/sandbox"
	"github.com/Menfre01/waveloom/pkg/session"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// context helpers
// ---------------------------------------------------------------------------

// ParentSystemPromptFromContext 从 ctx 提取父 Loop 注入的 system prompt。
// 委托到 agentloop.ParentSystemPromptFromContext(key 定义在 agentloop/context.go)。
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
	Model        string `json:"model,omitempty"`          // 可选模型覆盖,空/无效 = 继承主模型
}

// SettingsProvider 抽象 settings.json 中 LLM 配置的读取。
type SettingsProvider interface {
	LoadLLM() (*llm.LLMSettings, error)
}

// AgentTool 实现 tool.TypedTool[AgentParams],将任务委派给子 agent 执行。
type AgentTool struct {
	LLMClient       llm.Client
	Settings        SettingsProvider
	DefaultModel    string // 主模型名
	DefaultSubModel string // explore 等轻量 agent 的默认模型
	WorkspaceDir    string // 工作目录,用于分类器路径检查
	SandboxMgr      *sandbox.SandboxManager // 沙箱管理器(可选):子代理 bash 同样进沙箱
	Guard           permission.Guard         // 父级权限 Guard(规则继承,三审 High-1)
	CompactionConfig compaction.CompactionConfig // 压缩配置(与主 loop/HUD/ACP 同源;子代理用独立 compactor 实例)

	mu sync.RWMutex // 保护 LLMClient 的并发读写(SetClient 与 executeFork/executeCold)

	// subagent JSONL 持久化
	sessionsDir string // session 目录路径
	sessionID   string // 当前 session ID
	buildVer    string // 构建版本
}

func (a *AgentTool) SetSessionInfo(sessionsDir, sessionID, version string) {
	a.sessionsDir = sessionsDir
	a.sessionID = sessionID
	a.buildVer = version

}

// SetClient 更新 LLM Client 引用,用于 provider 运行时切换后热替换。
// 父 agent 的 TUI 层在 reconfigureLLMClient / reconfigureLLMClientForProvider 中调用。
// 线程安全:内部持写锁,与 executeFork/executeCold 的读锁互斥。
func (a *AgentTool) SetClient(client llm.Client) {
	a.mu.Lock()
	a.LLMClient = client
	a.mu.Unlock()
}

// newCompactor 创建子代理专用压缩器(独立实例,不复用父级——watermark/turn
// 计数会污染)。summarizer 为 nil → Tier 3 跳过(Tier3SkippedNoSummarizer),
// Tier 1/2(snip/prune)零成本生效。配置零值 → normalize 兜底 1M。
func (a *AgentTool) newCompactor() compaction.Compactor {
	return compaction.NewCompactor(a.CompactionConfig, nil)
}

// saveSubagentTranscript 将 subagent 事件持久化为 JSONL 文件和 metadata。
func (a *AgentTool) saveSubagentTranscript(agentID, agentType, description, model string, totalSteps, promptTok, complTok, cacheHitTok, cacheMissTok int, events []SubagentEvent) {
	if a.sessionsDir == "" || a.sessionID == "" {
		return
	}
	metaPath := session.SubagentMetaPath(a.sessionsDir, a.sessionID, agentID)
	_ = session.SaveAgentMetadata(metaPath, session.AgentMetadata{
		AgentType:        agentType,
		Description:      description,
		Model:            model,
		TotalSteps:       totalSteps,
		PromptTokens:     promptTok,
		CompletionTokens: complTok,
		CacheHitTokens:   cacheHitTok,
		CacheMissTokens:  cacheMissTok,
	})

	// 转换 SubagentEvent → TranscriptEntry(CC 兼容格式,isSidechain:true)
	messages := subagentEventsToMessages(events)
	entries := session.MessagesToTranscriptEntries(messages, nil, a.sessionID, a.buildVer, "", "")
	for i := range entries {
		entries[i].IsSidechain = true
	}

	jlPath := session.SubagentTranscriptPath(a.sessionsDir, a.sessionID, agentID)
	if err := session.WriteTranscriptEntries(jlPath, entries); err != nil {
		slog.Warn("subagent transcript write failed", "agentID", agentID, "err", err)
	}
}

// subagentEventsToMessages 将 SubagentEvent 列表转换为 llm.Message 列表。
// 连续的文本事件合并为一条 assistant 消息,tool_start+tool_result 配对为 assistant+tool 消息。
func subagentEventsToMessages(events []SubagentEvent) []llm.Message {
	if len(events) == 0 {
		return nil
	}
	var messages []llm.Message
	var textBuf strings.Builder
	var reasoningBuf strings.Builder
	flushText := func() {
		if textBuf.Len() > 0 || reasoningBuf.Len() > 0 {
			messages = append(messages, llm.Message{
				Role:             llm.RoleAssistant,
				Content:          textBuf.String(),
				ReasoningContent: reasoningBuf.String(),
			})
			textBuf.Reset()
			reasoningBuf.Reset()
		}
	}

	for _, ev := range events {
		switch ev.Kind {
		case SubagentText:
			textBuf.WriteString(ev.TextDelta)
		case SubagentThought:
			reasoningBuf.WriteString(ev.TextDelta)
		case SubagentToolStart:
			flushText()
			messages = append(messages, llm.Message{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{
					{ID: ev.ToolCallID, Name: ev.ToolName, Arguments: ev.ToolArgs},
				},
			})
		case SubagentToolResult:
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    ev.ToolResult,
				ToolCallID: ev.ToolCallID,
				Name:       ev.ToolName,
			})
		}
	}
	flushText()
	return messages
}

func (a *AgentTool) Name() string              { return "agent" }
func (a *AgentTool) ConcurrentSafe() bool      { return true }

// ToolTimeout 返回 agent 工具的推荐超时(30 分钟)。
// 子 agent 内部有多轮 LLM 调用 + 工具执行,需要比普通工具更充裕的时间。
func (a *AgentTool) ToolTimeout() time.Duration { return 30 * time.Minute }

// resolveModel 将 pro/flash 映射到实际模型名。
// "pro"/"" → 主模型;"flash" → SubModel(为空时 fallback 主模型)
func (a *AgentTool) resolveModel(m string) string {
	switch m {
	case "flash":
		if a.DefaultSubModel != "" {
			return a.DefaultSubModel
		}
		return a.DefaultModel
	default:
		return a.DefaultModel
	}
}

func (a *AgentTool) Description() string {
	return "Launch a subagent to handle complex, multi-step tasks. See ## Agent Tool in the system prompt for agent types, when to fork vs cold, and prompt-writing guidance."
}

func (a *AgentTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "subagent_type": {
      "type": "string",
      "description": "Omit to fork (DEFAULT). Set to 'Explore', 'evaluate', or 'verification' for specialized agents. See ## Agent Tool in system prompt for details."
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
      "enum": ["pro", "flash"],
      "description": "Optional model override. 'pro' = full reasoning (default), 'flash' = faster/cheaper. Omit to use default."
    }
  },
  "required": ["description", "prompt"]
}`)
}

// ---------------------------------------------------------------------------
// constants
// ---------------------------------------------------------------------------

const (
	forkMaxSteps    = 50
	coldMaxSteps    = 50
	exploreMaxSteps = 25

	// forkBoilerplateTag 是 fork 身份边界的 XML 标签,用于:
	// 1. 告知 fork 子 agent 它是 fork 而非主 agent(身份识别)
	// 2. 检测递归 fork(isInForkChild 通过扫描此标签判断)
	forkBoilerplateTag = "fork-boilerplate"
)

// ---------------------------------------------------------------------------
// agent system prompts
// ---------------------------------------------------------------------------

const coldAgentPreamble = `You are READ-ONLY for the project directory. You CANNOT use write_file or edit_file.
bash_subagent is for READ-ONLY operations: running tests, compiling, reading files,
searching code, checking git history — anything that does not modify project files.
Use web_fetch and web_search for online documentation and research.
NEVER use bash_subagent for: mkdir, touch, rm, cp, mv, chmod, chown, echo > (redirect),
tee, sed -i, git add, git commit, npm install, pip install, or any filesystem modification
inside the project directory.
`

func exploreSystemPrompt() string {
	return `You are a read-only file exploration agent. Search, read, and locate patterns in existing code.
You are a discovery tool — find where things are, not whether they are correct.
Tools: read_file, bash_subagent, web_fetch, web_search.
bash_subagent is for READ-ONLY operations: inspecting files, searching code, checking git history — anything that does not modify the filesystem.
NEVER use bash_subagent for: mkdir, touch, rm, cp, mv, chmod, chown, echo > (redirect), tee, git add, git commit, npm install, pip install, or any filesystem modification.
NEVER: write_file, edit_file, mkdir, rm, cp, mv, chmod, git add, git commit, or any filesystem write.

CRITICAL: Your final message MUST contain a non-empty summary. Never end with a silent response — even if you only ran searches, describe what you found.

OUTPUT RULES:
- You have a limited turn budget (~25 tool calls). Complete your task efficiently — prioritize precision over breadth.
- Be concise, but not at the expense of correctness. Include details when they matter.
- Do NOT echo back file contents verbatim — reference paths and line numbers instead. Short code snippets that ARE the answer are fine.
- Aim for under 200 words unless the findings genuinely demand more detail.
- No conversational filler, no "let me summarize", no meta-commentary.
- Preferred format (adapt as needed):
  Scope: <one sentence>
  Findings: <key facts or answers>
  Key files: <paths, line ranges>
  Issues: <only if something is wrong>
- When investigating bugs: report the call chain (function → function with file:line) and the conditions under which the bug triggers. Do NOT fix the bug — only report findings.`
}

func verificationSystemPrompt() string {
	return coldAgentPreamble + `You are a verification specialist. Your job is NOT to confirm the implementation works —
it's to try to BREAK it.

However, you MAY create ephemeral test scripts in /tmp via bash_subagent when inline commands
aren't sufficient (e.g., a multi-step test harness). Clean up ONLY the specific files you created in /tmp when done. Do NOT delete directories or files you did not create.

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
	return coldAgentPreamble + `You are an independent evaluation agent. Your role is to assess correctness, quality, and security —
not to implement changes.

You MAY create ephemeral test scripts in /tmp via bash_subagent when you need to test behavior.
Clean up ONLY the specific files you created in /tmp when done. Do NOT delete directories or files you did not create.

Approach:
- Read the relevant code thoroughly before forming an opinion
- If a test suite exists, run it — but don't trust it blindly (the implementer is an LLM too)
- Think about edge cases, error paths, race conditions, and security implications
- Distinguish between "this is wrong" (must fix) and "this could be improved" (nice to have)

OUTPUT RULES:
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

// ---------------------------------------------------------------------------
// Execute
// ---------------------------------------------------------------------------

func (a *AgentTool) Execute(ctx context.Context, p AgentParams) (*tool.ToolResult, error) {
	// Normalize subagent type: lowercase to tolerate LLM-generated casing
	// (e.g. "Explore", "EVALUATE" all map correctly).
	p.SubagentType = strings.ToLower(strings.TrimSpace(p.SubagentType))

	if p.SubagentType == "" {
		// Guard against recursive forking: detect fork-boilerplate tag in parent
		// message history and reject the fork attempt at call time.
		if parentRaw := agentloop.ParentMessagesFromContext(ctx); parentRaw != nil {
			if msgs, ok := parentRaw.([]llm.Message); ok && isInForkChild(msgs) {
				slog.Warn("recursive fork blocked")
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
	return a.executeCold(ctx, p)
}

// ---------------------------------------------------------------------------
// Fork execution
// ---------------------------------------------------------------------------

func (a *AgentTool) executeFork(ctx context.Context, p AgentParams) (*tool.ToolResult, error) {
	cb := agentloop.EventCallbackFromContext(ctx)

	// 模型安全兜底:空/"pro"→主模型,"flash"→子模型,其他→主模型
	model := a.resolveModel(p.Model)

	// 从 context 获取父消息历史。buildForkMessages 做"前缀零改写 + 尾部闭合
	// 截断",截断后剩余恰好 = 父上一请求负载 P_k(已发送、已缓存)。
	parentRaw := agentloop.ParentMessagesFromContext(ctx)
	messages := buildForkMessages(parentRaw, p.Description, p.Prompt)
	a.mu.RLock()
	client := a.LLMClient
	a.mu.RUnlock()

	registry := a.buildForkRegistry()
	// fork 首请求携带与父完全一致的 tools 数组(DeepSeek 前缀缓存包含 tools
	// schema,不一致则继承前缀在请求头部即分叉,命中归零)。
	parentTools := agentloop.ParentToolsFromContext(ctx)
	var reqTools []llm.ToolSpec
	if len(parentTools) > 0 {
		reqTools = append([]llm.ToolSpec(nil), parentTools...)
		// 请求侧 tools 被整体替换为父 schema 后,父独占工具(bash/agent/todo/
		// MCP 等)对 fork 模型可见但未注册 → loop 会静默剥离其调用,模型无
		// 反馈空转。补齐注册表:bash 别名到 fork 的沙箱 shell(执行语义一致),
		// 其余注册显式报错 stub。
		alignForkRegistry(registry, parentTools)
	} else {
		// 无父 tools 注入(单测/兼容路径)→ 退化为子代理自身 schema
		for _, s := range registry.List() {
			reqTools = append(reqTools, llm.ToolSpec{
				Name:        s.Name,
				Description: s.Description,
				Parameters:  s.Parameters,
			})
		}
	}
	// 预算兜底:继承前缀 + 指令 ≤ 窗口 90%(另扣 tools schema 与输出余量),
	// 超限沿完整轮次边界从尾部截断;截到保底仍超(或预算被 tools 占满)则
	// 拒绝发起必败请求(实测 fork 请求 1.44M > 模型上限 1,048,565 → HTTP 400)。
	compactor := a.newCompactor()
	window := compaction.DefaultContextLimit
	if tc, ok := compactor.(interface{ ContextLimit() int }); ok {
		window = tc.ContextLimit()
	}
	budget := int(float64(window)*forkContextBudgetRatio) - estimateToolSpecsTokens(reqTools) - 4096
	if budget <= 0 {
		return forkContextExceededResult()
	}
	if trimmed, ok := trimForkContextToBudget(messages, budget, forkMinKeepMessages); !ok {
		return forkContextExceededResult()
	} else {
		messages = trimmed
	}

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	subLoop := agentloop.New(client, registry, agentloop.Config{
		MaxSteps:      forkMaxSteps,
		SystemPrompt:  "", // messages already contain system prompt
		Guard:         a.subGuard(),
		SandboxMgr:    a.SandboxMgr,
		UserResponder: nil,
		ToolTimeout:   agentloop.DefaultToolTimeout,
		Model:         model,
		TodoState:     nil,
		Compactor:     compactor,
		ToolsOverride: reqTools,
	})

	startTime := time.Now()
	toolCallID := agentloop.ToolCallIDFromContext(ctx)

	agentType := "fork"

	if cb != nil {
		cb(SubagentStart{Prompt: p.Description, AgentType: agentType, InheritCtx: true, ToolCallID: toolCallID, Model: model})
	}

	lastStepText, totalSteps, promptTok, complTok, cacheHitTok, cacheMissTok, events, err := forwardEvents(subCtx, subLoop.Run(subCtx, messages), cb, toolCallID)
	if err != nil {
		if cb != nil {
			cb(SubagentEnd{ToolCallID: toolCallID, Model: model, DurationMs: time.Since(startTime).Milliseconds(), Error: err.Error()})
		}
		return &tool.ToolResult{
			Content: fmt.Sprintf("Fork subagent failed: %s", err),
			Meta:    tool.ToolMeta{Duration: time.Since(startTime)},
		}, nil
	}

	// Phase 2: Layer 3 事后分类器
	classified := classify(events, a.WorkspaceDir)

	if cb != nil {
		cb(SubagentEnd{ToolCallID: toolCallID, Model: model, TotalSteps: totalSteps, PromptTokens: promptTok, CompletionTokens: complTok, CacheHitTokens: cacheHitTok, CacheMissTokens: cacheMissTok, DurationMs: time.Since(startTime).Milliseconds()})
	}
	// 持久化 subagent JSONL
	a.saveSubagentTranscript(toolCallID, agentType, p.Description, model, totalSteps, promptTok, complTok, cacheHitTok, cacheMissTok, events)

	return &tool.ToolResult{
		Content: fmt.Sprintf("(fork subagent completed, %d steps, %d+%d tokens)\n\n%s%s", totalSteps, promptTok, complTok, lastStepText, formatFindings(classified)),
		Meta:    tool.ToolMeta{Duration: time.Since(startTime)},
	}, nil
}

// ---------------------------------------------------------------------------
// Cold execution
// ---------------------------------------------------------------------------

func (a *AgentTool) executeCold(ctx context.Context, p AgentParams) (*tool.ToolResult, error) {
	cb := agentloop.EventCallbackFromContext(ctx)

	// 模型锁定:每种子代理类型绑定固定模型,忽略 LLM 传入的 model 参数。
	// 防止 LLM 误传 model 导致审查/验证质量降级或搜索成本不必要升高。
	switch p.SubagentType {
	case "explore":
		// Explore 始终锁定 flash 模型(快速搜索,忽略 LLM 传入的 model)
		p.Model = "flash"
	case "evaluate", "verification":
		// 始终锁定主模型(推理质量优先)
		p.Model = "pro"
	}
	model := a.resolveModel(p.Model)

	sp, extraDisallowed := agentConfig(p.SubagentType)
	subRegistry := a.buildColdRegistry(extraDisallowed)

	// Build tailored environment section: only include OS/Shell/CWD, not the full
	// system tool list. The subagent's own tool registry defines what it can use;
	// listing unavailable tools wastes prompt tokens and misleads the LLM.
	sp += formatSubagentEnvironment(ctx, subRegistry)

	// 注入工具使用指南(ToolWithPrompt.Prompt() → C1)。
	// 按需组装 — 仅已注册且实现了 ToolWithPrompt 的工具会贡献内容。
	if toolPrompts := subRegistry.FormatToolPrompts(); toolPrompts != "" {
		sp += "\n\n" + toolPrompts
	}

	// All cold agents are read-only on project files — they don't need AGENTS.md
	// coding standards. Dropping it saves prompt tokens.
	maxSteps := coldMaxSteps
	if p.SubagentType == "explore" {
		maxSteps = exploreMaxSteps // 搜索任务更快完成
	}

	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	a.mu.RLock()
	client := a.LLMClient
	a.mu.RUnlock()
	subLoop := agentloop.New(client, subRegistry, agentloop.Config{
		MaxSteps:      maxSteps,
		SystemPrompt:  sp,
		Guard:         a.subGuard(),
		SandboxMgr:    a.SandboxMgr,
		UserResponder: nil,
		ToolTimeout:   agentloop.DefaultToolTimeout,
		Model:         model,
		TodoState:     nil,
		Compactor:     a.newCompactor(),
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

	lastStepText, totalSteps, promptTok, complTok, cacheHitTok, cacheMissTok, events, err := forwardEvents(subCtx, subLoop.Run(subCtx, messages), cb, toolCallID)
	if err != nil {
		if cb != nil {
			cb(SubagentEnd{ToolCallID: toolCallID, Model: model, DurationMs: time.Since(startTime).Milliseconds(), Error: err.Error()})
		}
		return &tool.ToolResult{
			Content: fmt.Sprintf("Subagent [%s] failed: %s", p.SubagentType, err),
			Meta:    tool.ToolMeta{Duration: time.Since(startTime)},
		}, nil
	}

	// Phase 2: Layer 3 事后分类器
	classified := classify(events, a.WorkspaceDir)

	if cb != nil {
		cb(SubagentEnd{ToolCallID: toolCallID, Model: model, TotalSteps: totalSteps, PromptTokens: promptTok, CompletionTokens: complTok, CacheHitTokens: cacheHitTok, CacheMissTokens: cacheMissTok, DurationMs: time.Since(startTime).Milliseconds()})
	}
	// 持久化 subagent JSONL
	a.saveSubagentTranscript(toolCallID, p.SubagentType, p.Description, model, totalSteps, promptTok, complTok, cacheHitTok, cacheMissTok, events)

	return &tool.ToolResult{
		Content: fmt.Sprintf("(subagent [%s] completed, %d steps, %d+%d tokens)\n\n%s%s", p.SubagentType, totalSteps, promptTok, complTok, lastStepText, formatFindings(classified)),
		Meta:    tool.ToolMeta{Duration: time.Since(startTime)},
	}, nil
}

// subGuard 构造子代理的权限 Guard:
// - 继承父 Guard 的规则(deny/ask/allow + session 记忆)——三审 High-1:
//   此前每次新建空规则 Guard,用户 deny 规则对子代理完全失效,
//   叠加 autoAllow = 无沙箱逃逸命令三重裸奔,违反"子能力是父的子集"
// - 沙箱可用 → autoAllow 二元决策(子代理无 responder + 沙箱兜底,不产生 ASK)
// - 沙箱不可用 → 维持原 bypass 语义(子代理委托信任父级)
func (a *AgentTool) subGuard() permission.Guard {
	var opts []permission.GuardOption
	if a.Guard != nil {
		if entries := a.Guard.ListRules(); len(entries) > 0 {
			opts = append(opts, permission.WithRules(entries))
		}
	}
	g := permission.NewGuard(opts...)
	if a.SandboxMgr != nil && a.SandboxMgr.Available() {
		g.EnableAutoAllow()
	} else {
		g.EnableBypass()
	}
	return g
}

// ---------------------------------------------------------------------------
// registry builders
// ---------------------------------------------------------------------------

func (a *AgentTool) buildForkRegistry() tool.Registry {
	r := tool.NewRegistry()
	for _, t := range a.allTools() {
		if !allAgentDisallowed[t.Name()] {
			r.Register(t)
		}
	}
	return r
}

func (a *AgentTool) buildColdRegistry(extraDisallowed map[string]bool) tool.Registry {
	r := tool.NewRegistry()
	for _, t := range a.allTools() {
		name := t.Name()
		if allAgentDisallowed[name] || extraDisallowed[name] {
			continue
		}
		r.Register(t)
	}
	return r
}

// alignForkRegistry 补齐 fork 注册表与父 tools(ToolsOverride 广告集)的差集:
// fork 首请求的 tools 数组被整体替换为父 schema(前缀缓存对齐),父独占工具
// 因此对 fork 模型可见但未注册——loop 对未注册工具的调用是静默剥离,模型
// 得不到任何反馈,任务空转。补齐后:
//   - "bash":父请求广告的是主循环 AllowBg=true 实例,fork 只有同 schema 的
//     "bash_subagent"(AllowBg=false、沙箱)。fork 模型看不见 bash_subagent
//     (请求 tools 被整体替换),调用 bash 会被剥离 → 别名注册到 fork 沙箱
//     shell 实例,名字不同但执行语义一致。
//   - 其余父独占工具(agent / todo_* / ask_user_question / plan mode /
//     kill_background_task / MCP 工具…):注册 unavailable stub,调用返回
//     明确的可恢复错误并给出替代指引。
func alignForkRegistry(reg tool.Registry, parentTools []llm.ToolSpec) {
	shell, _ := reg.Get("bash_subagent")
	for _, s := range parentTools {
		if _, ok := reg.Get(s.Name); ok {
			continue
		}
		if s.Name == "bash" && shell != nil {
			reg.Register(&forkToolAlias{name: "bash", inner: shell})
			continue
		}
		reg.Register(&forkUnavailableTool{spec: s})
	}
}

// forkToolAlias 以指定名字委托底层工具执行(fork 中 "bash" → 沙箱 shell)。
type forkToolAlias struct {
	name  string
	inner tool.Tool
}

func (t *forkToolAlias) Name() string {
	return t.name
}
func (t *forkToolAlias) Description() string {
	return t.inner.Description()
}
func (t *forkToolAlias) Schema() json.RawMessage {
	return t.inner.Schema()
}
func (t *forkToolAlias) ConcurrentSafe() bool {
	return t.inner.ConcurrentSafe()
}
func (t *forkToolAlias) Execute(ctx context.Context, raw json.RawMessage) (*tool.ToolResult, error) {
	return t.inner.Execute(ctx, raw)
}
func (t *forkToolAlias) SupportsStreaming() bool {
	if s, ok := t.inner.(tool.StreamableTool); ok {
		return s.SupportsStreaming()
	}
	return false
}
func (t *forkToolAlias) ExecuteStreaming(ctx context.Context, raw json.RawMessage, chunkCb func(string)) (*tool.ToolResult, error) {
	if s, ok := t.inner.(tool.StreamableTool); ok {
		return s.ExecuteStreaming(ctx, raw, chunkCb)
	}
	return t.inner.Execute(ctx, raw)
}

// forkUnavailableTool 是父独占工具在 fork 注册表中的占位:调用返回明确的
// 可恢复错误与替代指引,替代 loop 层对未注册工具的静默剥离。
type forkUnavailableTool struct {
	spec llm.ToolSpec
}

func (t *forkUnavailableTool) Name() string {
	return t.spec.Name
}
func (t *forkUnavailableTool) Description() string {
	return t.spec.Description
}
func (t *forkUnavailableTool) Schema() json.RawMessage {
	switch p := t.spec.Parameters.(type) {
	case json.RawMessage:
		return p
	case string:
		return json.RawMessage(p)
	default:
		if b, err := json.Marshal(p); err == nil {
			return b
		}
		return nil
	}
}
func (t *forkUnavailableTool) ConcurrentSafe() bool {
	return false
}
func (t *forkUnavailableTool) Execute(ctx context.Context, raw json.RawMessage) (*tool.ToolResult, error) {
	return &tool.ToolResult{
		Content: fmt.Sprintf("Error: 工具 %q 仅存在于父会话,fork 子代理不可调用。请改用可用工具(read / edit / write / web_fetch / web_search / bash / bash_subagent)。", t.spec.Name),
		Error: &tool.ToolError{
			Class:   tool.ErrorClassRecoverable,
			Kind:    "tool_unavailable_in_fork",
			Message: fmt.Sprintf("tool %q is only available in the parent session", t.spec.Name),
		},
	}, nil
}

func (a *AgentTool) allTools() []tool.Tool {
	return []tool.Tool{
		tool.Wrap(&tool.ReadFile{}),
		tool.Wrap(&tool.EditFile{}),
		tool.Wrap(&tool.WriteFile{}),
		tool.Wrap(&tool.WebFetch{}),
		tool.Wrap(&tool.WebSearch{}),
		tool.Wrap(&tool.Shell{AllowBg: false, SandboxMgr: a.SandboxMgr}), // bash_subagent 同样进沙箱
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
	// todo_create / todo_update 由父 agent loop 独占管理,子代理 TodoState 为 nil,
	// 其 Prompt 也因工具被禁止而不会注入子代理上下文。
	"todo_create":          true,
	"todo_update":          true,
}
var coldDisallowed = map[string]bool{
	"write_file": true,
	"write":      true,
	"edit_file":  true,
	"edit":       true,
}

func agentConfig(agentType string) (systemPrompt string, extraDisallowed map[string]bool) {
	switch agentType {
	case "explore":
		return exploreSystemPrompt(), coldDisallowed
	case "evaluate":
		return evaluateSystemPrompt(), coldDisallowed
	case "verification":
		return verificationSystemPrompt(), coldDisallowed
	default:
		// Unknown type: fall back to evaluate (safe default, read-only).
		// This path is reachable if the schema adds a new type before the
		// code is deployed — not a silent data-loss risk.
		slog.Warn("unknown subagent type, falling back to evaluate", "type", agentType)
		return evaluateSystemPrompt(), coldDisallowed
	}
}

// formatSubagentEnvironment 为冷启动子 agent 构建精简的 ## Environment 节。
//
// 与父 agent 的完整工具列表不同,子 agent 只需要:
//   - OS / Shell 信息(来自父 system prompt)
//   - 自身 registry 中的工具列表(父 prompt 的工具列表对子 agent 无效且误导)
//
// 这避免了向 Explore agent 列出 cargo、docker 等无法直接使用的工具。
//
// 依赖:解析父 prompt 的 "## Workspace" 和 "## Environment" 节。
// 如果 defaultSystemPrompt 的格式变更,对应的轮询测试
// TestSubagentEnvironment_RoundTrip(agent_test.go)必须同步更新。
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

	// 从父 system prompt 提取 Workspace(CWD)信息
	// 先尝试精确匹配 "## Workspace",再尝试松散匹配 "Workspace" 节
	wsStart := findSectionStart(parentSP, "## Workspace", "Workspace")
	if wsStart >= 0 {
		// 找下一个同级别节作为结束边界
		wsEnd := findNextSection(parentSP, wsStart)
		wsSection := parentSP[wsStart:wsEnd]
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

// findSectionStart 按优先级搜索节标题。主格式优先,辅助格式兜底。
func findSectionStart(s string, primary, fallback string) int {
	if idx := strings.Index(s, primary); idx >= 0 {
		return idx
	}
	// 辅助格式:搜索所有包含 fallback 的行(不局限 ## {title}),
	// 在 system prompt 结构稳定时兼顾灵活性。
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, fallback) && strings.HasPrefix(trimmed, "#") {
			return strings.Index(s, line)
		}
	}
	return -1
}

// findNextSection 从 pos 向后找到下一个 markdown 节(## / ### 开头)。
// 找不到时返回末尾,确保截取不越界。
func findNextSection(s string, pos int) int {
	rest := s[pos+1:] // 跳过当前节本身
	for _, line := range strings.Split(rest, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ") {
			return pos + 1 + strings.Index(rest, line)
		}
	}
	return len(s)
}

// truncateTo 截断字符串到 maxLen 字符,超出部分用 "..." 替代。
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

func forwardEvents(ctx context.Context, subCh <-chan agentloop.StepEvent, cb func(agentloop.StepEvent), toolCallID string) (lastStepText string, totalSteps int, promptTokens int, completionTokens int, cacheHitTokens int, cacheMissTokens int, events []SubagentEvent, finalErr error) {
	var sb strings.Builder
	var writeOps []writeOp
	var currentStep int
	var lastToolCalls []string // 最后一个 step 的工具调用名列表(用于空文本兜底)

	// 缓冲扇出通道:解耦 subagent 事件消费与 TUI 投递。
	fanout := make(chan agentloop.StepEvent, 16384)
	fanoutDone := make(chan struct{})
	go func() {
		defer close(fanoutDone)
		for ev := range fanout {
			if cb != nil {
				cb(ev)
			}
		}
	}()

	// defer 在函数返回前关闭 fanout 并等待所有事件投递完成,
	// 确保 SubagentEnd 之前的全部 SubagentEvent 已被 TUI 消费。
	defer func() {
		close(fanout)
		<-fanoutDone
	}()

	for ev := range subCh {
		switch e := ev.(type) {
		case agentloop.StreamDelta:
			if e.Step > currentStep {
				// 进入新 step:只保留最后一个 step 的文本,丢弃中间推理过程
				currentStep = e.Step
				sb.Reset()
				lastToolCalls = lastToolCalls[:0]
			}
			// Phase 2: 转发思考过程(dimmed 渲染)
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
			if e.ToolCallName == "write" || e.ToolCallName == "edit" {
				op := writeOp{ToolName: e.ToolCallName, FilePath: extractPath(e.Result), BytesIn: len(e.Result)}
				if e.ToolCallName == "edit" {
					op.LinesAdd, op.LinesDel = countDiff(e.Result)
				}
				writeOps = append(writeOps, op)
			}

		case agentloop.StepStats:
			promptTokens += e.PromptTokens
			completionTokens += e.CompletionTokens
			cacheHitTokens += e.CacheHitTokens
			cacheMissTokens += e.CacheMissTokens

		case agentloop.TurnDone:
			totalSteps = e.Step
			if e.Err != nil {
				finalErr = e.Err
			}
			// 兜底:子 agent 最后一个 step 无文本输出时,
			// 合成非空 fallback 防止 tool_result 内容为空,避免父 agent 因空结果而误解。
			ensureNonEmpty(&sb, lastToolCalls)
			if len(writeOps) > 0 {
				sb.WriteString("\n\n<subagent_write_operations>\n")
				for _, op := range writeOps {
					switch op.ToolName {
					case "write":
						fmt.Fprintf(&sb, "- %s: %s (%s)\n", op.ToolName, op.FilePath, fmtBytes(op.BytesIn))
					case "edit":
						fmt.Fprintf(&sb, "- %s: %s (+%d -%d lines)\n", op.ToolName, op.FilePath, op.LinesAdd, op.LinesDel)
					}
				}
				sb.WriteString("</subagent_write_operations>")
			}
		return sb.String(), totalSteps, promptTokens, completionTokens, cacheHitTokens, cacheMissTokens, events, finalErr
		}
	}
	// Channel 关闭但未收到 LoopDone(跨包防御:当前 agentloop.Run 总是会发送 LoopDone,
	// 但此处做兜底防止未来引入的不发送 LoopDone 的路径导致空文本传播)。
	ensureNonEmpty(&sb, lastToolCalls)
	return sb.String(), totalSteps, promptTokens, completionTokens, cacheHitTokens, cacheMissTokens, events, nil
}

// ensureNonEmpty 在 sb 为空时合成非空 fallback 文本。
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
	case "read", "write", "edit":
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
// 策略:
//  1. 前缀零改写:保留父消息原样(含 tool 结果),不做中段删除/改写——
//     前缀缓存要求请求 token 序列与父已缓存请求从头连续一致,任何中段
//     "打洞"(删除 tool 消息、剥离 tool_calls)都会使打洞点之后的命中全部丢失。
//  2. 尾部闭合截断:从最后一条含 tool_calls 的 assistant 处整体丢弃(它正是
//     触发 fork 的那条,含 agent tool_call;其后的兄弟工具结果一并截掉)。
//     fork 在工具执行阶段快照父消息,state.Messages = 父上一请求负载 P_k
//     (已发送、已缓存)+ 该 assistant,截断后剩余恰好 == P_k → 首请求
//     前缀与父缓存线一致,命中 ≈ P_k 全长。
//  3. 追加一条 user 消息,包含 <fork-boilerplate> 身份注入 + 任务指令
//
// 结果:[...P_k 原样, user(<fork-boilerplate> + task directive)]
//
// 若父消息不存在则创建新的干净消息(兜底)。
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
	// 1. 尾部闭合截断:从最后一条含 tool_calls 的 assistant 处整体丢弃。
	//    fork 由该 assistant 发起(agent tool_call),截断后前缀 == 父上一请求
	//    负载 P_k。若父历史已被压缩改写(无 assistant 含 tool_calls),则原样保留。
	filtered := msgs
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant && len(msgs[i].ToolCalls) > 0 {
			filtered = msgs[:i]
			break
		}
	}

	// 2. 防御层(627e50c 回归保护):异常历史(如压缩摘要拼接/外部注入)可能残留
	//    两类协议孤儿——① 孤儿轮次:assistant 声明了 tool_calls 但其结果已不在
	//    历史中;② 悬空 tool 段:tool 结果没有前导 assistant 声明。配对轮次
	//    (后继连续 tool 段完整覆盖全部声明)原样保留,保证前缀零改写;仅异常
	//    单元被清理:有文本的孤儿 assistant 保留文本但剥离 ToolCalls(其 tool
	//    段跳过,否则引用已剥离声明、协议违规);纯 tool_calls 孤儿 assistant
	//    连同其 tool 段整轮删除;悬空 tool 段整段删除。
	cleaned := make([]llm.Message, 0, len(filtered))
	for i := 0; i < len(filtered); i++ {
		m := filtered[i]
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0 {
			// 收集后继连续 tool 消息段,核对声明是否全部有结果返回
			ids := make(map[string]struct{}, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				ids[tc.ID] = struct{}{}
			}
			j := i + 1
			for ; j < len(filtered) && filtered[j].Role == llm.RoleTool; j++ {
				if _, ok := ids[filtered[j].ToolCallID]; ok {
					delete(ids, filtered[j].ToolCallID)
				}
			}
			if len(ids) > 0 {
				// 孤儿轮次(结果被压缩/拼接丢弃):清理并跳过其 tool 段
				if m.Content != "" {
					m.ToolCalls = nil
					cleaned = append(cleaned, m)
				}
				i = j - 1
				continue
			}
			// 配对轮次:原样保留(零改写),其 tool 段随后逐条正常通过
		} else if m.Role == llm.RoleTool && (i == 0 || filtered[i-1].Role != llm.RoleTool) {
			// 连续 tool 段的段首:前一条必须是有 tool_calls 声明的 assistant
			// (配对轮次的结果)。前导缺失(无消息 / user / system / 纯文本
			// assistant)→ 悬空 tool 段,整段跳过。
			if i == 0 || filtered[i-1].Role != llm.RoleAssistant || len(filtered[i-1].ToolCalls) == 0 {
				j := i + 1
				for ; j < len(filtered) && filtered[j].Role == llm.RoleTool; j++ {
				}
				i = j - 1
				continue
			}
		}
		cleaned = append(cleaned, m)
	}
	// 输出写入独立 backing,不复用输入切片:原地过滤会篡改父消息历史
	// (state.Messages 共享 backing),破坏父循环后续请求的协议合法性。
	filtered = cleaned

	if len(filtered) == 0 {
		// 异常历史(如首条即含 tool_calls 的 assistant)截断/清理后为空:
		// 回退到干净消息,避免产生无 system 引导的孤儿 directive 请求。
		return []llm.Message{
			{Role: llm.RoleSystem, Content: "You are a coding agent. Complete the task using the tools available to you."},
			{Role: llm.RoleUser, Content: buildForkDirective(description, prompt)},
		}
	}

	// 3. 追加 fork 身份注入 + 任务指令
	filtered = append(filtered, llm.Message{
		Role:    llm.RoleUser,
		Content: buildForkDirective(description, prompt),
	})
	return filtered
}

// forkContextBudgetRatio 是继承前缀占子代理模型窗口的比例上限(0.9 = 90%)。
// 超限部分由 trimForkContextToBudget 沿完整轮次边界从尾部截断,防止 fork
// 首请求超出模型上下文窗口被 provider 400 拒绝(实测:请求 1.44M > 1,048,565)。
const forkContextBudgetRatio = 0.9

// forkMinKeepMessages 是预算截断后允许保留的最少消息数(不足则判定为
// "上下文过大,不适合 fork",由调用方引导改用 cold/explore)。
const forkMinKeepMessages = 3

// forkContextExceededResult 构造 fork 上下文超窗的拒绝结果(可恢复:模型可
// 先压缩主上下文再重试,或改走 explore 冷启动)。
func forkContextExceededResult() (*tool.ToolResult, error) {
	return &tool.ToolResult{
		Content: "Error: fork 继承上下文超出模型窗口预算,已放弃发起请求。任务需要完整历史时请改用 explore(冷启动轻量搜索);需要大上下文执行时请先在主循环压缩上下文再 fork,或分小步委派。",
		Error: &tool.ToolError{
			Class:   tool.ErrorClassRecoverable,
			Kind:    "context_window_exceeded",
			Message: "fork inherited context exceeds model window budget",
		},
	}, nil
}

// estimateMessagesTokens 粗估消息列表的 token 数(护栏用途,宁可高估不低估:
// 低估会让超窗请求漏网,高估只会让截断略早发生)。
func estimateMessagesTokens(msgs []llm.Message) int {
	total := 0
	for _, m := range msgs {
		total += 4 // role/头部开销
		total += len(m.Content)/2 + len(m.ReasoningContent)/2
		for _, tc := range m.ToolCalls {
			total += len(tc.Name)/2 + len(tc.Arguments)/2 + 2
		}
	}
	return total
}

// estimateToolSpecsTokens 粗估工具 schema 列表的 token 数(请求头部)。
func estimateToolSpecsTokens(specs []llm.ToolSpec) int {
	total := 0
	for _, s := range specs {
		total += len(s.Name)/2 + len(s.Description)/3 + 24
		switch p := s.Parameters.(type) {
		case json.RawMessage:
			total += len(p) / 3
		case string:
			total += len(p) / 3
		default:
			if b, err := json.Marshal(s.Parameters); err == nil {
				total += len(b) / 3
			}
		}
	}
	return total
}

// forkRoundStart 返回 msgs[:idx] 中最后一个完整轮次的起点索引。
// 轮次 = user 单条 / 纯文本 assistant / assistant(tool_calls) + 其 tool 结果组。
// 起点满足:截断到该点后,前缀消息序列保持合法(不存在孤儿 tool_calls)。
func forkRoundStart(msgs []llm.Message, idx int) int {
	i := idx - 1
	if i < 0 {
		return 0
	}
	switch msgs[i].Role {
	case llm.RoleTool:
		// 回溯到该批 tool 结果所属 assistant(tool_calls)的起点;
		// 找不到配对(异常历史)→ 保守从首个 tool 消息处截。
		j := i
		for j > 0 && msgs[j].Role == llm.RoleTool {
			j--
		}
		if msgs[j].Role == llm.RoleAssistant && len(msgs[j].ToolCalls) > 0 {
			return j
		}
		return j + 1
	default:
		// user / assistant(含纯文本)/ system
		return i
	}
}

// trimForkContextToBudget 从尾部按完整轮次截断,使继承前缀 ≤ budgetTokens。
// 截断只沿轮次边界进行,不拆分配对、不改动前缀头部 → 截断后的前缀仍是某次
// 父请求负载,缓存友好。若截到 minKeep 仍超预算,返回 false 表示不适合 fork。
func trimForkContextToBudget(msgs []llm.Message, budgetTokens, minKeep int) ([]llm.Message, bool) {
	if estimateMessagesTokens(msgs) <= budgetTokens {
		return msgs, true
	}
	if len(msgs) <= minKeep {
		// 已到保底条数仍超预算 → 不适合 fork(原先短路在估算前,少数巨型
		// 消息可绕过护栏,超窗请求直接发送 → provider 400)。
		return msgs, false
	}
	idx := len(msgs)
	for idx > minKeep {
		start := forkRoundStart(msgs, idx)
		if start >= idx {
			// 无法继续整轮截断(防御,理论不可达)
			break
		}
		if estimateMessagesTokens(msgs[:start]) <= budgetTokens {
			return msgs[:start], true
		}
		idx = start
	}
	return msgs[:minKeep], false
}

// findLastAssistant 返回消息列表中最后一条 assistant 消息的指针,nil 表示不存在。
func findLastAssistant(msgs []llm.Message) *llm.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant {
			return &msgs[i]
		}
	}
	return nil
}

// buildForkDirective 构造 fork 子 agent 的身份注入提示词。
func buildForkDirective(description, prompt string) string {
	return fmt.Sprintf(`<%s>
You are a fork child process. The message history above is inherited from your parent —
understand the context, then execute the task below.

Rules:
1. Your final message MUST contain a non-empty summary of what you did. Never end with a silent/empty response — even if all work was done via tool calls, summarize the outcome.
2. Output is expensive — keep responses concise. Aim for under 300 words unless findings genuinely demand more detail.
3. Do NOT call the agent tool (you ARE the fork — execute directly)
4. Tool availability: the advertised tool schemas mirror the parent session (prefix-cache alignment), but only read / edit / write / web_fetch / web_search / bash / bash_subagent are executable here. Parent-only tools (agent, todo_create, todo_update, ask_user_question, enter_plan_mode, exit_plan_mode, kill_background_task, MCP tools, ...) return an explicit error if called — never call them.
5. You have unrestricted tool access (no permission prompts). No need to ask for confirmation before writes.
6. No conversation, no questions, no commentary. Use tools silently, report once at the end.
7. Stay within the task scope. Related observations outside scope deserve at most one sentence.
8. Preferred format (English labels; adapt as needed):

Scope: <one sentence echoing the task>
Result: <findings or work done — details when they matter>
Key files: <paths, line ranges>
Files changed: <paths, only if modified>
Issues: <only if something is wrong>

Task: %s
%s</%s>`, forkBoilerplateTag, description, prompt, forkBoilerplateTag)
}

// isInForkChild 检测消息历史中是否已包含 fork-boilerplate 标记,
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
