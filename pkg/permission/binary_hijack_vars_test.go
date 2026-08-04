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

func TestHasBinaryHijackVars(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{"LD_PRELOAD 开头", "LD_PRELOAD=/tmp/evil.so git status", true},
		{"DYLD 注入", `DYLD_INSERT_LIBRARIES=/tmp/x.dylib ls`, true},
		{"NODE_OPTIONS", "NODE_OPTIONS='--require /tmp/evil.js' node app.js", true},
		{"env 前缀", "env LD_PRELOAD=/tmp/evil.so git status", true},
		{"env 前缀多赋值", "env FOO=bar LD_PRELOAD=/tmp/evil.so git status", true},
		{"env -i 前缀", "env -i LD_PRELOAD=/tmp/evil.so git status", true},
		{"引号值", `LD_PRELOAD="/tmp/evil.so" git status`, true},
		{"多赋值混合", "LD_PRELOAD=/tmp/x.so FOO=bar git status", true},
		{"复合命令 &&", "cd /tmp && LD_PRELOAD=/tmp/evil.so ls", true},
		{"复合命令 ;", "true; LD_PRELOAD=/tmp/evil.so ls", true},
		{"复合命令管道", "echo x | LD_PRELOAD=/tmp/evil.so ls", true},
		{"注释行后", "# comment\nLD_PRELOAD=/tmp/evil.so ls", true},
		{"引号内分隔符不切分", `echo "a;b" LD_PRELOAD=x`, false}, // 引号内是字面量
		{"安全变量", "FOO=bar git status", false},
		{"构建参数变量不拦截", "CFLAGS=-O2 make build", false}, // 仅剥离,不硬拦截(二审 M2)
		{"无赋值", "git status", false},
		{"参数中含等号", "echo LD_PRELOAD=x", false},
		{"参数位置非赋值", "echo LD_PRELOAD=x", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasBinaryHijackVars(tt.cmd); got != tt.want {
				t.Errorf("HasBinaryHijackVars(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
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
