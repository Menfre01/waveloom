## Read File (Hashline)

Use `read` to get a TAG and line-numbered content for hash-anchored editing. **Always read before editing** — the TAG is required for `edit` to target lines precisely.

- **Exploring unfamiliar code files**: use `outline=true` first (returns `N Symbols in file:` with function/type/class names and line numbers), then `pattern` + `context_lines=30` to read only what you need. LSP provides precise results for supported languages; regex fallback covers all other code files.
- **Multi-edit session** (≥4 locations same file): read the FULL file — omit `pattern`, `offset`, `limit`. One call = complete file + TAG. For 2–3 edits, use `pattern` + `context_lines=30` at each location.
- **Single-target edit**: use `pattern` + `context_lines=30`. The centered window usually covers everything needed.
- **read replaces grep for file-local searches**: `pattern` does in one call what used to take two. Only use `bash grep` for multi-file search or >10MB files.

**Parameters**: `pattern` centers on match, use `offset` for subsequent matches. `context_lines` default 5, prefer 30 for editing. `limit=1` for match-line-only. TAG is computed from the full file automatically — you don't need to read the full file to get a valid TAG.
