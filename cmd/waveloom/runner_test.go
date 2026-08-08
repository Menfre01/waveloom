package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/reference"
	"github.com/Menfre01/waveloom/pkg/session"
	"github.com/Menfre01/waveloom/pkg/subagent"
	"github.com/Menfre01/waveloom/pkg/todo"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// runOneShotForTest 以最小依赖真实执行 runOneShot(mock LLM 一轮即回,
// 无 tool call → loop 正常结束),返回使用的 guard 供接线断言。
// 走真实入口而非直接调 helper——若 runOneShot 内的注入调用被删除,
// 接线测试必须失败(五审 High-2:此前测 helper 存在假阴性)。
func runOneShotForTest(t *testing.T, cfg CLIConfig, guard permission.Guard) {
	t.Helper()
	dir := t.TempDir()
	cm := session.New("")
	cm.SetSessionPath(filepath.Join(dir, "session.json"))
	runOneShot(
		cfg,
		&mockLLMClient{},
		tool.NewRegistry(),
		guard,
		nil, // sandboxMgr
		reference.New(guard),
		dir,
		cm,
		"", // agentsMdText
		LocaleEnUS,
		todo.NewTodoState(),
		"test-model",
		"", // planModel(proplan 锚点,测试不启用)
		"", // subModel(proplan 锚点,测试不启用)
		nil, // hookRunner
		&subagent.AgentTool{},
		nil, // mcpManager
		nil, // lspManager
	)
}

// newOneShotGuard 构造带 ask 规则的 guard(ask 命中是二元决策注入的判别信号:
// 注入前 → ASK,注入后 → ALLOW)。返回 guard 与其工作目录。
func newOneShotGuard(t *testing.T) (permission.Guard, string) {
	t.Helper()
	dir := t.TempDir()
	return permission.NewGuard(
		permission.WithWorkingDirs(dir),
		permission.WithRules([]permission.RuleEntry{
			{Rule: permission.Rule{Behavior: permission.RuleAsk, ToolName: "write"}, Source: permission.SourceConfig, Scope: permission.ScopeConfig},
			{Rule: permission.Rule{Behavior: permission.RuleDeny, ToolName: "bash"}, Source: permission.SourceConfig, Scope: permission.ScopeConfig},
		}),
	), dir
}

func oneShotAskRuleCheck(t *testing.T, guard permission.Guard, dir string) permission.Decision {
	t.Helper()
	res := guard.Check(context.Background(), "write",
		json.RawMessage(`{"file_path":"`+filepath.Join(dir, "notes.txt")+`"}`))
	return res.Decision
}

// TestRegression_OneShotAutoAllowWiring 验证 one-shot 终端/显式 bypass 场景的
// 接线:runOneShot 真实执行后 guard 进入二元决策(ask 规则 → ALLOW)。
// 若 runOneShot 内的 enableOneShotBinaryDecision 调用被删除,本测试必须失败。
func TestRegression_OneShotAutoAllowWiring(t *testing.T) {
	guard, dir := newOneShotGuard(t)
	cfg := CLIConfig{OneShot: "test task", BypassPerm: true, ToolTimeout: time.Minute}
	runOneShotForTest(t, cfg, guard)

	if d := oneShotAskRuleCheck(t, guard, dir); d != permission.DecisionAllow {
		t.Errorf("one-shot wiring (explicit bypass): Decision = %s, want %s (二元决策注入接线缺失)", d, permission.DecisionAllow)
	}
}

// TestRegression_OneShotPipedInputKeepsAsk 验证管道输入(stdin 非 tty)且未显式
// --bypass-permissions 时**不**注入二元决策:ask 规则保持 ASK(降级 deny 由
// execute.go 兜底),堵住"不可信管道内容 + 提示注入 → 任意写/执行"的敞口
// (五审 High-1)。go test 进程 stdin 非 tty,天然复现管道场景。
func TestRegression_OneShotPipedInputKeepsAsk(t *testing.T) {
	// go test 的 stdin 是 /dev/null(char device)→ isPiped() 恒 false;
	// 显式替换为 pipe 复现管道输入场景(读端无数据,关闭写端后 readStdin 返回空)。
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = pr
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = pr.Close()
	})
	_ = pw.Close()

	guard, dir := newOneShotGuard(t)
	cfg := CLIConfig{OneShot: "test task", ToolTimeout: time.Minute} // BypassPerm=false
	runOneShotForTest(t, cfg, guard)

	if d := oneShotAskRuleCheck(t, guard, dir); d != permission.DecisionAsk {
		t.Errorf("one-shot piped input: Decision = %s, want %s (管道输入必须保持 ASK→deny 降级)", d, permission.DecisionAsk)
	}
}

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
