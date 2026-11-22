package supervise

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/adapter/bb"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/state"
)

// childrenOf gives the primary thread n direct children, the first archived
// ones as given.
func childrenOf(f *fakeThreads, parent string, n, archived int) []string {
	if f.children == nil {
		f.children = map[string][]bb.ListedChild{}
	}
	ids := []string{}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("thr_child_%02d", i)
		f.children[parent] = append(f.children[parent], bb.ListedChild{ID: id, Archived: i < archived})
		ids = append(ids, id)
	}
	return ids
}

func childMove(t *testing.T, dir string) state.ChildMove {
	t.Helper()
	st, _ := state.Load(dir)
	if st.ChildMove == nil {
		t.Fatal("no child move recorded")
	}
	return *st.ChildMove
}

// The incident: the left conversation had 15 children, some in error on the
// provider that hit its limit. All but the archived one move to the created
// thread in the handoff check, each once, and the first prompt says so.
func TestChildrenFollowANewThreadHandoff(t *testing.T) {
	w, p, f := newThreadWorld(t)
	childrenOf(f, "thr_primary", 15, 1)
	obs := limited
	obs.HealthyChildren = []string{"thr_child_01", "thr_child_02"}
	obs.ErroredChildren = []string{"thr_child_03", "thr_child_04", "thr_child_05"}
	p.obs = obs
	res := mustThreadTick(t, w, f, t0)
	if res.Target != "claude-other" || res.ChildrenMoved != 14 || res.ChildrenPending != 0 {
		t.Fatalf("%+v", res)
	}
	m := childMove(t, w.dir)
	if !m.Done || m.Moved != 14 || m.Skipped != 1 || m.Failed != 0 || len(m.Remaining) != 0 || m.From != "thr_primary" || m.To != "thr_created_1" {
		t.Fatalf("%+v", m)
	}
	if len(f.moves) != 14 || len(f.children["thr_primary"]) != 1 || len(f.children["thr_created_1"]) != 14 || f.childLists != 3 {
		t.Fatalf("moves=%d left=%d new=%d listings=%d", len(f.moves), len(f.children["thr_primary"]), len(f.children["thr_created_1"]), f.childLists)
	}
	prompt := f.spawned[0].Prompt
	if !strings.Contains(prompt, "Child threads: once keepgoing records this handoff, it moves the direct child threads") || !strings.Contains(prompt, "5 direct child thread(s), 3 of them in error") || !strings.Contains(prompt, "stays in error until that limit resets") {
		t.Fatalf("first prompt: %q", prompt)
	}
	// Later checks do nothing more.
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	mustThreadTick(t, w, f, t0.Add(time.Minute))
	if len(f.moves) != 14 || f.childLists != 3 {
		t.Fatalf("moved again: %d %d", len(f.moves), f.childLists)
	}
}

// At most state.MaxMovedChildren move for one handoff; the rest are counted
// as skipped and left where they are.
func TestChildMoveIsBounded(t *testing.T) {
	w, _, f := newThreadWorld(t)
	childrenOf(f, "thr_primary", 60, 0)
	mustThreadTick(t, w, f, t0)
	m := childMove(t, w.dir)
	if m.Moved != state.MaxMovedChildren || m.Skipped != 10 || !m.Done || len(f.children["thr_primary"]) != 10 {
		t.Fatalf("%+v left=%d", m, len(f.children["thr_primary"]))
	}
}

// bb answering that it moved a child is not taken at its word: the listing
// after the moves decides. A child that does not move is tried again by
// later checks and given up after state.MaxChildMoveAttempts; an update that
// fails outright is treated the same.
func TestChildMoveIsConfirmedAndRetriedBoundedly(t *testing.T) {
	w, _, f := newThreadWorld(t)
	childrenOf(f, "thr_primary", 4, 0)
	f.ignoredMoves = map[string]bool{"thr_child_01": true}
	f.failMoves = map[string]bool{"thr_child_02": true}
	mustThreadTick(t, w, f, t0)
	m := childMove(t, w.dir)
	if m.Moved != 2 || m.Done || strings.Join(m.Remaining, ",") != "thr_child_01,thr_child_02" || m.Attempts["thr_child_01"] != 1 {
		t.Fatalf("%+v", m)
	}
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	for i := 1; i <= 4; i++ {
		mustThreadTick(t, w, f, t0.Add(time.Duration(i)*time.Minute))
	}
	m = childMove(t, w.dir)
	if m.Moved != 2 || m.Failed != 2 || !m.Done || len(m.Remaining) != 0 || len(f.moves) != 2+2*state.MaxChildMoveAttempts {
		t.Fatalf("%+v moves=%d", m, len(f.moves))
	}
}

