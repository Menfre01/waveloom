package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileSuccess(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(filePath, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "hello",
		NewString: "goodbye",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "goodbye world\n" {
		t.Errorf("file content = %q, want %q", string(data), "goodbye world\n")
	}

	// 验证精简 Content（给 LLM）
	if !contains(result.Content, "Edited file") {
		t.Error("Content should indicate edit")
	}
	if !contains(result.Content, "+1 -1 lines") {
		t.Errorf("Content should show change stats, got: %s", result.Content)
	}

	// 验证 DiffHunks（给 TUI）
	if len(result.Meta.DiffHunks) != 1 {
		t.Fatalf("DiffHunks count = %d, want 1", len(result.Meta.DiffHunks))
	}
	h := result.Meta.DiffHunks[0]
	if h.OldStart != 1 || h.NewStart != 1 {
		t.Errorf("hunk start = (%d, %d), want (1, 1)", h.OldStart, h.NewStart)
	}
	added, removed := h.Stats()
	if added != 1 || removed != 1 {
		t.Errorf("hunk stats = (+%d -%d), want (+1 -1)", added, removed)
	}

	// 验证 hunk 中有删除行和新增行
	var hasDel, hasAdd bool
	for _, l := range h.Lines {
		if l.Kind == DiffDel {
			hasDel = true
			if l.Content != "hello" {
				t.Errorf("del line content = %q, want %q", l.Content, "hello")
			}
		}
		if l.Kind == DiffAdd {
			hasAdd = true
			if l.Content != "goodbye" {
				t.Errorf("add line content = %q, want %q", l.Content, "goodbye")
			}
		}
	}
	if !hasDel || !hasAdd {
		t.Error("hunk should contain both del and add lines")
	}
}

func TestEditFileSearchHint(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc process() {\n\tfmt.Println(\"hello\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "fmt.Println(\"process\")", // 文件中是 "hello" 不是 "process"
		NewString: "fmt.Println(\"done\")",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for no match")
	}
	if result.Error.Kind != ErrKindNoMatch {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindNoMatch)
	}
	// 应包含搜索线索 — 输出编辑距离最接近的行
	if !contains(result.Error.Message, "closest matches") {
		t.Errorf("Error should include closest matches hint: %s", result.Error.Message)
	}
}

func TestEditFileEmptyOldString(t *testing.T) {
	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  "/tmp/any.txt",
		OldString: "",
		NewString: "replacement",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for empty old_string")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindInvalidArgs)
	}
}

func TestEditFileNoMatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(filePath, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "not found",
		NewString: "replacement",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for no match")
	}
	if result.Error.Kind != ErrKindNoMatch {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindNoMatch)
	}
	if result.Error.Class != ErrorClassRecoverable {
		t.Errorf("Error.Class = %v, want ErrorClassRecoverable", result.Error.Class)
	}
}

func TestEditFileMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(filePath, []byte("foo foo foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "foo",
		NewString: "bar",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for multiple matches")
	}
	if result.Error.Kind != ErrKindMultipleMatch {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindMultipleMatch)
	}
}

func TestEditFileReplaceAll(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(filePath, []byte("foo bar foo baz foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:   filePath,
		OldString:  "foo",
		NewString:  "qux",
		ReplaceAll: true,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	// 验证所有匹配被替换
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "qux bar qux baz qux\n" {
		t.Errorf("file content = %q, want %q", string(data), "qux bar qux baz qux\n")
	}
}

func TestEditFileNotFound(t *testing.T) {
	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  "/nonexistent/file.txt",
		OldString: "old",
		NewString: "new",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for missing file")
	}
	if result.Error.Kind != ErrKindFileNotFound {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindFileNotFound)
	}
}

func TestEditFileExactMatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "code.go")
	content := `package main

func hello() {
	fmt.Println("hello")
}

func world() {
	fmt.Println("world")
}
`
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: `fmt.Println("hello")`,
		NewString: `fmt.Println("hi")`,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !contains(string(data), `fmt.Println("hi")`) {
		t.Error("file should contain the replacement")
	}
	if !contains(string(data), `fmt.Println("world")`) {
		t.Error("file should still contain the other function")
	}
}

