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
            "description": "Current task status. Exactly ONE task should be in_progress at a time."
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
Create and manage a structured task list. Use proactively for complex tasks (3+ steps). Skip for single straightforward tasks or informational requests.

ALWAYS pass the COMPLETE todo list — copy the current list from the previous tool result, modify what you need (change status, add new items, remove done items), and pass the entire list back. The tool handles state tracking internally; you do NOT need to remember or pass any IDs.

content = imperative ("Fix bug"). activeForm = present continuous ("Fixing bug") — displayed with spinner during in_progress state. Both required for every task.

Max one in_progress at a time. When all tasks are completed, the list is automatically cleared.

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
