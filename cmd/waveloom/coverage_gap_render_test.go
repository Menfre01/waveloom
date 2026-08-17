package main

// 本文件补齐 tui_renderer.go / tui_styles.go 中 0 覆盖率的纯渲染与
// 纯逻辑函数,以及 setup.go / main.go 中可隔离的辅助函数测试。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/subagent"
	"github.com/Menfre01/waveloom/pkg/todo"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// 段落基础操作
// ---------------------------------------------------------------------------

func TestLastPara(t *testing.T) {
	if got := lastPara(nil); got != nil {
		t.Errorf("lastPara(nil) = %v, want nil", got)
	}
	if got := lastPara([]Paragraph{}); got != nil {
		t.Errorf("lastPara(empty) = %v, want nil", got)
	}
	paras := []Paragraph{{Text: "a"}, {Text: "b"}}
	if got := lastPara(paras); got == nil || got.Text != "b" {
		t.Errorf("lastPara should return pointer to last element, got %+v", got)
	}
}

// ---------------------------------------------------------------------------
// 工具错误后缀
// ---------------------------------------------------------------------------

func TestWebFetchErrorSuffix(t *testing.T) {
	tests := []struct {
		kind, msg, want string
	}{
		{"timeout", "", "(timeout)"},
		{"invalid_args", "", "(invalid URL)"},
		{"binary_file", "", "(unsupported type)"},
		{"command_failed", "HTTP 404 Not Found", "(HTTP 404)"},
		{"command_failed", "something else", "(request failed)"},
		{"other", "x", "(other)"},
	}
	for _, tt := range tests {
		if got := webFetchErrorSuffix(tt.kind, tt.msg); got != tt.want {
			t.Errorf("webFetchErrorSuffix(%q, %q) = %q, want %q", tt.kind, tt.msg, got, tt.want)
		}
	}
}

func TestWebSearchErrorSuffix(t *testing.T) {
	tests := []struct {
		kind, msg, want string
	}{
		{"timeout", "", "(timeout)"},
		{"invalid_args", "", "(empty query)"},
		{"command_failed", "HTTP 500", "(search API error)"},
		{"command_failed", "boom", "(search failed)"},
		{"other", "", "(other)"},
	}
	for _, tt := range tests {
		if got := webSearchErrorSuffix(tt.kind, tt.msg); got != tt.want {
			t.Errorf("webSearchErrorSuffix(%q, %q) = %q, want %q", tt.kind, tt.msg, got, tt.want)
		}
	}
}

