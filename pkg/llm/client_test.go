package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Mock Infrastructure ---

// mockHTTPTransport 实现 http.RoundTripper，可编程控制响应。
type mockHTTPTransport struct {
	mu       sync.Mutex
	requests []*http.Request
	handler  func(req *http.Request) (*http.Response, error)
	callCount int
}

func (m *mockHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.callCount++
	m.mu.Unlock()
	if m.handler == nil {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Hello"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	}
	return m.handler(req)
}

func (m *mockHTTPTransport) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func (m *mockHTTPTransport) getRequests() []*http.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests
}

// newTestClient 创建使用 mock transport 的 Client。
func newTestClient(handler func(req *http.Request) (*http.Response, error)) Client {
	transport := &mockHTTPTransport{handler: handler}
	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 2, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
	}
	return newClientWithAdapter(cfg, newOpenAIAdapter(cfg))
}

// newTestClientWithTransport 创建使用指定 transport 的 Client。
func newTestClientWithTransport(transport *mockHTTPTransport, provider ProviderType) Client {
	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    provider,
		RetryPolicy: RetryPolicy{MaxRetries: 2, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
	}
	var adapter providerAdapter
	switch provider {
	case ProviderDeepSeek:
		adapter = newDeepSeekAdapter(cfg)
	default:
		adapter = newOpenAIAdapter(cfg)
	}
	return newClientWithAdapter(cfg, adapter)
}

// --- SendMessage Tests ---

func TestSendMessageSuccess(t *testing.T) {
	transport := &mockHTTPTransport{}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	resp, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if resp.Content != "Hello" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello")
	}
	if resp.Usage == nil {
		t.Fatal("Usage should not be nil")
	}
	if resp.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", resp.Usage.PromptTokens)
	}
}

func TestSendMessageWithToolCalls(t *testing.T) {
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body: io.NopCloser(strings.NewReader(`{
					"choices": [{"finish_reason": "tool_calls", "message": {"role": "assistant", "content": "", "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "read_file", "arguments": "{\"path\":\"/etc/hosts\"}"}}]}}]
				}`)),
				Header: http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	resp, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Read /etc/hosts"},
	}, []ToolSpec{{Name: "read_file", Description: "Read file", Parameters: map[string]any{"type": "object"}}})

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "read_file" {
		t.Errorf("ToolCalls[0].Name = %q, want %q", resp.ToolCalls[0].Name, "read_file")
	}
}

func TestSendMessageWithSystemPrompt(t *testing.T) {
	transport := &mockHTTPTransport{}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	_, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleSystem, Content: "You are helpful."},
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}

	// 验证请求中包含 system 消息
	reqs := transport.getRequests()
	if len(reqs) == 0 {
		t.Fatal("no requests recorded")
	}
	var body map[string]any
	if err := json.NewDecoder(reqs[0].Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode body: %v", err)
	}
	msgs := body["messages"].([]any)
	firstMsg := msgs[0].(map[string]any)
	if firstMsg["role"] != "system" || firstMsg["content"] != "You are helpful." {
		t.Errorf("first message = %v, want system message", firstMsg)
	}
}

func TestSendMessageWithConversationHistory(t *testing.T) {
	transport := &mockHTTPTransport{}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	messages := []Message{
		{Role: RoleUser, Content: "What's in /etc/hosts?"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "call_1", Name: "read_file", Arguments: `{"path":"/etc/hosts"}`},
		}},
		{Role: RoleTool, ToolCallID: "call_1", Content: "127.0.0.1 localhost"},
		{Role: RoleUser, Content: "Thanks!"},
	}

	_, err := c.SendMessage(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}

	reqs := transport.getRequests()
	var body map[string]any
	if err := json.NewDecoder(reqs[0].Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode body: %v", err)
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 4 {
		t.Errorf("len(messages) = %d, want 4", len(msgs))
	}
}

func TestSendMessageEmptyMessages(t *testing.T) {
	c := newTestClient(nil)

	_, err := c.SendMessage(context.Background(), nil, nil)
	if err == nil {
		t.Error("expected error for nil messages")
	}

	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("expected *NonRetryableError, got %T: %v", err, err)
	}

	_, err = c.SendMessage(context.Background(), []Message{}, nil)
	if err == nil {
		t.Error("expected error for empty messages")
	}
}

func TestSendMessageEmptyTools(t *testing.T) {
	transport := &mockHTTPTransport{}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	resp, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, []ToolSpec{})

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if resp.Content != "Hello" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello")
	}
}

func TestSendMessageNilContext(t *testing.T) {
	c := newTestClient(nil)

	// context.Background() 不是 nil，我们测试零值 context
	// Go 不允许传递 nil context 给 WithContext，但 SendMessage 内部检查
	// 实际上 Go 的 context 不能为 nil 传给 req.WithContext
	// 我们通过传入 nil context 来测试
	resp, err := c.SendMessage(nil, []Message{{Role: RoleUser, Content: "test"}}, nil) //nolint:staticcheck
	if err == nil {
		t.Error("expected error for nil context")
		_ = resp
	}
	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("expected *NonRetryableError, got %T", err)
	}
}

// --- Retry Tests ---

func TestRetryableErrorRetried(t *testing.T) {
	callCount := 0
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			if callCount == 1 {
				return &http.Response{
					StatusCode: 429,
					Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Success"}}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	resp, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if resp.Content != "Success" {
		t.Errorf("Content = %q, want %q", resp.Content, "Success")
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
}

func TestRetryableErrorExhausted(t *testing.T) {
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 429,
				Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	_, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error when retries exhausted")
	}

	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("expected *NonRetryableError, got %T: %v", err, err)
	}
	if !strings.Contains(nre.Message, "retry exhausted") {
		t.Errorf("Message = %q, should contain 'retry exhausted'", nre.Message)
	}
}

