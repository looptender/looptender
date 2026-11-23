package supervise

import (
	"context"
	"time"

	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/hostcheck"
)

// HostSessionReason is the reason of a check that held everything because a
// provider's sign-in failed while this computer's login session was not
// available to background processes.
const HostSessionReason = hostcheck.Message + "; waiting for it, nothing sent"

// gateHostSession: a provider that says it is not logged in, while this
// computer's login session is unavailable to background processes (after a
// logout or a window-server restart, a supervisor that kept running cannot
// reach the keychain), was not signed out. Nothing is sent, nothing is
// handed off and no hold for a person is raised: the check waits. On the
// first check that finds the session back, the error is the outage's, so it
// is decided as an ordinary error (a re-prompt within the usual limits)
// rather than as a sign-in a person must fix.
func (t *tick) gateHostSession(obs decide.Observation, in decide.Input, d decide.Decision) decide.Decision {
	if t.opts.DryRun || obs.State != decide.Error || obs.ErrorClass != decide.ErrAuth {
		if obs.State != decide.Error && !t.st.HostSessionDownSince.IsZero() {
			t.st.HostSessionDownSince = time.Time{}
			t.res.HostSession = "restored"
		}
		return d
	}
	if !t.hostSessionAvailable() {
		if t.st.HostSessionDownSince.IsZero() {
			t.st.HostSessionDownSince = t.now
		}
		t.res.HostSession = "unavailable"
		t.warn("%s; the provider's sign-in failure is not acted on, and nothing is sent or handed off until it is back", hostcheck.Message)
		return decide.Decision{Action: decide.None, Reason: HostSessionReason}
	}
	if t.st.HostSessionDownSince.IsZero() {
		return d
	}
	t.st.HostSessionDownSince = time.Time{}
	t.res.HostSession = "restored"
	in.Obs.ErrorClass = decide.ErrUnknown
	return t.decide("host_session_restored", in)
}

// hostReaders reads this computer; tests replace it.
var hostReaders = hostcheck.System

func (t *tick) hostSessionAvailable() bool {
	ctx, cancel := context.WithTimeout(t.ctx, 3*hostcheck.Bound)
	defer cancel()
	if t.opts.HostSession != nil {
		return t.opts.HostSession(ctx)
	}
	return hostReaders.ReadSession(ctx).Available
}

// hostOverloadObservation: whether to tell the session, once per episode,
// that this computer is overloaded while its children run. The load is read
// only while children run; an episode ends when the load is no longer
// overloaded.
func (t *tick) hostOverloadObservation(obs decide.Observation) bool {
	if t.opts.DryRun || obs.ActiveChildren == 0 || obs.State != decide.Idle {
		return false
	}
	level := ""
	if t.opts.HostLoad != nil {
		level = t.opts.HostLoad()
	} else {
		level = hostReaders.ReadLoad().Level
	}
	if level != hostcheck.LoadOverloaded {
		if level != hostcheck.LoadUnknown {
			t.st.HostOverloadToldAt = time.Time{}
		}
		return false
	}
	return t.st.HostOverloadToldAt.IsZero()
}
