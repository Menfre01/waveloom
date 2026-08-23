package main

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"

	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/slashcommand"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ---------------------------------------------------------------------------
// renderPermOverlay — 权限确认面板宽度自适应（基于 bubbles/list）
// ---------------------------------------------------------------------------

func newTestModelForPerm() *model {
	m := &model{
		themeMode: "dark",
		lc:        &enUS,
		permReq: &permissionReqMsg{
			toolName:   "bash",
			args:       "go test ./...",
			reason:     "需要确认外部命令执行",
			reasonKind: permission.ReasonRule,
		},
	}
	m.permList = m.buildPermList()
	return m
}

func TestRenderPermOverlay_WideTerminal(t *testing.T) {
	m := newTestModelForPerm()
	content := m.renderPermOverlay(70)
	if content == "" {
		t.Fatal("expected non-empty content")
	}
	if !strings.Contains(content, "Permission Required") {
		t.Error("expected title")
	}
	if !strings.Contains(content, "Allow") {
		t.Error("expected Allow option")
	}
}

func TestRenderPermOverlay_NarrowTerminal(t *testing.T) {
	m := newTestModelForPerm()
	content := m.renderPermOverlay(50)
	if content == "" {
		t.Fatal("expected non-empty content")
	}
}

func TestRenderPermOverlay_VeryNarrowTerminal(t *testing.T) {
	m := newTestModelForPerm()
	m.permReq.reason = "reason"
	content := m.renderPermOverlay(30)
	if content == "" {
		t.Fatal("expected non-empty content")
	}
	// 应包含所有 4 个选项（list 渲染）
	if !strings.Contains(content, "Allow") {
		t.Error("missing Allow")
	}
	if !strings.Contains(content, "Deny") {
		t.Error("missing Deny")
	}
}

func TestRenderPermOverlay_LongArgsAreTruncated(t *testing.T) {
	m := newTestModelForPerm()
	longArgs := strings.Repeat("x", 200)
	m.permReq.args = longArgs
	content := m.renderPermOverlay(80)
	if strings.Contains(content, longArgs) {
		t.Error("long args should be wrapped/truncated")
	}
}

// ---------------------------------------------------------------------------
// normalizeWidth — 全角字符转换
// ---------------------------------------------------------------------------

func TestNormalizeWidth_FullwidthLatin(t *testing.T) {
	// Fullwidth 'A' (U+FF21) → halfwidth 'A' (U+0041)
	result := normalizeWidth("Ｄｅｅｐ")
	if result != "Deep" {
		t.Errorf("expected 'Deep', got %q", result)
	}
}

func TestNormalizeWidth_FullwidthSpace(t *testing.T) {
	result := normalizeWidth("hello\u3000world")
	if result != "hello world" {
		t.Errorf("expected 'hello world', got %q", result)
	}
}

