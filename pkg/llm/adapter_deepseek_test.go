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

// --- DeepSeek Adapter Tests ---

func TestDeepSeekBuildRequest(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{
		APIKey:  "sk-deepseek",
		Model:   "deepseek-v4-pro",
		BaseURL: "https://api.deepseek.com",
	})

	messages := []Message{
		{Role: RoleSystem, Content: "You are a helpful assistant."},
		{Role: RoleUser, Content: "Hello"},
	}

	req, err := adapter.BuildRequest(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", req.Method)
	}

	expectedURL := "https://api.deepseek.com/v1/chat/completions"
	if req.URL.String() != expectedURL {
		t.Errorf("URL = %q, want %q", req.URL.String(), expectedURL)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	if body["model"] != "deepseek-v4-pro" {
		t.Errorf("model = %v, want deepseek-v4-pro", body["model"])
	}
	if body["stream"] != false {
		t.Errorf("stream = %v, want false", body["stream"])
	}
}

func TestDeepSeekBuildRequestReasoningContent(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{
		APIKey:  "sk-deepseek",
		Model:   "deepseek-v4-pro",
		BaseURL: "https://api.deepseek.com",
	})

	tests := []struct {
		name               string
		messages           []Message
		wantReasoningInMsg int // 期望 reasoning_content 出现在第几条消息（-1 表示不应出现）
	}{
		{
			name: "assistant with tool calls includes reasoning_content",
			messages: []Message{
				{Role: RoleUser, Content: "Hello"},
				{Role: RoleAssistant, Content: "", ReasoningContent: "Let me think...", ToolCalls: []ToolCall{
					{ID: "call_1", Name: "read_file", Arguments: `{"path":"/etc/hosts"}`},
				}},
				{Role: RoleTool, ToolCallID: "call_1", Content: "file contents"},
			},
			wantReasoningInMsg: 1,
		},
		{
			// 清洗由 Loop 层负责，buildMessages 只是无条件透传。
			// 此处验证：即使 assistant 无 ToolCalls，只要 ReasoningContent 非空就输出。
			name: "assistant without tool calls passes through reasoning_content unconditionally",
			messages: []Message{
				{Role: RoleUser, Content: "Hello"},
				{Role: RoleAssistant, Content: "Hi there!", ReasoningContent: "Thinking..."},
			},
			wantReasoningInMsg: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := adapter.BuildRequest(context.Background(), tt.messages, nil)
			if err != nil {
				t.Fatalf("BuildRequest returned error: %v", err)
			}

			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("Failed to decode request body: %v", err)
			}

			msgs := body["messages"].([]any)

			for i, m := range msgs {
				msg := m.(map[string]any)
				_, hasReasoning := msg["reasoning_content"]

				if i == tt.wantReasoningInMsg {
					if !hasReasoning {
						t.Errorf("message %d: expected reasoning_content, but not found", i)
					}
				}
			}
		})
	}
}

