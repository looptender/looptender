package state

import (
	"testing"
	"time"
)

// An enabled loop that is not held and has no recorded check (a loop just
// moved, transferred, enabled or resumed with nothing running its checks)
// is judged from its last control change, not left out.
func TestNeverCheckedLoopIsJudgedFromItsLastControlChange(t *testing.T) {
	now := time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)
	changed := now.Add(-5 * time.Hour)
	base := ScheduleInput{Enabled: true, ControlChangedAt: changed, DaemonInterval: 5 * time.Minute}
	for name, c := range map[string]struct {
		in        func(ScheduleInput) ScheduleInput
		overdue   bool
		notJudged string
	}{
		"nothing checks it":       {func(in ScheduleInput) ScheduleInput { return in }, true, ""},
		"within the grace period": {func(in ScheduleInput) ScheduleInput { in.ControlChangedAt = now.Add(-10 * time.Minute); return in }, false, ""},
		"long interval":           {func(in ScheduleInput) ScheduleInput { in.DaemonInterval = 3 * time.Hour; return in }, false, ""},
		"held":                    {func(in ScheduleInput) ScheduleInput { in.Held = true; return in }, false, "held"},
		"disabled":                {func(in ScheduleInput) ScheduleInput { in.Enabled = false; return in }, false, "disabled"},
		"no control change known": {func(in ScheduleInput) ScheduleInput { in.ControlChangedAt = time.Time{}; return in }, false, "never_checked"},
		"daemon in its first pass": {func(in ScheduleInput) ScheduleInput {
			in.DaemonStartedAt = now.Add(-time.Hour)
			return in
		}, false, ""},
		"daemon passes record nothing": {func(in ScheduleInput) ScheduleInput {
			in.DaemonStartedAt, in.DaemonLastPassStartedAt = now.Add(-time.Hour), now.Add(-10*time.Minute)
			return in
		}, true, ""},
	} {
		t.Run(name, func(t *testing.T) {
			s := ScheduleOf(File{}, c.in(base), now)
			if s.Overdue != c.overdue || s.NotJudged != c.notJudged {
				t.Fatalf("%+v", s)
			}
			if c.notJudged == "" {
				if s.Basis != "never_checked" || s.Threshold != max(NeverCheckedOverdue, OverdueFactor*c.in(base).DaemonInterval) || !s.LastCheckAt.IsZero() {
					t.Fatalf("%+v", s)
				}
			}
		})
	}
	// Once a check is recorded, the ordinary rule applies again.
	if s := ScheduleOf(File{CheckedAt: now.Add(-time.Minute)}, base, now); s.Overdue || s.Basis != "fallback" {
		t.Fatalf("%+v", s)
	}
}
