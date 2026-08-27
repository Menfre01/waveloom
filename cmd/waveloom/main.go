package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	"github.com/Menfre01/waveloom/pkg/hook"
	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/session"
	"github.com/Menfre01/waveloom/pkg/environment"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/logging"
	"github.com/Menfre01/waveloom/pkg/lsp"
	"github.com/Menfre01/waveloom/pkg/mcp"
	"github.com/Menfre01/waveloom/pkg/memory"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/reference"
	"github.com/Menfre01/waveloom/pkg/sandbox"
	"github.com/Menfre01/waveloom/pkg/shellutil"
	"github.com/Menfre01/waveloom/pkg/skill"
	"github.com/Menfre01/waveloom/pkg/subagent"
	"github.com/Menfre01/waveloom/pkg/todo"
	"github.com/Menfre01/waveloom/pkg/tool"
)

func main() {
	// 0. 注入构建版本号到 context 包（ldflags → session 文件兼容性检查）
	session.BuildVersion = Version

	// 1. 解析命令行参数
	cfg := parseCLI()
	if cfg.ShowVersion {
		fmt.Println(Version)
		return
	}
	if cfg.ShowHelp {
		printHelp(resolveLocale(cfg.Locale))
		return
	}

	// 1.5 设置模式 — 首次配置向导（无需 LLM client）
	// 注意：setup 需要 settings paths，放在 resolveSettingsPaths 之后
	// 1.6 shell 补全 — 无需任何初始化

	// 2. 初始化结构化日志（始终写入文件，debug 级别时额外输出 stderr）
	var logLevel slog.Level
	switch cfg.LogLevel {
	case "error":
		logLevel = slog.LevelError
	case "warn":
		logLevel = slog.LevelWarn
	case "debug":
		logLevel = slog.LevelDebug
	default:
		logLevel = slog.LevelInfo
	}
	homeDir, _ := os.UserHomeDir()
	cleanup := logging.Init(filepath.Join(homeDir, ".waveloom", "logs"), logLevel)
	defer cleanup()
	// 3. 解析配置文件路径（全局 + 项目）
	globalPath, projectPath := resolveSettingsPaths(cfg.SettingsPath)

	// 解析 locale（后续多处使用）
	loc := resolveLocaleWithSettings(cfg.Locale, projectPath, globalPath)

	// 3.2 设置模式 — 首次配置向导（无需 LLM client）
	if cfg.Setup {
		runSetup(loc)
		return
	}

	// 3.3 shell 补全 — 无需任何初始化
	if cfg.CompletionShell != "" {
		runCompletion(cfg.CompletionShell)
		return
	}

	// 3.5 ls — 列出最近 sessions(无需 LLM client)
	if cfg.ListSessions {
		listSessions(projectPath, globalPath, loc)
		return
	}

	// 3.6 skill — 远程 skill 安装/管理(无需 LLM client)
	if cfg.SkillArgs != nil {
		runSkill(cfg.SkillArgs, homeDir, loc)
		return
	}

	// 4. 加载 LLM Client(合并全局和项目配置,项目字段优先;--model 覆盖配置文件)
	llmClient, llmClientCfg, llmSettings, err := createLLMClient(globalPath, projectPath, cfg.Model, cfg.Provider, loc)
	if err != nil {
		if needsSetup() {
			runSetup(loc)
			return
		}
		slog.Error("create LLM client", "err", humanizeError(err))
		os.Exit(1)
	}

	// 4.5 创建 Tier 3 摘要专用 Client(开启 JSON 模式)
	summarizerClient := llmClient
	summaryCfg := llmClientCfg
	summaryCfg.ResponseFormat = "json_object"
	if sc, err := llm.NewClient(summaryCfg); err == nil {
		summarizerClient = sc
	}

	// 4.6 解析主 Loop 模型选择(--model > curr_model > model;proplan 校验锚点)
	modelChoice, planModel, subModel, err := resolveModelChoice(cfg.Model, llmSettings)
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗", err)
		os.Exit(1)
	}

	// 5.3 加载 Guard(权限系统,合并全局和项目权限规则)
	// 必须在 skill loader 之前创建，skill 的 allowed-tools 白名单需注册到 Guard。
	guard := createGuard(globalPath, projectPath)

	// 5.4 环境探测：预先执行，结果用于 Guard 和 system prompt
	probeResults := environment.RunProbesWithCache(context.Background(), environment.DefaultProbes())

	// 提取探测到的工具名列表，注入 Guard 的 RiskLow 白名单
	var availableTools []string
	for _, r := range probeResults {
		if r.Found {
			availableTools = append(availableTools, r.Binary)
		}
	}
	guard.SetAvailableBuildTools(availableTools)

	// 5.5 获取 CWD、homeDir、构造 skill loader
	cwd, _ := os.Getwd()
	homeDir, _ = os.UserHomeDir()
	skillLoader := skill.NewLoader(cwd, homeDir, "", "medium", guard)

	// 5.5.1 沙箱管理器(可选):--bypass-permissions、oneshot(无交互,无条件
	// 激活,对齐 ACP)或显式 enabled 时激活。
	// REGRESSION: oneshot 必须无条件激活沙箱——此前仅 --bypass-permissions
	// 触发,普通 one-shot 管道运行无 OS 级兜底。无法单测:main 流程耦合 flag 解析。
	sandboxMgr, sandboxFatal := createSandboxManager(cfg.BypassPerm || cfg.OneShot != "", cfg.NoSandbox, cfg.SandboxNetwork, globalPath, projectPath, cwd)
	if sandboxFatal {
		os.Exit(1) // failIfUnavailable: true 且后端不可用 → 拒绝启动
	}

	// 6. 初始化 Tool Registry
	registry := tool.NewRegistry()
	settingsProvider := &fileSettingsProvider{projectPath: projectPath, globalPath: globalPath}

	// 窗口容量与压缩配置统一解析(与 ACP 入口同源,单一实现):
	// 默认 → settings 覆盖 → flag(--context-limit)覆盖 ContextLimit。
	// 三条路径(压缩阈值/HUD 显示/ACP size)+ 子代理压缩同值。
	compactionConfig := resolveCompactionConfig(cfg.ContextLimit, globalPath, projectPath)
	if cfg.ContextLimit == 0 {
		cfg.ContextLimit = compactionConfig.ContextLimit
	}

	// oneshot 无交互(无 UserResponder):交互式工具(ask_user_question /
	// enter_plan_mode / exit_plan_mode)不注册,从 schema 层杜绝 LLM 提议
	// 不可用工具(对齐 ACP registerACPBuiltinTools 的取舍)。
	agentTool := registerBuiltinTools(registry, skillLoader, llmClient, llmSettings.Model, llmSettings.SubModel, cwd, settingsProvider, sandboxMgr, guard, compactionConfig, cfg.OneShot == "")
	// 8.5 启动 MCP Manager — 连接配置的 MCP Server，注册工具代理
	mcpManager := mcp.NewManager(registry)
	mcpManager.Start(context.Background(), mcp.LoadConfigs(cwd, homeDir))
	// 等待 MCP 连接完成（本地连接 < 3s），确保 capability guide 注入时有 IDE 信息
	mcpWaitCtx, mcpWaitCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer mcpWaitCancel()