func TestDeepSeekBuildRequestExtraParams(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{
		APIKey:  "sk-deepseek",
		Model:   "deepseek-v4-pro",
		BaseURL: "https://api.deepseek.com",
		ExtraParams: map[string]any{
			"thinking":          map[string]any{"type": "enabled"},
			"reasoning_effort":  "high",
			"max_tokens":        4096,
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

	// 验证 ExtraParams 合并到 body 顶层
	if thinking, ok := body["thinking"].(map[string]any); !ok || thinking["type"] != "enabled" {
		t.Errorf("thinking = %v, want {type: enabled}", body["thinking"])
	}
	if body["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v, want high", body["reasoning_effort"])
	}
	if body["max_tokens"] != float64(4096) {
		t.Errorf("max_tokens = %v, want 4096", body["max_tokens"])
	}
}

func TestDeepSeekReasoningEffortMapping(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"low", "high"},
		{"medium", "high"},
		{"xhigh", "max"},
		{"high", "high"},
		{"max", "max"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			adapter := newDeepSeekAdapter(ClientConfig{
				APIKey:  "sk-deepseek",
				Model:   "deepseek-v4-pro",
				BaseURL: "https://api.deepseek.com",
				ExtraParams: map[string]any{
					"reasoning_effort": tt.input,
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

			if body["reasoning_effort"] != tt.expected {
				t.Errorf("reasoning_effort = %v, want %v (input: %s)", body["reasoning_effort"], tt.expected, tt.input)
			}
		})
	}
}

func TestDeepSeekParseResponseText(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})
	respJSON := `{
		"choices": [{
			"finish_reason": "stop",
			"message": {
				"role": "assistant",
				"content": "Hello! How can I help you?",
				"reasoning_content": "Let me think about how to respond..."
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
	if resp.ReasoningContent != "Let me think about how to respond..." {
		t.Errorf("ReasoningContent = %q, want %q", resp.ReasoningContent, "Let me think about how to respond...")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
}

func TestDeepSeekParseResponseToolCalls(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})
	respJSON := `{
		"choices": [{
			"finish_reason": "tool_calls",
			"message": {
				"role": "assistant",
				"content": "",
				"reasoning_content": "I need to read the file",
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
	// reasoning_content 也应被提取
	if resp.ReasoningContent != "I need to read the file" {
		t.Errorf("ReasoningContent = %q, want %q", resp.ReasoningContent, "I need to read the file")
	}
}

func TestDeepSeekParseResponseUsage(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})
	respJSON := `{
		"choices": [{
			"finish_reason": "stop",
			"message": {"role": "assistant", "content": "Hi"}
		}],
		"usage": {
			"prompt_tokens": 1000,
			"completion_tokens": 200,
			"total_tokens": 1200,
			"prompt_cache_hit_tokens": 800,
			"prompt_cache_miss_tokens": 200,
			"completion_tokens_details": {
				"reasoning_tokens": 30
			}
		}
	}`

	resp, err := adapter.ParseResponse([]byte(respJSON))
	if err != nil {
		t.Fatalf("ParseResponse returned error: %v", err)
	}

	if resp.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if resp.Usage.PromptTokens != 1000 {
		t.Errorf("PromptTokens = %d, want 1000", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 200 {
		t.Errorf("CompletionTokens = %d, want 200", resp.Usage.CompletionTokens)
	}
	if resp.Usage.TotalTokens != 1200 {
		t.Errorf("TotalTokens = %d, want 1200", resp.Usage.TotalTokens)
	}
	if resp.Usage.CacheHitTokens != 800 {
		t.Errorf("CacheHitTokens = %d, want 800", resp.Usage.CacheHitTokens)
	}
	if resp.Usage.CacheMissTokens != 200 {
		t.Errorf("CacheMissTokens = %d, want 200", resp.Usage.CacheMissTokens)
	}
	if resp.Usage.ReasoningTokens != 30 {
		t.Errorf("ReasoningTokens = %d, want 30", resp.Usage.ReasoningTokens)
	}
}

func TestDeepSeekParseResponseInsufficientSystemResource(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})
	respJSON := `{
		"choices": [{
			"finish_reason": "insufficient_system_resource",
			"message": {"role": "assistant", "content": ""}
		}]
	}`

	resp, err := adapter.ParseResponse([]byte(respJSON))
	if resp != nil {
		t.Errorf("expected nil response, got %v", resp)
	}

	var re *RetryableError
	if !isError(err, &re) {
		t.Fatalf("expected *RetryableError, got %T: %v", err, err)
	}
	if re.Message != "insufficient system resource" {
		t.Errorf("Message = %q, want %q", re.Message, "insufficient system resource")
	}
}

func TestDeepSeekAuthHeader(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{
		APIKey: "sk-deepseek123",
	})

	key, value := adapter.AuthHeader()
	if key != "Authorization" {
		t.Errorf("key = %q, want %q", key, "Authorization")
	}
	if value != "Bearer sk-deepseek123" {
		t.Errorf("value = %q, want %q", value, "Bearer sk-deepseek123")
	}
}