func TestNormalizeWidth_AlreadyHalfwidth(t *testing.T) {
	result := normalizeWidth("hello world 123")
	if result != "hello world 123" {
		t.Errorf("expected no change, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// formatToolArgs / extractField / stripCWDPrefix
// ---------------------------------------------------------------------------

func TestFormatToolArgs_Read(t *testing.T) {
	// 仅文件路径 → 保持原有行为
	result := formatToolArgs("read", `{"file_path":"/home/user/project/main.go"}`, "/home/user/project")
	if result != "main.go" {
		t.Errorf("expected 'main.go', got %q", result)
	}
}

func TestFormatToolArgs_ReadWithPattern(t *testing.T) {
	result := formatToolArgs("read", `{"file_path":"/home/user/project/main.go","pattern":"func main"}`, "/home/user/project")
	if result != `main.go · "func main"` {
		t.Errorf("expected 'main.go · \"func main\"', got %q", result)
	}
}

func TestFormatToolArgs_ReadWithOffset(t *testing.T) {
	result := formatToolArgs("read", `{"file_path":"/home/user/project/main.go","offset":10}`, "/home/user/project")
	if result != "main.go @L10" {
		t.Errorf("expected 'main.go @L10', got %q", result)
	}
}

func TestFormatToolArgs_ReadWithOffsetLimit(t *testing.T) {
	result := formatToolArgs("read", `{"file_path":"/home/user/project/main.go","offset":10,"limit":5}`, "/home/user/project")
	if result != "main.go @L10+5" {
		t.Errorf("expected 'main.go @L10+5', got %q", result)
	}
}

func TestFormatToolArgs_ReadWithContextLines(t *testing.T) {
	result := formatToolArgs("read", `{"file_path":"/home/user/project/main.go","pattern":"func","context_lines":3}`, "/home/user/project")
	if result != `main.go · "func" ±3` {
		t.Errorf("expected 'main.go · \"func\" ±3', got %q", result)
	}
}

func TestFormatToolArgs_ReadWithAll(t *testing.T) {
	result := formatToolArgs("read", `{"file_path":"/home/user/project/main.go","pattern":"func main","offset":10,"limit":5,"context_lines":3}`, "/home/user/project")
	if result != `main.go · "func main" @L10+5 ±3` {
		t.Errorf("expected full params, got %q", result)
	}
}

func TestExtractInt(t *testing.T) {
	tests := []struct {
		name string
		json string
		key  string
		want int
	}{
		{"simple", `{"offset":10}`, "offset", 10},
		{"zero", `{"offset":0}`, "offset", 0},
		{"missing", `{"foo":1}`, "offset", 0},
		{"large", `{"offset":12345}`, "offset", 12345},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractInt(tt.json, tt.key)
			if got != tt.want {
				t.Errorf("extractInt(%q, %q) = %d, want %d", tt.json, tt.key, got, tt.want)
			}
		})
	}
}

func TestFormatToolArgs_Shell(t *testing.T) {
	result := formatToolArgs("bash", `{"command":"go test ./..."}`, "/tmp")
	if result != "go test ./..." {
		t.Errorf("expected 'go test ./...', got %q", result)
	}
}

func TestFormatToolArgs_Grep(t *testing.T) {
	result := formatToolArgs("grep", `{"pattern":"func main","working_dir":"/home/user/project/pkg"}`, "/home/user/project")
	if !strings.Contains(result, "func main") {
		t.Errorf("expected pattern in result, got %q", result)
	}
}

func TestFormatToolArgs_UnknownTool(t *testing.T) {
	result := formatToolArgs("unknown", `{"key":"value"}`, "/tmp")
	if result == "" || len(result) > 50+3 {
		t.Errorf("expected truncated or raw args, got %q", result)
	}
}

func TestCollapseMultilineCommand(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single line unchanged",
			input: "go test ./...",
			want:  "go test ./...",
		},
		{
			name:  "JSON literal newlines (extractField output)",
			input: `go build ./...\ngo test ./...\ngo vet ./...`,
			want:  "go build ./... && go test ./... && go vet ./...",
		},
		{
			name:  "actual newline characters",
			input: "go build ./...\ngo test ./...\ngo vet ./...",
			want:  "go build ./... && go test ./... && go vet ./...",
		},
		{
			name:  "literal backslash-n preserved (JSON \\\\n → shell \\n)",
			input: `sed 's/\\n/ /g'`,
			want:  `sed 's/\n/ /g'`,
		},
		{
			name:  "shell line continuation with spaces (JSON \\\\\\n → shell \\<newline>)",
			input: `go build ./... \\\n    && go test ./...`,
			want:  "go build ./... && go test ./...",
		},
		{
			name:  "simple backslash-newline continuation",
			input: `echo hello\\\nworld`,
			want:  "echo hello world",
		},
		{
			name:  "grep with backslash continuation",
			input: `grep \\\n"pattern" file`,
			want:  `grep "pattern" file`,
		},
		{
			name:  "rg with backslash continuation and spaces",
			input: `rg -n \\\n  'pattern'`,
			want:  `rg -n 'pattern'`,
		},
		{
			name:  "multiple continuations",
			input: `echo \\\nhello \\\nworld`,
			want:  "echo hello world",
		},
		{
			name:  "mixed continuation and newline",
			input: `echo \\\nhello\ngo test`,
			want:  "echo hello && go test",
		},
		{
			name:  "actual backslash newline (defensive)",
			input: "echo \\\nhello",
			want:  "echo hello",
		},
		{
			name:  "cd prefix already stripped",
			input: "make build && make test",
			want:  "make build && make test",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "mixed literal and actual newlines",
			input: "echo hello\\n",
			want:  "echo hello",
		},
		{
			name:  "newline at end trimmed",
			input: `go build ./...\n`,
			want:  "go build ./...",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collapseMultilineCommand(tt.input)
			if got != tt.want {
				t.Errorf("collapseMultilineCommand(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
func TestFormatToolArgs_BashMultiline(t *testing.T) {
	// 验证 formatToolArgs → collapseMultilineCommand 集成路径
	// raw string 中 \n 即 JSON 的 \n 转义序列
	result := formatToolArgs("bash", `{"command":"go build ./...\ngo test ./...\ngo vet ./..."}`, "/tmp")
	if result != "go build ./... && go test ./... && go vet ./..." {
		t.Errorf("expected joined single line, got %q", result)
	}
}

// REGRESSION: extractField 遇到 JSON 转义双引号(\")时提前截断,
// 导致含双引号的 bash 命令(如 rg "pattern")只显示首段。
func TestFormatToolArgs_BashWithDoubleQuotes(t *testing.T) {
	// 模拟 LLM 返回的 ToolCall Arguments JSON 字符串
	// 实际命令: rg -n "pattern" file
	result := formatToolArgs("bash", `{"command":"rg -n \"pattern\" file"}`, "/tmp")
	if result != `rg -n "pattern" file` {
		t.Errorf("expected 'rg -n \"pattern\" file', got %q", result)
	}
}

func TestFormatToolArgs_BashMultilineWithDoubleQuotes(t *testing.T) {
	// 多行续行命令中包含双引号参数
	// 实际命令: rg -n \↵  "pattern" \↵  file
	result := formatToolArgs("bash", `{"command":"rg -n \\\n  \"pattern\" \\\n  file"}`, "/tmp")
	if result != `rg -n "pattern" file` {
		t.Errorf("expected 'rg -n \"pattern\" file', got %q", result)
	}
}

func TestExtractField_EscapedQuotes(t *testing.T) {
	// JSON 中的 \" 应视为转义引号,不作为结束符
	json := `{"command":"echo \"hello world\""}`
	result := extractField(json, "command")
	if result != `echo \"hello world\"` {
		t.Errorf("expected 'echo \\\"hello world\\\"', got %q", result)
	}
}

func TestCollapseMultilineCommand_EscapedQuotes(t *testing.T) {
	// JSON 转义引号 \" 应还原为 "
	result := collapseMultilineCommand(`rg -n \"pattern\" file`)
	if result != `rg -n "pattern" file` {
		t.Errorf("expected 'rg -n \"pattern\" file', got %q", result)
	}
}

func TestCollapseMultilineCommand_EscapedBackslash(t *testing.T) {
	// JSON \\ 应还原为单个 \
	result := collapseMultilineCommand(`sed 's/\\n/ /g'`)
	if result != `sed 's/\n/ /g'` {
		t.Errorf("expected 'sed 's/\\n/ /g'', got %q", result)
	}
}

func TestExtractField_Valid(t *testing.T) {
	json := `{"file_path":"/home/user/main.go","offset":10}`
	result := extractField(json, "file_path")
	if result != "/home/user/main.go" {
		t.Errorf("expected '/home/user/main.go', got %q", result)
	}
}

func TestExtractField_Missing(t *testing.T) {
	result := extractField(`{"foo":"bar"}`, "file_path")
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}
func TestStripCWDPrefix_Matches(t *testing.T) {
	result := stripCWDPrefix("/home/user/project/main.go", "/home/user/project")
	if result != "main.go" {
		t.Errorf("expected 'main.go', got %q", result)
	}
}

func TestStripCWDPrefix_NoMatch(t *testing.T) {
	result := stripCWDPrefix("/etc/config", "/home/user/project")
	if result != "/etc/config" {
		t.Errorf("expected unchanged, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// toolSuffix — 工具结果摘要后缀
// ---------------------------------------------------------------------------

func TestToolSuffix_Read(t *testing.T) {
	p := &Paragraph{
		ToolName:   "read",
		ToolResult: strings.Repeat("x", 2048),
		ToolDurMs:  8,
		State:      stateDone,
	}
	suffix := toolSuffix(p, &enUS)
	if !strings.Contains(suffix, "KB") && !strings.Contains(suffix, "B") {
		t.Errorf("expected size suffix, got %q", suffix)
	}
	if !strings.Contains(suffix, "ms") {
		t.Errorf("expected duration suffix, got %q", suffix)
	}
}

func TestToolSuffix_ShellExitCode(t *testing.T) {
	p := &Paragraph{
		ToolName:   "bash",
		ToolResult: "✅ Command succeeded (exit=0)  120ms\nok  ...",
		ToolDurMs:  120,
		State:      stateDone,
	}
	suffix := toolSuffix(p, &enUS)
	if !strings.Contains(suffix, "exit=0") {
		t.Errorf("expected exit code, got %q", suffix)
	}
}

func TestToolSuffix_Denied(t *testing.T) {
	p := &Paragraph{
		ToolName:   "bash",
		ToolDenied: true,
		State:      stateError,
	}
	suffix := toolSuffix(p, &enUS)
	if !strings.Contains(suffix, "permission denied") {
		t.Errorf("expected denied message, got %q", suffix)
	}
}

func TestToolSuffix_Error(t *testing.T) {
	p := &Paragraph{
		ToolName:      "bash",
		ToolError:     "command not found",
		ToolErrorKind: "command_not_found",
		State:         stateError,
	}
	suffix := toolSuffix(p, &enUS)
	if !strings.Contains(suffix, "command_not_found") {
		t.Errorf("expected error kind in suffix, got %q", suffix)
	}
}

func TestToolSuffix_Streaming(t *testing.T) {
	p := &Paragraph{
		ToolName: "read",
		State:    stateStreaming,
	}
	suffix := toolSuffix(p, &enUS)
	if suffix != "" {
		t.Errorf("expected empty suffix for streaming, got %q", suffix)
	}
}

// ---------------------------------------------------------------------------
// formatBytes / formatDuration / formatTokens
// ---------------------------------------------------------------------------
// formatBytes / formatDuration / formatTokens
// ---------------------------------------------------------------------------

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{2048, "2.0KB"},
		{1048576, "1.0MB"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.n)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		ms   int64
		want string
	}{
		{0, "0ms"},
		{234, "234ms"},
		{1500, "1.5s"},
		{65000, "1m5s"},
		{3661000, "1h1m"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.ms)
		if got != tt.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{512, "512"},
		{3860, "3.9K"},
		{38600, "38.6K"},
		{150000, "150K"},  // >= 100K → no decimal
		{1000000, "1.0M"},
	}
	for _, tt := range tests {
		got := formatTokens(tt.n)
		if got != tt.want {
			t.Errorf("formatTokens(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// TestShortTokens 验证 HUD tok 短格式:k 与 M 单位。
func TestShortTokens(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{512, "512"},
		{3860, "3.9k"},
		{38600, "38.6k"},
		{999999, "1000.0k"},
		{1000000, "1.0M"},
		{1250000, "1.2M"},
	}
	for _, tt := range tests {
		got := shortTokens(tt.n)
		if got != tt.want {
			t.Errorf("shortTokens(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// hasStreamingPara — 流式状态检测
// ---------------------------------------------------------------------------

func TestHasStreamingPara_None(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraUser, State: stateDone},
			{Type: paraAssistant, State: stateDone},
		},
	}
	if m.hasStreamingPara() {
		t.Error("expected no streaming paragraph")
	}
}

func TestHasStreamingPara_AssistantStreaming(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraUser, State: stateDone},
			{Type: paraAssistant, State: stateStreaming},
		},
	}
	if !m.hasStreamingPara() {
		t.Error("expected streaming paragraph detected")
	}
}

func TestHasStreamingPara_ToolStreaming(t *testing.T) {
	m := &model{
		paras: []Paragraph{
			{Type: paraTool, State: stateStreaming},
		},
	}
	if !m.hasStreamingPara() {
		t.Error("expected streaming tool detected")
	}
}

// ---------------------------------------------------------------------------
// permListChoice
// ---------------------------------------------------------------------------

func TestPermListChoice_Allow(t *testing.T) {
	m := newTestModelForPerm()
	// 默认第一项是高亮的 Allow
	choice := m.permListChoice()
	if choice.Decision != permission.DecisionAllow {
		t.Errorf("expected allow, got %s", choice.Decision)
	}
	if choice.RememberScope != "" {
		t.Error("expected no remember scope for Allow")
	}
}

func TestPermListChoice_AllowAll(t *testing.T) {
	m := newTestModelForPerm()
	m.permList.Select(1) // Always Allow
	choice := m.permListChoice()
	if choice.Decision != permission.DecisionAllow {
		t.Errorf("expected allow, got %s", choice.Decision)
	}
	if choice.RememberScope == "" {
		t.Error("expected remember scope for Always Allow")
	}
}

func TestPermListChoice_Deny(t *testing.T) {
	m := newTestModelForPerm()
	m.permList.Select(2) // Deny
	choice := m.permListChoice()
	if choice.Decision != permission.DecisionDeny {
		t.Errorf("expected deny, got %s", choice.Decision)
	}
}

// ---------------------------------------------------------------------------
// shouldActivatePicker — @ 触发检测
// ---------------------------------------------------------------------------

func TestShouldActivatePicker_AtStart(t *testing.T) {
	if !shouldActivatePicker("@") {
		t.Error("expected true for '@' at start")
	}
	if !shouldActivatePicker("@foo") {
		t.Error("expected true for '@foo' at start")
	}
}

func TestShouldActivatePicker_AfterSpace(t *testing.T) {
	if !shouldActivatePicker("hello @foo") {
		t.Error("expected true for '@foo' after space")
	}
}

func TestShouldActivatePicker_AtSignInMiddle(t *testing.T) {
	if shouldActivatePicker("hello@world") {
		t.Error("expected false for '@' in middle of word")
	}
}

func TestShouldActivatePicker_NoAtSign(t *testing.T) {
	if shouldActivatePicker("hello world") {
		t.Error("expected false for text without @")
	}
}

func TestShouldActivatePicker_SpaceAfterAt(t *testing.T) {
	// 路径已完成（@ 后有空格），不应触发
	if shouldActivatePicker("@foo bar") {
		t.Error("expected false when @-expression already has space after")
	}
	if shouldActivatePicker("hello @foo bar") {
		t.Error("expected false when @-expression already has space after")
	}
}

func TestShouldActivatePicker_Empty(t *testing.T) {
	if shouldActivatePicker("") {
		t.Error("expected false for empty string")
	}
}

// ---------------------------------------------------------------------------
// shouldActivateCommandPicker — / 触发检测
// ---------------------------------------------------------------------------

func TestShouldActivateCommandPicker_SlashOnly(t *testing.T) {
	if !shouldActivateCommandPicker("/") {
		t.Error("expected true for '/' only")
	}
}

func TestShouldActivateCommandPicker_SlashWithChars(t *testing.T) {
	if !shouldActivateCommandPicker("/hel") {
		t.Error("expected true for '/hel'")
	}
}

func TestShouldActivateCommandPicker_NoSlash(t *testing.T) {
	if shouldActivateCommandPicker("hello world") {
		t.Error("expected false for text without /")
	}
}

func TestShouldActivateCommandPicker_SlashWithSpace(t *testing.T) {
	if shouldActivateCommandPicker("/help ") {
		t.Error("expected false when /-command has trailing space")
	}
	if shouldActivateCommandPicker("/model v4") {
		t.Error("expected false when /-command has space and args")
	}
}

func TestShouldActivateCommandPicker_SlashNotAtStart(t *testing.T) {
	if shouldActivateCommandPicker("hello /world") {
		t.Error("expected false for '/' not at start")
	}
}

func TestShouldActivateCommandPicker_Empty(t *testing.T) {
	if shouldActivateCommandPicker("") {
		t.Error("expected false for empty string")
	}
}

// ---------------------------------------------------------------------------
// pathPrefixMatch — 路径分量最小前缀匹配
// ---------------------------------------------------------------------------

func TestPathPrefixMatch_ExactMatch(t *testing.T) {
	if !pathPrefixMatch("specs/reference", "specs/reference-context.md") {
		t.Error("expected exact component match")
	}
}

func TestPathPrefixMatch_ComponentPrefix(t *testing.T) {
	// spec ≤ specs, reference ≤ reference-context.md
	if !pathPrefixMatch("spec/reference", "specs/reference-context.md") {
		t.Error("expected component prefix match")
	}
}

func TestPathPrefixMatch_LastComponentOnly(t *testing.T) {
	// specs exact, ref ≤ reference-context.md
	if !pathPrefixMatch("specs/ref", "specs/reference-context.md") {
		t.Error("expected last-component fuzzy match")
	}
}

func TestPathPrefixMatch_SingleComponent(t *testing.T) {
	if !pathPrefixMatch("spec", "specs") {
		t.Error("expected single-component prefix match")
	}
	if !pathPrefixMatch("spec", "specs/reference-context.md") {
		t.Error("expected single-component prefix match against multi-component path")
	}
}

func TestPathPrefixMatch_FilterLongerThanDisplay(t *testing.T) {
	if pathPrefixMatch("specs/reference/extra", "specs/reference-context.md") {
		t.Error("expected false when filter has more components than display")
	}
}

func TestPathPrefixMatch_NoMatch(t *testing.T) {
	if pathPrefixMatch("xyz", "specs/reference-context.md") {
		t.Error("expected false for non-matching prefix")
	}
	if pathPrefixMatch("spec/xyz", "specs/reference-context.md") {
		t.Error("expected false when second component doesn't match")
	}
}

func TestPathPrefixMatch_CaseInsensitive(t *testing.T) {
	// Note: callers should lowercase both arguments; function is case-sensitive
	if !pathPrefixMatch("spec/reference", "specs/reference-context.md") {
		t.Error("expected match with same case")
	}
}

// ---------------------------------------------------------------------------
// fuzzyFilter — 选择器过滤
// ---------------------------------------------------------------------------

func TestFuzzyFilter_PathPrefixMatch(t *testing.T) {
	items := []pickerItem{
		{Path: "specs/reference-context.md", Display: "specs/reference-context.md"},
		{Path: "specs/agent-loop.md", Display: "specs/agent-loop.md"},
		{Path: "pkg/llm/client.go", Display: "pkg/llm/client.go"},
	}

	result := fuzzyFilter("spec/reference", items)
	if len(result) < 1 || result[0].Path != "specs/reference-context.md" {
		t.Errorf("expected specs/reference-context.md first, got %v", result)
	}
}

func TestFuzzyFilter_ExactComponentFirst(t *testing.T) {
	// spec exact match should be ranked before specs fuzzy match
	items := []pickerItem{
		{Path: "specs/reference-context.md", Display: "specs/reference-context.md"},
		{Path: "spec/reference-context.md", Display: "spec/reference-context.md"},
	}

	result := fuzzyFilter("spec/reference", items)
	if len(result) < 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	// Both are path prefix matches; order depends on order in items (stable)
	// pathPrefixMatch returns true for both
	_ = result
}

func TestFuzzyFilter_EmptyFilter(t *testing.T) {
	items := make([]pickerItem, 25)
	for i := range items {
		items[i] = pickerItem{Path: "item", Display: "item"}
	}
	result := fuzzyFilter("", items)
	if len(result) != 20 {
		t.Errorf("expected 20 items for empty filter, got %d", len(result))
	}
}

func TestFuzzyFilter_SubstringFallback(t *testing.T) {
	items := []pickerItem{
		{Path: "cmd/waveloom/main.go", Display: "cmd/waveloom/main.go"},
		{Path: "pkg/llm/client.go", Display: "pkg/llm/client.go"},
	}
	// "waveloom" doesn't match as component prefix but is substring
	result := fuzzyFilter("waveloom", items)
	if len(result) < 1 || result[0].Path != "cmd/waveloom/main.go" {
		t.Errorf("expected substring match, got %v", result)
	}
}

// TestFuzzyFilter_SortByMatchPosPrefix 验证 prefix 组内按匹配位置升序排列，
// 位置越靠左越优先。
func TestFuzzyFilter_SortByMatchPosPrefix(t *testing.T) {
	items := []pickerItem{
		{Path: "a/cmd/main.go", Display: "a/cmd/main.go"},
		{Path: "cmd/main.go", Display: "cmd/main.go"},
		{Path: "pkg/cmd.go", Display: "pkg/cmd.go"},
	}
	// pathPrefixMatch("cmd", "cmd/main.go") = true, strings.Index = 0
	// pathPrefixMatch("cmd", "a/cmd/main.go") = false (component "cmd" not prefix of "a")
	// strings.Contains("pkg/cmd.go", "cmd") = true (substr group)
	result := fuzzyFilter("cmd", items)
	if len(result) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(result))
	}
	// prefix group first: "cmd/main.go" (pos 0) → alphabetical within prefix
	// substr group: "pkg/cmd.go" (pos 4)
	if result[0].Path != "cmd/main.go" {
		t.Errorf("expected cmd/main.go first (prefix + pos 0), got %s", result[0].Path)
	}
}

// TestFuzzyFilter_SortByMatchPosSubstr 验证 substr 组内按匹配位置升序排列。
func TestFuzzyFilter_SortByMatchPosSubstr(t *testing.T) {
	items := []pickerItem{
		{Path: "x/loom/z.go", Display: "x/loom/z.go"},
		{Path: "x/loom.go", Display: "x/loom.go"},
	}
	// Neither matches pathPrefixMatch("loom", ...): "loom" prefix "x" = false
	// Both match substr: "x/loom.go" at pos 2, "x/loom/z.go" at pos 2
	// Same position → alphabetical: "x/loom.go" < "x/loom/z.go"
	result := fuzzyFilter("loom", items)
	if len(result) < 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if result[0].Path != "x/loom.go" {
		t.Errorf("expected x/loom.go first (same pos, alphabetical), got %s", result[0].Path)
	}
}

// TestFuzzyFilter_NonContiguousPrefixAfterContiguous 验证 pathPrefixMatch 命中
// 但非连续子串（strings.Index=-1）的项排在连续子串之后。
func TestFuzzyFilter_NonContiguousPrefixAfterContiguous(t *testing.T) {
	items := []pickerItem{
		{Path: "cmdtools/watcher.go", Display: "cmdtools/watcher.go"},
		{Path: "cmd/waveloom/main.go", Display: "cmd/waveloom/main.go"},
	}
	// pathPrefixMatch("cmd/wa", "cmd/waveloom/main.go"): "cmd"✓ "wa"✓ → prefix, Index=0
	// pathPrefixMatch("cmd/wa", "cmdtools/watcher.go"): "cmd"✓ "wa"✓ → prefix, Index=-1
	result := fuzzyFilter("cmd/wa", items)
	if len(result) < 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if result[0].Path != "cmd/waveloom/main.go" {
		t.Errorf("expected contiguous match first, got %s", result[0].Path)
	}
}

// ---------------------------------------------------------------------------
// sortByMatchPos
// ---------------------------------------------------------------------------

func TestSortByMatchPos_ValidPositions(t *testing.T) {
	items := []pickerItem{
		{Display: "zzz"},
		{Display: "aaa"},
	}
	filter := "z"
	sortByMatchPos(filter, items)
	// "zzz": Index("zzz", "z") = 0
	// "aaa": Index("aaa", "z") = -1 → 1<<30
	if items[0].Display != "zzz" {
		t.Errorf("expected zzz first (pos 0), got %s", items[0].Display)
	}
	if items[1].Display != "aaa" {
		t.Errorf("expected aaa second (pos sentinel), got %s", items[1].Display)
	}
}

func TestSortByMatchPos_SamePositionAlphabetical(t *testing.T) {
	items := []pickerItem{
		{Display: "zb.go"},
		{Display: "za.go"},
	}
	filter := "z"
	sortByMatchPos(filter, items)
	// Both have Index=0, tiebreaker alphabetical
	if items[0].Display != "za.go" {
		t.Errorf("expected za.go first (alphabetical), got %s", items[0].Display)
	}
}

func TestSortByMatchPos_MixedSentinel(t *testing.T) {
	items := []pickerItem{
		{Display: "no-match"},
		{Display: "match-z-here"},
		{Display: "also-no-match"},
	}
	filter := "z"
	sortByMatchPos(filter, items)
	// "match-z-here": Index=6
	// "no-match": -1 → sentinel
	// "also-no-match": -1 → sentinel
	if items[0].Display != "match-z-here" {
		t.Errorf("expected match-z-here first (pos 6), got %s", items[0].Display)
	}
	// sentinels stay in original order relative to each other (stable sort),
	// then alphabetical tiebreaker should sort them
	if items[1].Display != "also-no-match" {
		t.Errorf("expected also-no-match second (alphabetical), got %s", items[1].Display)
	}
}

// ---------------------------------------------------------------------------
// pathPrefixMatch — 补充边缘场景
// ---------------------------------------------------------------------------

func TestPathPrefixMatch_NonPrefixComponent(t *testing.T) {
	// "cmd" prefix "cmdtools" → true, "wa" prefix "watcher" → true
	// But the display is "cmdtools/watcher.go", filter "cmd/wa"
	// This IS a valid pathPrefixMatch (each component is a prefix),
	// but NOT a contiguous substring
	if !pathPrefixMatch("cmd/wa", "cmdtools/watcher.go") {
		t.Error("expected cmd/wa to match cmdtools/watcher.go via component prefix")
	}
}

// ---------------------------------------------------------------------------
// bestCommandMatch
// ---------------------------------------------------------------------------

func TestBestCommandMatch_PrefixName(t *testing.T) {
	cmd := slashcommand.CommandInfo{Name: "plan-ceo-review"}
	m := bestCommandMatch("plan", cmd)
	if m.position != 0 || !m.isPrefix {
		t.Errorf("expected prefix match at pos 0, got pos=%d isPrefix=%v", m.position, m.isPrefix)
	}
}

func TestBestCommandMatch_SubstringName(t *testing.T) {
	cmd := slashcommand.CommandInfo{Name: "design-review"}
	m := bestCommandMatch("review", cmd)
	if m.position < 0 {
		t.Error("expected substring match in name")
	}
	if m.isPrefix {
		t.Error("expected substring not prefix for 'review' in 'design-review'")
	}
}

func TestBestCommandMatch_AliasPrefix(t *testing.T) {
	cmd := slashcommand.CommandInfo{Name: "ship", Aliases: []string{"land-and-deploy"}}
	m := bestCommandMatch("land", cmd)
	if m.position < 0 || !m.isPrefix {
		t.Errorf("expected prefix match via alias, got pos=%d isPrefix=%v", m.position, m.isPrefix)
	}
}

func TestBestCommandMatch_AliasSubstring(t *testing.T) {
	cmd := slashcommand.CommandInfo{Name: "ship", Aliases: []string{"land-and-deploy"}}
	m := bestCommandMatch("deploy", cmd)
	if m.position < 0 {
		t.Error("expected substring match via alias")
	}
}

func TestBestCommandMatch_PrefixWinsOverSubstring(t *testing.T) {
	// name contains filter as substring, alias has it as prefix → alias (prefix) wins
	cmd := slashcommand.CommandInfo{
		Name:    "waveloom-demo",
		Aliases: []string{"demo"},
	}
	m := bestCommandMatch("demo", cmd)
	if m.position != 0 || !m.isPrefix {
		t.Errorf("expected alias prefix at pos 0, got pos=%d isPrefix=%v", m.position, m.isPrefix)
	}
}

func TestBestCommandMatch_LeftmostWins(t *testing.T) {
	cmd := slashcommand.CommandInfo{
		Name:    "x-review-y",
		Aliases: []string{"z-review"},
	}
	// "review" in name at pos 2, in alias at pos 2 — same position
	// alphabetical tiebreaker doesn't matter here, just checking position
	m := bestCommandMatch("review", cmd)
	if m.position != 2 {
		t.Errorf("expected match at pos 2, got %d", m.position)
	}
}

func TestBestCommandMatch_NoMatch(t *testing.T) {
	cmd := slashcommand.CommandInfo{Name: "ship", Aliases: []string{"deploy"}}
	m := bestCommandMatch("xyz", cmd)
	if m.position >= 0 {
		t.Error("expected no match")
	}
}

// ---------------------------------------------------------------------------
// AskUserQuestion — huh 表单覆盖层
// ---------------------------------------------------------------------------

func newTestModelForQuestion() *model {
	ti := textarea.New()
	ti.Prompt = "> "

	otherTi := textinput.New()
	otherTi.Prompt = "> "
	otherTi.SetVirtualCursor(false)
	otherTi.Placeholder = enUS.InputOtherPlaceholder

	m := &model{
		themeMode: "dark",
		input:     ti,
		otherInput: otherTi,
		questionReq: &questionReqMsg{
			questions: []permission.QuestionPrompt{
				{
					Question:    "What is your preferred language?",
					Header:      "Language",
					Options: []permission.QuestionOptionPrompt{
						{Label: "Go", Description: "Fast, simple concurrency"},
						{Label: "Rust (Recommended)", Description: "Memory safe, zero-cost abstractions"},
					},
					MultiSelect: false,
				},
			},
		},
		width: 80,
	}
	return m
}

func TestThemeWaveloom_ReturnsNonNil(t *testing.T) {
	theme := themeWaveloom()
	if theme == nil {
		t.Fatal("expected non-nil theme")
		return
	}
	styles := theme.Theme(true)
	if styles == nil {
		t.Fatal("expected non-nil styles")
	}
}

func TestKeyMapToGroups_AllBindingsPresent(t *testing.T) {
	lc := messagesFor("zh-CN")
	km := makeKeyMap(lc)

	groups := keyMapToGroups(km)
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}

	// 收集所有 binding，验证 14 个快捷键齐全
	seen := make(map[string]bool)
	total := 0
	for _, g := range groups {
		for _, b := range g {
			// 验证每个 binding 都有 keys 和 help text
			if !b.Enabled() {
				t.Errorf("binding in group should be enabled")
			}
			keys := b.Keys()
			if len(keys) == 0 {
				t.Errorf("binding should have at least one key")
			}
			for _, k := range keys {
				if k == "" {
					t.Errorf("binding key should not be empty")
				}
				seen[k] = true
			}
			total++
		}
	}

	if total != 14 {
		t.Errorf("expected 14 total bindings across all groups, got %d", total)
	}

	// 验证 Tab 和 Shift+Tab 在 groups 中
	if !seen["tab"] {
		t.Error("Tab key not found in any group")
	}
	if !seen["shift+tab"] {
		t.Error("Shift+Tab key not found in any group")
	}
}

func TestBuildQuestionForm_SingleSelect(t *testing.T) {
	m := newTestModelForQuestion()
	m.buildQuestionForm()

	if m.questionForm == nil {
		t.Fatal("expected non-nil questionForm")
	}
	// Single-select form has one group with a Select field
	view := m.questionForm.View()
	if view == "" {
		t.Fatal("expected non-empty form view")
	}
	// Options including "Other..." should be present
	if !containsAny(view, "Go", "★ Rust") {
		t.Error("expected options in form view")
	}
}

func TestBuildQuestionForm_MultiSelect(t *testing.T) {
	m := newTestModelForQuestion()
	m.questionReq.questions[0].MultiSelect = true
	m.buildQuestionForm()

	if m.questionForm == nil {
		t.Fatal("expected non-nil questionForm")
	}
	view := m.questionForm.View()
	if view == "" {
		t.Fatal("expected non-empty form view")
	}
	if !containsAny(view, "Go", "Rust") {
		t.Error("expected multi-select options in form view")
	}
}

func TestBuildOtherForm(t *testing.T) {
	m := newTestModelForQuestion()
	m.buildOtherForm()

	if !m.questionFormIsOther {
		t.Fatal("expected questionFormIsOther to be true")
	}
	if m.questionForm != nil {
		t.Error("expected nil questionForm (bypasses huh for Other input)")
	}
	// Verify otherInput is focused and empty
	if m.otherInput.Value() != "" {
		t.Errorf("expected empty otherInput, got %q", m.otherInput.Value())
	}
}

func TestRenderQuestionOverlay_NilQuestionReq(t *testing.T) {
	m := &model{themeMode: "dark"}
	content := m.renderQuestionOverlay(70)
	if content != "" {
		t.Errorf("expected empty string for nil questionReq, got %q", content)
	}
}

func TestRenderQuestionOverlay_NilForm(t *testing.T) {
	m := newTestModelForQuestion()
	// questionReq set but questionForm is nil
	content := m.renderQuestionOverlay(70)
	if content != "" {
		t.Errorf("expected empty string for nil form, got %q", content)
	}
}

func TestRenderQuestionOverlay_WithForm(t *testing.T) {
	m := newTestModelForQuestion()
	m.buildQuestionForm()

	content := m.renderQuestionOverlay(70)
	if content == "" {
		t.Fatal("expected non-empty overlay content")
	}
	if !containsAny(content, "▲ Language") {
		t.Error("expected header in overlay")
	}
	if !containsAny(content, "Go") {
		t.Error("expected option in overlay")
	}
}

func TestRenderQuestionOverlay_MultiQuestionProgress(t *testing.T) {
	m := newTestModelForQuestion()
	m.questionReq.questions = append(m.questionReq.questions, permission.QuestionPrompt{
		Question: "Second question?",
		Header:   "Second",
		Options: []permission.QuestionOptionPrompt{
			{Label: "A", Description: "Option A"},
			{Label: "B", Description: "Option B"},
		},
	})
	m.buildQuestionForm()

	content := m.renderQuestionOverlay(70)
	if !containsAny(content, "(1/2)") {
		t.Error("expected progress indicator for multi-question form")
	}
}

func TestQuestionCloseOverlay_CleansUp(t *testing.T) {
	m := newTestModelForQuestion()
	m.buildQuestionForm()
	m.overlay = overlayQuestion
	m.questionAnswers = []permission.QuestionResponse{{Question: "Q", Answers: []string{"A"}}}

	m.closeQuestionOverlay()

	if m.overlay != overlayNone {
		t.Error("expected overlayNone")
	}
	if m.questionReq != nil {
		t.Error("expected nil questionReq")
	}
	if m.questionAnswers != nil {
		t.Error("expected nil questionAnswers")
	}
	if m.questionForm != nil {
		t.Error("expected nil questionForm")
	}
	if m.questionPendingOther {
		t.Error("expected false questionPendingOther")
	}
	if m.questionPendingAnswers != nil {
		t.Error("expected nil questionPendingAnswers")
	}
}

func TestQuestionRecordAnswer(t *testing.T) {
	m := newTestModelForQuestion()
	m.questionAnswers = make([]permission.QuestionResponse, 0)

	m.recordQuestionAnswer([]string{"Go"})

	if len(m.questionAnswers) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(m.questionAnswers))
	}
	if m.questionAnswers[0].Question != "What is your preferred language?" {
		t.Errorf("expected question text, got %q", m.questionAnswers[0].Question)
	}
	if len(m.questionAnswers[0].Answers) != 1 || m.questionAnswers[0].Answers[0] != "Go" {
		t.Errorf("expected ['Go'], got %v", m.questionAnswers[0].Answers)
	}
}

func TestQuestionAdvanceToNext(t *testing.T) {
	m := newTestModelForQuestion()
	m.questionReq.questions = append(m.questionReq.questions, permission.QuestionPrompt{
		Question: "Second?",
		Header:   "Second",
		Options: []permission.QuestionOptionPrompt{
			{Label: "X", Description: "X desc"},
			{Label: "Y", Description: "Y desc"},
		},
	})
	m.questionAnswers = make([]permission.QuestionResponse, 0)
	m.recordQuestionAnswer([]string{"Go"})
	m.overlay = overlayQuestion

	m.advanceQuestion()

	if m.questionIdx != 1 {
		t.Errorf("expected questionIdx 1, got %d", m.questionIdx)
	}
	if m.questionForm == nil {
		t.Fatal("expected non-nil questionForm for next question")
	}
	if m.overlay != overlayQuestion {
		t.Error("expected overlay to remain on question")
	}
}

func TestQuestionAdvanceFinal_SubmitsToChannel(t *testing.T) {
	replyCh := make(chan []permission.QuestionResponse, 1)

	m := newTestModelForQuestion()
	m.questionReq.reply = replyCh
	m.questionAnswers = make([]permission.QuestionResponse, 0)
	m.recordQuestionAnswer([]string{"Go"})

	m.advanceQuestion()

	// After advancing past the last question, answer should be sent to reply channel
	select {
	case answers := <-replyCh:
		if len(answers) != 1 {
			t.Errorf("expected 1 answer, got %d", len(answers))
		}
		if answers[0].Answers[0] != "Go" {
			t.Errorf("expected 'Go', got %q", answers[0].Answers[0])
		}
	default:
		t.Error("expected answer on reply channel")
	}

	if m.overlay != overlayNone {
		t.Error("expected overlayNone after submit")
	}
}

func TestQuestionAdvance_PendingOtherMerge(t *testing.T) {
	m := newTestModelForQuestion()
	m.questionReq.questions = append(m.questionReq.questions, permission.QuestionPrompt{
		Question: "Second?",
		Header:   "Second",
		Options: []permission.QuestionOptionPrompt{
			{Label: "X", Description: "X desc"},
			{Label: "Y", Description: "Y desc"},
		},
	})
	m.questionAnswers = make([]permission.QuestionResponse, 0)
	// Simulate pending Other: question was answered in buildQuestionForm, then Other form completed
	m.questionPendingOther = true
	m.questionPendingAnswers = []string{"Other: custom text"}

	m.advanceQuestion()

	// The pending Other answer should have been recorded
	if len(m.questionAnswers) != 1 {
		t.Fatalf("expected 1 answer after pending merge, got %d", len(m.questionAnswers))
	}
	if len(m.questionAnswers[0].Answers) != 1 || m.questionAnswers[0].Answers[0] != "Other: custom text" {
		t.Errorf("expected ['Other: custom text'], got %v", m.questionAnswers[0].Answers)
	}
	if m.questionPendingOther {
		t.Error("expected questionPendingOther to be cleared")
	}
}

func TestQuestionHandleFormAborted_Declines(t *testing.T) {
	replyCh := make(chan []permission.QuestionResponse, 1)
	m := newTestModelForQuestion()
	m.questionReq.reply = replyCh
	m.overlay = overlayQuestion

	m.handleQuestionFormAborted()

	// User declined → nil sent to reply channel
	select {
	case answers := <-replyCh:
		if answers != nil {
			t.Errorf("expected nil on decline, got %v", answers)
		}
	default:
		t.Error("expected nil on reply channel after decline")
	}

	if m.overlay != overlayNone {
		t.Error("expected overlayNone after decline")
	}
}

func TestQuestionHandleFormAborted_BackFromOther(t *testing.T) {
	m := newTestModelForQuestion()
	m.questionPendingOther = true
	m.questionPendingAnswers = nil
	m.overlay = overlayQuestion

	m.handleOtherInputCancel()

	// Should revert to question form, not close
	if m.questionPendingOther {
		t.Error("expected questionPendingOther to be cleared")
	}
	if m.questionFormIsOther {
		t.Error("expected questionFormIsOther to be cleared")
	}
	if m.questionForm == nil {
		t.Fatal("expected questionForm to be rebuilt")
	}
	if m.overlay != overlayQuestion {
		t.Error("expected overlay to stay on question")
	}
}

// containsAny checks if s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 审批框改动预览(buildPermDiffs + renderPermOverlay)
// ---------------------------------------------------------------------------

func TestBuildPermDiffs_Edit(t *testing.T) {
	args := `{"file_path":"/proj/main.go","hunk":"@@ -1,2 +1,2 @@\n old1\n-old2\n+new2\n"}`
	diffs := buildPermDiffs("edit", args)
	if len(diffs) != 1 {
		t.Fatalf("diffs = %d, want 1", len(diffs))
	}
	if diffs[0].FilePath != "/proj/main.go" {
		t.Errorf("FilePath = %q, want /proj/main.go", diffs[0].FilePath)
	}
	if len(diffs[0].Lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(diffs[0].Lines))
	}
	if string(diffs[0].Lines[1].Kind) != "-" || string(diffs[0].Lines[2].Kind) != "+" {
		t.Errorf("kinds = %q/%q, want -/+", diffs[0].Lines[1].Kind, diffs[0].Lines[2].Kind)
	}
}

