package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file is the one list of events a person is told about. The panel page,
// `panel list --json` (so the macOS menu) and anything else that notifies
// read it; none derives its own. It is a pure function of what one read
// found, so it holds no state: each notice is a condition that is true now,
// with an id that stays the same for as long as it is true. A reader makes
// it edge-triggered by remembering ids (the first read is a baseline and says
// nothing; see docs/dock.md).

// Notice kinds, and the order a reader lists them in.
const (
	noticeNeedsYou   = "needs_you"       // a loop starts needing a person
	noticeLimit      = "limit"           // a usage limit was hit
	noticeSupervisor = "supervisor_gone" // no supervisor answers for a loop that should have one
	noticeLane       = "lane_failed"     // a lane failed
	noticeHold       = "hold"            // a hold that is not a request for a person
	noticeInstall    = "install"         // an install was adopted or refused
	noticeDisk       = "disk"            // the state volume is low or critical
	noticeBound      = "bound"           // a deadline or send limit was reached
	noticeModelStep  = "model_step"      // a conversation was stepped down a model, or could not be
)

// noticeKinds is every kind, for settings. Kinds a person is pushed by
// default are in noticeDefaultOn; the rest are listed but quiet.
var noticeKinds = []string{noticeNeedsYou, noticeLimit, noticeSupervisor, noticeLane, noticeHold, noticeInstall, noticeDisk, noticeBound, noticeModelStep}
var noticeDefaultOn = map[string]bool{noticeNeedsYou: true, noticeLimit: true, noticeSupervisor: true}

// Severities: urgent (an agent asked a question), high, normal, info.
const (
	sevUrgent = "urgent"
	sevHigh   = "high"
	sevNormal = "normal"
	sevInfo   = "info"
)

// panelNotice is one event. Text is one fixed sentence: never a note, path,
// thread ID, account name or model; Name is the loop's display name.
type panelNotice struct {
	// ID is stable while the condition lasts: kind, loop and the time it began
	// (or what it is, where it has no time).
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	// Loop is the loop's key (panel --loop accepts it); Name its display name.
	Loop string `json:"loop"`
	Name string `json:"name"`
	// Text is the sentence, with the name in it.
	Text string `json:"text"`
	// Since is when it began, or the read's time for a condition with no time.
	Since time.Time `json:"since"`
	// Group says which notices read as one when several begin together
	// (the same kind and cause); GroupText is their sentence with {n} for
	// the count, for a reader that coalesces them.
	Group     string `json:"group"`
	GroupText string `json:"groupText"`
	// EndText is what to say when the condition ends, for the kinds that
	// say so (a hold lifted); empty otherwise.
	EndText string `json:"endText,omitempty"`
}

func noticeID(kind, key string, since time.Time, what string) string {
	id := kind + "." + key
	if !since.IsZero() {
		id += "." + strconv.FormatInt(since.Unix(), 10)
	}
	if what != "" {
		id += "." + what
	}
	return id
}

func noticeTime(t time.Time) string { return t.Format("Mon 15:04") }

// noticeResetBucket is how coarsely a reset is told apart in an id: the same
// limit read with a reset a few minutes different is one limit.
const noticeResetBucket = 12 * time.Hour

// noticeActionWords say, in short fixed words by the kind of action the usage
// limit record offers, that something can be done; the panel says what.
var noticeActionWords = map[string]string{
	"login_bb_directory": "Another Claude account can take over; see the panel.",
	"arm_takeover":       "Another provider can take over; see the panel.",
}

// noticeName is the name a notice uses: the loop's display name when it has
// one (already masked by the naming rule), else its registered slug, which is
// "A loop" when it is a thread ID in disguise (`bb-thr-…`, `thr_…`) and
// otherwise as it is. Only those shapes: a real name that happens to contain
// "thr-" is kept, so loops stay apart.
func noticeName(l dockLoop) string {
	if l.Naming != nil && l.Naming.DisplayName != "" {
		return l.Naming.DisplayName
	}
	if noticeSlugIsThreadID(l.Name) {
		return "A loop"
	}
	return l.Name
}

var noticeThreadSlug = regexp.MustCompile(`^(bb-thr-|thr_)[0-9A-Za-z_-]+$`)

func noticeSlugIsThreadID(name string) bool { return noticeThreadSlug.MatchString(name) }

// noticeMid is the name inside a sentence: "a loop" for the generic one.
func noticeMid(name string) string {
	if name == "A loop" {
		return "a loop"
	}
	return name
}

// archivedLoop says whether the loop is archived: it is out of the way and
// nothing about it is news, whatever hold or disk reading it still has.
func archivedLoop(l dockLoop) bool { return l.Archived != nil || l.State == "archived" }

