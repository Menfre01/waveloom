package main

import (
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
	ctx := ViewportCtx{Width: 80}
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
	ctx := ViewportCtx{Width: 80}
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
	ctx := ViewportCtx{Width: 80, Thought: sp}
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
	ctx := ViewportCtx{Width: 80}
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
	ctx := ViewportCtx{Width: 80, Tool: sp}
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
			ToolName:   "shell",
			ToolArgs:   "go test ./...",
			ToolResult: "✅ Command succeeded (exit=0)  123ms\nok  waveloom/pkg/auth  0.234s",
			ToolDurMs:  123,
		},
	}
	ctx := ViewportCtx{Width: 80}
	lines, _ := buildViewportContent(paras, ctx, -1, 0)

	if len(lines) < 1 {
		t.Fatal("expected at least 1 line for done tool")
	}
	if !strings.Contains(lines[0], "shell") {
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
			ToolName:  "shell",
			ToolArgs:  "bad command",
			ToolError: "command not found",
		},
	}
	ctx := ViewportCtx{Width: 80}
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
	ctx := ViewportCtx{Width: 80}
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
	ctx := ViewportCtx{Width: 80}
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
	ctx := ViewportCtx{Width: 40} // different width → cache invalid
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
	ctx := ViewportCtx{Width: 80}
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
	ctx := ViewportCtx{Width: 80}
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
	ctx := ViewportCtx{Width: 80}
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
