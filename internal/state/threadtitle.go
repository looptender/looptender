package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/looptender/looptender/internal/snapshot"
)

// ThreadTitleName is the private file holding the title BB reported for the
// loop's own supervised thread, the last time a check read that thread. It is
// for display only, like lane-titles.json: no decision reads it, and nothing
// in state.json, the run graph, the incident history, a report or the
// timeline carries it. The check writes it from the `bb thread show` it
// already runs, after observing, only when the title changed; a failed write
// is ignored. Removing it changes nothing but what the panel can show. It is
// a file of its own, so builds from before it neither read nor rewrite it.
const ThreadTitleName = "thread-title.json"

const maxThreadTitleBytes = 4 << 10

// ErrThreadTitle means the file is present but cannot be used.
var ErrThreadTitle = errors.New("thread title unavailable")

var threadIDPattern = regexp.MustCompile(`^thr_[a-z0-9]{6,40}$`)

// ThreadTitle is the file's content: the thread it is for, its title as BB
// reported it (cleaned and bounded as lane titles are), and when it was read.
type ThreadTitle struct {
	Version int       `json:"version"`
	Thread  string    `json:"thread"`
	Title   string    `json:"title"`
	ReadAt  time.Time `json:"readAt"`
}

// NoteThreadTitle records the title read for thread at now, unless the file
// already holds the same title for it. An empty title or an unexpected thread
// ID records nothing.
// labels are the loop's own route labels (providers, models, groups,
// accounts), which a display name made from the title leaves out.
func NoteThreadTitle(dir, thread, title string, labels []string, now time.Time) error {
	title = CleanLaneTitle(title)
	if title == "" || !threadIDPattern.MatchString(thread) {
		return nil
	}
	// The first title read becomes the display name when none is recorded,
	// without the words of a provider, model or generation; a later title
	// never replaces it. A title that is nothing but such a qualifier is kept
	// as it is.
	if dn, err := LoadDisplayName(dir); err == nil && dn == nil {
		if name, trimmed := DisplayNameFromTitle(title, labels); name != "" {
			source := DisplayNameThreadTitle
			if trimmed {
				source = DisplayNameTitleTrimmed
			}
			_ = SaveDisplayName(dir, name, source, now)
		}
	}
	if old, err := LoadThreadTitle(dir); err == nil && old != nil && old.Thread == thread && old.Title == title {
		return nil
	}
	b, err := json.Marshal(ThreadTitle{Version: 1, Thread: thread, Title: title, ReadAt: now.UTC()})
	if err != nil {
		return ErrThreadTitle
	}
	return saveAllowance(dir, ThreadTitleName, b)
}

// LoadThreadTitle reads the file: nil with no error when there is none.
func LoadThreadTitle(dir string) (*ThreadTitle, error) {
	path := filepath.Join(dir, ThreadTitleName)
	if info, err := snapshot.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil || !info.Mode().IsRegular() || info.Size() > maxThreadTitleBytes {
		return nil, ErrThreadTitle
	}
	b, err := snapshot.ReadFile(path)
	if err != nil || len(b) > maxThreadTitleBytes {
		return nil, ErrThreadTitle
	}
	var f ThreadTitle
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&f) != nil || d.Decode(new(any)) != io.EOF || f.Version != 1 || f.ReadAt.IsZero() || !threadIDPattern.MatchString(f.Thread) || f.Title == "" || CleanLaneTitle(f.Title) != f.Title {
		return nil, ErrThreadTitle
	}
	return &f, nil
}

// DisplayNameName is the private file holding the loop's display name: the
// one name every surface shows, kept by the line of work rather than by a
// thread or the loop's slug. A person sets it (display-name); when none is
// set, the first check that reads the loop's BB thread title records that
// title as it, once, so the name stays the same when the title later
// changes. A successor loop takes its predecessor's. Like thread-title.json
// it is a file of its own, which builds from before it neither read nor
// rewrite, and no decision reads it.
const DisplayNameName = "display-name.json"

// MaxDisplayName bounds a display name, in characters.
const MaxDisplayName = 80

// Display name sources.
const (
	DisplayNameSet         = "set"          // a person set it
	DisplayNameThreadTitle = "thread_title" // the BB thread's title at the first check that read it, as it was
	// DisplayNameTitleTrimmed: that title without its provider, model,
	// effort or generation words (DisplayNameFromTitle).
	DisplayNameTitleTrimmed = "thread_title_trimmed"
	DisplayNameInherited    = "inherited" // taken from the loop this one succeeds
)

type DisplayName struct {
	Version int       `json:"version"`
	Name    string    `json:"name"`
	Source  string    `json:"source"`
	At      time.Time `json:"at"`
}

// CleanDisplayName makes a name one line of at most MaxDisplayName
// characters, as lane titles are cleaned; "" means no name.
func CleanDisplayName(s string) string {
	s = CleanLaneTitle(s)
	if r := []rune(s); len(r) > MaxDisplayName {
		s = string(r[:MaxDisplayName])
	}
	return s
}

// SaveDisplayName replaces the display name.
func SaveDisplayName(dir, name, source string, now time.Time) error {
	name = CleanDisplayName(name)
	if name == "" || (source != DisplayNameSet && source != DisplayNameThreadTitle && source != DisplayNameTitleTrimmed && source != DisplayNameInherited) {
		return ErrThreadTitle
	}
	b, err := json.Marshal(DisplayName{Version: 1, Name: name, Source: source, At: now.UTC()})
	if err != nil {
		return ErrThreadTitle
	}
	return saveAllowance(dir, DisplayNameName, b)
}

// LoadDisplayName reads it: nil with no error when there is none.
func LoadDisplayName(dir string) (*DisplayName, error) {
	path := filepath.Join(dir, DisplayNameName)
	if info, err := snapshot.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil || !info.Mode().IsRegular() || info.Size() > maxThreadTitleBytes {
		return nil, ErrThreadTitle
	}
	b, err := snapshot.ReadFile(path)
	if err != nil || len(b) > maxThreadTitleBytes {
		return nil, ErrThreadTitle
	}
	var f DisplayName
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&f) != nil || d.Decode(new(any)) != io.EOF || f.Version != 1 || f.At.IsZero() || f.Name == "" || CleanDisplayName(f.Name) != f.Name {
		return nil, ErrThreadTitle
	}
	return &f, nil
}
