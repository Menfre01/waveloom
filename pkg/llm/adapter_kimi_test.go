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

// --- Kimi Adapter Tests ---

func TestKimiBuildRequest(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{
		APIKey:  "sk-test",
		Model:   "kimi-k3",
		BaseURL: "https://api.moonshot.cn/v1",
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

	// URL 不应重复 /v1（baseURL 已含 /v1）
	expectedURL := "https://api.moonshot.cn/v1/chat/completions"
	if req.URL.String() != expectedURL {
		t.Errorf("URL = %q, want %q", req.URL.String(), expectedURL)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	if body["model"] != "kimi-k3" {
		t.Errorf("model = %v, want kimi-k3", body["model"])
	}
	if body["stream"] != false {
		t.Errorf("stream = %v, want false", body["stream"])
	}

	// reasoning_effort 应为 "max"
	if body["reasoning_effort"] != "max" {
		t.Errorf("reasoning_effort = %v, want max", body["reasoning_effort"])
	}

	// messages 应直接传递（不剥离 reasoning）
	msgs, ok := body["messages"].([]any)
	if !ok {
		t.Fatal("messages is not a slice")
	}
	if len(msgs) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(msgs))
	}

	// 验证 tools
	toolsBody, ok := body["tools"].([]any)
	if !ok {
		t.Fatal("tools is not a slice")
	}
	if len(toolsBody) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(toolsBody))
	}
}

func TestKimiBuildRequestDefaultBaseURL(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{
		APIKey: "sk-test",
		Model:  "kimi-k3",
	})

	if adapter.BaseURL() != "https://api.moonshot.cn/v1" {
		t.Errorf("default BaseURL = %q, want https://api.moonshot.cn/v1", adapter.BaseURL())
	}
}

func TestKimiBuildRequestFixedParamsFiltered(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{
		APIKey: "sk-test",
		Model:  "kimi-k3",
		ExtraParams: map[string]any{
			"thinking":          map[string]any{"type": "enabled"},
			"temperature":       0.7,
			"top_p":             0.9,
			"n":                 2,
			"presence_penalty":  0.5,
			"frequency_penalty": 0.3,
			"custom_param":      "keep-me",
		},
	})

	req, err := adapter.BuildRequest(context.Background(), []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	for _, key := range []string{"thinking", "temperature", "top_p", "n", "presence_penalty", "frequency_penalty"} {
		if _, exists := body[key]; exists {
			t.Errorf("fixed param %q should have been filtered, but is present", key)
		}
	}
	if _, exists := body["custom_param"]; !exists {
		t.Error("non-fixed param 'custom_param' should be preserved")
	}
}

