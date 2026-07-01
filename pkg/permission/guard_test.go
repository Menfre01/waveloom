package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// testGuardDir 创建一个临时工作目录（含 src/main.go 和 .git/）。
func testGuardDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, ".git", "refs"), 0o755)
	return dir
}

func TestGuard_Check_DenyRule(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(
		WithWorkingDirs(dir),
		WithRules([]RuleEntry{
			{Rule: Rule{Behavior: RuleDeny, ToolName: "bash"}, Source: SourceConfig, Scope: ScopeConfig},
		}),
	)

	result := g.Check(context.Background(), "bash", json.RawMessage(`{"command": "ls"}`))
	if result.Decision != DecisionDeny {
		t.Errorf("deny rule: Decision = %s, want %s", result.Decision, DecisionDeny)
	}
	if result.Reason != ReasonRule {
		t.Errorf("deny rule: Reason = %s, want %s", result.Reason, ReasonRule)
	}
}

func TestGuard_Check_AskRule(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(
		WithWorkingDirs(dir),
		WithRules([]RuleEntry{
			{Rule: Rule{Behavior: RuleAsk, ToolName: "write_file"}, Source: SourceConfig, Scope: ScopeConfig},
		}),
	)

	result := g.Check(context.Background(), "write_file", json.RawMessage(`{"file_path": "src/test.go"}`))
	if result.Decision != DecisionAsk {
		t.Errorf("ask rule: Decision = %s, want %s", result.Decision, DecisionAsk)
	}
	if result.Reason != ReasonRule {
		t.Errorf("ask rule: Reason = %s, want %s", result.Reason, ReasonRule)
	}
}

func TestGuard_Check_AllowRule(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(
		WithWorkingDirs(dir),
		WithRules([]RuleEntry{
			{Rule: Rule{Behavior: RuleAllow, ToolName: "bash"}, Source: SourceConfig, Scope: ScopeConfig},
		}),
	)

	result := g.Check(context.Background(), "bash", json.RawMessage(`{"command": "ls"}`))
	if result.Decision != DecisionAllow {
		t.Errorf("allow rule + low risk: Decision = %s, want %s", result.Decision, DecisionAllow)
	}
}

func TestGuard_Check_AllowRuleDoesNotBypassSafety(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(
		WithWorkingDirs(dir),
	)

	result := g.Check(context.Background(), "bash", json.RawMessage(`{"command": "rm -rf /"}`))
	if result.Decision != DecisionDeny {
		t.Errorf("high risk command: Decision = %s, want %s", result.Decision, DecisionDeny)
	}
	if result.Reason != ReasonSafety {
		t.Errorf("high risk command: Reason = %s, want %s", result.Reason, ReasonSafety)
	}
}

func TestGuard_Check_ShellSafeCommand(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(WithWorkingDirs(dir))

	result := g.Check(context.Background(), "bash", json.RawMessage(`{"command": "git status"}`))
	if result.Decision != DecisionAsk {
		t.Errorf("safe shell command: Decision = %s, want %s (default ask for execute)", result.Decision, DecisionAsk)
	}
}

func TestGuard_Check_FileReadSafe(t *testing.T) {
	dir := testGuardDir(t)
	absFile := filepath.Join(dir, "src", "main.go")
	input := json.RawMessage(fmt.Sprintf(`{"file_path": %q}`, absFile))

	g := NewGuard(WithWorkingDirs(dir))
	result := g.Check(context.Background(), "read_file", input)

	t.Logf("Check read_file(%s) = %+v, workingDirs=%v", absFile, result, g.workingDirs)
	if result.Decision != DecisionAllow {
		t.Errorf("read safe file: Decision = %s, want %s", result.Decision, DecisionAllow)
	}
}

func TestGuard_Check_FileWriteSafe(t *testing.T) {
	dir := testGuardDir(t)
	absFile := filepath.Join(dir, "src", "new.go")
	input := json.RawMessage(fmt.Sprintf(`{"file_path": %q}`, absFile))

	g := NewGuard(WithWorkingDirs(dir))
	result := g.Check(context.Background(), "write_file", input)

	t.Logf("Check write_file(%s) = %+v", absFile, result)
	if result.Decision != DecisionAsk {
		t.Errorf("write safe file: Decision = %s, want %s", result.Decision, DecisionAsk)
	}
}

func TestGuard_Check_FileReadGit(t *testing.T) {
	dir := testGuardDir(t)
	absFile := filepath.Join(dir, ".git", "config")
	input := json.RawMessage(fmt.Sprintf(`{"file_path": %q}`, absFile))

	g := NewGuard(WithWorkingDirs(dir))
	result := g.Check(context.Background(), "read_file", input)

	t.Logf("Check read_file(%s) = %+v", absFile, result)
	// 读取 .git 在工作目录内 → 默认 allow（read 类工具）
	if result.Decision != DecisionAllow {
		t.Errorf("read .git file: Decision = %s, want %s", result.Decision, DecisionAllow)
	}
}

func TestGuard_Check_FileWriteDangerous(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(WithWorkingDirs(dir))

	result := g.Check(context.Background(), "write_file", json.RawMessage(`{"file_path": "/etc/hosts"}`))
	if result.Decision != DecisionDeny {
		t.Errorf("write dangerous file: Decision = %s, want %s", result.Decision, DecisionDeny)
	}
	if result.Reason != ReasonSafety {
		t.Errorf("write dangerous file: Reason = %s, want %s", result.Reason, ReasonSafety)
	}
}

