package mcp

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestDetectIDEType(t *testing.T) {
	tests := []struct {
		name       string
		serverName string
		title      string
		want       string
	}{
		{name: "exact idea", serverName: "idea", title: "", want: "idea"},
		{name: "intellij idea", serverName: "intellij", title: "IntelliJ IDEA Ultimate", want: "idea"},
		{name: "jetbrains", serverName: "jetbrains", title: "JetBrains MCP Server", want: "idea"},
		{name: "idea with suffix", serverName: "my-idea-server", title: "My Server", want: "idea"},
		{name: "exact vscode", serverName: "vscode", title: "", want: "vscode"},
		{name: "vs code", serverName: "vs-code", title: "VS Code IDE", want: "vscode"},
		{name: "visual studio code", serverName: "vsc", title: "Visual Studio Code", want: "vscode"},
		{name: "unknown server", serverName: "github", title: "GitHub Copilot", want: ""},
		{name: "generic server", serverName: "pencil", title: "Design Tool", want: ""},
		{name: "empty", serverName: "", title: "", want: ""},
		{name: "uppercase IDEA", serverName: "IDEA", title: "IntelliJ IDEA", want: "idea"},
		{name: "mixed VSCODE", serverName: "VsCode", title: "VS CODE", want: "vscode"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectIDEType(tt.serverName, tt.title)
			if got != tt.want {
				t.Errorf("detectIDEType(%q, %q) = %q, want %q", tt.serverName, tt.title, got, tt.want)
			}
		})
	}
}

func TestIDEContextProvider_RegisterUnregister(t *testing.T) {
	p := &IDEContextProvider{}
	p.Register("my-idea", "IntelliJ IDEA", nil)
	guide := p.FormatCapabilityGuide()
	if guide == "" {
		t.Fatal("FormatCapabilityGuide should return non-empty after Register")
	}
	if !strings.Contains(guide, "my-idea") {
		t.Errorf("guide should contain server name 'my-idea', got:\n%s", guide)
	}
	if !strings.Contains(guide, "IntelliJ IDEA") {
		t.Errorf("guide should contain IDE type marker, got:\n%s", guide)
	}

	p.Unregister("my-idea")
	guide2 := p.FormatCapabilityGuide()
	if guide2 != "" {
		t.Errorf("FormatCapabilityGuide should return empty after Unregister, got:\n%s", guide2)
	}
}

func TestIDEContextProvider_NoIDE(t *testing.T) {
	p := &IDEContextProvider{}
	p.Register("github", "GitHub Copilot", nil)
	guide := p.FormatCapabilityGuide()
	if guide != "" {
		t.Errorf("FormatCapabilityGuide should be empty for non-IDE server, got:\n%s", guide)
	}
}

func TestIDEContextProvider_MultipleIDE(t *testing.T) {
	p := &IDEContextProvider{}
	p.Register("idea-server", "IntelliJ IDEA", nil)
	p.Register("vscode-server", "VS Code", nil)
	guide := p.FormatCapabilityGuide()
	if !strings.Contains(guide, "idea-server") {
		t.Error("guide should contain idea-server")
	}
	if !strings.Contains(guide, "vscode-server") {
		t.Error("guide should contain vscode-server")
	}
}

func TestIDEContextProvider_QueryDynamicContext_NoIDEs(t *testing.T) {
	p := &IDEContextProvider{}
	got := p.QueryDynamicContext(context.Background(), "/project")
	if got != "" {
		t.Errorf("QueryDynamicContext should return empty with no IDE connections, got: %s", got)
	}
}

func TestIDEContextProvider_QueryDynamicContext_NilClient(t *testing.T) {
	p := &IDEContextProvider{}
	p.Register("test-idea", "IntelliJ IDEA", nil)
	got := p.QueryDynamicContext(context.Background(), "/project")
	if got != "" {
		t.Errorf("QueryDynamicContext with nil client should return empty, got: %s", got)
	}
}

func TestIDEContextProvider_ConcurrentSafety(t *testing.T) {
	p := &IDEContextProvider{}
	var wg sync.WaitGroup
	n := 50
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p.Register("test-idea", "IntelliJ IDEA", nil)
			_ = p.FormatCapabilityGuide()
			_ = p.QueryDynamicContext(context.Background(), "/project")
			p.Unregister("test-idea")
		}(i)
	}
	wg.Wait()
}

func TestContainsAny(t *testing.T) {
	if !containsAny("hello world", "hello", "xyz") {
		t.Error("expected true")
	}
	if containsAny("hello world", "xyz", "abc") {
		t.Error("expected false")
	}
}

