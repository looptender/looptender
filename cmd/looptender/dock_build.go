package main

import (
	"regexp"
	"strconv"
	"strings"
)

// Which looptender build serves the panel, and which build ran each loop's
// last check, from the label each check records. Two labels are the same
// build only when they are equal and say which build they are: "dev", the
// label of a build made without one, says nothing, so two "dev" builds may
// differ, and nor does a "-dirty" label shared by two builds. Two dev builds
// of one release (v0.1.5-dev.26f91db and
// v0.1.5-dev.ab565b1) are different builds. Which one is newer is said only
// when both are plain releases.

// dockBuild is what the records say about the build that ran a loop's last
// check, against this panel's own.
type dockBuild struct {
	// Availability is same, different, unknown (a label says nothing about
	// which build it is) or not_recorded.
	Availability string `json:"availability"`
	Recorded     string `json:"recorded,omitempty"` // the last check's build label
	Panel        string `json:"panel"`              // this panel's build label
	// Order is older or newer when both are plain releases and differ: the
	// last check's build against this panel's.
	Order string `json:"order,omitempty"`
	Text  string `json:"text"`
	// Line is the short form shown under the loop's name: the build its last
	// check ran, and this panel's when they differ ("last check ran v0.2.0-dev.ccd2310
	// · panel v0.2.0"). "version unknown" when the label does not say. It
	// never says which is newer.
	Line string `json:"line"`
}

const dockMaxBuild = 64

var dockRelease = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// dockBuildKnown says whether a label identifies a build.
func dockBuildKnown(label string) bool { return label != "" && label != "dev" }