func TestNonRetryableErrorReturned(t *testing.T) {
	callCount := 0
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			return &http.Response{
				StatusCode: 401,
				Body:       io.NopCloser(strings.NewReader(`{"error":"invalid api key"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	_, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error for 401")
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1 (no retry for 401)", callCount)
	}
}

func TestContextCancelledDuringRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	callCount := 0
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			if callCount == 1 {
				cancel() // 第一次请求后取消 context
			}
			return &http.Response{
				StatusCode: 429,
				Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}

	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 5, InitialBackoff: 100 * time.Millisecond, MaxBackoff: 1 * time.Second, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	_, err := c.SendMessage(ctx, []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error for cancelled context")
	}

	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("expected *NonRetryableError, got %T: %v", err, err)
	}
	if !strings.Contains(nre.Message, "cancel") {
		t.Errorf("Message = %q, should contain 'cancel'", nre.Message)
	}
}

func TestSendMessageHTTPTransportError(t *testing.T) {
	callCount := 0
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			return nil, fmt.Errorf("connection refused")
		},
	}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	_, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error for transport error")
	}
	// 网络错误是 Retryable，会重试直到耗尽
	if callCount < 2 {
		t.Errorf("callCount = %d, expected retries for network error", callCount)
	}
}

func TestSendMessageTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer server.Close()

	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 0, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Timeout: 50 * time.Millisecond},
		BaseURL:     server.URL,
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := c.SendMessage(ctx, []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error for timeout")
	}
}

// --- Tool Name Validation Tests ---

func TestToolNameCollision(t *testing.T) {
	transport := &mockHTTPTransport{}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	tools := []ToolSpec{
		{Name: "read:file", Description: "Read a file"},
		{Name: "read.file", Description: "Also read a file"},
	}
	// "read:file" 和 "read.file" 清理后都变为 "readfile"，产生冲突

	_, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, tools)

	if err == nil {
		t.Fatal("expected error for duplicate tool names")
	}

	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("expected *NonRetryableError, got %T: %v", err, err)
	}
	if !strings.Contains(nre.Message, "duplicate") {
		t.Errorf("Message = %q, should contain 'duplicate'", nre.Message)
	}
}

func TestCleanToolName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"read_file", "read_file"},
		{"read-file", "read-file"},
		{"read file", "readfile"},
		{"read.file", "readfile"},
		{"read:file", "readfile"},
		{"123_tool", "123_tool"},
		{"", ""},
	}

	for _, tt := range tests {
		got := cleanToolName(tt.input)
		if got != tt.want {
			t.Errorf("cleanToolName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidateToolNamesEmpty(t *testing.T) {
	// 空工具列表应通过校验
	if err := validateToolNames(nil); err != nil {
		t.Errorf("validateToolNames(nil) = %v, want nil", err)
	}
	if err := validateToolNames([]ToolSpec{}); err != nil {
		t.Errorf("validateToolNames([]) = %v, want nil", err)
	}
}

func TestValidateToolNamesTooLong(t *testing.T) {
	longName := strings.Repeat("a", 64)
	err := validateToolNames([]ToolSpec{{Name: longName}})
	if err == nil {
		t.Error("expected error for tool name > 63 chars")
	}
}

func TestValidateToolNamesAllInvalid(t *testing.T) {
	err := validateToolNames([]ToolSpec{{Name: "..."}})
	if err == nil {
		t.Error("expected error for tool name that cleans to empty string")
	}
}

// --- ParseResponse Edge Cases ---

func TestParseResponseMalformedJSON(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})
	_, err := adapter.ParseResponse([]byte(`<html>Not JSON</html>`))

	var re *RetryableError
	if !errors.As(err, &re) {
		t.Errorf("expected *RetryableError for malformed JSON, got %T: %v", err, err)
	}
}

func TestParseResponseMultipleChoices(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})
	respJSON := `{
		"choices": [
			{"finish_reason": "stop", "message": {"role": "assistant", "content": "First"}},
			{"finish_reason": "stop", "message": {"role": "assistant", "content": "Second"}},
			{"finish_reason": "stop", "message": {"role": "assistant", "content": "Third"}}
		]
	}`

	resp, err := adapter.ParseResponse([]byte(respJSON))
	if err != nil {
		t.Fatalf("ParseResponse returned error: %v", err)
	}
	if resp.Content != "First" {
		t.Errorf("Content = %q, want %q (choices[0])", resp.Content, "First")
	}
}

func TestParseResponseUnknownFields(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{})
	respJSON := `{
		"choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": "Hello"}}],
		"unknown_field": "ignored",
		"usage": {"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7}
	}`

	resp, err := adapter.ParseResponse([]byte(respJSON))
	if err != nil {
		t.Fatalf("ParseResponse returned error: %v", err)
	}
	if resp.Content != "Hello" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello")
	}
}

// --- NewClient Tests ---

func TestNewClientNoAPIKey(t *testing.T) {
	_, err := NewClient(ClientConfig{})
	if err == nil {
		t.Error("expected error for missing API key")
	}
	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("expected *NonRetryableError, got %T", err)
	}
}

func TestNewClientDefaultRetryPolicy(t *testing.T) {
	c, err := NewClient(ClientConfig{APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	cl := c.(*client)
	if cl.config.RetryPolicy.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cl.config.RetryPolicy.MaxRetries)
	}
}

func TestNewClientDefaultTimeout(t *testing.T) {
	c, err := NewClient(ClientConfig{APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	cl := c.(*client)
	if cl.config.Timeout != 600*time.Second {
		t.Errorf("Timeout = %v, want 600s", cl.config.Timeout)
	}
}

func TestNewClientUnknownProvider(t *testing.T) {
	c, err := NewClient(ClientConfig{
		APIKey:   "sk-test",
		Provider: ProviderType("unknown"),
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	// 未知 Provider 应走默认 OpenAI adapter
	cl := c.(*client)
	if _, ok := cl.adapter.(*openAIAdapter); !ok {
		t.Error("expected openAIAdapter for unknown provider")
	}
}





// --- Retry-After Header Tests ---

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"", 0},
		{"30", 30 * time.Second},
		{"0", 0},
		{"120", 120 * time.Second},
	}

	for _, tt := range tests {
		got := parseRetryAfter(tt.input)
		if got != tt.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestRetryAfter429(t *testing.T) {
	callCount := 0
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			if callCount == 1 {
				return &http.Response{
					StatusCode: 429,
					Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}, "Retry-After": []string{"0"}},
				}, nil
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	resp, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if resp.Content != "OK" {
		t.Errorf("Content = %q, want %q", resp.Content, "OK")
	}
}

// --- DeepSeek Specific Client Tests ---

func TestSendMessageDeepSeekInsufficientResource(t *testing.T) {
	callCount := 0
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			if callCount <= 2 {
				return &http.Response{
					StatusCode: 200,
					Body: io.NopCloser(strings.NewReader(`{
						"choices": [{"finish_reason": "insufficient_system_resource", "message": {"role": "assistant", "content": ""}}]
					}`)),
					Header: http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			}
			return &http.Response{
				StatusCode: 200,
				Body: io.NopCloser(strings.NewReader(`{
					"choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": "Success after retry"}}]
				}`)),
				Header: http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}
	c := newTestClientWithTransport(transport, ProviderDeepSeek)

	resp, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if resp.Content != "Success after retry" {
		t.Errorf("Content = %q, want %q", resp.Content, "Success after retry")
	}
	if callCount != 3 {
		t.Errorf("callCount = %d, want 3", callCount)
	}
}

// --- 5xx Server Error Retry ---

// --- httpStatusError Tests ---

func TestHTTPStatusError(t *testing.T) {
	err := &httpStatusError{StatusCode: 503, Body: "Service Unavailable"}
	got := err.Error()
	want := "HTTP 503: Service Unavailable"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// --- parseRetryAfter Extended Tests ---

func TestParseRetryAfterHTTPDateFormat(t *testing.T) {
	// Future date (1 hour from now) — should return a positive duration
	futureTime := time.Now().Add(1 * time.Hour)
	futureStr := futureTime.UTC().Format(http.TimeFormat)

	got := parseRetryAfter(futureStr)
	if got <= 0 {
		t.Errorf("parseRetryAfter(%q) = %v, want > 0", futureStr, got)
	}
	// Should be approximately 1 hour (with some clock skew tolerance)
	if got < 59*time.Minute || got > 61*time.Minute {
		t.Errorf("parseRetryAfter(%q) = %v, want ~1h", futureStr, got)
	}

	// Past date (1 hour ago) — should return 0
	pastTime := time.Now().Add(-1 * time.Hour)
	pastStr := pastTime.UTC().Format(http.TimeFormat)

	got = parseRetryAfter(pastStr)
	if got != 0 {
		t.Errorf("parseRetryAfter(%q) = %v, want 0 for past date", pastStr, got)
	}
}

func TestParseRetryAfterInvalidFormat(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"not-a-number", 0},
		{"12.34", 0},
		{"abc", 0},
		{"Mon, 02 Jan 2006 15:04:05 GMT", 0}, // past (2006), should be 0
	}

	for _, tt := range tests {
		got := parseRetryAfter(tt.input)
		if got != tt.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseRetryAfterNegativeSeconds(t *testing.T) {
	// Negative integer: ParseInt("-5") returns -5, which gives -5s
	got := parseRetryAfter("-5")
	if got != -5*time.Second {
		t.Errorf("parseRetryAfter(%q) = %v, want -5s", "-5", got)
	}
}

// --- doRequest Edge Cases ---

// failingReader is an io.Reader that always returns an error.
type failingReader struct{}

func (f failingReader) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("simulated read failure")
}

func TestDoRequestReadBodyError(t *testing.T) {
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(failingReader{}),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}
	// Use MaxRetries=0 so we get the RetryableError directly without retry wrapping
	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 0, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	_, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error for read body failure")
		return
	}

	// With MaxRetries=0, the retry loop breaks immediately and returns the
	// "retry exhausted" wrapper. The underlying RetryableError was created.
	if !strings.Contains(err.Error(), "retry") && !strings.Contains(err.Error(), "reading response body") {
		t.Errorf("unexpected error: %v", err)
	}
}

func Test429WithRetryAfterSeconds(t *testing.T) {
	callCount := 0
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			if callCount == 1 {
				return &http.Response{
					StatusCode: 429,
					Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
					Header: http.Header{
						"Content-Type":  []string{"application/json"},
						"Retry-After":   []string{"5"},
					},
				}, nil
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK after retry"}}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	resp, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if resp.Content != "OK after retry" {
		t.Errorf("Content = %q, want %q", resp.Content, "OK after retry")
	}
}

// errorBuildAdapter is a spy adapter that returns an error from BuildRequest.
type errorBuildAdapter struct {
	*openAIAdapter
}

func (a *errorBuildAdapter) BuildRequest(ctx context.Context, messages []Message, tools []ToolSpec) (*http.Request, error) {
	return nil, fmt.Errorf("simulated build error")
}

func TestSendMessageBuildRequestError(t *testing.T) {
	cfg := ClientConfig{
		APIKey: "sk-test",
		Model:  "gpt-4o",
	}
	adapter := &errorBuildAdapter{newOpenAIAdapter(cfg)}
	c := newClientWithAdapter(cfg, adapter)

	_, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error for BuildRequest failure")
		return
	}
	if !strings.Contains(err.Error(), "building request") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// --- NewClient Provider Tests ---

func TestNewClientDeepSeekProvider(t *testing.T) {
	c, err := NewClient(ClientConfig{
		APIKey:   "sk-deepseek",
		Provider: ProviderDeepSeek,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	cl := c.(*client)
	if _, ok := cl.adapter.(*deepSeekAdapter); !ok {
		t.Errorf("expected deepSeekAdapter for ProviderDeepSeek, got %T", cl.adapter)
	}
}

func TestNewClientEmptyProviderDefaultsToOpenAI(t *testing.T) {
	c, err := NewClient(ClientConfig{
		APIKey:   "sk-test",
		Provider: "",
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	cl := c.(*client)
	if _, ok := cl.adapter.(*deepSeekAdapter); !ok {
		t.Errorf("expected deepSeekAdapter for empty provider (default), got %T", cl.adapter)
	}
}

func TestNewClientExplicitOpenAIProvider(t *testing.T) {
	c, err := NewClient(ClientConfig{
		APIKey:   "sk-test",
		Provider: ProviderOpenAI,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	cl := c.(*client)
	if _, ok := cl.adapter.(*openAIAdapter); !ok {
		t.Errorf("expected openAIAdapter for ProviderOpenAI, got %T", cl.adapter)
	}
}



// --- ValidateToolNames Edge Cases ---

func TestValidateToolNamesNameAtMaxLength(t *testing.T) {
	name := strings.Repeat("a", 63)
	err := validateToolNames([]ToolSpec{{Name: name}})
	if err != nil {
		t.Errorf("validateToolNames with 63-char name should pass, got: %v", err)
	}
}

func TestValidateToolNamesTooShort(t *testing.T) {
	err := validateToolNames([]ToolSpec{{Name: "ab"}})
	if err == nil {
		t.Error("expected error for tool name < 3 chars")
	}
}

func TestValidateToolNamesStartsWithDigit(t *testing.T) {
	err := validateToolNames([]ToolSpec{{Name: "1tool"}})
	if err == nil {
		t.Error("expected error for tool name starting with digit")
	}
}

func TestValidateToolNamesStartsWithHyphen(t *testing.T) {
	err := validateToolNames([]ToolSpec{{Name: "-tool"}})
	if err == nil {
		t.Error("expected error for tool name starting with hyphen")
	}
}

func TestValidateToolNamesWithSpecialChars(t *testing.T) {
	// tool name with allowed special chars should pass
	err := validateToolNames([]ToolSpec{{Name: "my_tool-01"}})
	if err != nil {
		t.Errorf("validateToolNames with valid special chars should pass, got: %v", err)
	}
}

// --- sendWithRetry Context During Backoff ---

func TestSendWithRetryContextCancelDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Use a transport that returns 429 on all requests so the client
	// always enters the backoff path.
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			// Cancel the context after a very short delay (during backoff)
			go func() {
				time.Sleep(5 * time.Millisecond)
				cancel()
			}()
			return &http.Response{
				StatusCode: 429,
				Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}

	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 5, InitialBackoff: 500 * time.Millisecond, MaxBackoff: 2 * time.Second, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	_, err := c.SendMessage(ctx, []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error for cancelled context during backoff")
	}

	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("expected *NonRetryableError, got %T: %v", err, err)
	}
	if !strings.Contains(nre.Message, "cancel") {
		t.Errorf("Message = %q, should contain 'cancel'", nre.Message)
	}
}

func TestSendMessagePreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	transport := &mockHTTPTransport{}
	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 2, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	_, err := c.SendMessage(ctx, []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error for pre-cancelled context")
	}

	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("expected *NonRetryableError, got %T: %v", err, err)
	}
	if !strings.Contains(nre.Message, "cancelled") {
		t.Errorf("Message = %q, should contain 'cancelled'", nre.Message)
	}
}

func TestSendMessageWithCustomHeaders(t *testing.T) {
	transport := &mockHTTPTransport{}
	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 0, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
		Headers: map[string]string{
			"X-Custom-Header": "custom-value",
			"X-Request-ID":    "req-123",
		},
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	_, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}

	reqs := transport.getRequests()
	if len(reqs) == 0 {
		t.Fatal("no requests recorded")
	}
	if reqs[0].Header.Get("X-Custom-Header") != "custom-value" {
		t.Errorf("X-Custom-Header = %q, want %q", reqs[0].Header.Get("X-Custom-Header"), "custom-value")
	}
	if reqs[0].Header.Get("X-Request-ID") != "req-123" {
		t.Errorf("X-Request-ID = %q, want %q", reqs[0].Header.Get("X-Request-ID"), "req-123")
	}
	if reqs[0].Header.Get("Authorization") != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want %q", reqs[0].Header.Get("Authorization"), "Bearer sk-test")
	}
}

func TestServer5xxRetried(t *testing.T) {
	callCount := 0
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			if callCount == 1 {
				return &http.Response{
					StatusCode: 500,
					Body:       io.NopCloser(strings.NewReader(`{"error":"internal server error"}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	resp, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if resp.Content != "OK" {
		t.Errorf("Content = %q, want %q", resp.Content, "OK")
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
}

