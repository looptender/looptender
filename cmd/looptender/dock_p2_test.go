package main

import (
	"encoding/json"
	"github.com/looptender/looptender/internal/control"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/prompt"
)

// One state word per state, the one status uses: a loop that is off is "off"
// whether or not its task was marked finished or a stop is recorded.
func TestDockCallsADisabledLoopOffLikeStatus(t *testing.T) {
	for name, c := range map[string]*clientControls{
		"disabled and completed": {Enabled: false, Completed: true},
		"disabled and stopped":   {Enabled: false, Stop: true},
		"disabled and paused":    {Enabled: false, Paused: true},
		"disabled and awaiting":  {Enabled: false, AwaitingUser: true},
	} {
		l, _ := projectDockLoop(dockEvidence{controls: c}.input(), dockNow, defaultStaleAfter, false)
		if l.State != "off" || l.Phrase != "Off" {
			t.Errorf("%s: %s %q", name, l.State, l.Phrase)
		}
	}
	// Enabled loops keep their words.
	for want, c := range map[string]*clientControls{"finished": {Enabled: true, Completed: true}, "stopped": {Enabled: true, Stop: true}, "paused": {Enabled: true, Paused: true}} {
		if l, _ := projectDockLoop(dockEvidence{controls: c}.input(), dockNow, defaultStaleAfter, false); l.State != want {
			t.Errorf("%s: %s", want, l.State)
		}
	}
}

// A long limit wait carries what status says about it: the reset falls after
// the loop's deadline, and a held loop's wait is from before the hold.
func TestDockLimitWaitCarriesTheDeadlineAndHoldNotes(t *testing.T) {
	reset := dockNow.Add(100 * time.Hour)
	in := dockEvidence{controls: &clientControls{Enabled: true, Paused: true}, status: func(s *statusOut) {
		s.LimitWait = limitWaitStatus{State: "not_subscribed", Target: "primary", ResetsAt: &reset, DetectedAt: &reset, Reason: "limit_no_fallback", ResetAfterDeadline: true, Held: "paused"}
	}}.input()
	l, _ := projectDockLoop(in, dockNow, defaultStaleAfter, false)
	if l.LimitWait == nil || !l.LimitWait.AfterDeadline {
		t.Fatalf("%+v", l.LimitWait)
	}
	note := l.LimitWait.Note.render(dockNow)
	for _, want := range []string{"reset falls after this loop's deadline", "will be held by the deadline", "The loop is held (paused)", "may be out of date"} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q: %s", want, note)
		}
	}
	// No deadline, not held: no note.
	in = dockEvidence{controls: &clientControls{Enabled: true}, status: func(s *statusOut) {
		s.LimitWait = limitWaitStatus{State: "not_subscribed", Target: "primary", ResetsAt: &reset, DetectedAt: &reset}
	}}.input()
	if l, _ := projectDockLoop(in, dockNow, defaultStaleAfter, false); l.LimitWait == nil || len(l.LimitWait.Note) != 0 || l.LimitWait.AfterDeadline {
		t.Fatalf("%+v", l.LimitWait)
	}
}

