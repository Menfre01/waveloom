package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/skill"
)

func setupSkillTool(t *testing.T) *SkillTool {
	t.Helper()
	home := t.TempDir()
	skillDir := filepath.Join(home, ".claude", "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: deploy
description: Deploy to production
---
Deploy $ARGUMENTS now.`), 0o644); err != nil {
		t.Fatal(err)
	}
	loader := skill.NewLoader(home, home, "test-sid", "medium")
	return NewSkillTool(loader)
}

func TestSkillTool_Execute(t *testing.T) {
	st := setupSkillTool(t)
	result, err := st.Execute(context.Background(), SkillParams{Name: "deploy", Arguments: "production"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if !strings.Contains(result.Content, "Deploy production now") {
		t.Errorf("unexpected content: %s", result.Content)
	}
}

func TestSkillTool_NotFound(t *testing.T) {
	st := setupSkillTool(t)
	result, err := st.Execute(context.Background(), SkillParams{Name: "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == nil {
		t.Fatal("expected error for nonexistent skill")
	}
	if result.Error.Class != ErrorClassRecoverable {
		t.Errorf("expected Recoverable error, got %v", result.Error.Class)
	}
}

func TestSkillTool_Schema(t *testing.T) {
	st := setupSkillTool(t)
	schema := st.Schema()
	if !strings.Contains(string(schema), "skill") {
		t.Error("schema should reference skill tool")
	}
	if !strings.Contains(string(schema), "required") {
		t.Error("schema should have required field")
	}
}

func TestSkillTool_Name(t *testing.T) {
	st := setupSkillTool(t)
	if st.Name() != "skill" {
		t.Errorf("name = %q, want %q", st.Name(), "skill")
	}
}
