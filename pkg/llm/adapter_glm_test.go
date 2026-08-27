package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// GLM Coding Plan OpenAI 兼容端点(官方接入指南:OpenAI Chat Completion 协议)。
const testGLMDefaultBaseURL = "https://open.bigmodel.cn/api/coding/paas/v4"

func TestGLMDefaultBaseURL(t *testing.T) {
	adapter := newGLMAdapter(ClientConfig{APIKey: "sk-test", Model: "glm-5.3"})
	if got := adapter.BaseURL(); got != testGLMDefaultBaseURL {
		t.Errorf("default BaseURL = %q, want %q", got, testGLMDefaultBaseURL)
	}
}

func TestGLMCustomBaseURL(t *testing.T) {
	adapter := newGLMAdapter(ClientConfig{
		APIKey:  "sk-test",
		Model:   "glm-5.3",
		BaseURL: "https://custom.example.com/v1",
	})
	if got := adapter.BaseURL(); got != "https://custom.example.com/v1" {
		t.Errorf("custom BaseURL = %q, want override", got)
	}
}

func TestGLMAuthHeader(t *testing.T) {
	adapter := newGLMAdapter(ClientConfig{APIKey: "sk-glm-test", Model: "glm-5.3"})
	key, value := adapter.AuthHeader()
	if key != "Authorization" || value != "Bearer sk-glm-test" {
		t.Errorf("AuthHeader = (%q, %q), want (Authorization, Bearer sk-glm-test)", key, value)
	}
}

func TestGLMBuildRequest(t *testing.T) {
	adapter := newGLMAdapter(ClientConfig{APIKey: "sk-test", Model: "glm-5.3"})
	messages := []Message{
		{Role: RoleSystem, Content: "You are a helpful assistant."},
		{Role: RoleUser, Content: "Hello"},
	}

	req, err := adapter.BuildRequest(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", req.Method)
	}
	expectedURL := testGLMDefaultBaseURL + "/chat/completions"
	if req.URL.String() != expectedURL {
		t.Errorf("URL = %q, want %q", req.URL.String(), expectedURL)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["model"] != "glm-5.3" {
		t.Errorf("model = %v, want glm-5.3", body["model"])
	}
	if body["stream"] != false {
		t.Errorf("stream = %v, want false", body["stream"])
	}
	if msgs, ok := body["messages"].([]any); !ok || len(msgs) != 2 {
		t.Errorf("messages = %v, want 2 entries", body["messages"])
	}
}

func TestGLMBuildStreamRequest(t *testing.T) {
	adapter := newGLMAdapter(ClientConfig{APIKey: "sk-test", Model: "glm-5.3"})
	req, err := adapter.BuildStreamRequest(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildStreamRequest: %v", err)
	}
	if req.URL.String() != testGLMDefaultBaseURL+"/chat/completions" {
		t.Errorf("URL = %q, want %s/chat/completions", req.URL.String(), testGLMDefaultBaseURL)
	}
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
}

// TestGLMStreamRequestIncludeUsage 回归根因:GLM 流式响应默认不返回 usage,
// 未携带 stream_options.include_usage 时 TUI cache/tok 数值无数据来源。
func TestGLMStreamRequestIncludeUsage(t *testing.T) {
	adapter := newGLMAdapter(ClientConfig{APIKey: "sk-test", Model: "glm-5.3"})
	req, err := adapter.BuildStreamRequest(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildStreamRequest: %v", err)
	}
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	opts, ok := body["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("stream_options = %#v, want {include_usage: true}", body["stream_options"])
	}
	if opts["include_usage"] != true {
		t.Errorf("include_usage = %#v, want true", opts["include_usage"])
	}
}

// TestGLMParseResponse_CachedTokens GLM 在 usage.prompt_tokens_details.cached_tokens
// 返回缓存命中数;未命中按 prompt_tokens - cached_tokens 推导。
func TestGLMParseResponse_CachedTokens(t *testing.T) {
	adapter := newGLMAdapter(ClientConfig{APIKey: "sk-test", Model: "glm-5.3"})
	respJSON := `{
		"choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": "ok"}}],
		"usage": {
			"prompt_tokens": 2000,
			"completion_tokens": 300,
			"total_tokens": 2300,
			"prompt_tokens_details": {"cached_tokens": 1600}
		}
	}`
	resp, err := adapter.ParseResponse([]byte(respJSON))
	if err != nil {
		t.Fatalf("ParseResponse: %v", err)
	}
	if resp.Usage.CacheHitTokens != 1600 {
		t.Errorf("CacheHitTokens = %d, want 1600", resp.Usage.CacheHitTokens)
	}
	if resp.Usage.CacheMissTokens != 400 {
		t.Errorf("CacheMissTokens = %d, want 400 (prompt - cached)", resp.Usage.CacheMissTokens)
	}
}

func TestGLMSupportsBalance(t *testing.T) {
	adapter := newGLMAdapter(ClientConfig{APIKey: "sk-test", Model: "glm-5.3"})
	if adapter.SupportsBalance() {
		t.Error("GLM adapter should NOT support balance query")
	}
	info, err := adapter.GetBalance(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if info != nil {
		t.Error("GetBalance should return nil, nil for GLM")
	}
}

// TestGLMNewClientSendMessage 验证 NewClient 对 ProviderGLM 的注册:
// 请求发往 {base}/chat/completions 且响应解析正常(端到端走 openAIAdapter 协议)。
func TestGLMNewClientSendMessage(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-glm-test",
			"model": "glm-5.3",
			"choices": [{
				"index": 0,
				"finish_reason": "stop",
				"message": {"role": "assistant", "content": "hello from glm"}
			}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Provider: ProviderGLM,
		APIKey:   "sk-glm-test",
		Model:    "glm-5.3",
		BaseURL:  server.URL,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resp, err := client.SendMessage(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if resp.Content != "hello from glm" {
		t.Errorf("Content = %q, want %q", resp.Content, "hello from glm")
	}
	if gotPath != "/chat/completions" {
		t.Errorf("request path = %q, want /chat/completions", gotPath)
	}
	if gotAuth != "Bearer sk-glm-test" {
		t.Errorf("Authorization = %q, want Bearer sk-glm-test", gotAuth)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
		t.Errorf("Usage = %+v, want TotalTokens=15", resp.Usage)
	}
}