waitLoop:
	for mcpManager.ClientCount() == 0 {
		select {
		case <-mcpWaitCtx.Done():
			break waitLoop
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer func() { _ = mcpManager.Stop() }()

	// 8.6 Create LSP Manager for post-edit diagnostics
	lspProbeMap := make(map[string]bool)
	for _, r := range probeResults {
		switch r.Binary {
		case "gopls", "rust-analyzer", "typescript-language-server", "clangd":
			lspProbeMap[r.Binary] = r.Found
		case "pyright":
			// pyright 与 pyright-langserver 同包安装(pip/npm);
			// langserver 无 --version,探测 CLI 即认定 langserver 可用
			lspProbeMap["pyright-langserver"] = r.Found
		}
	}
	userLSPServers := make(map[string]lsp.ServerConfig)
	var lspIdleTimeout time.Duration
	projectServers, projectIdle := lsp.LoadUserServers(projectPath)
	for k, v := range projectServers {
		userLSPServers[k] = v
	}
	if projectIdle > 0 {
		lspIdleTimeout = projectIdle
	}
	globalServers, globalIdle := lsp.LoadUserServers(globalPath)
	for k, v := range globalServers {
		if _, ok := userLSPServers[k]; !ok {
			userLSPServers[k] = v
		}
	}
	if lspIdleTimeout == 0 && globalIdle > 0 {
		lspIdleTimeout = globalIdle
	}
	if lspIdleTimeout == 0 {
		lspIdleTimeout = 5 * time.Minute
	}
	lspManager := lsp.NewManager(
		lsp.WithUserServers(userLSPServers),
		lsp.WithIdleTimeout(lspIdleTimeout),
	)
	lspManager.SetProbeMap(lspProbeMap)
	defer lspManager.Shutdown()

	// 9. 创建 @ 引用展开器（用于 AGENTS.md 和用户输入中的 @ 引用展开）
	expander := reference.New(guard)

	// 10. 加载 AGENTS.md 持久记忆
	var agentsMdText string
	if homeDir != "" {
		loader := memory.NewLoader(cwd, homeDir)
		text, warnings, loadErr := loader.Load()
		if loadErr != nil {
			slog.Warn("failed to load AGENTS.md", "err", loadErr)
		}
		for _, w := range warnings {
			slog.Warn("AGENTS.md warning", "warning", w)
		}
		agentsMdText = text
	}

	// 11. 展开 AGENTS.md 中的 @ 引用
	if agentsMdText != "" {
		expanded, _, expandErr := expander.Expand(context.Background(), agentsMdText, cwd)
		if expandErr != nil {
			slog.Warn("AGENTS.md @ expansion failed", "err", expandErr)
		} else {
			agentsMdText = expanded
		}
	}

	// 12. 创建 Context Manager（跨 Loop 调用累积消息历史，启用 DeepSeek 前缀缓存）
	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = buildSystemPrompt(cwd, loc)
	}

	// 注入环境探测结果：让 LLM 在首次交互前就知道系统可用工具链，	// 避免因命令缺失陷入探测死循环。
	// globalPath 和 projectPath 用于加载用户配置的工具路径覆盖。
	systemPrompt += formatEnvironmentSection(probeResults, cwd, globalPath, projectPath)

	// 注入 IDE 能力引导（静态，不破坏前缀缓存）
	if capGuide := mcpManager.IDEContextProvider().FormatCapabilityGuide(); capGuide != "" {
		systemPrompt += capGuide
	}

	// 注入 skill 列表到 system prompt
	if skillListing := skillLoader.FormatSkillListing(); skillListing != "" {
		systemPrompt += skillListing
	}

	// 注入工具使用指南：ToolWithPrompt.Prompt() → C1 system prompt。
	// 按需组装 — 仅已注册且实现了 ToolWithPrompt 的工具会贡献内容。
	// 放在 system prompt 最后（TAIL 末尾），Coding Scenarios / Agent Tool 等关键决策内容在前。
	if toolPrompts := registry.FormatToolPrompts(); toolPrompts != "" {
		systemPrompt += "\n\n" + toolPrompts
	}

	// 合并工具超时:优先级 CLI > project settings.json > global settings.json > 默认 5m
	if cfg.ToolTimeout == 0 {
		if d, ok, _ := agentloop.LoadToolTimeout(projectPath); ok {
			cfg.ToolTimeout = d
			cfg.ToolTimeoutSource = "settings.json"
		}
	}
	if cfg.ToolTimeout == 0 {
		if d, ok, _ := agentloop.LoadToolTimeout(globalPath); ok {
			cfg.ToolTimeout = d
			cfg.ToolTimeoutSource = "~/.waveloom/settings.json"
		}
	}
	if cfg.ToolTimeout == 0 {
		cfg.ToolTimeout = agentloop.DefaultToolTimeout
		cfg.ToolTimeoutSource = "default"
	}
	ctxMgr := session.NewWithCompaction(systemPrompt, compactionConfig, compaction.NewCompactionSummarizer(summarizerClient, 0))

	// 13. 将 AGENTS.md 作为 user 消息注入
	ctxMgr.InjectUserInstructions(agentsMdText)

	// 14. 计算 session 落盘路径
	// 优先级：settings.json session.dir > WAVELOOM_SESSION_DIR 环境变量 > ~/.waveloom/<project>/sessions/
	// --continue 恢复最近 session，--resume 指定 session ID 恢复，否则新建
	sessionOverride := session.LoadSessionDir(projectPath)
	if sessionOverride == "" {
		sessionOverride = session.LoadSessionDir(globalPath)
	}
	sessionDir, dirErr := session.ResolveSessionDir(cwd, sessionOverride)
	isResume := false
	if dirErr == nil {
		if cfg.ContinueSession {
			if sid, err := session.ContinueSessionID(sessionDir); err == nil && sid != "" {
				cfg.ResumeSessionID = sid
				fmt.Printf(messagesFor(loc).CLIContinueSession, sid)
			} else {
				fmt.Print(messagesFor(loc).CLINoRecentSession)
			}
		}
	if cfg.ResumeSessionID != "" {
		sessionPath := filepath.Join(sessionDir, cfg.ResumeSessionID+".json")
		if !ctxMgr.LoadFromFile(sessionPath) {
			slog.Error("session not found", "id", cfg.ResumeSessionID, "path", sessionPath)
			os.Exit(1)
		}
		// REGRESSION: LoadFromFile 会用磁盘持久化的 watermark.ContextLimit 整体
		// 覆盖 compactor 阈值,导致恢复会话的压缩阈值=旧值、HUD=新值(flag/settings
		// 覆盖被静默废弃)。加载后重放当前窗口容量。
		if c, ok := ctxMgr.Compactor().(interface{ SetContextLimit(int) }); ok {
			c.SetContextLimit(cfg.ContextLimit)
		}
		isResume = true
				fmt.Printf(messagesFor(loc).CLIResumedSession, cfg.ResumeSessionID)
		} else {
			sessionPath := filepath.Join(sessionDir, session.NewSessionID()+".json")
			ctxMgr.SetSessionPath(sessionPath)
		}
		// --name 命名:新建时写入,resume 时非空覆盖(改名)
		if cfg.SessionName != "" {
			ctxMgr.SetSessionName(cfg.SessionName)
		}
	}

	// REGRESSION: skill loader 在 session 确定前创建，SessionID 为空，导致 skill
	// 变量 ${CLAUDE_SESSION_ID} / ${WAVELOOM_SESSION_ID} 替换为空字符串。
	// 无法单测：skill loader 创建和 session 确定均在 main 流程中，受 flag 解析耦合。
	if sid := ctxMgr.SessionID(); sid != "" {
		skillLoader.SessionID = sid
	}

	// 15. 创建 session 级 TodoState
	todoState := todo.NewTodoState()

	// session resume: 恢复持久化的 todo 列表
	if isResume {
		if rawItems := ctxMgr.TodoItems(); len(rawItems) > 0 {
			var items []todo.TodoItem
			for _, raw := range rawItems {
				var item todo.TodoItem
				if err := json.Unmarshal(raw, &item); err == nil {
					items = append(items, item)
				}
			}
			if len(items) > 0 {
				todoState.Restore(items)
			}
		}
	}

	// 16. 分支：无 prompt → 交互式 TUI，有 prompt → 单次执行
	if cfg.OneShot == "" {
		// 16.5 加载 Hook Runner(RTK 等 hooks)
		hookRunner := loadHookRunner()
		runTUI(llmClient, registry, guard, sandboxMgr, expander, modelChoice, planModel, subModel, cfg.Theme, cfg.ContextLimit, cfg.MaxSteps, cfg.ToolTimeout, cfg.ToolTimeoutSource, cfg.BypassPerm, ctxMgr, isResume, sessionDir, globalPath, projectPath, agentsMdText, loc, todoState, hookRunner, agentTool, mcpManager, lspManager)
		return
	}

	// 16.5 加载 Hook Runner(RTK 等 hooks)
	hookRunner := loadHookRunner()
	runOneShot(cfg, llmClient, registry, guard, sandboxMgr, expander, cwd, ctxMgr, agentsMdText, loc, todoState, modelChoice, planModel, subModel, hookRunner, agentTool, mcpManager, lspManager)
}

