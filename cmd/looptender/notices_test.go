package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

var noticeNow = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

func tptr(t time.Time) *time.Time { return &t }

func noticeLoop(key, name string) dockLoop {
	return dockLoop{Key: key, Name: name, State: "supervising", Supervisor: "answering", LastCheck: dockCheck{At: tptr(noticeNow.Add(-time.Minute))}}
}

func noticeKindsOf(ns []panelNotice) map[string]panelNotice {
	m := map[string]panelNotice{}
	for _, n := range ns {
		m[n.Kind] = n
	}
	return m
}

func TestNoticesOneForEachKindWithFixedSentences(t *testing.T) {
	asked := noticeLoop("l1", "example-api")
	since := noticeNow.Add(-time.Hour)
	asked.Waiting = &dockWaiting{Kind: "asked", By: "agent", Since: &since}
	limit := noticeLoop("l2", "example-docs")
	limit.State = "waiting_on_limit"
	limit.Waiting = &dockWaiting{Kind: "limit", By: "check"}
	reset := time.Date(2026, 11, 28, 4, 0, 0, 0, time.UTC)
	limit.LimitWait = &dockLimitWait{ResetsAt: &reset}
	gone := noticeLoop("l3", "example-site")
	gone.Supervisor = "not_answering"
	lane := noticeLoop("l4", "example-lanes")
	lane.LanesFailed = "1 lane failed"
	held := noticeLoop("l5", "example-held")
	held.Hold = dockHoldState{State: "held", Since: tptr(since)}
	inst := noticeLoop("l6", "example-install")
	inst.BuildFreshness = &buildFreshness{State: freshRefused}
	disk := noticeLoop("l7", "example-disk")
	disk.DiskSpace = &dockDiskSpace{Level: "critical", Message: "free text with /private/path"}
	bound := noticeLoop("l8", "example-bound")
	bound.Limits = []dockLimit{{Label: "Deadline", Reached: true}}
	loops := []dockLoop{asked, limit, gone, lane, held, inst, disk, bound}
	ns := reportNotices(loops, []dockNeed{{Key: "l1"}}, noticeNow)
	m := noticeKindsOf(ns)
	want := map[string]string{
		noticeNeedsYou:   "example-api is asking you a question.",
		noticeLimit:      "example-docs stopped: usage limit until Sat 04:00.",
		noticeSupervisor: "Nothing is supervising example-site.",
		noticeLane:       "example-lanes has a failed lane.",
		noticeHold:       "example-held was held.",
		noticeInstall:    "The installed build was not adopted by example-install.",
		noticeDisk:       "The disk holding example-disk's state is nearly full.",
		noticeBound:      "example-bound reached its deadline.",
	}
	for k, text := range want {
		if m[k].Text != text {
			t.Errorf("%s: %q, want %q", k, m[k].Text, text)
		}
	}
	if m[noticeNeedsYou].Severity != sevUrgent || ns[0].Kind != noticeNeedsYou {
		t.Errorf("a question is first and urgent: %+v", ns[0])
	}
	if m[noticeHold].EndText != "example-held is no longer held." {
		t.Errorf("end text: %q", m[noticeHold].EndText)
	}
	b, _ := json.Marshal(ns)
	for _, bad := range []string{"/private", "free text", "thr_"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("notice carries %q: %s", bad, b)
		}
	}
	for _, n := range ns {
		if n.ID == "" || n.Group == "" || !strings.Contains(n.GroupText, "{n}") || n.Loop == "" || n.Name == "" {
			t.Errorf("incomplete: %+v", n)
		}
	}
}

func TestNoticeIDsAreStableAcrossReadsAndNewForANewCondition(t *testing.T) {
	l := noticeLoop("l1", "example-api")
	since := noticeNow.Add(-time.Hour)
	l.Waiting = &dockWaiting{Kind: "asked", Since: &since}
	a := reportNotices([]dockLoop{l}, []dockNeed{{Key: "l1"}}, noticeNow)
	b := reportNotices([]dockLoop{l}, []dockNeed{{Key: "l1"}}, noticeNow.Add(5*time.Minute))
	if len(a) != 1 || a[0].ID != b[0].ID {
		t.Fatalf("id changed between reads: %v %v", a, b)
	}
	later := noticeNow.Add(-time.Minute)
	l.Waiting = &dockWaiting{Kind: "asked", Since: &later}
	c := reportNotices([]dockLoop{l}, []dockNeed{{Key: "l1"}}, noticeNow)
	if c[0].ID == a[0].ID {
		t.Error("a new question is a new notice")
	}
}

