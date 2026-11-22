//go:build unix

package snapshot

import (
	"os"

	"golang.org/x/sys/unix"
)

// tryLockAppend takes the log's appenders' lock if it is free; it is held
// only across one line's write.
func tryLockAppend(f *os.File) (bool, error) {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		switch err {
		case nil:
			return true, nil
		case unix.EWOULDBLOCK:
			return false, nil
		case unix.EINTR:
			continue
		}
		return false, err
	}
}

func unlockAppend(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
