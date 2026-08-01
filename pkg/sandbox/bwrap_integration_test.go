package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bwrapFixture 是 bwrap 集成测试的共享环境。
type bwrapFixture struct {
	b          *bwrapBackend
	ws, home   string
	secretFile string // 遮蔽文件(存在)
	secretDir  string // 遮蔽目录(存在)
	normalFile string // 普通文件
}

// setupBwrapIntegration 构造测试环境;bwrap 不可用时返回 nil(调用方 Skip)。
func setupBwrapIntegration(t *testing.T) *bwrapFixture {
	t.Helper()
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// 默认缓存目录存在 → --tmpfs ~/.cache(缺失时 bwrap mkdir EROFS,REGRESSION)
	_ = os.MkdirAll(filepath.Join(home, ".cache"), 0o755)

	fx := &bwrapFixture{
		ws:         ws,
		home:       home,
		secretFile: filepath.Join(ws, "secret.txt"),
		secretDir:  filepath.Join(ws, "masked-dir"),
		normalFile: filepath.Join(ws, "normal.txt"),
	}
	_ = os.WriteFile(fx.secretFile, []byte("top-secret"), 0o600)
	_ = os.MkdirAll(fx.secretDir, 0o755)
	_ = os.WriteFile(filepath.Join(fx.secretDir, "inside.txt"), []byte("hidden"), 0o600)
	_ = os.WriteFile(fx.normalFile, []byte("visible"), 0o644)

	fx.b = newBwrapBackend()
	if err := fx.b.Probe(); err != nil {
		t.Skipf("bwrap unavailable, skipping integration: %v", err)
	}
	return fx
}

// run 在沙箱内执行命令(可指定 cfg 区分 off/on 网络)。
func (fx *bwrapFixture) run(t *testing.T, cfg *Config, cmdBin string, cmdArgs ...string) (string, error) {
	t.Helper()
	argv, err := fx.b.Transform(cmdBin, cmdArgs, cfg, fx.ws)
	if err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // 测试构造的 argv(非用户输入)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.ExtraFiles = fx.b.ExtraFiles() // --bind-data 遮蔽需要 fd 3
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestBwrapIntegration_RealExec 真实 bwrap 端到端验证(网络 off)。
func TestBwrapIntegration_RealExec(t *testing.T) {
	fx := setupBwrapIntegration(t)
	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Network:                  NetworkConfig{Mode: NetworkModeOff},
		Filesystem:               FilesystemConfig{DenyRead: []string{fx.secretFile, fx.secretDir}},
	}

	// 1. --bind-data 遮蔽文件:读返回空(不报错,内容为空——Linux 语义)
	out, err := fx.run(t, cfg, "/bin/cat", fx.secretFile)
	if err != nil {
		t.Errorf("masked file cat errored (want empty output): out=%q err=%v", out, err)
	}
	if strings.Contains(out, "top-secret") {
		t.Errorf("masked file content leaked: %q", out)
	}

	// 2. tmpfs 遮蔽目录:路径不存在(ENOENT)
	out, err = fx.run(t, cfg, "/bin/cat", filepath.Join(fx.secretDir, "inside.txt"))
	if err == nil || !strings.Contains(out, "No such file or directory") {
		t.Errorf("masked dir should ENOENT: out=%q err=%v", out, err)
	}

	// 3. 普通文件可读
	out, err = fx.run(t, cfg, "/bin/cat", fx.normalFile)
	if err != nil || !strings.Contains(out, "visible") {
		t.Errorf("normal file unreadable: out=%q err=%v", out, err)
	}

	// 4. 工作区可写(/bin/sh + 重定向,兼容 alpine busybox)
	out, err = fx.run(t, cfg, "/bin/sh", "-c", "echo x > "+filepath.Join(fx.ws, "new.txt"))
	if err != nil {
		t.Errorf("workspace write failed: out=%q err=%v", out, err)
	}

	// 5. 工作区外写被拒(只读根 EROFS)
	outside := filepath.Join(t.TempDir(), "out.txt")
	out, err = fx.run(t, cfg, "/bin/sh", "-c", "echo x > "+outside)
	if err == nil {
		t.Errorf("outside write should fail: out=%q", out)
	}

	// 6. 网络断连(有 curl 才测;无 curl 用 /dev/tcp fallback)
	netOut, netErr := fx.run(t, cfg, "/bin/sh", "-c", "echo > /dev/tcp/example.com/80 2>&1; echo rc=$?")
	if strings.Contains(netOut, "rc=0") {
		t.Errorf("network should be denied (off): %q", netOut)
	} else if netErr != nil && strings.Contains(netOut, "example") {
		t.Errorf("unexpected: %q err=%v", netOut, netErr)
	}

	// 7. 环境变量剥离
	t.Setenv("SB_INTEGRATION_TOKEN", "should-not-leak")
	out, err = fx.run(t, cfg, "/bin/sh", "-c", "echo TOKEN_VISIBLE=$SB_INTEGRATION_TOKEN")
	if err != nil {
		t.Errorf("sh composite failed: %v", err)
	}
	if strings.Contains(out, "should-not-leak") {
		t.Errorf("env var leaked into sandbox: %q", out)
	}
	if !strings.Contains(out, "TOKEN_VISIBLE=") {
		t.Errorf("sh -c output missing: %q", out)
	}

	// 8. TMPDIR=/tmp 生效 + /tmp 可写 + ~/.cache 可写(REGRESSION 修复验证)
	out, err = fx.run(t, cfg, "/bin/sh", "-c", "echo TMPDIR=$TMPDIR && touch /tmp/sb_tmp_test && echo tmp-ok && touch $HOME/.cache/sb_cache_test && echo cache-ok")
	if err != nil {
		t.Errorf("tmp/cache write failed: out=%q err=%v", out, err)
	}
	if !strings.Contains(out, "TMPDIR=/tmp") || !strings.Contains(out, "tmp-ok") || !strings.Contains(out, "cache-ok") {
		t.Errorf("TMPDIR/tmp/cache assertions missing: %q", out)
	}

	// 9. --chdir workspace 生效
	out, err = fx.run(t, cfg, "/bin/pwd")
	if err != nil || !strings.Contains(out, fx.ws) {
		t.Errorf("chdir to workspace failed: out=%q err=%v", out, err)
	}
}

