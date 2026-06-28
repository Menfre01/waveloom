package slashcommand

import "context"

// NewCommand 实现 /new 命令（别名 /clear）。
// 调用 SessionCreator.NewSession() 创建全新 session。
type NewCommand struct {
	creator SessionCreator
}

// NewNewCommand 构造 /new 命令。
func NewNewCommand(creator SessionCreator) *NewCommand {
	return &NewCommand{creator: creator}
}

// Name 返回命令名。
func (c *NewCommand) Name() string { return "new" }

// Description 返回命令说明。
func (c *NewCommand) Description() string { return "创建全新 session" }

// ArgsPlaceholder 返回参数占位符（无参数）。
func (c *NewCommand) ArgsPlaceholder() string { return "" }

// Aliases 返回别名列表。
func (c *NewCommand) Aliases() []string { return []string{"clear"} }

// Execute 调用 SessionCreator.NewSession() 创建新 session。
func (c *NewCommand) Execute(ctx context.Context, args string) (*Result, error) {
	if err := c.creator.NewSession(); err != nil {
		return &Result{Text: "创建新 session 失败: " + err.Error()}, nil
	}
	return &Result{
		Text: "新 session 已创建。",
		SideEffects: []SideEffect{
			{Kind: SideEffectSessionReset},
		},
	}, nil
}
