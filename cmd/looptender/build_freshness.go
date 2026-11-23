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
	freshCurrent   = "current"   // runs the installed build
	freshUpdated   = "updated"   // switched to it within freshRecent
	freshPending   = "pending"   // follows installs, has not switched yet
	freshDifferent = "different" // runs another build and will not switch; which is newer is not established
	freshOlder     = "older"     // as different, and the installed build is a newer plain release
	freshRefused   = "refused"   // follows installs, but its last switch to the installed build was refused
)

// freshRecent is how long after a daemon switched builds the row keeps saying
// so. Exactly this long after, it has gone.
const freshRecent = 24 * time.Hour

// freshShutdownCommand makes a supervisor exit and nothing else: it keeps
// every hold and control and starts nothing, so it is only ever offered with
// the command that starts supervision again, in the same words. A supervisor
// reporting how it follows installs was built after this command existed (it
// was added before that report was), so it can honour it; one that reports
// nothing may not, and is given words instead. A service is never given it:
// the installed units restart only after a failure (launchd KeepAlive
// SuccessfulExit false, systemd Restart=on-failure), so a clean exit would
// leave it with no supervisor; it gets the service's own restart instead.
const freshShutdownCommand = "looptender daemon-shutdown -c CONFIG"

// freshProcessStart is when this process started. A long-running panel whose
// executable file was written after that has an installed build newer than
// itself, so it cannot tell which supervisors are on it.
var freshProcessStart = time.Now()

// freshPanelBehindText is said in place of a per-loop claim by such a panel.
const freshPanelBehindText = "This panel started before the latest install, so it cannot say which supervisors are on the installed build. Restart it: looptender panel stop, then looptender panel."

// buildFreshness is one loop's supervisor build against the installed build.
// It is the same object in the dock report, panel list, status --json and
// client status --json; readers show Text and Detail as sent.
type buildFreshness struct {
	// State is current, updated, pending, refused, different or older.
	State string `json:"state"`
	// Text is the chip: the version alone when up to date. "Updated just
	// now" reads for the first 45 seconds after a switch, then "Updated 1
	// min ago", "Updated 3 h ago" and so on, worded when the report was
	// built; it is never worded by a reader from SwitchedAt.
	Text string `json:"text"`
	// Detail is the second line, when there is one.
	Detail string `json:"detail,omitempty"`
	// Command is what a person would run, only where it is true for that
	// supervisor's build. Nothing in looptender runs it for them.
	Command string `json:"command,omitempty"`
	// Action says in words what a person does when there is no command that
	// is true for that supervisor.
	Action string `json:"action,omitempty"`
	// Running and Installed are the short build labels compared; each absent
	// when it does not identify a build.
	Running   string `json:"running,omitempty"`
	Installed string `json:"installed,omitempty"`
	// Was, Now and SwitchedAt are the daemon's recorded switch, for state
	// updated only.
	Was        string     `json:"was,omitempty"`
	Now        string     `json:"now,omitempty"`
	SwitchedAt *time.Time `json:"switchedAt,omitempty"`
}

// buildFreshnessSummary is the line at the top of the dock and the menu.
type buildFreshnessSummary struct {
	Text string `json:"text"`
	// OnIt counts supervisors on the installed build; NotOnIt those that are
	// not (pending, refused, different and older). It says nothing about
	// which build is newer.
	OnIt    int `json:"onIt"`
	NotOnIt int `json:"notOnIt"`
}

// freshContext is what the loop's controls say, so words never read as an
// instruction to resume.
type freshContext struct {
	// Held: paused, stopped, awaiting a person, completed or disabled.
	Held bool
	// Retired: replaced or being transferred; nothing is restarted for it.
	Retired bool
	// Service is the service that runs the supervisor, when the loop has one.
	Service *freshService
}

// freshService is a loop's recorded service: enough to name its restart.
type freshService struct {
	Backend string // launchd or systemd
	UID     int
	Label   string
}

