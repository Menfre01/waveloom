## Read File (Hashline)

Use `read` to get a TAG and line-numbered content for hash-anchored editing.
Always read a file before editing it — the TAG certifies the file snapshot
and must match the TAG in the edit patch section header.

### Strategy: one read per file, not one read per section

- **Multi-edit session** (you'll edit ≥2 locations in the same file):
  read the FULL file — omit `pattern`, `offset`, and `limit`.
  One call gives you the complete file + TAG. Plan all edits from one read.

- **Single-target edit** (you know exactly which function/symbol to change):
  use `pattern` + `context_lines=30`. The centered window usually covers
  everything needed for that one edit.

- **Exploring an unfamiliar file**: read the full file first.
  Do NOT read the top 80 lines, then the next 80 — that wastes turns.
  Small files (≤200 lines) are shown entirely after edit anyway.

### read replaces rg/grep for file-local searches

`read` with `pattern` does in ONE call what used to take two (rg + read).
Skip `bash rg` entirely when you know which file to search.
Only reach for `bash rg`/`grep` for multi-file search or >10MB files.

### Parameters

- `pattern` — substring match, centers window ±context_lines on first hit.
  Use `offset` (0-based) to page through subsequent matches.
- `context_lines` — lines around match (default 5, max 50).
  For editing, prefer 30. Use `limit=1` for match-line-only.
- `offset` — without pattern: start line (0-based). With pattern: match index.
- `limit` — max lines (default: all). Omit to read the entire file.

### TAG and editing

- TAG covers the COMPLETE file, even when offset/limit show a range.
- After editing, the response includes new TAG + context window.
  Target in context → chain edit without re-read.
- Files >10MB rejected — use shell tools.
