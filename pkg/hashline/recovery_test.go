package hashline

import (
	"fmt"
	"testing"
)

// ---------------------------------------------------------------------------
// Recovery tests
// ---------------------------------------------------------------------------

func TestRecoverOpsFileHeadInsert(t *testing.T) {
	// 快照: 原始文件
	snapshot := "line1\nline2\nline3\n"
	// 当前: 头部插入了一行
	current := "// header\nline1\nline2\nline3\n"

	ops := []Op{{Kind: OpSWAP, LineStart: 2, LineEnd: 2, Body: []string{"new line2"}}}

	result := RecoverOps(snapshot, current, ops)
	if !result.Success {
		t.Fatalf("RecoverOps failed: %v", result.Warnings)
	}
	if len(result.MappedOps) != 1 {
		t.Fatalf("expected 1 mapped op, got %d", len(result.MappedOps))
	}

	// line2 在快照中是第 2 行，在当前文件中应该变成第 3 行
	mapped := result.MappedOps[0]
	if mapped.LineStart != 3 || mapped.LineEnd != 3 {
		t.Errorf("expected mapped range 3.=3, got %d.=%d", mapped.LineStart, mapped.LineEnd)
	}
}

func TestRecoverOpsFileTailAppend(t *testing.T) {
	// 快照: 原始文件
	snapshot := "line1\nline2\nline3\n"
	// 当前: 尾部追加了一行
	current := "line1\nline2\nline3\nline4\n"

	ops := []Op{{Kind: OpSWAP, LineStart: 2, LineEnd: 2, Body: []string{"new line2"}}}

	result := RecoverOps(snapshot, current, ops)
	if !result.Success {
		t.Fatalf("RecoverOps failed: %v", result.Warnings)
	}

	mapped := result.MappedOps[0]
	// line2 保持不变
	if mapped.LineStart != 2 || mapped.LineEnd != 2 {
		t.Errorf("expected mapped range 2.=2, got %d.=%d", mapped.LineStart, mapped.LineEnd)
	}
}

func TestRecoverOpsLineDeleted(t *testing.T) {
	// 快照: 原始文件
	snapshot := "line1\nline2\nline3\n"
	// 当前: line2 被删除
	current := "line1\nline3\n"

	// 尝试编辑已删除的行
	ops := []Op{{Kind: OpSWAP, LineStart: 2, LineEnd: 2, Body: []string{"new line"}}}

	result := RecoverOps(snapshot, current, ops)
	if result.Success {
		t.Fatal("expected RecoverOps to fail when target line is deleted")
	}
}

func TestRecoverOpsLineModified(t *testing.T) {
	// 快照: 原始文件
	snapshot := "line1\nline2\nline3\n"
	// 当前: line2 被修改
	current := "line1\nmodified line\nline3\n"

	// 尝试编辑被修改的行 → 冲突
	ops := []Op{{Kind: OpSWAP, LineStart: 2, LineEnd: 2, Body: []string{"new line"}}}

	result := RecoverOps(snapshot, current, ops)
	if result.Success {
		t.Fatal("expected RecoverOps to detect conflict when target line is modified")
	}
}

func TestRecoverOpsInsPost(t *testing.T) {
	// 快照: 原始文件
	snapshot := "line1\nline2\nline3\n"
	// 当前: 头部插入
	current := "// header\nline1\nline2\nline3\n"

	ops := []Op{{Kind: OpINS, Position: "post", RefLine: 2, Body: []string{"new line"}}}

	result := RecoverOps(snapshot, current, ops)
	if !result.Success {
		t.Fatalf("RecoverOps failed: %v", result.Warnings)
	}

	mapped := result.MappedOps[0]
	if mapped.RefLine != 3 {
		t.Errorf("expected INS ref line 3 (shifted from 2), got %d", mapped.RefLine)
	}
}

func TestRecoverOpsInsHeadTailUnaffected(t *testing.T) {
	// INS.HEAD 和 INS.TAIL 不依赖行号，应始终成功
	snapshot := "line1\nline2\n"
	current := "// new\nline1\nline2\n// end\n"

	ops := []Op{
		{Kind: OpINS, Position: "head", Body: []string{"header"}},
		{Kind: OpINS, Position: "tail", Body: []string{"footer"}},
	}

	result := RecoverOps(snapshot, current, ops)
	if !result.Success {
		t.Fatalf("RecoverOps failed for head/tail: %v", result.Warnings)
	}
	if len(result.MappedOps) != 2 {
		t.Fatalf("expected 2 mapped ops, got %d", len(result.MappedOps))
	}
}

