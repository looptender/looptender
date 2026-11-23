package supervise

import (
	"context"
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
	st, err := state.Load(w.dir)
	if err != nil || st.UsageLimit == nil {
		t.Fatalf("%+v %v", st.UsageLimit, err)
	}
	u := st.UsageLimit
	if u.Target != state.Primary || u.Provider != "claude-code" || u.Window.Kind() != "weekly" || !u.ResetsAt.Equal(obs.ResetsAt) || !u.ObservedAt.Equal(t0) || u.ConfigDir != dir {
		t.Fatalf("%+v", u)
	}
	p.obs = decide.Observation{State: decide.Idle}
	runTick(t, w, t0.Add(time.Hour))
	if st, _ := state.Load(w.dir); st.UsageLimit != nil {
		t.Fatalf("not cleared: %+v", st.UsageLimit)
	}
}

// The lanes a child-error notice is about are read, and those at a usage
// limit with a reset ahead are counted with the latest reset and its window.
// Lanes in error for another reason, or whose reset has passed, are not.
func TestLimitedLanesCountsOnlyLanesAtALimit(t *testing.T) {
	weekly := decide.CleanLimitWindow("seven_day", "", "")
	at := func(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }
	la := &laneAdapter{fakeAdapter: &fakeAdapter{}, facts: map[string]decide.LaneFacts{
		"thr_lane1": {Verified: true, Errored: true, ClassKnown: true, Class: decide.ErrRateLimit, ResetsAt: at(10), Window: decide.CleanLimitWindow("five_hour", "", "")},
		"thr_lane2": {Verified: true, Errored: true, ClassKnown: true, Class: decide.ErrRateLimit, ResetsAt: at(90), Window: weekly},
		"thr_lane3": {Verified: true, Errored: true, ClassKnown: true, Class: decide.ErrNetwork},
		"thr_lane4": {Verified: true, Errored: true, ClassKnown: true, Class: decide.ErrRateLimit, ResetsAt: at(-1)},
	}}
	tk := &tick{ctx: context.Background(), now: t0, opts: Options{Clock: func() time.Time { return t0 }}, deadline: t0.Add(time.Hour)}
	n, until, w := tk.limitedLanes(la, []string{"thr_lane1", "thr_lane2", "thr_lane3", "thr_lane4"})
	if n != 2 || !until.Equal(at(90)) || w.Kind() != "weekly" {
		t.Fatalf("%d %v %+v", n, until, w)
	}
	// An adapter that cannot read lanes counts none.
	if n, _, _ := tk.limitedLanes(&fakeAdapter{}, []string{"thr_lane1"}); n != 0 {
		t.Fatalf("%d", n)
	}
}
