//go:build windows

package tool

import "os/exec"

// SetSysProcAttr is a no-op on Windows — process groups are not supported.
func SetSysProcAttr(cmd *exec.Cmd) {}

// KillProcessGroup is a no-op on Windows — caller should use cmd.Process.Kill().
func KillProcessGroup(cmd *exec.Cmd) {}

// KillProcessGroupByPID is a no-op on Windows — caller should use os.Process.Kill().
func KillProcessGroupByPID(pid int) {}
