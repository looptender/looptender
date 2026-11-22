//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/state"
)

// The daemon tells of each level once per episode, at most once an hour,
// and free space hovering at a threshold is one episode: leaving a level
// needs a margin above its threshold.
func TestDaemonTellsOfALowVolumeOncePerEpisode(t *testing.T) {
	f, cfg, _, e := configReportFixture(t)
	clock := time.Date(2030, 1, 1, 9, 0, 0, 0, time.UTC)
	w := &diskWatch{now: func() time.Time { return clock }}
	const gib, mib = uint64(1) << 30, uint64(1) << 20
	pass := func(avail uint64) {
		clock = clock.Add(5 * time.Minute)
		volume(t, avail, 20*gib) // a small volume: filling below 1 GiB
		w.check(e, cfg)
	}
	lines := func(reason string) int {
		b, _ := os.ReadFile(filepath.Join(cfg.Loop.StateDir, state.LogName))
		return strings.Count(string(b), `"`+reason+`"`)
	}
	reports := func() int { return strings.Count(f.hooks.types(), "keepgoing.error") }
	pass(5 * gib)
	pass(gib - 10*mib) // filling: told
	if reports() != 1 || lines("disk_low") != 1 {
		t.Fatalf("filling: %d reports, %d disk_low", reports(), lines("disk_low"))
	}
	// Nearly full within the hour of that report: told at once, the hour
	// notwithstanding, and only once however long it hovers at 100 MiB.
	pass(90 * mib)
	if reports() != 2 || lines("disk_critical") != 1 {
		t.Fatalf("nearly full within the hour: %d reports, %d disk_critical", reports(), lines("disk_critical"))
	}
	for i := 0; i < 12; i++ {
		pass(95 * mib)
		pass(105 * mib)
	}
	if reports() != 2 || lines("disk_critical") != 1 || lines("disk_ok") != 0 {
		t.Fatalf("nearly full: %d reports, %d disk_critical, %d disk_ok", reports(), lines("disk_critical"), lines("disk_ok"))
	}
	// Back above 1 GiB but within the margin: still the same episode.
	pass(gib + 100*mib)
	if lines("disk_ok") != 0 {
		t.Fatal("recovered within the margin")
	}
	pass(2 * gib)
	if lines("disk_ok") != 1 {
		t.Fatalf("not recovered above the margin: %d", lines("disk_ok"))
	}
	// A new episode hovering at 1 GiB for hours: told once, never recovered.
	clock = clock.Add(2 * time.Hour)
	for i := 0; i < 20; i++ {
		pass(gib - 10*mib)
		pass(gib + 10*mib)
	}
	if reports() != 3 || lines("disk_low") != 2 || lines("disk_ok") != 1 {
		t.Fatalf("hovering at 1 GiB: %d reports, %d disk_low, %d disk_ok", reports(), lines("disk_low"), lines("disk_ok"))
	}
	b, _ := os.ReadFile(filepath.Join(cfg.Loop.StateDir, state.LogName))
	if !strings.Contains(string(b), `"loop":"`+cfg.Loop.Name+`"`) {
		t.Fatal("runs.jsonl lines do not name the loop")
	}
}

func TestRecoveryMargin(t *testing.T) {
	if diskRecoveryMargin(100<<20) != 256<<20 || diskRecoveryMargin(10<<30) != 1<<30 {
		t.Fatal(diskRecoveryMargin(100<<20), diskRecoveryMargin(10<<30))
	}
}
