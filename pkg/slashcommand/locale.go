package slashcommand

import "context"

// LocaleCommand 实现 /locale 命令。
// 返回 SideEffectOpenLocalePicker，由 TUI 弹出语言选择列表。
type LocaleCommand struct {
	messages *SlashMessages
}

// NewLocaleCommand 构造 /locale 命令。
func NewLocaleCommand(messages *SlashMessages) *LocaleCommand {
	return &LocaleCommand{messages: messages}
}

// Name 返回命令名。
func (c *LocaleCommand) Name() string { return "locale" }

// Description 返回命令说明。
func (c *LocaleCommand) Description() string { return c.messages.LocaleDescription }

// ArgsPlaceholder 返回参数占位符（无参数）。
func (c *LocaleCommand) ArgsPlaceholder() string { return "" }

// Aliases 返回别名列表。
func (c *LocaleCommand) Aliases() []string { return []string{"lang"} }

// Execute 返回 SideEffectOpenLocalePicker。
func (c *LocaleCommand) Execute(ctx context.Context, args string) (*Result, error) {
	return &Result{
		SideEffects: []SideEffect{
			{Kind: SideEffectOpenLocalePicker},
		},
	}, nil
}