// dockReleaseOrder compares two plain release labels: -1, 0 or 1, and false
// when either is not a plain release.
func dockReleaseOrder(a, b string) (int, bool) {
	x, y := dockRelease.FindStringSubmatch(a), dockRelease.FindStringSubmatch(b)
	if x == nil || y == nil {
		return 0, false
	}
	for i := 1; i <= 3; i++ {
		p, err1 := strconv.Atoi(x[i])
		q, err2 := strconv.Atoi(y[i])
		if err1 != nil || err2 != nil {
			return 0, false
		}
		if p != q {
			if p < q {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

// dockBuildLabel makes a recorded label safe to show.
func dockBuildLabel(s string) string {
	s = strings.TrimSpace(s)
	if strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "unreadable"
	}
	return clip(s, dockMaxBuild)
}

// dockBuildOf compares the build that ran a loop's last check with the
// panel's. recorded is the label from the loop's last check (state), or else
// from the newest retained check record; "" when none is recorded.
func dockBuildOf(recorded, panel string) dockBuild {
	recorded, panel = dockBuildLabel(recorded), dockBuildLabel(panel)
	b := dockBuild{Recorded: recorded, Panel: panel}
	switch {
	case recorded == "":
		b.Availability, b.Text = "not_recorded", "Which looptender build ran its last check is not recorded."
	case !dockBuildKnown(recorded) && !dockBuildKnown(panel):
		b.Availability, b.Text = "unknown", "Its last check and this panel both ran a looptender build with no version recorded (dev), so whether they are the same build is unknown."
	case !dockBuildKnown(recorded):
		b.Availability, b.Text = "unknown", "Its last check ran a looptender build with no version recorded (dev); this panel is looptender "+panel+". Whether they are the same build is unknown."
	case !dockBuildKnown(panel):
		b.Availability, b.Text = "unknown", "Its last check ran looptender "+recorded+"; this panel is a build with no version recorded (dev). Whether they are the same build is unknown."
	case recorded == panel && strings.HasSuffix(panel, "-dirty"):
		// A dirty label names a commit plus changes that were never committed;
		// two builds can share it and differ.
		b.Availability, b.Text = "unknown", "Its last check and this panel both ran looptender "+panel+", built with uncommitted changes, so whether they are the same build is unknown."
	case recorded == panel:
		b.Availability, b.Text = "same", "Its last check ran this panel's build, looptender "+panel+"."
	default:
		b.Availability = "different"
		if order, ok := dockReleaseOrder(recorded, panel); ok && order != 0 {
			b.Order = map[int]string{-1: "older", 1: "newer"}[order]
			b.Text = "Its last check ran looptender " + recorded + ", an " + b.Order + " release than this panel's " + panel + "."
			if b.Order == "newer" {
				b.Text = "Its last check ran looptender " + recorded + ", a newer release than this panel's " + panel + "."
			}
		} else {
			b.Text = "Its last check ran looptender " + recorded + "; this panel is looptender " + panel + ". They are different builds."
		}
	}
	b.Line = dockBuildLine(recorded, panel)
	return b
}

// dockBuildLine is the short sentence for one loop: the build it runs, and
// this panel's build too when the two are different or cannot be told apart.
func dockBuildLine(recorded, panel string) string {
	r, p := shortBuildPair(recorded, panel)
	switch {
	case r == "":
		return "version unknown"
	case p == "" || (recorded == panel && !strings.HasSuffix(recorded, "-dirty")):
		return "last check ran " + r
	case recorded == panel:
		return "last check ran " + r + " (built with uncommitted changes)"
	}
	return "last check ran " + r + " · panel " + p
}

// dockLastBuild is the build label of the loop's last check: what state
// recorded with it, or, for a check recorded without one, the newest retained
// check record's. "" when neither is recorded.
func dockLastBuild(st *statusOut, tl *timelineReport) string {
	if st != nil && st.State.LastCheck != nil && st.State.LastCheck.Version != "" {
		return st.State.LastCheck.Version
	}
	if tl != nil {
		for i := len(tl.Events) - 1; i >= 0; i-- {
			// The history stores a label it could not keep as "unavailable".
			if r := tl.Events[i].Incident; r != nil && r.Kind == "tick" && r.Build != "" && r.Build != "unavailable" {
				return r.Build
			}
		}
	}
	return ""
}

// dockPanelVersionText is the footer's sentence about the panel's own build.
func dockPanelVersionText(panel string) string {
	panel = dockBuildLabel(panel)
	if !dockBuildKnown(panel) {
		return "This page is served by a looptender build with no version recorded (dev)."
	}
	return "This page is served by looptender " + panel + "."
}

var (
	// buildLabelShape keeps a label that could carry a path, a name or any
	// free text out of status output.
	buildLabelShape = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,63}$`)
	devBuildSuffix  = regexp.MustCompile(`^(.*-dev\.)([0-9a-f]{8,40})(-dirty)?$`)
	bareSHA         = regexp.MustCompile(`^([0-9a-f]{8,40})(-dirty)?$`)
)

// shortBuildLabel is a build label made short to show: a dev build's commit
// is cut to seven characters (v0.2.0-dev.ccd2310, ccd2310). "" when the label
// does not identify a build (empty, "dev", "unavailable" or unreadable). It
// is for display only; builds are compared by their whole labels.
func shortBuildLabel(label string) string { return shortBuildLabelN(label, 7) }

// shortBuildLabelN cuts a commit to n characters.
func shortBuildLabelN(label string, n int) string {
	label = dockBuildLabel(label)
	if !dockBuildKnown(label) || label == "unreadable" || label == "unavailable" || !buildLabelShape.MatchString(label) {
		return ""
	}
	if m := devBuildSuffix.FindStringSubmatch(label); m != nil {
		return m[1] + m[2][:min(n, len(m[2]))] + m[3]
	}
	if m := bareSHA.FindStringSubmatch(label); m != nil {
		return m[1][:min(n, len(m[1]))] + m[2]
	}
	return label
}

// shortBuildPair shortens two labels to show side by side. Builds that
// differ never read the same: when only their commits' first seven characters
// agree, both are lengthened until they differ.
func shortBuildPair(a, b string) (string, string) {
	sa, sb := shortBuildLabel(a), shortBuildLabel(b)
	if sa == "" || sb == "" || sa != sb || dockBuildLabel(a) == dockBuildLabel(b) {
		return sa, sb
	}
	for n := 8; n <= 40; n++ {
		if x, y := shortBuildLabelN(a, n), shortBuildLabelN(b, n); x != y {
			return x, y
		}
	}
	return dockBuildLabel(a), dockBuildLabel(b)
}
