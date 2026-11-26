//go:build unix

package lock

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestInspectFIFOIsNotOpened(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, Tick+".lock"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(dir, Tick); got.Reason != "lock_not_regular" {
		t.Fatalf("%+v", got)
	}
}
