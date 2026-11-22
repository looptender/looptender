//go:build unix

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/session"
	"github.com/looptender/looptender/internal/state"
)

// TestScenarioMovedLoopWithNoSupervisorIsLoudUntilOneStarts is the case
// that went unseen for hours: a loop moved to another conversation by
// transfer apply, resumed by a person, and no supervisor started for it.
// Run through the built command with a stand-in bb: after the grace period,
// status, doctor (exit 1), panel list and the status data the BB plugin
// reads all say nothing supervises it; starting a daemon clears it.
func TestScenarioMovedLoopWithNoSupervisorIsLoudUntilOneStarts(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the command and runs a background daemon")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	path, dir := transferFixture(t)
	if err := control.Save(dir, control.File{Enabled: true, Ownership: &control.Ownership{ThreadID: "thr_source", Generation: 1}}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Add(-time.Hour)
	if err := state.Save(dir, state.File{ActiveTarget: state.Primary, LastCheck: &state.Check{At: before, Target: "primary"}, CheckedAt: before}); err != nil {
		t.Fatal(err)
	}
	world := t.TempDir()
	fakeDir := filepath.Join(world, "bb")
	if err := os.Mkdir(fakeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(world, "bb-cli")
	write(t, cli, fakeBB, 0o700)
	raw, _ := os.ReadFile(path)
	quoted, _ := json.Marshal(cli)
	raw = []byte(strings.Replace(string(raw), "  bb:\n", "  bb:\n    cli: "+string(quoted)+"\n", 1))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"thr_source", "thr_destination"} {
		b, _ := json.Marshal(transferResponse(id))
		if err := os.WriteFile(filepath.Join(fakeDir, id+".show"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(world, "looptender")
	if out, err := exec.Command(goBin, "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := filepath.Join(world, "home")
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := func(thread string, args ...string) (int, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		c := exec.CommandContext(ctx, bin, args...)
		// Its own registry and session directory: the package's tests share
		// theirs through the environment.
		c.Env = append(os.Environ(), "FAKE_BB="+fakeDir, "BB_THREAD_ID="+thread, "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
			registryDirEnv+"="+filepath.Join(world, "registry"), session.DirEnv+"="+filepath.Join(world, "sessions"))
		out, err := c.Output()
		var x *exec.ExitError
		switch {
		case err == nil:
			return 0, string(out)
		case errors.As(err, &x):
			return x.ExitCode(), string(out) + string(x.Stderr)
		}
		t.Fatalf("%v: %v", args, err)
		return -1, ""
	}
	// The move, by the owning conversation; then a person resumes it.
	if code, out := cmd("thr_source", "transfer", "apply", "-c", path, "--from", "thr_source", "--thread", "thr_destination"); code != 0 {
		t.Fatalf("transfer apply: %d %s", code, out)
	}
	if st, err := state.Load(dir); err != nil || !st.CheckedAt.IsZero() {
		t.Fatalf("the move kept a check record: %+v %v", st.CheckedAt, err)
	}
	if code, out := cmd("", "resume", "-c", path); code != 0 {
		t.Fatalf("resume: %d %s", code, out)
	}
	type statusView struct {
		Schedule struct {
			Overdue bool       `json:"overdue"`
			Basis   string     `json:"basis"`
			Message string     `json:"message"`
			Since   *time.Time `json:"since"`
		} `json:"schedule"`
	}
	status := func() (statusView, string) {
		t.Helper()
		_, js := cmd("", "status", "-c", path, "--json")
		var v statusView
		if err := json.Unmarshal([]byte(js), &v); err != nil {
			t.Fatalf("status --json: %v\n%s", err, js)
		}
		_, text := cmd("", "status", "-c", path)
		return v, text
	}
	if v, _ := status(); v.Schedule.Overdue {
		t.Fatalf("alarmed within the grace period: %+v", v.Schedule)
	}
	// The grace period passes: the resume is made an hour old, as if no one
	// had started a supervisor in that hour.
	ctlPath := filepath.Join(dir, control.FileName)
	b, _ := os.ReadFile(ctlPath)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	resumed := time.Now().UTC().Add(-time.Hour)
	m["updatedAt"] = resumed.Format(time.RFC3339Nano)
	b, _ = json.Marshal(m)
	if err := os.WriteFile(ctlPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	// status, and the status data the BB plugin reads (schedule.overdue and
	// its message are what its card and `bb keepgoing status` show).
	v, text := status()
	if !v.Schedule.Overdue || v.Schedule.Basis != "never_checked" || v.Schedule.Since == nil || !strings.Contains(v.Schedule.Message, "nothing has checked it") {
		t.Fatalf("status --json: %+v", v.Schedule)
	}
	if !strings.Contains(text, "schedule:  it is on and not held, but nothing has checked it since it was enabled, resumed or moved") {
		t.Fatalf("status:\n%s", text)
	}
	// doctor: an error, exit 1.
	code, js := cmd("", "doctor", "-c", path, "--json", "--offline")
	var report doctorReport
	if err := json.Unmarshal([]byte(js), &report); err != nil {
		t.Fatalf("doctor --json: %v\n%s", err, js)
	}
	if c := doctorFinding(report, "supervisor.schedule"); code != exitFailed || c == nil || c.Status != "error" {
		t.Fatalf("doctor: %d %+v", code, c)
	}
	// panel list: the loop needs the person, dated from the resume, which is
	// what the macOS menu tells a person about.
	if code, out := cmd("", "registry", "add", "-c", path); code != 0 {
		t.Fatalf("registry add: %d %s", code, out)
	}
	panel := func() panelListLoop {
		t.Helper()
		code, js := cmd("", "panel", "list", "--json")
		var list struct {
			Loops []panelListLoop `json:"loops"`
		}
		if err := json.Unmarshal([]byte(js), &list); err != nil || len(list.Loops) != 1 {
			t.Fatalf("panel list --json: %d %v\n%s", code, err, js)
		}
		return list.Loops[0]
	}
	if l := panel(); l.OverdueSince == nil || !l.NeedsYou || l.Phrase != "Not supervised" {
		t.Fatalf("panel list: %+v", l)
	}
	// Starting a supervisor clears it: its first check is recorded.
	code, out := cmd("thr_destination", "start", "--background", "--json", "-c", path)
	var started struct{ PID int }
	if code != 0 || json.Unmarshal([]byte(strings.TrimSpace(out)), &started) != nil || started.PID <= 0 {
		t.Fatalf("start: %d %s", code, out)
	}
	if proc, err := os.FindProcess(started.PID); err == nil {
		t.Cleanup(func() { _ = proc.Kill() })
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if st, err := state.Load(dir); err == nil && !st.CheckedAt.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the daemon recorded no check")
		}
	}
	if v, text := status(); v.Schedule.Overdue || strings.Contains(text, "schedule:  it is on and not held") {
		t.Fatalf("still alarmed with a supervisor running: %+v\n%s", v.Schedule, text)
	}
	_, js = cmd("", "doctor", "-c", path, "--json", "--offline")
	report = doctorReport{}
	if err := json.Unmarshal([]byte(js), &report); err != nil {
		t.Fatal(js)
	}
	if c := doctorFinding(report, "supervisor.schedule"); c != nil && c.Status == "error" {
		t.Fatalf("doctor still reports it: %+v", c)
	}
	if l := panel(); l.OverdueSince != nil {
		t.Fatalf("panel list still overdue: %+v", l)
	}
}
