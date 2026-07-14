package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/hashline"
)

// setupEditTest creates a temp file, records its snapshot, and returns the
// context (with store), temp dir, file path, and TAG.
func setupEditTest(t *testing.T, name, content string) (context.Context, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	filePath := filepath.Join(dir, name)
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	store := hashline.NewStore()
	tag, err := store.Record(filePath, content)
	if err != nil {
		t.Fatal(err)
	}
	ctx := hashline.WithStore(context.Background(), store)
	return ctx, dir, filePath, tag
}

// readTestFile reads a file from disk for assertion convenience.
func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// makePatch is a helper to build a patch string.
func makePatch(sections ...string) string {
	return "*** Begin Patch\n" + strings.Join(sections, "\n") + "\n*** End Patch"
}

// makeSection builds a section header for a given file and tag.
func makeSection(filePath, tag string, ops ...string) string {
	header := fmt.Sprintf("[%s#%s]", filePath, tag)
	return header + "\n" + strings.Join(ops, "\n")
}

// ---------------------------------------------------------------------------
// SWAP 单行替换
// ---------------------------------------------------------------------------

func TestEditFileHashline_SWAP_SingleLine(t *testing.T) {
	ctx, dir, filePath, tag := setupEditTest(t, "main.go",
		"package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")

	patch := makePatch(makeSection(filePath, tag, "SWAP 4.=4:", "+\tfmt.Println(\"hello, world\")"))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	content := readTestFile(t, filePath)
	if !strings.Contains(content, `"hello, world"`) {
		t.Errorf("expected replaced content, got: %s", content)
	}
}

// ---------------------------------------------------------------------------
// SWAP 多行替换
// ---------------------------------------------------------------------------

func TestEditFileHashline_SWAP_MultiLine(t *testing.T) {
	ctx, dir, filePath, tag := setupEditTest(t, "main.go",
		"package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n\tfmt.Println(\"world\")\n}\n")

	patch := makePatch(makeSection(filePath, tag, "SWAP 4.=5:", "+\tfmt.Println(\"replaced\")"))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	content := readTestFile(t, filePath)
	if !strings.Contains(content, `"replaced"`) {
		t.Errorf("expected replaced content, got: %s", content)
	}
	if strings.Contains(content, `"world"`) {
		t.Error("old content should have been removed")
	}
}

// ---------------------------------------------------------------------------
// INS.PRE
// ---------------------------------------------------------------------------

func TestEditFileHashline_INS_PRE(t *testing.T) {
	ctx, dir, filePath, tag := setupEditTest(t, "main.go",
		"package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")

	patch := makePatch(makeSection(filePath, tag, "INS.PRE 4:", "+\t// greeting"))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	content := readTestFile(t, filePath)
	if !strings.Contains(content, "// greeting") {
		t.Errorf("expected inserted line, got: %s", content)
	}
}

// ---------------------------------------------------------------------------
// INS.POST
// ---------------------------------------------------------------------------

func TestEditFileHashline_INS_POST(t *testing.T) {
	ctx, dir, filePath, tag := setupEditTest(t, "main.go",
		"package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")

	patch := makePatch(makeSection(filePath, tag, "INS.POST 4:", "+\t// after greeting"))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	content := readTestFile(t, filePath)
	if !strings.Contains(content, "// after greeting") {
		t.Errorf("expected inserted line, got: %s", content)
	}
}

// ---------------------------------------------------------------------------
// INS.HEAD
// ---------------------------------------------------------------------------

func TestEditFileHashline_INS_HEAD(t *testing.T) {
	ctx, dir, filePath, tag := setupEditTest(t, "main.go",
		"package main\n\nfunc main() {\n}\n")

	patch := makePatch(makeSection(filePath, tag, "INS.HEAD:", "+// Copyright 2024"))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	content := readTestFile(t, filePath)
	if !strings.HasPrefix(content, "// Copyright 2024") {
		t.Errorf("expected header line at start, got: %s", content)
	}
}

// ---------------------------------------------------------------------------
// INS.TAIL
// ---------------------------------------------------------------------------

