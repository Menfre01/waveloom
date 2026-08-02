package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ============================================================================
// Probe 能力探测(fake runner 注入)
// ============================================================================

// fakeBwrapRunner 构造带 fake runner 的 backend。
// helpOut: --help 输出;helpErr: --help 错误;smokeErr: 冒烟测试错误。
func fakeBwrapRunner(helpOut string, helpErr, smokeErr error) *bwrapBackend {
	b := newBwrapBackend()
	// LookPath 需真实存在的路径(绝对路径在 macOS 上可能因 /bin → /usr/bin 链接
	// 解析失败,用 PATH 可解析的 "true");runner 只处理 --help 与冒烟调用
	b.bin = "true"
	b.runCombined = func(name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "--help" {
			return []byte(helpOut), helpErr
		}
		// 冒烟测试
		return nil, smokeErr
	}
	return b
}

// skipIfWindows 跳过依赖 POSIX true 命令的 bwrap 探测测试:
// Windows 无 true 命令,Probe 的 LookPath 必然失败(与 linux_bwrap_test.go
// 平台限定同理;本文件其余测试跨平台,不能整文件加 build tag)。
func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("skipping bwrap probe tests on Windows (requires POSIX 'true' command)")
	}
}

func TestProbe_CapabilitiesParsed(t *testing.T) {
	skipIfWindows(t)
	// 隔离宿主 AppArmor 状态(ubuntu 24.04+ 默认限制 userns):
	// 本测试只验证 --help 能力解析,与真实 sysctl 无关。
	orig := apparmorSysctlPath
	apparmorSysctlPath = filepath.Join(t.TempDir(), "nonexistent")
	defer func() { apparmorSysctlPath = orig }()

	b := fakeBwrapRunner("--argv0\n--perms\n", nil, nil)
	if err := b.Probe(); err != nil {
		t.Fatal(err)
	}
	if !b.hasArgv0 || !b.hasPerms {
		t.Error("hasArgv0/hasPerms should be true when --help lists them")
	}
}

func TestProbe_CapabilitiesMissing_CompatibleFallback(t *testing.T) {
	skipIfWindows(t)
	// 隔离宿主 AppArmor 状态,同 TestProbe_CapabilitiesParsed。
	orig := apparmorSysctlPath
	apparmorSysctlPath = filepath.Join(t.TempDir(), "nonexistent")
	defer func() { apparmorSysctlPath = orig }()

	// 老版本 bwrap:--help 无 --argv0/--perms → 不报错,Transform 走兼容构造
	b := fakeBwrapRunner("usage: bwrap [options]\n", nil, nil)
	if err := b.Probe(); err != nil {
		t.Fatal(err)
	}
	if b.hasArgv0 || b.hasPerms {
		t.Error("hasArgv0/hasPerms should be false")
	}
}

func TestProbe_HelpFails(t *testing.T) {
	skipIfWindows(t)
	b := fakeBwrapRunner("", errors.New("exec format error"), nil)
	err := b.Probe()
	if err == nil || !strings.Contains(err.Error(), "--help failed") {
		t.Errorf("want --help failed error, got: %v", err)
	}
}

func TestProbe_SmokeFails(t *testing.T) {
	skipIfWindows(t)
	// 隔离宿主 AppArmor 状态,同 TestProbe_CapabilitiesParsed。
	orig := apparmorSysctlPath
	apparmorSysctlPath = filepath.Join(t.TempDir(), "nonexistent")
	defer func() { apparmorSysctlPath = orig }()

	b := fakeBwrapRunner("--argv0\n", nil, errors.New("operation not permitted"))
	err := b.Probe()
	if err == nil || !strings.Contains(err.Error(), "smoke test failed") {
		t.Errorf("want smoke test error, got: %v", err)
	}
}

