package llm

import (
	"testing"
	"time"
)

func TestDefaultRetryPolicy(t *testing.T) {
	p := DefaultRetryPolicy()
	if p.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", p.MaxRetries)
	}
	if p.InitialBackoff != 1*time.Second {
		t.Errorf("InitialBackoff = %v, want 1s", p.InitialBackoff)
	}
	if p.MaxBackoff != 30*time.Second {
		t.Errorf("MaxBackoff = %v, want 30s", p.MaxBackoff)
	}
	if p.Multiplier != 2.0 {
		t.Errorf("Multiplier = %f, want 2.0", p.Multiplier)
	}
}

func TestValidateMessages(t *testing.T) {
	tests := []struct {
		name     string
		input    []Message
		wantMsg  int    // expected message count
		wantOK   bool
	}{
		{
			name: "clean messages pass through",
			input: []Message{
				{Role: RoleSystem, Content: "you are helpful"},
				{Role: RoleUser, Content: "hello"},
				{Role: RoleAssistant, Content: "hi there"},
			},
			wantMsg: 3,
			wantOK:  true,
		},
		{
			name: "valid tool call sequence",
			input: []Message{
				{Role: RoleUser, Content: "read file"},
				{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{
					{ID: "tc1", Name: "read_file", Arguments: `{"file":"x"}`},
				}},
				{Role: RoleTool, Content: "content", ToolCallID: "tc1", Name: "read_file"},
				{Role: RoleAssistant, Content: "done"},
			},
			wantMsg: 4,
			wantOK:  true,
		},
		{
			name: "orphan tool_calls stripped",
			input: []Message{
				{Role: RoleUser, Content: "read file"},
				{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{
					{ID: "tc1", Name: "read_file", Arguments: `{}`},
				}},
				// No tool message for tc1
				{Role: RoleUser, Content: "next"},
			},
			wantMsg: 3,
			wantOK:  false,
		},
		{
			name: "empty tool_call ID stripped",
			input: []Message{
				{Role: RoleUser, Content: "do something"},
				{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{
					{ID: "", Name: "read_file", Arguments: `{}`},
				}},
			},
			wantMsg: 2,
			wantOK:  false,
		},
		{
			name: "multiple tool_calls with one orphan",
			input: []Message{
				{Role: RoleUser, Content: "read and write"},
				{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{
					{ID: "tc1", Name: "read_file", Arguments: `{}`},
					{ID: "tc2", Name: "write_file", Arguments: `{}`},
				}},
				{Role: RoleTool, Content: "ok", ToolCallID: "tc1", Name: "read_file"},
				// tc2 has no matching tool message → orphan
				{Role: RoleAssistant, Content: "done"},
			},
			wantMsg: 4,
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ValidateMessages(tt.input)
			if len(got) != tt.wantMsg {
				t.Errorf("got %d messages, want %d", len(got), tt.wantMsg)
				for i, m := range got {
					t.Logf("  msg[%d]: role=%s tool_calls=%d", i, m.Role, len(m.ToolCalls))
				}
			}
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			// clean messages should remain unchanged
			if tt.wantOK && !messagesEqual(tt.input, got) {
				t.Error("clean input should be unchanged by validation")
			}
		})
	}
}

func TestFilterValidToolCalls(t *testing.T) {
	tests := []struct {
		name     string
		calls    []ToolCall
		registry map[string]bool
		wantLen  int
	}{
		{
			name:    "nil calls returns nil",
			calls:   nil,
			wantLen: 0,
		},
		{
			name:    "empty calls returns nil",
			calls:   []ToolCall{},
			wantLen: 0,
		},
		{
			name: "all valid",
			calls: []ToolCall{
				{ID: "id1", Name: "read_file"},
				{ID: "id2", Name: "write_file"},
			},
			registry: nil,
			wantLen:  2,
		},
		{
			name: "filter empty ID",
			calls: []ToolCall{
				{ID: "", Name: "read_file"},
				{ID: "id2", Name: "write_file"},
			},
			wantLen: 1,
		},
		{
			name: "filter empty Name",
			calls: []ToolCall{
				{ID: "id1", Name: ""},
				{ID: "id2", Name: "write_file"},
			},
			wantLen: 1,
		},
		{
			name: "filter by registry",
			calls: []ToolCall{
				{ID: "id1", Name: "read_file"},
				{ID: "id2", Name: "unknown_tool"},
			},
			registry: map[string]bool{"read_file": true, "write_file": true},
			wantLen:  1,
		},
		{
			name: "all filtered returns nil",
			calls: []ToolCall{
				{ID: "", Name: ""},
			},
			wantLen: 0,
		},
		{
			name: "nil registry passes all non-empty",
			calls: []ToolCall{
				{ID: "id1", Name: "any_tool"},
				{ID: "id2", Name: "another_tool"},
			},
			registry: nil,
			wantLen:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterValidToolCalls(tt.calls, tt.registry)
			if tt.wantLen == 0 {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
			} else {
				if len(got) != tt.wantLen {
					t.Errorf("got %d calls, want %d", len(got), tt.wantLen)
				}
			}
		})
	}
}

func messagesEqual(a, b []Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content ||
			a[i].ToolCallID != b[i].ToolCallID || len(a[i].ToolCalls) != len(b[i].ToolCalls) {
			return false
		}
	}
	return true
}
