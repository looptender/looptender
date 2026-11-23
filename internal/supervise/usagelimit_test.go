package supervise

import (
	"testing"
	"time"

	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/state"
)

// A check that finds the active target at a usage limit records which
// window, until when, and the Claude directory it ran under; the next check
// that finds the target past it clears the record.
func TestCheckRecordsTheUsageLimitWindow(t *testing.T) {
	w := newWorld(t, true, false)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	obs := limited
	obs.ResetsAt = t0.Add(50 * time.Hour)
	obs.LimitWindow = decide.CleanLimitWindow("seven_day", "Weekly limit", "seven_day")
	p := w.primary(obs)
	runTick(t, w, t0)
	u, err := state.LoadUsageLimit(w.dir)
	if err != nil || u == nil {
		t.Fatalf("%+v %v", u, err)
	}
	if u.Target != state.Primary || u.Provider != "claude-code" || u.Window.Kind() != "weekly" || !u.ResetsAt.Equal(obs.ResetsAt) || !u.ObservedAt.Equal(t0) || u.ConfigDir != dir {
		t.Fatalf("%+v", u)
	}
	p.obs = decide.Observation{State: decide.Idle}
	runTick(t, w, t0.Add(time.Hour))
	if u, err := state.LoadUsageLimit(w.dir); u != nil || err != nil {
		t.Fatalf("not cleared: %+v %v", u, err)
	}
}

// Lanes lane recovery inspected are counted for the child-error notice when
// they are at a usage limit with a reset ahead, with the latest reset and its
// window. Lanes in error for another reason, or whose reset has passed, are
// not.
func TestLaneLimitsAreCountedFromLaneRecovery(t *testing.T) {
	at := func(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }
	tk := &tick{now: t0}
	for _, f := range []decide.LaneFacts{
		{Verified: true, Errored: true, ClassKnown: true, Class: decide.ErrRateLimit, ResetsAt: at(10), Window: decide.CleanLimitWindow("five_hour", "", "")},
		{Verified: true, Errored: true, ClassKnown: true, Class: decide.ErrRateLimit, ResetsAt: at(90), Window: decide.CleanLimitWindow("seven_day", "", "")},
		{Verified: true, Errored: true, ClassKnown: true, Class: decide.ErrNetwork},
		{Verified: true, Errored: true, ClassKnown: true, Class: decide.ErrRateLimit, ResetsAt: at(-1)},
		{Errored: true, ClassKnown: true, Class: decide.ErrRateLimit, ResetsAt: at(5)},
	} {
		tk.noteLaneLimit(f)
	}
	if tk.lanesAtLimit != 2 || !tk.lanesLimitUntil.Equal(at(90)) || tk.lanesLimitWindow.Kind() != "weekly" {
		t.Fatalf("%d %v %+v", tk.lanesAtLimit, tk.lanesLimitUntil, tk.lanesLimitWindow)
	}
}
