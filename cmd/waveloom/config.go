package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Menfre01/waveloom/pkg/agentloop"
)

// CLIConfig 命令行配置。
type CLIConfig struct {
	OneShot      string // 单次模式 prompt
	ShowHelp     bool
	ShowVersion  bool
	Setup        bool   // 首次设置向导
	MaxTurns     int
	SystemPrompt string
	Model        string
	ContextLimit int    // 解析后的上下文窗口 token 数
	Theme           string // 主题模式: auto / dark / light
	Locale          string // 界面语言: zh-CN / en-US / auto（自动检测）
	ResumeSessionID string // 恢复指定 session ID（空 = 新建 session）
	ContinueSession bool   // 恢复最近一个 session
	ListSessions    bool   // 列出最近 sessions
	CompletionShell string // shell 补全脚本名称（bash/zsh/fish），空 = 不输出
	BypassPerm      bool
	LogLevel  string // 日志级别: error / warn / info / debug，默认 info
	SettingsPath string // settings.json 路径
	ToolTimeoutRaw string // 单个工具执行超时（Go Duration 格式，如 "10m" / "600s"），空 = 默认 10m
	ToolTimeout    time.Duration // 解析后的值
	ToolTimeoutSource string   // 超时配置来源（CLI / settings.json / 默认），供 TUI 通知使用
}

// parseCLI 解析命令行参数。
func parseCLI() CLIConfig {
	cfg := CLIConfig{}
	var contextLimitRaw string

	flag.Usage = func() {
		printHelpWithAutoDetect()
	}

	flag.StringVar(&cfg.Model, "model", "", "LLM 模型名称（默认从环境变量 LLM_MODEL 读取）")
	flag.IntVar(&cfg.MaxTurns, "max-turns", 0, "最大 turn 数（0=无限制）")
	flag.StringVar(&cfg.SystemPrompt, "system-prompt", "", "系统提示词")
	flag.StringVar(&contextLimitRaw, "context-limit", "1M", "上下文窗口 token 上限")
	flag.StringVar(&cfg.Theme, "theme", "auto", "主题模式 (auto/dark/light)，auto 自动检测终端背景色")
	flag.StringVar(&cfg.Locale, "locale", "auto", "界面语言 (zh-CN/en-US/auto)，auto 从 LANG 环境变量自动检测")
	flag.StringVar(&cfg.SettingsPath, "settings", "", "显式指定项目配置文件路径（默认: .waveloom/settings.json）")
	flag.StringVar(&cfg.ResumeSessionID, "resume", "", "恢复指定 session ID 的对话（空 = 新建 session）")
	flag.BoolVar(&cfg.ContinueSession, "continue", false, "恢复最近一个 session 的对话")
	flag.StringVar(&cfg.LogLevel, "log-level", "info", "日志级别 (error/warn/info/debug)")
	flag.BoolVar(&cfg.BypassPerm, "bypass-permissions", false, "跳过权限检查（CI/测试）")
	flag.StringVar(&cfg.ToolTimeoutRaw, "tool-timeout", "", "单个工具执行超时（Go Duration 格式，如 10m/600s/0s，0=禁用，默认 10m）")

	setup := flag.Bool("setup", false, "首次设置向导")

	help := flag.Bool("help", false, "显示帮助")
	h := flag.Bool("h", false, "显示帮助")
	version := flag.Bool("version", false, "显示版本号")

	flag.Parse()

	cfg.Setup = *setup
	cfg.ShowHelp = *help || *h
	cfg.ShowVersion = *version

	// 解析上下文窗口大小（支持 1M / 200k / 1048576 等格式）
	var parseErr error
	cfg.ContextLimit, parseErr = parseTokenLimit(contextLimitRaw)
	if parseErr != nil {
		slog.Warn("cannot parse --context-limit, falling back to 1M", "value", contextLimitRaw, "err", parseErr)
		cfg.ContextLimit = 1000000
	}

	// 解析工具超时
	if cfg.ToolTimeoutRaw == "" {
		cfg.ToolTimeout = 0 // 0 表示未设置，由 main.go 从 settings.json 回退
	} else {
		d, err := time.ParseDuration(cfg.ToolTimeoutRaw)
		if err != nil {
			slog.Warn("cannot parse --tool-timeout, falling back to 10m", "value", cfg.ToolTimeoutRaw, "err", err)
			cfg.ToolTimeout = agentloop.DefaultToolTimeout
			cfg.ToolTimeoutSource = "default"
		} else {
			cfg.ToolTimeout = d
			cfg.ToolTimeoutSource = "CLI"
		}
	}

	// 单次模式：命令行剩余参数即 prompt
	args := flag.Args()
	if len(args) > 0 {
		// "setup"、"ls"、"completion"、"mcp" 作为子命令处理，不走 oneshot
		switch args[0] {
		case "setup":
			cfg.Setup = true
		case "ls":
			cfg.ListSessions = true
		case "completion":
			if len(args) >= 2 {
				cfg.CompletionShell = args[1]
			} else {
				fmt.Fprintf(os.Stderr, "Usage: waveloom completion <bash|zsh|fish>\n")
				os.Exit(1)
			}
		case "mcp":
			runMCPCommand(args[1:])
		default:
			cfg.OneShot = args[0]
		}
	}

	// 校验 theme 值
	switch cfg.Theme {
	case "auto", "dark", "light", "darkcolorblind", "lightcolorblind":
		// ok
	default:
		slog.Warn("unknown theme, falling back to auto", "theme", cfg.Theme)
		cfg.Theme = "auto"
	}

	// 校验 locale 值
	switch cfg.Locale {
	case "auto", "zh-CN", "en-US":
		// ok
	default:
		slog.Warn("unknown locale, falling back to auto", "locale", cfg.Locale)
		cfg.Locale = "auto"
	}

	return cfg
}

// parseTokenLimit 解析上下文窗口大小字符串（支持 1M / 200k / 1048576 等格式）。
func parseTokenLimit(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty value")
	}

	// 后缀单位
	multiplier := 1
	last := s[len(s)-1]
	switch last {
	case 'M', 'm':
		multiplier = 1000 * 1000
		s = s[:len(s)-1]
	case 'K', 'k':
		multiplier = 1000
		s = s[:len(s)-1]
	}

	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %w", err)
	}
	if v <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return v * multiplier, nil
}

// printHelp 显示帮助信息。
func printHelp(loc Locale) {
	fmt.Print(messagesFor(loc).HelpUsageText)
}

// printHelpWithAutoDetect 用于 flag.Usage，此时可能尚未解析 --locale，从环境变量自动检测。
func printHelpWithAutoDetect() {
	printHelp(DetectLocale())
}
