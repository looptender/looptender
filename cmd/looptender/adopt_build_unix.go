//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// adoptSupported: a daemon can replace its own image on this platform.
const adoptSupported = true

// execImage replaces this process's image; it returns only on failure.
// Tests replace it.
var execImage = func(path string, argv, env []string) error { return syscall.Exec(path, argv, env) }

// adoptUID is the user a build must belong to. Tests replace it.
var adoptUID = os.Getuid

// adoptFileRefusal says why a daemon does not run the opened file, or "".
// It runs only what this user owns and no other user can change.
func adoptFileRefusal(f *os.File) string {
	info, err := f.Stat()
	if err != nil {
		return "the installed build cannot be read"
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	switch {
	case !ok || int(st.Uid) != adoptUID():
		return "the installed build is not owned by this user"
	case info.Mode().Perm()&0o022 != 0:
		return "the installed build can be changed by other users"
	case info.Mode().Perm()&0o100 == 0:
		return "the installed build is not executable"
	}
	return ""
}

// adoptDirsRefusal says why a daemon does not trust the directories the
// launch path went through, or "": each, and every directory above it,
// must belong to this user or to root and be writable by no one else, even
// with the sticky bit, so that no other user can put a file or link there.
func adoptDirsRefusal(dirs []string) string { return adoptDirsRefusalUpTo(dirs, "") }

// adoptDirsRefusalUpTo stops below stop, for tests; "" checks up to the
// root.
func adoptDirsRefusalUpTo(dirs []string, stop string) string {
	seen := map[string]bool{}
	for _, d := range dirs {
		for d = filepath.Clean(d); !seen[d] && d != stop; d = filepath.Dir(d) {
			seen[d] = true
			info, err := os.Stat(d)
			if err != nil {
				return "a directory of the launch path cannot be read"
			}
			st, ok := info.Sys().(*syscall.Stat_t)
			if !ok || (int(st.Uid) != adoptUID() && st.Uid != 0) {
				return "a directory of the launch path belongs to another user"
			}
			if info.Mode().Perm()&0o022 != 0 {
				return "a directory of the launch path can be written by other users"
			}
			if parent := filepath.Dir(d); parent == d {
				break
			}
		}
	}
	return ""
}

// keepFDAcrossExec clears close-on-exec on f, so a new image inherits it.
func keepFDAcrossExec(f *os.File) (uintptr, error) {
	fd := f.Fd()
	_, err := unix.FcntlInt(fd, unix.F_SETFD, 0)
	return fd, err
}
