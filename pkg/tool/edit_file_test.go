package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// EditFile — 成功路径:单文件单 hunk
// ---------------------------------------------------------------------------

func TestEditFile_Success(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// 读文件建立 readState
	rs := NewReadStateStore()
	rs.Record(filePath, content)
	ctx := WithReadState(context.Background(), rs)

	tool := &EditFile{}
	result, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		Hunk: `@@
 line1
-line2
+lineTWO
 line3
`,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	// 验证文件内容
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := "line1\nlineTWO\nline3\n"
	if string(data) != expected {
		t.Errorf("file content = %q, want %q", string(data), expected)
	}

	// 验证 Meta: FilePath 为绝对路径
	absPath, _ := filepath.Abs(filePath)
	if result.Meta.FilePath != absPath {
		t.Errorf("FilePath = %q, want %q", result.Meta.FilePath, absPath)
	}
	if result.Meta.LineCount != 3 {
		t.Errorf("LineCount = %d, want 3", result.Meta.LineCount)
	}
	if result.Meta.ByteCount == 0 {
		t.Error("ByteCount should be non-zero")
	}
	if len(result.Meta.DiffHunks) == 0 {
		t.Error("DiffHunks should be non-empty")
	}
	if !strings.Contains(result.Content, "1/1 hunks succeeded") {
		t.Errorf("expected success message, got: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// EditFile — 空结果:无 hunk 时返回 no changes
// ---------------------------------------------------------------------------

func TestEditFile_NoChanges(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	tool := &EditFile{}
	result, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		// 信封格式但无 @@ hunk — 返回 no changes
		Hunk: "*** Begin Patch\n*** End Patch",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	if !strings.Contains(result.Content, "no changes needed") {
		t.Errorf("expected 'no changes needed', got: %s", result.Content)
	}

	absPath, _ := filepath.Abs(filePath)
	if result.Meta.FilePath != absPath {
		t.Errorf("FilePath = %q, want %q", result.Meta.FilePath, absPath)
	}
}

// ---------------------------------------------------------------------------
// EditFile — 全部失败: hunk 不匹配
// ---------------------------------------------------------------------------

func TestEditFile_AllHunksFailed(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := NewReadStateStore()
	rs.Record(filePath, content)
	ctx := WithReadState(context.Background(), rs)

	tool := &EditFile{}
	result, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		Hunk: `@@
 nonexistent
-this line does not exist
+replacement
`,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// 应返回 result.Error 而非 error
	if result.Error == nil {
		t.Fatal("expected result.Error for failed hunk, got nil")
	}
	if result.Error.Class != ErrorClassRecoverable {
		t.Errorf("Error.Class = %v, want recoverable", result.Error.Class)
	}

	// FilePath 应为空 — 全部 hunk 失败时文件未修改，不触发 LSP 诊断
	if result.Meta.FilePath != "" {
		t.Errorf("FilePath = %q, want empty (all hunks failed, no LSP needed)", result.Meta.FilePath)
	}

	// 文件内容不应被修改
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("file should be unchanged, got: %q", string(data))
	}
}

// ---------------------------------------------------------------------------
// EditFile — hunk 为空字符串
// ---------------------------------------------------------------------------

func TestEditFile_EmptyHunk(t *testing.T) {
	dir := t.TempDir()
	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath: filepath.Join(dir, "test.go"),
		Hunk:     "",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for empty hunk")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("Error.Kind = %v, want invalid_args", result.Error.Kind)
	}
}

// ---------------------------------------------------------------------------
// EditFile — 信封格式:多文件
// ---------------------------------------------------------------------------

func TestEditFile_EnvelopeMultiFile(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "a.go")
	file2 := filepath.Join(dir, "b.go")
	content1 := "package a\n\nfunc A() {\n}\n"
	content2 := "package b\n\nfunc B() {\n}\n"

	if err := os.WriteFile(file1, []byte(content1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte(content2), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := NewReadStateStore()
	rs.Record(file1, content1)
	rs.Record(file2, content2)
	ctx := WithReadState(context.Background(), rs)

	tool := &EditFile{}
	patch := "*** Begin Patch\n" +
		"*** Update File: " + file1 + "\n" +
		"@@\n" +
		" func A() {\n" +
		"-}\n" +
		"+}\n" +
		"*** Update File: " + file2 + "\n" +
		"@@\n" +
		" func B() {\n" +
		"-}\n" +
		"+}\n" +
		"*** End Patch"
	result, err := tool.Execute(ctx, EditFileParams{
		FilePath: file1,
		Hunk:     patch,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	if !strings.Contains(result.Content, "2/2 hunks succeeded") {
		t.Errorf("expected 2/2 success, got: %s", result.Content)
	}

	// Meta 应指向第一个成功的文件
	absPath1, _ := filepath.Abs(file1)
	if result.Meta.FilePath != absPath1 {
		t.Errorf("FilePath = %q, want %q", result.Meta.FilePath, absPath1)
	}

	// DiffHunks 应有两个
	if len(result.Meta.DiffHunks) != 2 {
		t.Errorf("DiffHunks count = %d, want 2", len(result.Meta.DiffHunks))
	}
}

// ---------------------------------------------------------------------------
// EditFile — 无 ReadState:跳过验证直接编辑
// ---------------------------------------------------------------------------

func TestEditFile_WithoutReadState(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// 不注入 ReadStateStore
	ctx := context.Background()

	tool := &EditFile{}
	result, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		Hunk: `@@
 line1
-line2
+lineTWO
 line3
`,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := "line1\nlineTWO\nline3\n"
	if string(data) != expected {
		t.Errorf("file content = %q, want %q", string(data), expected)
	}
}

// ---------------------------------------------------------------------------
// EditFile — 连续 edit 无需 re-read
// ---------------------------------------------------------------------------

func TestEditFile_ChainedEdits(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := NewReadStateStore()
	rs.Record(filePath, content)
	ctx := WithReadState(context.Background(), rs)

	tool := &EditFile{}

	// 第一次 edit
	result1, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		Hunk: `@@
 line1
-line2
+lineTWO
 line3
`,
	})
	if err != nil || result1.Error != nil {
		t.Fatalf("first edit failed: err=%v resultErr=%v", err, result1.Error)
	}

	// 第二次 edit — 无需 re-read
	result2, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		Hunk: `@@
-line1
+lineONE
 lineTWO
`,
	})
	if err != nil || result2.Error != nil {
		t.Fatalf("second edit failed: err=%v resultErr=%v", err, result2.Error)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := "lineONE\nlineTWO\nline3\n"
	if string(data) != expected {
		t.Errorf("file content = %q, want %q", string(data), expected)
	}
}
