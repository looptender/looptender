package supervise

import (
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/incident"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/state"
)

// ownedBy records a BB thread as the loop's owner, as enable from that thread
// would.
func ownedBy(t *testing.T, dir, thread string) {
	t.Helper()
	if _, err := control.Update(dir, func(f *control.File) { f.Ownership = &control.Ownership{ThreadID: thread, Generation: 1} }); err != nil {
		t.Fatal(err)
	}
}

func ownership(t *testing.T, dir string) control.Ownership {
	t.Helper()
	f, err := control.Load(dir)
	if err != nil || f.Ownership == nil {
		t.Fatalf("ownership: %+v %v", f.Ownership, err)
	}
	return *f.Ownership
}

// The loop is owned by the thread that hit its limit. The handoff to a
// thread the supervisor created moves ownership with it, the first prompt
// says so, and the move is in the history. Holds are kept.
func TestOwnershipFollowsANewThreadHandoff(t *testing.T) {
	w, _, f := newThreadWorld(t)
	ownedBy(t, w.dir, "thr_primary")
	res := mustThreadTick(t, w, f, t0)
	if res.Target != "claude-other" || res.Handoff == nil || res.Handoff.NewThread != "thr_created_1" || res.OwnerMovedTo != "thr_created_1" {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(f.spawned[0].Prompt, `once keepgoing records this handoff, this conversation owns the "example-loop" loop`) || strings.Contains(f.spawned[0].Prompt, "does not own") {
		t.Fatalf("first prompt: %q", f.spawned[0].Prompt)
	}
	o := ownership(t, w.dir)
	if o.ThreadID != "thr_created_1" || o.Generation != 2 || o.Followed == nil || o.Followed.Base != 1 || len(o.Followed.From) != 1 || o.Followed.From[0] != "thr_primary" ||
		o.Previous == nil || o.Previous.ThreadID != "thr_primary" || o.Previous.Status != control.StatusSupervisorHandoff {
		t.Fatalf("ownership %+v followed=%+v", o, o.Followed)
	}
	if !o.Follows("thr_primary", 1) || o.Follows("thr_primary", 2) || o.Follows("", 0) {
		t.Fatal("lease rule")
	}
	if st, _ := state.Load(w.dir); st.PendingOwnerMove != nil {
		t.Fatalf("pending move left: %+v", st.PendingOwnerMove)
	}
	c, _ := control.Load(w.dir)
	if !c.Enabled {
		t.Fatal("control lost its fields")
	}
	// The old thread is refused, by name of what happened.
	err := control.CheckOwner(c, "thr_primary", false)
	if err == nil || !strings.Contains(err.Error(), "moved from this thread to BB thread thr_created_1 with the supervisor's handoff") {
		t.Fatalf("old owner: %v", err)
	}
	if control.CheckOwner(c, "thr_created_1", false) != nil {
		t.Fatal("new owner refused")
	}
	records, _ := incident.Load(w.dir)
	found := false
	for _, r := range records.Records {
		if r.ControlChange != nil && r.ControlChange.Operation == "supervisor_handoff" && r.ControlChange.OwnershipChanged {
			found = true
		}
	}
	if !found {
		t.Fatalf("no history record: %+v", records)
	}
}

// A loop owned by another thread (a coordinator supervising a lane), or by
// no one, keeps its owner: the first prompt says the owner is unchanged.
func TestOwnershipStaysWhenTheLeftThreadIsNotTheOwner(t *testing.T) {
	for _, owner := range []string{"thr_coordinator", ""} {
		w, _, f := newThreadWorld(t)
		if owner != "" {
			ownedBy(t, w.dir, owner)
		}
		res := mustThreadTick(t, w, f, t0)
		if res.Target != "claude-other" || res.OwnerMovedTo != "" || !strings.Contains(f.spawned[0].Prompt, "does not own") {
			t.Fatalf("%q: %+v", owner, res)
		}
		c, _ := control.Load(w.dir)
		if owner == "" && c.Ownership != nil || owner != "" && (c.Ownership.ThreadID != owner || c.Ownership.Generation != 1 || c.Ownership.Followed != nil) {
			t.Fatalf("%q: %+v", owner, c.Ownership)
		}
	}
}

// A dry run previews the handoff and moves nothing.
func TestOwnershipDoesNotMoveInADryRun(t *testing.T) {
	w, _, f := newThreadWorld(t)
	ownedBy(t, w.dir, "thr_primary")
	opts := w.opts(t0)
	opts.DryRun = true
	opts.Threads = func(string) ThreadCreator { return f }
	if _, err := Tick(t.Context(), w.cfg, opts); err != nil {
		t.Fatal(err)
	}
	if o := ownership(t, w.dir); o.ThreadID != "thr_primary" || o.Generation != 1 || len(f.spawned) != 0 {
		t.Fatalf("%+v", o)
	}
}

// The supervisor stopped after the move was saved to state.json and before
// control.json changed: the old owner still owns the loop, and the next check
// past the holds completes the move. A held loop leaves it pending. An
// explicit claim made in between wins, and the pending move is dropped.
func TestPendingOwnerMoveAfterACrash(t *testing.T) {
	setup := func(t *testing.T) (*world, *fakeThreads) {
		w, _, f := newThreadWorld(t)
		ownedBy(t, w.dir, "thr_primary")
		mustThreadTick(t, w, f, t0)
		// Undo the control change and put the move back on record, as a
		// crash between the two saves leaves them.
		control.Update(w.dir, func(c *control.File) { c.Ownership = &control.Ownership{ThreadID: "thr_primary", Generation: 1} })
		st, _ := state.Load(w.dir)
		st.PendingOwnerMove = &state.OwnerMove{From: "thr_primary", FromGeneration: 1, To: "thr_created_1", Reason: "new_thread", At: t0}
		state.Save(w.dir, st)
		w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
		return w, f
	}
	t.Run("completed by the next check", func(t *testing.T) {
		w, f := setup(t)
		res := mustThreadTick(t, w, f, t0.Add(time.Minute))
		if o := ownership(t, w.dir); o.ThreadID != "thr_created_1" || o.Generation != 2 || res.OwnerMovedTo != "thr_created_1" || len(f.spawned) != 1 {
			t.Fatalf("%+v %+v", o, res)
		}
		if st, _ := state.Load(w.dir); st.PendingOwnerMove != nil {
			t.Fatal("pending move left")
		}
		// Completed once: a later check changes nothing.
		mustThreadTick(t, w, f, t0.Add(2*time.Minute))
		if o := ownership(t, w.dir); o.Generation != 2 {
			t.Fatalf("%+v", o)
		}
	})
	for _, hold := range []string{"pause", "stop", "awaiting user", "completed"} {
		t.Run("held: "+hold, func(t *testing.T) {
			w, f := setup(t)
			control.Update(w.dir, func(c *control.File) {
				c.Paused, c.Stop, c.AwaitingUser, c.Completed = hold == "pause", hold == "stop", hold == "awaiting user", hold == "completed"
			})
			mustThreadTick(t, w, f, t0.Add(time.Minute))
			if o := ownership(t, w.dir); o.ThreadID != "thr_primary" || o.Generation != 1 {
				t.Fatalf("held loop moved ownership: %+v", o)
			}
			if st, _ := state.Load(w.dir); st.PendingOwnerMove == nil {
				t.Fatal("held loop dropped the pending move")
			}
		})
	}
	t.Run("an explicit claim in between wins", func(t *testing.T) {
		w, f := setup(t)
		control.Update(w.dir, func(c *control.File) { control.ClaimOwner(c, "thr_person_choice", true) })
		res := mustThreadTick(t, w, f, t0.Add(time.Minute))
		if o := ownership(t, w.dir); o.ThreadID != "thr_person_choice" || o.Generation != 2 || o.Followed != nil || res.OwnerMovedTo != "" {
			t.Fatalf("%+v %+v", o, res)
		}
		if st, _ := state.Load(w.dir); st.PendingOwnerMove != nil || !strings.Contains(strings.Join(res.Warnings, "\n"), "changed explicitly") {
			t.Fatalf("%+v %v", st.PendingOwnerMove, res.Warnings)
		}
	})
}

// The supervisor stopped after BB created the thread and before anything was
// recorded: the reconcile that completes the handoff moves ownership too.
func TestOwnershipFollowsAReconciledCreation(t *testing.T) {
	w, _, f := newThreadWorld(t)
	ownedBy(t, w.dir, "thr_primary")
	f.crash = true
	func() {
		defer func() { recover() }()
		threadTick(t, w, f, t0, nil)
	}()
	if o := ownership(t, w.dir); o.ThreadID != "thr_primary" {
		t.Fatalf("moved before the thread was recorded: %+v", o)
	}
	f.crash = false
	res := mustThreadTick(t, w, f, t0.Add(time.Minute))
	if o := ownership(t, w.dir); res.Handoff == nil || !res.Handoff.Reconciled || o.ThreadID != "thr_created_1" || o.Generation != 2 {
		t.Fatalf("%+v %+v", res, o)
	}
}

// An automatic switch-back returns ownership to the primary's thread as
// another step of the same run; a later handoff moves it on again.
func TestOwnershipFollowsSwitchBack(t *testing.T) {
	w, p, f := newThreadWorld(t)
	ownedBy(t, w.dir, "thr_primary")
	w.cfg.Loop.SwitchBack = config.SwitchBack{Enabled: true, MinAfter: config.Duration{Duration: time.Hour}, RetryEvery: config.Duration{Duration: 30 * time.Minute}}
	w.cfg.Loop.Probe = config.Probe{Kind: "command", Command: []string{"x"}}
	obs := limited
	obs.ResetsAt = t0.Add(2 * time.Hour)
	p.obs = obs
	mustThreadTick(t, w, f, t0)
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	w.probeOK["primary"] = true
	p.obs = decide.Observation{State: decide.Idle}
	res := mustThreadTick(t, w, f, t0.Add(2*time.Hour+time.Minute))
	o := ownership(t, w.dir)
	if res.Action != "switch_back" || res.OwnerMovedTo != "thr_primary" || o.ThreadID != "thr_primary" || o.Generation != 3 || o.Followed == nil || o.Followed.Base != 1 ||
		strings.Join(o.Followed.From, ",") != "thr_created_1" || !o.Follows("thr_primary", 1) || !o.Follows("thr_created_1", 2) {
		t.Fatalf("%+v %+v followed=%+v", res, o, o.Followed)
	}
	// The primary hits its limit again: the next handoff moves it on.
	p.obs = limited
	mustThreadTick(t, w, f, t0.Add(5*time.Hour))
	if o := ownership(t, w.dir); o.ThreadID != "thr_created_2" || o.Generation != 4 || strings.Join(o.Followed.From, ",") != "thr_created_1,thr_primary" {
		t.Fatalf("second handoff: %+v %+v", o, o.Followed)
	}
}

// Review regression: the control lock is held (a concurrent writer, or a
// crash) through both the handoff check and the switch-back check, so the
// pending move to the created thread is never applied. The switch-back must
// drop it: after the lock is released the primary still owns the loop.
func TestStalePendingOwnerMoveDoesNotOverrideSwitchBack(t *testing.T) {
	w, p, f := newThreadWorld(t)
	ownedBy(t, w.dir, "thr_primary")
	w.cfg.Loop.SwitchBack = config.SwitchBack{Enabled: true, MinAfter: config.Duration{Duration: time.Hour}, RetryEvery: config.Duration{Duration: 30 * time.Minute}}
	w.cfg.Loop.Probe = config.Probe{Kind: "command", Command: []string{"x"}}
	obs := limited
	obs.ResetsAt = t0.Add(2 * time.Hour)
	p.obs = obs
	held, err := lock.AcquireNamed(w.dir, lock.Control)
	if err != nil {
		t.Fatal(err)
	}
	if res := mustThreadTick(t, w, f, t0); res.Target != "claude-other" || res.OwnerMovedTo != "" {
		t.Fatalf("%+v", res)
	}
	if st, _ := state.Load(w.dir); st.PendingOwnerMove == nil || st.PendingOwnerMove.To != "thr_created_1" {
		t.Fatalf("no pending move: %+v", st.PendingOwnerMove)
	}
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	w.probeOK["primary"] = true
	p.obs = decide.Observation{State: decide.Idle}
	if res := mustThreadTick(t, w, f, t0.Add(2*time.Hour+time.Minute)); res.Action != "switch_back" {
		t.Fatalf("%+v", res)
	}
	if st, _ := state.Load(w.dir); st.PendingOwnerMove != nil {
		t.Fatalf("stale pending move kept: %+v", st.PendingOwnerMove)
	}
	held.Release()
	mustThreadTick(t, w, f, t0.Add(2*time.Hour+2*time.Minute))
	if o := ownership(t, w.dir); o.ThreadID != "thr_primary" || o.Generation != 1 {
		t.Fatalf("owner moved away from the active primary: %+v", o)
	}
}

// Review regression: a pending move outstanding while control.json has a
// pending BB transfer is refused every check. A later move of the target
// drops it, so cancelling the transfer cannot apply it. While the target is
// unchanged it applies once the transfer is gone.
func TestPendingOwnerMoveAcrossAPendingTransfer(t *testing.T) {
	transfer := &control.PendingBBTransfer{Version: 1, ID: "move_" + strings.Repeat("a", 32), From: "thr_example_a", To: "thr_example_b",
		Before: strings.Repeat("1", 64), After: strings.Repeat("2", 64)}
	for _, moveAgain := range []bool{true, false} {
		t.Run(map[bool]string{true: "the target moves again", false: "the target stays"}[moveAgain], func(t *testing.T) {
			w, p, f := newThreadWorld(t)
			ownedBy(t, w.dir, "thr_primary")
			w.cfg.Loop.SwitchBack = config.SwitchBack{Enabled: true, MinAfter: config.Duration{Duration: time.Hour}, RetryEvery: config.Duration{Duration: 30 * time.Minute}}
			w.cfg.Loop.Probe = config.Probe{Kind: "command", Command: []string{"x"}}
			obs := limited
			obs.ResetsAt = t0.Add(2 * time.Hour)
			p.obs = obs
			held, _ := lock.AcquireNamed(w.dir, lock.Control)
			mustThreadTick(t, w, f, t0)
			held.Release()
			control.Update(w.dir, func(c *control.File) { c.PendingTransfer = transfer })
			w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
			mustThreadTick(t, w, f, t0.Add(time.Minute))
			if st, _ := state.Load(w.dir); st.PendingOwnerMove == nil || ownership(t, w.dir).ThreadID != "thr_primary" {
				t.Fatal("the move applied, or was dropped, while the transfer was pending")
			}
			if moveAgain {
				w.probeOK["primary"] = true
				p.obs = decide.Observation{State: decide.Idle}
				if res := mustThreadTick(t, w, f, t0.Add(2*time.Hour+time.Minute)); res.Action != "switch_back" {
					t.Fatalf("%+v", res)
				}
			}
			control.Update(w.dir, func(c *control.File) { c.PendingTransfer = nil })
			mustThreadTick(t, w, f, t0.Add(3*time.Hour))
			o := ownership(t, w.dir)
			if moveAgain && (o.ThreadID != "thr_primary" || o.Generation != 1) || !moveAgain && (o.ThreadID != "thr_created_1" || o.Generation != 2) {
				t.Fatalf("%+v", o)
			}
		})
	}
}

// A pending move whose thread is no longer the active target (here a person
// retargeted by hand, which records no move) is dropped, not applied.
func TestPendingOwnerMoveForAnotherTargetIsDropped(t *testing.T) {
	w, _, f := newThreadWorld(t)
	ownedBy(t, w.dir, "thr_primary")
	held, _ := lock.AcquireNamed(w.dir, lock.Control)
	mustThreadTick(t, w, f, t0)
	held.Release()
	st, _ := state.Load(w.dir)
	st.ActiveTarget = state.Primary
	state.Save(w.dir, st)
	res := mustThreadTick(t, w, f, t0.Add(time.Minute))
	if o := ownership(t, w.dir); o.ThreadID != "thr_primary" || !strings.Contains(strings.Join(res.Warnings, "\n"), "no longer the thread it was for") {
		t.Fatalf("%+v %v", o, res.Warnings)
	}
}

// Review follow-up: a pending move X->Z never applied, then a handoff Z->W
// the supervisor makes itself, chains to X->W: ownership ends with the
// thread the loop is on, not with X.
func TestPendingOwnerMoveChainsThroughASecondHandoff(t *testing.T) {
	w, _, f := newThreadWorld(t)
	for i := range w.cfg.FallbackSet.Candidates {
		if c := &w.cfg.FallbackSet.Candidates[i]; c.ID == "codex-main" {
			c.BB = &config.BB{NewThread: true}
		}
	}
	ownedBy(t, w.dir, "thr_primary")
	held, _ := lock.AcquireNamed(w.dir, lock.Control)
	if res := mustThreadTick(t, w, f, t0); res.Target != "claude-other" {
		t.Fatalf("%+v", res)
	}
	// The created thread hits its limit too, and the loop moves to a second
	// created thread while control.json still cannot be written.
	w.cfg.FallbackSet.Redundancy.MinHealthy = 1
	w.adapters["claude-other"] = &fakeAdapter{obs: limited}
	delete(w.probeOK, "claude-other")
	res := mustThreadTick(t, w, f, t0.Add(2*time.Hour))
	if res.Target != "codex-main" || res.Handoff == nil || res.Handoff.NewThread != "thr_created_2" {
		t.Fatalf("%+v %+v", res, res.Handoff)
	}
	if st, _ := state.Load(w.dir); st.PendingOwnerMove == nil || st.PendingOwnerMove.From != "thr_primary" || st.PendingOwnerMove.To != "thr_created_2" {
		t.Fatalf("not chained: %+v", st.PendingOwnerMove)
	}
	held.Release()
	w.adapters["codex-main"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	mustThreadTick(t, w, f, t0.Add(2*time.Hour+time.Minute))
	o := ownership(t, w.dir)
	if o.ThreadID != "thr_created_2" || o.Generation != 2 || o.Followed == nil || strings.Join(o.Followed.From, ",") != "thr_primary" {
		t.Fatalf("%+v %+v", o, o.Followed)
	}
	if st, _ := state.Load(w.dir); st.PendingOwnerMove != nil {
		t.Fatalf("%+v", st.PendingOwnerMove)
	}
}

// Review follow-up: a pending BB transfer that completes to another thread
// is an explicit move. The pending supervisor move then finds a different
// owner and is dropped; the transfer's owner stands.
func TestPendingOwnerMoveYieldsToACompletedTransfer(t *testing.T) {
	w, _, f := newThreadWorld(t)
	ownedBy(t, w.dir, "thr_primary")
	held, _ := lock.AcquireNamed(w.dir, lock.Control)
	mustThreadTick(t, w, f, t0)
	held.Release()
	transfer := &control.PendingBBTransfer{Version: 1, ID: "move_" + strings.Repeat("b", 32), From: "thr_primary", To: "thr_transfer_dest",
		Before: strings.Repeat("1", 64), After: strings.Repeat("2", 64)}
	control.Update(w.dir, func(c *control.File) { c.PendingTransfer = transfer })
	w.adapters["claude-other"] = &fakeAdapter{obs: decide.Observation{State: decide.Idle}}
	mustThreadTick(t, w, f, t0.Add(time.Minute))
	// The transfer finishes as transfer apply does.
	if _, err := control.UpdateChecked(w.dir, func(c *control.File) error {
		c.PendingTransfer = nil
		return control.BindBBTarget(c, "thr_transfer_dest")
	}); err != nil {
		t.Fatal(err)
	}
	res := mustThreadTick(t, w, f, t0.Add(2*time.Minute))
	o := ownership(t, w.dir)
	if o.ThreadID != "thr_transfer_dest" || o.Generation != 2 || o.Followed != nil || res.OwnerMovedTo != "" {
		t.Fatalf("%+v %+v", o, res)
	}
	if st, _ := state.Load(w.dir); st.PendingOwnerMove != nil || !strings.Contains(strings.Join(res.Warnings, "\n"), "changed explicitly") {
		t.Fatalf("%+v %v", st.PendingOwnerMove, res.Warnings)
	}
}
