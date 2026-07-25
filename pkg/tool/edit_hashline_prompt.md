## Edit File (Hashline)

`read` → TAG + line numbers → `edit` with TAG + line numbers. Never reproduce old code — only TAG, line numbers, and new content.

### Operations

```
SWAP N.=M:   Replace lines N–M with body. N.=N for single line.
DEL N.=M    Delete lines N–M. DEL N for single line.
INS.PRE N:  Insert before line N.  INS.POST N: after line N.
INS.HEAD:   Insert at file start.  INS.TAIL: insert at file end.
REM         Delete entire file.     MV DEST   rename/move.
```
Line numbers: `read` output `N:CONTENT`. Ranges inclusive. Use `N.=M` not `N:=M`.

### Patch format

```
*** Begin Patch
[src/pkg/foo.go#A1B2]       ← TAG from read
SWAP 2.=2:
+    fmt.Println("hello")   ← body lines start with +
INS.TAIL:
+    // end of file
*** End Patch
```
Multiple `[PATH#TAG]` sections in one patch merge atomically. Do NOT insert `*** End Patch` between sections.
**Pitfall**: adding `+` before an operation line (e.g. `+ DEL 5`) makes it literal body text — the edit succeeds but does nothing.

### Edit vs Write

| File size | 1-2 changes | 3-5 changes (scattered) | 6+ changes |
|---|---|---|---|
| ≤200 lines | edit | write | write |
| >500 lines | edit multi-step | edit multi-step | edit multi-step |

**After edit**: skim the edit delta — `✓ SUCCESS` means the tool executed, not that your intent was correct.
**Chain editing**: reuse TAG when targets are in context window. Re-read for different files or `tag_mismatch`.
