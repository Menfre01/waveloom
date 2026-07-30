package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

)

// ---------------------------------------------------------------------------
// ReadFile — 正常路径
// ---------------------------------------------------------------------------

func TestReadFile_Success(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: filePath})
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
// ReadFile — offset / limit
// ---------------------------------------------------------------------------

func TestReadFile_OffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "data.txt")
	content := ""
	for i := 1; i <= 20; i++ {
		content += fmt.Sprintf("line %d\n", i)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

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
			tool := &ReadFile{}
			result, err := tool.Execute(ctx, ReadFileParams{
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
// ReadFile — 文件不存在
// ---------------------------------------------------------------------------

func TestReadFile_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	missingPath := filepath.Join(dir, "nonexistent.go")

	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: missingPath})
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
// ReadFile — 父目录存在，文件不存在，相似文件提示
// ---------------------------------------------------------------------------

func TestReadFile_FileNotFound_ParentExists(t *testing.T) {
	dir := t.TempDir()
	// 在目录下创建一个类似文件
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)

	missingPath := filepath.Join(dir, "main_test.go") // 不存在，但 main.go 存在

	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: missingPath})
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
// ReadFile — 路径是目录
// ---------------------------------------------------------------------------

func TestReadFile_IsDirectory(t *testing.T) {
	dir := t.TempDir()

	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: dir})
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
// ReadFile — 目录中有同名文件提示
// ---------------------------------------------------------------------------

func TestReadFile_IsDirectory_SuggestsMatchingFile(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "skill")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 创建 skill.go —— 与目录名 skill 同名
	_ = os.WriteFile(filepath.Join(pkgDir, "skill.go"), []byte("package skill"), 0o644)

	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: pkgDir})
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
// ReadFile — 二进制文件（按扩展名）
// ---------------------------------------------------------------------------

func TestReadFile_BinaryByExtension(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "image.png")
	// 写入非文本内容
	if err := os.WriteFile(filePath, []byte{0x89, 0x50, 0x4E, 0x47}, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: filePath})
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
// ReadFile — 空文件
// ---------------------------------------------------------------------------

func TestReadFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(filePath, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: filePath})
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
// ReadFile — 设备文件拦截
// ---------------------------------------------------------------------------

func TestReadFile_DeviceBlocked(t *testing.T) {
	devicePath := "/dev/zero"
	if runtime.GOOS == "windows" {
		devicePath = "NUL"
	}

	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: devicePath})
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
// ReadFile — 无 Store（fallback TAG）
// ---------------------------------------------------------------------------

func TestReadFile_NoStore_StillWorks(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// 不注入 Store
	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: filePath})
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
// ReadFile — WorkingDir 解析
// ---------------------------------------------------------------------------

func TestReadFile_WorkingDirResolution(t *testing.T) {
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

	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{
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
// ReadFile — context 已取消
// ---------------------------------------------------------------------------

func TestReadFile_ContextAlreadyCancelled(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tool := &ReadFile{}
	_, err := tool.Execute(ctx, ReadFileParams{FilePath: filePath})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

// ---------------------------------------------------------------------------
// ReadFile — invalid path
// ---------------------------------------------------------------------------

func TestReadFile_InvalidPath(t *testing.T) {
	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: "\x00invalid"})
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
// ReadFile — binary detected by content (not extension)
// ---------------------------------------------------------------------------

func TestReadFile_BinaryByContent(t *testing.T) {
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

	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: filePath})
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
// ReadFile — 超大文件拒绝（>10MB）
// ---------------------------------------------------------------------------

func TestReadFile_LargeFileRejected(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "large.log")
	// 创建 11MB 稀疏文件，Size()=11MB 但不占磁盘空间
	if err := os.WriteFile(filePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filePath, 11*1024*1024); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: filePath})
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
// ReadFile — pattern 匹配
// ---------------------------------------------------------------------------

func TestReadFile_Pattern_SingleMatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n\nimport \"fmt\"\n\n// HandleRequest processes incoming requests\nfunc HandleRequest() {\n\tfmt.Println(\"handling\")\n}\n\nfunc main() {\n\tHandleRequest()\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: filePath, Pattern: "HandleRequest"})
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
}

func TestReadFile_Pattern_MultipleMatches(t *testing.T) {
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

	ctx := context.Background()

	tool := &ReadFile{}
	// 第一个匹配 (matchIdx=0)
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: filePath, Pattern: "TODO"})
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
	result2, err := tool.Execute(ctx, ReadFileParams{FilePath: filePath, Pattern: "TODO", Offset: 2})
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

func TestReadFile_Pattern_NoMatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	tool := &ReadFile{}
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: filePath, Pattern: "NotFound"})
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

