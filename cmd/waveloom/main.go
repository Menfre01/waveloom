package main

import (
	"context"
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
	"github.com/Menfre01/waveloom/pkg/lsp"
	"github.com/Menfre01/waveloom/pkg/memory"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/reference"
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
		printHelp()
		return
	}

	// 1.5 设置模式 — 首次配置向导
	if cfg.Setup {
		runSetup()
		return
	}

	// 1.6 shell 补全 — 无需任何初始化
	if cfg.CompletionShell != "" {
		runCompletion(cfg.CompletionShell)
		return
	}

	// 2. 设置 verbose 日志（放在 LLM 之前，确保无 API Key 也能记录启动错误）
	verboseLog, logErr := setupVerboseLog(cfg.Verbose)
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to open verbose log: %v\n", logErr)
	}
	if verboseLog != nil {
		defer verboseLog.Close()
	}

	// 3. 解析配置文件路径（全局 + 项目）
	globalPath, projectPath := resolveSettingsPaths(cfg.SettingsPath)

	// 3.5 ls — 列出最近 sessions（无需 LLM client）
	if cfg.ListSessions {
		listSessions(projectPath, globalPath)
		return
	}

	// 4. 加载 LLM Client（合并全局和项目配置，项目字段优先；--model 覆盖配置文件）
	llmClient, llmClientCfg, err := createLLMClient(globalPath, projectPath, cfg.Model)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		if needsSetup() {
			fmt.Fprintf(os.Stderr, "\n  请运行 waveloom setup 完成首次配置，或设置 LLM_API_KEY 环境变量。\n")
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

	// 5. 初始化 LSP Manager（全局，供 LSP 工具使用）
	lspProvider := initLSPManager(globalPath, projectPath, verboseLog)

	// 6. 初始化 Tool Registry
	registry := tool.NewRegistry()
	registerBuiltinTools(registry, lspProvider)

	// 7. 加载 Guard（权限系统，合并全局和项目权限规则）
	guard := createGuard(globalPath, projectPath)

	// 8. 获取 CWD
	cwd, _ := os.Getwd()

	// 9. 创建 @ 引用展开器（用于 AGENTS.md 和用户输入中的 @ 引用展开）
	expander := reference.New(guard)

	// 10. 加载 AGENTS.md 持久记忆
	var agentsMdText string
	if homeDir, err := os.UserHomeDir(); err == nil {
		loader := memory.NewLoader(cwd, homeDir)
		text, warnings, loadErr := loader.Load()
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: 加载 AGENTS.md 失败: %v\n", loadErr)
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
			fmt.Fprintf(os.Stderr, "Warning: AGENTS.md @ 引用展开失败: %v\n", expandErr)
		} else {
			agentsMdText = expanded
		}
	}

	// 12. 创建 Context Manager（跨 Loop 调用累积消息历史，启用 DeepSeek 前缀缓存）
	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = buildSystemPrompt(cwd)
	}

	// 注入环境探测结果：让 LLM 在首次交互前就知道系统可用工具链，
	// 避免因命令缺失陷入探测死循环。
	// globalPath 和 projectPath 用于加载用户配置的工具路径覆盖。
	systemPrompt += probeEnvironment(cwd, globalPath, projectPath)
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
		cfg.ToolTimeoutSource = "默认"
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
				fmt.Fprintf(os.Stderr, "继续最近 session: %s\n", sid)
			} else {
				fmt.Fprintf(os.Stderr, "没有找到最近的 session，将创建新 session\n")
			}
		}
		if cfg.ResumeSessionID != "" {
			sessionPath := filepath.Join(sessionDir, cfg.ResumeSessionID+".json")
			if !ctxMgr.LoadFromFile(sessionPath) {
				fmt.Fprintf(os.Stderr, "Error: session '%s' not found at %s\n", cfg.ResumeSessionID, sessionPath)
				os.Exit(1)
			}
			isResume = true
			fmt.Fprintf(os.Stderr, "已恢复 session: %s\n", cfg.ResumeSessionID)
		} else {
			sessionPath := filepath.Join(sessionDir, ctxpkg.NewSessionID()+".json")
			ctxMgr.SetSessionPath(sessionPath)
		}
	}

	// 15. 分支：无 prompt → 交互式 TUI，有 prompt → 单次执行
	if cfg.OneShot == "" {
		runTUI(llmClient, registry, guard, expander, cfg.Model, cfg.Theme, verboseLog, cfg.ContextLimit, cfg.MaxTurns, cfg.ToolTimeout, cfg.ToolTimeoutSource, cfg.BypassPerm, ctxMgr, isResume, sessionDir, globalPath, projectPath, agentsMdText)
		return
	}

	runOneShot(cfg, llmClient, registry, guard, expander, cwd, verboseLog, ctxMgr)
}

