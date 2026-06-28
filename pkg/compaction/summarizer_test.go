package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"waveloom/pkg/llm"
)

// mockClient 实现 llm.Client 用于测试 CompactionSummarizer。
type mockClient struct {
	content string
	err     error
}

func (m *mockClient) SendMessage(_ context.Context, _ []llm.Message, _ []llm.ToolSpec) (*llm.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.Response{Content: m.content}, nil
}

func (m *mockClient) SendMessageStream(_ context.Context, _ []llm.Message, _ []llm.ToolSpec) (<-chan llm.StreamingEvent, error) {
	return nil, errors.New("not implemented")
}

func (m *mockClient) GetBalance(_ context.Context) (*llm.BalanceInfo, error) { return nil, nil }
func (m *mockClient) SupportsBalance() bool                                   { return false }
func (m *mockClient) ListModels(_ context.Context) ([]llm.ModelInfo, error)   { return nil, nil }

func TestCompactionSummarizer_Success(t *testing.T) {
	summaryJSON := `{"progress":{"summary":"test","files":[]},"pending":[],"pitfalls":[],"constraints":""}`
	client := &mockClient{content: summaryJSON}
	s := NewCompactionSummarizer(client, 0)

	result, err := s.Summarize(context.Background(), nil, []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !json.Valid([]byte(result)) {
		t.Fatalf("result is not valid JSON: %s", result)
	}
}

func TestCompactionSummarizer_JSONWrappedInMarkdown(t *testing.T) {
	// 模型可能在 JSON 外用 ```json 包裹
	summaryJSON := `{"progress":{"summary":"test","files":[]},"pending":[],"pitfalls":[],"constraints":""}`
	content := "```json\n" + summaryJSON + "\n```"
	client := &mockClient{content: content}
	s := NewCompactionSummarizer(client, 0)

	result, err := s.Summarize(context.Background(), nil, []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, summaryJSON) {
		t.Fatalf("expected extracted JSON, got: %s", result)
	}
}

func TestCompactionSummarizer_EmptyResponse(t *testing.T) {
	client := &mockClient{content: ""}
	s := NewCompactionSummarizer(client, 0)

	_, err := s.Summarize(context.Background(), nil, []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	})
	if err == nil {
		t.Fatal("expected error on empty response")
	}
}

func TestCompactionSummarizer_InvalidJSON(t *testing.T) {
	client := &mockClient{content: "not json at all"}
	s := NewCompactionSummarizer(client, 0)

	_, err := s.Summarize(context.Background(), nil, []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	})
	if err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}

func TestCompactionSummarizer_ClientError(t *testing.T) {
	client := &mockClient{err: errors.New("network error")}
	s := NewCompactionSummarizer(client, 0)

	_, err := s.Summarize(context.Background(), nil, []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	})
	if err == nil {
		t.Fatal("expected error on client failure")
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"a":1}`, `{"a":1}`},
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"```\n{\"a\":1}\n```", `{"a":1}`},
		{"some text before ```json\n{\"a\":1}\n``` after", `{"a":1}`},
		{"no json here", "no json here"},
	}
	for _, tc := range tests {
		got := extractJSON(tc.input)
		if got != tc.want {
			t.Errorf("extractJSON(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
