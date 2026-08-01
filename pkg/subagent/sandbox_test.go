package subagent

import (
	"context"
	"testing"

	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/sandbox"
)

// subSandboxBackend 实现 sandbox.Backend,供 subagent 测试注入。
type subSandboxBackend struct{}

func (f *subSandboxBackend) Name() string { return "fake" }
func (f *subSandboxBackend) Probe() error { return nil }
func (f *subSandboxBackend) Transform(shellBin string, args []string, cfg *sandbox.Config, workspace string) ([]string, error) {
	return append([]string{"bwrap"}, args...), nil
}

// TestSubGuard_SandboxAvailable_AutoAllow 验证:沙箱可用 → autoAllow 二元决策。
// 二元决策不依赖 per-command 沙箱标志:逃逸命令(ctx inactive)同样 ALLOW。
func TestSubGuard_SandboxAvailable_AutoAllow(t *testing.T) {
	mgr := sandbox.NewManager(sandbox.DefaultConfig(), "/tmp")
	mgr.SetBackend(&subSandboxBackend{})
	a := &AgentTool{SandboxMgr: mgr}
	g := a.subGuard()

	// 沙箱内命令:默认策略 → ALLOW
	sandboxedCtx := sandbox.WithSandboxStatus(context.Background(), sandbox.SandboxStatus{Active: true})
	result := g.Check(sandboxedCtx, "bash", []byte(`{"command": "git status"}`))
	if result.Decision != permission.DecisionAllow {
		t.Errorf("sandboxed: Decision = %s, want allow", result.Decision)
	}

	// 逃逸命令(ctx inactive):同样 ALLOW(binaryDecision 不依赖 ctx)
	escapedCtx := sandbox.WithSandboxStatus(context.Background(), sandbox.SandboxStatus{Active: false, Reason: "excluded"})
	result = g.Check(escapedCtx, "bash", []byte(`{"command": "git status"}`))
	if result.Decision != permission.DecisionAllow {
		t.Errorf("escaped: Decision = %s, want allow (binary decision)", result.Decision)
	}
}

// TestSubGuard_NoSandbox_Bypass 验证:沙箱不可用 → 维持原 bypass 语义。
func TestSubGuard_NoSandbox_Bypass(t *testing.T) {
	a := &AgentTool{}
	g := a.subGuard()

	// bypass:Step 6 短路默认策略 → ALLOW
	result := g.Check(context.Background(), "bash", []byte(`{"command": "git status"}`))
	if result.Decision != permission.DecisionAllow || result.Reason != permission.ReasonBypass {
		t.Errorf("no sandbox: Decision = %s (reason %s), want allow/bypass", result.Decision, result.Reason)
	}

	// RiskHigh 硬拦截在 bypass 下仍保留
	result = g.Check(context.Background(), "bash", []byte(`{"command": "rm -rf /"}`))
	if result.Decision != permission.DecisionDeny {
		t.Errorf("no sandbox + high risk: Decision = %s, want deny", result.Decision)
	}
}

// TestSubAgentAllTools_ShellInjected 验证:子代理工具列表包含 bash_subagent
// (SandboxMgr 注入由 allTools 构造保证,与主 agent 的 Shell 同一路径)。
func TestSubAgentAllTools_ShellInjected(t *testing.T) {
	mgr := sandbox.NewManager(sandbox.DefaultConfig(), "/tmp")
	mgr.SetBackend(&subSandboxBackend{})
	a := &AgentTool{SandboxMgr: mgr}

	found := false
	for _, tl := range a.allTools() {
		if tl.Name() == "bash_subagent" {
			found = true
		}
	}
	if !found {
		t.Error("allTools missing bash_subagent")
	}
}

// TestSubGuard_InheritsParentRules 回归防护(三审 High-1):
// 子代理 Guard 必须继承父级 deny 规则——此前每次新建空规则 Guard,
// 用户 deny 规则对子代理完全失效(叠加 autoAllow = 三重裸奔)。
func TestSubGuard_InheritsParentRules(t *testing.T) {
	parent := permission.NewGuard(
		permission.WithRules([]permission.RuleEntry{
			{Rule: permission.Rule{Behavior: permission.RuleDeny, ToolName: "bash"}, Source: permission.SourceConfig, Scope: permission.ScopeConfig},
		}),
	)
	a := &AgentTool{Guard: parent}
	g := a.subGuard()

	// 父 deny 规则在子代理生效(二元决策下 deny 是唯一硬闸)
	result := g.Check(context.Background(), "bash", []byte(`{"command": "ls"}`))
	if result.Decision != permission.DecisionDeny {
		t.Errorf("subGuard should inherit parent deny rule: %s", result.Decision)
	}

	// 无父 Guard → 不 panic,空规则
	a2 := &AgentTool{}
	g2 := a2.subGuard()
	result = g2.Check(context.Background(), "bash", []byte(`{"command": "ls"}`))
	if result.Decision != permission.DecisionAllow {
		t.Errorf("empty subGuard should allow: %s", result.Decision)
	}
}
