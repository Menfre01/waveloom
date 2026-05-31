package tool

import (
	"context"
	"os"
	"path/filepath"
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
	// 应包含搜索线索 — "fmt.Println" 是共同关键字
	if !contains(result.Error.Message, "Similar line") {
		t.Errorf("Error should include similar line hint: %s", result.Error.Message)
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

	if len(result.Meta.DiffHunks) != 2 {
		t.Fatalf("DiffHunks count = %d, want 2 (one per match)", len(result.Meta.DiffHunks))
	}

	totalAdded, totalRemoved := diffStats(result.Meta.DiffHunks)
	if totalAdded != 2 || totalRemoved != 2 {
		t.Errorf("total diff stats = (+%d -%d), want (+2 -2)", totalAdded, totalRemoved)
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