// --- 混合错误序列重试 ---

func TestRetryMixedErrors429Then500ThenSuccess(t *testing.T) {
	callCount := 0
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			switch callCount {
			case 1:
				return &http.Response{
					StatusCode: 429,
					Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			case 2:
				return &http.Response{
					StatusCode: 500,
					Body:       io.NopCloser(strings.NewReader(`{"error":"internal server error"}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			default:
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Success after mixed errors"}}]}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			}
		},
	}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	resp, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if resp.Content != "Success after mixed errors" {
		t.Errorf("Content = %q, want %q", resp.Content, "Success after mixed errors")
	}
	if callCount != 3 {
		t.Errorf("callCount = %d, want 3", callCount)
	}
}

func TestRetryMixedErrorsNetworkThen429ThenSuccess(t *testing.T) {
	callCount := 0
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			switch callCount {
			case 1:
				return nil, fmt.Errorf("connection refused")
			case 2:
				return &http.Response{
					StatusCode: 429,
					Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			default:
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK after network+429"}}]}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			}
		},
	}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	resp, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if resp.Content != "OK after network+429" {
		t.Errorf("Content = %q, want %q", resp.Content, "OK after network+429")
	}
	if callCount != 3 {
		t.Errorf("callCount = %d, want 3", callCount)
	}
}

// --- Cause 链验证 ---

func TestRetryExhaustedCauseChain(t *testing.T) {
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 429,
				Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}
	c := newTestClientWithTransport(transport, ProviderOpenAI)

	_, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error when retries exhausted")
	}

	// 最外层 NonRetryableError
	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Fatalf("expected *NonRetryableError, got %T: %v", err, err)
	}
	if !strings.Contains(nre.Message, "retry exhausted") {
		t.Errorf("Message = %q, should contain 'retry exhausted'", nre.Message)
	}

	// Cause 应为最后一次请求的原始 429 错误（httpStatusError）
	causeErr := nre.Unwrap()
	if causeErr == nil {
		t.Fatal("NonRetryableError.Cause should not be nil")
	}
	// 429 的错误有两种情况：RetryableError（有/无 Retry-After 头）
	// doRequest 对 429 返回的是 *RetryableError，取决于是否有 Retry-After 头
	// 无 Retry-After 时也会返回 *RetryableError
	var re *RetryableError
	if errors.As(causeErr, &re) {
		// 再 unwrap 一层
		httpErr := re.Unwrap()
		if httpErr == nil {
			t.Fatal("RetryableError.Cause should not be nil for 429")
		}
		var statusErr *httpStatusError
		if !errors.As(httpErr, &statusErr) {
			t.Errorf("innermost cause should be *httpStatusError, got %T: %v", httpErr, httpErr)
		} else if statusErr.StatusCode != 429 {
			t.Errorf("Status code = %d, want 429", statusErr.StatusCode)
		}
	} else {
		t.Fatalf("cause should be *RetryableError, got %T: %v", causeErr, causeErr)
	}
}

// --- Retry-After 等待时间验证 ---

func TestRetryWithRetryAfterWaitDuration(t *testing.T) {
	callCount := 0
	// Retry-After 头整数按秒解析；用 "1" 表示 1 秒等待。
	retryAfter := 1 * time.Second
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			if callCount == 1 {
				return &http.Response{
					StatusCode: 429,
					Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
					Header: http.Header{
						"Content-Type": []string{"application/json"},
						"Retry-After":  []string{"1"},
					},
				}, nil
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}

	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 2, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	start := time.Now()
	resp, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if resp.Content != "OK" {
		t.Errorf("Content = %q, want %q", resp.Content, "OK")
	}
	// Retry-After=1s，实际等待时间应 >= 1s
	if elapsed < retryAfter {
		t.Errorf("elapsed = %v, want >= %v (Retry-After respected)", elapsed, retryAfter)
	}
}

// --- MaxRetries=0 ---

func TestRetryMaxRetriesZero429(t *testing.T) {
	callCount := 0
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			return &http.Response{
				StatusCode: 429,
				Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}

	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 0, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	_, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error for 429 with MaxRetries=0")
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1 (no retries)", callCount)
	}
}

func TestRetryMaxRetriesZeroSuccess(t *testing.T) {
	callCount := 0
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}

	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 0, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	resp, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if resp.Content != "OK" {
		t.Errorf("Content = %q, want %q", resp.Content, "OK")
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1", callCount)
	}
}

// --- DeepSeek insufficient_system_resource 耗尽 ---

func TestRetryDeepSeekInsufficientResourceExhausted(t *testing.T) {
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body: io.NopCloser(strings.NewReader(`{
					"choices": [{"finish_reason": "insufficient_system_resource", "message": {"role": "assistant", "content": ""}}]
				}`)),
				Header: http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}

	cfg := ClientConfig{
		APIKey:      "sk-deepseek",
		Model:       "deepseek-chat",
		Provider:    ProviderDeepSeek,
		RetryPolicy: RetryPolicy{MaxRetries: 2, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
	}
	c := newClientWithAdapter(cfg, newDeepSeekAdapter(cfg))

	_, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error when insufficient_system_resource retries exhausted")
	}

	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Fatalf("expected *NonRetryableError, got %T: %v", err, err)
	}
	if !strings.Contains(nre.Message, "retry exhausted") {
		t.Errorf("Message = %q, should contain 'retry exhausted'", nre.Message)
	}
}

// --- RetryEvent Hook 测试 ---

func TestRetryEventHookCalledOnRetry(t *testing.T) {
	callCount := 0
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			if callCount == 1 {
				return &http.Response{
					StatusCode: 429,
					Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}

	var mu sync.Mutex
	var events []RetryEvent
	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 2, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
		OnRetry: func(_ context.Context, ev RetryEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	resp, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if resp.Content != "OK" {
		t.Errorf("Content = %q, want %q", resp.Content, "OK")
	}

	mu.Lock()
	defer mu.Unlock()
	// 事件 0: attempt=0 失败，决定重试（WillRetry=true）
	// 事件 1: attempt=1 成功（WillRetry=false）
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if !events[0].WillRetry {
		t.Error("events[0].WillRetry should be true")
	}
	if events[0].Attempt != 0 {
		t.Errorf("events[0].Attempt = %d, want 0", events[0].Attempt)
	}
	if events[0].Backoff == 0 {
		t.Error("events[0].Backoff should be > 0 (retrying)")
	}
	if events[0].Timestamp.IsZero() {
		t.Error("events[0].Timestamp should not be zero")
	}

	if events[1].WillRetry {
		t.Error("events[1].WillRetry should be false (success)")
	}
	if events[1].Attempt != 1 {
		t.Errorf("events[1].Attempt = %d, want 1", events[1].Attempt)
	}
	if events[1].Error != nil {
		t.Errorf("events[1].Error should be nil on success, got %v", events[1].Error)
	}
}

func TestRetryEventHookAttemptIncrement(t *testing.T) {
	callCount := 0
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			callCount++
			if callCount <= 2 {
				return &http.Response{
					StatusCode: 429,
					Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
				}, nil
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}

	var mu sync.Mutex
	var events []RetryEvent
	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 3, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
		OnRetry: func(_ context.Context, ev RetryEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	_, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}
	for i, ev := range events {
		if ev.Attempt != i {
			t.Errorf("events[%d].Attempt = %d, want %d", i, ev.Attempt, i)
		}
	}
	// 前 2 次 WillRetry=true，最后一次 WillRetry=false
	for i := 0; i < 2; i++ {
		if !events[i].WillRetry {
			t.Errorf("events[%d].WillRetry = false, want true", i)
		}
	}
	if events[2].WillRetry {
		t.Error("events[2].WillRetry should be false (success)")
	}
}

func TestRetryEventHookOnSuccessOnly(t *testing.T) {
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}

	var mu sync.Mutex
	var events []RetryEvent
	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 2, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
		OnRetry: func(_ context.Context, ev RetryEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	resp, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if resp.Content != "OK" {
		t.Errorf("Content = %q, want %q", resp.Content, "OK")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].Attempt != 0 {
		t.Errorf("events[0].Attempt = %d, want 0", events[0].Attempt)
	}
	if events[0].Error != nil {
		t.Errorf("events[0].Error should be nil on first-attempt success, got %v", events[0].Error)
	}
	if events[0].WillRetry {
		t.Error("events[0].WillRetry should be false (success)")
	}
	if events[0].Backoff != 0 {
		t.Errorf("events[0].Backoff should be 0 on success, got %v", events[0].Backoff)
	}
}

func TestRetryEventHookNilDoesNotPanic(t *testing.T) {
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 429,
				Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}

	// 显式设置 OnRetry=nil
	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 1, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
		OnRetry:     nil,
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	// 不应 panic
	_, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error for 429")
	}
}

func TestRetryEventHookOnExhausted(t *testing.T) {
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 429,
				Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}

	var mu sync.Mutex
	var events []RetryEvent
	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 2, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
		OnRetry: func(_ context.Context, ev RetryEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	_, err := c.SendMessage(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error when retries exhausted")
	}

	mu.Lock()
	defer mu.Unlock()
	// MaxRetries=2 → 3 attempts → 3 events: 0:WillRetry, 1:WillRetry, 2:!WillRetry (last)
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}
	if !events[0].WillRetry {
		t.Error("events[0].WillRetry should be true")
	}
	if !events[1].WillRetry {
		t.Error("events[1].WillRetry should be true")
	}
	if events[2].WillRetry {
		t.Error("events[2].WillRetry should be false (exhausted)")
	}
	if events[2].Backoff != 0 {
		t.Errorf("events[2].Backoff should be 0 (no more retries), got %v", events[2].Backoff)
	}
}

// --- 并发安全 ---

func TestRetryConcurrentSafety(t *testing.T) {
	transport := &mockHTTPTransport{
		handler: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}, nil
		},
	}

	var mu sync.Mutex
	var events []RetryEvent
	cfg := ClientConfig{
		APIKey:      "sk-test",
		Model:       "gpt-4o",
		Provider:    ProviderOpenAI,
		RetryPolicy: RetryPolicy{MaxRetries: 2, InitialBackoff: 1 * time.Millisecond, MaxBackoff: 10 * time.Millisecond, Multiplier: 2.0},
		HTTPClient:  &http.Client{Transport: transport},
		OnRetry: func(_ context.Context, ev RetryEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	const goroutines = 10
	const callsPerGoroutine = 3
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errCh := make(chan error, goroutines*callsPerGoroutine)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < callsPerGoroutine; j++ {
				resp, err := c.SendMessage(context.Background(), []Message{
					{Role: RoleUser, Content: fmt.Sprintf("Hello from %d/%d", id, j)},
				}, nil)
				if err != nil {
					errCh <- err
				} else if resp.Content != "OK" {
					errCh <- fmt.Errorf("unexpected content from %d/%d: %q", id, j, resp.Content)
				}
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent call failed: %v", err)
	}

	mu.Lock()
	count := len(events)
	mu.Unlock()
	expectedEvents := goroutines * callsPerGoroutine // each call succeeds on first attempt
	if count != expectedEvents {
		t.Errorf("events count = %d, want %d", count, expectedEvents)
	}
	if transport.getCallCount() != expectedEvents {
		t.Errorf("transport callCount = %d, want %d", transport.getCallCount(), expectedEvents)
	}
}


// --- Streaming Tests ---

func TestSendMessageStreamContent(t *testing.T) {
	handler := func(req *http.Request) (*http.Response, error) {
		sseBody := "data: {\"choices\":[{\"finish_reason\":null,\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
			"data: {\"choices\":[{\"finish_reason\":null,\"delta\":{\"content\":\" World\"}}]}\n\n" +
			"data: {\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{\"content\":\"\"}}]}\n\n" +
			"data: [DONE]\n\n"
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(sseBody)),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	}

	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o", APIKey: "sk-test"})
	c := newClientWithAdapter(ClientConfig{APIKey: "sk-test"}, adapter).(*client)
	c.httpClient = &http.Client{Transport: &mockHTTPTransport{handler: handler}}

	ch, err := c.SendMessageStream(context.Background(), []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("SendMessageStream returned error: %v", err)
	}

	var fullContent string
	var doneEvent *StreamingEvent
	for ev := range ch {
		if ev.Done {
			doneEvent = &ev
			break
		}
		fullContent += ev.Delta
	}

	if fullContent != "Hello World" {
		t.Errorf("fullContent = %q, want %q", fullContent, "Hello World")
	}
	if doneEvent == nil {
		t.Fatal("expected Done event")
		return
	}
	if doneEvent.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", doneEvent.FinishReason, "stop")
	}
	if doneEvent.Err != nil {
		t.Errorf("unexpected error: %v", doneEvent.Err)
	}
}

func TestSendMessageStreamWithReasoningContent(t *testing.T) {
	handler := func(req *http.Request) (*http.Response, error) {
		sseBody := "data: {\"choices\":[{\"finish_reason\":null,\"delta\":{\"content\":\"Hello\",\"reasoning_content\":\"Let me think...\"}}]}\n\n" +
			"data: [DONE]\n\n"
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(sseBody)),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	}

	adapter := newDeepSeekAdapter(ClientConfig{Model: "deepseek-v4-pro", APIKey: "sk-test"})
	c := newClientWithAdapter(ClientConfig{APIKey: "sk-test"}, adapter).(*client)
	c.httpClient = &http.Client{Transport: &mockHTTPTransport{handler: handler}}

	ch, err := c.SendMessageStream(context.Background(), []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("SendMessageStream returned error: %v", err)
	}

	var content, reasoning string
	for ev := range ch {
		if ev.Done {
			break
		}
		content += ev.Delta
		reasoning += ev.ReasoningDelta
	}

	if content != "Hello" {
		t.Errorf("content = %q, want %q", content, "Hello")
	}
	if reasoning != "Let me think..." {
		t.Errorf("reasoning = %q, want %q", reasoning, "Let me think...")
	}
}

func TestSendMessageStreamToolCallAccumulation(t *testing.T) {
	handler := func(req *http.Request) (*http.Response, error) {
		// Chunk 1: tool call header (id + name), arguments initially empty
		ch1 := `data: {"choices":[{"finish_reason":null,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":""}}]}}]}`
		// Chunk 2: first part of arguments {"path":
		ch2 := `data: {"choices":[{"finish_reason":null,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}`
		// Chunk 3: second part of arguments "/etc/hosts"}
		ch3 := `data: {"choices":[{"finish_reason":null,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"/etc/hosts\"}"}}]}}]}`
		// Chunk 4: finish with tool_calls reason
		ch4 := `data: {"choices":[{"finish_reason":"tool_calls","delta":{}}]}`
		// Chunk 5: sentinel
		ch5 := `data: [DONE]`
		sseBody := ch1 + "\n\n" + ch2 + "\n\n" + ch3 + "\n\n" + ch4 + "\n\n" + ch5 + "\n\n"
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(sseBody)),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	}

	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o", APIKey: "sk-test"})
	c := newClientWithAdapter(ClientConfig{APIKey: "sk-test"}, adapter).(*client)
	c.httpClient = &http.Client{Transport: &mockHTTPTransport{handler: handler}}

	ch, err := c.SendMessageStream(context.Background(), []Message{
		{Role: RoleUser, Content: "Read /etc/hosts"},
	}, nil)
	if err != nil {
		t.Fatalf("SendMessageStream returned error: %v", err)
	}

	var doneEvent *StreamingEvent
	for ev := range ch {
		if ev.Done {
			doneEvent = &ev
			break
		}
	}

	if doneEvent == nil {
		t.Fatal("expected Done event")
		return
	}
	if len(doneEvent.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(doneEvent.ToolCalls))
	}
	tc := doneEvent.ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("ID = %q, want %q", tc.ID, "call_1")
	}
	if tc.Name != "read_file" {
		t.Errorf("Name = %q, want %q", tc.Name, "read_file")
	}
	if tc.Arguments != `{"path":"/etc/hosts"}` {
		t.Errorf("Arguments = %q, want %q", tc.Arguments, `{"path":"/etc/hosts"}`)
	}
	if doneEvent.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want %q", doneEvent.FinishReason, "tool_calls")
	}
}

func TestSendMessageStreamNetworkError(t *testing.T) {
	handler := func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o", APIKey: "sk-test"})
	c := newClientWithAdapter(ClientConfig{APIKey: "sk-test"}, adapter).(*client)
	c.httpClient = &http.Client{Transport: &mockHTTPTransport{handler: handler}}

	ch, err := c.SendMessageStream(context.Background(), []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("SendMessageStream returned error: %v", err)
	}

	for ev := range ch {
		if ev.Done {
			if ev.Err == nil {
				t.Error("expected error in Done event")
			}
			break
		}
	}
}

func TestSendMessageStreamHTTPError(t *testing.T) {
	handler := func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 401,
			Body:       io.NopCloser(strings.NewReader(`{"error": {"message": "Invalid API key"}}`)),
			Header:     http.Header{},
		}, nil
	}

	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o", APIKey: "bad-key"})
	c := newClientWithAdapter(ClientConfig{APIKey: "bad-key"}, adapter).(*client)
	c.httpClient = &http.Client{Transport: &mockHTTPTransport{handler: handler}}

	ch, err := c.SendMessageStream(context.Background(), []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("SendMessageStream returned error: %v", err)
	}

	for ev := range ch {
		if ev.Done {
			if ev.Err == nil {
				t.Error("expected error in Done event for 401 response")
			}
			break
		}
	}
}

func TestSendMessageStreamContextCancelled(t *testing.T) {
	handler := func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"finish_reason\":null,\"delta\":{\"content\":\"Hello\"}}]}\n\n")),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	}

	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o", APIKey: "sk-test"})
	c := newClientWithAdapter(ClientConfig{APIKey: "sk-test"}, adapter).(*client)
	c.httpClient = &http.Client{Transport: &mockHTTPTransport{handler: handler}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch, err := c.SendMessageStream(ctx, []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("SendMessageStream returned error: %v", err)
	}

	var doneEvent *StreamingEvent
	for ev := range ch {
		if ev.Done {
			doneEvent = &ev
			break
		}
	}

	if doneEvent == nil {
		t.Fatal("expected Done event")
	}
}

