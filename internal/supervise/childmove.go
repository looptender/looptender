package supervise

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/looptender/looptender/internal/adapter/bb"
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
// runs only past the holds, and only for the move the loop's current target
// came from (childMoveCurrent); any other record is dropped.
func (t *tick) completeChildMove() {
	m := t.st.ChildMove
	if m == nil || m.Done || m.Left || t.opts.DryRun {
		return
	}
	switch t.childMoveCurrent(m) {
	case childMoveRun:
		t.runChildMove()
	case childMoveDrop:
		t.warn("a pending move of child threads was dropped: it no longer matches the thread the loop is on; nothing was moved")
		t.st.ChildMove = nil
		t.saveChildMove()
	}
}

type childMoveVerdict int

const (
	childMoveRun   childMoveVerdict = iota
	childMoveDefer                  // its handoff is still being settled
	childMoveDrop
)

// childMoveCurrent says whether a recorded move belongs to how the loop got
// to its current target: To must be the active target's thread, and From the
// conversation that handoff left. For a handoff to a created thread, From is
// the conversation the created thread continues; for a switch-back to the
// primary, From is the created thread that was left. A move recorded for a
// creation not yet settled waits for the reconcile that completes it.
func (t *tick) childMoveCurrent(m *state.ChildMove) childMoveVerdict {
	active := t.activeThread()
	if m.Transfer != "" {
		// A transfer's move: only while the loop is still on the primary
		// it moved to, with that transfer finished and nothing pending.
		if m.Candidate == "" && active != "" && active == m.To && t.st.Target() == state.Primary && TransferMoveCurrent(t.loop.StateDir, m) {
			return childMoveRun
		}
		return childMoveDrop
	}
	created := t.st.NewThreads[m.Candidate]
	switch {
	case m.Candidate == "" || active != m.To || active == "":
	case t.st.Target() == m.Candidate && created.Thread == m.To && created.Continues == m.From:
		return childMoveRun
	case t.st.Target() == state.Primary && created.Thread == m.From:
		return childMoveRun
	}
	if in := t.st.NewThreadIntent; in != nil && in.Candidate == m.Candidate && in.Continues == m.From && in.From == t.st.Target() {
		return childMoveDefer
	}
	return childMoveDrop
}

// TransferMoveCurrent reports whether a transfer's child move still belongs
// to the loop: the control record shows that transfer finished (no transfer
// pending) and the loop bound to the move's destination.
func TransferMoveCurrent(stateDir string, m *state.ChildMove) bool {
	c, err := control.Load(stateDir)
	return err == nil && m.Transfer != "" && c.PendingTransfer == nil && c.SupersededBy == "" && c.BBTarget == m.To
}

// childPlace is where a listed child is: still under the left thread, under
// the thread that took over, or neither (archived, deleted, or moved
// elsewhere by someone).
type childPlace int

const (
	childStill childPlace = iota
	childMoved
	childGone
)

// ChildMover lists and re-parents BB child threads.
type ChildMover interface {
	ListChildren(ctx context.Context, parent string) ([]bb.ListedChild, error)
	SetParent(ctx context.Context, child, parent string) error
}

// ChildMoveEnv is what a child move needs from its caller: the time it may
// still use, whether a hold now ends the moves, how to save the record, and
// where warnings go.
type ChildMoveEnv struct {
	Left func() time.Duration
	Held func() bool
	Save func()
	Warn func(string)
}

func (t *tick) runChildMove() {
	m := t.st.ChildMove
	cli := config.DefaultBBCLI
	if t.fb != nil {
		if c, ok := t.fb.Find(m.Candidate); ok && c.BB != nil && c.BB.CLI != "" {
			cli = c.BB.CLI
		}
	}
	if m.Candidate == "" && t.loop.Agent.BB != nil && t.loop.Agent.BB.CLI != "" {
		cli = t.loop.Agent.BB.CLI // a transfer's move: the primary's bb
	}
	MoveChildren(t.ctx, t.opts.Threads(cli), m, ChildMoveEnv{Left: t.left, Held: t.heldNow, Save: t.saveChildMove, Warn: func(s string) { t.warn("%s", s) }})
	t.res.ChildrenMoved, t.res.ChildrenPending = m.Moved, len(m.Remaining)
}