func TestReadFile_Pattern_EmptyPattern(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	tool := &ReadFile{}
	// 空 pattern 等同于不传 pattern — 显示全文件,无 match footer
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: filePath, Pattern: ""})
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

func TestReadFile_Pattern_ContextLines(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := ""
	for i := 1; i <= 30; i++ {
		content += fmt.Sprintf("line %d\n", i)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	tool := &ReadFile{}
	// context_lines=2 → 匹配行 ±2 行
	result, err := tool.Execute(ctx, ReadFileParams{
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

func TestReadFile_Pattern_WithLimit(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := ""
	for i := 1; i <= 30; i++ {
		content += fmt.Sprintf("line %d\n", i)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	tool := &ReadFile{}
	// limit=3 → 只显示 3 行,即使 context 更大
	result, err := tool.Execute(ctx, ReadFileParams{
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

func TestReadFile_Pattern_OffsetOutOfRange(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	tool := &ReadFile{}
	// offset=99 超出匹配数 → 回退到最后一个匹配
	result, err := tool.Execute(ctx, ReadFileParams{FilePath: filePath, Pattern: "main", Offset: 99})
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

func TestReadFile_Pattern_OffsetAndLimit(t *testing.T) {
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

	ctx := context.Background()

	tool := &ReadFile{}
	// offset=2(第三个匹配) + limit=3 → 应显示第三个 MARKER ±5 行中的前 3 行
	result, err := tool.Execute(ctx, ReadFileParams{
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

// ── Outline mode ──

func TestReadFile_Outline_RegexFallback(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n\ntype MyStruct struct {\n\tName string\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	// No LSP manager in context → should fall back to regex
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() outline result error = %s", result.Error.Message)
	}
	if !strings.Contains(result.Content, "Symbols in") {
		t.Errorf("expected 'N Symbols in' header, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "main") {
		t.Errorf("expected 'main' function, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "function") {
		t.Errorf("expected 'function' kind, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "MyStruct") {
		t.Errorf("expected 'MyStruct' type, got: %s", result.Content)
	}
}

func TestReadFile_Outline_UnknownExtension(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "README.txt")
	content := "This is a plain text file.\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() outline result error = %s", result.Error.Message)
	}
	// .txt files fall through to generic patterns — should report no symbols
	if !strings.Contains(result.Content, "No symbols found") {
		t.Errorf("expected 'No symbols found', got: %s", result.Content)
	}
}

func TestReadFile_Outline_PythonFallback(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.py")
	content := "def hello():\n    pass\n\nclass Greeter:\n    def greet(self):\n        pass\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() outline result error = %s", result.Error.Message)
	}
	if !strings.Contains(result.Content, "hello") {
		t.Errorf("expected 'hello' function, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Greeter") {
		t.Errorf("expected 'Greeter' class, got: %s", result.Content)
	}
}

func TestReadFile_Outline_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "empty.go")
	if err := os.WriteFile(filePath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() outline result error = %s", result.Error.Message)
	}
	if !strings.Contains(result.Content, "No symbols found") {
		t.Errorf("expected 'No symbols found' for empty file, got: %s", result.Content)
	}
}

func TestReadFile_Outline_DefaultFalse(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "package main\n\nfunc main() {\n\tprintln(\"ok\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	tool := &ReadFile{}

	// outline=false (default) should return normal file content, not outline
	result, err := tool.Execute(ctx, ReadFileParams{
		FilePath: filePath,
		Outline:  false,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}
	if strings.Contains(result.Content, "Symbols in") {
		t.Errorf("outline=false should NOT produce symbol output, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "func main()") {
		t.Errorf("outline=false should return file content, got: %s", result.Content)
	}
}

func TestReadFile_Outline_JSFallback(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "app.js")
	content := "function hello() {\n  return 1;\n}\n\nconst x = 42;\n\nclass Greeter {\n  greet() {}\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() outline result error = %s", result.Error.Message)
	}
	if !strings.Contains(result.Content, "hello") {
		t.Errorf("expected 'hello' function, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "x") {
		t.Errorf("expected 'x' variable, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Greeter") {
		t.Errorf("expected 'Greeter' class, got: %s", result.Content)
	}
}

func TestReadFile_Outline_RustFallback(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.rs")
	content := "fn main() {}\n\npub fn public_func() {}\n\nstruct MyStruct {\n    field: i32,\n}\n\npub enum Color {\n    Red,\n    Blue,\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() outline result error = %s", result.Error.Message)
	}
	if !strings.Contains(result.Content, "main") {
		t.Errorf("expected 'main' function, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "public_func") {
		t.Errorf("expected 'public_func' function, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "MyStruct") {
		t.Errorf("expected 'MyStruct' struct, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Color") {
		t.Errorf("expected 'Color' enum, got: %s", result.Content)
	}
}

func TestReadFile_Outline_GoTypeVariants(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "types.go")
	content := "package main\n\ntype MyStruct struct {\n\tName string\n}\n\ntype MyInterface interface {\n\tDo() error\n}\n\ntype MyAlias = string\n\nfunc (m *MyStruct) Method() {}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() outline result error = %s", result.Error.Message)
	}
	if !strings.Contains(result.Content, "MyStruct") {
		t.Errorf("expected 'MyStruct', got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "MyInterface") {
		t.Errorf("expected 'MyInterface', got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Method") {
		t.Errorf("expected 'Method', got: %s", result.Content)
	}
}

func TestReadFile_Outline_Directory(t *testing.T) {
	dir := t.TempDir()

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: dir,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for directory + outline, got nil")
	}
	if result.Error.Kind != ErrKindNotDir {
		t.Errorf("expected NotDir error, got %s: %s", result.Error.Kind, result.Error.Message)
	}
}

func TestReadFile_Outline_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "nonexistent.go")

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for missing file + outline, got nil")
	}
	if result.Error.Kind != ErrKindFileNotFound {
		t.Errorf("expected FileNotFound error, got %s: %s", result.Error.Kind, result.Error.Message)
	}
}
func TestReadFile_Outline_LargeFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "large.go")
	// Create a sparse file just over 10MB (doesn't allocate disk space)
	if err := os.WriteFile(filePath, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filePath, 10*1024*1024+1); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for large file + outline, got nil")
	}
	if result.Error.Kind != ErrKindLargeFile {
		t.Errorf("expected LargeFile error, got %s: %s", result.Error.Kind, result.Error.Message)
	}
}

func TestReadFile_Outline_BinaryFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "binary.so")
	// Create a file with .so extension (binary extension blacklist)
	if err := os.WriteFile(filePath, []byte{0x7f, 0x45, 0x4c, 0x46}, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for binary file + outline, got nil")
	}
	if result.Error.Kind != ErrKindBinaryFile {
		t.Errorf("expected BinaryFile error, got %s: %s", result.Error.Kind, result.Error.Message)
	}
}

func TestReadFile_Outline_MarkdownFallback(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "README.md")
	content := "# Hello\n\n## Section\n\ntext\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() outline result error = %s", result.Error.Message)
	}
	if !strings.Contains(result.Content, "heading") {
		t.Errorf("expected heading kind in output, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Hello") {
		t.Errorf("expected 'Hello' heading, got: %s", result.Content)
	}
}

