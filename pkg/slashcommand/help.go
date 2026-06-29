package slashcommand

import (
	"context"
	"strings"
)

// HelpCommand 实现 /help 命令。
// 列出所有已注册命令的表格。
type HelpCommand struct {
	registry *Registry
}

// NewHelpCommand 构造 /help 命令。
func NewHelpCommand(registry *Registry) *HelpCommand {
	return &HelpCommand{registry: registry}
}

// Name 返回命令名。
func (c *HelpCommand) Name() string { return "help" }

// Description 返回命令说明。
func (c *HelpCommand) Description() string { return "显示所有可用命令" }

// ArgsPlaceholder 返回参数占位符（无参数）。
func (c *HelpCommand) ArgsPlaceholder() string { return "" }

// Aliases 返回别名列表（无别名）。
func (c *HelpCommand) Aliases() []string { return nil }

// Execute 列出使用技巧。
func (c *HelpCommand) Execute(ctx context.Context, args string) (*Result, error) {
	var b strings.Builder
	b.WriteString("使用技巧:\n\n")
	b.WriteString("  —— 以下仅在空闲时生效 ——\n")
	b.WriteString("  输入 /         查看并补全命令（↑↓ 导航，Enter 确认，Tab 自动补全）\n")
	b.WriteString("  输入 @         引用文件（↑↓ 导航，Enter 确认，Tab 深入目录）\n")
	b.WriteString("  ↑↓              浏览输入历史\n")
	b.WriteString("  Tab / Shift+Tab 段落间导航，Enter 展开 / 折叠\n")
	b.WriteString("  Esc（双击）      清空输入框\n")
	b.WriteString("  exit            退出程序\n")
	b.WriteString("\n")
	b.WriteString("  —— 以下任意时刻生效 ——\n")
	b.WriteString("  Ctrl+G          循环切换主题（dark → light → auto）\n")
	b.WriteString("  Ctrl+E / End    跳到底部\n")
	b.WriteString("  Ctrl+C          退出\n")
	b.WriteString("  PgUp / PgDn     上下翻页\n")
	b.WriteString("  Esc（运行中）     中断当前 Agent 执行\n")
	b.WriteString("\n")
	b.WriteString("  会话结束时 session 自动保存，使用 waveloom --continue 恢复最近会话。\n")
	b.WriteString("  单次执行：waveloom \"解释这段代码\"\n")

	return &Result{Text: strings.TrimSpace(b.String())}, nil
}
