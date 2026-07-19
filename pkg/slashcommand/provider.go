package slashcommand

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// ProviderCommand 实现 /provider 命令。
// 无参：列出所有已配置的 provider 及当前使用的 provider。
// 有参：切换到指定 provider，解析对应的 profile，落盘并通知 TUI 重建 Client。
type ProviderCommand struct {
	store    SettingsStore
	messages *SlashMessages
}

// NewProviderCommand 构造 /provider 命令。
func NewProviderCommand(store SettingsStore, messages *SlashMessages) *ProviderCommand {
	return &ProviderCommand{store: store, messages: messages}
}

// Name 返回命令名。
func (c *ProviderCommand) Name() string { return "provider" }

// Description 返回命令说明。
func (c *ProviderCommand) Description() string { return c.messages.ProviderDescription }

// ArgsPlaceholder 返回参数占位符。
func (c *ProviderCommand) ArgsPlaceholder() string { return "provider" }

// Aliases 返回别名列表。
func (c *ProviderCommand) Aliases() []string { return nil }

// Execute 执行 /provider 命令。
func (c *ProviderCommand) Execute(ctx context.Context, args string) (*Result, error) {
	if args == "" {
		return c.executeNoArgs()
	}
	return c.executeWithArgs(args)
}

func (c *ProviderCommand) executeNoArgs() (*Result, error) {
	settings, err := c.store.LoadLLM()
	if err != nil {
		return &Result{
			Text: fmt.Sprintf(c.messages.ProviderConfigReadFailed, err),
		}, nil
	}

	// 收集已配置的 provider 名（profiles 的 key + 已知内置类型）
	seen := make(map[string]bool)
	for name := range settings.Profiles {
		if name != "" {
			seen[name] = true
		}
	}
	// 确保当前 provider 也在列表中
	if settings.Provider != "" {
		seen[settings.Provider] = true
	}

	if len(seen) == 0 {
		return &Result{Text: c.messages.ProviderNoProfiles}, nil
	}

	providers := make([]string, 0, len(seen))
	for name := range seen {
		providers = append(providers, name)
	}
	sort.Strings(providers)

	current := settings.Provider
	var lines []string
	for _, p := range providers {
		marker := " "
		if p == current {
			marker = "*"
		}
		lines = append(lines, fmt.Sprintf("  %s %s", marker, p))
	}

	modelHint := ""
	if settings.Model != "" {
		modelHint = settings.Model
	} else if p, ok := settings.Profiles[current]; ok && p != nil && p.Model != "" {
		modelHint = p.Model
	}
	text := fmt.Sprintf(c.messages.ProviderList, current, modelHint)
	text += "\n\n" + fmt.Sprintf(c.messages.ProviderAvailable, strings.Join(lines, "\n"))

	return &Result{Text: text}, nil
}

func (c *ProviderCommand) executeWithArgs(name string) (*Result, error) {
	settings, err := c.store.LoadLLM()
	if err != nil {
		return &Result{
			Text: fmt.Sprintf(c.messages.ProviderConfigReadFailed, err),
		}, nil
	}

	// 检查目标 provider 是否合法：在 profiles 中，或是已知内置类型
	_, hasProfile := settings.Profiles[name]
	if !hasProfile && !isKnownProvider(name) {
		return &Result{
			Text: fmt.Sprintf(c.messages.ProviderUnknown, name),
		}, nil
	}

	oldProvider := settings.Provider

	settings.Provider = name

	if err := c.store.SaveLLM(settings); err != nil {
		return &Result{
			Text: fmt.Sprintf(c.messages.ProviderConfigSaveFailed, err),
		}, nil
	}

	text := fmt.Sprintf(c.messages.ProviderSwitched, oldProvider, name)
	if settings.Model != "" {
		text += "\n" + fmt.Sprintf(c.messages.ProviderModelNotice, settings.Model)
	}

	return &Result{
		Text: text,
		SideEffects: []SideEffect{
			{Kind: SideEffectProviderSwitched, Detail: name},
		},
	}, nil
}

// isKnownProvider 判断是否为 Waveloom 内置支持的 provider 类型。
func isKnownProvider(name string) bool {
	for _, pt := range []llm.ProviderType{llm.ProviderDeepSeek, llm.ProviderOpenAI, llm.ProviderKimi} {
		if string(pt) == name {
			return true
		}
	}
	return false
}
