package permission

import "testing"

func TestStripBinaryHijackVars_NoEnvVars(t *testing.T) {
	got := StripBinaryHijackVars("git status")
	if got != "git status" {
		t.Errorf("expected 'git status', got %q", got)
	}
}

func TestStripBinaryHijackVars_StripsDangerous(t *testing.T) {
	// LD_PRELOAD 应被剥离
	got := StripBinaryHijackVars("LD_PRELOAD=/tmp/evil.so git status")
	if got != "git status" {
		t.Errorf("LD_PRELOAD should be stripped, got %q", got)
	}
}

func TestStripBinaryHijackVars_KeepsSafe(t *testing.T) {
	// 安全环境变量保留
	got := StripBinaryHijackVars("FOO=bar git status")
	if got != "FOO=bar git status" {
		t.Errorf("FOO=bar should be kept, got %q", got)
	}
}

func TestStripBinaryHijackVars_MixedEnv(t *testing.T) {
	// 混合:剥离危险,保留安全
	got := StripBinaryHijackVars("LD_PRELOAD=/tmp/x.so FOO=bar git status")
	if got != "FOO=bar git status" {
		t.Errorf("expected 'FOO=bar git status', got %q", got)
	}
}

func TestStripBinaryHijackVars_DYLD(t *testing.T) {
	// macOS 危险变量
	got := StripBinaryHijackVars("DYLD_INSERT_LIBRARIES=/tmp/x.dylib git status")
	if got != "git status" {
		t.Errorf("DYLD_INSERT_LIBRARIES should be stripped, got %q", got)
	}
}

func TestStripBinaryHijackVars_QuotedValues(t *testing.T) {
	// 引号内的值也应正确剥离
	got := StripBinaryHijackVars(`LD_PRELOAD="/tmp/evil.so" git status`)
	if stringsTrimSpace(got) != "git status" {
		t.Errorf("quoted LD_PRELOAD should be stripped, got %q", got)
	}
}

func TestStripBinaryHijackVars_NoCommand(t *testing.T) {
	got := StripBinaryHijackVars("LD_PRELOAD=/tmp/x.so")
	if got != "" {
		t.Errorf("only dangerous var should yield empty, got %q", got)
	}
}

func TestStripBinaryHijackVars_NODE_OPTIONS(t *testing.T) {
	got := StripBinaryHijackVars("NODE_OPTIONS='--require /tmp/evil.js' node app.js")
	if stringsTrimSpace(got) != "node app.js" {
		t.Errorf("NODE_OPTIONS should be stripped, got %q", got)
	}
}

func TestStripBinaryHijackVars_PYTHONPATH(t *testing.T) {
	got := StripBinaryHijackVars("PYTHONPATH=/tmp/evil python3 script.py")
	if stringsTrimSpace(got) != "python3 script.py" {
		t.Errorf("PYTHONPATH should be stripped, got %q", got)
	}
}

func stringsTrimSpace(s string) string {
	i, j := 0, len(s)-1
	for i <= j && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	for j >= i && (s[j] == ' ' || s[j] == '\t') {
		j--
	}
	return s[i : j+1]
}