func TestGuard_Check_DenyPriorityOverAllow(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(
		WithWorkingDirs(dir),
		WithRules([]RuleEntry{
			{Rule: Rule{Behavior: RuleDeny, ToolName: "read_file"}, Source: SourceConfig, Scope: ScopeConfig},
			{Rule: Rule{Behavior: RuleAllow, ToolName: "read_file"}, Source: SourceConfig, Scope: ScopeConfig},
		}),
	)

	result := g.Check(context.Background(), "read_file", json.RawMessage(`{"file_path": "src/main.go"}`))
	if result.Decision != DecisionDeny {
		t.Errorf("deny + allow: Decision = %s, want %s", result.Decision, DecisionDeny)
	}
}

func TestGuard_Check_BypassMode(t *testing.T) {
	dir := testGuardDir(t)
	absFile := filepath.Join(dir, "src", "main.go")
	input := json.RawMessage(fmt.Sprintf(`{"file_path": %q}`, absFile))

	g := NewGuard(
		WithWorkingDirs(dir),
		WithBypassMode(true),
	)

	// bypass 模式下读文件 → allow
	result := g.Check(context.Background(), "read_file", input)
	if result.Decision != DecisionAllow {
		t.Errorf("bypass read: Decision = %s, want %s", result.Decision, DecisionAllow)
	}

	// bypass 模式下 shell 安全命令 → allow
	result = g.Check(context.Background(), "bash", json.RawMessage(`{"command": "ls"}`))
	if result.Decision != DecisionAllow {
		t.Errorf("bypass shell: Decision = %s, want %s", result.Decision, DecisionAllow)
	}

	// bypass 模式：高危命令仍被安全检查拦截（deny 不可 bypass）
	result = g.Check(context.Background(), "bash", json.RawMessage(`{"command": "rm -rf /"}`))
	if result.Decision != DecisionDeny {
		t.Errorf("bypass + high risk: Decision = %s, want %s", result.Decision, DecisionDeny)
	}
}

func TestGuard_Check_ContentLevelRule(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(
		WithWorkingDirs(dir),
		WithRules([]RuleEntry{
			{Rule: Rule{Behavior: RuleAllow, ToolName: "bash", Pattern: "git *"}, Source: SourceConfig, Scope: ScopeConfig},
		}),
	)

	result := g.Check(context.Background(), "bash", json.RawMessage(`{"command": "git status"}`))
	if result.Decision != DecisionAllow {
		t.Errorf("content-level allow: Decision = %s, want %s", result.Decision, DecisionAllow)
	}
}

func TestGuard_Check_DefaultUnknownTool(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(WithWorkingDirs(dir))

	result := g.Check(context.Background(), "unknown_tool", json.RawMessage(`{}`))
	if result.Decision != DecisionAsk {
		t.Errorf("unknown tool: Decision = %s, want %s", result.Decision, DecisionAsk)
	}
}

func TestGuard_Check_SessionMemory(t *testing.T) {
	dir := testGuardDir(t)
	absFile := filepath.Join(dir, "src", "test.go")
	input := json.RawMessage(fmt.Sprintf(`{"file_path": %q}`, absFile))

	g := NewGuard(WithWorkingDirs(dir))

	// 先让用户"记住"允许 write_file
	g.SessionAllow("write_file", input)

	// 第二次调用应通过 session 记忆
	result := g.Check(context.Background(), "write_file", input)
	if result.Decision != DecisionAllow {
		t.Errorf("session memory: Decision = %s, want %s", result.Decision, DecisionAllow)
	}
	if result.Reason != ReasonSession {
		t.Errorf("session memory: Reason = %s, want %s", result.Reason, ReasonSession)
	}
}

func TestGuard_Check_SessionMemoryContentLevel(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(WithWorkingDirs(dir))

	g.sessionMemory.Remember("bash", "git status", DecisionAllow)

	result := g.Check(context.Background(), "bash", json.RawMessage(`{"command": "git status"}`))
	if result.Decision != DecisionAllow {
		t.Errorf("session content-level: Decision = %s, want %s", result.Decision, DecisionAllow)
	}

	result = g.Check(context.Background(), "bash", json.RawMessage(`{"command": "make build"}`))
	if result.Decision == DecisionAllow && result.Reason == ReasonSession {
		t.Errorf("make build should not match git status session memory")
	}
}

func TestGuard_AddRule(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(WithWorkingDirs(dir))
	absFile := filepath.Join(dir, "src", "main.go")
	input := json.RawMessage(fmt.Sprintf(`{"file_path": %q}`, absFile))

	result := g.Check(context.Background(), "read_file", input)
	if result.Decision != DecisionAllow {
		t.Fatalf("expected default allow for read_file within working dir, got %s: %s", result.Decision, result.Message)
	}

	_ = g.AddRule(Rule{Behavior: RuleDeny, ToolName: "read_file"}, ScopeConfig)

	result = g.Check(context.Background(), "read_file", input)
	if result.Decision != DecisionDeny {
		t.Errorf("after AddRule deny: Decision = %s, want %s", result.Decision, DecisionDeny)
	}
}

func TestGuard_RemoveRule(t *testing.T) {
	dir := testGuardDir(t)
	absFile := filepath.Join(dir, "src", "main.go")
	input := json.RawMessage(fmt.Sprintf(`{"file_path": %q}`, absFile))

	g := NewGuard(WithWorkingDirs(dir))

	rule := Rule{Behavior: RuleDeny, ToolName: "read_file"}
	_ = g.AddRule(rule, ScopeConfig)

	result := g.Check(context.Background(), "read_file", input)
	if result.Decision != DecisionDeny {
		t.Fatal("deny rule should be active")
	}

	_ = g.RemoveRule(rule, ScopeConfig)

	result = g.Check(context.Background(), "read_file", input)
	if result.Decision != DecisionAllow {
		t.Errorf("after RemoveRule: Decision = %s (%s), want %s", result.Decision, result.Message, DecisionAllow)
	}
}

