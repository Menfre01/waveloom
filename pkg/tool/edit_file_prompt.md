## Edit File

`edit` — unified diff hunk format. Every code change uses the same pattern.

### Format

```
@@ [optional header]
 context line (space prefix)
-old line to remove
+new line to add
 context line
```

### Basic usage

**Replace** (change function signature):
```
@@ func greet
 func greet() string {
-    return "hello"
+    return "hello, " + name
 }
```

**Delete** (remove a function and its calls):
```
@@
 func main() {
-    y := oldHelper(5)
-    println(y)
 }
```

**Insert** (add error handling):
```
@@ func readConfig
 func readConfig() string {
     data, _ := os.ReadFile("config.txt")
+    if err != nil {
+        return ""
+    }
     return string(data)
```

### Multi-hunk (same file, same call)

```
@@ func greet
 func greet() string {
-    return "hello"
+    return "hello, " + name
 }

@@ func main
 func main() {
-    msg := greet()
+    msg := greet("world")
     println(msg)
```

### Tips

- **Read first**: always `read` the file before editing — the engine validates file state
- **Context lines**: include 1-2 unchanged lines around each change for unique matching
- **Exact whitespace**: copy tabs/spaces exactly from the read output
- **Matching**: engine tries exact → trailing whitespace → full trim → unicode normalize
- **Failed hunk**: re-read the file, check the diagnostics output, adjust context lines, retry

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