// TestBwrapIntegration_NetworkOn 规格书核心:网络 on 时遮蔽路径 → 空/ENOENT,
// 非 EROFS(遮蔽是"不可读"而非"只读"的真实验证)。
func TestBwrapIntegration_NetworkOn(t *testing.T) {
	fx := setupBwrapIntegration(t)
	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Network:                  NetworkConfig{Mode: NetworkModeOn},
		Filesystem:               FilesystemConfig{DenyRead: []string{fx.secretFile, fx.secretDir}},
	}

	// 遮蔽文件:读空(非 EROFS——若 EROFS 说明遮蔽实现成只读了)
	out, err := fx.run(t, cfg, "/bin/cat", fx.secretFile)
	if err != nil {
		t.Errorf("network on masked file errored (want empty, NOT EROFS): out=%q err=%v", out, err)
	}
	if strings.Contains(out, "Read-only file system") {
		t.Errorf("masked file is read-only (should be masked empty): %q", out)
	}
	if strings.Contains(out, "top-secret") {
		t.Errorf("masked file content leaked: %q", out)
	}

	// 遮蔽目录:ENOENT(非 EROFS)
	out, err = fx.run(t, cfg, "/bin/cat", filepath.Join(fx.secretDir, "inside.txt"))
	if err == nil || !strings.Contains(out, "No such file or directory") {
		t.Errorf("network on masked dir should ENOENT: out=%q err=%v", out, err)
	}
	if strings.Contains(out, "Read-only file system") {
		t.Errorf("masked dir is read-only (should be ENOENT): %q", out)
	}

	// 工作区外写仍被拒(网络 on 不影响文件边界)
	outside := filepath.Join(t.TempDir(), "out.txt")
	out, err = fx.run(t, cfg, "/bin/sh", "-c", "echo x > "+outside)
	if err == nil {
		t.Errorf("network on outside write should fail: out=%q", out)
	}
}

// TestBwrapIntegration_ViolationAnnotation 违规注解端到端:
// 真实沙箱 stderr → <sandbox_violations> 块。
func TestBwrapIntegration_ViolationAnnotation(t *testing.T) {
	fx := setupBwrapIntegration(t)
	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Network:                  NetworkConfig{Mode: NetworkModeOff},
	}

	// 只读根写失败(EROFS)→ 注解为 write blocked
	out, err := fx.run(t, cfg, "/bin/sh", "-c", "echo x > /etc/sb_violation_test 2>&1")
	if err == nil {
		t.Fatal("write to /etc should fail")
	}
	annotated := AnnotateViolations(out)
	if !strings.Contains(annotated, "<sandbox_violations>") ||
		!strings.Contains(annotated, "write blocked") {
		t.Errorf("violation annotation missing: %q", annotated)
	}
}
