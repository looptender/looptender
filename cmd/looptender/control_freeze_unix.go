//go:build darwin || linux

package main

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func freezeSupported() bool { return true }
func openFreezeRecord(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
func syncFreezeDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func freezeNativeID(info os.FileInfo) string {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%d", s.Dev, s.Ino)
}
