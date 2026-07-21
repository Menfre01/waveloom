package slashcommand

import (
	"context"
	"testing"
)

// stubCommand is a minimal Command implementation for testing.
type stubCommand struct {
	name        string
	description string
	aliases     []string
}

func (s *stubCommand) Name() string                         { return s.name }
func (s *stubCommand) Description() string                  { return s.description }
func (s *stubCommand) ArgsPlaceholder() string              { return "" }
func (s *stubCommand) Aliases() []string                    { return s.aliases }
func (s *stubCommand) Execute(_ context.Context, _ string) (*Result, error) {
	return &Result{Text: s.name}, nil
}

func TestRegistryMatchExact(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubCommand{name: "new", description: "New session", aliases: []string{"clear"}})
	r.Register(&stubCommand{name: "help", description: "Show help"})

	tests := []struct {
		input    string
		wantName string
		wantArgs string
	}{
		{"/new", "new", ""},
		{"/NEW", "new", ""},
		{"/New", "new", ""},
		{"/new  something", "new", "something"},
		{"/help", "help", ""},
		{"/help  ", "help", ""},
	}

	for _, tt := range tests {
		cmd, args := r.Match(tt.input)
		if tt.wantName == "" {
			if cmd != nil {
				t.Errorf("Match(%q) = (%q, %q), want nil", tt.input, cmd.Name(), args)
			}
			continue
		}
		if cmd == nil {
			t.Errorf("Match(%q) returned nil, want %q", tt.input, tt.wantName)
			continue
		}
		if cmd.Name() != tt.wantName {
			t.Errorf("Match(%q) cmd.Name() = %q, want %q", tt.input, cmd.Name(), tt.wantName)
		}
		if args != tt.wantArgs {
			t.Errorf("Match(%q) args = %q, want %q", tt.input, args, tt.wantArgs)
		}
	}
}

func TestRegistryMatchAlias(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubCommand{name: "new", description: "New session", aliases: []string{"clear"}})

	cmd, _ := r.Match("/clear")
	if cmd == nil || cmd.Name() != "new" {
		t.Errorf("Match(/clear) = %v, want /new", cmd)
	}

	// 大小写不敏感
	cmd, _ = r.Match("/CLEAR")
	if cmd == nil || cmd.Name() != "new" {
		t.Errorf("Match(/CLEAR) = %v, want /new", cmd)
	}
}

func TestRegistryMatchNoMatch(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubCommand{name: "help", description: "Show help"})

	cmd, args := r.Match("/unknown")
	if cmd != nil {
		t.Errorf("Match(/unknown) = (%q, %q), want nil", cmd.Name(), args)
	}

	cmd, _ = r.Match("/")
	if cmd != nil {
		t.Errorf("Match(/) = %v, want nil", cmd)
	}

	cmd, _ = r.Match("not a slash")
	if cmd != nil {
		t.Errorf("Match(not a slash) = %v, want nil", cmd)
	}
}

func TestRegistryList(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubCommand{name: "help", description: "Show help"})
	r.Register(&stubCommand{name: "new", description: "New session", aliases: []string{"clear"}})
	r.Register(&stubCommand{name: "model", description: "Switch model"})

	infos := r.List()
	if len(infos) != 3 {
		t.Fatalf("len(List()) = %d, want 3", len(infos))
	}

	// 按名称排序
	if infos[0].Name != "help" || infos[1].Name != "model" || infos[2].Name != "new" {
		t.Errorf("List() order = %v, want [help model new]", []string{infos[0].Name, infos[1].Name, infos[2].Name})
	}

	// new 的别名
	if len(infos[2].Aliases) != 1 || infos[2].Aliases[0] != "clear" {
		t.Errorf("new aliases = %v, want [clear]", infos[2].Aliases)
	}
}

func TestRegistryDuplicateCommandPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for duplicate command name")
		}
	}()
	r := NewRegistry()
	r.Register(&stubCommand{name: "new", description: "first"})
	r.Register(&stubCommand{name: "new", description: "second"})
}

func TestRegistryDuplicateAliasPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for duplicate alias")
		}
	}()
	r := NewRegistry()
	r.Register(&stubCommand{name: "new", description: "New session", aliases: []string{"clear"}})
	r.Register(&stubCommand{name: "reset", description: "Reset", aliases: []string{"clear"}})
}

// TestRegistryHasCommand verifies HasCommand returns correct existence check.
func TestRegistryHasCommand(t *testing.T) {
	r := NewRegistry()
	r.Register(&stubCommand{name: "help", description: "Show help"})

	if !r.HasCommand("help") {
		t.Error("HasCommand(\"help\") = false, want true")
	}
	if !r.HasCommand("HELP") {
		t.Error("HasCommand(\"HELP\") = false, want true (case-insensitive)")
	}
	if r.HasCommand("unknown") {
		t.Error("HasCommand(\"unknown\") = true, want false")
	}
}

// TestRegistryNoPanicWhenSkillCollisionChecked demonstrates the expected pattern:
// callers should use HasCommand before Register to avoid panic on skill/built-in collision.
// REGRESSION: 用户环境存在一个名为 "help" 的 skill/plugin command，
// newSlashRegistry 先注册内置 /help 再遍历 skill 注册，导致 Register panic。
// 修复：注册前用 HasCommand 检测冲突，跳过同名 skill。
func TestRegistryNoPanicWhenSkillCollisionChecked(t *testing.T) {
	r := NewRegistry()
	// 注册内置命令（模拟 newSlashRegistry 中的操作）
	r.Register(&stubCommand{name: "help", description: "Show help"})
	r.Register(&stubCommand{name: "new", description: "New session"})
	r.Register(&stubCommand{name: "model", description: "Switch model"})

	// 模拟 skill 注册：先检查 HasCommand，冲突时跳过
	skillNames := []string{"help", "my-skill", "new", "another-skill", "model"}
	registered := 0
	skipped := 0
	for _, name := range skillNames {
		if r.HasCommand(name) {
			skipped++
			continue
		}
		r.Register(&stubCommand{name: name, description: "skill: " + name})
		registered++
	}

	if skipped != 3 {
		t.Errorf("skipped = %d, want 3 (help, new, model)", skipped)
	}
	if registered != 2 {
		t.Errorf("registered = %d, want 2 (my-skill, another-skill)", registered)
	}

	// 验证各命令都在注册表中
	if !r.HasCommand("help") {
		t.Error("built-in help should still be registered")
	}
	if !r.HasCommand("my-skill") {
		t.Error("my-skill should be registered")
	}
	if !r.HasCommand("another-skill") {
		t.Error("another-skill should be registered")
	}

	// 验证总数：5 个内置 + 2 个 skill = 7
	infos := r.List()
	if len(infos) != 5 {
		t.Fatalf("len(List()) = %d, want 5 (builtins + non-colliding skills)", len(infos))
	}
}
