package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"waveloom/pkg/compaction"
	ctxpkg "waveloom/pkg/context"
	"waveloom/pkg/llm"
	"waveloom/pkg/lsp"
	"waveloom/pkg/memory"
	"waveloom/pkg/permission"
	"waveloom/pkg/reference"
	"waveloom/pkg/tool"
)

func main() {
	// 0. 注入构建版本号到 context 包（ldflags → session 文件兼容性检查）
	ctxpkg.BuildVersion = version

	// 1. 解析命令行参数
	cfg := parseCLI()
	if cfg.ShowHelp {
		printHelp()
		return
	}

	// 1.5 设置模式 — 首次配置向导
	if cfg.Setup {
		runSetup()
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

	// 4. 加载 LLM Client（合并全局和项目配置，项目字段优先）
	llmClient, llmClientCfg, err := createLLMClient(globalPath, projectPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		if needsSetup() {
			fmt.Fprintf(os.Stderr, "\n  请运行 wvl setup 完成首次配置，或设置 LLM_API_KEY 环境变量。\n")
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
	initLSPManager(globalPath, projectPath, verboseLog)

	// 6. 初始化 Tool Registry
	registry := tool.NewRegistry()
	registerBuiltinTools(registry)

	// 7. 加载 Guard（权限系统，合并全局和项目权限规则）
	guard := createGuard(globalPath, projectPath)

	// 8. 获取 CWD
	cwd, _ := os.Getwd()

	// 9. 加载 AGENTS.md 持久记忆
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

	// 10. 创建 Context Manager（跨 Loop 调用累积消息历史，启用 DeepSeek 前缀缓存）
	systemPrompt := cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = buildSystemPrompt(cwd)
	}
	// 合并 compaction 配置：默认值 + settings.json 覆盖
	compactionConfig := compaction.DefaultCompactionConfig()
	if cs := compaction.LoadCompactionSettings(globalPath); cs != nil {
		cs.ApplyToConfig(&compactionConfig)
	}
	if cs := compaction.LoadCompactionSettings(projectPath); cs != nil {
		cs.ApplyToConfig(&compactionConfig)
	}
	ctxMgr := ctxpkg.NewWithCompaction(systemPrompt, compactionConfig, compaction.NewCompactionSummarizer(summarizerClient, 0))

	// 11. 将 AGENTS.md 作为 user 消息注入
	ctxMgr.InjectUserInstructions(agentsMdText)

	// 12. 创建 @ 引用展开器
	expander := reference.New(registry, guard)

	// 9.5 计算 session 落盘路径
	// 优先级：settings.json session.dir > WAVELOOM_SESSION_DIR 环境变量 > ~/.waveloom/<project>/sessions/
	// --resume 指定 session ID 时恢复，否则新建
	sessionOverride := ctxpkg.LoadSessionDir(projectPath)
	if sessionOverride == "" {
		sessionOverride = ctxpkg.LoadSessionDir(globalPath)
	}
	sessionDir, dirErr := ctxpkg.ResolveSessionDir(cwd, sessionOverride)
	if dirErr == nil {
		if cfg.ResumeSessionID != "" {
			sessionPath := filepath.Join(sessionDir, cfg.ResumeSessionID+".json")
			if !ctxMgr.LoadFromFile(sessionPath) {
				fmt.Fprintf(os.Stderr, "Error: session '%s' not found at %s\n", cfg.ResumeSessionID, sessionPath)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "已恢复 session: %s\n", cfg.ResumeSessionID)
		} else {
			sessionPath := filepath.Join(sessionDir, ctxpkg.NewSessionID()+".json")
			ctxMgr.SetSessionPath(sessionPath)
		}
	}

	// 13. 分支：无 prompt → 交互式 TUI，有 prompt → 单次执行
	if cfg.OneShot == "" {
		runTUI(llmClient, registry, guard, expander, cfg.Model, cfg.Theme, verboseLog, cfg.ContextLimit, ctxMgr)
		return
	}

	runOneShot(cfg, llmClient, registry, guard, expander, cwd, verboseLog, ctxMgr)
}

// registerBuiltinTools 注册内置工具。
func registerBuiltinTools(r tool.Registry) {
	r.Register(tool.Wrap(&tool.ReadFile{}))
	r.Register(tool.Wrap(&tool.WriteFile{}))
	r.Register(tool.Wrap(&tool.EditFile{}))
	r.Register(tool.Wrap(&tool.Shell{}))
	r.Register(tool.Wrap(&tool.Grep{}))
	r.Register(tool.Wrap(&tool.SearchFile{}))
	r.Register(tool.Wrap(&tool.Ls{}))
	r.Register(tool.Wrap(&tool.WebFetch{}))

	// LSP 工具：仅在 LSPManager 已初始化时注册
	if tool.LSPManager != nil {
		r.Register(tool.Wrap(&tool.LSDiagnostic{}))
		r.Register(tool.Wrap(&tool.LSPDefinition{}))
		r.Register(tool.Wrap(&tool.LSPReferences{}))
		r.Register(tool.Wrap(&tool.LSPHover{}))
	}
}

// initLSPManager 初始化 LSP Server 管理器。
// 合并全局和项目 settings.json 中的 lsp 配置。
func initLSPManager(globalPath, projectPath string, verboseLog io.Writer) {
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
	tool.LSPManager = lsp.NewManager(opts...)
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
func createLLMClient(globalPath, projectPath string) (llm.Client, llm.ClientConfig, error) {
	globalSettings, _ := llm.LoadSettingsIfExists(globalPath)
	projectSettings, _ := llm.LoadSettingsIfExists(projectPath)

	// 两边都没有配置文件 → 生成默认项目配置
	if globalSettings == nil && projectSettings == nil {
		if err := llm.WriteDefaultSettings(projectPath); err != nil {
			return nil, llm.ClientConfig{}, fmt.Errorf("failed to create default settings: %w", err)
		}
		fmt.Fprintf(os.Stderr, "📝 已生成默认配置文件: %s\n", projectPath)
		fmt.Fprintf(os.Stderr, "   💡 运行 wvl setup 完成首次配置，或设置 LLM_API_KEY 环境变量\n")
		var loadErr error
		projectSettings, loadErr = llm.LoadSettingsIfExists(projectPath)
		if loadErr != nil {
			return nil, llm.ClientConfig{}, loadErr
		}
	}

	merged := llm.MergeLLMSettings(globalSettings, projectSettings)
	client, cfg, err := llm.NewClientFromLLMSettings(merged)
	if err != nil {
		return nil, llm.ClientConfig{}, err
	}
	return client, cfg, nil
}

// setupVerboseLog 在 .waveloom/ 下创建滚动日志。
// --verbose 时：wvl.log → wvl.log.1（丢弃更旧的），创建新 wvl.log。
// 非 verbose 时返回 nil, nil。
func setupVerboseLog(verbose bool) (io.WriteCloser, error) {
	if !verbose {
		return nil, nil
	}

	logDir := filepath.Join(".waveloom")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	logPath := filepath.Join(logDir, "wvl.log")
	oldPath := logPath + ".1"

	// 轮换: wvl.log → wvl.log.1
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