// MoveChildren lists the children of m.From once, then moves as many as time
// allows to m.To, then lists both threads to confirm: a child counts as moved
// only when it is listed under m.To, whatever bb answered. One still under
// m.From is tried again up to state.MaxChildMoveAttempts times; one archived,
// deleted or moved elsewhere is counted as skipped. The handoff check and
// transfer apply both use it; callers decide whether m is still current.
func MoveChildren(ctx context.Context, threads ChildMover, m *state.ChildMove, env ChildMoveEnv) {
	listOf := func(parent string) ([]bb.ListedChild, bool) {
		if env.Left() < childListTime {
			return nil, false
		}
		ctx, cancel := context.WithTimeout(ctx, childListTime)
		children, err := threads.ListChildren(ctx, parent)
		cancel()
		if err != nil {
			env.Warn("moving children to the new thread: could not list them; a later check tries again")
			return nil, false
		}
		return children, true
	}
	// placeOf lists both threads and says where each child is.
	placeOf := func() (func(string) childPlace, bool) {
		from, ok := listOf(m.From)
		if !ok {
			return nil, false
		}
		to, ok := listOf(m.To)
		if !ok {
			return nil, false
		}
		still, moved := map[string]bool{}, map[string]bool{}
		for _, c := range from {
			still[c.ID] = !c.Archived
		}
		for _, c := range to {
			moved[c.ID] = !c.Archived
		}
		return func(id string) childPlace {
			switch {
			case moved[id]:
				return childMoved
			case still[id]:
				return childStill
			}
			return childGone
		}, true
	}
	settle := func(place func(string) childPlace, tried int) {
		if m.Attempts == nil {
			m.Attempts = map[string]int{}
		}
		keep := []string{}
		for i, id := range m.Remaining {
			switch place(id) {
			case childMoved:
				m.Moved++
				m.MovedIDs = append(m.MovedIDs, id)
				delete(m.Attempts, id)
			case childGone:
				m.Skipped++
				delete(m.Attempts, id)
			default:
				if i < tried {
					m.Attempts[id]++
				}
				if m.Attempts[id] >= state.MaxChildMoveAttempts {
					m.Failed++
					delete(m.Attempts, id)
				} else {
					keep = append(keep, id)
				}
			}
		}
		m.Remaining = keep
	}
	if !m.Listed {
		from, ok := listOf(m.From)
		if !ok {
			return
		}
		live := []string{}
		for _, c := range from {
			switch {
			case c.ID == m.To:
			case c.Archived:
				m.Skipped++
			default:
				live = append(live, c.ID)
			}
		}
		sort.Strings(live)
		if len(live) > state.MaxMovedChildren {
			m.Skipped += len(live) - state.MaxMovedChildren
			live = live[:state.MaxMovedChildren]
		}
		m.Remaining, m.Listed = live, true
		env.Save()
	} else if len(m.Remaining) > 0 {
		// Continuing an earlier check's move, which may have ended between an
		// update and its record: a child already moved is not asked again.
		place, ok := placeOf()
		if !ok {
			return
		}
		settle(place, 0)
		env.Save()
	}
	tried := 0
	for tried < len(m.Remaining) {
		if env.Left() < childMoveTime+2*childListTime || env.Held() {
			break
		}
		ctx, cancel := context.WithTimeout(ctx, childMoveTime)
		err := threads.SetParent(ctx, m.Remaining[tried], m.To)
		cancel()
		if err != nil {
			env.Warn(fmt.Sprintf("moving a child to the new thread failed: %v", err))
		}
		tried++
	}
	if tried > 0 {
		// Confirm against BB, not against what bb answered. Unconfirmed, the
		// same children are tried again later.
		place, ok := placeOf()
		if !ok {
			return
		}
		settle(place, tried)
	}
	m.Done = len(m.Remaining) == 0
	env.Save()
}

// childrenNote is appended to a created thread's first prompt when the left
// conversation's children will be moved to it, or "" when the last check saw
// none.
func childrenNote(o decide.Observation, replaces bool) string {
	total := len(o.HealthyChildren) + len(o.ErroredChildren) // healthy: any known status but error
	if total == 0 && o.ActiveChildren == 0 && !o.ChildrenUnverified {
		return ""
	}
	n := fmt.Sprintf("%d direct child thread(s), %d of them in error", max(total, o.ActiveChildren), len(o.ErroredChildren))
	if o.ChildrenUnverified {
		n += " (the listing was incomplete, so there may be more)"
	}
	note := "\n\nChild threads: once keepgoing records this handoff, it moves the direct child threads of the conversation you continue to this one. At the last check it saw " + n + ". " +
		"Moving a child changes only its parent. A thread cannot change provider, so a child in error because its provider hit a usage limit stays in error until that limit resets"
	if !replaces {
		return note + "; decide whether to redo its work here.\n"
	}
	idle := max(0, len(o.HealthyChildren)-o.ActiveChildren)
	errored := len(o.ErroredChildren)
	started := min(errored, config.ChildSuccessorsPerRun)
	return note + fmt.Sprintf(". Once the children are here, keepgoing starts a successor thread on this provider for each child blocked by a usage limit on another provider, as your child lane. "+
		"From the last check: %d child thread(s) were in error, so up to %d successor lane(s) will be started and %d blocked child thread(s) skipped at the cap of %d per handoff (only those blocked by a usage limit are replaced, so it may be fewer; keepgoing status gives the numbers actually started and skipped). "+
		"The %d idle child thread(s), and any child in error for another reason, are left for you to decide; a finished turn says nothing about unfinished work. "+
		"When the old provider's limit resets, BB may run a replaced child again: check before redoing work twice.\n", errored, started, max(errored-config.ChildSuccessorsPerRun, 0), config.ChildSuccessorsPerRun, idle)
}
