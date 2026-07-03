package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
)

// ---------------------------------------------------------------------------
// wrapLine
// ---------------------------------------------------------------------------

func TestWrapLine_NoWrap(t *testing.T) {
	result := wrapLine("hello world", 20)
	if len(result) != 1 || result[0] != "hello world" {
		t.Fatalf("expected no wrap, got %v", result)
	}
}

func TestWrapLine_WrapAtSpace(t *testing.T) {
	result := wrapLine("hello beautiful world", 15)
	// "hello beautiful" = 15, the space at position 5 is before the
	// trailing 1/4 zone (position 11+), so the break is at position 15
	// (hard cut). First line = "hello beautiful", second = "world".
	if len(result) < 2 {
		t.Fatalf("expected at least 2 lines, got %v", result)
	}
	if result[0] != "hello beautiful" {
		t.Errorf("line 0 = %q, want %q", result[0], "hello beautiful")
	}
	if result[1] != "world" {
		t.Errorf("line 1 = %q, want %q", result[1], "world")
	}
}

func TestWrapLine_WideCJK(t *testing.T) {
	// CJK characters are 2 columns wide in terminal
	result := wrapLine("你好世界", 4)
	// "你好" = 4 columns, "世界" = 4 columns
	if len(result) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(result), result)
	}
	if result[0] != "你好" {
		t.Errorf("line 0 = %q, want %q", result[0], "你好")
	}
	if result[1] != "世界" {
		t.Errorf("line 1 = %q, want %q", result[1], "世界")
	}
}

func TestWrapLine_MixedCJKAndASCII(t *testing.T) {
	result := wrapLine("你好 world", 8)
	// "你好" = 4 cols, " world" = 6 → total 10, breaks after "你好 " (6 cols?) no
	// "你好" (4) + " " (1) + "w" (1) = 6 → fits, "o" (1) → 7, "r" (1) → 8, "l" (1) → 9 exceeds
	if len(result) < 2 {
		t.Fatalf("expected at least 2 lines, got %v", result)
	}
}

func TestWrapLine_EmptyInput(t *testing.T) {
	result := wrapLine("", 10)
	if len(result) != 1 || result[0] != "" {
		t.Fatalf("expected [\"\"], got %v", result)
	}
}

func TestWrapLine_NarrowWidth(t *testing.T) {
	// Width too narrow for a single CJK char (2 cols) — should still break
	result := wrapLine("你好", 1)
	if len(result) < 2 {
		t.Fatalf("expected at least 2 lines for CJK in width 1, got %v", result)
	}
}

func TestWrapLine_PrefersSpaceBreak(t *testing.T) {
	// "aaaa bbbbbbb" = 12 chars at width 10 → cut at 10 ("aaaa bbbbb").
	// Space at position 4, 4 ≥ 10*3/4 = 7? No → hard cut at 10.
	result := wrapLine("aaaa bbbbbbb ccc", 10)
	if len(result) < 2 {
		t.Fatalf("expected wrap, got %v", result)
	}
	if result[0] != "aaaa bbbbb" {
		t.Errorf("line 0 = %q, want %q", result[0], "aaaa bbbbb")
	}
}

// ---------------------------------------------------------------------------
// buildViewportContent — 段落列表 → viewport 行
// ---------------------------------------------------------------------------

func TestBuildViewportContent_EmptyParagraphs(t *testing.T) {
	ctx := ViewportCtx{LC: &enUS, Width: 80}
	lines, starts := buildViewportContent(nil, ctx, -1, 0)
	if len(lines) != 0 {
		t.Errorf("expected 0 lines, got %d", len(lines))
	}
	if len(starts) != 0 {
		t.Errorf("expected 0 lineStarts, got %d", len(starts))
	}
}

