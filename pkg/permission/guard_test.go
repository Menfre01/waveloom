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
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0o644)
	os.MkdirAll(filepath.Join(dir, ".git", "refs"), 0o755)
	return dir
}

func TestGuard_Check_DenyRule(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(
		WithWorkingDirs(dir),
		WithRules([]RuleEntry{
			{Rule: Rule{Behavior: RuleDeny, ToolName: "shell"}, Source: SourceConfig, Scope: ScopeConfig},
		}),
	)

	result := g.Check(context.Background(), "shell", json.RawMessage(`{"command": "ls"}`))
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
			{Rule: Rule{Behavior: RuleAllow, ToolName: "shell"}, Source: SourceConfig, Scope: ScopeConfig},
		}),
	)

	result := g.Check(context.Background(), "shell", json.RawMessage(`{"command": "ls"}`))
	if result.Decision != DecisionAllow {
		t.Errorf("allow rule + low risk: Decision = %s, want %s", result.Decision, DecisionAllow)
	}
}

func TestGuard_Check_AllowRuleDoesNotBypassSafety(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(
		WithWorkingDirs(dir),
	)

	result := g.Check(context.Background(), "shell", json.RawMessage(`{"command": "rm -rf /"}`))
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

	result := g.Check(context.Background(), "shell", json.RawMessage(`{"command": "git status"}`))
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
	result = g.Check(context.Background(), "shell", json.RawMessage(`{"command": "ls"}`))
	if result.Decision != DecisionAllow {
		t.Errorf("bypass shell: Decision = %s, want %s", result.Decision, DecisionAllow)
	}

	// bypass 模式：高危命令仍被安全检查拦截（deny 不可 bypass）
	result = g.Check(context.Background(), "shell", json.RawMessage(`{"command": "rm -rf /"}`))
	if result.Decision != DecisionDeny {
		t.Errorf("bypass + high risk: Decision = %s, want %s", result.Decision, DecisionDeny)
	}
}

