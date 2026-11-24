package supervise

import (
	"path/filepath"
	"time"

	"github.com/looptender/looptender/internal/adapter/sessionpipe"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/incident"
)

func (t *tick) recordIncident(failed bool) error {
	outcome := "succeeded"
	if t.opts.DryRun {
		outcome = "dry_run"
	}
	if failed {
		outcome = "failed"
	}
	t.incidentDecisions.Availability = "captured"
	t.incidentProbes.Availability = "captured"
	// The supervisor may append context to a reason; the typed rule is unaffected.
	cause := incident.Cause(t.res.Reason)
	if t.incidentLimitWait != "" && t.res.Action == string(decide.None) {
		cause = string(t.incidentLimitWait)
	}
	// A check in which usage-aware timing changed the thread has its cause;
	// the engine's own decision stays among the record's evaluations.
	if t.incidentTimingCause != "" {
		cause = t.incidentTimingCause
	}
	r := incident.Record{Kind: "tick", Decisions: t.incidentDecisions, CheckStartedAt: t.now, Build: t.opts.Version, Adapter: t.incidentAdapter,
		TargetKey: incident.TargetKey(t.incidentTarget), Observation: t.incidentObservation,
		// The target after this check differs when a handoff, a switch-back,
		// a usage-aware timing step or a route leaving the configuration moved the loop.
		TargetKeyAfter: incident.TargetKey(t.res.Target), Probes: t.incidentProbes,
		ControlBefore: t.incidentControl, Cause: cause, Action: t.res.Action, Outcome: outcome, DryRun: t.opts.DryRun,
		CountersAvailable: t.incidentStateLoaded, Nudges: t.st.Nudges, Reprompts: t.st.Reprompts,
		LimitWaitReport: t.incidentLimitWaitReport, NewThread: t.incidentNewThread, RouteListChanged: t.incidentRouteListChanged, TurnRetry: t.incidentTurnRetry, Lanes: t.incidentLanes, Refusal: t.refusalMark, ModelFallback: t.fallbackMark, ContextBytes: t.contextBytes}
	if t.returnSaved {
		r.Returned = t.incidentReturn
	}
	if c, err := control.Load(t.loop.StateDir); err == nil {
		r.ControlAfter = incident.Gate(c.Gate())
	}
	if s := t.st.StallRetry; s != nil && s.Target == t.incidentTarget && !s.Waiting && !s.ResumedAt.IsZero() {
		at := s.ResumedAt
		r.LastActiveRecoveryAt = &at
	}
	return incident.Append(t.loop.StateDir, r)
}

// observedOnly reports whether this check's target is observed, never woken.
func (t *tick) observedOnly() bool {
	return config.ObservedOnly(t.loop.Adapter) || config.ObservedOnly(t.incidentAdapter)
}

// observedWake reports whether this check's observed target may be woken:
// the loop opted in with sessionpipe.send: wake_only.
func (t *tick) observedWake() bool {
	return t.observedOnly() && t.loop.Agent.WakesObserved()
}

// WakeHoldReason is the reason recorded while a wake is held after one that
// did not get through, and what status and doctor say.
func WakeHoldReason(outcome, code string, until time.Time) string {
	s := "last wake " + outcome
	if code != "" {
		s += ": " + code
	}
	return s + ", next attempt after " + until.UTC().Format(time.RFC3339) + " or the session's next turn"
}

// gateObservedWake holds a wake of an observed session while this computer's
// login session is unavailable to background processes: sessionpipe's
// daemon delivers it, and a headless delivery would need the same keychain
// the supervisor cannot reach then.
func (t *tick) gateObservedWake(d decide.Decision) decide.Decision {
	if d.Action != decide.Wake || !t.observedWake() || t.opts.DryRun {
		return d
	}
	if !t.hostSessionAvailable() {
		t.res.HostSession = "unavailable"
		return decide.Decision{Action: decide.None, Reason: HostSessionReason}
	}
	// After a wake that did not get through, the next waits for the
	// session's next turn event or the idle spacing interval: a check that
	// still sees the session idle does not try again at once.
	dir := filepath.Join(t.loop.StateDir, "targets", t.res.Target)
	if held, outcome, code, until := sessionpipe.WakeHold(dir, t.now, t.loop.Progress.MinWakeInterval.Duration); held {
		return decide.Decision{Action: decide.None, Reason: WakeHoldReason(outcome, code, until)}
	}
	return d
}

func (t *tick) decide(stage string, in decide.Input) decide.Decision {
	// An observed target is never woken, whichever rule asks.
	in.ObservedOnly = in.ObservedOnly || t.observedOnly()
	in.ObservedWake = in.ObservedWake || t.observedWake()
	d := decide.Decide(in)
	t.incidentDecisions.Add(stage, in, d)
	t.incidentLimitWait = d.LimitWait
	// A limit wait with a structured reset still ahead: a daemon checks again
	// at that reset rather than at the end of its interval.
	t.limitResetsAt = time.Time{}
	if d.LimitWait != "" && in.Obs.ResetsAt.After(t.now) {
		t.limitResetsAt = in.Obs.ResetsAt
	}
	t.handoffAvailable = in.Failover.Available
	return d
}
