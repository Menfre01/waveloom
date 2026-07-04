//go:build !windows

package skill

import (
	"os/exec"
	"syscall"
)

func setSysProcAttrSkill(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroupSkill(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
