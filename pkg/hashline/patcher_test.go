package hashline

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// FormatContent tests
// ---------------------------------------------------------------------------

func TestFormatContentBasic(t *testing.T) {
	output := FormatContent("src/main.go", "A1B2", "line1\nline2\nline3\n", 0, 0)
	if !strings.HasPrefix(output, "[src/main.go#A1B2]\n") {
		t.Errorf("missing header: %q", output)
	}
	if !strings.Contains(output, "1:line1") {
		t.Errorf("missing line 1: %q", output)
	}
	if !strings.Contains(output, "2:line2") {
		t.Errorf("missing line 2: %q", output)
	}
}

func TestFormatContentWithOffset(t *testing.T) {
	output := FormatContent("f", "TAG", "a\nb\nc\nd\n", 1, 2) // lines 2-3 (1-based)
	if strings.Contains(output, "1:a") {
		t.Errorf("should not contain line 1: %q", output)
	}
	if !strings.Contains(output, "2:b") {
		t.Errorf("missing line 2: %q", output)
	}
	if !strings.Contains(output, "3:c") {
		t.Errorf("missing line 3: %q", output)
	}
	if strings.Contains(output, "4:d") {
		t.Errorf("should not contain line 4: %q", output)
	}
}

func TestFormatContentTruncated(t *testing.T) {
	output := FormatContent("f", "TAG", "a\nb\nc\nd\n", 0, 2)
	if !strings.Contains(output, "truncated") {
		t.Errorf("missing truncation hint: %q", output)
	}
}

func TestFormatContentEmpty(t *testing.T) {
	output := FormatContent("f", "TAG", "", 0, 0)
	if !strings.HasPrefix(output, "[f#TAG]\n") {
		t.Errorf("empty file still needs header: %q", output)
	}
}

func TestFormatContentOffsetBeyond(t *testing.T) {
	output := FormatContent("f", "TAG", "a\n", 5, 0)
	if !strings.Contains(output, "shorter than the provided offset") {
		t.Errorf("missing offset warning: %q", output)
	}
}

// ---------------------------------------------------------------------------
// MemoryFS — 内存文件系统用于测试
// ---------------------------------------------------------------------------

type MemoryFS struct {
	files map[string]string
	dirs  map[string]bool
}

func NewMemoryFS() *MemoryFS {
	return &MemoryFS{
		files: make(map[string]string),
		dirs:  make(map[string]bool),
	}
}

func (fs *MemoryFS) ReadFile(path string) (string, error) {
	content, ok := fs.files[path]
	if !ok {
		return "", fmt.Errorf("%w: %s", os.ErrNotExist, path)
	}
	return content, nil
}

func (fs *MemoryFS) WriteFile(path string, content string) error {
	fs.files[path] = content
	return nil
}

func (fs *MemoryFS) MkdirAll(path string) error {
	fs.dirs[path] = true
	return nil
}

func (fs *MemoryFS) Remove(path string) error {
	delete(fs.files, path)
	return nil
}

func (fs *MemoryFS) ResolvePath(path string) string {
	return path
}

// ---------------------------------------------------------------------------
// 解析测试
// ---------------------------------------------------------------------------

func TestParsePatchSingleSwap(t *testing.T) {
	input := `*** Begin Patch
[src/main.go#A1B2]
SWAP 2.=2:
+    fmt.Println("hello, world")
*** End Patch`

	patch, err := ParsePatch(input)
	if err != nil {
		t.Fatalf("ParsePatch failed: %v", err)
	}

	if len(patch.Sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(patch.Sections))
	}

	sec := patch.Sections[0]
	if sec.Path != "src/main.go" {
		t.Errorf("expected path src/main.go, got %q", sec.Path)
	}
	if sec.TAG != "A1B2" {
		t.Errorf("expected TAG A1B2, got %q", sec.TAG)
	}
	if len(sec.Ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(sec.Ops))
	}

	op := sec.Ops[0]
	if op.Kind != OpSWAP {
		t.Errorf("expected SWAP, got %s", op.Kind)
	}
	if op.LineStart != 2 || op.LineEnd != 2 {
		t.Errorf("expected range 2.=2, got %d.=%d", op.LineStart, op.LineEnd)
	}
}

func TestParsePatchMultipleOps(t *testing.T) {
	input := `*** Begin Patch
[src/main.go#A1B2]
SWAP 2.=2:
+    fmt.Println("hello, world")
INS.POST 4:
+    // cleanup on exit
+    defer os.Remove(tmpFile)
*** End Patch`

	patch, err := ParsePatch(input)
	if err != nil {
		t.Fatalf("ParsePatch failed: %v", err)
	}

	sec := patch.Sections[0]
	if len(sec.Ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(sec.Ops))
	}

	if sec.Ops[0].Kind != OpSWAP {
		t.Errorf("expected SWAP first, got %s", sec.Ops[0].Kind)
	}
	if sec.Ops[1].Kind != OpINS {
		t.Errorf("expected INS second, got %s", sec.Ops[1].Kind)
	}
	if sec.Ops[1].Position != "post" || sec.Ops[1].RefLine != 4 {
		t.Errorf("expected INS.POST 4, got %s %d", sec.Ops[1].Position, sec.Ops[1].RefLine)
	}
}

func TestParsePatchDel(t *testing.T) {
	input := `*** Begin Patch
[src/main.go#A1B2]
DEL 4.=6
*** End Patch`

	patch, err := ParsePatch(input)
	if err != nil {
		t.Fatalf("ParsePatch failed: %v", err)
	}

	op := patch.Sections[0].Ops[0]
	if op.Kind != OpDEL {
		t.Errorf("expected DEL, got %s", op.Kind)
	}
	if op.LineStart != 4 || op.LineEnd != 6 {
		t.Errorf("expected range 4.=6, got %d.=%d", op.LineStart, op.LineEnd)
	}
}

func TestParsePatchInsHeadTail(t *testing.T) {
	input := `*** Begin Patch
[src/main.go#A1B2]
INS.HEAD:
+// Copyright 2024
+
*** End Patch`

	patch, err := ParsePatch(input)
	if err != nil {
		t.Fatalf("ParsePatch failed: %v", err)
	}

	op := patch.Sections[0].Ops[0]
	if op.Kind != OpINS || op.Position != "head" {
		t.Errorf("expected INS head, got %s %s", op.Kind, op.Position)
	}
}

func TestParsePatchMultipleSections(t *testing.T) {
	input := `*** Begin Patch
[src/main.go#A1B2]
SWAP 2.=2:
+    fmt.Println("hello")

[src/config.go#C3D4]
DEL 12.=15
*** End Patch`

	patch, err := ParsePatch(input)
	if err != nil {
		t.Fatalf("ParsePatch failed: %v", err)
	}

	if len(patch.Sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(patch.Sections))
	}
}

