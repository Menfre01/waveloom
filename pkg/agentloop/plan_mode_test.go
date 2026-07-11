package agentloop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Menfre01/waveloom/pkg/llm"
	"github.com/Menfre01/waveloom/pkg/permission"
	"github.com/Menfre01/waveloom/pkg/tool"
)

// ============================================================================
// Mock UserResponder with plan mode support
// ============================================================================

// mockPlanResponder 实现 permission.UserResponder，支持 plan 模式的交互模拟。
type mockPlanResponder struct {
	askChoices     map[string]permission.UserChoice
	questionResp   []permission.QuestionResponse
	questionErr    error
	enterPlanOK    bool
	enterPlanErr   error
	approvePlan    permission.PlanApproval
	approvePlanErr error
}

func (m *mockPlanResponder) AskUser(ctx context.Context, toolName string, input json.RawMessage, result permission.DecisionResult) permission.UserChoice {
	if m.askChoices != nil {
		if c, ok := m.askChoices[toolName]; ok {
			return c
		}
	}
	return permission.UserChoice{Decision: permission.DecisionDeny}
}

func (m *mockPlanResponder) AnswerQuestion(ctx context.Context, questions []permission.QuestionPrompt) ([]permission.QuestionResponse, error) {
	if m.questionErr != nil {
		return nil, m.questionErr
	}
	return m.questionResp, nil
}

func (m *mockPlanResponder) EnterPlan(ctx context.Context) (bool, error) {
	if m.enterPlanErr != nil {
		return false, m.enterPlanErr
	}
	return m.enterPlanOK, nil
}

func (m *mockPlanResponder) ApprovePlan(ctx context.Context, plan string) (permission.PlanApproval, error) {
	if m.approvePlanErr != nil {
		return permission.PlanApproval{}, m.approvePlanErr
	}
	return m.approvePlan, nil
}

// ============================================================================
// executeEnterPlanMode 单元测试
// ============================================================================

func TestExecuteEnterPlanMode_Success(t *testing.T) {
	l := New(nil, nil, Config{
		UserResponder: &mockPlanResponder{enterPlanOK: true},
		Guard:         &mockGuard{},
		PlanFile:      "/tmp/test-plan.md",
	})

	tc := llm.ToolCall{
		ID:        "call_enter_1",
		Name:      "enter_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeEnterPlanMode(context.Background(), tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError() {
		t.Fatalf("unexpected error result: %s", result.Error.Message)
	}
	if !l.plan {
		t.Error("expected plan mode to be enabled")
	}
	if l.planPairID == "" {
		t.Error("expected planPairID to be set")
	}

	// 消费事件
	go func() { for range ch {} }()
}

func TestExecuteEnterPlanMode_AlreadyInPlan(t *testing.T) {
	l := New(nil, nil, Config{
		UserResponder: &mockPlanResponder{enterPlanOK: true},
		PlanFile:      "/tmp/test-plan.md",
	})
	// 模拟已在 plan 模式
	l.plan = true
	l.planPairID = "abcd"

	tc := llm.ToolCall{
		ID:        "call_enter_2",
		Name:      "enter_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeEnterPlanMode(context.Background(), tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError() {
		t.Fatalf("unexpected error result: %s", result.Error.Message)
	}
	// planPairID 不应改变
	if l.planPairID != "abcd" {
		t.Errorf("expected planPairID to remain 'abcd', got %s", l.planPairID)
	}
	go func() { for range ch {} }()
}

func TestExecuteEnterPlanMode_NoResponder(t *testing.T) {
	l := New(nil, nil, Config{Guard: &mockGuard{}}) // UserResponder = nil

	tc := llm.ToolCall{
		ID:        "call_enter_3",
		Name:      "enter_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeEnterPlanMode(context.Background(), tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError() {
		t.Fatal("expected error when no UserResponder")
	}
	if result.Error.Kind != "user_declined" {
		t.Errorf("expected kind 'user_declined', got %q", result.Error.Kind)
	}
	go func() { for range ch {} }()
}

