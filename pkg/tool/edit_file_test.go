package tool

import (
	"context"
	"fmt"
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
	if !strings.Contains(result.Error.Message, "Contents:") {
		t.Error("directory error should list contents")
	}
}

func TestEditFileIsDirectoryWithFiles(t *testing.T) {
	dir := t.TempDir()
	// 在目录中放置文件，覆盖 suggestFileInDir 的 Did you mean 分支
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Project\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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
	// 非空目录应列出内容
	if !strings.Contains(result.Error.Message, "Contents:") {
		t.Error("directory error should list contents")
	}
}

// ── buildMultipleMatchError — 多匹配上下文错误 ──

func TestEditFileMultipleMatchesContextualError(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "code.go")
	// 两个 cfg.Apply() 出现在不同函数中
	content := "func init() {\n\tcfg := loadConfig()\n\tcfg.Apply()\n\tregisterRoutes(cfg)\n}\n\nfunc reload() {\n\tcfg.Apply()\n\tlog.Println(\"reloaded\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "\tcfg.Apply()",
		NewString: "\tcfg.ApplyDefaults()",
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

	msg := result.Error.Message
	// 应包含每个匹配位置的上下文
	if !strings.Contains(msg, "Occurrence 1") {
		t.Error("error should list Occurrence 1")
	}
	if !strings.Contains(msg, "Occurrence 2") {
		t.Error("error should list Occurrence 2")
	}
	// 应包含行号
	if !strings.Contains(msg, "line ") {
		t.Error("error should include line numbers")
	}
	// 应包含匹配行的标记
	if !strings.Contains(msg, " → ") {
		t.Error("error should mark matching lines with →")
	}
	// 应包含周边上下文（init 和 reload 函数）
	if !strings.Contains(msg, "func init()") {
		t.Error("error should show context of first match: func init()")
	}
	if !strings.Contains(msg, "func reload()") {
		t.Error("error should show context of second match: func reload()")
	}
	// 应包含 actionable 提示
	if !strings.Contains(msg, "unique surrounding lines") {
		t.Error("error should suggest adding unique surrounding lines")
	}
}

// ── findMatchSkippingBlankLines — 空行容错 ──

func TestEditFileBlankLineTolerance_ExtraBlank(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "code.go")
	// 文件中实际有 1 个空行
	content := "func hello() {\n\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	// old_string 有 2 个空行（多余空行）
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "func hello() {\n\n\n\tfmt.Println(\"hello\")\n}",
		NewString: "func hello() {\n\n\tfmt.Println(\"hi\")\n}",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// 应自动修复成功
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v, want nil (blank-line tolerance should auto-fix)", result.Error)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), `fmt.Println("hi")`) {
		t.Errorf("file should contain replacement, got: %s", string(data))
	}
	// 应标注 blank lines 自动修复
	if !strings.Contains(result.Content, "blank lines") {
		t.Errorf("Content should mention blank lines fix: %s", result.Content)
	}
}

func TestEditFileBlankLineTolerance_MissingBlank(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "code.go")
	// 文件中有 2 个空行
	content := "func hello() {\n\n\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	// old_string 只有 1 个空行（缺少空行）
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "func hello() {\n\n\tfmt.Println(\"hello\")\n}",
		NewString: "func hello() {\n\n\n\tfmt.Println(\"hi\")\n}",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v, want nil (blank-line tolerance should auto-fix)", result.Error)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), `fmt.Println("hi")`) {
		t.Errorf("file should contain replacement, got: %s", string(data))
	}
}