func TestDeepSeekClassifyError(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})

	tests := []struct {
		name      string
		err       error
		wantClass ErrorClass
	}{
		{"429 rate limit", &httpStatusError{StatusCode: 429}, ErrorClassRetryable},
		{"500 server error", &httpStatusError{StatusCode: 500}, ErrorClassRetryable},
		{"401 unauthorized", &httpStatusError{StatusCode: 401}, ErrorClassNonRetryable},
		{"402 insufficient balance", &httpStatusError{StatusCode: 402}, ErrorClassNonRetryable},
		{"400 bad request", &httpStatusError{StatusCode: 400}, ErrorClassNonRetryable},
		{"RetryableError", &RetryableError{Message: "insufficient system resource"}, ErrorClassRetryable},
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

func TestDeepSeekBaseURL(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{
		BaseURL: "https://custom.deepseek.com",
	})
	if adapter.BaseURL() != "https://custom.deepseek.com" {
		t.Errorf("BaseURL() = %q, want %q", adapter.BaseURL(), "https://custom.deepseek.com")
	}

	defaultAdapter := newDeepSeekAdapter(ClientConfig{})
	if defaultAdapter.BaseURL() != "https://api.deepseek.com" {
		t.Errorf("Default BaseURL() = %q, want %q", defaultAdapter.BaseURL(), "https://api.deepseek.com")
	}
}

// --- ClassifyError Edge Cases ---

func TestDeepSeekClassifyErrorDefaultNon5xx(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})

	// Status 418 (I'm a teapot) — not in explicit list, < 500 → NonRetryable
	err := &httpStatusError{StatusCode: 418, Body: "I'm a teapot"}
	got := adapter.ClassifyError(err)
	if got != ErrorClassNonRetryable {
		t.Errorf("ClassifyError(418) = %v, want ErrorClassNonRetryable", got)
	}
}

func TestDeepSeekClassifyError403(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})

	// 403 is not in DeepSeek's explicit list (only 401, 402, 400)
	// 403 < 500 → NonRetryable via default branch
	err := &httpStatusError{StatusCode: 403, Body: "Forbidden"}
	got := adapter.ClassifyError(err)
	if got != ErrorClassNonRetryable {
		t.Errorf("ClassifyError(403) = %v, want ErrorClassNonRetryable", got)
	}
}

func TestDeepSeekClassifyError504(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})

	// 504 Gateway Timeout — >= 500 → Retryable via default branch
	err := &httpStatusError{StatusCode: 504, Body: "Gateway Timeout"}
	got := adapter.ClassifyError(err)
	if got != ErrorClassRetryable {
		t.Errorf("ClassifyError(504) = %v, want ErrorClassRetryable", got)
	}
}

// --- ParseResponse Edge Cases ---

func TestDeepSeekParseResponseEmptyChoices(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})
	_, err := adapter.ParseResponse([]byte(`{"choices":[]}`))

	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Fatalf("expected *NonRetryableError, got %T: %v", err, err)
	}
	if nre.Message != "response has no choices" {
		t.Errorf("Message = %q, want %q", nre.Message, "response has no choices")
	}
}

func TestDeepSeekParseResponseMalformedJSON(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})
	_, err := adapter.ParseResponse([]byte(`{invalid json`))

	var re *RetryableError
	if !errors.As(err, &re) {
		t.Errorf("expected *RetryableError for malformed JSON, got %T: %v", err, err)
	}
}

// --- BuildRequest with Tools ---