func TestNoticesQuietLoopsAndEndedConditionsSayNothing(t *testing.T) {
	l := noticeLoop("l1", "example-api")
	if ns := reportNotices([]dockLoop{l}, nil, noticeNow); len(ns) != 0 {
		t.Errorf("quiet loop: %v", ns)
	}
	l.Supervisor = "not_answering"
	for _, s := range []string{"stopped", "finished", "off", "paused"} {
		l.State = s
		if ns := reportNotices([]dockLoop{l}, nil, noticeNow); len(ns) != 0 {
			t.Errorf("%s with no supervisor is not a surprise: %v", s, ns)
		}
	}
	// A hold that is a request for a person is one notice (needs you), not two.
	h := noticeLoop("l2", "example-held")
	since := noticeNow.Add(-time.Hour)
	h.Hold = dockHoldState{State: "held", Since: &since}
	h.Waiting = &dockWaiting{Kind: "held", Since: &since}
	if ns := reportNotices([]dockLoop{h}, []dockNeed{{Key: "l2"}}, noticeNow); len(ns) != 1 || ns[0].Kind != noticeNeedsYou {
		t.Errorf("%v", ns)
	}
}

func TestNoticesGroupManyLoopsStoppedByOneLimit(t *testing.T) {
	reset := time.Date(2026, 11, 28, 4, 0, 0, 0, time.UTC)
	var loops []dockLoop
	for _, k := range []string{"l1", "l2", "l3", "l4", "l5"} {
		l := noticeLoop(k, "example-"+k)
		l.State = "waiting_on_limit"
		l.LimitWait = &dockLimitWait{ResetsAt: &reset}
		loops = append(loops, l)
	}
	ns := reportNotices(loops, nil, noticeNow)
	if len(ns) != 5 {
		t.Fatalf("%d", len(ns))
	}
	for _, n := range ns {
		if n.Group != ns[0].Group {
			t.Errorf("one cause, one group: %q %q", n.Group, ns[0].Group)
		}
	}
	if got := noticeGroupSentence(ns[0].GroupText, 5); got != "5 loops stopped: usage limit until Sat 04:00." {
		t.Errorf("%q", got)
	}
	// A loop that moved on to its next route is not stopped, and is not grouped with them.
	moved := loops[0]
	moved.Away = &dockAway{}
	if g := reportNotices([]dockLoop{moved}, nil, noticeNow)[0].Group; g == ns[0].Group {
		t.Error("moved on is another cause")
	}
}

func TestPanelListCarriesNoticesAlwaysAsAList(t *testing.T) {
	b, _ := json.Marshal(buildPanelList(dockReport{ReadAt: noticeNow}))
	if !strings.Contains(string(b), `"notices":[]`) {
		t.Errorf("%s", b)
	}
}

func TestNoticesAreBounded(t *testing.T) {
	var loops []dockLoop
	for i := 0; i < 500; i++ {
		l := noticeLoop(fmt.Sprintf("l%d", i), fmt.Sprintf("example-%d", i))
		l.Supervisor = "not_answering"
		l.LanesFailed = "1 lane failed"
		loops = append(loops, l)
	}
	ns := reportNotices(loops, nil, noticeNow)
	if len(ns) != maxNotices {
		t.Errorf("%d", len(ns))
	}
	// Worst case: four-byte names, which JSON escapes at twelve bytes a rune.
	for i := range loops {
		loops[i].Name = strings.Repeat("\U0001F600", 28)
	}
	b, _ := json.Marshal(reportNotices(loops, nil, noticeNow))
	if len(b) > maxNoticesBytes {
		t.Errorf("%d bytes", len(b))
	}
}

// sizeWithoutNotices is a reply's size less its notices, which are held to
// their own bound here; the rest is what the response-size tests compare with
// dockResponseMargin.
func sizeWithoutNotices(t *testing.T, body []byte) int {
	t.Helper()
	var v struct {
		Notices json.RawMessage `json:"notices"`
	}
	_ = json.Unmarshal(body, &v)
	if len(v.Notices) > maxNoticesBytes {
		t.Errorf("notices are %d bytes, over their %d", len(v.Notices), maxNoticesBytes)
	}
	return len(body) - len(v.Notices)
}

