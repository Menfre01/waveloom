package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Menfre01/waveloom/pkg/acp"
	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/skill"
	"github.com/Menfre01/waveloom/pkg/slashcommand"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// acpCommandRunner 实现 acp.CommandRunner:用 slashcommand.Registry 执行
// ACP 场景可用的命令(无 TUI overlay 依赖)。
type acpCommandRunner struct {
	registry *slashcommand.Registry
}

func (r *acpCommandRunner) Run(ctx context.Context, input string) (string, string, bool) {
	cmd, args := r.registry.Match(input)
	if cmd == nil {
		return "", "", false
	}
	result, err := cmd.Execute(ctx, args)
	if err != nil {
		return fmt.Sprintf("error: %v", err), "", true
	}
	// skill 命令:SideEffectInvokeSkill → 注入 skill body 到 prompt
	for _, se := range result.SideEffects {
		if se.Kind == slashcommand.SideEffectInvokeSkill {
			return "", se.Detail, true
		}
	}
	// 其他 SideEffect(TUI overlay 类,ACP 下不应注册)忽略,回退文本
	return result.Text, "", true
}

func (r *acpCommandRunner) AvailableCommands() []acp.AvailableCommand {
	infos := r.registry.List()
	out := make([]acp.AvailableCommand, 0, len(infos))
	for _, ci := range infos {
		cmd := acp.AvailableCommand{
			Name:        ci.Name,
			Description: ci.Description,
		}
		if ci.Args != "" {
			cmd.Input = &acp.AvailableCommandInput{Kind: "unstructured"}
		}
		out = append(out, cmd)
	}
	return out
}

// acpSettingsStore 实现 slashcommand.SettingsStore(读写 settings.json 的 llm 段)。
// SaveLLM 全量 read-modify-write,保留其他 section。
type acpSettingsStore struct {
	projectPath string
	globalPath  string
}

func (s *acpSettingsStore) LoadLLM() (*llm.LLMSettings, error) {
	global, err := llm.LoadSettingsIfExists(s.globalPath)
	if err != nil {
		panic(fmt.Sprintf("settings parse error in %s: %v", s.globalPath, err))
	}
	project, err := llm.LoadSettingsIfExists(s.projectPath)
	if err != nil {
		panic(fmt.Sprintf("settings parse error in %s: %v", s.projectPath, err))
	}
	return llm.MergeLLMSettings(global, project), nil
}

func (s *acpSettingsStore) SaveLLM(settings *llm.LLMSettings) error {
	return writeFullSettings(s.projectPath, settings, "", "")
}

// acpSkillExecutor 实现 slashcommand.SkillExecutor:通过 skill 工具加载
// SKILL.md(用户触发与 LLM 调用走相同代码路径)。
type acpSkillExecutor struct {
	registry tool.Registry
}

func (e *acpSkillExecutor) ExecuteSkill(ctx context.Context, name, args string) (string, error) {
	paramsJSON, err := json.Marshal(tool.SkillParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	result, err := e.registry.Execute(ctx, "skill", paramsJSON)
	if err != nil {
		return "", err
	}
	if result.Error != nil {
		return "", fmt.Errorf("%s", result.Error.Message)
	}
	return result.Content, nil
}

// newACPCommandRunner 构造 ACP 命令执行器。
// 仅注册无 TUI overlay 依赖的命令:help / model(有参)/ provider(有参)/ skill。
// theme/locale/rewind(纯 overlay)与 new(session 归客户端管)不注册。
func newACPCommandRunner(registry tool.Registry, settingsStore slashcommand.SettingsStore, lister slashcommand.ModelLister, currentModel string, loader *skill.Loader, loc Locale) *acpCommandRunner {
	sm := slashMessagesFrom(messagesFor(loc))
	r := slashcommand.NewRegistry()
	runner := &acpCommandRunner{registry: r}

	// /help
	r.Register(slashcommand.NewHelpCommand(r, sm))
	// /model(有参切换模型;无参在 ACP 下返回提示)
	r.Register(slashcommand.NewModelCommand(settingsStore, lister, currentModel, sm))
	// /provider(有参切换 provider)
	r.Register(slashcommand.NewProviderCommand(settingsStore, sm))

	// /skill 命令(user-invocable skills,经 skill 工具加载)
	if loader != nil {
		skills, _ := loader.List()
		executor := &acpSkillExecutor{registry: registry}
		for _, info := range skills {
			if !info.UserInvocable {
				continue
			}
			if r.HasCommand(info.Name) {
				continue // 与内置命令同名,跳过
			}
			r.Register(slashcommand.NewSkillCommand(slashcommand.SkillDescriptor{
				Name:        info.Name,
				Description: info.Description,
				Args:        info.Args,
			}, executor))
		}
	}
	return runner
}