func TestEditFileDiffHunks(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "code.go")
	content := `package main

func hello() {
	fmt.Println("hello")
}

func world() {
	fmt.Println("world")
}
`
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: `	fmt.Println("hello")`,
		NewString: `	fmt.Println("hi")`,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	if len(result.Meta.DiffHunks) != 1 {
		t.Fatalf("DiffHunks count = %d, want 1", len(result.Meta.DiffHunks))
	}
	h := result.Meta.DiffHunks[0]

	// hunk 应包含上下文行（前后各 ≤3 行）
	// matchLine 是 "func hello() {"（原文第3行，0-based），ctxStart=1
	if h.OldStart != 1 {
		t.Errorf("OldStart = %d, want 1", h.OldStart)
	}

	// 验证行号映射正确性
	var ctxLines, delLines, addLines int
	for _, l := range h.Lines {
		switch l.Kind {
		case DiffCtx:
			ctxLines++
			if l.OldNum == 0 || l.NewNum == 0 {
				t.Errorf("ctx line should have both old and new line numbers, got OldNum=%d NewNum=%d", l.OldNum, l.NewNum)
			}
		case DiffDel:
			delLines++
			if l.NewNum != 0 {
				t.Errorf("del line NewNum should be 0, got %d", l.NewNum)
			}
		case DiffAdd:
			addLines++
			if l.OldNum != 0 {
				t.Errorf("add line OldNum should be 0, got %d", l.OldNum)
			}
		}
	}

	if delLines != 1 {
		t.Errorf("del lines = %d, want 1", delLines)
	}
	if addLines != 1 {
		t.Errorf("add lines = %d, want 1", addLines)
	}
	if ctxLines < 1 {
		t.Errorf("ctx lines = %d, want at least 1", ctxLines)
	}

	// Stats 应与逐行统计一致
	a, r := h.Stats()
	if a != addLines || r != delLines {
		t.Errorf("Stats() = (+%d -%d), want (+%d -%d)", a, r, addLines, delLines)
	}
}

func TestEditFileDiffHunksReplaceAll(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "code.go")
	// 两个匹配间隔仅 1 行，默认 contextLines=3 下窗口重叠，应合并为一个 hunk
	content := `line 1
foo
line 3
foo
line 5
`
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:   filePath,
		OldString:  "foo",
		NewString:  "bar",
		ReplaceAll: true,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	// 窗口重叠 → 合并为 1 个 hunk（与 git diff -U3 行为一致）
	if len(result.Meta.DiffHunks) != 1 {
		t.Fatalf("DiffHunks count = %d, want 1 (merged due to overlapping windows)", len(result.Meta.DiffHunks))
	}

	h := result.Meta.DiffHunks[0]
	added, removed := h.Stats()
	if added != 2 || removed != 2 {
		t.Errorf("merged hunk stats = (+%d -%d), want (+2 -2)", added, removed)
	}

	// 验证 hunk 头正确
	if h.OldStart != 1 || h.NewStart != 1 {
		t.Errorf("hunk start = (%d, %d), want (1, 1)", h.OldStart, h.NewStart)
	}

	// 验证所有变更行都在 hunk 内
	var foundFirst, foundSecond bool
	for _, l := range h.Lines {
		if l.Kind == DiffAdd && l.Content == "bar" {
			if l.NewNum == 2 {
				foundFirst = true
			}
			if l.NewNum == 4 {
				foundSecond = true
			}
		}
	}
	if !foundFirst || !foundSecond {
		t.Error("merged hunk should contain both bar additions")
	}
}

