package permission

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDecisionConstants(t *testing.T) {
	tests := []struct {
		name  string
		value Decision
		want  string
	}{
		{"allow", DecisionAllow, "allow"},
		{"deny", DecisionDeny, "deny"},
		{"ask", DecisionAsk, "ask"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.value) != tt.want {
				t.Errorf("Decision %s = %q, want %q", tt.name, tt.value, tt.want)
			}
		})
	}
}

func TestDecisionReasonConstants(t *testing.T) {
	tests := []struct {
		name  string
		value DecisionReason
		want  string
	}{
		{"rule", ReasonRule, "rule"},
		{"default", ReasonDefault, "default"},
		{"safety", ReasonSafety, "safety"},
		{"session", ReasonSession, "session"},
		{"bypass", ReasonBypass, "bypass"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.value) != tt.want {
				t.Errorf("DecisionReason %s = %q, want %q", tt.name, tt.value, tt.want)
			}
		})
	}
}

func TestRuleBehaviorConstants(t *testing.T) {
	tests := []struct {
		name  string
		value RuleBehavior
		want  string
	}{
		{"allow", RuleAllow, "allow"},
		{"deny", RuleDeny, "deny"},
		{"ask", RuleAsk, "ask"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.value) != tt.want {
				t.Errorf("RuleBehavior %s = %q, want %q", tt.name, tt.value, tt.want)
			}
		})
	}
}

func TestRuleScopeConstants(t *testing.T) {
	if string(ScopeSession) != "session" {
		t.Errorf("ScopeSession = %q, want %q", ScopeSession, "session")
	}
	if string(ScopeConfig) != "config" {
		t.Errorf("ScopeConfig = %q, want %q", ScopeConfig, "config")
	}
}

func TestRuleSourceConstants(t *testing.T) {
	if string(SourceConfig) != "config" {
		t.Errorf("SourceConfig = %q, want %q", SourceConfig, "config")
	}
	if string(SourceSession) != "session" {
		t.Errorf("SourceSession = %q, want %q", SourceSession, "session")
	}
	if string(SourceCLI) != "cli" {
		t.Errorf("SourceCLI = %q, want %q", SourceCLI, "cli")
	}
}

func TestRiskLevelConstants(t *testing.T) {
	if string(PathSafe) != "safe" {
		t.Errorf("PathSafe = %q, want %q", PathSafe, "safe")
	}
	if string(PathSensitive) != "sensitive" {
		t.Errorf("PathSensitive = %q, want %q", PathSensitive, "sensitive")
	}
	if string(PathDangerous) != "dangerous" {
		t.Errorf("PathDangerous = %q, want %q", PathDangerous, "dangerous")
	}

	if string(RiskLow) != "low" {
		t.Errorf("RiskLow = %q, want %q", RiskLow, "low")
	}
	if string(RiskMedium) != "medium" {
		t.Errorf("RiskMedium = %q, want %q", RiskMedium, "medium")
	}
	if string(RiskHigh) != "high" {
		t.Errorf("RiskHigh = %q, want %q", RiskHigh, "high")
	}

	if string(RiskClassRead) != "read" {
		t.Errorf("RiskClassRead = %q, want %q", RiskClassRead, "read")
	}
	if string(RiskClassWrite) != "write" {
		t.Errorf("RiskClassWrite = %q, want %q", RiskClassWrite, "write")
	}
	if string(RiskClassExecute) != "execute" {
		t.Errorf("RiskClassExecute = %q, want %q", RiskClassExecute, "execute")
	}
}

func TestRuleString(t *testing.T) {
	tests := []struct {
		name string
		rule Rule
		want string
	}{
		{
			"tool-level allow",
			Rule{Behavior: RuleAllow, ToolName: "read_file"},
			"allow:read_file",
		},
		{
			"content-level deny",
			Rule{Behavior: RuleDeny, ToolName: "shell", Pattern: "rm -rf *"},
			"deny:shell(rm -rf *)",
		},
		{
			"content-level ask",
			Rule{Behavior: RuleAsk, ToolName: "write_file", Pattern: "src/**"},
			"ask:write_file(src/**)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rule.String(); got != tt.want {
				t.Errorf("Rule.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecisionResultZeroValue(t *testing.T) {
	var dr DecisionResult
	if dr.Decision != "" {
		t.Errorf("zero DecisionResult.Decision = %q, want empty", dr.Decision)
	}
	if dr.Reason != "" {
		t.Errorf("zero DecisionResult.Reason = %q, want empty", dr.Reason)
	}
	if dr.Message != "" {
		t.Errorf("zero DecisionResult.Message = %q, want empty", dr.Message)
	}
	if dr.Rule != "" {
		t.Errorf("zero DecisionResult.Rule = %q, want empty", dr.Rule)
	}
}

func TestRuleZeroValue(t *testing.T) {
	var r Rule
	if r.Behavior != "" {
		t.Errorf("zero Rule.Behavior = %q, want empty", r.Behavior)
	}
	if r.ToolName != "" {
		t.Errorf("zero Rule.ToolName = %q, want empty", r.ToolName)
	}
	if r.Pattern != "" {
		t.Errorf("zero Rule.Pattern = %q, want empty", r.Pattern)
	}
}

// mockGuard 用于验证 Guard 接口类型断言。
type mockGuard struct {
	checkFn func(ctx context.Context, toolName string, input json.RawMessage) DecisionResult
}

func (m *mockGuard) Check(ctx context.Context, toolName string, input json.RawMessage) DecisionResult {
	if m.checkFn != nil {
		return m.checkFn(ctx, toolName, input)
	}
	return DecisionResult{Decision: DecisionAllow, Reason: ReasonDefault}
}
func (m *mockGuard) AddRule(rule Rule, scope RuleScope) error   { return nil }
func (m *mockGuard) RemoveRule(rule Rule, scope RuleScope) error { return nil }
func (m *mockGuard) ListRules() []RuleEntry                      { return nil }
func (m *mockGuard) PersistRule(rule Rule) error                 { return nil }
func (m *mockGuard) SessionAllow(toolName string, input json.RawMessage)  {}
func (m *mockGuard) SessionDeny(toolName string, input json.RawMessage)   {}
func (m *mockGuard) ClearSession()                               {}
func (m *mockGuard) SessionMemoryLen() int                       { return 0 }

func TestGuardInterfaceTypeAssertion(t *testing.T) {
	var g Guard = &mockGuard{}
	_ = g // 编译期验证 mockGuard 实现 Guard 接口
}

// mockUserResponder 用于验证 UserResponder 接口类型断言。
type mockUserResponder struct {
	askFn func(ctx context.Context, toolName string, input json.RawMessage, result DecisionResult) UserChoice
}

func (m *mockUserResponder) AskUser(ctx context.Context, toolName string, input json.RawMessage, result DecisionResult) UserChoice {
	if m.askFn != nil {
		return m.askFn(ctx, toolName, input, result)
	}
	return UserChoice{Decision: DecisionAllow}
}

func (m *mockUserResponder) AnswerQuestion(ctx context.Context, questions []QuestionPrompt) ([]QuestionResponse, error) {
	// 默认返回空答案（拒绝）
	return nil, nil
}

func TestUserResponderInterfaceTypeAssertion(t *testing.T) {
	var u UserResponder = &mockUserResponder{}
	_ = u // 编译期验证 mockUserResponder 实现 UserResponder 接口
}