func TestExecuteEnterPlanMode_UserDeclined(t *testing.T) {
	l := New(nil, nil, Config{
		UserResponder: &mockPlanResponder{enterPlanOK: false},
		Guard:         &mockGuard{},
	})

	tc := llm.ToolCall{
		ID:        "call_enter_4",
		Name:      "enter_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeEnterPlanMode(context.Background(), tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError() {
		t.Fatal("expected error when user declines")
	}
	if result.Error.Kind != "user_declined" {
		t.Errorf("expected kind 'user_declined', got %q", result.Error.Kind)
	}
	go func() { for range ch {} }()
}

func TestExecuteEnterPlanMode_UserError(t *testing.T) {
	l := New(nil, nil, Config{
		UserResponder: &mockPlanResponder{enterPlanOK: false, enterPlanErr: context.Canceled},
		Guard:         &mockGuard{},
	})

	tc := llm.ToolCall{
		ID:        "call_enter_5",
		Name:      "enter_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeEnterPlanMode(context.Background(), tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError() {
		t.Fatal("expected error when EnterPlan returns error")
	}
	if result.Error.Kind != "user_declined" {
		t.Errorf("expected kind 'user_declined', got %q", result.Error.Kind)
	}
	go func() { for range ch {} }()
}

func TestExecuteEnterPlanMode_GeneratesPlanFile(t *testing.T) {
	l := New(nil, nil, Config{
		UserResponder: &mockPlanResponder{enterPlanOK: true},
		Guard:         &mockGuard{},
		// PlanFile 为空，应自动生成
	})

	tc := llm.ToolCall{
		ID:        "call_enter_6",
		Name:      "enter_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeEnterPlanMode(context.Background(), tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError() {
		t.Fatalf("unexpected error result: %s", result.Error.Message)
	}
	if l.config.PlanFile == "" {
		t.Error("expected PlanFile to be auto-generated")
	}
	// 验证文件在 ~/.waveloom/plans/ 下
	homeDir, _ := os.UserHomeDir()
	plansDir := filepath.Join(homeDir, ".waveloom", "plans")
	if !strings.HasPrefix(l.config.PlanFile, plansDir) {
		t.Errorf("expected plan file under %s, got %s", plansDir, l.config.PlanFile)
	}
	go func() { for range ch {} }()
}

func TestExecuteEnterPlanMode_InjectsStartMessage(t *testing.T) {
	l := New(nil, nil, Config{
		UserResponder: &mockPlanResponder{enterPlanOK: true},
		Guard:         &mockGuard{},
		PlanFile:      "/tmp/plan-test.md",
	})

	state := &LoopState{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "help me plan"},
		},
	}

	tc := llm.ToolCall{
		ID:        "call_enter_7",
		Name:      "enter_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeEnterPlanMode(context.Background(), tc, state, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError() {
		t.Fatalf("unexpected error result: %s", result.Error.Message)
	}
	// [plan:start] 不再由 executeEnterPlanMode 直接注入 state.Messages，
	// 改为 executeToolCalls 在 tool 消息之后统一注入（保证消息顺序正确）。
	// 此处仅验证 executeEnterPlanMode 本身的状态切换（l.plan / l.planPairID）。
	if !l.plan {
		t.Error("expected plan mode to be enabled")
	}
	if l.planPairID == "" {
		t.Error("expected planPairID to be set")
	}
	go func() { for range ch {} }()
}

// ============================================================================
// executeExitPlanMode 单元测试
// ============================================================================

func TestExecuteExitPlanMode_NotInPlanMode(t *testing.T) {
	l := New(nil, nil, Config{})
	l.plan = false

	tc := llm.ToolCall{
		ID:        "call_exit_1",
		Name:      "exit_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeExitPlanMode(context.Background(), tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError() {
		t.Fatal("expected error when not in plan mode")
	}
	if result.Error.Kind != "invalid_args" {
		t.Errorf("expected kind 'invalid_args', got %q", result.Error.Kind)
	}
	go func() { for range ch {} }()
}

func TestExecuteExitPlanMode_PlanFileNotFound(t *testing.T) {
	l := New(nil, nil, Config{
		PlanFile: "/tmp/nonexistent-plan-xyz.md",
	})
	l.plan = true
	l.planPairID = "test1"

	tc := llm.ToolCall{
		ID:        "call_exit_2",
		Name:      "exit_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeExitPlanMode(context.Background(), tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError() {
		t.Fatal("expected error when plan file not found")
	}
	if result.Error.Kind != "file_not_found" {
		t.Errorf("expected kind 'file_not_found', got %q", result.Error.Kind)
	}
	go func() { for range ch {} }()
}

