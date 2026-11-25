//go:build darwin

package sessionpipe

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// procStart names when process pid started, so a reused process ID is not
// taken for the wake that was recorded; "" when it cannot be read.
func procStart(pid int) string {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || k == nil || int(k.Proc.P_pid) != pid {
		return ""
	}
	t := k.Proc.P_starttime
	return fmt.Sprintf("darwin:%d.%06d", t.Sec, t.Usec)
}