func TestProbe_AppArmorRestricted(t *testing.T) {
	skipIfWindows(t)
	// sysctl 值为 1 → 拒绝并给出修复指引
	tmp := t.TempDir()
	sysctl := filepath.Join(tmp, "apparmor_restrict")
	orig := apparmorSysctlPath
	apparmorSysctlPath = sysctl
	defer func() { apparmorSysctlPath = orig }()

	_ = os.WriteFile(sysctl, []byte("1\n"), 0o644)
	b := fakeBwrapRunner("--argv0\n", nil, nil)
	err := b.Probe()
	if err == nil || !strings.Contains(err.Error(), "AppArmor") {
		t.Errorf("want AppArmor error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "sudo sysctl") {
		t.Errorf("error should include fix command: %v", err)
	}

	// 值为 0 → 放行
	_ = os.WriteFile(sysctl, []byte("0\n"), 0o644)
	b2 := fakeBwrapRunner("--argv0\n", nil, nil)
	if err := b2.Probe(); err != nil {
		t.Errorf("AppArmor value 0 should pass: %v", err)
	}
}

func TestProbe_AppArmorSysctlMissing(t *testing.T) {
	skipIfWindows(t)
	// sysctl 不存在(非 Ubuntu 24.04)→ 不拦截
	orig := apparmorSysctlPath
	apparmorSysctlPath = filepath.Join(t.TempDir(), "nonexistent")
	defer func() { apparmorSysctlPath = orig }()

	b := fakeBwrapRunner("--argv0\n", nil, nil)
	if err := b.Probe(); err != nil {
		t.Errorf("missing sysctl should not block: %v", err)
	}
}

// TestLinuxBwrapInstallHint 验证首次使用引导按发行版生成正确安装命令。
func TestLinuxBwrapInstallHint(t *testing.T) {
	tests := []struct {
		distro   string
		contains string
	}{
		{"ubuntu", "apt install bubblewrap"},
		{"debian", "apt install bubblewrap"},
		{"linuxmint", "apt install bubblewrap"},
		{"fedora", "dnf install bubblewrap"},
		{"centos", "dnf install bubblewrap"},
		{"arch", "pacman -S bubblewrap"},
		{"alpine", "apk add bubblewrap"},
		{"opensuse", "zypper install bubblewrap"},
		{"unknown-distro", "package repository"},
	}
	for _, tt := range tests {
		t.Run(tt.distro, func(t *testing.T) {
			cmd := bwrapInstallCommandFor(tt.distro)
			if !strings.Contains(cmd, tt.contains) {
				t.Errorf("distro %q: command = %q, want contains %q", tt.distro, cmd, tt.contains)
			}
		})
	}
}

// ============================================================================
// 小缺口补充
// ============================================================================

func TestManager_Name_NilBackend(t *testing.T) {
	m := NewManager(DefaultConfig(), "/tmp/ws")
	if m.Name() != "" {
		t.Errorf("Name() = %q, want empty", m.Name())
	}
}

func TestManager_IsExcluded_EmptyList(t *testing.T) {
	m := NewManager(&Config{}, "/tmp/ws")
	if m.IsExcluded("docker ps") {
		t.Error("empty excludedCommands should not exclude")
	}
}

func TestMatchExcludedPattern_MidWildcard(t *testing.T) {
	// 中间通配(glob)分支
	if !matchExcludedPattern("make build", "make *") {
		t.Error("glob 'make *' should match 'make build'")
	}
	if matchExcludedPattern("make", "make *") {
		t.Error("glob 'make *' should not match bare 'make'")
	}
	// path.Match 分支(通配不在尾部)
	if !matchExcludedPattern("git push origin main", "git push *") {
		t.Error("glob should match")
	}
	if matchExcludedPattern("git status", "git p*sh") {
		t.Error("mid wildcard should not match")
	}
}

func TestCollectMaskSpecs_Dedup(t *testing.T) {
	// 同一路径出现在默认清单 + denyRead → 只生成一条遮蔽
	home := t.TempDir()
	ws := t.TempDir()
	settings := filepath.Join(home, ".waveloom", "settings.json")
	_ = os.MkdirAll(filepath.Dir(settings), 0o755)
	_ = os.WriteFile(settings, []byte("{}"), 0o600)

	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Filesystem:               FilesystemConfig{DenyRead: []string{"~/.waveloom/settings.json"}},
	}
	specs := collectMaskSpecs(home, ws, cfg)
	count := 0
	for _, s := range specs {
		if s.path == settings {
			count++
		}
	}
	if count != 1 {
		t.Errorf("settings.json masked %d times, want 1", count)
	}
}

func TestSplitCompoundCommand_Empty(t *testing.T) {
	got := SplitCompoundCommand("")
	if len(got) != 1 || got[0] != "" {
		t.Errorf("empty command should degrade to single empty, got %v", got)
	}
}

func TestViolationString_AllKinds(t *testing.T) {
	tests := []struct {
		v    Violation
		want string
	}{
		{Violation{Kind: "write", Path: "/a", Detail: "read-only filesystem"}, "write blocked: /a (read-only filesystem)"},
		{Violation{Kind: "read", Path: "/b", Detail: "bound to /dev/null"}, "read masked (returned empty): /b (bound to /dev/null)"},
		{Violation{Kind: "path", Path: "/c", Detail: "tmpfs overlay"}, "path masked (returned ENOENT): /c (tmpfs overlay)"},
		{Violation{Kind: "other", Path: "/d", Detail: "x"}, "other: /d (x)"},
	}
	for _, tt := range tests {
		if got := tt.v.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestExtractPath_None(t *testing.T) {
	if p := extractPath("no path here"); p != "" {
		t.Errorf("extractPath = %q, want empty", p)
	}
}

func TestHomeHistoryFiles(t *testing.T) {
	home := t.TempDir()
	_ = os.WriteFile(filepath.Join(home, ".bash_history"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(home, ".zsh_history"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(home, "normal.txt"), []byte("x"), 0o600)
	_ = os.MkdirAll(filepath.Join(home, ".dir_history"), 0o755)

	files := homeHistoryFiles(home)
	if len(files) != 2 {
		t.Errorf("homeHistoryFiles = %v, want 2 files", files)
	}
	for _, f := range files {
		if !strings.HasSuffix(f, "history") {
			t.Errorf("unexpected file %q", f)
		}
	}
}

func TestHomeHistoryFiles_MissingHome(t *testing.T) {
	if files := homeHistoryFiles(filepath.Join(t.TempDir(), "nope")); files != nil {
		t.Errorf("missing home should return nil, got %v", files)
	}
}

func TestConfigAllowUnsandboxed_Nil(t *testing.T) {
	cfg := &Config{}
	if !cfg.AllowUnsandboxed() {
		t.Error("nil AllowUnsandboxedCommands should default to true")
	}
	f := false
	cfg.AllowUnsandboxedCommands = &f
	if cfg.AllowUnsandboxed() {
		t.Error("explicit false should be respected")
	}
}
