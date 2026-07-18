package tool

import (
	"context"
	"runtime"
	"encoding/json"
	"strings"
	"testing"
)

func TestRegistryRegister(t *testing.T) {
	r := NewRegistry()
	m := &mockTypedTool{
		name:   "test_tool",
		desc:   "test",
		schema: json.RawMessage(`{}`),
		execute: func(ctx context.Context, p mockParams) (*ToolResult, error) {
			return &ToolResult{Content: "ok"}, nil
		},
	}
	r.Register(Wrap(m))

	if len(r.List()) != 1 {
		t.Errorf("List() length = %d, want 1", len(r.List()))
	}
}

func TestRegistryRegisterDuplicate(t *testing.T) {
	r := NewRegistry()
	m := &mockTypedTool{
		name:   "dup_tool",
		desc:   "test",
		schema: json.RawMessage(`{}`),
		execute: func(ctx context.Context, p mockParams) (*ToolResult, error) {
			return &ToolResult{Content: "ok"}, nil
		},
	}
	r.Register(Wrap(m))

	defer func() {
		if rec := recover(); rec == nil {
			t.Error("Register() duplicate should panic")
		}
	}()
	r.Register(Wrap(m))
}

func TestRegistryList(t *testing.T) {
	r := NewRegistry()
	m1 := &mockTypedTool{name: "tool_a", desc: "A", schema: json.RawMessage(`{}`),
		execute: func(ctx context.Context, p mockParams) (*ToolResult, error) { return &ToolResult{}, nil }}
	m2 := &mockTypedTool{name: "tool_b", desc: "B", schema: json.RawMessage(`{}`),
		execute: func(ctx context.Context, p mockParams) (*ToolResult, error) { return &ToolResult{}, nil }}

	r.Register(Wrap(m1))
	r.Register(Wrap(m2))

	specs := r.List()
	if len(specs) != 2 {
		t.Fatalf("List() length = %d, want 2", len(specs))
	}
	if specs[0].Name != "tool_a" || specs[1].Name != "tool_b" {
		t.Errorf("List() names = %q, %q, want tool_a, tool_b", specs[0].Name, specs[1].Name)
	}
}

func TestRegistryGet(t *testing.T) {
	r := NewRegistry()
	m := &mockTypedTool{name: "find_me", desc: "test", schema: json.RawMessage(`{}`),
		execute: func(ctx context.Context, p mockParams) (*ToolResult, error) { return &ToolResult{}, nil }}
	r.Register(Wrap(m))

	tool, ok := r.Get("find_me")
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if tool.Name() != "find_me" {
		t.Errorf("Get() Name = %q, want %q", tool.Name(), "find_me")
	}
}

func TestRegistryGetNotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("Get() ok = true for nonexistent tool, want false")
	}
}

func TestRegistryExecuteUnknownTool(t *testing.T) {
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "unknown", json.RawMessage(`{}`))
	if err == nil {
		t.Error("Execute() error = nil for unknown tool, want error")
	}
}

func TestRegistryExecuteInvalidArgs(t *testing.T) {
	r := NewRegistry()
	m := &mockTypedTool{name: "test_tool", desc: "test", schema: json.RawMessage(`{}`),
		execute: func(ctx context.Context, p mockParams) (*ToolResult, error) { return &ToolResult{}, nil }}
	r.Register(Wrap(m))

	result, err := r.Execute(context.Background(), "test_tool", json.RawMessage(`{bad json`))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if result.Error == nil {
		t.Fatal("Execute() result.Error = nil for invalid args, want ToolError")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("Execute() error.Kind = %q, want %q", result.Error.Kind, ErrKindInvalidArgs)
	}
}

