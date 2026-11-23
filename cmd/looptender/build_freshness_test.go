package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
)

func freshAt(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
}

func freshDaemon(label, mode string) daemonStatus {
	return daemonStatus{State: "responsive", version: label, Adoption: mode}
}

func derive(d daemonStatus, rec *adoptRecord, installed string, installedAt, now time.Time, c freshContext) *buildFreshness {
	return deriveBuildFreshness(d, rec, installed, installedAt, now, c)
}

func TestBuildFreshnessStates(t *testing.T) {
	now := freshAt(t)
	adopted := func(ago time.Duration) *adoptRecord {
		return &adoptRecord{Outcome: adoptAdopted, At: now.Add(-ago), From: adoptBuildID{Label: "v0.1.0"}, To: adoptBuildID{Label: "v0.2.0"}}
	}
	cases := []struct {
		name   string
		d      daemonStatus
		rec    *adoptRecord
		state  string
		text   string
		detail string
	}{
		{"up to date is just the version", freshDaemon("v0.2.0", "moved_link"), nil, freshCurrent, "v0.2.0", ""},
		{"just switched reads just now", freshDaemon("v0.2.0", "moved_link"), adopted(10 * time.Second), freshUpdated, "Updated just now", "was v0.1.0 · now v0.2.0"},
		{"44 seconds is still just now", freshDaemon("v0.2.0", "moved_link"), adopted(44 * time.Second), freshUpdated, "Updated just now", ""},
		{"a minute after", freshDaemon("v0.2.0", "moved_link"), adopted(time.Minute), freshUpdated, "Updated 1 min ago", "was v0.1.0 · now v0.2.0"},
		{"23h59 after the switch", freshDaemon("v0.2.0", "moved_link"), adopted(23*time.Hour + 59*time.Minute), freshUpdated, "Updated 23 h ago", "was v0.1.0"},
		{"24h after the switch is gone", freshDaemon("v0.2.0", "moved_link"), adopted(24 * time.Hour), freshCurrent, "v0.2.0", ""},
		{"a switch in the future is not shown", freshDaemon("v0.2.0", "moved_link"), adopted(-time.Hour), freshCurrent, "v0.2.0", ""},
		{"a failed switch is not an update", freshDaemon("v0.2.0", "moved_link"), &adoptRecord{Outcome: adoptRefused, At: now}, freshCurrent, "v0.2.0", ""},
		{"follows installs, not switched", freshDaemon("v0.1.0", "moved_link"), nil, freshPending, "Update pending · switches at next check", "Runs v0.1.0; installed v0.2.0"},
		{"follows any install", freshDaemon("v0.1.0", "any_install"), nil, freshPending, "Update pending · switches at next check", ""},
		{"never follows, plain releases: the order is established", freshDaemon("v0.1.0", "off"), nil, freshOlder, "Older build · restart the supervisor to update", "does not follow installs"},
		{"never follows, unavailable", freshDaemon("v0.1.0", "unavailable: x"), nil, freshOlder, "Older build · restart the supervisor to update", ""},
		{"silent daemon", freshDaemon("v0.1.0", ""), nil, freshOlder, "Older build · restart the supervisor to update", "does not report following installs"},
		{"never follows, but the daemon's is the newer release", freshDaemon("v0.3.0", "off"), nil, freshDifferent, "Different build from the installed one", ""},
		{"dev builds: the order is not known", freshDaemon("v0.2.0-dev.aaaaaaaaaaaa", "off"), nil, freshDifferent, "Different build from the installed one", ""},
	}
	for _, c := range cases {
		installed := "v0.2.0"
		if strings.Contains(c.d.version, "dev") {
			installed = "v0.2.0-dev.bbbbbbbbbbbb"
		}
		f := derive(c.d, c.rec, installed, time.Time{}, now, freshContext{})
		if f == nil {
			t.Errorf("%s: nothing said", c.name)
			continue
		}
		if f.State != c.state || f.Text != c.text || !strings.Contains(f.Detail, c.detail) {
			t.Errorf("%s: got %q %q %q", c.name, f.State, f.Text, f.Detail)
		}
		if f.State == freshUpdated && (f.Was != "v0.1.0" || f.Now != "v0.2.0" || f.SwitchedAt == nil || !f.SwitchedAt.Equal(c.rec.At)) {
			t.Errorf("%s: was/now/switchedAt: %+v", c.name, f)
		}
		if f.State != freshUpdated && (f.Was != "" || f.Now != "" || f.SwitchedAt != nil) {
			t.Errorf("%s: switch fields outside updated: %+v", c.name, f)
		}
		for _, w := range []string{f.Command, f.Detail, f.Action} {
			if strings.Contains(w, "keepgoing") || strings.Contains(f.Command, "start") || strings.Contains(strings.ToLower(f.Command), "resume") {
				t.Errorf("%s: wrong program or a start/resume command: %q", c.name, w)
			}
		}
	}
}

