package main

import (
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
)

// A loop moved to another thread (transfer apply, or enable --take-over from
// the new thread) records the change of owner in control.json
// (ownership.previous, status handed_off) and has its check history reset in
// state.json, which clears the last check time. Until the first check on the
// new target runs, those two records together say "moved, not yet checked".
// While the awaiting-user hold the transfer leaves is still in place, resuming
// is the one action needed. Nothing here changes a hold; it only words them.

// movedStatus says a loop was moved to a new thread and has not been checked
// there yet. It carries no thread ID or title.
type movedStatus struct {
	// At is when ownership moved.
	At time.Time `json:"at"`
	// Held is set while the awaiting-user hold left by the move is in place:
	// then resuming is the one action needed.
	Held bool   `json:"held"`
	Text string `json:"text"`
}

const (
	movedHeldText    = "Moved to its new thread. Resuming it is the one action needed."
	movedUnheldText  = "Moved to its new thread; its first check there has not run yet."
	movedHeldPhrase  = "Moved: resume to start"
	movedNoCheckText = "none yet: the target is new and has not been checked"
)

// movedOf derives the note from the records, or nil when they do not show a
// move that has not been checked yet.
func movedOf(ctl control.File, st state.File) *movedStatus {
	o := ctl.Ownership
	if o == nil || o.Previous == nil || o.Previous.Status != "handed_off" || !st.CheckedAt.IsZero() || st.LastCheck != nil {
		return nil
	}
	m := &movedStatus{At: o.Previous.At, Held: ctl.AwaitingUser, Text: movedUnheldText}
	if m.Held {
		m.Text = movedHeldText
	}
	return m
}
