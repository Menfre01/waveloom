package slashcommand

import (
	"context"
)

// HelpCommand 实现 /help 命令。
// 列出所有已注册命令的表格。
type HelpCommand struct {
	registry *Registry
	messages *SlashMessages
}

// NewHelpCommand 构造 /help 命令。
func NewHelpCommand(registry *Registry, messages *SlashMessages) *HelpCommand {
	return &HelpCommand{registry: registry, messages: messages}
}

// Name 返回命令名。
func (c *HelpCommand) Name() string { return "help" }

// Description 返回命令说明。
func (c *HelpCommand) Description() string { return c.messages.HelpDescription }

// ArgsPlaceholder 返回参数占位符（无参数）。
func (c *HelpCommand) ArgsPlaceholder() string { return "" }

// Aliases 返回别名列表（无别名）。
func (c *HelpCommand) Aliases() []string { return nil }

// Execute 列出使用技巧。
func (c *HelpCommand) Execute(ctx context.Context, args string) (*Result, error) {
	return &Result{Text: c.messages.HelpText}, nil
}