func TestBuildFreshnessOrderFromFileTimes(t *testing.T) {
	now := freshAt(t)
	started := now.Add(-48 * time.Hour)
	d := freshDaemon("v0.2.0-dev.aaaaaaaaaaaa", "off")
	d.StartedAt = &started
	const inst = "v0.2.0-dev.bbbbbbbbbbbb"
	if f := derive(d, nil, inst, now.Add(-time.Hour), now, freshContext{}); f.State != freshOlder {
		t.Errorf("installed after the daemon's build started: %q", f.State)
	}
	if f := derive(d, nil, inst, started.Add(-time.Hour), now, freshContext{}); f.State != freshDifferent {
		t.Errorf("installed before the daemon started: %q", f.State)
	}
	if f := derive(d, nil, inst, time.Time{}, now, freshContext{}); f.State != freshDifferent {
		t.Errorf("no file time: %q", f.State)
	}
	// After a switch the daemon's build started at BuildStartedAt, not StartedAt.
	later := now.Add(-30 * time.Minute)
	d.BuildStartedAt = &later
	if f := derive(d, nil, inst, now.Add(-time.Hour), now, freshContext{}); f.State != freshDifferent {
		t.Errorf("installed before the running build started: %q", f.State)
	}
}

func TestBuildFreshnessCommandOnlyWhereItIsTrue(t *testing.T) {
	now := freshAt(t)
	reports := freshDaemon("v0.1.0", "off")
	silent := freshDaemon("v0.1.0", "")
	f := derive(reports, nil, "v0.2.0", time.Time{}, now, freshContext{})
	if f.Command != "looptender daemon-shutdown -c CONFIG" || !strings.Contains(f.Action, "start supervision again") || strings.Contains(f.Action, "stays held") {
		t.Errorf("daemon that reports adoption: %+v", f)
	}
	// A daemon that reports nothing may predate the command: words, no command.
	f = derive(silent, nil, "v0.2.0", time.Time{}, now, freshContext{})
	if f.Command != "" || !strings.Contains(f.Action, "stop its process") {
		t.Errorf("silent daemon: %+v", f)
	}
	// A held loop is told it stays held; neither reads as resume.
	for _, d := range []daemonStatus{reports, silent} {
		f = derive(d, nil, "v0.2.0", time.Time{}, now, freshContext{Held: true})
		if !strings.HasSuffix(f.Action, "The loop stays held; this does not resume it.") || strings.Contains(strings.ToLower(f.Command), "resume") {
			t.Errorf("held: %+v", f)
		}
	}
	// A replaced loop has nothing to restart.
	f = derive(reports, nil, "v0.2.0", time.Time{}, now, freshContext{Retired: true})
	if f.Command != "" || !strings.Contains(f.Action, "replaced") {
		t.Errorf("retired: %+v", f)
	}
	// Pending and current offer neither.
	if f := derive(freshDaemon("v0.1.0", "moved_link"), nil, "v0.2.0", time.Time{}, now, freshContext{}); f.Command != "" || f.Action != "" {
		t.Errorf("pending: %+v", f)
	}
}

func TestFreshContextOf(t *testing.T) {
	if freshContextOf(true, false, false, false, false, false).Held {
		t.Error("a running loop is not held")
	}
	for name, c := range map[string]freshContext{
		"disabled":  freshContextOf(false, false, false, false, false, false),
		"paused":    freshContextOf(true, true, false, false, false, false),
		"stopped":   freshContextOf(true, false, true, false, false, false),
		"awaiting":  freshContextOf(true, false, false, true, false, false),
		"completed": freshContextOf(true, false, false, false, true, false),
	} {
		if !c.Held {
			t.Errorf("%s should be held", name)
		}
	}
	if !freshContextOfSnapshot(nil).Held || !freshContextOfSnapshot(&clientSnapshot{ControlState: "superseded"}).Retired {
		t.Error("unread controls are taken as held; superseded is retired")
	}
}

func TestBuildFreshnessSaysNothingWithMissingFields(t *testing.T) {
	now := freshAt(t)
	for name, d := range map[string]daemonStatus{
		"no daemon":          {State: "unavailable"},
		"no version":         {State: "responsive"},
		"dev build":          {State: "responsive", version: "dev"},
		"not responsive":     {State: "not_recorded", version: "v0.1.0"},
		"unreadable version": {State: "responsive", version: "a\x01b"},
	} {
		if f := derive(d, nil, "v0.2.0", time.Time{}, now, freshContext{}); f != nil {
			t.Errorf("%s: said %+v", name, f)
		}
	}
	if f := derive(freshDaemon("v0.1.0", "moved_link"), nil, "dev", time.Time{}, now, freshContext{}); f != nil {
		t.Errorf("unknown installed build: said %+v", f)
	}
}

