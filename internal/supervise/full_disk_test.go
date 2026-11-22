package supervise

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/snapshot"
	"github.com/looptender/looptender/internal/state"
)

// full makes every write of the named records fail as on a full disk (a
// short write, then ENOSPC) until the returned function is called.
func full(t *testing.T, records ...string) func() {
	t.Helper()
	snapshot.InjectWriteFaults(func(path string) (int, error) {
		base := filepath.Base(path)
		for _, r := range records {
			if strings.HasPrefix(base, "."+r+"-") || base == r {
				return 3, snapshot.ENOSPC
			}
		}
		return 0, nil
	})
	lift := func() { snapshot.InjectWriteFaults(nil) }
	t.Cleanup(lift)
	return lift
}

func tickErr(w *world, now time.Time) (Result, error) {
	return Tick(context.Background(), w.cfg, w.opts(now))
}

// The record of a send cannot be saved: nothing is sent, and the check
// says why. Once the disk has room, the next check sends.
func TestFullDiskBeforeASendSendsNothing(t *testing.T) {
	w := newWorld(t, true, false)
	p := w.primary(decide.Observation{State: decide.Idle})
	lift := full(t, state.SendsName)
	_, err := tickErr(w, t0)
	if len(p.sent) != 0 {
		t.Fatalf("sent with no record: %v", p.sent)
	}
	if err == nil || !strings.Contains(err.Error(), "no space") {
		t.Fatalf("the check did not say why: %v", err)
	}
	lift()
	runTick(t, w, t0.Add(time.Minute))
	if len(p.sent) != 1 {
		t.Fatalf("sends after room returned: %d", len(p.sent))
	}
}

// A re-prompt was made, but state.json cannot be saved after it: the next
// check counts it as made, waits out its backoff and does not repeat it.
func TestFullDiskAfterASendDoesNotRepeatIt(t *testing.T) {
	w := newWorld(t, true, false)
	p := w.primary(decide.Observation{State: decide.Error, ErrorClass: decide.ErrUnknown})
	before, _ := os.ReadFile(filepath.Join(w.dir, state.FileName))
	lift := full(t, state.FileName)
	if _, err := tickErr(w, t0); err == nil {
		t.Fatal("a check that could not save its state said nothing")
	}
	if len(p.sent) != 1 {
		t.Fatalf("sends: %d", len(p.sent))
	}
	after, _ := os.ReadFile(filepath.Join(w.dir, state.FileName))
	if string(before) != string(after) {
		t.Fatal("state.json changed although its save failed")
	}
	lift()
	if r := runTick(t, w, t0.Add(time.Minute)); len(p.sent) != 1 || r.Reason != "backing off after error" {
		t.Fatalf("the re-prompt was repeated or not counted: %d %s (%s)", len(p.sent), r.Action, r.Reason)
	}
}

// A hold the check raises (the re-prompt cap reached) cannot be saved: the
// check fails and settles nothing; the next check with room raises it.
func TestFullDiskWhileAHoldIsSaved(t *testing.T) {
	obs := decide.Observation{State: decide.Error, ErrorClass: decide.ErrUnknown}
	// Where the hold comes, with room.
	ref := newWorld(t, true, false)
	ref.primary(obs)
	at := -1
	for i := 0; i < 20 && at < 0; i++ {
		if r := runTick(t, ref, t0.Add(time.Duration(i)*11*time.Minute)); r.Action == "await_user" {
			at = i
		}
	}
	if at < 0 {
		t.Fatal("no hold in the reference run")
	}
	w := newWorld(t, true, false)
	w.primary(obs)
	for i := 0; i < at; i++ {
		runTick(t, w, t0.Add(time.Duration(i)*11*time.Minute))
	}
	lift := full(t, control.FileName)
	if _, err := tickErr(w, t0.Add(time.Duration(at)*11*time.Minute)); err == nil {
		t.Fatal("a hold that could not be saved went unsaid")
	}
	if c, _ := control.Load(w.dir); c.AwaitingUser {
		t.Fatal("hold recorded although its write failed")
	}
	lift()
	runTick(t, w, t0.Add(time.Duration(at+1)*11*time.Minute))
	if c, _ := control.Load(w.dir); !c.AwaitingUser {
		t.Fatalf("the hold was not raised once room returned: %+v", c)
	}
}

// No record is ever left cut short or empty by a full disk: each is the
// old one, so the next check never reads a loop as new.
func TestFullDiskLeavesEveryRecordWholeOrOld(t *testing.T) {
	w := newWorld(t, true, false)
	w.primary(decide.Observation{State: decide.Idle})
	runTick(t, w, t0)
	records := []string{state.FileName, state.SendsName, control.FileName, "incidents.json", state.LogName}
	old := map[string]string{}
	for _, r := range records {
		b, _ := os.ReadFile(filepath.Join(w.dir, r))
		old[r] = string(b)
	}
	full(t, records...)
	_, _ = tickErr(w, t0.Add(2*time.Hour))
	control.Update(w.dir, func(f *control.File) { f.Paused = true }) // a write that fails too
	for _, r := range records {
		b, _ := os.ReadFile(filepath.Join(w.dir, r))
		if string(b) != old[r] {
			t.Errorf("%s changed under a full disk:\n%q\n%q", r, old[r], b)
		}
	}
	entries, _ := os.ReadDir(w.dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && strings.Contains(e.Name(), ".json-") {
			t.Errorf("staging file left: %s", e.Name())
		}
	}
	st, err := state.Load(w.dir)
	if err != nil || st.CheckedAt.IsZero() {
		t.Fatalf("state.json read as a new loop: %+v %v", st.CheckedAt, err)
	}
}

// The record of a move of child threads cannot be saved: no child is
// moved, since moves made with no record could not be continued after a
// crash.
func TestFullDiskMovesNoChildWithoutItsRecord(t *testing.T) {
	w, p, f := newThreadWorld(t)
	childrenOf(f, "thr_primary", 4, 0)
	p.obs = limited
	writes := 0
	snapshot.InjectWriteFaults(func(path string) (int, error) {
		if strings.HasPrefix(filepath.Base(path), "."+state.FileName+"-") {
			writes++
			if writes > 1 { // the new thread's intent is saved; nothing after
				return 0, snapshot.ENOSPC
			}
		}
		return 0, nil
	})
	t.Cleanup(func() { snapshot.InjectWriteFaults(nil) })
	_, _ = threadTick(t, w, f, t0, nil)
	if len(f.spawned) != 1 {
		t.Fatalf("the handoff did not create the thread: %d", len(f.spawned))
	}
	if len(f.moves) != 0 {
		t.Fatalf("children moved with no record: %d", len(f.moves))
	}
}
