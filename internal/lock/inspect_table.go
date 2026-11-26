package lock

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// /proc/locks is a structured kernel interface. Match device/inode only, never
// a name or PID. Negative results cannot prove absence across PID namespaces or
// remote filesystems, so only a positive held result is reported on Linux.
func inspectLinuxTable(r io.Reader, deviceMajor, deviceMinor, inode uint64) (string, string) {
	const maxTable = 1 << 20
	limited := &io.LimitedReader{R: r, N: maxTable + 1}
	scanner := bufio.NewScanner(limited)
	held := false
	for scanner.Scan() {
		if limited.N <= 0 {
			return "unavailable", "lock_query_failed"
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) > 1 && fields[1] == "->" {
			continue
		} // waiting requests hold nothing
		if len(fields) != 8 {
			return "unavailable", "lock_query_unknown"
		}
		identity := strings.Split(fields[5], ":")
		if len(identity) != 3 {
			return "unavailable", "lock_query_unknown"
		}
		major, e1 := strconv.ParseUint(identity[0], 16, 32)
		minor, e2 := strconv.ParseUint(identity[1], 16, 32)
		ino, e3 := strconv.ParseUint(identity[2], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil {
			return "unavailable", "lock_query_unknown"
		}
		if major != deviceMajor || minor != deviceMinor || ino != inode {
			continue
		}
		if fields[1] != "FLOCK" || fields[2] != "ADVISORY" || (fields[3] != "READ" && fields[3] != "WRITE") || fields[6] != "0" || fields[7] != "EOF" {
			return "unavailable", "lock_query_unknown"
		}
		held = true
	}
	if scanner.Err() != nil || limited.N <= 0 {
		return "unavailable", "lock_query_failed"
	}
	if held {
		return "held", "holder_reference_unavailable"
	}
	return "unavailable", "no_visible_lock"
}

// Keep format documentation adjacent to the reader without exposing identities
// in its output: https://man7.org/linux/man-pages/man5/proc_locks.5.html
