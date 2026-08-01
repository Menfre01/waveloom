package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSeatbeltIntegration_RealExec 真实 sandbox-exec 端到端验证。
// 仅在本机存在 sandbox-exec(macOS)时运行,否则自动跳过。
// 覆盖:遮蔽不可读、只读根、工作区可写、网络断连、环境变量剥离。
func TestSeatbeltIntegration_RealExec(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// 默认缓存目录需存在(Seatbelt 无 mkdir 兜底,~ 只读;真实用户 ~/.cache 存在)
	_ = os.MkdirAll(filepath.Join(home, ".cache"), 0o755)
	_ = os.MkdirAll(filepath.Join(home, "Library", "Caches"), 0o755)

	// 测试数据:遮蔽文件 + 遮蔽目录 + 普通文件
	secretFile := filepath.Join(ws, "secret.txt")
	_ = os.WriteFile(secretFile, []byte("top-secret"), 0o600)
	secretDir := filepath.Join(ws, "masked-dir")
	_ = os.MkdirAll(secretDir, 0o755)
	_ = os.WriteFile(filepath.Join(secretDir, "inside.txt"), []byte("hidden"), 0o600)
	normalFile := filepath.Join(ws, "normal.txt")
	_ = os.WriteFile(normalFile, []byte("visible"), 0o644)

	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Network:                  NetworkConfig{Mode: NetworkModeOff},
		Filesystem:               FilesystemConfig{DenyRead: []string{secretFile, secretDir}},
	}

	b := newSeatbeltBackend()
	if err := b.Probe(); err != nil {
		t.Skipf("seatbelt unavailable, skipping integration: %v", err)
	}

	run := func(cmdBin string, cmdArgs ...string) (string, error) {
		t.Helper()
		argv, err := b.Transform(cmdBin, cmdArgs, cfg, ws)
		if err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // 测试构造的 argv(非用户输入),gosec 命令注入误报
		out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		return string(out), err
	}

	// 1. 遮蔽文件不可读(EPERM)
	out, err := run("/bin/cat", secretFile)
	if err == nil || !strings.Contains(out, "Operation not permitted") {
		t.Errorf("masked file readable: out=%q err=%v", out, err)
	}

	// 2. 遮蔽目录不可读(EPERM)
	out, err = run("/bin/cat", filepath.Join(secretDir, "inside.txt"))
	if err == nil || !strings.Contains(out, "Operation not permitted") {
		t.Errorf("masked dir readable: out=%q err=%v", out, err)
	}

	// 2.5 macOS 特有遮蔽(钥匙串/cookie):集成测试无法覆盖真实路径——
	// profile 基于测试 HOME(t.Setenv 重定向)构建,管不到真实 ~/Library/Keychains。
	// 由单元测试 TestSeatbeltProfile_DarwinSensitiveDirs(真实 home 的 profile
	// 构造断言)+ 真实环境手动验证(ls ~/Library/Keychains → EPERM)兜底。

	// 3. 普通文件可读
	out, err = run("/bin/cat", normalFile)
	if err != nil || !strings.Contains(out, "visible") {
		t.Errorf("normal file unreadable: out=%q err=%v", out, err)
	}

	// 4. 工作区可写
	out, err = run("/usr/bin/touch", filepath.Join(ws, "new.txt"))
	if err != nil {
		t.Errorf("workspace write failed: out=%q err=%v", out, err)
	}

	// 5. 工作区外写被拒(只读根)
	outside := filepath.Join(t.TempDir(), "out.txt")
	out, err = run("/usr/bin/touch", outside)
	if err == nil {
		t.Errorf("outside write should fail: out=%q", out)
	}

	// 6. 网络断连(curl 短超时)
	out, err = run("/usr/bin/curl", "-s", "--max-time", "3", "https://example.com")
	if err == nil && strings.Contains(out, "example") {
		t.Errorf("network should be denied: out=%q", out)
	}

	// 7. bash -c 复合命令 + 环境变量剥离
	t.Setenv("SB_INTEGRATION_TOKEN", "should-not-leak")
	argv, err := b.Transform("/bin/bash", []string{"-c", "echo TOKEN_VISIBLE=$SB_INTEGRATION_TOKEN"}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}
	outBytes, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	out = string(outBytes)
	if err != nil {
		t.Errorf("bash composite failed: %v", err)
	}
	if strings.Contains(out, "should-not-leak") {
		t.Errorf("env var leaked into sandbox: %q", out)
	}
	if !strings.Contains(out, "TOKEN_VISIBLE=") {
		t.Errorf("bash -c output missing: %q", out)
	}

	// 8. TMPDIR=/tmp(env 包装)+ /tmp 可写 + ~/.cache 可写
	out, err = run("/bin/bash", "-c", "echo TMPDIR=$TMPDIR && touch /tmp/sb_tmp_test && echo tmp-ok && touch $HOME/.cache/sb_cache_test && echo cache-ok")
	if err != nil {
		t.Errorf("tmp/cache write failed: out=%q err=%v", out, err)
	}
	if !strings.Contains(out, "TMPDIR=/tmp") || !strings.Contains(out, "tmp-ok") || !strings.Contains(out, "cache-ok") {
		t.Errorf("TMPDIR/tmp/cache assertions missing: %q", out)
	}

	// 9. 违规注解端到端:只读根 EPERM → <sandbox_violations> write blocked
	out, err = run("/usr/bin/touch", filepath.Join(t.TempDir(), "outside2.txt"))
	if err == nil {
		t.Fatal("outside write should fail")
	}
	annotated := AnnotateViolations(out)
	if !strings.Contains(annotated, "<sandbox_violations>") ||
		!strings.Contains(annotated, "write blocked") ||
		strings.Contains(annotated, "read masked") {
		t.Errorf("violation annotation missing: %q", annotated)
	}
}

// TestSeatbeltIntegration_NetworkOn 网络 on:遮蔽路径仍 EPERM(遮蔽 ≠ 只读)、
// 直连可用。
func TestSeatbeltIntegration_NetworkOn(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	secretFile := filepath.Join(ws, "secret.txt")
	_ = os.WriteFile(secretFile, []byte("top-secret"), 0o600)

	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Network:                  NetworkConfig{Mode: NetworkModeOn},
		Filesystem:               FilesystemConfig{DenyRead: []string{secretFile}},
	}
	b := newSeatbeltBackend()
	if err := b.Probe(); err != nil {
		t.Skipf("seatbelt unavailable, skipping: %v", err)
	}
	run := func(cmdBin string, cmdArgs ...string) (string, error) {
		t.Helper()
		argv, err := b.Transform(cmdBin, cmdArgs, cfg, ws)
		if err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // 测试构造的 argv
		out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		return string(out), err
	}

	// 遮蔽文件:EPERM(非 EROFS——Seatbelt 无 EROFS,若返回 Read-only 说明实现错)
	out, err := run("/bin/cat", secretFile)
	if err == nil || !strings.Contains(out, "Operation not permitted") {
		t.Errorf("network on masked file should EPERM: out=%q err=%v", out, err)
	}
	if strings.Contains(out, "Read-only") {
		t.Errorf("masked file is read-only (should be EPERM): %q", out)
	}
	if strings.Contains(out, "top-secret") {
		t.Errorf("masked file content leaked: %q", out)
	}

	// 直连冒烟(curl 存在时)
	if _, lookErr := exec.LookPath("curl"); lookErr == nil {
		out, err = run("/usr/bin/curl", "-s", "--max-time", "5", "https://example.com")
		if err != nil || !strings.Contains(out, "example") {
			t.Errorf("network on should reach example.com: out=%q err=%v", out, err)
		}
	}
}
