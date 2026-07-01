// Package slashcommand 实现 Waveloom 的本地命令解释层。
// 拦截用户输入中以 / 开头的文本，在到达 Agent Loop 之前执行本地操作。
//
// 命令只声明副作用，具体执行由 TUI 负责。
// 本包不 import Bubble Tea，不 import TUI 代码。
package slashcommand

import (
	"context"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// ── Command 接口 ──

// Command 表示一个 slash 命令。
type Command interface {
	// Name 返回命令名（不含 / 前缀），如 "new"。
	Name() string
	// Description 返回命令的简短说明。
	Description() string
	// ArgsPlaceholder 返回参数占位符（如 "model"），无参数时返回 ""。
	ArgsPlaceholder() string
	// Aliases 返回命令的别名列表，如 ["clear"]。
	Aliases() []string
	// Execute 执行命令，args 为命令名后的参数字符串。
	Execute(ctx context.Context, args string) (*Result, error)
}

// ── /new 所需 ──

// SessionCreator 由 TUI 实现，编排新 session 创建流程。
type SessionCreator interface {
	NewSession() error
}

// ── /model 所需 ──

// SettingsStore 抽象 settings.json 中 llm section 的读写。
// SaveLLM 内部实现全量 read-modify-write，确保其他 section 不丢失。
type SettingsStore interface {
	LoadLLM() (*llm.LLMSettings, error)
	SaveLLM(settings *llm.LLMSettings) error
}

// ModelLister 通过 Provider API 获取可用模型列表。
type ModelLister interface {
	ListModels(ctx context.Context) ([]llm.ModelInfo, error)
}

// ── /skill-name 所需 ──

// SkillExecutor 由 TUI 实现，通过 skill 工具加载并渲染 SKILL.md。
// 返回渲染后的 body（变量已替换、!`cmd` 已执行、附属文件清单已追加）。
type SkillExecutor interface {
	ExecuteSkill(ctx context.Context, name, args string) (body string, err error)
}

// ── Result ──

// SideEffectKind 标识命令触发的副作用类型。
type SideEffectKind string

const (
	SideEffectNone            SideEffectKind = ""
	SideEffectSessionReset    SideEffectKind = "session_reset"
	SideEffectModelSwitched   SideEffectKind = "model_switched"
	SideEffectOpenThemePicker  SideEffectKind = "open_theme_picker"
	SideEffectOpenModelPicker  SideEffectKind = "open_model_picker"
	SideEffectOpenLocalePicker SideEffectKind = "open_locale_picker"
	SideEffectInvokeSkill     SideEffectKind = "invoke_skill" // TUI: 注入 skill body → doTurn
)

// SideEffect 描述命令执行后需要 TUI 执行的副作用。
type SideEffect struct {
	Kind    SideEffectKind
	Detail  string // model_switched → 新模型名；open_model_picker → 模型列表 JSON；invoke_skill → skill body（空表示加载失败）
	Detail2 string // invoke_skill → skill name
	Detail3 string // invoke_skill → skill args
	Detail4 string // invoke_skill → error message（仅 Detail 为空时有效）
}

// Result 是命令执行的结果。
type Result struct {
	Text        string       // 显示给用户的文本（追加为 paraSystem 段落）
	SideEffects []SideEffect // TUI 需执行的副作用列表
}