func TestDeepSeekBuildRequestWithTools(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{
		APIKey:  "sk-deepseek",
		Model:   "deepseek-v4-pro",
		BaseURL: "https://api.deepseek.com",
	})

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

	req, err := adapter.BuildRequest(context.Background(), []Message{{Role: RoleUser, Content: "Read /etc/hosts"}}, tools)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	toolsArr, ok := body["tools"].([]any)
	if !ok || len(toolsArr) != 1 {
		t.Fatalf("expected 1 tool, got %v", body["tools"])
	}
	tool := toolsArr[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type = %v, want function", tool["type"])
	}
}

// --- buildMessages with Name Field ---

func TestDeepSeekBuildMessagesNameField(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{
		APIKey:  "sk-deepseek",
		Model:   "deepseek-v4-pro",
		BaseURL: "https://api.deepseek.com",
	})

	messages := []Message{
		{Role: RoleTool, ToolCallID: "call_1", Name: "read_file", Content: "file contents"},
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
	msg := msgs[0].(map[string]any)
	if msg["name"] != "read_file" {
		t.Errorf("name = %v, want read_file", msg["name"])
	}
	if msg["tool_call_id"] != "call_1" {
		t.Errorf("tool_call_id = %v, want call_1", msg["tool_call_id"])
	}
}

func TestDeepSeekParseResponseNoUsage(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})
	respJSON := `{
		"choices": [{
			"finish_reason": "stop",
			"message": {"role": "assistant", "content": "Hello"}
		}]
	}`

	resp, err := adapter.ParseResponse([]byte(respJSON))
	if err != nil {
		t.Fatalf("ParseResponse returned error: %v", err)
	}
	if resp.Usage != nil {
		t.Errorf("Usage should be nil when not provided, got %v", resp.Usage)
	}
}

// --- DeepSeek Streaming Tests ---

func TestDeepSeekBuildStreamRequest(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{
		APIKey:  "sk-deepseek",
		Model:   "deepseek-v4-pro",
		BaseURL: "https://api.deepseek.com",
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
	if body["model"] != "deepseek-v4-pro" {
		t.Errorf("model = %v, want deepseek-v4-pro", body["model"])
	}
}

func TestDeepSeekParseStreamEventContent(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})

	chunk := `{"choices":[{"finish_reason":null,"delta":{"content":"Hello","reasoning_content":"Thinking..."}}]}`
	ev, err := adapter.ParseStreamEvent([]byte(chunk))
	if err != nil {
		t.Fatalf("ParseStreamEvent returned error: %v", err)
	}
	if ev.Delta != "Hello" {
		t.Errorf("Delta = %q, want %q", ev.Delta, "Hello")
	}
	if ev.ReasoningDelta != "Thinking..." {
		t.Errorf("ReasoningDelta = %q, want %q", ev.ReasoningDelta, "Thinking...")
	}
	if ev.Done {
		t.Error("Done = true, want false")
	}
}

func TestDeepSeekParseStreamEventToolCalls(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})

	chunk := `{"choices":[{"finish_reason":null,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}`
	ev, err := adapter.ParseStreamEvent([]byte(chunk))
	if err != nil {
		t.Fatalf("ParseStreamEvent returned error: %v", err)
	}
	if len(ev.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(ev.ToolCalls))
	}
	tc := ev.ToolCalls[0]
	if tc.Index != 0 {
		t.Errorf("Index = %d, want 0", tc.Index)
	}
	if tc.ID != "call_1" {
		t.Errorf("ID = %q, want %q", tc.ID, "call_1")
	}
	if tc.Name != "read_file" {
		t.Errorf("Name = %q, want %q", tc.Name, "read_file")
	}
	if tc.Arguments != `{"path":` {
		t.Errorf("Arguments = %q, want %q", tc.Arguments, `{"path":`)
	}
}

func TestDeepSeekParseStreamEventFinishReason(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})

	chunk := `{"choices":[{"finish_reason":"stop","delta":{"content":""}}]}`
	ev, err := adapter.ParseStreamEvent([]byte(chunk))
	if err != nil {
		t.Fatalf("ParseStreamEvent returned error: %v", err)
	}
	if ev.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", ev.FinishReason, "stop")
	}
}