// registerBuiltinTools 注册内置工具。
// interactive=false(one-shot 无交互入口)时不注册依赖 UserResponder 的
// 交互式工具(ask_user_question / enter_plan_mode / exit_plan_mode)——它们在
// 无 UserResponder 下调用必挂(execute.go 返回 recoverable error),不注册
// 可从 schema 层杜绝 LLM 提议不可用工具(对齐 ACP registerACPBuiltinTools)。
func registerBuiltinTools(r tool.Registry, skillLoader *skill.Loader, llmClient llm.Client, defaultModel, subModel string, cwd string, settings subagent.SettingsProvider, sandboxMgr *sandbox.SandboxManager, guard permission.Guard, compactionConfig compaction.CompactionConfig, interactive bool) *subagent.AgentTool {
	r.Register(tool.Wrap(&tool.ReadFile{}))
	r.Register(tool.Wrap(&tool.EditFile{}))
	r.Register(tool.Wrap(&tool.WriteFile{}))
	shellTool := &tool.Shell{AllowBg: true, SandboxMgr: sandboxMgr} // "bash"
	r.Register(tool.Wrap(shellTool))
	r.Register(tool.Wrap(&tool.WebFetch{}))
	r.Register(tool.Wrap(&tool.WebSearch{}))

	// Skill 工具
	if skillLoader != nil {
		r.Register(tool.Wrap(tool.NewSkillTool(&skillExecutorAdapter{loader: skillLoader})))
	}

	// AskUserQuestion — LLM 向用户发起选择题式交互决策(TUI 模式)
	// Plan mode — enter / exit(TUI 模式)
	// 两者依赖 UserResponder,仅交互入口注册。
	if interactive {
		r.Register(tool.Wrap(&tool.AskUserQuestion{}))
		r.Register(tool.Wrap(&tool.EnterPlanMode{}))
		r.Register(tool.Wrap(&tool.ExitPlanMode{}))
	}

	// Kill background task
	r.Register(tool.Wrap(&tool.KillBackgroundTask{}))

	// Agent — subagent delegation
     	at := &subagent.AgentTool{
     		LLMClient:    llmClient,
     		Settings:     settings,
     		DefaultModel: defaultModel,
     		DefaultSubModel: subModel,
     		WorkspaceDir: cwd,
     		SandboxMgr:   sandboxMgr,
     		Guard:        guard,
     		CompactionConfig: compactionConfig,
     	}
	r.Register(tool.Wrap(at))

	// TodoCreate / TodoUpdate — 结构化任务列表管理
	r.Register(tool.Wrap(&tool.TodoCreate{}))
	r.Register(tool.Wrap(&tool.TodoUpdate{}))
	return at
}

