package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failing makes writes to files whose names contain name fail after keep
// bytes, for the rest of the test.
func failing(t *testing.T, name string, keep int) {
	t.Helper()
	InjectWriteFaults(func(path string) (int, error) {
		if strings.Contains(filepath.Base(path), name) {
			return keep, ENOSPC
		}
		return 0, nil
	})
	t.Cleanup(func() { InjectWriteFaults(nil) })
}

func TestAFailedReplacementLeavesTheOldRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "record.json")
	if err := WriteFileAtomic(path, []byte("old\n"), 0o600, true); err != nil {
		t.Fatal(err)
	}
	for _, keep := range []int{0, 3} { // nothing written, or a short write
		failing(t, "record.json", keep)
		if err := WriteFileAtomic(path, []byte("a much longer new record\n"), 0o600, true); !errors.Is(err, ENOSPC) {
			t.Fatalf("keep %d: %v", keep, err)
		}
		if b, _ := os.ReadFile(path); string(b) != "old\n" {
			t.Fatalf("keep %d: record is %q", keep, b)
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.Name() != "record.json" && !strings.HasSuffix(e.Name(), ".present") {
				t.Fatalf("keep %d: temporary file left: %s", keep, e.Name())
			}
		}
	}
}

func TestAppendLineIsWholeOrNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	if err := AppendLine(path, []byte(`{"n":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	failing(t, "runs.jsonl", 5)
	if err := AppendLine(path, []byte(`{"n":2}`), 0o600); !errors.Is(err, ENOSPC) {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "{\"n\":1}\n" {
		t.Fatalf("a torn line was left: %q", b)
	}
	InjectWriteFaults(nil)
	if err := AppendLine(path, []byte(`{"n":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// A log torn by a crash: the next line starts on a line of its own.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.Write([]byte(`{"n":4,"cut`))
	f.Close()
	if err := AppendLine(path, []byte(`{"n":5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "{\"n\":1}\n{\"n\":3}\n{\"n\":4,\"cut\n{\"n\":5}\n" {
		t.Fatalf("%q", b)
	}
}

func TestShortWriteIsAnError(t *testing.T) {
	failing(t, "short", 2)
	f, err := os.Create(filepath.Join(t.TempDir(), "short"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := Write(f, []byte("abcdef")); err == nil {
		t.Fatal("a short write succeeded")
	}
}
