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
	return "Task tracker for complex multi-step work. Use proactively to track progress and pending tasks. Make sure at least one task is in_progress at all times. Always provide both content (imperative) and activeForm (present continuous) for each task."
}

// Prompt 返回 todo_write 的完整使用指南（When to Use / NOT / 示例 / 规则），
// 由 Registry 拼接到 ToolSpec.Description 中发送给 LLM。
func (t *TodoWrite) Prompt() string {
	return strings.TrimSpace(`
Use this tool to create and manage a structured task list for your current coding session. This helps you track progress, organize complex tasks, and demonstrate thoroughness to the user. It also helps the user understand the progress of the task and overall progress of their requests.

## When to Use This Tool

Use this tool proactively in these scenarios:

1. Complex multi-step tasks — When a task requires 3 or more distinct steps or actions
2. Non-trivial and complex tasks — Tasks that require careful planning or multiple operations
3. User explicitly requests todo list — When the user directly asks you to use the todo list
4. User provides multiple tasks — When users provide a list of things to be done (numbered or comma-separated)
5. After receiving new instructions — Immediately capture user requirements as todos
6. When you start working on a task — Mark it as in_progress BEFORE beginning work. Ideally you should only have one todo as in_progress at a time
7. After completing a task — Mark it as completed and add any new follow-up tasks discovered during implementation

## When NOT to Use This Tool

Skip using this tool when:

1. There is only a single, straightforward task
2. The task is trivial and tracking it provides no organizational benefit
3. The task can be completed in less than 3 trivial steps
4. The task is purely conversational or informational

NOTE that you should not use this tool if there is only one trivial task to do. In this case you are better off just doing the task directly.

## Examples of When to Use the Todo List

<example>
User: I want to add a dark mode toggle to the application settings. Make sure you run the tests and build when you're done!
Assistant: *Creates todo list with items: 1) Adding dark mode toggle component, 2) Adding dark mode state management, 3) Implementing dark theme styles, 4) Updating existing components, 5) Running tests and build*
*Begins working on the first task*

<reasoning>
Multi-step feature requiring UI, state management, and styling changes. The user also explicitly requested tests and build, which the assistant added as a final verification task.
</reasoning>
</example>

<example>
User: Help me rename the function getCwd to getCurrentWorkingDirectory across my project
Assistant: *Searches codebase, finds 15 instances across 8 files*
*Creates todo list with specific items for each file that needs updating*

<reasoning>
First searched to understand scope, then upon finding multiple occurrences determined this was complex enough to warrant systematic tracking across files.
</reasoning>
</example>

## Examples of When NOT to Use the Todo List

<example>
User: How do I print 'Hello World' in Python?
Assistant: In Python, you can print "Hello World" with: print("Hello World")

<reasoning>
Single, trivial task in one step — no need to track multiple steps.
</reasoning>
</example>

<example>
User: Can you add a comment to the calculateTotal function to explain what it does?
Assistant: *Uses edit_file to add the comment directly*

<reasoning>
Single straightforward task confined to one location — tracking provides no organizational benefit.
</reasoning>
</example>

## Task States and Management

1. **Task States**: Use these states to track progress:
   - pending: Task not yet started
   - in_progress: Currently working on (limit to ONE task at a time)
   - completed: Task finished successfully

   **IMPORTANT**: Task descriptions must have two forms:
   - content: The imperative form describing what needs to be done (e.g., "Run tests", "Build the project")
   - activeForm: The present continuous form shown during execution (e.g., "Running tests", "Building the project")

2. **Task Management**:
   - Update task status in real-time as you work
   - Mark tasks complete IMMEDIATELY after finishing (don't batch completions)
   - Exactly ONE task must be in_progress at any time (not less, not more)
   - Complete current tasks before starting new ones
   - Remove tasks that are no longer relevant from the list entirely

3. **Task Completion Requirements**:
   - ONLY mark a task as completed when you have FULLY accomplished it
   - If you encounter errors, blockers, or cannot finish, keep the task as in_progress
   - When blocked, create a new task describing what needs to be resolved
   - Never mark a task as completed if:
     - Tests are failing
     - Implementation is partial
     - You encountered unresolved errors
     - You couldn't find necessary files or dependencies

4. **Task Breakdown**:
   - Create specific, actionable items
   - Break complex tasks into smaller, manageable steps
   - Use clear, descriptive task names
   - Always provide both forms:
     - content: "Fix authentication bug"
     - activeForm: "Fixing authentication bug"

When in doubt, use this tool. Being proactive with task management demonstrates attentiveness and ensures you complete all requirements successfully.
`)
}

func (t *TodoWrite) Schema() json.RawMessage { return todoWriteSchema }

// Execute 返回占位结果。实际的状态更新由 Agent Loop 拦截处理。
func (t *TodoWrite) Execute(ctx context.Context, params any) (*ToolResult, error) {
	return &ToolResult{
		Content: "todo_write requires agent loop mediation. If you see this message, the tool was called outside of a running agent loop.",
	}, nil
}
