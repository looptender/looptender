package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
)

// Each loop on a filling volume shows it, and the overview has one notice
// for the volume, not one per loop.
func TestPanelShowsALowVolumeOnceAndOnEachLoop(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	low := diskSpaceStatus{Level: "low", AvailableBytes: 18 << 30, TotalBytes: 926 << 30, Message: "the volume that holds the state directory is filling (18.50 GiB free of 926 GiB, below 18.52 GiB): free space on it before records fail to save", volume: "dev:1"}
	in := func(name string, d diskSpaceStatus) dockInput {
		return dockInput{Client: clientStatusReport{Outcome: "available", Snapshot: &clientSnapshot{Loop: name, Controls: &clientControls{Enabled: true}}},
			Status: &statusOut{Loop: name, ControlState: "enabled", DiskSpace: d}}
	}
	other := low
	other.volume, other.AvailableBytes = "dev:2", 5<<30
	r := buildDockReport([]dockInput{
		in("example-a", low), in("example-b", low), in("example-c", low),
		in("example-d", diskSpaceStatus{Level: "ok", volume: "dev:3"}),
		in("example-e", other),
	}, now, time.Minute, 30*time.Minute, false, true, 0, true)
	if len(r.Volumes) != 2 || len(r.Volumes[0].Loops) != 3 || len(r.Volumes[1].Loops) != 1 || r.Volumes[0].Level != "low" || !strings.Contains(r.Volumes[0].Message, "18.50 GiB free") {
		t.Fatalf("%+v", r.Volumes)
	}
	marked := 0
	for _, l := range r.Loops {
		if l.DiskSpace != nil {
			marked++
		}
	}
	if marked != 4 || len(r.Loops) != 5 {
		t.Fatalf("%d of %d loops marked", marked, len(r.Loops))
	}
	list := buildPanelList(r)
	b, _ := json.Marshal(list)
	if strings.Count(string(b), `"diskSpace":"low"`) != 4 || len(list.Volumes) != 2 {
		t.Fatalf("%s", b)
	}
	// Nothing when every volume has room.
	r = buildDockReport([]dockInput{in("example-d", diskSpaceStatus{Level: "ok"})}, now, time.Minute, 30*time.Minute, false, true, 0, true)
	b, _ = json.Marshal(r)
	if strings.Contains(string(b), "volumes") || strings.Contains(string(b), `"diskSpace"`) {
		t.Fatalf("%s", b)
	}
}

// client status --json carries a filling volume for each loop on it, and
// nothing when there is room.
func TestClientStatusCarriesALowVolume(t *testing.T) {
	dir, entry := clientFixture(t)
	if err := control.Save(entry.StateDir, control.File{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	volume(t, 900<<20, 20<<30)
	r := clientRead(t, dir, entry, false)
	if r.Snapshot == nil || r.Snapshot.DiskSpace == nil || r.Snapshot.DiskSpace.Level != "low" || !strings.Contains(r.Snapshot.DiskSpace.Message, "900 MiB free of 20 GiB, below 1.0 GiB") {
		t.Fatalf("%+v", r.Snapshot)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), entry.StateDir) {
		t.Fatal("a path in the client JSON")
	}
	volume(t, 50<<30, 500<<30)
	if r := clientRead(t, dir, entry, false); r.Snapshot == nil || r.Snapshot.DiskSpace != nil {
		t.Fatalf("%+v", r.Snapshot)
	}
}
