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
| `shell` | ❌ | 命令 | 执行 Shell 命令 |
| `grep` | ✅ | 搜索 | 正则搜索文件内容 |
| `search_file` | ✅ | 搜索 | Glob 模式搜索文件名 |
| `ls` | ✅ | 搜索 | 列出目录内容 |
| `web_fetch` | ✅ | Web | 获取 URL 内容 |
| `lsp_diagnostic` | ✅ | LSP | 获取文件编译诊断 |
| `lsp_definition` | ✅ | LSP | 跳转到符号定义 |
| `lsp_references` | ✅ | LSP | 查找所有引用 |
| `lsp_hover` | ✅ | LSP | 获取符号类型签名和文档 |

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
Execute a shell command in a subprocess. Configurable timeout (default 120s, max 600s), captures stdout and stderr. Unix/macOS uses sh -c, Windows uses cmd /c. Command syntax must target the correct platform (Windows does not support ; for multi-command, use &&). Prefer dedicated tools over shell:   - Read files: read_file (not cat/head/tail)   - Write files: write_file (not echo >/cat <<EOF)   - Edit files: edit_file (not sed/awk)   - Find files: search_file (not find)   - Search content: grep (not grep/rg)   - List directories: ls (not ls command) Launch multiple independent commands as parallel shell calls in a single response. Chain dependent commands with &&, not newlines. Avoid unnecessary sleep; use run_in_background for long tasks (future support). To change working directory, use the working_dir parameter. Do NOT prefix commands with cd. Example: for ls in /tmp, pass {"command":"ls", "working_dir":"/tmp"}, not {"command":"cd /tmp && ls"}.
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

## grep

```
Search for lines matching a regular expression. Supports glob file filtering and context lines. Returns up to 250 matches.
```

```json
{
  "type": "object",
  "properties": {
    "pattern":          { "type": "string",  "description": "Regular expression (RE2 syntax)" },
    "include":          { "type": "string",  "description": "Glob pattern to filter files (optional, e.g. *.go)" },
    "working_dir":       { "type": "string",  "description": "Search root directory (optional)" },
    "case_insensitive": { "type": "boolean", "description": "Case-insensitive matching (default: false)", "default": false },
    "context_lines":    { "type": "integer", "description": "Number of context lines around matches (optional, default 0)" }
  },
  "required": ["pattern"]
}
```

## search_file

```
Search for file names using glob patterns. Supports ** recursive matching (e.g. **/*.go, src/**/*_test.go). Returns up to 100 files.
```

```json
{
  "type": "object",
  "properties": {
    "pattern":     { "type": "string", "description": "Glob pattern (e.g. **/*.go, *.md, src/**/*_test.go)" },
    "working_dir":  { "type": "string", "description": "Search root directory (optional)" }
  },
  "required": ["pattern"]
}
```

## ls

```
List files and subdirectories in a directory. Directories are suffixed with /. Supports recursive depth control (depth parameter, default 1).
```

```json
{
  "type": "object",
  "properties": {
    "path":        { "type": "string",  "description": "Directory path (optional, default: project root)" },
    "depth":       { "type": "integer", "description": "Recursion depth (optional, default: 1)", "default": 1 },
    "working_dir":  { "type": "string",  "description": "Working directory (optional)" }
  },
  "required": []
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

## lsp_diagnostic

```
Get diagnostics (compile errors, warnings, lint hints) for a file. Returns results grouped by severity, including file, line, column, and message.
```

```json
{
  "type": "object",
  "properties": {
    "file_path":   { "type": "string", "description": "Absolute file path" },
    "working_dir":  { "type": "string", "description": "Working directory (optional, for LSP server project context)" }
  },
  "required": ["file_path"]
}
```

## lsp_definition

```
Jump to the symbol definition at the cursor position. Returns file path, line, and column. Use for understanding third-party libraries, type definitions, and function signatures.
```

```json
{
  "type": "object",
  "properties": {
    "file_path":   { "type": "string",  "description": "Absolute file path" },
    "line":        { "type": "integer", "description": "Line number (0-based)" },
    "character":   { "type": "integer", "description": "Column number (0-based)" },
    "working_dir":  { "type": "string",  "description": "Working directory (optional)" }
  },
  "required": ["file_path", "line", "character"]
}
```

## lsp_references

```
Find all references to a symbol (including its definition). Returns a list of file paths, lines, and columns. Use for tracing dependencies and impact analysis.
```

```json
{
  "type": "object",
  "properties": {
    "file_path":            { "type": "string",  "description": "Absolute file path" },
    "line":                 { "type": "integer", "description": "Line number (0-based)" },
    "character":            { "type": "integer", "description": "Column number (0-based)" },
    "include_declaration":  { "type": "boolean", "description": "Include the definition location (default: true)", "default": true },
    "working_dir":           { "type": "string",  "description": "Working directory (optional)" }
  },
  "required": ["file_path", "line", "character"]
}
```

## lsp_hover

```
Get the type signature and documentation (Markdown) for a symbol at the cursor position. Use for quickly viewing API usage.
```

```json
{
  "type": "object",
  "properties": {
    "file_path":   { "type": "string",  "description": "Absolute file path" },
    "line":        { "type": "integer", "description": "Line number (0-based)" },
    "character":   { "type": "integer", "description": "Column number (0-based)" },
    "working_dir":  { "type": "string",  "description": "Working directory (optional)" }
  },
  "required": ["file_path", "line", "character"]
}
```
