## Edit File (Hashline)

`read` → TAG + line numbers → `edit` with TAG + line numbers. Never reproduce old code — only TAG, line numbers, and new content.

### Edit vs Write

`edit` is for 1-2 surgical changes in a file you've already read. For larger changes, choose based on file size:

| File size | 1-2 changes | 3-5 changes (scattered) | 3-5 changes (same function) | 6+ changes |
|---|---|---|---|---|
| ≤200 lines | edit | write | write | write |
| 200-500 lines | edit | edit (multi-section) | write | write |
| >500 lines | edit | edit (multi-step) | edit (multi-step) | edit (multi-step) |

**Multi-section** (same file, one edit call): put changes in separate `[PATH#TAG]` sections within one patch — they merge atomically, no TAG staleness. Preferred for 200-500 line files.

**Multi-step** (>500 line files): chain sequential edit calls. Read the full file once to get TAG + line numbers for ALL target regions, then edit one region per call. Edit bottom-to-top across calls so earlier edits don't shift later line numbers (within a single edit call the patcher auto-calculates offsets — this only matters _across_ separate calls). Verify with `make build` after each step.

**Write** (small files, concentrated changes): `read` + `write` the whole file. Use when the function/block being changed fits comfortably in the output. Always `read` first to get the current state.

**Prefer `write` when:**
- Changing 3+ lines inside a multi-line expression (e.g. `fmt.Sprintf` with many `%s` placeholders, large struct literal)
- Moving or renaming large code blocks (restructuring)
- Replacing >50% of the file
- A previous `edit` was **rejected** — for `tag_mismatch`: re-read and retry, or rewrite with `write`; for `overlapping`: split into separate `edit` calls

**Anti-pattern**: making 4+ individual SWAP/INS operations to update tool names inside a single `fmt.Sprintf` call — gets boundaries wrong, costs multiple turns. Rewrite the whole function with `write`.

Rule of thumb: if you'd rather modify the whole file in one pass than track line numbers across scattered changes, `read` + `write`. Each failed edit wastes a turn; when in doubt, write for small files, multi-step edit for large files.

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

- Multiple `[PATH#TAG]` sections allowed; same-file sections merge atomically — this is the RECOMMENDED way to edit one file in multiple places. Always merge same-file edits into one call to avoid TAG staleness.
- REM/MV cannot combine with line ops in the same section — split into separate sections.
- Overlapping operations (two ops on the same line) must be split into separate `edit` calls (different files are fine in one call).
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
