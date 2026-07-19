package tool

import (
	_ "embed"
	"context"
	"encoding/json"
)

//go:embed todo_update_prompt.md
var todoUpdatePrompt string

var todoUpdateSchema = json.RawMessage(`{
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
            "minLength": 1,
            "description": "ID of the task to update."
          },
          "status": {
            "type": "string",
            "enum": ["in_progress", "completed"],
            "description": "New status. Only ONE task in_progress at a time."
          }
        },
        "required": ["id", "status"]
      }
    }
  },
  "required": ["todos"]
}`)

type TodoUpdate struct{}

func (t *TodoUpdate) Name() string                 { return "todo_update" }
func (t *TodoUpdate) ConcurrentSafe() bool          { return false }
func (t *TodoUpdate) RequiresUserInteraction() bool { return false }
func (t *TodoUpdate) Description() string {
	return "Update task status in the todo list. Use to mark tasks as in_progress (start working) or completed (done)."
}
func (t *TodoUpdate) Prompt() string          { return todoUpdatePrompt }
func (t *TodoUpdate) Schema() json.RawMessage { return todoUpdateSchema }
func (t *TodoUpdate) Execute(ctx context.Context, params any) (*ToolResult, error) {
	return &ToolResult{Content: "todo_update requires agent loop mediation."}, nil
}
