package supervise

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/hostcheck"
)

func setHost(t *testing.T, sessionOK bool, load float64) {
	t.Helper()
	setHostConsole(t, sessionOK, true, load)
}

// setHostConsole sets the readers: whether the keychain can be read, whether
// someone is at the console, and the load. It forgets any console user seen.
func setHostConsole(t *testing.T, keychainOK, console bool, load float64) {
	t.Helper()
	old := hostReaders
	t.Cleanup(func() { hostReaders = old; consoleSeen.seen = false })
	consoleSeen.seen = false
	sessionOK := keychainOK
	r := old
	r.Console = func() (bool, bool) { return console, true }
	r.Keychain = func(context.Context) error {
		if sessionOK {
			return nil
		}
		return errors.New("fictional: the security daemon cannot be reached")
	}
	r.LoadAverage = func() (float64, error) { return load, nil }
	r.Cores = func() int { return 8 }
	hostReaders = r
}

// "Not logged in" while this computer's login session is gone for
// background processes is not a sign-out: nothing is sent, nothing is held
// for a person, and the check says why. When the session is back, the error
// is the outage's and is re-prompted; a sign-in failure after that is real.
func TestASignInFailureWhileTheSessionIsGoneHoldsNothingAndSaysWhy(t *testing.T) {
	w := newWorld(t, true, false)
	p := w.primary(decide.Observation{State: decide.Error, ErrorClass: decide.ErrAuth})
	setHost(t, false, 1)
	res := runTick(t, w, t0)
	if res.Action != "none" || res.Reason != HostSessionReason || res.HostSession != "unavailable" || len(p.sent) != 0 {
		t.Fatalf("session gone: %+v, %d sent", res, len(p.sent))
	}
	if strings.Contains(strings.ToLower(res.Reason), "log in") || !strings.Contains(res.Reason, "signed nothing out") {
		t.Fatalf("reason: %q", res.Reason)
	}
	if res := runTick(t, w, t0.Add(20*time.Minute)); res.Action != "none" || len(p.sent) != 0 {
		t.Fatalf("still gone: %+v", res)
	}
	setHost(t, true, 1)
	res = runTick(t, w, t0.Add(30*time.Minute))
	if res.Action != "reprompt" || res.HostSession != "restored" || len(p.sent) != 1 {
		t.Fatalf("back: %+v, %d sent", res, len(p.sent))
	}
	res = runTick(t, w, t0.Add(2*time.Hour))
	if res.Action != "await_user" || res.Reason != "auth error" {
		t.Fatalf("a real sign-in failure afterwards: %+v", res)
	}
}

// An overloaded computer while the session's children run: one count-only
// notice per overload, no action on the lanes.
func TestAnOverloadIsToldOncePerEpisodeWithACountOnly(t *testing.T) {
	w := newWorld(t, true, false)
	p := w.primary(decide.Observation{State: decide.Idle, ActiveChildren: 3})
	setHost(t, true, 100)
	res := runTick(t, w, t0)
	if res.Action != "wake" || res.Reason != decide.HostOverloadWakeReason || !res.HostOverloadTold || len(p.sent) != 1 {
		t.Fatalf("overloaded: %+v, %d sent", res, len(p.sent))
	}
	if msg := p.sent[0]; !strings.HasPrefix(msg, "[keepgoing:host-load lanes=3]\n") || !strings.Contains(msg, "3 lanes running") || strings.Contains(msg, "keep going") || strings.Contains(msg, "thr_") {
		t.Fatalf("message: %q", msg)
	}
	if res := runTick(t, w, t0.Add(2*time.Hour)); res.Action != "none" || len(p.sent) != 1 {
		t.Fatalf("told once: %+v", res)
	}
	setHost(t, true, 4)
	runTick(t, w, t0.Add(3*time.Hour))
	setHost(t, true, 100)
	if res := runTick(t, w, t0.Add(5*time.Hour)); res.Action != "wake" || len(p.sent) != 2 {
		t.Fatalf("a new overload: %+v, %d sent", res, len(p.sent))
	}
	_ = hostcheck.LoadOverloaded
}

