package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAskUserQuestionName(t *testing.T) {
	tool := &AskUserQuestion{}
	if tool.Name() != "ask_user_question" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "ask_user_question")
	}
}

func TestAskUserQuestionConcurrentSafe(t *testing.T) {
	tool := &AskUserQuestion{}
	if tool.ConcurrentSafe() {
		t.Error("ConcurrentSafe() = true, want false (user interaction must go through serial path for Loop interception)")
	}
}

func TestAskUserQuestionRequiresUserInteraction(t *testing.T) {
	tool := &AskUserQuestion{}
	if !tool.RequiresUserInteraction() {
		t.Error("RequiresUserInteraction() = false, want true")
	}
}

func TestAskUserQuestionDescription(t *testing.T) {
	tool := &AskUserQuestion{}
	desc := tool.Description()
	if !strings.Contains(desc, "Ask the user") {
		t.Error("Description should mention 'Ask the user'")
	}

	// "Do NOT use for plan approval" 已移至 Prompt() → C1
	prompt := tool.Prompt()
	if !strings.Contains(prompt, "exit_plan_mode") || !strings.Contains(prompt, "Do NOT use") {
		t.Error("Prompt should warn against using this tool for plan approval")
	}
}

func TestAskUserQuestionSchema(t *testing.T) {
	tool := &AskUserQuestion{}
	schema := tool.Schema()
	if len(schema) == 0 {
		t.Fatal("Schema() returned empty")
	}

	// Validate it's valid JSON
	var s map[string]interface{}
	if err := json.Unmarshal(schema, &s); err != nil {
		t.Fatalf("Schema() is not valid JSON: %v", err)
	}

	if s["type"] != "object" {
		t.Errorf("Schema type = %v, want object", s["type"])
	}

	required, ok := s["required"].([]interface{})
	if !ok {
		t.Fatal("Schema missing 'required' field")
	}
	hasQuestions := false
	for _, r := range required {
		if r == "questions" {
			hasQuestions = true
			break
		}
	}
	if !hasQuestions {
		t.Error("Schema 'required' should include 'questions'")
	}
}

func TestAskUserQuestionExecuteDuplicateQuestions(t *testing.T) {
	tool := &AskUserQuestion{}
	params := AskUserQuestionParams{
		Questions: []Question{
			{
				Question: "What approach?",
				Header:   "Approach",
				Options:  []QuestionOption{{Label: "A", Description: "Option A"}, {Label: "B", Description: "Option B"}},
			},
			{
				Question: "What approach?",
				Header:   "Approach2",
				Options:  []QuestionOption{{Label: "C", Description: "Option C"}, {Label: "D", Description: "Option D"}},
			},
		},
	}

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if result.Error == nil {
		t.Fatal("Execute() should return error for duplicate questions")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("Error kind = %q, want %q", result.Error.Kind, ErrKindInvalidArgs)
	}
}

func TestAskUserQuestionExecuteDuplicateOptions(t *testing.T) {
	tool := &AskUserQuestion{}
	params := AskUserQuestionParams{
		Questions: []Question{
			{
				Question: "What approach?",
				Header:   "Approach",
				Options: []QuestionOption{
					{Label: "A", Description: "Option A"},
					{Label: "A", Description: "Duplicate label"},
				},
			},
		},
	}

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if result.Error == nil {
		t.Fatal("Execute() should return error for duplicate option labels")
	}
	if result.Error.Kind != ErrKindInvalidArgs {
		t.Errorf("Error kind = %q, want %q", result.Error.Kind, ErrKindInvalidArgs)
	}
}

