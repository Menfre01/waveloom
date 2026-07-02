# Waveloom 工具描述

> 本文档记录了 16 个内置工具通过 function calling 发送给 LLM 的完整内容：
> `Description()` 文本 + `Schema()` JSON 参数定义。
> 这些内容与 `pkg/tool/*.go` 中的实现严格一致，改动时请同步更新。

## 概述

| 工具 | 并发安全 | 类别 | 说明 |
|------|:--:|------|------|
| `read_file` | ✅ | 文件 | 读取文件内容（带行号） |
| `write_file` | ❌ | 文件 | 创建或覆盖文件 |
| `edit_file` | ❌ | 文件 | 基于精确字符串匹配的查找替换 |
| `shell` | ❌ | 命令 | 执行 Shell 命令 |
| `web_fetch` | ✅ | Web | 获取 URL 内容 |
| `ask_user_question` | ❌ | 交互 | 向用户发起选择题决策 |
| `skill` | ❌ | 系统 | 调用用户定义的 Skill |
| `enter_plan_mode` | ❌ | Plan | 进入先规划后执行的 Plan 模式 |
| `exit_plan_mode` | ❌ | Plan | 提交 Plan 审批，通过后恢复正常模式 |

> 并发安全（✅ = `ConcurrentSafe() true`）：标记为并发的工具可由 Agent Loop 在同一轮中与其他读操作并行执行。

---

## read_file

```
Read a file with line numbers. Supports offset and limit parameters to read partial content. file_path must be a file, not a directory. On directory error, pick a file from the listing — use the Did you mean suggestion if present.
```

```json
{
  "type": "object",
  "properties": {
    "file_path":  { "type": "string",  "description": "Absolute file path" },
    "offset":     { "type": "integer", "description": "Starting line number (0-based, 0 = first line, optional)" },
    "limit":      { "type": "integer", "description": "Number of lines to read (optional, default: all)" },
    "working_dir": { "type": "string",  "description": "Working directory (optional)" }
  },
  "required": ["file_path"]
}
```

## write_file

```
Create a new file or overwrite an existing file. Creates parent directories automatically. Use only for new files or complete overwrites; for partial edits use edit_file.
```

```json
{
  "type": "object",
  "properties": {
    "file_path":  { "type": "string", "description": "Absolute file path" },
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

## shell

```
Execute a shell command in a subprocess. Configurable timeout (default 120s, max 600s), captures stdout and stderr. Unix/macOS uses sh -c, Windows uses cmd /c. Command syntax must target the correct platform (Windows does not support ; for multi-command, use &&). Prefer dedicated tools over shell:   - Read files: read_file (not cat/head/tail)   - Write files: write_file (not echo >/cat <<EOF)   - Edit files: edit_file (not sed/awk)   - Find files: search_file (not find)   - Search content: grep (not grep/rg)   - List directories: ls (not ls command) Launch multiple independent commands as parallel shell calls in a single response. Chain dependent commands with &&, not newlines.

Multi-line commands: Use \ at the end of EVERY line to continue.
In JSON, this is written as \\\n — three backslashes then n.
The first two backslashes produce a literal \ (JSON \\ → \),
the third backslash + n produces a newline (JSON \n → line break).
Example: {"command":"echo line1 \\\nline2 \\\nline3"}
becomes: echo line1 \
line2 \
line3 (all one logical line).
Without \\ at the end of each JSON line, each physical newline
starts a NEW command in bash -c, splitting your intended output.

Commands already run in the workspace directory.
```

```json
{
  "type": "object",
  "properties": {
    "command":     { "type": "string",  "description": "Shell command to execute. Unix/macOS uses sh -c, Windows uses cmd /c. Windows does not support ; for multi-command, use &&." },
    "working_dir":  { "type": "string",  "description": "Working directory (optional)" },
    "timeout_ms":  { "type": "integer", "description": "Timeout in milliseconds (default: 120000, max: 600000)" }
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

## ask_user_question

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
```
