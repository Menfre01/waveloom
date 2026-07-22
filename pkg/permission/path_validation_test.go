package permission

import (
	"path/filepath"
	"testing"
)

func TestFilterOutFlags(t *testing.T) {
	tests := []struct {
		args   []string
		expect []string
	}{
		{[]string{"file1", "file2"}, []string{"file1", "file2"}},
		{[]string{"-la", "file"}, []string{"file"}},
		{[]string{"-rf", "/tmp"}, []string{"/tmp"}},
		{[]string{"--", "-rf", "/tmp"}, []string{"-rf", "/tmp"}},
		{[]string{"--", "-/../.claude/settings.json"}, []string{"-/../.claude/settings.json"}},
	}
	for _, tc := range tests {
		got := filterOutFlags(tc.args)
		if len(got) != len(tc.expect) {
			t.Errorf("filterOutFlags(%v) = %v, want %v", tc.args, got, tc.expect)
			continue
		}
		for i := range got {
			if got[i] != tc.expect[i] {
				t.Errorf("filterOutFlags(%v)[%d] = %q, want %q", tc.args, i, got[i], tc.expect[i])
			}
		}
	}
}

func TestExtractCD(t *testing.T) {
	got := extractCD(nil)
	if len(got) == 0 || got[0] == "" {
		t.Error("extractCD(nil) should return home dir")
	}
	got = extractCD([]string{"/tmp"})
	if len(got) == 0 || got[0] != "/tmp" {
		t.Errorf("extractCD([/tmp]) = %v, want [/tmp]", got)
	}
}

func TestExtractFind(t *testing.T) {
	got := extractFind([]string{".", "-name", "*.go"})
	if len(got) != 1 || got[0] != "." {
		t.Errorf("extractFind(. -name *.go) = %v, want [.]", got)
	}
	got = extractFind([]string{"/tmp", "-name", "*.go"})
	if len(got) != 1 || got[0] != "/tmp" {
		t.Errorf("extractFind(/tmp -name *.go) = %v, want [/tmp]", got)
	}
	got = extractFind([]string{"-L", "/tmp", "-name", "*.go"})
	if len(got) != 1 || got[0] != "/tmp" {
		t.Errorf("extractFind(-L /tmp) = %v, want [/tmp]", got)
	}
	got = extractFind([]string{"--", "-path", "file"})
	if len(got) != 2 || got[0] != "-path" || got[1] != "file" {
		t.Errorf("extractFind(-- -path file) = %v, want [-path file]", got)
	}
}

func TestExtractGrep(t *testing.T) {
	got := extractGrep([]string{"-rn", "pattern", "."})
	if len(got) != 1 || got[0] != "." {
		t.Errorf("extractGrep(-rn pattern .) = %v, want [.]", got)
	}
	got = extractGrep([]string{"-r", "pattern"})
	if len(got) != 1 || got[0] != "." {
		t.Errorf("extractGrep(-r pattern) = %v, want [.]", got)
	}
	got = extractGrep([]string{"-n", "pattern", "file1.go", "file2.go"})
	if len(got) != 2 || got[0] != "file1.go" || got[1] != "file2.go" {
		t.Errorf("extractGrep = %v, want [file1.go file2.go]", got)
	}
}

func TestExtractRg(t *testing.T) {
	got := extractRg([]string{"pattern"})
	if len(got) != 1 || got[0] != "." {
		t.Errorf("extractRg(pattern) = %v, want [.]", got)
	}
}

func TestExtractSed(t *testing.T) {
	got := extractSed([]string{"-n", "10p", "file.txt"})
	if len(got) != 1 || got[0] != "file.txt" {
		t.Errorf("extractSed(-n 10p file.txt) = %v, want [file.txt]", got)
	}
	got = extractSed([]string{"-e", "s/a/b/", "file.txt"})
	if len(got) != 1 || got[0] != "file.txt" {
		t.Errorf("extractSed(-e s/a/b/ file.txt) = %v, want [file.txt]", got)
	}
}

func TestExtractJq(t *testing.T) {
	got := extractJq([]string{".items[]", "data.json"})
	if len(got) != 1 || got[0] != "data.json" {
		t.Errorf("extractJq(.items[] data.json) = %v, want [data.json]", got)
	}
}

func TestStripSafeWrappers(t *testing.T) {
	tests := []struct {
		cmd    string
		expect string
	}{
		{"rm -rf /tmp", "rm -rf /tmp"},
		{"timeout 10 rm -rf /tmp", "rm -rf /tmp"},
		{"nice rm file", "rm file"},
		{"nohup rm file", "rm file"},
		{"CC=gcc timeout 30 make build", "make build"},
	}
	for _, tc := range tests {
		got := stripSafeWrappers(tc.cmd)
		if got != tc.expect {
			t.Errorf("stripSafeWrappers(%q) = %q, want %q", tc.cmd, got, tc.expect)
		}
	}
}

func TestIsDangerousRemoval(t *testing.T) {
	tests := []struct {
		path   string
		expect bool
	}{
		{"/", true},
		{"/etc", true},
		{"/etc/passwd", false},
		{"/usr/local/bin", false},
		{string(filepath.Separator) + "tmp", true},
	}
	for _, tc := range tests {
		got := isDangerousRemoval(tc.path)
		if got != tc.expect {
			t.Errorf("isDangerousRemoval(%q) = %v, want %v", tc.path, got, tc.expect)
		}
	}
}

func TestValidatePathCommand_Safe(t *testing.T) {
	dir := t.TempDir()
	result := ValidatePathCommand("echo hello", dir, []string{dir}, false)
	if !result.Allowed {
		t.Errorf("echo should be allowed: %s", result.Message)
	}
}

func TestValidatePathCommand_CDWithWrite(t *testing.T) {
	dir := t.TempDir()
	result := ValidatePathCommand("mv file", dir, []string{dir}, true)
	if result.Allowed {
		t.Error("write with cd should NOT be allowed")
	}
}

func TestValidatePathCommand_MvWithFlags(t *testing.T) {
	dir := t.TempDir()
	result := ValidatePathCommand("mv --target-directory=/tmp file", dir, []string{dir}, false)
	if result.Allowed {
		t.Error("mv with flags should be blocked")
	}
}

func TestValidatePathCommand_OutsideWorkDir(t *testing.T) {
	dir := t.TempDir()
	result := ValidatePathCommand("cat /etc/passwd", dir, []string{dir}, false)
	if result.Allowed {
		t.Error("cat outside workdir should be blocked")
	}
}
