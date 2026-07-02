package agentloop

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/permission"
)

// mockQuestionResponder 实现 permission.UserResponder，返回预编程的问题答案。
type mockQuestionResponder struct {
	responses []permission.QuestionResponse
	err       error
}

func (m *mockQuestionResponder) AskUser(ctx context.Context, toolName string, input json.RawMessage, result permission.DecisionResult) permission.UserChoice {
	return permission.UserChoice{Decision: permission.DecisionDeny}
}

func (m *mockQuestionResponder) AnswerQuestion(ctx context.Context, questions []permission.QuestionPrompt) ([]permission.QuestionResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.responses, nil
}

func (m *mockQuestionResponder) EnterPlan(ctx context.Context) (bool, error) {
	return true, nil
}

func (m *mockQuestionResponder) ApprovePlan(ctx context.Context, plan string) (permission.PlanApproval, error) {
	return permission.PlanApproval{Approved: true}, nil
}

func TestExecuteAskUserQuestion_SingleSelect(t *testing.T) {
	responder := &mockQuestionResponder{
		responses: []permission.QuestionResponse{
			{Question: "Which approach?", Answers: []string{"Option A"}},
		},
	}
	l := New(nil, nil, Config{UserResponder: responder})

	args := `{"questions":[{"question":"Which approach?","header":"Approach","options":[{"label":"Option A","description":"First"},{"label":"Option B (Recommended)","description":"Second"}]}]}`

	tc := llm.ToolCall{
		ID:        "call_1",
		Name:      "ask_user_question",
		Arguments: args,
	}

	ctx := context.Background()
	ch := make(chan TurnEvent, 4)

	result, err := l.executeAskUserQuestion(ctx, tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("executeAskUserQuestion unexpected error: %v", err)
	}
	if result.IsError() {
		t.Fatalf("unexpected error: %s", result.Error.Message)
	}

	// 验证输出格式为 map[string]string
	var output struct {
		Questions []QuestionPrompt  `json:"questions"`
		Answers   map[string]string `json:"answers"`
	}
	if err := json.Unmarshal([]byte(result.Content), &output); err != nil {
		t.Fatalf("failed to unmarshal result: %v\nContent: %s", err, result.Content)
	}

	if len(output.Answers) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(output.Answers))
	}
	if output.Answers["Which approach?"] != "Option A" {
		t.Errorf("expected 'Option A', got %q", output.Answers["Which approach?"])
	}
}

