//go:build windows

package skill

import "os/exec"

func setSysProcAttrSkill(cmd *exec.Cmd) {}

func killProcessGroupSkill(pid int) {}
