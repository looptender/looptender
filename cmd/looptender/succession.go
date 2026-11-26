package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/registry"
	"github.com/looptender/looptender/internal/state"
)

// A line of work moves to a new loop by a succession: the successor records
// the loop it continues (continued-from.json), takes its display name at
// once, and its first completed check after that archives the predecessor,
// if the predecessor is held then, with a note naming the successor. Until
// that check, and while the predecessor still supervises, nothing is
// archived, so a successor that never confirms leaves both loops as they
// are. A succession is declared (archive -c OLD --succeeded-by NEW,
// init --succeeds OLD) or follows transfer supersede, which names the
// successor's thread; it is never guessed. looptender archives only its own
// loops, never a BB thread.

// lineageMax bounds how many predecessors are followed.
const lineageMax = 16

// successionPendingMax is how long a declared succession waits for the
// successor's first completed check before it is cancelled.
const successionPendingMax = 14 * 24 * time.Hour

var (
	errSuccessionOwner     = errors.New("the predecessor's owner changed since the succession was declared")
	errSuccessionAuthority = errors.New("the succession was not declared by the predecessor's owner or a person")
	errSuccessionChanged   = errors.New("the predecessor's control record changed after the succession was declared: it was resumed or held again")
	errSuccessionUnmarked  = errors.New("the predecessor carries no matching mark of this succession (succeeded-by.json), so the record was not made by a declaration")
)

// lineageStep is one earlier loop of a line of work, oldest first.
type lineageStep struct {
	Loop       string     `json:"loop"`
	Thread     string     `json:"thread,omitempty"`
	Key        string     `json:"key,omitempty"` // the page's loop key, when it is registered
	ArchivedAt *time.Time `json:"archivedAt,omitempty"`
}

// continuedFromView is the successor's record as status shows it.
type continuedFromView struct {
	lineageStep
	Source      string     `json:"source"`
	DeclaredAt  time.Time  `json:"declaredAt"`
	Waiting     string     `json:"waiting,omitempty"`
	CancelledAt *time.Time `json:"cancelledAt,omitempty"`
	Cancelled   string     `json:"cancelled,omitempty"`
}

// continuedRef is the loop that continues an archived one.
type continuedRef struct {
	Loop   string     `json:"loop,omitempty"`
	Thread string     `json:"thread,omitempty"`
	Key    string     `json:"key,omitempty"`
	At     *time.Time `json:"at,omitempty"`
}

// listLineage is what a list carries of a line of work: how many earlier
// loops it has and the key of the one it continues. The whole chain is in
// the loop's own status.
type listLineage struct {
	Count int    `json:"count"`
	From  string `json:"from,omitempty"`
}

func refKey(registryID string) string {
	if registryID == "" {
		return ""
	}
	return dockKey(registryID)
}

// registryIDOf is the registration of the loop in stateDir, or "".
func registryIDOf(stateDir string) string {
	dir, err := registryDirectory("")
	if err != nil {
		return ""
	}
	f, err := registry.Load(dir)
	if err != nil {
		return ""
	}
	for _, e := range f.Entries {
		if samePath(e.StateDir, stateDir) {
			return e.ID
		}
	}
	return ""
}

// samePath compares two paths with links resolved where they can be.
func samePath(a, b string) bool {
	canon := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return canon(a) == canon(b)
}

func bbThreadOf(c *config.Config) string {
	if c.Loop.BB != nil {
		return c.Loop.BB.Thread
	}
	return ""
}

// inheritedName is the name a successor takes: the predecessor's stored
// display name, else a name from its last recorded thread title, else its
// slug, so the line of work keeps one name.
func inheritedName(pred *config.Config) (string, string) {
	if dn, err := state.LoadDisplayName(pred.Loop.StateDir); err == nil && dn != nil {
		if state.AutoDisplayNameSource(dn.Source) {
			name, _ := state.DisplayNameFromTitle(namingForConfig(pred).DisplayName, pred.RouteLabels())
			return name, state.DisplayNameInheritedTitle
		}
		return dn.Name, state.DisplayNameInherited
	}
	if n := namingForConfig(pred); n.NameSource == state.DisplayNameThreadTitle {
		name, _ := state.DisplayNameFromTitle(n.DisplayName, pred.RouteLabels())
		return name, state.DisplayNameInheritedTitle
	}
	return pred.Loop.Name, state.DisplayNameInheritedTitle
}

