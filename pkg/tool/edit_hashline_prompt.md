## Edit File (Hashline)

`read` → TAG + line numbers → `edit` with TAG + line numbers. Never reproduce old code — only TAG, line numbers, and new content.

### Operations

```
SWAP N.=M      Replace lines N–M with body. N.=N for single line.
               Use %OLD ... %NEW sentinel to specify old content for verification.
SWAP           Content-only replace (no line numbers) — locate by %OLD match.
SWAP ALL       Replace ALL occurrences of %OLD in file.
DEL N.=M       Delete lines N–M. DEL N for single line.
INS.PRE N:     Insert before line N.  INS.POST N: after line N.
INS.HEAD:      Insert at file start.  INS.TAIL: insert at file end.
REM            Delete entire file.     MV DEST   rename/move.
```
Line numbers: `read` output `N:CONTENT`. Line ranges are inclusive. Use `N.=M` not `N:=M`.

Constraint: `INS.PRE N` + `INS.POST N` targeting the same line N in one section is rejected. Use two separate edit calls if you need to insert both before and after the same line.

**Sentinel format (preferred)** — old content between `%OLD` and `%NEW` for verification:

```
SWAP 2.=2
%OLD
func main() {
%NEW
func run() {
```

Body lines are written directly (no `+` prefix needed). `%OLD`/`%NEW` outside sentinel mode are literal content; only unindented `%OLD`/`%NEW` at line start act as sentinels.

**Legacy format (backward compatible)** — `SWAP N.=M:` with body lines prefixed by `+`:

```
SWAP 2.=2:
+    fmt.Println("hello")
```

### Patch format

```
*** Begin Patch
[src/pkg/foo.go#A1B2]       ← TAG from read
SWAP 2.=2
%OLD
func main() {
%NEW
func run() {

INS.TAIL:
+    // end of file
*** End Patch
```
Multiple `[PATH#TAG]` sections in one patch merge atomically for the same file. Do NOT insert `*** End Patch` between sections.

### Same-file multi-section rule

**When editing the same file with multiple sections in one patch, every SWAP MUST include `%OLD` — otherwise the entire patch is rejected** (to prevent line-number drift corrupting the file). If you can't provide `%OLD` for every SWAP, split into separate edit calls with re-read between them.

### ⚠️ Warning handling

If the edit response contains `⚠️ SKIPPING VERIFICATION` — this means content verification was skipped (missing `%OLD`), and the edit may corrupt the file if line numbers are stale. **Re-read the file and retry with `%OLD` sentinel blocks.**

### Edit vs Write

| File size | 1-2 changes | 3-5 changes (scattered) | 6+ changes |
|---|---|---|---|
| ≤200 lines | edit | write | write |
| 201-500 lines | edit | edit | write |
| >500 lines | edit multi-step | edit multi-step | edit multi-step |

**After edit**: skim the edit delta — `✓ SUCCESS` means the tool executed, not that your intent was correct.
**Chain editing**: reuse TAG when subsequent target lines are visible in the post-edit context emitted by the tool. Re-read for different files or `tag_mismatch`.
**Multi-section output**: each section gets a `§N` label in the edit delta (e.g., `--- edit delta §1 ---`), making it easy to match changes to sections.

### Common Mistakes

**1. SWAP for pure insertion — use INS.POST instead**

❌ WRONG: using `SWAP 3.=3` when you only want to add content after line 3:
```
SWAP 3.=3
%OLD
    "deepseek/": {CacheHit: 0.1, CacheMiss: 1.0, Output: 2.0},
%NEW
    "deepseek/": {CacheHit: 0.1, CacheMiss: 1.0, Output: 2.0},
    "openai/":   {Prompt: 14.0, Output: 56.0},
```
Problem: body must include the original line 3, doubling its length.

✅ RIGHT: `INS.POST 3:` adds lines without touching existing content:
```
INS.POST 3:
    "openai/":   {Prompt: 14.0, Output: 56.0},
```

**2. SWAP for pure deletion — use DEL**

❌ WRONG: `SWAP 5.=8` with empty body. Same result but DEL is clearer and self-documenting.

✅ RIGHT:
```
DEL 5.=8
```

**3. SWAP across function boundaries — split into sections (same TAG)**

❌ WRONG: one SWAP covering end of func1 through start of func2:
```
SWAP 41.=47
%OLD
func Lookup(...) {
    ...
}
func InferProvider(...) {
    ...
%NEW
func NewFunc() { ... }
```
Lines 42-47 (Lookup's closing brace + InferProvider's header) are destroyed.

✅ RIGHT (large file): read once, put two separate SWAP sections in one patch using the SAME TAG — each with its own `%OLD`:
```
[file.go#TAG]
SWAP 41.=41
%OLD
    return table["*/*"]
}
%NEW
    return table["*/*"]
}

func NewFunc() {
    // brand new implementation
}

SWAP 45.=47
%OLD
func InferProvider(model string) string {
    // existing implementation...
%NEW
func InferProvider(model string) string {
    // existing implementation...
```
No re-read, no write token cost.

✅ RIGHT (small file ≤200 lines): `write` the entire file — simplest when token cost is negligible.

