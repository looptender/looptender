package supervise

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/looptender/looptender/internal/adapter"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/state"
)

// noteUsageLimit records the provider usage limit this check observed on
// the active target, with the window it is in and the Claude config
// directory the check runs under, or clears the record when the target is
// not at a limit. Nothing is decided from it: it is what status, the panel
// and reports say about the limit.
func (t *tick) noteUsageLimit(target string, agent config.Agent, obs decide.Observation) {
	if t.opts.DryRun {
		return
	}
	if obs.ErrorClass != decide.ErrRateLimit || !obs.ResetsAt.After(t.now) {
		t.st.UsageLimit = nil
		return
	}
	u := &state.UsageLimit{Target: target, Provider: agent.Provider, Window: obs.LimitWindow, ResetsAt: obs.ResetsAt.UTC(), ObservedAt: t.now}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" && filepath.IsAbs(dir) && len(dir) <= 4096 {
		u.ConfigDir = filepath.Clean(dir)
	}
	t.st.UsageLimit = u
}

// maxLimitedLanesRead bounds the lanes one notice reads.
const maxLimitedLanesRead = 16

// limitedLanes reads the errored lanes a child-error notice is about and
// counts those at a provider usage limit with a reset still ahead, with the
// latest reset and its window. It reads each lane's status and the typed
// fields of its error events, as lane recovery does, never message text;
// it acts on no lane. Zero when the adapter cannot read lanes.
func (t *tick) limitedLanes(a adapter.Adapter, errored []string) (n int, until time.Time, w decide.LimitWindow) {
	lr, ok := a.(adapter.LaneRecoverer)
	if !ok || t.opts.DryRun {
		return 0, time.Time{}, decide.LimitWindow{}
	}
	for i, id := range errored {
		if i >= maxLimitedLanesRead || t.left() < 30*time.Second {
			break
		}
		ctx, cancel := context.WithTimeout(t.ctx, 10*time.Second)
		f := lr.InspectLane(ctx, id)
		cancel()
		if f.Verified && f.Errored && f.ClassKnown && f.Class == decide.ErrRateLimit && f.ResetsAt.After(t.now) {
			n++
			if f.ResetsAt.After(until) {
				until, w = f.ResetsAt, f.Window
			}
		}
	}
	return n, until, w
}
