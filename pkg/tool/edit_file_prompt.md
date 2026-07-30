## Edit File

`edit` — patch format with envelope.

### Parameters

- `file_path` — default target file. For single-file edits (no `*** Update File:` headers), this is the file being edited. For multi-file patches, relative paths in `*** Update File:` headers resolve against this file's directory.
- `hunk` — unified diff hunk(s) in the envelope format below.

### Format

```
*** Begin Patch
*** Update File: <relative/path>
@@ [optional context label]
 context line (space prefix)
-old line to remove
+new line to add
 context line
*** End Patch
```

- Every patch starts with `*** Begin Patch` and ends with `*** End Patch`.
- Every file MUST have a `*** Update File: <path>` header before its hunks — even single-file edits. Paths in headers are relative to `file_path`'s directory, not the workspace root.
- `@@ [context label]` is optional (bare `@@` is fine). Use a function name or line hint for readability.

### Basic usage

**Single file** — omit `*** Update File:` when editing one file; the engine uses `file_path` as the target:
```
*** Begin Patch
@@ func greet
 func greet() string {
-    return "hello"
+    return "hello, " + name
 }
*** End Patch
```

**Single file with explicit header** — also valid, path in header must match `file_path`:
```
*** Begin Patch
*** Update File: src/main.go
@@ func greet
 func greet() string {
-    return "hello"
+    return "hello, " + name
 }
*** End Patch
```

**Replace** (change function signature):
```
*** Begin Patch
*** Update File: src/main.go
@@ func greet
 func greet() string {
-    return "hello"
+    return "hello, " + name
 }
*** End Patch
```

**Delete** (remove a function and its calls):
```
*** Begin Patch
*** Update File: src/main.go
@@
 func main() {
-    y := oldHelper(5)
-    println(y)
 }
*** End Patch
```

**Insert** (add error handling):
```
*** Begin Patch
*** Update File: src/config.go
@@ func readConfig
 func readConfig() string {
     data, _ := os.ReadFile("config.txt")
+    if err != nil {
+        return ""
+    }
     return string(data)
*** End Patch
```

### Multi-file (same call)

Set `file_path` to any file in the target directory — relative paths in `*** Update File:` headers resolve against its directory. E.g., with `file_path="src/main.go"`, a header `*** Update File: util.go` targets `src/util.go`.

```
*** Begin Patch
*** Update File: src/greet.go
@@ func greet
 func greet() string {
-    return "hello"
+    return "hello, " + name
 }
*** Update File: src/util.go
@@ func helper
 func helper(x int) int {
-    return x * 2
+    return x * 3
 }
*** End Patch
```

Each additional file gets its own `*** Update File: <path>` header before its hunks.

### Tips

- **Read first**: always `read` the file before editing — the engine validates file state. `write` also updates file state, so write→edit works without re-read.
- **Context lines**: include 1-2 unchanged lines around each change for unique matching. Copy content exactly from `read` output — line numbers, colons, and the `[path]` header are NOT part of the context.
- **Exact whitespace**: copy tabs/spaces exactly from the read output.
- **Empty lines in read output**: an empty line appears as `N:` in read output (line number + colon, no visible content). In a hunk context line, copy it as ` ` (space prefix) with nothing after it — the space is the unified diff context marker, followed by the empty content.
- **Matching**: engine tries 4 progressively tolerant layers:
  1. exact — byte-for-byte match
  2. trailing whitespace — strips `" \t\r"` from line ends before comparing
  3. full trim — strips leading+trailing whitespace before comparing
  4. unicode normalize — normalizes fancy quotes/dashes/spaces to ASCII before comparing
  Each layer is only tried if the previous one fails. The failure diagnostics report which layer matched (or was closest), so you can diagnose the mismatch type without guesswork.
- **Failed hunk**: re-read the file, check the diagnostics output, adjust context lines, retry. If a hunk fails twice on the same file, re-read with `read(file_path, pattern="<unique line from target area>", context_lines=5)` to get exact bytes — invisible Unicode whitespace (zero-width spaces, BOM, direction marks) may be present in the file but invisible in standard output.

### Common mistakes

❌ Missing context — hunk starts with `-` or `+` without a ` ` context line above
```
@@
-    return "hello"    ← no context to anchor the match
```

✅ Include one context line above the change
```
@@
 func greet() string {
-    return "hello"
```

❌ Too little context — ambiguous match (same pattern appears multiple times)
```
@@
-    return x * 2     ← matches multiple functions
```

✅ Include enough context to make the match unique
```
@@ func helper
 func helper(x int) int {
-    return x * 2
 }
```

❌ Missing `*** Update File:` header — every file section needs one
```
@@ func greet
 func greet() string {
-    return "hello"
```

✅ Always include file header before hunks
```
*** Update File: src/main.go
@@ func greet
 func greet() string {
-    return "hello"
```

❌ Wrong empty line — using visible placeholder instead of actual empty line in hunk
```
*** Update File: src/main.go
@@
 func main() {
-·                   ← copied the placeholder from read output
+    new line
```
Read output shows empty lines as `N:` with no content after colon. In a hunk, an empty context line is ` ` (space prefix) + nothing.

✅ Correct — ` ` prefix with nothing after it
```
*** Update File: src/main.go
@@
 func main() {
+
     fmt.Println("hello")
 }
```

### Consecutive failures

Two failures on the same file → **stop using edit**, switch to `read` and verify the target content at byte level. Likely causes:

- **Invisible Unicode**: zero-width spaces (`\u200B`), BOM (`\uFEFF`), direction markers (`\u200E`/`\u200F`), line separators (`\u2028`). Use `read(file_path, pattern="<unique snippet>", context_lines=5)` to reveal the exact bytes around the target area.
- **Indentation mismatch**: tabs vs spaces, or trailing whitespace not visible in terminal output. Re-read with `context_lines=30` to see surrounding indentation pattern.
- **Wrong file section**: the area has changed since last read. Re-read the full file or use `read(outline=true)` first.

After re-reading, construct a new hunk with exact context lines from the fresh read output. Do NOT attempt more than 2 edits on the same file without re-reading. Do NOT fall back to bash/python/sed — the only way through a failed hunk is re-read → better hunk → retry edit.