func TestBuildFreshnessJSONShape(t *testing.T) {
	now := freshAt(t)
	rec := &adoptRecord{Outcome: adoptAdopted, At: now.Add(-time.Hour), From: adoptBuildID{Label: "v0.1.0"}, To: adoptBuildID{Label: "v0.2.0"}}
	b, _ := json.Marshal(derive(freshDaemon("v0.2.0", "moved_link"), rec, "v0.2.0", time.Time{}, now, freshContext{}))
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	for _, k := range []string{"state", "text", "detail", "running", "installed", "was", "now", "switchedAt"} {
		if _, ok := got[k]; !ok {
			t.Errorf("updated lacks %q: %s", k, b)
		}
	}
	for _, k := range []string{"command", "action"} {
		if _, ok := got[k]; ok {
			t.Errorf("updated has %q: %s", k, b)
		}
	}
}

func TestBuildFreshnessSummary(t *testing.T) {
	now := freshAt(t)
	old := installedBuildTime
	defer func() { installedBuildTime = old }()
	installedBuildTime = func() time.Time { return now.Add(-3 * time.Hour) }
	cur, pend := &buildFreshness{State: freshCurrent}, &buildFreshness{State: freshPending}
	got := summarizeBuildFreshness([]*buildFreshness{cur, {State: freshUpdated}, pend, {State: freshOlder}, {State: freshDifferent}, nil}, "v0.2.0", now)
	if got == nil || got.OnIt != 2 || got.Older != 3 || got.Text != "Build v0.2.0 installed 3 h ago · 2 supervisors on it · 3 older" {
		t.Fatalf("got %+v", got)
	}
	one := summarizeBuildFreshness([]*buildFreshness{cur}, "v0.2.0", now)
	if one.Text != "Build v0.2.0 installed 3 h ago · 1 supervisor on it" {
		t.Errorf("got %q", one.Text)
	}
	installedBuildTime = func() time.Time { return time.Time{} }
	if s := summarizeBuildFreshness([]*buildFreshness{cur}, "v0.2.0", now); s.Text != "Build v0.2.0 · 1 supervisor on it" {
		t.Errorf("unknown install time: %q", s.Text)
	}
	if s := summarizeBuildFreshness([]*buildFreshness{nil}, "v0.2.0", now); s != nil {
		t.Errorf("nothing known should say nothing: %+v", s)
	}
}

func TestPanelListCarriesFreshnessAndOffersNoRestart(t *testing.T) {
	f := &buildFreshness{State: freshOlder, Text: "Older build · restart the supervisor to update", Command: freshShutdownCommand}
	r := dockReport{ReadAt: freshAt(t), Freshness: &buildFreshnessSummary{Text: "Build v0.2.0 · 0 supervisors on it · 1 older", Older: 1},
		Loops: []dockLoop{{Key: "k", Name: "held", State: "paused", Phrase: "Paused · also waiting for you", Can: []string{"resume"}, Freshness: f}}}
	l := buildPanelList(r)
	if l.Freshness == nil || l.Freshness.Older != 1 || l.Loops[0].Freshness != f {
		t.Fatalf("not carried: %+v", l)
	}
	if got := strings.Join(l.Loops[0].Can, ","); got != "resume" {
		t.Errorf("freshness changed what a person can do: %q", got)
	}
}

func TestStatusAndClientStatusCarryTheSameFreshness(t *testing.T) {
	old := version
	version = "v0.2.0"
	defer func() { version = old }()
	query := func(context.Context, string, string, bool) daemonStatus { return freshDaemon("v0.1.0", "off") }
	dir, entry := clientFixture(t)
	var want *buildFreshness
	for _, private := range []bool{false, true} {
		r := readClientStatus(context.Background(), dir, entry.ID, private, false, time.Minute, time.Now().UTC(), query)
		if r.Snapshot == nil || r.Snapshot.Freshness == nil {
			t.Fatalf("private=%v: no freshness: %+v", private, r.Snapshot)
		}
		f := r.Snapshot.Freshness
		// A freshly registered loop is disabled, so its words say it stays held.
		if f.State != freshOlder || f.Command != freshShutdownCommand || !strings.HasSuffix(f.Action, "does not resume it.") {
			t.Fatalf("private=%v: %+v", private, f)
		}
		want = f
	}
	cfg, err := config.Load(entry.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	out, err := readStatus(context.Background(), cfg, time.Now(), time.Minute, query)
	if err != nil || out.Freshness == nil || *out.Freshness != *want {
		t.Fatalf("status differs from client status: %v %+v vs %+v", err, out.Freshness, want)
	}
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), `"freshness":{"state":"older"`) {
		t.Errorf("status --json lacks it: %s", b)
	}
}
