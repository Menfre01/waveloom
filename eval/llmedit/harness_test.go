package llmedit

import (
	"context"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// mockEditClient 模拟 LLM 返回一个 edit 工具调用。
type mockEditClient struct {
	responses []*llm.Response
	callCount int
}

func (m *mockEditClient) SendMessage(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (*llm.Response, error) {
	idx := m.callCount
	m.callCount++
	if idx < len(m.responses) {
		return m.responses[idx], nil
	}
	return &llm.Response{Content: "done"}, nil
}

func (m *mockEditClient) SendMessageStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (<-chan llm.StreamingEvent, error) {
	ch := make(chan llm.StreamingEvent, 4)
	go func() {
		defer close(ch)
		resp, _ := m.SendMessage(ctx, messages, tools)
		if resp.Content != "" {
			ch <- llm.StreamingEvent{Delta: resp.Content}
		}
		if len(resp.ToolCalls) > 0 {
			ch <- llm.StreamingEvent{ToolCalls: resp.ToolCalls, Done: false}
		}
		ch <- llm.StreamingEvent{Done: true}
	}()
	return ch, nil
}

func (m *mockEditClient) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	return nil, nil
}

func (m *mockEditClient) ProviderName() string { return "mock" }

func (m *mockEditClient) GetBalance(ctx context.Context) (*llm.BalanceInfo, error) {
	return nil, nil
}
func (m *mockEditClient) SupportsBalance() bool { return false }

func TestHarnessSmoke(t *testing.T) {
	tasks, err := LoadTasks("testdata/tasks")
	if err != nil {
		t.Fatalf("LoadTasks: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatal("no tasks found")
	}
	task := tasks[0]
	t.Logf("task: %s (%s)", task.Name, task.Type)

	client := &mockEditClient{
		responses: []*llm.Response{
			{Content: "done"},
		},
	}
	registry := tool.NewRegistry()
	RegisterEditTools(registry)

	runner := NewRunner(client, registry, "You are a coding agent.")
	runner.MaxSteps = 2

	ctx := context.Background()
	result := runner.Run(ctx, task)

	if result.Metrics == nil {
		t.Fatal("metrics should not be nil")
	}
	t.Logf("turns: %d, error: %v", result.Metrics.TotalTurns, result.Error)
}

func TestLoadTasks_Empty(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadTasks(dir)
	if err == nil || !strings.Contains(err.Error(), "no tasks") {
		t.Fatalf("expected 'no tasks' error, got: %v", err)
	}
}

func TestTaskValidate(t *testing.T) {
	tests := []struct {
		name string
		t    Task
		ok   bool
	}{
		{
			name: "valid",
			t:    Task{Name: "t", Instruction: "do", Files: map[string]string{"f": "x"}, Gold: map[string]string{"f": "y"}},
			ok:   true,
		},
		{
			name: "missing name",
			t:    Task{Instruction: "do", Files: map[string]string{"f": "x"}},
			ok:   false,
		},
	}
	for _, tt := range tests {
		err := tt.t.Validate()
		if (err == nil) != tt.ok {
			t.Errorf("%s: expected ok=%v, got err=%v", tt.name, tt.ok, err)
		}
	}
}

func TestScore(t *testing.T) {
	task := &Task{
		Name: "test",
		Files: map[string]string{
			"main.go": "package main\nfunc main() {}\n",
		},
		Gold: map[string]string{
			"main.go": "package main\n\nfunc main() {}\n",
		},
	}

	got := map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	}
	sr := Score(task, got)
	if !sr.Passed {
		t.Error("expected passed for exact match")
	}

	got2 := map[string]string{
		"main.go": "package main\nfunc main() {}\n",
	}
	sr2 := Score(task, got2)
	if sr2.Passed {
		t.Error("expected not passed for mismatch")
	}
}

func TestBatchResult(t *testing.T) {
	br := &BatchResult{Total: 10, Passed: 8, Failed: 2}
	summary := br.SummaryJSON()
	if !strings.Contains(summary, "\"passed\": 8") {
		t.Errorf("unexpected summary: %s", summary)
	}
}
