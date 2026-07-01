package slashcommand

import (
	"context"
)

// SkillDescriptor 是 skill 的轻量标识信息，由上层在注册时从 skill.SkillInfo 转换后传入。
// 定义此类型消除 slashcommand → skill 的编译期依赖。
type SkillDescriptor struct {
	Name        string // skill 名
	Description string // 简短描述
	Args        string // 参数占位符
}

// SkillCommand 将 user-invocable skill 包装为 SlashCommand。
// 不直接调用 skill.Loader —— 而是通过 SkillExecutor 接口委托给 TUI 侧实现，
// TUI 侧通过 tool.Registry.Execute("skill", ...) 完成加载和渲染，
// 确保用户触发和 LLM 触发走相同的代码路径。
type SkillCommand struct {
	info     SkillDescriptor
	executor SkillExecutor
}

// NewSkillCommand 构造 SkillCommand。
func NewSkillCommand(info SkillDescriptor, executor SkillExecutor) *SkillCommand {
	return &SkillCommand{info: info, executor: executor}
}

func (c *SkillCommand) Name() string             { return c.info.Name }
func (c *SkillCommand) Description() string      { return c.info.Description }
func (c *SkillCommand) ArgsPlaceholder() string  { return c.info.Args }
func (c *SkillCommand) Aliases() []string        { return nil }

func (c *SkillCommand) Execute(ctx context.Context, args string) (*Result, error) {
	body, err := c.executor.ExecuteSkill(ctx, c.info.Name, args)
	if err != nil {
		// 加载失败：通过 SideEffectInvokeSkill + 空 Detail 传递错误，
		// TUI 侧渲染为 paraTool 错误态（红色 │ 前缀），而非 paraSystem 通知。
		return &Result{
			SideEffects: []SideEffect{
				{
					Kind:    SideEffectInvokeSkill,
					Detail:  "",              // 空 body = 加载失败
					Detail2: c.info.Name,     // skill name
					Detail3: args,             // skill args
					Detail4: err.Error(),      // 错误消息
				},
			},
		}, nil
	}

	return &Result{
		Text: "", // 不在 paraSystem 中显示，通过 paraTool 渲染
		SideEffects: []SideEffect{
			{
				Kind:    SideEffectInvokeSkill,
				Detail:  body,           // 渲染后的 skill body
				Detail2: c.info.Name,    // skill name
				Detail3: args,            // skill args
			},
		},
	}, nil
}