func TestExecuteAskUserQuestion_MultiSelect(t *testing.T) {
	responder := &mockQuestionResponder{
		responses: []permission.QuestionResponse{
			{Question: "Pick toppings?", Answers: []string{"A", "C"}},
		},
	}
	l := New(nil, nil, Config{UserResponder: responder})

	args := `{"questions":[{"question":"Pick toppings?","header":"Toppings","multiSelect":true,"options":[{"label":"A","description":"A"},{"label":"B","description":"B"},{"label":"C","description":"C"}]}]}`

	tc := llm.ToolCall{
		ID:        "call_2",
		Name:      "ask_user_question",
		Arguments: args,
	}

	ctx := context.Background()
	ch := make(chan TurnEvent, 4)

	result, err := l.executeAskUserQuestion(ctx, tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var output struct {
		Answers map[string]string `json:"answers"`
	}
	if err := json.Unmarshal([]byte(result.Content), &output); err != nil {
		t.Fatalf("failed to unmarshal result: %v\nContent: %s", err, result.Content)
	}

	if output.Answers["Pick toppings?"] != "A, C" {
		t.Errorf("expected 'A, C', got %q", output.Answers["Pick toppings?"])
	}
}

func TestExecuteAskUserQuestion_UserDeclined(t *testing.T) {
	responder := &mockQuestionResponder{
		responses: nil, // nil = 拒绝
	}
	l := New(nil, nil, Config{UserResponder: responder})

	args := `{"questions":[{"question":"Q?","header":"H","options":[{"label":"X","description":"X"},{"label":"Y","description":"Y"}]}]}`

	tc := llm.ToolCall{
		ID:        "call_3",
		Name:      "ask_user_question",
		Arguments: args,
	}

	ctx := context.Background()
	ch := make(chan TurnEvent, 4)

	result, err := l.executeAskUserQuestion(ctx, tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError() {
		t.Fatal("expected error for declined answer")
	}
	if result.Error.Kind != "user_declined" {
		t.Errorf("expected kind 'user_declined', got %q", result.Error.Kind)
	}
}

func TestExecuteAskUserQuestion_NoResponder(t *testing.T) {
	l := New(nil, nil, Config{}) // UserResponder = nil

	args := `{"questions":[{"question":"Q?","header":"H","options":[{"label":"X","description":"X"},{"label":"Y","description":"Y"}]}]}`

	tc := llm.ToolCall{
		ID:        "call_4",
		Name:      "ask_user_question",
		Arguments: args,
	}

	ctx := context.Background()
	ch := make(chan TurnEvent, 4)

	result, err := l.executeAskUserQuestion(ctx, tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError() {
		t.Fatal("expected error when no UserResponder configured")
	}
	if result.Error.Kind != "user_declined" {
		t.Errorf("expected kind 'user_declined', got %q", result.Error.Kind)
	}
}

func TestExecuteAskUserQuestion_ContextCancelled(t *testing.T) {
	responder := &mockQuestionResponder{
		err: context.Canceled,
	}
	l := New(nil, nil, Config{UserResponder: responder})

	args := `{"questions":[{"question":"Q?","header":"H","options":[{"label":"X","description":"X"},{"label":"Y","description":"Y"}]}]}`

	tc := llm.ToolCall{
		ID:        "call_5",
		Name:      "ask_user_question",
		Arguments: args,
	}

	ctx := context.Background()
	ch := make(chan TurnEvent, 4)

	result, err := l.executeAskUserQuestion(ctx, tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError() {
		t.Fatal("expected error for cancelled context")
	}
	if result.Error.Kind != "user_declined" {
		t.Errorf("expected kind 'user_declined', got %q", result.Error.Kind)
	}
}

func TestExecuteAskUserQuestion_InvalidJSON(t *testing.T) {
	l := New(nil, nil, Config{})

	tc := llm.ToolCall{
		ID:        "call_6",
		Name:      "ask_user_question",
		Arguments: `{invalid json`,
	}

	ctx := context.Background()
	ch := make(chan TurnEvent, 4)

	result, err := l.executeAskUserQuestion(ctx, tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError() {
		t.Fatal("expected error for invalid JSON")
	}
	if result.Error.Kind != "invalid_args" {
		t.Errorf("expected kind 'invalid_args', got %q", result.Error.Kind)
	}
}

func TestAskUserQuestionEvent_ImplementsTurnEvent(t *testing.T) {
	// AskUserQuestionEvent is a concrete struct — verifying it satisfies TurnEvent at compile time.
	var _ TurnEvent = AskUserQuestionEvent{}
}

func TestQuestionResponseTypes(t *testing.T) {
	// Verify the types are properly JSON-serializable
	prompt := QuestionPrompt{
		Question:    "Test?",
		Header:      "Test",
		MultiSelect: true,
		Options: []QuestionOptionPrompt{
			{Label: "A", Description: "Option A"},
			{Label: "B", Description: "Option B"},
		},
	}

	data, err := json.Marshal(prompt)
	if err != nil {
		t.Fatalf("QuestionPrompt marshal error: %v", err)
	}

	var decoded QuestionPrompt
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("QuestionPrompt unmarshal error: %v", err)
	}
	if decoded.Question != "Test?" {
		t.Errorf("roundtrip failed: %q != %q", decoded.Question, "Test?")
	}
}
