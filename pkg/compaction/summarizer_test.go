package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/llm"
)

// mockClient 实现 llm.Client 用于测试 CompactionSummarizer。
type mockClient struct {
	content      string
	err          error
	delay        time.Duration // >0 → SendMessage 先等待 delay 或 ctx 取消
	gotMaxTokens int           // SendMessage 收到 ctx 中的 max_tokens(0 = 未设置)
}

func (m *mockClient) SendMessage(ctx context.Context, _ []llm.Message, _ []llm.ToolSpec) (*llm.Response, error) {
	if n, ok := llm.MaxTokensFromContext(ctx); ok {
		m.gotMaxTokens = n
	}
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
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

// TestRegression_Summarize_Timeout 验证 per-call deadline:
// 服务端挂起(不响应)时 Summarize 在 summarizeCallTimeout 内快速失败,
// 而不是无限等待(最坏冻结曾达 40 分钟/调用)。
func TestRegression_Summarize_Timeout(t *testing.T) {
	old := summarizeCallTimeout
	summarizeCallTimeout = 50 * time.Millisecond
	t.Cleanup(func() { summarizeCallTimeout = old })

	client := &mockClient{content: "{}", delay: 500 * time.Millisecond}
	s := NewCompactionSummarizer(client, 0)

	start := time.Now()
	_, err := s.Summarize(context.Background(), nil, []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error on hanging client")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("deadline not enforced: took %v (mock delay 500ms)", elapsed)
	}
}

// TestRegression_Summarize_Timeout_ExternalCtxShorter 验证外部 ctx 更短时
// 外部 deadline 优先(WithTimeout 取更早者),调用不放大冻结时长。
func TestRegression_Summarize_Timeout_ExternalCtxShorter(t *testing.T) {
	// summarizeCallTimeout 保持默认 300s,外部 ctx 仅 50ms
	client := &mockClient{content: "{}", delay: 500 * time.Millisecond}
	s := NewCompactionSummarizer(client, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := s.Summarize(ctx, nil, []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error on hanging client")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("external ctx deadline not honored: took %v", elapsed)
	}
}

// TestRegression_Summarize_Timeout_NormalPath 验证超时变量恢复后
// 正常路径不受影响(回归防护:timeout 注入不泄漏到其他测试)。
func TestRegression_Summarize_Timeout_NormalPath(t *testing.T) {
	old := summarizeCallTimeout
	summarizeCallTimeout = 50 * time.Millisecond
	t.Cleanup(func() { summarizeCallTimeout = old })

	summaryJSON := `{"progress":{"summary":"test","files":[]},"pending":[],"pitfalls":[],"constraints":""}`
	// delay 20ms < 50ms timeout → 正常返回
	client := &mockClient{content: summaryJSON, delay: 20 * time.Millisecond}
	s := NewCompactionSummarizer(client, 0)

	result, err := s.Summarize(context.Background(), nil, []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error on normal path: %v", err)
	}
	if !json.Valid([]byte(result)) {
		t.Fatalf("result is not valid JSON: %s", result)
	}
}

// TestRegression_Summarize_SendsMaxTokens 回归防护:
// 摘要请求必须显式携带 max_tokens——不发送时服务端默认上限
// 会概率性截断长 JSON 输出(实测复现:截断在 UTF-8 中间 → json.Valid
// 失败 → 压缩失败 → Tier3ConsecutiveFailures++)。
func TestRegression_Summarize_SendsMaxTokens(t *testing.T) {
	summaryJSON := `{"progress":{"summary":"test","files":[]},"pending":[],"pitfalls":[],"constraints":""}`
	client := &mockClient{content: summaryJSON}
	s := NewCompactionSummarizer(client, 0) // maxTokens=0 → SummaryMaxTokens

	if _, err := s.Summarize(context.Background(), nil, []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.gotMaxTokens != SummaryMaxTokens {
		t.Fatalf("请求应携带 max_tokens=%d,实际 %d", SummaryMaxTokens, client.gotMaxTokens)
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