// allKindsLoops is one loop for each kind of notice, built with the given
// last-check time, so a test can move the check without moving the condition.
func allKindsLoops(check time.Time) []dockLoop {
	since := noticeNow.Add(-3 * time.Hour)
	reset := time.Date(2026, 11, 28, 4, 0, 0, 0, time.UTC)
	mk := func(key string) dockLoop {
		l := noticeLoop(key, "example-"+key)
		l.LastCheck.At = &check
		return l
	}
	asked := mk("l1")
	asked.Waiting = &dockWaiting{Kind: "asked", Since: &since}
	limit := mk("l2")
	limit.State = "waiting_on_limit"
	limit.LimitWait = &dockLimitWait{ResetsAt: &reset}
	gone := mk("l3")
	gone.Supervisor = "not_answering"
	lane := mk("l4")
	lane.LanesFailed = "1 lane failed"
	held := mk("l5")
	held.Hold = dockHoldState{State: "held", Since: &since}
	refused := mk("l6")
	refused.BuildFreshness = &buildFreshness{State: freshRefused}
	adopted := mk("l7")
	adopted.BuildFreshness = &buildFreshness{State: freshUpdated, SwitchedAt: &since}
	disk := mk("l8")
	disk.DiskSpace = &dockDiskSpace{Level: "low"}
	bound := mk("l9")
	bound.Limits = []dockLimit{{Label: "Messages sent", Reached: true}}
	return []dockLoop{asked, limit, gone, lane, held, refused, adopted, disk, bound}
}

func noticeIDSet(loops []dockLoop, now time.Time) map[string]string {
	out := map[string]string{}
	for _, n := range reportNotices(loops, []dockNeed{{Key: "l1"}}, now) {
		out[n.Kind+"/"+n.Loop] = n.ID
	}
	return out
}

func TestEveryKindHasAnIDThatHoldsAcrossChecks(t *testing.T) {
	first := noticeIDSet(allKindsLoops(noticeNow.Add(-20*time.Minute)), noticeNow)
	if len(first) < 9 {
		t.Fatalf("every kind should be present: %v", first)
	}
	// Checks keep running while a condition lasts: ten minutes, an hour, a day later.
	for _, later := range []time.Duration{10 * time.Minute, time.Hour, 24 * time.Hour} {
		again := noticeIDSet(allKindsLoops(noticeNow.Add(-20*time.Minute+later)), noticeNow.Add(later))
		for k, id := range first {
			if again[k] != id {
				t.Errorf("%s: id moved with the check, %q then %q (after %v)", k, id, again[k], later)
			}
		}
	}
}

func TestAConditionThatEndsAndComesBackReadsAsTheSameIDForAReaderThatForgets(t *testing.T) {
	// A reader diffs each read against the last one, not against every id it
	// has ever seen (docs/dock.md): so an id that is gone and back is new to
	// it. That needs the id to be the same for the same condition.
	a := noticeIDSet(allKindsLoops(noticeNow), noticeNow)
	gone := reportNotices(nil, nil, noticeNow.Add(time.Hour))
	if len(gone) != 0 {
		t.Fatal("nothing when no loop has a condition")
	}
	b := noticeIDSet(allKindsLoops(noticeNow.Add(2*time.Hour)), noticeNow.Add(2*time.Hour))
	for k, id := range a {
		if b[k] != id {
			t.Errorf("%s: the same condition has two ids: %q %q", k, id, b[k])
		}
	}
}

func TestANewConditionOfAKindWithATimeIsANewID(t *testing.T) {
	a := noticeIDSet(allKindsLoops(noticeNow), noticeNow)
	loops := allKindsLoops(noticeNow)
	newer := noticeNow.Add(-time.Minute)
	reset := time.Date(2026, 12, 5, 4, 0, 0, 0, time.UTC)
	loops[0].Waiting = &dockWaiting{Kind: "asked", Since: &newer}
	loops[1].LimitWait = &dockLimitWait{ResetsAt: &reset}
	loops[4].Hold.Since = &newer
	loops[6].BuildFreshness = &buildFreshness{State: freshUpdated, SwitchedAt: &newer}
	b := noticeIDSet(loops, noticeNow)
	for _, k := range []string{"needs_you/l1", "limit/l2", "hold/l5", "install/l7"} {
		if a[k] == "" || a[k] == b[k] {
			t.Errorf("%s: a new condition kept its id: %q %q", k, a[k], b[k])
		}
	}
}

func TestNoticesNeverCarryAThreadIDInDisguise(t *testing.T) {
	l := noticeLoop("l1", "bb-thr-ab12cd34ef")
	l.Supervisor = "not_answering"
	l.DiskSpace = &dockDiskSpace{Level: "low"}
	l.LanesFailed = "1 lane failed"
	ns := reportNotices([]dockLoop{l}, nil, noticeNow)
	if len(ns) != 3 {
		t.Fatalf("%v", ns)
	}
	b, _ := json.Marshal(ns)
	for _, bad := range []string{"bb-thr-", "thr_", "ab12cd34ef"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("carries %q: %s", bad, b)
		}
	}
	got := map[string]string{}
	for _, n := range ns {
		got[n.Kind] = n.Text
	}
	if got[noticeLane] != "A loop has a failed lane." || got[noticeSupervisor] != "Nothing is supervising a loop." || got[noticeDisk] != "The disk holding a loop's state is filling." {
		t.Errorf("%v", got)
	}
}

