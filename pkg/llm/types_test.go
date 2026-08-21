package llm

import (
	"encoding/json"
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
			wantMsg: 2, // orphan tc stripped → assistant empty → skipped
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
			wantMsg: 1, // invalid tc stripped → assistant empty → skipped
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
		{
			name: "invalid role skipped",
			input: []Message{
				{Role: RoleSystem, Content: "system"},
				{Role: Role(""), Content: "bad role"},
				{Role: RoleUser, Content: "hello"},
			},
			wantMsg: 2, // empty role skipped
			wantOK:  false,
		},
		{
			name: "empty assistant skipped",
			input: []Message{
				{Role: RoleUser, Content: "hello"},
				{Role: RoleAssistant, Content: "", ToolCalls: nil},
				{Role: RoleUser, Content: "next"},
			},
			wantMsg: 2, // empty assistant skipped
			wantOK:  false,
		},
		{
			name: "web_search only assistant preserved",
			input: []Message{
				{Role: RoleUser, Content: "search the web"},
				// Responses API 纯服务端搜索轮:无文本无 function_call,
				// 但 web_search_call item 需回传恢复搜索上下文
				{Role: RoleAssistant, Content: "", WebSearchCalls: []WebSearchCall{
					{ID: "ws_1", Status: "completed"},
				}},
				{Role: RoleUser, Content: "next"},
			},
			wantMsg: 3, // web_search_call 消息保留
			wantOK:  true,
		},
		{
			name: "orphan tool message skipped",
			input: []Message{
				{Role: RoleUser, Content: "do"},
				{Role: RoleTool, Content: "result", ToolCallID: "tc_missing", Name: "read_file"},
				{Role: RoleAssistant, Content: "done"},
			},
			wantMsg: 2, // orphan tool message skipped
			wantOK:  false,
		},
		{
			name: "tool_call with empty Name stripped",
			input: []Message{
				{Role: RoleUser, Content: "do"},
				{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{
					{ID: "tc1", Name: "", Arguments: `{}`},
				}},
				{Role: RoleTool, Content: "result", ToolCallID: "tc1", Name: "read_file"},
				{Role: RoleAssistant, Content: "done"},
			},
			wantMsg: 2, // tc empty Name stripped → assistant empty → skipped, tool orphan → skipped
			wantOK:  false,
		},
		{
			name: "nil input returns nil",
			input: nil,
			wantMsg: 0,
			wantOK:  true,
		},
		{
			name: "empty input returns nil",
			input: []Message{},
			wantMsg: 0,
			wantOK:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, report := ValidateMessages(tt.input)
			ok := len(report) == 0
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

// ---------------------------------------------------------------------------
// 多模态图片
// ---------------------------------------------------------------------------

func TestImagePart_DataURI(t *testing.T) {
	p := ImagePart{MIME: "image/png", B64: "AAAA"}
	if got := p.DataURI(); got != "data:image/png;base64,AAAA" {
		t.Errorf("DataURI = %q", got)
	}
}

func TestMessagesHaveImages(t *testing.T) {
	if MessagesHaveImages(nil) {
		t.Error("nil should not have images")
	}
	if MessagesHaveImages([]Message{{Role: RoleUser, Content: "x"}}) {
		t.Error("plain message should not have images")
	}
	if !MessagesHaveImages([]Message{{Role: RoleUser, Images: []ImagePart{{MIME: "image/png", B64: "A"}}}}) {
		t.Error("message with images should report true")
	}
}

func TestIsVisionModel_PrefixTolerant(t *testing.T) {
	if !IsVisionModel(ModelDeepSeekV4FlashVision) {
		t.Error("bare vision model name should be vision")
	}
	if !IsVisionModel("deepseek/" + ModelDeepSeekV4FlashVision) {
		t.Error("provider-prefixed vision model should be vision")
	}
	if IsVisionModel(ModelDeepSeekV4Flash) {
		t.Error("flash is not a vision model")
	}
	if IsVisionModel("deepseek/" + ModelDeepSeekV4Flash) {
		t.Error("prefixed flash is not a vision model")
	}
}

func TestValidateMessages_StripsImagesFromNonUser(t *testing.T) {
	msgs := []Message{
		{Role: RoleAssistant, Content: "hi", Images: []ImagePart{{MIME: "image/png", B64: "A"}}},
		{Role: RoleTool, ToolCallID: "tc1", Images: []ImagePart{{MIME: "image/png", B64: "C"}}},
		{Role: RoleUser, Content: "look", Images: []ImagePart{{MIME: "image/png", B64: "B"}}},
	}
	// 构造配对的 assistant tool_call,使 tool 消息通过配对检查
	assistant := msgs[0]
	assistant.ToolCalls = []ToolCall{{ID: "tc1", Name: "bash", Arguments: "{}"}}
	msgs[0] = assistant
	clean, report := ValidateMessages(msgs)
	if len(clean) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(clean))
	}
	if len(clean[0].Images) != 0 {
		t.Error("assistant images should be stripped")
	}
	if len(clean[1].Images) != 0 {
		t.Error("tool message images should be stripped (tool branch skips 3b)")
	}
	if len(clean[2].Images) != 1 {
		t.Error("user images should be preserved")
	}
	found := false
	for _, r := range report {
		if r.Action == RepairStripImages {
			found = true
		}
	}
	if !found {
		t.Error("expected RepairStripImages repair entry")
	}
}

func TestWireMessages_NoImages_Passthrough(t *testing.T) {
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "hi"},
	}
	wire := WireMessages(msgs)
	if len(wire) != 2 {
		t.Fatalf("len = %d, want 2", len(wire))
	}
	// 无图消息原样透传(Message 类型,JSON 形状不变)
	if _, ok := wire[0].(Message); !ok {
		t.Errorf("no-image message should pass through as Message, got %T", wire[0])
	}
	b, err := json.Marshal(wire[0])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, isStr := m["content"].(string); !isStr {
		t.Errorf("content should be string, got %T", m["content"])
	}
	if _, hasImages := m["images"]; hasImages {
		t.Error("wire format must not contain images field")
	}
}

func TestWireMessages_WithImages_ContentBlocks(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "describe", Images: []ImagePart{{MIME: "image/png", B64: "AAAA"}}},
	}
	wire := WireMessages(msgs)
	w, ok := wire[0].(map[string]any)
	if !ok {
		t.Fatalf("image message should become map, got %T", wire[0])
	}
	blocks, ok := w["content"].([]any)
	if !ok {
		t.Fatalf("content should be []any, got %T", w["content"])
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2 (text + image)", len(blocks))
	}
	text := blocks[0].(map[string]any)
	if text["type"] != "text" || text["text"] != "describe" {
		t.Errorf("text block = %v", text)
	}
	img := blocks[1].(map[string]any)
	if img["type"] != "image_url" {
		t.Errorf("image block type = %v", img["type"])
	}
	iu := img["image_url"].(map[string]any)
	if iu["url"] != "data:image/png;base64,AAAA" {
		t.Errorf("image url = %v", iu["url"])
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