func TestExecuteExitPlanMode_NoResponder(t *testing.T) {
	// 创建临时 plan 文件
	tmpFile, err := os.CreateTemp("", "plan-test-*.md")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name()) //nolint:errcheck
	_, _ = tmpFile.WriteString("# Test Plan\nThis is a plan.")
	_ = tmpFile.Close()

	l := New(nil, nil, Config{
		PlanFile: tmpFile.Name(),
		// UserResponder = nil
	})
	l.plan = true
	l.planPairID = "test2"

	tc := llm.ToolCall{
		ID:        "call_exit_3",
		Name:      "exit_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeExitPlanMode(context.Background(), tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError() {
		t.Fatal("expected error when no UserResponder")
	}
	if result.Error.Kind != "user_declined" {
		t.Errorf("expected kind 'user_declined', got %q", result.Error.Kind)
	}
	go func() { for range ch {} }()
}

func TestExecuteExitPlanMode_UserRejected(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "plan-test-*.md")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name()) //nolint:errcheck
	_, _ = tmpFile.WriteString("# Test Plan\nThis is a plan.")
	_ = tmpFile.Close()

	l := New(nil, nil, Config{
		UserResponder: &mockPlanResponder{
			approvePlan: permission.PlanApproval{Approved: false, Feedback: "Needs more detail"},
		},
		PlanFile: tmpFile.Name(),
	})
	l.plan = true
	l.planPairID = "test3"

	tc := llm.ToolCall{
		ID:        "call_exit_4",
		Name:      "exit_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeExitPlanMode(context.Background(), tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError() {
		t.Fatal("expected error when user rejects plan")
	}
	if result.Error.Kind != "user_declined" {
		t.Errorf("expected kind 'user_declined', got %q", result.Error.Kind)
	}
	if !contains(result.Error.Message, "Needs more detail") {
		t.Errorf("expected feedback in error message, got: %s", result.Error.Message)
	}
	// 应仍然在 plan 模式
	if !l.plan {
		t.Error("expected to still be in plan mode after rejection")
	}
	go func() { for range ch {} }()
}

func TestExecuteExitPlanMode_UserRejectedNoFeedback(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "plan-test-*.md")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name()) //nolint:errcheck
	_, _ = tmpFile.WriteString("# Test Plan\nThis is a plan.")
	_ = tmpFile.Close()

	l := New(nil, nil, Config{
		UserResponder: &mockPlanResponder{
			approvePlan: permission.PlanApproval{Approved: false},
		},
		PlanFile: tmpFile.Name(),
	})
	l.plan = true
	l.planPairID = "test3b"

	tc := llm.ToolCall{
		ID:        "call_exit_4b",
		Name:      "exit_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeExitPlanMode(context.Background(), tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError() {
		t.Fatal("expected error when user rejects plan")
	}
	if !contains(result.Error.Message, "User rejected the plan") {
		t.Errorf("expected 'User rejected the plan' in message, got: %s", result.Error.Message)
	}
	go func() { for range ch {} }()
}

func TestExecuteExitPlanMode_ApprovalSuccess(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "plan-test-*.md")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name()) //nolint:errcheck
	_, _ = tmpFile.WriteString("# Test Plan\nThis is a plan.")
	_ = tmpFile.Close()

	l := New(nil, nil, Config{
		UserResponder: &mockPlanResponder{
			approvePlan: permission.PlanApproval{Approved: true},
		},
		Guard:    &mockGuard{},
		PlanFile: tmpFile.Name(),
	})
	l.plan = true
	l.planPairID = "test4"

	state := &LoopState{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "go"},
		},
	}

	tc := llm.ToolCall{
		ID:        "call_exit_5",
		Name:      "exit_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeExitPlanMode(context.Background(), tc, state, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError() {
		t.Fatalf("unexpected error result: %s", result.Error.Message)
	}
	if l.plan {
		t.Error("expected to exit plan mode after approval")
	}
	// [plan:end] 不再由 executeExitPlanMode 直接注入 state.Messages，
	// 改为 executeToolCalls 在 tool 消息之后统一注入（保证消息顺序正确）。
	// 此处仅验证 executeExitPlanMode 本身的状态切换。
	go func() { for range ch {} }()
}