// resolveSettingsPaths 返回全局和项目配置文件路径。
// globalPath: ~/.waveloom/settings.json（用户全局，可能不存在）
// projectPath: --settings 显式指定 或 .waveloom/settings.json（项目级）
func resolveSettingsPaths(explicit string) (globalPath, projectPath string) {
	homeDir, _ := os.UserHomeDir()
	if homeDir != "" {
		globalPath = filepath.Join(homeDir, ".waveloom", "settings.json")
	}

	if explicit != "" {
		projectPath = explicit
	} else {
		projectPath = filepath.Join(".waveloom", "settings.json")
	}

	// 将相对路径转为绝对路径，避免工作目录变化导致文件找不到。
	if !filepath.IsAbs(projectPath) {
		if abs, err := filepath.Abs(projectPath); err == nil {
			projectPath = abs
		}
	}

	return globalPath, projectPath
}

// createLLMClient 合并全局和项目配置创建 LLM Client。
// 项目配置字段覆盖全局。若均无配置则生成默认项目配置。
// cliModel 为 --model 命令行参数，非空时覆盖配置文件中的模型名。
// cliProvider 为 --provider 命令行参数，非空时覆盖配置文件中的 provider 并查找 profiles。
func createLLMClient(globalPath, projectPath, cliModel, cliProvider string, loc Locale) (llm.Client, llm.ClientConfig, *llm.LLMSettings, error) {
	globalSettings, err := llm.LoadSettingsIfExists(globalPath)
	if err != nil {
		panic(fmt.Sprintf("settings parse error in %s: %v", globalPath, err))
	}
	projectSettings, err := llm.LoadSettingsIfExists(projectPath)
	if err != nil {
		panic(fmt.Sprintf("settings parse error in %s: %v", projectPath, err))
	}

	merged := llm.MergeLLMSettings(globalSettings, projectSettings)
	if merged != nil {
		if cliProvider != "" {
			merged.Provider = cliProvider
		}
		// 先解析 profile(provider 专属字段覆盖顶层残留),再应用 CLI 显式
		// 指定的模型,保证 --model 优先级高于 profile。
		merged.ResolveProfile()
		applyCliModel(merged, cliModel)
	}
	client, cfg, err := llm.NewClientFromLLMSettings(merged)
	if err != nil {
		return nil, llm.ClientConfig{}, nil, err
	}
	return client, cfg, merged, nil
}

