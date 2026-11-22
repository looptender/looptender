//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/state"
)

// The daemon tells of each worsening once, through runs.jsonl and the error
// report, and records the recovery.
func TestDaemonTellsOfALowVolumeOncePerEpisode(t *testing.T) {
	f, cfg, _, e := configReportFixture(t)
	w := &diskWatch{}
	pass := func(avail uint64) {
		volume(t, avail, 500<<30)
		w.check(e, cfg)
	}
	pass(50 << 30)
	pass(5 << 30) // filling: told
	pass(4 << 30)
	pass(60 << 20) // nearly full: told again
	pass(50 << 20)
	pass(50 << 30) // room again: recorded
	pass(5 << 30)  // a new episode: told
	if got := strings.Count(f.hooks.types(), "keepgoing.error"); got != 3 {
		t.Fatalf("%d reports: %s", got, f.hooks.types())
	}
	b, _ := os.ReadFile(filepath.Join(cfg.Loop.StateDir, state.LogName))
	runs := string(b)
	if strings.Count(runs, `"disk_low"`) != 2 || strings.Count(runs, `"disk_critical"`) != 1 || strings.Count(runs, `"disk_ok"`) != 1 || !strings.Contains(runs, `"loop":"`+cfg.Loop.Name+`"`) {
		t.Fatalf("runs.jsonl: %s", runs)
	}
}
