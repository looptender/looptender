package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/looptender/looptender/internal/adapter/bb"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
	"github.com/looptender/looptender/internal/supervise"
)

// transfer apply moves the source thread's direct, non-archived children to
// the destination once the transfer itself has finished, with the same
// machinery a supervisor-made handoff uses (supervise.MoveChildren): the
// same listing, cap, confirmation by listing both threads, bounded retries,
// state record and counts. Only a child's parent changes. The record is
// tied to the transfer's identity, source and destination, is saved before
// the transfer begins so recover can finish it, and is used only while the
// loop is bound to that destination with no transfer pending.

// transferChildBudget bounds the time apply or recover spends moving children.
const transferChildBudget = 3 * time.Minute

// transferChildren runs bb through the transfer's runner, with the loop's
// bb CLI.
func transferChildren(cli string, run doctorRun) bb.Children {
	return bb.Children{Run: func(ctx context.Context, args ...string) ([]byte, error) { return run(ctx, cli, args, nil, "") }}
}

// recordTransferChildMove saves, once the transfer has begun, that p.From's
// children are to move to p.To, or with leave that they stay. A choice
// already recorded for this transfer is kept.
func recordTransferChildMove(dir string, p control.PendingBBTransfer, leave bool) error {
	st, err := state.Load(dir)
	if err != nil {
		return err
	}
	if m := st.ChildMove; m != nil && m.Transfer == p.ID && m.From == p.From && m.To == p.To {
		return nil
	}
	st.ChildMove = &state.ChildMove{From: p.From, To: p.To, Transfer: p.ID, At: time.Now().UTC(), Left: leave, Done: leave}
	return state.Save(dir, st)
}

// transferChildResult is the outcome of the child move, in counts.
type transferChildResult struct {
	Moved   int  `json:"moved"`
	Pending int  `json:"pending"`
	Failed  int  `json:"failed"`
	Skipped int  `json:"skipped"`
	Listed  bool `json:"listed"`
	Done    bool `json:"done"`
}

// moveTransferChildren moves the children recorded for transfer id, if any,
// once that transfer has finished. A control write by anyone after the
// moves start (a pause, stop, resume or await-user) ends them; the rest wait
// for transfer recover, or for the loop's checks once it is resumed. A
// record that no longer matches the loop's binding is dropped.
func moveTransferChildren(e env, dir, thread, cli, id string, run doctorRun) *transferChildResult {
	st, err := state.Load(dir)
	if err != nil {
		e.errf("warning: child threads: state unavailable; none were moved")
		return nil
	}
	m := st.ChildMove
	if m == nil || m.Transfer != id || m.Left {
		return nil
	}
	if m.To != thread || !supervise.TransferMoveCurrent(dir, m) {
		st.ChildMove = nil
		_ = state.Save(dir, st)
		e.errf("warning: child threads: the recorded move no longer matches the loop's thread; it was dropped and nothing was moved")
		return nil
	}
	if !m.Done {
		baseline, err := control.Load(dir)
		if err != nil {
			e.errf("warning: child threads: control unavailable; none were moved")
			return nil
		}
		deadline := time.Now().Add(transferChildBudget)
		supervise.MoveChildren(e.ctx, transferChildren(cli, run), m, supervise.ChildMoveEnv{
			Left: func() time.Duration { return time.Until(deadline) },
			Held: func() bool {
				c, err := control.Load(dir)
				return err != nil || !c.UpdatedAt.Equal(baseline.UpdatedAt) || !supervise.TransferMoveCurrent(dir, m)
			},
			Save: func() {
				if s, err := state.Load(dir); err == nil {
					s.ChildMove = m
					_ = state.Save(dir, s)
				}
			},
			Warn: func(s string) { e.errf("warning: child threads: %s", s) },
		})
	}
	return &transferChildResult{Moved: m.Moved, Pending: len(m.Remaining), Failed: m.Failed, Skipped: m.Skipped, Listed: m.Listed, Done: m.Done}
}

func printTransferChildren(w io.Writer, r *transferChildResult, id string) {
	if r == nil {
		return
	}
	if !r.Listed {
		fmt.Fprintf(w, "Child threads: not listed yet; run transfer recover --id %s to move them.\n", id)
		return
	}
	fmt.Fprintf(w, "Child threads: %d moved to the new thread", r.Moved)
	if r.Pending > 0 {
		fmt.Fprintf(w, "; %d still to move (transfer recover --id %s, or the loop's checks once resumed)", r.Pending, id)
	}
	if r.Failed > 0 {
		fmt.Fprintf(w, "; %d could not be moved", r.Failed)
	}
	if r.Skipped > 0 {
		fmt.Fprintf(w, "; %d left where they were (archived, gone, or beyond %d)", r.Skipped, state.MaxMovedChildren)
	}
	fmt.Fprintln(w, ". Only their parent changed; none was messaged, retried or stopped, and none needs a resume.")
}

