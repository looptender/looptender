//go:build unix

package main

import (
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestHelperHoldLock(t *testing.T) {
	path := os.Getenv("HOLD_LOCK")
	if path == "" {
		t.Skip("helper process for lock tests")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(os.Getenv("HOLD_READY"), nil, 0o600)
	time.Sleep(time.Minute)
	// The lock lives as long as this open file description. Without this the
	// file is unreachable once Fd() has been called, a garbage collection runs
	// its finalizer, the descriptor closes and the lock is released under the
	// test (seen as "held lock: 0" in a race run).
	runtime.KeepAlive(f)
}
