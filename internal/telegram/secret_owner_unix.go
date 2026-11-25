//go:build unix

package telegram

import (
	"os"
	"syscall"
)

// privateFile: no group or other permission bits, and owned by this user.
func privateFile(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return info.Mode().Perm()&0o077 == 0 && ok && int(st.Uid) == os.Getuid()
}