// A stop saved while children are being moved ends the moves; held checks
// move nothing; the next check after the hold is lifted moves the rest, and
// no child is moved twice.
func TestStopWinsOverChildMoves(t *testing.T) {
	for _, hold := range []string{"stop", "pause", "awaiting user"} {
		t.Run(hold, func(t *testing.T) {
			w, _, f := newThreadWorld(t)
			childrenOf(f, "thr_primary", 5, 0)
			f.onMove = func(child string) {
				if child == "thr_child_01" {
					control.Update(w.dir, func(c *control.File) {
						c.Stop, c.Paused, c.AwaitingUser = hold == "stop", hold == "pause", hold == "awaiting user"
					})
				}
			}
			mustThreadTick(t, w, f, t0)
			m := childMove(t, w.dir)
			if m.Moved != 2 || len(m.Remaining) != 3 || m.Done {
				t.Fatalf("%+v", m)
			}
			w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
			mustThreadTick(t, w, f, t0.Add(time.Minute))
			if len(f.moves) != 2 {
				t.Fatalf("held check moved children: %v", f.moves)
			}
			control.Update(w.dir, func(c *control.File) { c.Stop, c.Paused, c.AwaitingUser = false, false, false })
			mustThreadTick(t, w, f, t0.Add(2*time.Minute))
			m = childMove(t, w.dir)
			if !m.Done || m.Moved != 5 || len(f.moves) != 5 || len(f.children["thr_primary"]) != 0 {
				t.Fatalf("%+v moves=%v", m, f.moves)
			}
		})
	}
}

// The supervisor ends half-way through. The record on disk lists what is
// left, and the next check moves only that.
func TestChildMoveAfterACrash(t *testing.T) {
	w, _, f := newThreadWorld(t)
	childrenOf(f, "thr_primary", 5, 0)
	f.onMove = func(child string) {
		if child == "thr_child_02" {
			panic("simulated process exit while moving children")
		}
	}
	func() {
		defer func() { recover() }()
		threadTick(t, w, f, t0, nil)
	}()
	f.onMove = nil
	// The created thread was not yet recorded, but the move was: the next
	// check continues it, and the reconcile that completes the handoff
	// keeps that record rather than starting over.
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	res := mustThreadTick(t, w, f, t0.Add(time.Minute))
	m := childMove(t, w.dir)
	if res.Target != "claude-other" || !m.Done || m.Moved != 5 || len(f.children["thr_primary"]) != 0 || len(f.children["thr_created_1"]) != 5 {
		t.Fatalf("%+v %+v left=%v", res, m, f.children["thr_primary"])
	}
	// Two were moved before the exit and three after, each once.
	if len(f.moves) != 5 {
		t.Fatalf("%v", f.moves)
	}
}

// A listing that fails leaves the move pending; a later check lists and
// moves.
func TestChildMoveWaitsForAListing(t *testing.T) {
	w, _, f := newThreadWorld(t)
	childrenOf(f, "thr_primary", 3, 0)
	f.childListErr = fmt.Errorf("bb thread list: failed")
	res := mustThreadTick(t, w, f, t0)
	if m := childMove(t, w.dir); m.Listed || m.Done || len(f.moves) != 0 || !strings.Contains(strings.Join(res.Warnings, "\n"), "could not list them") {
		t.Fatalf("%+v %v", m, res.Warnings)
	}
	f.childListErr = nil
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	mustThreadTick(t, w, f, t0.Add(time.Minute))
	if m := childMove(t, w.dir); !m.Done || m.Moved != 3 {
		t.Fatalf("%+v", m)
	}
}

// bb.move_children: false, and a dry run, list and move nothing.
func TestChildMoveOptOutAndDryRun(t *testing.T) {
	w, _, f := newThreadWorld(t)
	childrenOf(f, "thr_primary", 3, 0)
	no := false
	for i := range w.cfg.FallbackSet.Candidates {
		if c := &w.cfg.FallbackSet.Candidates[i]; c.ID == "claude-other" {
			c.BB.MoveChildren = &no
		}
	}
	mustThreadTick(t, w, f, t0)
	if st, _ := state.Load(w.dir); st.ChildMove != nil || f.childLists != 0 || len(f.moves) != 0 || strings.Contains(f.spawned[0].Prompt, "Child threads:") {
		t.Fatalf("opted out: %+v lists=%d", st.ChildMove, f.childLists)
	}
	d, _, g := newThreadWorld(t)
	childrenOf(g, "thr_primary", 3, 0)
	opts := d.opts(t0)
	opts.DryRun = true
	opts.Threads = func(string) ThreadCreator { return g }
	if _, err := Tick(t.Context(), d.cfg, opts); err != nil || g.childLists != 0 || len(g.moves) != 0 {
		t.Fatalf("dry run: %v lists=%d", err, g.childLists)
	}
}

