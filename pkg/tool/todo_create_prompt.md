## Todo Create

Use this tool to create new tasks in your todo list. All tasks start as `pending`.

### When to Use
- After receiving new instructions — capture user requirements as todos
- When planning multi-step work that needs tracking
- User explicitly requests todo list
- User provides multiple tasks (numbered or comma-separated)

### When NOT to Use
- To update task status — use `todo_update` instead
- For single, straightforward tasks that don't need tracking
- The task can be completed in less than 3 trivial steps

### How It Works
- Each item needs a `content` (what needs to be done) and optionally a `description` (details to help remember)
- Tasks are created with status `pending` and assigned an automatic ID
- Use `todo_update` with the returned ID to change status to `in_progress` or `completed`

### Task Lifecycle
```
todo_create → [pending]     → task is planned
todo_update → [in_progress] → start working
todo_update → [completed]   → done
```

### Task Breakdown
- Each task should represent a meaningful unit of work, not a single command
- "Run make build" is not a task — "Build and verify the project" is
- Use clear, descriptive names
- content: Imperative form (e.g., "Add dark mode toggle component")
- description: Optional context/details to help remember the task's purpose

### Common Mistakes
- ❌ Creating duplicates of existing tasks → check the list first before creating
- ❌ "Run make build" as a task → single commands are NOT tasks
- ❌ Using todo_create to update status → use todo_update instead

When in doubt, use this tool. Being proactive with task management demonstrates attentiveness.
