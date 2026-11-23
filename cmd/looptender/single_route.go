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
func doctorRoutes(r *doctorReport, cfg *config.Config, cfgPath string, st state.File, stateReadable bool, held string, now time.Time) {
	// A completed or disabled loop sends nothing, so a limit can not stop work
	// it is not doing: the warning is for loops that can run.
	if w := singleRouteWarning(cfg); w != "" && held != "completed" && held != "disabled" {
		r.add("routes.single", "warning", w, singleRouteFix(cfg, cfgPath))
	}
	for _, p := range successorPlans(cfg) {
		r.add("routes.successors."+p.Candidate, "warning", p.Text(), "Leave it on to have blocked lanes continued on the new provider, or set bb.replace_blocked_children: false.")
	}
	if !stateReadable {
		return
	}
	s := describeLimitWait(st.LimitWaitReport, cfg.Loop, held, now)
	if s.State == "none" || s.ResetPassed {
		return
	}
	if held != "" {
		// Information only: a held loop observes nothing, so this is what the
		// last check before the hold recorded.
		msg := fmt.Sprintf("A long limit wait was recorded before the hold (%s): %s was waiting on a provider limit at the check of %s, with the reset reported for %s, in about %s. The loop is held (%s), so this may be out of date.", held, s.Target, s.DetectedAt.UTC().Format(time.RFC3339), s.ResetsAt.UTC().Format(time.RFC3339), s.ResetIn, held)
		if s.ResetAfterDeadline {
			msg += " The reset falls after the loop's deadline."
		}
		r.add("limit_wait", "ok", msg, "")
		return
	}
	r.add("limit_wait", "warning", fmt.Sprintf("Long limit wait: %s has been waiting on a provider limit since the check of %s, and the reset is reported for %s, in about %s. No handoff is possible because %s. The loop is not held and sends nothing until then.%s %s",
		s.Target, s.DetectedAt.UTC().Format(time.RFC3339), s.ResetsAt.UTC().Format(time.RFC3339), s.ResetIn, limitWaitReasonText(s.Reason), map[bool]string{true: " The reset falls after the loop's deadline, which will hold the loop first.", false: ""}[s.ResetAfterDeadline], limitWaitTold(s)), limitWaitWayOut)
}

// singleRouteFix is the advice for a single-route loop. A BB loop is named
// the one command that gives it a takeover by a new conversation.
func singleRouteFix(cfg *config.Config, cfgPath string) string {
	if cfg.Loop.Adapter == "bb" && !cfg.Loop.FollowsStandard() {
		return "To have a new conversation on another provider take over at a limit, run looptender fallbacks arm -c " + commandQuote(cfgPath) + " (it previews first). " + limitWaitWayOut
	}
	return limitWaitWayOut
}
