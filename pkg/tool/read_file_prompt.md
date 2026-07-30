## Read File

Use `read` to inspect file content before editing. The system records file state internally — no TAG needed.

### Parameters

- `file_path` — required. Absolute path, or relative to workspace root.
- `pattern` — optional substring to locate. Centers output on match ±context_lines.
- `context_lines` — lines of context around match (default 5).
- `outline` — set true for symbol index (function/type/variable names with line numbers).

### Tips

- Always read before editing — required by the edit tool
- Use `outline=true` for large files before reading specific sections
- Use `pattern` + `context_lines=30` for targeted reading
