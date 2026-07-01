package slashcommand

import (
	"context"
	"errors"
	"testing"
)

// stubSkillExecutor 用于测试的 SkillExecutor 桩。
type stubSkillExecutor struct {
	body string
	err  error
}

func (s *stubSkillExecutor) ExecuteSkill(ctx context.Context, name, args string) (string, error) {
	return s.body, s.err
}

func setupSkillCmd(t *testing.T) *SkillCommand {
	t.Helper()
	return NewSkillCommand(SkillDescriptor{
		Name:        "deploy",
		Description: "Deploy",
		Args:        "env",
	}, &stubSkillExecutor{body: "Deploy production"})
}

func TestSkillCommand_Name(t *testing.T) {
	cmd := setupSkillCmd(t)
	if cmd.Name() != "deploy" {
		t.Errorf("name = %q, want %q", cmd.Name(), "deploy")
	}
}

func TestSkillCommand_ArgsPlaceholder(t *testing.T) {
	cmd := setupSkillCmd(t)
	if cmd.ArgsPlaceholder() != "env" {
		t.Errorf("args = %q, want %q", cmd.ArgsPlaceholder(), "env")
	}
}

func TestSkillCommand_Description(t *testing.T) {
	cmd := setupSkillCmd(t)
	if cmd.Description() != "Deploy" {
		t.Errorf("description = %q, want %q", cmd.Description(), "Deploy")
	}
}

func TestSkillCommand_Aliases(t *testing.T) {
	cmd := setupSkillCmd(t)
	if aliases := cmd.Aliases(); aliases != nil {
		t.Errorf("Aliases = %v, want nil", aliases)
	}
}

func TestSkillCommand_Execute(t *testing.T) {
	cmd := setupSkillCmd(t)
	result, err := cmd.Execute(context.Background(), "production")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SideEffects) != 1 {
		t.Fatalf("expected 1 side effect, got %d", len(result.SideEffects))
	}
	se := result.SideEffects[0]
	if se.Kind != SideEffectInvokeSkill {
		t.Errorf("kind = %q, want %q", se.Kind, SideEffectInvokeSkill)
	}
	if se.Detail != "Deploy production" {
		t.Errorf("body = %q, want %q", se.Detail, "Deploy production")
	}
	if se.Detail2 != "deploy" {
		t.Errorf("name = %q, want %q", se.Detail2, "deploy")
	}
	if se.Detail3 != "production" {
		t.Errorf("args = %q, want %q", se.Detail3, "production")
	}
}

func TestSkillCommand_ExecuteNoArgs(t *testing.T) {
	cmd := setupSkillCmd(t)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	se := result.SideEffects[0]
	if se.Detail3 != "" {
		t.Errorf("args should be empty, got: %s", se.Detail3)
	}
}

func TestSkillCommand_ExecuteError(t *testing.T) {
	info := SkillDescriptor{Name: "gone", Description: "Gone"}
	exec := &stubSkillExecutor{err: errors.New("skill not found: gone")}
	cmd := NewSkillCommand(info, exec)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	// 错误不再走 result.Text（paraSystem），改为走 SideEffectInvokeSkill（paraTool 错误态）
	if result.Text != "" {
		t.Errorf("Text should be empty for error case, got: %s", result.Text)
	}
	if len(result.SideEffects) != 1 {
		t.Fatalf("expected 1 side effect, got %d", len(result.SideEffects))
	}
	se := result.SideEffects[0]
	if se.Kind != SideEffectInvokeSkill {
		t.Errorf("kind = %q, want %q", se.Kind, SideEffectInvokeSkill)
	}
	if se.Detail != "" {
		t.Errorf("Detail should be empty for error case, got: %s", se.Detail)
	}
	if se.Detail2 != "gone" {
		t.Errorf("Detail2 (skill name) = %q, want %q", se.Detail2, "gone")
	}
	if se.Detail4 == "" {
		t.Error("Detail4 (error message) should not be empty")
	}
}
