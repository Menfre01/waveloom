//go:build linux

// bwrap 后端为 Linux 专属(windows_stub.go 在 Windows 提供占位)。
// 测试随实现平台限定,避免 Windows 上 filepath 路径语义差异导致断言失败
// (如 expandPath 的 "/" 前缀断言在 Windows 变为 "\" 前缀)。
package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
	// 2026-09:默认读遮蔽已移除(settings.json / ~/.ssh 不再内置遮蔽)。
	// 固定项:工作区 .git/hooks 防写 → --tmpfs;~/.cache 构建缓存 → --tmpfs
	_ = os.MkdirAll(filepath.Join(home, ".cache"), 0o755)
	_ = os.MkdirAll(filepath.Join(ws, ".git", "hooks"), 0o755)

	return newBwrapBackend(), home, ws
}

func TestBwrapTransform_OffMode_StickerOrder(t *testing.T) {
	b, home, ws := newTestBwrap(t)
	cfg := DefaultConfig()
	// 通用 env 注入(配置驱动):路径值展开为 workspace 相对,URL 原样
	cfg.Env = map[string]string{
		"GOPATH":     "./.waveloom-gopath",
		"GOMODCACHE": "./.waveloom-gomodcache",
		"GOPROXY":    "https://proxy.golang.org,direct",
	}

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
	// 默认读遮蔽已移除(2026-09):settings.json / ~/.ssh / .env 不再出现在 argv
	for _, p := range []string{
		filepath.Join(home, ".waveloom", "settings.json"),
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".env"),
	} {
		if argIndex(argv, p) >= 0 {
			t.Errorf("path should NOT be masked by default: %s (argv=%v)", p, argv)
		}
	}
	// 固定防写遮蔽:tmpfs 覆盖工作区 .git/hooks(防持久化注入)
	assertSubsequence(t, argv, "--tmpfs", filepath.Join(ws, ".git", "hooks"))
	// 默认缓存目录 tmpfs(沙箱内独立缓存)
	assertSubsequence(t, argv, "--tmpfs", filepath.Join(home, ".cache"))

	// 基础文件系统(--tmpfs /tmp 已提前)
	assertSubsequence(t, argv, "--proc", "/proc")
	assertSubsequence(t, argv, "--dev", "/dev")

	// chdir + TMPDIR
	assertSubsequence(t, argv, "--chdir", ws)
	assertSubsequence(t, argv, "--setenv", "TMPDIR", "/tmp")
	// 通用 env 注入:路径值展开到 workspace,非路径值原样
	assertSubsequence(t, argv, "--setenv", "GOMODCACHE", filepath.Join(ws, ".waveloom-gomodcache"))
	assertSubsequence(t, argv, "--setenv", "GOPATH", filepath.Join(ws, ".waveloom-gopath"))
	assertSubsequence(t, argv, "--setenv", "GOPROXY", "https://proxy.golang.org,direct")

	// 能力
	assertSubsequence(t, argv, "--cap-drop", "ALL")
	assertSubsequence(t, argv, "--die-with-parent", "--new-session")

	// 目标命令
	assertSubsequence(t, argv, "--new-session", "bash", "-c", "ls")
}