func TestDeepSeekParseStreamEventEmptyChoices(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})

	ev, err := adapter.ParseStreamEvent([]byte(`{"choices":[]}`))
	if err != nil {
		t.Fatalf("ParseStreamEvent returned error: %v", err)
	}
	if ev.Delta != "" || ev.ReasoningDelta != "" {
		t.Error("expected empty event for empty choices")
	}
}

// --- DeepSeek Balance Tests ---

func TestDeepSeekBalanceParsing(t *testing.T) {
	body := []byte(`{
		"is_available": true,
		"balance_infos": [
			{
				"currency": "CNY",
				"total_balance": "110.00",
				"granted_balance": "10.00",
				"topped_up_balance": "100.00"
			},
			{
				"currency": "USD",
				"total_balance": "5.00",
				"granted_balance": "0.00",
				"topped_up_balance": "5.00"
			}
		]
	}`)

	var br deepSeekBalanceResponse
	if err := json.Unmarshal(body, &br); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if !br.IsAvailable {
		t.Error("expected IsAvailable=true")
	}
	if len(br.BalanceInfos) != 2 {
		t.Fatalf("expected 2 balance infos, got %d", len(br.BalanceInfos))
	}

	cny := br.BalanceInfos[0]
	if cny.Currency != "CNY" || cny.TotalBalance != "110.00" || cny.GrantedBalance != "10.00" || cny.ToppedUpBalance != "100.00" {
		t.Errorf("CNY balance mismatch: %+v", cny)
	}

	usd := br.BalanceInfos[1]
	if usd.Currency != "USD" || usd.TotalBalance != "5.00" {
		t.Errorf("USD balance mismatch: %+v", usd)
	}
}

func TestDeepSeekParseStreamEventMalformedJSON(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})

	_, err := adapter.ParseStreamEvent([]byte(`{invalid`))
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestDeepSeekParseStreamEventWithUsage(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{Model: "deepseek-v4-flash"})
	data := []byte(`{
		"choices": [{"finish_reason": "stop", "delta": {"content": "Done"}}],
		"usage": {
			"prompt_tokens": 100,
			"completion_tokens": 50,
			"total_tokens": 150,
			"prompt_cache_hit_tokens": 80,
			"prompt_cache_miss_tokens": 20,
			"completion_tokens_details": {"reasoning_tokens": 30}
		}
	}`)

	ev, err := adapter.ParseStreamEvent(data)
	if err != nil {
		t.Fatalf("ParseStreamEvent: %v", err)
	}
	if ev.Usage == nil {
		t.Fatal("expected non-nil Usage")
	}
	if ev.Usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", ev.Usage.PromptTokens)
	}
	if ev.Usage.CompletionTokens != 50 {
		t.Errorf("CompletionTokens = %d, want 50", ev.Usage.CompletionTokens)
	}
	if ev.Usage.TotalTokens != 150 {
		t.Errorf("TotalTokens = %d, want 150", ev.Usage.TotalTokens)
	}
	if ev.Usage.CacheHitTokens != 80 {
		t.Errorf("CacheHitTokens = %d, want 80", ev.Usage.CacheHitTokens)
	}
	if ev.Usage.CacheMissTokens != 20 {
		t.Errorf("CacheMissTokens = %d, want 20", ev.Usage.CacheMissTokens)
	}
	if ev.Usage.ReasoningTokens != 30 {
		t.Errorf("ReasoningTokens = %d, want 30", ev.Usage.ReasoningTokens)
	}
}

