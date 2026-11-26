//go:build unix

package lock

import (
	"os"
	"syscall"
)

func openInspection(path string) (*os.File, error) {
	// NOFOLLOW and NONBLOCK also guard replacement with a symlink or FIFO
	// between Lstat and open; the caller verifies the opened identity.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
