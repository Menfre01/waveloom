package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/filehistory"
)

func TestSeekExact(t *testing.T) {
	lines := strings.Split("package main\n\nfunc greet() string {\n\treturn \"hello\"\n}\n", "\n")
	pattern := []string{"func greet() string {", "\treturn \"hello\"", "}"}
	idx := seekExact(lines, pattern, 0)
	if idx != 2 {
		t.Fatalf("expected idx=2, got %d", idx)
	}
}

func TestSeekExactNotFound(t *testing.T) {
	lines := strings.Split("package main\n\nfunc main() {}\n", "\n")
	pattern := []string{"func greet() string {"}
	idx := seekExact(lines, pattern, 0)
	if idx >= 0 {
		t.Fatalf("expected not found, got idx=%d", idx)
	}
}

func TestSeekRstrip(t *testing.T) {
	lines := strings.Split("func greet() string {   \n\treturn \"hello\"\t\t\n}\n", "\n")
	pattern := []string{"func greet() string {", "\treturn \"hello\"", "}"}
	idx := seekRstrip(lines, pattern, 0)
	if idx != 0 {
		t.Fatalf("expected idx=0, got %d", idx)
	}
}

// ---------------------------------------------------------------------------
// parsePatchFiles — envelope format
// ---------------------------------------------------------------------------

func TestParsePatchFilesDefaultPath(t *testing.T) {
	text := "*** Update File: main.go\n@@ func greet\n func greet() string {\n-    return \"hello\"\n+    return \"hello, \" + name\n }\n"
	files := parsePatchFiles(text, "")
	if len(files) != 1 || files[0].path != "main.go" || len(files[0].hunks) != 1 {
		t.Fatalf("got %d files, path=%q, hunks=%d", len(files), files[0].path, len(files[0].hunks))
	}
}

func TestParsePatchFilesMultiFile(t *testing.T) {
	text := "*** Update File: main.go\n@@ func greet\n \n*** Update File: helper.go\n@@ func helper\n \n"
	files := parsePatchFiles(text, "")
	if len(files) != 2 || files[0].path != "main.go" || files[1].path != "helper.go" {
		t.Fatalf("got %d files, paths: %q, %q", len(files), files[0].path, files[1].path)
	}
}

func TestParsePatchFilesWithEnvelope(t *testing.T) {
	// Envelope format must not create phantom entries
	text := "*** Begin Patch\n*** Update File: main.go\n@@ func greet\n func greet() string {\n-    return \"hello\"\n+    return \"hello, \" + name\n }\n*** End Patch\n"
	files := parsePatchFiles(text, "")
	if len(files) != 1 {
		t.Fatalf("envelope: expected 1 file, got %d (phantom entries?)", len(files))
	}
	if files[0].path != "main.go" {
		t.Fatalf("envelope: expected path main.go, got %q", files[0].path)
	}
	if len(files[0].hunks) != 1 {
		t.Fatalf("envelope: expected 1 hunk, got %d", len(files[0].hunks))
	}
}

func TestParsePatchFilesEnvelopeMultiFile(t *testing.T) {
	text := "*** Begin Patch\n*** Update File: main.go\n@@ func greet\n \n*** Update File: helper.go\n@@ func helper\n \n*** End Patch\n"
	files := parsePatchFiles(text, "")
	if len(files) != 2 {
		t.Fatalf("envelope multi: expected 2 files, got %d", len(files))
	}
	if files[0].path != "main.go" || files[1].path != "helper.go" {
		t.Fatalf("envelope multi: paths: %q, %q", files[0].path, files[1].path)
	}
}

func TestParsePatchFilesRelativePath(t *testing.T) {
	// Relative paths in *** Update File: get resolved against defaultPath's dir
	text := "*** Update File: main.go\n@@ func greet\n func greet() string {\n-    return \"hello\"\n+    return \"hello, \" + name\n }\n"
	defaultPath := filepath.Join(os.TempDir(), "edit-test", "main.go")
	files := parsePatchFiles(text, defaultPath)
	if len(files) != 1 {
		t.Fatalf("relpath: expected 1 file, got %d", len(files))
	}
	expected := filepath.Join(filepath.Dir(defaultPath), "main.go")
	if files[0].path != expected {
		t.Fatalf("relpath: expected %q, got %q", expected, files[0].path)
	}
}

