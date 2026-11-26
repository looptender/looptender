//go:build darwin || linux

package main

import (
	"golang.org/x/sys/unix"
	"os"
)

// Nonblocking and no-follow apply to the actual open, not a preceding path
// check. The caller validates this descriptor as regular before any read.
func openUsageEvidence(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