func TestEditFileBlankLineCollapseAmbiguous(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "code.go")
	// 两个函数非空行完全相同（"func doWork() {" + "fmt.Println(\"hello\")" + "}"），
	// 仅空行数量不同：第一个无空行，第二个有 1 个空行
	content := "func doWork() {\n\tfmt.Println(\"hello\")\n}\n\nfunc doWork() {\n\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	// old_string 的空行模式与两处都不完全一致（2 个空行），
	// 但非空行在文件中出现 2 次 → 跳过空行后应检测到不唯一
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "func doWork() {\n\n\n\tfmt.Println(\"hello\")\n}",
		NewString: "func doWork() {\n\tfmt.Println(\"hi\")\n}",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// Tier 1 归一化匹配失败（空行数不匹配），
	// Tier 2 跳过空行匹配发现两处非空行结构完全一致 → 不唯一 → 回退到错误
	if result.Error == nil {
		t.Fatal("Error should not be nil when non-blank structure is ambiguous")
	}
	// 不唯一时走 renderSearchHint，错误 kind 为 no_match
	if result.Error.Kind != ErrKindNoMatch {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindNoMatch)
	}
}

// ── extractNonBlankNormalized ──

func TestExtractNonBlankNormalized(t *testing.T) {
	lines := []string{"func hello() {", "", "    fmt.Println(\"hi\")", "}", "", ""}
	nonBlank, lineMap := extractNonBlankNormalized(lines)

	if len(nonBlank) != 3 {
		t.Fatalf("nonBlank count = %d, want 3", len(nonBlank))
	}
	if lineMap[0] != 0 {
		t.Errorf("lineMap[0] = %d, want 0", lineMap[0])
	}
	if lineMap[1] != 2 {
		t.Errorf("lineMap[1] = %d, want 2 (blank line at idx 1 skipped)", lineMap[1])
	}
	if lineMap[2] != 3 {
		t.Errorf("lineMap[2] = %d, want 3", lineMap[2])
	}
	// 空白行被跳过但归一化后仍为空的行不应出现
	for _, nb := range nonBlank {
		if strings.TrimSpace(nb) == "" {
			t.Error("nonBlank should not contain empty strings")
		}
	}
}

// ── formatCharDiffHint ──

func TestFormatCharDiffHint_MidCharDiff(t *testing.T) {
	query := "m.slashRegistry = newSlashRegistry(sessionCreator, store, lister, modelName, skillLoader, registry"
	fileLine := "m.slashRegistry = newSlashRegistry(sessionCreator, store, lister, modelName, skillLoader, registry)"

	hint := formatCharDiffHint(query, fileLine)
	if hint == "" {
		t.Fatal("expected non-empty char diff hint")
	}
	if !strings.Contains(hint, "differs here") {
		t.Error("hint should mark the difference location")
	}
	if !strings.Contains(hint, "File:") {
		t.Error("hint should show file line")
	}
	if !strings.Contains(hint, "Yours:") {
		t.Error("hint should show your line")
	}
}

func TestFormatCharDiffHint_Identical(t *testing.T) {
	// 完全相同时返回空字符串（防御性处理）
	hint := formatCharDiffHint("hello", "hello")
	if hint != "" {
		t.Errorf("expected empty hint for identical strings, got: %s", hint)
	}
}

func TestFormatCharDiffHint_LengthDiff(t *testing.T) {
	// 长度不同（末尾多了字符）
	hint := formatCharDiffHint("fmt.Println(x)", "fmt.Println(xyz)")
	if !strings.Contains(hint, "differs here") {
		t.Error("hint should mark difference for length mismatch")
	}
}

func TestFormatCharDiffHint_LongPrefixTruncated(t *testing.T) {
	// 公共前缀超过 30 字符时应截断
	prefix := "this is a very long common prefix that exceeds thirty characters"
	query := prefix + "AAA"
	fileLine := prefix + "BBB"

	hint := formatCharDiffHint(query, fileLine)
	if !strings.Contains(hint, "...") {
		t.Error("long prefix should be truncated with ...")
	}
	if !strings.Contains(hint, "differs here") {
		t.Error("hint should still mark the difference")
	}
}

// ── Unicode 归一化降级 ──

