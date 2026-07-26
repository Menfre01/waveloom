## Todo Update

Change task status: `in_progress` (before starting), `completed` (immediately after finishing), or `pending` (revert a mistaken status).

- `id` is always a string like `"1"`, not a number. Get it from `todo_create`/`todo_update` results.
- Mark complete ONLY when fully done — not for partial or failing work.
- Multiple parallel completions → update all in one call.
- Non-existent IDs are silently ignored. Auto-clears when all tasks completed.

- Only ONE task `in_progress` at a time — complete current before starting new. Exception: when you are about to spawn 2+ parallel agent() calls in the same turn, you may update those specific tasks to in_progress together. If you accidentally mark multiple in_progress, revert extras to `pending` immediately.
