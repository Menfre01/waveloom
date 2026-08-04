//go:build darwin

// Seatbelt 后端为 macOS 专属。测试随实现平台限定:
// - 避免 Windows 上 filepath 路径语义差异导致断言失败
// - 避免 Linux CI 上进程环境中的 GITHUB_* 变量被动态 strip 列表捕获,
//   导致 env strip 断言与本地开发环境不一致
package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unsetEnv 移除环境变量并在测试结束后恢复原值。
// (testing.T 无 Unsetenv,用 os 层操作保证测试环境可控)
func unsetEnv(t *testing.T, name string) {
	t.Helper()
	orig, had := os.LookupEnv(name)
	_ = os.Unsetenv(name)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(name, orig)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}

// newTestSeatbelt 构造测试环境:可控 home + workspace。
func newTestSeatbelt(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	ws := t.TempDir()
	t.Setenv("HOME", home)

	// 2026-09:默认读遮蔽已移除(settings.json / ~/.ssh 不再内置遮蔽)。
	// 固定项:工作区 .git/hooks 防写 → deny file-read* + file-write*
	_ = os.MkdirAll(filepath.Join(ws, ".git", "hooks"), 0o755)

	return home, ws
}

func TestSeatbeltProfile_Structure(t *testing.T) {
	home, ws := newTestSeatbelt(t)
	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Network:                  NetworkConfig{Mode: NetworkModeOff},
	}

	prof := buildSeatbeltProfile(cfg, ws)

	// 基础结构
	for _, want := range []string{
		`(version 1)`,
		`(import "system.sb")`,
		`(deny default)`,
		`(allow process*)`,
		`(allow signal)`, // REGRESSION: 缺 signal 放行时沙箱内 kill EPERM,Shell 中断/超时清理失效
		`(deny network*)`, // off 模式全断
	} {
		if !strings.Contains(prof, want) {
			t.Errorf("profile missing %q:\n%s", want, prof)
		}
	}
	if !strings.Contains(prof, `(allow file-write* (subpath "`+realPath(ws)+`") (subpath "/tmp") (subpath "/private/tmp") (subpath "`+realPath(filepath.Join(home, ".cache"))+`") (subpath "`+realPath(filepath.Join(home, "Library", "Caches"))+`"))`) {
		t.Errorf("workspace write missing:\n%s", prof)
	}
	// 默认读遮蔽已移除(2026-09):settings.json / ~/.ssh 不再出现 deny 规则
	settings := filepath.Join(home, ".waveloom", "settings.json")
	if strings.Contains(prof, `(deny file-read* (literal "`+realPath(settings)+`"))`) {
		t.Errorf("settings.json must NOT be masked by default:\n%s", prof)
	}
	sshDir := filepath.Join(home, ".ssh")
	if strings.Contains(prof, `(deny file-read* (subpath "`+realPath(sshDir)+`"))`) {
		t.Errorf("~/.ssh must NOT be masked by default:\n%s", prof)
	}
	// 固定防写遮蔽:ws/.git/hooks deny file-read* + file-write*(防持久化注入)
	hooksDir := realPath(filepath.Join(ws, ".git", "hooks"))
	if !strings.Contains(prof, `(deny file-read* (subpath "`+hooksDir+`"))`) {
		t.Errorf("git hooks read mask missing:\n%s", prof)
	}
	if !strings.Contains(prof, `(deny file-write* (subpath "`+hooksDir+`"))`) {
		t.Errorf("git hooks write mask missing (persistent injection protection):\n%s", prof)
	}
}

// TestDarwinMaskedDirs 验证固定防逃逸遮蔽仅含 docker.sock 两项
// (2026-09:钥匙串/cookie/浏览器等凭据读遮蔽移除,改由 denyRead 显式配置)。
func TestDarwinMaskedDirs(t *testing.T) {
	home := t.TempDir()
	dirs := darwinMaskedDirs(home)
	want := []string{
		"/var/run/docker.sock",
		filepath.Join(home, ".docker", "run", "docker.sock"),
	}
	if len(dirs) != len(want) {
		t.Fatalf("darwinMaskedDirs = %v, want %v", dirs, want)
	}
	for i := range want {
		if dirs[i] != want[i] {
			t.Errorf("darwinMaskedDirs[%d] = %q, want %q", i, dirs[i], want[i])
		}
	}
}

