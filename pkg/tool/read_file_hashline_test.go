package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Menfre01/waveloom/pkg/hashline"
)


// ---------------------------------------------------------------------------
// ReadFileHashline — 正常路径
// ---------------------------------------------------------------------------

func TestReadFileHashline_Success(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: filePath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	if result.Content == "" {
		t.Fatal("expected non-empty content")
	}
	// 检查 TAG 存在
	if _, ok := store.Get(filePath); !ok {
		t.Error("store should contain snapshot after read")
	}
}

// ---------------------------------------------------------------------------
// ReadFileHashline — offset / limit
// ---------------------------------------------------------------------------

func TestReadFileHashline_OffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "data.txt")
	content := ""
	for i := 1; i <= 20; i++ {
		content += fmt.Sprintf("line %d\n", i)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tests := []struct {
		name          string
		offset, limit int
	}{
		{"read all", 0, 0},
		{"offset only", 5, 0},
		{"offset and limit", 10, 3},
		{"limit only", 0, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &ReadFileHashline{}
			result, err := tool.Execute(ctx, ReadFileHashlineParams{
				FilePath: filePath,
				Offset:   tt.offset,
				Limit:    tt.limit,
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if result.Error != nil {
				t.Fatalf("unexpected error: %s", result.Error.Message)
			}
			if result.Content == "" {
				t.Fatal("expected non-empty content")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ReadFileHashline — 文件不存在
// ---------------------------------------------------------------------------

func TestReadFileHashline_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	missingPath := filepath.Join(dir, "nonexistent.go")

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: missingPath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for missing file")
	}
	if result.Error.Kind != ErrKindFileNotFound {
		t.Errorf("expected ErrKindFileNotFound, got %q", result.Error.Kind)
	}
}

// ---------------------------------------------------------------------------
// ReadFileHashline — 父目录存在，文件不存在，相似文件提示
// ---------------------------------------------------------------------------

func TestReadFileHashline_FileNotFound_ParentExists(t *testing.T) {
	dir := t.TempDir()
	// 在目录下创建一个类似文件
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)

	missingPath := filepath.Join(dir, "main_test.go") // 不存在，但 main.go 存在

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: missingPath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for missing file")
	}
	if result.Error.Kind != ErrKindFileNotFound {
		t.Errorf("expected ErrKindFileNotFound, got %q", result.Error.Kind)
	}
}

// ---------------------------------------------------------------------------
// ReadFileHashline — 路径是目录
// ---------------------------------------------------------------------------

func TestReadFileHashline_IsDirectory(t *testing.T) {
	dir := t.TempDir()

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for directory")
	}
	if result.Error.Kind != ErrKindNotDir {
		t.Errorf("expected ErrKindNotDir, got %q", result.Error.Kind)
	}
}

// ---------------------------------------------------------------------------
// ReadFileHashline — 目录中有同名文件提示
// ---------------------------------------------------------------------------

func TestReadFileHashline_IsDirectory_SuggestsMatchingFile(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "skill")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 创建 skill.go —— 与目录名 skill 同名
	_ = os.WriteFile(filepath.Join(pkgDir, "skill.go"), []byte("package skill"), 0o644)

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: pkgDir})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for directory")
	}
	if result.Error.Kind != ErrKindNotDir {
		t.Errorf("expected ErrKindNotDir, got %q", result.Error.Kind)
	}
	// 错误信息应包含 Did you mean 提示
	if !containsStr(result.Error.Message, "Did you mean") {
		t.Error("expected 'Did you mean' suggestion in error message")
	}
}

// ---------------------------------------------------------------------------
// ReadFileHashline — 二进制文件（按扩展名）
// ---------------------------------------------------------------------------

func TestReadFileHashline_BinaryByExtension(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "image.png")
	// 写入非文本内容
	if err := os.WriteFile(filePath, []byte{0x89, 0x50, 0x4E, 0x47}, 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: filePath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for binary file")
	}
	if result.Error.Kind != ErrKindBinaryFile {
		t.Errorf("expected ErrKindBinaryFile, got %q", result.Error.Kind)
	}
}

// ---------------------------------------------------------------------------
// ReadFileHashline — 空文件
// ---------------------------------------------------------------------------

func TestReadFileHashline_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(filePath, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: filePath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	if !containsStr(result.Content, "empty") {
		t.Error("expected empty file warning in content")
	}
}

// ---------------------------------------------------------------------------
// ReadFileHashline — 设备文件拦截
// ---------------------------------------------------------------------------

func TestReadFileHashline_DeviceBlocked(t *testing.T) {
	devicePath := "/dev/zero"
	if runtime.GOOS == "windows" {
		devicePath = "NUL"
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: devicePath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for device file")
	}
	if result.Error.Kind != ErrKindSecurityViolation {
		t.Errorf("expected ErrKindSecurityViolation, got %q", result.Error.Kind)
	}
}

// ---------------------------------------------------------------------------
// ReadFileHashline — 无 Store（fallback TAG）
// ---------------------------------------------------------------------------

func TestReadFileHashline_NoStore_StillWorks(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// 不注入 Store
	ctx := context.Background()

	tool := &ReadFileHashline{}
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: filePath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	if result.Content == "" {
		t.Fatal("expected non-empty content")
	}
}

// ---------------------------------------------------------------------------
// ReadFileHashline — WorkingDir 解析
// ---------------------------------------------------------------------------

func TestReadFileHashline_WorkingDirResolution(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(subDir, "file.txt")
	content := "hello world\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	result, err := tool.Execute(ctx, ReadFileHashlineParams{
		FilePath:   "file.txt",
		WorkingDir: subDir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	if result.Content == "" {
		t.Fatal("expected non-empty content")
	}
}

// ---------------------------------------------------------------------------
// ReadFileHashline — context 已取消
// ---------------------------------------------------------------------------

func TestReadFileHashline_ContextAlreadyCancelled(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx, cancel := context.WithCancel(hashline.WithStore(context.Background(), store))
	cancel()

	tool := &ReadFileHashline{}
	_, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: filePath})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

// ---------------------------------------------------------------------------
// ReadFileHashline — invalid path
// ---------------------------------------------------------------------------

func TestReadFileHashline_InvalidPath(t *testing.T) {
	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: "\x00invalid"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for invalid path")
	}
	// null byte in path may produce different error kinds across platforms;
	// just verify error is present, not its exact kind
}

// ---------------------------------------------------------------------------
// ReadFileHashline — binary detected by content (not extension)
// ---------------------------------------------------------------------------

func TestReadFileHashline_BinaryByContent(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "data.bin")
	// 写入 null 字节，但扩展名不是已知二进制扩展名
	content := make([]byte, 2048)
	for i := range content {
		if i < 1024 {
			content[i] = 0
		} else {
			content[i] = 'A'
		}
	}
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: filePath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for binary file (null byte detected)")
	}
	if result.Error.Kind != ErrKindBinaryFile {
		t.Errorf("expected ErrKindBinaryFile, got %q", result.Error.Kind)
	}
}

