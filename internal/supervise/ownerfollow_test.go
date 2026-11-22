package supervise

import (
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/incident"
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