// TestEditFileAutoFixUnicode_EmDash 验证 LLM 用 ASCII 破折号替代 em dash 时自动修复。
func TestEditFileAutoFixUnicode_EmDash(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "dash.go")
	// 源文件含 em dash \u2014
	content := "// local import \u2014 avoids top-level dep\nfunc main() {}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	// LLM 用 ASCII 破折号 '-'
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "// local import - avoids top-level dep",
		NewString: "// HELLO",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v, want nil (unicode dash should be auto-corrected)", result.Error)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "// HELLO") {
		t.Errorf("file should contain replacement, got: %s", string(data))
	}
	if strings.Contains(string(data), "\u2014") {
		t.Error("em dash should have been replaced")
	}
	if !strings.Contains(result.Content, "unicode") {
		t.Errorf("Content should mention unicode auto-fix: %s", result.Content)
	}
}

// TestEditFileAutoFixUnicode_SmartQuotes 验证 LLM 用 ASCII 直引号替代弯引号时自动修复。
func TestEditFileAutoFixUnicode_SmartQuotes(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "quotes.go")
	// 源文件含弯引号
	content := "fmt.Println(\u201Chello world\u201D)\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	// LLM 用 ASCII 直引号
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: `fmt.Println("hello world")`,
		NewString: `fmt.Println("hi")`,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v, want nil (smart quotes should be auto-corrected)", result.Error)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), `fmt.Println("hi")`) {
		t.Errorf("file should contain replacement, got: %s", string(data))
	}
}

// TestEditFileAutoFixUnicode_NBSP 验证不换行空格被归一化为普通空格。
func TestEditFileAutoFixUnicode_NBSP(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "nbsp.go")
	// 缩进含不换行空格 \u00A0
	content := "func hello() {\n\tfmt\u00A0:=\u00A0\"x\"\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "\tfmt := \"x\"",
		NewString: "\tfmt := \"y\"",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v, want nil (NBSP should be auto-corrected)", result.Error)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), `fmt := "y"`) {
		t.Errorf("file should contain replacement, got: %s", string(data))
	}
}

// TestEditFileUnicodeAmbiguous 验证 Unicode 归一化后仍不唯一时返回错误。
func TestEditFileUnicodeAmbiguous(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "ambiguous.go")
	// 两处完全相同的模式（Unicode 归一化后一样）
	content := "// import \u2014 local\n// import \u2014 local\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "// import - local",
		NewString: "// new comment",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// 归一化后两处匹配 → 不唯一 → 应返回错误
	if result.Error == nil {
		t.Fatal("Error should not be nil for ambiguous unicode match")
	}
	if result.Error.Kind != ErrKindNoMatch {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindNoMatch)
	}
}

// ── 行号前缀自动修复 ──

// TestEditFileAutoFixLineNumberPrefix 验证行号前缀被自动剥离后匹配成功。
func TestEditFileAutoFixLineNumberPrefix(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "code.go")
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	// old_string 误带了 read_file 的行号前缀
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath: filePath,
		OldString: "[1] package main\n[2] \n[3] func main() {",
		NewString: "package main\n\nfunc main() {\n\tfmt.Println(\"updated\")",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v, want nil (line number prefix should be auto-corrected)\n  Error: %s", result.Error, result.Error.Message)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), `fmt.Println("updated")`) {
		t.Errorf("file should contain replacement, got: %s", string(data))
	}
	if !strings.Contains(result.Content, "line number prefixes") {
		t.Errorf("Content should mention line number prefix auto-fix: %s", result.Content)
	}
}

// TestEditFileLineNumberPrefixCleanedNoMatch 验证剥离行号后仍不匹配时返回错误。
func TestEditFileLineNumberPrefixCleanedNoMatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "code.go")
	content := "package main\nfunc main() {}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	// old_string 带行号前缀，但剥离后的内容文件中也不存在
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "[1] package other\n[2] func test() {}",
		NewString: "replacement",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil when cleaned content also has no match")
	}
	if result.Error.Kind != ErrKindNoMatch {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindNoMatch)
	}
}

// ── stripLineNumberPrefixes ──