// declareSuccession records on succ that it continues pred. The caller must
// own pred or be a person outside BB. succ takes pred's display name unless a
// person has set one for it.
func declareSuccession(pred, succ *config.Config, source string, now time.Time) (control.Succession, error) {
	if samePath(pred.Loop.StateDir, succ.Loop.StateDir) {
		return control.Succession{}, errors.New("a loop cannot succeed itself")
	}
	caller := os.Getenv("BB_THREAD_ID")
	ctl, err := control.Load(pred.Loop.StateDir)
	if err != nil {
		return control.Succession{}, fmt.Errorf("the predecessor's control record could not be read: %w", err)
	}
	if err := control.CheckOwner(ctl, caller, false); err != nil {
		return control.Succession{}, fmt.Errorf("BB_THREAD_ID identifies a different thread from the predecessor's owner (%w); a person can declare it from an ordinary terminal outside BB", err)
	}
	if old, err := control.LoadSuccession(succ.Loop.StateDir); err != nil {
		return control.Succession{}, err
	} else if old != nil && old.ArchivedAt == nil && old.CancelledAt == nil && !samePath(old.From.StateDir, pred.Loop.StateDir) {
		return control.Succession{}, fmt.Errorf("loop %s already continues loop %s, whose archive is still to come", succ.Loop.Name, old.From.Loop)
	}
	// No cycle: the new loop is not already among the old loop's own
	// predecessors.
	for _, dir := range lineageDirs(pred.Loop.StateDir) {
		if samePath(dir, succ.Loop.StateDir) {
			return control.Succession{}, fmt.Errorf("loop %s is already an earlier loop of %s's line of work", succ.Loop.Name, pred.Loop.Name)
		}
	}
	// One successor per loop.
	if a, _ := control.LoadArchive(pred.Loop.StateDir); a != nil && a.ContinuedIn != nil && !samePath(a.ContinuedIn.StateDir, succ.Loop.StateDir) {
		return control.Succession{}, fmt.Errorf("loop %s is already continued in loop %s", pred.Loop.Name, a.ContinuedIn.Loop)
	}
	by := "person"
	if caller != "" {
		by = caller
	}
	s := control.Succession{From: control.LoopRef{Loop: pred.Loop.Name, Config: pred.Path(), StateDir: pred.Loop.StateDir, Thread: bbThreadOf(pred),
		RegistryID: registryIDOf(pred.Loop.StateDir), At: now.UTC()}, Source: source, By: by, DeclaredAt: now.UTC(),
		FromHeld: !ctl.Supervising(), FromControlAt: ctl.UpdatedAt}
	if o := ctl.Ownership; o != nil {
		s.FromOwner, s.FromGeneration = o.ThreadID, o.Generation
	}
	// The predecessor's own mark first: without it no record archives it.
	if err := control.SaveSucceededBy(pred.Loop.StateDir, control.SucceededBy{StateDir: succ.Loop.StateDir, Loop: succ.Loop.Name, DeclaredAt: s.DeclaredAt}); err != nil {
		return s, fmt.Errorf("the predecessor's mark could not be written: %w", err)
	}
	if err := control.SaveSuccession(succ.Loop.StateDir, s); err != nil {
		return s, err
	}
	// Written now, before any check, so the successor's first title read
	// cannot name it first; a name a person set stays.
	if dn, err := state.LoadDisplayName(succ.Loop.StateDir); err == nil && (dn == nil || dn.Source != state.DisplayNameSet) {
		name, source := inheritedName(pred)
		_ = state.SaveDisplayName(succ.Loop.StateDir, name, source, now)
	}
	return s, nil
}

