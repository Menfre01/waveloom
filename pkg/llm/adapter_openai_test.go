package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- OpenAI Adapter Tests ---

func TestOpenAIBuildRequest(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{
		APIKey:  "sk-test",
		Model:   "gpt-4o",
		BaseURL: "https://api.openai.com/v1",
	})

	messages := []Message{
		{Role: RoleSystem, Content: "You are a helpful assistant."},
		{Role: RoleUser, Content: "Hello"},
	}
	tools := []ToolSpec{
		{
			Name:        "read_file",
			Description: "Read a file from disk",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
			},
		},
	}

	req, err := adapter.BuildRequest(context.Background(), messages, tools)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", req.Method)
	}

	expectedURL := "https://api.openai.com/v1/chat/completions"
	if req.URL.String() != expectedURL {
		t.Errorf("URL = %q, want %q", req.URL.String(), expectedURL)
	}

	// 解析请求 body
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	if body["model"] != "gpt-4o" {
		t.Errorf("model = %v, want gpt-4o", body["model"])
	}
	if body["stream"] != false {
		t.Errorf("stream = %v, want false", body["stream"])
	}

	// 验证 messages
	msgs, ok := body["messages"].([]any)
	if !ok {
		t.Fatal("messages is not a slice")
	}
	if len(msgs) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(msgs))
	}
	sysMsg := msgs[0].(map[string]any)
	if sysMsg["role"] != "system" || sysMsg["content"] != "You are a helpful assistant." {
		t.Errorf("system message = %v, unexpected", sysMsg)
	}

	// 验证 tools
	toolsArr, ok := body["tools"].([]any)
	if !ok {
		t.Fatal("tools is not a slice")
	}
	if len(toolsArr) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(toolsArr))
	}
	tool := toolsArr[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type = %v, want function", tool["type"])
	}
	fn := tool["function"].(map[string]any)
	if fn["name"] != "read_file" {
		t.Errorf("tool name = %v, want read_file", fn["name"])
	}
}

func TestOpenAIBuildRequestExtraParams(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{
		APIKey:  "sk-test",
		Model:   "gpt-4o",
		BaseURL: "https://api.openai.com/v1",
		ExtraParams: map[string]any{
			"temperature": 0.7,
			"max_tokens":  4096,
		},
	})

	req, err := adapter.BuildRequest(context.Background(), []Message{{Role: RoleUser, Content: "Hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	if body["temperature"] != 0.7 {
		t.Errorf("temperature = %v, want 0.7", body["temperature"])
	}
	if body["max_tokens"] != float64(4096) {
		t.Errorf("max_tokens = %v, want 4096", body["max_tokens"])
	}
}

func TestOpenAIBuildRequestNoTools(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{
		APIKey:  "sk-test",
		Model:   "gpt-4o",
		BaseURL: "https://api.openai.com/v1",
	})

	req, err := adapter.BuildRequest(context.Background(), []Message{{Role: RoleUser, Content: "Hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	// 无工具时不应包含 tools 字段
	if _, exists := body["tools"]; exists {
		t.Error("tools field should not be present when no tools provided")
	}
}

func TestOpenAIParseResponseText(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})
	respJSON := `{
		"choices": [{
			"finish_reason": "stop",
			"message": {
				"role": "assistant",
				"content": "Hello! How can I help you?"
			}
		}],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 8,
			"total_tokens": 18
		}
	}`

	resp, err := adapter.ParseResponse([]byte(respJSON))
	if err != nil {
		t.Fatalf("ParseResponse returned error: %v", err)
	}

	if resp.Content != "Hello! How can I help you?" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello! How can I help you?")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %v, want empty", resp.ToolCalls)
	}
	if resp.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if resp.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 8 {
		t.Errorf("CompletionTokens = %d, want 8", resp.Usage.CompletionTokens)
	}
}