// A console that belongs to root from the start, with a keychain that can be
// read, is a headless computer or one at the login window: a sign-in failure
// there is real, and is held as usual.
func TestARootConsoleFromTheStartIsNotAnOutage(t *testing.T) {
	w := newWorld(t, true, false)
	w.primary(decide.Observation{State: decide.Error, ErrorClass: decide.ErrAuth})
	setHostConsole(t, true, false, 1)
	if res := runTick(t, w, t0); res.Action != "await_user" || res.Reason != "auth error" || res.HostSession != "" {
		t.Fatalf("%+v", res)
	}
}

// A console user seen at an earlier check and gone now is the session
// ending, even while the keychain still answers.
func TestAConsoleUserWhoLeavesIsAnOutage(t *testing.T) {
	w := newWorld(t, true, false)
	p := w.primary(decide.Observation{State: decide.Idle})
	setHostConsole(t, true, true, 1)
	runTick(t, w, t0)
	sent := len(p.sent)
	r := hostReaders
	r.Console = func() (bool, bool) { return false, true }
	hostReaders = r
	p.obs = decide.Observation{State: decide.Error, ErrorClass: decide.ErrAuth}
	if res := runTick(t, w, t0.Add(time.Hour)); res.Action != "none" || res.HostSession != "unavailable" || len(p.sent) != sent {
		t.Fatalf("%+v", res)
	}
}

// The quiet state is bounded: after 45 minutes the sign-in failure is held
// for a person, saying both things it may be.
func TestALostSessionIsWaitedOutForAtMost45Minutes(t *testing.T) {
	w := newWorld(t, true, false)
	w.primary(decide.Observation{State: decide.Error, ErrorClass: decide.ErrAuth})
	setHost(t, false, 1)
	if res := runTick(t, w, t0); res.Action != "none" {
		t.Fatalf("first: %+v", res)
	}
	if res := runTick(t, w, t0.Add(30*time.Minute)); res.Action != "none" {
		t.Fatalf("within: %+v", res)
	}
	if res := runTick(t, w, t0.Add(46*time.Minute)); res.Action != "await_user" || res.Reason != HostSessionLongReason {
		t.Fatalf("after 45 min: %+v", res)
	}
}

// Only sends and handoffs are held back; every other decision on that check
// stands: a deadline, a send limit, a stop, any other hold.
func TestTheGateHoldsOnlySendsAndHandoffs(t *testing.T) {
	w := newWorld(t, true, false)
	tk := &tick{ctx: context.Background(), loop: w.cfg.Loop, now: t0, res: &Result{}}
	setHost(t, false, 1)
	auth := decide.Observation{State: decide.Error, ErrorClass: decide.ErrAuth}
	for _, d := range []decide.Decision{{Action: decide.Wake}, {Action: decide.Nudge}, {Action: decide.Reprompt}, {Action: decide.Handoff}, {Action: decide.SwitchBack}, {Action: decide.AwaitUser, Reason: "auth error"}} {
		if got := tk.gateHostSession(auth, decide.Input{}, d); got.Action != decide.None || got.Reason != HostSessionReason {
			t.Errorf("%s %q: %+v", d.Action, d.Reason, got)
		}
	}
	for _, d := range []decide.Decision{
		{Action: decide.AwaitUser, Reason: "loop deadline reached; revise loop.limits.deadline before resuming"},
		{Action: decide.AwaitUser, Reason: "loop send limit reached; increase loop.limits.max_sends before resuming"},
		{Action: decide.Stop, Reason: "stop requested"},
		{Action: decide.AwaitUser, Reason: "error persisted after re-prompts"},
		{Action: decide.None, Reason: "paused"},
	} {
		if got := tk.gateHostSession(auth, decide.Input{}, d); got != d {
			t.Errorf("%s %q changed to %+v", d.Action, d.Reason, got)
		}
	}
}
