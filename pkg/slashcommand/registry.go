package slashcommand

import (
	"fmt"
	"sort"
	"strings"
)

// CommandInfo 是命令的公开信息（给 /help 和自动补全使用）。
type CommandInfo struct {
	Name        string
	Aliases     []string
	Description string
	Args        string // 参数占位符，如 "model"；无参数时为空
}

// Registry 管理所有注册的 slash 命令。
// 构造期注册，运行期不可变。
type Registry struct {
	commands map[string]Command // name → cmd
	aliases  map[string]string  // alias → name
}

// NewRegistry 创建空的 Registry。
func NewRegistry() *Registry {
	return &Registry{
		commands: make(map[string]Command),
		aliases:  make(map[string]string),
	}
}

// Register 注册一个命令。若命令名或别名与已有命令冲突，panic。
func (r *Registry) Register(cmd Command) {
	name := strings.ToLower(cmd.Name())
	if _, exists := r.commands[name]; exists {
		panic(fmt.Sprintf("slashcommand: duplicate command name %q", name))
	}
	r.commands[name] = cmd

	for _, alias := range cmd.Aliases() {
		aliasLower := strings.ToLower(alias)
		if _, exists := r.aliases[aliasLower]; exists {
			panic(fmt.Sprintf("slashcommand: duplicate alias %q", aliasLower))
		}
		r.aliases[aliasLower] = name
	}
}

// Match 根据用户输入返回匹配的命令和参数。
// 输入以 "/" 开头，Match 先去掉 "/" 前缀，再按首个空格分割命令名和参数。
// 命令名大小写不敏感。无匹配时返回 nil, ""。
func (r *Registry) Match(input string) (Command, string) {
	input = strings.TrimLeft(input, "/")
	if input == "" {
		return nil, ""
	}

	// 首个空格分割
	cmdName := input
	args := ""
	if idx := strings.Index(input, " "); idx >= 0 {
		cmdName = input[:idx]
		args = strings.TrimSpace(input[idx+1:])
	}

	cmdName = strings.ToLower(cmdName)

	// 精确匹配命令名
	if cmd, ok := r.commands[cmdName]; ok {
		return cmd, args
	}

	// 尝试别名
	if name, ok := r.aliases[cmdName]; ok {
		return r.commands[name], args
	}

	return nil, ""
}

// List 返回所有注册命令的公开信息，按名称排序。
func (r *Registry) List() []CommandInfo {
	infos := make([]CommandInfo, 0, len(r.commands))
	for _, cmd := range r.commands {
		aliases := cmd.Aliases()
		// 排序别名副本
		sorted := make([]string, len(aliases))
		copy(sorted, aliases)
		sort.Strings(sorted)
		infos = append(infos, CommandInfo{
			Name:        cmd.Name(),
			Aliases:     sorted,
			Description: cmd.Description(),
			Args:        cmd.ArgsPlaceholder(),
		})
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Name < infos[j].Name
	})
	return infos
}