func TestOpenAIParseResponseToolCalls(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})
	respJSON := `{
		"choices": [{
			"finish_reason": "tool_calls",
			"message": {
				"role": "assistant",
				"content": null,
				"tool_calls": [{
					"id": "call_abc123",
					"type": "function",
					"function": {
						"name": "read_file",
						"arguments": "{\"path\":\"/etc/hosts\"}"
					}
				}]
			}
		}]
	}`

	resp, err := adapter.ParseResponse([]byte(respJSON))
	if err != nil {
		t.Fatalf("ParseResponse returned error: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_abc123" {
		t.Errorf("ID = %q, want %q", tc.ID, "call_abc123")
	}
	if tc.Name != "read_file" {
		t.Errorf("Name = %q, want %q", tc.Name, "read_file")
	}
	if tc.Arguments != `{"path":"/etc/hosts"}` {
		t.Errorf("Arguments = %q, want %q", tc.Arguments, `{"path":"/etc/hosts"}`)
	}
}

func TestOpenAIParseResponseUsage(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})
	respJSON := `{
		"choices": [{
			"finish_reason": "stop",
			"message": {"role": "assistant", "content": "Hi"}
		}],
		"usage": {
			"prompt_tokens": 100,
			"completion_tokens": 50,
			"total_tokens": 150
		}
	}`

	resp, err := adapter.ParseResponse([]byte(respJSON))
	if err != nil {
		t.Fatalf("ParseResponse returned error: %v", err)
	}

	if resp.Usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 50 {
		t.Errorf("CompletionTokens = %d, want 50", resp.Usage.CompletionTokens)
	}
	if resp.Usage.TotalTokens != 150 {
		t.Errorf("TotalTokens = %d, want 150", resp.Usage.TotalTokens)
	}
	// OpenAI 不返回 cache 字段，应为 0
	if resp.Usage.CacheHitTokens != 0 {
		t.Errorf("CacheHitTokens = %d, want 0", resp.Usage.CacheHitTokens)
	}
}

func TestOpenAIAuthHeader(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{
		APIKey: "sk-test123",
	})

	key, value := adapter.AuthHeader()
	if key != "Authorization" {
		t.Errorf("key = %q, want %q", key, "Authorization")
	}
	if value != "Bearer sk-test123" {
		t.Errorf("value = %q, want %q", value, "Bearer sk-test123")
	}
}

func TestOpenAIClassifyError(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})

	tests := []struct {
		name      string
		err       error
		wantClass ErrorClass
	}{
		{"429 rate limit", &httpStatusError{StatusCode: 429}, ErrorClassRetryable},
		{"500 server error", &httpStatusError{StatusCode: 500}, ErrorClassRetryable},
		{"503 service unavailable", &httpStatusError{StatusCode: 503}, ErrorClassRetryable},
		{"401 unauthorized", &httpStatusError{StatusCode: 401}, ErrorClassNonRetryable},
		{"403 forbidden", &httpStatusError{StatusCode: 403}, ErrorClassNonRetryable},
		{"404 not found", &httpStatusError{StatusCode: 404}, ErrorClassNonRetryable},
		{"400 bad request", &httpStatusError{StatusCode: 400}, ErrorClassNonRetryable},
		{"RetryableError", &RetryableError{Message: "network error"}, ErrorClassRetryable},
		{"NonRetryableError", &NonRetryableError{Message: "auth error"}, ErrorClassNonRetryable},
		{"network error", fmt.Errorf("connection refused"), ErrorClassRetryable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := adapter.ClassifyError(tt.err)
			if got != tt.wantClass {
				t.Errorf("ClassifyError(%v) = %v, want %v", tt.err, got, tt.wantClass)
			}
		})
	}
}

func TestOpenAIBaseURL(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{
		BaseURL: "https://custom.api.com/v1",
	})
	if adapter.BaseURL() != "https://custom.api.com/v1" {
		t.Errorf("BaseURL() = %q, want %q", adapter.BaseURL(), "https://custom.api.com/v1")
	}

	// 默认值
	defaultAdapter := newOpenAIAdapter(ClientConfig{})
	if defaultAdapter.BaseURL() != "https://api.openai.com/v1" {
		t.Errorf("Default BaseURL() = %q, want %q", defaultAdapter.BaseURL(), "https://api.openai.com/v1")
	}
}

// --- 辅助：验证请求 body 中消息序列化 ---

func TestOpenAIBuildRequestToolResultMessage(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{
		APIKey:  "sk-test",
		Model:   "gpt-4o",
		BaseURL: "https://api.openai.com/v1",
	})

	messages := []Message{
		{Role: RoleUser, Content: "What's in /etc/hosts?"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "call_1", Name: "read_file", Arguments: `{"path":"/etc/hosts"}`},
		}},
		{Role: RoleTool, ToolCallID: "call_1", Content: "127.0.0.1 localhost"},
		{Role: RoleUser, Content: "Thanks!"},
	}

	req, err := adapter.BuildRequest(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	msgs := body["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("len(messages) = %d, want 4", len(msgs))
	}

	// assistant 消息含 tool_calls
	assistantMsg := msgs[1].(map[string]any)
	if assistantMsg["role"] != "assistant" {
		t.Errorf("assistant role = %v", assistantMsg["role"])
	}
	toolCalls, ok := assistantMsg["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Errorf("assistant tool_calls = %v, want 1 entry", assistantMsg["tool_calls"])
	}

	// tool 消息含 tool_call_id
	toolMsg := msgs[2].(map[string]any)
	if toolMsg["role"] != "tool" {
		t.Errorf("tool role = %v", toolMsg["role"])
	}
	if toolMsg["tool_call_id"] != "call_1" {
		t.Errorf("tool_call_id = %v, want call_1", toolMsg["tool_call_id"])
	}
}