**4. SWAP body missing structural wrappers**

❌ WRONG: replacing map entries but omitting the variable declaration:
```
SWAP 17.=22
%OLD
    "deepseek/deepseek-v4-flash": {CacheHit: 0.02, CacheMiss: 1.0, Output: 2.0},
%NEW
    "deepseek/deepseek-v4-flash": {CacheHit: 0.02, CacheMiss: 1.0, Output: 2.0},
    "deepseek/deepseek-v4-pro":   {CacheHit: 0.025, CacheMiss: 3.0, Output: 6.0},
```
Line 17 (`var table = map[string]Price{`) deleted, and lines 18-22 (remaining entries + `}`) also gone. Map literal broken.

✅ RIGHT: include the structural wrapper AND the closing brace in the %OLD/%NEW block:
```
SWAP 17.=22
%OLD
var table = map[string]Price{
    "deepseek/deepseek-v4-flash": {CacheHit: 0.02, CacheMiss: 1.0, Output: 2.0},
}
%NEW
var table = map[string]Price{
    "deepseek/deepseek-v4-flash": {CacheHit: 0.02, CacheMiss: 1.0, Output: 2.0},
    "deepseek/deepseek-v4-pro":   {CacheHit: 0.025, CacheMiss: 3.0, Output: 6.0},
    "deepseek/deepseek-chat":     {CacheHit: 0.1, CacheMiss: 1.0, Output: 2.0},
    "deepseek/":                  {CacheHit: 0.02, CacheMiss: 1.0, Output: 2.0},
}
```

**5. Five separate edit calls on same file — merge into one patch**

❌ WRONG: calling edit 5 times sequentially. Each call changes TAG, forcing 4 re-reads and risking cascading errors.

✅ RIGHT: read the full file once, put all changes in one patch with the SAME TAG:
```
*** Begin Patch
[tui.go#ABCD]
SWAP 445.=445
%OLD
%NEW
    hudPromptTokens   int
    hudComplTokens    int
    hudTurns          int

[tui.go#ABCD]
SWAP 1038.=1039
%OLD
    m.hudPromptTokens += msg.PromptTokens
    m.hudComplTokens += msg.CompletionTokens
%NEW
    m.hudPromptTokens += msg.PromptTokens
    m.hudComplTokens += msg.CompletionTokens
*** End Patch
```

**6. INS.PRE vs INS.POST confusion**

❌ WRONG: wanting to append code at end of a function, but using `INS.PRE 47:` which inserts BEFORE line 47 instead of after:
```
INS.PRE 47:
    return nil
}
```
Actual result: inserted at function START, not end.

✅ RIGHT: check the line number from `read` output — line 47 is the function's opening brace. To append after the function body, use `INS.POST` on the closing brace line, or target the correct line:
```
INS.POST 52:
    return nil
}
```

Mental model: "PRE" = before this line, "POST" = after this line. Confirm the target line's content from the read output before choosing.

**7. TAG invalidation after chained edits — when to re-read**

❌ WRONG: completing a successful edit, seeing the new post-edit context, then immediately issuing another edit to a line OUTSIDE that context window — using the old TAG.

The file on disk has changed (TAG updated), but the edit references the stale TAG. Result: `tag_mismatch` error, or worse — edits applied to wrong lines.

✅ RIGHT chain (no re-read): subsequent target line IS visible in the post-edit context. Example: you just swapped line 50, the delta shows lines 35-65. Your next target is line 58 → safe to chain with same TAG.

✅ RIGHT chain (re-read required): subsequent target line is NOT in the context window (e.g., line 200 after editing line 50). Re-read the file first to get the new TAG, then use it in the next patch.

**8. Forgetting `+` prefix on body lines (legacy format only)**

❌ WRONG: using legacy `SWAP N.=M:` without `+` prefix on body lines:
```
SWAP 5.=5:
    fmt.Println("hello")
```
The line is treated as whitespace, NOT body content. The edit succeeds but produces empty output.

✅ RIGHT (legacy): use `+` prefix. ✅ RIGHT (sentinel): no prefix needed.

**9. Accidentally prefixing operation lines with `+` (legacy format only)**

❌ WRONG: `+ DEL 5` — the `+` makes it literal body text, the file gets a line containing "DEL 5" instead of deleting line 5.

✅ RIGHT: `DEL 5` — operation lines never start with `+`. In sentinel format, body content lines never start with `+` either.

**10. SWAP across block boundaries — count braces**

❌ WRONG: replacing an if/for/func body but SWAP range ends one line early, leaving a stray `}` that closes the outer block:
```
SWAP 5.=9
%OLD
    ...
%NEW
    // new body
```
Original lines 5–9 include the opening `{` and body, but line 10 (the closing `}`) is left behind — now unpaired, breaking the file structure.

✅ RIGHT: before writing the patch, count `{` and `}` in the SWAP range from the `read` output. If they're unbalanced, the range is wrong:
```
SWAP 5.=10
%OLD
    ...
}
%NEW
    // new body
}
```
Rule: the SWAP body MUST include a closing `}` for every opening `{` in the replaced range. If you only see `{` lines being replaced but no `}` lines, extend the range.
