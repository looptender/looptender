//go:build unix

package scenario

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/state"
)

// TestScenarioArmedIncidentAdoptsInstalledBuild (M): the armed incident
// under a background daemon, with a new build installed just before the
// handoff. The daemon hands off and moves the children, adopts the new
// build at the safe point with the successor pass still to run, and the new
// build creates exactly the two successor lanes. A build declaring an older
// state format is then refused, and the daemon carries on. Wall clock.
func TestScenarioArmedIncidentAdoptsInstalledBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a background daemon on the wall clock")
	}
	t.Parallel()
	w := NewWorld(t, time.Now())
	w.AllowSignalExits = true
	codexUsable(w)
	w.InitWith("--new-thread-provider", "none")
	w.EditConfig(func(l map[string]any) {
		l["report"] = map[string]any{"events": incidentEvents, "timeout": "5s"}
		l["daemon"] = map[string]any{"interval": "1h", "control_poll": "200ms"}
	})
	s := w.record("enable as owner", w.RunAs(primaryThread, "enable", "-c", w.Config), nil)
	w.Check(s, "enable", exit(s, 0))
	if r := w.RunAs(primaryThread, "fallbacks", "arm", "-c", w.Config, "--yes"); r.Code != 0 {
		t.Fatalf("fallbacks arm: %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	w.initCalls()
	w.editFallback(initCandidate, func(bb map[string]any) {
		bb["child_model"], bb["child_reasoning"] = childModel, childReasoning
	})
	install, launch := adoptionBuilds(t)
	d := startDaemon(t, w, launch)
	s = d.checked("daemon: first check", nil)
	w.Check(s, "first wake", sentExactly(s, Send{Kind: "tell", Thread: primaryThread, Mode: "auto"}))

	// Lanes, two of them blocked; a new build installed; the limit.
	reset := time.Now().Add(incidentReset)
	primary := w.Thread(primaryThread)
	var children []string
	for i := 1; i <= 2; i++ {
		c := primary.Child(blockedLane(i))
		c.Provider, c.Permission = "claude-code", "auto"
		c.StartTurn()
		c.RateLimited(reset)
		children = append(children, c.ID)
	}
	idle := primary.Child(idleLane)
	idle.Provider = "claude-code"
	idle.FinishTurn()
	children = append(children, idleLane)
	install("b")
	w.At(time.Now().UTC())
	primary.RateLimited(reset)
	d.nudge()
	// The new build makes the successor pass after the handoff check.
	done := func() bool {
		st, err := state.Load(d.stateDir)
		o, _ := d.adoption()
		return err == nil && o != "" && o != "switching" && st.ChildSuccessors != nil && st.ChildSuccessors.Done
	}
	for deadline := time.Now().Add(40 * time.Second); !done(); time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			o, r := d.adoption()
			t.Fatalf("no successor pass after the adoption (adoption %s: %s)", o, r)
		}
		d.nudge()
	}
	d.checks = countTicks(d.stateDir)
	d.nudge()
	s = d.checked("daemon: after the successor pass", nil)
	o, reason := d.adoption()
	w.Check(s, "adopted, handed off, replaced", all(
		check(o == "adopted", "adoption %s: %s", o, reason), spawns(w, 3), ownerIs(w, successor, 2),
		replacedExactly(w, blockedLane(1), blockedLane(2)), successorsAre(s, 2, 2), noSendsTo(w, children...),
	))

	install("older")
	d.nudge()
	s = d.checked("daemon: an older build installed", func() bool { o, _ := d.adoption(); return o == "refused" })
	o, reason = d.adoption()
	w.Check(s, "older build refused", all(check(o == "refused" && strings.Contains(reason, "older than this loop's state format"), "adoption %s: %s", o, reason),
		check(len(successorSpawns(w)) == 2, "%d successors", len(successorSpawns(w)))))
	if _, err := os.Stat(filepath.Join(d.stateDir, "build-adoption.json")); err != nil {
		t.Fatal(err)
	}
}