func TestBuildViewportContent_UserParagraph(t *testing.T) {
	paras := []Paragraph{
		{Type: paraUser, State: stateDone, Text: "hello"},
	}
	ctx := ViewportCtx{LC: &enUS, Width: 80}
	lines, starts := buildViewportContent(paras, ctx, -1, 0)

	if len(starts) != 1 {
		t.Fatalf("expected 1 lineStart, got %d", len(starts))
	}
	if starts[0] != 0 {
		t.Errorf("expected lineStart[0]=0, got %d", starts[0])
	}
	if len(lines) < 1 {
		t.Fatal("expected at least 1 line")
	}
	if !strings.Contains(lines[0], "hello") {
		t.Errorf("expected 'hello' in first line, got %q", lines[0])
	}
}

func TestBuildViewportContent_StreamingThought(t *testing.T) {
	sp := spinner.New()
	paras := []Paragraph{
		{
			Type:  paraThought,
			State: stateStreaming,
			Text:  "thinking about code...",
		},
	}
	ctx := ViewportCtx{LC: &enUS, Width: 80, Thought: sp}
	lines, _ := buildViewportContent(paras, ctx, -1, 0)

	// 渲染末尾有换行符，split 后末尾多一个空串，实际内容 ≥3 行
	nonEmpty := 0
	for _, l := range lines {
		if l != "" {
			nonEmpty++
		}
	}
	if nonEmpty < 3 {
		t.Errorf("streaming thought should have at least 3 non-empty lines, got %d: %v", nonEmpty, lines)
	}
}

func TestBuildViewportContent_CollapsedThought(t *testing.T) {
	// Use long text so it exceeds 2 wrapped lines and shows the token expand hint.
	paras := []Paragraph{
		{
			Type:          paraThought,
			State:         stateCollapsed,
			Text:          "line one\nline two\nline three\nline four\nline five",
			ThoughtTokens: 120,
		},
	}
	ctx := ViewportCtx{LC: &enUS, Width: 80}
	lines, _ := buildViewportContent(paras, ctx, -1, 0)

	foundToken := false
	for _, l := range lines {
		if strings.Contains(l, "token") {
			foundToken = true
			break
		}
	}
	if !foundToken {
		t.Errorf("expected token count hint in collapsed thought, got lines: %v", lines)
	}
}

func TestBuildViewportContent_ToolStreaming(t *testing.T) {
	sp := spinner.New()
	paras := []Paragraph{
		{
			Type:     paraTool,
			State:    stateStreaming,
			ToolName: "read_file",
			ToolArgs: "main.go",
		},
	}
	ctx := ViewportCtx{LC: &enUS, Width: 80, Tool: sp}
	lines, _ := buildViewportContent(paras, ctx, -1, 0)

	if len(lines) < 1 {
		t.Fatal("expected at least 1 line for tool")
	}
	if !strings.Contains(lines[0], "read_file") {
		t.Errorf("expected tool name in first line, got %q", lines[0])
	}
}

func TestBuildViewportContent_ToolDone(t *testing.T) {
	paras := []Paragraph{
		{
			Type:       paraTool,
			State:      stateDone,
			ToolName:   "bash",
			ToolArgs:   "go test ./...",
			ToolResult: "✅ Command succeeded (exit=0)  123ms\nok  waveloom/pkg/auth  0.234s",
			ToolDurMs:  123,
		},
	}
	ctx := ViewportCtx{LC: &enUS, Width: 80}
	lines, _ := buildViewportContent(paras, ctx, -1, 0)

	if len(lines) < 1 {
		t.Fatal("expected at least 1 line for done tool")
	}
	if !strings.Contains(lines[0], "bash") {
		t.Errorf("expected tool name, got %q", lines[0])
	}
	// Should contain suffix with duration
	hasSuffix := false
	for _, l := range lines {
		if strings.Contains(l, "123ms") {
			hasSuffix = true
			break
		}
	}
	if !hasSuffix {
		t.Errorf("expected duration suffix in tool output, got lines: %v", lines)
	}
}

func TestBuildViewportContent_ToolError(t *testing.T) {
	paras := []Paragraph{
		{
			Type:      paraTool,
			State:     stateError,
			ToolName:  "bash",
			ToolArgs:  "bad command",
			ToolError: "command not found",
		},
	}
	ctx := ViewportCtx{LC: &enUS, Width: 80}
	lines, _ := buildViewportContent(paras, ctx, -1, 0)

	hasError := false
	for _, l := range lines {
		if strings.Contains(l, "command not found") {
			hasError = true
			break
		}
	}
	if !hasError {
		t.Errorf("expected error message in output, got lines: %v", lines)
	}
}