func TestAnArchivedLoopHasNoNotices(t *testing.T) {
	l := noticeLoop("l1", "example-api")
	since := noticeNow.Add(-time.Hour)
	l.Hold = dockHoldState{State: "held", Since: &since}
	l.DiskSpace = &dockDiskSpace{Level: "low"}
	l.Supervisor = "not_answering"
	if len(reportNotices([]dockLoop{l}, nil, noticeNow)) == 0 {
		t.Fatal("the loop has conditions before it is archived")
	}
	l.Archived = &archiveView{}
	l.State = "archived"
	if ns := reportNotices([]dockLoop{l}, nil, noticeNow); len(ns) != 0 {
		t.Errorf("an archived loop is out of the way: %v", ns)
	}
}

func TestNoticeNameUsesTheDisplayNameAndOnlyDisguisesRealThreadSlugs(t *testing.T) {
	l := noticeLoop("l1", "bb-thr-ab12cd34ef")
	l.Naming = &loopNaming{DisplayName: "Billing cleanup"}
	if got := noticeName(l); got != "Billing cleanup" {
		t.Errorf("a display name is used: %q", got)
	}
	l.Naming = nil
	for _, slug := range []string{"bb-thr-ab12cd34ef", "thr_example123"} {
		l.Name = slug
		if got := noticeName(l); got != "A loop" {
			t.Errorf("%s: %q", slug, got)
		}
	}
	// Real names that merely contain the letters are kept, so loops stay apart.
	for _, name := range []string{"example-thr-api", "my-thr-loop", "throttle-docs", "example-api"} {
		l.Name = name
		if got := noticeName(l); got != name {
			t.Errorf("%s was turned into %q", name, got)
		}
	}
}

func TestTheLimitIDHoldsForTheSameWindowAndChangesWithANewOne(t *testing.T) {
	mk := func(reset time.Time, key string) dockLoop {
		l := noticeLoop("l1", "example-api")
		l.State = "waiting_on_limit"
		l.UsageLimit = &usageLimitStatus{Window: "weekly", WindowKey: key, Words: "weekly usage limit", ResetsAt: reset}
		return l
	}
	base := time.Date(2026, 11, 28, 4, 0, 10, 0, time.UTC)
	id := func(l dockLoop) string { return reportNotices([]dockLoop{l}, nil, noticeNow)[0].ID }
	group := func(l dockLoop) string { return reportNotices([]dockLoop{l}, nil, noticeNow)[0].Group }
	a := mk(base, "seven_day")
	if id(mk(base.Add(30*time.Second), "seven_day")) != id(a) || group(mk(base.Add(30*time.Second), "seven_day")) != group(a) {
		t.Error("a reset read 30 seconds later is the same limit, and the same group")
	}
	// Across a minute boundary the group does not split either: it is told by the hour.
	edge := time.Date(2026, 11, 28, 4, 30, 55, 0, time.UTC)
	if group(mk(edge, "seven_day")) != group(mk(edge.Add(10*time.Second), "seven_day")) {
		t.Error("the group split across a minute")
	}
	if id(mk(base, "seven_day_opus")) == id(a) {
		t.Error("a new window is a new limit")
	}
	if id(mk(base.Add(time.Hour), "seven_day")) == id(a) || id(mk(base.Add(7*24*time.Hour), "seven_day")) == id(a) {
		t.Error("an hour or a week later is a new limit")
	}
	if got := reportNotices([]dockLoop{a}, nil, noticeNow)[0].Text; got != "example-api stopped: weekly usage limit until Sat 04:00." {
		t.Errorf("%q", got)
	}
	// The first action's own words are carried; its command is not.
	a.UsageLimit.Actions = []usageAction{{Kind: "login_bb_directory", Text: "Another configured account has quota.", Command: "claude auth login --secret"}}
	n := reportNotices([]dockLoop{a}, nil, noticeNow)[0]
	if !strings.HasSuffix(n.Text, "Another configured account has quota.") || strings.Contains(n.Text, "secret") {
		t.Errorf("%q", n.Text)
	}
	// Without the new record, the older limit-wait data still works, to the hour.
	old := noticeLoop("l2", "example-docs")
	old.State = "waiting_on_limit"
	old.LimitWait = &dockLimitWait{ResetsAt: &base}
	o2 := old
	later := base.Add(40 * time.Second)
	o2.LimitWait = &dockLimitWait{ResetsAt: &later}
	if id(old) != id(o2) {
		t.Error("the fallback is stable too")
	}
}
