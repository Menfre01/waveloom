# Waveloom 工具描述

> 本文档记录了 12 个内置工具通过 function calling 发送给 LLM 的完整内容：
> `Description()` 文本 + `Schema()` JSON 参数定义。
> 这些内容与 `pkg/tool/*.go` 中的实现严格一致，改动时请同步更新。

## 概述

| 工具 | 并发安全 | 类别 | 说明 |
|------|:--:|------|------|
| `read_file` | ✅ | 文件 | 读取文件内容（带行号） |
| `write_file` | ❌ | 文件 | 创建或覆盖文件 |
| `edit_file` | ❌ | 文件 | 基于精确字符串匹配的查找替换 |
| `bash` | ❌ | 命令 | 执行 Shell 命令（bg 变体支持后台任务） |
| `web_fetch` | ✅ | Web | 获取 URL 内容 |
| `web_search` | ✅ | Web | 搜索引擎查询（DDG 默认 + Brave 可选）|
| `ask_user_question` | ❌ | 交互 | 向用户发起选择题决策 |
| `skill` | ❌ | 系统 | 调用用户定义的 Skill |
| `enter_plan_mode` | ❌ | Plan | 进入先规划后执行的 Plan 模式 |
| `exit_plan_mode` | ❌ | Plan | 提交 Plan 审批，通过后恢复正常模式 |
| `agent` | ✅ | 子代理 | 委派复杂任务给子 agent（Fork / Cold / Explore） |
| `kill_background_task` | ✅ | 任务 | 终止后台运行的任务 |
| `todo_write` | ❌ | 任务 | 创建和管理结构化任务列表 |

> 并发安全（✅ = `ConcurrentSafe() true`）：标记为并发的工具可由 Agent Loop 在同一轮中与其他读操作并行执行。
>
> `bash_subagent` 是 `bash` 的子代理只读变体（`AllowBg: false`），不直接暴露给用户，仅子代理内部使用。

---

## read_file

```
Read a file with line numbers. Supports offset and limit parameters to read partial content. file_path must be a file, not a directory. On directory error, pick a file from the listing — use the Did you mean suggestion if present.
```

```json
{
  "type": "object",
  "properties": {
    "file_path":  { "type": "string",  "description": "File path (absolute, or relative to working_dir / workspace root). Must be a file, not a directory — use shell('ls') first to explore directories. Paths without a file extension are likely directories." },
    "offset":     { "type": "integer", "description": "Starting line number (0-based, 0 = first line, optional)" },
    "limit":      { "type": "integer", "description": "Number of lines to read (optional, default: all)" },
    "working_dir": { "type": "string",  "description": "Working directory (optional)" }
  },
  "required": ["file_path"]
}
```

## write_file

```
Create a new file or overwrite an existing file. Creates parent directories automatically. Use only for new files or complete overwrites; for partial edits use edit_file. IMPORTANT: file_path must be a file, not a directory — use ls to explore directories first.
```

```json
{
  "type": "object",
  "properties": {
    "file_path":  { "type": "string", "description": "File path (absolute, or relative to working_dir / workspace root). Must be a file, not a directory — use shell('ls') to explore directories first." },
    "content":    { "type": "string", "description": "File content to write" },
    "working_dir": { "type": "string", "description": "Working directory (optional)" }
  },
  "required": ["file_path", "content"]
}
```

## edit_file

```
Find-and-replace on an existing file by exact string match.
old_string must be unique in the file — if ambiguous, include 1-2 surrounding lines as context.
Set replace_all=true to replace every occurrence.
Whitespace and blank-line differences are auto-corrected when the match is unambiguous.
```

```json
{
  "type": "object",
  "properties": {
    "file_path":   { "type": "string",  "description": "File path (absolute, or relative to working_dir / workspace root)" },
    "old_string":  { "type": "string",  "description": "Text to replace — must match original exactly (indentation, whitespace, punctuation). Must be unique in the file; if ambiguous, include more surrounding context lines." },
    "new_string":  { "type": "string",  "description": "Replacement text. Use empty string to delete the matched text." },
    "replace_all": { "type": "boolean", "description": "Replace all occurrences (default: false, first match only)", "default": false },
    "working_dir":  { "type": "string",  "description": "Working directory (optional)" }
  },
  "required": ["file_path", "old_string", "new_string"]
}
```