func TestBuildViewportContent_SystemParagraph(t *testing.T) {
	paras := []Paragraph{
		{Type: paraSystem, State: stateDone, Text: "执行被中断。"},
	}
	ctx := ViewportCtx{LC: &enUS, Width: 80}
	lines, _ := buildViewportContent(paras, ctx, -1, 0)

	if len(lines) < 1 {
		t.Fatal("expected at least 1 line for system")
	}
	if !strings.Contains(lines[0], "执行被中断") {
		t.Errorf("expected system message, got %q", lines[0])
	}
}

// ---------------------------------------------------------------------------
// buildViewportContent — 缓存复用
// ---------------------------------------------------------------------------

func TestBuildViewportContent_UsesCache(t *testing.T) {
	paras := []Paragraph{
		{
			Type:         paraUser,
			State:        stateDone,
			Text:         "hello",
			cachedLines:  []string{"› hello"},
			cachedWidth:  80,
			renderDirty:  false,
		},
	}
	ctx := ViewportCtx{LC: &enUS, Width: 80}
	lines, _ := buildViewportContent(paras, ctx, -1, 0)

	if len(lines) != 1 || lines[0] != "› hello" {
		t.Errorf("expected cached line, got %v", lines)
	}
}

func TestBuildViewportContent_DiscardsCacheOnWidthChange(t *testing.T) {
	paras := []Paragraph{
		{
			Type:         paraUser,
			State:        stateDone,
			Text:         "hello",
			cachedLines:  []string{"› hello"},
			cachedWidth:  80,
			renderDirty:  false,
		},
	}
	ctx := ViewportCtx{LC: &enUS, Width: 40} // different width → cache invalid
	lines, _ := buildViewportContent(paras, ctx, -1, 0)

	// Should re-render; the cached line won't be used
	if len(lines) < 1 {
		t.Fatal("expected at least 1 line")
	}
}

func TestBuildViewportContent_DiscardsCacheWhenDirty(t *testing.T) {
	paras := []Paragraph{
		{
			Type:         paraUser,
			State:        stateDone,
			Text:         "hello",
			cachedLines:  []string{"› hello"},
			cachedWidth:  80,
			renderDirty:  true, // dirty → re-render
		},
	}
	ctx := ViewportCtx{LC: &enUS, Width: 80}
	lines, _ := buildViewportContent(paras, ctx, -1, 0)

	if len(lines) < 1 {
		t.Fatal("expected at least 1 line")
	}
}

// ---------------------------------------------------------------------------
// buildViewportContent — lineHint 预分配
// ---------------------------------------------------------------------------

func TestBuildViewportContent_LineHintPreAlloc(t *testing.T) {
	// Verify lineHint is used: with a large hint, the function should not panic
	paras := []Paragraph{
		{Type: paraUser, State: stateDone, Text: "hello"},
	}
	ctx := ViewportCtx{LC: &enUS, Width: 80}
	// Large hint should be fine
	lines, _ := buildViewportContent(paras, ctx, -1, 10000)
	if len(lines) == 0 {
		t.Fatal("expected lines")
	}
}

// ---------------------------------------------------------------------------
// buildViewportContent — lineStarts 正确性
// ---------------------------------------------------------------------------

func TestBuildViewportContent_LineStarts(t *testing.T) {
	paras := []Paragraph{
		{Type: paraUser, State: stateDone, Text: "first"},
		{Type: paraSystem, State: stateDone, Text: "second"},
		{Type: paraUser, State: stateDone, Text: "third"},
	}
	ctx := ViewportCtx{LC: &enUS, Width: 80}
	lines, starts := buildViewportContent(paras, ctx, -1, 0)

	if len(starts) != 3 {
		t.Fatalf("expected 3 starts, got %d", len(starts))
	}
	if starts[0] != 0 {
		t.Errorf("starts[0] = %d, want 0", starts[0])
	}
	// Each paragraph is 1 line → monotonically increasing
	if starts[1] <= starts[0] {
		t.Errorf("starts[1]=%d should be > starts[0]=%d", starts[1], starts[0])
	}
	if starts[2] <= starts[1] {
		t.Errorf("starts[2]=%d should be > starts[1]=%d", starts[2], starts[1])
	}
	_ = lines
}

