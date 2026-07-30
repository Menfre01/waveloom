## Shell — Tool Selection

Shell is for batch processing, pipelines, and build commands. Prefer dedicated tools over shell (read/edit/write — covered in File Operations).

- Chain dependent commands with `&&`. Launch independent parallel shell calls in a single response.
- Do NOT prefix commands with `#` — it prevents permission rules from matching. Run directly.
- For bulk multi-file scans to find WHICH files match a pattern, use `grep -r`. For single-file or targeted searches, use `read(pattern=...)` instead — it returns context in one call.
- For throwaway verification scripts: prefer python, write to `/tmp`, clean up after.
- Git Bash on Windows provides standard Unix paths (`/tmp`, `/usr/bin`). Use forward-slash paths.

### Background Tasks

Long-running commands: `bash(run_in_background=true)` → returns task ID + log path → use `read` to check progress. Kill with `kill_background_task(task_id="<id>")`. On Unix, kills the process group (SIGKILL).