func TestExecuteExitPlanMode_ApprovalError(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "plan-test-*.md")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name()) //nolint:errcheck
	_, _ = tmpFile.WriteString("# Test Plan\nThis is a plan.")
	_ = tmpFile.Close()

	l := New(nil, nil, Config{
		UserResponder: &mockPlanResponder{
			approvePlan:    permission.PlanApproval{},
			approvePlanErr: context.Canceled,
		},
		PlanFile: tmpFile.Name(),
	})
	l.plan = true

	tc := llm.ToolCall{
		ID:        "call_exit_6",
		Name:      "exit_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeExitPlanMode(context.Background(), tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError() {
		t.Fatal("expected error when ApprovePlan returns error")
	}
	go func() { for range ch {} }()
}

// ============================================================================
// SetPlanMode / InPlanMode / SetPlanFile 测试
// ============================================================================

func TestSetPlanMode(t *testing.T) {
	guard := &mockGuard{}
	l := New(nil, nil, Config{
		Guard:    guard,
		PlanFile: "/tmp/manual-plan.md",
	})

	pairID, msg := l.SetPlanMode("/tmp/manual-plan.md")
	if pairID == "" {
		t.Error("expected non-empty planPairID")
	}
	if !l.plan {
		t.Error("expected plan mode to be enabled")
	}
	if !contains(msg.Content, "[plan:start #") {
		t.Errorf("expected [plan:start #...] in message, got: %s", msg.Content)
	}
}

func TestInPlanMode(t *testing.T) {
	l := New(nil, nil, Config{})
	if l.InPlanMode() {
		t.Error("expected not in plan mode initially")
	}

	l.plan = true
	if !l.InPlanMode() {
		t.Error("expected in plan mode after setting")
	}
}

func TestSetPlanFile(t *testing.T) {
	l := New(nil, nil, Config{})
	l.SetPlanFile("/tmp/custom-plan.md")
	if l.config.PlanFile != "/tmp/custom-plan.md" {
		t.Errorf("expected PlanFile to be '/tmp/custom-plan.md', got %s", l.config.PlanFile)
	}
}

// ============================================================================
// planModeStartMessage / planModeEndMessage 测试
// ============================================================================

func TestPlanModeStartMessage(t *testing.T) {
	l := New(nil, nil, Config{PlanFile: "/tmp/my-plan.md"})
	l.planPairID = "f1a2"
	msg := l.planModeStartMessage()

	if !contains(msg, "[plan:start #f1a2]") {
		t.Errorf("expected [plan:start #f1a2] in message, got: %s", msg)
	}
	if !contains(msg, "/tmp/my-plan.md") {
		t.Errorf("expected plan file path in message")
	}
}

func TestPlanModeEndMessage(t *testing.T) {
	l := New(nil, nil, Config{PlanFile: "/tmp/my-plan.md"})
	l.planPairID = "f1a2"
	msg := l.planModeEndMessage("# My Approved Plan\nDo this.")

	if !contains(msg, "[plan:end #f1a2]") {
		t.Errorf("expected [plan:end #f1a2] in message, got: %s", msg)
	}
	if !contains(msg, "## Approved Plan:") {
		t.Errorf("expected '## Approved Plan:' section, got: %s", msg)
	}
	if !contains(msg, "# My Approved Plan") {
		t.Errorf("expected plan content in message")
	}
	if !contains(msg, "/tmp/my-plan.md") {
		t.Errorf("expected plan file path in message")
	}
}

// ============================================================================
// formatAnswersAsText 测试
// ============================================================================

func TestFormatAnswersAsText(t *testing.T) {
	prompts := []QuestionPrompt{
		{Question: "Which approach?", Header: "Approach", Options: nil},
	}
	responses := []QuestionResponse{
		{Question: "Which approach?", Answers: []string{"Option A"}},
	}

	result := formatAnswersAsText(prompts, responses)
	if result != "Which approach?: Option A\n" {
		t.Errorf("unexpected formatted text: %q", result)
	}
}