// --- ClassifyError Edge Cases ---

func TestOpenAIClassifyErrorDefaultNon5xx(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})

	// Status 418 (I'm a teapot) — not in explicit list, < 500 → NonRetryable
	err := &httpStatusError{StatusCode: 418, Body: "I'm a teapot"}
	got := adapter.ClassifyError(err)
	if got != ErrorClassNonRetryable {
		t.Errorf("ClassifyError(418) = %v, want ErrorClassNonRetryable", got)
	}
}

func TestOpenAIClassifyError502(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})

	// 502 Bad Gateway — >= 500 → Retryable
	err := &httpStatusError{StatusCode: 502, Body: "Bad Gateway"}
	got := adapter.ClassifyError(err)
	if got != ErrorClassRetryable {
		t.Errorf("ClassifyError(502) = %v, want ErrorClassRetryable", got)
	}
}

// --- ParseResponse Edge Cases ---

func TestOpenAIParseResponseEmptyChoices(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})
	_, err := adapter.ParseResponse([]byte(`{"choices":[]}`))

	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Fatalf("expected *NonRetryableError, got %T: %v", err, err)
	}
	if nre.Message != "response has no choices" {
		t.Errorf("Message = %q, want %q", nre.Message, "response has no choices")
	}
}

// --- newJSONRequest Error Paths ---

func TestNewJSONRequestMarshalError(t *testing.T) {
	// Channel values cannot be marshaled to JSON
	ch := make(chan int)
	_, err := newJSONRequest(http.MethodPost, "http://example.com", map[string]any{"ch": ch})
	if err == nil {
		t.Error("expected marshal error for channel value")
	}
	if !strings.Contains(err.Error(), "marshaling request body") {
		t.Errorf("expected 'marshaling request body' in error, got: %v", err)
	}
}

func TestNewJSONRequestInvalidURL(t *testing.T) {
	// URL with null byte causes http.NewRequest to fail
	_, err := newJSONRequest(http.MethodPost, "http://example.com/\x00path", map[string]any{"key": "value"})
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
	return
	if !strings.Contains(err.Error(), "creating request") {
		t.Errorf("expected 'creating request' in error, got: %v", err)
	}
}

// --- buildMessages with Name Field ---

func TestOpenAIBuildMessagesNameField(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{
		APIKey:  "sk-test",
		Model:   "gpt-4o",
		BaseURL: "https://api.openai.com/v1",
	})

	messages := []Message{
		{Role: RoleTool, ToolCallID: "call_1", Name: "read_file", Content: "file contents"},
		{Role: RoleUser, Name: "user1", Content: "Hello with name"},
	}

	req, err := adapter.BuildRequest(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	msgs := body["messages"].([]any)

	// First message (tool) should have name
	toolMsg := msgs[0].(map[string]any)
	if toolMsg["name"] != "read_file" {
		t.Errorf("tool message name = %v, want read_file", toolMsg["name"])
	}
	if toolMsg["tool_call_id"] != "call_1" {
		t.Errorf("tool_call_id = %v, want call_1", toolMsg["tool_call_id"])
	}

	// Second message (user) should have name
	userMsg := msgs[1].(map[string]any)
	if userMsg["name"] != "user1" {
		t.Errorf("user message name = %v, want user1", userMsg["name"])
	}
}

// --- OpenAI Streaming Tests ---

func TestOpenAIBuildStreamRequest(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{
		APIKey:  "sk-test",
		Model:   "gpt-4o",
		BaseURL: "https://api.openai.com/v1",
	})

	req, err := adapter.BuildStreamRequest(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)
	if err != nil {
		t.Fatalf("BuildStreamRequest returned error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
}

func TestOpenAIParseStreamEventContent(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})

	chunk := `{"choices":[{"finish_reason":null,"delta":{"content":"Hello"}}]}`
	ev, err := adapter.ParseStreamEvent([]byte(chunk))
	if err != nil {
		t.Fatalf("ParseStreamEvent returned error: %v", err)
	}
	if ev.Delta != "Hello" {
		t.Errorf("Delta = %q, want %q", ev.Delta, "Hello")
	}
	if ev.ReasoningDelta != "" {
		t.Errorf("OpenAI should not have ReasoningDelta")
	}
}

func TestOpenAIParseStreamEventToolCalls(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})

	chunk := `{"choices":[{"finish_reason":null,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}`
	ev, err := adapter.ParseStreamEvent([]byte(chunk))
	if err != nil {
		t.Fatalf("ParseStreamEvent returned error: %v", err)
	}
	if len(ev.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(ev.ToolCalls))
	}
	if ev.ToolCalls[0].Index != 0 {
		t.Errorf("Index = %d, want 0", ev.ToolCalls[0].Index)
	}
}