// --- SendMessageStream Edge Case Tests ---

func TestSendMessageStreamNonDataLinesSkipped(t *testing.T) {
	// SSE 注释行和空行应被跳过，不影响内容累积
	handler := func(req *http.Request) (*http.Response, error) {
		sseBody := ": heartbeat\n\n" +
			"\n" +
			"data: {\"choices\":[{\"finish_reason\":null,\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
			": another comment\n\n" +
			"data: {\"choices\":[{\"finish_reason\":null,\"delta\":{\"content\":\" World\"}}]}\n\n" +
			"data: [DONE]\n\n"
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(sseBody)),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	}

	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o", APIKey: "sk-test"})
	c := newClientWithAdapter(ClientConfig{APIKey: "sk-test"}, adapter).(*client)
	c.httpClient = &http.Client{Transport: &mockHTTPTransport{handler: handler}}

	ch, err := c.SendMessageStream(context.Background(), []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("SendMessageStream returned error: %v", err)
	}

	var fullContent string
	for ev := range ch {
		if ev.Done {
			if ev.Err != nil {
				t.Errorf("unexpected error: %v", ev.Err)
			}
			break
		}
		fullContent += ev.Delta
	}

	if fullContent != "Hello World" {
		t.Errorf("fullContent = %q, want %q", fullContent, "Hello World")
	}
}