// recoverTransferChildren finishes the child move of transfer id after the
// transfer itself has finished. handled is false when there is no such move,
// so recover reports the usual mismatch.
func recoverTransferChildren(e env, cfg *config.Config, ctl control.File, id string, takeOver, asJSON bool, run doctorRun) (int, bool) {
	st, err := state.Load(cfg.Loop.StateDir)
	if err != nil || st.ChildMove == nil || st.ChildMove.Transfer != id {
		return 0, false
	}
	if st.ChildMove.Left {
		fmt.Fprintf(e.stdout, "Transfer %s had already finished, and its child threads were left where they were.\n", id)
		return exitOK, true
	}
	if err := transferCaller(ctl, st.ChildMove.To, takeOver); err != nil {
		e.errf("transfer: %v", err)
		return exitFailed, true
	}
	r := moveTransferChildren(e, cfg.Loop.StateDir, cfg.Loop.BB.Thread, cfg.Loop.BB.CLI, id, run)
	if r == nil {
		return exitFailed, true
	}
	if asJSON {
		out := struct {
			Outcome  string               `json:"outcome"`
			ID       string               `json:"id"`
			Children *transferChildResult `json:"children"`
		}{"children_recovered", id, r}
		if json.NewEncoder(e.stdout).Encode(out) != nil {
			return exitFailed, true
		}
	} else {
		fmt.Fprintf(e.stdout, "Transfer %s had already finished; only its child thread move was continued.\n", id)
		printTransferChildren(e.stdout, r, id)
	}
	if !r.Done {
		return exitFailed, true
	}
	return exitOK, true
}

// transferChildrenPreview says what apply would do with the source thread's
// children, in counts only: no child IDs or titles.
type transferChildrenPreview struct {
	// Listed is false when the children could not be listed.
	Listed bool `json:"listed"`
	// WouldMove is how many apply would move (live children, at most
	// state.MaxMovedChildren); Beyond is how many live ones it would leave.
	WouldMove int `json:"wouldMove"`
	Beyond    int `json:"beyond,omitempty"`
	// OnOtherProvider is how many of those are on a provider other than the
	// destination's: a thread cannot change provider, so they stay on theirs
	// (and stay blocked after a usage limit until it resets).
	// ProviderUnknown is how many could not be read.
	OnOtherProvider int `json:"onOtherProvider"`
	ProviderUnknown int `json:"providerUnknown,omitempty"`
}

// previewTransferChildren lists source's children and reads the provider of
// each that apply would move.
func previewTransferChildren(ctx context.Context, cli, source, destProvider string, timeout time.Duration, run doctorRun) *transferChildrenPreview {
	out := &transferChildrenPreview{}
	qctx, cancel := context.WithTimeout(ctx, timeout)
	children, err := transferChildren(cli, run).ListChildren(qctx, source)
	cancel()
	if err != nil {
		return out
	}
	out.Listed = true
	live := []string{}
	for _, c := range children {
		if !c.Archived {
			live = append(live, c.ID)
		}
	}
	sort.Strings(live)
	if len(live) > state.MaxMovedChildren {
		out.Beyond, live = len(live)-state.MaxMovedChildren, live[:state.MaxMovedChildren]
	}
	out.WouldMove = len(live)
	for _, id := range live {
		t := inventoryShow(ctx, cli, id, timeout, run)
		switch {
		case !t.Available:
			out.ProviderUnknown++
		case t.Provider != destProvider:
			out.OnOtherProvider++
		}
	}
	return out
}

// transferChildrenCheck words the preview as a check line.
func transferChildrenCheck(p *transferChildrenPreview) doctorCheck {
	c := doctorCheck{ID: "children", Status: "ok"}
	switch {
	case !p.Listed:
		c.Status, c.Message = "unavailable", "The source thread's child threads could not be listed; apply will list them again."
		return c
	case p.WouldMove == 0:
		c.Message = "The source thread has no live child threads to move."
		return c
	}
	c.Message = fmt.Sprintf("Apply moves %d child thread(s) of the source to the destination (only their parent changes; add --leave-children to keep them where they are).", p.WouldMove)
	if p.Beyond > 0 {
		c.Status = "warning"
		c.Message += fmt.Sprintf(" %d more would be left where they are: at most %d move per transfer.", p.Beyond, state.MaxMovedChildren)
	}
	if p.OnOtherProvider > 0 {
		c.Status = "warning"
		c.Message += fmt.Sprintf(" %d of them run on a provider other than the destination's: a thread cannot change provider, so they stay on theirs, and after a usage limit there they stay blocked until it resets.", p.OnOtherProvider)
	}
	if p.ProviderUnknown > 0 {
		c.Status = "warning"
		c.Message += fmt.Sprintf(" The provider of %d could not be read.", p.ProviderUnknown)
	}
	return c
}
