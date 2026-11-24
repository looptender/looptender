package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/looptender/looptender/internal/snapshot"
)

// A self hold is an await-user hold that can be undone exactly, by the
// supervisor or by the session that set it, and by nobody else's authority:
//
//   - WaitFileName: a wait (looptender wait --until CONDITION). The wait is
//     saved as an ordinary await-user hold, so a build that does not know
//     the record sends nothing either; this build lifts exactly that hold
//     when the condition is met.
//   - OwnQuestionFileName: an await-user the loop's owner thread set on its
//     own loop (await-user --withdraw undoes it).
//
// Undoing restores Before, the control record as it was just before the
// hold, and only while the record is still exactly After, as saved then,
// with the same updatedAt: any later change by anyone (a person's pause, a
// resume, another await-user) ends the right to undo it. A person's hold
// that was already there is in Before, so it stays.
const (
	WaitFileName        = "wait.json"
	OwnQuestionFileName = "own-question.json"
)

const selfHoldLimit = 64 << 10

// SelfHold is the record.
type SelfHold struct {
	Version int `json:"version"`
	// Thread and Generation are the loop's owner when the hold was set
	// ("" for a wait set from a terminal); Caller is the journal's role for
	// the save (owner_thread, terminal).
	Thread     string    `json:"thread,omitempty"`
	Generation uint64    `json:"generation,omitempty"`
	Caller     string    `json:"caller"`
	SetAt      time.Time `json:"setAt"`
	Note       string    `json:"note,omitempty"`
	Before     File      `json:"before"`
	After      File      `json:"after"`
	// Wait only: the condition, its time (time: and limit-reset), and when
	// the wait becomes an ordinary await-user.
	Until     string     `json:"until,omitempty"`
	UntilTime *time.Time `json:"untilTime,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	// OwnQuestion only, after a withdrawal: when and why. The record is kept
	// so status can say so until the next await-user replaces it.
	WithdrawnAt *time.Time `json:"withdrawnAt,omitempty"`
	Withdrawn   string     `json:"withdrawn,omitempty"`
}

// ErrSelfHoldChanged refuses to undo a hold whose control record changed.
var ErrSelfHoldChanged = errors.New("the control record changed after the hold was set")

// LoadSelfHold reads the named record: nil with no error when there is none.
func LoadSelfHold(stateDir, name string) (*SelfHold, error) {
	reader, err := snapshot.OpenRecord(filepath.Join(stateDir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	b, err := io.ReadAll(io.LimitReader(reader, selfHoldLimit+1))
	var h SelfHold
	if err != nil || len(b) > selfHoldLimit || json.Unmarshal(b, &h) != nil || h.Version != 1 || h.SetAt.IsZero() || h.After.UpdatedAt.IsZero() {
		return nil, errors.New(name + " could not be read")
	}
	return &h, nil
}

// SaveSelfHold replaces the named record.
func SaveSelfHold(stateDir, name string, h SelfHold) error {
	h.Version = 1
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(stateDir, name), append(b, '\n'), 0o600)
}

// RemoveSelfHold removes the named record, if there is one.
func RemoveSelfHold(stateDir, name string) error {
	err := snapshot.Remove(filepath.Join(stateDir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Unchanged reports whether cur is exactly the record the hold saved.
func (h SelfHold) Unchanged(cur File) bool {
	if !cur.UpdatedAt.Equal(h.After.UpdatedAt) {
		return false
	}
	a, err1 := json.Marshal(h.After)
	c, err2 := json.Marshal(cur)
	return err1 == nil && err2 == nil && bytes.Equal(a, c)
}

// Restore undoes the hold on f, which must be unchanged: the record
// becomes Before again, keeping the current owner (the same, since nothing
// changed) and leaving updatedAt to the save.
func (h SelfHold) Restore(f *File) error {
	if !h.Unchanged(*f) {
		return ErrSelfHoldChanged
	}
	owner, updated := f.Ownership, f.UpdatedAt
	*f = h.Before
	f.Ownership, f.UpdatedAt = owner, updated
	return nil
}