// TestEditFileDiffHunksReplaceAllShift 验证 replace_all 时不同行数替换的累积偏移。
func TestEditFileDiffHunksReplaceAllShift(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "shift.go")
	// 两处匹配间隔足够大（> 2*contextLines + oldLineCount），窗口不重叠
	content := "line 1\nline 2\nAAA\nBBB\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nAAA\nBBB\nline 14\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:   filePath,
		OldString:  "AAA\nBBB",
		NewString:  "XXX",
		ReplaceAll: true,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	if len(result.Meta.DiffHunks) != 2 {
		t.Fatalf("DiffHunks count = %d, want 2 (non-overlapping)", len(result.Meta.DiffHunks))
	}

	h1 := result.Meta.DiffHunks[0]
	h2 := result.Meta.DiffHunks[1]

	// Hunk 1: 第一次替换，无累积偏移
	if h1.NewStart != h1.OldStart {
		t.Errorf("Hunk1 NewStart = %d, want %d (no prior shift)", h1.NewStart, h1.OldStart)
	}
	a1, r1 := h1.Stats()
	if a1 != 1 || r1 != 2 {
		t.Errorf("Hunk1 stats = (+%d -%d), want (+1 -2)", a1, r1)
	}

	// Hunk 2: 第二次替换，累积偏移 = r1 - a1 = 2 - 1 = 1
	// NewStart 应比 OldStart 小 1（前面删 2 增 1，净删 1 行，新文件行号前移 1）
	if h2.NewStart != h2.OldStart-1 {
		t.Errorf("Hunk2 NewStart = %d, want %d (cumulative shift -1)", h2.NewStart, h2.OldStart-1)
	}
	a2, r2 := h2.Stats()
	if a2 != 1 || r2 != 2 {
		t.Errorf("Hunk2 stats = (+%d -%d), want (+1 -2)", a2, r2)
	}

	// 验证 hunk2 中上下文行的 NewNum 正确偏移
	for _, l := range h2.Lines {
		if l.Kind == DiffCtx {
			expectedNew := l.OldNum - 1 // cumulative shift = -1
			if l.NewNum != expectedNew {
				t.Errorf("Hunk2 ctx line %q: NewNum = %d, want %d (OldNum=%d, shift=-1)",
					l.Content, l.NewNum, expectedNew, l.OldNum)
			}
		}
		if l.Kind == DiffAdd && l.Content == "XXX" {
			// 新增行 NewNum 也应计入累积偏移
			if l.NewNum == 0 {
				t.Error("Hunk2 add line should have non-zero NewNum")
			}
		}
	}

	totalAdded, totalRemoved := diffStats(result.Meta.DiffHunks)
	if totalAdded != 2 || totalRemoved != 4 {
		t.Errorf("total diff stats = (+%d -%d), want (+2 -4)", totalAdded, totalRemoved)
	}
}

func TestEditFileDiffHunksEmptyNew(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "delete.txt")
	content := "line 1\nremove me\nline 3\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "remove me\n",
		NewString: "",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	if len(result.Meta.DiffHunks) != 1 {
		t.Fatalf("DiffHunks count = %d, want 1", len(result.Meta.DiffHunks))
	}

	h := result.Meta.DiffHunks[0]
	added, removed := h.Stats()
	if added != 0 || removed != 1 {
		t.Errorf("stats = (+%d -%d), want (+0 -1)", added, removed)
	}

	// 确认没有 DiffAdd 行
	for _, l := range h.Lines {
		if l.Kind == DiffAdd {
			t.Error("pure deletion should not have add lines")
		}
	}
}

func TestEditFileDiffHunksEmptyOld(t *testing.T) {
	// 不生成 diff — 参数校验阶段即报错
	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  "/tmp/any.txt",
		OldString: "",
		NewString: "replacement",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for empty old_string")
	}
	if result.Meta.DiffHunks != nil {
		t.Error("DiffHunks should be nil when old_string is empty")
	}
}

func TestEditFileDiffHunksNoMatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "nomatch.txt")
	if err := os.WriteFile(filePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "nonexistent",
		NewString: "replacement",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for no match")
	}
	if result.Meta.DiffHunks != nil {
		t.Error("DiffHunks should be nil when no match found")
	}
}

// ── normalizeWhitespace ──

