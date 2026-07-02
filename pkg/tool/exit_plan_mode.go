package tool

import (
	"context"
	"encoding/json"
	"strings"
)

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
	return strings.TrimSpace(`
Exit plan mode when your plan is complete and ready for user approval.

## Before Using This Tool
- Write your plan to the plan file first (use write_file with the plan file path shown in [plan:start #xxxx])
- Ensure your plan is complete and unambiguous
- Resolve any open questions with ask_user_question BEFORE calling exit_plan_mode

## How This Tool Works
- This tool reads the plan from the file you wrote
- The user will see the plan content and approve or request changes
- If approved, you return to normal mode and can begin implementation
- If rejected, you stay in plan mode to revise the plan

Do NOT use ask_user_question to ask "is my plan ready?" or "should I proceed?" — 
that's exactly what this tool does.
`)
}

func (t *ExitPlanMode) Schema() json.RawMessage { return exitPlanModeSchema }

// Execute 返回占位结果。实际的审批流程由 Agent Loop 在 executeToolCalls 中拦截完成。
func (t *ExitPlanMode) Execute(ctx context.Context, params any) (*ToolResult, error) {
	return &ToolResult{
		Content: `User approved the plan. Plan mode has ended. Begin implementing according to the approved plan.`,
	}, nil
}
