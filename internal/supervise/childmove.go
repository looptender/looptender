package supervise

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/state"
)

// After a handoff the supervisor makes itself to a thread it created, and on
// an automatic switch-back from such a thread, the direct children of the
// conversation left are moved to the thread taking over, so their parent
// notifications reach a conversation that can act on them. Only the parent
// changes: a child is never messaged, retried, stopped, archived or given a
// new model, and one in error stays in error on its own provider. A
// candidate opts out with bb.move_children: false.
//
// The move is bounded (state.MaxMovedChildren per handoff, a few seconds per
// child, only in the time the check has left), recorded in state.json before
// the first child and after each, and continued by later checks that get
// past the holds. A stop, pause or other hold saved while children are being
// moved ends the moves; the rest stay pending.

const (
	childListTime = 10 * time.Second
	childMoveTime = 5 * time.Second
)

// childrenFollow reports whether children follow a move involving candidate
// c, and the bb CLI to use.
func childrenFollow(c config.Candidate) (string, bool) {
	if !isNewThread(c) || !c.BB.ChildrenFollow() {
		return "", false
	}
	return c.BB.CLI, true
}

// planChildMove records that from's children are to move to to, and moves
// what fits in this check.
func (t *tick) planChildMove(candidate, from, to string) {
	if t.opts.DryRun || from == "" || to == "" || from == to || t.fb == nil {
		return
	}
	c, ok := t.fb.Find(candidate)
	if !ok {
		return
	}
	if _, follow := childrenFollow(c); !follow {
		return
	}
	if m := t.st.ChildMove; m != nil && m.From == from && m.To == to {
		// The same move, recorded before a crash and completed or continued
		// at the start of this check: it carries on, counts and all.
		if !m.Done {
			t.runChildMove()
		}
		return
	}
	t.st.ChildMove = &state.ChildMove{From: from, To: to, Candidate: candidate, At: t.now}
	t.saveChildMove()
	t.runChildMove()
}

// saveChildMove writes the move to state.json on its own, so a crash keeps it.
func (t *tick) saveChildMove() {
	if saved, err := state.Load(t.loop.StateDir); err == nil {
		saved.ChildMove = t.st.ChildMove
		_ = state.Save(t.loop.StateDir, saved)
	}
}

// heldNow reports a hold saved since the check began.
func (t *tick) heldNow() bool {
	c, err := control.Load(t.loop.StateDir)
	if err != nil {
		return true
	}
	return c.Stop || c.Paused || c.AwaitingUser || c.Completed || !c.Enabled
}

// completeChildMove continues a move left pending by an earlier check. It
// runs only past the holds.
func (t *tick) completeChildMove() {
	if m := t.st.ChildMove; m != nil && !m.Done && !t.opts.DryRun {
		t.runChildMove()
	}
}

// runChildMove lists the children once, then moves as many as time allows,
// then lists again to confirm: a child still under the left thread was not
// moved, whatever bb answered, and is tried again up to
// state.MaxChildMoveAttempts times.
func (t *tick) runChildMove() {
	m := t.st.ChildMove
	cli := config.DefaultBBCLI
	if c, ok := t.fb.Find(m.Candidate); ok && c.BB != nil && c.BB.CLI != "" {
		cli = c.BB.CLI
	}
	threads := t.opts.Threads(cli)
	list := func() (map[string]bool, bool) {
		if t.left() < childListTime {
			return nil, false
		}
		ctx, cancel := context.WithTimeout(t.ctx, childListTime)
		children, err := threads.ListChildren(ctx, m.From)
		cancel()
		if err != nil {
			t.warn("moving children to the new thread: could not list them; a later check tries again")
			return nil, false
		}
		live := map[string]bool{}
		for _, c := range children {
			if c.ID == m.To {
				continue
			}
			if c.Archived {
				if !m.Listed {
					m.Skipped++
				}
				continue
			}
			live[c.ID] = true
		}
		return live, true
	}
	if !m.Listed {
		live, ok := list()
		if !ok {
			return
		}
		for id := range live {
			if len(m.Remaining) == state.MaxMovedChildren {
				m.Skipped++
				continue
			}
			m.Remaining = append(m.Remaining, id)
		}
		sort.Strings(m.Remaining)
		m.Listed = true
		t.saveChildMove()
	} else if len(m.Remaining) > 0 {
		// Continuing an earlier check's move, which may have ended between an
		// update and its record: a child no longer under the left thread is
		// not asked again.
		live, ok := list()
		if !ok {
			return
		}
		keep := []string{}
		for _, id := range m.Remaining {
			if live[id] {
				keep = append(keep, id)
			} else {
				m.Moved++
				delete(m.Attempts, id)
			}
		}
		m.Remaining = keep
		t.saveChildMove()
	}
	tried := []string{}
	for len(m.Remaining) > len(tried) {
		if t.left() < childMoveTime+childListTime || t.heldNow() {
			break
		}
		id := m.Remaining[len(tried)]
		ctx, cancel := context.WithTimeout(t.ctx, childMoveTime)
		err := threads.SetParent(ctx, id, m.To)
		cancel()
		if err != nil {
			t.warn("moving a child to the new thread failed: %v", err)
		}
		tried = append(tried, id)
	}
	if len(tried) == 0 {
		if len(m.Remaining) == 0 {
			m.Done = true
			t.saveChildMove()
		}
		return
	}
	// Confirm against BB, not against what bb answered.
	live, ok := list()
	if !ok {
		return // unconfirmed: the same children are tried again later
	}
	if m.Attempts == nil {
		m.Attempts = map[string]int{}
	}
	keep := []string{}
	for i, id := range m.Remaining {
		switch {
		case i >= len(tried):
			keep = append(keep, id)
		case !live[id]:
			m.Moved++
			delete(m.Attempts, id)
		default:
			m.Attempts[id]++
			if m.Attempts[id] >= state.MaxChildMoveAttempts {
				m.Failed++
				delete(m.Attempts, id)
			} else {
				keep = append(keep, id)
			}
		}
	}
	m.Remaining = keep
	m.Done = len(keep) == 0
	t.res.ChildrenMoved, t.res.ChildrenPending = m.Moved, len(keep)
	t.saveChildMove()
}

// childrenNote is appended to a created thread's first prompt when the left
// conversation's children will be moved to it, or "" when the last check saw
// none.
func childrenNote(o decide.Observation) string {
	total := len(o.HealthyChildren) + len(o.ErroredChildren) // healthy: any known status but error
	if total == 0 && o.ActiveChildren == 0 && !o.ChildrenUnverified {
		return ""
	}
	n := fmt.Sprintf("%d direct child thread(s), %d of them in error", max(total, o.ActiveChildren), len(o.ErroredChildren))
	if o.ChildrenUnverified {
		n += " (the listing was incomplete, so there may be more)"
	}
	return "\n\nChild threads: once keepgoing records this handoff, it moves the direct child threads of the conversation you continue to this one. At the last check it saw " + n + ". " +
		"Moving a child changes only its parent. A thread cannot change provider, so a child in error because its provider hit a usage limit stays in error until that limit resets; decide whether to redo its work here.\n"
}
