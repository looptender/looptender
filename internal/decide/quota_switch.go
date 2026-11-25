package decide

import (
	"fmt"
	"math"
	"time"
)

// QuotaSwitch configures the opt-in quota switch threshold (Q4) evaluated at
// turn end against the active route's structured quota window reading.
type QuotaSwitch struct {
	// Threshold is the share in (0, 1] of any reported quota window at or
	// above which the loop switches to a confirmed fallback at turn end.
	// Zero disables quota-threshold switching.
	Threshold float64
	// ReadingAvailable is true only when a valid structured reading exists for
	// the active route's current account identity.
	ReadingAvailable bool
	// HighestUsed is the highest used fraction in [0, 1] across the reading's
	// active (non-reset) quota windows.
	HighestUsed float64
	// ReadingAge is how old the reading is at this check; negative is invalid.
	ReadingAge time.Duration
	// MaxAge is the freshness limit; readings without a positive MaxAge or
	// older than MaxAge are treated as stale and never trigger a switch.
	MaxAge time.Duration
	// Stale marks a reading that is no longer fresh (for example, every
	// window's reset has passed since the reading was taken).
	Stale bool
	// Window optionally names the highest-used window key (diagnostic only).
	Window string
}

// decideQuotaSwitch evaluates the opt-in quota switch threshold at turn end.
// It never switches on unavailable, stale, or malformed readings, never switches
// without a confirmed fallback, and never routes around a refusal or restriction.
func decideQuotaSwitch(in Input) (Decision, bool) {
	q := in.QuotaSwitch
	if math.IsNaN(q.Threshold) || math.IsInf(q.Threshold, 0) || q.Threshold <= 0 || q.Threshold > 1 {
		return Decision{}, false
	}
	if !q.ReadingAvailable || q.Stale {
		return Decision{}, false
	}
	if q.MaxAge <= 0 || q.ReadingAge < 0 || q.ReadingAge > q.MaxAge {
		return Decision{}, false
	}
	if math.IsNaN(q.HighestUsed) || math.IsInf(q.HighestUsed, 0) || q.HighestUsed < 0 || q.HighestUsed > 1 {
		return Decision{}, false
	}
	if q.HighestUsed < q.Threshold {
		return Decision{}, false
	}
	if in.RefusalStreak > 0 || Declined(in.Obs.ErrorClass) {
		return Decision{}, false
	}
	if !in.Failover.Available {
		return Decision{}, false
	}
	if in.HandoffDeferred {
		return none(HandoffDeferredReason), true
	}
	reason := fmt.Sprintf("quota usage %.0f%% reached switch_at %.0f%%", q.HighestUsed*100, q.Threshold*100)
	if q.Window != "" {
		reason = fmt.Sprintf("quota window %s usage %.0f%% reached switch_at %.0f%%", q.Window, q.HighestUsed*100, q.Threshold*100)
	}
	return Decision{Action: Handoff, QuotaSwitch: true, Reason: reason}, true
}