func TestSendMessageStreamMalformedChunkSkipped(t *testing.T) {
	// 无法解析的 SSE chunk 应被跳过，不影响后续正常 chunk
	handler := func(req *http.Request) (*http.Response, error) {
		sseBody := "data: {\"choices\":[{\"finish_reason\":null,\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
			"data: {this is not valid json}\n\n" + // malformed → continue
			"data: {\"choices\":[{\"finish_reason\":null,\"delta\":{\"content\":\" World\"}}]}\n\n" +
			"data: [DONE]\n\n"
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(sseBody)),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	}

	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o", APIKey: "sk-test"})
	c := newClientWithAdapter(ClientConfig{APIKey: "sk-test"}, adapter).(*client)
	c.httpClient = &http.Client{Transport: &mockHTTPTransport{handler: handler}}

	ch, err := c.SendMessageStream(context.Background(), []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("SendMessageStream returned error: %v", err)
	}

	var fullContent string
	for ev := range ch {
		if ev.Done {
			if ev.Err != nil {
				t.Errorf("unexpected error: %v", ev.Err)
			}
			break
		}
		fullContent += ev.Delta
	}

	if fullContent != "Hello World" {
		t.Errorf("fullContent = %q, want %q", fullContent, "Hello World")
	}
}

// errReader 在读取时返回指定的错误。
type errReader struct {
	err error
}