// applyCliModel 应用 --model 覆盖到合并后的 settings。
// llm.ModelChoiceProPlan 是特殊选择值(不是模型名):不覆盖 merged.Model,
// 保持 settings 的 pro 锚点 —— client / summarizer client / subagent 锚点
// 全部使用 pro,天然安全("proplan" 字符串绝不进入 Client 配置)。
func applyCliModel(merged *llm.LLMSettings, cliModel string) {
	if cliModel != "" && cliModel != llm.ModelChoiceProPlan {
		merged.Model = cliModel
	}
}

// resolveModelChoice 解析主 Loop 的模型选择与 proplan 锚点。
// 优先级:--model > curr_model(profile 已解析)> model(空值回退)。
// 返回 (modelChoice, planModel, subModel, err):
//   - modelChoice 为具体模型名或 llm.ModelChoiceProPlan(传给 Loop 的 Config.Model)
//   - planModel/subModel 为 proplan 语义的锚点(settings model/sub_model)
//   - choice == proplan 时校验锚点非空且非 proplan 自身(锚点自指会泄漏到 API)
func resolveModelChoice(cliModel string, s *llm.LLMSettings) (string, string, string, error) {
	choice := s.CurrModel
	if cliModel != "" {
		choice = cliModel
	}
	if choice == "" {
		choice = s.Model // curr_model 为空 → 自动使用 model 配置
	}
	planModel, subModel := s.Model, s.SubModel
	if choice == llm.ModelChoiceProPlan {
		if planModel == "" || subModel == "" ||
			planModel == llm.ModelChoiceProPlan || subModel == llm.ModelChoiceProPlan {
			return "", "", "", fmt.Errorf("proplan 需要非空的 model 与 sub_model 锚点(锚点不能为 proplan 自身)")
		}
	}
	return choice, planModel, subModel, nil
}

