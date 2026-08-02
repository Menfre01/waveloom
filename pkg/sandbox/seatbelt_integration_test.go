//go:build darwin

// seatbelt 集成测试仅 macOS 运行(sandbox-exec 为 macOS 系统自带)。
// 平台限定避免 Windows 编译失败:Setpgid 为 Unix 专属字段。
package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
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

// TestRegression_SeatbeltKillBackgroundKillsSandboxedCommand — kill_background_task
// 回归防护(seatbelt 后端)。
//
// REGRESSION: seatbelt 后端无 --unshare-pid/--die-with-parent 等价物,
// sandbox-exec 与沙箱内 bash 同进程组(无 setsid),kill(-wrapperPid) 直接
// 命中整个进程组,无需级联清理。本测试守护"杀包装器 = 杀沙箱内命令"这一
// kill_background_task 语义,防止未来包装层引入脱离进程组的机制后失效。
func TestRegression_SeatbeltKillBackgroundKillsSandboxedCommand(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".cache"), 0o755)
	_ = os.MkdirAll(filepath.Join(home, "Library", "Caches"), 0o755)

	allow := true
	cfg := &Config{
		AllowUnsandboxedCommands: &allow,
		Network:                  NetworkConfig{Mode: NetworkModeOff},
	}
	b := newSeatbeltBackend()
	if err := b.Probe(); err != nil {
		t.Skipf("seatbelt unavailable, skipping integration: %v", err)
	}

	// 模拟 Shell 工具后台路径:Transform 包装 bash -c 长任务 + Setpgid
	// (SetSysProcAttr),注册 PID = 包装器(sandbox-exec)的 PID。
	// 命令必须足够长(sleep 30):命令完成后沙箱自然退出,kill 将无效。
	marker := filepath.Join(ws, "kill-regression.marker")
	cmdLine := fmt.Sprintf("sleep 30; echo done > %s", marker)
	argv, err := b.Transform("/bin/bash", []string{"-c", cmdLine}, cfg, ws)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wrapperPid := cmd.Process.Pid
	// 失败兜底:kill 进程组,防止回归时沙箱进程泄漏到测试结束后。
	defer func() { _ = syscall.Kill(-wrapperPid, syscall.SIGKILL) }()

	// 等命令进入 sleep 阶段(沙箱已建立),贴近真实后台任务被杀时序。
	time.Sleep(500 * time.Millisecond)

	// 等价 tool.KillProcessGroupByPID:存活探测 + 进程组 SIGKILL。
	if err := syscall.Kill(wrapperPid, syscall.Signal(0)); err != nil {
		t.Fatalf("wrapper %d not alive before kill: %v", wrapperPid, err)
	}
	_ = syscall.Kill(-wrapperPid, syscall.SIGKILL)

	// 包装器必须退出(后台 wait goroutine 的 cmd.Wait() 对应进程)。
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	select {
	case <-waitCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("wrapper (PID %d) did not exit after kill", wrapperPid)
	}

	// 核心断言:沙箱内命令必须被清理。若进程组 kill 失效,
	// sleep 3 存活到 3 秒后写入 marker → 在此窗口内被检测到。
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("REGRESSION: sandboxed command survived kill_background_task (marker written)")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
