## Read File (Hashline)

Use read to get a TAG and line-numbered content for hash-anchored editing.
Always read a file before editing it — the TAG certifies the file snapshot
and must match the TAG in the edit patch section header.

- TAG is computed from the COMPLETE file content, even when offset/limit
  are used to display only a range of lines.
- Files larger than 10MB are rejected — use shell tools (head/tail/grep/sed/awk)
  to both read and edit large files.
- Empty files return a TAG with a warning; INS.HEAD / I