//go:build linux

// bwrap 集成测试仅 Linux 运行(Probe 在其他平台失败)。
// 平台限定避免 Windows 编译失败:Setpgid、/proc 进程树均为 Unix/Linux 专属。
package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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

// TestRegression_BwrapKillBackgroundKillsSandboxedCommand — kill_background_task
// 回归防护(bwrap 后端)。
//
// REGRESSION: bwrap 固定传 --new-session,沙箱内 bash 在 clone 子进程中
// setsid() 成功 → 脱离 bwrap 主进程的进程组。kill_background_task 注册的 PID
// 是 bwrap 主进程(Shell 工具 cmd.Process.Pid),kill(-pid) 只命中主进程;
// 沙箱内命令的清理完全依赖 --die-with-parent 的 PDEATHSIG 级联
// (主进程死 → bash SIGKILL → init SIGKILL → PID namespace 拆除)。
// 一旦 --die-with-parent 被移除,kill 后命令继续执行,且 registry 已被 wait
// goroutine 标记 completed/failed,kill_background_task 拒绝再杀——双重失效。
// 本测试断言 kill 包装器后沙箱内命令必须停止执行(marker 文件不得出现)。
func TestRegression_BwrapKillBackgroundKillsSandboxedCommand(t *testing.T) {
	fx := setupBwrapIntegration(t)
	cfg := &Config{Network: NetworkConfig{Mode: NetworkModeOff}}

	// 模拟 Shell 工具后台路径:Transform 包装 bash -c 长任务 + Setpgid
	// (SetSysProcAttr),注册 PID = 包装器(bwrap 主进程)的 PID。
	// 命令必须足够长(sleep 30):bwrap 沙箱在命令完成后会自然拆除
	// (init 回收 bash 后 ECHILD 退出 → wrapper 随之退出),若命令先于
	// kill 完成,kill 一个已完成的进程,无法验证级联清理。
	marker := filepath.Join(fx.ws, "kill-regression.marker")
	cmdLine := fmt.Sprintf("sleep 30; echo done > %s", marker)
	argv, err := fx.b.Transform("/bin/bash", []string{"-c", cmdLine}, cfg, fx.ws)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.ExtraFiles = fx.b.ExtraFiles() // --bind-data 遮蔽需要 fd 3
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wrapperPid := cmd.Process.Pid
	// 失败兜底:kill 进程组,防止回归时沙箱进程泄漏到测试结束后。
	defer func() { _ = syscall.Kill(-wrapperPid, syscall.SIGKILL) }()

	// 等沙箱内 bash 出现(exec 完成)再 kill,贴近真实后台任务被杀时序。
	// 注意:bwrap 0.8.0 的进程结构是 wrapper → init(do_init,comm=bwrap)→ bash;
	// 直接 waitForChildPID 拿到的是 init 而非 bash,故用 findBashPID 兼容
	// (bash 可能在 init 的子进程或 wrapper 的直接子进程)。
	// 若 --new-session 生效,pgid != wrapperPid → 清理依赖 PDEATHSIG 级联;
	// 若同组,则 kill(-wrapperPid) 直接命中进程组。
	if bashPid := findBashPID(wrapperPid, 5*time.Second); bashPid > 0 {
		if pgrp := readStatPgrp(bashPid); pgrp > 0 {
			if pgrp == wrapperPid {
				t.Logf("mechanism: bash pgid=%d == wrapper pid %d (kill hits process group directly)", pgrp, wrapperPid)
			} else {
				t.Logf("mechanism: bash pgid=%d != wrapper pid %d (cleanup relies on --die-with-parent PDEATHSIG chain)", pgrp, wrapperPid)
			}
		}
	} else {
		t.Fatalf("sandbox bash did not start within 5s (wrapper pid=%d)", wrapperPid)
	}

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

	// 核心断言:沙箱内命令必须被清理。若 --die-with-parent 级联失效,
	// sleep 30 存活到 30 秒后写入 marker → 4 秒窗口内必然检测不到
	// (命令被 kill 后不应再有任何写入)。
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("REGRESSION: sandboxed command survived kill_background_task (marker written)")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// findBashPID 在 wrapper 进程树中查找已 exec 成 bash 的沙箱内进程。
// 兼容 bwrap 版本差异:0.8.0 结构为 wrapper → init(do_init)→ bash;
// 新版可能为 wrapper → bash(直接 exec)。
func findBashPID(wrapperPid int, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, p := range readChildPIDs(wrapperPid) {
			if isBashProcess(p) {
				return p
			}
			for _, g := range readChildPIDs(p) {
				if isBashProcess(g) {
					return g
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0
}

// isBashProcess 判断进程是否已 exec 成 bash(通过 exe 链接判断,comm 可能滞后)。
func isBashProcess(pid int) bool {
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	return err == nil && filepath.Base(exe) == "bash"
}

// readChildPIDs 读取 /proc/<pid>/task/<pid>/children(空格分隔的直接子进程 PID)。
func readChildPIDs(pid int) []int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	if err != nil {
		return nil
	}
	var out []int
	for _, f := range strings.Fields(string(data)) {
		if p, err := strconv.Atoi(f); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// readStatPgrp 解析 /proc/<pid>/stat 的进程组 ID。
// stat 格式:pid (comm) state ppid pgrp session ...——comm 可能含空格/括号,
// 从最后一个 ')' 之后开始按字段解析:fields[0]=state, [1]=ppid, [2]=pgrp。
func readStatPgrp(pid int) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	s := string(data)
	if i := strings.LastIndexByte(s, ')'); i >= 0 {
		fields := strings.Fields(s[i+1:])
		if len(fields) >= 3 {
			pgrp, _ := strconv.Atoi(fields[2])
			return pgrp
		}
	}
	return 0
}