// TestRegression_EnvInjectionStrippedConflict — env 注入与凭据剥离冲突时
// 必须被忽略(剥离优先),防止配置回填敏感变量进沙箱。
func TestRegression_EnvInjectionStrippedConflict(t *testing.T) {
	b, _, ws := newTestBwrap(t)
	cfg := DefaultConfig()
	cfg.Env = map[string]string{
		"GOPATH":                "./.waveloom-gopath", // 正常注入
		"AWS_SECRET_ACCESS_KEY": "./.waveloom-aws",    // 命中剥离模式 → 忽略
		"MY_SB_TOKEN":           "./.waveloom-tok",    // 命中 *TOKEN* → 忽略
	}

	argv, err := b.Transform("bash", []string{"-c", "ls"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}
	assertSubsequence(t, argv, "--setenv", "GOPATH", filepath.Join(ws, ".waveloom-gopath"))
	for _, a := range argv {
		if a == "AWS_SECRET_ACCESS_KEY" || a == "MY_SB_TOKEN" {
			t.Fatalf("stripped env key leaked into sandbox: %s (argv=%v)", a, argv)
		}
	}
}

func TestBwrapTransform_MissingPathSkipped(t *testing.T) {
	b, _, ws := newTestBwrap(t)
	cfg := DefaultConfig()

	argv, err := b.Transform("bash", []string{"-c", "ls"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}

	// 固定项 /var/run/docker.sock 不存在 → 跳过遮蔽(REGRESSION:--file 在
	// --ro-bind / / 后创建目标会 EROFS;只读根下沙箱内无法创建,无逃逸风险)
	if argIndex(argv, "/var/run/docker.sock") >= 0 {
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

// TestBwrapTransform_SocketMaskTmpfsParent 验证 socket 遮蔽走父目录 tmpfs。
// REGRESSION: bwrap 无法 bind 覆盖已存在的 socket 目标——文件源
// ensure_file() open 对 socket 失败、目录源 ensure_dir() 要求目标为目录
// (CI 实测 "Can't create file/mkdir /var/run/docker.sock: ENOENT",三轮修复
// --bind-data / --ro-bind 文件 / --ro-bind 目录均失败)。tmpfs 父目录是
// bwrap 下唯一可行的 socket 遮蔽:沙箱内 /var/run 清空,docker.sock 不可达。
func TestBwrapTransform_SocketMaskTmpfsParent(t *testing.T) {
	b, home, ws := newTestBwrap(t)
	sockPath := filepath.Join(home, "docker.sock")
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: sockPath}); err != nil {
		t.Fatal(err)
	}
	_ = syscall.Close(fd) // 保留 socket 文件(net.UnixListener Close 会 unlink)

	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Filesystem:               FilesystemConfig{DenyRead: []string{"~" + sockPath[len(home):]}},
	}
	argv, err := b.Transform("bash", []string{"-c", "ls"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}
	// sockPath 的遮蔽必须是 --tmpfs <父目录>(sockPath 不得以任何形式出现在 argv)
	if idx := argIndex(argv, sockPath); idx >= 0 {
		t.Errorf("socket %s must not appear in argv (tmpfs parent mask): %v", sockPath, argv)
	}
	assertSubsequence(t, argv, "--tmpfs", filepath.Dir(sockPath))

	// symlink 父目录场景:/var/run → /run 同型。内核挂载目标解析跟随绝对
	// symlink 时相对挂载 ns 根(新 tmpfs),必须 tmpfs 挂载真实路径。
	realDir := filepath.Join(home, "realrun")
	_ = os.MkdirAll(realDir, 0o755)
	linkDir := filepath.Join(home, "var")
	_ = os.MkdirAll(linkDir, 0o755)
	if err := os.Symlink(realDir, filepath.Join(linkDir, "run")); err != nil {
		t.Fatal(err)
	}
	sock2 := filepath.Join(linkDir, "run", "docker.sock")
	fd2, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Bind(fd2, &syscall.SockaddrUnix{Name: sock2}); err != nil {
		t.Fatal(err)
	}
	_ = syscall.Close(fd2)

	cfg2 := &Config{
		AllowUnsandboxedCommands: &allow,
		Filesystem:               FilesystemConfig{DenyRead: []string{"~" + sock2[len(home):]}},
	}
	argv2, err := b.Transform("bash", []string{"-c", "ls"}, cfg2, ws)
	if err != nil {
		t.Fatal(err)
	}
	// 遮蔽真实父目录(realDir),而非 symlink 路径
	assertSubsequence(t, argv2, "--tmpfs", realDir)
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

// TestBwrapTransform_DefaultNoCredentialMask 回归防护:
// 默认(无显式配置)不遮蔽任何凭据路径(~/.git-credentials、~/.ssh 等),
// 2026-09 决策对齐 Claude Code / Codex;显式 denyRead 时遮蔽生效。
func TestBwrapTransform_DefaultNoCredentialMask(t *testing.T) {
	b, home, ws := newTestBwrap(t)
	_ = os.WriteFile(filepath.Join(home, ".git-credentials"), []byte("https://token@github.com\n"), 0o600)
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o755)

	argv, err := b.Transform("bash", []string{"-c", "git status"}, DefaultConfig(), ws)
	if err != nil {
		t.Fatal(err)
	}
	gitCred := filepath.Join(home, ".git-credentials")
	if argIndex(argv, gitCred) >= 0 {
		t.Errorf(".git-credentials must NOT be masked by default: %v", argv)
	}
	sshDir := filepath.Join(home, ".ssh")
	if argIndex(argv, sshDir) >= 0 {
		t.Errorf("~/.ssh must NOT be masked by default: %v", argv)
	}

	// 显式 denyRead → 遮蔽生效
	cfg := &Config{Filesystem: FilesystemConfig{DenyRead: []string{"~/.git-credentials"}}}
	argv2, err := b.Transform("bash", []string{"-c", "ls"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}
	assertSubsequence(t, argv2, "--bind-data", "3", gitCred)
}

// TestBwrapTransform_ExplicitDenyWins 验证显式遮蔽(denyRead / credentials.files)
// 完整生效——默认读遮蔽移除后,凭据防护完全由显式配置承载(2026-09)。
func TestBwrapTransform_ExplicitDenyWins(t *testing.T) {
	b, home, ws := newTestBwrap(t)
	allow := true
	secretFile := filepath.Join(home, "secret.txt")
	_ = os.WriteFile(secretFile, []byte("x"), 0o600)
	secretDir := filepath.Join(home, "secretdir")
	_ = os.MkdirAll(secretDir, 0o755)

	// denyRead 与 credentials.files 同时配置:均生效(文件 → /dev/null,目录 → tmpfs)
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Filesystem:               FilesystemConfig{DenyRead: []string{"~" + secretFile[len(home):]}},
		Credentials:              CredentialsConfig{Files: []string{secretDir}},
	}
	argv, err := b.Transform("bash", []string{"-c", "ls"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}
	assertSubsequence(t, argv, "--bind-data", "3", secretFile)
	assertSubsequence(t, argv, "--tmpfs", secretDir)
}
