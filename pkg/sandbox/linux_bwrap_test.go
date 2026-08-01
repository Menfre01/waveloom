package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// argIndex 返回 argv 中 token 的索引;不存在返回 -1。
func argIndex(argv []string, token string) int {
	for i, a := range argv {
		if a == token {
			return i
		}
	}
	return -1
}

// assertSubsequence 断言 seq 中的 token 按顺序出现在 argv 中(允许间隔)。
// 用于验证贴纸叠贴顺序;避免 argIndex 在重复 token(--bind/--tmpfs)上的歧义。
func assertSubsequence(t *testing.T, argv []string, seq ...string) {
	t.Helper()
	idx := 0
	for _, s := range seq {
		found := -1
		for i := idx; i < len(argv); i++ {
			if argv[i] == s {
				found = i
				break
			}
		}
		if found < 0 {
			t.Fatalf("argv missing %q after idx %d: %v", s, idx, argv)
		}
		idx = found + 1
	}
}

// newTestBwrap 构造测试环境:可控 home + workspace,返回 backend 与目录。
func newTestBwrap(t *testing.T) (*bwrapBackend, string, string) {
	t.Helper()
	home := t.TempDir()
	ws := t.TempDir()
	t.Setenv("HOME", home)

	// 存在性控制:
	// settings.json 存在 → --bind /dev/null
	_ = os.MkdirAll(filepath.Join(home, ".waveloom"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".waveloom", "settings.json"), []byte("{}"), 0o600)
	// ~/.ssh 目录存在 → --tmpfs
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o755)
	// 默认缓存目录存在 → --tmpfs ~/.cache(缺失时跳过,REGRESSION EROFS)
	_ = os.MkdirAll(filepath.Join(home, ".cache"), 0o755)
	// 工作区 .git/hooks 存在 → --tmpfs
	_ = os.MkdirAll(filepath.Join(ws, ".git", "hooks"), 0o755)
	// home/.env 不存在 → --file 3 占位
	// ws/.env 不存在 → --file 3 占位

	return newBwrapBackend(), home, ws
}