func TestFormatAnswersAsText_MultiAnswer(t *testing.T) {
	prompts := []QuestionPrompt{
		{Question: "Pick?", Header: "P", Options: nil},
	}
	responses := []QuestionResponse{
		{Question: "Pick?", Answers: []string{"A", "B", "C"}},
	}

	result := formatAnswersAsText(prompts, responses)
	if result != "Pick?: A, B, C\n" {
		t.Errorf("unexpected formatted text: %q", result)
	}
}

// ============================================================================
// generatePairID / generateWordSlug 测试
// ============================================================================

func TestGeneratePairID(t *testing.T) {
	id := generatePairID()
	if len(id) != 4 {
		t.Errorf("expected 4-char hex pair ID, got %q (len=%d)", id, len(id))
	}
	// 应每次不同（概率极高）
	id2 := generatePairID()
	if id == id2 {
		// 极小概率冲突，重试一次
		id2 = generatePairID()
		if id == id2 {
			t.Skip("extremely unlikely collision — skipping")
		}
	}
}

func TestGenerateWordSlug(t *testing.T) {
	slug := generateWordSlug()
	if slug == "" {
		t.Error("expected non-empty slug")
	}
	// 格式应为 "adjective-noun"
	if !contains(slug, "-") {
		t.Errorf("expected slug format 'adj-noun', got: %s", slug)
	}
}

func TestRandInt(t *testing.T) {
	// 验证 randInt 返回 [0, max) 范围内的值
	for max := 1; max <= 25; max++ {
		for i := 0; i < 10; i++ {
			val := randInt(max)
			if val < 0 || val >= max {
				t.Errorf("randInt(%d) = %d, out of range [0, %d)", max, val, max)
			}
		}
	}
}

func TestGeneratePlanFilePath(t *testing.T) {
	l := New(nil, nil, Config{})
	path := l.generatePlanFilePath()

	if path == "" {
		t.Error("expected non-empty plan file path")
	}

	// 应在 ~/.waveloom/plans/ 目录下
	homeDir, _ := os.UserHomeDir()
	expectedDir := filepath.Join(homeDir, ".waveloom", "plans")
	if !strings.HasPrefix(path, expectedDir) {
		t.Errorf("expected path under %s, got %s", expectedDir, path)
	}

	// 目录应已创建
	if _, err := os.Stat(expectedDir); os.IsNotExist(err) {
		t.Errorf("expected directory %s to exist", expectedDir)
	}

	// 应以 .md 结尾
	if filepath.Ext(path) != ".md" {
		t.Errorf("expected .md extension, got %s", filepath.Ext(path))
	}
}

func TestPlansDirectory(t *testing.T) {
	l := New(nil, nil, Config{})
	dir := l.plansDirectory()

	homeDir, _ := os.UserHomeDir()
	expectedDir := filepath.Join(homeDir, ".waveloom", "plans")
	if dir != expectedDir {
		t.Errorf("expected plans dir %s, got %s", expectedDir, dir)
	}
}

// ============================================================================
// executeToolCalls with plan mode tools (串行路径中的 UserInteractionTool)
// ============================================================================

func TestExecuteToolCalls_EnterPlanMode(t *testing.T) {
	guard := &mockGuard{}
	l := New(nil, nil, Config{
		UserResponder: &mockPlanResponder{enterPlanOK: true},
		Guard:         guard,
		PlanFile:      "/tmp/plan-exec-test.md",
	})

	// 注册 enter_plan_mode 工具，使其在 registry 中可查
	// 注意：serial 路径中通过 Registry.Get 检查 RequiresUserInteraction
	// 这里我们直接通过 mockTool 实现 UserInteractionTool 来测试
	registry := newTestRegistry()
	// 使用真实的 EnterPlanMode tool（需要 tool 包）
	// 由于循环依赖，使用模拟的 UserInteractionTool
	registry.Register(&mockUserInteractionTool{name: "enter_plan_mode", requiresUI: true})
	registry.Register(&mockUserInteractionTool{name: "exit_plan_mode", requiresUI: true})
	l.toolRegistry = registry

	state := &LoopState{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "test"},
		},
	}

	calls := []llm.ToolCall{
		makeToolCall("tc1", "enter_plan_mode", `{}`),
	}

	ch := make(chan TurnEvent, 16)
	go func() { for range ch {} }()

	msgs, reason, err := l.executeToolCalls(context.Background(), calls, state, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %s", reason)
	}
	// tool 消息 + [plan:start] user 消息
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (tool + [plan:start]), got %d", len(msgs))
	}
	if !l.plan {
		t.Error("expected plan mode to be enabled")
	}
}