func TestKimiBuildRequestMessagesNotStripped(t *testing.T) {
	// 验证 reasoning_content 不被剥离（与 DeepSeek adapter 的关键区别）
	adapter := newKimiAdapter(ClientConfig{
		APIKey: "sk-test",
		Model:  "kimi-k3",
	})

	messages := []Message{
		{Role: RoleAssistant, Content: "answer", ReasoningContent: "thinking process"},
		{Role: RoleUser, Content: "next"},
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
	firstMsg := msgs[0].(map[string]any)
	if rc, ok := firstMsg["reasoning_content"]; !ok || rc != "thinking process" {
		t.Errorf("reasoning_content should be preserved, got %v", rc)
	}
}

func TestKimiBuildStreamRequest(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{
		APIKey: "sk-test",
		Model:  "kimi-k3",
	})

	req, err := adapter.BuildStreamRequest(context.Background(), []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("BuildStreamRequest returned error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	if stream, ok := body["stream"]; !ok || stream != true {
		t.Errorf("stream = %v, want true", stream)
	}

	so, ok := body["stream_options"].(map[string]any)
	if !ok {
		t.Fatal("stream_options missing or wrong type")
	}
	if includeUsage, ok := so["include_usage"]; !ok || includeUsage != true {
		t.Errorf("stream_options.include_usage = %v, want true", includeUsage)
	}
}

// REGRESSION: 流式响应默认不返回 usage，必须显式开启 include_usage
// 才能让 TUI 的 ctx 进度条和 cache 数值有值。
func TestRegression_KimiStreamRequestsUsage(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{
		APIKey: "sk-test",
		Model:  "kimi-k3",
	})

	req, err := adapter.BuildStreamRequest(context.Background(), []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("BuildStreamRequest returned error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	so, ok := body["stream_options"].(map[string]any)
	if !ok {
		t.Fatal("stream_options missing or wrong type")
	}
	if includeUsage, ok := so["include_usage"]; !ok || includeUsage != true {
		t.Errorf("stream_options.include_usage = %v, want true", includeUsage)
	}

	// 非流式请求不应发送 stream_options
	req2, err := adapter.BuildRequest(context.Background(), []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}
	var body2 map[string]any
	if err := json.NewDecoder(req2.Body).Decode(&body2); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}
	if _, exists := body2["stream_options"]; exists {
		t.Error("stream_options should not be present in non-streaming request")
	}
}

// --- ParseResponse Tests ---

func TestKimiParseResponse(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{Model: "kimi-k3"})

	respJSON := `{
		"id": "cmpl-xxx",
		"model": "kimi-k3",
		"choices": [{
			"finish_reason": "stop",
			"message": {
				"role": "assistant",
				"content": "Hello, world!",
				"reasoning_content": "The user is greeting me, I should respond warmly."
			}
		}],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 20,
			"total_tokens": 30,
			"cached_tokens": 5
		}
	}`

	resp, err := adapter.ParseResponse([]byte(respJSON))
	if err != nil {
		t.Fatalf("ParseResponse returned error: %v", err)
	}

	if resp.Content != "Hello, world!" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello, world!")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", resp.FinishReason)
	}
	if resp.ReasoningContent != "The user is greeting me, I should respond warmly." {
		t.Errorf("ReasoningContent = %q", resp.ReasoningContent)
	}
	if resp.Model != "kimi-k3" {
		t.Errorf("Model = %q, want kimi-k3", resp.Model)
	}

	if resp.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if resp.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", resp.Usage.PromptTokens)
	}
	if resp.Usage.CompletionTokens != 20 {
		t.Errorf("CompletionTokens = %d, want 20", resp.Usage.CompletionTokens)
	}
	if resp.Usage.TotalTokens != 30 {
		t.Errorf("TotalTokens = %d, want 30", resp.Usage.TotalTokens)
	}
	// Kimi 仅返回 cached_tokens → 映射到 CacheHitTokens
	if resp.Usage.CacheHitTokens != 5 {
		t.Errorf("CacheHitTokens = %d, want 5", resp.Usage.CacheHitTokens)
	}
	// Kimi 不返回 CacheMissTokens；用 prompt_tokens - cached_tokens 估算
	if resp.Usage.CacheMissTokens != 5 {
		t.Errorf("CacheMissTokens = %d, want 5", resp.Usage.CacheMissTokens)
	}
	// Kimi 无 ReasoningTokens
	if resp.Usage.ReasoningTokens != 0 {
		t.Errorf("ReasoningTokens = %d, want 0", resp.Usage.ReasoningTokens)
	}
}