func TestReadFile_Outline_ShellFallback(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "script.sh")
	content := "#!/bin/bash\n\nhello() {\n    echo hi\n}\n\nfunction world {\n    echo there\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() outline result error = %s", result.Error.Message)
	}
	if !strings.Contains(result.Content, "hello") {
		t.Errorf("expected 'hello' function, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "world") {
		t.Errorf("expected 'world' function, got: %s", result.Content)
	}
}

func TestReadFile_Outline_MakefileFallback(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "Makefile")
	content := "build:\n\tgo build ./...\n\ntest:\n\tgo test ./...\n\n.PHONY: build test\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() outline result error = %s", result.Error.Message)
	}
	if !strings.Contains(result.Content, "build") {
		t.Errorf("expected 'build' target, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "test") {
		t.Errorf("expected 'test' target, got: %s", result.Content)
	}
}

func TestReadFile_Outline_RubyFallback(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "hello.rb")
	content := "def greet\n  puts 'hi'\nend\n\nclass Greeter\n  def hello\n  end\nend\n\nmodule Helper\nend\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() outline result error = %s", result.Error.Message)
	}
	if !strings.Contains(result.Content, "greet") {
		t.Errorf("expected 'greet' function, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Greeter") {
		t.Errorf("expected 'Greeter' class, got: %s", result.Content)
	}
}

func TestReadFile_Outline_YAMLEmptySymbols(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "config.yml")
	content := "name: test\nversion: 1\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFile{}
	result, err := tool.Execute(context.Background(), ReadFileParams{
		FilePath: filePath,
		Outline:  true,
	})
	if err != nil {
		t.Fatalf("Execute() outline error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() outline result error = %s", result.Error.Message)
	}
	// YAML files return nil — should report no symbols gracefully
	if !strings.Contains(result.Content, "No symbols found") {
		t.Errorf("expected 'No symbols found' for YAML, got: %s", result.Content)
	}
}
