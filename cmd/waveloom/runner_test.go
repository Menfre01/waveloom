package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// TestRegression_OneShotNoInteractiveTools 验证 oneshot 入口不注册交互式工具:
// registerBuiltinTools(interactive=false) 的注册集不含 ask_user_question /
// enter_plan_mode / exit_plan_mode(依赖 UserResponder,oneshot 无交互下调用
// 必挂),从 schema 层杜绝 LLM 提议不可用工具;TUI(interactive=true)保留。
func TestRegression_OneShotNoInteractiveTools(t *testing.T) {
	dir := t.TempDir()
	interactiveTools := []string{"ask_user_question", "enter_plan_mode", "exit_plan_mode"}

	// oneshot 模式(interactive=false)
	oneshotRegistry := tool.NewRegistry()
	registerBuiltinTools(oneshotRegistry, nil, &mockLLMClient{}, "test-model", "test-sub", dir, nil, nil, permission.NewGuard(), compaction.CompactionConfig{}, false)
	for _, name := range interactiveTools {
		if _, ok := oneshotRegistry.Get(name); ok {
			t.Errorf("oneshot registry: %q should NOT be registered (interactive tool, no UserResponder)", name)
		}
	}
	// 非交互工具两个模式都必须有
	for _, name := range []string{"read", "write", "edit", "bash", "agent", "todo_create", "todo_update", "kill_background_task"} {
		if _, ok := oneshotRegistry.Get(name); !ok {
			t.Errorf("oneshot registry: missing non-interactive tool %q", name)
		}
	}

	// TUI 模式(interactive=true):交互工具保留
	tuiRegistry := tool.NewRegistry()
	registerBuiltinTools(tuiRegistry, nil, &mockLLMClient{}, "test-model", "test-sub", dir, nil, nil, permission.NewGuard(), compaction.CompactionConfig{}, true)
	for _, name := range interactiveTools {
		if _, ok := tuiRegistry.Get(name); !ok {
			t.Errorf("TUI registry: expected %q registered", name)
		}
	}
}

// TestRegression_OneShotAutoAllowUnconditional 验证 oneshot 无条件注入
// autoAllow 二元决策(不依赖 --bypass-permissions flag,对齐 ACP):
// ask 规则 → ALLOW(无交互下 ask 无处安放);deny 规则仍 DENY(fail-closed)。
// REGRESSION 根因:此前仅 cfg.BypassPerm 时注入,普通 one-shot 管道运行
// ask 降级 deny,浪费一轮且行为与 ACP 不一致。
func TestRegression_OneShotAutoAllowUnconditional(t *testing.T) {
	dir := t.TempDir()
	guard := permission.NewGuard(
		permission.WithWorkingDirs(dir),
		permission.WithRules([]permission.RuleEntry{
			{Rule: permission.Rule{Behavior: permission.RuleAsk, ToolName: "write"}, Source: permission.SourceConfig, Scope: permission.ScopeConfig},
			{Rule: permission.Rule{Behavior: permission.RuleDeny, ToolName: "bash"}, Source: permission.SourceConfig, Scope: permission.ScopeConfig},
		}),
	)

	// 与 runOneShot 相同的注入点(无条件,无 BypassPerm 前置条件)
	enableOneShotBinaryDecision(guard)

	res := guard.Check(context.Background(), "write",
		json.RawMessage(`{"file_path":"`+filepath.Join(dir, "notes.txt")+`"}`))
	if res.Decision != permission.DecisionAllow {
		t.Errorf("oneshot + ask rule: Decision = %s, want %s (oneshot 无交互,ask 应二元化为 ALLOW)", res.Decision, permission.DecisionAllow)
	}

	res = guard.Check(context.Background(), "bash", json.RawMessage(`{"command":"ls"}`))
	if res.Decision != permission.DecisionDeny {
		t.Errorf("oneshot + deny rule: Decision = %s, want %s", res.Decision, permission.DecisionDeny)
	}
}