// createGuard 创建权限守门人，合并全局和项目权限规则。
// 以 (Behavior, ToolName, Pattern) 为键，项目规则覆盖全局同键规则。
func createGuard(globalPath, projectPath string) permission.Guard {
	rules, err := permission.LoadRulesFromConfigFiles(globalPath, projectPath)
	if err != nil {
		slog.Warn("failed to load permission rules", "err", err)
		return permission.NewGuard(
			permission.WithProjectConfigPath(projectPath),
		)
	}
	opts := []permission.GuardOption{
		permission.WithProjectConfigPath(projectPath),
	}
	if len(rules) > 0 {
		opts = append(opts, permission.WithRules(rules))
	}

	// 将用户级 skill 目录加入工作目录白名单，允许 write_file/edit_file 直接操作
	if homeDir, err := os.UserHomeDir(); err == nil {
		opts = append(opts, permission.WithExtraWorkingDirs(
			filepath.Join(homeDir, ".waveloom"),
			filepath.Join(homeDir, ".claude"),
		))
	}

	return permission.NewGuard(opts...)
}

// formatEnvironmentSection 探测系统环境（编译器、运行时、构建工具），// 返回格式化的 ## Environment 节追加到 System Prompt。
// globalPath 和 projectPath 用于加载用户配置的工具路径覆盖（environment.tools）。
func formatEnvironmentSection(results []environment.ProbeResult, cwd, globalPath, projectPath string) string {
	overrides := make(map[string]string)
	for k, v := range environment.LoadToolOverrides(globalPath) {
		overrides[k] = v
	}
	for k, v := range environment.LoadToolOverrides(projectPath) {
		overrides[k] = v
	}

	osName := runtime.GOOS

	// 报告 shell 工具实际使用的解释器，非用户登录 shell。
	// 这对 LLM 编写命令语法至关重要（sh ≠ zsh ≠ cmd）。
	shellBin, _ := shellutil.ShellInterpreter()
	shellInfo := shellBin + " -c"

	return environment.FormatEnvironmentSection(results, osName, shellInfo, overrides)
}