func TestExecuteToolCalls_ExitPlanMode(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "plan-exec-test-*.md")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name()) //nolint:errcheck
	_, _ = tmpFile.WriteString("# Exec Plan\nTest.")
	_ = tmpFile.Close()

	guard := &mockGuard{}
	l := New(nil, nil, Config{
		UserResponder: &mockPlanResponder{
			approvePlan: permission.PlanApproval{Approved: true},
		},
		Guard:    guard,
		PlanFile: tmpFile.Name(),
	})
	l.plan = true
	l.planPairID = "exit1"

	registry := newTestRegistry()
	registry.Register(&mockUserInteractionTool{name: "enter_plan_mode", requiresUI: true})
	registry.Register(&mockUserInteractionTool{name: "exit_plan_mode", requiresUI: true})
	l.toolRegistry = registry

	state := &LoopState{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "test"},
		},
	}

	calls := []llm.ToolCall{
		makeToolCall("tc1", "exit_plan_mode", `{}`),
	}

	ch := make(chan TurnEvent, 16)
	go func() { for range ch {} }()

	msgs, reason, err := l.executeToolCalls(context.Background(), calls, state, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %s", reason)
	}
	// tool 消息 + [plan:end] user 消息
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (tool + [plan:end]), got %d", len(msgs))
	}
	if msgs[0].Role != llm.RoleTool {
		t.Errorf("msg[0]: expected tool role, got %s", msgs[0].Role)
	}
	if msgs[1].Role != llm.RoleUser {
		t.Errorf("msg[1]: expected user role for [plan:end], got %s", msgs[1].Role)
	}
	if l.plan {
		t.Error("expected to exit plan mode")
	}
}

// ============================================================================
// checkPermission with SuggestedPattern
// ============================================================================

func TestCheckPermission_WithSuggestedPattern(t *testing.T) {
	guard := &mockGuard{
		results: map[string]permission.DecisionResult{
			"test_tool": {
				Decision:         permission.DecisionAsk,
				Reason:           permission.ReasonDefault,
				Message:          "need confirmation",
				SuggestedPattern: "/tmp/test.txt",
			},
		},
	}
	l := New(nil, nil, Config{
		Guard: guard,
		UserResponder: &mockUserResponder{
			choices: map[string]permission.UserChoice{
				"test_tool": {Decision: permission.DecisionAllow, RememberScope: permission.ScopeSession},
			},
		},
	})

	results := make(map[string]*tool.ToolResult)
	skip := make(map[string]bool)

	tc := makeToolCall("tc1", "test_tool", `{"file_path":"/tmp/test.txt"}`)
	denied := l.checkPermission(context.Background(), tc, results, skip)

	if denied {
		t.Error("expected not denied when user allows")
	}
	if guard.sessionAllowCalls == 0 {
		t.Error("expected SessionAllow to be called with ScopeSession")
	}
}

func TestCheckPermission_NoSuggestedPattern(t *testing.T) {
	guard := &mockGuard{
		results: map[string]permission.DecisionResult{
			"test_tool": {
				Decision: permission.DecisionAsk,
				Reason:   permission.ReasonDefault,
				Message:  "need confirmation",
				// SuggestedPattern 为空 → 应从输入自动提取
			},
		},
	}
	l := New(nil, nil, Config{
		Guard: guard,
		UserResponder: &mockUserResponder{
			choices: map[string]permission.UserChoice{
				"test_tool": {Decision: permission.DecisionAllow, RememberScope: permission.ScopeSession},
			},
		},
	})

	results := make(map[string]*tool.ToolResult)
	skip := make(map[string]bool)

	tc := makeToolCall("tc1", "test_tool", `{"file_path":"/tmp/auto.txt"}`)
	denied := l.checkPermission(context.Background(), tc, results, skip)

	if denied {
		t.Error("expected not denied when user allows")
	}
	if guard.sessionAllowCalls == 0 {
		t.Error("expected SessionAllow to be called")
	}
}

