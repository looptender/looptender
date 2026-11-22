package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
)

// backdateControl saves control with its last change at the given time, as
// a loop enabled, resumed or moved then.
func setControlAt(t *testing.T, dir string, f control.File, at time.Time) {
	t.Helper()
	if err := control.Save(dir, f); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, control.FileName)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["updatedAt"] = at.UTC().Format(time.RFC3339Nano)
	b, _ = json.Marshal(m)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := control.Load(dir); err != nil || !got.UpdatedAt.Equal(at.UTC()) {
		t.Fatalf("backdated control: %+v %v", got.UpdatedAt, err)
	}
}

func statusOf(t *testing.T, f *doctorFixture, now time.Time) (statusOut, string) {
	t.Helper()
	cfg, err := config.Load(f.config)
	if err != nil {
		t.Fatal(err)
	}
	var js, text bytes.Buffer
	status(env{context.Background(), &js, &js}, cfg, now, true, defaultStaleAfter)
	status(env{context.Background(), &text, &text}, cfg, now, false, defaultStaleAfter)
	var out statusOut
	if err := json.Unmarshal(js.Bytes(), &out); err != nil {
		t.Fatal(js.String())
	}
	return out, text.String()
}

func doctorOf(t *testing.T, f *doctorFixture) (int, doctorReport) {
	t.Helper()
	var out bytes.Buffer
	code := run(context.Background(), []string{"doctor", "-c", f.config, "--json", "--offline"}, &out, &out)
	var r doctorReport
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(out.String())
	}
	return code, r
}

func doctorFinding(r doctorReport, id string) *doctorCheck {
	for i := range r.Checks {
		if r.Checks[i].ID == id {
			return &r.Checks[i]
		}
	}
	return nil
}

// F1: an enabled, unheld loop with no recorded check and nothing checking
// it (every loop just moved or transferred has no check record) is loud in
// status and doctor, measured from its last control change; held, complete
// and disabled loops are not.
func TestNeverCheckedLoopIsLoud(t *testing.T) {
	now := time.Now().UTC()
	f := newDoctorFixture(t)
	if err := os.MkdirAll(f.state, 0o700); err != nil {
		t.Fatal(err)
	}
	setControlAt(t, f.state, control.File{Enabled: true}, now.Add(-5*time.Hour))
	out, text := statusOf(t, f, now)
	if !out.Schedule.Overdue || out.Schedule.Basis != "never_checked" || out.Schedule.Since == nil || !strings.Contains(out.Schedule.Message, "nothing has checked it since it was enabled, resumed or moved") {
		t.Fatalf("%+v", out.Schedule)
	}
	if !strings.Contains(text, "schedule:  it is on and not held, but nothing has checked it") || !strings.Contains(text, "looptender start -c CONFIG") {
		t.Fatalf("%s", text)
	}
	code, r := doctorOf(t, f)
	if c := doctorFinding(r, "supervisor.schedule"); code != exitFailed || c == nil || c.Status != "error" || !strings.Contains(c.Message, "Unsupervised") {
		t.Fatalf("doctor %d %+v", code, c)
	}
	for name, hold := range map[string]control.File{
		"paused": {Enabled: true, Paused: true}, "awaiting a person": {Enabled: true, AwaitingUser: true},
		"complete": {Enabled: true, Completed: true}, "disabled": {},
	} {
		setControlAt(t, f.state, hold, now.Add(-5*time.Hour))
		out, text := statusOf(t, f, now)
		if out.Schedule.Overdue || strings.Contains(text, "schedule:") {
			t.Fatalf("%s: %+v", name, out.Schedule)
		}
		if _, r := doctorOf(t, f); doctorFinding(r, "supervisor.schedule") != nil && doctorFinding(r, "supervisor.schedule").Status == "error" {
			t.Fatalf("%s: doctor error", name)
		}
	}
	// Just enabled: within the grace period.
	setControlAt(t, f.state, control.File{Enabled: true}, now.Add(-2*time.Minute))
	if out, _ := statusOf(t, f, now); out.Schedule.Overdue {
		t.Fatalf("within grace: %+v", out.Schedule)
	}
}

