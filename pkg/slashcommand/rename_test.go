package slashcommand

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRenamer 是 SessionRenamer 的测试替身。
type fakeRenamer struct {
	current    string
	renameErr  error
	renamedTo  string
	renameCalls int
}

func (f *fakeRenamer) RenameSession(name string) error {
	f.renameCalls++
	f.renamedTo = name
	if f.renameErr == nil {
		// 模拟 renamer 内部规范化(与 SetSessionName 一致):TrimSpace + 截断 10 rune
		runes := []rune(strings.TrimSpace(name))
		if len(runes) > 10 {
			runes = runes[:10]
		}
		f.current = string(runes)
	}
	return f.renameErr
}

func (f *fakeRenamer) CurrentName() string {
	return f.current
}

func testRenameMessages() *SlashMessages {
	return &SlashMessages{
		RenameDescription: "Rename current session",
		RenameDone:        "Session renamed to: %s",
		RenameFailed:      "Rename failed: %v",
		RenameCurrent:     "Current session name: %s",
		RenameUnnamed:     "Session is unnamed.",
	}
}

// TestRenameCommand_WithArgs 验证 /rename <name> 调用 renamer 并返回成功文案。
func TestRenameCommand_WithArgs(t *testing.T) {
	renamer := &fakeRenamer{}
	cmd := NewRenameCommand(renamer, testRenameMessages())

	result, err := cmd.Execute(context.Background(), "  修复登录 bug  ")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if renamer.renameCalls != 1 {
		t.Fatalf("RenameSession calls = %d, want 1", renamer.renameCalls)
	}
	if renamer.renamedTo != "修复登录 bug" {
		t.Errorf("renamedTo = %q, want %q(TrimSpace 后)", renamer.renamedTo, "修复登录 bug")
	}
	if result.Text != "Session renamed to: 修复登录 bug" {
		t.Errorf("Text = %q, want %q", result.Text, "Session renamed to: 修复登录 bug")
	}
}

// TestRenameCommand_DoneUsesNormalizedName 验证成功文案显示规范化后的名称
// (remaner 可能截断/过滤控制字符,显示必须与落盘一致)。
func TestRenameCommand_DoneUsesNormalizedName(t *testing.T) {
	renamer := &fakeRenamer{}
	cmd := NewRenameCommand(renamer, testRenameMessages())

	// 超长名称:fakeRenamer 截断到 10 rune,文案应显示截断后的值
	result, err := cmd.Execute(context.Background(), "这是一个非常长的会话名称超过十个字")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "Session renamed to: " + renamer.current
	if result.Text != want {
		t.Errorf("Text = %q, want %q(使用规范化后名称)", result.Text, want)
	}
}

// TestRenameCommand_NoArgs_Unnamed 验证无参且未命名时提示 RenameUnnamed。
func TestRenameCommand_NoArgs_Unnamed(t *testing.T) {
	renamer := &fakeRenamer{}
	cmd := NewRenameCommand(renamer, testRenameMessages())

	result, err := cmd.Execute(context.Background(), "   ")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if renamer.renameCalls != 0 {
		t.Fatalf("RenameSession calls = %d, want 0", renamer.renameCalls)
	}
	if result.Text != "Session is unnamed." {
		t.Errorf("Text = %q, want %q", result.Text, "Session is unnamed.")
	}
}

// TestRenameCommand_NoArgs_Named 验证无参且已命名时显示当前名称。
func TestRenameCommand_NoArgs_Named(t *testing.T) {
	renamer := &fakeRenamer{current: "重构 TUI"}
	cmd := NewRenameCommand(renamer, testRenameMessages())

	result, err := cmd.Execute(context.Background(), "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if renamer.renameCalls != 0 {
		t.Fatalf("RenameSession calls = %d, want 0", renamer.renameCalls)
	}
	if result.Text != "Current session name: 重构 TUI" {
		t.Errorf("Text = %q, want %q", result.Text, "Current session name: 重构 TUI")
	}
}

// TestRenameCommand_RenameFailed 验证 renamer 返回错误时透传失败文案。
func TestRenameCommand_RenameFailed(t *testing.T) {
	renamer := &fakeRenamer{renameErr: errors.New("disk full")}
	cmd := NewRenameCommand(renamer, testRenameMessages())

	result, err := cmd.Execute(context.Background(), "新名字")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if renamer.renameCalls != 1 {
		t.Fatalf("RenameSession calls = %d, want 1", renamer.renameCalls)
	}
	if result.Text != "Rename failed: disk full" {
		t.Errorf("Text = %q, want %q", result.Text, "Rename failed: disk full")
	}
}
