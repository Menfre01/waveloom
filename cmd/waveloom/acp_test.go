package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/compaction"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// mockLLMClient 是最小 mock,工具注册测试不需要真实 LLM 调用。
type mockLLMClient struct{}

// mockModelLister 提供固定模型列表供 /model 命令校验。
type mockModelLister struct{}

func (m *mockModelLister) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{
		{ID: "deepseek-chat"},
		{ID: "deepseek-r1"},
	}, nil
}

func (m *mockLLMClient) SendMessage(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (*llm.Response, error) {
	return &llm.Response{Content: "mock response"}, nil
}
func (m *mockLLMClient) SendMessageStream(ctx context.Context, messages []llm.Message, tools []llm.ToolSpec) (<-chan llm.StreamingEvent, error) {
	panic("not implemented in mock")
}
func (m *mockLLMClient) GetBalance(ctx context.Context) (*llm.BalanceInfo, error) {
	return nil, nil
}
func (m *mockLLMClient) SupportsBalance() bool { return false }
func (m *mockLLMClient) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	return nil, nil
}

// TestRegression_ACPAutoAllowBinaryDecision 验证 ACP 入口权限接线
// (createGuard + EnableAutoAllow,与 runACP 相同的注入顺序):
// write/execute 工具默认 ALLOW(二元决策,不产生 ASK——ACP v1 无权限确认协议);
// 高危命令硬拦截与显式 deny 规则仍 DENY(fail-closed 底线,规格书不变量 #2/#9)。
func TestRegression_ACPAutoAllowBinaryDecision(t *testing.T) {
	guard := createGuard("", "")
	impl, ok := guard.(*permission.GuardImpl)
	if !ok {
		t.Fatal("createGuard should return *permission.GuardImpl")
	}
	impl.EnableAutoAllow()

	cases := []struct {
		name  string
		tool  string
		input string
		want  permission.Decision
	}{
		{"write 默认放行(二元决策)", "write", `{"file_path":"test.txt"}`, permission.DecisionAllow},
		{"bash 默认放行(二元决策)", "bash", `{"command":"ls"}`, permission.DecisionAllow},
		{"bash 高危命令硬拦截", "bash", `{"command":"rm -rf /"}`, permission.DecisionDeny},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			res := guard.Check(context.Background(), tt.tool, json.RawMessage(tt.input))
			if res.Decision != tt.want {
				t.Errorf("Check(%s) = %s, want %s (msg: %s)", tt.tool, res.Decision, tt.want, res.Message)
			}
		})
	}

	// 显式 deny 规则在二元决策下仍拦截(Step 1 优先级最高)
	denyGuard := permission.NewGuard(
		permission.WithRules([]permission.RuleEntry{
			{Rule: permission.Rule{Behavior: permission.RuleDeny, ToolName: "bash"}, Source: permission.SourceConfig, Scope: permission.ScopeConfig},
		}),
	)
	denyGuard.EnableAutoAllow()
	res := denyGuard.Check(context.Background(), "bash", json.RawMessage(`{"command":"ls"}`))
	if res.Decision != permission.DecisionDeny {
		t.Errorf("deny rule under autoAllow: Decision = %s, want %s", res.Decision, permission.DecisionDeny)
	}
}

// TestRegression_ACPNoInteractiveTools 验证 ACP 工具 schema 对齐:
// 依赖 UserResponder 的交互式工具(ACP 无交互,调用必挂)必须不注册,
// 从 schema 层杜绝 LLM 提议不可用工具;可用工具必须正常注册。
func TestRegression_ACPNoInteractiveTools(t *testing.T) {
	registry := tool.NewRegistry()
	guard := permission.NewGuard(permission.WithBypassMode(false))
	registerACPBuiltinTools(registry, nil, &mockLLMClient{}, "test-model", "test-sub", t.TempDir(), nil, nil, guard, compaction.CompactionConfig{})

	// 交互式工具(execute.go 走 UserInteractionTool 分支 → UserResponder nil → 必挂)
	for _, name := range []string{"ask_user_question", "enter_plan_mode", "exit_plan_mode"} {
		if _, ok := registry.Get(name); ok {
			t.Errorf("interactive tool %q must NOT be registered in ACP mode", name)
		}
	}

	// 可用工具(二元决策下正常执行)
	for _, name := range []string{
		"read", "edit", "write", "bash",
		"web_fetch", "web_search", "kill_background_task",
		"agent", "todo_create", "todo_update",
	} {
		if _, ok := registry.Get(name); !ok {
			t.Errorf("tool %q should be registered in ACP mode", name)
		}
	}
}