func TestAskUserQuestionExecuteValidParams(t *testing.T) {
	tool := &AskUserQuestion{}
	params := AskUserQuestionParams{
		Questions: []Question{
			{
				Question:    "Which library?",
				Header:      "Library",
				MultiSelect: false,
				Options: []QuestionOption{
					{Label: "Option A", Description: "First option"},
					{Label: "Option B (Recommended)", Description: "Recommended option"},
				},
			},
		},
	}

	result, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	// Without Loop mediation, Execute should return a user_declined error
	// since it requires interactive TUI session
	if result.Error == nil {
		t.Error("Execute() without Loop mediation should return error")
	}
	if result.Error.Kind != "user_declined" {
		t.Errorf("Error kind = %q, want %q", result.Error.Kind, "user_declined")
	}
}

func TestAskUserQuestionWrapAndRegister(t *testing.T) {
	tool := &AskUserQuestion{}
	erased := Wrap(tool)

	if erased.Name() != "ask_user_question" {
		t.Errorf("Name() = %q, want %q", erased.Name(), "ask_user_question")
	}

	// Verify it can be registered in a registry
	r := NewRegistry()
	r.Register(erased)
	specs := r.List()

	found := false
	for _, s := range specs {
		if s.Name == "ask_user_question" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ask_user_question not found in registry after Register")
	}

	// Verify Get works
	got, ok := r.Get("ask_user_question")
	if !ok {
		t.Fatal("Get(ask_user_question) returned false")
	}
	if got.Name() != "ask_user_question" {
		t.Errorf("Get().Name() = %q, want %q", got.Name(), "ask_user_question")
	}
}

func TestAskUserQuestionSchemaMinItems(t *testing.T) {
	tool := &AskUserQuestion{}
	schema := tool.Schema()

	var s map[string]interface{}
	if err := json.Unmarshal(schema, &s); err != nil {
		t.Fatalf("Schema unmarshal error: %v", err)
	}

	props := s["properties"].(map[string]interface{})
	questions := props["questions"].(map[string]interface{})

	minItems, ok := questions["minItems"].(float64)
	if !ok {
		t.Fatal("Schema: questions.minItems missing or not a number")
	}
	if minItems != 1 {
		t.Errorf("questions.minItems = %v, want 1", minItems)
	}

	maxItems, ok := questions["maxItems"].(float64)
	if !ok {
		t.Fatal("Schema: questions.maxItems missing or not a number")
	}
	if maxItems != 4 {
		t.Errorf("questions.maxItems = %v, want 4", maxItems)
	}
}

func TestAskUserQuestionSchemaOptionsBounds(t *testing.T) {
	tool := &AskUserQuestion{}
	schema := tool.Schema()

	var s map[string]interface{}
	if err := json.Unmarshal(schema, &s); err != nil {
		t.Fatalf("Schema unmarshal error: %v", err)
	}

	props := s["properties"].(map[string]interface{})
	questions := props["questions"].(map[string]interface{})
	items := questions["items"].(map[string]interface{})
	itemProps := items["properties"].(map[string]interface{})
	options := itemProps["options"].(map[string]interface{})

	minItems, _ := options["minItems"].(float64)
	if minItems != 2 {
		t.Errorf("options.minItems = %v, want 2", minItems)
	}
	maxItems, _ := options["maxItems"].(float64)
	if maxItems != 4 {
		t.Errorf("options.maxItems = %v, want 4", maxItems)
	}
}

func TestAskUserQuestionSchemaMultiSelectDefault(t *testing.T) {
	tool := &AskUserQuestion{}
	schema := tool.Schema()

	var s map[string]interface{}
	if err := json.Unmarshal(schema, &s); err != nil {
		t.Fatalf("Schema unmarshal error: %v", err)
	}

	props := s["properties"].(map[string]interface{})
	questions := props["questions"].(map[string]interface{})
	items := questions["items"].(map[string]interface{})
	itemProps := items["properties"].(map[string]interface{})
	multiSelect := itemProps["multiSelect"].(map[string]interface{})

	defaultVal, ok := multiSelect["default"].(bool)
	if !ok {
		t.Fatal("Schema: multiSelect.default missing or not a boolean")
	}
	if defaultVal != false {
		t.Errorf("multiSelect.default = %v, want false", defaultVal)
	}
}
