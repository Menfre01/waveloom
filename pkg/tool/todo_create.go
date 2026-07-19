package tool

import (
	_ "embed"
	"context"
	"encoding/json"
)

//go:embed todo_create_prompt.md
var todoCreatePrompt string

var todoCreateSchema = json.RawMessage(`{
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
            "description": "Imperative form describing what needs to be done."
          },
          "description": {
            "type": "string",
            "description": "Optional details to help remember what this task involves."
          }
        },
        "required": ["content"]
      }
    }
  },
  "required": ["todos"]
}`)

type TodoCreate struct{}

func (t *TodoCreate) Name() string                 { return "todo_create" }
func (t *TodoCreate) ConcurrentSafe() bool          { return false }
func (t *TodoCreate) RequiresUserInteraction() bool { return false }
func (t *TodoCreate) Description() string {
	return "Create new tasks in the todo list. Tasks are created with status 'pending'. Use todo_update to change status."
}
func (t *TodoCreate) Prompt() string          { return todoCreatePrompt }
func (t *TodoCreate) Schema() json.RawMessage { return todoCreateSchema }
func (t *TodoCreate) Execute(ctx context.Context, params any) (*ToolResult, error) {
	return &ToolResult{Content: "todo_create requires agent loop mediation."}, nil
}
