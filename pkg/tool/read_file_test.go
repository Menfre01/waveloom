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
	dir := t.TempDir()

	tool := &ReadFile{}

	// 场景 B：父目录不存在 → 路径整体错误，不做文件猜测
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filepath.Join(dir, "nonexistent_dir", "file.txt"),
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
	if !contains(result.Error.Message, "Parent directory not found") {
		t.Errorf("Error should mention parent directory not found, got: %s", result.Error.Message)
	}
}

func TestReadFileNotFound_ParentExistsSimilarFile(t *testing.T) {
	dir := t.TempDir()
	// 父目录存在，文件不存在但有相似文件
	_ = os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "world.go"), []byte("package main"), 0o644)

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filepath.Join(dir, "helo.go"),
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
	// 应建议相似文件（距离 1）
	if !contains(result.Error.Message, "Did you mean") {
		t.Errorf("Error should contain 'Did you mean', got: %s", result.Error.Message)
	}
	if !contains(result.Error.Message, "hello.go") {
		t.Errorf("Error should suggest hello.go, got: %s", result.Error.Message)
	}
}

func TestReadFileNotFound_ParentExistsNoSimilar(t *testing.T) {
	dir := t.TempDir()
	// 父目录存在，文件不存在，也没有相似文件（距离太大）
	_ = os.WriteFile(filepath.Join(dir, "skill_test.go"), []byte("package skill_test"), 0o644)

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filepath.Join(dir, "help_test.go"),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for missing file")
	}
	// 不应建议 skill_test.go（距离太大）
	if contains(result.Error.Message, "Did you mean") {
		t.Errorf("Should not suggest unrelated file, got: %s", result.Error.Message)
	}
	// 应列出目录内容
	if !contains(result.Error.Message, "Files in") {
		t.Errorf("Should list files in parent directory, got: %s", result.Error.Message)
	}
}

func TestReadFileIsDir(t *testing.T) {
	dir := t.TempDir()
	// Create a file inside the dir so the listing is non-empty
	if err := os.WriteFile(filepath.Join(dir, "example.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create a subdirectory too
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

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
	// Error message should include the directory listing
	if !strings.Contains(result.Error.Message, "example.go") {
		t.Errorf("Error message should contain 'example.go', got: %s", result.Error.Message)
	}
	if !strings.Contains(result.Error.Message, "subdir/") {
		t.Errorf("Error message should contain 'subdir/', got: %s", result.Error.Message)
	}
}

func TestReadFileIsDir_SuggestsMatchingFile(t *testing.T) {
	dir := t.TempDir()
	// 创建与目录同名的 .py 文件（语言无关：pkg/skill → skill.py）
	dirName := filepath.Base(dir)
	expectedFile := dirName + ".py"
	if err := os.WriteFile(filepath.Join(dir, expectedFile), []byte("def main(): pass"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 干扰文件
	_ = os.WriteFile(filepath.Join(dir, "helper.py"), []byte("def helper(): pass"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# doc"), 0o644)

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{FilePath: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for directory")
	}
	if !strings.Contains(result.Error.Message, "Did you mean") {
		t.Errorf("Error should contain 'Did you mean' suggestion, got: %s", result.Error.Message)
	}
	if !strings.Contains(result.Error.Message, expectedFile) {
		t.Errorf("Suggestion should include %s, got: %s", expectedFile, result.Error.Message)
	}
}

func TestReadFileIsDir_SuggestsEntryFile(t *testing.T) {
	dir := t.TempDir()
	// 没有目录同名文件，但有 index.ts —— 应优先建议入口文件
	_ = os.WriteFile(filepath.Join(dir, "index.ts"), []byte("export {}"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "utils.ts"), []byte("export {}"), 0o644)

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{FilePath: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for directory")
	}
	if !strings.Contains(result.Error.Message, "Did you mean") {
		t.Error("Should suggest an entry file")
	}
	if !strings.Contains(result.Error.Message, "index.ts") {
		t.Errorf("Should suggest index.ts as entry file, got: %s", result.Error.Message)
	}
}

func TestReadFileIsDir_SmartSort(t *testing.T) {
	dir := t.TempDir()
	// 创建多种类型的文件和目录
	_ = os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# doc"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "main.rs"), []byte("fn main() {}"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "lib.py"), []byte("def foo(): pass"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "alpha_dir"), 0o755)

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{FilePath: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for directory")
	}

	msg := result.Error.Message
	// 定位到 Contents: 之后，排除 Did you mean 行的干扰
	contentsPos := strings.Index(msg, "Contents:")
	if contentsPos < 0 {
		t.Fatalf("expected 'Contents:' marker in message, got: %s", msg)
	}
	listing := msg[contentsPos:]

	// 所有文件应在所有目录之前
	readmeIdx := strings.Index(listing, "README.md")
	mainIdx := strings.Index(listing, "main.rs")
	libIdx := strings.Index(listing, "lib.py")
	alphaIdx := strings.Index(listing, "alpha_dir/")
	subdirIdx := strings.Index(listing, "subdir/")

	// 文件按字母序排列（大小写敏感，ASCII 序）：README.md < lib.py < main.rs
	if libIdx < 0 || mainIdx < 0 || readmeIdx < 0 {
		t.Fatalf("expected all files in listing, got: %s", msg)
	}
	if readmeIdx > libIdx {
		t.Error("README.md should appear before lib.py (ASCII alphabetical, R < l)")
	}
	if libIdx > mainIdx {
		t.Error("lib.py should appear before main.rs (alphabetical)")
	}

	// 目录应在文件之后，且目录间也是字母序
	if alphaIdx < 0 || subdirIdx < 0 {
		t.Fatalf("expected all dirs in listing, got: %s", msg)
	}
	if readmeIdx > alphaIdx {
		t.Error("last file should appear before first directory")
	}
	if alphaIdx > subdirIdx {
		t.Error("alpha_dir/ should appear before subdir/ (alphabetical)")
	}
}

func TestReadFileIsDir_NoSuggestion(t *testing.T) {
	dir := t.TempDir()
	// 只有子目录和隐藏文件 — 不应产生文件建议
	_ = os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".hidden"), []byte("secret"), 0o644)

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{FilePath: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for directory")
	}
	if strings.Contains(result.Error.Message, "Did you mean") {
		t.Error("Should not contain 'Did you mean' when no suggestible files exist")
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
	_ = f.Close()

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

func TestFileExtension(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"foo.go", ".go"},
		{"path/to/file.txt", ".txt"},
		{"noextension", ""},
		{"hidden.rc", ".rc"},
		{".hidden", ".hidden"},   // dotfile, last dot returns full name
		{".hidden.txt", ".txt"},  // dotfile with extension, last dot wins
		{"a/b", ""},
		{"tar.gz", ".gz"},   // only last extension
		{"path/to/file", ""},
	}
	for _, tt := range tests {
		got := fileExtension(tt.path)
		if got != tt.want {
			t.Errorf("fileExtension(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