// TestSeatbeltProfile_DefaultNoCredentialMask 回归防护:
// 默认(无显式配置)不遮蔽凭据文件(.gitconfig / .git-credentials),
// 2026-09 决策对齐 Claude Code / Codex;显式 denyRead 时遮蔽生效。
func TestSeatbeltProfile_DefaultNoCredentialMask(t *testing.T) {
	home, ws := newTestSeatbelt(t)
	_ = os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = test\n"), 0o600)
	gitCred := filepath.Join(home, ".git-credentials")
	_ = os.WriteFile(gitCred, []byte("https://token@github.com\n"), 0o600)

	allow := true
	cfg := &Config{AllowUnsandboxedCommands: &allow, Network: NetworkConfig{Mode: NetworkModeOff}}
	prof := buildSeatbeltProfile(cfg, ws)

	gitconfig := realPath(filepath.Join(home, ".gitconfig"))
	if strings.Contains(prof, `(deny file-read* (literal "`+gitconfig+`"))`) {
		t.Errorf(".gitconfig must NOT be masked by default:\n%s", prof)
	}
	gitCredReal := realPath(gitCred)
	if strings.Contains(prof, `(deny file-read* (literal "`+gitCredReal+`"))`) {
		t.Errorf(".git-credentials must NOT be masked by default:\n%s", prof)
	}

	// 显式 denyRead → 遮蔽生效
	cfg2 := &Config{
		AllowUnsandboxedCommands: &allow,
		Filesystem:               FilesystemConfig{DenyRead: []string{"~/.git-credentials"}},
	}
	prof2 := buildSeatbeltProfile(cfg2, ws)
	if !strings.Contains(prof2, `(deny file-read* (literal "`+gitCredReal+`"))`) {
		t.Errorf("explicit denyRead should mask .git-credentials:\n%s", prof2)
	}
}

func TestSeatbeltProfile_NetworkOn(t *testing.T) {
	_, ws := newTestSeatbelt(t)
	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Network:                  NetworkConfig{Mode: NetworkModeOn},
	}
	prof := buildSeatbeltProfile(cfg, ws)
	if !strings.Contains(prof, "(allow network*)") {
		t.Error("network on should allow network")
	}
	if strings.Contains(prof, "(deny network*)") {
		t.Error("network on should not deny network")
	}
}

func TestSeatbeltProfile_DenyRead(t *testing.T) {
	home, ws := newTestSeatbelt(t)
	secretFile := filepath.Join(home, "secret.txt")
	_ = os.WriteFile(secretFile, []byte("x"), 0o600)
	secretDir := filepath.Join(home, "secretdir")
	_ = os.MkdirAll(secretDir, 0o755)

	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Filesystem:               FilesystemConfig{DenyRead: []string{"~secret.txt"}},
	}
	// 注意:denyRead 用 ~/ 前缀语义,这里直接传绝对路径验证 append 逻辑
	cfg.Credentials = CredentialsConfig{Files: []string{secretFile, secretDir}}
	_ = home

	prof := buildSeatbeltProfile(cfg, ws)
	if !strings.Contains(prof, `(deny file-read* (literal "`+realPath(secretFile)+`"))`) {
		t.Errorf("denyRead file missing:\n%s", prof)
	}
	if !strings.Contains(prof, `(deny file-read* (subpath "`+realPath(secretDir)+`"))`) {
		t.Errorf("denyRead dir missing:\n%s", prof)
	}
}

func TestSeatbeltProfile_AllowWrite(t *testing.T) {
	home, ws := newTestSeatbelt(t)
	cacheDir := filepath.Join(home, ".cache")
	_ = os.MkdirAll(cacheDir, 0o755)

	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Filesystem:               FilesystemConfig{AllowWrite: []string{"~/.cache"}},
	}
	prof := buildSeatbeltProfile(cfg, ws)
	if !strings.Contains(prof, `(subpath "`+realPath(cacheDir)+`")`) {
		t.Errorf("allowWrite missing from write subpaths:\n%s", prof)
	}
}

func TestSeatbeltProfile_DarwinSensitiveDirs(t *testing.T) {
	_, ws := newTestSeatbelt(t)
	home, _ := os.UserHomeDir()

	allow := true
	cfg := &Config{AllowUnsandboxedCommands: &allow, Network: NetworkConfig{Mode: NetworkModeOn}}
	prof := buildSeatbeltProfile(cfg, ws)

	// 固定防逃逸目录(docker.sock)必须遮蔽
	for _, p := range darwinMaskedDirs(home) {
		if _, err := os.Stat(p); err != nil {
			continue // 本机不存在 → 跳过
		}
		real := realPath(p)
		if !strings.Contains(prof, `(deny file-read* (subpath "`+real+`"))`) {
			t.Errorf("darwin sensitive dir not masked: %s\n%s", real, prof)
		}
	}
}