---

## bash

```
Execute a shell command in a subprocess. Configurable timeout (default 120s, max 600s), captures stdout and stderr.

Set run_in_background to true for long-running commands (servers, watchers, daemons). The tool returns immediately with a task ID and log path — use read_file to check progress. Use kill_background_task to stop a running background task.

Unix/macOS uses bash -c (sh fallback), Windows uses Git Bash (bash -c).

Prefer dedicated tools over shell:
  - Read files: read_file (not cat/head/tail)
  - Write files: write_file (not echo >/cat <<EOF)
  - Edit files: edit_file (not sed/awk)

Keep commands to a SINGLE LINE. Chain dependent commands with && — do NOT use newlines or \ line continuation.
If you absolutely must split, escape newlines as \\\n in JSON (three backslashes + n).

Launch multiple independent commands as parallel shell calls in a single response.
Chain dependent commands with &&, not newlines.

Commands already run in the workspace directory.
To operate in a different directory, use the working_dir parameter.

For throwaway verification scripts: prefer python, write to a temp file, and clean up after.
  Git Bash on Windows provides standard Unix paths (/tmp, /usr/bin). Use forward-slash paths.

Examples:
  {"command":"python /tmp/check.py && rm /tmp/check.py"}  — Unix/macOS or Windows (Git Bash)
  {"command":"make build"}                                 — runs in workspace
  {"command":"ls", "working_dir":"/tmp"}                   — runs in /tmp, clean
```

```json
{
  "type": "object",
  "properties": {
    "command":            { "type": "string",  "description": "Shell command to execute. Unix/macOS uses bash -c (sh fallback), Windows uses Git Bash (bash -c)." },
    "working_dir":         { "type": "string",  "description": "Working directory (optional)" },
    "timeout_ms":         { "type": "integer", "description": "Timeout in milliseconds (default: 120000, max: 600000)" },
    "run_in_background":  { "type": "boolean", "description": "Set to true to run this command in the background. The tool returns immediately with a task ID and log path. Use read_file to check progress. The next turn will receive a completion notification.", "default": false }
  },
  "required": ["command"]
}
```

---

## web_fetch

```
Fetch content from a URL and return text. Use for consulting online docs, API references, package registries, etc. Only text-based content is supported (text/*, application/json, application/xml, application/javascript). HTML pages are automatically stripped to plain text. Binary content (images, videos, etc.) is rejected. Note: this tool only makes GET requests, and does not modify any remote resources.
```

```json
{
  "type": "object",
  "properties": {
    "url":        { "type": "string",  "description": "URL to fetch (http/https only)" },
    "max_size":   { "type": "integer", "description": "Maximum response size in bytes (optional, default: 1MB, max: 5MB)" },
    "timeout_ms": { "type": "integer", "description": "Timeout in milliseconds (optional, default: 30000, max: 120000)" }
  },
  "required": ["url"]
}
```

---

## web_search

```
Search the web and return a list of results (title, URL, snippet).
Use this to find current documentation, API references, solutions, or any information not in your training data.

After searching, use web_fetch to read the full content of promising URLs.

Backends (auto-selected):
- DuckDuckGo (default, no configuration needed)
- Brave Search (set BRAVE_API_KEY environment variable for better results)
```

```json
{
  "type": "object",
  "properties": {
    "query":       { "type": "string",  "description": "Search query — keywords, natural language, or technical terms", "minLength": 1 },
    "max_results": { "type": "integer", "description": "Maximum number of results to return (default: 10, max: 20)" }
  },
  "required": ["query"]
}
```

---

```
Ask the user one or more multiple-choice questions to gather preferences,
clarify ambiguity, or make decisions during execution. Use this tool when
you need to:

1. Gather user preferences or requirements
2. Clarify ambiguous instructions
3. Get decisions on implementation choices as you work
4. Offer choices to the user about what direction to take

Usage notes:
- Users will always be able to select "Other" to provide custom text input
- Use multiSelect: true to allow multiple answers for a question
- Put "(Recommended)" at the end of the label for the suggested option
- Question texts must be unique; option labels must be unique within each question

Do NOT use this tool to ask "is my plan ready?" or "should I proceed?" —
use exit_plan_mode for plan approval.
```

