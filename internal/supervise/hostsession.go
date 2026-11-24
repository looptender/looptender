package supervise

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/hostcheck"
	"github.com/looptender/looptender/internal/snapshot"
)

// HostSessionReason is the reason of a check that held everything because a
// provider's sign-in failed while this computer's login session was not
// available to background processes.
const HostSessionReason = hostcheck.Message + "; waiting for it, nothing sent"

// HostSessionBound is how long a sign-in failure is taken for a lost login
// session. After it, the failure is handled as usual: held for a person and
// reported, with HostSessionLongReason.
const HostSessionBound = 45 * time.Minute

// HostSessionLongReason is the sign-in hold's reason once HostSessionBound
// has passed with the session still unavailable.
const HostSessionLongReason = "the login session has been unavailable for 45 min, or the account is signed out"

// gateHostSession: a provider that says it is not logged in, while this
// computer's login session is unavailable to background processes (after a
// logout or a window-server restart, a supervisor that kept running cannot
// reach the keychain), was not necessarily signed out. For up to
// HostSessionBound, nothing is sent and nothing is handed off (a wake, a
// nudge, a re-prompt, a handoff to a route or a new thread, a switch-back),
// and the sign-in hold is not raised. Every other decision stands: a
// deadline, a send limit, a saved stop or any other hold still applies. On
// the first check that finds the session back, the error is taken as the
// outage's and decided as an ordinary error.
func (t *tick) gateHostSession(obs decide.Observation, in decide.Input, d decide.Decision) decide.Decision {
	ep := t.hostEp()
	noteConsole()
	if t.opts.DryRun || obs.State != decide.Error || obs.ErrorClass != decide.ErrAuth {
		if obs.State != decide.Error && !ep.SessionDownSince.IsZero() {
			ep.SessionDownSince = time.Time{}
			t.saveHostEpisode()
			t.res.HostSession = "restored"
		}
		return d
	}
	signInHold := d.Action == decide.AwaitUser && d.Reason == "auth error"
	blocked := signInHold
	switch d.Action {
	case decide.Wake, decide.Nudge, decide.Reprompt, decide.Handoff, decide.SwitchBack:
		blocked = true
	}
	if !t.hostSessionAvailable() {
		if ep.SessionDownSince.IsZero() {
			ep.SessionDownSince = t.now
			t.saveHostEpisode()
		}
		t.res.HostSession = "unavailable"
		if t.now.Sub(ep.SessionDownSince) >= HostSessionBound {
			if signInHold {
				d.Reason = HostSessionLongReason
			}
			return d
		}
		if !blocked {
			return d
		}
		t.warn("%s; the provider's sign-in failure is not acted on, and nothing is sent or handed off until it is back (at most %s)", hostcheck.Message, HostSessionBound)
		return decide.Decision{Action: decide.None, Reason: HostSessionReason}
	}
	if ep.SessionDownSince.IsZero() {
		return d
	}
	ep.SessionDownSince = time.Time{}
	t.saveHostEpisode()
	t.res.HostSession = "restored"
	if !signInHold && !blocked {
		return d
	}
	in.Obs.ErrorClass = decide.ErrUnknown
	return t.decide("host_session_restored", in)
}

// consoleSeen: this process has seen someone logged in at the console. A
// console that belongs to root from the start (a headless computer, one
// reached over ssh, one at the login window) is not an outage; one whose
// user is gone since an earlier check is.
var consoleSeen struct {
	sync.Mutex
	seen bool
}

// noteConsole remembers a console user seen at any check (a stat only).
func noteConsole() {
	if hostcheck.System.Console == nil {
		return
	}
	if present, known := hostcheck.System.Console(); known && present {
		consoleSeen.Lock()
		consoleSeen.seen = true
		consoleSeen.Unlock()
	}
}

func (t *tick) hostSessionAvailable() bool {
	ctx, cancel := context.WithTimeout(t.ctx, 3*hostcheck.Bound)
	defer cancel()
	if t.opts.HostSession != nil {
		return t.opts.HostSession(ctx)
	}
	s := hostcheck.System.ReadSession(ctx)
	consoleSeen.Lock()
	defer consoleSeen.Unlock()
	if s.Console == hostcheck.OK {
		consoleSeen.seen = true
	}
	return s.Available && !(consoleSeen.seen && s.Console == hostcheck.Unavailable)
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
		level = hostcheck.System.ReadLoad().Level
	}
	if level != hostcheck.LoadOverloaded {
		if level != hostcheck.LoadUnknown {
			t.hostEp().OverloadToldAt = time.Time{}
			t.saveHostEpisode()
		}
		return false
	}
	return t.hostEp().OverloadToldAt.IsZero()
}

// hostEpisodeName is the record of a host episode: a file of its own, not
// state.json, which older builds rewrite and would drop new fields from.
const hostEpisodeName = "host-episode.json"

// hostEpisode is when a check first found a provider's sign-in failing
// while the login session was unavailable (cleared on the check that finds
// it back), and when the session was told the computer is overloaded
// (cleared when the overload ends).
type hostEpisode struct {
	SessionDownSince time.Time `json:"sessionDownSince,omitempty"`
	OverloadToldAt   time.Time `json:"overloadToldAt,omitempty"`
}

// hostEp is this check's episode record, read once.
func (t *tick) hostEp() *hostEpisode {
	if t.hostEpisode == nil {
		t.hostEpisode = &hostEpisode{}
		if b, err := os.ReadFile(filepath.Join(t.loop.StateDir, hostEpisodeName)); err == nil {
			_ = json.Unmarshal(b, t.hostEpisode)
		}
		t.hostEpisodeWas = *t.hostEpisode
	}
	return t.hostEpisode
}

// saveHostEpisode writes the record when this check changed it.
func (t *tick) saveHostEpisode() {
	if t.hostEpisode == nil || *t.hostEpisode == t.hostEpisodeWas || t.opts.DryRun {
		return
	}
	path := filepath.Join(t.loop.StateDir, hostEpisodeName)
	if t.hostEpisode.SessionDownSince.IsZero() && t.hostEpisode.OverloadToldAt.IsZero() {
		_ = os.Remove(path)
	} else if b, err := json.Marshal(t.hostEpisode); err == nil {
		if err := snapshot.WriteFileAtomic(path, b, 0o600, false); err != nil {
			t.warn("the host episode record could not be saved: %v", err)
			return
		}
	}
	t.hostEpisodeWas = *t.hostEpisode
}
