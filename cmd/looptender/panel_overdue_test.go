package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// panel list gives the last check's time for a loop whose checks have
// stopped, and only for such a loop, so a client can tell a person once per
// gap. It carries nothing else from the need: no reason, no identifiers.
func TestPanelListGivesTheLastCheckOfAnOverdueLoop(t *testing.T) {
	last := time.Date(2030, 1, 1, 9, 0, 0, 0, time.UTC)
	held := time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC)
	r := dockReport{
		Loops: []dockLoop{{Key: "l0123456789ab", Name: "example-api"}, {Key: "l0123456789cd", Name: "example-docs"}, {Key: "l0123456789ef", Name: "example-site"}},
		NeedsYou: []dockNeed{
			{Kind: "overdue", Key: "l0123456789ab", Reason: "Its checks have stopped: the BB automation that runs this loop (auto_example1) may have disabled itself.", Since: &last},
			{Kind: "held", Key: "l0123456789cd", Since: &held},
		},
	}
	list := buildPanelList(r)
	if l := list.Loops[0]; l.OverdueSince == nil || !l.OverdueSince.Equal(last) || !l.NeedsYou {
		t.Fatalf("overdue loop: %+v", l)
	}
	if list.Loops[1].OverdueSince != nil || list.Loops[2].OverdueSince != nil {
		t.Fatalf("set for a loop that is not overdue: %+v", list.Loops)
	}
	b, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); strings.Count(s, `"overdueSince"`) != 1 || strings.Contains(s, "auto_example1") || strings.Contains(s, "checks have stopped") {
		t.Fatalf("%s", s)
	}
}

// panel list carries each loop's recorded build and the installed one as
// short labels, and says plainly when they differ; an unknown build is absent.
func TestPanelListCarriesBuilds(t *testing.T) {
	old := version
	defer func() { version = old }()
	version = "v0.2.0"
	r := dockReport{Loops: []dockLoop{
		{Key: "l0123456789ab", Name: "a", Build: dockBuildOf("v0.2.0-dev.ccd23105e0aa", version)},
		{Key: "l0123456789cd", Name: "b", Build: dockBuildOf("v0.2.0", version)},
		{Key: "l0123456789ef", Name: "c", Build: dockBuildOf("", version)},
	}}
	list := buildPanelList(r)
	if list.InstalledBuild != "v0.2.0" || list.Loops[0].RecordedBuild != "v0.2.0-dev.ccd2310" || !list.Loops[0].BuildDiffers ||
		list.Loops[1].RecordedBuild != "v0.2.0" || list.Loops[1].BuildDiffers || list.Loops[2].RecordedBuild != "" || list.Loops[2].BuildDiffers {
		t.Fatalf("%+v", list)
	}
	b, _ := json.Marshal(list.Loops[2])
	if strings.Contains(string(b), "uild") {
		t.Fatalf("an unknown build must be absent: %s", b)
	}
	version = "dev"
	if buildPanelList(r).InstalledBuild != "" {
		t.Fatal("a dev build has no version to show")
	}
}