// freshServiceOf reads a loop's service record; nil when it has none or it
// cannot be read.
func freshServiceOf(stateDir string) *freshService {
	p, exists, err := readServicePlan(localServiceHost(), stateDir)
	if err != nil || !exists || p.Label == "" {
		return nil
	}
	return &freshService{Backend: p.Backend, UID: p.UID, Label: p.Label}
}

// freshServiceRestart is the service manager's own restart: it stops the
// running supervisor and starts it again from the installed build, whatever
// the exit would have been, and changes no control.
func freshServiceRestart(s *freshService) string {
	if s.Backend == "systemd" {
		return "systemctl --user restart " + s.Label
	}
	return fmt.Sprintf("launchctl kickstart -k gui/%d/%s", s.UID, s.Label)
}

// freshContextOf reads a loop's control flags.
func freshContextOf(enabled, paused, stop, awaiting, completed, retired bool, service *freshService) freshContext {
	return freshContext{Held: !enabled || paused || stop || awaiting || completed, Retired: retired, Service: service}
}

// freshContextOfSnapshot is freshContextOf for a client snapshot; with no
// controls read the loop is taken as held, the cautious wording.
func freshContextOfSnapshot(snap *clientSnapshot, service *freshService) freshContext {
	if snap == nil || snap.Controls == nil {
		return freshContext{Held: true, Retired: snap != nil && snap.ControlState == "superseded", Service: service}
	}
	c := snap.Controls
	return freshContextOf(c.Enabled, c.Paused, c.Stop, c.AwaitingUser, c.Completed, snap.ControlState == "superseded", service)
}

// freshDaemonFollows reports whether an adoption mode from a daemon means it
// will move to a new install; known is false when the daemon said nothing.
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

// freshInstalledIsNewer says whether it is established that the installed
// build is newer than the daemon's: by release order, when both are plain
// releases. A file time proves nothing (a downgrade, a restore or a copy
// writes a new one), so dev and hash builds are never "older".
func freshInstalledIsNewer(label, installed string) bool {
	return dockBuildOf(label, installed).Order == "older"
}

// freshRestartWords is what a person does about a supervisor that will not
// switch by itself, true for how it is run. No sequence is given after which
// the loop is left with no supervisor without saying so in the same sentence,
// and none that enables a held or disabled loop.
func freshRestartWords(d daemonStatus, c freshContext) (command, action string) {
	const held = " The loop stays held; this does not resume it."
	switch {
	case c.Retired:
		return "", "This loop was replaced, so there is nothing to restart for it."
	case c.Service != nil:
		command = freshServiceRestart(c.Service)
		action = "This restarts it under its service, on the installed build, and keeps its holds and controls."
	default:
		again := "looptender start --background -c CONFIG"
		if c.Held {
			// start enables the loop; daemon starts supervision with the controls as they are.
			again = "looptender daemon -c CONFIG in a terminal you leave open (it starts supervision with the loop's controls as they are; start would enable it)"
		}
		if d.Adoption != "" {
			command = freshShutdownCommand
			action = "This ends the supervisor and starts nothing, so until you start it again the loop has no supervisor. Start it again with " + again + "."
		} else {
			// Built before daemons could be asked to exit: that command does not exist for it.
			action = "This supervisor was built before it could be asked to exit, so no command ends it: end its process yourself. Until you start it again with " + again + " the loop has no supervisor."
		}
	}
	if c.Held {
		action += held
	}
	return command, action
}

// freshRefusal is the daemon's last refused switch to the installed build,
// when it is newer than the build it runs and names the installed build.
func freshRefusal(d daemonStatus, record *adoptRecord, installed string) *adoptRecord {
	if record == nil || record.Outcome != adoptRefused || record.At.IsZero() {
		return nil
	}
	started := d.StartedAt
	if d.BuildStartedAt != nil {
		started = d.BuildStartedAt
	}
	if started != nil && record.At.Before(*started) {
		return nil
	}
	if to := dockBuildLabel(record.To.Label); to != "" && to != dockBuildLabel(installed) {
		return nil
	}
	return record
}

