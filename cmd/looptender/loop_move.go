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

// markMoved records, in state.json, that this loop's hold is the move's own:
// when the move finished and the control record's updatedAt right after. It is
// best effort: without it the loop is recognised from the ownership handoff.
func markMoved(dir string) {
	st, err := state.Load(dir)
	ctl, cerr := control.Load(dir)
	if err != nil || cerr != nil {
		return
	}
	at, ctlAt := time.Now().UTC(), ctl.UpdatedAt
	st.MovedAt, st.MovedControlAt = &at, &ctlAt
	_ = state.Save(dir, st)
}

// handoffSlack is how long after the ownership handoff the control record may
// still have been last written by the move itself, for a loop moved by a build
// that left no mark.
const handoffSlack = 5 * time.Second

// movedOf derives the note from the records, or nil when they do not show a
// move that has not been checked yet.
//
// A move by transfer apply marks its own hold in state.json (MovedAt and
// MovedControlAt). A loop moved by a build without the mark is recognised from
// the ownership handoff instead. Either way no check has run since (an empty
// last-check time), and no pending transfer, superseding, stop or completion
// is in force. The hold counts as the move's own (held) only while the control
// record has not been written since the move (a resume, an await-user or a
// pause writes it) and nothing else rides on it: no note, question or brief,
// because then the person has something other than a plain resume to deal
// with.
func movedOf(ctl control.File, st state.File) *movedStatus {
	o := ctl.Ownership
	marked := st.MovedAt != nil && st.MovedControlAt != nil
	handoff := o != nil && o.Previous != nil && o.Previous.Status == "handed_off"
	if !(marked || handoff) || !st.CheckedAt.IsZero() || st.LastCheck != nil {
		return nil
	}
	if ctl.PendingTransfer != nil || ctl.SupersededBy != "" || ctl.Stop || ctl.Completed {
		return nil
	}
	var at time.Time
	var untouched bool // the control record is as the move left it
	if marked {
		at, untouched = *st.MovedAt, ctl.UpdatedAt.Equal(*st.MovedControlAt)
	} else {
		at = o.Previous.At
		untouched = !ctl.UpdatedAt.After(at.Add(handoffSlack))
	}
	m := &movedStatus{At: at, Text: movedUnheldText}
	if ctl.AwaitingUser && untouched && ctl.Note == "" && ctl.Question == nil && ctl.OpenQuestion == nil && len(ctl.BriefHold) == 0 && !ctl.EpisodeHold {
		m.Held, m.Text = true, movedHeldText
	}
	return m
}