// registerBuiltinTools 注册内置工具。
func registerBuiltinTools(r tool.Registry, lspProvider *tool.LSPProvider) {
	r.Register(tool.Wrap(&tool.ReadFile{}))
	r.Register(tool.Wrap(&tool.WriteFile{}))
	r.Register(tool.Wrap(&tool.EditFile{}))
	r.Register(tool.Wrap(&tool.Shell{}))
	r.Register(tool.Wrap(&tool.Grep{}))
	r.Register(tool.Wrap(&tool.SearchFile{}))
	r.Register(tool.Wrap(&tool.Ls{}))
	r.Register(tool.Wrap(&tool.WebFetch{}))

	// LSP 工具：通过依赖注入初始化
	if lspProvider != nil && lspProvider.Manager != nil {
		r.Register(tool.Wrap(tool.NewLSDiagnostic(lspProvider)))
		r.Register(tool.Wrap(tool.NewLSPDefinition(lspProvider)))
		r.Register(tool.Wrap(tool.NewLSPReferences(lspProvider)))
		r.Register(tool.Wrap(tool.NewLSPHover(lspProvider)))
	}
}

// initLSPManager 初始化 LSP Server 管理器。
// 合并全局和项目 settings.json 中的 lsp 配置。
func initLSPManager(globalPath, projectPath string, verboseLog io.Writer) *tool.LSPProvider {
	// 加载用户覆盖配置
	userServers := lsp.LoadUserServers(projectPath)
	if globalOverrides := lsp.LoadUserServers(globalPath); len(globalOverrides) > 0 {
		for ext, cfg := range globalOverrides {
			if _, exists := userServers[ext]; !exists {
				userServers[ext] = cfg
			}
		}
	}

	opts := []lsp.ManagerOption{lsp.WithUserServers(userServers)}
	if verboseLog != nil {
		opts = append(opts, lsp.WithLogger(log.New(verboseLog, "[lsp] ", log.LstdFlags)))
	}
	mgr := lsp.NewManager(opts...)

	return tool.NewLSPProvider(mgr)
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
func createLLMClient(globalPath, projectPath, cliModel string) (llm.Client, llm.ClientConfig, error) {
	globalSettings, _ := llm.LoadSettingsIfExists(globalPath)
	projectSettings, _ := llm.LoadSettingsIfExists(projectPath)

	// 两边都没有配置文件 → 生成默认项目配置
	if globalSettings == nil && projectSettings == nil {
		if err := llm.WriteDefaultSettings(projectPath); err != nil {
			return nil, llm.ClientConfig{}, fmt.Errorf("failed to create default settings: %w", err)
		}
		fmt.Fprintf(os.Stderr, "📝 已生成默认配置文件: %s\n", projectPath)
		fmt.Fprintf(os.Stderr, "   💡 运行 waveloom setup 完成首次配置，或设置 LLM_API_KEY 环境变量\n")
		var loadErr error
		projectSettings, loadErr = llm.LoadSettingsIfExists(projectPath)
		if loadErr != nil {
			return nil, llm.ClientConfig{}, loadErr
		}
	}

	merged := llm.MergeLLMSettings(globalSettings, projectSettings)
	if cliModel != "" {
		merged.Model = cliModel
	}
	client, cfg, err := llm.NewClientFromLLMSettings(merged)
	if err != nil {
		return nil, llm.ClientConfig{}, err
	}
	return client, cfg, nil
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
		os.Remove(oldPath)                     // 丢弃更旧
		os.Rename(logPath, oldPath)           // 当前 → .1
	}

	f, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "📝 verbose 日志: %s\n", logPath)
	fmt.Fprintf(os.Stderr, "   另一个终端运行: tail -f %s\n", logPath)
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
		fmt.Fprintf(os.Stderr, "📋 已加载 %d 条权限规则\n", len(rules))
		opts = append(opts, permission.WithRules(rules))
	}
	return permission.NewGuard(opts...)
}

// probeEnvironment 探测系统环境（编译器、运行时、构建工具），
// 返回格式化的 ## Environment 节追加到 System Prompt。
// globalPath 和 projectPath 用于加载用户配置的工具路径覆盖（environment.tools）。
func probeEnvironment(cwd, globalPath, projectPath string) string {
	results := environment.RunProbes(context.Background(), environment.DefaultProbes())

	// 合并工具路径覆盖：全局 + 项目，项目优先
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
	var shellInfo string
	if runtime.GOOS == "windows" {
		shellInfo = "cmd /c"
	} else {
		shellInfo = "sh -c"
	}

	return environment.FormatEnvironmentSection(results, osName, shellInfo, overrides)
}

// listSessions 列出最近的 sessions（waveloom ls）。
func listSessions(projectPath, globalPath string) {
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
		fmt.Println("没有找到最近的 session。")
		return
	}

	fmt.Println("最近 sessions:")
	for _, e := range entries {
		fmt.Printf("  %s  (%d messages, %s)\n", e.ID, e.MessageCount, e.UpdatedAt)
	}
	fmt.Println()
	fmt.Println("恢复: waveloom --resume <id>  或  waveloom --continue")
}

