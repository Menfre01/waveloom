## Read File (Hashline)

Use `read` to get a TAG and line-numbered content for hash-anchored editing. **Always read before editing** — the TAG is required for `edit` to target lines precisely.

- **Multi-edit session** (≥2 locations same file): read the FULL file — omit `pattern`, `offset`, `limit`. One call = complete file + TAG.
- **Single-target edit**: use `pattern` + `context_lines=30`. The centered window usually covers everything needed.
- **Exploring unfamiliar file**: read the full file first. Don't paginate — small files (≤200 lines) are shown entirely anyway.
- **read replaces rg/grep for file-local searches**: `pattern` does in one call what used to take two. Only use `bash rg` for multi-file search or >10MB files.

**Parameters**: `pattern` centers on match, use `offset` for subsequent matches. `context_lines` default 5, prefer 30 for editing. `limit=1` for match-line-only. TAG covers the complete file even when offset/limit show a range.