func TestGuard_ListRules(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(
		WithWorkingDirs(dir),
		WithRules([]RuleEntry{
			{Rule: Rule{Behavior: RuleAllow, ToolName: "read_file"}, Source: SourceConfig, Scope: ScopeConfig},
		}),
	)

	g.SessionAllow("write_file", nil)

	rules := g.ListRules()
	if len(rules) < 2 {
		t.Errorf("ListRules returned %d rules, want at least 2", len(rules))
	}

	hasConfig := false
	hasSession := false
	for _, r := range rules {
		if r.Source == SourceConfig {
			hasConfig = true
		}
		if r.Source == SourceSession {
			hasSession = true
		}
	}
	if !hasConfig {
		t.Error("ListRules should include config rules")
	}
	if !hasSession {
		t.Error("ListRules should include session rules")
	}
}

func TestGuard_NewGuard_DefaultWorkingDir(t *testing.T) {
	g := NewGuard()
	if len(g.workingDirs) == 0 {
		t.Error("NewGuard should set default working dirs")
	}
	cwd, _ := os.Getwd()
	found := false
	for _, d := range g.workingDirs {
		if d == cwd {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("default working dirs should contain CWD %q, got %v", cwd, g.workingDirs)
	}
}

func TestGuard_NewGuard_Options(t *testing.T) {
	dir := t.TempDir()
	g := NewGuard(
		WithWorkingDirs(dir),
		WithBypassMode(true),
		WithToolRiskClass("custom_tool", RiskClassRead),
		WithRules([]RuleEntry{
			{Rule: Rule{Behavior: RuleAllow, ToolName: "read_file"}, Source: SourceConfig, Scope: ScopeConfig},
		}),
	)

	if len(g.workingDirs) != 1 || g.workingDirs[0] != dir {
		t.Errorf("WithWorkingDirs: got %v, want [%s]", g.workingDirs, dir)
	}
	if !g.bypassMode {
		t.Error("WithBypassMode(true): bypassMode should be true")
	}
	if g.toolRiskClass["custom_tool"] != RiskClassRead {
		t.Errorf("WithToolRiskClass: got %s, want %s", g.toolRiskClass["custom_tool"], RiskClassRead)
	}
}

func TestExtractContentPattern_Shell(t *testing.T) {
	// 精确匹配：返回完整归一化命令
	pattern := ExtractPattern("bash", json.RawMessage(`{"command": "git status"}`))
	if pattern != "git status" {
		t.Errorf("extractContentPattern shell 'git status' = %q, want %q", pattern, "git status")
	}

	pattern = ExtractPattern("bash", json.RawMessage(`{"command": "git add file.txt"}`))
	if pattern != "git add file.txt" {
		t.Errorf("extractContentPattern shell 'git add file.txt' = %q, want %q", pattern, "git add file.txt")
	}

	pattern = ExtractPattern("bash", json.RawMessage(`{"command": "docker compose up -d"}`))
	if pattern != "docker compose up -d" {
		t.Errorf("extractContentPattern shell 'docker compose up -d' = %q, want %q", pattern, "docker compose up -d")
	}

	pattern = ExtractPattern("bash", json.RawMessage(`{"command": "ls"}`))
	if pattern != "ls" {
		t.Errorf("extractContentPattern shell 'ls' = %q, want %q", pattern, "ls")
	}

	pattern = ExtractPattern("bash", json.RawMessage(`{"command": "npm install"}`))
	if pattern != "npm install" {
		t.Errorf("extractContentPattern shell 'npm install' = %q, want %q", pattern, "npm install")
	}

	pattern = ExtractPattern("bash", json.RawMessage(`{"command": "npm install express"}`))
	if pattern != "npm install express" {
		t.Errorf("extractContentPattern shell 'npm install express' = %q, want %q", pattern, "npm install express")
	}

	// 归一化 cd 前缀后返回完整命令
	pattern = ExtractPattern("bash", json.RawMessage(`{"command": "cd /app && go test ./..."}`))
	if pattern != "go test ./..." {
		t.Errorf("extractContentPattern shell 'cd /app && go test ./...' = %q, want %q", pattern, "go test ./...")
	}

	pattern = ExtractPattern("bash", json.RawMessage(`{"command": "cd /tmp; ls"}`))
	if pattern != "ls" {
		t.Errorf("extractContentPattern shell 'cd /tmp; ls' = %q, want %q", pattern, "ls")
	}

	pattern = ExtractPattern("bash", json.RawMessage(`{"command": "cd /app && go build"}`))
	if pattern != "go build" {
		t.Errorf("extractContentPattern shell 'cd /app && go build' = %q, want %q", pattern, "go build")
	}

	pattern = ExtractPattern("bash", json.RawMessage(`{}`))
	if pattern != "" {
		t.Errorf("extractContentPattern shell empty = %q, want empty", pattern)
	}
}

func TestExtractFilePath_Tools(t *testing.T) {
	path, _ := extractFilePath(json.RawMessage(`{"file_path": "src/main.go"}`))
	if path != "src/main.go" {
		t.Errorf("extractFilePath read_file = %q, want %q", path, "src/main.go")
	}

	path, _ = extractFilePath(json.RawMessage(`{"working_dir": "src"}`))
	if path != "src" {
		t.Errorf("extractFilePath grep = %q, want %q", path, "src")
	}

	path, _ = extractFilePath(json.RawMessage(`{}`))
	if path != "" {
		t.Errorf("extractFilePath unknown = %q, want empty", path)
	}
}

// ---------------------------------------------------------------------------
// bypass + session memory 交互测试
// ---------------------------------------------------------------------------

func TestGuard_Check_SessionDenyBlocksBypass(t *testing.T) {
	dir := testGuardDir(t)
	absFile := filepath.Join(dir, "src", "test.go")
	input := json.RawMessage(fmt.Sprintf(`{"file_path": %q}`, absFile))

	g := NewGuard(
		WithWorkingDirs(dir),
		WithBypassMode(true),
	)

	// 先设置 session deny
	g.SessionDeny("write_file", input)

	// session deny 在 step 5，优先于 step 7 的 bypass → 仍应 deny
	result := g.Check(context.Background(), "write_file", input)
	if result.Decision != DecisionDeny {
		t.Errorf("session deny + bypass: Decision = %s, want %s", result.Decision, DecisionDeny)
	}
	if result.Reason != ReasonSession {
		t.Errorf("session deny + bypass: Reason = %s, want %s", result.Reason, ReasonSession)
	}
}

// ---------------------------------------------------------------------------
// 内部辅助函数单元测试
// ---------------------------------------------------------------------------

func TestStringsJoin(t *testing.T) {
	tests := []struct {
		elems []string
		sep   string
		want  string
	}{
		{[]string{"a", "b", "c"}, " ", "a b c"},
		{[]string{"git", "add"}, " ", "git add"},
		{[]string{"single"}, ",", "single"},
		{nil, " ", ""},
		{[]string{}, " ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := stringsJoin(tt.elems, tt.sep)
			if got != tt.want {
				t.Errorf("stringsJoin(%v, %q) = %q, want %q", tt.elems, tt.sep, got, tt.want)
			}
		})
	}
}