// The panel shows status's wake-policy warning, without a path.
func TestDockShowsTheWakePolicyWarningWithoutAPath(t *testing.T) {
	for name, c := range map[string]struct {
		w    *wakePolicyStatus
		want string
	}{
		"hand-edited":       {&wakePolicyStatus{PolicyState: prompt.PolicyState{Source: prompt.PolicyCustom}}, "hand-edited"},
		"not recorded":      {&wakePolicyStatus{PolicyState: prompt.PolicyState{Source: prompt.PolicyGenerated, FileVersion: 4}}, "version 4 but is not recorded as following"},
		"edited after":      {&wakePolicyStatus{PolicyState: prompt.PolicyState{Source: prompt.PolicyGenerated, Recorded: true}}, "edited after it was recorded"},
		"another program":   {&wakePolicyStatus{PolicyState: prompt.PolicyState{Source: prompt.PolicyFollowing}, CommandPrograms: []promptCommandFinding{{Path: "/private/example/old/keepgoing", Verdict: prefixOtherBuild}}}, "not the one installed"},
		"prefix is missing": {&wakePolicyStatus{PolicyState: prompt.PolicyState{Source: prompt.PolicyFollowing}, CommandPrefix: prefixMissing}, "does not exist"},
		"not all checked":   {&wakePolicyStatus{PolicyState: prompt.PolicyState{Source: prompt.PolicyFollowing}, CommandProgramsNotLooked: 3}, "Not every program"},
	} {
		got := dockWakePolicyText(c.w).render(dockNow)
		if !strings.Contains(got, c.want) || strings.Contains(got, "/private/") {
			t.Errorf("%s: %q", name, got)
		}
	}
	if got := dockWakePolicyText(&wakePolicyStatus{PolicyState: prompt.PolicyState{Source: prompt.PolicyFollowing}}); len(got) != 0 {
		t.Errorf("a loop that follows updates is warned: %v", got)
	}
	if got := dockWakePolicyText(nil); got != nil {
		t.Error("nil status")
	}
	in := dockEvidence{controls: &clientControls{Enabled: true}, status: func(s *statusOut) {
		s.WakePolicy = &wakePolicyStatus{PolicyState: prompt.PolicyState{Source: prompt.PolicyCustom}}
	}}.input()
	if l, _ := projectDockLoop(in, dockNow, defaultStaleAfter, false); len(l.WakePolicy) == 0 {
		t.Error("the loop view carries no wake-policy warning")
	}
}

// The rule for a hold's note: absolute and home paths, web addresses, email,
// IDs and long codes are hidden; a relative project path is shown, and the
// page's copy says so.
func TestDockNoteHidesAbsolutePathsAndAddressesButShowsRelativePaths(t *testing.T) {
	got, hid := dockMaskNote("Read docs/reference/PLAN.md, then /Users/example/work/file.txt, ~/notes.md and http://localhost:8080/x or www.example.com/a")
	if !hid || !strings.Contains(got, "docs/reference/PLAN.md") || strings.Contains(got, "/Users/example") || strings.Contains(got, "localhost") || strings.Contains(got, "~/notes") || strings.Contains(got, "www.example") {
		t.Fatalf("%s", got)
	}
	if js := dockUIText(t, "dock.js"); !strings.Contains(js, "an absolute or home path (also one that contains .. or starts with Users/, home/ or root/") || !strings.Contains(js, "IPv4 or IPv6 address") || !strings.Contains(js, "user name and password before a host") || !strings.Contains(js, "another relative path such as docs/plan.md is shown") {
		t.Error("the page's copy does not say what is hidden")
	}
}

// The Usage table scrolls inside its own region instead of widening the page,
// and the failure banner leaves room for content at phone widths.
func TestDockCSSKeepsTheUsageTableAndBannerFromCoveringContent(t *testing.T) {
	css := dockUIText(t, "dock.css")
	for _, want := range []string{".table-wrap { margin-top: 0.75rem; max-inline-size: 100%; min-inline-size: 0; overflow-x: auto; }", ".pane, .panel { min-inline-size: 0; }", "@media (max-width: 34rem) { body.has-banner { padding-bottom: 14rem; } }"} {
		if !strings.Contains(css, want) {
			t.Errorf("dock.css lacks %q", want)
		}
	}
}

// Every loop field the jsdom page test sets or deletes (testdata/dock_page_variant_keys.json,
// which the script checks its variants against) must exist in dockLoop's JSON
// tags, nested paths included, so a renamed field cannot leave a variant
// testing nothing.
func TestDockPageVariantKeysExistInTheLoopJSON(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "dock_page_variant_keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil || len(keys) < 20 {
		t.Fatalf("%v %d", err, len(keys))
	}
	for _, path := range keys {
		typ := reflect.TypeOf(dockLoop{})
		for _, part := range strings.Split(path, ".") {
			for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
				typ = typ.Elem()
			}
			if typ.Kind() != reflect.Struct {
				t.Errorf("%s: %q is not inside a struct (%s)", path, part, typ)
				break
			}
			var next reflect.Type
			for i := 0; i < typ.NumField(); i++ {
				if name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ","); name == part {
					next = typ.Field(i).Type
				}
			}
			if next == nil {
				t.Errorf("%s: dockLoop's JSON has no field %q", path, part)
				break
			}
			typ = next
		}
	}
}

