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
	old := hostReaders
	t.Cleanup(func() { hostReaders = old })
	r := old
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
	if strings.Contains(strings.ToLower(res.Reason), "log in") || !strings.Contains(res.Reason, "nothing was signed out") {
		t.Fatalf("reason: %q", res.Reason)
	}
	if res := runTick(t, w, t0.Add(time.Hour)); res.Action != "none" || len(p.sent) != 0 {
		t.Fatalf("still gone: %+v", res)
	}
	setHost(t, true, 1)
	res = runTick(t, w, t0.Add(2*time.Hour))
	if res.Action != "reprompt" || res.HostSession != "restored" || len(p.sent) != 1 {
		t.Fatalf("back: %+v, %d sent", res, len(p.sent))
	}
	res = runTick(t, w, t0.Add(3*time.Hour))
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
	if msg := p.sent[0]; !strings.HasPrefix(msg, "[looptender:host-load lanes=3]\n") || !strings.Contains(msg, "3 lanes running") || strings.Contains(msg, "keep going") || strings.Contains(msg, "thr_") {
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