func TestRecoverOpsDel(t *testing.T) {
	// 快照: 原始文件
	snapshot := "line1\nline2\nline3\nline4\n"
	// 当前: 头部插入一行
	current := "// header\nline1\nline2\nline3\nline4\n"

	ops := []Op{{Kind: OpDEL, LineStart: 2, LineEnd: 3}}

	result := RecoverOps(snapshot, current, ops)
	if !result.Success {
		t.Fatalf("RecoverOps failed: %v", result.Warnings)
	}

	mapped := result.MappedOps[0]
	// line2→3, line3→4
	if mapped.LineStart != 3 || mapped.LineEnd != 4 {
		t.Errorf("expected mapped range 3.=4, got %d.=%d", mapped.LineStart, mapped.LineEnd)
	}
}

func TestRecoverOpsEmptyFile(t *testing.T) {
	snapshot := ""
	current := "new line\n"

	ops := []Op{{Kind: OpINS, Position: "tail", Body: []string{"appended"}}}

	result := RecoverOps(snapshot, current, ops)
	if !result.Success {
		t.Fatalf("RecoverOps failed on empty snapshot: %v", result.Warnings)
	}
}

func TestRecoverOpsInsRefDeleted(t *testing.T) {
	snapshot := "line1\nline2\nline3\n"
	current := "line1\nline3\n" // line2 deleted

	ops := []Op{{Kind: OpINS, Position: "post", RefLine: 2, Body: []string{"new"}}}

	result := RecoverOps(snapshot, current, ops)
	if result.Success {
		t.Fatal("expected failure when INS reference line is deleted")
	}
}

// ---------------------------------------------------------------------------
// LCS tests
// ---------------------------------------------------------------------------

func TestComputeLCSIdentical(t *testing.T) {
	a := []string{"a", "b", "c"}
	b := []string{"a", "b", "c"}

	pairs := computeLCS(a, b)
	if len(pairs) != 3 {
		t.Fatalf("expected 3 LCS pairs, got %d", len(pairs))
	}
	for i, p := range pairs {
		if p.snapIdx != i || p.currIdx != i {
			t.Errorf("pair[%d]: expected (%d,%d), got (%d,%d)", i, i, i, p.snapIdx, p.currIdx)
		}
	}
}

func TestComputeLCSWithInsertion(t *testing.T) {
	a := []string{"a", "b", "c"}
	b := []string{"a", "x", "b", "c"}

	pairs := computeLCS(a, b)
	// a b c should all match
	if len(pairs) != 3 {
		t.Fatalf("expected 3 LCS pairs, got %d", len(pairs))
	}
}

func TestComputeLCSWithDeletion(t *testing.T) {
	a := []string{"a", "b", "c", "d"}
	b := []string{"a", "c", "d"}

	pairs := computeLCS(a, b)
	// a, c, d match; b is deleted
	if len(pairs) != 3 {
		t.Fatalf("expected 3 LCS pairs, got %d", len(pairs))
	}
}

func TestComputeLCSEmpty(t *testing.T) {
	pairs := computeLCS(nil, nil)
	if len(pairs) != 0 {
		t.Fatalf("expected 0 pairs, got %d", len(pairs))
	}
}

// ---------------------------------------------------------------------------
// buildLineMappings tests
// ---------------------------------------------------------------------------

func TestBuildLineMappingsUnchanged(t *testing.T) {
	lcs := []lcsPair{
		{snapIdx: 0, currIdx: 0},
		{snapIdx: 1, currIdx: 1},
		{snapIdx: 2, currIdx: 2},
	}
	mappings := buildLineMappings(lcs, 3, 3)

	for i, m := range mappings {
		if m.Status != MapUnchanged {
			t.Errorf("line %d: expected Unchanged, got %v", i+1, m.Status)
		}
		if m.NewLine != i+1 {
			t.Errorf("line %d: expected NewLine=%d, got %d", i+1, i+1, m.NewLine)
		}
	}
}

func TestBuildLineMappingsShifted(t *testing.T) {
	// 快照: a b c, 当前: x a b c (头部插入导致偏移)
	lcs := []lcsPair{
		{snapIdx: 0, currIdx: 1},
		{snapIdx: 1, currIdx: 2},
		{snapIdx: 2, currIdx: 3},
	}
	mappings := buildLineMappings(lcs, 3, 4)

	for i, m := range mappings {
		if m.Status != MapShifted {
			t.Errorf("line %d: expected Shifted, got %v", i+1, m.Status)
		}
		if m.NewLine != i+2 {
			t.Errorf("line %d: expected NewLine=%d, got %d", i+1, i+2, m.NewLine)
		}
	}
}