// An automatic switch-back moves the created thread's children to the
// primary's thread, including those the handoff brought over.
func TestChildrenFollowSwitchBack(t *testing.T) {
	w, p, f := newThreadWorld(t)
	childrenOf(f, "thr_primary", 2, 0)
	w.cfg.Loop.SwitchBack = config.SwitchBack{Enabled: true, MinAfter: config.Duration{Duration: time.Hour}, RetryEvery: config.Duration{Duration: 30 * time.Minute}}
	w.cfg.Loop.Probe = config.Probe{Kind: "command", Command: []string{"x"}}
	obs := limited
	obs.ResetsAt = t0.Add(2 * time.Hour)
	p.obs = obs
	mustThreadTick(t, w, f, t0)
	f.children["thr_created_1"] = append(f.children["thr_created_1"], bb.ListedChild{ID: "thr_child_new"})
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	w.probeOK["primary"] = true
	p.obs = decide.Observation{State: decide.Idle}
	res := mustThreadTick(t, w, f, t0.Add(2*time.Hour+time.Minute))
	m := childMove(t, w.dir)
	if res.Action != "switch_back" || m.From != "thr_created_1" || m.To != "thr_primary" || m.Moved != 3 || len(f.children["thr_primary"]) != 3 || len(f.children["thr_created_1"]) != 0 {
		t.Fatalf("%+v %+v", res, m)
	}
}

// Review regression: a saved move that does not belong to how the loop got
// to its current target is dropped, never run. Here a record names the
// primary as From and some other thread as To while the loop is on the
// primary.
func TestUnrelatedChildMoveRecordIsDropped(t *testing.T) {
	w, p, f := newThreadWorld(t)
	childrenOf(f, "thr_primary", 3, 0)
	p.obs = decide.Observation{State: decide.Idle}
	state.Save(w.dir, state.File{ChildMove: &state.ChildMove{From: "thr_primary", To: "thr_somewhere_else", Candidate: "claude-other", At: t0, Listed: true, Remaining: []string{"thr_child_00", "thr_child_01", "thr_child_02"}}})
	res := mustThreadTick(t, w, f, t0)
	st, _ := state.Load(w.dir)
	if len(f.moves) != 0 || f.childLists != 0 || st.ChildMove != nil || !strings.Contains(strings.Join(res.Warnings, "\n"), "no longer matches") {
		t.Fatalf("moves=%v lists=%d record=%+v %v", f.moves, f.childLists, st.ChildMove, res.Warnings)
	}
}

// Review regression: a move left pending after a failed first listing does
// not run once the loop is back on the primary by other means.
func TestPendingChildMoveStopsWhenTheTargetChanges(t *testing.T) {
	w, p, f := newThreadWorld(t)
	childrenOf(f, "thr_primary", 3, 0)
	f.childListErr = fmt.Errorf("bb thread list: failed")
	mustThreadTick(t, w, f, t0)
	if m := childMove(t, w.dir); m.Listed || m.Done {
		t.Fatalf("%+v", m)
	}
	// A person retargets to the primary.
	st, _ := state.Load(w.dir)
	st.ActiveTarget = state.Primary
	state.Save(w.dir, st)
	f.childListErr = nil
	p.obs = decide.Observation{State: decide.Idle}
	mustThreadTick(t, w, f, t0.Add(time.Minute))
	if st, _ := state.Load(w.dir); len(f.moves) != 0 || st.ChildMove != nil {
		t.Fatalf("moves=%v record=%+v", f.moves, st.ChildMove)
	}
}

// Archived children do not count toward any bound: a thread with hundreds of
// archived children still has its live ones moved.
func TestManyArchivedChildrenDoNotBlockTheMove(t *testing.T) {
	w, _, f := newThreadWorld(t)
	childrenOf(f, "thr_primary", 253, 250)
	mustThreadTick(t, w, f, t0)
	if m := childMove(t, w.dir); !m.Done || m.Moved != 3 || m.Skipped != 250 {
		t.Fatalf("%+v", m)
	}
}

// A child archived between the listing and its update is counted as
// skipped, not as moved.
func TestChildArchivedMidMoveIsSkipped(t *testing.T) {
	w, _, f := newThreadWorld(t)
	childrenOf(f, "thr_primary", 3, 0)
	f.failMoves = map[string]bool{"thr_child_02": true}
	f.onMove = func(child string) {
		if child == "thr_child_01" {
			for i, c := range f.children["thr_primary"] {
				if c.ID == "thr_child_02" {
					f.children["thr_primary"][i].Archived = true
				}
			}
		}
	}
	mustThreadTick(t, w, f, t0)
	if m := childMove(t, w.dir); !m.Done || m.Moved != 2 || m.Skipped != 1 || m.Failed != 0 {
		t.Fatalf("%+v", m)
	}
}
