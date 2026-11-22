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
//
// A move by transfer apply marks its own hold (control.MovedAt, set when the
// transfer finishes). A loop moved by a build without the mark is recognised
// from the ownership handoff instead. Either way no check has run since (an
// empty last-check time), and the hold must be nothing but the move's: no
// pending or superseding transfer, no stop or completion, and no note,
// question or brief an agent or a check attached, because then the person
// has something other than a plain resume to deal with.
func movedOf(ctl control.File, st state.File) *movedStatus {
	o := ctl.Ownership
	marked := ctl.MovedAt != nil
	handoff := o != nil && o.Previous != nil && o.Previous.Status == "handed_off"
	if !(marked || handoff) || !st.CheckedAt.IsZero() || st.LastCheck != nil {
		return nil
	}
	if ctl.PendingTransfer != nil || ctl.SupersededBy != "" || ctl.Stop || ctl.Completed {
		return nil
	}
	at := time.Time{}
	switch {
	case marked:
		at = *ctl.MovedAt
	case handoff:
		at = o.Previous.At
	}
	m := &movedStatus{At: at, Text: movedUnheldText}
	// The hold is the move's own only when nothing else rides on it; otherwise
	// the loop is still moved and unchecked, but resume is not said to be the
	// one action needed.
	if ctl.AwaitingUser && ctl.Note == "" && ctl.Question == nil && ctl.OpenQuestion == nil && len(ctl.BriefHold) == 0 && !ctl.EpisodeHold {
		m.Held, m.Text = true, movedHeldText
	}
	return m
}
