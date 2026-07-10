package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Menfre01/waveloom/pkg/agentloop"
	"github.com/Menfre01/waveloom/pkg/compaction"
	ctxpkg "github.com/Menfre01/waveloom/pkg/context"
	"github.com/Menfre01/waveloom/pkg/environment"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/mcp"
	"github.com/Menfre01/waveloom/pkg/memory"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/reference"
	"github.com/Menfre01/waveloom/pkg/shellutil"
	"github.com/Menfre01/waveloom/pkg/skill"
	"github.com/Menfre01/waveloom/pkg/subagent"
	"github.com/Menfre01/waveloom/pkg/todo"
	"github.com/Menfre01/waveloom/pkg/tool"
)

func main() {
	// 0. 注入构建版本号到 context 包（ldflags → session 文件兼容性检查）
	ctxpkg.BuildVersion = Version

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

	// 2. 设置 verbose 日志（放在 LLM 之前，确保无 API Key 也能记录启动错误）
	verboseLog, logErr := setupVerboseLog(cfg.Verbose)
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to open verbose log: %v\n", logErr)
	}
	if verboseLog != nil {
		defer func() { _ = verboseLog.Close() }()
	}

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

	// 3.5 ls — 列出最近 sessions（无需 LLM client）
	if cfg.ListSessions {
		listSessions(projectPath, globalPath, loc)
		return
	}

	// 4. 加载 LLM Client（合并全局和项目配置，项目字段优先；--model 覆盖配置文件）
	llmClient, llmClientCfg, llmSettings, err := createLLMClient(globalPath, projectPath, cfg.Model, loc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		if needsSetup() {
			fmt.Fprint(os.Stderr, messagesFor(loc).CLINoAPIKeySetupHint)
		}
		os.Exit(1)
	}

	// 4.5 创建 Tier 3 摘要专用 Client（开启 JSON 模式）
	summarizerClient := llmClient
	summaryCfg := llmClientCfg
	summaryCfg.ResponseFormat = "json_object"
	if sc, err := llm.NewClient(summaryCfg); err == nil {
		summarizerClient = sc
	}

	// 5.3 加载 Guard（权限系统，合并全局和项目权限规则）
	// 必须在 skill loader 之前创建，skill 的 allowed-tools 白名单需注册到 Guard。
	guard := createGuard(globalPath, projectPath)

	// 5.4 环境探测：预先执行，结果用于 Guard 和 system prompt
	probeResults := environment.RunProbes(context.Background(), environment.DefaultProbes())

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
	homeDir, _ := os.UserHomeDir()
	skillLoader := skill.NewLoader(cwd, homeDir, "", "medium", guard)

	// 6. 初始化 Tool Registry
	registry := tool.NewRegistry()
	subModelValidation := buildValidModels(llmSettings)
	registerBuiltinTools(registry, skillLoader, llmClient, subModelValidation, llmSettings.SubModel, cwd)

	// 8.5 启动 MCP Manager — 连接配置的 MCP Server，注册工具代理
	// 日志输出策略：
	//   - --verbose：写入滚动日志文件
	//   - TUI 模式（默认）：丢弃（避免泄漏到界面）
	//   - One-shot 模式：输出到 stderr（无 TUI，安全）
	mcpLogger := io.Discard
	if verboseLog != nil {
		mcpLogger = verboseLog
	} else if cfg.OneShot != "" {
		mcpLogger = os.Stderr
	}
	mcpManager := mcp.NewManager(registry,
		mcp.WithLogger(log.New(mcpLogger, "[mcp] ", log.LstdFlags)),
	)
	mcpManager.Start(context.Background(), mcp.LoadConfigs(cwd, homeDir))
	defer func() { _ = mcpManager.Stop() }()

	// 9. 创建 @ 引用展开器（用于 AGENTS.md 和用户输入中的 @ 引用展开）
	expander := reference.New(guard)

	// 10. 加载 AGENTS.md 持久记忆
	var agentsMdText string
	if homeDir != "" {
		loader := memory.NewLoader(cwd, homeDir)
		text, warnings, loadErr := loader.Load()
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to load AGENTS.md: %v\n", loadErr)
		}
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
		}
		agentsMdText = text
	}

	// 11. 展开 AGENTS.md 中的 @ 引用
	if agentsMdText != "" {
		expanded, _, expandErr := expander.Expand(context.Background(), agentsMdText, cwd)
		if expandErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: AGENTS.md @ reference expansion failed: %v\n", expandErr)
		} else {
			agentsMdText = expanded
		}
	}

	// 12. 创建 Context Manager（跨 Loop 调用累积消息历史，启用 DeepSeek 前缀缓存）
	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = buildSystemPrompt(cwd, loc)
	}

	// 注入环境探测结果：让 LLM 在首次交互前就知道系统可用工具链，
	// 避免因命令缺失陷入探测死循环。
	// globalPath 和 projectPath 用于加载用户配置的工具路径覆盖。
	systemPrompt += formatEnvironmentSection(probeResults, cwd, globalPath, projectPath)

	// 注入 skill 列表到 system prompt
	if skillListing := skillLoader.FormatSkillListing(); skillListing != "" {
		systemPrompt += skillListing
	}

	// 注入 subagent 模型选择指导（主模型 ≠ sub_model 时）
	if llmSettings.SubModel != "" && llmSettings.Model != llmSettings.SubModel {
		systemPrompt += buildModelSelectionSection(llmSettings.Model, llmSettings.SubModel)
	}

	// 合并 compaction 配置：默认值 + settings.json 覆盖
	compactionConfig := compaction.DefaultCompactionConfig()
	if cs := compaction.LoadCompactionSettings(globalPath); cs != nil {
		cs.ApplyToConfig(&compactionConfig)
	}
	if cs := compaction.LoadCompactionSettings(projectPath); cs != nil {
		cs.ApplyToConfig(&compactionConfig)
	}

	// 合并工具超时：优先级 CLI > project settings.json > global settings.json > 默认 10m
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
	ctxMgr := ctxpkg.NewWithCompaction(systemPrompt, compactionConfig, compaction.NewCompactionSummarizer(summarizerClient, 0))

	// 13. 将 AGENTS.md 作为 user 消息注入
	ctxMgr.InjectUserInstructions(agentsMdText)

	// 14. 计算 session 落盘路径
	// 优先级：settings.json session.dir > WAVELOOM_SESSION_DIR 环境变量 > ~/.waveloom/<project>/sessions/
	// --continue 恢复最近 session，--resume 指定 session ID 恢复，否则新建
	sessionOverride := ctxpkg.LoadSessionDir(projectPath)
	if sessionOverride == "" {
		sessionOverride = ctxpkg.LoadSessionDir(globalPath)
	}
	sessionDir, dirErr := ctxpkg.ResolveSessionDir(cwd, sessionOverride)
	isResume := false
	if dirErr == nil {
		if cfg.ContinueSession {
			if sid, err := ctxpkg.ContinueSessionID(sessionDir); err == nil && sid != "" {
				cfg.ResumeSessionID = sid
				fmt.Fprintf(os.Stderr, messagesFor(loc).CLIContinueSession, sid)
			} else {
				fmt.Fprint(os.Stderr, messagesFor(loc).CLINoRecentSession)
			}
		}
		if cfg.ResumeSessionID != "" {
			sessionPath := filepath.Join(sessionDir, cfg.ResumeSessionID+".json")
			if !ctxMgr.LoadFromFile(sessionPath) {
				fmt.Fprintf(os.Stderr, "Error: session '%s' not found at %s\n", cfg.ResumeSessionID, sessionPath)
				os.Exit(1)
			}
			isResume = true
			fmt.Fprintf(os.Stderr, messagesFor(loc).CLIResumedSession, cfg.ResumeSessionID)
		} else {
			sessionPath := filepath.Join(sessionDir, ctxpkg.NewSessionID()+".json")
			ctxMgr.SetSessionPath(sessionPath)
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
		runTUI(llmClient, registry, guard, expander, cfg.Model, cfg.Theme, verboseLog, cfg.ContextLimit, cfg.MaxTurns, cfg.ToolTimeout, cfg.ToolTimeoutSource, cfg.BypassPerm, ctxMgr, isResume, sessionDir, globalPath, projectPath, agentsMdText, loc, todoState)
		return
	}

	runOneShot(cfg, llmClient, registry, guard, expander, cwd, verboseLog, ctxMgr, agentsMdText, loc, todoState)
}

