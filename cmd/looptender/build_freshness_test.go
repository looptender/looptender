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

func TestBuildFreshnessNeverSaysOlderFromAFileTime(t *testing.T) {
	now := freshAt(t)
	started := now.Add(-48 * time.Hour)
	oldStart := freshProcessStart
	defer func() { freshProcessStart = oldStart }()
	freshProcessStart = now // this reader started after every file time below: it is not the one that is behind
	d := freshDaemon("v0.2.0-dev.aaaaaaaaaaaa", "off")
	d.StartedAt = &started
	const inst = "v0.2.0-dev.bbbbbbbbbbbb"
	// A downgrade, a restore or a copy writes a new file time: it proves nothing.
	for _, at := range []time.Time{now.Add(-time.Hour), started.Add(-time.Hour), {}} {
		if f := derive(d, nil, inst, at, now, freshContext{}); f.State != freshDifferent || f.Text != "Different build from the installed one" {
			t.Errorf("installed file time %v: %q %q", at, f.State, f.Text)
		}
	}
}

func TestBuildFreshnessSaysNothingPerLoopFromAPanelOlderThanTheInstall(t *testing.T) {
	now := freshAt(t)
	old := freshProcessStart
	defer func() { freshProcessStart = old }()
	freshProcessStart = now.Add(-2 * time.Hour)
	// The installed file was written after this panel started: its own build is the earlier one.
	if f := derive(freshDaemon("v0.3.0", "moved_link"), nil, "v0.2.0", now.Add(-time.Hour), now, freshContext{}); f != nil {
		t.Errorf("a switched daemon read as %+v", f)
	}
	if f := derive(freshDaemon("v0.2.0", "off"), nil, "v0.2.0", now.Add(-time.Hour), now, freshContext{}); f != nil {
		t.Errorf("a daemon on the panel's own build read as current: %+v", f)
	}
	oldTime := installedBuildTime
	defer func() { installedBuildTime = oldTime }()
	installedBuildTime = func() time.Time { return now.Add(-time.Hour) }
	sum := summarizeBuildFreshness([]*buildFreshness{{State: freshCurrent}}, "v0.2.0", now)
	if sum == nil || sum.Text != freshPanelBehindText || sum.OnIt != 0 {
		t.Errorf("summary: %+v", sum)
	}
	// Written before this panel started: nothing is behind.
	installedBuildTime = func() time.Time { return now.Add(-3 * time.Hour) }
	if f := derive(freshDaemon("v0.2.0", "off"), nil, "v0.2.0", now.Add(-3*time.Hour), now, freshContext{}); f == nil || f.State != freshCurrent {
		t.Errorf("not behind: %+v", f)
	}
}

func TestBuildFreshnessARefusedSwitchIsNotPending(t *testing.T) {
	now := freshAt(t)
	started := now.Add(-48 * time.Hour)
	d := freshDaemon("v0.1.0", "moved_link")
	d.StartedAt = &started
	refused := &adoptRecord{Outcome: adoptRefused, At: now.Add(-time.Minute), From: adoptBuildID{Label: "v0.1.0"}, To: adoptBuildID{Label: "v0.2.0"},
		Reason: "the installed build is older than this loop's state format; going back needs the documented stop, restore and start"}
	f := derive(d, refused, "v0.2.0", time.Time{}, now, freshContext{})
	if f.State != freshRefused || f.Text != "Not switching to the installed build" || !strings.Contains(f.Detail, "state format") || strings.Contains(f.Text, "next check") {
		t.Fatalf("%+v", f)
	}
	if f.Action == "" {
		t.Errorf("a refused switch is told what to do: %+v", f)
	}
	// A refusal from before this build started, or about another build, is history.
	older := *refused
	older.At = started.Add(-time.Hour)
	if f := derive(d, &older, "v0.2.0", time.Time{}, now, freshContext{}); f.State != freshPending {
		t.Errorf("old refusal: %q", f.State)
	}
	other := *refused
	other.To = adoptBuildID{Label: "v0.9.0"}
	if f := derive(d, &other, "v0.2.0", time.Time{}, now, freshContext{}); f.State != freshPending {
		t.Errorf("refusal of another build: %q", f.State)
	}
}