func TestBwrapTransform_OffMode_StickerOrder(t *testing.T) {
	b, home, ws := newTestBwrap(t)
	cfg := DefaultConfig()

	argv, err := b.Transform("bash", []string{"-c", "ls"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}

	// 头部
	if argv[0] != "bwrap" {
		t.Errorf("argv[0] = %q, want bwrap", argv[0])
	}
	assertSubsequence(t, argv, "--unshare-user", "--unshare-pid", "--unshare-net") // off 模式默认断网

	// 贴纸顺序:A(ro-bind /)→ tmpfs /tmp 提前清空 → B(bind ws)→ C(遮蔽)
	assertSubsequence(t, argv, "--ro-bind", "/", "/", "--tmpfs", "/tmp")
	assertSubsequence(t, argv, "--bind", ws, ws)
	assertSubsequence(t, argv, "--bind-data", "3", filepath.Join(home, ".waveloom", "settings.json"))

	// 目录遮蔽:tmpfs 覆盖 ~/.ssh 与工作区 .git/hooks
	assertSubsequence(t, argv, "--tmpfs", filepath.Join(ws, ".git", "hooks"))
	assertSubsequence(t, argv, "--tmpfs", filepath.Join(home, ".ssh"))
	// 默认缓存目录 tmpfs(沙箱内独立缓存)
	assertSubsequence(t, argv, "--tmpfs", filepath.Join(home, ".cache"))

	// 基础文件系统(--tmpfs /tmp 已提前)
	assertSubsequence(t, argv, "--proc", "/proc")
	assertSubsequence(t, argv, "--dev", "/dev")

	// chdir + TMPDIR
	assertSubsequence(t, argv, "--chdir", ws)
	assertSubsequence(t, argv, "--setenv", "TMPDIR", "/tmp")

	// 能力
	assertSubsequence(t, argv, "--cap-drop", "ALL")
	assertSubsequence(t, argv, "--die-with-parent", "--new-session")

	// 目标命令
	assertSubsequence(t, argv, "--new-session", "bash", "-c", "ls")
}

func TestBwrapTransform_MissingPathSkipped(t *testing.T) {
	b, home, ws := newTestBwrap(t)
	cfg := DefaultConfig()

	argv, err := b.Transform("bash", []string{"-c", "ls"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}

	// home/.env 不存在 → 跳过遮蔽(REGRESSION:--file 在 --ro-bind / / 后创建
	// 目标会 EROFS;只读根下沙箱内无法创建该文件,无逃逸风险)
	envPath := filepath.Join(home, ".env")
	if argIndex(argv, envPath) >= 0 {
		t.Errorf("missing path should be skipped, but found in argv: %v", argv)
	}
	if argIndex(argv, "--file") >= 0 {
		t.Error("--file should not be used (EROFS regression)")
	}
}

func TestBwrapTransform_NetworkOn_NoUnshareNet(t *testing.T) {
	b, _, ws := newTestBwrap(t)
	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Network:                  NetworkConfig{Mode: NetworkModeOn},
	}

	argv, err := b.Transform("bash", []string{"-c", "ls"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}
	if argIndex(argv, "--unshare-net") >= 0 {
		t.Error("network on should not include --unshare-net")
	}
	assertSubsequence(t, argv, "--unshare-pid", "--ro-bind")
}

func TestBwrapTransform_DenyReadMasks(t *testing.T) {
	b, home, ws := newTestBwrap(t)
	// 显式 denyRead:存在的文件 → /dev/null;存在的目录 → tmpfs
	secretFile := filepath.Join(home, "secret.txt")
	_ = os.WriteFile(secretFile, []byte("x"), 0o600)
	secretDir := filepath.Join(home, "secretdir")
	_ = os.MkdirAll(secretDir, 0o755)

	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Filesystem:               FilesystemConfig{DenyRead: []string{"~" + secretFile[len(home):], "~" + secretDir[len(home):]}},
	}

	argv, err := b.Transform("bash", []string{"-c", "ls"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}
	assertSubsequence(t, argv, "--bind-data", "3", secretFile)
	assertSubsequence(t, argv, "--tmpfs", secretDir)
}

func TestBwrapTransform_EnvVarStrip(t *testing.T) {
	b, _, ws := newTestBwrap(t)
	t.Setenv("MY_TEST_TOKEN_ABC", "secret")
	t.Setenv("MY_TEST_PASSWORD", "pw")
	t.Setenv("MY_TEST_NORMAL_VAR", "ok")

	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Credentials:              CredentialsConfig{EnvVars: []string{"MY_EXTRA_CRED"}},
	}
	t.Setenv("MY_EXTRA_CRED", "x")

	argv, err := b.Transform("bash", []string{"-c", "ls"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}
	if argIndex(argv, "--unsetenv") < 0 {
		t.Fatal("missing --unsetenv")
	}
	assertSubsequence(t, argv, "--unsetenv", "MY_EXTRA_CRED")
	assertSubsequence(t, argv, "--unsetenv", "MY_TEST_PASSWORD")
	assertSubsequence(t, argv, "--unsetenv", "MY_TEST_TOKEN_ABC")
	if argIndex(argv, "--unsetenv") > argIndex(argv, "bash") {
		t.Error("--unsetenv must precede target command")
	}
	if strings.Contains(strings.Join(argv, " "), "MY_TEST_NORMAL_VAR") {
		t.Error("normal env var should not be stripped")
	}
}

func TestBwrapTransform_CapabilitiesKeep(t *testing.T) {
	b, _, ws := newTestBwrap(t)
	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Capabilities:             CapabilitiesConfig{Keep: []string{"net_raw"}},
	}

	argv, err := b.Transform("bash", []string{"-c", "ping localhost"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}
	assertSubsequence(t, argv, "--cap-drop", "ALL")
	assertSubsequence(t, argv, "--cap-add", "net_raw")
}

func TestBwrapTransform_AllowWrite(t *testing.T) {
	b, _, ws := newTestBwrap(t)
	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Filesystem:               FilesystemConfig{AllowWrite: []string{"~/.cache"}},
	}

	argv, err := b.Transform("bash", []string{"-c", "ls"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}
	// allowWrite 展开到 home 下(无需存在,bwrap bind 目标缺失时会创建?不——bwrap 需要目标存在)
	// 仅断言配置项被展开为 bind 参数(目标存在性由 bwrap 处理,测试只验证构造)
	home, _ := os.UserHomeDir()
	cachePath := filepath.Join(home, ".cache")
	if argIndex(argv, cachePath) < 0 {
		t.Errorf("allowWrite not expanded: %v", argv)
	}
}

func TestBwrapProbe_BinaryMissing(t *testing.T) {
	// PATH 指向空目录,bwrap 必然找不到
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	b := newBwrapBackend()
	err := b.Probe()
	if err == nil {
		t.Fatal("Probe should fail when bwrap missing")
	}
	if !strings.Contains(err.Error(), "bwrap not found") {
		t.Errorf("error should mention bwrap not found: %v", err)
	}
}

func TestExpandPath(t *testing.T) {
	home := "/home/u"
	ws := "/proj"
	tests := []struct {
		in   string
		want string
	}{
		{"~/.ssh", "/home/u/.ssh"},
		{"~", "/home/u"},
		{"//etc/hosts", "/etc/hosts"},
		{"./src", "/proj/src"},
		{"src/main.go", "/proj/src/main.go"},
	}
	for _, tt := range tests {
		if got := expandPath(tt.in, home, ws); got != tt.want {
			t.Errorf("expandPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMatchStripPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    bool
	}{
		{"OPENAI_API_KEY", "*_API_KEY", true},
		{"GEMINI_API_KEY", "*_API_KEY", true},
		{"KEYBOARD_LAYOUT", "*_API_KEY", false},
		{"AWS_SECRET_ACCESS_KEY", "*SECRET*", true},
		{"AWS_ACCESS_KEY_ID", "AWS_*", true},
		{"GH_TOKEN", "GH_*", true},
		{"SSH_AUTH_SOCK", "SSH_AUTH_SOCK", true},
		{"DATABASE_URL", "DATABASE_URL", true},
		{"DOCKER_HOST", "DOCKER_*", true},
		{"HOME", "*TOKEN*", false},
	}
	for _, tt := range tests {
		if got := matchStripPattern(tt.name, tt.pattern); got != tt.want {
			t.Errorf("matchStripPattern(%q, %q) = %v, want %v", tt.name, tt.pattern, got, tt.want)
		}
	}
}
