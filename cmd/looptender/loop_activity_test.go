package main

import (
	"context"
	"github.com/looptender/looptender/internal/state"
	"testing"
	"time"
)

func TestLoopActivityUsesSavedWorkNotPolling(t *testing.T) {
	now := time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)
	old := now.Add(-7 * 24 * time.Hour)
	for _, tc := range []struct {
		name string
		st   state.File
		want time.Time
	}{
		{"polling is not activity", state.File{CheckedAt: now, CheckFinishedAt: now, LastWakeAt: now, LastHeldNudgeAt: now, LastProgressAt: now}, time.Time{}},
		{"actual send", state.File{LastSentAt: old, CheckedAt: now}, old},
		{"handoff", state.File{LastSentAt: old, LastHandoffAt: now.Add(-time.Hour)}, now.Add(-time.Hour)},
		{"explicit move", state.File{LastSentAt: old, MovedAt: &now}, now},
		{"repository change", state.File{ProgressChangedAt: old}, old},
		{"initial repository baseline", state.File{ProgressChangedAt: now, ProgressBaseline: true}, time.Time{}},
		{"future evidence does not win", state.File{LastSentAt: old, LastHandoffAt: now.Add(time.Hour), ProgressChangedAt: now.Add(time.Minute)}, old},
		{"unknown", state.File{}, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := loopActivityAt(tc.st, now)
			if tc.want.IsZero() {
				if got != nil {
					t.Fatalf("unexpected activity %v", got)
				}
			} else if got == nil || !got.Equal(tc.want) {
				t.Fatalf("activity %v want %v", got, tc.want)
			}
		})
	}
}

func TestPanelActivitySurvivesStaleOfflineReads(t *testing.T) {
	dir, entry := clientFixture(t)
	now := time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)
	old := now.Add(-7 * 24 * time.Hour)
	if err := state.Save(entry.StateDir, state.File{CheckedAt: now, LastSentAt: old}); err != nil {
		t.Fatal(err)
	}
	for _, readAt := range []time.Time{now, now.Add(time.Hour)} {
		r := readClientStatus(context.Background(), dir, entry.ID, false, true, time.Minute, readAt, inspectDaemon)
		if r.Snapshot == nil || r.Snapshot.ActivityAt == nil || !r.Snapshot.ActivityAt.Equal(old) {
			t.Fatalf("snapshot %+v", r)
		}
		report := buildDockReport([]dockInput{{Entry: entry, Client: r}}, readAt, 15*time.Second, 30*time.Minute, false, true, 0, true)
		list := buildPanelList(report)
		if len(list.Loops) != 1 || list.Loops[0].ActivityAt == nil || !list.Loops[0].ActivityAt.Equal(old) {
			t.Fatalf("panel %+v", list.Loops)
		}
	}
}
