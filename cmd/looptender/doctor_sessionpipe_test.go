package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
)

// fakeSessionpipe stands in for the sessionpipe command: the test binary is
// the executable doctor resolves (on every platform), and these answers are
// what it "prints". It records every argument list, so the test can show
// doctor ran only its read-only commands.
type fakeSessionpipe struct {
	version string
	status  string
	pool    string
	calls   [][]string
}

func (f *fakeSessionpipe) run(_ context.Context, _ string, args []string, _ map[string]string, _ string) ([]byte, error) {
	f.calls = append(f.calls, args)
	switch strings.Join(args, " ") {
	case "--version":
		return []byte(f.version + "\n"), nil
	case "status --json":
		if f.status == "" {
			return nil, errors.New("exit status 1")
		}
		return []byte(f.status), nil
	case "pool --json":
		if f.pool == "" {
			return nil, errors.New("exit status 1")
		}
		return []byte(f.pool), nil
	}
	return nil, errors.New("unexpected command")
}

func sessionpipeStatusJSON(url string, tier int, maxTier string, paused string, hookState string) string {
	return `{"version":"0.18.2","machine":"example","state":"/example","config":"/example",` +
		`"harnesses":{"claude-code":[{"file":"~/.claude/settings.json","state":"` + hookState + `"}]},` +
		`"sinks":[{"name":"looptender","url":` + spQuote(url) + `,"tier":` + spItoa(tier) + `,"max_tier":` + maxTier + `,"pii":false,"tools":"all","control":false,"paused":` + paused + `,"pending_sessions":0}],` +
		`"timing":null,"update":null,"jobs":0}`
}

func spQuote(s string) string { b, _ := json.Marshal(s); return string(b) }
func spItoa(n int) string     { b, _ := json.Marshal(n); return string(b) }

func doctorSessionpipeChecks(t *testing.T, s *config.Sessionpipe, offline bool, f *fakeSessionpipe) map[string]doctorCheck {
	t.Helper()
	var r doctorReport
	doctorSessionpipe(context.Background(), &r, "agent.primary", s, offline, time.Second, f.run)
	out := map[string]doctorCheck{}
	for _, c := range r.Checks {
		out[c.ID] = c
		for _, private := range []string{s.EventsFile, filepath.Base(s.EventsFile), "FICTIONAL"} {
			if strings.Contains(c.Message+c.Fix, private) {
				t.Errorf("%s says %q", c.ID, private)
			}
		}
	}
	return out
}

