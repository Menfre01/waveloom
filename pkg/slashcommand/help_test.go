package slashcommand

import (
	"context"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// NewHelpCommand
// ---------------------------------------------------------------------------

func TestNewHelpCommand(t *testing.T) {
	r := NewRegistry()
	msg := testMessagesZhCN()
	cmd := NewHelpCommand(r, msg)
	if cmd == nil {
		t.Fatal("NewHelpCommand should return non-nil")
	}
}

// ---------------------------------------------------------------------------
// HelpCommand.Name
// ---------------------------------------------------------------------------

func TestHelpCommand_Name(t *testing.T) {
	r := NewRegistry()
	msg := testMessagesZhCN()
	cmd := NewHelpCommand(r, msg)

	if got := cmd.Name(); got != "help" {
		t.Errorf("Name() = %q, want %q", got, "help")
	}
}

// ---------------------------------------------------------------------------
// HelpCommand.Description
// ---------------------------------------------------------------------------

func TestHelpCommand_Description(t *testing.T) {
	r := NewRegistry()
	msg := testMessagesZhCN()
	cmd := NewHelpCommand(r, msg)

	if got := cmd.Description(); got != msg.HelpDescription {
		t.Errorf("Description() = %q, want %q", got, msg.HelpDescription)
	}
}

// ---------------------------------------------------------------------------
// HelpCommand.ArgsPlaceholder
// ---------------------------------------------------------------------------

func TestHelpCommand_ArgsPlaceholder(t *testing.T) {
	r := NewRegistry()
	msg := testMessagesZhCN()
	cmd := NewHelpCommand(r, msg)

	if got := cmd.ArgsPlaceholder(); got != "" {
		t.Errorf("ArgsPlaceholder() = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// HelpCommand.Aliases
// ---------------------------------------------------------------------------

func TestHelpCommand_Aliases(t *testing.T) {
	r := NewRegistry()
	msg := testMessagesZhCN()
	cmd := NewHelpCommand(r, msg)

	aliases := cmd.Aliases()
	if aliases != nil {
		t.Errorf("Aliases() = %v, want nil", aliases)
	}
}

// ---------------------------------------------------------------------------
// HelpCommand.Execute — 验证 /help 输出内容
// ---------------------------------------------------------------------------

func TestHelpCommand_Execute_ContainsTips(t *testing.T) {
	r := NewRegistry()
	msg := testMessagesZhCN()

	// 注册多条命令后 /help 仍返回静态 HelpText（不含命令列表）
	r.Register(NewHelpCommand(r, msg))
	r.Register(NewThemeCommand(msg))
	r.Register(NewLocaleCommand(msg))
	r.Register(NewNewCommand(nil, msg))

	cmd := NewHelpCommand(r, msg)
	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// HelpText 是静态文案，包含使用技巧
	if !strings.Contains(result.Text, "使用技巧") {
		t.Error("help output should contain usage tips")
	}
}

func TestHelpCommand_Execute_EmptyRegistry(t *testing.T) {
	r := NewRegistry()
	msg := testMessagesZhCN()
	cmd := NewHelpCommand(r, msg)

	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Text == "" {
		t.Error("help output should not be empty even with empty registry")
	}
}