// TestResolveContextLimit:窗口容量优先级 flag > settings(compaction.context_limit_tokens,
// setup 向导写入)> 默认 1M。
func TestResolveContextLimit(t *testing.T) {
	// 1. flag 优先
	if got := resolveContextLimit(200_000, "", ""); got != 200_000 {
		t.Errorf("flag override = %d, want 200000", got)
	}

	// 2. settings 生效(setup 写入 context_limit_tokens)
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"compaction":{"context_limit_tokens":"128K"}}`), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	if got := resolveContextLimit(0, "", settingsPath); got != 128_000 {
		t.Errorf("settings value = %d, want 128000", got)
	}

	// 3. project 覆盖 global
	globalPath := filepath.Join(dir, "global.json")
	if err := os.WriteFile(globalPath, []byte(`{"compaction":{"context_limit_tokens":"64K"}}`), 0o644); err != nil {
		t.Fatalf("write global settings: %v", err)
	}
	if got := resolveContextLimit(0, globalPath, settingsPath); got != 128_000 {
		t.Errorf("project should override global: got %d, want 128000", got)
	}

	// 4. 均未配置 → 默认 1M
	if got := resolveContextLimit(0, "", ""); got != 1_000_000 {
		t.Errorf("default = %d, want 1000000", got)
	}
}

func TestACPCommandRunner(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	store := &acpSettingsStore{projectPath: settingsPath, globalPath: ""}

	registry := tool.NewRegistry()
	registry.Register(tool.Wrap(&tool.ReadFile{}))

	// 无 skill loader:仅内置命令(lister 提供可用模型供 /model 校验)
	runner := newACPCommandRunner(registry, store, &mockModelLister{}, "deepseek-chat", nil, LocaleZhCN)

	// 1. 可用命令列表:含 help/model/provider,不含 TUI overlay 命令
	cmds := runner.AvailableCommands()
	names := map[string]bool{}
	for _, c := range cmds {
		names[c.Name] = true
	}
	for _, want := range []string{"help", "model", "provider"} {
		if !names[want] {
			t.Errorf("available commands should include %q, got %v", want, names)
		}
	}
	for _, notWant := range []string{"theme", "locale", "rewind", "new"} {
		if names[notWant] {
			t.Errorf("TUI overlay command %q must NOT be registered in ACP", notWant)
		}
	}

	// 2. /help → 文本结果
	text, injected, handled := runner.Run(context.Background(), "/help")
	if !handled || injected != "" || text == "" {
		t.Errorf("/help: handled=%v text=%q injected=%q", handled, text, injected)
	}

	// 3. 未知命令 → 不拦截(走 LLM)
	if _, _, handled := runner.Run(context.Background(), "normal question"); handled {
		t.Error("normal text must not be handled as command")
	}

	// 4. /model <name> → 切换模型(settings 写入)
	text, _, handled = runner.Run(context.Background(), "/model deepseek-r1")
	if !handled {
		t.Fatal("/model should be handled")
	}
	if !strings.Contains(text, "deepseek-r1") {
		t.Errorf("/model result = %q, want mention deepseek-r1", text)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil || !strings.Contains(string(data), "deepseek-r1") {
		t.Errorf("settings should persist new model, data=%s err=%v", data, err)
	}
}
