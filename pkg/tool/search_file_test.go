package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchFileSuccess(t *testing.T) {
	dir := t.TempDir()
	// 创建测试文件结构
	_ = os.MkdirAll(filepath.Join(dir, "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "pkg", "util.go"), []byte("package pkg"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# readme"), 0o644)

	tool := &SearchFile{}
	result, err := tool.Execute(context.Background(), SearchFileParams{
		Pattern:    "*.go",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "main.go") {
		t.Error("Content should contain main.go")
	}
	if !contains(result.Content, "util.go") {
		t.Error("Content should contain util.go")
	}
	if result.Meta.LineCount != 2 {
		t.Errorf("LineCount = %d, want 2", result.Meta.LineCount)
	}
}

func TestSearchFileNoResults(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# readme"), 0o644)

	tool := &SearchFile{}
	result, err := tool.Execute(context.Background(), SearchFileParams{
		Pattern:    "*.go",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// 空结果不是 error — 返回简洁的 "No files found"
	if result.Error != nil {
		t.Fatalf("Empty results should NOT be an error, got: %v", result.Error)
	}
	if !contains(result.Content, "No files matching") {
		t.Errorf("Content should indicate no results: %s", result.Content)
	}
	if result.Meta.LineCount != 0 {
		t.Errorf("LineCount = %d, want 0", result.Meta.LineCount)
	}
}

func TestSearchFileRecursive(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "sub", "deep"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "test.go"), []byte("package main"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "sub", "sub.go"), []byte("package sub"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "sub", "deep", "deep.go"), []byte("package deep"), 0o644)

	tool := &SearchFile{}
	result, err := tool.Execute(context.Background(), SearchFileParams{
		Pattern:    "*.go",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if result.Meta.LineCount != 3 {
		t.Errorf("LineCount = %d, want 3", result.Meta.LineCount)
	}
}

func TestSearchFileSkipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".git", "objects", "file.go"), []byte("package git"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "mod.go"), []byte("package mod"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)

	tool := &SearchFile{}
	result, err := tool.Execute(context.Background(), SearchFileParams{
		Pattern:    "*.go",
		WorkingDir: dir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	// 应该只找到 main.go，跳过 .git 和 node_modules
	if result.Meta.LineCount != 1 {
		t.Errorf("LineCount = %d, want 1 (hidden dirs should be skipped)", result.Meta.LineCount)
	}
}

func TestMatchDoubleStar_MidComponent(t *testing.T) {
	// **/waveloom* 应当匹配路径中间含有 waveloom 分量的文件
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// 末级分量（文件名）匹配 — 已有行为
		{"**/waveloom*", "waveloom", true},
		{"**/waveloom*", "cmd/waveloom", true},
		// 中间分量匹配 — 新增行为
		{"**/waveloom*", "cmd/waveloom/main.go", true},
		{"**/waveloom*", "cmd/waveloom/sub/deep/file.go", true},
		{"**/spec*", "specs/tui/main.go", true},
		// 基准
		{"**/*.go", "cmd/waveloom/main.go", true},
		{"**/cmd*", "cmd/waveloom/main.go", true},
		{"**/main*", "cmd/waveloom/main.go", true},
		// 不匹配
		{"**/waveloom*", "cmd/wave/main.go", false},
		{"**/waveloom*", "pkg/other/main.go", false},
	}

	for _, tt := range tests {
		got := matchDoubleStar(tt.pattern, tt.path)
		if got != tt.want {
			t.Errorf("matchDoubleStar(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestSearchFileDirNotFound(t *testing.T) {
	tool := &SearchFile{}
	result, err := tool.Execute(context.Background(), SearchFileParams{
		Pattern:    "*.go",
		WorkingDir: "/nonexistent/dir",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for missing dir")
	}
	if result.Error.Kind != ErrKindFileNotFound {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindFileNotFound)
	}
}
