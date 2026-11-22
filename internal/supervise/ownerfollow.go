package supervise

import (
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/incident"
	"github.com/looptender/looptender/internal/state"
)

// Loop ownership follows a move the supervisor made itself, when the loop's
// owner is the BB thread being left: a handoff to a thread it created, and an
// automatic switch-back. A loop owned by some other thread (a coordinator
// supervising a lane) keeps its owner, and an unowned loop stays unowned.
// Manual retargets never move ownership.
//
// The move is saved to state.json first (PendingOwnerMove), with the rest of
// the check, and control.json is changed after that save. A crash in between
// leaves the old owner in place and the pending move on record; the next
// check that gets past the holds completes it.

// ownerOf reads the recorded owner, or "" when there is none or it cannot be
// read.
func (t *tick) ownerOf() (string, uint64) {
	f, err := control.Load(t.loop.StateDir)
	if err != nil || f.Ownership == nil || control.CheckOwner(f, "", false) != nil || control.CheckTransferReady(f) != nil {
		return "", 0
	}
	return f.Ownership.ThreadID, f.Ownership.Generation
}

// ownerWillFollow reports whether moving the loop away from thread would move
// its ownership too.
func (t *tick) ownerWillFollow(thread string) bool {
	owner, _ := t.ownerOf()
	return thread != "" && owner == thread
}

// followOwner records that ownership is to move from the thread being left to
// to, when the left thread owns the loop, and applies it once the check's
// state is saved.
func (t *tick) followOwner(from, to, reason string) {
	if t.opts.DryRun || from == "" || to == "" || from == to {
		return
	}
	// A move not yet applied, to the thread the loop is now leaving, is
	// chained: ownership is to go from its owner straight to the new
	// thread, or stays where it is if the loop returns to that owner. Any
	// other pending move is about a target the loop has already left and
	// is dropped; the rule below then decides afresh.
	if m := t.st.PendingOwnerMove; m != nil && m.To == from {
		if owner, generation := t.ownerOf(); owner == m.From && generation == m.FromGeneration {
			if to == m.From {
				t.st.PendingOwnerMove = nil
				t.saveOwnerMove()
				return
			}
			t.st.PendingOwnerMove = &state.OwnerMove{From: m.From, FromGeneration: m.FromGeneration, To: to, Reason: reason, At: t.now}
			t.saveOwnerMove()
			t.afterSave = append(t.afterSave, t.applyOwnerMove)
			return
		}
	}
	t.dropOwnerMove("the loop moved again before it was applied")
	owner, generation := t.ownerOf()
	if owner != from {
		return
	}
	t.st.PendingOwnerMove = &state.OwnerMove{From: from, FromGeneration: generation, To: to, Reason: reason, At: t.now}
	t.afterSave = append(t.afterSave, t.applyOwnerMove)
}

// applyOwnerMove changes control.json for a pending move and then clears it
// from state.json. A move an explicit claim overtook is dropped. A failure to
// change control.json leaves the move pending for the next check.
func (t *tick) applyOwnerMove() {
	m := t.st.PendingOwnerMove
	if m == nil || t.opts.DryRun {
		return
	}
	// Only while the thread it moves ownership to is still the active
	// target: a target changed by anything else (a manual retarget, a
	// return to the primary) makes it stale.
	if t.activeThread() != m.To {
		t.dropOwnerMove("the active target is no longer the thread it was for")
		return
	}
	var r incident.Record
	moved := false
	_, err := control.UpdateChecked(t.loop.StateDir, func(f *control.File) error {
		r = incident.Record{Kind: "control", CheckStartedAt: t.now, Build: t.opts.Version,
			ControlBefore: incident.Gate(f.Gate()), ControlChange: &incident.ControlChange{Operation: "supervisor_handoff", Caller: "unavailable", OwnerPresentBefore: f.Ownership != nil}}
		var err error
		moved, err = control.FollowHandoff(f, m.From, m.FromGeneration, m.To, m.At)
		r.ControlAfter = incident.Gate(f.Gate())
		r.ControlChange.OwnerPresentAfter = f.Ownership != nil
		r.ControlChange.OwnershipChanged = moved
		return err
	})
	if err != nil {
		t.warn("loop ownership could not follow the move to %s yet; the earlier owner keeps it and the next check tries again: %v", m.To, err)
		return
	}
	if moved {
		r.ControlChange.SaveObservedAt = time.Now().UTC()
		_ = incident.Append(t.loop.StateDir, r)
		t.res.OwnerMovedTo = m.To
	} else if owner, _ := t.ownerOf(); owner != m.To {
		t.warn("loop ownership did not follow the move to %s: the owner changed explicitly in the meantime, and that change stands", m.To)
	}
	t.st.PendingOwnerMove = nil
	if saved, err := state.Load(t.loop.StateDir); err == nil {
		saved.PendingOwnerMove = nil
		_ = state.Save(t.loop.StateDir, saved)
	}
}

// saveOwnerMove writes the pending move as this check holds it.
func (t *tick) saveOwnerMove() {
	if saved, err := state.Load(t.loop.StateDir); err == nil {
		saved.PendingOwnerMove = t.st.PendingOwnerMove
		_ = state.Save(t.loop.StateDir, saved)
	}
}

// dropOwnerMove forgets a pending move, in this check's state and on disk.
func (t *tick) dropOwnerMove(why string) {
	if t.st.PendingOwnerMove == nil {
		return
	}
	t.warn("a pending move of loop ownership was dropped: %s; the owner is unchanged", why)
	t.st.PendingOwnerMove = nil
	if saved, err := state.Load(t.loop.StateDir); err == nil && saved.PendingOwnerMove != nil {
		saved.PendingOwnerMove = nil
		_ = state.Save(t.loop.StateDir, saved)
	}
}

// activeThread is the BB thread of the active target as recorded, or "".
// It warns about nothing: resolving and returning are the check's own work.
func (t *tick) activeThread() string {
	target := t.st.Target()
	agent := t.loop.Agent
	if target != state.Primary {
		if t.fb == nil {
			return ""
		}
		c, ok := t.fb.Find(target)
		if !ok || c.Disabled {
			return ""
		}
		if agent, ok = t.st.CandidateAgent(c); !ok {
			return ""
		}
	}
	if agent.Adapter != "bb" || agent.BB == nil {
		return ""
	}
	return agent.BB.Thread
}

// completeOwnerMove finishes a move left pending by an earlier check that
// stopped between saving its state and changing control.json. It runs only
// past the holds, so a held loop changes nothing.
func (t *tick) completeOwnerMove() {
	if t.st.PendingOwnerMove != nil {
		t.applyOwnerMove()
	}
}
