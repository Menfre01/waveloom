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

// TestDeepSeekBuildRequestMaxTokensOverride 验证 per-request max_tokens
// context 覆盖写入请求 body(压缩摘要等场景需显式输出上限,
// 否则服务端默认上限可能截断长 JSON 输出)。
func TestDeepSeekBuildRequestMaxTokensOverride(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{
		APIKey:  "sk-deepseek",
		Model:   "deepseek-v4-pro",
		BaseURL: "https://api.deepseek.com",
	})

	ctx := WithMaxTokens(context.Background(), 8000)
	req, err := adapter.BuildRequest(ctx, []Message{{Role: RoleUser, Content: "Hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}
	if body["max_tokens"] != float64(8000) {
		t.Errorf("max_tokens = %v, want 8000 (ctx override)", body["max_tokens"])
	}

	// 无覆盖时不应写入 max_tokens
	req2, err := adapter.BuildRequest(context.Background(), []Message{{Role: RoleUser, Content: "Hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}
	var body2 map[string]any
	if err := json.NewDecoder(req2.Body).Decode(&body2); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}
	if _, exists := body2["max_tokens"]; exists {
		t.Errorf("max_tokens 不应在无 ctx 覆盖时出现: %v", body2["max_tokens"])
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
		Model:          "deepseek-v4-pro",
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
		Model:          "deepseek-v4-pro",
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
		return
	}
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
		return
	}
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

// --- Responses API 模式(deepseek-v4-flash)---

func newResponsesAdapter() *deepSeekAdapter {
	return newDeepSeekAdapter(ClientConfig{
		Provider: ProviderDeepSeek,
		APIKey:   "sk-deepseek",
		Model:    ModelDeepSeekV4Flash,
		BaseURL:  "https://api.deepseek.com",
	})
}

// TestResponsesBuildRequest_Endpoint 验证 v4-flash 走 /v1/responses,v4-pro 走 chat/completions。
func TestResponsesBuildRequest_Endpoint(t *testing.T) {
	flash := newResponsesAdapter()
	req, err := flash.BuildRequest(context.Background(), []Message{{Role: RoleUser, Content: "Hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildRequest error: %v", err)
	}
	if req.URL.String() != "https://api.deepseek.com/v1/responses" {
		t.Errorf("URL = %q, want /v1/responses", req.URL.String())
	}

	pro := newDeepSeekAdapter(ClientConfig{
		Provider: ProviderDeepSeek,
		APIKey:   "sk-deepseek",
		Model:    "deepseek-v4-pro",
		BaseURL:  "https://api.deepseek.com",
	})
	req, err = pro.BuildRequest(context.Background(), []Message{{Role: RoleUser, Content: "Hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildRequest error: %v", err)
	}
	if req.URL.String() != "https://api.deepseek.com/v1/chat/completions" {
		t.Errorf("URL = %q, want /v1/chat/completions", req.URL.String())
	}
}

// TestResponsesBuildRequest_ModelOverride 验证 ModelOverride 切换模型时每请求独立判定端点。
func TestResponsesBuildRequest_ModelOverride(t *testing.T) {
	// 配置为 v4-pro(chat),但 override 为 v4-flash → 走 responses
	pro := newDeepSeekAdapter(ClientConfig{
		Provider: ProviderDeepSeek,
		APIKey:   "sk-deepseek",
		Model:    "deepseek-v4-pro",
		BaseURL:  "https://api.deepseek.com",
	})
	ctx := WithModelOverride(context.Background(), ModelDeepSeekV4Flash)
	req, err := pro.BuildRequest(ctx, []Message{{Role: RoleUser, Content: "Hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildRequest error: %v", err)
	}
	if req.URL.String() != "https://api.deepseek.com/v1/responses" {
		t.Errorf("override flash URL = %q, want /v1/responses", req.URL.String())
	}

	// 配置为 v4-flash,override 为 v4-pro → 走 chat
	flash := newResponsesAdapter()
	ctx = WithModelOverride(context.Background(), "deepseek-v4-pro")
	req, err = flash.BuildRequest(ctx, []Message{{Role: RoleUser, Content: "Hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildRequest error: %v", err)
	}
	if req.URL.String() != "https://api.deepseek.com/v1/chat/completions" {
		t.Errorf("override pro URL = %q, want /v1/chat/completions", req.URL.String())
	}
}

// TestResponsesBuildRequest_Instructions 验证首条 system 提取为 instructions,
// 后续 system 保留为 input item。
func TestResponsesBuildRequest_Instructions(t *testing.T) {
	adapter := newResponsesAdapter()
	messages := []Message{
		{Role: RoleSystem, Content: "You are a helpful assistant."},
		{Role: RoleSystem, Content: "Extra rules."},
		{Role: RoleUser, Content: "Hi"},
	}
	req, err := adapter.BuildRequest(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("BuildRequest error: %v", err)
	}
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if body["instructions"] != "You are a helpful assistant." {
		t.Errorf("instructions = %q, want first system message", body["instructions"])
	}
	input := body["input"].([]any)
	if len(input) != 2 {
		t.Fatalf("input len = %d, want 2 (extra system + user)", len(input))
	}
	sysItem := input[0].(map[string]any)
	if sysItem["role"] != "system" {
		t.Errorf("input[0].role = %v, want system", sysItem["role"])
	}
}

// TestResponsesBuildRequest_InputItems 验证消息 → input items 的完整转换
// (assistant tool_calls → function_call items;tool 消息 → function_call_output)。
func TestResponsesBuildRequest_InputItems(t *testing.T) {
	adapter := newResponsesAdapter()
	messages := []Message{
		{Role: RoleSystem, Content: "Sys"},
		{Role: RoleUser, Content: "read the file"},
		{Role: RoleAssistant, Content: "ok", ReasoningContent: "thinking...",
			ToolCalls: []ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`}}},
		{Role: RoleTool, ToolCallID: "call_1", Name: "read_file", Content: "file contents"},
	}
	req, err := adapter.BuildRequest(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("BuildRequest error: %v", err)
	}
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	input := body["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("input len = %d, want 5 (user, assistant, reasoning, function_call, function_call_output)", len(input))
	}

	userItem := input[0].(map[string]any)
	content := userItem["content"].([]any)
	if userItem["role"] != "user" || content[0].(map[string]any)["type"] != "input_text" {
		t.Errorf("input[0] = %v, want user input_text", userItem)
	}

	asstItem := input[1].(map[string]any)
	asstContent := asstItem["content"].([]any)
	if asstItem["role"] != "assistant" || asstContent[0].(map[string]any)["type"] != "output_text" {
		t.Errorf("input[1] = %v, want assistant output_text", asstItem)
	}

	reasoningItem := input[2].(map[string]any)
	if reasoningItem["type"] != "reasoning" {
		t.Errorf("input[2].type = %v, want reasoning", reasoningItem["type"])
	}

	fcItem := input[3].(map[string]any)
	if fcItem["type"] != "function_call" || fcItem["call_id"] != "call_1" ||
		fcItem["name"] != "read_file" || fcItem["arguments"] != `{"path":"a.go"}` {
		t.Errorf("input[3] = %v, want function_call item", fcItem)
	}

	fcoItem := input[4].(map[string]any)
	if fcoItem["type"] != "function_call_output" || fcoItem["call_id"] != "call_1" ||
		fcoItem["output"] != "file contents" {
		t.Errorf("input[4] = %v, want function_call_output item", fcoItem)
	}
}

// TestResponsesBuildRequest_WebSearchTool 验证 web_search 转为服务端声明,
// 其他工具保持 function 声明。
func TestResponsesBuildRequest_WebSearchTool(t *testing.T) {
	adapter := newResponsesAdapter()
	tools := []ToolSpec{
		{Name: "web_search", Description: "Search the web"},
		{Name: "read_file", Description: "Read a file", Parameters: map[string]any{"type": "object"}},
	}
	req, err := adapter.BuildRequest(context.Background(), []Message{{Role: RoleUser, Content: "Hi"}}, tools)
	if err != nil {
		t.Fatalf("BuildRequest error: %v", err)
	}
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	toolsArr := body["tools"].([]any)
	if len(toolsArr) != 2 {
		t.Fatalf("tools len = %d, want 2 (web_search 声明 + read_file function)", len(toolsArr))
	}
	// 实现顺序:function 声明在前,web_search 服务端声明最后追加
	fnTool := toolsArr[0].(map[string]any)
	if fnTool["type"] != "function" || fnTool["name"] != "read_file" {
		t.Errorf("tools[0] = %v, want function read_file", fnTool)
	}
	wsTool := toolsArr[1].(map[string]any)
	if wsTool["type"] != "web_search" {
		t.Errorf("tools[1] = %v, want {type: web_search}", wsTool)
	}
	if _, hasName := wsTool["name"]; hasName {
		t.Errorf("web_search 声明不应包含 function schema 字段 name")
	}
}

// TestResponsesBuildRequest_ParamsMapping 验证参数映射:
// max_tokens → max_output_tokens;reasoning_effort → reasoning.effort(原始值);
// response_format → text.format。
func TestResponsesBuildRequest_ParamsMapping(t *testing.T) {
	adapter := newDeepSeekAdapter(ClientConfig{
		Provider:       ProviderDeepSeek,
		APIKey:         "sk-deepseek",
		Model:          ModelDeepSeekV4Flash,
		BaseURL:        "https://api.deepseek.com",
		ResponseFormat: "json_object",
		ExtraParams:    map[string]any{"reasoning_effort": "low", "max_tokens": 4096},
	})
	ctx := WithMaxTokens(context.Background(), 2048)
	req, err := adapter.BuildRequest(ctx, []Message{{Role: RoleUser, Content: "Hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildRequest error: %v", err)
	}
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if maxOut, ok := body["max_output_tokens"].(float64); !ok || int(maxOut) != 2048 {
		t.Errorf("max_output_tokens = %v, want 2048 (ctx override 优先)", body["max_output_tokens"])
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "low" {
		t.Errorf("reasoning = %v, want {effort: low}(原始值,不经 chat 重映射)", body["reasoning"])
	}
	text, ok := body["text"].(map[string]any)
	if !ok {
		t.Fatalf("text = %v, want text.format", body["text"])
	}
	format := text["format"].(map[string]any)
	if format["type"] != "json_object" {
		t.Errorf("text.format.type = %v, want json_object", format["type"])
	}
}

// TestResponsesParseResponse_Text 验证非流式文本响应解析。
func TestResponsesParseResponse_Text(t *testing.T) {
	adapter := newResponsesAdapter()
	body := []byte(`{
		"object": "response",
		"status": "completed",
		"model": "deepseek-v4-flash",
		"output": [
			{"type": "message", "role": "assistant", "content": [
				{"type": "output_text", "text": "Hello "},
				{"type": "output_text", "text": "world"}
			]}
		]
	}`)
	resp, err := adapter.ParseResponse(body)
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}
	if resp.Content != "Hello world" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello world")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %v, want empty", resp.ToolCalls)
	}
}

// TestResponsesParseResponse_FunctionCall 验证 function_call item 提取 + web_search_call 忽略。
func TestResponsesParseResponse_FunctionCall(t *testing.T) {
	adapter := newResponsesAdapter()
	body := []byte(`{
		"object": "response",
		"status": "completed",
		"output": [
			{"type": "function_call", "call_id": "call_1", "name": "read_file", "arguments": "{\"path\":\"a.go\"}"},
			{"type": "web_search_call", "id": "ws_1", "status": "completed"}
		]
	}`)
	resp, err := adapter.ParseResponse(body)
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "read_file" || tc.Arguments != `{"path":"a.go"}` {
		t.Errorf("ToolCall = %+v, want call_1/read_file", tc)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want tool_calls", resp.FinishReason)
	}
}

// TestResponsesParseResponse_Usage 验证 usage 映射(含 reasoning_tokens / cached_tokens)。
func TestResponsesParseResponse_Usage(t *testing.T) {
	adapter := newResponsesAdapter()
	body := []byte(`{
		"object": "response",
		"status": "completed",
		"usage": {
			"input_tokens": 100,
			"output_tokens": 50,
			"total_tokens": 150,
			"input_tokens_details": {"cached_tokens": 70, "cache_miss_tokens": 30},
			"output_tokens_details": {"reasoning_tokens": 20}
		}
	}`)
	resp, err := adapter.ParseResponse(body)
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("Usage = nil, want non-nil")
	}
	if resp.Usage.PromptTokens != 100 || resp.Usage.CompletionTokens != 50 || resp.Usage.TotalTokens != 150 {
		t.Errorf("usage tokens = %+v, want 100/50/150", resp.Usage)
	}
	if resp.Usage.CacheHitTokens != 70 || resp.Usage.CacheMissTokens != 30 {
		t.Errorf("cache tokens = hit:%d miss:%d, want 70/30", resp.Usage.CacheHitTokens, resp.Usage.CacheMissTokens)
	}
	if resp.Usage.ReasoningTokens != 20 {
		t.Errorf("ReasoningTokens = %d, want 20", resp.Usage.ReasoningTokens)
	}
}

// TestResponsesParseResponse_Failed 验证 failed 状态返回 NonRetryableError。
func TestResponsesParseResponse_Failed(t *testing.T) {
	adapter := newResponsesAdapter()
	body := []byte(`{
		"object": "response",
		"status": "failed",
		"error": {"code": "rate_limit_exceeded", "message": "too many requests"}
	}`)
	_, err := adapter.ParseResponse(body)
	if err == nil {
		t.Fatal("ParseResponse error = nil, want NonRetryableError")
	}
	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("err type = %T, want NonRetryableError", err)
	}
	if !strings.Contains(nre.Message, "too many requests") {
		t.Errorf("message = %q, want include error detail", nre.Message)
	}
}

// TestResponsesParseStreamEvent_Delta 验证 output_text.delta → Delta。
func TestResponsesParseStreamEvent_Delta(t *testing.T) {
	adapter := newResponsesAdapter()
	ev, err := adapter.ParseStreamEvent([]byte(`{"type":"response.output_text.delta","delta":"Hello"}`))
	if err != nil {
		t.Fatalf("ParseStreamEvent error: %v", err)
	}
	if ev.Delta != "Hello" {
		t.Errorf("Delta = %q, want Hello", ev.Delta)
	}
	if ev.Done {
		t.Error("Done = true, want false")
	}
}

// TestResponsesParseStreamEvent_Reasoning 验证 reasoning_text.delta → ReasoningDelta。
func TestResponsesParseStreamEvent_Reasoning(t *testing.T) {
	adapter := newResponsesAdapter()
	ev, err := adapter.ParseStreamEvent([]byte(`{"type":"response.reasoning_text.delta","delta":"thinking"}`))
	if err != nil {
		t.Fatalf("ParseStreamEvent error: %v", err)
	}
	if ev.ReasoningDelta != "thinking" {
		t.Errorf("ReasoningDelta = %q, want thinking", ev.ReasoningDelta)
	}
}

// TestResponsesParseStreamEvent_FunctionCall 验证 function_call 流式累积:
// output_item.added → ID/Name;delta → 参数增量;done → 完整参数(ToolCallReplace)。
func TestResponsesParseStreamEvent_FunctionCall(t *testing.T) {
	adapter := newResponsesAdapter()

	ev, err := adapter.ParseStreamEvent([]byte(
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":""}}`))
	if err != nil {
		t.Fatalf("added error: %v", err)
	}
	if len(ev.ToolCalls) != 1 || ev.ToolCalls[0].ID != "call_1" || ev.ToolCalls[0].Name != "read_file" || ev.ToolCalls[0].Index != 1 {
		t.Errorf("added ev = %+v, want call_1/read_file @index 1", ev)
	}

	ev, err = adapter.ParseStreamEvent([]byte(`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"path\":"}`))
	if err != nil {
		t.Fatalf("delta error: %v", err)
	}
	if len(ev.ToolCalls) != 1 || ev.ToolCalls[0].Arguments != `{"path":` || ev.ToolCalls[0].Index != 1 {
		t.Errorf("delta ev = %+v, want arguments fragment @index 1", ev)
	}

	ev, err = adapter.ParseStreamEvent([]byte(`{"type":"response.function_call_arguments.done","output_index":1,"arguments":"{\"path\":\"a.go\"}"}`))
	if err != nil {
		t.Fatalf("done error: %v", err)
	}
	if len(ev.ToolCalls) != 1 || ev.ToolCalls[0].Arguments != `{"path":"a.go"}` {
		t.Errorf("done ev = %+v, want full arguments", ev)
	}
	if !ev.ToolCallReplace {
		t.Error("ToolCallReplace = false, want true (完整参数应覆盖累积)")
	}
}

// TestResponsesParseStreamEvent_WebSearch 验证 web_search_call 状态透传。
func TestResponsesParseStreamEvent_WebSearch(t *testing.T) {
	adapter := newResponsesAdapter()
	ev, err := adapter.ParseStreamEvent([]byte(
		`{"type":"response.web_search_call.in_progress","item":{"type":"web_search_call","id":"ws_1","status":"in_progress"}}`))
	if err != nil {
		t.Fatalf("in_progress error: %v", err)
	}
	if ev.WebSearchStatus != "in_progress" || ev.WebSearchCallID != "ws_1" {
		t.Errorf("ev = status:%q id:%q, want in_progress/ws_1", ev.WebSearchStatus, ev.WebSearchCallID)
	}

	ev, err = adapter.ParseStreamEvent([]byte(
		`{"type":"response.web_search_call.completed","item":{"type":"web_search_call","id":"ws_1","status":"completed"}}`))
	if err != nil {
		t.Fatalf("completed error: %v", err)
	}
	if ev.WebSearchStatus != "completed" {
		t.Errorf("WebSearchStatus = %q, want completed", ev.WebSearchStatus)
	}

	// 防御性解析:OpenAI 兼容 search_queries(DeepSeek 文档未承诺,若返回则透传)
	ev, err = adapter.ParseStreamEvent([]byte(
		`{"type":"response.web_search_call.completed","item":{"type":"web_search_call","id":"ws_1","status":"completed","search_queries":["go 1.25","deepseek api"]}}`))
	if err != nil {
		t.Fatalf("completed with queries error: %v", err)
	}
	if len(ev.WebSearchQueries) != 2 || ev.WebSearchQueries[0] != "go 1.25" || ev.WebSearchQueries[1] != "deepseek api" {
		t.Errorf("WebSearchQueries = %v, want [go 1.25 deepseek api]", ev.WebSearchQueries)
	}
}

// TestResponsesParseStreamEvent_Completed 验证 completed 事件 → Done + Usage。
func TestResponsesParseStreamEvent_Completed(t *testing.T) {
	adapter := newResponsesAdapter()
	ev, err := adapter.ParseStreamEvent([]byte(`{
		"type": "response.completed",
		"response": {
			"object": "response",
			"status": "completed",
			"model": "deepseek-v4-flash",
			"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
		}
	}`))
	if err != nil {
		t.Fatalf("ParseStreamEvent error: %v", err)
	}
	if !ev.Done {
		t.Fatal("Done = false, want true")
	}
	if ev.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", ev.FinishReason)
	}
	if ev.Err != nil {
		t.Errorf("Err = %v, want nil", ev.Err)
	}
	if ev.Usage == nil || ev.Usage.PromptTokens != 10 || ev.Usage.CompletionTokens != 5 {
		t.Errorf("Usage = %+v, want 10/5", ev.Usage)
	}
	if ev.Model != "deepseek-v4-flash" {
		t.Errorf("Model = %q, want deepseek-v4-flash", ev.Model)
	}
}

// TestResponsesParseStreamEvent_Failed 验证 failed 事件 → Done + Err。
func TestResponsesParseStreamEvent_Failed(t *testing.T) {
	adapter := newResponsesAdapter()
	ev, err := adapter.ParseStreamEvent([]byte(`{
		"type": "response.failed",
		"response": {"object": "response", "status": "failed", "error": {"code": "x", "message": "boom"}}
	}`))
	if err != nil {
		t.Fatalf("ParseStreamEvent error: %v", err)
	}
	if !ev.Done {
		t.Fatal("Done = false, want true")
	}
	if ev.Err == nil || !strings.Contains(ev.Err.Error(), "boom") {
		t.Errorf("Err = %v, want include boom", ev.Err)
	}
}

// TestResponsesParseStreamEvent_IgnoredEvents 验证无关事件(created/in_progress/content_part)静默忽略。
func TestResponsesParseStreamEvent_IgnoredEvents(t *testing.T) {
	adapter := newResponsesAdapter()
	for _, data := range []string{
		`{"type":"response.created","response":{"object":"response","model":"deepseek-v4-flash"}}`,
		`{"type":"response.in_progress"}`,
		`{"type":"response.content_part.added","part":{"type":"output_text","text":""}}`,
		`{"type":"response.output_item.done"}`,
	} {
		ev, err := adapter.ParseStreamEvent([]byte(data))
		if err != nil {
			t.Fatalf("ParseStreamEvent(%s) error: %v", data, err)
		}
		if ev.Delta != "" || ev.Done || len(ev.ToolCalls) != 0 {
			t.Errorf("ParseStreamEvent(%s) = %+v, want zero event", data, ev)
		}
	}
}

// TestResponsesFormatDetection 验证格式探测:chat chunk 不带 type 字段 → chat 分支;
// responses 事件带 response.* type → responses 分支。
func TestResponsesFormatDetection(t *testing.T) {
	adapter := newResponsesAdapter()

	// chat chunk(无 type 字段)→ 按 chat 解析
	ev, err := adapter.ParseStreamEvent([]byte(`{"choices":[{"finish_reason":null,"delta":{"content":"Hi"}}]}`))
	if err != nil {
		t.Fatalf("chat chunk error: %v", err)
	}
	if ev.Delta != "Hi" {
		t.Errorf("chat Delta = %q, want Hi", ev.Delta)
	}

	// chat 非流式响应(带 choices)→ chat 分支
	resp, err := adapter.ParseResponse([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Hi"}}]}`))
	if err != nil {
		t.Fatalf("chat response error: %v", err)
	}
	if resp.Content != "Hi" {
		t.Errorf("chat Content = %q, want Hi", resp.Content)
	}
}

// TestResponsesBuildStreamRequest 验证流式请求体(stream=true)同样走 responses 格式。
func TestResponsesBuildStreamRequest(t *testing.T) {
	adapter := newResponsesAdapter()
	req, err := adapter.BuildStreamRequest(context.Background(),
		[]Message{{Role: RoleUser, Content: "Hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildStreamRequest error: %v", err)
	}
	if req.URL.String() != "https://api.deepseek.com/v1/responses" {
		t.Errorf("URL = %q, want /v1/responses", req.URL.String())
	}
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	if _, hasInput := body["input"]; !hasInput {
		t.Error("input 缺失,responses 请求应使用 input items 而非 messages")
	}
	if _, hasMessages := body["messages"]; hasMessages {
		t.Error("messages 不应出现在 responses 请求体中")
	}
}

// TestResponsesBuildRequest_InputEdgeCases 验证 input 转换边界:
//  1. 首条消息非 system → 无 instructions 字段
//  2. assistant 无 tool_calls 但带 reasoning_content → 不输出 reasoning item
//     (明文归并到 assistant 消息,避免冗余 token)
func TestResponsesBuildRequest_InputEdgeCases(t *testing.T) {
	adapter := newResponsesAdapter()

	// 1. 无 system 消息 → body 不含 instructions
	req, err := adapter.BuildRequest(context.Background(),
		[]Message{{Role: RoleUser, Content: "Hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildRequest error: %v", err)
	}
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, hasInstructions := body["instructions"]; hasInstructions {
		t.Error("instructions 不应存在(首条消息非 system)")
	}

	// 2. assistant 无 tool_calls 带 reasoning → 仅 message item,无 reasoning item
	req, err = adapter.BuildRequest(context.Background(), []Message{
		{Role: RoleUser, Content: "question"},
		{Role: RoleAssistant, Content: "answer", ReasoningContent: "thinking but no tools"},
	}, nil)
	if err != nil {
		t.Fatalf("BuildRequest error: %v", err)
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	input := body["input"].([]any)
	if len(input) != 2 {
		t.Fatalf("input len = %d, want 2 (user + assistant,无 reasoning item)", len(input))
	}
	for _, item := range input {
		if m := item.(map[string]any); m["type"] == "reasoning" {
			t.Error("无 tool_calls 的 assistant 不应输出 reasoning item")
		}
	}
}

// TestResponsesBuildRequest_NoWebSearchTool 验证 tools 列表不含 web_search 时
// 不声明服务端 web_search 工具(避免意外启用服务端搜索)。
func TestResponsesBuildRequest_NoWebSearchTool(t *testing.T) {
	adapter := newResponsesAdapter()
	tools := []ToolSpec{
		{Name: "read_file", Description: "Read a file", Parameters: map[string]any{"type": "object"}},
	}
	req, err := adapter.BuildRequest(context.Background(),
		[]Message{{Role: RoleUser, Content: "Hi"}}, tools)
	if err != nil {
		t.Fatalf("BuildRequest error: %v", err)
	}
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	toolsArr := body["tools"].([]any)
	if len(toolsArr) != 1 {
		t.Fatalf("tools len = %d, want 1 (仅 read_file function)", len(toolsArr))
	}
	if t0 := toolsArr[0].(map[string]any); t0["type"] != "function" {
		t.Errorf("tools[0] = %v, want function 声明", t0)
	}
}

// TestResponsesParseResponse_EmptyOutput 验证 output 为空或仅含 web_search_call
// 时返回空内容 + 空 tool_calls,不崩溃(服务端纯搜索无文本输出的场景)。
func TestResponsesParseResponse_EmptyOutput(t *testing.T) {
	adapter := newResponsesAdapter()
	body := []byte(`{
		"object": "response",
		"status": "completed",
		"output": [
			{"type": "web_search_call", "id": "ws_1", "status": "completed"}
		]
	}`)
	resp, err := adapter.ParseResponse(body)
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}
	if resp.Content != "" {
		t.Errorf("Content = %q, want empty", resp.Content)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %v, want empty", resp.ToolCalls)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", resp.FinishReason)
	}
}

// TestResponsesParseStreamEvent_Incomplete 验证 incomplete 事件 → Done + length。
func TestResponsesParseStreamEvent_Incomplete(t *testing.T) {
	adapter := newResponsesAdapter()
	ev, err := adapter.ParseStreamEvent([]byte(`{
		"type": "response.incomplete",
		"response": {
			"object": "response",
			"status": "incomplete",
			"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
		}
	}`))
	if err != nil {
		t.Fatalf("ParseStreamEvent error: %v", err)
	}
	if !ev.Done {
		t.Fatal("Done = false, want true")
	}
	if ev.FinishReason != "length" {
		t.Errorf("FinishReason = %q, want length", ev.FinishReason)
	}
	if ev.Usage == nil || ev.Usage.TotalTokens != 15 {
		t.Errorf("Usage = %+v, want total 15", ev.Usage)
	}
}
