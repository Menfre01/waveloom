package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Menfre01/waveloom/pkg/acp"
	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/logging"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/sandbox"
	"github.com/Menfre01/waveloom/pkg/subagent"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// runACP 处理 waveloom acp 子命令,以 ACP Agent 模式运行。
func runACP(args []string) {
	fs := flag.NewFlagSet("acp", flag.ExitOnError)
	sessionDir := fs.String("session-dir", "", "ACP session 存储目录(默认 ~/.waveloom/acp-sessions)")
	settingsPath := fs.String("settings", "", "显式指定 settings.json 路径")
	model := fs.String("model", "", "LLM 模型名称")
	provider := fs.String("provider", "", "LLM Provider 名称")
	logLevel := fs.String("log-level", "info", "日志级别 (error/warn/info/debug)")
	contextLimit := fs.String("context-limit", "", "上下文窗口 token 上限(支持 1M/200k;默认读 settings 的 compaction.context_limit_tokens,再默认 1M)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: waveloom acp [options]

以 ACP (Agent Client Protocol) Agent 模式运行,通过 stdio 与 ACP Client 通信。

Options:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
参见 https://agentclientprotocol.com 了解 ACP 协议详情。
`)
	}

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "acp: %v\n", err)
		os.Exit(1)
	}

	// 初始化日志
	var level slog.Level
	switch *logLevel {
	case "error":
		level = slog.LevelError
	case "warn":
		level = slog.LevelWarn
	case "debug":
		level = slog.LevelDebug
	default:
		level = slog.LevelInfo
	}
	homeDir, _ := os.UserHomeDir()
	logCleanup := logging.Init(filepath.Join(homeDir, ".waveloom", "logs"), level)
	defer logCleanup()

	// 解析 settings 路径
	globalPath, projectPath := resolveSettingsPaths(*settingsPath)

	// 上下文窗口容量:--context-limit flag 优先(支持 1M/200k 格式);
	// 否则读 settings 的 compaction.context_limit_tokens(setup 向导写入);
	// 再默认 1M(与 TUI 对齐)。
	contextLimitVal := 0
	if *contextLimit != "" {
		var parseErr error
		contextLimitVal, parseErr = parseTokenLimit(*contextLimit)
		if parseErr != nil {
			slog.Warn("cannot parse --context-limit, falling back to settings", "value", *contextLimit, "err", parseErr)
		}
	}
	compactionConfig := resolveCompactionConfig(contextLimitVal, globalPath, projectPath)
	contextLimitFinal := compactionConfig.ContextLimit

	// 获取 CWD
	cwd, _ := os.Getwd()

	// 权限守门人:复用 createGuard(全局+项目权限规则)。ACP 是无交互入口
	// (v1 协议无权限确认通道),自动注入 autoAllowMode → Guard 进入二元决策
	// (仅 DENY/ALLOW,不产生 ASK)。deny 规则 / RiskHigh / PathDangerous 硬拦截
	// 在二元决策下保留(fail-closed 底线)。
	guard := createGuard(globalPath, projectPath)
	if impl, ok := guard.(*permission.GuardImpl); ok {
		impl.EnableAutoAllow()
	}

	// 沙箱管理器:ACP 无交互 → 自动激活(bypassPerm=true,即使 sandbox.enabled=false)。
	// 不可用 → 警告 + 降级运行(二元决策不受影响——规格书 2025-08 决策:
	// "bypass 即二元决策");failIfUnavailable:true 且后端不可用 → 拒绝启动。
	sandboxMgr, sandboxFatal := createSandboxManager(true, "", globalPath, projectPath, cwd)
	if sandboxFatal {
		// fatal 原因对 ACP 客户端用户必须可见(slog 只写日志文件)
		fmt.Fprintln(os.Stderr, "⚠ acp: sandbox required but unavailable (failIfUnavailable=true), refusing to start")
		slog.Error("acp: sandbox required but unavailable (failIfUnavailable=true), refusing to start")
		os.Exit(1)
	}

	// 加载 LLM Client
	llmClient, llmClientCfg, llmSettings, err := createLLMClient(globalPath, projectPath, *model, *provider, DetectLocale())
	if err != nil {
		slog.Error("acp: create LLM client", "err", err)
		os.Exit(1)
	}

	// Tier 3 摘要专用 Client(开启 JSON 模式,与 TUI 一致)
	summarizerClient := llmClient
	summaryCfg := llmClientCfg
	summaryCfg.ResponseFormat = "json_object"
	if sc, err := llm.NewClient(summaryCfg); err == nil {
		summarizerClient = sc
	}
	summarizer := compaction.NewCompactionSummarizer(summarizerClient, 0)

	// 构建 system prompt
	systemPrompt := buildSystemPrompt(cwd, DetectLocale())

	// 环境探测 + 注入
	// 注意:ACP 模式下不做完整的环境探测以避免阻塞 stdio 通道
	// probeResults := environment.RunProbesWithCache(context.Background(), environment.DefaultProbes())
	// systemPrompt += formatEnvironmentSection(probeResults, cwd, globalPath, projectPath)

	// 初始化 Tool Registry(注册所有内置工具)
	registry := tool.NewRegistry()
	settingsProvider := &fileSettingsProvider{projectPath: projectPath, globalPath: globalPath}

	modelName := llmSettings.Model
	subModel := llmSettings.SubModel
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	// 注册内置工具(注入沙箱与权限守门人;交互式工具在 ACP 下不可用,不注册)
	registerACPBuiltinTools(registry, llmClient, modelName, subModel, cwd, settingsProvider, sandboxMgr, guard, compactionConfig)

	// 解析 session 目录
	acpSessionDir := *sessionDir
	if acpSessionDir == "" {
		if homeDir != "" {
			acpSessionDir = filepath.Join(homeDir, ".waveloom", "acp-sessions")
		} else {
			acpSessionDir = filepath.Join(cwd, ".waveloom", "acp-sessions")
		}
	}

	// 沙箱状态启动日志:沙箱永不静默失效(规格书不变量 #1)
	if sandboxMgr != nil {
		slog.Info("acp: sandbox activated", "backend", sandboxMgr.Name())
	} else {
		// slog 默认只写日志文件,ACP 客户端用户无感知 → 必须 stderr 可见
		// (对齐 sandbox_setup.go 降级路径;二元决策不受影响,fail-closed 底线保留)
		fmt.Fprintf(os.Stderr, "⚠ acp: sandbox unavailable, running unsandboxed — auto-allow binary decision still active (deny rules / hard blocks remain)\n")
		slog.Warn("acp: sandbox unavailable, running unsandboxed — auto-allow binary decision still active (deny rules / hard blocks remain)")
	}

	// 创建 ACP Server
	server := acp.NewServer(acp.ServerConfig{
		LLMClient:    llmClient,
		ToolRegistry: registry,
		SystemPrompt: systemPrompt,
		BuildVersion: Version,
		CWD:          cwd,
		MaxTurns:     0, // ACP 模式下默认不限制 turn 数
		Guard:        guard,
		SandboxMgr:   sandboxMgr,
		SessionDir:   acpSessionDir,
		ContextLimit: contextLimitFinal,
		CompactionConfig: compactionConfig,
		Summarizer:       summarizer,
	})

	slog.Info("acp: starting server", "version", Version, "cwd", cwd, "sessionDir", acpSessionDir)

	if err := server.Run(); err != nil {
		slog.Error("acp: server error", "err", err)
		os.Exit(1)
	}
}

// resolveContextLimit 解析上下文窗口容量(整数形式):
// flagVal > 0 → flag 优先;否则读 settings;均未配置 → 默认 1M。
func resolveContextLimit(flagVal int, globalPath, projectPath string) int {
	return resolveCompactionConfig(flagVal, globalPath, projectPath).ContextLimit
}

// resolveCompactionConfig 解析完整压缩配置(单一实现,main.go 与 acp.go 共用):
// 默认配置 → global/project settings 的 compaction 段覆盖(setup 向导写入,
// project 优先)→ flag 显式指定时覆盖 ContextLimit。
func resolveCompactionConfig(flagVal int, globalPath, projectPath string) compaction.CompactionConfig {
	cfg := compaction.DefaultCompactionConfig()
	if cs := compaction.LoadCompactionSettings(globalPath); cs != nil {
		cs.ApplyToConfig(&cfg)
	}
	if cs := compaction.LoadCompactionSettings(projectPath); cs != nil {
		cs.ApplyToConfig(&cfg)
	}
	if flagVal > 0 {
		cfg.ContextLimit = flagVal
	}
	return cfg
}

// registerACPBuiltinTools 为 ACP 模式注册内置工具。
// 工具协议对齐:依赖 UserResponder 的交互式工具(ask_user_question /
// enter_plan_mode / exit_plan_mode)在 ACP(无交互,UserResponder=nil)下调用
// 必挂,不注册——从 schema 层杜绝 LLM 提议不可用工具。
func registerACPBuiltinTools(r tool.Registry, llmClient llm.Client, defaultModel, subModel string, cwd string, settings subagent.SettingsProvider, sandboxMgr *sandbox.SandboxManager, guard permission.Guard, compactionConfig compaction.CompactionConfig) *subagent.AgentTool {
	r.Register(tool.Wrap(&tool.ReadFile{}))
	r.Register(tool.Wrap(&tool.EditFile{}))
	r.Register(tool.Wrap(&tool.WriteFile{}))
	shellTool := &tool.Shell{AllowBg: true, SandboxMgr: sandboxMgr} // "bash"
	r.Register(tool.Wrap(shellTool))
	r.Register(tool.Wrap(&tool.WebFetch{}))
	r.Register(tool.Wrap(&tool.WebSearch{}))
	r.Register(tool.Wrap(&tool.KillBackgroundTask{}))

	at := &subagent.AgentTool{
		LLMClient:       llmClient,
		Settings:        settings,
		DefaultModel:    defaultModel,
		DefaultSubModel: subModel,
		WorkspaceDir:    cwd,
		SandboxMgr:      sandboxMgr,
		Guard:           guard,
		CompactionConfig: compactionConfig,
	}
	r.Register(tool.Wrap(at))
	r.Register(tool.Wrap(&tool.TodoCreate{}))
	r.Register(tool.Wrap(&tool.TodoUpdate{}))
	return at
}