func TestParsePatchFilesRelativePathWithEnvelope(t *testing.T) {
	text := "*** Begin Patch\n*** Update File: main.go\n@@ func greet\n func greet() string {\n-    return \"hello\"\n+    return \"hello, \" + name\n }\n*** End Patch\n"
	defaultPath := filepath.Join(os.TempDir(), "edit-test", "main.go")
	files := parsePatchFiles(text, defaultPath)
	if len(files) != 1 {
		t.Fatalf("relpath+env: expected 1 file, got %d", len(files))
	}
	expected := filepath.Join(filepath.Dir(defaultPath), "main.go")
	if files[0].path != expected {
		t.Fatalf("relpath+env: expected %q, got %q", expected, files[0].path)
	}
}

func TestParsePatchFilesAbsolutePathPreserved(t *testing.T) {
	text := "*** Update File: /absolute/path/to/main.go\n@@ func greet\n \n"
	files := parsePatchFiles(text, filepath.FromSlash("/other/default/path.go"))
	expected := filepath.FromSlash("/absolute/path/to/main.go")
	if len(files) != 1 || files[0].path != expected {
		t.Fatalf("abs: expected %s, got %q", expected, files[0].path)
	}
}

func TestParsePatchFilesBareHunk(t *testing.T) {
	// Bare @@ hunk without *** Update File: — uses defaultPath
	text := "@@ func greet\n func greet() string {\n-    return \"hello\"\n+    return \"hello, \" + name\n }\n"
	files := parsePatchFiles(text, "main.go")
	if len(files) != 1 || files[0].path != "main.go" || len(files[0].hunks) != 1 {
		t.Fatalf("bare: got %d files, path=%q, hunks=%d", len(files), files[0].path, len(files[0].hunks))
	}
}

// ---------------------------------------------------------------------------
// rawBody
// ---------------------------------------------------------------------------

func TestHunkRawBody(t *testing.T) {
	text := "*** Update File: main.go\n@@ func greet\n func greet() string {\n-    return \"hello\"\n+    return \"hello, world\"\n }\n"
	files := parsePatchFiles(text, "")
	if len(files) != 1 || len(files[0].hunks) != 1 {
		t.Fatal("expected 1 file with 1 hunk")
	}
	h := files[0].hunks[0]
	if h.rawBody == "" {
		t.Fatal("rawBody is empty")
	}
	if !strings.Contains(h.rawBody, "func greet()") {
		t.Fatalf("rawBody missing expected content: %q", h.rawBody)
	}
	if strings.Contains(h.rawBody, "@@") {
		t.Fatalf("rawBody should not contain @@ header: %q", h.rawBody)
	}
}

func TestHunkRawBodyCRLF(t *testing.T) {
	// rawBody preserves original line endings; parseDiffHunk normalizes
	text := "*** Update File: main.go\r\n@@ func greet\r\n func greet() string {\r\n-    return \"hello\"\r\n+    return \"hello, world\"\r\n }\r\n"
	files := parsePatchFiles(text, "")
	if len(files) != 1 || len(files[0].hunks) != 1 {
		t.Fatal("expected 1 file with 1 hunk")
	}
	h := files[0].hunks[0]
	if h.rawBody == "" {
		t.Fatal("rawBody is empty for CRLF input")
	}
}

// ---------------------------------------------------------------------------
// parseHunkHeader / parseDiffHunk
// ---------------------------------------------------------------------------

func TestParseHunkHeaderStandard(t *testing.T) {
	oldStart, oldCount, newStart, newCount, heading := parseHunkHeader("@@ -2,3 +2,3 @@ func greet() string {")
	if oldStart != 2 || oldCount != 3 || newStart != 2 || newCount != 3 {
		t.Fatalf("standard: got old=%d,%d new=%d,%d heading=%q", oldStart, oldCount, newStart, newCount, heading)
	}
	if heading != "func greet() string {" {
		t.Fatalf("standard: heading=%q", heading)
	}
}

func TestParseHunkHeaderOmittedCounts(t *testing.T) {
	oldStart, oldCount, newStart, newCount, heading := parseHunkHeader("@@ -1 +1 @@")
	if oldStart != 1 || oldCount != 1 || newStart != 1 || newCount != 1 {
		t.Fatalf("omitted: got old=%d,%d new=%d,%d", oldStart, oldCount, newStart, newCount)
	}
	if heading != "" {
		t.Fatalf("omitted: unexpected heading=%q", heading)
	}
}

