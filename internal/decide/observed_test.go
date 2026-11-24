package decide

import (
	"reflect"
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

// With sessionpipe.send: wake_only, an idle observed session is woken by the
// ordinary rules; everything else stays held, and the holds still apply.
func TestObservedTargetWithWakeOnly(t *testing.T) {
	h := time.Hour
	for _, tc := range []struct {
		name string
		mut  func(*Input)
		want Action
	}{
		{"idle wakes", func(i *Input) { i.Obs.State = Idle }, Wake},
		{"active and stalled is not nudged", func(i *Input) {
			i.Obs.State, i.ProgressTracked, i.UnchangedFor, i.StallAfter = Active, true, 24*h, h
		}, None},
		{"error is not re-prompted", func(i *Input) { i.Obs.State, i.Obs.ErrorClass = Error, ErrUnknown }, None},
		{"a limit with a fallback is not handed off", func(i *Input) {
			i.Obs.State, i.Obs.ErrorClass = Error, ErrRateLimit
			i.Failover = Failover{Eligible: true, Available: true}
		}, None},
		{"waiting on a person", func(i *Input) { i.Obs.State, i.Obs.AwaitingInput = Active, true }, None},
		{"background work", func(i *Input) { i.Obs.State, i.Obs.Background = Idle, 1 }, None},
		{"unknown", func(i *Input) { i.Obs.State = Unknown }, None},
		{"stop", func(i *Input) { i.Obs.State, i.Control.Stop = Active, true }, None},
		{"paused", func(i *Input) { i.Obs.State, i.Control.Paused = Idle, true }, None},
		{"send limit holds", func(i *Input) { i.Obs.State, i.SendLimitReached = Idle, true }, AwaitUser},
		{"gone holds", func(i *Input) { i.Obs.State, i.Obs.Gone = Idle, true }, AwaitUser},
		{"switch-back is never made", func(i *Input) { i.Obs.State, i.SwitchBackReady = Idle, true }, None},
	} {
		in := base()
		in.ObservedOnly, in.ObservedWake = true, true
		tc.mut(&in)
		d := Decide(in)
		if d.Action != tc.want {
			t.Errorf("%s: %s (%q), want %s", tc.name, d.Action, d.Reason, tc.want)
		}
	}
	// Without ObservedOnly the flag means nothing.
	in := base()
	in.ObservedWake, in.Obs.State, in.ProgressTracked, in.UnchangedFor, in.StallAfter = true, Active, true, 24*h, h
	if d := Decide(in); d.Action != Nudge {
		t.Fatalf("an ordinary target with ObservedWake: %s", d.Action)
	}
}

// Only the ordinary idle wake reaches an observed session that opted in;
// every other kind of wake, including any added later, is held by default.
func TestObservedWakeIsAnAllowList(t *testing.T) {
	if !OrdinaryIdleWake(Decision{Action: Wake, Reason: "idle", IdleWake: true}) {
		t.Fatal("the ordinary idle wake is not allowed")
	}
	if OrdinaryIdleWake(Decision{Action: Wake}) || OrdinaryIdleWake(Decision{Action: Nudge, IdleWake: true}) {
		t.Fatal("a wake that is not the idle rule's, or a nudge, is allowed")
	}
	// Every other flag named ...Wake, set with IdleWake, holds it.
	rt := reflect.TypeOf(Decision{})
	flags := 0
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Name == "IdleWake" || !strings.HasSuffix(f.Name, "Wake") || f.Type.Kind() != reflect.Bool {
			continue
		}
		flags++
		d := Decision{Action: Wake, IdleWake: true}
		reflect.ValueOf(&d).Elem().Field(i).SetBool(true)
		if OrdinaryIdleWake(d) {
			t.Errorf("a wake with %s is allowed", f.Name)
		}
	}
	if flags < 4 {
		t.Fatalf("found %d wake kinds; expected at least ChildErrorWake, AssignmentWake, UsageWarningWake and HostOverloadWake", flags)
	}
	// And each notice wake the rules make is held for an observed target.
	for name, mut := range map[string]func(*Input){
		"child errors": func(i *Input) {
			i.Obs.ErroredChildren, i.UnannouncedErroredChildren, i.MaxChildErrorNotices = []string{"c"}, 1, 3
		},
		"usage warning": func(i *Input) { i.UsageWarning = true },
		"host overload": func(i *Input) { i.HostOverload = true },
		"assignment":    func(i *Input) { i.AssignmentReady = 1 },
	} {
		in := base()
		in.Obs.State, in.Obs.ActiveChildren = Idle, 1
		mut(&in)
		ordinary := Decide(in)
		if ordinary.Action != Wake {
			t.Fatalf("%s: the ordinary rules give %s (%q); the case needs a notice wake", name, ordinary.Action, ordinary.Reason)
		}
		in.ObservedOnly, in.ObservedWake = true, true
		if d := Decide(in); d.Action != None {
			t.Errorf("%s: an observed target gets %s (%q)", name, d.Action, d.Reason)
		}
	}
}
