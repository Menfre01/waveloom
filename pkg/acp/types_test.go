package acp

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// JSON-RPC 信封序列化测试
// ---------------------------------------------------------------------------

func TestNewRequest(t *testing.T) {
	req, err := NewRequest(1, MethodInitialize, InitializeParams{
		ProtocolVersion: 1,
	})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if req.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want %q", req.JSONRPC, "2.0")
	}
	if req.Method != MethodInitialize {
		t.Errorf("method = %q, want %q", req.Method, MethodInitialize)
	}

	// 验证可以序列化为有效 JSON
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(data) {
		t.Errorf("invalid JSON: %s", data)
	}

	// 验证单行（无嵌入换行）
	if strings.ContainsAny(string(data), "\n\r") {
		t.Errorf("message contains embedded newline: %s", data)
	}
}

func TestNewResponse(t *testing.T) {
	resp, err := NewResponse(1, SessionNewResult{SessionID: "test-session"})
	if err != nil {
		t.Fatalf("NewResponse: %v", err)
	}
	if resp.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want %q", resp.JSONRPC, "2.0")
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(data) {
		t.Errorf("invalid JSON: %s", data)
	}
	if strings.ContainsAny(string(data), "\n\r") {
		t.Errorf("message contains embedded newline: %s", data)
	}
}

func TestNewNotification(t *testing.T) {
	notif, err := NewNotification(MethodSessionUpdate, SessionUpdateParams{
		SessionID: "test-session",
		Update:    json.RawMessage(`{"sessionUpdate":"agent_message_chunk"}`),
	})
	if err != nil {
		t.Fatalf("NewNotification: %v", err)
	}
	if notif.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want %q", notif.JSONRPC, "2.0")
	}
	if notif.Method != MethodSessionUpdate {
		t.Errorf("method = %q, want %q", notif.Method, MethodSessionUpdate)
	}
}

func TestNewErrorResponse(t *testing.T) {
	resp := NewErrorResponse(1, ErrMethodNotFound, "unknown method")
	if resp.Error.Code != ErrMethodNotFound {
		t.Errorf("code = %d, want %d", resp.Error.Code, ErrMethodNotFound)
	}
	if resp.Error.Message != "unknown method" {
		t.Errorf("message = %q, want %q", resp.Error.Message, "unknown method")
	}
	if resp.Result != nil {
		t.Errorf("result should be nil for error response")
	}
}

// ---------------------------------------------------------------------------
// InitializeResult 序列化测试
// ---------------------------------------------------------------------------

