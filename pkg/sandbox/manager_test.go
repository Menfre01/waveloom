package sandbox

import (
	"encoding/json"
	"reflect"
	"testing"
)

// ============================================================================
// fakeBackend — 测试后端
// ============================================================================

type fakeBackend struct {
	name      string
	probeErr  error
	transform []string
	lastArgs  []string
}

func (f *fakeBackend) Name() string { return f.name }
func (f *fakeBackend) Probe() error { return f.probeErr }
func (f *fakeBackend) Transform(shellBin string, args []string, cfg *Config, workspace string) ([]string, error) {
	f.lastArgs = append([]string{shellBin}, args...)
	return f.transform, nil
}

func TestManager_SelectPlatform(t *testing.T) {
	cfg := DefaultConfig()
	m := NewManager(cfg, "/tmp/ws")

	// 直接注入 fake 后端(Select 按 GOOS 分发,测试环境不可控)
	fake := &fakeBackend{name: "fake", transform: []string{"bwrap", "true"}}
	m.backend = fake

	if !m.Available() {
		t.Error("Available() = false, want true")
	}
	if m.Name() != "fake" {
		t.Errorf("Name() = %q, want fake", m.Name())
	}
}

func TestManager_Transform_Unavailable(t *testing.T) {
	m := NewManager(DefaultConfig(), "/tmp/ws")
	if _, err := m.Transform("bash", []string{"-c", "ls"}); err != ErrSandboxUnavailable {
		t.Errorf("Transform without backend: err = %v, want ErrSandboxUnavailable", err)
	}
}

func TestManager_Transform_ForwardsArgs(t *testing.T) {
	fake := &fakeBackend{name: "fake", transform: []string{"bwrap", "bash", "-c", "ls"}}
	m := NewManager(DefaultConfig(), "/tmp/ws")
	m.backend = fake

	got, err := m.Transform("bash", []string{"-c", "ls"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"bwrap", "bash", "-c", "ls"}) {
		t.Errorf("Transform = %v", got)
	}
	if !reflect.DeepEqual(fake.lastArgs, []string{"bash", "-c", "ls"}) {
		t.Errorf("backend received %v, want [bash -c ls]", fake.lastArgs)
	}
}

func TestManager_ExtraFiles_NilWhenBackendLacks(t *testing.T) {
	m := NewManager(DefaultConfig(), "/tmp/ws")
	m.backend = &fakeBackend{name: "fake"}
	if m.ExtraFiles() != nil {
		t.Error("ExtraFiles should be nil for fake backend")
	}
}

// ============================================================================
// IsExcludedForTool / IsExcluded
// ============================================================================

func TestManager_IsExcludedForTool_NonBash(t *testing.T) {
	m := NewManager(&Config{ExcludedCommands: []string{"docker *"}}, "/tmp/ws")
	excluded, err := m.IsExcludedForTool("write_file", json.RawMessage(`{"file_path": "a.go"}`))
	if err != nil {
		t.Fatal(err)
	}
	if excluded {
		t.Error("non-bash tool should never be excluded")
	}
}

func TestManager_IsExcludedForTool_BashMatch(t *testing.T) {
	m := NewManager(&Config{ExcludedCommands: []string{"docker *"}}, "/tmp/ws")
	excluded, err := m.IsExcludedForTool("bash", json.RawMessage(`{"command": "docker ps"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !excluded {
		t.Error("docker ps should be excluded")
	}
}

func TestManager_IsExcludedForTool_BashNoMatch(t *testing.T) {
	m := NewManager(&Config{ExcludedCommands: []string{"docker *"}}, "/tmp/ws")
	excluded, err := m.IsExcludedForTool("bash", json.RawMessage(`{"command": "git status"}`))
	if err != nil {
		t.Fatal(err)
	}
	if excluded {
		t.Error("git status should not be excluded")
	}
}

func TestManager_IsExcludedForTool_Compound(t *testing.T) {
	// A && B 拆分后 B 命中排除 → 整体逃逸
	m := NewManager(&Config{ExcludedCommands: []string{"git push *"}}, "/tmp/ws")
	excluded, err := m.IsExcludedForTool("bash", json.RawMessage(`{"command": "git add a && git push origin main"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !excluded {
		t.Error("compound with git push should be excluded")
	}
}

func TestManager_IsExcludedForTool_EnvWrapper(t *testing.T) {
	// env VAR=x git push origin main → 剥离后命中
	m := NewManager(&Config{ExcludedCommands: []string{"git push *"}}, "/tmp/ws")
	excluded, err := m.IsExcludedForTool("bash", json.RawMessage(`{"command": "env GIT_AUTHOR=x git push origin main"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !excluded {
		t.Error("env-wrapped git push should be excluded")
	}
}

func TestManager_IsExcludedForTool_InvalidInput(t *testing.T) {
	m := NewManager(&Config{}, "/tmp/ws")
	if _, err := m.IsExcludedForTool("bash", json.RawMessage(`{bad`)); err == nil {
		t.Error("invalid json should return error")
	}
}

func TestMatchExcludedPattern(t *testing.T) {
	tests := []struct {
		cmd     string
		pattern string
		want    bool
	}{
		{"docker ps", "docker *", true},          // 前缀通配
		{"docker", "docker *", false},            // 前缀不匹配(需后随空格)
		{"docker ps", "docker", false},           // 精确不匹配整条命令
		{"git push origin main", "git push *", true},
		{"git status", "git push *", false},
		{"exact", "exact", true},                 // 精确匹配
		{"make build", "make *", true},
	}
	for _, tt := range tests {
		if got := matchExcludedPattern(tt.cmd, tt.pattern); got != tt.want {
			t.Errorf("matchExcludedPattern(%q, %q) = %v, want %v", tt.cmd, tt.pattern, got, tt.want)
		}
	}
}

// ============================================================================
// SplitCompoundCommand / StripEnvWrapper
// ============================================================================

func TestSplitCompoundCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want []string
	}{
		{"simple", "git status", []string{"git status"}},
		{"and chain", "git add a && git push origin main", []string{"git add a", "git push origin main"}},
		{"or chain", "false || echo hi", []string{"false", "echo hi"}},
		{"pipe", "cat a | grep x", []string{"cat a", "grep x"}},
		{"semicolon", "cd /tmp; ls", []string{"cd /tmp", "ls"}},
		{"triple", "a && b && c", []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitCompoundCommand(tt.cmd)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitCompoundCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestSplitCompoundCommand_ParseError(t *testing.T) {
	// 解析失败 → 整条退化
	got := SplitCompoundCommand(`echo "unterminated`)
	if len(got) != 1 || got[0] != `echo "unterminated` {
		t.Errorf("parse-error should degrade to single command, got %v", got)
	}
}

func TestStripEnvWrapper(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{"plain", "git status", "git status"},
		{"env assignment", "env FOO=1 git status", "git status"},
		{"env multi", "env A=1 B=2 make build", "make build"},
		{"env flag", "env -i PATH=/usr/bin go build", "go build"},
		{"env flag value", "env -u SECRET go test", "go test"},
		{"env only", "env FOO=1", "env FOO=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripEnvWrapper(tt.cmd); got != tt.want {
				t.Errorf("StripEnvWrapper(%q) = %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}
}
