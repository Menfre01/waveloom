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

	// 校验 name 是否在可用列表中。proplan 是特殊选择值(不在 provider
	// 模型列表中,对齐 Claude Code 的 opusplan alias),跳过列表校验。
	if name != llm.ModelChoiceProPlan && !modelInList(models, name) {
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
	// REGRESSION: settings 无 llm 段时 LoadLLM 返回 nil,SetModel 曾 nil panic。
	if settings == nil {
		settings = &llm.LLMSettings{}
	}

	// proplan 需要 model/sub_model 锚点(plan 与日常模型来源)。缺失时拒绝,
	// 防磁盘持久化 proplan 后启动报错或 "proplan" 泄漏到 API。
	// 锚点可能只配置在 profile 内(Merge 白名单不含 SubModel),需解析 profile
	// 后判断;校验用副本,持久化仍用原 settings,避免合并结果污染项目文件。
	resolved := *settings
	resolved.ResolveProfile()
	if name == llm.ModelChoiceProPlan &&
		(resolved.Model == "" || resolved.SubModel == "" ||
			resolved.Model == llm.ModelChoiceProPlan || resolved.SubModel == llm.ModelChoiceProPlan) {
		return &Result{
			Text: c.messages.ModelProPlanAnchorMissing,
		}, nil
	}

	// 持久化:写入目标 = 项目文件自身(LoadProjectLLM,不合并全局),防止
	// 全局配置被复制进项目文件 / 空 profile 覆盖全局完整配置。
	project, err := c.store.LoadProjectLLM()
	if err != nil {
		return &Result{
			Text: fmt.Sprintf(c.messages.ModelConfigReadFailed, err),
		}, nil
	}
	if project == nil {
		project = &llm.LLMSettings{}
	}
	// 项目文件已有该 provider 的 profile → 写 profile.curr_model;
	// 无 → 写顶层 curr_model(合并时全局完整 profile 保留,顶层作 fallback,
	// 避免创建空 profile 覆盖全局)。
	if p := project.Profiles[settings.Provider]; p != nil {
		p.CurrModel = name
	} else {
		project.CurrModel = name
	}
	if err := c.store.SaveLLM(project); err != nil {
		return &Result{
			Text: fmt.Sprintf(c.messages.ModelConfigSaveFailed, err),
		}, nil
	}

	text := fmt.Sprintf(c.messages.ModelSwitched, name)

	return &Result{
		Text: text,
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