// listSessions 列出最近的 sessions（waveloom ls）。
func listSessions(projectPath, globalPath string, loc Locale) {
	lc := messagesFor(loc)
	sessionOverride := session.LoadSessionDir(projectPath)
	if sessionOverride == "" {
		sessionOverride = session.LoadSessionDir(globalPath)
	}
	cwd, err := os.Getwd()
	if err != nil {
		slog.Error("get current directory", "err", err)
		os.Exit(1)
	}
	sessionDir, err := session.ResolveSessionDir(cwd, sessionOverride)
	if err != nil {
		slog.Error("resolve session directory", "err", err)
		os.Exit(1)
	}

	entries, err := session.LoadRecentSessions(sessionDir)
	if err != nil {
		slog.Error("load recent sessions", "err", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Println(lc.CLILsNoRecent)
		return
	}

	fmt.Println(lc.CLILsHeader)
	// name 列宽自适应:取所有条目 name 显示宽度的最大值(上限 20),
	// 使 name 与 session ID 紧凑对齐,避免短名称时出现大段空白。
	nameWidth := 0
	for _, e := range entries {
		n := e.Name
		if n == "" {
			n = "-"
		}
		if w := displayWidth(n); w > nameWidth {
			nameWidth = w
		}
	}
	if nameWidth > 20 {
		nameWidth = 20
	}
	for _, e := range entries {
		name := e.Name
		if name == "" {
			name = "-"
		}
		name = truncateByDisplayWidth(name, nameWidth)
		pad := nameWidth - displayWidth(name)
		fmt.Printf("  %s%s  %s  (%d messages, %s)\n", name, strings.Repeat(" ", pad), e.ID, e.MessageCount, e.UpdatedAt)
	}
	fmt.Println()
	fmt.Println(lc.CLILsRestoreHint)
}

// resolveLocaleWithSettings 解析 locale，优先级：
//
//	CLI --locale (非 auto) > settings.json locale > LANG 环境变量
func resolveLocaleWithSettings(cliLocale, projectPath, globalPath string) Locale {
	// 1. CLI 显式指定
	if cliLocale == "zh-CN" {
		return LocaleZhCN
	}
	if cliLocale == "en-US" {
		return LocaleEnUS
	}

	// 2. settings.json 中的 locale 字段（项目 > 全局）
	for _, p := range []string{projectPath, globalPath} {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var cfg struct {
			Locale string `json:"locale"`
		}
		if json.Unmarshal(data, &cfg) == nil && cfg.Locale != "" {
			switch cfg.Locale {
			case "zh-CN":
				return LocaleZhCN
			case "en-US":
				return LocaleEnUS
			}
		}
	}

	// 3. 环境变量检测
	return DetectLocale()
}

// skillExecutorAdapter 将 skill.Loader 适配为 tool.SkillExecutor 接口，// 消除 tool 包对 skill 包的编译期依赖。
type skillExecutorAdapter struct {
	loader *skill.Loader
}

func (a *skillExecutorAdapter) Load(name, args string) (*tool.SkillLoadResult, error) {
	loaded, err := a.loader.Load(name, args)
	if err != nil {
		return nil, err
	}
	return &tool.SkillLoadResult{
		Body:    loaded.Body,
		DirPath: loaded.DirPath,
	}, nil
}

// buildValidModels 从 LLMSettings 构造可用模型列表（用于 AgentTool 参数校验）。

// fileSettingsProvider 实现 subagent.SettingsProvider，从 project + global settings.json 合并读取。
type fileSettingsProvider struct {
	projectPath string
	globalPath  string
}

func (p *fileSettingsProvider) LoadLLM() (*llm.LLMSettings, error) {
	globalSettings, err := llm.LoadSettingsIfExists(p.globalPath)
	if err != nil {
		panic(fmt.Sprintf("settings parse error in %s: %v", p.globalPath, err))
	}
	projectSettings, err := llm.LoadSettingsIfExists(p.projectPath)
	if err != nil {
		panic(fmt.Sprintf("settings parse error in %s: %v", p.projectPath, err))
	}
	merged := llm.MergeLLMSettings(globalSettings, projectSettings)
	if merged != nil {
		merged.ResolveProfile()
	}
	return merged, nil
}

// loadHookRunner 从 settings.json 加载 hook 配置并创建 Runner。
// 配置来源：~/.claude/settings.json、.claude/settings.json、.claude/settings.local.json
// 按优先级从低到高合并（local > project > user）。
func loadHookRunner() *hook.Runner {
	var hookSources []map[hook.EventType][]hook.HookConfig

	for _, path := range hookSettingsPaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		cfg, err := hook.LoadFromSettings(data)
		if err != nil {
			slog.Warn("failed to load hooks from settings", "path", path, "err", err)
			continue
		}
		if len(cfg) > 0 {
			hookSources = append(hookSources, cfg)
			slog.Info("hooks loaded", "path", path, "eventCount", len(cfg))
		}
	}

	if len(hookSources) == 0 {
		return nil
	}

	merged := hook.MergeConfigs(hookSources...)
	runner := hook.NewRunner(merged, "", "")

	// 启动时校验 hook 命令可达性，提前暴露配置错误
	for _, w := range runner.Validate() {
		slog.Warn("hook configuration warning", "warning", w)
	}

	return runner
}

// hookSettingsPaths 返回需要检查 hooks 配置的文件路径（按优先级从低到高）。
// 优先级：~/.claude → .claude → .claude/local → ~/.waveloom → .waveloom
// Waveloom 自有配置优先级高于 外部兼容配置。
func hookSettingsPaths() []string {
	homeDir, _ := os.UserHomeDir()
	var paths []string

	// 兼容：用户全局 → 项目 → 本地
	if homeDir != "" {
		paths = append(paths, filepath.Join(homeDir, ".claude", "settings.json"))
	}
	paths = append(paths, filepath.Join(".claude", "settings.json"))
	paths = append(paths, filepath.Join(".claude", "settings.local.json"))

	// Waveloom 自有配置：优先级高于 （后覆盖前）
	if homeDir != "" {
		paths = append(paths, filepath.Join(homeDir, ".waveloom", "settings.json"))
	}
	paths = append(paths, filepath.Join(".waveloom", "settings.json"))

	return paths
}
