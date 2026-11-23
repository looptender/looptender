//go:build unix

package sessionpipe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
)

// A real FIFO named as the events file, with no writer, is refused at once:
// opening it would block the check until a writer appeared.
func TestObserveRefusesAFIFOWithoutWaiting(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "events.jsonl")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fifo, link} {
		a := New(config.Sessionpipe{Session: harness + ":" + session, EventsFile: path}, t.TempDir())
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		done := make(chan error, 1)
		go func() { _, err := a.Observe(ctx); done <- err }()
		select {
		case err := <-done:
			if !errors.Is(err, ErrNotRegular) {
				t.Errorf("%s: %v, want ErrNotRegular", filepath.Base(path), err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: Observe blocked on a FIFO", filepath.Base(path))
		}
		cancel()
	}
	// The non-blocking open, the second line, does not wait either.
	f, err := openEvents(fifo)
	if err != nil {
		t.Fatalf("a non-blocking open of a FIFO: %v", err)
	}
	f.Close()
}

// A file other users can write is not read: they could make the session
// look waiting, ended or idle.
func TestObserveRefusesAFileOthersCanWrite(t *testing.T) {
	f := newFixture(t)
	f.append(line("session.started", 1, t0, nil))
	for _, mode := range []os.FileMode{0o620, 0o602, 0o666} {
		if err := os.Chmod(f.file, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := f.a.Observe(t.Context()); !errors.Is(err, ErrWritableByOthers) {
			t.Errorf("mode %o: %v", mode, err)
		}
	}
	if err := os.Chmod(f.file, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.Observe(t.Context()); err != nil {
		t.Fatalf("a file only its owner can write: %v", err)
	}
}