func TestDoctorSessionpipeTarget(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "FICTIONAL-events.jsonl")
	s := &config.Sessionpipe{Session: "claude-code:8c132906-8c3f-4814-870a-afc3d05e1d2e", EventsFile: file, CLI: os.Args[0]}

	// Before the sink exists.
	f := &fakeSessionpipe{version: "0.18.2", status: sessionpipeStatusJSON("file:/elsewhere/events.jsonl", 0, "null", "null", "current")}
	c := doctorSessionpipeChecks(t, s, false, f)
	if got := c["agent.primary.observed"]; got.Status != "ok" || !strings.Contains(got.Message, "never sends to, wakes, re-prompts or stops") {
		t.Errorf("observed: %+v", got)
	}
	if got := c["agent.primary.events_file"]; got.Status != "error" || !strings.Contains(got.Fix, "sessionpipe sink add file:PATH --tier 0 --name looptender") {
		t.Errorf("a missing events file: %+v", got)
	}
	if got := c["agent.primary.sink"]; got.Status != "error" {
		t.Errorf("no sink for the file: %+v", got)
	}
	for _, call := range f.calls {
		if j := strings.Join(call, " "); j != "--version" && j != "status --json" && j != "pool --json" {
			t.Errorf("doctor ran sessionpipe %s", j)
		}
	}

	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		version, status string
		check, want     string
	}{
		"all well":       {"0.18.2", sessionpipeStatusJSON("file:"+file, 0, "null", "null", "current"), "agent.primary.sink", "ok"},
		"old version":    {"0.17.9", sessionpipeStatusJSON("file:"+file, 0, "null", "null", "current"), "agent.primary.version", "error"},
		"new version":    {"1.0.0", sessionpipeStatusJSON("file:"+file, 0, "null", "null", "current"), "agent.primary.version", "ok"},
		"tier 1":         {"0.18.2", sessionpipeStatusJSON("file:"+file, 1, "null", "null", "current"), "agent.primary.sink", "error"},
		"tier capped":    {"0.18.2", sessionpipeStatusJSON("file:"+file, 2, "0", "null", "current"), "agent.primary.sink", "ok"},
		"paused":         {"0.18.2", sessionpipeStatusJSON("file:"+file, 0, "null", `"2026-11-23T00:00:00Z"`, "current"), "agent.primary.sink", "warning"},
		"stale hooks":    {"0.18.2", sessionpipeStatusJSON("file:"+file, 0, "null", "null", "stale"), "agent.primary.hooks", "warning"},
		"no hooks":       {"0.18.2", strings.Replace(sessionpipeStatusJSON("file:"+file, 0, "null", "null", "current"), `"claude-code"`, `"codex"`, 1), "agent.primary.hooks", "error"},
		"no status":      {"0.18.2", "", "agent.primary.sink", "unavailable"},
		"odd status":     {"0.18.2", `{"sinks":"text"}`, "agent.primary.sink", "unavailable"},
		"odd version":    {"version FICTIONAL", sessionpipeStatusJSON("file:"+file, 0, "null", "null", "current"), "agent.primary.version", "unavailable"},
		"current hooks":  {"0.18.2", sessionpipeStatusJSON("file:"+file, 0, "null", "null", "current"), "agent.primary.hooks", "ok"},
		"version passes": {"0.18.0", sessionpipeStatusJSON("file:"+file, 0, "null", "null", "current"), "agent.primary.version", "ok"},
	} {
		t.Run(name, func(t *testing.T) {
			c := doctorSessionpipeChecks(t, s, false, &fakeSessionpipe{version: tc.version, status: tc.status})
			if got := c[tc.check]; got.Status != tc.want {
				t.Fatalf("%s: %+v, want %s", tc.check, got, tc.want)
			}
		})
	}

	// Offline, nothing runs.
	f = &fakeSessionpipe{}
	c = doctorSessionpipeChecks(t, s, true, f)
	if len(f.calls) != 0 || c["agent.primary.version"].Status != "unavailable" || c["agent.primary.sink"].Status != "unavailable" {
		t.Errorf("offline ran %v: %+v", f.calls, c)
	}
	// Without the command, the file is still checked.
	missing := *s
	missing.CLI = filepath.Join(dir, "absent-sessionpipe")
	c = doctorSessionpipeChecks(t, &missing, false, &fakeSessionpipe{})
	if c["agent.primary.cli"].Status != "warning" || c["agent.primary.events_file"].Status == "" {
		t.Errorf("no command: %+v", c)
	}
}

func TestDoctorSessionpipeFilePrivacy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode bits; Windows reports the DACL check instead")
	}
	file := filepath.Join(t.TempDir(), "events.jsonl")
	for mode, want := range map[os.FileMode]string{0o600: "ok", 0o644: "warning", 0o664: "error", 0o602: "error"} {
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(file, mode); err != nil {
			t.Fatal(err)
		}
		var r doctorReport
		doctorSessionpipeFile(&r, "f", file)
		if len(r.Checks) != 1 || r.Checks[0].Status != want {
			t.Errorf("mode %o: %+v, want %s", mode, r.Checks, want)
		}
	}
}

func TestSessionpipePathsAndVersions(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	if !sameSessionpipePath("~/example/events.jsonl", filepath.Join(home, "example", "events.jsonl")) {
		t.Error("a sink path under ~ does not match")
	}
	if sameSessionpipePath("events.jsonl", filepath.Join(home, "events.jsonl")) {
		t.Error("a relative sink path matched")
	}
	for v, before := range map[string]bool{"0.17.12": true, "0.18.0": false, "0.18.0-beta.1": false, "0.19.0": false, "1.0.0": false, "0.9.9": true} {
		if versionBefore(v, sessionpipeMinVersion) != before {
			t.Errorf("%s before %s: want %v", v, sessionpipeMinVersion, before)
		}
	}
}

