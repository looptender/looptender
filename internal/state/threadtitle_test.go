package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestThreadTitleIsCleanedAndWrittenOnlyWhenItChanges(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if f, err := LoadThreadTitle(dir); f != nil || err != nil {
		t.Fatalf("missing: %+v %v", f, err)
	}
	if err := NoteThreadTitle(dir, "thr_example1", "  Fix\tthe\nexample  ", nil, now); err != nil {
		t.Fatal(err)
	}
	f, err := LoadThreadTitle(dir)
	if err != nil || f.Thread != "thr_example1" || f.Title != "Fix the example" || !f.ReadAt.Equal(now) {
		t.Fatalf("%+v %v", f, err)
	}
	// The same title again: not rewritten.
	if err := NoteThreadTitle(dir, "thr_example1", "Fix the example", nil, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if f, _ := LoadThreadTitle(dir); !f.ReadAt.Equal(now) {
		t.Fatalf("rewritten: %+v", f)
	}
	// Nothing for an empty title or an unexpected thread.
	for _, c := range [][2]string{{"thr_example1", " \t"}, {"../x", "A title"}, {"", "A title"}} {
		if err := NoteThreadTitle(dir, c[0], c[1], nil, now.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if f, _ := LoadThreadTitle(dir); f.Title != "Fix the example" {
		t.Fatalf("changed: %+v", f)
	}
	if err := os.WriteFile(filepath.Join(dir, ThreadTitleName), []byte(`{"version":1,"thread":"thr_example1","title":"a\u0007b","readAt":"2030-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadThreadTitle(dir); err != ErrThreadTitle {
		t.Fatalf("uncleaned title accepted: %v", err)
	}
}
