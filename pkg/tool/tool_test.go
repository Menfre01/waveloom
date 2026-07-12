package tool

import (
	"context"
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// ErasedTool + Wrap 测试
// ---------------------------------------------------------------------------

// mockTypedTool 是一个可编程的 TypedTool，用于测试 Wrap 和 ErasedTool。
type mockTypedTool struct {
	name           string
	desc           string
	prompt         string // ToolWithPrompt 支持
	schema         json.RawMessage
	concurrentSafe bool
	execute        func(ctx context.Context, params mockParams) (*ToolResult, error)
}

type mockParams struct {
	Value string `json:"value"`
}

func (t *mockTypedTool) Name() string                     { return t.name }
func (t *mockTypedTool) Description() string              { return t.desc }
func (t *mockTypedTool) Prompt() string                   { return t.prompt }
func (t *mockTypedTool) Schema() json.RawMessage          { return t.schema }
func (t *mockTypedTool) ConcurrentSafe() bool             { return t.concurrentSafe }
func (t *mockTypedTool) Execute(ctx context.Context, p mockParams) (*ToolResult, error) {
	return t.execute(ctx, p)
}

func TestWrapSuccess(t *testing.T) {
	m := &mockTypedTool{
		name:           "mock_tool",
		desc:           "A mock tool for testing",
		schema:         json.RawMessage(`{"type":"object"}`),
		concurrentSafe: true,
		execute: func(ctx context.Context, p mockParams) (*ToolResult, error) {
			return &ToolResult{Content: "hello " + p.Value}, nil
		},
	}

	tool := Wrap(m)

	// 验证接口方法
	if tool.Name() != "mock_tool" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "mock_tool")
	}
	if tool.Description() != "A mock tool for testing" {
		t.Errorf("Description() = %q, want %q", tool.Description(), "A mock tool for testing")
	}
	if !tool.ConcurrentSafe() {
		t.Error("ConcurrentSafe() = false, want true")
	}

	// 验证 Execute 反序列化 + 调用
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"value":"world"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Content != "hello world" {
		t.Errorf("Execute() Content = %q, want %q", result.Content, "hello world")
	}
}

func TestWrapInvalidJSON(t *testing.T) {
	m := &mockTypedTool{
		name:   "mock_tool",
		desc:   "test",
		schema: json.RawMessage(`{}`),
		execute: func(ctx context.Context, p mockParams) (*ToolResult, error) {
			return &ToolResult{Content: "should not reach"}, nil
		},
	}

	tool := Wrap(m)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{invalid json`))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil (error should be in ToolResult)", err)
	}
	if result.Error == nil {
		t.Fatal("Error should not be nil for invalid JSON")
	}
	if result.Error.Class != ErrorClassRecoverable {
		t.Errorf("Error.Class = %v, want ErrorClassRecoverable", result.Error.Class)
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("Error.Kind = %q, want %q", result.Error.Kind, ErrKindInvalidArgs)
	}
}

// ---------------------------------------------------------------------------
// ToolResult 测试
// ---------------------------------------------------------------------------

func TestToolResultIsError(t *testing.T) {
	tests := []struct {
		name    string
		result  ToolResult
		wantErr bool
	}{
		{
			name:    "success",
			result:  ToolResult{Content: "ok"},
			wantErr: false,
		},
		{
			name:    "with error",
			result:  ToolResult{Content: "failed", Error: &ToolError{Kind: ErrKindFileNotFound}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.result.IsError() != tt.wantErr {
				t.Errorf("IsError() = %v, want %v", tt.result.IsError(), tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ToolError 测试
// ---------------------------------------------------------------------------

func TestToolErrorError(t *testing.T) {
	err := &ToolError{
		Class:   ErrorClassRecoverable,
		Kind:    ErrKindFileNotFound,
		Message: "file not found: /tmp/test.txt",
	}
	if err.Error() != "file not found: /tmp/test.txt" {
		t.Errorf("Error() = %q, want %q", err.Error(), "file not found: /tmp/test.txt")
	}
}

func TestToolErrorUnwrap(t *testing.T) {
	inner := context.Canceled
	err := &ToolError{
		Message: "canceled",
		Cause:   inner,
	}
	if err.Unwrap() != inner {
		t.Errorf("Unwrap() = %v, want %v", err.Unwrap(), inner)
	}
}

// ---------------------------------------------------------------------------
// ToolSpec 测试
// ---------------------------------------------------------------------------

func TestToolSpecJSON(t *testing.T) {
	spec := ToolSpec{
		Name:        "read_file",
		Description: "Read a file",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got ToolSpec
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.Name != spec.Name {
		t.Errorf("Name = %q, want %q", got.Name, spec.Name)
	}
}