func TestParseHunkHeaderNonStandard(t *testing.T) {
	// Bare @@ func name (no line numbers) — parsed as inferFromBody=true later
	oldStart, _, newStart, _, _ := parseHunkHeader("@@ func greet")
	if oldStart != 0 || newStart != 0 {
		t.Fatalf("nonstd: expected 0,0 got %d,%d", oldStart, newStart)
	}
}

func TestParseDiffHunk(t *testing.T) {
	header := "@@ -2,3 +2,3 @@ func greet() string {"
	body := " func greet() string {\n-    return \"hello\"\n+    return \"hello, world\"\n }\n"
	dh := parseDiffHunk(header, body, "main.go")
	if dh == nil {
		t.Fatal("parseDiffHunk returned nil")
	}
	if dh.FilePath != "main.go" {
		t.Fatalf("path: expected main.go, got %q", dh.FilePath)
	}
	if dh.OldStart != 2 || dh.OldCount != 3 || dh.NewStart != 2 || dh.NewCount != 3 {
		t.Fatalf("ranges: old=%d,%d new=%d,%d", dh.OldStart, dh.OldCount, dh.NewStart, dh.NewCount)
	}
	if len(dh.Lines) != 4 {
		t.Fatalf("lines: expected 4, got %d", len(dh.Lines))
	}
	// Line 0: context "func greet() string {"
	if dh.Lines[0].Kind != DiffCtx || dh.Lines[0].Content != "func greet() string {" {
		t.Fatalf("line 0: %s %q", dh.Lines[0].Kind, dh.Lines[0].Content)
	}
	// Line 1: deletion
	if dh.Lines[1].Kind != DiffDel || dh.Lines[1].Content != "    return \"hello\"" {
		t.Fatalf("line 1: %s %q", dh.Lines[1].Kind, dh.Lines[1].Content)
	}
	// Line 2: addition
	if dh.Lines[2].Kind != DiffAdd || dh.Lines[2].Content != "    return \"hello, world\"" {
		t.Fatalf("line 2: %s %q", dh.Lines[2].Kind, dh.Lines[2].Content)
	}
	// Line 3: context "}"
	if dh.Lines[3].Kind != DiffCtx || dh.Lines[3].Content != "}" {
		t.Fatalf("line 3: %s %q", dh.Lines[3].Kind, dh.Lines[3].Content)
	}
}

func TestParseDiffHunkEmptyBody(t *testing.T) {
	dh := parseDiffHunk("@@ -1,3 +1,3 @@", "", "main.go")
	if dh != nil {
		t.Fatal("expected nil for empty body")
	}
}

func TestParseDiffHunkCRLFBody(t *testing.T) {
	header := "@@ -2,3 +2,3 @@ func greet() string {"
	body := " func greet() string {\r\n-    return \"hello\"\r\n+    return \"hello, world\"\r\n }\r\n"
	dh := parseDiffHunk(header, body, "main.go")
	if dh == nil {
		t.Fatal("parseDiffHunk returned nil for CRLF body")
	}
	if len(dh.Lines) != 4 {
		t.Fatalf("CRLF: expected 4 lines, got %d", len(dh.Lines))
	}
	// Lines should not contain \r
	for i, l := range dh.Lines {
		if strings.Contains(l.Content, "\r") {
			t.Fatalf("line %d still contains \\r: %q", i, l.Content)
		}
	}
}

func TestParseDiffHunkNonStandardHeader(t *testing.T) {
	// Bare @@ func greet — inferFromBody=true
	header := "@@ func greet"
	body := " func greet() string {\n-    return \"hello\"\n+    return \"hello, world\"\n }\n"
	dh := parseDiffHunk(header, body, "main.go")
	if dh == nil {
		t.Fatal("parseDiffHunk returned nil for non-standard header")
	}
	if dh.OldStart != 1 || dh.NewStart != 1 {
		t.Fatalf("nonstd: expected start=1, got old=%d new=%d", dh.OldStart, dh.NewStart)
	}
	if dh.Heading != "func greet" {
		t.Fatalf("nonstd: heading=%q", dh.Heading)
	}
}

// ---------------------------------------------------------------------------
// computeWriteDiff
// ---------------------------------------------------------------------------

func TestComputeWriteDiffNewFile(t *testing.T) {
	hunks := computeWriteDiff("", "package main\n\nfunc main() {\n}\n", "main.go")
	if len(hunks) != 1 {
		t.Fatalf("new file: expected 1 hunk, got %d", len(hunks))
	}
	h := hunks[0]
	if h.OldCount != 0 || h.NewCount != 4 {
		t.Fatalf("new file: old=%d new=%d", h.OldCount, h.NewCount)
	}
	for _, l := range h.Lines {
		if l.Kind != DiffAdd {
			t.Fatalf("new file: all lines should be additions, got %s", l.Kind)
		}
	}
}

