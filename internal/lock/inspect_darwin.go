//go:build darwin

package lock

import (
	"os"
	"syscall"
)

func inspectFile(f *os.File) (string, string) {
	// Darwin's F_GETLK observes flock locks as well as record locks. It does
	// not acquire a lock. Flock holders have PID -1, so no identity is inferred.
	query := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_GETLK, &query); err != nil {
		return "unavailable", "lock_query_failed"
	}
	switch query.Type {
	case syscall.F_UNLCK:
		return "none", "no_lock_held"
	case syscall.F_RDLCK, syscall.F_WRLCK:
		return "held", "holder_reference_unavailable"
	default:
		return "unavailable", "lock_query_unknown"
	}
}