// completeSuccession archives the predecessor after the successor's first
// completed check since the succession was recorded. Best effort: a failure
// is recorded as why it waits, and tried again at the next check.
func completeSuccession(e env, cfg *config.Config, now time.Time) {
	s, err := control.LoadSuccession(cfg.Loop.StateDir)
	if err != nil || s == nil || s.ArchivedAt != nil || s.CancelledAt != nil {
		return
	}
	cancel := func(why string) {
		at := now.UTC()
		s.CancelledAt, s.Cancelled, s.Waiting = &at, why+"; nothing was archived, and a new declaration is needed to archive it", ""
		_ = state.Append(cfg.Loop.StateDir, state.Event{Loop: cfg.Loop.Name, Event: "control", Action: "succession_cancelled", Reason: s.From.Loop})
		if err := control.SaveSuccession(cfg.Loop.StateDir, *s); err != nil {
			e.errf("warning: the succession record could not be updated: %v", err)
		}
	}
	if now.Sub(s.DeclaredAt) > successionPendingMax {
		cancel(fmt.Sprintf("no check of this loop archived it within %s of the declaration", state.ShortDuration(successionPendingMax)))
		return
	}
	st, err := state.Load(cfg.Loop.StateDir)
	if err != nil || st.LastCheck == nil || st.LastCheck.Outcome != "succeeded" || !st.LastCheck.At.After(s.DeclaredAt) {
		return
	}
	me := &control.LoopRef{Loop: cfg.Loop.Name, Config: cfg.Path(), StateDir: cfg.Loop.StateDir, Thread: bbThreadOf(cfg), RegistryID: registryIDOf(cfg.Loop.StateDir), At: now.UTC()}
	note := fmt.Sprintf("continued in loop %s", cfg.Loop.Name)
	if me.Thread != "" {
		note += " (" + me.Thread + ")"
	}
	note += ", whose first check completed " + st.LastCheck.At.UTC().Format(time.RFC3339)
	// Judged under the predecessor's control lock, on its record as it is
	// now: the authority the declaration had must still hold.
	check := func(f control.File) error {
		owner, generation := "", uint64(0)
		if o := f.Ownership; o != nil {
			owner, generation = o.ThreadID, o.Generation
		}
		m := control.LoadSucceededBy(s.From.StateDir)
		switch {
		case m == nil || !samePath(m.StateDir, cfg.Loop.StateDir) || !m.DeclaredAt.Equal(s.DeclaredAt):
			return errSuccessionUnmarked
		case owner != s.FromOwner || generation != s.FromGeneration:
			return errSuccessionOwner
		case s.By != "person" && s.By != s.FromOwner:
			return errSuccessionAuthority
		case s.FromHeld && !f.UpdatedAt.Equal(s.FromControlAt):
			return errSuccessionChanged
		}
		return nil
	}
	_, _, err = control.ArchiveLoopFor(s.From.StateDir, note, now, check, me)
	switch {
	case errors.Is(err, control.ErrArchiveSupervising):
		s.Waiting = "the predecessor is still supervising; pause or stop it, and the next completed check archives it"
	case errors.Is(err, errSuccessionUnmarked), errors.Is(err, errSuccessionOwner), errors.Is(err, errSuccessionAuthority), errors.Is(err, errSuccessionChanged), errors.Is(err, control.ErrContinuedElsewhere):
		cancel(err.Error())
		return
	case err != nil:
		s.Waiting = "the predecessor could not be archived: " + err.Error()
	default:
		at := now.UTC()
		s.ArchivedAt, s.Waiting = &at, ""
		_ = state.Append(s.From.StateDir, state.Event{Loop: s.From.Loop, Event: "control", Action: "archive", Reason: "continued in " + cfg.Loop.Name})
		_ = state.Append(cfg.Loop.StateDir, state.Event{Loop: cfg.Loop.Name, Event: "control", Action: "succession", Reason: "archived " + s.From.Loop})
	}
	if err := control.SaveSuccession(cfg.Loop.StateDir, *s); err != nil {
		e.errf("warning: the succession record could not be updated: %v", err)
	}
}

