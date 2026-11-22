package main

import (
	"strings"
	"testing"
	"time"
)

func freshAt(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
}

func freshDaemon(label, mode string) daemonStatus {
	return daemonStatus{State: "responsive", version: label, Adoption: mode}
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
		{"just switched", freshDaemon("v0.2.0", "moved_link"), adopted(time.Minute), freshUpdated, "Updated 1 min ago", "was v0.1.0 · now v0.2.0"},
		{"23h59 after the switch", freshDaemon("v0.2.0", "moved_link"), adopted(23*time.Hour + 59*time.Minute), freshUpdated, "Updated 23 h ago", "was v0.1.0"},
		{"24h after the switch is gone", freshDaemon("v0.2.0", "moved_link"), adopted(24 * time.Hour), freshCurrent, "v0.2.0", ""},
		{"a switch in the future is not shown", freshDaemon("v0.2.0", "moved_link"), adopted(-time.Hour), freshCurrent, "v0.2.0", ""},
		{"a failed switch is not an update", freshDaemon("v0.2.0", "moved_link"), &adoptRecord{Outcome: adoptRefused, At: now}, freshCurrent, "v0.2.0", ""},
		{"follows installs, not switched", freshDaemon("v0.1.0", "moved_link"), nil, freshPending, "Update pending · switches at next check", "Runs v0.1.0; installed v0.2.0"},
		{"follows any install", freshDaemon("v0.1.0", "any_install"), nil, freshPending, "Update pending · switches at next check", ""},
		{"never follows: off", freshDaemon("v0.1.0", "off"), nil, freshOlder, "Older build · restart the supervisor to update", "does not follow installs"},
		{"never follows: unavailable", freshDaemon("v0.1.0", "unavailable: x"), nil, freshOlder, "Older build · restart the supervisor to update", ""},
		{"older build that reports nothing", freshDaemon("v0.1.0", ""), nil, freshOlder, "Older build · restart the supervisor to update", "does not report following installs"},
	}
	for _, c := range cases {
		f := deriveBuildFreshness(c.d, c.rec, "v0.2.0", now)
		if f == nil {
			t.Errorf("%s: nothing said", c.name)
			continue
		}
		if f.State != c.state || f.Text != c.text || !strings.Contains(f.Detail, c.detail) {
			t.Errorf("%s: got %q %q %q", c.name, f.State, f.Text, f.Detail)
		}
		if (f.State == freshOlder) != (f.Command != "") {
			t.Errorf("%s: command %q for state %q", c.name, f.Command, f.State)
		}
		if strings.Contains(f.Command+f.Detail, "resume") || strings.Contains(f.Command, "start") {
			t.Errorf("%s: words a restart or resume: %q %q", c.name, f.Command, f.Detail)
		}
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
		if f := deriveBuildFreshness(d, nil, "v0.2.0", now); f != nil {
			t.Errorf("%s: said %+v", name, f)
		}
	}
	if f := deriveBuildFreshness(freshDaemon("v0.1.0", "moved_link"), nil, "dev", now); f != nil {
		t.Errorf("unknown installed build: said %+v", f)
	}
}

func TestBuildFreshnessSummary(t *testing.T) {
	now := freshAt(t)
	old := installedBuildTime
	defer func() { installedBuildTime = old }()
	installedBuildTime = func() time.Time { return now.Add(-3 * time.Hour) }
	cur, pend := &buildFreshness{State: freshCurrent}, &buildFreshness{State: freshPending}
	got := summarizeBuildFreshness([]*buildFreshness{cur, {State: freshUpdated}, pend, {State: freshOlder}, nil}, "v0.2.0", now)
	if got == nil || got.OnIt != 2 || got.Older != 2 || got.Text != "Build v0.2.0 installed 3 h ago · 2 supervisors on it · 2 older" {
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
	f := &buildFreshness{State: freshOlder, Text: "Older build · restart the supervisor to update", Command: freshOlderCommand}
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
