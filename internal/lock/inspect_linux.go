//go:build linux

package lock

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func inspectFile(f *os.File) (string, string) {
	info, err := f.Stat()
	if err != nil {
		return "unavailable", "lock_query_failed"
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "unavailable", "lock_query_failed"
	}
	table, err := os.Open("/proc/locks")
	if err != nil {
		return "unavailable", "lock_query_failed"
	}
	defer table.Close()
	return inspectLinuxTable(table, uint64(unix.Major(uint64(stat.Dev))), uint64(unix.Minor(uint64(stat.Dev))), stat.Ino)
}