func TestSplitFields(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"git status", []string{"git", "status"}},
		{"docker compose up -d", []string{"docker", "compose", "up", "-d"}},
		{"ls", []string{"ls"}},
		{"", nil},
		{"  ", nil},
		{"  a  b  ", []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitFields(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("splitFields(%q) = %v (len=%d), want %v (len=%d)", tt.input, got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitFields(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSplitBySpace(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"a b c", []string{"a", "b", "c"}},
		{"a\tb", []string{"a", "b"}},
		{"hello", []string{"hello"}},
		{"", nil},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitBySpace(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("splitBySpace(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitBySpace(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 回归测试：安全硬检查不可被 allow 规则绕过
// ---------------------------------------------------------------------------

func TestGuard_Check_AllowRuleMustNotBypassSafetyHardBlock(t *testing.T) {
	dir := testGuardDir(t)
	// allow:shell(rm -rf *) 匹配 "rm -rf /" 的 prefix，但安全硬检查应在 allow 规则之前拦截
	g := NewGuard(
		WithWorkingDirs(dir),
		WithRules([]RuleEntry{
			{Rule: Rule{Behavior: RuleAllow, ToolName: "bash", Pattern: "rm -rf *"}, Source: SourceConfig, Scope: ScopeConfig},
		}),
	)

	result := g.Check(context.Background(), "bash", json.RawMessage(`{"command": "rm -rf /"}`))
	if result.Decision != DecisionDeny {
		t.Errorf("allow rule must NOT bypass safety hard block: Decision = %s, want %s", result.Decision, DecisionDeny)
	}
	if result.Reason != ReasonSafety {
		t.Errorf("allow rule bypass: Reason = %s, want %s", result.Reason, ReasonSafety)
	}
}

// ---------------------------------------------------------------------------
// 回归测试：文件路径归一化 — 绝对路径 vs 相对路径 pattern
// ---------------------------------------------------------------------------

func TestGuard_Check_FilePathAbsoluteVsRelativeRule(t *testing.T) {
	dir := testGuardDir(t)
	absFile := filepath.Join(dir, "cmd", "waveloom", "tui.go")
	_ = os.MkdirAll(filepath.Dir(absFile), 0o755)
	_ = os.WriteFile(absFile, []byte("package main"), 0o644)

	// 用户配置的规则使用相对路径
	g := NewGuard(
		WithWorkingDirs(dir),
		WithRules([]RuleEntry{
			{Rule: Rule{Behavior: RuleAllow, ToolName: "read_file", Pattern: "cmd/waveloom/tui.go"}, Source: SourceConfig, Scope: ScopeConfig},
		}),
	)

	// LLM 传入绝对路径 → 应匹配
	input := json.RawMessage(fmt.Sprintf(`{"file_path": %q}`, absFile))
	result := g.Check(context.Background(), "read_file", input)
	if result.Decision != DecisionAllow {
		t.Errorf("relative pattern should match absolute path: Decision = %s, want %s", result.Decision, DecisionAllow)
	}
}

func TestGuard_Check_FilePathRelativeTargetWithAbsoluteRule(t *testing.T) {
	dir := testGuardDir(t)
	absPattern := filepath.Join(dir, "cmd", "waveloom", "tui.go")
	_ = os.MkdirAll(filepath.Dir(absPattern), 0o755)
	_ = os.WriteFile(absPattern, []byte("package main"), 0o644)

	// 用户配置的规则使用绝对路径
	g := NewGuard(
		WithWorkingDirs(dir),
		WithRules([]RuleEntry{
			{Rule: Rule{Behavior: RuleAllow, ToolName: "read_file", Pattern: absPattern}, Source: SourceConfig, Scope: ScopeConfig},
		}),
	)

	// LLM 传入相对路径 → 应匹配
	result := g.Check(context.Background(), "read_file", json.RawMessage(`{"file_path": "cmd/waveloom/tui.go"}`))
	if result.Decision != DecisionAllow {
		t.Errorf("absolute pattern should match relative path: Decision = %s (%s), want %s", result.Decision, result.Message, DecisionAllow)
	}
}

// ---------------------------------------------------------------------------
// WithProjectConfigPath
// ---------------------------------------------------------------------------

func TestWithProjectConfigPath(t *testing.T) {
	g := NewGuard(WithProjectConfigPath("/test/settings.json"))
	if g.projectConfigPath != "/test/settings.json" {
		t.Errorf("expected /test/settings.json, got %q", g.projectConfigPath)
	}
}

// ---------------------------------------------------------------------------
// SessionMemory / ClearSession / SessionMemoryLen
// ---------------------------------------------------------------------------

func TestSessionMemory(t *testing.T) {
	g := NewGuard()
	sm := g.SessionMemory()
	if sm == nil {
		t.Fatal("SessionMemory returned nil")
	}
}

func TestClearSession(t *testing.T) {
	g := NewGuard()
	// 直接向 SessionMemory 添加条目
	sm := g.SessionMemory()
	sm.Remember("read_file", "*.go", DecisionAllow)
	if g.SessionMemoryLen() == 0 {
		t.Error("session memory should have entries after Remember")
	}
	g.ClearSession()
	if g.SessionMemoryLen() != 0 {
		t.Errorf("session memory should be empty after ClearSession, got %d", g.SessionMemoryLen())
	}
}

func TestSessionMemoryLen_Empty(t *testing.T) {
	g := NewGuard()
	if g.SessionMemoryLen() != 0 {
		t.Errorf("expected 0, got %d", g.SessionMemoryLen())
	}
}

// ---------------------------------------------------------------------------
// PersistRule / PersistRuleToConfig / containsRule
// ---------------------------------------------------------------------------

func TestPersistRule_NoConfigPath(t *testing.T) {
	g := NewGuard()
	err := g.PersistRule(Rule{Behavior: RuleAllow, ToolName: "bash"})
	if err != nil {
		t.Errorf("PersistRule without config path should return nil, got %v", err)
	}
}

func TestPersistRuleToConfig_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	err := PersistRuleToConfig(path, Rule{Behavior: RuleAllow, ToolName: "read_file", Pattern: "*.go"})
	if err != nil {
		t.Fatalf("PersistRuleToConfig: %v", err)
	}

	// 验证文件存在
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("config file not created")
	}
	if len(data) == 0 {
		t.Error("config file is empty")
	}
}

func TestPersistRuleToConfig_Append(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// 第一次写入
	if err := PersistRuleToConfig(path, Rule{Behavior: RuleAllow, ToolName: "read_file"}); err != nil {
		t.Fatal(err)
	}
	// 第二次写入（不同规则）
	if err := PersistRuleToConfig(path, Rule{Behavior: RuleDeny, ToolName: "bash", Pattern: "rm -rf *"}); err != nil {
		t.Fatal(err)
	}

	// 验证文件包含两条规则
	data, _ := os.ReadFile(path)
	content := string(data)
	if !containsSubstr(content, "read_file") || !containsSubstr(content, "bash") {
		t.Error("config should contain both rules")
	}
}

func TestPersistRuleToConfig_Duplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// 写入相同规则两次
	_ = PersistRuleToConfig(path, Rule{Behavior: RuleAllow, ToolName: "read_file"})
	err := PersistRuleToConfig(path, Rule{Behavior: RuleAllow, ToolName: "read_file"})
	if err != nil {
		t.Fatalf("duplicate PersistRuleToConfig should be silent no-op: %v", err)
	}
}

func TestContainsRule_ToolLevel(t *testing.T) {
	rules := []string{"read_file"}
	if !containsRule(rules, "read_file", "") {
		t.Error("tool-level rule should match any pattern")
	}
	if !containsRule(rules, "read_file", "*.go") {
		t.Error("tool-level rule should match with pattern too")
	}
}

func TestContainsRule_ExactMatch(t *testing.T) {
	rules := []string{"shell(git *)"}
	if !containsRule(rules, "bash", "git *") {
		t.Error("exact match should return true")
	}
	if containsRule(rules, "bash", "rm *") {
		t.Error("non-matching pattern should return false")
	}
}

func TestContainsRule_EmptyList(t *testing.T) {
	if containsRule(nil, "bash", "") {
		t.Error("empty list should return false")
	}
}

// ---------------------------------------------------------------------------
// LoadRulesFromConfigFiles / ruleEntryKey
// ---------------------------------------------------------------------------

func TestLoadRulesFromConfigFiles_EmptyPaths(t *testing.T) {
	rules, err := LoadRulesFromConfigFiles("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(rules))
	}
}

func TestLoadRulesFromConfigFiles_ProjectOverridesGlobal(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.json")
	projectPath := filepath.Join(dir, "project.json")

	// 全局：allow shell(git *) 
	_ = os.WriteFile(globalPath, []byte(`{"permissions": {"allow": ["shell(git *)"]}}`), 0o644)
	// 项目：allow shell(git *) → 同键，覆盖（相同规则无实际变化，但验证合并逻辑不报错）
	_ = os.WriteFile(projectPath, []byte(`{"permissions": {"allow": ["shell(git *)"], "deny": ["shell(rm *)"]}}`), 0o644)

	rules, err := LoadRulesFromConfigFiles(globalPath, projectPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 应有 2 条规则：allow shell(git *) + deny shell(rm *)
	if len(rules) != 2 {
		t.Errorf("expected 2 rules, got %d", len(rules))
	}
}

func TestRuleEntryKey(t *testing.T) {
	e := RuleEntry{Rule: Rule{Behavior: RuleAllow, ToolName: "bash", Pattern: "git *"}}
	key := ruleEntryKey(e)
	if key == "" {
		t.Error("ruleEntryKey should not be empty")
	}
	// 不同 Behavior 应产生不同 key
	e2 := RuleEntry{Rule: Rule{Behavior: RuleDeny, ToolName: "bash", Pattern: "git *"}}
	key2 := ruleEntryKey(e2)
	if key == key2 {
		t.Error("different behaviors should produce different keys")
	}
}

// ---------------------------------------------------------------------------
// ExtractPattern partial
// ---------------------------------------------------------------------------

func TestExtractPattern_UnknownTool(t *testing.T) {
	// 未知工具名走 default 分支：尝试从 input 提取文件路径
	pattern := ExtractPattern("unknown_tool", json.RawMessage(`{"file_path": "/tmp/test.go"}`))
	if pattern == "" {
		t.Error("unknown tool with file_path should return resolved path")
	}
}

func TestExtractPattern_UnknownToolEmptyInput(t *testing.T) {
	pattern := ExtractPattern("unknown_tool", json.RawMessage(`{}`))
	if pattern != "" {
		t.Errorf("expected empty for empty input, got %q", pattern)
	}
}

func TestExtractPattern_WebFetch(t *testing.T) {
	pattern := ExtractPattern("web_fetch", json.RawMessage(`{"url": "https://example.com"}`))
	if pattern != "https://example.com" {
		t.Errorf("expected https://example.com, got %q", pattern)
	}
}

func TestExtractPattern_WebFetchEmptyURL(t *testing.T) {
	pattern := ExtractPattern("web_fetch", json.RawMessage(`{"url": ""}`))
	if pattern != "" {
		t.Errorf("expected empty for empty URL, got %q", pattern)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// REGRESSION: ask_user_question builtin allow
// ---------------------------------------------------------------------------

func TestGuard_Check_BuiltinAllowAskUserQuestion(t *testing.T) {
	g := NewGuard()

	args := json.RawMessage(`{"questions":[{"question":"Which lib?","header":"Lib","options":[{"label":"A","description":"desc"},{"label":"B","description":"desc2"}]}]}`)

	result := g.Check(context.Background(), "ask_user_question", args)
	if result.Decision != DecisionAllow {
		t.Errorf("ask_user_question: Decision = %s, want %s", result.Decision, DecisionAllow)
	}
	if result.Reason != ReasonBuiltinAllow {
		t.Errorf("ask_user_question: Reason = %s, want %s", result.Reason, ReasonBuiltinAllow)
	}
}

// ---------------------------------------------------------------------------
// REGRESSION: 真正安全命令（RiskNone）直接 ALLOW，不走 ASK
// ---------------------------------------------------------------------------

// TestGuard_Check_TrulySafeCommandsDirectAllow 验证纯只读命令不需要用户确认。
func TestGuard_Check_TrulySafeCommandsDirectAllow(t *testing.T) {
	g := NewGuard()

	tests := []struct {
		name    string
		command string
	}{
		{"ls", "ls -la"},
		{"cat", "cat README.md"},
		{"pwd", "pwd"},
		{"whoami", "whoami"},
		{"date", "date"},
		{"wc", "wc -l file.go"},
		{"diff", "diff a.go b.go"},
		{"test", "test -f README.md"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]string{"command": tt.command})
			if err != nil {
				t.Fatal(err)
			}
			result := g.Check(context.Background(), "bash", args)
			if result.Decision != DecisionAllow {
				t.Errorf("%q: Decision = %s, want %s (reason: %s)", tt.command, result.Decision, DecisionAllow, result.Reason)
			}
		})
	}
}

// TestGuard_Check_RemovedFromTrulySafeNowAsk 验证从 RiskNone 移除的命令恢复 ASK。
func TestGuard_Check_RemovedFromTrulySafeNowAsk(t *testing.T) {
	g := NewGuard()

	tests := []struct {
		name    string
		command string
	}{
		{"echo", "echo hello"},
		{"echo redirect", "echo 'key=val' >> .env"},
		{"env", "env"},
		{"printenv", "printenv"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]string{"command": tt.command})
			if err != nil {
				t.Fatal(err)
			}
			result := g.Check(context.Background(), "bash", args)
			if result.Decision != DecisionAsk {
				t.Errorf("%q: Decision = %s, want %s (reason: %s, msg: %s)", tt.command, result.Decision, DecisionAsk, result.Reason, result.Message)
			}
		})
	}
}

