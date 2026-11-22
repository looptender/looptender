//go:build unix

package scenario

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/daemoninfo"
	"github.com/looptender/looptender/internal/state"
	"github.com/looptender/looptender/internal/testexec"
)

// TestScenarioIncidentAdoptsInstalledBuild is the incident under a real
// background daemon started through a `current` link, with a new build
// installed (the link moved) just before the handoff check, and two child
// moves failing in that check. The daemon hands off, ownership follows, and
// at the safe point after the check it moves to the new build with the
// followed owner, as the same process; the new build finishes the child
// moves. A build that declares an older state format is then refused, and
// the daemon carries on. The daemon runs on the wall clock, so the world's
// clock follows it.
func TestScenarioIncidentAdoptsInstalledBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a background daemon on the wall clock")
	}
	t.Parallel()
	w := NewWorld(t, time.Now())
	// The daemon runs stand-ins of its own between the harness's steps.
	w.AllowSignalExits = true
	w.Init(incidentEvents...)
	w.Fallbacks(fallbacksFile(codexNewThreadFallback), func(l map[string]any) {
		l["group"] = "claude-account-a"
		errs, _ := l["errors"].(map[string]any)
		errs["failover_on"] = []string{"rate_limit"}
		l["probe"] = map[string]any{"cli": w.BinPath("claude")}
		// Checks run when the test changes control.json, not on a timer.
		l["daemon"] = map[string]any{"interval": "1h", "control_poll": "200ms"}
	})
	install, launch := adoptionBuilds(t)
	d := startDaemon(t, w, launch)
	stateDir, checked, nudge, adoption, started := d.stateDir, d.checked, d.nudge, d.adoption, d.pid
	checks := &d.checks
	s := checked("daemon: first check", nil)
	w.Check(s, "first wake, owned by the coordinator", all(sentExactly(s, Send{Kind: "tell", Thread: primaryThread, Mode: "auto"}), ownerIs(w, primaryThread, 1)))

	// A new build is installed; the coordinator's lanes run and it reaches
	// its limit; the first two parent changes fail.
	install("b")
	startLanes(w)
	if err := os.WriteFile(filepath.Join(w.world, "fail.update"), []byte("2"), 0o600); err != nil {
		t.Fatal(err)
	}
	hitLimit(w)
	nudge()
	s = checked("daemon: handoff and adoption", func() bool {
		o, _ := adoption()
		return o != "" && o != "switching"
	})
	o, reason := adoption()
	if o == "refused" && strings.Contains(reason, "a directory of the launch path") {
		// The install tree is beside this package's sources, not in the
		// shared temporary directory, so a CI checkout passes the check. In
		// CI a refusal fails the test: the regression must run there.
		if os.Getenv("CI") != "" {
			t.Fatalf("the daemon refused the fixture's directories in CI, so the adoption regression did not run: %s", reason)
		}
		t.Skipf("no directory here passes the daemon's directory checks: %s", reason)
	}
	w.Check(s, "adopted after the handoff", all(
		check(o == "adopted", "adoption %s: %s", o, reason), spawns(w, 1), ownerIs(w, successor, 2),
		targetIs(s, codexCandidate, successor),
	))
	snap, err := daemoninfo.Query(context.Background(), stateDir)
	w.Check(s, "the same process supervises", check(err == nil && snap.ProcessID == started, "daemon %+v %v, started %d", snap, err, started))

	// The new build finishes the moves the old one could not make. The
	// daemon runs its next check by itself (control.json changed with the
	// owner), so the test waits for the moves to be on record, nudging it,
	// before it records the step: a step read in the middle of a check sees
	// some moves and not others.
	moved := func() bool {
		var moves []Move
		readLines(t, filepath.Join(w.world, "moves.jsonl"), &moves)
		st, err := state.Load(stateDir)
		return len(moves) >= len(lanes) && err == nil && st.ChildMove != nil && st.ChildMove.Done
	}
	for deadline := time.Now().Add(30 * time.Second); !moved(); time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the new build did not finish the child moves")
		}
		nudge()
	}
	*checks = countTicks(stateDir)
	nudge()
	s = checked("daemon: next check on the new build", nil)
	converged(w, s)

	// A build that declares an older state format is never switched to.
	install("older")
	nudge()
	s = checked("daemon: an older build installed", func() bool { o, _ := adoption(); return o == "refused" })
	o, reason = adoption()
	w.Check(s, "older build refused", all(
		check(o == "refused" && strings.Contains(reason, "older than this loop's state format"), "adoption %s: %s", o, reason),
		nothingSent(s),
	))
	nudge()
	s = checked("daemon: still supervising", nil)
	converged(w, s)
	snap, err = daemoninfo.Query(context.Background(), stateDir)
	w.Check(s, "the daemon carries on", check(err == nil && snap.ProcessID == started, "daemon %+v %v", snap, err))
	oldThreadRefused(w)
}

// goTool is the go command that built this test, or a skip.
func goTool(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("go"); err == nil {
		return p
	}
	p := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(p); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("the Go toolchain is not available in CI to build a second build")
		}
		t.Skip("the Go toolchain is not available to build a second build")
	}
	return p
}

