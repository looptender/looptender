//go:build unix

package snapshot

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockAppend takes the log's appenders' lock, waiting for it: it is held
// only across one line's write.
func lockAppend(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX)
		if err != unix.EINTR {
			return err
		}
	}
}

func unlockAppend(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
