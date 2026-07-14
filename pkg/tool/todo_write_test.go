package tool

import (
	"context"
	"testing"
)

func TestTodoWrite_Name(t *testing.T) {
	tw := &TodoWrite{}
	if tw.Name() != "todo_write" {
		t.Errorf("Name() = %q, want %q", tw.Name(), "todo_write")
	}
}

func TestTodoWrite_ConcurrentSafe(t *testing.T) {
	tw := &TodoWrite{}
	if tw.ConcurrentSafe() {
		t.Error("TodoWrite should not be concurrent-safe")
	}
}

func TestTodoWrite_RequiresUserInteraction(t *testing.T) {
	tw := &TodoWrite{}
	if tw.RequiresUserInteraction() {
		t.Error("TodoWrite should not require user interaction")
	}
}

func TestTodoWrite_Schema(t *testing.T) {
	tw := &TodoWrite{}
	schema := tw.Schema()
	if len(schema) == 0 {
		t.Error("Schema should not be empty")
	}
	// Quick sanity: should mention "todos"
	s := string(schema)
	if !containsStr(s, "todos") {
		t.Error("Schema missing 'todos'")
	}
}

func TestTodoWrite_Description(t *testing.T) {
	tw := &TodoWrite{}
	desc := tw.Description()
	if len(desc) < 100 {
		t.Errorf("Description too short: %d chars", len(desc))
	}
}

func TestTodoWrite_Execute(t *testing.T) {
	tw := &TodoWrite{}
	result, err := tw.Execute(context.TODO(), nil)
	if err != nil {
		t.Errorf("Execute error: %v", err)
	}
	if result == nil {
		t.Fatal("Execute returned nil result")
		return
	}
	if result.Content == "" {
		t.Error("Execute result content should not be empty")
	}
}
