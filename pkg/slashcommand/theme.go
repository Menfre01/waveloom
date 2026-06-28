package slashcommand

import "context"

// ThemeCommand 实现 /theme 命令。
// 返回 SideEffectOpenThemePicker，由 TUI 弹出主题选择列表。
type ThemeCommand struct{}

// NewThemeCommand 构造 /theme 命令（零依赖）。
func NewThemeCommand() *ThemeCommand { return &ThemeCommand{} }

// Name 返回命令名。
func (c *ThemeCommand) Name() string { return "theme" }

// Description 返回命令说明。
func (c *ThemeCommand) Description() string { return "选择主题（Auto / Dark / Light）" }

// ArgsPlaceholder 返回参数占位符（无参数）。
func (c *ThemeCommand) ArgsPlaceholder() string { return "" }

// Aliases 返回别名列表（无别名）。
func (c *ThemeCommand) Aliases() []string { return nil }

// Execute 返回 SideEffectOpenThemePicker。
func (c *ThemeCommand) Execute(ctx context.Context, args string) (*Result, error) {
	return &Result{
		SideEffects: []SideEffect{
			{Kind: SideEffectOpenThemePicker},
		},
	}, nil
}