// Each shape the copy names is hidden; ordinary text that looks a little like
// one is not.
func TestDockNoteMaskingFollowsTheCopyExactly(t *testing.T) {
	hidden := map[string]string{
		"user name and password":       "see user:pass@host.example/path now",
		"user name and password, port": "see admin:hunter2@db.example:5432 now",
		"a path that goes up":          "see ../secret/b.txt now",
		"a path that goes up, deeper":  "see docs/../../etc/passwd now",
		"home directory without slash": "see Users/example/work/x.txt now",
		"home directory, lower case":   "see home/example/x now",
		"root's home":                  "see root/.ssh/id now",
		"a UNC path":                   `see \\server\share\a.txt now`,
		"a bracketed IPv6 address":     "listening on [::1]:80 now",
		"a bare IPv6 address":          "see fe80::1 now",
		"a full IPv6 address":          "see 2001:db8:0:0:0:0:0:1 now",
	}
	for name, note := range hidden {
		got, hid := dockMaskNote(note)
		if !hid || !strings.Contains(got, dockNoteHidden) {
			t.Errorf("%s: %q was not hidden: %q", name, note, got)
			continue
		}
		for _, secret := range []string{"user:", "pass@", "hunter2", "admin", "secret", "passwd", "example/work", "id now", "server", "fe80", "2001", "[::1]"} {
			if strings.Contains(note, secret) && strings.Contains(got, secret) {
				t.Errorf("%s: %q still shows %q: %q", name, note, secret, got)
			}
		}
	}
	shown := []string{
		"see docs/reference/PLAN.md now", "version 1.2.3 and v0.2.0", "at 12:30:45 the check ran", "ratio 3:1 or 10:30",
		"see src/app/main.go and pkg/x", "go to the home page", "root cause", "users like it", "a/b/c",
	}
	for _, note := range shown {
		got, hid := dockMaskNote(note)
		if hid {
			t.Errorf("%q was hidden: %q", note, got)
		}
	}
}

