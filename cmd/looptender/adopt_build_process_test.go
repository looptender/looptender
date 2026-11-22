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
	if err := os.WriteFile(bad, []byte("#!/bin/sh\nif [ \"$1\" = __adopt-build ] && [ \"$2\" = --probe ]; then echo '{\"protocol\":1,\"stateFormat\":2,\"version\":\"fixture-bad\"}'; exit 0; fi\nexit 2\n"), 0o700); err != nil {
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
	ctlBefore, err := os.ReadFile(filepath.Join(stateDir, control.FileName))
	if err != nil {
		t.Fatal(err)
	}
	install("new")
	waitFor("new build answering", 40*time.Second, func() bool { return query().Version == "fixture-new" })
	if s := query(); s.ProcessID != started.PID {
		t.Fatalf("a new process: %d, started %d", s.ProcessID, started.PID)
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
