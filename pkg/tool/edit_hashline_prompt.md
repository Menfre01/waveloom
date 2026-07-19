## Edit File (Hashline)

`read` → TAG + line numbers → `edit` with TAG + line numbers. Never reproduce old code — only TAG, line numbers, and new content.

### Edit vs Write

Prefer `edit` for 1-3 surgical changes in a file you've already read. Prefer `write` when:
- 4+ separate changes in the same file
- Moving or renaming large code blocks (restructuring)
- Replacing >50% of the file
- A previous `edit` was **rejected** — for `tag_mismatch`: re-read and retry, or rewrite with `write`; for `overlapping`: split into separate `edit` calls

Rule of thumb: if you'd rather modify the whole file in one pass than track line numbers across scattered changes, `read` + `write`.

### Operations

```
SWAP N.=M:      Replace lines N–M with body. N.=N for single line.
DEL N.=M        Delete lines N–M. DEL N for single line.
INS.PRE N:      Insert body before line N.  INS.POST N: after line N.
INS.HEAD:       Insert at file start.      INS.TAIL: insert at file end.
REM             Delete entire file.         MV DEST   rename/move.
```

> When inserting new functions, types, or constants, INS.POST after the closing `}` of the preceding block — not before the next block's comment. One line off = inserted inside the wrong scope.

Line numbers: `read` output `N:CONTENT`. Ranges are inclusive. Use `N.=M` not `N:=M`.

### Body lines

Every body line starts with `+` (including leading whitespace). Standalone `+` = blank line.

**The #1 pitfall**: adding `+` before an operation line makes it silent body text — the edit succeeds but does nothing:

```
+ DEL 5          ← WRONG: DEL treated as literal body text
DEL 5            ← correct: DEL is an operation
```

Blank lines between body lines are skipped. Use standalone `+` for intentional blank lines.

### Format

```
*** Begin Patch
[src/pkg/foo.go#A1B2]       ← TAG from read
SWAP 2.=2:
+    fmt.Println("hello")
INS.TAIL:
+    // end of file
*** End Patch
```

- Multiple `[PATH#TAG]` sections allowed; same-file sections merge atomically.
- REM/MV cannot combine with line ops in the same section — split into separate sections.
- Overlapping operations (two ops on the same line) must be split into separate `edit` calls.
- Operations apply in declaration order; offsets auto-calculated — use original line numbers.

### Reading edit responses

The edit response has these sections (not all appear every time):

| Section | Meaning | How to use |
|---|---|---|
| `✓ path — TAG: X — (+N lines)` | Success; new TAG for next edit | Use this TAG for chain edits |
| `--- edit delta ---` | Diff of changed lines (old→new line numbers) | Verify the edit touched the right lines |
| `--- post-edit context ---` | Current file content around the edit (±5 lines) | Chain edit IF target lines are in this window |
| `→ Context covers lines X-Y` | Exact line range visible in the context | Within range → reuse TAG; outside → re-read |
| `→ Full file shown (lines 1-N)` | Small file (≤200 lines): entire file displayed | Edit anywhere in this file without re-read |
| `--- file index ---` | Paragraph-first-line anchors for large files | Use to find line numbers outside the context window |
| `--- tail ---` | Last 3 lines of a large file after a mid-file edit | Structural sanity check — don't edit from tail |
| `— Next TAGs: PATH#TAG` | All new TAGs from this edit, one per file | Copy the TAG for your next edit on that file |
| `⚠ TAG expired, auto-recovered (L5→L8)` | TAG was stale but system remapped line numbers | The edit SUCCEEDED; use the new TAG, not the old one |

> **Important**: `✓ SUCCESS` means the tool executed your instructions exactly — not that the result matches your intent. Always skim the edit delta or post-edit context to confirm the outcome, especially when inserting into ordered blocks (switch cases, struct fields, function args).

### Chain editing

After edit, chain without re-read when target lines are in the post-edit context window. Re-read when: (a) targets outside context, (b) different file, (c) `tag_mismatch` error.