func TestEditFileHashline_INS_TAIL(t *testing.T) {
	ctx, dir, filePath, tag := setupEditTest(t, "main.go",
		"package main\n\nfunc main() {\n}\n")

	patch := makePatch(makeSection(filePath, tag, "INS.TAIL:", "+// END"))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	content := readTestFile(t, filePath)
	if !strings.Contains(content, "// END") {
		t.Errorf("expected trailer line at end, got: %s", content)
	}
}

// ---------------------------------------------------------------------------
// DEL 单行
// ---------------------------------------------------------------------------

func TestEditFileHashline_DEL_SingleLine(t *testing.T) {
	ctx, dir, filePath, tag := setupEditTest(t, "main.go",
		"package main\n\n// TODO: implement\nfunc main() {\n}\n")

	patch := makePatch(makeSection(filePath, tag, "DEL 3"))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	content := readTestFile(t, filePath)
	if strings.Contains(content, "TODO") {
		t.Errorf("line should have been deleted, got: %s", content)
	}
}

// ---------------------------------------------------------------------------
// DEL 多行
// ---------------------------------------------------------------------------

func TestEditFileHashline_DEL_MultiLine(t *testing.T) {
	ctx, dir, filePath, tag := setupEditTest(t, "main.go",
		"package main\n\n// TODO: implement\n// FIXME: broken\nfunc main() {\n}\n")

	patch := makePatch(makeSection(filePath, tag, "DEL 3.=4"))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	content := readTestFile(t, filePath)
	if strings.Contains(content, "TODO") || strings.Contains(content, "FIXME") {
		t.Errorf("lines should have been deleted, got: %s", content)
	}
}

// ---------------------------------------------------------------------------
// REM
// ---------------------------------------------------------------------------

func TestEditFileHashline_REM(t *testing.T) {
	ctx, dir, filePath, tag := setupEditTest(t, "unused.go", "package main\n")

	patch := makePatch(makeSection(filePath, tag, "REM"))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	if _, statErr := os.Stat(filePath); !os.IsNotExist(statErr) {
		t.Error("file should have been removed")
	}
}

// ---------------------------------------------------------------------------
// MV
// ---------------------------------------------------------------------------

func TestEditFileHashline_MV(t *testing.T) {
	ctx, dir, filePath, tag := setupEditTest(t, "old.go", "package main\n")

	newPath := filepath.Join(dir, "new.go")
	patch := makePatch(makeSection(filePath, tag, fmt.Sprintf("MV %s", newPath)))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	if _, statErr := os.Stat(filePath); !os.IsNotExist(statErr) {
		t.Error("old file should have been removed")
	}
	if _, statErr := os.Stat(newPath); statErr != nil {
		t.Errorf("new file should exist: %v", statErr)
	}
}

// ---------------------------------------------------------------------------
// 多文件 patch
// ---------------------------------------------------------------------------

func TestEditFileHashline_MultiFile(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	fileA := filepath.Join(dir, "a.go")
	contentA := "package a\n\nvar A = 1\n"
	if err := os.WriteFile(fileA, []byte(contentA), 0o644); err != nil {
		t.Fatal(err)
	}
	tagA, _ := store.Record(fileA, contentA)

	fileB := filepath.Join(dir, "b.go")
	contentB := "package b\n\nvar B = 2\n"
	if err := os.WriteFile(fileB, []byte(contentB), 0o644); err != nil {
		t.Fatal(err)
	}
	tagB, _ := store.Record(fileB, contentB)

	ctx := hashline.WithStore(context.Background(), store)

	patch := makePatch(
		makeSection(fileA, tagA, "SWAP 3.=3:", "+var A = 10"),
		makeSection(fileB, tagB, "SWAP 3.=3:", "+var B = 20"),
	)

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}

	if !strings.Contains(readTestFile(t, fileA), "var A = 10") {
		t.Error("file A should have been updated")
	}
	if !strings.Contains(readTestFile(t, fileB), "var B = 20") {
		t.Error("file B should have been updated")
	}
}

// ---------------------------------------------------------------------------
// 无 Store 报错
// ---------------------------------------------------------------------------