func TestKimiParseResponseWithToolCalls(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{Model: "kimi-k3"})

	respJSON := `{
		"id": "cmpl-xxx",
		"model": "kimi-k3",
		"choices": [{
			"finish_reason": "tool_calls",
			"message": {
				"role": "assistant",
				"content": "",
				"tool_calls": [{
					"id": "call_abc",
					"type": "function",
					"function": {
						"name": "read_file",
						"arguments": "{\"path\":\"/tmp/test\"}"
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
	if tc.ID != "call_abc" {
		t.Errorf("ToolCall.ID = %q, want call_abc", tc.ID)
	}
	if tc.Name != "read_file" {
		t.Errorf("ToolCall.Name = %q, want read_file", tc.Name)
	}
	if tc.Arguments != `{"path":"/tmp/test"}` {
		t.Errorf("ToolCall.Arguments = %q", tc.Arguments)
	}
}

func TestKimiParseResponseNoChoices(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{Model: "kimi-k3"})

	_, err := adapter.ParseResponse([]byte(`{"choices": []}`))
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("expected *NonRetryableError, got %T: %v", err, err)
	}
}

func TestKimiParseResponseMalformed(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{Model: "kimi-k3"})

	_, err := adapter.ParseResponse([]byte(`<html>Not JSON</html>`))
	var re *RetryableError
	if !errors.As(err, &re) {
		t.Errorf("expected *RetryableError for malformed JSON, got %T: %v", err, err)
	}
}

// --- ParseStreamEvent Tests ---

func TestKimiParseStreamEvent(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{Model: "kimi-k3"})

	// 中间 chunk: 有 delta content，无 finish_reason
	chunkJSON := `{
		"id": "cmpl-xxx",
		"model": "kimi-k3",
		"choices": [{
			"delta": {"content": "Hello"},
			"finish_reason": null
		}]
	}`

	ev, err := adapter.ParseStreamEvent([]byte(chunkJSON))
	if err != nil {
		t.Fatalf("ParseStreamEvent returned error: %v", err)
	}
	if ev.Delta != "Hello" {
		t.Errorf("Delta = %q, want Hello", ev.Delta)
	}
	if ev.Done {
		t.Error("Done should be false for intermediate chunk")
	}
}

func TestKimiParseStreamEventReasoning(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{Model: "kimi-k3"})

	chunkJSON := `{
		"id": "cmpl-xxx",
		"model": "kimi-k3",
		"choices": [{
			"delta": {"reasoning_content": "Let me think about this..."},
			"finish_reason": null
		}]
	}`

	ev, err := adapter.ParseStreamEvent([]byte(chunkJSON))
	if err != nil {
		t.Fatalf("ParseStreamEvent returned error: %v", err)
	}
	if ev.ReasoningDelta != "Let me think about this..." {
		t.Errorf("ReasoningDelta = %q", ev.ReasoningDelta)
	}
}

func TestKimiParseStreamEventToolCalls(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{Model: "kimi-k3"})

	chunkJSON := `{
		"id": "cmpl-xxx",
		"model": "kimi-k3",
		"choices": [{
			"delta": {
				"tool_calls": [{
					"index": 0,
					"id": "call_abc",
					"type": "function",
					"function": {"name": "read_file", "arguments": "{\"path\":"}
				}]
			},
			"finish_reason": null
		}]
	}`

	ev, err := adapter.ParseStreamEvent([]byte(chunkJSON))
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
	if tc.ID != "call_abc" {
		t.Errorf("ID = %q, want call_abc", tc.ID)
	}
	if tc.Arguments != `{"path":` {
		t.Errorf("Arguments = %q", tc.Arguments)
	}
}

func TestKimiParseStreamEventFinalChunk(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{Model: "kimi-k3"})

	// 最后一帧: finish_reason + usage
	chunkJSON := `{
		"id": "cmpl-xxx",
		"model": "kimi-k3",
		"choices": [{
			"delta": {},
			"finish_reason": "stop"
		}],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 8,
			"total_tokens": 18,
			"cached_tokens": 5
		}
	}`

	ev, err := adapter.ParseStreamEvent([]byte(chunkJSON))
	if err != nil {
		t.Fatalf("ParseStreamEvent returned error: %v", err)
	}
	if ev.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", ev.FinishReason)
	}
	if ev.Usage == nil {
		t.Fatal("Usage is nil for final chunk")
	}
	if ev.Usage.CacheHitTokens != 5 {
		t.Errorf("CacheHitTokens = %d, want 5", ev.Usage.CacheHitTokens)
	}
	// Kimi 不返回 miss；按 prompt - cached 估算，应为 5
	if ev.Usage.CacheMissTokens != 5 {
		t.Errorf("CacheMissTokens = %d, want 5", ev.Usage.CacheMissTokens)
	}
}

// --- ClassifyError Tests ---


// REGRESSION: include_usage=true 时 Kimi 会返回一个 choices 为空的 usage-only chunk，
// 旧实现直接返回零值事件，导致 TUI 拿不到 token 统计。
func TestKimiParseStreamEventUsageOnlyChunk(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{Model: "kimi-k3"})

	chunkJSON := `{
		"id": "cmpl-xxx",
		"object": "chat.completion.chunk",
		"created": 1698999575,
		"model": "kimi-k3",
		"choices": [],
		"usage": {
			"prompt_tokens": 100,
			"completion_tokens": 20,
			"total_tokens": 120,
			"cached_tokens": 30
		}
	}`

	ev, err := adapter.ParseStreamEvent([]byte(chunkJSON))
	if err != nil {
		t.Fatalf("ParseStreamEvent returned error: %v", err)
	}
	if ev.Delta != "" || ev.ReasoningDelta != "" || ev.FinishReason != "" {
		t.Errorf("expected zero content event, got Delta=%q ReasoningDelta=%q FinishReason=%q", ev.Delta, ev.ReasoningDelta, ev.FinishReason)
	}
	if ev.Usage == nil {
		t.Fatal("Usage is nil for usage-only chunk")
	}
	if ev.Usage.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", ev.Usage.PromptTokens)
	}
	if ev.Usage.CompletionTokens != 20 {
		t.Errorf("CompletionTokens = %d, want 20", ev.Usage.CompletionTokens)
	}
	if ev.Usage.TotalTokens != 120 {
		t.Errorf("TotalTokens = %d, want 120", ev.Usage.TotalTokens)
	}
	if ev.Usage.CacheHitTokens != 30 {
		t.Errorf("CacheHitTokens = %d, want 30", ev.Usage.CacheHitTokens)
	}
	// Kimi 不返回 miss；按 prompt - cached 估算，应为 70
	if ev.Usage.CacheMissTokens != 70 {
		t.Errorf("CacheMissTokens = %d, want 70", ev.Usage.CacheMissTokens)
	}
}
func TestKimiClassifyError(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{Model: "kimi-k3"})

	tests := []struct {
		name  string
		err   error
		class ErrorClass
	}{
		{"retryable wrapped", &RetryableError{Message: "retry"}, ErrorClassRetryable},
		{"nonretryable wrapped", &NonRetryableError{Message: "bad"}, ErrorClassNonRetryable},
		{"http 429", &httpStatusError{StatusCode: 429}, ErrorClassRetryable},
		{"http 401", &httpStatusError{StatusCode: 401}, ErrorClassNonRetryable},
		{"http 400", &httpStatusError{StatusCode: 400}, ErrorClassNonRetryable},
		{"http 500", &httpStatusError{StatusCode: 500}, ErrorClassRetryable},
		{"http 503", &httpStatusError{StatusCode: 503}, ErrorClassRetryable},
		{"network error", fmt.Errorf("connect refused"), ErrorClassRetryable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			class := adapter.ClassifyError(tt.err)
			if class != tt.class {
				t.Errorf("ClassifyError(%v) = %v, want %v", tt.err, class, tt.class)
			}
		})
	}
}

// --- GetBalance Tests ---

func TestKimiGetBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/users/me/balance" {
			t.Errorf("Path = %q, want /users/me/balance", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("Authorization header missing or wrong")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"code": 0,
			"data": {
				"available_balance": 49.58894,
				"voucher_balance": 46.58893,
				"cash_balance": 3.00001
			},
			"scode": "0x0",
			"status": true
		}`)
	}))
	defer server.Close()

	adapter := newKimiAdapter(ClientConfig{
		APIKey:  "sk-test",
		BaseURL: server.URL,
	})

	balance, err := adapter.GetBalance(context.Background(), server.Client())
	if err != nil {
		t.Fatalf("GetBalance returned error: %v", err)
	}
	if !balance.IsAvailable {
		t.Error("IsAvailable should be true")
	}
	if len(balance.BalanceInfos) != 1 {
		t.Fatalf("len(BalanceInfos) = %d, want 1", len(balance.BalanceInfos))
	}
	bi := balance.BalanceInfos[0]
	if bi.Currency != "CNY" {
		t.Errorf("Currency = %q, want CNY", bi.Currency)
	}
	if bi.TotalBalance != "49.58894" {
		t.Errorf("TotalBalance = %q, want 49.58894", bi.TotalBalance)
	}
	if bi.GrantedBalance != "46.58893" {
		t.Errorf("GrantedBalance = %q, want 46.58893", bi.GrantedBalance)
	}
	if bi.ToppedUpBalance != "3.00001" {
		t.Errorf("ToppedUpBalance = %q, want 3.00001", bi.ToppedUpBalance)
	}
}

func TestKimiGetBalanceZeroBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"code": 0,
			"data": {
				"available_balance": 0,
				"voucher_balance": 0,
				"cash_balance": 0
			},
			"scode": "0x0",
			"status": true
		}`)
	}))
	defer server.Close()

	adapter := newKimiAdapter(ClientConfig{
		APIKey:  "sk-test",
		BaseURL: server.URL,
	})

	balance, err := adapter.GetBalance(context.Background(), server.Client())
	if err != nil {
		t.Fatalf("GetBalance returned error: %v", err)
	}
	if balance.IsAvailable {
		t.Error("IsAvailable should be false when balance is 0")
	}
}

func TestKimiSupportsBalance(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{APIKey: "sk-test"})
	if !adapter.SupportsBalance() {
		t.Error("SupportsBalance should return true")
	}
}

// --- ListModels Tests ---

func TestKimiListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/models" {
			t.Errorf("Path = %q, want /models", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"object": "list",
			"data": [
				{"id": "kimi-k3", "object": "model", "owned_by": "moonshot"},
				{"id": "kimi-k2.7-code", "object": "model", "owned_by": "moonshot"}
			]
		}`)
	}))
	defer server.Close()

	adapter := newKimiAdapter(ClientConfig{
		APIKey:  "sk-test",
		BaseURL: server.URL,
	})

	models, err := adapter.ListModels(context.Background(), server.Client())
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(models))
	}
	if models[0].ID != "kimi-k3" {
		t.Errorf("models[0].ID = %q, want kimi-k3", models[0].ID)
	}
	if models[1].ID != "kimi-k2.7-code" {
		t.Errorf("models[1].ID = %q, want kimi-k2.7-code", models[1].ID)
	}
}

