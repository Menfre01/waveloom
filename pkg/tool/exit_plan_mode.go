package tool

import (
	_ "embed"
	"context"
	"encoding/json"
)
//go:embed exit_plan_mode_prompt.md
var exitPlanModePrompt string


// ---------------------------------------------------------------------------
// ExitPlanMode — LLM 调用此工具退出规划模式并提交 plan 审批
// ---------------------------------------------------------------------------

// exitPlanModeSchema 是 exit_plan_mode 的 JSON Schema。
var exitPlanModeSchema = json.RawMessage(`{
  "type": "object",
  "properties": {},
  "required": []
}`)

// ExitPlanMode 实现 TypedTool[any]。
// 实际的 plan 审批由 Agent Loop 拦截处理，工具自身的 Execute 返回占位结果。
type ExitPlanMode struct{}

func (t *ExitPlanMode) Name() string           { return "exit_plan_mode" }
func (t *ExitPlanMode) ConcurrentSafe() bool     { return false }
func (t *ExitPlanMode) RequiresUserInteraction() bool { return true }

func (t *ExitPlanMode) Description() string {
	return "Exit plan mode when your plan is complete and ready for user approval."
}

// Prompt 返回 exit_plan_mode 使用指南，由 Registry.FormatToolPrompts() 注入 C1 system prompt。
// Prompt 返回使用指南，由 Registry.FormatToolPrompts() 注入 system prompt。
func (t *ExitPlanMode) Prompt() string { return exitPlanModePrompt }

func (t *ExitPlanMode) Schema() json.RawMessage { return exitPlanModeSchema }

// Execute 返回占位结果。实际的审批流程由 Agent Loop 在 executeToolCalls 中拦截完成。
func (t *ExitPlanMode) Execute(ctx context.Context, params any) (*ToolResult, error) {
	return &ToolResult{
		Content: `User approved the plan. Plan mode has ended. Begin implementing according to the approved plan.`,
	}, nil
}
