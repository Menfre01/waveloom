package tool

import (
	"context"
	"testing"
)

func TestEnterPlanMode_Name(t *testing.T) {
	tool := &EnterPlanMode{}
	if tool.Name() != "enter_plan_mode" {
		t.Errorf("expected name 'enter_plan_mode', got %q", tool.Name())
	}
}

func TestEnterPlanMode_ConcurrentSafe(t *testing.T) {
	tool := &EnterPlanMode{}
	if tool.ConcurrentSafe() {
		t.Error("EnterPlanMode should NOT be concurrent safe (serial only)")
	}
}

func TestEnterPlanMode_RequiresUserInteraction(t *testing.T) {
	tool := &EnterPlanMode{}
	if !tool.RequiresUserInteraction() {
		t.Error("EnterPlanMode should require user interaction")
	}
}

func TestEnterPlanMode_Description(t *testing.T) {
	tool := &EnterPlanMode{}
	desc := tool.Description()
	if desc == "" {
		t.Error("expected non-empty description")
	}
	if !containsStr(desc, "plan mode") {
		t.Errorf("expected 'plan mode' in description, got: %s", truncateStr(desc, 80))
	}
	if !containsStr(desc, "exit_plan_mode") {
		t.Errorf("expected 'exit_plan_mode' in description")
	}
}

func TestEnterPlanMode_Schema(t *testing.T) {
	tool := &EnterPlanMode{}
	schema := string(tool.Schema())
	if schema == "" {
		t.Error("expected non-empty schema")
	}
	if !containsStr(schema, `"type"`) {
		t.Errorf("expected JSON schema with type, got: %s", schema)
	}
}

func TestEnterPlanMode_Execute(t *testing.T) {
	tool := &EnterPlanMode{}
	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
	if !containsStr(result.Content, "plan mode") {
		t.Errorf("expected 'plan mode' in result, got: %s", truncateStr(result.Content, 80))
	}
	if result.IsError() {
		t.Errorf("unexpected error in result: %s", result.Error.Message)
	}
}

// ============================================================================
// ExitPlanMode 测试
// ============================================================================

func TestExitPlanMode_Name(t *testing.T) {
	tool := &ExitPlanMode{}
	if tool.Name() != "exit_plan_mode" {
		t.Errorf("expected name 'exit_plan_mode', got %q", tool.Name())
	}
}

func TestExitPlanMode_ConcurrentSafe(t *testing.T) {
	tool := &ExitPlanMode{}
	if tool.ConcurrentSafe() {
		t.Error("ExitPlanMode should NOT be concurrent safe (serial only)")
	}
}

func TestExitPlanMode_RequiresUserInteraction(t *testing.T) {
	tool := &ExitPlanMode{}
	if !tool.RequiresUserInteraction() {
		t.Error("ExitPlanMode should require user interaction")
	}
}

func TestExitPlanMode_Description(t *testing.T) {
	tool := &ExitPlanMode{}
	desc := tool.Description()
	if desc == "" {
		t.Error("expected non-empty description")
	}
	if !containsStr(desc, "plan") {
		t.Errorf("expected 'plan' in description, got: %s", truncateStr(desc, 80))
	}
}

func TestExitPlanMode_Schema(t *testing.T) {
	tool := &ExitPlanMode{}
	schema := string(tool.Schema())
	if schema == "" {
		t.Error("expected non-empty schema")
	}
	if !containsStr(schema, `"type"`) {
		t.Errorf("expected JSON schema with type, got: %s", schema)
	}
}

func TestExitPlanMode_Execute(t *testing.T) {
	tool := &ExitPlanMode{}
	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
	if !containsStr(result.Content, "approved") {
		t.Errorf("expected 'approved' in result, got: %s", truncateStr(result.Content, 80))
	}
	if result.IsError() {
		t.Errorf("unexpected error in result: %s", result.Error.Message)
	}
}

// ============================================================================
// 验证 EnterPlanMode / ExitPlanMode 在 DefaultRegistry 中注册
// ============================================================================

func TestDefaultRegistryIncludesPlanModeTools(t *testing.T) {
	r := NewDefaultRegistry()

	enter, ok := r.Get("enter_plan_mode")
	if !ok {
		t.Fatal("enter_plan_mode not found in default registry")
	}
	if enter.Name() != "enter_plan_mode" {
		t.Errorf("unexpected name: %s", enter.Name())
	}

	exit, ok := r.Get("exit_plan_mode")
	if !ok {
		t.Fatal("exit_plan_mode not found in default registry")
	}
	if exit.Name() != "exit_plan_mode" {
		t.Errorf("unexpected name: %s", exit.Name())
	}
}

// ============================================================================
// helpers (avoid importing from other test files)
// ============================================================================

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