// F1 in the panel and the menu's data: the overdue need, dated from the
// control change, and a phrase that says nothing supervises the loop.
func TestPanelRaisesANeverCheckedLoop(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	since := now.Add(-5 * time.Hour)
	in := dockInput{Client: clientStatusReport{Outcome: "available", Snapshot: &clientSnapshot{Loop: "example-loop",
		Controls: &clientControls{Enabled: true},
		Runtime:  &clientRuntime{State: "unknown", Freshness: "never", Overdue: true, OverdueSince: &since, OverdueMessage: "it is on and not held, but nothing has checked it since it was enabled, resumed or moved"},
	}}, Status: &statusOut{Loop: "example-loop", ControlState: "enabled"}}
	l, need := projectDockLoop(in, now, 30*time.Minute, false)
	if need == nil || need.Kind != "overdue" || need.Since == nil || !need.Since.Equal(since) || !strings.Contains(need.Reason, "Nothing is supervising it") || l.Phrase != "Not supervised" {
		t.Fatalf("%+v %+v", l, need)
	}
	list := buildPanelList(dockReport{Loops: []dockLoop{{Key: "l0123456789ab", Name: "example-loop"}}, NeedsYou: []dockNeed{{Kind: "overdue", Key: "l0123456789ab", Since: need.Since}}})
	if list.Loops[0].OverdueSince == nil || !list.Loops[0].OverdueSince.Equal(since) {
		t.Fatalf("%+v", list.Loops[0])
	}
}

