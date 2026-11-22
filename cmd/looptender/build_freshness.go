package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// This file is the one place that words how fresh a supervisor's build is.
// The panel page, `panel list --json` (so the macOS menu) and any other
// reader take these words from here; none writes its own.

// Freshness states. An empty state means nothing can be said: no daemon
// answered, or it did not report its build.
const (
	freshCurrent = "current" // runs the installed build
	freshUpdated = "updated" // switched to it within freshRecent
	freshPending = "pending" // follows installs, has not switched yet
	freshOlder   = "older"   // does not follow installs
)

// freshRecent is how long after a daemon switched builds the row keeps saying
// so. Exactly this long after, it has gone.
const freshRecent = 24 * time.Hour

// freshOlderCommand is what a person runs to make an older daemon exit; it
// keeps every control (holds, pause, stop) as they are and starts nothing.
// Supervision is then started again the way it was started.
const freshOlderCommand = "keepgoing daemon-shutdown -c CONFIG"

// buildFreshness is one loop's supervisor build against the installed build.
type buildFreshness struct {
	State string `json:"state"`
	// Text is the chip: the version alone when up to date.
	Text string `json:"text"`
	// Detail is the second line, when there is one.
	Detail string `json:"detail,omitempty"`
	// Command is what a person would run, for an older daemon. Nothing in
	// looptender runs it for them.
	Command string `json:"command,omitempty"`
	// SwitchedAt is when the daemon switched, while it is recent.
	SwitchedAt *time.Time `json:"switchedAt,omitempty"`
}

// buildFreshnessSummary is the line at the top of the dock and the menu.
type buildFreshnessSummary struct {
	Text string `json:"text"`
	// OnIt counts supervisors on the installed build; Older those that are
	// not (pending and older).
	OnIt  int `json:"onIt"`
	Older int `json:"older"`
}

// freshDaemonFollows reports whether an adoption mode from a daemon means it
// will move to a new install: ok is false when it is known not to, and
// known is false when the daemon said nothing.
func freshDaemonFollows(mode string) (follows, known bool) {
	switch {
	case mode == "":
		return false, false
	case mode == "moved_link" || mode == "any_install":
		return true, true
	}
	return false, true
}

func freshSwitchLabel(b adoptBuildID) string {
	if l := shortBuildLabel(b.Label); l != "" {
		return l
	}
	if b.SHA256 != "" {
		return shortDigest(b.SHA256)
	}
	return "unknown build"
}

// deriveBuildFreshness words one loop's supervisor build. installed is the
// build answering the read. record is the daemon's last build change, nil
// when it has none or it could not be read; a missing field never produces a
// state.
func deriveBuildFreshness(d daemonStatus, record *adoptRecord, installed string, now time.Time) *buildFreshness {
	label := dockBuildLabel(d.version)
	if d.State != "responsive" || label == "" || !dockBuildKnown(label) || !dockBuildKnown(dockBuildLabel(installed)) {
		return nil
	}
	short := shortBuildLabel(label)
	if short == "" {
		return nil
	}
	if dockBuildOf(label, installed).Availability == "different" {
		follows, known := freshDaemonFollows(d.Adoption)
		if follows {
			return &buildFreshness{State: freshPending, Text: "Update pending · switches at next check", Detail: "Runs " + short + "; installed " + shortBuildLabel(installed) + "."}
		}
		detail := "Runs " + short + "; installed " + shortBuildLabel(installed) + ". "
		if known {
			detail += "It does not follow installs. "
		} else {
			detail += "It does not report following installs. "
		}
		detail += "A person runs " + freshOlderCommand + ", then starts supervision again; holds and controls are kept."
		return &buildFreshness{State: freshOlder, Text: "Older build · restart the supervisor to update", Detail: detail, Command: freshOlderCommand}
	}
	if record != nil && record.Outcome == adoptAdopted && !record.At.IsZero() {
		if age := now.Sub(record.At); age >= 0 && age < freshRecent {
			at := record.At
			return &buildFreshness{State: freshUpdated, Text: "Updated " + dockAgo(now, at),
				Detail:     "Installed " + at.Local().Format("2 Jan 15:04") + " · was " + freshSwitchLabel(record.From) + " · now " + freshSwitchLabel(record.To),
				SwitchedAt: &at}
		}
	}
	return &buildFreshness{State: freshCurrent, Text: short}
}

// installedBuildTime is when the installed build's file was last written, as
// the answer to "installed <when>". Zero when it cannot be read. A variable
// so tests need no real executable.
var installedBuildTime = func() time.Time {
	p, err := os.Executable()
	if err != nil {
		return time.Time{}
	}
	info, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// summarizeBuildFreshness counts the loops by freshness. nil when no loop
// has a state, so nothing is said where nothing is known.
func summarizeBuildFreshness(fs []*buildFreshness, installed string, now time.Time) *buildFreshnessSummary {
	s := &buildFreshnessSummary{}
	for _, f := range fs {
		switch {
		case f == nil:
		case f.State == freshCurrent || f.State == freshUpdated:
			s.OnIt++
		default:
			s.Older++
		}
	}
	short := shortBuildLabel(installed)
	if short == "" || s.OnIt+s.Older == 0 {
		return nil
	}
	text := "Build " + short
	if t := installedBuildTime(); !t.IsZero() && !t.After(now) {
		text += " installed " + dockAgo(now, t)
	}
	text += " · " + freshCount(s.OnIt, "supervisor") + " on it"
	if s.Older > 0 {
		text += fmt.Sprintf(" · %d older", s.Older)
	}
	s.Text = strings.TrimSpace(text)
	return s
}

func freshCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