func TestGuard_Check_ContentLevelRule(t *testing.T) {
	dir := testGuardDir(t)
	g := NewGuard(
		WithWorkingDirs(dir),
		WithRules([]RuleEntry{
			{Rule: Rule{Behavior: RuleAllow, ToolName: "shell", Pattern: "git *"}, Source: SourceConfig, Scope: ScopeConfig},
		}),
	)

	result := g.Check(context.Background(), "shell", json.RawMessage(`{"command": "git status"}`))
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

	g.sessionMemory.Remember("shell", "git status", DecisionAllow)

	result := g.Check(context.Background(), "shell", json.RawMessage(`{"command": "git status"}`))
	if result.Decision != DecisionAllow {
		t.Errorf("session content-level: Decision = %s, want %s", result.Decision, DecisionAllow)
	}

	result = g.Check(context.Background(), "shell", json.RawMessage(`{"command": "make build"}`))
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

	g.AddRule(Rule{Behavior: RuleDeny, ToolName: "read_file"}, ScopeConfig)

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
	g.AddRule(rule, ScopeConfig)

	result := g.Check(context.Background(), "read_file", input)
	if result.Decision != DecisionDeny {
		t.Fatal("deny rule should be active")
	}

	g.RemoveRule(rule, ScopeConfig)

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
	pattern := ExtractPattern("shell", json.RawMessage(`{"command": "git status"}`))
	if pattern != "git status" {
		t.Errorf("extractContentPattern shell 'git status' = %q, want %q", pattern, "git status")
	}

	pattern = ExtractPattern("shell", json.RawMessage(`{"command": "git add file.txt"}`))
	if pattern != "git add file.txt" {
		t.Errorf("extractContentPattern shell 'git add file.txt' = %q, want %q", pattern, "git add file.txt")
	}

	pattern = ExtractPattern("shell", json.RawMessage(`{"command": "docker compose up -d"}`))
	if pattern != "docker compose up -d" {
		t.Errorf("extractContentPattern shell 'docker compose up -d' = %q, want %q", pattern, "docker compose up -d")
	}

	pattern = ExtractPattern("shell", json.RawMessage(`{"command": "ls"}`))
	if pattern != "ls" {
		t.Errorf("extractContentPattern shell 'ls' = %q, want %q", pattern, "ls")
	}

	pattern = ExtractPattern("shell", json.RawMessage(`{"command": "npm install"}`))
	if pattern != "npm install" {
		t.Errorf("extractContentPattern shell 'npm install' = %q, want %q", pattern, "npm install")
	}

	pattern = ExtractPattern("shell", json.RawMessage(`{"command": "npm install express"}`))
	if pattern != "npm install express" {
		t.Errorf("extractContentPattern shell 'npm install express' = %q, want %q", pattern, "npm install express")
	}

	// 归一化 cd 前缀后返回完整命令
	pattern = ExtractPattern("shell", json.RawMessage(`{"command": "cd /app && go test ./..."}`))
	if pattern != "go test ./..." {
		t.Errorf("extractContentPattern shell 'cd /app && go test ./...' = %q, want %q", pattern, "go test ./...")
	}

	pattern = ExtractPattern("shell", json.RawMessage(`{"command": "cd /tmp; ls"}`))
	if pattern != "ls" {
		t.Errorf("extractContentPattern shell 'cd /tmp; ls' = %q, want %q", pattern, "ls")
	}

	pattern = ExtractPattern("shell", json.RawMessage(`{"command": "cd /app && go build"}`))
	if pattern != "go build" {
		t.Errorf("extractContentPattern shell 'cd /app && go build' = %q, want %q", pattern, "go build")
	}

	pattern = ExtractPattern("shell", json.RawMessage(`{}`))
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
			{Rule: Rule{Behavior: RuleAllow, ToolName: "shell", Pattern: "rm -rf *"}, Source: SourceConfig, Scope: ScopeConfig},
		}),
	)

	result := g.Check(context.Background(), "shell", json.RawMessage(`{"command": "rm -rf /"}`))
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
	os.MkdirAll(filepath.Dir(absFile), 0o755)
	os.WriteFile(absFile, []byte("package main"), 0o644)

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
	os.MkdirAll(filepath.Dir(absPattern), 0o755)
	os.WriteFile(absPattern, []byte("package main"), 0o644)

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
	err := g.PersistRule(Rule{Behavior: RuleAllow, ToolName: "shell"})
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
	if err := PersistRuleToConfig(path, Rule{Behavior: RuleDeny, ToolName: "shell", Pattern: "rm -rf *"}); err != nil {
		t.Fatal(err)
	}

	// 验证文件包含两条规则
	data, _ := os.ReadFile(path)
	content := string(data)
	if !containsSubstr(content, "read_file") || !containsSubstr(content, "shell") {
		t.Error("config should contain both rules")
	}
}

func TestPersistRuleToConfig_Duplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// 写入相同规则两次
	PersistRuleToConfig(path, Rule{Behavior: RuleAllow, ToolName: "read_file"})
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
	if !containsRule(rules, "shell", "git *") {
		t.Error("exact match should return true")
	}
	if containsRule(rules, "shell", "rm *") {
		t.Error("non-matching pattern should return false")
	}
}

func TestContainsRule_EmptyList(t *testing.T) {
	if containsRule(nil, "shell", "") {
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
	os.WriteFile(globalPath, []byte(`{"permissions": {"allow": ["shell(git *)"]}}`), 0o644)
	// 项目：allow shell(git *) → 同键，覆盖（相同规则无实际变化，但验证合并逻辑不报错）
	os.WriteFile(projectPath, []byte(`{"permissions": {"allow": ["shell(git *)"], "deny": ["shell(rm *)"]}}`), 0o644)

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
	e := RuleEntry{Rule: Rule{Behavior: RuleAllow, ToolName: "shell", Pattern: "git *"}}
	key := ruleEntryKey(e)
	if key == "" {
		t.Error("ruleEntryKey should not be empty")
	}
	// 不同 Behavior 应产生不同 key
	e2 := RuleEntry{Rule: Rule{Behavior: RuleDeny, ToolName: "shell", Pattern: "git *"}}
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
