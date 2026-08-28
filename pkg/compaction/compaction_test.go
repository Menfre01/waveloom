package compaction

import (
	"strings"
	"testing"
	"unicode/utf8"

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

// TestToolTruncationStrategies_BashThreshold 验证放宽后的 bash 策略触发边界:
// 41 行起截(原 61 行),40 行不截。阈值依据历史 session 数据校准
// (原 60 行门槛下 84% 的 bash 结果不触发,Tier 1 近乎空转)。
func TestToolTruncationStrategies_BashThreshold(t *testing.T) {
	strategy := toolTruncationStrategies["bash"]
	if strategy.maxLines == 0 {
		t.Fatal("bash strategy missing")
	}
	mk := func(n int) string {
		lines := make([]string, n)
		for i := range lines {
			lines[i] = "line"
		}
		return strings.Join(lines, "\n")
	}

	// 40 行(≤ max(head+tail+10, maxLines)=40)→ 不截
	if _, did := truncateByStrategy(mk(40), strategy); did {
		t.Error("40 lines should not truncate")
	}
	// 41 行 → 截
	if _, did := truncateByStrategy(mk(41), strategy); !did {
		t.Error("41 lines should truncate (relaxed threshold)")
	}
	// 字符门槛:8001 字符触发总字符截断(单行内容)
	long := strings.Repeat("x", 8001)
	if _, did := truncateByStrategy(long, strategy); !did {
		t.Error("8001 chars should truncate via maxTotalChars")
	}
}

// TestDefaultWatermarkThresholds 验证默认水位线阈值按历史 session 校准:
// 45/65/85(原 60/80/95 在 1M 窗口下几乎不可达——148 个 session 水位峰值
// 最高 64.8%,≥80% 为 0,Tier 2/3 从未在实际工作中触发)。
func TestDefaultWatermarkThresholds(t *testing.T) {
	if DefaultTier1Threshold != 0.45 {
		t.Errorf("DefaultTier1Threshold = %v, want 0.45 (1M 窗口 = 450K)", DefaultTier1Threshold)
	}
	if DefaultTier2Threshold != 0.65 {
		t.Errorf("DefaultTier2Threshold = %v, want 0.65 (1M 窗口 = 650K)", DefaultTier2Threshold)
	}
	if DefaultTier3Threshold != 0.85 {
		t.Errorf("DefaultTier3Threshold = %v, want 0.85 (1M 窗口 = 850K)", DefaultTier3Threshold)
	}
	// 单调性:Tier1 < Tier2 < Tier3
	if !(DefaultTier1Threshold < DefaultTier2Threshold && DefaultTier2Threshold < DefaultTier3Threshold) {
		t.Error("thresholds must be strictly increasing")
	}
}

// TestTruncateRunes_UTF8Boundary 验证单行截断沿 rune 边界,
// 不切坏多字节字符(与 shell truncateOutput 对齐)。
func TestTruncateRunes_UTF8Boundary(t *testing.T) {
	// 中文 "你" = 3 字节;截到 4 字节应保留 1 个完整中文字符(3 字节)
	s := "你好世界"
	got := truncateRunes(s, 4)
	if got != "你" {
		t.Errorf("truncateRunes(%q, 4) = %q, want 你 (完整 rune)", s, got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncated string must be valid UTF-8, got %q", got)
	}
	// 长内容完整截断不超限
	long := strings.Repeat("a", 100) + "好" + strings.Repeat("b", 100)
	got = truncateRunes(long, 150)
	if len(got) > 150 || !utf8.ValidString(got) {
		t.Errorf("truncateRunes len=%d (limit 150) or invalid UTF-8", len(got))
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
	if !strings.Contains(result, "omitted") {
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
	if !strings.Contains(result, "line truncated") {
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
	if !strings.Contains(result, "content truncated") {
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
	truncatedPart := result[:strings.Index(result, "[... content truncated")]
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
	if strings.Contains(result, "full result") {
		t.Fatal("tail=0 should not include 'full result' text")
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
	// 行数截断优先，应包含 "omitted" 而非 "line truncated"
	if !strings.Contains(result, "omitted") {
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

func TestEstimatedTokensFromMessage_ImagesFixedCost(t *testing.T) {
	// 图片按官方单图 token 上限(384)估算,base64 体积不得进入估算
	base := estimatedTokensFromMessage(llm.Message{Role: llm.RoleUser, Content: "look at this"})
	big := strings.Repeat("A", 1_000_000) // ~1MB base64 载荷
	withImg := estimatedTokensFromMessage(llm.Message{
		Role:    llm.RoleUser,
		Content: "look at this",
		Images:  []llm.ImagePart{{MIME: "image/png", B64: big}},
	})
	if withImg != base+imageTokensPerImage {
		t.Errorf("with image = %d, want %d + %d = %d (base64 must not leak into estimate)",
			withImg, base, imageTokensPerImage, base+imageTokensPerImage)
	}
	// 两张图 = 2 × 384
	base2 := estimatedTokensFromMessage(llm.Message{Role: llm.RoleUser})
	two := estimatedTokensFromMessage(llm.Message{
		Role:   llm.RoleUser,
		Images: []llm.ImagePart{{MIME: "image/png", B64: "A"}, {MIME: "image/jpeg", B64: "B"}},
	})
	if two != base2+2*imageTokensPerImage {
		t.Errorf("two images = %d, want %d + %d", two, base2, 2*imageTokensPerImage)
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
	if !strings.Contains(result, "Existing Summary Chain") {
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
	if !strings.Contains(result, "content truncated") {
		t.Fatal("truncation marker missing")
	}
}
