package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileSuccess(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "output.txt")

	tool := &WriteFile{}
	result, err := tool.Execute(context.Background(), WriteFileParams{
		FilePath: filePath,
		Content:  "hello world\nline2\n",
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
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "hello world\nline2\n" {
		t.Errorf("file content = %q, want %q", string(data), "hello world\nline2\n")
	}

	// 验证 Meta
	if result.Meta.LineCount != 2 {
		t.Errorf("LineCount = %d, want 2", result.Meta.LineCount)
	}
	if result.Meta.FilePath != filePath {
		t.Errorf("FilePath = %q, want %q", result.Meta.FilePath, filePath)
	}
}

func TestWriteFileCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sub", "dir", "output.txt")

	tool := &WriteFile{}
	result, err := tool.Execute(context.Background(), WriteFileParams{
		FilePath: filePath,
		Content:  "nested content",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	// 验证文件存在
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "nested content" {
		t.Errorf("file content = %q, want %q", string(data), "nested content")
	}
}

func TestWriteFileOverwrite(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "existing.txt")

	// 先写入初始内容
	if err := os.WriteFile(filePath, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &WriteFile{}
	result, err := tool.Execute(context.Background(), WriteFileParams{
		FilePath: filePath,
		Content:  "new content",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	// 验证内容被覆写
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "new content" {
		t.Errorf("file content = %q, want %q", string(data), "new content")
	}
}

func TestWriteFileIsDirectory(t *testing.T) {
	dir := t.TempDir()

	tool := &WriteFile{}
	result, err := tool.Execute(context.Background(), WriteFileParams{
		FilePath: dir,
		Content:  "should fail",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil when writing to a directory")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindInvalidArgs)
	}
}

func TestWriteFileContentTooLarge(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "big.txt")

	largeContent := make([]byte, MaxWriteBytes+1)
	for i := range largeContent {
		largeContent[i] = 'A'
	}

	tool := &WriteFile{}
	result, err := tool.Execute(context.Background(), WriteFileParams{
		FilePath: filePath,
		Content:  string(largeContent),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for oversized content")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindInvalidArgs)
	}
}

func TestWriteFileCreateShowsCreated(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "new.txt")

	tool := &WriteFile{}
	result, err := tool.Execute(context.Background(), WriteFileParams{
		FilePath: filePath,
		Content:  "line1\nline2\n",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("result.Error = %v", result.Error)
	}
	if !contains(result.Content, "Created new file") {
		t.Error("Output should indicate new file creation")
	}
	if !contains(result.Content, "Preview") {
		t.Error("Output should include content preview")
	}
}

func TestWriteFileUpdateShowsUpdated(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "update.txt")
	os.WriteFile(filePath, []byte("old line1\nold line2\n"), 0o644)

	tool := &WriteFile{}
	result, err := tool.Execute(context.Background(), WriteFileParams{
		FilePath: filePath,
		Content:  "old line1\nnew line2\nnew line3\n",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("result.Error = %v", result.Error)
	}
	if !contains(result.Content, "Updated file") {
		t.Error("Output should indicate file update")
	}
	if !contains(result.Content, "Preview") {
		t.Error("Output should include content preview")
	}
	// 应该有变化摘要
	if !contains(result.Content, "added") && !contains(result.Content, "Changed") {
		t.Errorf("Output should include change summary: %s", result.Content)
	}
}

func TestWriteFileNoChangeWarning(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "same.txt")
	os.WriteFile(filePath, []byte("same content\n"), 0o644)

	tool := &WriteFile{}
	result, err := tool.Execute(context.Background(), WriteFileParams{
		FilePath: filePath,
		Content:  "same content\n",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("result.Error = %v", result.Error)
	}
	if !contains(result.Content, "Updated file") {
		t.Error("Output should indicate update (even though content unchanged)")
	}
}
