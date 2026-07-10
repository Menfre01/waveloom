package slashcommand

import "context"

// RewindCommand 实现 /rewind 命令。
// 返回 SideEffectOpenRewind，由 TUI 弹出 rewind 消息选择覆盖层。
type RewindCommand struct {
	messages *SlashMessages
}

// NewRewindCommand 构造 /rewind 命令。
func NewRewindCommand(messages *SlashMessages) *RewindCommand {
	return &RewindCommand{messages: messages}
}

// Name 返回命令名。
func (c *RewindCommand) Name() string { return "rewind" }

// Description 返回命令说明。
func (c *RewindCommand) Description() string { return c.messages.RewindDescription }

// ArgsPlaceholder 返回参数占位符（无参数）。
func (c *RewindCommand) ArgsPlaceholder() string { return "" }

// Aliases 返回别名列表（无别名）。
func (c *RewindCommand) Aliases() []string { return nil }

// Execute 返回 SideEffectOpenRewind。
func (c *RewindCommand) Execute(ctx context.Context, args string) (*Result, error) {
	return &Result{
		SideEffects: []SideEffect{
			{Kind: SideEffectOpenRewind},
		},
	}, nil
}
