package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// EditFile — 成功路径:单文件单 hunk
// ---------------------------------------------------------------------------

func TestEditFile_Success(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// 读文件建立 readState
	rs := NewReadStateStore()
	rs.Record(filePath, content)
	ctx := WithReadState(context.Background(), rs)

	tool := &EditFile{}
	result, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		Hunk: `@@
 line1
-line2
+lineTWO
 line3
`,
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
		t.Fatal(err)
	}
	expected := "line1\nlineTWO\nline3\n"
	if string(data) != expected {
		t.Errorf("file content = %q, want %q", string(data), expected)
	}

	// 验证 Meta: FilePath 为绝对路径
	absPath, _ := filepath.Abs(filePath)
	if result.Meta.FilePath != absPath {
		t.Errorf("FilePath = %q, want %q", result.Meta.FilePath, absPath)
	}
	if result.Meta.LineCount != 3 {
		t.Errorf("LineCount = %d, want 3", result.Meta.LineCount)
	}
	if result.Meta.ByteCount == 0 {
		t.Error("ByteCount should be non-zero")
	}
	if len(result.Meta.DiffHunks) == 0 {
		t.Error("DiffHunks should be non-empty")
	}
	if !strings.Contains(result.Content, "1/1 hunks succeeded") {
		t.Errorf("expected success message, got: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// EditFile — 空结果:无 hunk 时返回 no changes
// ---------------------------------------------------------------------------

func TestEditFile_NoChanges(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	tool := &EditFile{}
	result, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		// 信封格式但无 @@ hunk — 返回 no changes
		Hunk: "*** Begin Patch\n*** End Patch",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	if !strings.Contains(result.Content, "no changes needed") {
		t.Errorf("expected 'no changes needed', got: %s", result.Content)
	}

	absPath, _ := filepath.Abs(filePath)
	if result.Meta.FilePath != absPath {
		t.Errorf("FilePath = %q, want %q", result.Meta.FilePath, absPath)
	}
}

// ---------------------------------------------------------------------------
// EditFile — 全部失败: hunk 不匹配
// ---------------------------------------------------------------------------

func TestEditFile_AllHunksFailed(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := NewReadStateStore()
	rs.Record(filePath, content)
	ctx := WithReadState(context.Background(), rs)

	tool := &EditFile{}
	result, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		Hunk: `@@
 nonexistent
-this line does not exist
+replacement
`,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// 应返回 result.Error 而非 error
	if result.Error == nil {
		t.Fatal("expected result.Error for failed hunk, got nil")
	}
	if result.Error.Class != ErrorClassRecoverable {
		t.Errorf("Error.Class = %v, want recoverable", result.Error.Class)
	}

	// FilePath 应为空 — 全部 hunk 失败时文件未修改，不触发 LSP 诊断
	if result.Meta.FilePath != "" {
		t.Errorf("FilePath = %q, want empty (all hunks failed, no LSP needed)", result.Meta.FilePath)
	}

	// 文件内容不应被修改
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("file should be unchanged, got: %q", string(data))
	}
}

// ---------------------------------------------------------------------------
// EditFile — hunk 为空字符串
// ---------------------------------------------------------------------------

func TestEditFile_EmptyHunk(t *testing.T) {
	dir := t.TempDir()
	tool := &EditFile{}
	result, err := tool.Execute(context.Background(), EditFileParams{
		FilePath: filepath.Join(dir, "test.go"),
		Hunk:     "",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for empty hunk")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("Error.Kind = %v, want invalid_args", result.Error.Kind)
	}
}

// ---------------------------------------------------------------------------
// EditFile — 信封格式:多文件
// ---------------------------------------------------------------------------

func TestEditFile_EnvelopeMultiFile(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "a.go")
	file2 := filepath.Join(dir, "b.go")
	content1 := "package a\n\nfunc A() {\n}\n"
	content2 := "package b\n\nfunc B() {\n}\n"

	if err := os.WriteFile(file1, []byte(content1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte(content2), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := NewReadStateStore()
	rs.Record(file1, content1)
	rs.Record(file2, content2)
	ctx := WithReadState(context.Background(), rs)

	tool := &EditFile{}
	patch := "*** Begin Patch\n" +
		"*** Update File: " + file1 + "\n" +
		"@@\n" +
		" func A() {\n" +
		"-}\n" +
		"+}\n" +
		"*** Update File: " + file2 + "\n" +
		"@@\n" +
		" func B() {\n" +
		"-}\n" +
		"+}\n" +
		"*** End Patch"
	result, err := tool.Execute(ctx, EditFileParams{
		FilePath: file1,
		Hunk:     patch,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	if !strings.Contains(result.Content, "2/2 hunks succeeded") {
		t.Errorf("expected 2/2 success, got: %s", result.Content)
	}

	// Meta 应指向第一个成功的文件
	absPath1, _ := filepath.Abs(file1)
	if result.Meta.FilePath != absPath1 {
		t.Errorf("FilePath = %q, want %q", result.Meta.FilePath, absPath1)
	}

	// DiffHunks 应有两个
	if len(result.Meta.DiffHunks) != 2 {
		t.Errorf("DiffHunks count = %d, want 2", len(result.Meta.DiffHunks))
	}
}

// ---------------------------------------------------------------------------
// EditFile — 无 ReadState:跳过验证直接编辑
// ---------------------------------------------------------------------------

func TestEditFile_WithoutReadState(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// 不注入 ReadStateStore
	ctx := context.Background()

	tool := &EditFile{}
	result, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		Hunk: `@@
 line1
-line2
+lineTWO
 line3
`,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := "line1\nlineTWO\nline3\n"
	if string(data) != expected {
		t.Errorf("file content = %q, want %q", string(data), expected)
	}
}

// ---------------------------------------------------------------------------
// ParseEditPreview — 审批框改动预览解析(不应用文件)
// ---------------------------------------------------------------------------

func TestParseEditPreview_SingleFileSingleHunk(t *testing.T) {
	hunks := ParseEditPreview("/proj/main.go", `@@ -2,3 +2,4 @@ func main
 line1
-line2
+lineTWO
+line2b
 line3
`)
	if len(hunks) != 1 {
		t.Fatalf("hunks = %d, want 1", len(hunks))
	}
	h := hunks[0]
	if h.FilePath != "/proj/main.go" {
		t.Errorf("FilePath = %q, want /proj/main.go", h.FilePath)
	}
	if h.OldStart != 2 || h.NewStart != 2 {
		t.Errorf("start: old=%d new=%d, want 2/2", h.OldStart, h.NewStart)
	}
	if len(h.Lines) != 5 {
		t.Fatalf("lines = %d, want 5", len(h.Lines))
	}
	wantKinds := []DiffLineKind{DiffCtx, DiffDel, DiffAdd, DiffAdd, DiffCtx}
	for i, want := range wantKinds {
		if h.Lines[i].Kind != want {
			t.Errorf("line %d kind = %q, want %q", i, h.Lines[i].Kind, want)
		}
	}
	// 行号:上下文 2/2,删除 3/-,新增 -/3、-/4,上下文 4/5
	// (@@ -2,3 +2,4 覆盖旧文件 2-4 行、新文件 2-5 行)
	if h.Lines[1].OldNum != 3 || h.Lines[1].NewNum != 0 {
		t.Errorf("del line num: old=%d new=%d, want 3/0", h.Lines[1].OldNum, h.Lines[1].NewNum)
	}
	if h.Lines[2].NewNum != 3 || h.Lines[3].NewNum != 4 {
		t.Errorf("add line nums: %d %d, want 3 4", h.Lines[2].NewNum, h.Lines[3].NewNum)
	}
	if h.Lines[4].OldNum != 4 || h.Lines[4].NewNum != 5 {
		t.Errorf("trailing ctx num: old=%d new=%d, want 4/5", h.Lines[4].OldNum, h.Lines[4].NewNum)
	}
}

func TestParseEditPreview_MultiFile(t *testing.T) {
	hunks := ParseEditPreview("/proj/main.go", `*** Begin Patch
*** Update File: a.go
@@ -1,2 +1,2 @@
-oldA
+newA
*** Update File: b.go
@@ -5 +5 @@
 b5
*** End Patch
`)
	if len(hunks) != 2 {
		t.Fatalf("hunks = %d, want 2", len(hunks))
	}
	if !strings.HasSuffix(hunks[0].FilePath, "a.go") || !strings.HasSuffix(hunks[1].FilePath, "b.go") {
		t.Errorf("file paths = %q / %q, want a.go / b.go", hunks[0].FilePath, hunks[1].FilePath)
	}
}

func TestParseEditPreview_CountOmittedHeader(t *testing.T) {
	// "@@ -1 +1 @@" 省略 count 的 header:count 默认 1
	hunks := ParseEditPreview("/proj/main.go", `@@ -3 +3 @@
 old3
-del3
+add3
`)
	if len(hunks) != 1 {
		t.Fatalf("hunks = %d, want 1", len(hunks))
	}
	h := hunks[0]
	if h.OldStart != 3 || h.NewStart != 3 {
		t.Errorf("start: old=%d new=%d, want 3/3", h.OldStart, h.NewStart)
	}
}

func TestParseEditPreview_EmptyReturnsNil(t *testing.T) {
	if hunks := ParseEditPreview("/proj/main.go", ""); hunks != nil {
		t.Errorf("empty hunk = %v, want nil", hunks)
	}
}

// TestEditFile_NotBeenReadHint — 未读文件编辑失败时,输出必须含可操作的
// header 提示(单文件编辑省略 *** Update File: 头)。
func TestEditFile_NotBeenReadHint(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// 注入空 ReadStateStore(有校验但未读过)→ not-been-read
	ctx := WithReadState(context.Background(), NewReadStateStore())
	tool := &EditFile{}
	result, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		Hunk: `@@
 line1
-line2
+lineTWO
 line3
`,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error == nil {
		t.Fatal("expected error for unread file")
	}
	if !strings.Contains(result.Content, "file has not been read yet") {
		t.Errorf("output missing not-been-read error: %q", result.Content)
	}
	if !strings.Contains(result.Content, "hint:") ||
		!strings.Contains(result.Content, "edit 前必须 read") {
		t.Errorf("output missing header hint: %q", result.Content)
	}
}

// TestRegression_DiffHunkLineNumbersActualPosition — TUI diff view 行号回归防护。
//
// REGRESSION: 非标准 @@ header(如 "@@ func name" / 裸 "@@")下,parseDiffHunk
// 的行号从 1 开始编号,而 hunk 实际匹配在文件中部——TUI 的行号列显示
// 1,2,3... 与实际位置无关。修复后行号以 seekHunk 实际匹配位置为基准。
func TestRegression_DiffHunkLineNumbersActualPosition(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = fmt.Sprintf("line%d", i+1)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := WithReadState(context.Background(), NewReadStateStore())
	ReadStateFromContext(ctx).Record(filePath, content)

	tool := &EditFile{}
	result, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		Hunk: `@@ func foo
 line50
-line51
+line51-changed
 line52
`,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	hunks := result.Meta.DiffHunks
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	h := hunks[0]
	if h.OldStart != 50 || h.NewStart != 50 {
		t.Errorf("hunk start should be actual match position 50: old=%d new=%d", h.OldStart, h.NewStart)
	}
	// 行号:上下文 line50=50/50,删除 line51=51/-,新增= -/51,上下文 line52=52/52
	want := []struct {
		kind    DiffLineKind
		old, new int
	}{
		{DiffCtx, 50, 50},
		{DiffDel, 51, 0},
		{DiffAdd, 0, 51},
		{DiffCtx, 52, 52},
	}
	if len(h.Lines) != len(want) {
		t.Fatalf("expected %d lines, got %d", len(want), len(h.Lines))
	}
	for i, w := range want {
		if h.Lines[i].Kind != w.kind || h.Lines[i].OldNum != w.old || h.Lines[i].NewNum != w.new {
			t.Errorf("line %d: got kind=%s old=%d new=%d, want kind=%s old=%d new=%d",
				i, h.Lines[i].Kind, h.Lines[i].OldNum, h.Lines[i].NewNum, w.kind, w.old, w.new)
		}
	}
}

// TestRegression_DiffHunkMultiHunkNewStartOffset — 多 hunk 时新文件行号
// 必须反映前面 hunk 的净行数变化。
//
// REGRESSION: 每个 hunk 独立从 1 开始编号,第二个 hunk 的 NewStart 不包含
// 第一个 hunk 的净变化(如删 1 加 2 → +1),TUI 显示的新文件行号偏移。
func TestRegression_DiffHunkMultiHunkNewStartOffset(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("line%d", i+1)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := WithReadState(context.Background(), NewReadStateStore())
	ReadStateFromContext(ctx).Record(filePath, content)

	tool := &EditFile{}
	result, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		Hunk: `@@
 line10
-line11
+line11a
+line11b
 line12
@@
 line20
-line21
+line21a
 line22
`,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Error != nil {
		t.Fatalf("Execute() result.Error = %v", result.Error)
	}
	hunks := result.Meta.DiffHunks
	if len(hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(hunks))
	}
	// 第一个 hunk:匹配 line10,净变化 +1(删 1 加 2)
	if hunks[0].OldStart != 10 || hunks[0].NewStart != 10 {
		t.Errorf("hunk1 start: old=%d new=%d, want 10/10", hunks[0].OldStart, hunks[0].NewStart)
	}
	// 第二个 hunk:内容 line20 在应用 hunk1 后位于新文件第 21 行(旧文件第 20 行)
	if hunks[1].OldStart != 20 || hunks[1].NewStart != 21 {
		t.Errorf("hunk2 start: old=%d new=%d, want 20/21", hunks[1].OldStart, hunks[1].NewStart)
	}
}

// ---------------------------------------------------------------------------
// EditFile — 连续 edit 无需 re-read
// ---------------------------------------------------------------------------

func TestEditFile_ChainedEdits(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := NewReadStateStore()
	rs.Record(filePath, content)
	ctx := WithReadState(context.Background(), rs)

	tool := &EditFile{}

	// 第一次 edit
	result1, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		Hunk: `@@
 line1
-line2
+lineTWO
 line3
`,
	})
	if err != nil || result1.Error != nil {
		t.Fatalf("first edit failed: err=%v resultErr=%v", err, result1.Error)
	}

	// 第二次 edit — 无需 re-read
	result2, err := tool.Execute(ctx, EditFileParams{
		FilePath: filePath,
		Hunk: `@@
-line1
+lineONE
 lineTWO
`,
	})
	if err != nil || result2.Error != nil {
		t.Fatalf("second edit failed: err=%v resultErr=%v", err, result2.Error)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	expected := "lineONE\nlineTWO\nline3\n"
	if string(data) != expected {
		t.Errorf("file content = %q, want %q", string(data), expected)
	}
}
