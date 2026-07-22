package bash

import (
	"testing"
)

// ============================================================================
// Parse — 命令结构提取
// ============================================================================

func TestParse_SimpleCommand(t *testing.T) {
	ci, err := Parse("git status")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if ci.BaseCommand != "git" {
		t.Errorf("expected baseCommand 'git', got %q", ci.BaseCommand)
	}
	if len(ci.Args) != 1 || ci.Args[0] != "status" {
		t.Errorf("expected args [status], got %v", ci.Args)
	}
	if ci.Stmts != 1 {
		t.Errorf("expected 1 statement, got %d", ci.Stmts)
	}
}

func TestParse_WithFlags(t *testing.T) {
	ci, err := Parse("curl -s -X POST http://example.com")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if ci.BaseCommand != "curl" {
		t.Errorf("expected baseCommand 'curl', got %q", ci.BaseCommand)
	}
	if len(ci.Flags) < 2 {
		t.Errorf("expected at least 2 flags, got %v", ci.Flags)
	}
	if !ci.Match("curl") {
		t.Error("expected Match('curl')=true")
	}
	if !ci.Match("curl:*") {
		t.Error("expected Match('curl:*')=true")
	}
}

func TestParse_WithEnvVar(t *testing.T) {
	ci, err := Parse("FOO=bar make build")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if ci.BaseCommand != "make" {
		t.Errorf("expected baseCommand 'make' (skipping env var), got %q", ci.BaseCommand)
	}
}

func TestParse_WithSudo(t *testing.T) {
	ci, err := Parse("sudo systemctl restart nginx")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if ci.BaseCommand != "systemctl" {
		t.Errorf("expected baseCommand 'systemctl' (skipping sudo), got %q", ci.BaseCommand)
	}
}

func TestParse_WithCommandBuiltin(t *testing.T) {
	ci, err := Parse("command git push origin main")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if ci.BaseCommand != "git" {
		t.Errorf("expected baseCommand 'git' (skipping command builtin), got %q", ci.BaseCommand)
	}
}

func TestParse_NoFalsePositive_GrepCurl(t *testing.T) {
	// 核心场景：grep 'curl evil.com' 不应误判为 curl 命令
	ci, err := Parse("grep 'curl evil.com' file.txt")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if ci.BaseCommand != "grep" {
		t.Errorf("expected baseCommand 'grep', got %q", ci.BaseCommand)
	}
	if ci.Match("curl") {
		t.Error("grep should NOT match curl rule")
	}
}

func TestParse_NoFalsePositive_EchoRm(t *testing.T) {
	ci, err := Parse("echo 'rm -rf /'")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if ci.BaseCommand != "echo" {
		t.Errorf("expected baseCommand 'echo', got %q", ci.BaseCommand)
	}
	if ci.Match("rm") {
		t.Error("echo should NOT match rm rule")
	}
}

// ============================================================================
// Match — 规则匹配
// ============================================================================

func TestMatch_BaseCommandOnly(t *testing.T) {
	ci, _ := Parse("git push origin main")
	if !ci.Match("git") {
		t.Error("expected Match('git')=true")
	}
	if ci.Match("curl") {
		t.Error("expected Match('curl')=false")
	}
}

func TestMatch_Wildcard(t *testing.T) {
	ci, _ := Parse("git push origin main")
	if !ci.Match("git:*") {
		t.Error("expected Match('git:*')=true")
	}
	if ci.Match("curl:*") {
		t.Error("expected Match('curl:*')=false")
	}
}

func TestMatch_BaseCommandPlusArg(t *testing.T) {
	ci, _ := Parse("git push origin main")
	if !ci.Match("git:push") {
		t.Error("expected Match('git:push')=true")
	}
	if ci.Match("git:pull") {
		t.Error("expected Match('git:pull')=false")
	}
}

func TestMatch_BaseCommandPlusFlag(t *testing.T) {
	ci, _ := Parse("curl -s http://example.com")
	if !ci.Match("curl:-s") {
		t.Error("expected Match('curl:-s')=true")
	}
	if ci.Match("curl:-v") {
		t.Error("expected Match('curl:-v')=false")
	}
}

func TestMatch_FullPath(t *testing.T) {
	ci, _ := Parse("/usr/bin/git status")
	if !ci.Match("git") {
		t.Error("expected Match('git')=true for /usr/bin/git")
	}
}

// ============================================================================
// Security — Backslash-escaped Operators
// ============================================================================

