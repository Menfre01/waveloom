## Read File (Hashline)

Use `read` to get a TAG and line-numbered content for hash-anchored editing.
Always read a file before editing it — the TAG certifies the file snapshot
and must match the TAG in the edit patch section header.

### Primary usage: read replaces rg/grep for file-local searches

`read` with `pattern` does in ONE call what used to take two (rg + read):
- Provide `file_path` and `pattern` → you get a TAG AND a centered window
  around the match. No need to grep first.
- Use `context_lines` generously for initial exploration (15-30, not the
  default 5). A larger window lets you verify the edit region without a
  follow-up read. The max is 50.

**The default pattern**: when you know which file to read, go straight to
`read` with `pattern` and `context_lines=30`. Skip `bash rg` entirely.

Only reach for `bash rg`/`grep` when:
- You need to find WHICH files contain a symbol (multi-file search)
- The file is >10MB (rejected by read)
- You need regex patterns beyond simple substring matching

### Parameters

- `pattern` — substring to locate in the file. Centers the output on the
  first match ±context_lines. Use `offset` (0-based) to page through
  subsequent matches.
- `context_lines` — lines above and below each match (default 5, max 50).
  For initial reading, prefer 20-30 to minimize re-reads. Use `limit=1`
  for match-line-only display.
- `offset` — without pattern: starting line number (0-based). With pattern:
  match index (0-based) to skip earlier matches.
- `limit` — maximum lines to return (default: all, capped by file size).

### TAG and editing

- TAG is computed from the COMPLETE file content, even when offset/limit
  display only a range. Read once, edit multiple times with the same TAG.
- After editing, the response includes a new TAG and context window.
  If your next edit target falls within that context, chain without re-read.
- Files >10MB rejected — use shell tools (head/tail/grep/sed/awk).