func TestCheckPermission_NoGuard(t *testing.T) {
	l := New(nil, nil, Config{}) // Guard = nil

	results := make(map[string]*tool.ToolResult)
	skip := make(map[string]bool)

	tc := makeToolCall("tc1", "test_tool", `{}`)
	denied := l.checkPermission(context.Background(), tc, results, skip)

	if denied {
		t.Error("expected not denied when no guard configured")
	}
}

// ============================================================================
// buildToolMessages with skip map
// ============================================================================

func TestBuildToolMessages_WithSkip(t *testing.T) {
	l := New(nil, nil, Config{})

	calls := []llm.ToolCall{
		makeToolCall("tc1", "tool_a", `{}`),
		makeToolCall("tc2", "tool_b", `{}`),
	}

	results := map[string]*tool.ToolResult{
		"tc1": {Content: "result-a"},
		"tc2": {Content: "result-b"},
	}
	skip := map[string]bool{
		"tc2": true, // tool_b 被跳过（权限拒绝）
	}
	msgs, reason, err := l.buildToolMessages(calls, results, skip)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %s", reason)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	// tc1: 正常结果
	if msgs[0].Content != "result-a" {
		t.Errorf("msg[0]: expected 'result-a', got %s", msgs[0].Content)
	}
	// tc2: 在 skip 中，原始内容应保留
	if msgs[1].Content != "result-b" {
		t.Errorf("msg[1]: expected 'result-b' (skip preserves original), got %s", msgs[1].Content)
	}
}

