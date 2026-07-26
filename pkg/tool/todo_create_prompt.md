## Todo Create

Create tasks with status `pending`. Each needs a `content` (imperative form, meaningful unit of work) and optionally a `description`. Use the returned ID with `todo_update` to change status.

- Use for: multi-step work, new instructions, user provides task list.
- Don't use for: status updates (use `todo_update`), trivial single-command tasks, tasks completable in <3 steps.
- Only ONE task `in_progress` at a time — complete current before starting new.
- Exception: when you are about to spawn 2+ parallel agent() calls IN THE SAME TURN, you may mark those specific tasks in_progress. After the parallel agents return, immediately update their statuses to completed. Do NOT pre-mark tasks in_progress in advance just because you plan to use subagents later.
