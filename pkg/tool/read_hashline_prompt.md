## Read File (Hashline)

Use `read` to get a TAG and line-numbered content for hash-anchored editing.
Always read a file before editing it — the TAG certifies the file snapshot
and must match the TAG in the edit patch section header.

- TAG is computed from the COMPLETE file content, even when offset/limit
  are used to display only a range of lines.
- `pattern` parameter locates a symbol in the file (substring match — may also
  match longer words, so prefer specific patterns). Centers the output window
  on the first match ±context_lines, eliminating a separate `bash rg` call.
  Use `offset` as match index (0-based) to page through multiple matches.
- `pattern` is for exploration and locating symbols. Before editing, verify the
  target is within the displayed window. If not, re-read the full file or adjust
  offset/context_lines to confirm the edit region.
- `context_lines` controls window size around matches (default 5, max 50).
  0 is treated as default — use `limit=1` for match-line-only.
- Files larger than 10MB are rejected — use shell tools (head/tail/grep/sed/awk)
  to both read and edit large files.
- Empty files return a TAG with a warning; INS.HEAD / I
