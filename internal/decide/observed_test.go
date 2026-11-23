package decide

import (
	"strings"
	"testing"
	"time"
)

// An observed target (a sessionpipe session) is never sent to, re-prompted,
// nudged, stopped or moved, whatever its state and whatever else would ask
// for it. A gone target still holds for a person.
func TestObservedTargetIsNeverWoken(t *testing.T) {
	h := time.Hour
	for _, tc := range []struct {
		name string
		mut  func(*Input)
		want Action
	}{
		{"idle", func(i *Input) { i.Obs.State = Idle }, None},
		{"idle long after the last wake", func(i *Input) { i.Obs.State, i.SinceWake = Idle, 24*h }, None},
		{"active and stalled", func(i *Input) {
			i.Obs.State, i.ProgressTracked, i.UnchangedFor, i.StallAfter = Active, true, 24*h, h
		}, None},
		{"error with re-prompts left", func(i *Input) { i.Obs.State, i.Obs.ErrorClass = Error, ErrUnknown }, None},
		{"a usage limit with a fallback", func(i *Input) {
			i.Obs.State, i.Obs.ErrorClass = Error, ErrRateLimit
			i.Failover = Failover{Eligible: true, Available: true}
		}, None},
		{"switch-back ready", func(i *Input) { i.Obs.State, i.SwitchBackReady = Idle, true }, None},
		{"children errored", func(i *Input) { i.Obs.State, i.Obs.ActiveChildren, i.Obs.ErroredChildren = Idle, 1, []string{"child"} }, None},
		{"waiting on a person", func(i *Input) { i.Obs.State, i.Obs.AwaitingInput = Active, true }, None},
		{"unknown", func(i *Input) { i.Obs.State = Unknown }, None},
		{"stop while active", func(i *Input) { i.Obs.State, i.Control.Stop = Active, true }, None},
		{"gone", func(i *Input) { i.Obs.State, i.Obs.Gone = Idle, true }, AwaitUser},
		{"paused", func(i *Input) { i.Obs.State, i.Control.Paused = Idle, true }, None},
	} {
		in := base()
		in.ObservedOnly = true
		tc.mut(&in)
		d := Decide(in)
		if d.Action != tc.want {
			t.Errorf("%s: %s (%q), want %s", tc.name, d.Action, d.Reason, tc.want)
		}
		if d.Action == None && !strings.Contains(d.Reason, "observed") && d.Reason != "paused" {
			t.Errorf("%s: the reason %q does not say the session is observed", tc.name, d.Reason)
		}
		// The same input on an ordinary target acts, so the rule is what holds it.
		if tc.name == "idle" {
			in.ObservedOnly = false
			if d := Decide(in); d.Action != Wake {
				t.Fatalf("control: an ordinary idle target gets %s, want wake", d.Action)
			}
		}
	}
	if got := observedReason(Observation{State: Error}); got != "its last turn ended in error"+ObservedReasonSuffix {
		t.Fatal(got)
	}
}