```json
{
  "type": "object",
  "properties": {
    "questions": {
      "type": "array",
      "minItems": 1,
      "maxItems": 4,
      "items": {
        "type": "object",
        "properties": {
          "question":    { "type": "string",  "description": "The complete question to ask the user. Should be clear, specific, and end with a question mark." },
          "header":      { "type": "string",  "maxLength": 12, "description": "Very short label displayed as a chip/tag. Examples: 'Auth method', 'Library', 'Approach'." },
          "options":     { "type": "array", "minItems": 2, "maxItems": 4, "items": { "type": "object", "properties": { "label": { "type": "string", "description": "The display text for this option (1-5 words). Append '(Recommended)' if this is the suggested choice." }, "description": { "type": "string", "description": "Explanation of what this option means or what will happen if chosen." } }, "required": ["label", "description"] } },
          "multiSelect": { "type": "boolean", "default": false, "description": "Set to true to allow multiple selections (for non-mutually-exclusive choices)." }
        },
        "required": ["question", "header", "options"]
      }
    }
  },
  "required": ["questions"]
}
```

---

## skill

```
Invoke a user-defined skill. Use this when a task matches an available skill's description. Call with skill name and optional arguments.
```

```json
{
  "type": "object",
  "properties": {
    "name":      { "type": "string", "description": "The skill name (e.g., 'deploy', 'summarize-changes')" },
    "arguments": { "type": "string", "description": "Optional arguments to pass to the skill" }
  },
  "required": ["name"]
}
```

---

## enter_plan_mode

```
Enter plan mode for complex tasks requiring exploration and design before coding.
Use this proactively when:
- Implementing new features with architectural ambiguity
- Multiple valid approaches exist and the choice matters
- Changes affect 3+ files or restructure existing behavior
- User preferences matter for the implementation approach

Skip plan mode for:
- Single-line or few-line fixes (typos, obvious bugs)
- Tasks with very specific, detailed instructions from the user
- Adding a single function with clear requirements

In plan mode you CAN: read/search/explore code, ask questions, use shell for
analysis commands (lint, test, version checks, git log/diff), and write/edit
the plan file.
In plan mode you CANNOT: write or edit source files — those operations will be
blocked by the permission system and must wait until after plan approval.

Exit with exit_plan_mode when your plan is complete and ready for review.
```

```json
{
  "type": "object",
  "properties": {},
  "required": []
}
```

---

## exit_plan_mode

```
Exit plan mode when your plan is complete and ready for user approval.

## Before Using This Tool
- Write your plan to the plan file first (use write_file with the plan file
  path shown in [plan:start #xxxx])
- Ensure your plan is complete and unambiguous
- Resolve any open questions with ask_user_question BEFORE calling exit_plan_mode

## How This Tool Works
- This tool reads the plan from the file you wrote
- The user will see the plan content and approve or request changes
- If approved, you return to normal mode and can begin implementation
- If rejected, you stay in plan mode to revise the plan

Do NOT use ask_user_question to ask "is my plan ready?" or "should I proceed?"
— that's exactly what this tool does.
```

```json
{
  "type": "object",
  "properties": {},
  "required": []
}
```

---

## agent

