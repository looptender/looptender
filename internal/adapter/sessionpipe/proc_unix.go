//go:build unix

package sessionpipe

import (
	"os/exec"
	"syscall"
)

// detach puts the ask in its own process group, so a signal to the
// supervisor's group (a terminal's Ctrl-C) does not end a delivery under way.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
