package supervise

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/hostcheck"
	"github.com/looptender/looptender/internal/snapshot"
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
		if obs.State != decide.Error && !t.hostEp().SessionDownSince.IsZero() {
			t.hostEp().SessionDownSince = time.Time{}
			t.saveHostEpisode()
			t.res.HostSession = "restored"
		}
		return d
	}
	if !t.hostSessionAvailable() {
		if t.hostEp().SessionDownSince.IsZero() {
			t.hostEp().SessionDownSince = t.now
			t.saveHostEpisode()
		}
		t.res.HostSession = "unavailable"
		t.warn("%s; the provider's sign-in failure is not acted on, and nothing is sent or handed off until it is back", hostcheck.Message)
		return decide.Decision{Action: decide.None, Reason: HostSessionReason}
	}
	if t.hostEp().SessionDownSince.IsZero() {
		return d
	}
	t.hostEp().SessionDownSince = time.Time{}
	t.saveHostEpisode()
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
