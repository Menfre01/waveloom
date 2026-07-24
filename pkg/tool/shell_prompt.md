## Shell Usage

Prefer dedicated tools over shell:
  - Read files: read (not cat/head/tail)
  - Write files: write (not echo >/cat <<EOF)
  - Edit files: edit (not sed/awk)
  Exception for files >10MB (rejected by read): use head/tail/grep to read, sed/awk to edit.
When rg is available (see ## Environment), prefer rg (ripgrep) over grep -r for recursive content search — respects .gitignore, faster.
Keep commands to a SINGLE LINE. Chain dependent commands with && — do NOT use newlines or \ line continuation.
If you absolutely must split, escape newlines as \\\n in JSON (three backslashes + n).
Do NOT prefix commands with # comment lines — they prevent permission rules from matching the actual command. Run the command directly.

Launch multiple independent commands as parallel shell calls in a single response.
Chain dependent commands with &&, not newlines.

For throwaway verification scripts: prefer python, write to a temp file, and clean up after.
  Git Bash on Windows provides standard Unix paths (/tmp, /usr/bin). Use forward-slash paths.

Examples:
  {"command":"python /tmp/check.py && rm /tmp/check.py"}  — Unix/macOS or Windows (Git Bash)

  {"command":"ls", "working_dir":"/tmp"}                   — runs in /tmp, clean

### Background Tasks

Long-running commands can run in the background via `bash(run_in_background=true)`. The tool returns immediately with a task ID and log path — use `read` to check progress.

Use `kill_background_task(task_id="<id>")` to stop a running background task. The task ID is shown in background-task notifications (`<background-task id="..."/>`) and in the original bash tool response.

On Unix, kills the entire process group (SIGKILL). On Windows, kills the process. If the task is already completed or not found, returns an appropriate message.