// The panel's state word is the one status uses, for every combination of the
// control flags, and for a superseded loop.
func TestDockStateWordAgreesWithStatusForEveryControlCombination(t *testing.T) {
	want := map[string]string{"disabled": "off", "stop requested": "stopped", "paused": "paused", "awaiting user": "waiting_for_you", "completed": "finished"}
	for bits := 0; bits < 32; bits++ {
		c := &clientControls{Enabled: bits&1 != 0, Paused: bits&2 != 0, Stop: bits&4 != 0, AwaitingUser: bits&8 != 0, Completed: bits&16 != 0}
		word := control.File{Enabled: c.Enabled, Paused: c.Paused, Stop: c.Stop, AwaitingUser: c.AwaitingUser, Completed: c.Completed}.Summary()
		in := dockEvidence{controls: c}.input()
		l, need := projectDockLoop(in, dockNow, defaultStaleAfter, false)
		// Who is waited for is what it was before the state word followed
		// status: an enabled loop that is not stopped or completed and has a
		// person asked for, paused or not.
		wantNeed := c.Enabled && !c.Stop && !c.Completed && c.AwaitingUser
		if (need != nil) != wantNeed {
			t.Errorf("%+v: needs-you membership is %v, want %v", *c, need != nil, wantNeed)
		}
		r := buildDockReport([]dockInput{in}, dockNow, time.Minute, defaultStaleAfter, false, true, 0, true)
		if (len(r.NeedsYou) == 1) != wantNeed || buildPanelList(r).Loops[0].NeedsYou != wantNeed {
			t.Errorf("%+v: the report lists %d needs and panel list says %v, want %v", *c, len(r.NeedsYou), buildPanelList(r).Loops[0].NeedsYou, wantNeed)
		}
		if c.Enabled && c.Paused && !c.Stop && !c.Completed && c.AwaitingUser {
			if l.State != "paused" || l.Phrase != "Paused \u00B7 also waiting for you" || l.AlsoWaiting == "" || !strings.Contains(l.Detail, "Also waiting for you: ") {
				t.Errorf("%+v: paused and waiting reads %s %q %q", *c, l.State, l.Phrase, l.AlsoWaiting)
			}
		} else if l.AlsoWaiting != "" {
			t.Errorf("%+v: also waiting where it is not paused and waiting", *c)
		}
		// One exception, kept on purpose: awaiting and completed together is
		// status's "awaiting user", but a finished task asks nothing of the
		// person, so the panel keeps reading it as finished (no need).
		if c.Enabled && !c.Stop && !c.Paused && c.AwaitingUser && c.Completed {
			if l.State != "finished" || need != nil {
				t.Errorf("%+v: completed with an awaiting flag reads %s", *c, l.State)
			}
			continue
		}
		if w, ok := want[word]; ok {
			if l.State != w {
				t.Errorf("%+v: status says %q, the panel says %s (%q)", *c, word, l.State, l.Phrase)
			}
		} else if l.State == "off" || l.State == "stopped" || l.State == "paused" || l.State == "finished" || l.State == "waiting_for_you" {
			t.Errorf("%+v: status says %q, the panel says %s", *c, word, l.State)
		}
	}
	// Enabled, completed and paused: paused, as status says.
	if l, _ := projectDockLoop(dockEvidence{controls: &clientControls{Enabled: true, Completed: true, Paused: true}}.input(), dockNow, defaultStaleAfter, false); l.State != "paused" {
		t.Errorf("completed and paused reads %s", l.State)
	}
	// A superseded loop: the snapshot's own word wins.
	in := dockEvidence{controls: &clientControls{Enabled: true}}.input()
	in.Client.Snapshot.ControlState = "superseded"
	if l, _ := projectDockLoop(in, dockNow, defaultStaleAfter, false); l.Phrase != "Superseded" || l.State != "off" {
		t.Errorf("superseded reads %s %q", l.State, l.Phrase)
	}
}

// A paused loop that is still waiting for a person sorts with the loops that
// need one, ahead of a supervising loop.
func TestDockPausedAndWaitingSortsWithTheLoopsThatNeedYou(t *testing.T) {
	waiting := dockEvidence{controls: &clientControls{Enabled: true, Paused: true, AwaitingUser: true}}.input()
	waiting.Entry.ID, waiting.Entry.Loop = "loop_00000000000000000000000000000001", "zz-paused-and-waiting"
	fine := dockEvidence{controls: &clientControls{Enabled: true}}.input()
	fine.Entry.ID, fine.Entry.Loop = "loop_00000000000000000000000000000002", "aa-supervising"
	r := buildDockReport([]dockInput{fine, waiting}, dockNow, time.Minute, defaultStaleAfter, false, true, 0, true)
	if r.Loops[0].Name != "zz-paused-and-waiting" || len(r.NeedsYou) != 1 {
		t.Fatalf("%q first, %d needs", r.Loops[0].Name, len(r.NeedsYou))
	}
}

