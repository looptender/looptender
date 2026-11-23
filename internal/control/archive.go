package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/snapshot"
)

// ArchiveFileName is the archive record beside control.json. It is a file of
// its own, not a field of control.json, so that a build from before archiving
// neither reads nor rewrites it: such a build saves control.json whole from
// the fields it knows, and would drop a field it does not, and control.json's
// schema is part of the state format a daemon checks before it switches
// builds. A build that does not know the file sees the loop exactly as its
// control record says: held, since only a loop that is not supervising can be
// archived.
const ArchiveFileName = "archive.json"

// ArchiveNoteLimit bounds the note, in bytes.
const ArchiveNoteLimit = 300

const archiveFileLimit = 4 << 10

// Archive is a loop put out of the way: listed apart, and not resumable until
// it is unarchived. It changes nothing else: holds, owner, note, task and
// state are as they were, and unarchiving resumes nothing.
type Archive struct {
	Version int       `json:"version"`
	At      time.Time `json:"at"`
	Note    string    `json:"note,omitempty"`
	// Hold is what held the loop when it was archived (Summary of its
	// control record: paused, stop requested, awaiting user, completed,
	// disabled or superseded), for the record.
	Hold string `json:"hold"`
}

// ErrArchived refuses a control change that would leave an archived loop
// supervising: resuming or enabling it needs unarchiving first.
var ErrArchived = errors.New("the loop is archived: unarchive it first (looptender unarchive -c CONFIG), then resume it; unarchiving resumes nothing")

// ErrArchiveSupervising refuses to archive a loop that is supervising.
var ErrArchiveSupervising = errors.New("the loop is supervising: pause or stop it first, then archive it; archiving changes no hold")

// ErrArchiveUnreadable is an archive record that exists but cannot be read.
// It is treated as archived, so that no control change resumes the loop
// while it is there.
var ErrArchiveUnreadable = errors.New("archive.json could not be read")

// Supervising reports whether the record lets the loop act: on, not
// superseded, and with no stop, pause, awaiting-user or completed hold.
func (f File) Supervising() bool {
	g := f.Gate()
	return g.Enabled && !g.Paused && !g.Stop && !g.AwaitingUser && !g.Completed
}

// LoadArchive reads the archive record: nil and no error when there is none.
// A record that exists and cannot be read returns ErrArchiveUnreadable with
// a non-nil, zero Archive, so callers that only ask "is there one" fail
// closed.
func LoadArchive(stateDir string) (*Archive, error) {
	reader, err := snapshot.OpenRecord(filepath.Join(stateDir, ArchiveFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return &Archive{}, fmt.Errorf("%w: %v", ErrArchiveUnreadable, err)
	}
	defer reader.Close()
	b, err := io.ReadAll(io.LimitReader(reader, archiveFileLimit+1))
	var a Archive
	if err != nil || len(b) > archiveFileLimit || json.Unmarshal(b, &a) != nil || a.Version != 1 || a.At.IsZero() {
		return &Archive{}, ErrArchiveUnreadable
	}
	return &a, nil
}

// Archived is whether the loop counts as archived: an archive record, and a
// control record that does not let it supervise. A record left on a loop
// that supervises again (resumed by a build from before archiving, which
// does not know the record) has lapsed: the loop is shown as it is, with the
// lapsed record named, until it is unarchived or held again.
func Archived(a *Archive, f File) bool { return a != nil && !f.Supervising() }

// CleanArchiveNote makes a note one bounded line.
func CleanArchiveNote(note string) (string, error) {
	note = strings.Join(strings.Fields(note), " ")
	if !utf8.ValidString(note) || strings.ContainsFunc(note, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", errors.New("the note must be plain text")
	}
	if len(note) > ArchiveNoteLimit {
		return "", fmt.Errorf("the note is longer than %d bytes", ArchiveNoteLimit)
	}
	return note, nil
}

// ArchiveLoop records the loop as archived, under the control lock, if its
// control record does not let it supervise. It writes only archive.json.
// Archiving an archived loop again replaces the note and keeps the first
// time; it returns the record saved and the control record it was judged on.
func ArchiveLoop(stateDir, note string, now time.Time) (Archive, File, error) {
	note, err := CleanArchiveNote(note)
	if err != nil {
		return Archive{}, File{}, err
	}
	l, err := lock.AcquireNamed(stateDir, lock.Control)
	if err != nil {
		return Archive{}, File{}, err
	}
	defer l.Release()
	if _, err := checkControlPath(stateDir); err != nil {
		return Archive{}, File{}, err
	}
	f, err := Load(stateDir)
	if err != nil {
		return Archive{}, f, err
	}
	if f.Supervising() {
		return Archive{}, f, ErrArchiveSupervising
	}
	a := Archive{Version: 1, At: now.UTC(), Note: note, Hold: f.Summary()}
	if old, err := LoadArchive(stateDir); err == nil && old != nil {
		a.At = old.At
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return Archive{}, f, err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return Archive{}, f, err
	}
	return a, f, writeAtomic(filepath.Join(stateDir, ArchiveFileName), append(b, '\n'), 0o600)
}

// UnarchiveLoop removes the archive record under the control lock and
// returns it; nil when there was none. It changes no hold.
func UnarchiveLoop(stateDir string) (*Archive, error) {
	l, err := lock.AcquireNamed(stateDir, lock.Control)
	if err != nil {
		return nil, err
	}
	defer l.Release()
	a, err := LoadArchive(stateDir)
	if a == nil && err == nil {
		return nil, nil
	}
	if rerr := snapshot.Remove(filepath.Join(stateDir, ArchiveFileName)); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return a, rerr
	}
	return a, nil
}

// archiveGuard refuses, under the control lock, a change that would let an
// archived loop supervise again. A loop that already supervises (its record
// lapsed) is not refused other changes.
func archiveGuard(stateDir string, before, after File) error {
	if before.Supervising() || !after.Supervising() {
		return nil
	}
	a, err := LoadArchive(stateDir)
	if a == nil && err == nil {
		return nil
	}
	return ErrArchived
}