// adoptionBuilds installs the builds a daemon scenario switches between,
// beside this package's sources: "a" (a copy of the suite's build), "b"
// (the same source with another version label, so different contents) and
// "older" (a script that speaks the handover and declares an older state
// format). install moves the `current` link; launch is the path the daemon
// is started by, through that link.
func adoptionBuilds(t *testing.T) (install func(label string), launch string) {
	t.Helper()
	dir := adoptFixtureDir(t)
	install = func(label string) {
		t.Helper()
		tmp := filepath.Join(dir, "current.tmp")
		os.Remove(tmp)
		if err := os.Symlink(filepath.Join("builds", label), tmp); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, filepath.Join(dir, "current")); err != nil {
			t.Fatal(err)
		}
	}
	// The running build is a copy of the suite's; the new one is the same
	// source with another version label, so its contents differ. Each is
	// run once before the daemon can, so no step is the first to run it.
	for _, label := range []string{"a", "b"} {
		dst := filepath.Join(dir, "builds", label, "looptender")
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			t.Fatal(err)
		}
		var err error
		if label == "a" {
			var b []byte
			if b, err = os.ReadFile(keepgoingBin); err == nil {
				err = os.WriteFile(dst, b, 0o700)
			}
		} else {
			cmd := exec.Command(goTool(t), "build", "-trimpath", "-buildvcs=false", "-ldflags", "-X main.version=fixture-installed", "-o", dst, "../../cmd/looptender")
			if b, berr := cmd.CombinedOutput(); berr != nil {
				t.Fatalf("build: %v\n%s", berr, b)
			}
		}
		if err == nil {
			err = testexec.RunOnce(dst, nil, "version")
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	// Speaks the handover, accepts the configuration, and declares a state
	// format older than any this build reads.
	older := filepath.Join(dir, "builds", "older", "looptender")
	if err := os.MkdirAll(filepath.Dir(older), 0o700); err != nil {
		t.Fatal(err)
	}
	testexec.Script(t, older, "#!/bin/sh\nif [ \"$1\" = __adopt-build ] && [ \"$2\" = --probe ]; then echo '{\"protocol\":1,\"stateFormat\":1,\"version\":\"fixture-older\"}'; exit 0; fi\nexit 0\n")
	install("a")
	launch = filepath.Join(dir, "bin", "looptender")
	if err := os.MkdirAll(filepath.Dir(launch), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "current", "looptender"), launch); err != nil {
		t.Fatal(err)
	}

	return install, launch
}

// daemonRun is a background daemon started for a world, with what the
// scenario needs to step it.
type daemonRun struct {
	stateDir string
	pid      int
	checks   int
	// checked waits for the daemon's next recorded check (and more, when
	// given), then records the step at the wall clock.
	checked func(label string, more func() bool) *Step
	// nudge starts the daemon's next pass: a changed control.json does.
	nudge    func()
	adoption func() (outcome, reason string)
}

// startDaemon starts a background daemon for the world's loop through
// launch, from the coordinator's thread, and kills it at the end.
func startDaemon(t *testing.T, w *World, launch string) *daemonRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := exec.CommandContext(ctx, launch, "start", "--background", "--json", "-c", w.Config)
	start.Dir = w.Root
	start.Env = append(append([]string{}, w.env...), "BB_THREAD_ID="+primaryThread)
	out, err := start.Output()
	var started struct{ PID int }
	if err != nil || json.Unmarshal(out, &started) != nil || started.PID <= 0 {
		t.Fatalf("start: %v %s", err, out)
	}
	proc, _ := os.FindProcess(started.PID)
	t.Cleanup(func() { proc.Kill(); proc.Wait() })
	stateDir := w.runtimeDir()
	d := &daemonRun{stateDir: stateDir, pid: started.PID}
	// A background daemon's own output goes to the null device; what it
	// did is in its records. A failure prints them.
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, f := range []string{filepath.Join(stateDir, state.LogName), filepath.Join(stateDir, "build-adoption.json"), filepath.Join(w.world, "moves.jsonl")} {
			b, err := os.ReadFile(f)
			t.Logf("%s (%v):\n%s", filepath.Base(f), err, b)
		}
	})

	// checked waits for the daemon's next recorded check after the given
	// number of checks, then records the step at the wall clock.
	d.checked = func(label string, more func() bool) *Step {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			n := countTicks(stateDir)
			if n > d.checks && (more == nil || more()) {
				d.checks = n
				break
			}
			if time.Now().After(deadline) {
				b, _ := os.ReadFile(filepath.Join(stateDir, state.LogName))
				t.Fatalf("%s: no check recorded (%d so far)\nruns.jsonl:\n%s", label, n, b)
			}
			time.Sleep(50 * time.Millisecond)
		}
		w.At(time.Now().UTC())
		return w.record(label, Result{}, nil)
	}
	// nudge starts the daemon's next pass: a changed control.json does.
	d.nudge = func() {
		now := time.Now()
		if err := os.Chtimes(filepath.Join(stateDir, "control.json"), now, now); err != nil {
			t.Fatal(err)
		}
	}
	d.adoption = func() (outcome, reason string) {
		var r struct{ Outcome, Reason string }
		b, err := os.ReadFile(filepath.Join(stateDir, "build-adoption.json"))
		if err == nil {
			json.Unmarshal(b, &r)
		}
		return r.Outcome, r.Reason
	}

	return d
}

// countTicks counts the checks recorded in runs.jsonl.
func countTicks(stateDir string) int {
	b, err := os.ReadFile(filepath.Join(stateDir, state.LogName))
	if err != nil {
		return 0
	}
	return strings.Count(string(b), `"event":"tick"`)
}

// adoptFixtureDir is a directory the daemon's directory checks accept: the
// build's link chain must pass through directories only this user can
// write, which a shared temporary directory (sticky, world-writable) is
// not. One beside this package's sources is used, and removed afterwards.
func adoptFixtureDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp(".", ".adopt-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	abs, err := filepath.Abs(d)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
