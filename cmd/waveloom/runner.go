package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	"github.com/Menfre01/waveloom/pkg/hook"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/lsp"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/mcp"
	"github.com/Menfre01/waveloom/pkg/reference"
	"github.com/Menfre01/waveloom/pkg/sandbox"
	"github.com/Menfre01/waveloom/pkg/session"
	"github.com/Menfre01/waveloom/pkg/subagent"
	"github.com/Menfre01/waveloom/pkg/todo"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// runOneShot 执行单次/管道模式（无 TUI，纯文本输出）。
func runOneShot(cfg CLIConfig, llmClient llm.Client, registry tool.Registry, guard permission.Guard, sandboxMgr *sandbox.SandboxManager, expander *reference.Expander, cwd string, cm *session.ContextManager, agentsMdText string, loc Locale, todoState *todo.TodoState, model string, hookRunner *hook.Runner, agentTool *subagent.AgentTool, mcpManager *mcp.Manager, lspManager *lsp.Manager) {
	lc := messagesFor(loc)
	// Context Manager 已管理 system prompt，Loop 无需重复注入
	loopCfg := agentloop.Config{
		MaxTurns:     cfg.MaxTurns,
		SystemPrompt: "",
		ToolTimeout:  cfg.ToolTimeout,
		AgentsMD:     agentsMdText,
		TodoState:    todoState,
		Model:        model,
		LSPManager:   lspManager,
	}

	// bypass 模式:无条件注入 autoAllow 二元决策(仅 DENY/ALLOW,不产生 ASK)。
	// --bypass-permissions 即"不要确认"的显式声明,与沙箱可用性无关;
	// 沙箱兜底是额外保障而非前置条件(用户决策:bypass 即二元决策)。
	if cfg.BypassPerm {
		if impl, ok := guard.(*permission.GuardImpl); ok {
			impl.EnableAutoAllow()
		}
	}
	loopCfg.Guard = guard
	// 注入沙箱管理器:agentloop 为每命令注入 per-command 沙箱状态,
	// Shell 工具据此决定是否 bwrap 包装
	loopCfg.SandboxMgr = sandboxMgr

	// 单次模式无 UserResponder，ask 降级为 deny
	loop := agentloop.New(llmClient, registry, loopCfg)
	if hookRunner != nil {
		loop.SetHookRunner(hookRunner)
		if sid := cm.SessionID(); sid != "" {
			hookRunner.SetSessionInfo(sid, session.TranscriptPath(filepath.Dir(cm.SessionPath()), sid))
		}
	}
	// AgentTool 不依赖 hookRunner
	if sid := cm.SessionID(); sid != "" {
		agentTool.SetSessionInfo(filepath.Dir(cm.SessionPath()), sid, session.BuildVersion)
	}
	// 构造用户输入（含管道数据）
	userInput := cfg.OneShot
	if isPiped() {
		stdin, err := readStdin()
		if err == nil && stdin != "" {
			userInput = fmt.Sprintf("%s\n\n---\n%s", stdin, cfg.OneShot)
		}
	}

	// 展开 @ 引用
	ctx := context.Background()
	expandedInput, _, expandErr := expander.Expand(ctx, userInput, cwd)
	if expandErr != nil {
		slog.Warn("@ reference expansion failed", "err", expandErr)
		expandedInput = userInput
	}
	// 查询 IDE 动态上下文(当前打开文件等),注入到 user 消息
	if mcpManager != nil {
		if dc := mcpManager.IDEContextProvider().QueryDynamicContext(ctx, cwd); dc != "" {
			expandedInput = "[IDECONTEXT]\n" + dc + "\n\n" + expandedInput
		}
	}

	messages, _ := cm.PrepareRun(expandedInput)

	ctx = context.Background()
	fmt.Fprintf(os.Stderr, lc.OneShotHeader, cwd)

	// Drain 事件 channel，取最终 LoopDone 事件 + 累计 token 统计
	startTime := time.Now()
	var finalEv agentloop.LoopDone
	var runPromptTokens, runComplTokens, runCacheHit, runCacheMiss, runReasoningTokens int
	var lastTurnPrompt int // 最后一个 TurnStats 的 PromptTokens（完整上下文）
	for ev := range loop.Run(ctx, messages) {
		switch e := ev.(type) {
		case agentloop.TurnStats:
			runPromptTokens += e.PromptTokens
			runComplTokens += e.CompletionTokens
			runCacheHit += e.CacheHitTokens
			runCacheMiss += e.CacheMissTokens
			runReasoningTokens += e.ReasoningTokens
			if e.PromptTokens > 0 {
				lastTurnPrompt = e.PromptTokens
			}
		case agentloop.LoopDone:
			finalEv = e
		}
	}

	elapsed := time.Since(startTime)

	if finalEv.Err != nil {
		fmt.Fprintf(os.Stderr, lc.OneShotError, humanizeError(finalEv.Err))
		os.Exit(1)
	}

	// 提交完整消息历史到 Context Manager（单次模式无 duration 统计，传 0）
	_ = cm.CompleteRun(finalEv.Messages, runPromptTokens, lastTurnPrompt, runComplTokens, runCacheHit, runCacheMiss, runReasoningTokens, cfg.Model, 0, string(finalEv.Reason))

	// 输出最后一条 assistant 消息
	for i := len(finalEv.Messages) - 1; i >= 0; i-- {
		if finalEv.Messages[i].Role == llm.RoleAssistant && finalEv.Messages[i].Content != "" {
			fmt.Println(finalEv.Messages[i].Content)
			break
		}
	}

	// footer：格式对齐 subagent 输出 (model, N轮, 2.3s, ↑12.5k, ↓3.2k)
	turnsText := fmt.Sprintf(lc.SubagentTurnsFmt, finalEv.Turn)
	fmt.Fprintf(os.Stderr, lc.OneShotFooter,
		model,
		turnsText,
		formatDuration(elapsed.Milliseconds()),
		formatTokens(runPromptTokens),
		formatTokens(runComplTokens),
	)
}

// isPiped 检查 stdin 是否为管道。
func isPiped() bool {
	stat, _ := os.Stdin.Stat()
	return (stat.Mode() & os.ModeCharDevice) == 0
}

// readStdin 读取 stdin 全部内容。
func readStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