func TestSeatbeltTransform_NoEnvStrip(t *testing.T) {
	home, ws := newTestSeatbelt(t)
	unsetEnv(t, "SSH_AUTH_SOCK") // 屏蔽本机真实环境变量,确保无剥离变量
	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Network:                  NetworkConfig{Mode: NetworkModeOff},
		Env: map[string]string{
			"GOPATH":  "./.waveloom-gopath",
			"GOPROXY": "https://proxy.golang.org,direct",
		},
	}
	_ = home

	b := newSeatbeltBackend()
	argv, err := b.Transform("bash", []string{"-c", "ls"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != "sandbox-exec" {
		t.Errorf("argv[0] = %q, want sandbox-exec", argv[0])
	}
	if argv[1] != "-p" {
		t.Errorf("argv[1] = %q, want -p", argv[1])
	}
	// argv[2] 是 profile;随后是 env 包装(TMPDIR 恒设置 + cfg.Env 注入)+ 目标命令
	// 无剥离变量时: [env, TMPDIR=/tmp, GOPATH=..., GOPROXY=..., bash, -c, ls]
	if argv[3] != "/usr/bin/env" || argv[4] != "TMPDIR=/tmp" ||
		argv[5] != "GOPATH="+filepath.Join(ws, ".waveloom-gopath") ||
		argv[6] != "GOPROXY=https://proxy.golang.org,direct" ||
		argv[7] != "bash" || argv[8] != "-c" || argv[9] != "ls" {
		t.Errorf("target command wrong: %v", argv[3:])
	}
}

func TestSeatbeltTransform_EnvStrip(t *testing.T) {
	_, ws := newTestSeatbelt(t)
	t.Setenv("MY_SB_TOKEN", "secret")
	unsetEnv(t, "SSH_AUTH_SOCK")
	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Network:                  NetworkConfig{Mode: NetworkModeOff},
	}

	b := newSeatbeltBackend()
	argv, err := b.Transform("bash", []string{"-c", "ls"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}
	// 剥离变量存在 → 插入 /usr/bin/env -u NAME
	envIdx := -1
	for i, a := range argv {
		if a == "/usr/bin/env" {
			envIdx = i
			break
		}
	}
	if envIdx < 0 {
		t.Fatalf("missing env wrapper: %v", argv)
	}
	if argv[envIdx+1] != "-u" || argv[envIdx+2] != "MY_SB_TOKEN" {
		t.Errorf("env strip wrong: %v", argv[envIdx:envIdx+3])
	}
	// 剥离变量之后是 TMPDIR 赋值,再是目标命令(无 cfg.Env 时)
	if argv[envIdx+3] != "TMPDIR=/tmp" ||
		argv[envIdx+4] != "bash" {
		t.Errorf("env assignments/target wrong: %v", argv[envIdx+3:envIdx+5])
	}
}

func TestSeatbeltProbe_Missing(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	b := newSeatbeltBackend()
	err := b.Probe()
	if err == nil {
		t.Fatal("Probe should fail when sandbox-exec missing")
	}
	if !strings.Contains(err.Error(), "sandbox-exec not found") {
		t.Errorf("error should mention sandbox-exec: %v", err)
	}
}

func TestSeatbeltProbe_SmokeFails(t *testing.T) {
	b := newSeatbeltBackend()
	b.bin = "true" // LookPath 可解析
	b.runCombined = func(name string, args ...string) ([]byte, error) {
		return nil, errors.New("operation not permitted")
	}
	err := b.Probe()
	if err == nil || !strings.Contains(err.Error(), "smoke test failed") {
		t.Errorf("want smoke test error, got: %v", err)
	}
}

func TestSeatbeltProbe_OK(t *testing.T) {
	b := newSeatbeltBackend()
	b.bin = "true"
	b.runCombined = func(name string, args ...string) ([]byte, error) {
		return nil, nil
	}
	if err := b.Probe(); err != nil {
		t.Errorf("Probe should pass: %v", err)
	}
}

func TestSbplString_Escape(t *testing.T) {
	if got := sbplString(`a"b\c`); got != `"a\"b\\c"` {
		t.Errorf("sbplString = %q", got)
	}
	if got := sbplString(`/plain/path`); got != `"/plain/path"` {
		t.Errorf("sbplString = %q", got)
	}
}

// TestSeatbeltExtraFiles_Nil 验证 Seatbelt 后端无 fd 需求。
func TestSeatbeltExtraFiles_Nil(t *testing.T) {
	b := newSeatbeltBackend()
	// Seatbelt 不实现 ExtraFiles 接口 → manager 返回 nil
	m := NewManager(DefaultConfig(), "/tmp")
	m.SetBackend(b)
	if m.ExtraFiles() != nil {
		t.Error("seatbelt ExtraFiles should be nil")
	}
	if m.Name() != "seatbelt" {
		t.Errorf("Name() = %q, want seatbelt", m.Name())
	}
}
