//go:build !windows

package tool

import (
	"os/exec"
	"syscall"
)

// SetSysProcAttr sets process-group attributes for shell commands.
// On Unix, this enables Setpgid so the entire process group can be
// killed with a single signal. On Windows, this is a no-op.
func SetSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// KillProcessGroup kills the command's entire process group.
// On Unix, sends SIGKILL to -pgid. On Windows, does nothing
// (caller should use cmd.Process.Kill() instead).
func KillProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pgid := cmd.Process.Pid
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		_ = cmd.Process.Kill()
	}
}

// KillProcessGroupByPID kills the process group identified by the given PID.
// On Unix, sends SIGKILL to -pid. On Windows, does nothing
// (caller should use os.Process.Kill() instead).
func KillProcessGroupByPID(pid int) {
	// REGRESSION: resume 后 PID 可能已被 OS 重用,盲目 kill(-pid) 会误杀无辜进程。
	// 先用 signal 0 做 best-effort 存活探测:进程不存在时跳过,存在时继续。
	// 注意:此检查与 kill 之间仍有竞态窗口(进程可能恰好退出并被重用),
	// 但在实践中显著降低了误杀概率。
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(pid, syscall.Signal(0)); err != nil {
		return // 进程不存在或无权限,安全跳过
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