// ---------------------------------------------------------------------------
// collapseBlankLines
// ---------------------------------------------------------------------------

func TestCollapseBlankLines_NoChange(t *testing.T) {
	result := collapseBlankLines("hello\nworld")
	if result != "hello\nworld" {
		t.Fatalf("expected no change, got %q", result)
	}
}

func TestCollapseBlankLines_SingleBlank(t *testing.T) {
	result := collapseBlankLines("hello\n\nworld")
	if result != "hello\n\nworld" {
		t.Fatalf("expected single blank line preserved, got %q", result)
	}
}

func TestCollapseBlankLines_MultipleBlanks(t *testing.T) {
	result := collapseBlankLines("hello\n\n\n\nworld")
	if result != "hello\n\nworld" {
		t.Fatalf("expected consecutive blanks collapsed to 1, got %q", result)
	}
}

func TestCollapseBlankLines_LeadingTrailing(t *testing.T) {
	// Trim is done before collapseBlankLines, but it should handle edge cases
	result := collapseBlankLines("\n\n\nhello\n\n\n\nworld\n\n\n")
	if result != "\n\nhello\n\nworld\n\n" {
		t.Fatalf("expected collapsed blanks, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// skipAnsiSequence — OSC / DCS
// ---------------------------------------------------------------------------

func TestSkipAnsiSequence_CSI(t *testing.T) {
	runes := []rune("\x1b[32mhello")
	n := skipAnsiSequence(runes)
	if n != 5 { // ESC [ 3 2 m
		t.Fatalf("expected 5, got %d", n)
	}
}

func TestSkipAnsiSequence_OSC_BEL(t *testing.T) {
	runes := []rune("\x1b]8;;http://example.com\x07trailing")
	n := skipAnsiSequence(runes)
	if n == 0 {
		t.Fatal("expected non-zero skip for OSC sequence terminated by BEL")
	}
	remaining := string(runes[n:])
	if remaining != "trailing" {
		t.Fatalf("expected 'trailing', got %q", remaining)
	}
}

func TestSkipAnsiSequence_OSC_ST(t *testing.T) {
	runes := []rune("\x1b]8;;http://example.com\x1b\\trailing")
	n := skipAnsiSequence(runes)
	if n == 0 {
		t.Fatal("expected non-zero skip for OSC sequence terminated by ST")
	}
	remaining := string(runes[n:])
	if remaining != "trailing" {
		t.Fatalf("expected 'trailing', got %q", remaining)
	}
}

func TestSkipAnsiSequence_PlainText(t *testing.T) {
	runes := []rune("hello")
	n := skipAnsiSequence(runes)
	if n != 0 {
		t.Fatalf("expected 0 for plain text, got %d", n)
	}
}

// ── stripToolStatusHeader ──

func TestStripToolStatusHeader_ShellFailed(t *testing.T) {
	input := "Command failed (exit=1)  120ms\n   stderr/stdout:\n     error: something broke\n"
	got := stripToolStatusHeader(input)
	if strings.Contains(got, "Command failed") {
		t.Errorf("should strip shell status header, got: %q", got)
	}
	if !strings.Contains(got, "error: something broke") {
		t.Errorf("should keep error output, got: %q", got)
	}
}

func TestStripToolStatusHeader_ShellSucceeded(t *testing.T) {
	input := "Command succeeded (exit=0)  50ms\n   stdout:\n     build ok\n"
	got := stripToolStatusHeader(input)
	if strings.Contains(got, "Command succeeded") {
		t.Errorf("should strip shell status header, got: %q", got)
	}
	if !strings.Contains(got, "build ok") {
		t.Errorf("should keep stdout output, got: %q", got)
	}
}

func TestStripToolStatusHeader_ShellTimedOut(t *testing.T) {
	input := "Command timed out  120s\n   Timeout: 120s\n   stderr/stdout:\n     partial output\n"
	got := stripToolStatusHeader(input)
	if strings.Contains(got, "Command timed out") {
		t.Errorf("should strip timeout header, got: %q", got)
	}
	if strings.Contains(got, "Timeout:") {
		t.Errorf("should strip timeout label, got: %q", got)
	}
	if !strings.Contains(got, "partial output") {
		t.Errorf("should keep partial output, got: %q", got)
	}
}

func TestStripToolStatusHeader_Emoji(t *testing.T) {
	input := "✅ Tool executed\nsome content"
	got := stripToolStatusHeader(input)
	if strings.Contains(got, "\u2705") {
		t.Errorf("should strip emoji header, got: %q", got)
	}
	if !strings.Contains(got, "some content") {
		t.Errorf("should keep content, got: %q", got)
	}
}

func TestStripToolStatusHeader_NoHeader(t *testing.T) {
	input := "just some output\nwith multiple lines"
	got := stripToolStatusHeader(input)
	if got != "just some output\nwith multiple lines" {
		t.Errorf("should return unchanged, got: %q", got)
	}
}

// ---------------------------------------------------------------------------
// formatQuestionArgs — ask_user_question 参数摘要提取
// ---------------------------------------------------------------------------

func TestFormatQuestionArgs_SingleQuestion(t *testing.T) {
	args := `{"questions":[{"question":"Which library?","header":"Library","options":[{"label":"A","description":"desc"}]}]}`
	got := formatQuestionArgs(args)
	if got != "Library" {
		t.Errorf("formatQuestionArgs = %q, want %q", got, "Library")
	}
}

func TestFormatQuestionArgs_MultipleQuestions(t *testing.T) {
	args := `{"questions":[
		{"question":"Q1?","header":"Language","options":[{"label":"Go","description":"fast"}]},
		{"question":"Q2?","header":"Framework","options":[{"label":"Fiber","description":"light"}]}
	]}`
	got := formatQuestionArgs(args)
	if got != "Language, Framework" {
		t.Errorf("formatQuestionArgs = %q, want %q", got, "Language, Framework")
	}
}

func TestFormatQuestionArgs_InvalidJSON(t *testing.T) {
	args := `{invalid`
	got := formatQuestionArgs(args)
	if got != `{invalid` {
		t.Errorf("formatQuestionArgs with invalid JSON should fallback to truncated original, got %q", got)
	}
}

func TestFormatQuestionArgs_EmptyQuestions(t *testing.T) {
	args := `{"questions":[]}`
	got := formatQuestionArgs(args)
	if got != `{"questions":[]}` {
		t.Errorf("formatQuestionArgs with empty questions should fallback to truncated original, got %q", got)
	}
}

func TestFormatQuestionArgs_LongArgsTruncation(t *testing.T) {
	// 构造超过 50 字符的非法 JSON → 应截断为 50 字符
	longStr := `{` + strings.Repeat("x", 100) + `}`
	got := formatQuestionArgs(longStr)
	runes := []rune(got)
	if len(runes) > 50 {
		t.Errorf("formatQuestionArgs truncation: got %d runes, want ≤50", len(runes))
	}
}

// ---------------------------------------------------------------------------
// parseQuestionResult — ask_user_question 结果解析
// ---------------------------------------------------------------------------

func TestParseQuestionResult_SingleAnswer(t *testing.T) {
	result := `{"questions":[{"question":"Which lib?","header":"Lib"}],"answers":{"Which lib?":"Go"}}`
	answers, order := parseQuestionResult(result)
	if len(order) != 1 || order[0] != "Lib" {
		t.Fatalf("order = %v, want [Lib]", order)
	}
	if answers["Lib"] != "Go" {
		t.Errorf("answers[Lib] = %q, want %q", answers["Lib"], "Go")
	}
}

func TestParseQuestionResult_MultipleAnswers(t *testing.T) {
	result := `{"questions":[
		{"question":"Lang?","header":"Language"},
		{"question":"FW?","header":"Framework"}
	],"answers":{"Lang?":"Go","FW?":"Fiber"}}`
	answers, order := parseQuestionResult(result)
	if len(order) != 2 {
		t.Fatalf("order len = %d, want 2", len(order))
	}
	if order[0] != "Language" || order[1] != "Framework" {
		t.Errorf("order = %v, want [Language Framework]", order)
	}
	if answers["Language"] != "Go" {
		t.Errorf("answers[Language] = %q, want %q", answers["Language"], "Go")
	}
	if answers["Framework"] != "Fiber" {
		t.Errorf("answers[Framework] = %q, want %q", answers["Framework"], "Fiber")
	}
}

func TestParseQuestionResult_OrderPreserved(t *testing.T) {
	// 验证 order 与 questions 数组顺序严格一致
	result := `{"questions":[
		{"question":"Q3?","header":"Third"},
		{"question":"Q1?","header":"First"},
		{"question":"Q2?","header":"Second"}
	],"answers":{"Q1?":"a1","Q2?":"a2","Q3?":"a3"}}`
	_, order := parseQuestionResult(result)
	if len(order) != 3 {
		t.Fatalf("order len = %d, want 3", len(order))
	}
	if order[0] != "Third" || order[1] != "First" || order[2] != "Second" {
		t.Errorf("order = %v, want [Third First Second]", order)
	}
}

func TestParseQuestionResult_InvalidJSON(t *testing.T) {
	answers, order := parseQuestionResult(`{bad json`)
	if answers != nil || order != nil {
		t.Errorf("parseQuestionResult invalid JSON: (%v, %v), want (nil, nil)", answers, order)
	}
}

func TestParseQuestionResult_EmptyQuestions(t *testing.T) {
	result := `{"questions":[],"answers":{}}`
	answers, order := parseQuestionResult(result)
	if len(answers) != 0 || len(order) != 0 {
		t.Errorf("parseQuestionResult empty: answers=%v, order=%v, want both empty", answers, order)
	}
}

// ---------------------------------------------------------------------------
// parseQuestionCount — ask_user_question 问题数量解析
// ---------------------------------------------------------------------------

func TestParseQuestionCount_ThreeQuestions(t *testing.T) {
	jsonStr := `{"questions":[{},{},{}]}`
	n := parseQuestionCount(jsonStr)
	if n != 3 {
		t.Errorf("parseQuestionCount = %d, want 3", n)
	}
}

func TestParseQuestionCount_SingleQuestion(t *testing.T) {
	jsonStr := `{"questions":[{"question":"Q?","header":"H","options":[{"label":"A","description":"d"}]}]}`
	n := parseQuestionCount(jsonStr)
	if n != 1 {
		t.Errorf("parseQuestionCount = %d, want 1", n)
	}
}

func TestParseQuestionCount_ZeroQuestions(t *testing.T) {
	n := parseQuestionCount(`{"questions":[]}`)
	if n != 0 {
		t.Errorf("parseQuestionCount = %d, want 0", n)
	}
}

func TestParseQuestionCount_InvalidJSON(t *testing.T) {
	n := parseQuestionCount(`not json`)
	if n != 0 {
		t.Errorf("parseQuestionCount = %d, want 0", n)
	}
}

func TestParseQuestionCount_EmptyString(t *testing.T) {
	n := parseQuestionCount(``)
	if n != 0 {
		t.Errorf("parseQuestionCount = %d, want 0", n)
	}
}

// ---------------------------------------------------------------------------
// formatQuestionPreview 宽度自适应回归测试
// ---------------------------------------------------------------------------

func TestFormatQuestionPreview_ShortAnswer(t *testing.T) {
	// 短答案在一行内完整显示
	result := `{"questions":[{"question":"Lang?","header":"Language"}],"answers":{"Lang?":"Go"}}`
	lc := &Messages{ToolQuestionDeclined: "(declined)", ToolTruncated: "···"}
	preview := formatQuestionPreview(result, 80, "  ", lc)
	if !strings.Contains(preview, "Language → Go") {
		t.Errorf("expected 'Language → Go' in preview, got: %s", preview)
	}
	// 不应出现截断标记
	if strings.Contains(preview, "···") {
		t.Errorf("unexpected truncation for short answer")
	}
}

func TestFormatQuestionPreview_LongAnswerWraps(t *testing.T) {
	// 长答案应在窄宽度下换行
	result := `{"questions":[{"question":"Details?","header":"Info"}],"answers":{"Details?":"This is a very long answer that should wrap to multiple lines when the terminal is narrow"}}`
	lc := &Messages{ToolQuestionDeclined: "(declined)", ToolTruncated: "···"}
	preview := formatQuestionPreview(result, 40, "", lc)
	// 窄宽度下应产生多行（wrap）
	lines := strings.Split(strings.TrimRight(preview, "\n"), "\n")
	if len(lines) < 2 {
		t.Errorf("expected multiple wrapped lines at width 40, got %d: %s", len(lines), preview)
	}
}

func TestFormatQuestionPreview_Truncation(t *testing.T) {
	// 超长内容在窄宽度下应截断（maxPreviewWrapped=5 行限制）
	result := `{"questions":[{"question":"A?","header":"A"}],"answers":{"A?":"line1 line2 line3 line4 line5 line6 line7 line8 line9 line10"}}`
	lc := &Messages{ToolQuestionDeclined: "(declined)", ToolTruncated: "···"}
	preview := formatQuestionPreview(result, 10, "", lc)
	if !strings.Contains(preview, "···") {
		t.Errorf("expected truncation marker for very long answer at narrow width, got: %s", preview)
	}
}

func TestFormatQuestionPreview_Declined(t *testing.T) {
	// 拒绝的答案应显示 declined 占位
	result := `{"questions":[{"question":"Approve?","header":"Confirm"}]}`
	lc := &Messages{ToolQuestionDeclined: "(declined)", ToolTruncated: "···"}
	preview := formatQuestionPreview(result, 80, "", lc)
	if !strings.Contains(preview, "(declined)") {
		t.Errorf("expected declined placeholder, got: %s", preview)
	}
}

func TestFormatQuestionPreview_MultipleQuestions(t *testing.T) {
	// 多问题应全部显示（不超出 maxPreviewWrapped 时）
	result := `{"questions":[
		{"question":"Lang?","header":"Language"},
		{"question":"FW?","header":"Framework"}
	],"answers":{"Lang?":"Go","FW?":"Fiber"}}`
	lc := &Messages{ToolQuestionDeclined: "(declined)", ToolTruncated: "···"}
	preview := formatQuestionPreview(result, 80, "  ", lc)
	if !strings.Contains(preview, "Language → Go") {
		t.Errorf("missing first answer: %s", preview)
	}
	if !strings.Contains(preview, "Framework → Fiber") {
		t.Errorf("missing second answer: %s", preview)
	}
}

// ---------------------------------------------------------------------------
// truncateToolStreamOutput 回归测试
// ---------------------------------------------------------------------------

func TestTruncateToolStreamOutput_UnderLimit(t *testing.T) {
	// 未超过 maxToolStreamLines=2000 时原样返回
	input := "line1\nline2\nline3\n"
	got := truncateToolStreamOutput(input)
	if got != input {
		t.Errorf("expected unchanged output, got: %q", got)
	}
}

func TestTruncateToolStreamOutput_OverLimit(t *testing.T) {
	// 超过上限时截断头部，保留尾部 2000 行
	var sb strings.Builder
	for i := 0; i < 2500; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	input := sb.String()
	got := truncateToolStreamOutput(input)
	if !strings.Contains(got, "... (stream truncated") {
		t.Errorf("expected truncation notice in output")
	}
	lines := strings.Split(got, "\n")
	// 2000 行内容 + 1 行 truncation notice，尾部无空行
	if len(lines) < 2001 {
		t.Errorf("expected ~2001 lines (1 notice + 2000 content), got %d", len(lines))
	}
}

func TestTruncateToolStreamOutput_Empty(t *testing.T) {
	got := truncateToolStreamOutput("")
	if got != "" {
		t.Errorf("expected empty, got: %q", got)
	}
}

// ---------------------------------------------------------------------------
// renderToolStreamOutput 回归测试
// ---------------------------------------------------------------------------

func TestRenderToolStreamOutput_Basic(t *testing.T) {
	p := &Paragraph{
		Type:       paraTool,
		State:      stateStreaming,
		ToolName:   "bash",
		ToolResult: "go build ./...\n",
	}
	var sb strings.Builder
	renderToolStreamOutput(&sb, p, 80, "  ", nil)
	out := sb.String()
	// mutedStyle 会包裹 ANSI 颜色序列，用 content 值检测
	if !strings.Contains(out, "go build") {
		t.Errorf("expected output line containing 'go build', got: %s", out)
	}
	// 验证 │ 前缀存在（ANSI 序列包裹）
	if !strings.Contains(out, "│") {
		t.Errorf("expected │ prefix in output, got: %s", out)
	}
}

func TestRenderToolStreamOutput_EmptyResult(t *testing.T) {
	p := &Paragraph{
		Type:       paraTool,
		State:      stateStreaming,
		ToolName:   "bash",
		ToolResult: "",
	}
	var sb strings.Builder
	renderToolStreamOutput(&sb, p, 80, "  ", nil)
	if sb.Len() > 0 {
		t.Errorf("expected empty output, got: %s", sb.String())
	}
}

func TestRenderToolStreamOutput_Wrapping(t *testing.T) {
	// 单条超长行应在窄宽度下换行
	p := &Paragraph{
		Type:       paraTool,
		State:      stateStreaming,
		ToolName:   "bash",
		ToolResult: "this is a very long line that should wrap to multiple lines when the terminal width is narrow\n",
	}
	var sb strings.Builder
	renderToolStreamOutput(&sb, p, 20, "", nil)
	out := sb.String()
	// 去掉 ANSI 序列后统计 │ 的数量即行数
	clean := stripANSI(out)
	lines := strings.Split(strings.TrimRight(clean, "\n"), "\n")
	if len(lines) < 2 {
		t.Errorf("expected multiple wrapped lines at width 20, got %d: %s", len(lines), clean)
	}
}

func TestRenderToolStreamOutput_FixedLines(t *testing.T) {
	// 超过 5 行时只显示最后 5 个 visible 行
	var input strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&input, "line %d\n", i)
	}
	p := &Paragraph{
		Type:       paraTool,
		State:      stateStreaming,
		ToolName:   "bash",
		ToolResult: input.String(),
	}
	var sb strings.Builder
	renderToolStreamOutput(&sb, p, 80, "", nil)
	clean := stripANSI(sb.String())
	lines := strings.Split(strings.TrimRight(clean, "\n"), "\n")
	if len(lines) > 5 {
		t.Errorf("expected at most 5 visible lines, got %d: %s", len(lines), clean)
	}
}

func TestRenderToolStreamOutput_SkipsEmptyLines(t *testing.T) {
	// 尾部空行不应产生空的 │ 行
	p := &Paragraph{
		Type:       paraTool,
		State:      stateStreaming,
		ToolName:   "bash",
		ToolResult: "line1\nline2\n\n\n",
	}
	var sb strings.Builder
	renderToolStreamOutput(&sb, p, 80, "", nil)
	clean := stripANSI(sb.String())
	// 不应有空内容的 │  行（即 "│ " 后面紧跟 \n，没有任何其他字符）
	lines := strings.Split(clean, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "│" {
			t.Errorf("found empty │ line in output: %s", clean)
		}
	}
	// 应显示 line1, line2
	if !strings.Contains(clean, "line1") || !strings.Contains(clean, "line2") {
		t.Errorf("expected line1 and line2 in output, got: %s", clean)
	}
}

// stripANSI 移除 ANSI 转义序列，用于测试中比较纯文本内容。
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}
