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

func TestToolCallMarshalUnmarshalRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		tc   ToolCall
	}{
		{
			name: "basic",
			tc:   ToolCall{ID: "call_123", Name: "read_file", Arguments: `{"path":"/tmp/test"}`},
		},
		{
			name: "empty_arguments",
			tc:   ToolCall{ID: "call_456", Name: "ls", Arguments: `{}`},
		},
		{
			name: "with_index",
			tc:   ToolCall{Index: 3, ID: "call_789", Name: "grep", Arguments: `{"pattern":"foo"}`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.tc.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON: %v", err)
			}

			var loaded ToolCall
			if err := loaded.UnmarshalJSON(data); err != nil {
				t.Fatalf("UnmarshalJSON: %v", err)
			}

			// Index 不参与序列化，反序列化后应为 0
			if loaded.ID != tt.tc.ID {
				t.Errorf("ID = %q, want %q", loaded.ID, tt.tc.ID)
			}
			if loaded.Name != tt.tc.Name {
				t.Errorf("Name = %q, want %q", loaded.Name, tt.tc.Name)
			}
			if loaded.Arguments != tt.tc.Arguments {
				t.Errorf("Arguments = %q, want %q", loaded.Arguments, tt.tc.Arguments)
			}
			if loaded.Index != 0 {
				t.Errorf("Index = %d, want 0 (not serialized)", loaded.Index)
			}
		})
	}
}

func TestToolCallUnmarshalInvalidJSON(t *testing.T) {
	var tc ToolCall
	if err := tc.UnmarshalJSON([]byte(`{invalid}`)); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestToolCallMarshalProducesOpenAIFormat(t *testing.T) {
	tc := ToolCall{ID: "c1", Name: "read_file", Arguments: `{"path":"/f"}`}
	data, err := tc.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	// 验证输出包含 OpenAI 必需的字段
	s := string(data)
	if !contains(s, `"type":"function"`) {
		t.Error("missing type:function")
	}
	if !contains(s, `"function"`) {
		t.Error("missing function wrapper")
	}
	// Index 不应出现在输出中
	if contains(s, `"Index"`) || contains(s, `"index"`) {
		t.Error("Index should not appear in output")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchSub(s, sub)
}

func searchSub(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
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
