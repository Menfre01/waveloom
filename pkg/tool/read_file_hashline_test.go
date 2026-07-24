package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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


// ---------------------------------------------------------------------------
// ReadFileHashline — 超大文件拒绝（>10MB）
// ---------------------------------------------------------------------------

func TestReadFileHashline_LargeFileRejected(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "large.log")
	// 创建 11MB 稀疏文件，Size()=11MB 但不占磁盘空间
	if err := os.WriteFile(filePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filePath, 11*1024*1024); err != nil {
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
		t.Fatal("expected error for large file")
	}
	if result.Error.Kind != ErrKindLargeFile {
		t.Errorf("expected ErrKindLargeFile, got %q", result.Error.Kind)
	}
}

// ---------------------------------------------------------------------------
// ReadFileHashline — pattern 匹配
// ---------------------------------------------------------------------------

func TestReadFileHashline_Pattern_SingleMatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n\nimport \"fmt\"\n\n// HandleRequest processes incoming requests\nfunc HandleRequest() {\n\tfmt.Println(\"handling\")\n}\n\nfunc main() {\n\tHandleRequest()\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: filePath, Pattern: "HandleRequest"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	// 应该显示匹配行周围的上下文,而不是全文件
	if !strings.Contains(result.Content, "Match 1 of") {
		t.Errorf("expected content to contain matched pattern, got:\n%s", result.Content)
	}
	// TAG 仍应在 store 中
	if _, ok := store.Get(filePath); !ok {
		t.Error("store should contain snapshot after read with pattern")
	}
}

func TestReadFileHashline_Pattern_MultipleMatches(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := ""
	for i := 1; i <= 30; i++ {
		if i%5 == 0 {
			content += fmt.Sprintf("// TODO: fix line %d\n", i)
		} else {
			content += fmt.Sprintf("line %d\n", i)
		}
	}
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	// 第一个匹配 (matchIdx=0)
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: filePath, Pattern: "TODO"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	if !strings.Contains(result.Content, "Match 1 of 6") {
		t.Errorf("expected Match 1 of 6, got footer:\n%s", result.Content)
	}

	// 第三个匹配 (matchIdx=2)
	result2, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: filePath, Pattern: "TODO", Offset: 2})
	if err != nil {
		t.Fatalf("Execute() offset=2 error = %v", err)
	}
	if result2.Error != nil {
		t.Fatalf("unexpected error: %s", result2.Error.Message)
	}
	if !strings.Contains(result2.Content, "Match 3 of 6") {
		t.Errorf("expected Match 3 of 6, got:\n%s", result2.Content)
	}
}

func TestReadFileHashline_Pattern_NoMatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: filePath, Pattern: "NotFound"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	// 应显示全文件 + 未匹配提示
	if !strings.Contains(result.Content, "not found in file") {
		t.Errorf("expected 'not found' reminder, got:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "package main") {
		t.Errorf("expected full file content, got:\n%s", result.Content)
	}
}

func TestReadFileHashline_Pattern_EmptyPattern(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	// 空 pattern 等同于不传 pattern — 显示全文件,无 match footer
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: filePath, Pattern: ""})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	if strings.Contains(result.Content, "Match") || strings.Contains(result.Content, "not found") {
		t.Errorf("empty pattern should behave like no pattern, got:\n%s", result.Content)
	}
}

func TestReadFileHashline_Pattern_ContextLines(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := ""
	for i := 1; i <= 30; i++ {
		content += fmt.Sprintf("line %d\n", i)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	// context_lines=2 → 匹配行 ±2 行
	result, err := tool.Execute(ctx, ReadFileHashlineParams{
		FilePath:     filePath,
		Pattern:      "line 15",
		ContextLines: 2,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	// 应显示 line 13-17 (5 行 = 2+1+2)
	lines := strings.Split(strings.TrimSpace(result.Content), "\n")
	matched := false
	for _, l := range lines {
		if strings.Contains(l, "line 15") {
			matched = true
			break
		}
	}
	if !matched {
		t.Errorf("expected match for 'line 15', got:\n%s", result.Content)
	}
}

func TestReadFileHashline_Pattern_WithLimit(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := ""
	for i := 1; i <= 30; i++ {
		content += fmt.Sprintf("line %d\n", i)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	// limit=3 → 只显示 3 行,即使 context 更大
	result, err := tool.Execute(ctx, ReadFileHashlineParams{
		FilePath:     filePath,
		Pattern:      "line 15",
		ContextLines: 10,
		Limit:        3,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	if result.Meta.LineCount != 3 {
		t.Errorf("expected LineCount=3 with limit=3, got %d", result.Meta.LineCount)
	}
}

func TestReadFileHashline_Pattern_OffsetOutOfRange(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	// offset=99 超出匹配数 → 回退到最后一个匹配
	result, err := tool.Execute(ctx, ReadFileHashlineParams{FilePath: filePath, Pattern: "main", Offset: 99})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	// 应显示最后一个匹配,不崩溃
	if !strings.Contains(result.Content, "Match") {
		t.Errorf("expected match footer even with out-of-range offset, got:\n%s", result.Content)
	}
}

func TestReadFileHashline_Pattern_OffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := ""
	for i := 1; i <= 30; i++ {
		if i%10 == 0 {
			content += fmt.Sprintf("// MARKER at line %d\n", i)
		} else {
			content += fmt.Sprintf("line %d\n", i)
		}
	}
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	ctx := hashline.WithStore(context.Background(), store)

	tool := &ReadFileHashline{}
	// offset=2(第三个匹配) + limit=3 → 应显示第三个 MARKER ±5 行中的前 3 行
	result, err := tool.Execute(ctx, ReadFileHashlineParams{
		FilePath:     filePath,
		Pattern:      "MARKER",
		Offset:       2,
		Limit:        3,
		ContextLines: 5,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	if !strings.Contains(result.Content, "Match 3 of 3") {
		t.Errorf("expected Match 3 of 3, got:\n%s", result.Content)
	}
	if result.Meta.LineCount != 3 {
		t.Errorf("expected LineCount=3 with limit=3, got %d", result.Meta.LineCount)
	}
}