func TestBuildLineMappingsModified(t *testing.T) {
	// 快照: a b c, 当前: a modified c (b 被改)
	lcs := []lcsPair{
		{snapIdx: 0, currIdx: 0},
		{snapIdx: 2, currIdx: 2},
	}
	mappings := buildLineMappings(lcs, 3, 3)

	if mappings[0].Status != MapUnchanged {
		t.Errorf("line 1: expected Unchanged, got %v", mappings[0].Status)
	}
	if mappings[1].Status != MapModified {
		t.Errorf("line 2: expected Modified, got %v", mappings[1].Status)
	}
	if mappings[2].Status != MapUnchanged {
		t.Errorf("line 3: expected Unchanged, got %v", mappings[2].Status)
	}
}

// ---------------------------------------------------------------------------
// findMapping tests
// ---------------------------------------------------------------------------

func TestFindMapping(t *testing.T) {
	mappings := []LineMapping{
		{OldLine: 1, NewLine: 1, Status: MapUnchanged},
		{OldLine: 2, NewLine: 3, Status: MapShifted},
		{OldLine: 3, NewLine: 0, Status: MapModified},
	}

	m := findMapping(mappings, 2)
	if m == nil {
		t.Fatal("expected to find mapping for line 2")
	}
	if m.NewLine != 3 {
		t.Errorf("expected NewLine=3, got %d", m.NewLine)
	}

	m = findMapping(mappings, 5)
	if m != nil {
		t.Fatal("expected nil for non-existent line 5")
	}
}

// ---------------------------------------------------------------------------
// mapOp detailed tests
// ---------------------------------------------------------------------------

func TestMapOpSwapUnchanged(t *testing.T) {
	mappings := []LineMapping{
		{OldLine: 1, NewLine: 1, Status: MapUnchanged},
		{OldLine: 2, NewLine: 2, Status: MapUnchanged},
	}

	op := Op{Kind: OpSWAP, LineStart: 2, LineEnd: 2, Body: []string{"new"}}
	mapped, err := mapOp(op, mappings)
	if err != nil {
		t.Fatalf("mapOp failed: %v", err)
	}
	if mapped.LineStart != 2 {
		t.Errorf("expected LineStart=2, got %d", mapped.LineStart)
	}
}

func TestMapOpDelDeleted(t *testing.T) {
	mappings := []LineMapping{
		{OldLine: 1, NewLine: 0, Status: MapDeleted},
	}
	op := Op{Kind: OpDEL, LineStart: 1, LineEnd: 1}
	_, err := mapOp(op, mappings)
	if err == nil {
		t.Fatal("expected error when target line is deleted")
	}
}

func TestMapOpDelRange(t *testing.T) {
	// DEL range (multi-line): both start and end need to be remapped
	mappings := []LineMapping{
		{OldLine: 1, NewLine: 1, Status: MapUnchanged},
		{OldLine: 2, NewLine: 3, Status: MapShifted},
		{OldLine: 3, NewLine: 4, Status: MapShifted},
		{OldLine: 4, NewLine: 5, Status: MapShifted},
	}
	op := Op{Kind: OpDEL, LineStart: 2, LineEnd: 4}
	mapped, err := mapOp(op, mappings)
	if err != nil {
		t.Fatalf("mapOp DEL range failed: %v", err)
	}
	if mapped.LineStart != 3 || mapped.LineEnd != 5 {
		t.Errorf("expected DEL 3.=5, got %d.=%d", mapped.LineStart, mapped.LineEnd)
	}
}

// ---------------------------------------------------------------------------
// computeFastLCS test (large file fast path)
// ---------------------------------------------------------------------------

func TestComputeFastLCS(t *testing.T) {
	// Generate >5000-line files to trigger fast path
	a := make([]string, 5001)
	b := make([]string, 5001)
	for i := range a {
		a[i] = fmt.Sprintf("line-%d", i)
		b[i] = fmt.Sprintf("line-%d", i)
	}
	// Insert an extra line in b
	b = append([]string{"// header"}, b...)

	pairs := computeLCS(a, b)
	// With the extra line in b, all a lines should still match
	// (each line is unique so hash matching works)
	if len(pairs) != 5001 {
		t.Errorf("fast LCS: expected 5001 pairs, got %d", len(pairs))
	}
}
