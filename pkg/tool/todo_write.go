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
Task tracker for complex multi-step work.

Fields:
- content: imperative, WHAT to do ("Fix login bug")
- activeForm: present continuous, shown during execution ("Fixing login bug")
- status: pending → in_progress → completed (multiple can be in_progress simultaneously)
- description: optional longer description with task details, context, or notes

RULES:
1. After receiving new instructions — capture all tasks before starting work.
2. ALWAYS pass the COMPLETE list — copy from previous result, change only status fields. Never drop items, never change content or activeForm between calls.
3. Mark completed immediately, set next to in_progress before starting. Auto-clears when all completed.

Example (initialize 2 tasks, start working on first):
  todo_write([{content:"Fix login",status:"in_progress",activeForm:"Fixing login"},{content:"Add tests",status:"pending",activeForm:"Adding tests"}])

→ When to use / not use: see system prompt ## Todo List.
`)
}

func (t *TodoWrite) Schema() json.RawMessage { return todoWriteSchema }

// Execute 返回占位结果。实际的状态更新由 Agent Loop 拦截处理。
func (t *TodoWrite) Execute(ctx context.Context, params any) (*ToolResult, error) {
	return &ToolResult{
		Content: "todo_write requires agent loop mediation. If you see this message, the tool was called outside of a running agent loop.",
	}, nil
}
