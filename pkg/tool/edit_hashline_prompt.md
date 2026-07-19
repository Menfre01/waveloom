## Edit File (Hashline) — Recommended

Use edit to modify existing files. read gives you
TAGs and line numbers; edit applies changes by referencing them. Never
reproduce old code — only the TAG, line numbers, and new content.

### Operations

SWAP N.=M:     Replace lines N through M (inclusive) with body lines below
DEL N.=M       Delete lines N through M. DEL N for single line.
INS.PRE N:     Insert body lines BEFORE line N
INS.POST N:    Insert body lines AFTER line N
INS.HEAD:      Insert body lines at the very start of the file
INS.TAIL:      Insert body lines at the very end of the file
REM            Delete the entire file (no body, no line numbers)
MV DEST        Move/rename the file to DEST

### Body lines

Every body line starts with + followed by the actual content (including leading whitespace).
+ alone adds a blank line. The body is ONLY the new content — old lines are deleted
implicitly by the range in SWAP/DEL.

Blank lines between body lines are silently skipped by the parser. To insert
an intentional blank line, use a standalone + line (no content after the +).

Operation lines (SWAP, DEL, INS.PRE, INS.POST, INS.HEAD, INS.TAIL, REM, MV)
must NOT start with +. Adding + before an operation line causes it to be
treated as body content inserted into the file — the operation is silently lost.

Wrong (DEL treated as body text, not as an operation):
*** Begin Patch
[src/main.go#A1B2]
SWAP 2.=2:
+new line
+ DEL 5             ← + DEL (space after +): silent body text, not an operation
*** End Patch

Correct:
*** Begin Patch
[src/main.go#A1B2]
SWAP 2.=2:
+new line
DEL 5               ← no + prefix, recognized as an operation
*** End Patch

### Line numbers

Line numbers come directly from read output (N:CONTENT format).
Ranges are INCLUSIVE: SWAP 2.=3: covers lines 2 and 3.
A range of N.=N: replaces a single line with any number of body lines.
- Use N.=M for ranges, not N:=M — '':='' is not hashline syntax and will produce an error.
Note: files without a trailing newline may acquire one after editing — normal.

### Rules

- After each successful edit/rename, the response includes a new TAG plus a post-edit context showing current line numbers around the changed area.
- Chain edits without re-reading when the target lines are visible in the post-edit context — use the new TAG directly.
- Re-read the file before editing when: (a) the target lines fall outside the post-edit context, (b) you are editing a file not touched in the previous edit, or (c) a tag_mismatch error occurs.

- Post-edit context structure after a successful edit:
  • Small files (≤200 lines): the entire file is displayed with current line numbers — you can edit any line directly.
  • Large files (>200 lines): a ±5-line context window around the edit, followed by a file index (paragraph-first-line navigation anchors) and a tail check (last 3 lines for structural integrity).
  • Use the file index to locate target line numbers outside the context window, then re-read if needed.
- Touch only lines that change. For pure additions, use INS.PRE / INS.POST — never
  widen a SWAP to include unchanged lines.
- Operations are applied in declaration order. After each operation, the system
  automatically computes the line offset and adjusts subsequent operations' line
  numbers accordingly. All line numbers refer to the original file — you do NOT
  need to manually calculate offsets. This allows editing multiple places in
  a single edit call.
- Do NOT create overlapping operations on the same lines (e.g., SWAP 5.=6: and
  DEL 5 in the same patch; or INS.PRE 4: and INS.POST 4: on the same reference
  line). Note: INS.PRE N followed by SWAP N (or DEL N) is safe — the system
  automatically offsets the SWAP/DEL line number after the insertion.
  Overlapping ops will be rejected with an error — split them into
  separate edit calls.
- On tag_mismatch error: the file was modified since your last read — re-read to
  get a fresh TAG and line numbers before editing again.
- A patch may contain multiple [PATH#TAG] sections for different files, or multiple sections for the same file. For the same file, all sections are merged and applied atomically in a single write — there is no intermediate disk state. If any section's TAG validation fails, or any operations overlap across sections, ALL sections for that file are rejected together (the file is not modified). Edits to other files in the same patch are unaffected. REM/MV cannot be combined with line-range operations on the same file in one patch; split them.

The edit response includes an edit delta (with original line numbers) followed by a post-edit context (with current line numbers). The two sections use different numbering — cross-reference them by content, not by line number.

- The parser tolerates trailing comments (// ... , # ...) and minor spacing variations such as INS. PRE. You do not need to strip them manually.

### Format

*** Begin Patch
[src/pkg/foo.go#A1B2]       ← first file
OP1
+BODY

[src/pkg/bar.go#C3D4]       ← second file
OP2
+BODY
*** End Patch

Example — replace line 2, insert after line 4:

*** Begin Patch
[src/main.go#A1B2]
SWAP 2.=2:
+    fmt.Println("hello, world")
INS.POST 4:
+    // cleanup on exit
+    defer os.Remove(tmpFile)
*** End Patch

### Patch Boundary Check

Before submitting a patch, verify the edited region is structurally sound:

- The SWAP range should cover only the lines you intend to replace.
- If the line immediately after the range is a structural delimiter (e.g., a closing brace, 'return', 'else', 'case', 'end', 'fi'), keep it outside the SWAP range unless you deliberately replace it.
- If your new body already includes its own closing delimiter, do not include the original closing delimiter in the SWAP range.
- When unsure, prefer multiple smaller patches over one large patch that spans structural boundaries.

### When NOT to use

- Creating a new file → use write (returns a TAG, no read needed)
- Reading a file → use read
- Very simple single-word replacements on short files → use edit with a single SWAP line.