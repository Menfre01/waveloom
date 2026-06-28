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

	cmd, args = r.Match("/")
	if cmd != nil {
		t.Errorf("Match(/) = %v, want nil", cmd)
	}

	cmd, args = r.Match("not a slash")
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
