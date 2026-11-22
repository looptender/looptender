package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/state"
)

// singleRouteWarning says, for a loop that cannot hand off on a usage limit,
// what that means in practice, or "" when a usage limit can move the work.
// It reads the configuration only.
func singleRouteWarning(cfg *config.Config) string {
	routes := 0
	if cfg.FallbackSet != nil {
		for _, c := range cfg.FallbackSet.Candidates {
			if !c.Disabled {
				routes++
			}
		}
	}
	why := ""
	switch {
	case routes == 0:
		why = "it has no other route to hand off to"
	case !slices.Contains(cfg.Loop.Errors.FailoverOn, "rate_limit"):
		why = "errors.failover_on does not include rate_limit, so no route takes over on a usage limit"
	default:
		return ""
	}
	told := ""
	if cfg.Loop.Report == nil {
		told = " With no report section, nobody is told while it waits."
	}
	return fmt.Sprintf("This loop has a single route: %s. Any provider usage limit stops its work for the limit's full length, which can be days for a weekly limit: it waits and sends nothing until the limit lifts.%s", why, told)
}

// doctorRoutes warns about a loop that cannot hand off on a usage limit, and
// about a long limit wait the last checks recorded. A held loop observes
// nothing, so its recorded wait is not reported as current.
func doctorRoutes(r *doctorReport, cfg *config.Config, st state.File, stateReadable bool, held string, now time.Time) {
	if w := singleRouteWarning(cfg); w != "" {
		r.add("routes.single", "warning", w, limitWaitWayOut)
	}
	if !stateReadable || held != "" {
		return
	}
	s := describeLimitWait(st.LimitWaitReport, cfg.Loop, held, now)
	if s.State == "none" || s.ResetPassed {
		return
	}
	r.add("limit_wait", "warning", fmt.Sprintf("Long limit wait: %s has been waiting on a provider limit since the check of %s, and the reset is reported for %s, in about %s. No handoff is possible because %s. The loop is not held and sends nothing until then. %s",
		s.Target, s.DetectedAt.UTC().Format(time.RFC3339), s.ResetsAt.UTC().Format(time.RFC3339), s.ResetIn, limitWaitReasonText(s.Reason), limitWaitTold(s)), limitWaitWayOut)
}
