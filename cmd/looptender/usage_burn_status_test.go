package main

import (
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/state"
)

// Status and the panel say the projection only while, from a reading of the
// watched account that is recent enough, the weekly window runs out before
// it resets.
func TestUsageBurnIsSaidOnlyWhileItRunsOutFirst(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	l := config.Loop{StateDir: t.TempDir(), UsageWatch: &config.UsageWatch{Account: "example-claude-a", Every: config.Duration{Duration: 30 * time.Minute}, WarnWithin: config.Duration{Duration: 12 * time.Hour}}}
	if describeUsageBurn(l, now) != nil {
		t.Fatal("said with no record")
	}
	rec := state.UsageWatch{Account: "example-claude-a", CheckedAt: now.Add(-10 * time.Minute), ReadAt: now.Add(-10 * time.Minute), Bucket: "seven_day", UsedPercent: 91,
		ResetsAt: now.Add(48 * time.Hour), PercentPerHour: 0.9, ExhaustsAt: now.Add(9 * time.Hour)}
	if err := state.SaveUsageWatch(l.StateDir, rec); err != nil {
		t.Fatal(err)
	}
	v := describeUsageBurn(l, now)
	if v == nil || v.Words != "At this rate the weekly window is used up in about 9 h, before it resets." || v.UsedPercent != 91 || v.Warned {
		t.Fatalf("%+v", v)
	}
	var b strings.Builder
	printUsageBurn(&b, v)
	if !strings.HasPrefix(b.String(), "usage: At this rate the weekly window is used up in about 9 h") {
		t.Fatal(b.String())
	}
	for name, change := range map[string]func(*config.Loop, *state.UsageWatch){
		"stale reading":      func(_ *config.Loop, r *state.UsageWatch) { r.ReadAt = now.Add(-2 * time.Hour) },
		"lasts to the reset": func(_ *config.Loop, r *state.UsageWatch) { r.ExhaustsAt = time.Time{} },
		"window reset":       func(_ *config.Loop, r *state.UsageWatch) { r.ResetsAt = now.Add(-time.Minute) },
		"another account":    func(l *config.Loop, _ *state.UsageWatch) { l.UsageWatch.Account = "example-claude-b" },
		"watch off":          func(l *config.Loop, _ *state.UsageWatch) { l.UsageWatch = nil },
	} {
		ll, rr := l, rec
		w := *l.UsageWatch
		ll.UsageWatch = &w
		change(&ll, &rr)
		if err := state.SaveUsageWatch(l.StateDir, rr); err != nil {
			t.Fatal(err)
		}
		if v := describeUsageBurn(ll, now); v != nil {
			t.Fatalf("%s: %+v", name, v)
		}
	}
}