```
Launch a subagent to handle complex, multi-step tasks autonomously.

Available subagent types and the tools they have access to:
- evaluate: read-only evaluation (read_file, bash_subagent, web_fetch).
  Assesses correctness, quality, and security — reports issues with severity.
- Explore: read-only exploration (read_file, bash_subagent, web_fetch)
- verification: read-only verification (read_file, bash_subagent, web_fetch).
  Attempts to BREAK the implementation — runs builds, tests, and adversarial probes.

Omit subagent_type to fork yourself — the fork inherits your conversation context
(minus the agent call itself). This is the DEFAULT choice: it shares your prompt
cache (cheap) and already knows the background. Use fork for research, implementation,
and any task where the context you've built up is useful.

Specify subagent_type for a cold agent that starts with fresh context and filtered tools.
Cold agents CANNOT reuse your prompt cache — they are EXPENSIVE. Only use them when
you specifically need an independent perspective uncontaminated by your own analysis.

Cold agent types:
- evaluate: for assessment, not implementation. Code review, security audit,
  second opinion on architecture, or any task that requires evaluating
  correctness or quality — NOT writing or editing code. Reports issues with
  severity (CRITICAL / WARNING / NOTE) and specific file:line references.
- Explore: for discovery, not judgment. Find where things are defined, map out
  unfamiliar subsystems, locate patterns across the codebase. Use when you need
  to answer "where is X?" or "how is Y structured?" — NOT "is this code correct?".
- verification: for post-implementation verification. Use after completing a
  non-trivial task (3+ file edits, backend/API changes, infrastructure changes).
  The agent runs builds, tests, and adversarial probes, then reports PASS/FAIL
  with evidence. Pass the original task, files changed, and approach taken.

Do NOT use cold agents just to parallelize work — fork multiple times instead.
Each fork shares the same cache prefix; each cold agent pays the full input cost.

- Launch multiple agents concurrently whenever possible — use a single message
  with multiple tool calls. Map each agent to a separate todo item via todo_write
  and mark them all in_progress BEFORE dispatching. Mark each completed
  immediately as its agent returns.

Usage: for forks, write a directive (context is inherited); for cold agents, provide
a self-contained prompt with full background — the agent hasn't seen this conversation.
You will receive the subagent's final output as the tool result.

Do NOT use the agent tool for: reading a known file path (use read_file),
searching within 1-3 files (use read_file), or simple file pattern matching (use shell).
Explore agent should be used proactively for codebase exploration without the user having to ask.
```

```json
{
  "type": "object",
  "properties": {
    "subagent_type": {
      "type": "string",
      "description": "Omit to fork (DEFAULT, cheap, shares cache). Set to 'evaluate' for code review / security audit, 'Explore' for finding code patterns, or 'verification' for post-implementation testing. Cold agents are expensive — they cannot reuse your prompt cache."
    },
    "description": {
      "type": "string",
      "description": "A short (3-5 word) description of the task"
    },
    "prompt": {
      "type": "string",
      "description": "The task for the subagent to perform"
    },
    "model": {
      "type": "string",
      "description": "Optional model override. Available values are listed in the system prompt under 'Subagent Model Selection'. Omit or leave blank to use the default. Invalid values are ignored."
    }
  },
  "required": ["description", "prompt"]
}
```

---

## kill_background_task

```
Kill a running background task by its task ID.
Use this to stop long-running background commands (servers, watchers) started via bash(run_in_background=true).
The task ID is shown in the background-task notifications (<background-task id="..."/>) and in the original bash tool response (Task ID: xxx).
Call with kill_background_task(task_id="<id>").

On Unix, kills the entire process group (SIGKILL). On Windows, kills the process.
If the task is already completed or not found, returns an appropriate message.
```

```json
{
  "type": "object",
  "properties": {
    "task_id": {
      "type": "string",
      "description": "The task ID of the background command to kill. Obtained from the bash tool response or background-task notifications."
    }
  },
  "required": ["task_id"]
}
```

---

## todo_write

```
Task tracker for complex multi-step work. Use only when tasks have meaningful dependencies or run in parallel — skip for linear single-file changes.

Trigger test (BOTH must be true before using this tool):
1. ≥3 steps with real dependencies (B depends on A) or parallelizable units (subagents)
2. Work spans ≥2 turns OR dispatches parallel subagents

→ If either is false, skip the todo list and just do the work.

RULES:
1. After receiving new instructions — capture all tasks before starting work.
2. Mark in_progress BEFORE beginning each task. Update status in real-time.
3. Mark completed IMMEDIATELY after finishing — never batch-mark.
4. ALWAYS pass the COMPLETE list — copy from previous result, modify, pass it all back.
5. When all tasks are completed, the list auto-clears.

content = imperative ("Fix bug"). activeForm = present continuous ("Fixing bug") — displayed with spinner during in_progress state. Both required for every task.

Multiple tasks can be in_progress simultaneously when running parallel work.

Skip this tool for: single-file fixes, linear micro-tasks (locate→edit→build), informational requests. When uncertain, skip — a missed todo is cheaper than noise.

→ Detailed rules and examples: see system prompt section "## Todo List".
```

```json
{
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
}
```
