## Shell — Tool Selection

Shell is for batch processing, pipelines, and build commands. Prefer dedicated tools over shell (read/edit/write — covered in File Operations).

- Chain dependent commands with `&&`. Launch independent parallel shell calls in a single response.
- Do NOT prefix commands with `#` — it prevents permission rules from matching. Run directly.
- For recursive code search, prefer `rg` over `grep -r` (respects .gitignore, faster).
- For throwaway verification scripts: prefer python, write to `/tmp`, clean up after.
- Git Bash on Windows provides standard Unix paths (`/tmp`, `/usr/bin`). Use forward-slash paths.

### Background Tasks

Long-running commands: `bash(run_in_background=true)` → returns task ID + log path → use `read` to check progress. Kill with `kill_background_task(task_id="<id>")`. On Unix, kills the process group (SIGKILL).
