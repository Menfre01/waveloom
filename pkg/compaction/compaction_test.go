package compaction

import (
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// ---------------------------------------------------------------------------
// compactionDecisionSet: canApply / upsert
// ---------------------------------------------------------------------------

func TestDecisionSet_CanApply(t *testing.T) {
	var ds compactionDecisionSet

	if !ds.canApply(5, "snip") {
		t.Fatal("empty set should allow snip")
	}
	if !ds.canApply(5, "prune") {
		t.Fatal("empty set should allow prune")
	}

	ds.upsert(CompactionDecision{MsgIndex: 5, Action: "snip"})
	if ds.canApply(5, "snip") {
		t.Fatal("existing snip should block another snip")
	}
	if !ds.canApply(5, "prune") {
		t.Fatal("existing snip should allow upgrade to prune")
	}

	ds.upsert(CompactionDecision{MsgIndex: 5, Action: "prune"})
	if ds.canApply(5, "snip") {
		t.Fatal("existing prune should block snip")
	}
	if ds.canApply(5, "prune") {
		t.Fatal("existing prune should block another prune")
	}
}

func TestDecisionSet_Upsert_Ordered(t *testing.T) {
	var ds compactionDecisionSet

	ds.upsert(CompactionDecision{MsgIndex: 10, Action: "snip"})
	ds.upsert(CompactionDecision{MsgIndex: 3, Action: "prune"})
	ds.upsert(CompactionDecision{MsgIndex: 7, Action: "snip"})

	if len(ds) != 3 {
		t.Fatalf("expected 3 decisions, got %d", len(ds))
	}
	if ds[0].MsgIndex != 3 || ds[1].MsgIndex != 7 || ds[2].MsgIndex != 10 {
		t.Fatalf("decisions not sorted: %v", []int{ds[0].MsgIndex, ds[1].MsgIndex, ds[2].MsgIndex})
	}
}

func TestDecisionSet_Upsert_Replace(t *testing.T) {
	var ds compactionDecisionSet

	ds.upsert(CompactionDecision{MsgIndex: 5, Action: "snip", DecisionTier: 1})
	ds.upsert(CompactionDecision{MsgIndex: 5, Action: "prune", DecisionTier: 2})

	if len(ds) != 1 {
		t.Fatalf("expected 1 decision after replace, got %d", len(ds))
	}
	if ds[0].Action != "prune" || ds[0].DecisionTier != 2 {
		t.Fatal("upsert should replace existing entry")
	}
}

func TestNewDecisionSetFromList_Sorted(t *testing.T) {
	list := []CompactionDecision{
		{MsgIndex: 10, Action: "snip"},
		{MsgIndex: 3, Action: "prune"},
		{MsgIndex: 7, Action: "snip"},
	}
	ds := NewDecisionSetFromList(list)
	if len(ds) != 3 {
		t.Fatalf("expected 3, got %d", len(ds))
	}
	for i := 1; i < len(ds); i++ {
		if ds[i-1].MsgIndex >= ds[i].MsgIndex {
			t.Fatalf("not sorted at index %d: %d >= %d", i, ds[i-1].MsgIndex, ds[i].MsgIndex)
		}
	}
}

func TestDecisionSetToList_RoundTrip(t *testing.T) {
	ds := compactionDecisionSet{
		{MsgIndex: 1, Action: "snip"},
		{MsgIndex: 2, Action: "prune"},
	}
	list := DecisionSetToList(ds)
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
	ds2 := NewDecisionSetFromList(list)
	if len(ds2) != 2 {
		t.Fatal("round-trip failed")
	}
}

// ---------------------------------------------------------------------------
// truncateByStrategy
// ---------------------------------------------------------------------------

func TestTruncateByStrategy_ShortContent(t *testing.T) {
	s := truncationStrategy{maxLines: 100, headLines: 50, tailLines: 10, maxLineChars: 2000, maxTotalChars: 20000}
	content := "line1\nline2\nline3"
	result, did := truncateByStrategy(content, s)
	if did {
		t.Fatal("should not truncate short content")
	}
	if result != content {
		t.Fatalf("content changed: %q", result)
	}
}

func TestTruncateByStrategy_LongContent(t *testing.T) {
	s := truncationStrategy{maxLines: 60, headLines: 20, tailLines: 30, maxLineChars: 2000, maxTotalChars: 20000}
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = "line"
	}
	content := strings.Join(lines, "\n")
	result, did := truncateByStrategy(content, s)
	if !did {
		t.Fatal("should truncate long content")
	}
	if !strings.Contains(result, "省略") {
		t.Fatalf("expected omission marker, got: %s", result)
	}
}

