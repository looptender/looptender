//go:build windows

package snapshot

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// The appenders' lock is one byte far past any log's end: holding it keeps
// other appenders out without blocking reads or writes of the log itself.
const appendLockOffset = 1 << 62

func appendLockOverlapped() *windows.Overlapped {
	return &windows.Overlapped{Offset: uint32(appendLockOffset & 0xffffffff), OffsetHigh: uint32(appendLockOffset >> 32)}
}

func tryLockAppend(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, appendLockOverlapped())
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlockAppend(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, appendLockOverlapped())
}
