package tool

import (
	"context"
	"encoding/json"
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

func TestNewDefaultRegistry(t *testing.T) {
	r := NewDefaultRegistry()
	specs := r.List()

	expectedTools := []string{
		"read_file", "write_file", "edit_file",
		"bash", "search_file", "grep", "ls",
		"web_fetch",
		"lsp_diagnostic", "lsp_definition", "lsp_references", "lsp_hover",
		"ask_user_question",
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
