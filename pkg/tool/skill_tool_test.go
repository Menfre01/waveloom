package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/permission"
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
	loader := skill.NewLoader(home, home, "test-sid", "medium", nil)
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

// TestRegression_SkillLoadError 验证非“skill 不存在”类错误（如白名单拦截）
// 返回 "Skill load failed" 而非误导性的 "Skill not found"。
func TestRegression_SkillLoadError(t *testing.T) {
	home := t.TempDir()
	skillDir := filepath.Join(home, ".claude", "skills", "restricted")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 写入一个含非白名单注入的 skill（allowed-tools 只允许 echo，但 body 中有 date）
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
allowed-tools:
  - "Bash(echo *)"
---
!`+"`date '+%Y-%m-%d'`"+`
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Guard 非 nil，lintInjections 会拦截 date
	loader := skill.NewLoader(home, home, "test-sid", "medium", guardStub{})
	st := NewSkillTool(loader)

	result, err := st.Execute(context.Background(), SkillParams{Name: "restricted"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == nil {
		t.Fatal("expected error for restricted skill")
	}
	if !strings.Contains(result.Error.Message, "Skill load failed") {
		t.Errorf("expected 'Skill load failed', got: %s", result.Error.Message)
	}
	if strings.Contains(result.Error.Message, "Skill not found") {
		t.Error("should not say 'Skill not found' for load errors")
	}
}

// guardStub 实现 permission.Guard，仅用于触发 lintInjections 校验（非 nil 即可）。
type guardStub struct{}

func (guardStub) Check(_ context.Context, _ string, _ json.RawMessage) permission.DecisionResult {
	return permission.DecisionResult{Decision: permission.DecisionAllow}
}
func (guardStub) AddRule(_ permission.Rule, _ permission.RuleScope) error  { return nil }
func (guardStub) RemoveRule(_ permission.Rule, _ permission.RuleScope) error { return nil }
func (guardStub) ListRules() []permission.RuleEntry                           { return nil }
func (guardStub) PersistRule(_ permission.Rule) error                         { return nil }
func (guardStub) SessionAllow(_ string, _ json.RawMessage)                    {}
func (guardStub) SessionDeny(_ string, _ json.RawMessage)                     {}
func (guardStub) ClearSession()                                               {}
func (guardStub) SessionMemoryLen() int                                       { return 0 }
