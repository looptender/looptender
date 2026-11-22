//go:build unix

package lock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// KeepAcrossExec lets a new program image that replaces this process (exec)
// keep holding the lock: it clears close-on-exec on the lock's descriptor
// and returns the descriptor's number, to be passed to Inherit in the new
// image. The lock stays held throughout, so no other process can take it
// between the two images. restore sets close-on-exec again, for when the
// exec did not happen.
func (l *Lock) KeepAcrossExec() (fd uintptr, restore func(), err error) {
	if l == nil || l.f == nil {
		return 0, nil, errors.New("lock not held")
	}
	fd = l.f.Fd()
	if _, err := unix.FcntlInt(fd, unix.F_SETFD, 0); err != nil {
		return 0, nil, err
	}
	return fd, func() { _, _ = unix.FcntlInt(fd, unix.F_SETFD, unix.FD_CLOEXEC) }, nil
}

// Inherit takes over a lock that an earlier image of this process held and
// handed on with KeepAcrossExec. path is the lock file it must be: a
// descriptor open on any other file is refused. The lock is taken again on
// the descriptor: that keeps it when this process holds it, and fails when
// another process does. On failure the descriptor is left open, so a lock
// this process holds stays held for whatever runs next.
func Inherit(fd uintptr, path string) (*Lock, error) {
	var opened, named unix.Stat_t
	if err := unix.Fstat(int(fd), &opened); err != nil {
		return nil, errors.New("inherited lock descriptor is not open")
	}
	if err := unix.Stat(path, &named); err != nil || opened.Dev != named.Dev || opened.Ino != named.Ino {
		return nil, errors.New("inherited lock descriptor is not the named lock file")
	}
	if err := unix.Flock(int(fd), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrHeld
		}
		return nil, err
	}
	unix.CloseOnExec(int(fd))
	return &Lock{f: os.NewFile(fd, path)}, nil
}
