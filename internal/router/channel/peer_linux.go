package channel

import (
	"net"

	"golang.org/x/sys/unix"
)

// systemPeerUID asks the system which user the process at the other end of
// c runs as.
func systemPeerUID(c *net.UnixConn) (uid int, ok bool) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, false
	}
	raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err == nil {
			uid, ok = int(cred.Uid), true
		}
	})
	return uid, ok
}
