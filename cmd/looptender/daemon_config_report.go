package main

import (
	"context"
	"errors"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/report"
	"github.com/looptender/looptender/internal/state"
)

// updateDaemonConfiguration runs after stop handling. It changes diagnostic
// metadata only, under the tick lock; it never observes or messages an agent.
// Saving the attempt before delivery favors a missed report over duplicates.
func updateDaemonConfiguration(e env, cfg *config.Config, loaded, dryRun bool, lease *ownerLease, since *time.Time) *time.Time {
	since, _ = updateDaemonConfigurationDeferred(e, cfg, loaded, dryRun, lease, since)
	return since
}

// updateDaemonConfigurationDeferred also reports whether recording or sending
// the diagnostic was put off only because the tick lock was busy. The daemon
// retries at its next control poll then, not after a whole interval: the
// diagnostic is already visible, and its single report must not wait behind it.
func updateDaemonConfigurationDeferred(e env, cfg *config.Config, loaded, dryRun bool, lease *ownerLease, since *time.Time) (*time.Time, bool) {
	if since == nil && cfg.UnreadableSources() {
		at := time.Now().UTC()
		since = &at
	}
	if dryRun {
		if loaded && cfg.CheckSources() == nil {
			return nil, false
		}
		return since, false
	}
	l, err := lock.Acquire(cfg.Loop.StateDir)
	if err != nil {
		return since, since != nil && errors.Is(err, lock.ErrHeld) && reportPending(cfg)
	}
	defer l.Release()
	if checkOwnership(cfg.Loop.StateDir, lease) != nil {
		return since, false
	}
	st, err := state.Load(cfg.Loop.StateDir)
	if err != nil {
		e.errf("configuration diagnostic could not be saved; no report sent")
		return since, false
	}
	if loaded && cfg.CheckSources() == nil {
		if st.DaemonConfigUnavailable != nil {
			st.DaemonConfigUnavailable = nil
			if state.Save(cfg.Loop.StateDir, st) != nil {
				e.errf("configuration recovery diagnostic could not be saved")
			}
		}
		return nil, false
	}
	if st.DaemonConfigUnavailable != nil {
		at := st.DaemonConfigUnavailable.Since
		since = &at
	} else if since != nil {
		st.DaemonConfigUnavailable = &state.DaemonConfigUnavailable{Since: *since}
	}
	episode := st.DaemonConfigUnavailable
	if episode == nil || episode.ReportAttempted {
		return since, false
	}
	episode.ReportAttempted = true
	if state.Save(cfg.Loop.StateDir, st) != nil {
		e.errf("configuration diagnostic could not be saved; no report sent")
		return since, false
	}
	ctx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
	defer cancel()
	event := report.Event{Type: "error", Loop: cfg.Loop.Name, At: time.Now().UTC(), Text: cfg.Loop.Name + ": the configuration file cannot be loaded. Ordinary checks have stopped until it is restored. A saved stop may use the last configuration read.", Data: map[string]any{"cause": "configuration_unavailable", "since": episode.Since}}
	if err := reporter(e, cfg).Report(ctx, event); err != nil {
		e.errf("configuration error report attempt failed; it will not be repeated")
	}
	return since, false
}

// reportPending reads the saved episode without the tick lock: true until the
// episode's one report has been attempted, so a long outage whose report was
// already sent does not make the daemon poll at the control rate.
func reportPending(cfg *config.Config) bool {
	st, err := state.Load(cfg.Loop.StateDir)
	return err == nil && (st.DaemonConfigUnavailable == nil || !st.DaemonConfigUnavailable.ReportAttempted)
}