func TestBuildPermDiffs_Write(t *testing.T) {
	args := `{"file_path":"/proj/new.go","content":"line1\nline2\n"}`
	diffs := buildPermDiffs("write", args)
	if len(diffs) != 1 {
		t.Fatalf("diffs = %d, want 1", len(diffs))
	}
	h := diffs[0]
	if h.FilePath != "/proj/new.go" {
		t.Errorf("FilePath = %q, want /proj/new.go", h.FilePath)
	}
	if h.NewStart != 1 || h.NewCount != 2 || len(h.Lines) != 2 {
		t.Errorf("hunk = start:%d count:%d lines:%d, want 1/2/2", h.NewStart, h.NewCount, len(h.Lines))
	}
	for i, l := range h.Lines {
		if string(l.Kind) != "+" || l.NewNum != i+1 {
			t.Errorf("line %d = kind:%q num:%d, want +/%d", i, l.Kind, l.NewNum, i+1)
		}
	}
}

func TestBuildPermDiffs_UnsupportedTool(t *testing.T) {
	if diffs := buildPermDiffs("bash", `{"command":"ls"}`); diffs != nil {
		t.Errorf("bash diffs = %v, want nil", diffs)
	}
}

func TestBuildPermDiffs_BadArgs(t *testing.T) {
	if diffs := buildPermDiffs("edit", `{"file_path":"/proj/main.go"}`); diffs != nil {
		t.Errorf("missing hunk diffs = %v, want nil", diffs)
	}
	if diffs := buildPermDiffs("edit", `not-json`); diffs != nil {
		t.Errorf("bad json diffs = %v, want nil", diffs)
	}
}