func TestOpenAIParseStreamEventFinishReason(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})

	chunk := `{"choices":[{"finish_reason":"stop","delta":{"content":""}}]}`
	ev, err := adapter.ParseStreamEvent([]byte(chunk))
	if err != nil {
		t.Fatalf("ParseStreamEvent returned error: %v", err)
	}
	if ev.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", ev.FinishReason, "stop")
	}
}

func TestOpenAIParseStreamEventMalformedJSON(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})

	_, err := adapter.ParseStreamEvent([]byte(`{broken`))
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestOpenAIParseStreamEventWithUsage(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o"})
	data := []byte(`{
		"choices": [{"finish_reason": "stop", "delta": {"content": "Done"}}],
		"usage": {
			"prompt_tokens": 200,
			"completion_tokens": 100,
			"total_tokens": 300
		}
	}`)

	ev, err := adapter.ParseStreamEvent(data)
	if err != nil {
		t.Fatalf("ParseStreamEvent: %v", err)
	}
	if ev.Usage == nil {
		t.Fatal("expected non-nil Usage")
	}
	if ev.Usage.PromptTokens != 200 {
		t.Errorf("PromptTokens = %d, want 200", ev.Usage.PromptTokens)
	}
	if ev.Usage.CompletionTokens != 100 {
		t.Errorf("CompletionTokens = %d, want 100", ev.Usage.CompletionTokens)
	}
	if ev.Usage.TotalTokens != 300 {
		t.Errorf("TotalTokens = %d, want 300", ev.Usage.TotalTokens)
	}
}

func TestOpenAIBuildRequestResponseFormat(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{
		APIKey:         "sk-test",
		Model:          "gpt-4o",
		BaseURL:        "https://api.openai.com/v1",
		ResponseFormat: "json_object",
	})

	messages := []Message{{Role: RoleUser, Content: "Give me JSON"}}
	req, err := adapter.BuildRequest(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	respFmt, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format type = %T, want map[string]any", body["response_format"])
	}
	if respFmt["type"] != "json_object" {
		t.Errorf("response_format.type = %v, want json_object", respFmt["type"])
	}
}

func TestOpenAIBuildStreamRequestResponseFormat(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{
		APIKey:         "sk-test",
		Model:          "gpt-4o",
		BaseURL:        "https://api.openai.com/v1",
		ResponseFormat: "json_object",
	})

	messages := []Message{{Role: RoleUser, Content: "Give me JSON"}}
	req, err := adapter.BuildStreamRequest(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("BuildStreamRequest: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	respFmt, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format type = %T, want map[string]any", body["response_format"])
	}
	if respFmt["type"] != "json_object" {
		t.Errorf("response_format.type = %v, want json_object", respFmt["type"])
	}
}

func TestOpenAIGetBalanceReturnsNil(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})
	info, err := adapter.GetBalance(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if info != nil {
		t.Error("OpenAI GetBalance should return nil")
	}
}

func TestOpenAISupportsBalance(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})
	if adapter.SupportsBalance() {
		t.Error("OpenAI adapter should NOT support balance")
	}
}

// --- OpenAI ListModels Tests ---

func TestOpenAIListModelsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"gpt-4o","object":"model","owned_by":"openai"},
			{"id":"gpt-4o-mini","object":"model","owned_by":"openai"}
		]}`))
	}))
	defer server.Close()

	adapter := newOpenAIAdapter(ClientConfig{
		APIKey:  "sk-test",
		BaseURL: server.URL,
	})

	models, err := adapter.ListModels(context.Background(), server.Client())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(models))
	}
	if models[0].ID != "gpt-4o" {
		t.Errorf("models[0].ID = %q, want gpt-4o", models[0].ID)
	}
	if models[1].ID != "gpt-4o-mini" {
		t.Errorf("models[1].ID = %q, want gpt-4o-mini", models[1].ID)
	}
}

func TestOpenAIListModelsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	adapter := newOpenAIAdapter(ClientConfig{
		APIKey:  "sk-test",
		BaseURL: server.URL,
	})

	_, err := adapter.ListModels(context.Background(), server.Client())
	if err == nil {
		t.Fatal("expected error for 401 on list models endpoint")
	}
}

func TestOpenAIListModelsParseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer server.Close()

	adapter := newOpenAIAdapter(ClientConfig{
		APIKey:  "sk-test",
		BaseURL: server.URL,
	})

	_, err := adapter.ListModels(context.Background(), server.Client())
	if err == nil {
		t.Fatal("expected error for malformed JSON list models response")
	}
	return
	if !strings.Contains(err.Error(), "parsing list models response") {
		t.Errorf("unexpected error: %v", err)
	}
}

// httpStatusError 用于测试 ClassifyError，在 client.go 中定义。
