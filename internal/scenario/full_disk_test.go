//go:build unix

package scenario

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// unwritableAt makes the stand-in bb make the state directory unwritable
// before (or, after true, once) `thread <command>` answers: from there on
// every new record fails as on a full disk.
func (w *World) unwritableAt(command string, after bool) {
	w.t.Helper()
	name := "readonly." + command
	if after {
		name = "readonlyafter." + command
	}
	if err := os.WriteFile(filepath.Join(w.world, name), []byte(w.stateDir()), 0o600); err != nil {
		w.t.Fatal(err)
	}
}

// fullDiskTick runs a check that meets a full disk partway, gives the disk
// its room back, and records the check.
func (w *World) fullDiskTick(label string) *Step {
	w.t.Helper()
	r := w.Run("tick", "-c", w.Config, "--now", w.stamp())
	if err := os.Chmod(w.stateDir(), 0o700); err != nil {
		w.t.Fatal(err)
	}
	return w.record(label, r, nil)
}

func noRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes an unwritable directory")
	}
}

// TestFaultFullDisk: a check whose records stop being saved partway, on the
// built binary (TestScenarioUnwritableStateDirectory covers whole checks
// and control commands). Nothing is sent without its record, nothing sent
// is sent twice, and the check says it failed.
func TestFaultFullDisk(t *testing.T) {
	noRoot(t)
	t.Parallel()

	t.Run("before the record of a turn retry", func(t *testing.T) {
		t.Parallel()
		w := NewWorld(t, scenarioStart)
		w.Init("await_user")
		w.Control("enable")
		failingTurns(w.Thread(primaryThread))
		w.unwritableAt("show", false)
		w.Advance(time.Minute)
		s := w.fullDiskTick("check on a full disk")
		w.Check(s, "nothing was sent", nothingSent(s))
		w.Check(s, "the check failed and said so", check(s.Result.Code != 0 && s.Result.Stderr != "", "exit %d, stderr %q", s.Result.Code, s.Result.Stderr))
		w.Advance(time.Minute)
		s = w.Tick()
		w.Check(s, "with room again the retry is made, once", all(action(s, "reprompt"), sentExactly(s, Send{Kind: "retry", Thread: primaryThread}), holds(s)))
	})

	t.Run("after a turn retry, before the check is saved", func(t *testing.T) {
		t.Parallel()
		w := NewWorld(t, scenarioStart)
		w.Init("await_user")
		w.Control("enable")
		failingTurns(w.Thread(primaryThread))
		w.unwritableAt("retry", true)
		w.Advance(time.Minute)
		s := w.fullDiskTick("check on a full disk")
		w.Check(s, "the retry was sent", sentExactly(s, Send{Kind: "retry", Thread: primaryThread}))
		w.Check(s, "the check failed and said so", check(s.Result.Code != 0 && s.Result.Stderr != "", "exit %d, stderr %q", s.Result.Code, s.Result.Stderr))
		w.Advance(time.Minute)
		s = w.Tick()
		w.Check(s, "the retry is not repeated inside its backoff", all(action(s, "none"), nothingSent(s), holds(s)))
	})

}