func TestTruncateByStrategy_SingleLongLine(t *testing.T) {
	// 单行内容未超过行数限制，但单行字符数超限 → 应触发行截断
	s := truncationStrategy{maxLines: 200, headLines: 150, tailLines: 10, maxLineChars: 100, maxTotalChars: 0}
	content := strings.Repeat("x", 5000)
	result, did := truncateByStrategy(content, s)
	if !did {
		t.Fatal("should truncate single long line")
	}
	if !strings.Contains(result, "行截断") {
		t.Fatalf("expected line truncation marker, got: %s", result[:200])
	}
	if len(result) >= len(content) {
		t.Fatal("result should be shorter than original")
	}
}

func TestTruncateByStrategy_TotalChars(t *testing.T) {
	// 多行内容，行数和单行长度均未超限，但总字符数超限 → 应触发总字符截断
	s := truncationStrategy{maxLines: 200, headLines: 150, tailLines: 10, maxLineChars: 2000, maxTotalChars: 500}
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = strings.Repeat("x", 100)
	}
	content := strings.Join(lines, "\n")
	result, did := truncateByStrategy(content, s)
	if !did {
		t.Fatal("should truncate by total chars")
	}
	if !strings.Contains(result, "内容截断") {
		t.Fatalf("expected total truncation marker, got: %s", result)
	}
	if len(result) >= len(content) {
		t.Fatal("result should be shorter than original")
	}
}

func TestTruncateByStrategy_TotalCharsAtNewline(t *testing.T) {
	// 总字符截断应在换行边界处切断
	s := truncationStrategy{maxLines: 200, headLines: 150, tailLines: 10, maxLineChars: 2000, maxTotalChars: 500}
	// 构造内容使 cutPoint 落在行中间
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = strings.Repeat("x", 60) // 每行 61 字符（含 \n）= 1220 总
	}
	content := strings.Join(lines, "\n")
	result, did := truncateByStrategy(content, s)
	if !did {
		t.Fatal("should truncate")
	}
	// 截断点应在换行处
	truncatedPart := result[:strings.Index(result, "[... 内容截断")]
	if strings.Count(truncatedPart, "\n") == 0 {
		t.Fatal("expected truncation at newline boundary")
	}
}

func TestTruncateByStrategy_Empty(t *testing.T) {
	s := truncationStrategy{maxLines: 10, headLines: 5, tailLines: 2, maxLineChars: 100, maxTotalChars: 500}
	result, did := truncateByStrategy("", s)
	if did {
		t.Fatal("should not truncate empty content")
	}
	if result != "" {
		t.Fatalf("expected empty, got %q", result)
	}
}

func TestTruncateByStrategy_NoTail(t *testing.T) {
	s := truncationStrategy{maxLines: 60, headLines: 50, tailLines: 0, maxLineChars: 2000, maxTotalChars: 20000}
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "line"
	}
	content := strings.Join(lines, "\n")
	result, did := truncateByStrategy(content, s)
	if !did {
		t.Fatal("should truncate")
	}
	if strings.Contains(result, "完整结果") {
		t.Fatal("tail=0 should not include '完整结果' text")
	}
}

func TestTruncateByStrategy_MixedLongAndManyLines(t *testing.T) {
	// 行数超限 + 某行超长 → 行数截断优先（更语义化）
	s := truncationStrategy{maxLines: 60, headLines: 20, tailLines: 30, maxLineChars: 50, maxTotalChars: 50000}
	lines := make([]string, 200)
	for i := range lines {
		if i == 10 {
			lines[i] = strings.Repeat("LONG", 500) // 2000 字符，远超 maxLineChars
		} else {
			lines[i] = "normal line"
		}
	}
	content := strings.Join(lines, "\n")
	result, did := truncateByStrategy(content, s)
	if !did {
		t.Fatal("should truncate")
	}
	// 行数截断优先，应包含 "省略" 而非 "行截断"
	if !strings.Contains(result, "省略") {
		t.Fatalf("line-count truncation should take priority: %s", result[:300])
	}
}

