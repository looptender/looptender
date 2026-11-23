package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
)

// Archiving puts a loop that is not supervising out of the way: it is listed
// apart, counts in no "needs you", gets no resume offer and no group pause or
// stop, and cannot be resumed or enabled until it is unarchived. A daemon
// still running it exits at its next check. Nothing else changes: holds,
// owner, note, task and records are as they were, and unarchiving resumes
// nothing. The record is archive.json beside control.json (see
// control.ArchiveFileName for why it is a file of its own).

// archiveView is what every surface says of an archive record: status JSON
// (archived, or archiveLapsed), client status, the panel and the dock.
type archiveView struct {
	At   time.Time `json:"at"`
	Note string    `json:"note,omitempty"`
	// Hold is the hold the loop had when it was archived; a record that
	// could not be read has none, and Unreadable set.
	Hold       string `json:"hold,omitempty"`
	Unreadable bool   `json:"unreadable,omitempty"`
}

// loopArchive reads the loop's archive record against its control record:
// archived when the record is there and the loop does not supervise, lapsed
// when the loop supervises again. Both nil when there is no record.
func loopArchive(stateDir string, ctl control.File) (archived, lapsed *archiveView) {
	a, err := control.LoadArchive(stateDir)
	if a == nil && err == nil {
		return nil, nil
	}
	v := &archiveView{At: a.At, Note: a.Note, Hold: a.Hold, Unreadable: err != nil}
	if control.Archived(a, ctl) {
		return v, nil
	}
	return nil, v
}

// archivedRefusal refuses enable and resume of an archived loop.
func archivedRefusal(cfg *config.Config, cmd string, f control.File) error {
	a, err := control.LoadArchive(cfg.Loop.StateDir)
	if (a == nil && err == nil) || f.Supervising() {
		return nil
	}
	since := ""
	if !a.At.IsZero() {
		since = " since " + a.At.UTC().Format(time.RFC3339)
	}
	return fmt.Errorf("%s refused, nothing changed: loop %s is archived%s. Unarchive it first with `looptender unarchive -c %s`, which resumes nothing, then %s it: %w",
		cmd, cfg.Loop.Name, since, shellQuote(cfg.Path()), cmd, control.ErrArchived)
}

// archivedNow reports whether the loop is archived now, from its records; an
// unreadable control record is not read as archived.
func archivedNow(stateDir string) (*archiveView, bool) {
	ctl, err := control.Load(stateDir)
	if err != nil {
		return nil, false
	}
	a, _ := loopArchive(stateDir, ctl)
	return a, a != nil
}

type archiveOut struct {
	Loop     string       `json:"loop"`
	Outcome  string       `json:"outcome"` // archived, unarchived, not_archived, refused
	Archived *archiveView `json:"archived,omitempty"`
	Hold     string       `json:"hold,omitempty"`
	Text     string       `json:"text"`
}

func printArchiveOut(e env, out archiveOut, asJSON bool) {
	if asJSON {
		_ = json.NewEncoder(e.stdout).Encode(out)
		return
	}
	fmt.Fprintln(e.stdout, out.Text)
}

func archiveCmd(e env, cfg *config.Config, note string, now time.Time, asJSON bool) int {
	path := shellQuote(cfg.Path())
	a, ctl, err := control.ArchiveLoop(cfg.Loop.StateDir, note, now)
	switch {
	case errors.Is(err, control.ErrArchiveSupervising):
		out := archiveOut{Loop: cfg.Loop.Name, Outcome: "refused", Hold: ctl.Summary(),
			Text: fmt.Sprintf("archive refused, nothing written: loop %s is supervising. Pause or stop it first (looptender pause -c %s), then archive it; archiving changes no hold.", cfg.Loop.Name, path)}
		printArchiveOut(e, out, asJSON)
		return exitFailed
	case err != nil:
		e.errf("archive: %v; nothing written", err)
		return exitFailed
	}
	_ = state.Append(cfg.Loop.StateDir, state.Event{Loop: cfg.Loop.Name, Event: "control", Action: "archive", Reason: ctl.Summary()})
	v := &archiveView{At: a.At, Note: a.Note, Hold: a.Hold}
	text := fmt.Sprintf("Archived loop %s (it is %s). Nothing else changed: its holds, owner, note and records are as they were. It is listed apart, needs nobody, and cannot be resumed until `looptender unarchive -c %s`, which resumes nothing.", cfg.Loop.Name, ctl.Summary(), path)
	if d := inspectDaemon(e.ctx, cfg.Loop.StateDir, version, false); d.State == "responsive" {
		text += " Its daemon exits at its next check."
	}
	printArchiveOut(e, archiveOut{Loop: cfg.Loop.Name, Outcome: "archived", Archived: v, Hold: ctl.Summary(), Text: text}, asJSON)
	return exitOK
}

