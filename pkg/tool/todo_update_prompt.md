## Todo Update

Change task status: `in_progress` (before starting) or `completed` (immediately after finishing).

- `id` is always a string like `"1"`, not a number. Get it from `todo_create`/`todo_update` results.
- Mark complete ONLY when fully done — not for partial or failing work.
- Multiple parallel completions → update all in one call.
- Non-existent IDs are silently ignored. Auto-clears when all tasks completed.
