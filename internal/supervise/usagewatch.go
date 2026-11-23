package supervise

import (
	"context"
	"errors"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/prompt"
	"github.com/looptender/looptender/internal/state"
	"github.com/looptender/looptender/internal/usageburn"
)

// UsageReadFunc reads the subscription quota of an accounts catalog entry,
// with no model request (the catalog's quota read).
type UsageReadFunc func(ctx context.Context, account string) (accounts.QuotaView, error)

// usageWarning is a pending usage-burn warning this check found.
type usageWarning struct {
	rec  state.UsageWatch
	text prompt.UsageWarning
}

// usageWatchObservation reads the account's quota when the watch is due,
// keeps the reading and its weekly projection, and returns whether a warning
// is pending for the agent. It runs only for the loop's primary
// conversation, on a check that observed it; never on a dry run. It reads no
// lane and changes nothing but its own record.
func (t *tick) usageWatchObservation() bool {
	w := t.loop.UsageWatch
	if w == nil || t.opts.DryRun || t.opts.UsageRead == nil || t.res.Target != state.Primary {
		return false
	}
	rec, err := state.LoadUsageWatch(t.loop.StateDir)
	if err != nil {
		t.warn("the usage watch record could not be read: %v", err)
		return false
	}
	if rec == nil || rec.Account != w.Account {
		rec = &state.UsageWatch{Account: w.Account}
	}
	if (rec.CheckedAt.IsZero() || t.now.Sub(rec.CheckedAt) >= w.Every.Duration || rec.CheckedAt.After(t.now)) && t.left() > 10*time.Second {
		t.usageRead(rec)
	}
	t.usageView = rec
	if rec.ExhaustsAt.IsZero() || !t.now.Before(rec.ResetsAt) || rec.WarnedFor.Equal(rec.ResetsAt) ||
		t.now.Sub(rec.ReadAt) > 2*w.Every.Duration || rec.ExhaustsAt.Sub(t.now) > w.WarnWithin.Duration {
		return false
	}
	t.usageWarn = &usageWarning{rec: *rec, text: prompt.UsageWarning{
		UsedPercent: rec.UsedPercent, Hours: usageburn.Hours(rec.ExhaustsAt, t.now), ExhaustsAt: rec.ExhaustsAt, ResetsAt: rec.ResetsAt}}
	return true
}

func (t *tick) usageRead(rec *state.UsageWatch) {
	ctx, cancel := context.WithTimeout(t.ctx, min(15*time.Second, t.left()-5*time.Second))
	v, err := t.opts.UsageRead(ctx, rec.Account)
	timedOut := errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)
	cancel()
	rec.CheckedAt, rec.Reason = t.now, ""
	r, ok := usageburn.Weekly(v)
	switch {
	case timedOut:
		rec.Reason = "timeout"
	case err != nil:
		rec.Reason = "unavailable"
	case !ok:
		rec.Reason = "no_weekly_window"
		if v.Reason != "" && accounts.ValidQuotaReason(v.Reason) {
			rec.Reason = v.Reason
		}
	}
	if rec.Reason == "" {
		if !rec.ResetsAt.Equal(r.ResetsAt) && !rec.WarnedFor.Equal(r.ResetsAt) {
			rec.WarnedFor = time.Time{}
		}
		rec.ReadAt, rec.Bucket, rec.UsedPercent, rec.ResetsAt = t.now, r.Bucket, r.UsedPercent, r.ResetsAt
		rec.PercentPerHour, rec.ExhaustsAt = 0, time.Time{}
		if p, ok := usageburn.Project(r, t.now); ok {
			rec.PercentPerHour = p.PercentPerHour
			if p.BeforeReset {
				rec.ExhaustsAt = p.ExhaustsAt
			}
		}
	}
	if err := state.SaveUsageWatch(t.loop.StateDir, *rec); err != nil {
		t.warn("the usage watch record could not be saved: %v", err)
	}
}

// usageWarningText adds the pending warning to a wake, or makes it the whole
// message of the wake it caused. It reports whether the message carries it.
func (t *tick) usageWarningText(d decide.Decision, m prompt.Message) (prompt.Message, bool) {
	if t.usageWarn == nil || d.Action != decide.Wake {
		return m, false
	}
	if d.UsageWarningWake {
		return prompt.UsageWarningOnly(t.usageWarn.text, t.decisionInput.Obs.ActiveChildren), true
	}
	return prompt.WithUsageWarning(m, t.usageWarn.text), true
}

// usageWarningSent records that the agent was warned about this window, once
// the send was accepted. A failed send leaves it pending.
func (t *tick) usageWarningSent(carried bool, err error) {
	if !carried || err != nil || t.usageWarn == nil {
		return
	}
	rec := t.usageWarn.rec
	rec.WarnedFor = rec.ResetsAt
	if err := state.SaveUsageWatch(t.loop.StateDir, rec); err != nil {
		t.warn("the usage warning could not be recorded as sent: %v", err)
		return
	}
	t.res.UsageWarned = true
}
