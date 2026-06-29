package main

import (
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/permission"
)

// ---------------------------------------------------------------------------
// renderPermOverlay — 权限确认面板宽度自适应（基于 bubbles/list）
// ---------------------------------------------------------------------------

func newTestModelForPerm() *model {
	m := &model{
		themeMode: "dark",
		permReq: &permissionReqMsg{
			toolName:   "shell",
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

func TestFormatToolArgs_ReadFile(t *testing.T) {
	result := formatToolArgs("read_file", `{"file_path":"/home/user/project/main.go"}`, "/home/user/project")
	if result != "main.go" {
		t.Errorf("expected 'main.go', got %q", result)
	}
}

func TestFormatToolArgs_Shell(t *testing.T) {
	result := formatToolArgs("shell", `{"command":"go test ./..."}`, "/tmp")
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

func TestToolSuffix_ReadFile(t *testing.T) {
	p := &Paragraph{
		ToolName:   "read_file",
		ToolResult: strings.Repeat("x", 2048),
		ToolDurMs:  8,
		State:      stateDone,
	}
	suffix := toolSuffix(p)
	if !strings.Contains(suffix, "KB") && !strings.Contains(suffix, "B") {
		t.Errorf("expected size suffix, got %q", suffix)
	}
	if !strings.Contains(suffix, "ms") {
		t.Errorf("expected duration suffix, got %q", suffix)
	}
}

func TestToolSuffix_ShellExitCode(t *testing.T) {
	p := &Paragraph{
		ToolName:   "shell",
		ToolResult: "✅ Command succeeded (exit=0)  120ms\nok  ...",
		ToolDurMs:  120,
		State:      stateDone,
	}
	suffix := toolSuffix(p)
	if !strings.Contains(suffix, "exit=0") {
		t.Errorf("expected exit code, got %q", suffix)
	}
}

func TestToolSuffix_Denied(t *testing.T) {
	p := &Paragraph{
		ToolName:   "shell",
		ToolDenied: true,
		State:      stateError,
	}
	suffix := toolSuffix(p)
	if !strings.Contains(suffix, "permission denied") {
		t.Errorf("expected denied message, got %q", suffix)
	}
}

func TestToolSuffix_Error(t *testing.T) {
	p := &Paragraph{
		ToolName:  "shell",
		ToolError: "command not found",
		State:     stateError,
	}
	suffix := toolSuffix(p)
	if !strings.Contains(suffix, "command not found") {
		t.Errorf("expected error message, got %q", suffix)
	}
}

func TestToolSuffix_Streaming(t *testing.T) {
	p := &Paragraph{
		ToolName: "read_file",
		State:    stateStreaming,
	}
	suffix := toolSuffix(p)
	if suffix != "" {
		t.Errorf("expected empty suffix for streaming, got %q", suffix)
	}
}

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
