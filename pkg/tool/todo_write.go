package tool

import (
	_ "embed"
	"context"
	"encoding/json"
)
//go:embed todo_write_prompt.md
var todoWritePrompt string


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
          "id": {
            "type": "string",
            "description": "Task ID for precise status updates. Omit when creating new tasks — the system assigns an ID automatically. Include the ID returned from a previous todo_write result to update an existing task by ID rather than by content."
          },
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
	return "Task tracker for complex multi-step work. Create and manage a structured task list to track progress, organize tasks, and demonstrate thoroughness. Rules: see system prompt ## Todo List."
}

// Prompt 返回 todo_write 的完整使用指南（When to Use / NOT / 示例 / 规则），
// 由 Registry 拼接到 ToolSpec.Description 中发送给 LLM。
// Prompt 返回使用指南，由 Registry.FormatToolPrompts() 注入 system prompt。
func (t *TodoWrite) Prompt() string { return todoWritePrompt }

func (t *TodoWrite) Schema() json.RawMessage { return todoWriteSchema }

// Execute 返回占位结果。实际的状态更新由 Agent Loop 拦截处理。
func (t *TodoWrite) Execute(ctx context.Context, params any) (*ToolResult, error) {
	return &ToolResult{
		Content: "todo_write requires agent loop mediation. If you see this message, the tool was called outside of a running agent loop.",
	}, nil
}