func TestRegistryExecuteSuccess(t *testing.T) {
	r := NewRegistry()
	m := &mockTypedTool{
		name:   "echo_tool",
		desc:   "echo",
		schema: json.RawMessage(`{}`),
		execute: func(ctx context.Context, p mockParams) (*ToolResult, error) {
			return &ToolResult{Content: "echo: " + p.Value}, nil
		},
	}
	r.Register(Wrap(m))

	result, err := r.Execute(context.Background(), "echo_tool", json.RawMessage(`{"value":"hello"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Content != "echo: hello" {
		t.Errorf("Execute() Content = %q, want %q", result.Content, "echo: hello")
	}
}

func TestRegistry_RegisterAndList(t *testing.T) {
	r := NewRegistry()
	r.Register(Wrap(&ReadFileHashline{}))
	r.Register(Wrap(&WriteFile{}))
	r.Register(Wrap(&EditFileHashline{}))
	r.Register(Wrap(&Shell{AllowBg: true}))  // "bash"
	r.Register(Wrap(&Shell{AllowBg: false})) // "bash_subagent"
	r.Register(Wrap(&WebFetch{}))
	r.Register(Wrap(&AskUserQuestion{}))
	r.Register(Wrap(&EnterPlanMode{}))
	r.Register(Wrap(&ExitPlanMode{}))
	r.Register(Wrap(&KillBackgroundTask{}))

	specs := r.List()

	expectedTools := []string{
		"read", "write", "edit",
		"bash", "bash_subagent",
		"web_fetch",
		"ask_user_question",
		"enter_plan_mode", "exit_plan_mode",
		"kill_background_task",
	}
	if len(specs) != len(expectedTools) {
		t.Fatalf("List() length = %d, want %d", len(specs), len(expectedTools))
	}

	for i, name := range expectedTools {
		if specs[i].Name != name {
			t.Errorf("specs[%d].Name = %q, want %q", i, specs[i].Name, name)
		}
		_, ok := r.Get(name)
		if !ok {
			t.Errorf("Get(%q) not found", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Registry streaming 回归测试
// ---------------------------------------------------------------------------

func TestRegistry_IsStreamable_Shell(t *testing.T) {
	r := NewRegistry()
	r.Register(Wrap(&Shell{AllowBg: true}))
	if !r.IsStreamable("bash") {
		t.Error("bash should be streamable")
	}
}

func TestRegistry_IsStreamable_NonStreamable(t *testing.T) {
	r := NewRegistry()
	r.Register(Wrap(&ReadFileHashline{}))
	if r.IsStreamable("read") {
		t.Error("read_file should not be streamable")
	}
}

func TestRegistry_IsStreamable_Unknown(t *testing.T) {
	r := NewRegistry()
	if r.IsStreamable("nonexistent") {
		t.Error("unknown tool should not be streamable")
	}
}

func TestRegistry_ExecuteStreaming(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping streaming test on Windows")
	}
	r := NewRegistry()
	r.Register(Wrap(&Shell{AllowBg: true}))
	var chunks []string
	result, err := r.ExecuteStreaming(context.Background(), "bash", json.RawMessage(`{"command":"echo streaming-test"}`), func(chunk string) {
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatalf("ExecuteStreaming error: %v", err)
	}
	if len(chunks) == 0 {
		t.Error("expected at least one chunk")
	}
	if !strings.Contains(result.Content, "streaming-test") {
		t.Errorf("result should contain 'streaming-test': %s", result.Content)
	}
}

func TestRegistry_ExecuteStreaming_NotStreamable(t *testing.T) {
	r := NewRegistry()
	r.Register(Wrap(&ReadFileHashline{}))
	_, err := r.ExecuteStreaming(context.Background(), "read", json.RawMessage(`{"file_path":"test"}`), func(chunk string) {})
	if err == nil {
		t.Error("expected error for non-streamable tool")
	}
}

// ============================================================================
// FormatToolPrompts 测试
// ============================================================================

func TestFormatToolPrompts_Empty(t *testing.T) {
	r := NewRegistry()
	r.Register(Wrap(&mockTypedTool{name: "no_prompt", desc: "desc", schema: json.RawMessage(`{}`),
		execute: func(ctx context.Context, p mockParams) (*ToolResult, error) { return &ToolResult{}, nil }},
	))

	result := r.FormatToolPrompts()
	if result != "" {
		t.Errorf("empty prompts should return empty string, got: %q", result)
	}
}

func TestFormatToolPrompts_Single(t *testing.T) {
	r := NewRegistry()
	r.Register(Wrap(&mockTypedTool{
		name:   "todo_write",
		desc:   "Task tracker",
		prompt: "## Todo List\n\nUse this tool...",
		schema: json.RawMessage(`{}`),
		execute: func(ctx context.Context, p mockParams) (*ToolResult, error) { return &ToolResult{}, nil },
	}))

	result := r.FormatToolPrompts()
	if !strings.Contains(result, "## Todo List") {
		t.Errorf("expected '## Todo List' in prompts, got: %q", result)
	}
	if !strings.Contains(result, "Use this tool") {
		t.Errorf("expected 'Use this tool' in prompts, got: %q", result)
	}
}

func TestFormatToolPrompts_Multiple(t *testing.T) {
	r := NewRegistry()
	noop := func(ctx context.Context, p mockParams) (*ToolResult, error) { return &ToolResult{}, nil }
	r.Register(Wrap(&mockTypedTool{name: "tool_a", desc: "A", prompt: "## Section A\n\nContent A", schema: json.RawMessage(`{}`), execute: noop}))
	r.Register(Wrap(&mockTypedTool{name: "tool_b", desc: "B", prompt: "## Section B\n\nContent B", schema: json.RawMessage(`{}`), execute: noop}))
	r.Register(Wrap(&mockTypedTool{name: "no_prompt", desc: "C", schema: json.RawMessage(`{}`), execute: noop}))

	result := r.FormatToolPrompts()
	if !strings.Contains(result, "## Section A") || !strings.Contains(result, "Content A") {
		t.Errorf("missing section A in: %q", result)
	}
	if !strings.Contains(result, "## Section B") || !strings.Contains(result, "Content B") {
		t.Errorf("missing section B in: %q", result)
	}
	// 无 Prompt 的工具不应出现
	if strings.Contains(result, "## no_prompt") {
		t.Error("tool without Prompt should not appear")
	}
	// 多个 Prompt 之间用 \n\n 分隔
	if !strings.Contains(result, "\n\n## Section B") {
		t.Errorf("missing separator between prompts, got: %q", result)
	}
}

func TestFormatToolPrompts_PromptNotInDescription(t *testing.T) {
	// REGRESSION: Prompt() 内容不应出现在 Description（C2）中，仅通过 FormatToolPrompts（C1）暴露。
	r := NewRegistry()
	r.Register(Wrap(&mockTypedTool{
		name:   "test",
		desc:   "API contract only",
		prompt: "## Usage Guide\n\nBehavioral rules",
		schema: json.RawMessage(`{}`),
		execute: func(ctx context.Context, p mockParams) (*ToolResult, error) { return &ToolResult{}, nil },
	}))

	specs := r.List()
	if len(specs) != 1 {
		t.Fatal("expected 1 spec")
	}
	if strings.Contains(specs[0].Description, "Usage Guide") || strings.Contains(specs[0].Description, "Behavioral rules") {
		t.Error("Prompt content leaked into Description (C2)")
	}
	if specs[0].Prompt == "" {
		t.Error("Prompt should be stored in spec.Prompt")
	}
}