// lineageOf is the chain of loops the loop in stateDir continues, oldest
// first, and its own record of the latest one.
func lineageOf(stateDir string) ([]lineageStep, *continuedFromView) {
	var steps []lineageStep
	var first *continuedFromView
	seen := map[string]bool{filepath.Clean(stateDir): true}
	dir := stateDir
	for len(steps) < lineageMax {
		s, err := control.LoadSuccession(dir)
		if err != nil || s == nil || seen[filepath.Clean(s.From.StateDir)] {
			break
		}
		step := lineageStep{Loop: s.From.Loop, Thread: s.From.Thread, Key: refKey(s.From.RegistryID), ArchivedAt: s.ArchivedAt}
		if first == nil {
			first = &continuedFromView{lineageStep: step, Source: s.Source, DeclaredAt: s.DeclaredAt, Waiting: s.Waiting, CancelledAt: s.CancelledAt, Cancelled: s.Cancelled}
		}
		if s.CancelledAt != nil {
			break // a cancelled succession is no part of the line of work
		}
		steps = append([]lineageStep{step}, steps...)
		seen[filepath.Clean(s.From.StateDir)] = true
		dir = s.From.StateDir
	}
	return steps, first
}

// listLineageOf is the lineage as lists carry it; nil for a loop that
// continues none.
func listLineageOf(steps []lineageStep, from *continuedFromView) *listLineage {
	if from == nil {
		return nil
	}
	return &listLineage{Count: len(steps), From: from.Key}
}

// succeededByCmd records, for archive -c OLD --succeeded-by NEW, that NEW
// continues OLD; OLD is archived after NEW's first completed check.
func succeededByCmd(e env, pred *config.Config, newPath string, now time.Time, asJSON bool) int {
	succ, err := config.Load(newPath)
	if err != nil {
		e.errf("archive --succeeded-by: the successor's configuration could not be loaded: %v; nothing written", err)
		return exitUsage
	}
	s, err := declareSuccession(pred, succ, "declared", now)
	if err != nil {
		e.errf("archive --succeeded-by refused: %v; nothing written", err)
		return exitFailed
	}
	text := fmt.Sprintf("Recorded: loop %s continues loop %s, and is shown by its name. Loop %s is archived after the first check of %s that completes from now, if it is held then; until then neither changes.", succ.Loop.Name, pred.Loop.Name, pred.Loop.Name, succ.Loop.Name)
	printArchiveOut(e, archiveOut{Loop: pred.Loop.Name, Outcome: "succession_recorded", Text: text}, asJSON)
	_ = s
	return exitOK
}

// supersedeSuccession links transfer supersede to the loop that supervises
// the successor thread, when exactly one registered loop does.
func supersedeSuccession(e env, pred *config.Config, successorThread string, now time.Time) {
	dir, err := registryDirectory("")
	if err != nil {
		return
	}
	f, err := registry.Load(dir)
	if err != nil {
		return
	}
	var found []*config.Config
	for _, entry := range f.Entries {
		c, err := config.Load(entry.ConfigPath)
		if err != nil || samePath(c.Loop.StateDir, pred.Loop.StateDir) || bbThreadOf(c) != successorThread {
			continue
		}
		found = append(found, c)
	}
	switch len(found) {
	case 1:
		if _, err := declareSuccession(pred, found[0], "supersede", now); err != nil {
			e.errf("supersede: the successor loop %s was not recorded as continuing this one: %v", found[0].Loop.Name, err)
			return
		}
		fmt.Fprintf(e.stdout, "Loop %s, on the successor thread, continues this loop; this loop is archived after its first completed check.\n", found[0].Loop.Name)
	case 0:
		fmt.Fprintln(e.stdout, "No registered loop supervises the successor thread, so this loop is not archived by a succession; once one does, `looptender archive -c CONFIG --succeeded-by ITS_CONFIG` records it.")
	default:
		fmt.Fprintln(e.stdout, "Several registered loops supervise the successor thread, so none was recorded as continuing this one; `looptender archive -c CONFIG --succeeded-by ITS_CONFIG` names it.")
	}
}

// lineageDirs are the state directories of the loops the loop in stateDir
// continues, through records that were not cancelled, nearest first.
func lineageDirs(stateDir string) []string {
	var out []string
	seen := map[string]bool{filepath.Clean(stateDir): true}
	dir := stateDir
	for len(out) < lineageMax {
		s, err := control.LoadSuccession(dir)
		if err != nil || s == nil || s.CancelledAt != nil || seen[filepath.Clean(s.From.StateDir)] {
			break
		}
		out = append(out, s.From.StateDir)
		seen[filepath.Clean(s.From.StateDir)] = true
		dir = s.From.StateDir
	}
	return out
}