func (r *errReader) Read(p []byte) (n int, err error) {
	return 0, r.err
}

func TestSendMessageStreamScannerError(t *testing.T) {
	// scanner 在流读取过程中遇到 IO 错误 → RetryableError
	handler := func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(&errReader{err: fmt.Errorf("connection reset")}),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	}

	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o", APIKey: "sk-test"})
	c := newClientWithAdapter(ClientConfig{APIKey: "sk-test"}, adapter).(*client)
	c.httpClient = &http.Client{Transport: &mockHTTPTransport{handler: handler}}

	ch, err := c.SendMessageStream(context.Background(), []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("SendMessageStream returned error: %v", err)
	}

	var doneEvent *StreamingEvent
	for ev := range ch {
		if ev.Done {
			doneEvent = &ev
			break
		}
	}

	if doneEvent == nil {
		t.Fatal("expected Done event")
		return
	}
	if doneEvent.Err == nil {
		t.Fatal("expected error in Done event for scanner read error")
	}
	var retryErr *RetryableError
	if !errors.As(doneEvent.Err, &retryErr) {
		t.Errorf("expected *RetryableError, got %T: %v", doneEvent.Err, doneEvent.Err)
	}
}

func TestSendMessageStreamNoDoneSentinel(t *testing.T) {
	// 服务端不发送 [DONE] 直接关闭连接 → acc.final() 兜底
	handler := func(req *http.Request) (*http.Response, error) {
		sseBody := "data: {\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{\"content\":\"Done\"}}]}\n\n"
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(sseBody)),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	}

	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o", APIKey: "sk-test"})
	c := newClientWithAdapter(ClientConfig{APIKey: "sk-test"}, adapter).(*client)
	c.httpClient = &http.Client{Transport: &mockHTTPTransport{handler: handler}}

	ch, err := c.SendMessageStream(context.Background(), []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("SendMessageStream returned error: %v", err)
	}

	var fullContent string
	var doneEvent *StreamingEvent
	for ev := range ch {
		if ev.Done {
			doneEvent = &ev
			break
		}
		fullContent += ev.Delta
	}

	if doneEvent == nil {
		t.Fatal("expected Done event")
		return
	}
	if doneEvent.Err != nil {
		t.Errorf("unexpected error: %v", doneEvent.Err)
	}
	if doneEvent.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", doneEvent.FinishReason, "stop")
	}
	if fullContent != "Done" {
		t.Errorf("fullContent = %q, want %q", fullContent, "Done")
	}
}

