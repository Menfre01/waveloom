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
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
