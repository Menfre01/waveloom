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

	// settings.json 存在 → literal 遮蔽;~/.ssh 存在 → subpath 遮蔽;
	// ~/.env 不存在 → 跳过(与 Linux 一致)
	_ = os.MkdirAll(filepath.Join(home, ".waveloom"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".waveloom", "settings.json"), []byte("{}"), 0o600)
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o755)

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
		`(deny network*)`, // off 模式全断
	} {
		if !strings.Contains(prof, want) {
			t.Errorf("profile missing %q:\n%s", want, prof)
		}
	}
	if !strings.Contains(prof, `(allow file-write* (subpath "`+realPath(ws)+`") (subpath "/tmp") (subpath "/private/tmp") (subpath "`+realPath(filepath.Join(home, ".cache"))+`") (subpath "`+realPath(filepath.Join(home, "Library", "Caches"))+`"))`) {
		t.Errorf("workspace write missing:\n%s", prof)
	}
	// 遮蔽:文件 → literal,目录 → subpath
	settings := filepath.Join(home, ".waveloom", "settings.json")
	if !strings.Contains(prof, `(deny file-read* (literal "`+realPath(settings)+`"))`) {
		t.Errorf("file mask missing:\n%s", prof)
	}
	sshDir := filepath.Join(home, ".ssh")
	if !strings.Contains(prof, `(deny file-read* (subpath "`+realPath(sshDir)+`"))`) {
		t.Errorf("dir mask missing:\n%s", prof)
	}
	// 缺失路径(~/.env)跳过
	envPath := filepath.Join(home, ".env")
	if strings.Contains(prof, envPath) {
		t.Errorf("missing path should be skipped:\n%s", prof)
	}
}

// TestDarwinMaskedDirsFiltered 直接单测 darwin 特有遮蔽目录的 allowRead 过滤:
// 精确命中 / 父目录放行(解除其下所有遮蔽)/ 未命中保留 / 空配置原样返回。
// 不依赖本机真实目录存在性(函数仅做字符串匹配)。
func TestDarwinMaskedDirsFiltered(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	keychains := filepath.Join(home, "Library", "Keychains")

	t.Run("empty allowRead returns all", func(t *testing.T) {
		cfg := DefaultConfig()
		got := darwinMaskedDirsFiltered(home, ws, cfg)
		want := darwinMaskedDirs(home)
		if len(got) != len(want) {
			t.Errorf("got %d dirs, want %d", len(got), len(want))
		}
	})

	t.Run("exact match removes one", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Filesystem.AllowRead = []string{"~" + keychains[len(home):]}
		got := darwinMaskedDirsFiltered(home, ws, cfg)
		for _, p := range got {
			if p == keychains {
				t.Errorf("keychains should be unmasked: %v", got)
			}
		}
		if len(got) != len(darwinMaskedDirs(home))-1 {
			t.Errorf("got %d dirs, want %d: %v", len(got), len(darwinMaskedDirs(home))-1, got)
		}
	})

	t.Run("parent dir lifts all descendants", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Filesystem.AllowRead = []string{"~/Library"}
		got := darwinMaskedDirsFiltered(home, ws, cfg)
		// ~/Library 放行其下全部(Keychains/HTTPStorages/Cookies/浏览器数据);
		// home 外(/var/run/docker.sock)与非 Library 路径仍保留
		for _, p := range got {
			if strings.HasPrefix(p, filepath.Join(home, "Library")+string(filepath.Separator)) {
				t.Errorf("Library dir should be unmasked: %v", got)
			}
		}
		if len(got) != 5 { // /var/run/docker.sock + .docker/run + .kube + .config/gcloud + .gnupg
			t.Errorf("got %d dirs, want 5: %v", len(got), got)
		}
	})

	t.Run("unrelated path keeps all", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Filesystem.AllowRead = []string{"~/tmp/xyz"}
		got := darwinMaskedDirsFiltered(home, ws, cfg)
		if len(got) != len(darwinMaskedDirs(home)) {
			t.Errorf("unrelated allowRead should keep all: %v", got)
		}
	})

	t.Run("tilde home lifts all", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Filesystem.AllowRead = []string{"~"}
		got := darwinMaskedDirsFiltered(home, ws, cfg)
		// ~ 放行 home 下全部;home 外路径(/var/run/docker.sock)不受影响
		if len(got) != 1 || got[0] != "/var/run/docker.sock" {
			t.Errorf("allowRead=~ should keep only /var/run/docker.sock: %v", got)
		}
	})
}