// F3: a loop ticked every ten minutes by something other than a daemon says
// so, from the recorded checks, instead of "next check unknown".
func TestTickedLoopSaysItIsCheckedByTicks(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f := newDoctorFixture(t)
	if err := os.MkdirAll(f.state, 0o700); err != nil {
		t.Fatal(err)
	}
	setControlAt(t, f.state, control.File{Enabled: true, AwaitingUser: true}, now.Add(-24*time.Hour))
	last := now.Add(-4 * time.Minute)
	gaps := []time.Duration{10 * time.Minute, 10 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	if err := state.Save(f.state, state.File{CheckedAt: last, ScheduleGaps: gaps}); err != nil {
		t.Fatal(err)
	}
	out, text := statusOf(t, f, now)
	if out.Schedule.CheckedBy != "ticks" || out.Schedule.Cadence != "10m" || out.Schedule.Overdue {
		t.Fatalf("%+v", out.Schedule)
	}
	if !strings.Contains(text, "next check: not scheduled by a daemon: checks are run by an external scheduler or ticks run by hand; recent checks ran about every 10m, the last 4m ago") {
		t.Fatalf("%s", text)
	}
	if _, r := doctorOf(t, f); doctorFinding(r, "supervisor.ticks") == nil {
		t.Fatal("doctor says nothing about the ticks")
	}
	// Checks that stopped long ago are not "being checked".
	if err := state.Save(f.state, state.File{CheckedAt: now.Add(-6 * time.Hour), ScheduleGaps: gaps}); err != nil {
		t.Fatal(err)
	}
	if out, text := statusOf(t, f, now); out.Schedule.CheckedBy != "" || strings.Contains(text, "not scheduled by a daemon") {
		t.Fatalf("%+v", out.Schedule)
	}
}

// F2: a supervisor start refused for ownership is recorded where a person
// looks, without changing control, and the record goes once a supervisor
// starts.
func TestRefusedSupervisorStartIsRecorded(t *testing.T) {
	f := newDoctorFixture(t)
	if err := os.MkdirAll(f.state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := control.Save(f.state, control.File{Enabled: true, Ownership: &control.Ownership{ThreadID: "thr_owner00001", Generation: 1}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(f.state, control.FileName))
	for _, cmd := range []string{"daemon", "start"} {
		os.Remove(filepath.Join(f.state, startRefusalName))
		t.Setenv("BB_THREAD_ID", "thr_other00001")
		var out bytes.Buffer
		args := []string{cmd, "-c", f.config}
		if cmd == "start" {
			args = append(args, "--foreground")
		}
		if code := run(context.Background(), args, &out, &out); code != exitFailed {
			t.Fatalf("%s: %d %s", cmd, code, out.String())
		}
		r := loadStartRefusal(f.state)
		if r == nil || r.Reason != refusedOwner || r.CallerThread != "set" || r.Mode == "" || r.Count != 1 {
			t.Fatalf("%s: %+v", cmd, r)
		}
		raw, _ := os.ReadFile(filepath.Join(f.state, startRefusalName))
		if strings.Contains(string(raw), "thr_") {
			t.Fatalf("%s: a thread ID in the record: %s", cmd, raw)
		}
		if after, _ := os.ReadFile(filepath.Join(f.state, control.FileName)); !bytes.Equal(before, after) {
			t.Fatalf("%s: a refused start changed control", cmd)
		}
	}
	runs, _ := os.ReadFile(filepath.Join(f.state, state.LogName))
	if strings.Count(string(runs), `"start_refused"`) != 2 || strings.Contains(string(runs), "thr_") {
		t.Fatalf("runs.jsonl: %s", runs)
	}
	// A service restarting into the same refusal: counted, logged once an hour.
	for i := 0; i < 5; i++ {
		var o bytes.Buffer
		run(context.Background(), []string{"daemon", "-c", f.config}, &o, &o)
	}
	if r := loadStartRefusal(f.state); r == nil || r.Count != 6 {
		t.Fatalf("%+v", r)
	}
	if runs, _ := os.ReadFile(filepath.Join(f.state, state.LogName)); strings.Count(string(runs), `"start_refused"`) != 2 {
		t.Fatalf("repeated refusals each logged: %s", runs)
	}
	out, text := statusOf(t, f, time.Now())
	if out.SupervisorStartRefused == nil || !strings.Contains(text, "supervisor start: the last supervisor start (daemon) was refused at") ||
		!strings.Contains(text, "another BB thread owns the loop") || strings.Contains(text, "supervisor start: the last supervisor start (daemon) was refused at "+"x") {
		t.Fatalf("%s", text)
	}
	// Hidden while a daemon answers, and once a check is recorded after it.
	if currentStartRefusal(f.state, time.Time{}, daemonStatus{State: "responsive"}) != nil {
		t.Fatal("shown beside a running daemon")
	}
	if currentStartRefusal(f.state, time.Now().Add(time.Minute), daemonStatus{}) != nil {
		t.Fatal("shown after a later check")
	}
	if _, r := doctorOf(t, f); doctorFinding(r, "supervisor.start_refused") == nil {
		t.Fatal("doctor does not show the refusal")
	}
	// The owner's supervisor starts: the record goes.
	t.Setenv("BB_THREAD_ID", "thr_owner00001")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var o bytes.Buffer
	run(ctx, []string{"daemon", "-c", f.config}, &o, &o)
	if loadStartRefusal(f.state) != nil {
		t.Fatalf("record kept after a supervisor started: %s", o.String())
	}
	// A held lock is not a refusal.
	t.Setenv("BB_THREAD_ID", "thr_other00001")
	if code := refusedStart(env{context.Background(), &o, &o}, f.state, "daemon", context.Canceled); code == exitOK || loadStartRefusal(f.state) != nil {
		t.Fatal("a cancelled start was recorded as refused")
	}
}

// F8: status and doctor name the builds, and say whether the daemon will
// follow new installs, from what is recorded; unknown rather than a guess.
func TestStatusNamesBuildsAndWhetherTheDaemonFollowsInstalls(t *testing.T) {
	saved := version
	version = "v9.0.0-dev.fictional2"
	t.Cleanup(func() { version = saved })
	st := state.File{LastCheck: &state.Check{Version: "v9.0.0-dev.fictional1"}}
	if got := buildNames(st, daemonStatus{State: "not_recorded"}); got != "last check ran v9.0.0-dev.fictional1; this command is v9.0.0-dev.fictional2" {
		t.Fatal(got)
	}
	if got := buildNames(state.File{}, daemonStatus{State: "responsive", version: "v9.0.0-dev.fictional1"}); got != "last check ran unknown; this command is v9.0.0-dev.fictional2; the daemon runs v9.0.0-dev.fictional1" {
		t.Fatal(got)
	}
	for mode, want := range map[string]string{
		"":                                  "unknown: the daemon does not report it",
		"moved_link":                        "yes, after a check, when the link it was started through moves",
		"any_install":                       "yes, after a check, for any new build",
		"off":                               "no: loop.daemon.adopt_installed_build is false",
		"unavailable: not on this platform": "no: not on this platform; restart supervision",
		"something new":                     "unknown: the daemon's answer is not understood",
	} {
		if _, got := followsInstalls(daemonStatus{State: "responsive", Adoption: mode}); !strings.Contains(got, want) {
			t.Errorf("%q: %s", mode, got)
		}
	}
	if answer, got := followsInstalls(daemonStatus{State: "not_recorded", Adoption: "moved_link"}); answer != "unknown" || got != "unknown: no daemon answers" {
		t.Fatal(got)
	}
	// What a daemon publishes.
	on, off := true, false
	cfg := &config.Config{}
	var none *buildAdopter
	if got := none.followMode(cfg); !strings.HasPrefix(got, "unavailable: ") {
		t.Fatal(got)
	}
	a := &buildAdopter{}
	for setting, want := range map[*bool]string{nil: "moved_link", &on: "any_install", &off: "off"} {
		cfg.Loop.Daemon.AdoptInstalledBuild = setting
		if got := a.followMode(cfg); got != want {
			t.Errorf("%v: %s", setting, got)
		}
	}
	a.off = "not available on this platform"
	if got := a.followMode(cfg); got != "unavailable: not available on this platform" {
		t.Fatal(got)
	}
	// In status text, with no daemon.
	f := newDoctorFixture(t)
	if err := os.MkdirAll(f.state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(f.state, st); err != nil {
		t.Fatal(err)
	}
	out, text := statusOf(t, f, time.Now())
	if out.FollowsInstalls != "unknown" || !strings.Contains(text, "build:     last check ran v9.0.0-dev.fictional1; this command is v9.0.0-dev.fictional2 (last check's build versus this command's: different)") ||
		!strings.Contains(text, "daemon follows new installs: unknown: no daemon answers") {
		t.Fatalf("%s", text)
	}
}

// A recorded check removes a refusal record: something supervises the loop.
func TestRecordedCheckClearsARefusal(t *testing.T) {
	f := newDoctorFixture(t)
	if err := os.MkdirAll(f.state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := control.Save(f.state, control.File{}); err != nil { // disabled: the check records without the agent
		t.Fatal(err)
	}
	noteStartRefused(f.state, "service", control.ErrTransferPending, time.Now().Add(-time.Minute))
	if r := loadStartRefusal(f.state); r == nil || r.Reason != refusedTransfer {
		t.Fatalf("%+v", r)
	}
	var out bytes.Buffer
	run(context.Background(), []string{"tick", "-c", f.config}, &out, &out)
	if st, err := state.Load(f.state); err != nil || st.CheckedAt.IsZero() {
		t.Fatalf("no check recorded: %v %s", err, out.String())
	}
	if loadStartRefusal(f.state) != nil {
		t.Fatal("a recorded check kept the refusal")
	}
}

// The same refusal again is logged after an hour; a new reason at once.
func TestRepeatedRefusalsAreLoggedSparingly(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2030, 1, 1, 9, 0, 0, 0, time.UTC)
	lines := func() int {
		b, _ := os.ReadFile(filepath.Join(dir, state.LogName))
		return strings.Count(string(b), `"start_refused"`)
	}
	noteStartRefused(dir, "service", control.ErrSuperseded, at)
	noteStartRefused(dir, "service", control.ErrSuperseded, at.Add(time.Minute))
	if lines() != 1 {
		t.Fatalf("%d", lines())
	}
	noteStartRefused(dir, "service", control.ErrSuperseded, at.Add(61*time.Minute))
	if lines() != 2 {
		t.Fatalf("%d", lines())
	}
	noteStartRefused(dir, "service", control.ErrOwnerConflict, at.Add(62*time.Minute))
	if r := loadStartRefusal(dir); lines() != 3 || r.Reason != refusedOwner || r.Count != 1 {
		t.Fatalf("%d %+v", lines(), r)
	}
}