func TestDeepSeekBuildRequestResponseFormat(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{
		APIKey:         "sk-test",
		Model:          "deepseek-v4-flash",
		BaseURL:        "https://api.deepseek.com",
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

func TestDeepSeekBuildStreamRequestResponseFormat(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{
		APIKey:         "sk-test",
		Model:          "deepseek-v4-flash",
		BaseURL:        "https://api.deepseek.com",
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

func TestDeepSeekGetBalanceHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	adapter := newDeepSeekAdapter(ClientConfig{
		APIKey:  "sk-test",
		BaseURL: server.URL,
	})

	_, err := adapter.GetBalance(context.Background(), server.Client())
	if err == nil {
		t.Fatal("expected error for 401 on balance endpoint")
	}
}

func TestDeepSeekSupportsBalance(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{})
	if !adapter.SupportsBalance() {
		t.Error("DeepSeek adapter should support balance")
	}
}

func TestDeepSeekGetBalanceParseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer server.Close()

	adapter := newDeepSeekAdapter(ClientConfig{
		APIKey:  "sk-test",
		BaseURL: server.URL,
	})

	_, err := adapter.GetBalance(context.Background(), server.Client())
	if err == nil {
		t.Fatal("expected error for malformed JSON balance response")
	}
	return
	if !strings.Contains(err.Error(), "parsing balance response") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- DeepSeek ListModels Tests ---

func TestDeepSeekListModelsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"deepseek-v4-pro","object":"model","owned_by":"deepseek"},
			{"id":"deepseek-v4-flash","object":"model","owned_by":"deepseek"}
		]}`))
	}))
	defer server.Close()

	adapter := newDeepSeekAdapter(ClientConfig{
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
	if models[0].ID != "deepseek-v4-pro" {
		t.Errorf("models[0].ID = %q, want deepseek-v4-pro", models[0].ID)
	}
	if models[1].ID != "deepseek-v4-flash" {
		t.Errorf("models[1].ID = %q, want deepseek-v4-flash", models[1].ID)
	}
	if models[0].OwnedBy != "deepseek" {
		t.Errorf("models[0].OwnedBy = %q, want deepseek", models[0].OwnedBy)
	}
}

func TestDeepSeekListModelsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	adapter := newDeepSeekAdapter(ClientConfig{
		APIKey:  "sk-test",
		BaseURL: server.URL,
	})

	_, err := adapter.ListModels(context.Background(), server.Client())
	if err == nil {
		t.Fatal("expected error for 401 on list models endpoint")
	}
}

func TestDeepSeekListModelsParseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer server.Close()

	adapter := newDeepSeekAdapter(ClientConfig{
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

// isError 辅助函数，检查 err 是否匹配 target 类型。
func isError(err error, target interface{}) bool {
	return errors.As(err, target)
}

// REGRESSION: model override from context should replace a.model in request body.
func TestDeepSeekAdapter_ModelOverrideFromContext(t *testing.T) {
	cfg := ClientConfig{
		Provider: ProviderDeepSeek,
		APIKey:   "sk-test",
		Model:    "deepseek-v4-pro",
	}
	adapter := newDeepSeekAdapter(cfg)
	ctx := WithModelOverride(context.Background(), "deepseek-v4-flash")

	req, err := adapter.BuildRequest(ctx, []Message{{Role: RoleUser, Content: "Hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildRequest error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["model"] != "deepseek-v4-flash" {
		t.Errorf("body[model] = %q, want %q", body["model"], "deepseek-v4-flash")
	}
}

// REGRESSION: no model override → use default from ClientConfig.
func TestDeepSeekAdapter_ModelOverrideEmpty(t *testing.T) {
	cfg := ClientConfig{
		Provider: ProviderDeepSeek,
		APIKey:   "sk-test",
		Model:    "deepseek-v4-pro",
	}
	adapter := newDeepSeekAdapter(cfg)

	req, err := adapter.BuildRequest(context.Background(), []Message{{Role: RoleUser, Content: "Hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildRequest error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["model"] != "deepseek-v4-pro" {
		t.Errorf("body[model] = %q, want %q", body["model"], "deepseek-v4-pro")
	}
}
