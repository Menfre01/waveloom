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
	// J2 修复后，同一文件多 Section 不再被拒绝，每个 Section 独立验证 TAG 并应用操作
	if result.Error != nil {
		t.Fatalf("expected success for multi-section same file, got error: %s", result.Error.Message)
	}
	content := readTestFile(t, filePath)
	if !strings.Contains(content, "var x = 10") || !strings.Contains(content, "var y = 20") {
		t.Errorf("expected both SWAPs applied, got: %s", content)
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

// TestEditFileHashline_MultipleOps 验证非重叠多操作按声明顺序应用。
// INS.PRE 4 和 INS.POST 4 同参考行重叠已被拒绝，分两次 edit 调用。
func TestEditFileHashline_MultipleOps(t *testing.T) {
	ctx, dir, filePath, tag := setupEditTest(t, "main.go",
		"package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")

	tool := &EditFileHashline{}

	// 第一步：INS.PRE 4 + SWAP 2（无重叠）
	patch1 := makePatch(makeSection(filePath, tag,
		"INS.PRE 4:", "+\t// before",
		"SWAP 2.=2:", "+", "+import \"fmt\"",
	))
	result1, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch1, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() step1 error = %v", err)
	}
	if result1.Error != nil {
		t.Fatalf("step1 unexpected error: %s", result1.Error.Message)
	}

	// 第二步：重新 read 获取新 TAG，再 INS.POST
	reader := &ReadFileHashline{}
	readResult, readErr := reader.Execute(ctx, ReadFileHashlineParams{FilePath: "main.go", WorkingDir: dir})
	if readErr != nil || readResult.Error != nil {
		t.Fatalf("read step2 failed: err=%v, toolErr=%v", readErr, readResult.Error)
	}
	newTag := extractTag(readResult.Content)

	patch2 := makePatch(makeSection(filePath, newTag,
		"INS.POST 4:", "+\t// after",
	))
	result2, err := tool.Execute(ctx, EditFileHashlineParams{Patch: patch2, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Execute() step2 error = %v", err)
	}
	if result2.Error != nil {
		t.Fatalf("step2 unexpected error: %s", result2.Error.Message)
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

// extractTag 从编辑返回内容中提取 TAG（格式 [path#XXXX]）。
func extractTag(output string) string {
	// Format: [path#TAG] ✓ update ...
	idx := strings.Index(output, "#")
	if idx < 0 {
		return ""
	}
	start := idx + 1
	end := start
	for end < len(output) && output[end] != ']' && output[end] != ' ' {
		end++
	}
	return output[start:end]
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
// ---------------------------------------------------------------------------
// formatLocalDiffExcerpt 单元测试
// ---------------------------------------------------------------------------

func TestFormatLocalDiffExcerpt_DeleteAndAdd(t *testing.T) {
	hunks := []hashline.EditHunk{
		{
			OldStart: 3,
			OldCount: 1,
			NewStart: 3,
			NewCount: 1,
			Lines: []hashline.EditLine{
				{Kind: hashline.LineDel, Content: "old line", OldNum: 3},
				{Kind: hashline.LineAdd, Content: "new line", NewNum: 3},
			},
		},
	}
	got := formatLocalDiffExcerpt(hunks, 12)
	if !strings.Contains(got, "--- edit delta ---") {
		t.Error("missing header")
	}
	if !strings.Contains(got, "-3:old line") {
		t.Errorf("missing delete line with line number, got:\n%s", got)
	}
	if !strings.Contains(got, "+3:new line") {
		t.Errorf("missing add line with line number, got:\n%s", got)
	}
}

func TestFormatLocalDiffExcerpt_ContextLines(t *testing.T) {
	hunks := []hashline.EditHunk{
		{
			OldStart: 3,
			OldCount: 3,
			NewStart: 3,
			NewCount: 3,
			Lines: []hashline.EditLine{
				{Kind: hashline.LineCtx, Content: "  unchanged", OldNum: 3, NewNum: 3},
				{Kind: hashline.LineDel, Content: "old", OldNum: 4},
				{Kind: hashline.LineAdd, Content: "new", NewNum: 4},
				{Kind: hashline.LineCtx, Content: "  unchanged", OldNum: 5, NewNum: 5},
			},
		},
	}
	got := formatLocalDiffExcerpt(hunks, 12)
	if !strings.Contains(got, " 3:  unchanged") {
		t.Errorf("missing context line with line number, got:\n%s", got)
	}
}

func TestFormatLocalDiffExcerpt_MaxLines(t *testing.T) {
	hunks := []hashline.EditHunk{
		{
			Lines: []hashline.EditLine{
				{Kind: hashline.LineAdd, Content: "line1", NewNum: 1},
				{Kind: hashline.LineAdd, Content: "line2", NewNum: 2},
				{Kind: hashline.LineAdd, Content: "line3", NewNum: 3},
			},
		},
	}
	got := formatLocalDiffExcerpt(hunks, 2)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	// header + 2 lines + truncation = 4 lines total
	if len(lines) < 3 {
		t.Errorf("expected at least 3 lines (header + 2 content + truncation), got %d:\n%s", len(lines), got)
	}
	if !strings.Contains(got, "...") {
		t.Error("missing truncation marker")
	}
	// line3 should NOT appear
	if strings.Contains(got, "line3") {
		t.Error("line3 should be truncated")
	}
}

func TestFormatLocalDiffExcerpt_LineHeaderIgnored(t *testing.T) {
	hunks := []hashline.EditHunk{
		{
			Lines: []hashline.EditLine{
				{Kind: hashline.LineHeader, Content: "@@ -1,3 +1,3 @@", OldNum: 0, NewNum: 0},
				{Kind: hashline.LineDel, Content: "removed", OldNum: 1},
				{Kind: hashline.LineAdd, Content: "added", NewNum: 1},
			},
		},
	}
	got := formatLocalDiffExcerpt(hunks, 12)
	if !strings.Contains(got, "@@ -1,3 +1,3 @@") {
		t.Error("LineHeader should be present in output (not silently dropped)")
	}
	// LineHeader 不计入 maxLines，所以两行变更都应显示
	if !strings.Contains(got, "-1:removed") {
		t.Error("delete line should be present after header")
	}
	if !strings.Contains(got, "+1:added") {
		t.Error("add line should be present after header")
	}
}

func TestFormatLocalDiffExcerpt_EmptyHunks(t *testing.T) {
	got := formatLocalDiffExcerpt(nil, 12)
	if got != "--- edit delta ---\n" {
		t.Errorf("expected only header for nil hunks, got:\n%s", got)
	}

	got = formatLocalDiffExcerpt([]hashline.EditHunk{}, 12)
	if got != "--- edit delta ---\n" {
		t.Errorf("expected only header for empty hunks, got:\n%s", got)
	}
}

func TestFormatLocalDiffExcerpt_MultipleLinesPerHunk(t *testing.T) {
	hunks := []hashline.EditHunk{
		{
			Lines: []hashline.EditLine{
				{Kind: hashline.LineDel, Content: "  old1", OldNum: 2},
				{Kind: hashline.LineAdd, Content: "  new1", NewNum: 2},
			},
		},
		{
			Lines: []hashline.EditLine{
				{Kind: hashline.LineDel, Content: "  old2", OldNum: 5},
				{Kind: hashline.LineAdd, Content: "  new2", NewNum: 5},
			},
		},
	}
	got := formatLocalDiffExcerpt(hunks, 12)
	if !strings.Contains(got, "-2:  old1") {
		t.Errorf("missing first delete, got:\n%s", got)
	}
	if !strings.Contains(got, "+5:  new2") {
		t.Errorf("missing second add, got:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// formatSectionResults 单元测试
// ---------------------------------------------------------------------------

func TestFormatSectionResults_Update(t *testing.T) {
	results := []hashline.SectionResult{
		{
			Path: "src/main.go", Op: "update", NewTAG: "A1B2", LinesDelta: 3,
			DiffHunks: []hashline.EditHunk{
				{Lines: []hashline.EditLine{
					{Kind: hashline.LineDel, Content: "old", OldNum: 1},
					{Kind: hashline.LineAdd, Content: "new", NewNum: 1},
				}},
			},
		},
	}
	got := formatSectionResults(results)
	// TAG must be present for chained edits
	if !strings.Contains(got, "[src/main.go#A1B2]") {
		t.Errorf("missing TAG in output, got:\n%s", got)
	}
	if !strings.Contains(got, "(+3 lines)") {
		t.Errorf("missing line delta, got:\n%s", got)
	}
	if !strings.Contains(got, "--- edit delta ---") {
		t.Errorf("missing delta excerpt, got:\n%s", got)
	}
}

func TestFormatSectionResults_Delete(t *testing.T) {
	results := []hashline.SectionResult{
		{Path: "src/main.go", Op: "delete", OldTAG: "A1B2", NewTAG: "A1B2"},
	}
	got := formatSectionResults(results)
	if !strings.Contains(got, "deleted") {
		t.Errorf("missing delete confirmation, got:\n%s", got)
	}
	if !strings.Contains(got, "#A1B2") {
		t.Errorf("missing TAG, got:\n%s", got)
	}
}

func TestFormatSectionResults_Rename(t *testing.T) {
	results := []hashline.SectionResult{
		{Path: "src/main.go", Op: "rename", OldTAG: "A1B2", NewTAG: "C3D4"},
	}
	got := formatSectionResults(results)
	if !strings.Contains(got, "renamed") {
		t.Errorf("missing rename confirmation, got:\n%s", got)
	}
	if !strings.Contains(got, "#C3D4") {
		t.Errorf("missing new TAG, got:\n%s", got)
	}
}

func TestFormatSectionResults_Error(t *testing.T) {
	results := []hashline.SectionResult{
		{Path: "src/main.go", Error: &hashline.EditError{Kind: "file_not_found", Message: "file not found: src/main.go"}},
	}
	got := formatSectionResults(results)
	if !strings.Contains(got, "✗") {
		t.Errorf("missing error marker, got:\n%s", got)
	}
	if !strings.Contains(got, "file not found") {
		t.Errorf("missing error message, got:\n%s", got)
	}
}

func TestFormatSectionResults_Warning(t *testing.T) {
	results := []hashline.SectionResult{
		{Path: "src/main.go", Op: "update", NewTAG: "A1B2", LinesDelta: 0, Warning: "TAG expired, auto-recovered"},
	}
	got := formatSectionResults(results)
	if !strings.Contains(got, "⚠") {
		t.Errorf("missing warning marker, got:\n%s", got)
	}
	if !strings.Contains(got, "TAG expired, auto-recovered") {
		t.Errorf("missing warning text, got:\n%s", got)
	}
}

func TestFormatSectionResults_MultipleResults(t *testing.T) {
	results := []hashline.SectionResult{
		{Path: "src/foo.go", Op: "update", NewTAG: "A1B2", LinesDelta: 1},
		{Path: "src/bar.go", Op: "delete", OldTAG: "C3D4", NewTAG: "C3D4"},
	}
	got := formatSectionResults(results)
	if !strings.Contains(got, "src/foo.go") || !strings.Contains(got, "src/bar.go") {
		t.Errorf("missing one or both files, got:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// formatPostEditContext 单元测试
// ---------------------------------------------------------------------------

// testFS 是 hashline.FileSystem 的内存实现，用于测试。
type testFS struct {
	files map[string]string
}

func (fs *testFS) ReadFile(path string) (string, error)     { return fs.files[path], nil }
func (fs *testFS) WriteFile(path string, content string) error { fs.files[path] = content; return nil }
func (fs *testFS) MkdirAll(path string) error               { return nil }
func (fs *testFS) Remove(path string) error                 { delete(fs.files, path); return nil }
func (fs *testFS) ResolvePath(path string) string           { return path }

func TestFormatPostEditContext_Normal(t *testing.T) {
	fs := &testFS{files: map[string]string{
		"src/main.go": "line1\nline2\nline3\nline4\nline5\n",
	}}
	results := []hashline.SectionResult{
		{
			Path: "src/main.go", Op: "update",
			DiffHunks: []hashline.EditHunk{
				{NewStart: 2, NewCount: 2, Lines: []hashline.EditLine{
					{Kind: hashline.LineDel, OldNum: 2},
					{Kind: hashline.LineAdd, NewNum: 2},
				}},
			},
		},
	}
	got := formatPostEditContext(fs, results)
	if !strings.Contains(got, "--- post-edit context (use TAG above) ---") {
		t.Errorf("missing separator, got:\n%s", got)
	}
	if !strings.Contains(got, "1:line1") {
		t.Errorf("missing context before change, got:\n%s", got)
	}
}

func TestFormatPostEditContext_FileBeginning(t *testing.T) {
	fs := &testFS{files: map[string]string{
		"src/main.go": "line1\nline2\nline3\nline4\nline5\n",
	}}
	results := []hashline.SectionResult{
		{
			Path: "src/main.go", Op: "update",
			DiffHunks: []hashline.EditHunk{
				{NewStart: 1, NewCount: 1, Lines: []hashline.EditLine{
					{Kind: hashline.LineAdd, NewNum: 1},
				}},
			},
		},
	}
	got := formatPostEditContext(fs, results)
	if strings.Contains(got, "lines above omitted") {
		t.Errorf("unexpected 'above omitted' at file beginning, got:\n%s", got)
	}
}

func TestFormatPostEditContext_FileEnd(t *testing.T) {
	fs := &testFS{files: map[string]string{
		"src/main.go": "line1\nline2\nline3\n",
	}}
	results := []hashline.SectionResult{
		{
			Path: "src/main.go", Op: "update",
			DiffHunks: []hashline.EditHunk{
				{NewStart: 3, NewCount: 1, Lines: []hashline.EditLine{
					{Kind: hashline.LineAdd, NewNum: 3},
				}},
			},
		},
	}
	got := formatPostEditContext(fs, results)
	if strings.Contains(got, "lines below omitted") {
		t.Errorf("unexpected 'below omitted' at file end, got:\n%s", got)
	}
}

func TestFormatPostEditContext_LargeEditTruncation(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("line%d", i+1))
	}
	content := strings.Join(lines, "\n") + "\n"
	fs := &testFS{files: map[string]string{"src/main.go": content}}
	results := []hashline.SectionResult{
		{
			Path: "src/main.go", Op: "update",
			DiffHunks: []hashline.EditHunk{
				{NewStart: 5, NewCount: 25, Lines: []hashline.EditLine{
					{Kind: hashline.LineAdd, NewNum: 5},
				}},
			},
		},
	}
	got := formatPostEditContext(fs, results)
	if !strings.Contains(got, "lines in edit region omitted") {
		t.Errorf("missing truncation marker for large edit, got:\n%s", got)
	}
}

func TestFormatPostEditContext_SkipError(t *testing.T) {
	fs := &testFS{files: map[string]string{}}
	results := []hashline.SectionResult{
		{Path: "src/main.go", Op: "update", Error: &hashline.EditError{Kind: "tag_mismatch", Message: "TAG mismatch"}},
	}
	got := formatPostEditContext(fs, results)
	if got != "" {
		t.Errorf("expected empty output for error result, got:\n%s", got)
	}
}

func TestFormatPostEditContext_SkipNonUpdate(t *testing.T) {
	fs := &testFS{files: map[string]string{"src/main.go": "content\n"}}
	results := []hashline.SectionResult{
		{Path: "src/main.go", Op: "delete", OldTAG: "A1B2", NewTAG: "A1B2"},
	}
	got := formatPostEditContext(fs, results)
	if got != "" {
		t.Errorf("expected empty output for delete, got:\n%s", got)
	}
}

func TestFormatPostEditContext_SkipEmptyHunks(t *testing.T) {
	fs := &testFS{files: map[string]string{"src/main.go": "content\n"}}
	results := []hashline.SectionResult{
		{Path: "src/main.go", Op: "update", DiffHunks: nil},
	}
	got := formatPostEditContext(fs, results)
	if got != "" {
		t.Errorf("expected empty for nil hunks, got:\n%s", got)
	}
}
