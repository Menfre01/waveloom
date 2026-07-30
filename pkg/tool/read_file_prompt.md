## Read File

Use `read` to inspect file content before editing.

### Output format

```
[file_path]
1:line one
2:
3:line three
...
<system-reminder>...</system-reminder>
```

Line numbers are 1-based. Empty lines appear as `N:` (no visible content after colon). Content after the colon is the file's exact bytes — including trailing whitespace. The system reminder at the bottom describes the hunk format and matching tolerance for the edit tool.

### Scenarios

**Read entire file:**
`read("path/to/file.go")` — just `file_path`. Use for small files or when you need the full content.

**Explore a large file before reading:**
`read("path/to/file.go", outline=true)` — returns symbol index (functions, types, classes). Use this first to find the area of interest, then read that section with `offset` + `limit`.

**Find and read around a match:**
`read("x.go", pattern="func main", context_lines=30)` — centers output on the first line containing `pattern`, showing ±`context_lines` around it. Ideal for locating a function or variable without grep.

**Page through multiple matches:**
`read("x.go", pattern="TODO", offset=2)` — when there are multiple matches, `offset` selects which match to display (0 = first, 1 = second, …). The first result tells you how many matches exist; use `offset=N` to step through them.

**Read a specific line range:**
`read("large.go", offset=100, limit=50)` — reads lines 101–150. Without `pattern`, `offset` is the starting line number (0-based). Use for paginating through large files.

**Resolve relative paths:**
`read("config.json", working_dir="/project/src")` — resolves `file_path` relative to `working_dir`.

### Parameters

| Param | Type | Required | Meaning |
|-------|------|:--:|---------|
| `file_path` | string | ✓ | Absolute path, or relative to workspace root |
| `pattern` | string | | Substring to locate — output centers on first match ±`context_lines` |
| `context_lines` | int | | Lines of context around match (default 5, max 50) |
| `offset` | int | | Without `pattern`: starting line (0-based). With `pattern`: match index (0 = 1st match) |
| `limit` | int | | Max lines to return (0 = unlimited) |
| `outline` | bool | | Return symbol index instead of file content |
| `working_dir` | string | | Base directory for relative `file_path` |

### Rules

- Always read before editing — the edit tool validates read state.
- Use `outline=true` for large files before reading specific sections.
- Use `pattern` + `context_lines=30` for targeted reading.
- When a hunk fails, re-read with `read(file_path, pattern="<unique line>", context_lines=5)` to get exact bytes.