// --- Auth Header Test ---

func TestKimiAuthHeader(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{APIKey: "sk-test-key"})
	key, value := adapter.AuthHeader()
	if key != "Authorization" {
		t.Errorf("key = %q, want Authorization", key)
	}
	if value != "Bearer sk-test-key" {
		t.Errorf("value = %q, want Bearer sk-test-key", value)
	}
}

// --- ResponseFormat Test ---

func TestKimiBuildRequestJSONMode(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{
		APIKey:         "sk-test",
		Model:          "kimi-k3",
		ResponseFormat: "json_object",
	})

	req, err := adapter.BuildRequest(context.Background(), []Message{
		{Role: RoleUser, Content: "Output JSON"},
	}, nil)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	rf, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatal("response_format missing or wrong type")
	}
	if rf["type"] != "json_object" {
		t.Errorf("response_format.type = %v, want json_object", rf["type"])
	}
}

// --- ModelOverrideFromContext Test ---

func TestKimiBuildRequestModelOverride(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{
		APIKey: "sk-test",
		Model:  "kimi-k3",
	})

	ctx := WithModelOverride(context.Background(), "kimi-k2.7-code")
	req, err := adapter.BuildRequest(ctx, []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	if body["model"] != "kimi-k2.7-code" {
		t.Errorf("model = %v, want kimi-k2.7-code", body["model"])
	}
}