func TestBuildFreshnessAdviceIsTrueForHowTheSupervisorRuns(t *testing.T) {
	now := freshAt(t)
	svcLaunchd := &freshService{Backend: "launchd", UID: 501, Label: "org.keepgoing.loop.0123"}
	svcSystemd := &freshService{Backend: "systemd", UID: 1000, Label: "org.keepgoing.loop.0123.service"}
	reports, silent := freshDaemon("v0.1.0", "off"), freshDaemon("v0.1.0", "")
	get := func(d daemonStatus, c freshContext) *buildFreshness {
		f := derive(d, nil, "v0.2.0", time.Time{}, now, c)
		if f == nil {
			t.Fatal("nothing said")
		}
		return f
	}
	// A service: its own restart, never the clean exit that its unit would not restart from.
	for name, tc := range map[string]struct {
		svc  *freshService
		want string
	}{"launchd": {svcLaunchd, "launchctl kickstart -k gui/501/org.keepgoing.loop.0123"}, "systemd": {svcSystemd, "systemctl --user restart org.keepgoing.loop.0123.service"}} {
		for _, d := range []daemonStatus{reports, silent} {
			f := get(d, freshContext{Service: tc.svc})
			if f.Command != tc.want || strings.Contains(f.Command+f.Action, "daemon-shutdown") || strings.Contains(f.Action, "starts again by itself") {
				t.Errorf("%s: %+v", name, f)
			}
		}
	}
	// A background-started supervisor that can be asked to exit: the sequence says it is left with none until started.
	f := get(reports, freshContext{})
	if f.Command != freshShutdownCommand || !strings.Contains(f.Action, "until you start it again the loop has no supervisor") || !strings.Contains(f.Action, "looptender start --background -c CONFIG") {
		t.Errorf("background: %+v", f)
	}
	// One that cannot: no command, and the same sentence about being left with none.
	f = get(silent, freshContext{})
	if f.Command != "" || !strings.Contains(f.Action, "end its process yourself") || !strings.Contains(f.Action, "the loop has no supervisor") {
		t.Errorf("silent: %+v", f)
	}
	// Held or disabled: never start (it enables the loop); daemon keeps the controls, and the loop stays held.
	for _, d := range []daemonStatus{reports, silent} {
		f = get(d, freshContext{Held: true})
		if strings.Contains(f.Action, "start --background") || !strings.Contains(f.Action, "looptender daemon -c CONFIG") || !strings.HasSuffix(f.Action, "The loop stays held; this does not resume it.") {
			t.Errorf("held: %+v", f)
		}
	}
	f = get(reports, freshContext{Held: true, Service: svcLaunchd})
	if !strings.HasSuffix(f.Action, "The loop stays held; this does not resume it.") || strings.Contains(f.Action, "daemon -c") {
		t.Errorf("held service: %+v", f)
	}
	// A replaced loop: nothing to restart, and no chip that says to restart.
	f = get(freshDaemon("v0.1.0", "off"), freshContext{Retired: true})
	if f.Command != "" || !strings.Contains(f.Action, "replaced") || strings.Contains(f.Text, "restart") || f.State == freshOlder {
		t.Errorf("retired: %+v", f)
	}
	// Pending and current offer neither.
	if f := get(freshDaemon("v0.1.0", "moved_link"), freshContext{}); f.Command != "" || f.Action != "" {
		t.Errorf("pending: %+v", f)
	}
}

func TestServiceAdviceMatchesTheInstalledUnits(t *testing.T) {
	// The advice rests on these two facts about what the installed units do on a clean exit.
	p := servicePlan{Backend: "launchd", UID: 501, Label: "org.keepgoing.loop.0123", Executable: "/x/looptender", Config: "/x/c.yaml", StateDir: "/x/s", SearchPath: "/usr/bin"}
	plist, err := launchdDefinition(p)
	if err != nil || !strings.Contains(string(plist), "<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>") {
		t.Fatalf("launchd unit no longer restarts only after a failure: %v %s", err, plist)
	}
	p.Backend = "systemd"
	if !strings.Contains(string(systemdDefinition(p)), "Restart=on-failure") {
		t.Fatal("systemd unit no longer restarts only after a failure")
	}
	// So no service advice may end the supervisor cleanly.
	for _, s := range []*freshService{{Backend: "launchd", UID: 501, Label: "l"}, {Backend: "systemd", Label: "l.service"}} {
		if c := freshServiceRestart(s); strings.Contains(c, "shutdown") || !(strings.HasPrefix(c, "launchctl kickstart -k ") || strings.HasPrefix(c, "systemctl --user restart ")) {
			t.Errorf("%q", c)
		}
	}
}

func TestFreshContextOf(t *testing.T) {
	if freshContextOf(true, false, false, false, false, false, nil).Held {
		t.Error("a running loop is not held")
	}
	for name, c := range map[string]freshContext{
		"disabled":  freshContextOf(false, false, false, false, false, false, nil),
		"paused":    freshContextOf(true, true, false, false, false, false, nil),
		"stopped":   freshContextOf(true, false, true, false, false, false, nil),
		"awaiting":  freshContextOf(true, false, false, true, false, false, nil),
		"completed": freshContextOf(true, false, false, false, true, false, nil),
	} {
		if !c.Held {
			t.Errorf("%s should be held", name)
		}
	}
	if !freshContextOfSnapshot(nil, nil).Held || !freshContextOfSnapshot(&clientSnapshot{ControlState: "superseded"}, nil).Retired {
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
	oldStart := freshProcessStart
	defer func() { installedBuildTime, freshProcessStart = old, oldStart }()
	freshProcessStart = now
	installedBuildTime = func() time.Time { return now.Add(-3 * time.Hour) }
	cur, pend := &buildFreshness{State: freshCurrent}, &buildFreshness{State: freshPending}
	got := summarizeBuildFreshness([]*buildFreshness{cur, {State: freshUpdated}, pend, {State: freshOlder}, {State: freshDifferent}, nil}, "v0.2.0", now)
	if got == nil || got.OnIt != 2 || got.NotOnIt != 3 || got.Text != "Build v0.2.0 installed 3 h ago · 2 supervisors on it · 3 not on it" {
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
	r := dockReport{ReadAt: freshAt(t), BuildFreshness: &buildFreshnessSummary{Text: "Build v0.2.0 · 0 supervisors on it · 1 not on it", NotOnIt: 1},
		Loops: []dockLoop{{Key: "k", Name: "held", State: "paused", Phrase: "Paused · also waiting for you", Can: []string{"resume"}, BuildFreshness: f}}}
	l := buildPanelList(r)
	if l.BuildFreshness == nil || l.BuildFreshness.NotOnIt != 1 || l.Loops[0].BuildFreshness != f {
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
		if r.Snapshot == nil || r.Snapshot.BuildFreshness == nil {
			t.Fatalf("private=%v: no freshness: %+v", private, r.Snapshot)
		}
		f := r.Snapshot.BuildFreshness
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
	if err != nil || out.BuildFreshness == nil || *out.BuildFreshness != *want {
		t.Fatalf("status differs from client status: %v %+v vs %+v", err, out.BuildFreshness, want)
	}
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), `"buildFreshness":{"state":"older"`) {
		t.Errorf("status --json lacks it: %s", b)
	}
}
