//go:build windows

package sessionpipe

import (
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup  = 0x00000200
	processQueryLimitedInf = 0x1000
	stillActive            = 259
)

// detach starts the ask in its own process group, so a console interrupt to
// the supervisor does not end a delivery under way.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInf, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
