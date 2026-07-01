package slashcommand

import (
	"context"
	"testing"
)

func TestLocaleCommand(t *testing.T) {
	msg := testMessagesZhCN()
	cmd := NewLocaleCommand(msg)
	if cmd.Name() != "locale" {
		t.Errorf("Name = %q, want locale", cmd.Name())
	}
	if cmd.Description() == "" {
		t.Error("Description should not be empty")
	}
	if cmd.ArgsPlaceholder() != "" {
		t.Errorf("ArgsPlaceholder = %q, want empty", cmd.ArgsPlaceholder())
	}
	aliases := cmd.Aliases()
	if len(aliases) != 1 || aliases[0] != "lang" {
		t.Errorf("Aliases = %v, want [lang]", aliases)
	}

	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.SideEffects) != 1 || result.SideEffects[0].Kind != SideEffectOpenLocalePicker {
		t.Errorf("expected SideEffectOpenLocalePicker, got %+v", result.SideEffects)
	}
	if result.Text != "" {
		t.Errorf("Text should be empty, got %q", result.Text)
	}
}
