package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
)

// Menu and panel list both consume the same offline client naming contract.
// A cached rename must repair legacy frozen automatic names without writing
// records or querying BB, including while controls prevent observation.
func TestOfflineHeldSnapshotsUseLatestCachedBBTitle(t *testing.T) {
	for _, hold := range []string{"paused", "off", "awaiting", "stopped", "completed", "custom", "legacy-inherited"} {
		t.Run(hold, func(t *testing.T) {
			dir, entry := clientFixture(t)
			ctl := control.File{Enabled: true}
			switch hold {
			case "paused":
				ctl.Paused = true
			case "off":
				ctl.Enabled = false
			case "awaiting":
				ctl.AwaitingUser = true
			case "stopped":
				ctl.Stop = true
			case "completed":
				ctl.Completed = true
			}
			if err := control.Save(entry.StateDir, ctl); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if err := state.NoteThreadTitle(entry.StateDir, "thr_example", "Example task (Codex)", nil, now); err != nil {
				t.Fatal(err)
			}
			// Model a cache written by an older build: current thread title, frozen
			// first display name. Reading the current snapshot must repair the view.
			if err := state.SaveDisplayName(entry.StateDir, "Old task", state.DisplayNameTitleTrimmed, now.Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			want := "Example task (Codex)"
			if hold == "custom" || hold == "legacy-inherited" {
				source := state.DisplayNameSet
				if hold == "legacy-inherited" {
					source = state.DisplayNameInherited
				}
				want = "Human task name"
				if err := state.SaveDisplayName(entry.StateDir, want, source, now); err != nil {
					t.Fatal(err)
				}
			}
			files := []string{control.FileName, state.DisplayNameName, state.ThreadTitleName}
			before := map[string]string{}
			for _, file := range files {
				b, err := os.ReadFile(filepath.Join(entry.StateDir, file))
				if err != nil {
					t.Fatal(err)
				}
				before[file] = string(b)
			}
			query := func(_ context.Context, _, _ string, offline bool) daemonStatus {
				if !offline {
					t.Fatal("online query")
				}
				return daemonStatus{State: "skipped"}
			}
			r := readClientStatus(context.Background(), dir, entry.ID, false, true, time.Minute, now, query)
			if r.Snapshot == nil || r.Snapshot.Naming == nil || r.Snapshot.Naming.DisplayName != want || r.Snapshot.Naming.ThreadTitle != "" {
				t.Fatalf("snapshot %+v", r)
			}
			cfg, err := config.Load(entry.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			if n := namingForConfig(cfg); n.DisplayName != want {
				t.Fatalf("display-name command contract %+v", n)
			}
			in := dockInput{Entry: entry, Client: r}
			dock := buildDockReport([]dockInput{in}, now, 15*time.Second, 30*time.Minute, false, true, 0, true)
			panel := buildPanelList(dock)
			if len(dock.Loops) != 1 || dock.Loops[0].Naming == nil || dock.Loops[0].Naming.DisplayName != want {
				t.Fatalf("dock %+v", dock.Loops)
			}
			if len(panel.Loops) != 1 || panel.Loops[0].Naming == nil || panel.Loops[0].Naming.DisplayName != want {
				t.Fatalf("panel %+v", panel.Loops)
			}
			for _, file := range files {
				b, err := os.ReadFile(filepath.Join(entry.StateDir, file))
				if err != nil || string(b) != before[file] {
					t.Fatalf("snapshot changed %s: %v", file, err)
				}
			}
		})
	}
}