// registerBuiltinTools 注册内置工具。
func registerBuiltinTools(r tool.Registry, skillLoader *skill.Loader, llmClient llm.Client, validModels []string, subModel string, cwd string) {
	r.Register(tool.Wrap(&tool.ReadFile{}))
	r.Register(tool.Wrap(&tool.WriteFile{}))
	r.Register(tool.Wrap(&tool.EditFile{}))
	r.Register(tool.Wrap(&tool.Shell{AllowBg: true})) // "bash"
	r.Register(tool.Wrap(&tool.WebFetch{}))
	r.Register(tool.Wrap(&tool.WebSearch{}))

	// Skill 工具
	if skillLoader != nil {
		r.Register(tool.Wrap(tool.NewSkillTool(&skillExecutorAdapter{loader: skillLoader})))
	}

	// AskUserQuestion — LLM 向用户发起选择题式交互决策（TUI 模式）
	r.Register(tool.Wrap(&tool.AskUserQuestion{}))

	// Plan mode — enter / exit
	r.Register(tool.Wrap(&tool.EnterPlanMode{}))
	r.Register(tool.Wrap(&tool.ExitPlanMode{}))

	// Kill background task
	r.Register(tool.Wrap(&tool.KillBackgroundTask{}))

	// Agent — subagent delegation
	at := &subagent.AgentTool{
		LLMClient:       llmClient,
		ValidModels:     validModels,
		DefaultSubModel: subModel,
		WorkspaceDir:    cwd,
	}
	r.Register(tool.Wrap(at))

	// TodoWrite — 结构化任务列表管理
	r.Register(tool.Wrap(&tool.TodoWrite{}))
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
func createLLMClient(globalPath, projectPath, cliModel string, loc Locale) (llm.Client, llm.ClientConfig, *llm.LLMSettings, error) {
	globalSettings, _ := llm.LoadSettingsIfExists(globalPath)
	projectSettings, _ := llm.LoadSettingsIfExists(projectPath)

	// 两边都没有配置文件 → 生成默认项目配置
	if globalSettings == nil && projectSettings == nil {
		if err := llm.WriteDefaultSettings(projectPath); err != nil {
			return nil, llm.ClientConfig{}, nil, fmt.Errorf("failed to create default settings: %w", err)
		}
		fmt.Fprintf(os.Stderr, messagesFor(loc).CLIDefaultConfigCreated, projectPath)
		fmt.Fprint(os.Stderr, messagesFor(loc).CLISetupHint)
		var loadErr error
		projectSettings, loadErr = llm.LoadSettingsIfExists(projectPath)
		if loadErr != nil {
			return nil, llm.ClientConfig{}, nil, loadErr
		}
	}

	merged := llm.MergeLLMSettings(globalSettings, projectSettings)
	if cliModel != "" {
		merged.Model = cliModel
	}
	client, cfg, err := llm.NewClientFromLLMSettings(merged)
	if err != nil {
		return nil, llm.ClientConfig{}, nil, err
	}
	return client, cfg, merged, nil
}

// setupVerboseLog 在 .waveloom/ 下创建滚动日志。
// --verbose 时：waveloom.log → waveloom.log.1（丢弃更旧的），创建新 waveloom.log。
// 非 verbose 时返回 nil, nil。
func setupVerboseLog(verbose bool) (io.WriteCloser, error) {
	if !verbose {
		return nil, nil
	}

	logDir := filepath.Join(".waveloom")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	logPath := filepath.Join(logDir, "waveloom.log")
	oldPath := logPath + ".1"

	// 轮换: waveloom.log → waveloom.log.1
	if _, err := os.Stat(logPath); err == nil {
		_ = os.Remove(oldPath)          // 丢弃更旧
		_ = os.Rename(logPath, oldPath) // 当前 → .1
	}

	f, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Verbose log: %s\n", logPath)
	fmt.Fprintf(os.Stderr, "   Monitor: tail -f %s\n", logPath)
	return f, nil
}

// createGuard 创建权限守门人，合并全局和项目权限规则。
// 以 (Behavior, ToolName, Pattern) 为键，项目规则覆盖全局同键规则。
func createGuard(globalPath, projectPath string) permission.Guard {
	rules, err := permission.LoadRulesFromConfigFiles(globalPath, projectPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load permission rules: %v\n", err)
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

// formatEnvironmentSection 探测系统环境（编译器、运行时、构建工具），
// 返回格式化的 ## Environment 节追加到 System Prompt。
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
	sessionOverride := ctxpkg.LoadSessionDir(projectPath)
	if sessionOverride == "" {
		sessionOverride = ctxpkg.LoadSessionDir(globalPath)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: get current directory: %v\n", err)
		os.Exit(1)
	}
	sessionDir, err := ctxpkg.ResolveSessionDir(cwd, sessionOverride)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: resolve session directory: %v\n", err)
		os.Exit(1)
	}

	entries, err := ctxpkg.LoadRecentSessions(sessionDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load recent sessions: %v\n", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Println(lc.CLILsNoRecent)
		return
	}

	fmt.Println(lc.CLILsHeader)
	for _, e := range entries {
		fmt.Printf("  %s  (%d messages, %s)\n", e.ID, e.MessageCount, e.UpdatedAt)
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

// skillExecutorAdapter 将 skill.Loader 适配为 tool.SkillExecutor 接口，
// 消除 tool 包对 skill 包的编译期依赖。
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
// 列表包含主模型和子模型（去重），仅在有子模型时启用校验。
func buildValidModels(s *llm.LLMSettings) []string {
	if s == nil || s.SubModel == "" {
		return nil
	}
	models := []string{s.Model}
	if s.SubModel != s.Model {
		models = append(models, s.SubModel)
	}
	return models
}

// buildModelSelectionSection 构造注入到 system prompt 的模型选择指导。
func buildModelSelectionSection(defaultModel, flashModel string) string {
	return fmt.Sprintf(`
## Subagent Model Selection

When spawning subagents with the agent tool, you can override the model via the optional
`+"`model`"+` parameter.

  (omit / empty)  → %s — full reasoning capability, higher per-token cost.
  "%s"             → %s — ~2x cheaper per token, optimized for speed.

Choose based on the task:
- Tasks requiring analysis, judgment, or multi-step planning → prefer %s. Deep reasoning justifies the higher cost.
- Tasks requiring search, lookup, single-step edits, or pattern matching → prefer %s. For these tasks, %s matches %s in output quality while costing significantly less — no quality trade-off.

If you pass an unrecognized value, the default is used.
`, defaultModel, flashModel, flashModel, defaultModel, flashModel, flashModel, defaultModel)
}
