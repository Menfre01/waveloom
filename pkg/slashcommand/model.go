package slashcommand

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// ModelCommand 实现 /model 命令。
// 无参：通过 ModelLister 获取可用模型列表，返回 SideEffectOpenModelPicker。
// 有参：校验模型名后写入 settings，返回 SideEffectModelSwitched。
type ModelCommand struct {
	store        SettingsStore
	lister       ModelLister
	currentModel string
	messages     *SlashMessages
}

// NewModelCommand 构造 /model 命令。
func NewModelCommand(store SettingsStore, lister ModelLister, currentModel string, messages *SlashMessages) *ModelCommand {
	return &ModelCommand{store: store, lister: lister, currentModel: currentModel, messages: messages}
}

// Name 返回命令名。
func (c *ModelCommand) Name() string { return "model" }

// Description 返回命令说明。
func (c *ModelCommand) Description() string { return c.messages.ModelDescription }

// ArgsPlaceholder 返回参数占位符。
func (c *ModelCommand) ArgsPlaceholder() string { return "model" }

// Aliases 返回别名列表（无别名）。
func (c *ModelCommand) Aliases() []string { return nil }

// Execute 执行 /model 命令。
func (c *ModelCommand) Execute(ctx context.Context, args string) (*Result, error) {
	if args == "" {
		return c.executeNoArgs(ctx)
	}
	return c.executeWithArgs(ctx, args)
}

func (c *ModelCommand) executeNoArgs(ctx context.Context) (*Result, error) {
	models, err := c.lister.ListModels(ctx)
	if err != nil {
		return &Result{
			Text: fmt.Sprintf(c.messages.ModelListFailed, err),
		}, nil
	}

	// 序列化模型列表到 Detail
	data, err := json.Marshal(models)
	if err != nil {
		return nil, fmt.Errorf("序列化模型列表失败: %w", err)
	}

	return &Result{
		SideEffects: []SideEffect{
			{Kind: SideEffectOpenModelPicker, Detail: string(data)},
		},
	}, nil
}

func (c *ModelCommand) executeWithArgs(ctx context.Context, name string) (*Result, error) {
	models, err := c.lister.ListModels(ctx)
	if err != nil {
		return &Result{
			Text: c.messages.ModelListFailedNoNet,
		}, nil
	}

	// 校验 name 是否在可用列表中
	if !modelInList(models, name) {
		return &Result{
			Text: fmt.Sprintf(c.messages.ModelUnknown, name),
		}, nil
	}

	settings, err := c.store.LoadLLM()
	if err != nil {
		return &Result{
			Text: fmt.Sprintf(c.messages.ModelConfigReadFailed, err),
		}, nil
	}

	settings.Model = name
	if err := c.store.SaveLLM(settings); err != nil {
		return &Result{
			Text: fmt.Sprintf(c.messages.ModelConfigSaveFailed, err),
		}, nil
	}

	return &Result{
		Text: fmt.Sprintf(c.messages.ModelSwitched, name),
		SideEffects: []SideEffect{
			{Kind: SideEffectModelSwitched, Detail: name},
		},
	}, nil
}

// modelInList 检查模型 ID 是否在列表中。
func modelInList(models []llm.ModelInfo, id string) bool {
	for _, m := range models {
		if m.ID == id {
			return true
		}
	}
	return false
}