func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello world", "hello world"},
		{"hello   world", "hello world"},
		{"\thello\t\tworld", "hello world"},
		{"  hello world  ", "hello world"},
		{"line1\n\nline2", "line1 line2"},
		{"\tfunc hello() {\n\t\tfmt.Println(\"hi\")\n\t}", "func hello() { fmt.Println(\"hi\") }"},
	}
	for _, tt := range tests {
		got := normalizeWhitespace(tt.input)
		if got != tt.want {
			t.Errorf("normalizeWhitespace(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ── tryNormalizedMatch ──

func TestTryNormalizedMatch_WhitespaceMismatch(t *testing.T) {
	original := "func hello() {\n\tfmt.Println(\"hello world\")\n}"
	oldStr := "func hello() {\n    fmt.Println(\"hello world\")\n}" // 4 spaces instead of tab

	hint := tryNormalizedMatch(original, oldStr)
	if hint == "" {
		t.Fatal("expected hint for whitespace mismatch")
	}
	if !contains(hint, "Whitespace mismatch") {
		t.Errorf("hint should mention whitespace mismatch: %s", hint)
	}
	if !contains(hint, "fmt.Println") {
		t.Errorf("hint should show matching lines: %s", hint)
	}
}

func TestTryNormalizedMatch_NoMatchAtAll(t *testing.T) {
	original := "package main\nfunc main() {}"
	oldStr := "completely different content"

	hint := tryNormalizedMatch(original, oldStr)
	if hint != "" {
		t.Errorf("expected empty hint for no match, got: %s", hint)
	}
}

func TestTryNormalizedMatch_MultipleNormalizedMatches(t *testing.T) {
	original := "hello world\nhello world\n"
	oldStr := "hello world"

	hint := tryNormalizedMatch(original, oldStr)
	if hint != "" {
		t.Errorf("expected empty hint for ambiguous match, got: %s", hint)
	}
}

// ── pickBestQueryLine ──

func TestPickBestQueryLine(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"single line", "single line"},
		{"short\nlonger line here\n}", "longer line here"},
		{"}\nfunc main() {\n\tfmt.Println()\n}", "func main() {"},
	}
	for _, tt := range tests {
		got := pickBestQueryLine(tt.input)
		if got != tt.want {
			t.Errorf("pickBestQueryLine(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ── looksLikeLineNumberPrefix ──

func TestLooksLikeLineNumberPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"[1] package main\n[2] import \"fmt\"", true},
		{"[123] func hello() {}", true},
		{"package main\nimport \"fmt\"", false},
		{"func main() {\n\tfmt.Println()\n}", false},
		{"[not a number] text", false},
	}
	for _, tt := range tests {
		got := looksLikeLineNumberPrefix(tt.input)
		if got != tt.want {
			t.Errorf("looksLikeLineNumberPrefix(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ── edit_file with whitespace fallback ──

// TestEditFileAutoFixWhitespace 验证空白归一化匹配唯一时自动修复成功，
// 不再返回错误让 LLM 重试。
func TestEditFileAutoFixWhitespace(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "code.go")
	content := "func hello() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "func hello() {\n    fmt.Println(\"hello\")\n}", // 4 spaces instead of tab
		NewString: "func hello() {\n\tfmt.Println(\"hi\")\n}",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// 自动修复成功 — 不应返回 Error
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v, want nil (auto-fix should succeed)", result.Error)
	}

	// 验证文件内容已被正确替换
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !contains(string(data), `fmt.Println("hi")`) {
		t.Errorf("file should contain the replacement, got: %s", string(data))
	}
	if !contains(string(data), "\tfmt.Println") {
		t.Errorf("file should preserve tab indentation, got: %s", string(data))
	}

	// 验证 Content 中标注了自动修复
	if !contains(result.Content, "Auto-corrected whitespace") {
		t.Errorf("Content should mention auto-corrected whitespace: %s", result.Content)
	}
	if !contains(result.Content, "Matched lines") {
		t.Errorf("Content should mention matched lines: %s", result.Content)
	}

	// 验证 DiffHunks
	if len(result.Meta.DiffHunks) != 1 {
		t.Fatalf("DiffHunks count = %d, want 1", len(result.Meta.DiffHunks))
	}
	a, r := result.Meta.DiffHunks[0].Stats()
	if a != 3 || r != 3 { // 替换了三行（func 声明 + fmt.Println 行 + 闭括号）
		t.Errorf("stats = (+%d -%d), want (+3 -3)", a, r)
	}
}

// TestEditFileNoMatch_WhitespaceHint 验证归一化匹配不唯一时仍返回 hint 错误，
// 不会触发自动修复（因为无法确定替换哪个）。
func TestEditFileNoMatch_WhitespaceHint(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "code.go")
	// 两个相同的函数 — 归一化后 old_string 命中两处，不能自动修复
	content := "func hello() {\n\tfmt.Println(\"hello\")\n}\nfunc hello() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "func hello() {\n    fmt.Println(\"hello\")\n}", // 空格缩进
		NewString: "func hello() {\n\tfmt.Println(\"hi\")\n}",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// 不唯一 → 不触发自动修复 → 应返回 Error（走 renderSearchHint 路径）
	if result.Error == nil {
		t.Fatal("Error should not be nil for ambiguous whitespace mismatch")
	}
	if result.Error.Kind != ErrKindNoMatch {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindNoMatch)
	}
	// 归一化匹配不唯一时走 renderSearchHint，应包含 closest matches 提示
	if !contains(result.Error.Message, "closest matches") {
		t.Errorf("Error message should include closest matches hint: %s", result.Error.Message)
	}
	// 两处重复函数都在 hint 中
	if strings.Count(result.Error.Message, "fmt.Println") < 2 {
		t.Errorf("Error message should show both ambiguous matches: %s", result.Error.Message)
	}
}

func TestEditFileIsDirectory(t *testing.T) {
	dir := t.TempDir()

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  dir,
		OldString: "hello",
		NewString: "goodbye",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil when editing a directory")
	}
	if result.Error.Kind != ErrKindNotDir {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindNotDir)
	}
	if !strings.Contains(result.Error.Message, "Top entries:") {
		t.Error("directory error should list top entries")
	}
}