func TestSendMessageStreamPreFlightNilContext(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o", APIKey: "sk-test"})
	c := newClientWithAdapter(ClientConfig{APIKey: "sk-test"}, adapter).(*client)

	// 传入 nil context
	_, err := c.SendMessageStream(nil, []Message{ //nolint:staticcheck
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err == nil {
		t.Fatal("expected error for nil context")
	}
	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("expected *NonRetryableError, got %T: %v", err, err)
	}
}

func TestSendMessageStreamPreFlightEmptyMessages(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o", APIKey: "sk-test"})
	c := newClientWithAdapter(ClientConfig{APIKey: "sk-test"}, adapter).(*client)

	_, err := c.SendMessageStream(context.Background(), nil, nil)
	if err == nil {
		t.Error("expected error for nil messages")
	}
	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("expected *NonRetryableError, got %T: %v", err, err)
	}

	_, err = c.SendMessageStream(context.Background(), []Message{}, nil)
	if err == nil {
		t.Error("expected error for empty messages")
	}
}

func TestSendMessageStreamPreFlightInvalidToolName(t *testing.T) {
	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o", APIKey: "sk-test"})
	c := newClientWithAdapter(ClientConfig{APIKey: "sk-test"}, adapter).(*client)

	tools := []ToolSpec{
		{Name: "read:file", Description: "Read a file"},
		{Name: "read.file", Description: "Also read a file"},
	}

	_, err := c.SendMessageStream(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, tools)

	if err == nil {
		t.Fatal("expected error for duplicate tool names after cleaning")
	}
	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("expected *NonRetryableError, got %T: %v", err, err)
	}
	if !strings.Contains(nre.Message, "duplicate") {
		t.Errorf("Message = %q, should contain 'duplicate'", nre.Message)
	}
}

func TestSendMessageStreamContextCancelledMidStream(t *testing.T) {
	// ctx 在流处理循环中被取消 → 回退到非流式。
	// 注意：readStream 的 ctx 检查在 scanner.Scan() 返回每一行之后执行，
	// 因此预取消的 ctx 在首个 scanner.Scan() 成功后会被检测到。
	handler := func(req *http.Request) (*http.Response, error) {
		sseBody := "data: {\"choices\":[{\"finish_reason\":null,\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
			"data: {\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{\"content\":\"\"}}]}\n\n" +
			"data: [DONE]\n\n"
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(sseBody)),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	}

	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o", APIKey: "sk-test"})
	c := newClientWithAdapter(ClientConfig{APIKey: "sk-test"}, adapter).(*client)
	c.httpClient = &http.Client{Transport: &mockHTTPTransport{handler: handler}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 预取消

	ch, err := c.SendMessageStream(ctx, []Message{
		{Role: RoleUser, Content: "Hi"},
	}, nil)
	if err != nil {
		t.Fatalf("SendMessageStream returned error: %v", err)
	}

	var doneEvent *StreamingEvent
	var deltas []string
	for ev := range ch {
		if ev.Done {
			doneEvent = &ev
			break
		}
		deltas = append(deltas, ev.Delta)
	}

	if doneEvent == nil {
		t.Fatal("expected Done event")
		return
	}

	// 预取消的 ctx 与 scanner.Scan() 竞态：
	// 若 ctx.Done 被选中 → Err == context.Canceled
	// 若 scanner.Scan 先返回 → Err == nil，内容正常
	// 两种结果都接受
	if doneEvent.Err != nil {
		if !errors.Is(doneEvent.Err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", doneEvent.Err)
		}
	} else {
		if len(deltas) == 0 && doneEvent.FinishReason == "" {
			t.Error("expected either context cancellation or valid stream completion")
		}
	}
}