func TestEditFileHashline_NoStore(t *testing.T) {
	ctx := context.Background()
	tool := &EditFileHashline{}

	result, err := tool.Execute(ctx, EditFileHashlineParams{
		Patch: `*** Begin Patch
[main.go#0000]
SWAP 1.=1:
+// hi
*** End Patch`,
		WorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error: no hashline store")
	}
}

// ---------------------------------------------------------------------------
// 无效 patch 语法
// ---------------------------------------------------------------------------

func TestEditFileHashline_InvalidPatchSyntax(t *testing.T) {
	ctx, dir, _, _ := setupEditTest(t, "main.go", "package main\n")

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{
		Patch:      `this is not a valid patch`,
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected parse error")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("expected ErrKindInvalidArgs, got %q", result.Error.Kind)
	}
}

// ---------------------------------------------------------------------------
// 同文件多 Section 报错
// ---------------------------------------------------------------------------

func TestEditFileHashline_DuplicateSection(t *testing.T) {
	ctx, dir, filePath, tag := setupEditTest(t, "main.go", "package main\n\nvar x = 1\nvar y = 2\n")

	patch := makePatch(
		makeSection(filePath, tag, "SWAP 3.=3:", "+var x = 10"),
		makeSection(filePath, tag, "SWAP 4.=4:", "+var y = 20"),
	)

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for duplicate section")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("expected ErrKindInvalidArgs, got %q", result.Error.Kind)
	}
}

// ---------------------------------------------------------------------------
// TAG 不匹配且 recovery 也失败
// ---------------------------------------------------------------------------

func TestEditFileHashline_TagMismatch(t *testing.T) {
	ctx, dir, filePath, _ := setupEditTest(t, "main.go", "package main\n\nvar x = 1\n")

	// 修改文件内容使 LCS recovery 也无法恢复
	if err := os.WriteFile(filePath, []byte("package main\n\nvar x = 999\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patch := makePatch(makeSection(filePath, "GGGG", "SWAP 3.=3:", "+var x = 10"))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected TAG mismatch error")
	}
}

// ---------------------------------------------------------------------------
// 文件不存在
// ---------------------------------------------------------------------------

func TestEditFileHashline_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	missingPath := filepath.Join(dir, "missing.go")
	tag, _ := store.Record(missingPath, "old content")

	patch := makePatch(makeSection(missingPath, tag, "SWAP 1.=1:", "+new content"))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for missing file")
	}
}

// ---------------------------------------------------------------------------
// WorkingDir 解析
// ---------------------------------------------------------------------------

func TestEditFileHashline_WorkingDirResolution(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	filePath := filepath.Join(subDir, "file.txt")
	content := "hello\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	tag, _ := store.Record(filePath, content)
	ctx := hashline.WithStore(context.Background(), store)

	patch := makePatch(makeSection(filePath, tag, "SWAP 1.=1:", "+world"))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: subDir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	if readTestFile(t, filePath) != "world\n" {
		t.Errorf("expected 'world', got %q", readTestFile(t, filePath))
	}
}

// ---------------------------------------------------------------------------
// 多操作复合
// ---------------------------------------------------------------------------

func TestEditFileHashline_MultipleOps(t *testing.T) {
	ctx, dir, filePath, tag := setupEditTest(t, "main.go",
		"package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")

	patch := makePatch(makeSection(filePath, tag,
		"INS.PRE 4:", "+\t// before",
		"INS.POST 4:", "+\t// after",
		"SWAP 2.=2:", "+", "+import \"fmt\"",
	))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	content := readTestFile(t, filePath)
	if !strings.Contains(content, "// before") {
		t.Error("missing // before")
	}
	if !strings.Contains(content, "// after") {
		t.Error("missing // after")
	}
	if !strings.Contains(content, `"fmt"`) {
		t.Error("missing import")
	}
}

// ---------------------------------------------------------------------------
// SWAP 在不存在的文件上（store 有记录但磁盘无文件）
// ---------------------------------------------------------------------------

func TestEditFileHashline_SwapOnMissingFile(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()
	missingPath := filepath.Join(dir, "new.go")
	tag, _ := store.Record(missingPath, "placeholder")

	ctx := hashline.WithStore(context.Background(), store)

	patch := makePatch(makeSection(missingPath, tag, "SWAP 1.=1:", "+package new"))

	tool := &EditFileHashline{}
	result, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error: file does not exist on disk")
	}
}
