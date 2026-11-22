package main

import (
	"encoding/json"
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
	if js := dockUIText(t, "dock.js"); !strings.Contains(js, "absolute or home path") || !strings.Contains(js, "a relative path such as docs/plan.md is shown") {
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