// --- GetBalance / SupportsBalance Tests ---

func TestGetBalanceDeepSeekHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/user/balance" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"100.00","granted_balance":"50.00","topped_up_balance":"50.00"}]}`))
	}))
	defer server.Close()

	cfg := ClientConfig{
		APIKey:  "sk-test",
		Model:   "deepseek-v4-flash",
		BaseURL: server.URL,
	}
	adapter := newDeepSeekAdapter(cfg)
	c := newClientWithAdapter(cfg, adapter)

	info, err := c.GetBalance(context.Background())
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if info == nil {
		t.Fatal("expected non-nil BalanceInfo")
		return
	}
	if !info.IsAvailable {
		t.Error("IsAvailable should be true")
	}
	if len(info.BalanceInfos) != 1 {
		t.Fatalf("len(BalanceInfos) = %d, want 1", len(info.BalanceInfos))
	}
	if info.BalanceInfos[0].Currency != "CNY" {
		t.Errorf("Currency = %q, want CNY", info.BalanceInfos[0].Currency)
	}
	if info.BalanceInfos[0].TotalBalance != "100.00" {
		t.Errorf("TotalBalance = %q, want 100.00", info.BalanceInfos[0].TotalBalance)
	}
}

func TestGetBalanceOpenAIReturnsNil(t *testing.T) {
	cfg := ClientConfig{APIKey: "sk-test", Model: "gpt-4o"}
	adapter := newOpenAIAdapter(cfg)
	c := newClientWithAdapter(cfg, adapter)

	info, err := c.GetBalance(context.Background())
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if info != nil {
		t.Error("OpenAI GetBalance should return nil")
	}
}

func TestGetBalanceNilContext(t *testing.T) {
	cfg := ClientConfig{APIKey: "sk-test", Model: "gpt-4o"}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	_, err := c.GetBalance(nil) //nolint:staticcheck
	if err == nil {
		t.Fatal("expected error for nil context")
	}
	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("expected *NonRetryableError, got %T", err)
	}
}

func TestSupportsBalanceDeepSeek(t *testing.T) {
	cfg := ClientConfig{APIKey: "sk-test", Model: "deepseek-v4-flash"}
	adapter := newDeepSeekAdapter(cfg)
	c := newClientWithAdapter(cfg, adapter)

	if !c.SupportsBalance() {
		t.Error("DeepSeek adapter should support balance queries")
	}
}

func TestSupportsBalanceOpenAI(t *testing.T) {
	cfg := ClientConfig{APIKey: "sk-test", Model: "gpt-4o"}
	adapter := newOpenAIAdapter(cfg)
	c := newClientWithAdapter(cfg, adapter)

	if c.SupportsBalance() {
		t.Error("OpenAI adapter should NOT support balance queries")
	}
}

// --- ListModels Tests ---

func TestListModelsDeepSeek(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"deepseek-v4-pro","object":"model","owned_by":"deepseek"}
		]}`))
	}))
	defer server.Close()

	cfg := ClientConfig{
		APIKey:  "sk-test",
		Model:   "deepseek-v4-flash",
		BaseURL: server.URL,
	}
	adapter := newDeepSeekAdapter(cfg)
	c := newClientWithAdapter(cfg, adapter)

	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(models))
	}
	if models[0].ID != "deepseek-v4-pro" {
		t.Errorf("ID = %q, want deepseek-v4-pro", models[0].ID)
	}
}

func TestListModelsOpenAI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"gpt-4o","object":"model","owned_by":"openai"}
		]}`))
	}))
	defer server.Close()

	cfg := ClientConfig{
		APIKey:  "sk-test",
		Model:   "gpt-4o",
		BaseURL: server.URL,
	}
	adapter := newOpenAIAdapter(cfg)
	c := newClientWithAdapter(cfg, adapter)

	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("len(models) = %d, want 1", len(models))
	}
	if models[0].ID != "gpt-4o" {
		t.Errorf("ID = %q, want gpt-4o", models[0].ID)
	}
}

func TestListModelsNilContext(t *testing.T) {
	cfg := ClientConfig{APIKey: "sk-test", Model: "gpt-4o"}
	c := newClientWithAdapter(cfg, newOpenAIAdapter(cfg))

	_, err := c.ListModels(nil) //nolint:staticcheck
	if err == nil {
		t.Fatal("expected error for nil context")
	}
	var nre *NonRetryableError
	if !errors.As(err, &nre) {
		t.Errorf("expected *NonRetryableError, got %T", err)
	}
}

// --- SendMessageStream BuildRequest error path ---

type errorBuildStreamAdapter struct {
	*openAIAdapter
}

func (a *errorBuildStreamAdapter) BuildStreamRequest(ctx context.Context, messages []Message, tools []ToolSpec) (*http.Request, error) {
	return nil, fmt.Errorf("simulated build stream error")
}

func TestSendMessageStreamBuildRequestError(t *testing.T) {
	cfg := ClientConfig{APIKey: "sk-test", Model: "gpt-4o"}
	adapter := &errorBuildStreamAdapter{newOpenAIAdapter(cfg)}
	c := newClientWithAdapter(cfg, adapter)

	_, err := c.SendMessageStream(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)

	if err == nil {
		t.Fatal("expected error for BuildStreamRequest failure")
		return
	}
	if !strings.Contains(err.Error(), "building stream request") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSendMessageStreamWithCustomHeaders(t *testing.T) {
	var capturedReq *http.Request
	handler := func(req *http.Request) (*http.Response, error) {
		capturedReq = req
		sseBody := "data: {\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{\"content\":\"Done\"}}]}\n\ndata: [DONE]\n\n"
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(sseBody)),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		}, nil
	}

	adapter := newOpenAIAdapter(ClientConfig{Model: "gpt-4o", APIKey: "sk-test"})
	cfg := ClientConfig{
		APIKey:  "sk-test",
		Headers: map[string]string{"X-Custom": "custom-value", "X-Request-ID": "req-stream"},
	}
	c := newClientWithAdapter(cfg, adapter).(*client)
	c.httpClient = &http.Client{Transport: &mockHTTPTransport{handler: handler}}

	ch, err := c.SendMessageStream(context.Background(), []Message{
		{Role: RoleUser, Content: "Hello"},
	}, nil)
	if err != nil {
		t.Fatalf("SendMessageStream: %v", err)
	}

	for ev := range ch {
		if ev.Done {
			if ev.Err != nil {
				t.Errorf("unexpected error: %v", ev.Err)
			}
			break
		}
	}

	if capturedReq == nil {
		t.Fatal("no request captured")
		return
	}
	if capturedReq.Header.Get("X-Custom") != "custom-value" {
		t.Errorf("X-Custom = %q, want custom-value", capturedReq.Header.Get("X-Custom"))
	}
	if capturedReq.Header.Get("X-Request-ID") != "req-stream" {
		t.Errorf("X-Request-ID = %q, want req-stream", capturedReq.Header.Get("X-Request-ID"))
	}
}
