//go:build windows

package sessionpipe

import (
	"fmt"
	"syscall"
)

// procStart names when process pid was created, so a reused process ID is
// not taken for the wake that was recorded; "" when it cannot be read.
func procStart(pid int) string {
	h, err := syscall.OpenProcess(processQueryLimitedInf, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer syscall.CloseHandle(h)
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return ""
	}
	return fmt.Sprintf("windows:%d", created.Nanoseconds())
}