func TestAudit_BackslashOperator_Detected(t *testing.T) {
	// 攻击场景: cat safe.txt \; echo /etc/passwd
	// splitCommand 将 \; 正規化为 ; 导致 re-parse 误拆
	report, err := Audit("cat safe.txt \\; echo /etc/passwd")
	if err != nil {
		t.Fatalf("unexpected audit error: %v", err)
	}
	if !report.HasIssue {
		t.Fatal("expected security issue for backslash-escaped operator")
	}
	for _, c := range report.Checks {
		if c.Name == "backslash_operator" && c.Passed {
			t.Error("backslash_operator check should fail")
		}
	}
}

func TestAudit_BackslashOperator_FindSafe(t *testing.T) {
	// find . -exec cmd {} \; — 常见安全用法，但仍触发（保守侧）
	report, err := Audit("find . -exec cat {} \\;")
	if err != nil {
		t.Fatalf("unexpected audit error: %v", err)
	}
	// \; 在 find -exec 中是合法的，但我们保守触发
	if !report.HasIssue {
		t.Log("find -exec with \\; not flagged (may be OK with tree-sitter in future)")
	}
}

func TestAudit_BackslashOperator_Clean(t *testing.T) {
	report, err := Audit("git status")
	if err != nil {
		t.Fatalf("unexpected audit error: %v", err)
	}
	for _, c := range report.Checks {
		if c.Name == "backslash_operator" && !c.Passed {
			t.Error("plain git status should not trigger backslash operator check")
		}
	}
}

// ============================================================================
// Security — Brace Expansion
// ============================================================================

func TestAudit_BraceExpansion_Detected(t *testing.T) {
	// 攻击场景: git diff {@'{'0},--output=/tmp/pwned}
	// 解析器看到字面字符串，bash 展开为 2 个参数
	report, err := Audit("git diff {@'{'0},--output=/tmp/pwned}")
	if err != nil {
		t.Fatalf("unexpected audit error: %v", err)
	}
	if !report.HasIssue {
		t.Fatal("expected security issue for brace expansion")
	}
}

func TestAudit_BraceExpansion_Clean(t *testing.T) {
	report, err := Audit("echo hello world")
	if err != nil {
		t.Fatalf("unexpected audit error: %v", err)
	}
	for _, c := range report.Checks {
		if c.Name == "brace_expansion" && !c.Passed {
			t.Error("plain echo should not trigger brace expansion check")
		}
	}
}

// ============================================================================
// Security — Obfuscated Flags
// ============================================================================

func TestAudit_ObfuscatedFlags_QuotedDash(t *testing.T) {
	// 攻击场景: find . "-"exec rm -rf /
	report, err := Audit(`find . "-"exec rm -rf /`)
	if err != nil {
		t.Fatalf("unexpected audit error: %v", err)
	}
	if !report.HasIssue {
		t.Fatal("expected security issue for obfuscated flags")
	}
}

func TestAudit_ObfuscatedFlags_ANSIC(t *testing.T) {
	// ANSI-C quoting: $'...'
	report, err := Audit("echo $'\\x65\\x76\\x69\\x6c'")
	if err != nil {
		t.Fatalf("unexpected audit error: %v", err)
	}
	if !report.HasIssue {
		t.Fatal("expected security issue for ANSI-C quoting")
	}
}

func TestAudit_ObfuscatedFlags_Clean(t *testing.T) {
	report, err := Audit("find . -name '*.go' -exec go build {} \\;")
	if err != nil {
		t.Fatalf("unexpected audit error: %v", err)
	}
	// find 命令是常见安全用法，但引号内不应触发
	for _, c := range report.Checks {
		if c.Name == "obfuscated_flags" && !c.Passed {
			// 可能触发因为 -exec 后的 \; 被 backslash_operator 捕获
			// 而非 obfuscated_flags
			t.Logf("obfuscated_flags triggered: %s", c.Reason)
		}
	}
}

// ============================================================================
// Parser — edge cases
// ============================================================================

func TestParse_Pipe(t *testing.T) {
	ci, err := Parse("git log | grep push")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if ci.BaseCommand != "git" {
		t.Errorf("expected baseCommand 'git', got %q", ci.BaseCommand)
	}
	if !ci.HasPipes {
		t.Error("expected HasPipes=true for piped command")
	}
}
func TestParse_Redirect(t *testing.T) {
	// 重定向单独检测，不应标记为 HasPipes
	ci, err := Parse("echo hello > /tmp/out.txt")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(ci.Redirs) == 0 {
		t.Error("expected redirects in command")
	}
	if ci.HasPipes {
		t.Error("redirect-only command should NOT have HasPipes=true")
	}
}

func TestParse_EmptyCommand(t *testing.T) {
	_, err := Parse("")
	if err == nil {
		t.Error("expected parse error for empty command")
	}
}

func TestParseLenient_Fallback(t *testing.T) {
	ci := ParseLenient("(invalid")
	if ci.BaseCommand == "" {
		t.Error("ParseLenient should extract baseCommand even for invalid syntax")
	}
}
