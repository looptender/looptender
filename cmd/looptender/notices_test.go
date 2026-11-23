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
	if n := len(reportNotices(loops, nil, noticeNow)); n != maxNotices {
		t.Errorf("%d", n)
	}
}
