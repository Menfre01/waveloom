package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Yolo-Auto OpenAI 兼容端点(接入文档:https://yolo-auto.com/docs)。
const testYoloAutoDefaultBaseURL = "https://yolo-auto.com/v1"

func TestYoloAutoDefaultBaseURL(t *testing.T) {
	adapter := newYoloAutoAdapter(ClientConfig{APIKey: "yolo-test-key", Model: "yolo"})
	if got := adapter.BaseURL(); got != testYoloAutoDefaultBaseURL {
		t.Errorf("default BaseURL = %q, want %q", got, testYoloAutoDefaultBaseURL)
	}
}

func TestYoloAutoCustomBaseURL(t *testing.T) {
	adapter := newYoloAutoAdapter(ClientConfig{
		APIKey:  "yolo-test-key",
		Model:   "yolo",
		BaseURL: "https://custom.example.com/v1",
	})
	if got := adapter.BaseURL(); got != "https://custom.example.com/v1" {
		t.Errorf("custom BaseURL = %q, want override", got)
	}
}

func TestYoloAutoAuthHeader(t *testing.T) {
	adapter := newYoloAutoAdapter(ClientConfig{APIKey: "yolo-test-key", Model: "yolo"})
	key, value := adapter.AuthHeader()
	if key != "Authorization" || value != "Bearer yolo-test-key" {
		t.Errorf("AuthHeader = (%q, %q), want (Authorization, Bearer yolo-test-key)", key, value)
	}
}

func TestYoloAutoBuildRequest(t *testing.T) {
	adapter := newYoloAutoAdapter(ClientConfig{APIKey: "yolo-test-key", Model: "yolo"})
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
	expectedURL := testYoloAutoDefaultBaseURL + "/chat/completions"
	if req.URL.String() != expectedURL {
		t.Errorf("URL = %q, want %q", req.URL.String(), expectedURL)
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["model"] != "yolo" {
		t.Errorf("model = %v, want yolo", body["model"])
	}
	if body["stream"] != false {
		t.Errorf("stream = %v, want false", body["stream"])
	}
	if msgs, ok := body["messages"].([]any); !ok || len(msgs) != 2 {
		t.Errorf("messages = %v, want 2 entries", body["messages"])
	}
}

func TestYoloAutoBuildStreamRequest(t *testing.T) {
	adapter := newYoloAutoAdapter(ClientConfig{APIKey: "yolo-test-key", Model: "yolo"})
	req, err := adapter.BuildStreamRequest(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("BuildStreamRequest: %v", err)
	}
	if req.URL.String() != testYoloAutoDefaultBaseURL+"/chat/completions" {
		t.Errorf("URL = %q, want %s/chat/completions", req.URL.String(), testYoloAutoDefaultBaseURL)
	}
	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
}

// TestYoloAutoListModelsDiscovery 验证模型目录来自 GET {base}/models 自动发现,
// 而非 adapter 内置清单(yolo / yolo-small 为公开别名,服务端可随时增补)。
func TestYoloAutoListModelsDiscovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/models" {
			t.Errorf("Path = %q, want /models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer yolo-test-key" {
			t.Errorf("Authorization = %q, want Bearer yolo-test-key", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"object": "list",
			"data": [
				{"id": "yolo", "object": "model", "owned_by": "yolo-auto"},
				{"id": "yolo-small", "object": "model", "owned_by": "yolo-auto"}
			]
		}`)
	}))
	defer server.Close()

	adapter := newYoloAutoAdapter(ClientConfig{
		APIKey:  "yolo-test-key",
		Model:   "yolo",
		BaseURL: server.URL,
	})

	models, err := adapter.ListModels(context.Background(), server.Client())
	if err != nil {
		t.Fatalf("ListModels returned error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(models))
	}
	if models[0].ID != "yolo" || models[1].ID != "yolo-small" {
		t.Errorf("models = %+v, want yolo + yolo-small", models)
	}
}

func TestYoloAutoSupportsBalance(t *testing.T) {
	adapter := newYoloAutoAdapter(ClientConfig{APIKey: "yolo-test-key", Model: "yolo"})
	if adapter.SupportsBalance() {
		t.Error("Yolo-Auto adapter should NOT support balance query")
	}
	info, err := adapter.GetBalance(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if info != nil {
		t.Error("GetBalance should return nil, nil for Yolo-Auto")
	}
}

// TestYoloAutoNewClientSendMessage 验证 NewClient 对 ProviderYoloAuto 的注册:
// 请求发往 {base}/chat/completions 且响应解析正常(端到端走 openAIAdapter 协议)。
func TestYoloAutoNewClientSendMessage(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-yolo-test",
			"model": "yolo",
			"choices": [{
				"index": 0,
				"finish_reason": "stop",
				"message": {"role": "assistant", "content": "hello from yolo-auto"}
			}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
		}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		Provider: ProviderYoloAuto,
		APIKey:   "yolo-test-key",
		Model:    "yolo",
		BaseURL:  server.URL,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resp, err := client.SendMessage(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if resp.Content != "hello from yolo-auto" {
		t.Errorf("Content = %q, want %q", resp.Content, "hello from yolo-auto")
	}
	if gotPath != "/chat/completions" {
		t.Errorf("request path = %q, want /chat/completions", gotPath)
	}
	if gotAuth != "Bearer yolo-test-key" {
		t.Errorf("Authorization = %q, want Bearer yolo-test-key", gotAuth)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
		t.Errorf("Usage = %+v, want TotalTokens=15", resp.Usage)
	}
}