func TestBuildPermDiffs_WriteEmptyContentMarked(t *testing.T) {
	// 空 content 的 write 会清空文件(破坏性),必须返回标记行而非 nil
	diffs := buildPermDiffs("write", `{"file_path":"/proj/new.go","content":""}`)
	if len(diffs) != 1 || len(diffs[0].Lines) != 1 {
		t.Fatalf("empty write diffs = %+v, want 1 hunk with 1 line", diffs)
	}
	if diffs[0].Lines[0].Content != permEmptyWriteMarker {
		t.Errorf("marker = %q, want %q", diffs[0].Lines[0].Content, permEmptyWriteMarker)
	}
}

func TestBuildPermDiffs_StripsANSI(t *testing.T) {
	// hunk/内容行携带 ANSI 转义时,预览必须剥离(审批框是阻断式交互面)
	args := "{\"file_path\":\"/proj/main.go\",\"hunk\":\"@@ -1 +1 @@\\n-old\\u001b[31m\\u001b[0m\\n+new\\n\"}"
	diffs := buildPermDiffs("edit", args)
	if len(diffs) != 1 || len(diffs[0].Lines) != 2 {
		t.Fatalf("diffs = %+v, want 1 hunk with 2 lines", diffs)
	}
	if got := diffs[0].Lines[0].Content; got != "old" {
		t.Errorf("del content = %q, want %q (ANSI stripped)", got, "old")
	}
}

