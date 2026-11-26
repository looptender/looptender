//go:build !darwin && !linux

package lock

import "os"

// Windows LockFileEx would acquire a lock, so it is not a read-only probe.
func inspectFile(*os.File) (string, string) {
	return "unavailable", "read_only_lock_query_unsupported"
}