// The whole doctor command runs on an observed target's configuration,
// which has no prompt, and says it is observed.
func TestDoctorCommandOnAnObservedTarget(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "looptender.yaml")
	body := "version: 1\nloop:\n  name: example-observed\n  state_dir: state\n  adapter: sessionpipe\n  sessionpipe:\n    session: codex:01a10860-a43f-7ce2-b12f-a7911ae8a880\n    events_file: events.jsonl\n    cli: " + spQuote(filepath.Join(dir, "absent")) + "\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	run(context.Background(), []string{"doctor", "-c", cfg, "--offline", "--json"}, &out, &errOut)
	var r struct {
		Checks []doctorCheck `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("%v\n%s\n%s", err, out.String(), errOut.String())
	}
	seen := map[string]doctorCheck{}
	for _, c := range r.Checks {
		seen[c.ID] = c
	}
	if seen["agent.primary.observed"].Status != "ok" || seen["agent.primary.events_file"].Status != "error" {
		t.Fatalf("checks: %+v", seen)
	}
}

func TestDoctorSessionpipeAskStatus(t *testing.T) {
	for name, tc := range map[string]struct {
		out    string
		err    error
		status string
	}{
		"on and running": {"  ask: on\n  daemon: running (pid 1, 0.18.2)\n  agents: claude-code\n  folders: /x\n", nil, "ok"},
		"off":            {"  ask: off (`sessionpipe ask on` turns it on)\n  daemon: running (pid 1, 0.18.2)\n", nil, "error"},
		"no daemon":      {"  ask: on\n  daemon: not running (`sessionpipe ask on` installs it as a service)\n", nil, "error"},
		"odd page":       {"something else\n", nil, "unavailable"},
		"failed":         {"", errors.New("exit status 1"), "unavailable"},
	} {
		var r doctorReport
		doctorSessionpipeAsk(&r, "agent.primary.send", []byte(tc.out), tc.err)
		if len(r.Checks) != 1 || r.Checks[0].Status != tc.status || strings.Contains(r.Checks[0].Message+r.Checks[0].Fix, "/x") {
			t.Errorf("%s: %+v, want %s", name, r.Checks, tc.status)
		}
	}
}

// A wake still running shows in status and doctor, with how long it has been
// outstanding; a long one is a warning.
func TestSessionpipeOutstandingWakeShows(t *testing.T) {
	stateDir := t.TempDir()
	target := filepath.Join(stateDir, "targets", "primary")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	// This test process stands in for the running ask: alive, and its start
	// time unrecorded (so only the process ID is compared).
	write := func(started time.Time, outcome string) {
		b, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "startedAt": started, "outcome": outcome})
		if err := os.WriteFile(filepath.Join(target, "sessionpipe-ask.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	write(now.Add(-2*time.Hour), "delivering")
	pw := pendingSessionpipeWake(stateDir, "primary", now, time.Hour)
	if pw == nil || pw.ForSeconds < 7000 || !strings.Contains(pw.Text, "has been running since") {
		t.Fatalf("pending wake: %+v", pw)
	}
	var r doctorReport
	doctorSessionpipeWake(&r, "agent.primary.wake", stateDir, now, time.Hour)
	if r.Checks[0].Status != "warning" || !strings.Contains(r.Checks[0].Message, "2h0m0s") {
		t.Fatalf("doctor: %+v", r.Checks)
	}
	write(now.Add(-time.Minute), "delivering")
	r = doctorReport{}
	doctorSessionpipeWake(&r, "agent.primary.wake", stateDir, now, time.Hour)
	if r.Checks[0].Status != "ok" || !strings.Contains(r.Checks[0].Message, "being answered") {
		t.Fatalf("a short wake: %+v", r.Checks)
	}
	write(now.Add(-time.Minute), "answered")
	if pendingSessionpipeWake(stateDir, "primary", now, time.Hour) != nil {
		t.Fatal("an answered wake shows as pending")
	}
	write(now.Add(-25*time.Hour), "delivering")
	if pendingSessionpipeWake(stateDir, "primary", now, time.Hour) != nil {
		t.Fatal("a wake over a day old holds the next one back")
	}
}

// After a wake that did not get through, status and doctor say the next is
// held, why and until when.
func TestSessionpipeHeldWakeShows(t *testing.T) {
	stateDir := t.TempDir()
	target := filepath.Join(stateDir, "targets", "primary")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	b, _ := json.Marshal(map[string]any{"startedAt": now.Add(-10 * time.Minute), "outcome": "refused", "code": "ask_off"})
	if err := os.WriteFile(filepath.Join(target, "sessionpipe-ask.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	pw := pendingSessionpipeWake(stateDir, "primary", now, time.Hour)
	if pw == nil || !pw.Held || pw.Code != "ask_off" || !strings.Contains(pw.Text, "last wake refused: ask_off, next attempt after ") {
		t.Fatalf("held wake: %+v", pw)
	}
	var r doctorReport
	doctorSessionpipeWake(&r, "agent.primary.wake", stateDir, now, time.Hour)
	if r.Checks[0].Status != "warning" || !strings.Contains(r.Checks[0].Message, "The next wake is held: last wake refused: ask_off") || !strings.Contains(r.Checks[0].Fix, "sessionpipe ask on") {
		t.Fatalf("doctor: %+v", r.Checks)
	}
	if pendingSessionpipeWake(stateDir, "primary", now.Add(time.Hour), time.Hour) != nil {
		t.Fatal("still held after the interval")
	}
}

func TestDoctorSessionpipePoolStatus(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		out     string
		err     error
		status  string
		wantSub string
	}{
		"off":        {`{"on":false,"listening":false,"port":0,"accounts":[]}`, nil, "ok", "pool is off"},
		"down":       {`{"on":true,"listening":false,"port":47812,"accounts":[{"label":"FICTIONAL-LABEL","email":"a@example.com","enabled":true,"status":"ready"}]}`, nil, "error", "connection refused"},
		"ready":      {`{"on":true,"listening":true,"port":47812,"accounts":[{"label":"FICTIONAL-LABEL","email":"a@example.com","enabled":true,"status":"ready","current":true},{"enabled":true,"status":"limited"}]}`, nil, "ok", "1 of 2 enabled accounts ready"},
		"exhausted":  {`{"on":true,"listening":true,"port":47812,"accounts":[{"enabled":true,"status":"limited","until":"2026-11-25T13:00:00Z"}]}`, nil, "warning", "none ready"},
		"degraded":   {`{"on":true,"listening":true,"port":47812,"accounts":[{"enabled":true,"status":"error"}]}`, nil, "warning", "1 enabled accounts in error"},
		"empty":      {`{"on":true,"listening":true,"port":47812,"accounts":[]}`, nil, "warning", "no enabled accounts"},
		"bad shape":  {`{"other":true}`, nil, "unavailable", "raw output is withheld"},
		"cli failed": {"", errors.New("exit status 1"), "unavailable", "raw output is withheld"},
	} {
		t.Run(name, func(t *testing.T) {
			var r doctorReport
			doctorSessionpipePool(&r, "agent.primary.pool", []byte(tc.out), tc.err, now)
			if len(r.Checks) != 1 || r.Checks[0].Status != tc.status || !strings.Contains(r.Checks[0].Message, tc.wantSub) {
				t.Fatalf("got %+v, want status %s containing %q", r.Checks, tc.status, tc.wantSub)
			}
			for _, secret := range []string{"FICTIONAL-LABEL", "example.com"} {
				if strings.Contains(r.Checks[0].Message+r.Checks[0].Fix, secret) {
					t.Fatalf("pool check leaked %q: %+v", secret, r.Checks[0])
				}
			}
		})
	}
}