// freshBehind says whether the installed build's file is newer than this
// process: it is an earlier build than the installed one.
func freshBehind(installedAt time.Time) bool {
	return !installedAt.IsZero() && installedAt.After(freshProcessStart)
}

// deriveBuildFreshness words one loop's supervisor build. installed is the
// build answering the read and installedAt when its file was last written
// (zero when unknown). record is the daemon's last build change, nil when it
// has none or it could not be read; a missing field never produces a state.
// A reader that started before the installed file was written is an earlier
// build than the installed one and says nothing per loop.
func deriveBuildFreshness(d daemonStatus, record *adoptRecord, installed string, installedAt, now time.Time, c freshContext) *buildFreshness {
	label := dockBuildLabel(d.version)
	if d.State != "responsive" || label == "" || !dockBuildKnown(label) || !dockBuildKnown(dockBuildLabel(installed)) || freshBehind(installedAt) {
		return nil
	}
	short, inst := shortBuildLabel(label), shortBuildLabel(installed)
	if short == "" || inst == "" {
		return nil
	}
	f := &buildFreshness{Running: short, Installed: inst}
	if dockBuildOf(label, installed).Availability == "different" {
		follows, known := freshDaemonFollows(d.Adoption)
		if follows {
			if r := freshRefusal(d, record, installed); r != nil {
				f.State, f.Text = freshRefused, "Not switching to the installed build"
				f.Detail = "Runs " + short + "; installed " + inst + ". Its switch was refused at " + r.At.Local().Format("2 Jan 15:04") + ": " + dockBuildLabel(r.Reason) + "."
				f.Command, f.Action = freshRestartWords(d, c)
				return f
			}
			f.State, f.Text = freshPending, "Update pending · switches at next check"
			f.Detail = "Runs " + short + "; installed " + inst + "."
			return f
		}
		f.Command, f.Action = freshRestartWords(d, c)
		why := "It does not report following installs."
		if known {
			why = "It does not follow installs."
		}
		f.Detail = "Runs " + short + "; installed " + inst + ". " + why
		switch {
		case c.Retired:
			f.State, f.Text = freshDifferent, "Different build from the installed one"
		case freshInstalledIsNewer(label, installed):
			f.State, f.Text = freshOlder, "Older build · restart the supervisor to update"
		default:
			f.State, f.Text = freshDifferent, "Different build from the installed one"
		}
		return f
	}
	if record != nil && record.Outcome == adoptAdopted && !record.At.IsZero() {
		if age := now.Sub(record.At); age >= 0 && age < freshRecent {
			at := record.At
			f.State, f.Text = freshUpdated, "Updated "+dockAgo(now, at)
			f.Was, f.Now, f.SwitchedAt = freshSwitchLabel(record.From), freshSwitchLabel(record.To), &at
			f.Detail = "Installed " + at.Local().Format("2 Jan 15:04") + " · was " + f.Was + " · now " + f.Now
			return f
		}
	}
	f.State, f.Text = freshCurrent, short
	return f
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
// has a state, so nothing is said where nothing is known. A panel that started
// before the latest install says so instead of counting.
func summarizeBuildFreshness(fs []*buildFreshness, installed string, now time.Time) *buildFreshnessSummary {
	if len(fs) > 0 && freshBehind(installedBuildTime()) {
		return &buildFreshnessSummary{Text: freshPanelBehindText}
	}
	s := &buildFreshnessSummary{}
	for _, f := range fs {
		switch {
		case f == nil:
		case f.State == freshCurrent || f.State == freshUpdated:
			s.OnIt++
		default:
			s.NotOnIt++
		}
	}
	short := shortBuildLabel(installed)
	if short == "" || s.OnIt+s.NotOnIt == 0 {
		return nil
	}
	text := "Build " + short
	if t := installedBuildTime(); !t.IsZero() && !t.After(now) {
		text += " installed " + dockAgo(now, t)
	}
	text += " · " + freshCount(s.OnIt, "supervisor") + " on it"
	if s.NotOnIt > 0 {
		text += fmt.Sprintf(" · %d not on it", s.NotOnIt)
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