func unarchiveCmd(e env, cfg *config.Config, asJSON bool) int {
	path := shellQuote(cfg.Path())
	a, err := control.UnarchiveLoop(cfg.Loop.StateDir)
	if err != nil {
		e.errf("unarchive: %v; nothing changed", err)
		return exitFailed
	}
	ctl, _ := control.Load(cfg.Loop.StateDir)
	if a == nil {
		printArchiveOut(e, archiveOut{Loop: cfg.Loop.Name, Outcome: "not_archived", Hold: ctl.Summary(), Text: fmt.Sprintf("Loop %s is not archived; nothing changed.", cfg.Loop.Name)}, asJSON)
		return exitOK
	}
	_ = state.Append(cfg.Loop.StateDir, state.Event{Loop: cfg.Loop.Name, Event: "control", Action: "unarchive", Reason: ctl.Summary()})
	text := fmt.Sprintf("Unarchived loop %s: it is listed normally again. Nothing was resumed: it is still %s.", cfg.Loop.Name, ctl.Summary())
	if !ctl.Supervising() {
		text += fmt.Sprintf(" To run it again, resume it (looptender resume -c %s) and start supervision (looptender start -c %s), since its daemon exited when it was archived.", path, path)
	}
	printArchiveOut(e, archiveOut{Loop: cfg.Loop.Name, Outcome: "unarchived", Archived: &archiveView{At: a.At, Note: a.Note, Hold: a.Hold}, Hold: ctl.Summary(), Text: text}, asJSON)
	return exitOK
}

// archiveSince is "since TIME (note)", or for an unreadable record what is
// known of it.
func archiveSince(a *archiveView) string {
	if a.Unreadable {
		return "unreadable (archive.json could not be read; it counts as archived)"
	}
	s := "since " + a.At.UTC().Format(time.RFC3339)
	if a.Hold != "" {
		s += ", when it was " + a.Hold
	}
	if a.Note != "" {
		s += " (" + a.Note + ")"
	}
	return s
}

// displayNameCmd shows the loop's display name, or sets it to name.
func displayNameCmd(e env, cfg *config.Config, name string, set bool, now time.Time) int {
	if set {
		clean := state.CleanDisplayName(name)
		if clean == "" {
			e.errf("display-name: the name is empty after cleaning; nothing changed")
			return exitUsage
		}
		if err := state.SaveDisplayName(cfg.Loop.StateDir, clean, state.DisplayNameSet, now); err != nil {
			e.errf("display-name: %v; nothing changed", err)
			return exitFailed
		}
		fmt.Fprintf(e.stdout, "Loop %s is shown as %q everywhere from its next read. Its slug, its BB thread and their titles are unchanged.\n", cfg.Loop.Name, clean)
		return exitOK
	}
	n := namingOf(cfg.Loop, "")
	fmt.Fprintf(e.stdout, "%s (%s)\n", n.DisplayName, n.NameSource)
	return exitOK
}

// withoutNote is a copy of a, nil for nil, with no note.
func withoutNote(a *archiveView) *archiveView {
	if a == nil {
		return nil
	}
	c := *a
	c.Note = ""
	return &c
}
