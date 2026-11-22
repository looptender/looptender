//go:build windows

package snapshot

import (
	"os"

	"golang.org/x/sys/windows"
)

// The appenders' lock is one byte far past any log's end: holding it keeps
// other appenders out without blocking reads or writes of the log itself.
const appendLockOffset = 1 << 62

func lockAppend(f *os.File) error {
	ol := windows.Overlapped{Offset: uint32(appendLockOffset & 0xffffffff), OffsetHigh: uint32(appendLockOffset >> 32)}
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &ol)
}

func unlockAppend(f *os.File) {
	ol := windows.Overlapped{Offset: uint32(appendLockOffset & 0xffffffff), OffsetHigh: uint32(appendLockOffset >> 32)}
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}
