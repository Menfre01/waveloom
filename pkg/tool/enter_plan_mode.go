package tool

import (
	"context"
	"encoding/json"
	"strings"
)

// ---------------------------------------------------------------------------
// EnterPlanMode — LLM 调用此工具进入规划模式
// ---------------------------------------------------------------------------

// enterPlanModeSchema 是 enter_plan_mode 的 JSON Schema。
var enterPlanModeSchema = json.RawMessage(`{
  "type": "object",
  "properties": {},
  "required": []
}`)

// EnterPlanMode 实现 TypedTool[any]。
// 实际的状态切换由 Agent Loop 拦截处理，工具自身的 Execute 返回占位结果。
type EnterPlanMode struct{}

func (t *EnterPlanMode) Name() string           { return "enter_plan_mode" }
func (t *EnterPlanMode) ConcurrentSafe() bool     { return false }
func (t *EnterPlanMode) RequiresUserInteraction() bool { return true }

func (t *EnterPlanMode) Description() string {
	return strings.TrimSpace(`
Enter plan mode for complex tasks requiring exploration and design before coding.
Use this proactively when:
- Implementing new features with architectural ambiguity
- Multiple valid approaches exist and the choice matters
- Changes affect 3+ files or restructure existing behavior
- User preferences matter for the implementation approach

Skip plan mode for:
- Single-line or few-line fixes (typos, obvious bugs)
- Tasks with very specific, detailed instructions from the user
- Adding a single function with clear requirements

In plan mode you CAN: read/search/explore code, ask questions, use shell for analysis commands (lint, test, version checks, git log/diff), and write/edit the plan file.
In plan mode you CANNOT: write or edit source files — those operations will be blocked by the permission system and must wait until after plan approval.

Exit with exit_plan_mode when your plan is complete and ready for review.
`)
}

func (t *EnterPlanMode) Schema() json.RawMessage { return enterPlanModeSchema }

// Execute 返回占位结果。实际的状态切换由 Agent Loop 在 executeToolCalls 中拦截完成。
func (t *EnterPlanMode) Execute(ctx context.Context, params any) (*ToolResult, error) {
	return &ToolResult{
		Content: `Entered plan mode. You should now focus on exploring the codebase and designing an implementation approach.

In plan mode, you should:
1. Thoroughly explore the codebase to understand existing patterns
2. Identify similar features and architectural approaches
3. Consider multiple approaches and their trade-offs
4. Use ask_user_question if you need to clarify the approach
5. Design a concrete implementation strategy
6. Write your plan to the plan file (shown in [plan:start #xxxx])
7. When ready, use exit_plan_mode to present your plan for approval

Remember: DO NOT write or edit any source files — these operations will be blocked by the permission system. Use write_file only for the plan file. Use shell for analysis commands (lint, test, version checks, git log/diff) — destructive commands will be blocked.`,
	}, nil
}