func TestHasTool(t *testing.T) {
	c := &Client{
		tools: []ToolDef{
			{Name: "get_all_open_file_paths"},
			{Name: "build_project"},
		},
	}
	if !hasTool(c, "get_all_open_file_paths") {
		t.Error("expected to find get_all_open_file_paths")
	}
	if hasTool(c, "nonexistent") {
		t.Error("should not find nonexistent tool")
	}
}

func TestExtractTextContent(t *testing.T) {
	result := &CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: "file1.go"},
			{Type: "text", Text: "file2.go"},
			{Type: "image", Data: "base64..."},
		},
	}
	got := extractTextContent(result)
	if !strings.Contains(got, "file1.go") {
		t.Errorf("expected file1.go in result, got: %s", got)
	}
	if !strings.Contains(got, "file2.go") {
		t.Errorf("expected file2.go in result, got: %s", got)
	}
	if strings.Contains(got, "base64") {
		t.Error("image content should be excluded")
	}
}

func TestExtractTextContent_Empty(t *testing.T) {
	result := &CallToolResult{
		Content: []ContentBlock{},
	}
	got := extractTextContent(result)
	if got != "" {
		t.Errorf("expected empty, got: %s", got)
	}
}

// ---------------------------------------------------------------------------
// Capability guide 内容验证
// ---------------------------------------------------------------------------

func TestFormatCapabilityGuide_IDEA(t *testing.T) {
	info := &IDEInfo{ServerName: "idea", IDEType: "idea"}
	prefix := "mcp__idea__"
	guide := formatCapabilityGuide(info, prefix)
	if guide == "" {
		t.Fatal("IDEA capability guide should not be empty")
	}
	expectedTools := []string{
		"find_files_by_name_keyword",
		"search_text",
		"get_symbol_info",
		"search_symbol",
		"build_project",
		"get_file_problems",
		"rename_refactoring",
		"execute_run_configuration",
	}
	for _, tool := range expectedTools {
		if !strings.Contains(guide, tool) {
			t.Errorf("IDEA guide should mention %q", tool)
		}
	}
	if strings.Contains(guide, "git_diff") {
		t.Error("IDEA guide should NOT mention git_diff (replaced by get_repositories)")
	}
}

func TestFormatCapabilityGuide_VSCode(t *testing.T) {
	info := &IDEInfo{ServerName: "vscode", IDEType: "vscode"}
	prefix := "mcp__vscode__"
	guide := formatCapabilityGuide(info, prefix)
	if guide == "" {
		t.Fatal("VS Code capability guide should not be empty")
	}
	expectedTools := []string{
		"get_workspace_folders",
		"list_files",
		"get_document_symbols",
		"get_workspace_symbols",
		"find_references",
		"get_diagnostics",
		"rename_symbol",
		"start_debugging",
		"add_breakpoint",
		"execute_in_terminal",
	}
	for _, tool := range expectedTools {
		if !strings.Contains(guide, tool) {
			t.Errorf("VS Code guide should mention %q", tool)
		}
	}
}

func TestFormatCapabilityGuide_UnknownIDEType(t *testing.T) {
	info := &IDEInfo{ServerName: "unknown", IDEType: "unknown"}
	guide := formatCapabilityGuide(info, "mcp__unknown__")
	if guide != "" {
		t.Errorf("formatCapabilityGuide should return empty for unknown IDE type, got: %s", guide)
	}
}

// ---------------------------------------------------------------------------
// 降级逻辑测试（不依赖 MCP 网络调用）
// ---------------------------------------------------------------------------

func TestIsProjectOpen_ToolNotExists(t *testing.T) {
	client := &Client{tools: []ToolDef{{Name: "other_tool"}}}
	open := isProjectOpen(context.Background(), client, "/project")
	if !open {
		t.Error("isProjectOpen should return true when get_project_modules is not available (graceful degradation)")
	}
}

func TestIsVSCodeWorkspaceOpen_ToolNotExists(t *testing.T) {
	client := &Client{tools: []ToolDef{{Name: "other_tool"}}}
	open := isVSCodeWorkspaceOpen(context.Background(), client, "/project")
	if !open {
		t.Error("isVSCodeWorkspaceOpen should return true when get_workspace_folders is not available (graceful degradation)")
	}
}

func TestQueryOpenFiles_NilClient(t *testing.T) {
	info := &IDEInfo{ServerName: "test", IDEType: "idea", Client: nil}
	result := queryOpenFiles(context.Background(), info, "/project")
	if result != "" {
		t.Errorf("queryOpenFiles should return empty for nil client, got: %s", result)
	}
}

func TestQueryOpenFiles_UnknownIDEType(t *testing.T) {
	info := &IDEInfo{ServerName: "test", IDEType: "unknown", Client: &Client{}}
	result := queryOpenFiles(context.Background(), info, "/project")
	if result != "" {
		t.Errorf("queryOpenFiles should return empty for unknown IDE type, got: %s", result)
	}
}