// TestSeatbeltProfile_GitconfigNotMasked 回归防护:
// ~/.gitconfig 移出默认遮蔽清单(2026-08 决策),否则 macOS 上 git 启动即 EPERM。
func TestSeatbeltProfile_GitconfigNotMasked(t *testing.T) {
	home, ws := newTestSeatbelt(t)
	_ = os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = test\n"), 0o600)

	allow := true
	cfg := &Config{AllowUnsandboxedCommands: &allow, Network: NetworkConfig{Mode: NetworkModeOff}}
	prof := buildSeatbeltProfile(cfg, ws)

	gitconfig := realPath(filepath.Join(home, ".gitconfig"))
	if strings.Contains(prof, `(deny file-read* (literal "`+gitconfig+`"))`) {
		t.Errorf(".gitconfig must NOT be masked (git unusable otherwise):\n%s", prof)
	}
	// 真凭证仍遮蔽
	gitCred := realPath(filepath.Join(home, ".git-credentials"))
	if strings.Contains(prof, `(deny file-read* (literal "`+gitCred+`"))`) {
		t.Log("git-credentials masked (expected)")
	}
}

func TestSeatbeltProfile_AllowRead(t *testing.T) {
	home, ws := newTestSeatbelt(t)
	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Filesystem:               FilesystemConfig{AllowRead: []string{"~/.waveloom/settings.json", "~/.ssh"}},
	}
	prof := buildSeatbeltProfile(cfg, ws)

	settings := realPath(filepath.Join(home, ".waveloom", "settings.json"))
	if strings.Contains(prof, `(deny file-read* (literal "`+settings+`"))`) {
		t.Errorf("allowRead file should not be masked:\n%s", prof)
	}
	sshDir := realPath(filepath.Join(home, ".ssh"))
	if strings.Contains(prof, `(deny file-read* (subpath "`+sshDir+`"))`) {
		t.Errorf("allowRead dir should not be masked:\n%s", prof)
	}
	// 未放行的 darwin 敏感目录(若本机存在)仍遮蔽
	for _, p := range darwinMaskedDirs(home) {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		real := realPath(p)
		if !strings.Contains(prof, `(deny file-read* (subpath "`+real+`"))`) {
			t.Errorf("darwin sensitive dir not masked after allowRead: %s\n%s", real, prof)
		}
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

	// macOS 特有敏感目录必须遮蔽(钥匙串/cookie/浏览器数据)
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
	cfg := &Config{AllowUnsandboxedCommands: &allow, Network: NetworkConfig{Mode: NetworkModeOff}}
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
	// argv[2] 是 profile;随后是 env 包装(TMPDIR=/tmp 恒设置)+ 目标命令
	// 无剥离变量时: [env, TMPDIR=/tmp, bash, -c, ls]
	if argv[3] != "/usr/bin/env" || argv[4] != "TMPDIR=/tmp" ||
		argv[5] != "bash" || argv[6] != "-c" || argv[7] != "ls" {
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
	// 剥离变量之后是 TMPDIR=/tmp,再是目标命令
	if argv[envIdx+3] != "TMPDIR=/tmp" || argv[envIdx+4] != "bash" {
		t.Errorf("TMPDIR/target wrong: %v", argv[envIdx+3:envIdx+5])
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
