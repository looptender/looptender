//go:build darwin || linux

package main

import (
	"context"
	"encoding/json"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/supervise"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageCollectionProcessBounds(t *testing.T) {
	cli := filepath.Join(t.TempDir(), "fixture")
	write(t, cli, "#!/bin/sh\n[ \"$1 $2 $3 $4\" = 'looptender usage-events thr_example latest' ] || exit 8\nprintf '{}'\nprintf 'private error' >&2\n", 0700)
	out, err := fetchUsagePage(context.Background(), cli, "thr_example", "latest")
	if err != nil || string(out) != "{}" {
		t.Fatal(string(out), err)
	}
	write(t, cli, "#!/bin/sh\nhead -c 40000 /dev/zero\n", 0700)
	if out, err := fetchUsagePage(context.Background(), cli, "thr_example", "latest"); err == nil || len(out) != 0 {
		t.Fatal("output bounds")
	}
	write(t, cli, "#!/bin/sh\nexec sleep 20\n", 0700)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := fetchUsagePage(ctx, cli, "thr_example", "latest"); err == nil || time.Since(start) > 3*time.Second {
		t.Fatal("deadline")
	}
}
func TestUsageCollectionReadNeverQueries(t *testing.T) {
	cfg, now := collectionFixture(t)
	marker := filepath.Join(t.TempDir(), "marker")
	cli := filepath.Join(t.TempDir(), "fixture")
	write(t, cli, "#!/bin/sh\ntouch '"+marker+"'\n", 0700)
	cfg.Loop.BB.CLI = cli
	for i := 0; i < 3; i++ {
		automaticUsageMeasurement(cfg, now)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("read executed source")
	}
}

func TestUsageCollectionAdmission(t *testing.T) {
	cfg, _ := collectionFixture(t)
	cli := filepath.Join(t.TempDir(), "fixture")
	marker := filepath.Join(t.TempDir(), "called")
	write(t, cli, "#!/bin/sh\ntouch '"+marker+"'\nprintf '%s' '{\"version\":1,\"samples\":[]}'\n", 0700)
	cfg.Loop.BB.CLI = cli
	base := supervise.Result{Recorded: true, Target: "primary"}
	for _, tc := range []struct {
		name string
		ctl  control.File
		res  supervise.Result
		dry  bool
	}{
		{"disabled", control.File{}, base, false}, {"paused", control.File{Enabled: true, Paused: true}, base, false}, {"stopped", control.File{Enabled: true, Stop: true}, base, false}, {"held", control.File{Enabled: true, AwaitingUser: true}, base, false}, {"complete", control.File{Enabled: true, Completed: true}, base, false}, {"dry", control.File{Enabled: true}, base, true}, {"unrecorded", control.File{Enabled: true}, supervise.Result{Target: "primary"}, false}, {"fallback", control.File{Enabled: true}, supervise.Result{Recorded: true, Target: "example-fallback"}, false}, {"retargeted", control.File{Enabled: true, BBTarget: "thr_other"}, base, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.ctl)
			os.WriteFile(filepath.Join(cfg.Loop.StateDir, "control.json"), b, 0600)
			collectUsageAfterTick(context.Background(), cfg, tc.res, tc.dry)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("ineligible read")
			}
		})
	}
	b, _ := json.Marshal(control.File{Enabled: true})
	os.WriteFile(filepath.Join(cfg.Loop.StateDir, "control.json"), b, 0600)
	collectUsageAfterTick(context.Background(), cfg, base, false)
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("eligible check did not collect", err)
	}
}
