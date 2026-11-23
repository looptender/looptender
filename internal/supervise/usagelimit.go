package supervise

import (
	"os"
	"path/filepath"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/state"
)

// noteUsageLimit records the provider usage limit this check observed on
// the active target (state.UsageLimitFileName), with the window it is in
// and the Claude config directory the check runs under, or removes the
// record when the target is not at a limit. Nothing is decided from it: it
// is what status, the panel and reports say about the limit.
func (t *tick) noteUsageLimit(target string, agent config.Agent, obs decide.Observation) {
	if t.opts.DryRun {
		return
	}
	if obs.ErrorClass != decide.ErrRateLimit || !obs.ResetsAt.After(t.now) {
		if err := state.SaveUsageLimit(t.loop.StateDir, nil); err != nil {
			t.warn("the usage limit record could not be cleared: %v", err)
		}
		return
	}
	u := &state.UsageLimit{Target: target, Provider: agent.Provider, Window: obs.LimitWindow, ResetsAt: obs.ResetsAt.UTC(), ObservedAt: t.now}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" && filepath.IsAbs(dir) && len(dir) <= 4096 {
		u.ConfigDir = filepath.Clean(dir)
	}
	if err := state.SaveUsageLimit(t.loop.StateDir, u); err != nil {
		t.warn("the usage limit could not be recorded: %v", err)
	}
}

// noteLaneLimit counts a lane lane recovery inspected that is at a provider
// usage limit with its reset still ahead, keeping the latest reset and its
// window, for the child-error notice. Nothing else reads a lane for it: a
// lane lane recovery does not inspect (it is off, or the lane was never seen
// working) is not counted.
func (t *tick) noteLaneLimit(f decide.LaneFacts) {
	if !f.Verified || !f.Errored || !f.ClassKnown || f.Class != decide.ErrRateLimit || !f.ResetsAt.After(t.now) {
		return
	}
	t.lanesAtLimit++
	if f.ResetsAt.After(t.lanesLimitUntil) {
		t.lanesLimitUntil, t.lanesLimitWindow = f.ResetsAt, f.Window
	}
}
