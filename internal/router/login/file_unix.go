//go:build unix

package login

import (
	"os"
	"syscall"
)

func defaultOwnUID() int {
	return os.Getuid()
}

func sameOwner(info os.FileInfo, ownUID int) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == ownUID
}
