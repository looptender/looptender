package main

import (
	"github.com/looptender/looptender/internal/state"
	"time"
)

// loopActivityAt is display-only sorting evidence. Check/read times, held
// nudges and sleep/outage credits do not establish work. Old valid evidence
// stays old; a missing record or a future timestamp never becomes "now".
func loopActivityAt(st state.File, now time.Time) *time.Time {
	var latest time.Time
	note := func(at time.Time) {
		if !at.IsZero() && !at.After(now) && at.After(latest) {
			latest = at
		}
	}
	note(st.LastSentAt)
	note(st.LastHandoffAt)
	if st.MovedAt != nil {
		note(*st.MovedAt)
	}
	if !st.ProgressBaseline {
		note(st.ProgressChangedAt)
	}
	if latest.IsZero() {
		return nil
	}
	latest = latest.UTC()
	return &latest
}