func TestEditFileErrorSuffixAndParseMatchCount(t *testing.T) {
	if got := editFileErrorSuffix("multiple_matches", "found 3 matches for x in /a/b"); got != "(3 matches)" {
		t.Errorf("multiple_matches with count = %q", got)
	}
	if got := editFileErrorSuffix("multiple_matches", "no count here"); got != "(multiple_matches)" {
		t.Errorf("multiple_matches without count = %q", got)
	}
	if got := editFileErrorSuffix("no_match", "x"); got != "(no_match)" {
		t.Errorf("default = %q", got)
	}

	tests := []struct {
		msg  string
		want int
	}{
		{"found 3 matches for old in /x", 3},
		{"found 42 matches", 42},
		{"not a match count", 0},
		{"found abc matches", 0},
		{"", 0},
	}
	for _, tt := range tests {
		if got := parseMatchCount(tt.msg); got != tt.want {
			t.Errorf("parseMatchCount(%q) = %d, want %d", tt.msg, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 余额 / diff 统计
// ---------------------------------------------------------------------------

func TestFormatBalance(t *testing.T) {
	if got := formatBalance(nil); got != "" {
		t.Errorf("nil balance = %q", got)
	}
	if got := formatBalance(&llm.BalanceInfo{}); got != "" {
		t.Errorf("empty balance = %q", got)
	}
	// USD 优先
	b := &llm.BalanceInfo{BalanceInfos: []llm.CurrencyBalance{
		{Currency: "CNY", TotalBalance: "100.00"},
		{Currency: "USD", TotalBalance: "14.50"},
	}}
	if got := formatBalance(b); got != "USD 14.50" {
		t.Errorf("USD priority = %q", got)
	}
	// 无 USD → 首个币种
	b2 := &llm.BalanceInfo{BalanceInfos: []llm.CurrencyBalance{
		{Currency: "CNY", TotalBalance: "100.00"},
	}}
	if got := formatBalance(b2); got != "CNY 100.00" {
		t.Errorf("fallback first currency = %q", got)
	}
}

func TestCountDiffLines(t *testing.T) {
	in := "+++ header\n--- header\n+added1\n+added2\n-removed1\n context\n+ added3"
	added, removed := countDiffLines(in)
	if added != 3 || removed != 1 {
		t.Errorf("countDiffLines = (%d, %d), want (3, 1)", added, removed)
	}
}

// ---------------------------------------------------------------------------
// MCP 标签 / 搜索结果统计
// ---------------------------------------------------------------------------

func TestMcpToolLabel(t *testing.T) {
	tests := []struct{ in, want string }{
		{"mcp__vscode__get_symbols", "vscode:get_symbols"},
		{"mcp__server", "server"},
		{"not_mcp", "not_mcp"},
	}
	for _, tt := range tests {
		if got := mcpToolLabel(tt.in); got != tt.want {
			t.Errorf("mcpToolLabel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCountSearchResults(t *testing.T) {
	// "10. Ten" 是两位数编号,实现仅匹配个位数编号(trimmed[1] == '.'),不计数
	in := "1. First result\n2. Second\nnot numbered\n10. Ten\n 3. Three"
	if got := countSearchResults(in); got != 3 {
		t.Errorf("countSearchResults = %d, want 3", got)
	}
}

// ---------------------------------------------------------------------------
// 问答展开渲染
// ---------------------------------------------------------------------------

func TestFormatQuestionExpanded(t *testing.T) {
	resultJSON := `{"questions":[{"question":"q1","header":"Header One"}],"answers":{"q1":"Answer A"}}`
	got := formatQuestionExpanded(resultJSON, "  ", 80, &zhCN)
	if !strings.Contains(got, "Header One") || !strings.Contains(got, "Answer A") {
		t.Errorf("expanded output missing content: %q", got)
	}

	if got := formatQuestionExpanded("not json", "", 80, &zhCN); got != "" {
		t.Errorf("invalid JSON should return empty, got %q", got)
	}
	if got := formatQuestionExpanded(`{"questions":[]}`, "", 80, &zhCN); got != "" {
		t.Errorf("no questions should return empty, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// web_fetch / web_search 完整展开渲染
// ---------------------------------------------------------------------------

func TestRenderWebFetchFull(t *testing.T) {
	var sb strings.Builder
	result := "Fetched https://x.com  HTTP 200  1.2s\nContent-Type: text/html\n\nbody line 1\nbody line 2"
	renderWebFetchFull(&sb, result, 80, "", &zhCN)
	got := sb.String()
	if !strings.Contains(got, "Fetched https://x.com") {
		t.Errorf("header missing: %q", got)
	}
	if !strings.Contains(got, "body line 1") || !strings.Contains(got, "body line 2") {
		t.Errorf("body missing: %q", got)
	}
}

func TestRenderWebSearchFull(t *testing.T) {
	var sb strings.Builder
	result := "Search results for: \"go\"  (DuckDuckGo)  1.3s\n\n1. First\n2. Second"
	renderWebSearchFull(&sb, result, 80, "", &zhCN)
	got := sb.String()
	if !strings.Contains(got, "1. First") || !strings.Contains(got, "2. Second") {
		t.Errorf("results missing: %q", got)
	}
}

// ---------------------------------------------------------------------------
// assistant 段落渲染(无 Glamour 纯文本路径)
// ---------------------------------------------------------------------------

func TestRenderAssistantPara(t *testing.T) {
	t.Run("done without glamour", func(t *testing.T) {
		var sb strings.Builder
		p := &Paragraph{Type: paraAssistant, State: stateDone, Text: "hello **world**"}
		ctx := ViewportCtx{Width: 40, LC: &enUS}
		renderAssistantPara(&sb, p, ctx)
		if !strings.Contains(sb.String(), "hello **world**") {
			t.Errorf("content missing: %q", sb.String())
		}
	})

	t.Run("streaming appends cursor marker", func(t *testing.T) {
		var sb strings.Builder
		p := &Paragraph{Type: paraAssistant, State: stateStreaming, Text: "partial"}
		ctx := ViewportCtx{Width: 40, LC: &enUS}
		renderAssistantPara(&sb, p, ctx)
		if !strings.Contains(sb.String(), "partial") {
			t.Errorf("content missing: %q", sb.String())
		}
	})

	t.Run("empty text renders prefix line only", func(t *testing.T) {
		var sb strings.Builder
		p := &Paragraph{Type: paraAssistant, State: stateStreaming, Text: ""}
		ctx := ViewportCtx{Width: 40, LC: &enUS}
		renderAssistantPara(&sb, p, ctx)
		if sb.Len() == 0 {
			t.Error("expected non-empty output for empty text")
		}
	})
}

// ---------------------------------------------------------------------------
// 工具展开渲染
// ---------------------------------------------------------------------------

func TestRenderToolFullOutput(t *testing.T) {
	t.Run("bash expanded", func(t *testing.T) {
		var sb strings.Builder
		p := &Paragraph{ToolName: "bash", ToolResult: "out line\nout line2"}
		renderToolFullOutput(&sb, p, 80, "", &zhCN)
		got := sb.String()
		if !strings.Contains(got, "out line2") {
			t.Errorf("output missing: %q", got)
		}
		if !strings.Contains(got, zhCN.ToolCollapseHint) {
			t.Errorf("collapse hint missing: %q", got)
		}
	})

	t.Run("read with line numbers", func(t *testing.T) {
		var sb strings.Builder
		p := &Paragraph{ToolName: "read", ToolResult: "line1\nline2"}
		renderToolFullOutput(&sb, p, 80, "", &zhCN)
		got := sb.String()
		if !strings.Contains(got, "│") {
			t.Errorf("line number gutter missing: %q", got)
		}
	})

	t.Run("error fallback when result empty", func(t *testing.T) {
		var sb strings.Builder
		p := &Paragraph{ToolName: "bash", ToolError: "boom", ToolFatal: true}
		renderToolFullOutput(&sb, p, 80, "", &zhCN)
		if !strings.Contains(sb.String(), "boom") {
			t.Errorf("error message missing: %q", sb.String())
		}
	})

	t.Run("empty result and error yields empty output", func(t *testing.T) {
		var sb strings.Builder
		p := &Paragraph{ToolName: "bash"}
		renderToolFullOutput(&sb, p, 80, "", &zhCN)
		if sb.Len() != 0 {
			t.Errorf("expected empty output, got %q", sb.String())
		}
	})

	t.Run("web_fetch routes to full renderer", func(t *testing.T) {
		var sb strings.Builder
		p := &Paragraph{ToolName: "web_fetch", ToolResult: "header\n\nbody text"}
		renderToolFullOutput(&sb, p, 80, "", &zhCN)
		if !strings.Contains(sb.String(), "body text") {
			t.Errorf("web_fetch body missing: %q", sb.String())
		}
	})

	t.Run("ask_user_question routes to expanded formatter", func(t *testing.T) {
		var sb strings.Builder
		p := &Paragraph{
			ToolName:   "ask_user_question",
			ToolResult: `{"questions":[{"question":"q1","header":"H1"}],"answers":{"q1":"A1"}}`,
		}
		renderToolFullOutput(&sb, p, 80, "", &zhCN)
		if !strings.Contains(sb.String(), "H1") {
			t.Errorf("question output missing: %q", sb.String())
		}
	})
}

// ---------------------------------------------------------------------------
// diff 渲染
// ---------------------------------------------------------------------------

func sampleHunks() []tool.DiffHunk {
	return []tool.DiffHunk{
		{
			FilePath: "main.go", OldStart: 1, OldCount: 1, NewStart: 1, NewCount: 1,
			Heading: "func greet",
			Lines: []tool.DiffLine{
				{Kind: tool.DiffDel, Content: "old", OldNum: 1},
				{Kind: tool.DiffAdd, Content: "new", NewNum: 1},
				{Kind: tool.DiffCtx, Content: "ctx", OldNum: 2, NewNum: 2},
			},
		},
	}
}

func TestRenderDiffPreview(t *testing.T) {
	var sb strings.Builder
	ctx := ViewportCtx{LC: &zhCN, Width: 80}
	renderDiffPreview(&sb, sampleHunks(), 80, "", ctx)
	if !strings.Contains(sb.String(), "+new") || !strings.Contains(sb.String(), "-old") {
		t.Errorf("diff lines missing: %q", sb.String())
	}

	var empty strings.Builder
	renderDiffPreview(&empty, nil, 80, "", ctx)
	if empty.Len() != 0 {
		t.Errorf("nil hunks should render nothing, got %q", empty.String())
	}
}

func TestRenderDiffView(t *testing.T) {
	t.Run("single file with heading", func(t *testing.T) {
		var sb strings.Builder
		ctx := ViewportCtx{LC: &zhCN, Width: 80, CWD: "/proj"}
		renderDiffView(&sb, sampleHunks(), 80, "", ctx)
		got := sb.String()
		for _, want := range []string{"main.go", "@@ -1 +1 @@ func greet", "-old", "+new"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in:\n%s", want, got)
			}
		}
	})

	t.Run("multi-file renders separator", func(t *testing.T) {
		hunks := []tool.DiffHunk{
			sampleHunks()[0],
			{FilePath: "util.go", OldStart: 3, OldCount: 2, NewStart: 3, NewCount: 2,
				Lines: []tool.DiffLine{{Kind: tool.DiffCtx, Content: "x", OldNum: 3, NewNum: 3}}},
		}
		var sb strings.Builder
		ctx := ViewportCtx{LC: &zhCN, Width: 80}
		renderDiffView(&sb, hunks, 80, "", ctx)
		if !strings.Contains(sb.String(), "util.go") {
			t.Errorf("second file header missing: %q", sb.String())
		}
	})

	t.Run("nil hunks render nothing", func(t *testing.T) {
		var sb strings.Builder
		renderDiffView(&sb, nil, 80, "", ViewportCtx{LC: &zhCN})
		if sb.Len() != 0 {
			t.Errorf("expected empty, got %q", sb.String())
		}
	})
}

func TestDigitCountAndHunkRange(t *testing.T) {
	tests := []struct {
		n    int
		want int
	}{
		{0, 1}, {-5, 1}, {9, 1}, {10, 2}, {100, 3}, {12345, 5},
	}
	for _, tt := range tests {
		if got := digitCount(tt.n); got != tt.want {
			t.Errorf("digitCount(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}

	if got := hunkRange(5, 1); got != "5" {
		t.Errorf("hunkRange(5, 1) = %q", got)
	}
	if got := hunkRange(5, 3); got != "5,3" {
		t.Errorf("hunkRange(5, 3) = %q", got)
	}
}

func TestDiffLineStyleHelpers(t *testing.T) {
	if linePrefix(tool.DiffAdd) != "+" || linePrefix(tool.DiffDel) != "-" ||
		linePrefix(tool.DiffHeader) != "@@" || linePrefix(tool.DiffCtx) != " " {
		t.Error("linePrefix returns wrong prefixes")
	}
	p, _ := diffLinePrefixAndStyle(tool.DiffAdd)
	if p != "+" {
		t.Errorf("diffLinePrefixAndStyle(add) prefix = %q", p)
	}
	p, _ = diffLinePrefixAndStyle(tool.DiffDel)
	if p != "-" {
		t.Errorf("diffLinePrefixAndStyle(del) prefix = %q", p)
	}
	// lineStyle 覆盖所有分支(仅验证不 panic 且返回样式对象)
	for _, k := range []tool.DiffLineKind{tool.DiffAdd, tool.DiffDel, tool.DiffHeader, tool.DiffCtx} {
		_ = lineStyle(k)
	}
}

func TestCountHunksAndExtractPatchPaths(t *testing.T) {
	patch := "*** Update File: a.go\n@@ func x\n-x\n+y\n@@ func y\n-z\n+w\n*** Update File: b.go\n@@\n"
	if got := countHunks(patch); got != 3 {
		t.Errorf("countHunks = %d, want 3", got)
	}
	paths := extractPatchPaths(patch)
	if len(paths) != 2 || paths[0] != "a.go" || paths[1] != "b.go" {
		t.Errorf("extractPatchPaths = %v", paths)
	}
	// 去重
	dup := "*** Update File: a.go\n*** Update File: a.go\n"
	if got := extractPatchPaths(dup); len(got) != 1 {
		t.Errorf("dedup failed: %v", got)
	}
}

func TestFindFirstPromptPos(t *testing.T) {
	if got := findFirstPromptPos("no double space"); got != -1 {
		t.Errorf("no match = %d", got)
	}
	if got := findFirstPromptPos("a  b"); got != 1 {
		t.Errorf("plain match = %d", got)
	}
	ansi := "\x1b[31mred\x1b[0m  rest"
	if got := findFirstPromptPos(ansi); got != len("\x1b[31mred\x1b[0m") {
		t.Errorf("ANSI match = %d, want %d", got, len("\x1b[31mred\x1b[0m"))
	}
}

// ---------------------------------------------------------------------------
// subagent 渲染
// ---------------------------------------------------------------------------

func TestSubagentSuffix(t *testing.T) {
	tests := []struct {
		name string
		p    Paragraph
		want string
	}{
		{"streaming empty", Paragraph{State: stateStreaming}, ""},
		{"error interrupted", Paragraph{State: stateError, ToolError: "x"}, "(interrupted)"},
		{"model only", Paragraph{State: stateDone, SubagentModel: "flash"}, "(flash)"},
		{"turns and duration", Paragraph{State: stateDone, SubagentTurns: 3, ToolDurMs: 2100}, "(3步, 2.1s)"},
		{"model turns tokens", Paragraph{State: stateDone, SubagentModel: "flash", SubagentTurns: 2, SubagentPromptTok: 1500, SubagentComplTok: 2500}, "(flash, 2步, ↑1.5K, ↓2.5K)"},
		{"all zero", Paragraph{State: stateDone}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subagentSuffix(&tt.p, &zhCN); got != tt.want {
				t.Errorf("subagentSuffix = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderSubagentStreamLines(t *testing.T) {
	t.Run("muted preview", func(t *testing.T) {
		var sb strings.Builder
		renderSubagentStreamLines(&sb, "l1\nl2\nl3", 60, "", 5, true)
		if !strings.Contains(sb.String(), "l3") {
			t.Errorf("tail lines missing: %q", sb.String())
		}
	})
	t.Run("empty text renders nothing", func(t *testing.T) {
		var sb strings.Builder
		renderSubagentStreamLines(&sb, "", 60, "", 5, false)
		if sb.Len() != 0 {
			t.Errorf("expected empty, got %q", sb.String())
		}
	})
}

func TestRenderSubagentToolOutput(t *testing.T) {
	t.Run("collapsed tail-5 with ellipsis", func(t *testing.T) {
		var sb strings.Builder
		lines := make([]string, 10)
		for i := range lines {
			lines[i] = "line" + strings.Repeat("x", i)
		}
		renderSubagentToolOutput(&sb, strings.Join(lines, "\n"), 60, "", false)
		if !strings.Contains(sb.String(), "...") {
			t.Errorf("ellipsis missing: %q", sb.String())
		}
	})
	t.Run("empty output renders nothing", func(t *testing.T) {
		var sb strings.Builder
		renderSubagentToolOutput(&sb, "", 60, "", true)
		if sb.Len() != 0 {
			t.Errorf("expected empty, got %q", sb.String())
		}
	})
}

func TestRenderSubagentBody(t *testing.T) {
	t.Run("structured events", func(t *testing.T) {
		var sb strings.Builder
		p := &Paragraph{State: stateExpanded, SubagentEvents: []subagent.SubagentEvent{
			{Kind: subagent.SubagentThought, TextDelta: "thinking hard"},
			{Kind: subagent.SubagentToolStart, ToolName: "read", ToolArgs: `{"file_path":"a.go"}`},
			{Kind: subagent.SubagentToolStream, TextDelta: "skip me"}, // 展开态跳过流式块
			{Kind: subagent.SubagentToolResult, ToolResult: "file content"},
			{Kind: subagent.SubagentText, TextDelta: "final answer"},
		}}
		renderSubagentBody(&sb, p, 60, "")
		got := sb.String()
		for _, want := range []string{"thinking hard", "read", "file content", "final answer"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in:\n%s", want, got)
			}
		}
		if strings.Contains(got, "skip me") {
			t.Errorf("stream chunks should be skipped in expanded view: %q", got)
		}
	})

	t.Run("no events falls back to text", func(t *testing.T) {
		var sb strings.Builder
		p := &Paragraph{State: stateExpanded, Text: "plain text body"}
		renderSubagentBody(&sb, p, 60, "")
		if !strings.Contains(sb.String(), "plain text body") {
			t.Errorf("fallback text missing: %q", sb.String())
		}
	})
}

func TestRenderSubagentPara(t *testing.T) {
	t.Run("done collapsed with preview", func(t *testing.T) {
		var sb strings.Builder
		p := &Paragraph{
			Type: paraSubagent, State: stateDone,
			SubagentType: "Explore", SubagentPrompt: "find stuff",
			Text: "answer here", SubagentTurns: 2,
		}
		ctx := ViewportCtx{LC: &zhCN, Width: 80, Subagent: spinner.New()}
		renderSubagentPara(&sb, p, ctx)
		got := sb.String()
		if !strings.Contains(got, "Explore") || !strings.Contains(got, "find stuff") {
			t.Errorf("summary missing: %q", got)
		}
	})

	t.Run("streaming uses text tail", func(t *testing.T) {
		var sb strings.Builder
		p := &Paragraph{Type: paraSubagent, State: stateStreaming, SubagentType: "fork", Text: "streaming output"}
		ctx := ViewportCtx{LC: &zhCN, Width: 80, Subagent: spinner.New()}
		renderSubagentPara(&sb, p, ctx)
		if !strings.Contains(sb.String(), "streaming output") {
			t.Errorf("stream text missing: %q", sb.String())
		}
	})

	t.Run("error state", func(t *testing.T) {
		var sb strings.Builder
		p := &Paragraph{Type: paraSubagent, State: stateError, ToolError: "killed", SubagentType: "fork", Text: "err out"}
		ctx := ViewportCtx{LC: &zhCN, Width: 80, Subagent: spinner.New()}
		renderSubagentPara(&sb, p, ctx)
		if !strings.Contains(sb.String(), "err out") {
			t.Errorf("error text missing: %q", sb.String())
		}
	})
}

// ---------------------------------------------------------------------------
// Todo 面板渲染
// ---------------------------------------------------------------------------

func TestRenderTodoPanel(t *testing.T) {
	items := []todo.TodoItem{
		{ID: "1", Content: "done task", Status: "completed"},
		{ID: "2", Content: "doing task", Status: "in_progress"},
		{ID: "3", Content: "waiting task", Status: "pending"},
	}
	t.Run("empty list renders nothing", func(t *testing.T) {
		out, lines := renderTodoPanel(&zhCN, nil, 60, false, false, "/")
		if out != "" || lines != 0 {
			t.Errorf("empty = (%q, %d)", out, lines)
		}
	})
	t.Run("renders title and items sorted in_progress first", func(t *testing.T) {
		out, lines := renderTodoPanel(&zhCN, items, 60, false, false, "/")
		if !strings.Contains(out, zhCN.TodoTitle) && !strings.Contains(out, "1/3") {
			t.Errorf("title missing: %q", out)
		}
		if lines < 3 {
			t.Errorf("expected multiple lines, got %d", lines)
		}
		// in_progress 必须排在 completed 之前
		idxDoing := strings.Index(out, "doing task")
		idxDone := strings.Index(out, "done task")
		if idxDoing < 0 || idxDone < 0 || idxDoing > idxDone {
			t.Errorf("sort order wrong: doing=%d done=%d\n%s", idxDoing, idxDone, out)
		}
	})
	t.Run("collapsed hides items with summary", func(t *testing.T) {
		many := make([]todo.TodoItem, 8)
		for i := range many {
			many[i] = todo.TodoItem{ID: string(rune('a' + i)), Content: "task", Status: "pending"}
		}
		out, _ := renderTodoPanel(&zhCN, many, 60, false, false, "/")
		if !strings.Contains(out, "项隐藏") {
			t.Errorf("hidden summary missing: %q", out)
		}
	})
	t.Run("expanded shows all items", func(t *testing.T) {
		many := make([]todo.TodoItem, 8)
		for i := range many {
			many[i] = todo.TodoItem{ID: string(rune('a' + i)), Content: "task", Status: "pending"}
		}
		out, _ := renderTodoPanel(&zhCN, many, 60, true, false, "/")
		if strings.Contains(out, "项隐藏") {
			t.Errorf("expanded should not hide items: %q", out)
		}
	})
}

func TestRenderTodoItemAndSort(t *testing.T) {
	inProg := renderTodoItem(todo.TodoItem{Content: "a", Status: "in_progress"}, 30, "/")
	if !strings.Contains(inProg, "a") {
		t.Errorf("in_progress render: %q", inProg)
	}
	done := renderTodoItem(todo.TodoItem{Content: "b", Status: "completed"}, 30, "")
	if !strings.Contains(done, "b") {
		t.Errorf("completed render: %q", done)
	}
	pend := renderTodoItem(todo.TodoItem{Content: "c", Status: "pending"}, 30, "")
	if !strings.Contains(pend, "c") {
		t.Errorf("pending render: %q", pend)
	}

	todos := []todo.TodoItem{
		{Content: "d1", Status: "completed"},
		{Content: "d2", Status: "pending"},
		{Content: "d3", Status: "in_progress"},
		{Content: "d4", Status: "completed"},
	}
	sortTodos(todos)
	if todos[0].Status != "in_progress" || todos[1].Status != "pending" || todos[2].Status != "completed" {
		t.Errorf("sort order wrong: %+v", todos)
	}
}

func TestFormatHiddenSummary(t *testing.T) {
	hidden := []todo.TodoItem{
		{Status: "completed"}, {Status: "completed"}, {Status: "in_progress"},
	}
	got := formatHiddenSummary(&zhCN, hidden, 3)
	if !strings.Contains(got, "3 项隐藏") || !strings.Contains(got, "2 完成") || !strings.Contains(got, "1 进行中") {
		t.Errorf("summary = %q", got)
	}
}

// ---------------------------------------------------------------------------
// tui_styles.go — 前缀辅助
// ---------------------------------------------------------------------------

func TestPrefixHelpers(t *testing.T) {
	sp := spinner.New()
	if got := asstPrefix(sp, true); !strings.Contains(got, "·") {
		t.Errorf("asstPrefix = %q", got)
	}
	if got := thoughtPrefix(sp, false); !strings.Contains(got, "·") {
		t.Errorf("thoughtPrefix(done) = %q", got)
	}
	if got := thoughtPrefix(sp, true); got == "" {
		t.Error("thoughtPrefix(streaming) should render spinner frame")
	}
	if got := toolPrefix(sp, stateDone, false); !strings.Contains(got, "•") {
		t.Errorf("toolPrefix(done) = %q", got)
	}
	if got := toolPrefix(sp, stateStreaming, false); got == "" {
		t.Error("toolPrefix(streaming) should render spinner frame")
	}
	// fatal / recoverable 错误路径(仅验证非空)
	if got := toolPrefix(sp, stateError, true); got == "" {
		t.Error("toolPrefix(error fatal) empty")
	}
	if got := toolPrefix(sp, stateError, false); got == "" {
		t.Error("toolPrefix(error recoverable) empty")
	}
	for _, kind := range []systemNotifKind{notifInfo, notifWarn, notifError} {
		if got := systemPrefix(kind); !strings.Contains(got, "◼") {
			t.Errorf("systemPrefix(%d) = %q", kind, got)
		}
		_ = systemTextStyle(kind)
	}
}

// ---------------------------------------------------------------------------
// setup.go — saveAndFinish / needsSetup
// ---------------------------------------------------------------------------

func TestSaveAndFinish(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := newSetupModel(LocaleEnUS)
	m.state.apiKey = "sk-test-1234"
	m.state.prov = "kimi"
	m.state.model = "kimi-k3"
	m.state.subModel = "kimi-k2.7"
	m.state.baseURL = "https://api.kimi.com/coding/v1"
	m.saveAndFinish()

	wantPath := filepath.Join(home, ".waveloom", "settings.json")
	if m.state.configPath != wantPath {
		t.Errorf("configPath = %q, want %q", m.state.configPath, wantPath)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	llmMap, ok := cfg["llm"].(map[string]any)
	if !ok {
		t.Fatalf("llm section missing: %v", cfg)
	}
	profiles, ok := llmMap["profiles"].(map[string]any)
	if !ok {
		t.Fatalf("profiles missing: %v", llmMap)
	}
	kimi, ok := profiles["kimi"].(map[string]any)
	if !ok {
		t.Fatalf("kimi profile missing: %v", profiles)
	}
	if kimi["api_key"] != "sk-test-1234" || kimi["model"] != "kimi-k3" {
		t.Errorf("kimi profile wrong: %v", kimi)
	}
	if cfg["locale"] != "en-US" || cfg["theme"] != "auto" {
		t.Errorf("top-level fields wrong: %v", cfg)
	}
}

func TestNeedsSetup(t *testing.T) {
	t.Run("env var LLM_API_KEY satisfies", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("LLM_API_KEY", "sk-env")
		if needsSetup() {
			t.Error("LLM_API_KEY set → no setup needed")
		}
	})

	t.Run("global settings with key satisfies", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("LLM_API_KEY", "")
		raw, _ := json.Marshal(map[string]any{"llm": map[string]any{"api_key": "sk-x"}})
		p := filepath.Join(home, ".waveloom", "settings.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if needsSetup() {
			t.Error("global settings with key → no setup needed")
		}
	})

	t.Run("nothing configured requires setup", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("LLM_API_KEY", "")
		if !needsSetup() {
			t.Error("expected setup needed")
		}
	})
}

// ---------------------------------------------------------------------------
// main.go — loadHookRunner
// ---------------------------------------------------------------------------

func TestLoadHookRunner(t *testing.T) {
	t.Run("no hook files yields nil", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if got := loadHookRunner(); got != nil {
			t.Errorf("expected nil runner, got %v", got)
		}
	})

	t.Run("global hook config loads", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		raw := []byte(`{
			"hooks": {
				"PreToolUse": [
					{
						"matcher": "Bash",
						"hooks": [
							{"type": "command", "command": "echo hi", "timeout": 5000}
						]
					}
				]
			}
		}`)
		p := filepath.Join(home, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		runner := loadHookRunner()
		if runner == nil {
			t.Fatal("expected runner from global settings")
		}
	})
}
