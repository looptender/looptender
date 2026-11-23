package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchiveIsRefusedWhileSupervisingAndChangesNoHold(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := Save(dir, File{Enabled: true, Note: "fictional"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ArchiveLoop(dir, "", now); !errors.Is(err, ErrArchiveSupervising) {
		t.Fatalf("archived a supervising loop: %v", err)
	}
	if a, _ := LoadArchive(dir); a != nil {
		t.Fatal("a record was written")
	}
	for _, held := range []File{{Enabled: true, Paused: true}, {Enabled: true, Stop: true}, {Enabled: true, AwaitingUser: true},
		{Enabled: true, Completed: true}, {}, {Enabled: true, SupersededBy: "loop_x"}} {
		d := t.TempDir()
		if err := Save(d, held); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(filepath.Join(d, FileName))
		a, f, err := ArchiveLoop(d, "  retired\tto   the example  ", now)
		if err != nil || a.Note != "retired to the example" || !a.At.Equal(now) || a.Hold != held.Summary() || f.Supervising() {
			t.Fatalf("%+v: %+v %v", held, a, err)
		}
		after, _ := os.ReadFile(filepath.Join(d, FileName))
		if !bytes.Equal(before, after) {
			t.Fatalf("control.json changed:\n%s\n%s", before, after)
		}
		// Again: the first time is kept, the note replaced.
		if a2, _, err := ArchiveLoop(d, "second", now.Add(time.Hour)); err != nil || !a2.At.Equal(now) || a2.Note != "second" {
			t.Fatalf("again: %+v %v", a2, err)
		}
	}
}

func TestArchivedLoopCannotBeResumedOrEnabledUntilUnarchived(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	if err := Save(dir, File{Enabled: true, Paused: true, AwaitingUser: true, Note: "held for a person"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ArchiveLoop(dir, "", now); err != nil {
		t.Fatal(err)
	}
	// Changes that keep it held are allowed; lifting the last hold is not.
	if _, err := Update(dir, func(f *File) { f.Stop = true }); err != nil {
		t.Fatalf("a further hold was refused: %v", err)
	}
	f, err := UpdateChecked(dir, func(f *File) error { f.Paused, f.Stop, f.AwaitingUser = false, false, false; return nil })
	if !errors.Is(err, ErrArchived) || !f.Paused {
		t.Fatalf("resumed while archived: %+v %v", f, err)
	}
	if got, _ := Load(dir); !got.Paused || !got.Stop || !got.AwaitingUser {
		t.Fatalf("saved: %+v", got)
	}
	// Unarchiving resumes nothing; then resuming works.
	a, err := UnarchiveLoop(dir)
	if err != nil || a == nil {
		t.Fatalf("unarchive: %+v %v", a, err)
	}
	if got, _ := Load(dir); !got.Paused || !got.Stop || !got.AwaitingUser || got.Note != "held for a person" {
		t.Fatalf("unarchive changed control: %+v", got)
	}
	if again, err := UnarchiveLoop(dir); again != nil || err != nil {
		t.Fatalf("second unarchive: %+v %v", again, err)
	}
	if _, err := Update(dir, func(f *File) { f.Paused, f.Stop, f.AwaitingUser = false, false, false }); err != nil {
		t.Fatalf("resume after unarchive: %v", err)
	}
	// Enabling a disabled archived loop is refused too.
	d := t.TempDir()
	if _, _, err := ArchiveLoop(d, "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(d, func(f *File) { f.Enabled = true }); !errors.Is(err, ErrArchived) {
		t.Fatalf("enabled while archived: %v", err)
	}
}

// A build from before archiving does not know archive.json and can resume
// the loop: the record then lapses. The loop reads as it is, other changes
// are not refused, and a hold makes the record count again.
func TestArchiveLapsesWhenTheLoopSupervisesAgain(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, File{Enabled: true, Paused: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ArchiveLoop(dir, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	// What an earlier build's resume writes: control.json alone.
	raw, _ := os.ReadFile(filepath.Join(dir, FileName))
	var old map[string]any
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	old["paused"] = false
	b, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(dir, FileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := LoadArchive(dir)
	f, _ := Load(dir)
	if a == nil || Archived(a, f) {
		t.Fatalf("lapsed record counted: %+v %+v", a, f)
	}
	if _, err := Update(dir, func(f *File) { f.Note = "x" }); err != nil {
		t.Fatalf("a change to a lapsed loop was refused: %v", err)
	}
	if _, err := Update(dir, func(f *File) { f.Paused = true }); err != nil {
		t.Fatal(err)
	}
	f, _ = Load(dir)
	if !Archived(a, f) {
		t.Fatal("held again, not archived")
	}
}

// An archive record that cannot be read counts as archived: nothing resumes
// the loop while it is there.
func TestUnreadableArchiveFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, File{Enabled: true, Paused: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ArchiveFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := LoadArchive(dir)
	if a == nil || !errors.Is(err, ErrArchiveUnreadable) {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := Update(dir, func(f *File) { f.Paused = false }); !errors.Is(err, ErrArchived) {
		t.Fatalf("resumed past an unreadable record: %v", err)
	}
	if _, err := UnarchiveLoop(dir); err != nil {
		t.Fatalf("unarchive of an unreadable record: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ArchiveFileName)); !os.IsNotExist(err) {
		t.Fatal("unreadable record kept")
	}
}

func TestArchiveNoteIsOneBoundedLine(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, File{Enabled: true, Paused: true}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Repeat("a", ArchiveNoteLimit+1), "bell\a", string([]byte{0xff})} {
		if _, _, err := ArchiveLoop(dir, bad, time.Now()); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if a, _ := LoadArchive(dir); a != nil {
		t.Fatal("record written for a refused note")
	}
}