// noticesOf is the notices for one loop. needs says it is under Needs you.
func noticesOf(l dockLoop, needs bool, now time.Time) []panelNotice {
	var out []panelNotice
	name := noticeName(l)
	mid := noticeMid(name)
	add := func(n panelNotice) {
		n.Loop, n.Name = l.Key, name
		if n.Since.IsZero() {
			n.Since = now
		}
		out = append(out, n)
	}
	if archivedLoop(l) {
		return nil // an archived loop is out of the way: nothing about it is news
	}
	checked := time.Time{}
	if l.LastCheck.At != nil {
		checked = *l.LastCheck.At
	}
	if v := l.ModelStep; v != nil && v.InForce != nil {
		s := v.InForce
		n := panelNotice{Kind: noticeModelStep, Severity: sevInfo, Since: s.At, ID: noticeID(noticeModelStep, l.Key, s.At, ""), Group: noticeModelStep + ".down",
			Text: name + " runs on a weaker model until a one-model weekly limit resets " + noticeTime(s.ResetsAt) + ".", GroupText: "{n} loops run on a weaker model until a one-model weekly limit resets.",
			EndText: name + " is back on its stronger model, or was moved off the weaker one."}
		if s.Failed {
			n.Severity, n.Group, n.ID = sevNormal, noticeModelStep+".failed", noticeID(noticeModelStep, l.Key, s.At, "failed")
			n.Text, n.GroupText, n.EndText = name+" could not step down a model at a one-model weekly limit; it waits for the reset "+noticeTime(s.ResetsAt)+".", "{n} loops could not step down a model at a one-model weekly limit.", ""
		}
		add(n)
	}
	if w := l.Waiting; w != nil && needs {
		since := time.Time{}
		if w.Since != nil {
			since = *w.Since
		}
		n := panelNotice{Kind: noticeNeedsYou, Severity: sevHigh, Since: since, ID: noticeID(noticeNeedsYou, l.Key, since, w.Kind),
			Group: noticeNeedsYou + "." + w.Kind}
		switch w.Kind {
		case "asked":
			n.Severity, n.Text, n.GroupText = sevUrgent, name+" is asking you a question.", "{n} loops are asking you a question."
		case "checks":
			n.Text, n.GroupText = name+"'s checks have stopped.", "{n} loops' checks have stopped."
		default:
			n.Text, n.GroupText = name+" is held for you.", "{n} loops are held for you."
		}
		add(n)
	}
	if w := l.Waiting; w != nil && w.Kind == "limit" || l.State == "waiting_on_limit" || l.UsageLimit != nil {
		// When it resets. The text says the real minute; the id and the group
		// use a coarse bucket of it, so a reset read a few seconds or minutes
		// differently at the next check is the same limit (readers forget an id
		// that goes away, so a coarse bucket costs nothing).
		var reset time.Time
		window, words := "", "usage limit"
		if u := l.UsageLimit; u != nil {
			window = u.WindowKey
			if window == "" {
				window = u.Window
			}
			if w := strings.TrimSpace(u.Words); w != "" && len(w) <= 60 {
				words = w
			}
			if !u.ResetPassed && !u.ResetsAt.IsZero() {
				reset = u.ResetsAt
			}
		} else if lw := l.LimitWait; lw != nil && lw.ResetsAt != nil && !lw.Passed {
			reset = *lw.ResetsAt
		}
		bucket := time.Time{}
		cause := words
		if !reset.IsZero() {
			bucket = time.Unix(reset.Unix()/int64(noticeResetBucket/time.Second)*int64(noticeResetBucket/time.Second), 0)
			cause += " until " + noticeTime(reset.In(now.Location()))
		}
		text := name + " stopped: " + cause + "."
		moved := l.Away != nil
		group := noticeLimit + "." + window + "." + strconv.FormatInt(bucket.Unix(), 10)
		if moved {
			text = name + " reached a usage limit and moved to its next route."
			cause = "usage limit, moved on"
			group = noticeLimit + ".moved"
		} else if u := l.UsageLimit; u != nil && len(u.Actions) > 0 {
			// What a person can do, in fixed words by kind (the record keeps the kind only).
			if w, ok := noticeActionWords[u.Actions[0].Kind]; ok {
				text += " " + w
			}
		}
		what := window
		if moved {
			what += "moved"
		}
		add(panelNotice{Kind: noticeLimit, Severity: sevHigh, Since: checked, ID: noticeID(noticeLimit, l.Key, bucket, what),
			Text: text, Group: group, GroupText: "{n} loops stopped: " + cause + "."})
	}
	if l.Supervisor == "not_answering" && l.State != "stopped" && l.State != "finished" && l.State != "off" && l.State != "paused" {
		add(panelNotice{Kind: noticeSupervisor, Severity: sevHigh, Since: checked, ID: noticeID(noticeSupervisor, l.Key, time.Time{}, ""),
			Text: "Nothing is supervising " + mid + ".", Group: noticeSupervisor, GroupText: "{n} loops have no supervisor."})
	}
	if l.LanesFailed != "" {
		// Not keyed on the check that found it: that moves at every check.
		add(panelNotice{Kind: noticeLane, Severity: sevNormal, Since: checked, ID: noticeID(noticeLane, l.Key, time.Time{}, ""),
			Text: name + " has a failed lane.", Group: noticeLane, GroupText: "{n} loops have a failed lane."})
	}
	if h := l.Hold; h.State == "held" && !needs && h.Since != nil {
		add(panelNotice{Kind: noticeHold, Severity: sevInfo, Since: *h.Since, ID: noticeID(noticeHold, l.Key, *h.Since, ""),
			Text: name + " was held.", Group: noticeHold, GroupText: "{n} loops were held.", EndText: name + " is no longer held."})
	}
	if f := l.BuildFreshness; f != nil {
		switch {
		case f.State == freshRefused:
			add(panelNotice{Kind: noticeInstall, Severity: sevNormal, ID: noticeID(noticeInstall, l.Key, time.Time{}, "refused"),
				Text: "The installed build was not adopted by " + mid + ".", Group: noticeInstall + ".refused", GroupText: "{n} supervisors did not adopt the installed build."})
		case f.State == freshUpdated && f.SwitchedAt != nil:
			add(panelNotice{Kind: noticeInstall, Severity: sevInfo, Since: *f.SwitchedAt, ID: noticeID(noticeInstall, l.Key, *f.SwitchedAt, "adopted"),
				Text: name + " switched to the installed build.", Group: noticeInstall + ".adopted", GroupText: "{n} supervisors switched to the installed build."})
		}
	}
	if d := l.DiskSpace; d != nil {
		sev := sevNormal
		if d.Level == "critical" {
			sev = sevHigh
		}
		add(panelNotice{Kind: noticeDisk, Severity: sev, ID: noticeID(noticeDisk, l.Key, time.Time{}, d.Level),
			Text: "The disk holding " + mid + "'s state is " + map[bool]string{true: "nearly full", false: "filling"}[d.Level == "critical"] + ".", Group: noticeDisk + "." + d.Level,
			GroupText: "{n} loops are on a disk that is " + map[bool]string{true: "nearly full", false: "filling"}[d.Level == "critical"] + "."})
	}
	for _, lim := range l.Limits {
		if !lim.Reached || (lim.Label != "Deadline" && lim.Label != "Messages sent") {
			continue
		}
		what := map[string]string{"Deadline": "its deadline", "Messages sent": "its send limit"}[lim.Label]
		add(panelNotice{Kind: noticeBound, Severity: sevNormal, ID: noticeID(noticeBound, l.Key, time.Time{}, strings.ToLower(strings.ReplaceAll(lim.Label, " ", "_"))),
			Text: name + " reached " + what + ".", Group: noticeBound + "." + lim.Label, GroupText: "{n} loops reached " + what + "."})
	}
	return out
}