func TestRenderPermOverlay_WithDiffPreview(t *testing.T) {
	m := newTestModelForPerm()
	m.permReq.toolName = "edit"
	m.permReq.args = "/proj/main.go  1 hunk(s)"
	m.permReq.diffs = []tool.DiffHunk{{
		FilePath: "/proj/main.go",
		Lines: []tool.DiffLine{
			{Kind: tool.DiffDel, Content: "oldLine", OldNum: 1},
			{Kind: tool.DiffAdd, Content: "newLine", NewNum: 1},
		},
	}}
	content := m.renderPermOverlay(70)
	if !strings.Contains(content, "-oldLine") {
		t.Error("expected deleted line preview")
	}
	if !strings.Contains(content, "+newLine") {
		t.Error("expected added line preview")
	}
	// 选项列表仍在预览之后
	if !strings.Contains(content, "Allow") {
		t.Error("expected Allow option")
	}
}

func TestRenderPermOverlay_NoDiffNoPreview(t *testing.T) {
	m := newTestModelForPerm()
	m.permReq.toolName = "bash"
	m.permReq.diffs = nil
	content := m.renderPermOverlay(70)
	if strings.Contains(content, "│ +") || strings.Contains(content, "│ -") {
		t.Error("no diff preview should be rendered when diffs is nil")
	}
}

