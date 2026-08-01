package agentloop

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/sandbox"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// sandboxCaptureTool 记录执行时 ctx 中的 per-command 沙箱状态。
// 用于验证 executeToolCalls 完整链路中 withSandboxStatus 注入不被 ctx 重建覆盖。
type sandboxCaptureTool struct {
	mu     sync.Mutex
	status sandbox.SandboxStatus
	called bool
	name   string // 工具名(默认 "bash";子代理场景 "bash_subagent")
}

func (t *sandboxCaptureTool) Name() string {
	if t.name != "" {
		return t.name
	}
	return "bash" // 走串行组(非 ConcurrentSafe)
}
func (t *sandboxCaptureTool) Description() string     { return "test capture tool" }
func (t *sandboxCaptureTool) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (t *sandboxCaptureTool) ConcurrentSafe() bool    { return false }
func (t *sandboxCaptureTool) Execute(ctx context.Context, raw json.RawMessage) (*tool.ToolResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.called = true
	t.status = sandbox.SandboxStatusFrom(ctx)
	return &tool.ToolResult{Content: "ok"}, nil
}

func (t *sandboxCaptureTool) statusSnapshot() (sandbox.SandboxStatus, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status, t.called
}

// TestRegression_SandboxStatusSurvivesTimeoutCtx 回归防护:
// executeToolCalls 中 context.WithTimeout 必须从 withSandboxStatus 的 execCtx 派生,
// 否则 per-command 沙箱状态被原始 ctx 重建覆盖丢失(bash 超时恒 > 0 → 沙箱包装永不生效)。
func TestRegression_SandboxStatusSurvivesTimeoutCtx(t *testing.T) {
	registry := tool.NewRegistry()
	capture := &sandboxCaptureTool{}
	registry.Register(tool.Wrap(capture))

	// 可用沙箱管理器 + fake backend
	mgr := sandbox.NewManager(sandbox.DefaultConfig(), "/tmp")
	mgr.SetBackend(&fakeSandboxBackend{})
	loop := New(&mockLLMClient{}, registry, Config{
		SandboxMgr:   mgr,
		ToolTimeout:  5 * 1000 * 1000 * 1000, // 5s,确保走 WithTimeout 分支
		UserResponder: nil,
	})

	state := &LoopState{}
	ch := make(chan TurnEvent, 16)

	msgs, termReason, execErr := loop.executeToolCalls(context.Background(), []llm.ToolCall{
		{ID: "tc1", Name: "bash", Arguments: `{"command": "ls"}`},
	}, state, ch)

	if execErr != nil {
		t.Fatalf("executeToolCalls error: %v", execErr)
	}
	if termReason != "" && termReason != ReasonAborted {
		t.Errorf("unexpected term reason: %s", termReason)
	}
	if len(msgs) == 0 {
		t.Fatal("no tool messages produced")
	}

	status, called := capture.statusSnapshot()
	if !called {
		t.Fatal("capture tool not called")
	}
	if !status.Active {
		t.Errorf("sandbox status lost in execution chain: %+v", status)
	}
}

// TestRegression_BashSubagentGetsSandbox 回归防护(二审 High-1):
// 子代理工具名为 "bash_subagent"(Shell.AllowBg=false),此前 withSandboxStatus
// 名单遗漏 → 子代理 bash 永不进沙箱,而 subGuard autoAllow 全放行 → 组合裸奔。
func TestRegression_BashSubagentGetsSandbox(t *testing.T) {
	registry := tool.NewRegistry()
	capture := &sandboxCaptureTool{name: "bash_subagent"}
	registry.Register(tool.Wrap(capture))

	mgr := sandbox.NewManager(sandbox.DefaultConfig(), "/tmp")
	mgr.SetBackend(&fakeSandboxBackend{})
	loop := New(&mockLLMClient{}, registry, Config{
		SandboxMgr:  mgr,
		ToolTimeout: 5 * 1000 * 1000 * 1000,
	})
	state := &LoopState{}
	ch := make(chan TurnEvent, 16)

	_, _, execErr := loop.executeToolCalls(context.Background(), []llm.ToolCall{
		{ID: "tc1", Name: "bash_subagent", Arguments: `{"command": "ls"}`},
	}, state, ch)
	if execErr != nil {
		t.Fatalf("executeToolCalls error: %v", execErr)
	}

	status, called := capture.statusSnapshot()
	if !called {
		t.Fatal("capture tool not called")
	}
	if !status.Active {
		t.Errorf("bash_subagent should be sandboxed (High-1 regression): %+v", status)
	}
}
