package control

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/looptender/looptender/internal/lock"
)

func TestUpdateCheckedValidated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	original := []byte(`{"enabled":true,"stop":true,"futureHold":true}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("fixture unsupported record")
	mutated := false
	_, err := UpdateCheckedValidated(dir, 1024, func(raw []byte) error {
		if !bytes.Equal(raw, original) {
			t.Fatal("validator did not receive exact bytes")
		}
		held, err := lock.Held(dir, lock.Control)
		if err != nil || !held {
			t.Fatal("validation outside lock")
		}
		return rejected
	}, func(f *File) error { mutated = true; f.Paused = true; return nil })
	if !errors.Is(err, rejected) || mutated {
		t.Fatal("validation failure mutated control", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("unsupported evidence rewritten")
	}
	called := false
	_, err = UpdateCheckedValidated(dir, 1, func([]byte) error { called = true; return nil }, func(*File) error { t.Fatal("oversize mutation"); return nil })
	if err == nil || called {
		t.Fatal("unbounded validation")
	}
	os.Remove(path)
	saved, err := UpdateCheckedValidated(dir, 1024, func([]byte) error { t.Fatal("absence is not damaged content"); return nil }, func(f *File) error { f.Paused = true; return nil })
	if err != nil || !saved.Paused || saved.Enabled {
		t.Fatal("missing-control restrictive semantics changed", err)
	}
}