// --- NewClient Integration Test ---

func TestNewClientWithKimi(t *testing.T) {
	client, err := NewClient(ClientConfig{
		Provider: ProviderKimi,
		APIKey:   "sk-test",
		Model:    "kimi-k3",
	})
	if err != nil {
		t.Fatalf("NewClient(ProviderKimi) returned error: %v", err)
	}
	if client == nil {
		t.Fatal("NewClient(ProviderKimi) returned nil")
	}
	// Verify SupportsBalance
	if !client.SupportsBalance() {
		t.Error("Kimi client should support balance query")
	}
}

// --- ExtraParams reasoning_effort Override ---

func TestKimiBuildRequestReasoningEffortOverride(t *testing.T) {
	// 用户可以通过 ExtraParams 覆盖 reasoning_effort（未来多档位上线后使用）
	adapter := newKimiAdapter(ClientConfig{
		APIKey: "sk-test",
		Model:  "kimi-k3",
		ExtraParams: map[string]any{
			"reasoning_effort": "custom_value",
		},
	})

	req, err := adapter.BuildRequest(context.Background(), []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode request body: %v", err)
	}

	if body["reasoning_effort"] != "custom_value" {
		t.Errorf("reasoning_effort = %v, want custom_value (user override)", body["reasoning_effort"])
	}
}

// --- ParseResponse Without Usage ---

func TestKimiParseResponseWithoutUsage(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{Model: "kimi-k3"})

	respJSON := `{
		"id": "cmpl-xxx",
		"model": "kimi-k3",
		"choices": [{
			"finish_reason": "stop",
			"message": {
				"role": "assistant",
				"content": "ok"
			}
		}]
	}`

	resp, err := adapter.ParseResponse([]byte(respJSON))
	if err != nil {
		t.Fatalf("ParseResponse returned error: %v", err)
	}
	if resp.Usage != nil {
		t.Errorf("Usage should be nil when not provided, got %+v", resp.Usage)
	}
}

// --- ParseStreamEvent Empty Choices ---

func TestKimiParseStreamEventEmptyChoices(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{Model: "kimi-k3"})

	ev, err := adapter.ParseStreamEvent([]byte(`{"choices": []}`))
	if err != nil {
		t.Fatalf("ParseStreamEvent returned error: %v", err)
	}
	// 应返回零值事件，不报错
	if ev.Delta != "" || ev.Done {
		t.Errorf("expected zero-value event for empty choices, got %+v", ev)
	}
}

// --- Custom BaseURL ---

func TestKimiCustomBaseURL(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{
		APIKey:  "sk-test",
		Model:   "kimi-k3",
		BaseURL: "https://custom.kimi.com/v1",
	})

	if adapter.BaseURL() != "https://custom.kimi.com/v1" {
		t.Errorf("BaseURL = %q", adapter.BaseURL())
	}

	req, err := adapter.BuildRequest(context.Background(), []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}

	if !strings.Contains(req.URL.String(), "custom.kimi.com") {
		t.Errorf("URL should use custom base URL, got %q", req.URL.String())
	}
}

// --- Auth Header ---

func TestKimiAuthHeaderBearer(t *testing.T) {
	adapter := newKimiAdapter(ClientConfig{APIKey: "moonshot-key-123"})
	key, value := adapter.AuthHeader()
	if key != "Authorization" {
		t.Errorf("key = %q, want Authorization", key)
	}
	if value != "Bearer moonshot-key-123" {
		t.Errorf("value = %q, want Bearer moonshot-key-123", value)
	}
}