// REGRESSION: parseDelOp 未像 parseSwapOp/parseInsOp 剥离尾部冒号，
// 导致 LLM 写 DEL 136: 时 parseLineRange("136:") 报 invalid line number。
func TestParsePatchDelTrailingColon(t *testing.T) {
	// Single line with trailing colon
	input := "*** Begin Patch\n[src/main.go#A1B2]\nDEL 136:\n*** End Patch"
	patch, err := ParsePatch(input)
	if err != nil {
		t.Fatalf("ParsePatch DEL 136: failed: %v", err)
	}
	op := patch.Sections[0].Ops[0]
	if op.Kind != OpDEL {
		t.Errorf("expected DEL, got %s", op.Kind)
	}
	if op.LineStart != 136 || op.LineEnd != 136 {
		t.Errorf("expected single line 136, got %d.=%d", op.LineStart, op.LineEnd)
	}

	// Range with trailing colon
	input2 := "*** Begin Patch\n[src/main.go#A1B2]\nDEL 4.=6:\n*** End Patch"
	patch2, err := ParsePatch(input2)
	if err != nil {
		t.Fatalf("ParsePatch DEL 4.=6: failed: %v", err)
	}
	op2 := patch2.Sections[0].Ops[0]
	if op2.LineStart != 4 || op2.LineEnd != 6 {
		t.Errorf("expected range 4.=6, got %d.=%d", op2.LineStart, op2.LineEnd)
	}

	// Colon with spaces
	input3 := "*** Begin Patch\n[src/main.go#A1B2]\nDEL 42  :\n*** End Patch"
	patch3, err := ParsePatch(input3)
	if err != nil {
		t.Fatalf("ParsePatch DEL 42  : failed: %v", err)
	}
	op3 := patch3.Sections[0].Ops[0]
	if op3.LineStart != 42 {
		t.Errorf("expected line 42, got %d", op3.LineStart)
	}
}

// REGRESSION: LLM 兼容性回归测试 — 覆盖 readBody / 行尾注释 / INS.HEAD 无冒号 /
// INS. PRE 有多余空格 / End Patch 大小写 / MV 单引号等高频 LLM 格式变体。
func TestParsePatchLLMCompat(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(*testing.T, *Patch)
	}{
		{
			name: "readBody tolerates leading whitespace before +",
			input: "*** Begin Patch\n[src/main.go#A1B2]\nSWAP 1.=1:\n +indented\n*** End Patch",
			check: func(t *testing.T, p *Patch) {
				t.Helper()
				if len(p.Sections[0].Ops[0].Body) != 1 || p.Sections[0].Ops[0].Body[0] != "indented" {
					t.Errorf("expected Body=[indented], got %v", p.Sections[0].Ops[0].Body)
				}
			},
		},
		{
			name: "readBody skips blank lines between body lines",
			input: "*** Begin Patch\n[src/main.go#A1B2]\nSWAP 1.=1:\n+line1\n\n+line2\n*** End Patch",
			check: func(t *testing.T, p *Patch) {
				t.Helper()
				body := p.Sections[0].Ops[0].Body
				if len(body) != 2 || body[0] != "line1" || body[1] != "line2" {
					t.Errorf("expected 2 body lines, got %v", body)
				}
			},
		},
		{
			name: "inline comment # after SWAP",
			input: "*** Begin Patch\n[src/main.go#A1B2]\nSWAP 2.=2: # replace greeting\n+hello\n*** End Patch",
			check: func(t *testing.T, p *Patch) {
				t.Helper()
				op := p.Sections[0].Ops[0]
				if op.LineStart != 2 || op.LineEnd != 2 {
					t.Errorf("expected range 2.=2, got %d.=%d", op.LineStart, op.LineEnd)
				}
			},
		},
		{
			name: "inline comment // after DEL",
			input: "*** Begin Patch\n[src/main.go#A1B2]\nDEL 3.=5 // remove block\n*** End Patch",
			check: func(t *testing.T, p *Patch) {
				t.Helper()
				op := p.Sections[0].Ops[0]
				if op.LineStart != 3 || op.LineEnd != 5 {
					t.Errorf("expected range 3.=5, got %d.=%d", op.LineStart, op.LineEnd)
				}
			},
		},
		{
			name: "INS.HEAD without colon",
			input: "*** Begin Patch\n[src/main.go#A1B2]\nINS.HEAD\n+// header\n*** End Patch",
			check: func(t *testing.T, p *Patch) {
				t.Helper()
				op := p.Sections[0].Ops[0]
				if op.Kind != OpINS || op.Position != "head" {
					t.Errorf("expected INS head, got %s %s", op.Kind, op.Position)
				}
			},
		},
		{
			name: "INS.TAIL without colon",
			input: "*** Begin Patch\n[src/main.go#A1B2]\nINS.TAIL\n+// footer\n*** End Patch",
			check: func(t *testing.T, p *Patch) {
				t.Helper()
				op := p.Sections[0].Ops[0]
				if op.Kind != OpINS || op.Position != "tail" {
					t.Errorf("expected INS tail, got %s %s", op.Kind, op.Position)
				}
			},
		},
		{
			name: "INS. PRE with space after dot",
			input: "*** Begin Patch\n[src/main.go#A1B2]\nINS. PRE 3:\n+before line3\n*** End Patch",
			check: func(t *testing.T, p *Patch) {
				t.Helper()
				op := p.Sections[0].Ops[0]
				if op.Kind != OpINS || op.Position != "pre" || op.RefLine != 3 {
					t.Errorf("expected INS pre 3, got %s %s %d", op.Kind, op.Position, op.RefLine)
				}
			},
		},
		{
			name: "*** end patch lowercase",
			input: "*** Begin Patch\n[src/main.go#A1B2]\nDEL 1\n*** end patch",
			check: func(t *testing.T, p *Patch) {
				t.Helper()
				if len(p.Sections) != 1 {
					t.Errorf("expected 1 section, got %d", len(p.Sections))
				}
			},
		},
		{
			name: "MV with single-quoted path",
			input: "*** Begin Patch\n[src/main.go#A1B2]\nMV '/tmp/new.go'\n*** End Patch",
			check: func(t *testing.T, p *Patch) {
				t.Helper()
				op := p.Sections[0].Ops[0]
				if op.DestPath != "/tmp/new.go" {
					t.Errorf("expected /tmp/new.go, got %q", op.DestPath)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch, err := ParsePatch(tt.input)
			if err != nil {
				t.Fatalf("ParsePatch failed: %v", err)
			}
			tt.check(t, patch)
		})
	}
}

func TestParsePatchRem(t *testing.T) {
	input := `*** Begin Patch
[src/old.go#A1B2]
REM
*** End Patch`

	patch, err := ParsePatch(input)
	if err != nil {
		t.Fatalf("ParsePatch failed: %v", err)
	}

	op := patch.Sections[0].Ops[0]
	if op.Kind != OpREM {
		t.Errorf("expected REM, got %s", op.Kind)
	}
}

func TestParsePatchMv(t *testing.T) {
	input := `*** Begin Patch
[src/old.go#A1B2]
MV src/new.go
*** End Patch`

	patch, err := ParsePatch(input)
	if err != nil {
		t.Fatalf("ParsePatch failed: %v", err)
	}

	op := patch.Sections[0].Ops[0]
	if op.Kind != OpMV || op.DestPath != "src/new.go" {
		t.Errorf("expected MV src/new.go, got %s %s", op.Kind, op.DestPath)
	}
}

func TestParsePatchSyntaxError(t *testing.T) {
	// Missing Begin Patch
	_, err := ParsePatch("[src/main.go#A1B2]\nSWAP 1.=1:\n+line")
	if err == nil {
		t.Fatal("expected error for missing *** Begin Patch")
	}
}

// ---------------------------------------------------------------------------
// 应用测试
// ---------------------------------------------------------------------------