func TestInitializeResultMarshaling(t *testing.T) {
	result := InitializeResult{
		ProtocolVersion: 1,
		AgentCapabilities: AgentCapabilities{
			LoadSession: true,
			PromptCapabilities: PromptCapabilities{
				Image:           false,
				Audio:           false,
				EmbeddedContext: true,
			},
			McpCapabilities: &McpCapabilities{
				HTTP: true,
				SSE:  false,
			},
			SessionCapabilities: &SessionCapabilities{
				Close:  &struct{}{},
				List:   &struct{}{},
				Delete: &struct{}{},
			},
		},
		AgentInfo: &ImplementationInfo{
			Name:    "waveloom",
			Title:   "Waveloom",
			Version: "dev",
		},
		AuthMethods: []AuthMethod{{
			ID:   "terminal-setup",
			Name: "Log in from the terminal",
			Type: "terminal",
			Args: []string{"setup"},
			Meta: map[string]any{
				"terminal-auth": map[string]any{
					"label":   "waveloom acp setup",
					"command": "waveloom",
					"args":    []string{"setup"},
					"env":     map[string]any{},
				},
			},
		}},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(data) {
		t.Errorf("invalid JSON: %s", data)
	}
	if strings.ContainsAny(string(data), "\n\r") {
		t.Errorf("message contains embedded newline: %s", data)
	}

	// 反序列化验证
	var parsed InitializeResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.ProtocolVersion != 1 {
		t.Errorf("protocolVersion = %d, want 1", parsed.ProtocolVersion)
	}
	if !parsed.AgentCapabilities.LoadSession {
		t.Error("loadSession should be true")
	}
	if !parsed.AgentCapabilities.PromptCapabilities.EmbeddedContext {
		t.Error("embeddedContext should be true")
	}
}

// ---------------------------------------------------------------------------
// Session 管理类型序列化测试
// ---------------------------------------------------------------------------

func TestSessionNewResultMarshaling(t *testing.T) {
	result := SessionNewResult{SessionID: "a1b2c3d4-e5f6-a7b8-c9d0-e1f2a3b4c5d6"}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed SessionNewResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.SessionID != result.SessionID {
		t.Errorf("sessionId = %q, want %q", parsed.SessionID, result.SessionID)
	}
}

func TestSessionPromptResultMarshaling(t *testing.T) {
	result := SessionPromptResult{StopReason: "end_turn"}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed SessionPromptResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.StopReason != "end_turn" {
		t.Errorf("stopReason = %q, want %q", parsed.StopReason, "end_turn")
	}
}

// ---------------------------------------------------------------------------
// TextContent 测试
// ---------------------------------------------------------------------------

func TestTextContent(t *testing.T) {
	tests := []struct {
		name   string
		blocks []ContentBlock
		want   string
	}{
		{"single text", []ContentBlock{{Type: "text", Text: "hello"}}, "hello"},
		{"multiple text", []ContentBlock{
			{Type: "text", Text: "hello"},
			{Type: "text", Text: "world"},
		}, "hello\nworld"},
		{"mixed types", []ContentBlock{
			{Type: "text", Text: "hello"},
			{Type: "resource", URI: "file://test.go"},
			{Type: "text", Text: "world"},
		}, "hello\nworld"},
		{"empty", nil, ""},
		{"no text", []ContentBlock{{Type: "resource", URI: "file://test.go"}}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TextContent(tt.blocks)
			if got != tt.want {
				t.Errorf("TextContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ACPStopReason 测试
// ---------------------------------------------------------------------------

func TestACPStopReason(t *testing.T) {
	tests := []struct {
		reason string
		want   string
	}{
		{"completed", "end_turn"},
		{"aborted", "cancelled"},
		{"max_steps", "max_turn_requests"},
		{"model_error", "refusal"},
		{"unknown", "end_turn"},
		{"", "end_turn"},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			got := ACPStopReason(tt.reason)
			if got != tt.want {
				t.Errorf("ACPStopReason(%q) = %q, want %q", tt.reason, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ContentChunk 序列化测试
// ---------------------------------------------------------------------------

func TestContentChunkMarshaling(t *testing.T) {
	chunk := ContentChunk{SessionUpdate: "agent_message_chunk",
		MessageID: "msg-001",
		Content: ContentBlock{
			Type: "text",
			Text: "Hello, world!",
		},
	}

	data, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(data) {
		t.Errorf("invalid JSON: %s", data)
	}

	var parsed ContentChunk
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.MessageID != "msg-001" {
		t.Errorf("messageId = %q, want %q", parsed.MessageID, "msg-001")
	}
	if parsed.Content.Type != "text" {
		t.Errorf("content.type = %q, want %q", parsed.Content.Type, "text")
	}
	if parsed.Content.Text != "Hello, world!" {
		t.Errorf("content.text = %q, want %q", parsed.Content.Text, "Hello, world!")
	}
}

// ---------------------------------------------------------------------------
// SessionPromptParams 反序列化测试
// ---------------------------------------------------------------------------

func TestSessionPromptParamsUnmarshaling(t *testing.T) {
	input := `{
		"sessionId": "test-session",
		"prompt": [
			{"type": "text", "text": "hello"},
			{"type": "resource", "uri": "file://main.go"}
		]
	}`
	var params SessionPromptParams
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if params.SessionID != "test-session" {
		t.Errorf("sessionId = %q, want %q", params.SessionID, "test-session")
	}
	if len(params.Prompt) != 2 {
		t.Fatalf("len(prompt) = %d, want 2", len(params.Prompt))
	}
	if params.Prompt[0].Type != "text" {
		t.Errorf("prompt[0].type = %q, want %q", params.Prompt[0].Type, "text")
	}
	if params.Prompt[0].Text != "hello" {
		t.Errorf("prompt[0].text = %q, want %q", params.Prompt[0].Text, "hello")
	}
}

// ---------------------------------------------------------------------------
// SessionUpdateParams 序列化测试
// ---------------------------------------------------------------------------

func TestSessionUpdateParamsMarshaling(t *testing.T) {
	params := SessionUpdateParams{
		SessionID: "test-session",
		Update:    json.RawMessage(`{"sessionUpdate":"agent_message_chunk","messageId":"msg-1","content":{"type":"text","text":"hello"}}`),
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(data) {
		t.Errorf("invalid JSON: %s", data)
	}
	if strings.ContainsAny(string(data), "\n\r") {
		t.Errorf("message contains embedded newline: %s", data)
	}
}
