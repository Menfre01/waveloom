## Todo Update

Use this tool to change the status of existing tasks. Status can be `in_progress` (working on it) or `completed` (done).

### When to Use
- When you start working on a task → set to `in_progress` BEFORE beginning work
- After completing a task → set to `completed` immediately

### How It Works
- `id`: The task ID from a previous `todo_create` or `todo_update` result — always a string like `"1"`, not a number
- `status`: `in_progress` or `completed`
- ID matching is exact — if the ID doesn't exist, you'll get a warning and no update will be applied
- Only ONE task should be `in_progress` at a time
- Tasks not mentioned remain unchanged (no accidental deletion)
- When ALL tasks are completed, the list auto-clears

### Task Management
- Update task status in real-time as you work
- Mark tasks complete IMMEDIATELY after finishing (don't batch completions)
- Only ONE task should be in_progress at a time — complete the current task before starting a new one

### Task Completion Requirements
- ONLY mark a task as completed when you have FULLY accomplished it
- If you encounter errors, blockers, or cannot finish, keep the task as in_progress and report to user
- Never mark a task as completed if tests are failing, implementation is partial, or there are unresolved errors

### Common Mistakes
- ❌ Multiple in_progress → complete current task first
- ❌ Using non-existent IDs → check the list for valid IDs
- ❌ Marking complete prematurely → only mark "completed" when FULLY done
- ❌ Forgetting to update status → mark completed IMMEDIATELY after finishing
