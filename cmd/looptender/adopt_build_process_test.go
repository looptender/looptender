//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/daemoninfo"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/state"
)

// A background daemon started through a link to the installed build moves
// to a new build installed there, after a check, as the same process with
// its holds, owner and state; and stays when the next build does not
// validate. Real builds of this command; a stand-in agent and a stand-in
// broken build. Nothing outside the test's directory is touched.
func TestBackgroundDaemonAdoptsTheInstalledBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the command twice and waits for two checks")
	}
	dir := adoptTestDir(t)
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	build := func(label string) {
		t.Helper()
		out := filepath.Join(dir, "builds", label, "looptender")
		// As installed builds are made: a stamped label, -trimpath, and no
		// version-control data, which adoption must not need.
		cmd := exec.Command(goBin, "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w -X main.version=fixture-"+label, "-o", out, ".")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", label, err, b)
		}
	}
	build("old")
	build("new")
	if b, err := exec.Command(goBin, "version", "-m", filepath.Join(dir, "builds", "new", "looptender")).Output(); err != nil || strings.Contains(string(b), "vcs.") {
		t.Fatalf("fixture build carries version-control data: %v\n%s", err, b)
	}
	bad := filepath.Join(dir, "builds", "bad", "looptender")
	if err := os.MkdirAll(filepath.Dir(bad), 0o700); err != nil {
		t.Fatal(err)
	}
	// Speaks the handover but refuses this loop's configuration.
	if err := os.WriteFile(bad, []byte("#!/bin/sh\nif [ \"$1\" = __adopt-build ] && [ \"$2\" = --probe ]; then echo '{\"protocol\":1,\"stateFormat\":8,\"version\":\"fixture-bad\"}'; exit 0; fi\nexit 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	install := func(label string) {
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
	install("old")
	launch := filepath.Join(dir, "bin", "looptender")
	if err := os.MkdirAll(filepath.Dir(launch), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "current", "looptender"), launch); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(dir, "agent.sh")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\n[ \"$1\" = status ] && echo '{\"state\":\"active\"}'\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	quote := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	cfg := filepath.Join(dir, "looptender.yaml")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf(`version: 1
loop:
  name: example-adopt
  state_dir: state
  adapter: process
  process:
    status_command: [%[1]s, status]
    resume_command: [%[1]s, resume]
    stop_command: [%[1]s, stop]
  prompt: Continue the fictional example.
  daemon: {interval: 10s, control_poll: 100ms}
`, quote(agent))), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, "state")
	if err := control.Save(stateDir, control.File{Paused: true, Note: "fictional hold kept across builds"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := exec.CommandContext(ctx, launch, "start", "--background", "--json", "-c", cfg)
	start.Env = append(os.Environ(), "BB_THREAD_ID=thr_adopt_fixture")
	out, err := start.Output()
	if err != nil {
		t.Fatalf("start: %v %s", err, out)
	}
	var started struct{ PID int }
	if err := json.Unmarshal(out, &started); err != nil || started.PID <= 0 {
		t.Fatalf("start: %s", out)
	}
	proc, _ := os.FindProcess(started.PID)
	t.Cleanup(func() {
		_ = proc.Kill()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if held, _ := lock.Held(stateDir, lock.Daemon); !held {
				return
			}
		}
	})
	query := func() daemoninfo.Snapshot {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		s, _ := daemoninfo.Query(ctx, stateDir)
		return s
	}
	waitFor := func(what string, budget time.Duration, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(budget); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if ok() {
				return
			}
		}
		b, _ := os.ReadFile(filepath.Join(stateDir, state.LogName))
		r, _ := os.ReadFile(filepath.Join(stateDir, adoptRecordName))
		t.Fatalf("%s not reached; daemon %+v\nrecord %s\nruns %s", what, query(), r, b)
	}
	waitFor("old build answering", 10*time.Second, func() bool { return query().Version == "fixture-old" })
	startedAt := query().StartedAt
	ctlBefore, err := os.ReadFile(filepath.Join(stateDir, control.FileName))
	if err != nil {
		t.Fatal(err)
	}
	install("new")
	waitFor("new build answering", 40*time.Second, func() bool { return query().Version == "fixture-new" })
	if s := query(); s.ProcessID != started.PID {
		t.Fatalf("a new process: %d, started %d", s.ProcessID, started.PID)
	}
	// The same process: its start is the one it had before the switch, and
	// the switch is when the new build took over.
	if s := query(); !s.StartedAt.Equal(startedAt) || s.BuildStartedAt == nil || !s.BuildStartedAt.After(startedAt) {
		t.Fatalf("start across the switch: %v, before %v, build %v", s.StartedAt, startedAt, s.BuildStartedAt)
	}
	// The process list still names the loop's configuration.
	if b, err := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(started.PID)).Output(); err != nil || !strings.Contains(string(b), adoptCommandName+" --handover-fd ") || !strings.Contains(string(b), " -c "+cfg) {
		t.Fatalf("ps: %v %q", err, b)
	}
	ctlAfter, _ := os.ReadFile(filepath.Join(stateDir, control.FileName))
	if !bytes.Equal(ctlBefore, ctlAfter) {
		t.Fatalf("control changed across the switch:\n%s\n%s", ctlBefore, ctlAfter)
	}
	ctl, _ := control.Load(stateDir)
	if !ctl.Paused || ctl.Note != "fictional hold kept across builds" || ctl.Ownership == nil || ctl.Ownership.ThreadID != "thr_adopt_fixture" || ctl.Ownership.Generation != 1 {
		t.Fatalf("control: %+v", ctl)
	}
	if r, err := loadAdoptRecord(stateDir); err != nil || r.Outcome != adoptAdopted || r.From.Label != "fixture-old" || r.To.Label != "fixture-new" || r.Mode != "background" {
		t.Fatalf("record: %+v %v", r, err)
	}
	runs, _ := os.ReadFile(filepath.Join(stateDir, state.LogName))
	if !strings.Contains(string(runs), `"build_switching"`) || !strings.Contains(string(runs), `"build_adopted"`) {
		t.Fatalf("runs.jsonl: %s", runs)
	}
	// Every daemon event names the loop, as check events do.
	for _, line := range strings.Split(strings.TrimSpace(string(runs)), "\n") {
		if strings.Contains(line, `"event":"daemon"`) && !strings.Contains(line, `"loop":"example-adopt"`) {
			t.Fatalf("daemon event without the loop's name: %s", line)
		}
	}
	// The status of the new build says what happened.
	st := exec.Command(launch, "status", "-c", cfg)
	b, _ := st.CombinedOutput()
	if !strings.Contains(string(b), "daemon build change: The daemon switched from fixture-old") {
		t.Fatalf("status: %s", b)
	}
	// A build that does not validate this loop is refused; the daemon stays.
	install("bad")
	waitFor("refusal recorded", 40*time.Second, func() bool {
		r, err := loadAdoptRecord(stateDir)
		return err == nil && r.Outcome == adoptRefused
	})
	if r, _ := loadAdoptRecord(stateDir); !strings.Contains(r.Reason, "does not validate") || r.From.Label != "fixture-new" {
		t.Fatalf("record: %+v", r)
	}
	if s := query(); s.Version != "fixture-new" || s.ProcessID != started.PID {
		t.Fatalf("daemon after refusal: %+v", s)
	}
}

// adoptTestDir is a directory the daemon's directory checks accept: the
// test's temporary directory when no other user can write above it, else
// one beside this package's sources, removed afterwards.
func adoptTestDir(t *testing.T) string {
	t.Helper()
	if d, err := filepath.EvalSymlinks(t.TempDir()); err == nil && adoptDirsRefusal([]string{d}) == "" {
		return d
	}
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
	if r := adoptDirsRefusal([]string{abs}); r != "" {
		t.Skipf("no directory here passes the daemon's directory checks: %s", r)
	}
	return abs
}

// A switch between this build and one that speaks the earlier handover
// (adoptEarlierForm: no configArg in its probe answer, exactly
// `--handover-fd N` accepted), in both directions: the same process goes
// on supervising, and the earlier form is never handed `-c`, which it
// would refuse, leaving no supervisor.
func TestDaemonSwitchesToAndFromTheEarlierHandoverForm(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the command twice and waits for two switches")
	}
	dir := adoptTestDir(t)
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	build := func(label, ldflags string) {
		t.Helper()
		out := filepath.Join(dir, "builds", label, "looptender")
		cmd := exec.Command(goBin, "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w -X main.version=fixture-"+label+ldflags, "-o", out, ".")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", label, err, b)
		}
	}
	build("earlier", " -X main.adoptEarlierForm=1")
	build("new", "")
	// The earlier form answers the probe as a released earlier build does.
	if b, err := exec.Command(filepath.Join(dir, "builds", "earlier", "looptender"), adoptCommandName, "--probe").Output(); err != nil || strings.Contains(string(b), "configArg") {
		t.Fatalf("earlier probe: %v %s", err, b)
	}
	if code := exec.Command(filepath.Join(dir, "builds", "earlier", "looptender"), adoptCommandName, "--handover-fd", "9", "-c", filepath.Join(dir, "x.yaml")).Run(); code == nil {
		t.Fatal("the earlier form accepted -c")
	}
	// Each install is a copy under its own name, so every switch is to a
	// file with its own path, as a release is.
	n := 0
	install := func(label string) {
		t.Helper()
		n++
		dst := filepath.Join(dir, "builds", fmt.Sprintf("%s-%d", label, n))
		if err := os.MkdirAll(dst, 0o700); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dir, "builds", label, "looptender"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, "looptender"), b, 0o700); err != nil {
			t.Fatal(err)
		}
		tmp := filepath.Join(dir, "current.tmp")
		os.Remove(tmp)
		if err := os.Symlink(filepath.Join("builds", filepath.Base(dst)), tmp); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, filepath.Join(dir, "current")); err != nil {
			t.Fatal(err)
		}
	}
	launch := filepath.Join(dir, "bin", "looptender")
	if err := os.MkdirAll(filepath.Dir(launch), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "current", "looptender"), launch); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(dir, "agent.sh")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\n[ \"$1\" = status ] && echo '{\"state\":\"active\"}'\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	quote := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	cfg := filepath.Join(dir, "looptender.yaml")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf(`version: 1
loop:
  name: example-adopt-forms
  state_dir: state
  adapter: process
  process:
    status_command: [%[1]s, status]
    resume_command: [%[1]s, resume]
    stop_command: [%[1]s, stop]
  prompt: Continue the fictional example.
  daemon: {interval: 10s, control_poll: 100ms}
`, quote(agent))), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, "state")
	if err := control.Save(stateDir, control.File{Paused: true, Note: "fictional hold kept across builds"}); err != nil {
		t.Fatal(err)
	}
	query := func() daemoninfo.Snapshot {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		s, _ := daemoninfo.Query(ctx, stateDir)
		return s
	}
	waitFor := func(what string, budget time.Duration, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(budget); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if ok() {
				return
			}
		}
		b, _ := os.ReadFile(filepath.Join(stateDir, state.LogName))
		r, _ := os.ReadFile(filepath.Join(stateDir, adoptRecordName))
		t.Fatalf("%s not reached; daemon %+v\nrecord %s\nruns %s", what, query(), r, b)
	}
	args := func(pid int) string {
		b, err := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			t.Fatalf("ps: %v", err)
		}
		return string(b)
	}
	start := func(label string) int {
		t.Helper()
		install(label)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, launch, "start", "--background", "--json", "-c", cfg)
		cmd.Env = append(os.Environ(), "BB_THREAD_ID=thr_adopt_fixture")
		out, err := cmd.Output()
		var started struct{ PID int }
		if err != nil || json.Unmarshal(out, &started) != nil || started.PID <= 0 {
			t.Fatalf("start: %v %s", err, out)
		}
		proc, _ := os.FindProcess(started.PID)
		t.Cleanup(func() {
			_ = proc.Kill()
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				if held, _ := lock.Held(stateDir, lock.Daemon); !held {
					return
				}
			}
		})
		waitFor("fixture-"+label+" answering", 10*time.Second, func() bool { return query().Version == "fixture-"+label })
		return started.PID
	}
	switchTo := func(pid int, label, from string) daemoninfo.Snapshot {
		t.Helper()
		install(label)
		waitFor("fixture-"+label+" answering", 40*time.Second, func() bool { return query().Version == "fixture-"+label })
		s := query()
		if s.ProcessID != pid {
			t.Fatalf("a new process: %d, started %d", s.ProcessID, pid)
		}
		if r, err := loadAdoptRecord(stateDir); err != nil || r.Outcome != adoptAdopted || r.From.Label != "fixture-"+from || r.To.Label != "fixture-"+label {
			t.Fatalf("record: %+v %v", r, err)
		}
		if ctl, _ := control.Load(stateDir); !ctl.Paused || ctl.Note != "fictional hold kept across builds" || ctl.Ownership == nil || ctl.Ownership.ThreadID != "thr_adopt_fixture" {
			t.Fatalf("control: %+v", ctl)
		}
		return s
	}

	// Earlier to new: the earlier form hands over only the descriptor and
	// no start, so the new build runs on with the switch as its start.
	pid := start("earlier")
	if a := args(pid); !strings.Contains(a, "__start-background") {
		t.Fatalf("ps before: %q", a)
	}
	s := switchTo(pid, "new", "earlier")
	if a := args(pid); !strings.Contains(a, adoptCommandName+" --handover-fd ") || strings.Contains(a, " -c ") {
		t.Fatalf("ps after the earlier form handed over: %q", a)
	}
	if s.BuildStartedAt != nil {
		t.Fatalf("a start carried from the earlier form: %+v", s)
	}
	// New to earlier: the earlier form's probe has no configArg, so it is
	// handed exactly `--handover-fd N`; given `-c` it would refuse and the
	// process would end, which waitFor would report.
	switchTo(pid, "earlier", "new")
	if a := args(pid); !strings.Contains(a, adoptCommandName+" --handover-fd ") || strings.Contains(a, " -c ") {
		t.Fatalf("ps after handing to the earlier form: %q", a)
	}
	if held, _ := lock.Held(stateDir, lock.Daemon); !held {
		t.Fatal("no daemon holds the loop")
	}
	// And forward again, from the earlier form to the new one.
	switchTo(pid, "new", "earlier")
}