// ---------------------------------------------------------------------------
// formatToolPlaceholder
// ---------------------------------------------------------------------------

func TestFormatToolPlaceholder(t *testing.T) {
	content := strings.Repeat("result line\n", 100)
	result := formatToolPlaceholder("read_file", content)
	if !strings.Contains(result, "read_file") {
		t.Fatalf("placeholder should mention tool name: %s", result)
	}
	if !strings.Contains(result, "compressed") {
		t.Fatalf("placeholder should indicate compression: %s", result)
	}
}

// ---------------------------------------------------------------------------
// checkHardLimit
// ---------------------------------------------------------------------------

func TestCheckHardLimit(t *testing.T) {
	if reached, _ := checkHardLimit(0.97, 0); reached {
		t.Fatal("should not reach hard limit at 97%")
	}
	if reached, reason := checkHardLimit(0.99, 0); !reached || reason != "usage" {
		t.Fatalf("should reach usage hard limit at 99%%: reached=%v reason=%s", reached, reason)
	}
	if reached, reason := checkHardLimit(0.50, 2); !reached || reason != "tier3_failures" {
		t.Fatalf("should reach tier3_failures hard limit: reached=%v reason=%s", reached, reason)
	}
}

// ---------------------------------------------------------------------------
// findProtectionStartIdx
// ---------------------------------------------------------------------------

func TestFindProtectionStartIdx_Empty(t *testing.T) {
	if idx := findProtectionStartIdx(nil, 8000); idx != 0 {
		t.Fatalf("empty messages should return 0, got %d", idx)
	}
}

func TestFindProtectionStartIdx_ShortMessages(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "short"},
		{Role: llm.RoleUser, Content: "hello"},
	}
	if idx := findProtectionStartIdx(messages, 8000); idx != 0 {
		t.Fatalf("short messages should return 0, got %d", idx)
	}
}

// ---------------------------------------------------------------------------
// estimatedTokensFromContent
// ---------------------------------------------------------------------------

func TestEstimatedTokensFromContent(t *testing.T) {
	if n := estimatedTokensFromContent(""); n != 0 {
		t.Errorf("empty string should be 0, got %d", n)
	}
	if n := estimatedTokensFromContent("hello"); n <= 0 {
		t.Errorf("non-empty should be > 0, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// FormatSummaryPrompt / FormatSummaryUserMessage
// ---------------------------------------------------------------------------

func TestFormatSummaryPrompt_ContainsJSON(t *testing.T) {
	prompt := FormatSummaryPrompt()
	if !strings.Contains(prompt, "json") && !strings.Contains(prompt, "JSON") {
		t.Fatal("prompt should mention JSON for DeepSeek json_object mode requirement")
	}
	if !strings.Contains(prompt, "progress") {
		t.Fatal("prompt should define the output schema")
	}
}

func TestFormatSummaryUserMessage_Empty(t *testing.T) {
	result := FormatSummaryUserMessage(nil, nil)
	if result == "" {
		t.Fatal("empty input should still produce output")
	}
}

func TestFormatSummaryUserMessage_WithExisting(t *testing.T) {
	existing := []string{`{"progress":{"summary":"round 1","files":[]},"pending":[],"pitfalls":[],"constraints":""}`}
	result := FormatSummaryUserMessage(existing, []llm.Message{
		{Role: llm.RoleUser, Content: "new message"},
	})
	if !strings.Contains(result, "已有摘要链") {
		t.Fatal("should include existing summaries section")
	}
	if !strings.Contains(result, "round 1") {
		t.Fatal("should contain existing summary content")
	}
	if !strings.Contains(result, "new message") {
		t.Fatal("should contain delta message")
	}
}

func TestFormatSummaryUserMessage_Truncation(t *testing.T) {
	longContent := strings.Repeat("x", 3000)
	result := FormatSummaryUserMessage(nil, []llm.Message{
		{Role: llm.RoleUser, Content: longContent},
	})
	if strings.Contains(result, longContent) {
		t.Fatal("long content should be truncated")
	}
	if !strings.Contains(result, "已截断") {
		t.Fatal("truncation marker missing")
	}
}
