package slashcommand

import (
	"context"

	"github.com/Menfre01/waveloom/pkg/skill"
)

// SkillCommand 将 user-invocable skill 包装为 SlashCommand。
// 不直接调用 skill.Loader —— 而是通过 SkillExecutor 接口委托给 TUI 侧实现，
// TUI 侧通过 tool.Registry.Execute("skill", ...) 完成加载和渲染，
// 确保用户触发和 LLM 触发走相同的代码路径。
type SkillCommand struct {
	info     skill.SkillInfo
	executor SkillExecutor
}

// NewSkillCommand 构造 SkillCommand。
func NewSkillCommand(info skill.SkillInfo, executor SkillExecutor) *SkillCommand {
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