func TestComputeWriteDiffUpdate(t *testing.T) {
	old := "package main\n\nfunc greet() string {\n    return \"hello\"\n}\n"
	new := "package main\n\nfunc greet() string {\n    return \"hello, world\"\n}\n"
	hunks := computeWriteDiff(old, new, "main.go")
	if len(hunks) != 1 {
		t.Fatalf("update: expected 1 hunk, got %d", len(hunks))
	}
	h := hunks[0]
	hasDel := false
	hasAdd := false
	for _, l := range h.Lines {
		if l.Kind == DiffDel && strings.Contains(l.Content, "\"hello\"") {
			hasDel = true
		}
		if l.Kind == DiffAdd && strings.Contains(l.Content, "\"hello, world\"") {
			hasAdd = true
		}
	}
	if !hasDel || !hasAdd {
		t.Fatal("update: expected both deletion and addition lines")
	}
}

func TestComputeWriteDiffIdentical(t *testing.T) {
	content := "package main\n\nfunc main() {\n}\n"
	hunks := computeWriteDiff(content, content, "main.go")
	if len(hunks) != 0 {
		t.Fatalf("identical: expected 0 hunks, got %d", len(hunks))
	}
}

func TestComputeWriteDiffEmpty(t *testing.T) {
	hunks := computeWriteDiff("", "", "main.go")
	if len(hunks) != 0 {
		t.Fatalf("empty: expected 0 hunks, got %d", len(hunks))
	}
}

func TestComputeWriteDiffTrailingNewline(t *testing.T) {
	// Content that differs only in trailing newline — should be treated as identical
	old := "hello\n"
	new := "hello"
	hunks := computeWriteDiff(old, new, "main.go")
	// Both normalize to ["hello"] after trailing-empty-line stripping, so nil
	if len(hunks) != 0 {
		t.Fatalf("trailing-nl: expected 0 hunks, got %d", len(hunks))
	}
}

// ---------------------------------------------------------------------------
// Integration: full ApplyHunk with envelope + path resolution
// ---------------------------------------------------------------------------

func TestApplyHunkEnvelopeIntegration(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	original := "package main\n\nfunc greet() string {\n    return \"hello\"\n}\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	// Envelope format with relative path in *** Update File:
	patch := "*** Begin Patch\n*** Update File: main.go\n@@ func greet\n func greet() string {\n-    return \"hello\"\n+    return \"hello, world\"\n }\n*** End Patch\n"
	results, err := ApplyHunk(context.Background(), filePath, patch, nil)
	if err != nil {
		t.Fatalf("ApplyHunk error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Error != "" {
		t.Fatalf("hunk error: %s", r.Error)
	}
	if r.RawBody == "" {
		t.Fatal("RawBody is empty")
	}

	// Verify file content
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"hello, world\"") {
		t.Fatalf("file not updated: %s", string(data))
	}
}