func TestStripLineNumberPrefixes(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"[1] package main", "package main"},
		{"[1] package main\n[2] import \"fmt\"", "package main\nimport \"fmt\""},
		{"[123] func hello() {}", "func hello() {}"},
		{"package main", "package main"},                                    // 无前缀
		{"[not a number] text", "[not a number] text"},                      // 非数字前缀
		{"  [1] indented\n  [2] line", "  indented\n  line"},               // 带前导空白
		{"[1] \tpackage main", "\tpackage main"},                              // 行号后跟制表符，tab 是实际内容
	}
	for _, tt := range tests {
		got := stripLineNumberPrefixes(tt.input)
		if got != tt.want {
			t.Errorf("stripLineNumberPrefixes(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ── normalizeLineWithUnicode ──

func TestNormalizeLineWithUnicode(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"import \u2014 local", "import - local"},                                  // em dash → -
		{"\u201Chello\u201D", "\"hello\""},                                         // smart quotes → "
		{"fmt\u00A0:=\u00A0\"x\"", "fmt := \"x\""},                                // NBSP → space + compress
		{"hello\u2003world", "hello world"},                                        // em space → space
		{"normal text", "normal text"},                                             // no change
		{"\tindented\tline", "indented line"},                                      // tabs → space + compress
	}
	for _, tt := range tests {
		got := normalizeLineWithUnicode(tt.input)
		if got != tt.want {
			t.Errorf("normalizeLineWithUnicode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ── applyAutoFix with replace_all ──

func TestEditFileAutoFixReplaceAll(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "code.go")
	content := "func hello() {\n    fmt.Println(\"hello\")\n}\nfunc world() {\n    fmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	// 用 tab 缩进（与文件的 4-space 不同），触发空白归一化 + replace_all
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:   filePath,
		OldString:  "\tfmt.Println(\"hello\")",
		NewString:  "\tfmt.Println(\"hi\")",
		ReplaceAll: true,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v, want nil\n  Error: %s", result.Error, result.Error.Message)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	// 两处都应被替换
	if strings.Count(string(data), `fmt.Println("hello")`) != 0 {
		t.Error("all hello should be replaced")
	}
	if strings.Count(string(data), `fmt.Println("hi")`) != 2 {
		t.Errorf("expected 2 occurrences of hi, got: %s", string(data))
	}
}

// ── levenshteinDistance 边界 ──

func TestLevenshteinDistance_EdgeCases(t *testing.T) {
	// 空序列
	if d := levenshteinDistance([]rune(""), []rune("abc")); d != 3 {
		t.Errorf("empty a → b: %d, want 3", d)
	}
	if d := levenshteinDistance([]rune("abc"), []rune("")); d != 3 {
		t.Errorf("a → empty b: %d, want 3", d)
	}
	// 超长跳过（>200 runes）
	long := []rune(strings.Repeat("x", 201))
	if d := levenshteinDistance(long, []rune("y")); d != -1 {
		t.Errorf("long a should return -1, got %d", d)
	}
	if d := levenshteinDistance([]rune("y"), long); d != -1 {
		t.Errorf("long b should return -1, got %d", d)
	}
	// a < b 交换分支
	if d := levenshteinDistance([]rune("ab"), []rune("abc")); d != 1 {
		t.Errorf("ab → abc: %d, want 1", d)
	}
}

// ── formatCharDiffHint 边界 ──

func TestFormatCharDiffHint_EdgeCases(t *testing.T) {
	// 完全相同
	hint := formatCharDiffHint("hello", "hello")
	if hint != "" {
		t.Errorf("identical should return empty, got: %s", hint)
	}
	// 末尾差异（长度不同）
	hint = formatCharDiffHint("hello world", "hello world!")
	if !strings.Contains(hint, "differs here") {
		t.Error("should mark difference")
	}
}

// ── lookLikeLineNumberPrefix 边界 ──

func TestLooksLikeLineNumberPrefix_EdgeCases(t *testing.T) {
	// 太短
	if looksLikeLineNumberPrefix("ab") {
		t.Error("too short should be false")
	}
	// 不以 [ 开头
	if looksLikeLineNumberPrefix("no bracket here") {
		t.Error("no bracket should be false")
	}
	// [ 但 ] 不在范围内
	if looksLikeLineNumberPrefix("[12345678] text") {
		t.Error("bracket index >= 8 should be false")
	}
	if looksLikeLineNumberPrefix("[] text") {
		t.Error("empty bracket should be false")
	}
}

// ── pickBestQueryLine 边界 ──

func TestPickBestQueryLine_AllBlank(t *testing.T) {
	// 所有行都是空白 → 返回第一行
	got := pickBestQueryLine("   \n\t\n  ")
	if got != "   " {
		t.Errorf("all blank should return first line, got %q", got)
	}
}

// ── renderSearchHint 边界 ──

func TestRenderSearchHint_Empty(t *testing.T) {
	if hint := renderSearchHint("", "content"); hint != "" {
		t.Errorf("empty target: %q", hint)
	}
	if hint := renderSearchHint("target", ""); hint != "" {
		t.Errorf("empty content: %q", hint)
	}
}

func TestRenderSearchHint_ShortQuery(t *testing.T) {
	// query < 4 runes
	hint := renderSearchHint("ab", "line1\nline2\n")
	if hint != "" {
		t.Errorf("short query should return empty: %q", hint)
	}
}

// ── buildMultipleMatchError truncation ──

func TestBuildMultipleMatchError_Truncation(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "many.txt")
	// 生成 7 个相同行 → 触发截断
	var lines []string
	for i := 0; i < 7; i++ {
		lines = append(lines, "same line")
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:  filePath,
		OldString: "same line",
		NewString: "new line",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for 7 matches")
	}
	if result.Error.Kind != ErrKindMultipleMatch {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindMultipleMatch)
	}
	// 应截断到 5 个 + 提示
	if !strings.Contains(result.Error.Message, "and 2 more") {
		t.Errorf("should mention truncated matches: %s", result.Error.Message)
	}
}

// ── dirToListing with >50 entries ──

func TestEditFileIsDirectoryLargeListing(t *testing.T) {
	dir := t.TempDir()
	// 创建 55 个文件
	for i := 0; i < 55; i++ {
		name := fmt.Sprintf("file_%02d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

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
		t.Fatal("Error should not be nil")
	}
	// 应显示 "Showing first 50 of 55"
	if !strings.Contains(result.Error.Message, "Showing first 50") {
		t.Errorf("should mention showing first 50: %s", result.Error.Message)
	}
	if !strings.Contains(result.Error.Message, "55") {
		t.Errorf("should mention total 55: %s", result.Error.Message)
	}
	if !strings.Contains(result.Error.Message, "and 5 more") {
		t.Errorf("should mention remaining: %s", result.Error.Message)
	}
}

// ── Unicode auto-fix with replace_all ──

func TestEditFileAutoFixUnicodeReplaceAll(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "dash.go")
	content := "// import \u2014 local\n// import \u2014 local\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath:   filePath,
		OldString:  "// import - local",
		NewString:  "// new",
		ReplaceAll: true,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v, want nil\n  Error: %s", result.Error, result.Error.Message)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Count(string(data), "// new") != 2 {
		t.Errorf("both lines should be replaced, got: %s", string(data))
	}
}

// ── tryNormalizedMatch hints ──

func TestTryNormalizedMatch_Exact(t *testing.T) {
	// 精确匹配存在时不应返回 hint（由调用方先 exact match 再 fallback）
	original := "hello world\n"
	hint := tryNormalizedMatch(original, "hello world")
	if hint == "" {
		t.Error("should return hint for whitespace-normalized match")
	}
}

func TestEditFile_Prompt(t *testing.T) {
	tool := &EditFile{}
	prompt := tool.Prompt()
	if prompt == "" {
		t.Error("Prompt should not be empty")
	}
	if !strings.Contains(prompt, "When NOT to use") {
		t.Error("Prompt should contain usage restrictions")
	}
	if !strings.Contains(prompt, "use write_file") {
		t.Error("Prompt should redirect to write_file for new files")
	}
	if !strings.Contains(prompt, "use read_file") {
		t.Error("Prompt should redirect to read_file")
	}
}