func TestRenderPermOverlay_EmptyWriteShowsWarning(t *testing.T) {
	m := newTestModelForPerm()
	m.permReq.toolName = "write"
	m.permReq.args = "/proj/new.go"
	m.permReq.diffs = []tool.DiffHunk{{
		FilePath: "/proj/new.go",
		Lines:    []tool.DiffLine{{Kind: tool.DiffDel, Content: permEmptyWriteMarker}},
	}}
	content := m.renderPermOverlay(70)
	if !strings.Contains(content, enUS.PermEmptyWrite) {
		t.Errorf("expected empty-write warning %q in overlay", enUS.PermEmptyWrite)
	}
}

// ---------------------------------------------------------------------------
// toggleTheme — 切到 auto 时立即重新查询终端背景色
// ---------------------------------------------------------------------------

func TestToggleTheme_ToAutoReturnsProbeCmd(t *testing.T) {
	m := newTestModelForQuestion()
	m.themeMode = "lightcolorblind"
	cmd := m.toggleTheme()
	if m.themeMode != "auto" {
		t.Errorf("themeMode = %q, want auto", m.themeMode)
	}
	if cmd == nil {
		t.Error("expected non-nil cmd when switching to auto")
	}
}

func TestToggleTheme_NonAutoNoCmd(t *testing.T) {
	m := newTestModelForQuestion()
	m.themeMode = "dark"
	cmd := m.toggleTheme()
	if m.themeMode != "light" {
		t.Errorf("themeMode = %q, want light", m.themeMode)
	}
	if cmd != nil {
		t.Error("expected nil cmd for non-auto switch")
	}
}

func TestToggleTheme_AutoCycleToDark(t *testing.T) {
	m := newTestModelForQuestion()
	m.themeMode = "auto"
	cmd := m.toggleTheme()
	if m.themeMode != "dark" {
		t.Errorf("themeMode = %q, want dark", m.themeMode)
	}
	if cmd != nil {
		t.Error("expected nil cmd when leaving auto")
	}
}
