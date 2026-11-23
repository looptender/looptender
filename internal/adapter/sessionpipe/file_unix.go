//go:build unix

package sessionpipe

import (
	"fmt"
	"os"
	"syscall"
)

// openEvents opens the events file without blocking: a FIFO put in its place
// would otherwise wait for a writer. A regular file ignores O_NONBLOCK.
func openEvents(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

// fileIdentity names the file itself (device and inode), so a file replaced
// under the same name is read again from its start.
func fileIdentity(_ *os.File, info os.FileInfo) string {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("unix:%d:%d", st.Dev, st.Ino)
}

// othersCanWrite reports whether a user other than the owner can write the
// file. Anyone who can write it can make the session look waiting, ended or
// idle, so it is refused.
func othersCanWrite(_ string, info os.FileInfo) (refused bool, known bool) {
	return info.Mode().Perm()&0o022 != 0, true
}