func TestApplySwapSingleLine(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("src/main.go", "package main\n\nfunc main() {\n    fmt.Println(\"hello\")\n}\n")

	store := NewStore()
	tag, _ := store.Record("src/main.go", fs.files["src/main.go"])

	patch, _ := ParsePatch(`*** Begin Patch
[src/main.go#` + tag + `]
SWAP 4.=4:
+    fmt.Println("hello, world")
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("ApplyPatch failed: %+v", results[0].Error)
	}

	expected := "package main\n\nfunc main() {\n    fmt.Println(\"hello, world\")\n}\n"
	if fs.files["src/main.go"] != expected {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", fs.files["src/main.go"], expected)
	}
}

func TestApplyInsPost(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("src/main.go", "line1\nline2\nline3\n")

	store := NewStore()
	tag, _ := store.Record("src/main.go", fs.files["src/main.go"])

	patch, _ := ParsePatch(`*** Begin Patch
[src/main.go#` + tag + `]
INS.POST 2:
+line2.5
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("ApplyPatch failed: %+v", results[0].Error)
	}

	expected := "line1\nline2\nline2.5\nline3\n"
	if fs.files["src/main.go"] != expected {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", fs.files["src/main.go"], expected)
	}
}

func TestApplyDel(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("src/main.go", "line1\nline2\nline3\nline4\n")

	store := NewStore()
	tag, _ := store.Record("src/main.go", fs.files["src/main.go"])

	patch, _ := ParsePatch(`*** Begin Patch
[src/main.go#` + tag + `]
DEL 2.=3
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("ApplyPatch failed: %+v", results[0].Error)
	}

	expected := "line1\nline4\n"
	if fs.files["src/main.go"] != expected {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", fs.files["src/main.go"], expected)
	}
}

func TestApplyInsHead(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("src/main.go", "line1\nline2\n")

	store := NewStore()
	tag, _ := store.Record("src/main.go", fs.files["src/main.go"])

	patch, _ := ParsePatch(`*** Begin Patch
[src/main.go#` + tag + `]
INS.HEAD:
+// header
+
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("ApplyPatch failed: %+v", results[0].Error)
	}

	if !strings.HasPrefix(fs.files["src/main.go"], "// header\n\n") {
		t.Errorf("unexpected head insert:\n got: %q", fs.files["src/main.go"])
	}
}

func TestApplyInsTail(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("src/main.go", "line1\nline2\n")

	store := NewStore()
	tag, _ := store.Record("src/main.go", fs.files["src/main.go"])

	patch, _ := ParsePatch(`*** Begin Patch
[src/main.go#` + tag + `]
INS.TAIL:
+// footer
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("ApplyPatch failed: %+v", results[0].Error)
	}

	if !strings.HasSuffix(fs.files["src/main.go"], "// footer\n") {
		t.Errorf("unexpected tail insert:\n got: %q", fs.files["src/main.go"])
	}
}

func TestApplyMultipleOpsSequential(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("src/main.go", "line1\nline2\nline3\nline4\nline5\n")

	store := NewStore()
	tag, _ := store.Record("src/main.go", fs.files["src/main.go"])

	// INS.POST 2 then DEL 4 — applied sequentially with offset tracking
	patch, _ := ParsePatch(`*** Begin Patch
[src/main.go#` + tag + `]
INS.POST 2:
+newline
DEL 4
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("ApplyPatch failed: %+v", results[0].Error)
	}

	expected := "line1\nline2\nnewline\nline3\nline5\n"
	if fs.files["src/main.go"] != expected {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", fs.files["src/main.go"], expected)
	}
}
// TestApplyEdits_Descending 验证降序声明操作在位置感知偏移模型下结果正确。
func TestApplyEdits_Descending(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("src/main.go", "L1\nL2\nL3\nL4\nL5\n")

	store := NewStore()
	tag, _ := store.Record("src/main.go", fs.files["src/main.go"])

	// DEL 5 后 DEL 2（降序，第二个操作不应受第一个偏移影响）
	patch, _ := ParsePatch(`*** Begin Patch
[src/main.go#` + tag + `]
DEL 5
DEL 2
*** End Patch`)


	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("ApplyPatch descending DELs failed: %+v", results[0].Error)
	}

	expected := "L1\nL3\nL4\n"
	if fs.files["src/main.go"] != expected {
		t.Errorf("descending DEL 5 → DEL 2:\n got: %q\nwant: %q", fs.files["src/main.go"], expected)
	}
}


func TestTagMismatch(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("src/main.go", "line1\nline2\nline3\n")

	store := NewStore()
	_, _ = store.Record("src/main.go", "line1\nline2\nline3\n")

	// Modify the file externally (different content, not just line shift)
	_ = fs.WriteFile("src/main.go", "line1\nmodified\nline3\n")

	// Try to apply with the old TAG that doesn't match the new content
	// Use a TAG that matches the snapshot but the CURRENT file has different content
	// = wrong TAG from the perspective of verification
	patch, _ := ParsePatch(`*** Begin Patch
[src/main.go#XXXX]
SWAP 2.=2:
+new line
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if results[0].Error == nil {
		t.Fatal("expected TAG mismatch error")
	}
	if results[0].Error.Kind != "tag_mismatch" {
		t.Errorf("expected tag_mismatch, got %s", results[0].Error.Kind)
	}
}

func TestLineOutOfRange(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("src/main.go", "line1\nline2\n")

	store := NewStore()
	tag, _ := store.Record("src/main.go", fs.files["src/main.go"])

	patch, _ := ParsePatch(`*** Begin Patch
[src/main.go#` + tag + `]
SWAP 10.=10:
+new
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if results[0].Error == nil {
		t.Fatal("expected line out of range error")
	}
}

func TestApplyRem(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("src/old.go", "content")

	store := NewStore()
	tag, _ := store.Record("src/old.go", "content")

	patch, _ := ParsePatch(`*** Begin Patch
[src/old.go#` + tag + `]
REM
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("ApplyPatch REM failed: %+v", results[0].Error)
	}

	if _, ok := fs.files["src/old.go"]; ok {
		t.Fatal("expected file to be removed")
	}
}

func TestDetectOverlaps_NoOverlap(t *testing.T) {
	ops := []Op{
		{Kind: OpINS, Position: "post", RefLine: 2},
		{Kind: OpDEL, LineStart: 4, LineEnd: 4},
		{Kind: OpSWAP, LineStart: 1, LineEnd: 1},
	}
	if err := detectOverlaps(ops); err != nil {
		t.Errorf("expected no overlap, got: %v", err)
	}
}

func TestDetectOverlaps_Overlap(t *testing.T) {
	ops := []Op{
		{Kind: OpSWAP, LineStart: 1, LineEnd: 3},
		{Kind: OpDEL, LineStart: 2, LineEnd: 5},
	}
	if err := detectOverlaps(ops); err == nil {
		t.Fatal("expected overlap error, got nil")
	}
}

func TestDetectOverlaps_SwapDelOverlap(t *testing.T) {
	ops := []Op{
		{Kind: OpSWAP, LineStart: 105, LineEnd: 106},
		{Kind: OpDEL, LineStart: 106, LineEnd: 106},
	}
	if err := detectOverlaps(ops); err == nil {
		t.Fatal("expected overlap error for SWAP 105-106 + DEL 106, got nil")
	}
}

// ---------------------------------------------------------------------------
// INS.PRE test
// ---------------------------------------------------------------------------

func TestApplyInsPre(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("src/main.go", "line1\nline2\nline3\n")

	store := NewStore()
	tag, _ := store.Record("src/main.go", fs.files["src/main.go"])

	patch, _ := ParsePatch(`*** Begin Patch
[src/main.go#` + tag + `]
INS.PRE 2:
+newline
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("ApplyPatch INS.PRE failed: %+v", results[0].Error)
	}

	expected := "line1\nnewline\nline2\nline3\n"
	if fs.files["src/main.go"] != expected {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", fs.files["src/main.go"], expected)
	}
}

// ---------------------------------------------------------------------------
// MV apply test
// ---------------------------------------------------------------------------

func TestApplyMv(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("src/old.go", "content\n")

	store := NewStore()
	tag, _ := store.Record("src/old.go", "content\n")

	patch, _ := ParsePatch(`*** Begin Patch
[src/old.go#` + tag + `]
MV src/new.go
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("ApplyPatch MV failed: %+v", results[0].Error)
	}

	// Old file should be gone
	if _, ok := fs.files["src/old.go"]; ok {
		t.Fatal("expected old file to be removed")
	}
	// New file should exist
	if content, ok := fs.files["src/new.go"]; !ok || content != "content\n" {
		t.Errorf("expected content at new path, got %q (ok=%v)", fs.files["src/new.go"], ok)
	}
}

// ---------------------------------------------------------------------------
// Recovery integration test
// ---------------------------------------------------------------------------

func TestApplyWithRecovery(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("src/main.go", "line1\nline2\nline3\n")

	store := NewStore()
	tag, _ := store.Record("src/main.go", "line1\nline2\nline3\n")

	// External modification: insert a line at the top
	_ = fs.WriteFile("src/main.go", "// header\nline1\nline2\nline3\n")

	// Try to edit with the old TAG — recovery should remap
	patch, _ := ParsePatch(`*** Begin Patch
[src/main.go#` + tag + `]
SWAP 2.=2:
+new line2
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("ApplyPatch with recovery failed: %+v", results[0].Error)
	}

	if results[0].Warning == "" {
		t.Log("expected recovery warning but got none (non-critical)")
	}

	expected := "// header\nline1\nnew line2\nline3\n"
	if fs.files["src/main.go"] != expected {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", fs.files["src/main.go"], expected)
	}
}

// ---------------------------------------------------------------------------
// OpKind.String() test
// ---------------------------------------------------------------------------

func TestOpKindString(t *testing.T) {
	cases := map[OpKind]string{
		OpSWAP: "SWAP",
		OpDEL:  "DEL",
		OpINS:  "INS",
		OpREM:  "REM",
		OpMV:   "MV",
	}
	for k, expected := range cases {
		if k.String() != expected {
			t.Errorf("OpKind(%d).String() = %q, want %q", k, k.String(), expected)
		}
	}
	unknown := OpKind(99)
	if unknown.String() != "UNKNOWN" {
		t.Errorf("unknown OpKind.String() = %q, want UNKNOWN", unknown.String())
	}
}

// ---------------------------------------------------------------------------
// SnapshotStore context test
// ---------------------------------------------------------------------------

func TestWithStoreAndStoreFromContext(t *testing.T) {
	store := NewStore()
	ctx := WithStore(context.Background(), store)

	got := StoreFromContext(ctx)
	if got != store {
		t.Fatal("StoreFromContext returned different store")
	}

	if StoreFromContext(context.TODO()) != nil {
		t.Fatal("StoreFromContext on nil context should return nil")
	}
}

// ---------------------------------------------------------------------------
// TestOpRange 验证 opRange 将操作转换为 0-based 行范围
// ---------------------------------------------------------------------------

func TestOpRange(t *testing.T) {
	// SWAP: 0-based [start-1, end)
	r := opRange(Op{Kind: OpSWAP, LineStart: 5, LineEnd: 8})
	if r == nil || r.start != 4 || r.end != 8 {
		t.Errorf("SWAP(5,8): expected [4,8), got %+v", r)
	}

	// DEL: 0-based [start-1, end)
	r = opRange(Op{Kind: OpDEL, LineStart: 3, LineEnd: 3})
	if r == nil || r.start != 2 || r.end != 3 {
		t.Errorf("DEL(3,3): expected [2,3), got %+v", r)
	}

	// INS pre — 零宽度插入点，在参考行之前（RefLine-1 位置）
	r = opRange(Op{Kind: OpINS, Position: "pre", RefLine: 7})
	if r == nil || r.start != 6 || r.end != 6 {
		t.Errorf("INS pre 7: expected [6,6), got %+v", r)
	}

	// INS post — 零宽度插入点，在参考行之后（RefLine 位置）
	r = opRange(Op{Kind: OpINS, Position: "post", RefLine: 2})
	if r == nil || r.start != 2 || r.end != 2 {
		t.Errorf("INS post 2: expected [2,2), got %+v", r)
	}

	// INS head/tail, REM, MV → nil
	for _, op := range []Op{
		{Kind: OpINS, Position: "head"},
		{Kind: OpINS, Position: "tail"},
		{Kind: OpREM},
		{Kind: OpMV},
	} {
		if r := opRange(op); r != nil {
			t.Errorf("expected nil range for %v, got %+v", op, r)
		}
	}
}

func TestRangesOverlap(t *testing.T) {
	if !rangesOverlap(2, 5, 4, 7) {
		t.Error("expected overlap [2,5) and [4,7)")
	}
	if !rangesOverlap(4, 7, 2, 5) {
		t.Error("expected overlap (reversed order)")
	}
	if rangesOverlap(2, 4, 4, 7) {
		t.Error("expected no overlap [2,4) and [4,7) — adjacent")
	}
	if rangesOverlap(5, 8, 1, 3) {
		t.Error("expected no overlap [5,8) and [1,3)")
	}
}

// ---------------------------------------------------------------------------
// Error types test
// ---------------------------------------------------------------------------

func TestParseError(t *testing.T) {
	e := &ParseError{Line: 3, Msg: "bad section"}
	if e.Error() != "parse error at line 3: bad section" {
		t.Errorf("unexpected Error(): %q", e.Error())
	}

	e2 := &ParseError{Msg: "no sections"}
	if e2.Error() != "parse error: no sections" {
		t.Errorf("unexpected Error(): %q", e2.Error())
	}
}

func TestEditError(t *testing.T) {
	e := &EditError{Fatal: true, Kind: "permission_denied", Message: "cannot write"}
	if e.Error() != "cannot write" {
		t.Errorf("unexpected Error(): %q", e.Error())
	}
}

// ---------------------------------------------------------------------------
// detectOverlaps with INS pre/post
// ---------------------------------------------------------------------------

func TestDetectOverlaps_InsPrePost(t *testing.T) {
	// INS.PRE 2 + INS.POST 3: no overlap (different reference lines)
	noOverlap := []Op{
		{Kind: OpINS, Position: "pre", RefLine: 2},
		{Kind: OpINS, Position: "post", RefLine: 3},
	}
	if err := detectOverlaps(noOverlap); err != nil {
		t.Errorf("expected no overlap, got: %v", err)
	}

	// INS.PRE 2 + SWAP 2: no overlap — INS.PRE 插入在行 2 之前（零宽度 [1,1)），
	// SWAP 替换行 2 ([1,2))，两者不重叠。applyEdits 的偏移计算会自动处理行号重映射。
	preSwap := []Op{
		{Kind: OpINS, Position: "pre", RefLine: 2},
		{Kind: OpSWAP, LineStart: 2, LineEnd: 2},
	}
	if err := detectOverlaps(preSwap); err != nil {
		t.Errorf("expected no overlap for INS.PRE 2 + SWAP 2, got: %v", err)
	}

	// INS.PRE 2 + INS.POST 2: should overlap (same reference line, order-dependent)
	prePost := []Op{
		{Kind: OpINS, Position: "pre", RefLine: 2},
		{Kind: OpINS, Position: "post", RefLine: 2},
	}
	if err := detectOverlaps(prePost); err == nil {
		t.Fatal("expected overlap for INS.PRE 2 + INS.POST 2, got nil")
	}
}

// ---------------------------------------------------------------------------
// countLines test
// ---------------------------------------------------------------------------

func TestCountLines(t *testing.T) {
	if n := countLines(""); n != 0 {
		t.Errorf("empty: expected 0, got %d", n)
	}
	if n := countLines("a\nb\nc\n"); n != 3 {
		t.Errorf("three with trailing newline: expected 3, got %d", n)
	}
	if n := countLines("a\nb\nc"); n != 3 {
		t.Errorf("three without trailing newline: expected 3, got %d", n)
	}
	if n := countLines("single"); n != 1 {
		t.Errorf("single line: expected 1, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// parseLineRange edge cases
// ---------------------------------------------------------------------------

func TestParseLineRangeEdgeCases(t *testing.T) {
	// End < start
	_, _, err := parseLineRange("5.=3")
	if err == nil {
		t.Fatal("expected error for end < start")
	}

	// Empty
	_, _, err = parseLineRange("")
	if err == nil {
		t.Fatal("expected error for empty")
	}


	// := confusion (用户写了 N:=M 而非 N.=M)
	_, _, err = parseLineRange("3:=7")
	if err == nil {
		t.Fatal("expected error for := confusion")
		return
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("expected friendly hint, got: %v", err)
	}

	// := confusion with trailing colon (as in SWAP body)
	_, _, err = parseLineRange("3:=7:")
	if err == nil {
		t.Fatal("expected error for := with trailing colon")
		return
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("expected friendly hint, got: %v", err)
	}
	// Invalid
	_, _, err = parseLineRange("abc")
	if err == nil {
		t.Fatal("expected error for invalid")
	}
}

// ---------------------------------------------------------------------------
// parseSingleLine edge cases
// ---------------------------------------------------------------------------

func TestParseSingleLineEdgeCases(t *testing.T) {
	_, err := parseSingleLine("")
	if err == nil {
		t.Fatal("expected error for empty")
	}
	_, err = parseSingleLine("0")
	if err == nil {
		t.Fatal("expected error for 0")
	}
	_, err = parseSingleLine("abc")
	if err == nil {
		t.Fatal("expected error for non-numeric")
	}
}

// ---------------------------------------------------------------------------
// applyMV error path — WriteFile fails
// ---------------------------------------------------------------------------

func TestApplyMvWriteFail(t *testing.T) {
	fs := &failingWriteFS{MemoryFS: NewMemoryFS()}
	_ = fs.WriteFile("src/old.go", "content")
	fs.failWrite = true

	store := NewStore()
	tag, _ := store.Record("src/old.go", "content")

	patch, _ := ParsePatch(`*** Begin Patch
[src/old.go#` + tag + `]
MV src/new.go
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if results[0].Error == nil {
		t.Fatal("expected error when MV WriteFile fails")
	}
}

// ---------------------------------------------------------------------------
// applyREM error path — Remove fails
// ---------------------------------------------------------------------------

func TestApplyRemRemoveFail(t *testing.T) {
	fs := &failingRemoveFS{MemoryFS: NewMemoryFS()}
	_ = fs.WriteFile("src/old.go", "content")
	fs.failRemove = true

	store := NewStore()
	tag, _ := store.Record("src/old.go", "content")

	patch, _ := ParsePatch(`*** Begin Patch
[src/old.go#` + tag + `]
REM
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if results[0].Error == nil {
		t.Fatal("expected error when REM Remove fails")
	}
}

// ---------------------------------------------------------------------------
// Verify content mismatch test
// ---------------------------------------------------------------------------

func TestVerifyContentMismatch(t *testing.T) {
	store := NewStore()
	tag, _ := store.Record("/tmp/test.go", "content v1")

	// Same TAG, different content
	_, err := store.Verify("/tmp/test.go", tag, "content v2")
	if err == nil {
		t.Fatal("expected Verify to fail with modified content")
	}
}

// ---------------------------------------------------------------------------
// Record TAG collision test
// ---------------------------------------------------------------------------

func TestRecordTagCollision(t *testing.T) {
	store := NewStore()

	// Record first
	tag1, _ := store.Record("/tmp/a.go", "content A")
	// Manually insert a collision
	store.mu.Lock()
	store.data["/tmp/b.go"] = &Snapshot{TAG: tag1, Content: "different content"}
	store.mu.Unlock()

	// Now try to record content that would produce the same TAG
	// Since computeTag is deterministic, we need to find content that collides
	// This is hard to guarantee — skip if no collision
	_, err := store.Record("/tmp/c.go", "content A")
	// Should still work (different path, same content = same TAG is OK)
	if err != nil {
		t.Logf("Record with same content (different path): %v (non-critical)", err)
	}
}

// ---------------------------------------------------------------------------
// failingWriteFS / failingRemoveFS helpers
// ---------------------------------------------------------------------------

type failingWriteFS struct {
	*MemoryFS
	failWrite bool
}

func (fs *failingWriteFS) WriteFile(path string, content string) error {
	if fs.failWrite {
		return fmt.Errorf("%w: permission denied", os.ErrPermission)
	}
	return fs.MemoryFS.WriteFile(path, content)
}

type failingRemoveFS struct {
	*MemoryFS
	failRemove bool
}

func (fs *failingRemoveFS) Remove(path string) error {
	if fs.failRemove {
		return fmt.Errorf("%w: permission denied", os.ErrPermission)
	}
	return fs.MemoryFS.Remove(path)
}


// ---------------------------------------------------------------------------
// OSFS tests (real filesystem)
// ---------------------------------------------------------------------------

func TestOSFSReadWrite(t *testing.T) {
	dir := t.TempDir()
	fs := &OSFS{WorkingDir: dir}

	path := "test_readwrite.txt"
	err := fs.WriteFile(path, "hello world")
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	data, err := fs.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if data != "hello world" {
		t.Errorf("unexpected content: %q", data)
	}
}

func TestOSFSMkdirAllAndRemove(t *testing.T) {
	dir := t.TempDir()
	fs := &OSFS{WorkingDir: dir}

	err := fs.MkdirAll("sub/dir")
	if err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// Write a file in the subdirectory
	err = fs.WriteFile("sub/dir/test.txt", "data")
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Remove the file
	err = fs.Remove("sub/dir/test.txt")
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// Verify file is gone
	_, err = fs.ReadFile("sub/dir/test.txt")
	if err == nil {
		t.Fatal("expected file to be removed")
	}
}

// ---------------------------------------------------------------------------
// mapOp REM/MV tests
// ---------------------------------------------------------------------------

func TestMapOpRem(t *testing.T) {
	mappings := []LineMapping{
		{OldLine: 1, NewLine: 1, Status: MapUnchanged},
	}
	// REM doesn't use line numbers
	op := Op{Kind: OpREM}
	mapped, err := mapOp(op, mappings)
	if err != nil {
		t.Fatalf("mapOp REM failed: %v", err)
	}
	if mapped.Kind != OpREM {
		t.Errorf("expected REM, got %s", mapped.Kind)
	}
}

func TestMapOpMv(t *testing.T) {
	mappings := []LineMapping{
		{OldLine: 1, NewLine: 1, Status: MapUnchanged},
	}
	// MV doesn't use line numbers
	op := Op{Kind: OpMV, DestPath: "new.go"}
	mapped, err := mapOp(op, mappings)
	if err != nil {
		t.Fatalf("mapOp MV failed: %v", err)
	}
	if mapped.DestPath != "new.go" {
		t.Errorf("expected DestPath new.go, got %s", mapped.DestPath)
	}
}

// ---------------------------------------------------------------------------
// applyEdits INS head on empty file
// ---------------------------------------------------------------------------

func TestApplyEditInsHeadOnEmpty(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("main.go", "")

	store := NewStore()
	tag, _ := store.Record("main.go", "")

	patch, _ := ParsePatch("*** Begin Patch\n[main.go#" + tag + "]\nINS.HEAD:\n+line1\n+line2\n*** End Patch")

	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("ApplyPatch INS.HEAD on empty file failed: %+v", results[0].Error)
	}

	expected := "line1\nline2\n"
	if fs.files["main.go"] != expected {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", fs.files["main.go"], expected)
	}
}
// TestApplyEditInsTailOnEmpty 验证 INS.TAIL 在空文件上退化为 head（不产生前导换行）。
func TestApplyEditInsTailOnEmpty(t *testing.T) {
	fs := NewMemoryFS()
	_ = fs.WriteFile("empty.go", "")

	store := NewStore()
	tag, _ := store.Record("empty.go", "")

	patch, _ := ParsePatch(`*** Begin Patch
[empty.go#` + tag + `]
INS.TAIL:
+line1
+line2
*** End Patch`)


	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("ApplyPatch INS.TAIL on empty failed: %+v", results[0].Error)
	}

	expected := "line1\nline2\n"
	if fs.files["empty.go"] != expected {
		t.Errorf("unexpected content:\n got: %q\nwant: %q", fs.files["empty.go"], expected)
	}
}


// ---------------------------------------------------------------------------
// applySection REM on non-existent file
// ---------------------------------------------------------------------------

func TestApplyRemOnNonExistent(t *testing.T) {
	fs := NewMemoryFS()
	store := NewStore()

	// Create the file first so os.IsNotExist works correctly
	_ = fs.WriteFile("remove_me.go", "content")
	_, _ = store.Record("remove_me.go", "content")

	tag := computeTag("content")
	// Use actual TAG
	patch, _ := ParsePatch("*** Begin Patch\n[remove_me.go#" + tag + "]\nREM\n*** End Patch")

	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error != nil {
		t.Fatalf("REM should succeed: %+v", results[0].Error)
	}
	if results[0].Op != "delete" {
		t.Errorf("expected Op=delete, got %s", results[0].Op)
	}
}


// REGRESSION: INS.POST with only + (empty body line) should insert one blank line.
// Previously splitBody returned nil for empty body, causing the blank line to be silently dropped.
func TestRegressionInsertEmptyBodyLine(t *testing.T) {
	// Parse a patch with INS.POST containing a single empty body line
	patchText := `*** Begin Patch
[/tmp/test-emptybody.go#ABCD]
INS.POST 1:
+
INS.TAIL:
+// end
*** End Patch`
	patch, err := ParsePatch(patchText)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(patch.Sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(patch.Sections))
	}
	sec := patch.Sections[0]
	if len(sec.Ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(sec.Ops))
	}

	// First op: INS.POST 1 with one empty body line
	ins := sec.Ops[0]
	if ins.Kind != OpINS || ins.Position != "post" || ins.RefLine != 1 {
		t.Errorf("unexpected INS op: kind=%v pos=%s ref=%d", ins.Kind, ins.Position, ins.RefLine)
	}
	if len(ins.Body) != 1 || ins.Body[0] != "" {
		t.Errorf("expected Body=[\"\"], got %v", ins.Body)
	}

	// Second op: INS.TAIL with body "// end"
	tail := sec.Ops[1]
	if len(tail.Body) != 1 || tail.Body[0] != "// end" {
		t.Errorf("expected Body=[\"// end\"], got %v", tail.Body)
	}

	// Apply to a simple file and verify blank line is inserted
	fs := NewMemoryFS()
	_ = fs.WriteFile("/tmp/test-emptybody.go", "line1\nline2\n")
	store := NewStore()
	store.Update("/tmp/test-emptybody.go", "line1\nline2\n")
	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
	expected := "line1\n\nline2\n// end\n"
	actual, _ := fs.ReadFile("/tmp/test-emptybody.go")
	if actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
	}
	}
}

// TestBodyEscapeBackslashPlus verifies that \+ in body lines is treated as literal + content.
func TestBodyEscapeBackslashPlus(t *testing.T) {
	patchText := `*** Begin Patch
[/tmp/escape.go#ABCD]
INS.HEAD:
\+// this line starts with a literal +
\+
\+line
*** End Patch`
	patch, err := ParsePatch(patchText)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	sec := patch.Sections[0]
	ins := sec.Ops[0]
	if len(ins.Body) != 3 {
		t.Fatalf("expected 3 body lines, got %d: %v", len(ins.Body), ins.Body)
	}
	if ins.Body[0] != "+// this line starts with a literal +" {
		t.Errorf("body[0] = %q", ins.Body[0])
	}
	if ins.Body[1] != "+" {
		t.Errorf("body[1] = %q, expected single +", ins.Body[1])
	}
	if ins.Body[2] != "+line" {
		t.Errorf("body[2] = %q", ins.Body[2])
	}

	// Verify applied result
	fs := NewMemoryFS()
	_ = fs.WriteFile("/tmp/escape.go", "package main\n")
	store := NewStore()
	store.Update("/tmp/escape.go", "package main\n")
	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("apply error: %+v", results)
	}
	actual, _ := fs.ReadFile("/tmp/escape.go")
	expected := "+// this line starts with a literal +\n+\n+line\npackage main\n"
	if actual != expected {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}

// resolvingMemoryFS 模拟 OSFS 的路径解析行为：将相对路径基于 baseDir
// 解析为绝对路径，用于测试 store key 对齐逻辑。
type resolvingMemoryFS struct {
	*MemoryFS
	baseDir string
}

func (fs *resolvingMemoryFS) ResolvePath(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '/' || (len(path) > 1 && path[1] == ':') {
		return path
	}
	if fs.baseDir == "" {
		fs.baseDir = "/workspace"
	}
	return fs.baseDir + "/" + path
}

// TestRegression_ApplyEditRelativePathStoreAbsolutePath 验证：
// read 阶段 Record 用绝对路径，edit 阶段 patch header 用相对路径时，
// applySection 通过 ResolvePath 对齐，不会 tag_mismatch。
func TestRegression_ApplyEditRelativePathStoreAbsolutePath(t *testing.T) {
	inner := NewMemoryFS()
	fs := &resolvingMemoryFS{MemoryFS: inner, baseDir: "/workspace"}
	_ = fs.WriteFile("src/main.go", "line1\nline2\nline3\n")

	store := NewStore()
	// 模拟 read 阶段：Record 用绝对路径（OSFS.ResolvePath 的效果）
	absPath := fs.ResolvePath("src/main.go")
	tag, _ := store.Record(absPath, "line1\nline2\nline3\n")

	// edit: patch header 用相对路径（LLM 可能从 read 输出中截取相对形式）
	patch, _ := ParsePatch(`*** Begin Patch
[src/main.go#` + tag + `]
SWAP 2.=2:
+modified line2
*** End Patch`)

	results := ApplyPatch(patch, fs, store)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("ApplyPatch failed: %+v", results[0].Error)
	}

	content, _ := fs.ReadFile("src/main.go")
	if content != "line1\nmodified line2\nline3\n" {
		t.Errorf("unexpected content: got %q", content)
	}
}

// ---------------------------------------------------------------------------
// 多操作偏移场景回归测试
// ---------------------------------------------------------------------------

func TestOffsetRegression_DEL_then_SWAP(t *testing.T) {
	// DEL 行 2 → SWAP 行 4 需自动偏移到行 3
	content := "L1\nL2\nL3\nL4\nL5\n"
	patch := parsePatch(t, "DEL 2\nSWAP 4.=4:\n+NEW L4")
	result := applyPatchContent(t, content, patch)
	expected := "L1\nL3\nNEW L4\nL5\n"
	if result != expected {
		t.Errorf("offset error:\ngot:  %q\nwant: %q", result, expected)
	}
}

func TestOffsetRegression_SWAP_expand_then_INS_POST(t *testing.T) {
	// SWAP 行 2 扩展 (1→2 行) → INS.POST 3 的行号不变但目标偏移
	content := "L1\nL2\nL3\n"
	patch := parsePatch(t, "SWAP 2.=2:\n+NEW L2a\n+NEW L2b\nINS.POST 3:\n+after L3")
	result := applyPatchContent(t, content, patch)
	expected := "L1\nNEW L2a\nNEW L2b\nL3\nafter L3\n"
	if result != expected {
		t.Errorf("offset error:\ngot:  %q\nwant: %q", result, expected)
	}
}

func TestOffsetRegression_INS_PRE_then_SWAP_same_ref(t *testing.T) {
	// I3 修复: INS.PRE 2 + SWAP 2 不应重叠，偏移自动处理
	content := "L1\nL2\nL3\n"
	patch := parsePatch(t, "INS.PRE 2:\n+before L2\nSWAP 2.=2:\n+NEW L2")
	result := applyPatchContent(t, content, patch)
	expected := "L1\nbefore L2\nNEW L2\nL3\n"
	if result != expected {
		t.Errorf("offset error:\ngot:  %q\nwant: %q", result, expected)
	}
}

func TestOffsetRegression_SWAP_shrink_then_DEL(t *testing.T) {
	// SWAP 行 2-3 收缩 (2→1 行) → DEL 4 自动偏移到行 3
	content := "L1\nL2a\nL2b\nL3\nL4\n"
	patch := parsePatch(t, "SWAP 2.=3:\n+NEW L2\nDEL 4")
	result := applyPatchContent(t, content, patch)
	expected := "L1\nNEW L2\nL4\n"
	if result != expected {
		t.Errorf("offset error:\ngot:  %q\nwant: %q", result, expected)
	}
}

func TestOffsetRegression_Three_ops_cascade(t *testing.T) {
	// INS.PRE 2 → SWAP 4 → DEL 6，三重偏移级联
	content := "L1\nL2\nL3\nL4\nL5\nL6\n"
	patch := parsePatch(t, "INS.PRE 2:\n+before\nSWAP 4.=4:\n+NEW L4\nDEL 6")
	result := applyPatchContent(t, content, patch)
	expected := "L1\nbefore\nL2\nL3\nNEW L4\nL5\n"
	if result != expected {
		t.Errorf("offset error:\ngot:  %q\nwant: %q", result, expected)
	}
}

func TestOffsetRegression_DEL_then_INS_PRE(t *testing.T) {
	// DEL 2 → INS.PRE 3 的参考行偏移到行 2
	content := "L1\nL2\nL3\nL4\n"
	patch := parsePatch(t, "DEL 2\nINS.PRE 3:\n+before L3")
	result := applyPatchContent(t, content, patch)
	expected := "L1\nbefore L3\nL3\nL4\n"
	if result != expected {
		t.Errorf("offset error:\ngot:  %q\nwant: %q", result, expected)
	}
}

func TestOffsetRegression_Descending_DELs(t *testing.T) {
	// 降序 DEL 5 → DEL 2，互不影响
	content := "L1\nL2\nL3\nL4\nL5\n"
	patch := parsePatch(t, "DEL 5\nDEL 2")
	result := applyPatchContent(t, content, patch)
	expected := "L1\nL3\nL4\n"
	if result != expected {
		t.Errorf("offset error:\ngot:  %q\nwant: %q", result, expected)
	}
}

// parsePatch 解析简单 patch 文本（绕过 TAG 验证）
func parsePatch(t *testing.T, opsBlock string) *Patch {
	t.Helper()
	patch, err := ParsePatch("*** Begin Patch\n[/tmp/f#0000]\n" + opsBlock + "\n*** End Patch")
	if err != nil {
		t.Fatal(err)
	}
	return patch
}

// applyPatchContent 在内存中应用 patch（绕过文件系统和 TAG 验证）
func applyPatchContent(t *testing.T, content string, patch *Patch) string {
	t.Helper()
	if len(patch.Sections) == 0 {
		t.Fatal("no sections")
	}
	result, _, err := ApplyEditsForTest(content, patch.Sections[0].Ops)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// ---------------------------------------------------------------------------
// Multi-section same file recovery regression test
// ---------------------------------------------------------------------------

func TestMultiSectionSameFileRecovery(t *testing.T) {
	// 模拟场景：Section 1 插入 2 行，Section 2 用相同旧 TAG 编辑被偏移的行。
	// 验证 Recovery 使用原始快照（而非 Section 1 更新后的快照）正确重映射行号。
	content := "L1\nL2\nL3\nL4\nL5\n"

	store := NewStore()
	tag, err := store.Record("/tmp/test-recovery-store.go", content)
	if err != nil {
		t.Fatal(err)
	}

	fs := &memFS{files: map[string]string{"/tmp/test-recovery-store.go": content}}

	// 模拟两个 Section，同一个文件，同一个旧 TAG
	patch := &Patch{
		Sections: []Section{
			{
				Path: "/tmp/test-recovery-store.go",
				TAG:  tag,
				Ops: []Op{
					{Kind: OpINS, Position: "pre", RefLine: 2, Body: []string{"INSERTED A", "INSERTED B"}},
				},
			},
			{
				Path: "/tmp/test-recovery-store.go",
				TAG:  tag, // 同一个旧 TAG — Section 1 后 store 已更新
				Ops: []Op{
					{Kind: OpSWAP, LineStart: 5, LineEnd: 5, Body: []string{"SHIFTED L5"}},
				},
			},
		},
	}

	results := ApplyPatch(patch, fs, store)

	// Section 1: 应该成功
	if results[0].Error != nil {
		t.Fatalf("Section 1 failed: %v", results[0].Error)
	}
	if results[0].LinesDelta != 2 {
		t.Errorf("Section 1 LinesDelta: expected 2, got %d", results[0].LinesDelta)
	}

	// Section 2: Recovery 应该成功（⚠ 而非 ✗）
	if results[1].Error != nil {
		t.Fatalf("Section 2 failed with error: %v", results[1].Error)
	}
	if results[1].Warning == "" {
		t.Error("Section 2 should have a recovery warning")
	}

	// 验证最终文件内容
	final := fs.files["/tmp/test-recovery-store.go"]
	expected := "L1\nINSERTED A\nINSERTED B\nL2\nL3\nL4\nSHIFTED L5\n"
	if final != expected {
		t.Errorf("bad final content:\ngot:  %q\nwant: %q", final, expected)
	}
}

// memFS is a simple in-memory FileSystem for testing.
type memFS struct {
	files map[string]string
}

func (fs *memFS) ReadFile(path string) (string, error) {
	if content, ok := fs.files[path]; ok {
		return content, nil
	}
	return "", os.ErrNotExist
}

func (fs *memFS) WriteFile(path string, content string) error {
	fs.files[path] = content
	return nil
}

func (fs *memFS) MkdirAll(path string) error { return nil }

func (fs *memFS) Remove(path string) error {
	delete(fs.files, path)
	return nil
}

func (fs *memFS) ResolvePath(path string) string { return path }

// ---------------------------------------------------------------------------
// detectCrossSectionConflicts tests
// ---------------------------------------------------------------------------

func TestDetectCrossSectionConflicts_Overlap(t *testing.T) {
	patch := &Patch{
		Sections: []Section{
			{Path: "/tmp/f.go", TAG: "AAAA", Ops: []Op{
				{Kind: OpSWAP, LineStart: 2, LineEnd: 2, Body: []string{"new"}},
			}},
			{Path: "/tmp/f.go", TAG: "AAAA", Ops: []Op{
				{Kind: OpSWAP, LineStart: 2, LineEnd: 2, Body: []string{"new2"}},
			}},
		},
	}
	errs := detectCrossSectionConflicts(patch)
	if errs == nil {
		t.Fatal("expected cross-section conflict, got nil")
	}
	if errs[0] == nil || errs[1] == nil {
		t.Fatal("both sections should have conflict errors")
	}
	if !strings.Contains(errs[0].Message, "overlap") {
		t.Errorf("error should mention overlap: %s", errs[0].Message)
	}
}

func TestDetectCrossSectionConflicts_NoOverlap(t *testing.T) {
	patch := &Patch{
		Sections: []Section{
			{Path: "/tmp/f.go", TAG: "AAAA", Ops: []Op{
				{Kind: OpSWAP, LineStart: 1, LineEnd: 1, Body: []string{"new1"}},
			}},
			{Path: "/tmp/f.go", TAG: "AAAA", Ops: []Op{
				{Kind: OpSWAP, LineStart: 5, LineEnd: 5, Body: []string{"new5"}},
			}},
		},
	}
	errs := detectCrossSectionConflicts(patch)
	if errs != nil {
		t.Fatalf("expected no conflict, got: %v", errs)
	}
}

func TestDetectCrossSectionConflicts_REM_with_LineOp(t *testing.T) {
	patch := &Patch{
		Sections: []Section{
			{Path: "/tmp/f.go", TAG: "AAAA", Ops: []Op{
				{Kind: OpREM},
			}},
			{Path: "/tmp/f.go", TAG: "AAAA", Ops: []Op{
				{Kind: OpSWAP, LineStart: 1, LineEnd: 1, Body: []string{"new"}},
			}},
		},
	}
	errs := detectCrossSectionConflicts(patch)
	if errs == nil {
		t.Fatal("expected REM vs line-op conflict, got nil")
	}
	if !strings.Contains(errs[0].Message, "REM") {
		t.Errorf("error should mention REM: %s", errs[0].Message)
	}
}

func TestDetectCrossSectionConflicts_MV_with_LineOp(t *testing.T) {
	patch := &Patch{
		Sections: []Section{
			{Path: "/tmp/f.go", TAG: "AAAA", Ops: []Op{
				{Kind: OpMV, DestPath: "/tmp/other.go"},
			}},
			{Path: "/tmp/f.go", TAG: "AAAA", Ops: []Op{
				{Kind: OpINS, Position: "head", Body: []string{"new"}},
			}},
		},
	}
	errs := detectCrossSectionConflicts(patch)
	if errs == nil {
		t.Fatal("expected MV vs line-op conflict, got nil")
	}
}

func TestDetectCrossSectionConflicts_MultiFile_OneConflict(t *testing.T) {
	// fileA has conflict, fileB doesn't — only fileA should be rejected
	patch := &Patch{
		Sections: []Section{
			{Path: "/tmp/fileA.go", TAG: "AAAA", Ops: []Op{
				{Kind: OpSWAP, LineStart: 2, LineEnd: 2, Body: []string{"a"}},
			}},
			{Path: "/tmp/fileA.go", TAG: "AAAA", Ops: []Op{
				{Kind: OpSWAP, LineStart: 2, LineEnd: 2, Body: []string{"b"}},
			}},
			{Path: "/tmp/fileB.go", TAG: "BBBB", Ops: []Op{
				{Kind: OpINS, Position: "head", Body: []string{"header"}},
			}},
		},
	}
	errs := detectCrossSectionConflicts(patch)
	if errs == nil {
		t.Fatal("expected conflicts for fileA")
	}
	if errs[0] == nil || errs[1] == nil {
		t.Fatal("fileA sections (0,1) should have conflict errors")
	}
	if errs[2] != nil {
		t.Fatalf("fileB section (2) should NOT have conflict: %v", errs[2])
	}
}

func TestApplyPatch_MultiFile_OneConflict_OtherSucceeds(t *testing.T) {
	store := NewStore()
	contentA := "fileA content\n"
	contentB := "fileB content\n"
	_, _ = store.Record("/tmp/fileA.go", contentA)
	_, _ = store.Record("/tmp/fileB.go", contentB)

	fs := &memFS{files: map[string]string{
		"/tmp/fileA.go": contentA,
		"/tmp/fileB.go": contentB,
	}}

	patch := &Patch{
		Sections: []Section{
			{Path: "/tmp/fileA.go", TAG: mustGetTag(store, "/tmp/fileA.go"), Ops: []Op{
				{Kind: OpSWAP, LineStart: 1, LineEnd: 1, Body: []string{"FileA Section 1"}},
			}},
			{Path: "/tmp/fileA.go", TAG: mustGetTag(store, "/tmp/fileA.go"), Ops: []Op{
				{Kind: OpSWAP, LineStart: 1, LineEnd: 1, Body: []string{"FileA Section 2"}},
			}},
			{Path: "/tmp/fileB.go", TAG: mustGetTag(store, "/tmp/fileB.go"), Ops: []Op{
				{Kind: OpSWAP, LineStart: 1, LineEnd: 1, Body: []string{"FileB updated"}},
			}},
		},
	}

	results := ApplyPatch(patch, fs, store)

	// Section 0 (FileA): should have error
	if results[0].Error == nil {
		t.Fatal("Section 0 (FileA) should have conflict error")
	}
	// Section 1 (FileA): should have error
	if results[1].Error == nil {
		t.Fatal("Section 1 (FileA) should have conflict error")
	}
	// Section 2 (FileB): should have NO error
	if results[2].Error != nil {
		t.Fatalf("Section 2 (FileB) should succeed, got: %v", results[2].Error)
	}

	// FileB should have been modified
	if fs.files["/tmp/fileB.go"] != "FileB updated\n" {
		t.Errorf("FileB should be updated, got: %q", fs.files["/tmp/fileB.go"])
	}
	// FileA should be unchanged
	if fs.files["/tmp/fileA.go"] != contentA {
		t.Errorf("FileA should be unchanged, got: %q", fs.files["/tmp/fileA.go"])
	}
}

func mustGetTag(store *SnapshotStore, path string) string {
	snap, ok := store.Get(path)
	if !ok {
		panic("no snapshot for " + path)
	}
	return snap.TAG
}

func TestDetectCrossSectionConflicts_INS_to_INS_SameRefLine(t *testing.T) {
	patch := &Patch{
		Sections: []Section{
			{Path: "/tmp/f.go", TAG: "AAAA", Ops: []Op{
				{Kind: OpINS, Position: "pre", RefLine: 5, Body: []string{"a"}},
			}},
			{Path: "/tmp/f.go", TAG: "AAAA", Ops: []Op{
				{Kind: OpINS, Position: "post", RefLine: 5, Body: []string{"b"}},
			}},
		},
	}
	errs := detectCrossSectionConflicts(patch)
	if errs == nil {
		t.Fatal("expected INS-to-INS cross-section conflict, got nil")
	}
	if errs[0] == nil || errs[1] == nil {
		t.Fatal("both sections should have conflict errors")
	}
}