// TestGuard_Check_BuildToolsStillAsk 验证构建工具仍然需要用户确认（未来子命令白名单可细分）。
func TestGuard_Check_BuildToolsStillAsk(t *testing.T) {
	g := NewGuard()

	tests := []struct {
		name    string
		command string
	}{
		{"git status", "git status"},
		{"go test", "go test ./..."},
		{"go build", "go build ./..."},
		{"cargo test", "cargo test"},
		{"make build", "make build"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]string{"command": tt.command})
			if err != nil {
				t.Fatal(err)
			}
			result := g.Check(context.Background(), "bash", args)
			if result.Decision != DecisionAsk {
				t.Errorf("%q: Decision = %s, want %s (reason: %s)", tt.command, result.Decision, DecisionAsk, result.Reason)
			}
		})
	}
}

// TestGuard_Check_FormerlySafeNowAsk 验证之前被列入安全列表但有风险的命令现在走 ASK。
func TestGuard_Check_FormerlySafeNowAsk(t *testing.T) {
	g := NewGuard()

	tests := []struct {
		name    string
		command string
	}{
		{"python -c print", "python -c 'print(1+1)'"},
		{"node -e", "node -e 'console.log(1+1)'"},
		{"npm install", "npm install"},
		{"pip install", "pip install requests"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]string{"command": tt.command})
			if err != nil {
				t.Fatal(err)
			}
			result := g.Check(context.Background(), "bash", args)
			if result.Decision != DecisionAsk {
				t.Errorf("%q: Decision = %s, want %s (reason: %s, msg: %s)", tt.command, result.Decision, DecisionAsk, result.Reason, result.Message)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// REGRESSION: DenialTracker 接入 Check() — 连续拒绝达上限后强制 DENY
// ---------------------------------------------------------------------------

// TestRegression_DenialTrackerBlocksAfterLimit 验证连续拒绝 ≥3 次后，
// 后续任何 Check() 在 Step 1.5 直接返回 DENY，防止暴力试探。
func TestRegression_DenialTrackerBlocksAfterLimit(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(WithWorkingDirs(dir))

	// 触发 3 次连续拒绝（通过 deny 规则）
	for i := 0; i < 3; i++ {
		_ = g.AddRule(Rule{Behavior: RuleDeny, ToolName: "bash"}, ScopeSession)
	}
	// 第 1 次：deny rule 命中
	result := g.Check(context.Background(), "bash", json.RawMessage(`{"command": "ls"}`))
	if result.Decision != DecisionDeny {
		t.Fatalf("step 1: expected deny rule to hit, got %s", result.Decision)
	}
	// 移除 deny 规则，后续应靠 tracker 拦截
	_ = g.RemoveRule(Rule{Behavior: RuleDeny, ToolName: "bash"}, ScopeSession)
	// 确认规则已移除
	g.ruleEngine.LoadRules(nil)

	// 再触发 2 次 deny（合计 3 次连续）
	for i := 0; i < 2; i++ {
		_ = g.AddRule(Rule{Behavior: RuleDeny, ToolName: "bash"}, ScopeSession)
		result = g.Check(context.Background(), "bash", json.RawMessage(`{"command": "ls"}`))
		_ = g.RemoveRule(Rule{Behavior: RuleDeny, ToolName: "bash"}, ScopeSession)
		g.ruleEngine.LoadRules(nil)
	}

	// 验证已达上限
	if !g.denialTracker.AtLimit() {
		t.Fatal("denial tracker should be at limit after 3 consecutive denials")
	}

	// 此时 deny 规则已全部移除，但 tracker at limit → Step 1.5 应强制 DENY
	result = g.Check(context.Background(), "bash", json.RawMessage(`{"command": "ls"}`))
	if result.Decision != DecisionDeny {
		t.Errorf("after 3 denials: Decision = %s, want %s (tracker should force DENY)", result.Decision, DecisionDeny)
	}
	if result.Reason != ReasonSafety {
		t.Errorf("after 3 denials: Reason = %s, want %s", result.Reason, ReasonSafety)
	}

	// Allow 操作应重置连续计数（但 total 仍较高）
	g.denialTracker.RecordAllow()
	g.ruleEngine.LoadRules(nil)

	result = g.Check(context.Background(), "bash", json.RawMessage(`{"command": "ls"}`))
	// 连续计数已重置，应走默认策略（RiskLow → ASK）
	if result.Decision == DecisionDeny && result.Reason == ReasonSafety {
		t.Errorf("after RecordAllow: Decision = %s, want non-denial (consecutive reset)", result.Decision)
	}
}

// TestRegression_BuiltinAllowBypassesTracker 验证内置白名单工具不受 DenialTracker 限制。
func TestRegression_BuiltinAllowBypassesTracker(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(WithWorkingDirs(dir))

	// 触发 3 次连续拒绝
	for i := 0; i < 3; i++ {
		_ = g.AddRule(Rule{Behavior: RuleDeny, ToolName: "read_file"}, ScopeSession)
		g.Check(context.Background(), "read_file", json.RawMessage(`{"file_path": "/etc/hosts"}`))
		_ = g.RemoveRule(Rule{Behavior: RuleDeny, ToolName: "read_file"}, ScopeSession)
		g.ruleEngine.LoadRules(nil)
	}

	if !g.denialTracker.AtLimit() {
		t.Fatal("denial tracker should be at limit")
	}

	// ask_user_question 是 Step 0 内置白名单，不受 tracker 限制
	args := json.RawMessage(`{"questions":[{"question":"Q?","header":"H","options":[{"label":"A","description":"d"}]}]}`)
	result := g.Check(context.Background(), "ask_user_question", args)
	if result.Decision != DecisionAllow {
		t.Errorf("builtin allow should bypass tracker: Decision = %s, want %s", result.Decision, DecisionAllow)
	}
}

// ---------------------------------------------------------------------------
// REGRESSION: WithExtraWorkingDirs — 0% 覆盖补充
// ---------------------------------------------------------------------------

func TestWithExtraWorkingDirs(t *testing.T) {
	g := NewGuard(WithExtraWorkingDirs("/custom/dir"))
	found := false
	for _, d := range g.workingDirs {
		if d == "/custom/dir" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("WithExtraWorkingDirs should include /custom/dir, got %v", g.workingDirs)
	}
	// 默认的 cwd /tmp 也应保留
	if len(g.workingDirs) < 3 {
		t.Errorf("WithExtraWorkingDirs should retain default dirs, got %d", len(g.workingDirs))
	}
}

// ---------------------------------------------------------------------------
// REGRESSION: SetSkillBashWhitelist / ClearSkillBashWhitelist — 0% 覆盖补充
// ---------------------------------------------------------------------------

func TestSetSkillBashWhitelist(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(WithWorkingDirs(dir))

	// 设置白名单
	g.SetSkillBashWhitelist([]string{"git *"})
	if len(g.skillBashPatterns) != 1 || g.skillBashPatterns[0] != "git *" {
		t.Errorf("SetSkillBashWhitelist: got %v, want [git *]", g.skillBashPatterns)
	}

	// 白名单命令应绕过安全检查直接 ALLOW（通过 shellSafetyCheck 的 skill 分支）
	result := g.Check(context.Background(), "bash", json.RawMessage(`{"command": "git status"}`))
	if result.Decision != DecisionAllow {
		t.Errorf("skill whitelist 'git *': Decision = %s, want %s", result.Decision, DecisionAllow)
	}
	if result.Reason != ReasonBuiltinAllow {
		t.Errorf("skill whitelist: Reason = %s, want %s", result.Reason, ReasonBuiltinAllow)
	}
}

func TestClearSkillBashWhitelist(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(WithWorkingDirs(dir))

	g.SetSkillBashWhitelist([]string{"git *"})
	g.ClearSkillBashWhitelist()

	if len(g.skillBashPatterns) != 0 {
		t.Errorf("ClearSkillBashWhitelist: patterns should be empty after clear, got %v", g.skillBashPatterns)
	}

	// 清除后 git status 应回到默认 ASK
	result := g.Check(context.Background(), "bash", json.RawMessage(`{"command": "git status"}`))
	if result.Decision != DecisionAsk {
		t.Errorf("after clear: Decision = %s, want %s", result.Decision, DecisionAsk)
	}
}

// ---------------------------------------------------------------------------
// REGRESSION: shellSafetyCheck — JSON 解析失败 / 空命令
// ---------------------------------------------------------------------------

func TestShellSafetyCheck_InvalidJSON(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(WithWorkingDirs(dir))

	// JSON 解析失败 → passthrough
	result := g.shellSafetyCheck(json.RawMessage(`invalid`))
	if result.Decision != "" {
		t.Errorf("invalid JSON: Decision = %s, want empty (passthrough)", result.Decision)
	}
}

// ---------------------------------------------------------------------------
// REGRESSION: fileSafetyCheck — 空路径
// ---------------------------------------------------------------------------

func TestFileSafetyCheck_EmptyPath(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(WithWorkingDirs(dir))

	// 空 file_path → passthrough
	result := g.fileSafetyCheck(json.RawMessage(`{}`), true)
	if result.Decision != "" {
		t.Errorf("empty path: Decision = %s, want empty (passthrough)", result.Decision)
	}
}

// ---------------------------------------------------------------------------
// REGRESSION: PersistRule — config path 设置 / 未设置
// ---------------------------------------------------------------------------

func TestPersistRule_WithConfigPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	g := NewGuard(WithProjectConfigPath(path))

	rule := Rule{Behavior: RuleAllow, ToolName: "read_file", Pattern: "*.go"}
	err := g.PersistRule(rule)
	if err != nil {
		t.Fatalf("PersistRule with config path: %v", err)
	}

	// 验证文件被创建
	if _, statErr := os.Stat(path); statErr != nil {
		t.Error("config file not created after PersistRule")
	}
}

// ---------------------------------------------------------------------------
// REGRESSION: ExtractPattern — web_fetch 空 URL
// ---------------------------------------------------------------------------

func TestExtractPattern_WebFetchNoURL(t *testing.T) {
	pattern := ExtractPattern("web_fetch", json.RawMessage(`{}`))
	if pattern != "" {
		t.Errorf("expected empty for web_fetch with no url, got %q", pattern)
	}
}

// ---------------------------------------------------------------------------
// REGRESSION: Guard Check — Step 2.5 skill whitelist 不匹配回退
// ---------------------------------------------------------------------------

func TestGuard_Check_SkillWhitelistNoMatch(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(WithWorkingDirs(dir))

	// 设置白名单仅 git *
	g.SetSkillBashWhitelist([]string{"git *"})

	// "make build" 不匹配白名单 → 应走正常流程
	result := g.Check(context.Background(), "bash", json.RawMessage(`{"command": "make build"}`))
	if result.Decision == DecisionAllow && result.Reason == ReasonBuiltinAllow {
		t.Error("make build should NOT match git * whitelist")
	}

	g.ClearSkillBashWhitelist()
}

// ---------------------------------------------------------------------------
// REGRESSION: Guard Check — Step 0 内置白名单 skill 工具
// ---------------------------------------------------------------------------

func TestGuard_Check_BuiltinAllowSkill(t *testing.T) {
	g := NewGuard()

	result := g.Check(context.Background(), "skill", json.RawMessage(`{"name": "test-skill"}`))
	if result.Decision != DecisionAllow {
		t.Errorf("skill tool: Decision = %s, want %s", result.Decision, DecisionAllow)
	}
	if result.Reason != ReasonBuiltinAllow {
		t.Errorf("skill tool: Reason = %s, want %s", result.Reason, ReasonBuiltinAllow)
	}
}

// ---------------------------------------------------------------------------
// REGRESSION: ExtractPattern — ask_user_question 分支
// ---------------------------------------------------------------------------

func TestExtractPattern_AskUserQuestion(t *testing.T) {
	pattern := ExtractPattern("ask_user_question", json.RawMessage(`{"questions":[]}`))
	if pattern != "" {
		t.Errorf("ask_user_question should return empty pattern, got %q", pattern)
	}
}

// ---------------------------------------------------------------------------
// REGRESSION: ExtractPattern — bash unmarshal 失败
// ---------------------------------------------------------------------------

func TestExtractPattern_BashInvalidJSON(t *testing.T) {
	pattern := ExtractPattern("bash", json.RawMessage(`invalid`))
	if pattern != "" {
		t.Errorf("invalid JSON for bash should return empty, got %q", pattern)
	}
}

// ---------------------------------------------------------------------------
// REGRESSION: shellSafetyCheck — unmarshal 失败
// ---------------------------------------------------------------------------

func TestShellSafetyCheck_EmptyInput(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(WithWorkingDirs(dir))

	// 空 JSON → unmarshal 成功但 command 为空 → CommandSafetyCheck 返回 RiskNone
	result := g.shellSafetyCheck(json.RawMessage(`{}`))
	if result.Decision == "" {
		t.Error("empty command should still be checked (RiskNone → ALLOW)")
	}
}