func TestApplyHunkEnvelopeMultiFileIntegration(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.go")
	utilPath := filepath.Join(dir, "util.go")

	if err := os.WriteFile(mainPath, []byte("package main\n\nfunc greet() string {\n    return \"hello\"\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(utilPath, []byte("package main\n\nfunc helper(x int) int {\n    return x * 2\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	patch := "*** Begin Patch\n*** Update File: main.go\n@@ func greet\n func greet() string {\n-    return \"hello\"\n+    return \"hello, world\"\n }\n*** Update File: util.go\n@@ func helper\n func helper(x int) int {\n-    return x * 2\n+    return x * 3\n }\n*** End Patch\n"
	results, err := ApplyHunk(context.Background(), mainPath, patch, nil)
	if err != nil {
		t.Fatalf("ApplyHunk error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Error != "" {
			t.Fatalf("hunk error on %s: %s", r.File, r.Error)
		}
		if r.RawBody == "" {
			t.Fatalf("RawBody empty on %s", r.File)
		}
	}

	mainData, _ := os.ReadFile(mainPath)
	utilData, _ := os.ReadFile(utilPath)
	if !strings.Contains(string(mainData), "\"hello, world\"") {
		t.Fatal("main.go not updated")
	}
	if !strings.Contains(string(utilData), "x * 3") {
		t.Fatal("util.go not updated")
	}
}

// ---------------------------------------------------------------------------
// Integration: FileHistory tracking
// ---------------------------------------------------------------------------

func TestApplyHunkFileHistoryTracking(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	original := "package main\n\nfunc greet() string {\n    return \"hello\"\n}\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	// Set up FileHistory context
	sessionDir := t.TempDir()
	fh := filehistory.NewState()
	ctx := context.Background()
	ctx = filehistory.WithFileHistory(ctx, fh)
	ctx = filehistory.WithSessionDir(ctx, sessionDir)
	ctx = filehistory.WithMessageID(ctx, "msg-test-1")

	patch := "*** Begin Patch\n*** Update File: main.go\n@@ func greet\n func greet() string {\n-    return \"hello\"\n+    return \"hello, world\"\n }\n*** End Patch\n"
	results, err := ApplyHunk(ctx, filePath, patch, nil)
	if err != nil {
		t.Fatalf("ApplyHunk error: %v", err)
	}
	if len(results) != 1 || results[0].Error != "" {
		t.Fatalf("hunk failed: %+v", results)
	}

	// Verify a backup was created in the file-history directory
	backupDir := filepath.Join(sessionDir, "file-history")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("backup dir not found: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no backup files created — TrackEdit was not called")
	}
	t.Logf("backup files: %d", len(entries))
	for _, e := range entries {
		t.Logf("  %s", e.Name())
	}
}

func TestApplyHunkWithoutFileHistory(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	original := "package main\n\nfunc greet() string {\n    return \"hello\"\n}\n"
	if err := os.WriteFile(filePath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	// No FileHistory in context — should not panic
	ctx := context.Background()
	patch := "*** Begin Patch\n*** Update File: main.go\n@@ func greet\n func greet() string {\n-    return \"hello\"\n+    return \"hello, world\"\n }\n*** End Patch\n"
	results, err := ApplyHunk(ctx, filePath, patch, nil)
	if err != nil {
		t.Fatalf("ApplyHunk error: %v", err)
	}
	if len(results) != 1 || results[0].Error != "" {
		t.Fatalf("hunk failed: %+v", results)
	}
}

// ---------------------------------------------------------------------------
// normalizeUnicode2
// ---------------------------------------------------------------------------

func TestNormalizeUnicode2(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Fullwidth punctuation → ASCII
		{"\uFF1A", ":"}, // ： → :
		{"\uFF0C", ","}, // ， → ,
		{"\uFF1B", ";"}, // ； → ;
		{"\uFF1F", "?"}, // ？ → ?
		{"\uFF01", "!"}, // ！ → !
		{"\uFF08", "("}, // （ → (
		{"\uFF09", ")"}, // ） → )
		{"\uFF3B", "["}, // ［ → [
		{"\uFF3D", "]"}, // ］ → ]
		{"\uFF5B", "{"}, // ｛ → {
		{"\uFF5D", "}"}, // ｝ → }
		{"\uFF5E", "~"}, // ～ → ~
		// CJK punctuation → ASCII
		{"\u3001", ","}, // 、 → ,
		{"\u3002", "."}, // 。 → .
		// Mixed: fullwidth colon in Chinese text → halfwidth
		{"\u6587\u4EF6\uFF1A\u6D4B\u8BD5", "\u6587\u4EF6:\u6D4B\u8BD5"}, // 文件：测试 → 文件:测试
		// Existing mappings should still work
		{"\u2014", "-"}, // — → -
		{"\u201C\u201D", "\"\""}, // "" → ""
		{"\u00A0", " "}, // NBSP → space
		// Fullwidth letters/digits should NOT be affected
		{"\uFF21\uFF22", "\uFF21\uFF22"}, // ＡＢ → ＡＢ (unchanged)
		{"\uFF10\uFF19", "\uFF10\uFF19"}, // ０９ → ０９ (unchanged)
		// Invisible characters → removed
		{"a\u200Bb", "ab"},                  // ZWSP removed
		{"\uFEFFtext", "text"},              // BOM removed
		{"line1\u2028line2", "line1line2"},  // LINE SEPARATOR removed
		{"p1\u2029p2", "p1p2"},              // PARAGRAPH SEPARATOR removed
		{"a\u200Cb", "ab"},                  // ZWNJ removed
		{"a\u200Db", "ab"},                  // ZWJ removed
		{"\u00ADsoft", "soft"},              // SOFT HYPHEN removed
		{"\u200E\u200Ftext", "text"},        // LRM/RLM removed
		{"\u2060middle", "middle"},           // WORD JOINER removed
		// CJK brackets → ASCII
		{"\u3008\u3009", "<>"},   // 〈〉 → <>
		{"\u300A\u300B", "<>"},   // 《》 → <>
		{"\u300C\u300D", "\"\""}, // 「」 → ""
		{"\u300E\u300F", "\"\""}, // 『』 → ""
		{"\u3010\u3011", "[]"},   // 【】 → []
		{"\u3014\u3015", "()"},   // 〔〕 → ()
		{"\u3016\u3017", "[]"},   // 〖〗 → []
		// CJK quotation marks
		{"\u301D\u301E\u301F", "\"\"\""}, // 〝〞〟 → """
		// CJK wave dashes
		{"\u301C", "~"}, // 〜 → ~
		{"\u3030", "~"}, // 〰 → ~
		// Fullwidth white parentheses
		{"\uFF5F\uFF60", "()"}, // ｟｠ → ()
		// Additional punctuation
		{"\u2026", "."},           // … → .
		{"\u2022", "-"},           // • → -
		{"\u00AB\u00BB", "\"\""},  // «» → ""
		{"\u00B7", "."},           // · → .
		{"\u2032", "'"},           // ′ → '
		{"\u2033", "\""},          // ″ → "
		// Single guillemets
		{"\u2039\u203A", "''"}, // ‹› → ''
		// Fraction slash
		{"\u2044", "/"}, // ⁄ → /
		// Small form dashes
		{"\uFE58", "-"}, // ﹘ → -
		{"\uFE63", "-"}, // ﹣ → -
		// Multi-em dashes
		{"\u2E3A", "-"}, // ⸺ → -
		{"\u2E3B", "-"}, // ⸻ → -
		// Hyphen bullet
		{"\u2043", "-"}, // ⁃ → -
		// Halfwidth Katakana punctuation
		{"\uFF61", "."},           // ｡ → .
		{"\uFF62\uFF63", "\"\""},  // ｢｣ → ""
		{"\uFF64", ","},           // ､ → ,
		// CJK Compatibility Forms → ASCII
		{"\uFE35\uFE36", "()"},    // ︵︶ → ()
		{"\uFE37\uFE38", "{}"},    // ︷︸ → {}
		{"\uFE39\uFE3A", "[]"},    // ︹︺ → []
		{"\uFE3D\uFE3E", "\"\""},  // ︽︾ → ""
		{"\uFE3F\uFE40", "<>"},    // ︿﹀ → <>
		{"\uFE41\uFE42", "\"\""},  // ﹁﹂ → ""
		{"\uFE30", ":"},           // ︰ → :
		{"\uFE31", "|"},           // ︱ → |
		{"\uFE45\uFE46", "??"},    // ﹅﹆ → ??
		{"\uFE4D\uFE4E\uFE4F", "___"}, // ﹍﹎﹏ → ___
		// Small Form Variants → ASCII
		{"\uFE50", ","},  // ﹐ → ,
		{"\uFE51", ","},  // ﹑ → ,
		{"\uFE52", "."},  // ﹒ → .
		{"\uFE54", ";"},  // ﹔ → ;
		{"\uFE55", ":"},  // ﹕ → :
		{"\uFE56", "?"},  // ﹖ → ?
		{"\uFE57", "!"},  // ﹗ → !
		{"\uFE59\uFE5A", "()"},  // ﹙﹚ → ()
		{"\uFE5B\uFE5C", "{}"},  // ﹛﹜ → {}
		{"\uFE5D\uFE5E", "[]"},  // ﹝﹞ → []
		{"\uFE5F", "#"},  // ﹟ → #
		{"\uFE60", "&"},  // ﹠ → &
		{"\uFE61", "*"},  // ﹡ → *
		{"\uFE62", "+"},  // ﹢ → +
		{"\uFE64\uFE65", "<>"},  // ﹤﹥ → <>
		{"\uFE66", "="},  // ﹦ → =
		{"\uFE68", "\\"}, // ﹨ → \
		{"\uFE69", "$"},  // ﹩ → $
		{"\uFE6A", "%"},  // ﹪ → %
		{"\uFE6B", "@"},  // ﹫ → @
		// Superscript/subscript operators → ASCII
		{"\u207A\u207B\u207C\u207D\u207E", "+-=()"}, // ⁺⁻⁼⁽⁾ → +-=()
		{"\u208A\u208B\u208C\u208D\u208E", "+-=()"}, // ₊₋₌₍₎ → +-=()
		// Math symbols → ASCII
		{"\u2217", "*"}, // ∗ → *
		{"\u2236", ":"}, // ∶ → :
		// Additional space variants
		{"\u2000", " "}, // EN QUAD → space
		{"\u2001", " "}, // EM QUAD → space
		// Tilde operator
		{"\u223C", "~"}, // ∼ → ~
	}
	for _, tt := range tests {
		got := normalizeUnicode2(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeUnicode2(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// TestRegression_FullwidthColonHunkMatch 回归:全角冒号(：)在半角 hunk 中无法匹配。
// 根因：normalizeUnicode2 未涵盖 U+FF1A 全角冒号，而中文文本中全角标点极为常见。
func TestRegression_FullwidthColonHunkMatch(t *testing.T) {
	// 文件使用全角冒号 (U+FF1A)
	fileContent := "Release \u6807\u9898\uFF1A\u6D4B\u8BD5" // Release 标题：测试

	// hunk 使用半角冒号 (:),LLM 生成 hunk 时的典型行为
	hunkText := "*** Begin Patch\n*** Update File: test.md\n@@\n Release 标题:测试\n*** End Patch\n"

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	if err := os.WriteFile(filePath, []byte(fileContent), 0o644); err != nil {
		t.Fatal(err)
	}

	states := NewReadStateStore()
	states.Record(filePath, fileContent)

	results, err := ApplyHunk(context.Background(), filePath, hunkText, states)
	if err != nil {
		t.Fatalf("ApplyHunk error: %v", err)
	}
	if len(results) != 1 || results[0].Error != "" {
		t.Fatalf("hunk not found (unicode norm should match fullwidth colon): %+v", results)
	}
}

// TestRegression_BareEmptyLineTreatedAsContext verifies that bare empty lines
// (without space prefix) in hunk bodies are tolerated as empty context lines.
// LLMs and humans naturally use blank lines for visual separation when writing
// hunks, even though strict unified diff expects a leading space.
func TestRegression_BareEmptyLineTreatedAsContext(t *testing.T) {
	// File has two functions separated by an empty line.
	fileContent := "package main\n\nfunc helper() int {\n    return 42\n}\n\nfunc greet() string {\n    return \"hello\"\n}\n"

	// Hunk inserts a comment above greet(). The empty line between functions
	// is a bare empty line (no space prefix) — exactly what LLMs and humans write.
	hunkText := "*** Begin Patch\n*** Update File: main.go\n@@ func greet\n func helper() int {\n     return 42\n }\n\n+// sayHi greets the user\n+\n func greet() string {\n*** End Patch\n"

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(filePath, []byte(fileContent), 0o644); err != nil {
		t.Fatal(err)
	}

	states := NewReadStateStore()
	states.Record(filePath, fileContent)

	results, err := ApplyHunk(context.Background(), filePath, hunkText, states)
	if err != nil {
		t.Fatalf("ApplyHunk error: %v", err)
	}
	if len(results) != 1 || results[0].Error != "" {
		t.Fatalf("hunk not found (bare empty line should be tolerated): %+v", results)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := "package main\n\nfunc helper() int {\n    return 42\n}\n\n// sayHi greets the user\n\nfunc greet() string {\n    return \"hello\"\n}\n"
	if string(data) != expected {
		t.Fatalf("unexpected file content:\ngot:  %q\nwant: %q", string(data), expected)
	}
}

// TestRegression_MultipleTrailingEmptyLinesSkipped verifies that multiple
// consecutive bare empty lines at the end of a hunk body are ALL skipped,
// not partially treated as context.
func TestRegression_MultipleTrailingEmptyLinesSkipped(t *testing.T) {
	fileContent := "line1\nline2\nline3\n"

	// Hunk ends with two trailing newlines (two bare empty lines before *** End Patch).
	hunkText := "*** Begin Patch\n*** Update File: test.txt\n@@\n line1\n-line2\n+lineTWO\n line3\n\n\n*** End Patch\n"

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte(fileContent), 0o644); err != nil {
		t.Fatal(err)
	}

	states := NewReadStateStore()
	states.Record(filePath, fileContent)

	results, err := ApplyHunk(context.Background(), filePath, hunkText, states)
	if err != nil {
		t.Fatalf("ApplyHunk error: %v", err)
	}
	if len(results) != 1 || results[0].Error != "" {
		t.Fatalf("hunk not found (multiple trailing empty lines should be skipped): %+v", results)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := "line1\nlineTWO\nline3\n"
	if string(data) != expected {
		t.Fatalf("unexpected file content:\ngot:  %q\nwant: %q", string(data), expected)
	}
}

// TestRegression_BareEmptyLineAtHunkStart verifies that a bare empty line
// right after the @@ header (first line of hunk body is empty) is tolerated.
func TestRegression_BareEmptyLineAtHunkStart(t *testing.T) {
	fileContent := "package main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"hi\")\n}\n"

	// Hunk body starts with a bare empty line (no space prefix).
	hunkText := "*** Begin Patch\n*** Update File: main.go\n@@\n\n import \"fmt\"\n*** End Patch\n"

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(filePath, []byte(fileContent), 0o644); err != nil {
		t.Fatal(err)
	}

	states := NewReadStateStore()
	states.Record(filePath, fileContent)

	results, err := ApplyHunk(context.Background(), filePath, hunkText, states)
	if err != nil {
		t.Fatalf("ApplyHunk error: %v", err)
	}
	if len(results) != 1 || results[0].Error != "" {
		t.Fatalf("hunk not found (bare empty line at hunk start should be tolerated): %+v", results)
	}
}

// TestRegression_TrailingEmptyLineStillSkipped verifies that a bare empty line
// at the END of a hunk body (trailing newline artifact) is still skipped,
// not treated as an extra empty context line that would break matching.
func TestRegression_TrailingEmptyLineStillSkipped(t *testing.T) {
	fileContent := "line1\nline2\nline3\n"

	// Hunk ends with a trailing newline — the empty line after "line3" should be skipped.
	hunkText := "*** Begin Patch\n*** Update File: test.txt\n@@\n line1\n-line2\n+lineTWO\n line3\n*** End Patch\n"

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte(fileContent), 0o644); err != nil {
		t.Fatal(err)
	}

	states := NewReadStateStore()
	states.Record(filePath, fileContent)

	results, err := ApplyHunk(context.Background(), filePath, hunkText, states)
	if err != nil {
		t.Fatalf("ApplyHunk error: %v", err)
	}
	if len(results) != 1 || results[0].Error != "" {
		t.Fatalf("hunk not found (trailing empty line should still be skipped): %+v", results)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := "line1\nlineTWO\nline3\n"
	if string(data) != expected {
		t.Fatalf("unexpected file content:\ngot:  %q\nwant: %q", string(data), expected)
	}
}

// TestRegression_MultipleBareEmptyLines verifies that multiple consecutive
// bare empty lines in a hunk body are all tolerated.
func TestRegression_MultipleBareEmptyLines(t *testing.T) {
	fileContent := "package main\n\n\nfunc main() {\n}\n"

	// Hunk has two consecutive bare empty lines between context lines.
	hunkText := "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n\n\n func main() {\n*** End Patch\n"

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(filePath, []byte(fileContent), 0o644); err != nil {
		t.Fatal(err)
	}

	states := NewReadStateStore()
	states.Record(filePath, fileContent)

	results, err := ApplyHunk(context.Background(), filePath, hunkText, states)
	if err != nil {
		t.Fatalf("ApplyHunk error: %v", err)
	}
	if len(results) != 1 || results[0].Error != "" {
		t.Fatalf("hunk not found (multiple bare empty lines should be tolerated): %+v", results)
	}
}

// TestRegression_BareEmptyLineBetweenHunks verifies that a bare empty line
// between two hunks in the same file is skipped (next line is @@, a boundary).
func TestRegression_BareEmptyLineBetweenHunks(t *testing.T) {
	fileContent := "line1\nline2\nlineA\nlineB\n"

	// Two hunks separated by a bare empty line.
	hunkText := "*** Begin Patch\n*** Update File: test.txt\n@@\n line1\n-line2\n+lineTWO\n\n@@\n-lineA\n+lineALPHA\n lineB\n*** End Patch\n"

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte(fileContent), 0o644); err != nil {
		t.Fatal(err)
	}

	states := NewReadStateStore()
	states.Record(filePath, fileContent)

	results, err := ApplyHunk(context.Background(), filePath, hunkText, states)
	if err != nil {
		t.Fatalf("ApplyHunk error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 hunk results, got %d: %+v", len(results), results)
	}
	for i, r := range results {
		if r.Error != "" {
			t.Fatalf("hunk %d failed: %+v", i, r)
		}
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := "line1\nlineTWO\nlineALPHA\nlineB\n"
	if string(data) != expected {
		t.Fatalf("unexpected file content:\ngot:  %q\nwant: %q", string(data), expected)
	}
}
