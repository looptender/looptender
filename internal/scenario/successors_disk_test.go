//go:build unix

package scenario

import (
	"os"
	"testing"
	"time"
)

// TestFaultArmedIncidentFullDisk (L): the state directory stops taking
// writes, as on a full disk, at each external step of the armed incident.
// bb may still do what it was asked, but nothing is created or moved
// without its record first, nothing is created twice, and once there is
// room the loop converges: one created thread owning the loop, the
// children under it, one successor for each blocked lane.
func TestFaultArmedIncidentFullDisk(t *testing.T) {
	noRoot(t)
	t.Parallel()
	converge := func(w *World, children []string) {
		w.t.Helper()
		for i := 0; i < 3; i++ {
			w.Advance(10 * time.Minute)
			s := w.Tick()
			w.Check(s, "with room again", exit(s, 0))
		}
		s := w.Journal[len(w.Journal)-1]
		w.Check(s, "converged", all(
			spawns(w, 3), targetIs(s, initCandidate, successor), ownerIs(w, successor, 2), childrenOf(w, successor, append(append([]string{}, children...), successorsOf(w)...)...),
			replacedExactly(w, blockedLane(1), blockedLane(2)), successorsAre(s, 2, 2), noSendsTo(w, children...), holds(s),
		))
	}
	t.Run("before the created thread's spawn", func(t *testing.T) {
		t.Parallel()
		w := NewWorld(t, scenarioStart)
		children, _ := armIncident(w, armed{blocked: 2})
		w.unwritableAt("spawn", false)
		s := w.fullDiskTick("handoff on a full disk")
		w.Check(s, "created once, the check failed", all(spawns(w, 1), check(s.Result.Code != 0, "exit %d", s.Result.Code)))
		converge(w, children)
	})
	t.Run("before the first parent change", func(t *testing.T) {
		t.Parallel()
		w := NewWorld(t, scenarioStart)
		children, _ := armIncident(w, armed{blocked: 2})
		w.unwritableAt("update", false)
		s := w.fullDiskTick("child moves on a full disk")
		w.Check(s, "created once", spawns(w, 1))
		converge(w, children)
	})
	t.Run("before a successor's spawn", func(t *testing.T) {
		t.Parallel()
		w := NewWorld(t, scenarioStart)
		children, _ := armIncident(w, armed{blocked: 2})
		handedOff(w)
		w.unwritableAt("spawn", false)
		s := w.fullDiskTick("successor on a full disk")
		w.Check(s, "one successor asked for, then nothing", check(len(successorSpawns(w)) == 1, "%d successors", len(successorSpawns(w))))
		converge(w, children)
	})
	t.Run("from the start of the successor pass", func(t *testing.T) {
		t.Parallel()
		w := NewWorld(t, scenarioStart)
		children, _ := armIncident(w, armed{blocked: 2})
		handedOff(w)
		w.unwritableAt("show", false)
		s := w.fullDiskTick("successor pass on a full disk")
		w.Check(s, "nothing asked of bb without its record", check(len(successorSpawns(w)) == 0, "%d successors", len(successorSpawns(w))))
		converge(w, children)
	})
	t.Run("a pause before the disk fills", func(t *testing.T) {
		t.Parallel()
		w := NewWorld(t, scenarioStart)
		children, _ := armIncident(w, armed{blocked: 2})
		handedOff(w)
		s := w.Control("pause")
		w.Check(s, "pause", exit(s, 0))
		// A held check runs no bb command: the disk fills before it.
		if err := os.Chmod(w.stateDir(), 0o500); err != nil {
			t.Fatal(err)
		}
		w.Advance(time.Minute)
		s = w.fullDiskTick("held on a full disk")
		w.Check(s, "the hold is kept and nothing is done", all(nothingSent(s), holds(s, "paused")))
		w.Advance(time.Minute)
		s = w.Tick()
		w.Check(s, "still paused with room again", all(nothingSent(s), holds(s, "paused")))
		s = w.Control("resume")
		w.Check(s, "resume", exit(s, 0))
		converge(w, children)
	})
}