func TestBuildToolMessages_MissingResult(t *testing.T) {
	l := New(nil, nil, Config{})

	calls := []llm.ToolCall{
		makeToolCall("tc1", "tool_a", `{}`),
	}

	results := map[string]*tool.ToolResult{} // 空：tc1 无结果
	skip := map[string]bool{}

	msgs, reason, err := l.buildToolMessages(calls, results, skip)
	// missing result → 生成 Fatal 占位错误
	if err == nil {
		t.Fatal("expected error for missing result")
	}
	if reason != ReasonToolFatal {
		t.Errorf("expected ReasonToolFatal, got %s", reason)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if !contains(msgs[0].Content, "execution interrupted") {
		t.Errorf("expected 'execution interrupted' in placeholder, got: %s", msgs[0].Content)
	}
}

// ============================================================================
// Helper
// ============================================================================

func contains(s, substring string) bool {
	for i := 0; i <= len(s)-len(substring); i++ {
		if s[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}

// mockUserInteractionTool 实现 tool.Tool 和 tool.UserInteractionTool。
type mockUserInteractionTool struct {
	name       string
	requiresUI bool
}

func (m *mockUserInteractionTool) Name() string             { return m.name }
func (m *mockUserInteractionTool) Description() string      { return "mock: " + m.name }
func (m *mockUserInteractionTool) Schema() json.RawMessage  { return json.RawMessage(`{}`) }
func (m *mockUserInteractionTool) ConcurrentSafe() bool     { return false }
func (m *mockUserInteractionTool) RequiresUserInteraction() bool { return m.requiresUI }

func (m *mockUserInteractionTool) Execute(ctx context.Context, raw json.RawMessage) (*tool.ToolResult, error) {
	return &tool.ToolResult{Content: m.name + " executed"}, nil
}

// ============================================================================
// ResetPlanMode 测试
// ============================================================================

func TestResetPlanMode(t *testing.T) {
	l := New(nil, nil, Config{
		Guard:    &mockGuard{},
		PlanFile: "/tmp/test.md",
	})

	// 模拟用户进入 plan 模式
	l.plan = true
	l.planPairID = "abcd"

	l.ResetPlanMode()

	if l.plan {
		t.Error("expected plan=false after ResetPlanMode")
	}
	if l.planPairID != "" {
		t.Errorf("expected empty planPairID after ResetPlanMode, got %q", l.planPairID)
	}
}

func TestResetPlanMode_Idempotent(t *testing.T) {
	l := New(nil, nil, Config{})

	// 多次调用不应 panic
	l.ResetPlanMode()
	l.ResetPlanMode()

	if l.plan {
		t.Error("expected plan=false after ResetPlanMode on fresh Loop")
	}
}

// ============================================================================
// generatePlanFilePath fallback 路径测试
// ============================================================================

func TestGeneratePlanFilePath_Unique(t *testing.T) {
	// 生成两个路径，应不同
	l := New(nil, nil, Config{})
	path1 := l.generatePlanFilePath()
	path2 := l.generatePlanFilePath()

	if path1 == path2 {
		t.Errorf("expected different plan file paths, got same: %s", path1)
	}
}

// ============================================================================
// Advisor mode plan mode switching tests
// ============================================================================

func TestAdvisorMode_EnterPlanMode_SwitchesModel(t *testing.T) {
	l := New(nil, nil, Config{
		AdvisorMode:   true,
		SubModel:      "sub-model",
		Model:         "sub-model",
		UserResponder: &mockPlanResponder{enterPlanOK: true},
		Guard:         &mockGuard{},
		PlanFile:      "/tmp/test-plan.md",
	})

	tc := llm.ToolCall{
		ID:        "call_adv_enter_1",
		Name:      "enter_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeEnterPlanMode(context.Background(), tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError() {
		t.Fatalf("unexpected error result: %s", result.Error.Message)
	}
	// Should switch to primary model (empty string = client default)
	if l.config.Model != "" {
		t.Errorf("expected Model to be empty (primary), got %q", l.config.Model)
	}
	// Should save the sub model
	if l.prePlanModel != "sub-model" {
		t.Errorf("expected prePlanModel to be %q, got %q", "sub-model", l.prePlanModel)
	}
	go func() { for range ch {} }()
}

func TestAdvisorMode_ExitPlanMode_RestoresModel(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "plan-adv-exit-*.md")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name()) //nolint:errcheck
	_, _ = tmpFile.WriteString("# Advisor Plan\nApproved plan content.")
	_ = tmpFile.Close()

	l := New(nil, nil, Config{
		AdvisorMode:   true,
		SubModel:      "sub-model",
		Model:         "sub-model",
		UserResponder: &mockPlanResponder{
			approvePlan: permission.PlanApproval{Approved: true},
		},
		Guard:    &mockGuard{},
		PlanFile: tmpFile.Name(),
	})
	// Simulate having entered plan mode: plan=true, model cleared, prePlanModel set
	l.plan = true
	l.planPairID = "adv1"
	l.prePlanModel = "sub-model"
	l.config.Model = ""

	tc := llm.ToolCall{
		ID:        "call_adv_exit_1",
		Name:      "exit_plan_mode",
		Arguments: `{}`,
	}

	state := &LoopState{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "approve"},
		},
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeExitPlanMode(context.Background(), tc, state, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError() {
		t.Fatalf("unexpected error result: %s", result.Error.Message)
	}
	// Should restore sub model
	if l.config.Model != "sub-model" {
		t.Errorf("expected Model to be restored to %q, got %q", "sub-model", l.config.Model)
	}
	// Should clear prePlanModel
	if l.prePlanModel != "" {
		t.Errorf("expected prePlanModel to be empty, got %q", l.prePlanModel)
	}
	go func() { for range ch {} }()
}

func TestAdvisorMode_NormalMode_NoSwitch(t *testing.T) {
	l := New(nil, nil, Config{
		AdvisorMode:   false,
		Model:         "some-model",
		UserResponder: &mockPlanResponder{enterPlanOK: true},
		Guard:         &mockGuard{},
		PlanFile:      "/tmp/test-plan.md",
	})

	tc := llm.ToolCall{
		ID:        "call_normal_enter_1",
		Name:      "enter_plan_mode",
		Arguments: `{}`,
	}

	ch := make(chan TurnEvent, 4)
	result, err := l.executeEnterPlanMode(context.Background(), tc, &LoopState{}, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError() {
		t.Fatalf("unexpected error result: %s", result.Error.Message)
	}
	// Model should remain unchanged in non-advisor mode
	if l.config.Model != "some-model" {
		t.Errorf("expected Model to remain %q, got %q", "some-model", l.config.Model)
	}
	// prePlanModel should not be set
	if l.prePlanModel != "" {
		t.Errorf("expected prePlanModel to be empty, got %q", l.prePlanModel)
	}
	go func() { for range ch {} }()
}