// reportNotices is every loop's notices, most urgent first, then newest.
func reportNotices(loops []dockLoop, needs []dockNeed, now time.Time) []panelNotice {
	n := map[string]bool{}
	for _, x := range needs {
		n[x.Key] = true
	}
	out := []panelNotice{}
	for _, l := range loops {
		out = append(out, noticesOf(l, n[l.Key], now)...)
	}
	rank := map[string]int{sevUrgent: 0, sevHigh: 1, sevNormal: 2, sevInfo: 3}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Severity] != rank[out[j].Severity] {
			return rank[out[i].Severity] < rank[out[j].Severity]
		}
		return out[i].Since.After(out[j].Since)
	})
	if len(out) > maxNotices {
		out = out[:maxNotices]
	}
	return out
}

// maxNotices bounds the list: a registry of a thousand loops all stopped by
// one limit is told as the newest few and their group, and the loops list
// itself says the rest.
const maxNotices = 40

// maxNoticesBytes is what the notices may add to a report, worst case (every
// name four-byte, every notice a long sentence); TestNoticesAreBounded holds
// them to it. The response-size tests keep their three-quarters margin for
// everything else and allow for this.
const maxNoticesBytes = 64 << 10

// noticeGroupSentence is the sentence for n notices of one group.
func noticeGroupSentence(groupText string, n int) string {
	return strings.ReplaceAll(groupText, "{n}", fmt.Sprint(n))
}

// dockResponseMargin is the size the page's reply may reach, not counting
// notices, before it is near its bound: three quarters of it. The notices
// have their own bound (maxNoticesBytes), so other fields cannot grow into it.
const dockResponseMargin = dockMaxResponse * 3 / 4