// How a loop is checked is said on every loop, held or not, by the rule status
// uses (scheduleStatus.CheckedBy): a daemon, a service, ticks, nothing (or not
// checked yet), or unknown.
func TestDockSaysHowALoopIsCheckedWhateverItsHolds(t *testing.T) {
	at := func(c *clientControls, daemon daemonStatus, sch scheduleStatus, service string) dockCheckedBy {
		in := dockEvidence{controls: c, status: func(s *statusOut) { s.Schedule = sch }}.input()
		in.Client.Snapshot.Daemon = daemon
		in.ServiceFile = service
		l, _ := projectDockLoop(in, dockNow, defaultStaleAfter, false)
		return l.CheckedBy
	}
	ago := func(d time.Duration) *time.Time { x := dockNow.Add(-d); return &x }
	none, notRec := daemonStatus{State: "not_recorded"}, daemonStatus{State: "not_recorded"}
	for name, held := range map[string]*clientControls{"paused": {Enabled: true, Paused: true}, "awaiting": {Enabled: true, AwaitingUser: true}, "stopped": {Enabled: true, Stop: true}, "finished": {Enabled: true, Completed: true}, "off": {Enabled: false}, "running": {Enabled: true}} {
		// Ticks exactly when status says so, whatever the age of the last check.
		for label, sch := range map[string]scheduleStatus{
			"a cadence known and a recent check":      {CheckedBy: "ticks", LastCheckAt: ago(9 * time.Minute)},
			"a weekly cadence, checked two hours ago": {CheckedBy: "ticks", LastCheckAt: ago(2 * time.Hour)},
		} {
			if got := at(held, notRec, sch, ""); got.Kind != "ticks" || !strings.Contains(got.Text, "Checked by ticks from a scheduler not on record") {
				t.Errorf("%s, %s: %+v", name, label, got)
			}
		}
		// Not ticks where status does not say ticks, however recent the check.
		for label, sch := range map[string]scheduleStatus{
			"one manual tick 20 minutes ago (no cadence)":      {LastCheckAt: ago(20 * time.Minute)},
			"a 5-minute scheduler that stopped 25 minutes ago": {LastCheckAt: ago(25 * time.Minute), GapsSeen: 5},
			"a check long ago": {LastCheckAt: ago(5 * time.Hour)},
		} {
			if got := at(held, notRec, sch, ""); got.Kind != "none" || got.Text != "Nothing on record checks it." {
				t.Errorf("%s, %s: %+v", name, label, got)
			}
		}
		// No check recorded: not yet checked until the alarm fires, then nothing.
		if got := at(held, none, scheduleStatus{}, ""); got.Kind != "none" || got.Text != "Not checked yet." {
			t.Errorf("%s never checked, in the grace period: %+v", name, got)
		}
		if got := at(held, none, scheduleStatus{Overdue: true}, ""); got.Kind != "none" || got.Text != "Nothing on record checks it." {
			t.Errorf("%s never checked, overdue: %+v", name, got)
		}
		// A daemon that answers wins over everything else.
		if got := at(held, daemonStatus{State: "responsive"}, scheduleStatus{CheckedBy: "ticks"}, ""); got.Kind != "daemon" || got.Text != "Checked by its daemon." {
			t.Errorf("%s with a daemon: %+v", name, got)
		}
		if got := at(held, daemonStatus{State: "responsive"}, scheduleStatus{}, "same_file"); got.Kind != "service" {
			t.Errorf("%s with a service: %+v", name, got)
		}
		// Not asked (offline) and not answering say different things.
		if got := at(held, daemonStatus{State: "skipped"}, scheduleStatus{}, ""); got.Kind != "unknown" || got.Text != "Its scheduler was not asked." {
			t.Errorf("%s offline: %+v", name, got)
		}
		if got := at(held, daemonStatus{State: "unavailable"}, scheduleStatus{CheckedBy: "ticks"}, ""); got.Kind != "unknown" || got.Text != "Its scheduler did not answer." {
			t.Errorf("%s with a scheduler that did not answer: %+v", name, got)
		}
	}
	if l, _ := projectDockLoop(dockEvidence{controls: &clientControls{Enabled: true}, noSnap: true}.input(), dockNow, defaultStaleAfter, false); l.CheckedBy.Kind != "unknown" || l.CheckedBy.Text != "How it is checked is unknown." {
		t.Errorf("an unread loop: %+v", l.CheckedBy)
	}
}
