package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileSuccess(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{FilePath: filePath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if result.Meta.FilePath != filePath {
		t.Errorf("FilePath = %q, want %q", result.Meta.FilePath, filePath)
	}
	if result.Meta.LineCount != 3 {
		t.Errorf("LineCount = %d, want 3", result.Meta.LineCount)
	}
	wantLines := []string{"[1] line1", "[2] line2", "[3] line3"}
	for _, want := range wantLines {
		if !contains(result.Content, want) {
			t.Errorf("Content missing %q", want)
		}
	}
}

func TestReadFileWithOffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Offset:   1, // 0-based，从第2行开始
		Limit:    2,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "[2] line2") {
		t.Error("Content missing [2] line2")
	}
	if !contains(result.Content, "[3] line3") {
		t.Error("Content missing [3] line3")
	}
	if contains(result.Content, "[1] line1") {
		t.Error("Content should not contain [1] line1")
	}
	if contains(result.Content, "[4] line4") {
		t.Error("Content should not contain [4] line4")
	}
}

func TestReadFileNotFound(t *testing.T) {
	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: "/nonexistent/path/file.txt",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for missing file")
	}
	if result.Error.Class != ErrorClassRecoverable {
		t.Errorf("Error.Class = %v, want ErrorClassRecoverable", result.Error.Class)
	}
	if result.Error.Kind != ErrKindFileNotFound {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindFileNotFound)
	}
	// 智能 ENOENT：应包含 CWD
	if !contains(result.Error.Message, "CWD:") {
		t.Error("Error message should contain CWD")
	}
}

func TestReadFileIsDir(t *testing.T) {
	dir := t.TempDir()
	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{FilePath: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for directory")
	}
	if result.Error.Kind != ErrKindNotDir {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindNotDir)
	}
}

func TestReadFileBinary(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "binary.bin")
	binaryContent := make([]byte, 512)
	for i := range binaryContent {
		if i%3 == 0 {
			binaryContent[i] = 0 // ~33% null bytes
		} else {
			binaryContent[i] = 'A'
		}
	}
	if err := os.WriteFile(filePath, binaryContent, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{FilePath: filePath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for binary file")
	}
	if result.Error.Kind != ErrKindBinaryFile {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindBinaryFile)
	}
}

func TestReadFileBinaryByExtension(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "image.png")
	// 内容是文本，但扩展名是 .png — 扩展名检查应优先
	if err := os.WriteFile(filePath, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{FilePath: filePath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for binary extension file")
	}
	if result.Error.Kind != ErrKindBinaryFile {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindBinaryFile)
	}
}

func TestReadFileTruncated(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "large.txt")
	// 生成超过 MaxReadBytes (100KB) 的文本
	line := "line content here for truncation test\n" // ~40 bytes
	var builder string
	for i := 0; i < 5000; i++ {
		builder += line
	}
	if err := os.WriteFile(filePath, []byte(builder), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{FilePath: filePath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "truncated") {
		t.Error("Content should contain truncation notice")
	}
}

func TestReadFileEmptyWarning(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(filePath, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{FilePath: filePath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "empty") {
		t.Error("Content should warn about empty file")
	}
}

func TestReadFileOffsetBeyondFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "short.txt")
	if err := os.WriteFile(filePath, []byte("only 3 lines\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Offset:   10, // 超出文件行数
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "shorter than the provided offset") {
		t.Error("Content should warn that offset exceeds file length")
	}
}

func TestReadFileViaRegistry(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "via_registry.txt")
	if err := os.WriteFile(filePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	r.Register(Wrap(&ReadFile{}))

	result, err := r.Execute(context.Background(), "read_file", json.RawMessage(
		`{"file_path":"`+filePath+`"}`,
	))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	if !contains(result.Content, "[1] hello") {
		t.Errorf("Content = %q, want to contain [1] hello", result.Content)
	}
}

func TestReadFileDeviceBlocked(t *testing.T) {
	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: "/dev/urandom",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for device file")
	}
	if result.Error.Kind != ErrKindSecurityViolation {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindSecurityViolation)
	}
}

// ---------------------------------------------------------------------------
// readFileWithContext — 分块读取 + context 取消
// ---------------------------------------------------------------------------

func TestReadFileWithContextSuccess(t *testing.T) {
	dir := t.TempDir()
	content := []byte("hello world\nline 2\nline 3\n")
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), content, 0644); err != nil {
		t.Fatal(err)
	}

	result, err := readFileWithContext(context.Background(), filepath.Join(dir, "test.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != string(content) {
		t.Errorf("content mismatch:\n  got:  %q\n  want: %q", string(result), string(content))
	}
}

func TestReadFileWithContextAlreadyCancelled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := readFileWithContext(ctx, filepath.Join(dir, "test.txt"))
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestReadFileCancelledDuringStreaming(t *testing.T) {
	// 验证大文件流式读取时 context 取消能被检测。
	// 创建一个 1MB+ 的文件以命中 readStreaming 路径（FastPathMaxSize = 10MB，
	// 但流式路径的选择由调用方 read_file.Execute 基于 info.Size() 决定，
	// 这里直接调用 readStreaming 测试其内部 ctx 检查）。
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")

	// 生成 ~1MB 内容（每行 ~100 字节，约 10000 行），触发 ctx 检查（每 64 行）。
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		_, _ = f.WriteString("line " + strings.Repeat("x", 90) + "\n")
	}
	f.Close()

	ctx, cancel := context.WithCancel(context.Background())

	// 启动后立即取消 — 第一次 ctx 检查在 64 行处触发，
	// 应在 ~64 行内退出。
	cancel()

	_, _, _, err = readStreaming(ctx, path, 0, 0)
	if err == nil {
		t.Fatal("expected error with cancelled context during streaming")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

// contains 辅助函数
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
