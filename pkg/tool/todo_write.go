package tool

import (
	"context"
	"encoding/json"
	"strings"
)

// ---------------------------------------------------------------------------
// TodoWrite — LLM 创建和管理结构化任务列表
// ---------------------------------------------------------------------------

// todoWriteSchema 是 todo_write 的 JSON Schema。
var todoWriteSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "todos": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "properties": {
          "content": {
            "type": "string",
            "minLength": 1,
            "description": "Imperative form describing what needs to be done (e.g., 'Run tests', 'Build the project')"
          },
          "status": {
            "type": "string",
            "enum": ["pending", "in_progress", "completed"],
            "description": "Current task status. Multiple tasks can be in_progress simultaneously when running parallel work."
          },
          "activeForm": {
            "type": "string",
            "minLength": 1,
            "description": "Present continuous form shown during execution (e.g., 'Running tests', 'Building the project')"
          },
          "description": {
            "type": "string",
            "description": "Optional longer description with task details, context, or notes"
          }
        },
        "required": ["content", "status", "activeForm"]
      }
    }
  },
  "required": ["todos"]
}`)

// TodoWrite 实现 TypedTool[any]。
// 实际的状态更新由 Agent Loop 在 executeToolCalls 中拦截处理，
// 工具自身的 Execute 返回占位结果。
type TodoWrite struct{}

func (t *TodoWrite) Name() string                 { return "todo_write" }
func (t *TodoWrite) ConcurrentSafe() bool          { return false }
func (t *TodoWrite) RequiresUserInteraction() bool { return false }

func (t *TodoWrite) Description() string {
	return strings.TrimSpace(`
Task tracker for complex multi-step work. Use only when tasks have meaningful dependencies or run in parallel — skip for linear single-file changes.

Trigger test (BOTH must be true before using this tool):
1. ≥3 steps with real dependencies (B depends on A) or parallelizable units (subagents)
2. Work spans ≥2 turns OR dispatches parallel subagents

→ If either is false, skip the todo list and just do the work.

RULES:
1. After receiving new instructions — capture all tasks before starting work.
2. Mark in_progress BEFORE beginning each task. Update status in real-time.
3. Mark completed IMMEDIATELY after finishing — never batch-mark.
4. ALWAYS pass the COMPLETE list — copy from previous result, modify, pass it all back.
5. When all tasks are completed, the list auto-clears.

content = imperative ("Fix bug"). activeForm = present continuous ("Fixing bug") — displayed with spinner during in_progress state. Both required for every task.

Multiple tasks can be in_progress simultaneously when running parallel work.

Skip this tool for: single-file fixes, linear micro-tasks (locate→edit→build), informational requests. When uncertain, skip — a missed todo is cheaper than noise.

→ Detailed rules and examples: see system prompt section "## Todo List".
`)
}

func (t *TodoWrite) Schema() json.RawMessage { return todoWriteSchema }

// Execute 返回占位结果。实际的状态更新由 Agent Loop 拦截处理。
func (t *TodoWrite) Execute(ctx context.Context, params any) (*ToolResult, error) {
	return &ToolResult{
		Content: "todo_write requires agent loop mediation. If you see this message, the tool was called outside of a running agent loop.",
	}, nil
}
