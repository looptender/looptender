package main

import (
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/state"
)

// Status and the panel say the projection only while, from a reading of the
// watched account that is recent enough, the weekly window runs out before
// it resets.
func TestUsageBurnIsSaidOnlyWhileItRunsOutFirst(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	setting := &state.UsageWatchSetting{Account: "example-claude-a", Every: "30m", WarnWithin: "12h", SetAt: now}
	if err := state.SaveUsageWatchSetting(dir, setting); err != nil {
		t.Fatal(err)
	}
	if describeUsageBurn(dir, now) != nil {
		t.Fatal("said with no record")
	}
	rec := state.UsageWatch{Account: "example-claude-a", CheckedAt: now.Add(-10 * time.Minute), ReadAt: now.Add(-10 * time.Minute), Bucket: "seven_day", UsedPercent: 91,
		ResetsAt: now.Add(48 * time.Hour), PercentPerHour: 0.9, ExhaustsAt: now.Add(9 * time.Hour)}
	if err := state.SaveUsageWatch(dir, rec); err != nil {
		t.Fatal(err)
	}
	v := describeUsageBurn(dir, now)
	if v == nil || v.Words != "At this rate the weekly window is used up in about 9 h, before it resets." || v.UsedPercent != 91 || v.Warned {
		t.Fatalf("%+v", v)
	}
	var b strings.Builder
	printUsageBurn(&b, v)
	if !strings.HasPrefix(b.String(), "usage: At this rate the weekly window is used up in about 9 h") {
		t.Fatal(b.String())
	}
	for name, change := range map[string]func(*state.UsageWatchSetting, *state.UsageWatch) *state.UsageWatchSetting{
		"stale reading": func(w *state.UsageWatchSetting, r *state.UsageWatch) *state.UsageWatchSetting {
			r.ReadAt = now.Add(-2 * time.Hour)
			return w
		},
		"lasts to the reset": func(w *state.UsageWatchSetting, r *state.UsageWatch) *state.UsageWatchSetting {
			r.ExhaustsAt = time.Time{}
			return w
		},
		"window reset": func(w *state.UsageWatchSetting, r *state.UsageWatch) *state.UsageWatchSetting {
			r.ResetsAt = now.Add(-time.Minute)
			return w
		},
		"another account": func(w *state.UsageWatchSetting, _ *state.UsageWatch) *state.UsageWatchSetting {
			w.Account = "example-claude-b"
			return w
		},
		"watch off": func(*state.UsageWatchSetting, *state.UsageWatch) *state.UsageWatchSetting { return nil },
	} {
		ws, rr := *setting, rec
		if err := state.SaveUsageWatchSetting(dir, change(&ws, &rr)); err != nil {
			t.Fatal(err)
		}
		if err := state.SaveUsageWatch(dir, rr); err != nil {
			t.Fatal(err)
		}
		if v := describeUsageBurn(dir, now); v != nil {
			t.Fatalf("%s: %+v", name, v)
		}
	}
}
