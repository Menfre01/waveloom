package slashcommand

import (
	"context"
	"fmt"
	"strings"
)

// SessionRenamer 由 TUI 实现,为当前 session 改名并落盘。
type SessionRenamer interface {
	// RenameSession 将当前 session 重命名为 name 并持久化(JSON + recent.json)。
	RenameSession(name string) error
	// CurrentName 返回当前 session 名称(未命名时为空字符串)。
	CurrentName() string
}

// RenameCommand 实现 /rename 命令。
// 有参数:重命名当前 session;无参数:显示当前 session 名称。
type RenameCommand struct {
	renamer  SessionRenamer
	messages *SlashMessages
}

// NewRenameCommand 构造 /rename 命令。
func NewRenameCommand(renamer SessionRenamer, messages *SlashMessages) *RenameCommand {
	return &RenameCommand{renamer: renamer, messages: messages}
}

// Name 返回命令名。
func (c *RenameCommand) Name() string { return "rename" }

// Description 返回命令说明。
func (c *RenameCommand) Description() string { return c.messages.RenameDescription }

// ArgsPlaceholder 返回参数占位符。
func (c *RenameCommand) ArgsPlaceholder() string { return "name" }

// Aliases 返回别名列表(无别名)。
func (c *RenameCommand) Aliases() []string { return nil }

// Execute 执行重命名。
func (c *RenameCommand) Execute(ctx context.Context, args string) (*Result, error) {
	name := strings.TrimSpace(args)
	if name == "" {
		current := c.renamer.CurrentName()
		if current == "" {
			return &Result{Text: c.messages.RenameUnnamed}, nil
		}
		return &Result{Text: fmt.Sprintf(c.messages.RenameCurrent, current)}, nil
	}
	if err := c.renamer.RenameSession(name); err != nil {
		return &Result{Text: fmt.Sprintf(c.messages.RenameFailed, err)}, nil
	}
	// 成功文案使用规范化后的当前名称(remaner 内部可能截断/过滤控制字符),
	// 保证显示与落盘一致,避免将未规范化字符渲染进 TUI 段落。
	return &Result{Text: fmt.Sprintf(c.messages.RenameDone, c.renamer.CurrentName())}, nil
}
