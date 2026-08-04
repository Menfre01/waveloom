package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/session"
)

// TestRegression_TUIBypassBinaryDecision 验证 TUI --bypass-permissions 接线:
// wireLoop 除 EnableBypass 外必须注入 EnableAutoAllow → 二元决策
// (仅 DENY/ALLOW,不产生 ASK 弹窗)。
// REGRESSION 根因:此前 TUI bypass 仅 EnableBypass,Step 2 ask 规则 / Step 3
// 安全 ASK 仍返回 ASK,TUI 继续弹权限确认面板;2025-09 决策:TUI 与
// one-shot/ACP 入口一致,bypass 即二元决策,不再 ask。
func TestRegression_TUIBypassBinaryDecision(t *testing.T) {
	dir := t.TempDir()
	rules := []permission.RuleEntry{
		{Rule: permission.Rule{Behavior: permission.RuleAsk, ToolName: "write"}, Source: permission.SourceConfig, Scope: permission.ScopeConfig},
		{Rule: permission.Rule{Behavior: permission.RuleDeny, ToolName: "bash"}, Source: permission.SourceConfig, Scope: permission.ScopeConfig},
	}
	guard := permission.NewGuard(permission.WithWorkingDirs(dir), permission.WithRules(rules))

	// 与 runTUI 相同的接线:构造 model 并真实调用 wireLoop(覆盖 bypass 注入)。
	m := &model{
		llmClient:  &mockLLMClient{},
		guard:      guard,
		cm:         session.New(""),
		bypassPerm: true,
		hudModel:   "test-model",
		cwd:        dir,
	}
	m.wireLoop()
	if m.loop == nil {
		t.Fatal("wireLoop: expected non-nil loop")
	}

	// ask 规则命中 → ALLOW(二元决策;回归前为 ASK → TUI 弹权限面板)。
	res := guard.Check(context.Background(), "write",
		json.RawMessage(`{"file_path":"`+filepath.Join(dir, "notes.txt")+`"}`))
	if res.Decision != permission.DecisionAllow {
		t.Errorf("TUI bypass + ask rule: Decision = %s, want %s (TUI 不应再产生 ASK 弹窗)", res.Decision, permission.DecisionAllow)
	}

	// 底线:deny 规则在二元决策下仍拦截(fail-closed)。
	res = guard.Check(context.Background(), "bash", json.RawMessage(`{"command":"ls"}`))
	if res.Decision != permission.DecisionDeny {
		t.Errorf("TUI bypass + deny rule: Decision = %s, want %s", res.Decision, permission.DecisionDeny)
	}

	// 对照:非 bypass 的 TUI 保持 ASK(回归护栏,防止二元决策污染正常模式)。
	normalGuard := permission.NewGuard(
		permission.WithWorkingDirs(dir),
		permission.WithRules([]permission.RuleEntry{
			{Rule: permission.Rule{Behavior: permission.RuleAsk, ToolName: "write"}, Source: permission.SourceConfig, Scope: permission.ScopeConfig},
		}),
	)
	res = normalGuard.Check(context.Background(), "write",
		json.RawMessage(`{"file_path":"`+filepath.Join(dir, "notes.txt")+`"}`))
	if res.Decision != permission.DecisionAsk {
		t.Errorf("normal TUI + ask rule: Decision = %s, want %s", res.Decision, permission.DecisionAsk)
	}
}
