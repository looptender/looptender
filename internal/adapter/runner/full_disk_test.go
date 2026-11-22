package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/snapshot"
)

// A full disk while a session ID is saved leaves the one saved before, never
// an empty or cut-short ID that a later turn would resume.
func TestFullDiskKeepsTheSavedSession(t *testing.T) {
	d := Dir(t.TempDir())
	if err := d.SaveSession("session-fictional-0001"); err != nil {
		t.Fatal(err)
	}
	snapshot.InjectWriteFaults(func(path string) (int, error) {
		if strings.Contains(filepath.Base(path), "session") {
			return 8, snapshot.ENOSPC
		}
		return 0, nil
	})
	t.Cleanup(func() { snapshot.InjectWriteFaults(nil) })
	if err := d.SaveSession("session-fictional-0002"); err == nil {
		t.Fatal("a failed save went unsaid")
	}
	if b, _ := os.ReadFile(filepath.Join(string(d), "session")); string(b) != "session-fictional-0001\n" {
		t.Fatalf("saved session is %q", b)
	}
	entries, _ := os.ReadDir(string(d))
	for _, e := range entries {
		if e.Name() != "session" && !strings.HasSuffix(e.Name(), ".present") {
			t.Fatalf("staging left: %s", e.Name())
		}
	}
}
