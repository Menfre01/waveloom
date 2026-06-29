package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLsSuccess(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "file2.go"), []byte("b"), 0o644)
	_ = os.Mkdir(filepath.Join(dir, "subdir"), 0o755)

	tool := &Ls{}
	result, err := tool.Execute(context.Background(), LsParams{
		Path: dir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "subdir/") {
		t.Error("Content should contain subdir/ (directory with trailing /)")
	}
	if !contains(result.Content, "file1.txt") {
		t.Error("Content should contain file1.txt")
	}
	if !contains(result.Content, "file2.go") {
		t.Error("Content should contain file2.go")
	}
}

func TestLsNotFound(t *testing.T) {
	tool := &Ls{}
	result, err := tool.Execute(context.Background(), LsParams{
		Path: "/nonexistent/directory",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for missing directory")
	}
	if result.Error.Kind != ErrKindFileNotFound {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindFileNotFound)
	}
}

func TestLsNotDir(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "file.txt")
	os.WriteFile(filePath, []byte("content"), 0o644)

	tool := &Ls{}
	result, err := tool.Execute(context.Background(), LsParams{
		Path: filePath,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for file path")
	}
	if result.Error.Kind != ErrKindNotDir {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindNotDir)
	}
}

func TestLsRecursiveDepth(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "a", "b", "c"), 0o755)
	os.WriteFile(filepath.Join(dir, "a", "a.go"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(dir, "a", "b", "b.go"), []byte("2"), 0o644)
	os.WriteFile(filepath.Join(dir, "a", "b", "c", "c.go"), []byte("3"), 0o644)

	// depth=1 只显示顶层
	tool := &Ls{}
	result, err := tool.Execute(context.Background(), LsParams{
		Path:  dir,
		Depth: 1,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "a/") {
		t.Error("Content should contain a/ at depth=1")
	}
	if contains(result.Content, "b.go") {
		t.Error("Content should not contain b.go at depth=1 (only one level)")
	}

	// depth=2 显示两层（dir 内容 + a/ 子目录内容）
	result, err = tool.Execute(context.Background(), LsParams{
		Path:  dir,
		Depth: 2,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "a.go") {
		t.Error("Content should contain a.go at depth=2")
	}
	if !contains(result.Content, "b/") {
		t.Error("Content should contain b/ at depth=2")
	}
	if contains(result.Content, "b.go") {
		t.Error("Content should not contain b.go at depth=2 (b/ is level 3)")
	}

	// depth=4 显示所有层级
	result, err = tool.Execute(context.Background(), LsParams{
		Path:  dir,
		Depth: 4,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "a.go") {
		t.Error("Content should contain a.go at depth=4")
	}
	if !contains(result.Content, "b.go") {
		t.Error("Content should contain b.go at depth=4")
	}
	if !contains(result.Content, "c.go") {
		t.Error("Content should contain c.go at depth=4")
	}
}

func TestLsEmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	tool := &Ls{}
	result, err := tool.Execute(context.Background(), LsParams{
		Path: dir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "empty directory") {
		t.Errorf("Content = %q, want to contain 'empty directory'", result.Content)
	}
}

func TestLsSkipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, ".git"), 0o755)
	os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("git config"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)

	tool := &Ls{}
	result, err := tool.Execute(context.Background(), LsParams{
		Path: dir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if contains(result.Content, ".git") {
		t.Error("Content should not contain .git (hidden dir)")
	}
	if !contains(result.Content, "main.go") {
		t.Error("Content should contain main.go")
	}
}

func TestLsTruncated(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxListEntries+10; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("file_%04d.txt", i)), []byte("x"), 0o644)
	}

	tool := &Ls{}
	result, err := tool.Execute(context.Background(), LsParams{
		Path: dir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "Truncated") {
		t.Errorf("Content should mention truncation: %s", result.Content)
	}
	if result.Meta.LineCount != MaxListEntries {
		t.Errorf("LineCount = %d, want %d (should be capped)", result.Meta.LineCount, MaxListEntries)
	}
}
