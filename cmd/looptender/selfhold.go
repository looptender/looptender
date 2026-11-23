package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/hostcheck"
	"github.com/looptender/looptender/internal/incident"
	"github.com/looptender/looptender/internal/state"
)

// Holds a session sets on itself, and how they end without a person
// (internal/control/selfhold.go has the rule they share):
//
//   - looptender wait --until CONDITION: the loop waits for something the
//     supervisor can check by itself, and carries on by itself when it
//     holds, with no resume. Until then it is an await-user hold (builds
//     that do not know wait.json send nothing either), shown as a wait.
//   - looptender await-user --withdraw: the owner thread that set an
//     await-user takes back exactly that question.

const (
	waitDefaultFor = 24 * time.Hour
	waitMaxFor     = 7 * 24 * time.Hour
)

// waitConditions are what a wait can wait for, in words for status.
var waitConditions = map[string]string{
	"host-session": "this computer's login session (the keychain reachable and someone logged in at the console)",
	"network":      "the network (the loop's connectivity check)",
	"time":         "a time",
	"limit-reset":  "the recorded provider usage-limit reset",
}

// waitView is a wait as status, doctor, client status and the page show it.
type waitView struct {
	Until     string     `json:"until"`
	UntilTime *time.Time `json:"untilTime,omitempty"`
	Since     time.Time  `json:"since"`
	ExpiresAt time.Time  `json:"expiresAt"`
	Note      string     `json:"note,omitempty"`
	// Text is the one sentence: what it waits for, since when, and what
	// happens if it does not come.
	Text string `json:"text"`
}

// withdrawnView is the owner thread taking back its own question.
type withdrawnView struct {
	At   time.Time `json:"at"`
	Note string    `json:"note,omitempty"`
	Text string    `json:"text"`
}

// parseWaitCondition reads --until.
func parseWaitCondition(s string, now time.Time) (until string, at *time.Time, err error) {
	switch {
	case s == "host-session", s == "network", s == "limit-reset":
		return s, nil, nil
	case strings.HasPrefix(s, "time:"):
		t, perr := time.Parse(time.RFC3339, strings.TrimPrefix(s, "time:"))
		if perr != nil {
			return "", nil, errors.New("time: takes an RFC 3339 time, such as time:2030-01-02T15:04:05Z")
		}
		if !t.After(now) {
			return "", nil, errors.New("the time has already passed")
		}
		u := t.UTC()
		return "time", &u, nil
	}
	return "", nil, fmt.Errorf("unknown condition %q: a wait is for host-session, network, time:RFC3339 or limit-reset, which the supervisor checks by itself", s)
}

func waitText(h *control.SelfHold) string {
	what := waitConditions[h.Until]
	if h.UntilTime != nil {
		what += " (" + h.UntilTime.UTC().Format(time.RFC3339) + ")"
	}
	s := "waiting for " + what + " since " + h.SetAt.UTC().Format(time.RFC3339) + "; it carries on by itself when that holds, with no resume"
	if h.ExpiresAt != nil {
		s += ", and becomes an ordinary await-user at " + h.ExpiresAt.UTC().Format(time.RFC3339) + " if it does not"
	}
	return s
}

// waitOf is the loop's wait, when it is in force: its record and a control
// record unchanged since the wait saved it.
func waitOf(stateDir string, ctl control.File) *waitView {
	h, err := control.LoadSelfHold(stateDir, control.WaitFileName)
	if err != nil || h == nil || !h.Unchanged(ctl) {
		return nil
	}
	v := &waitView{Until: h.Until, UntilTime: h.UntilTime, Since: h.SetAt, Note: h.Note, Text: waitText(h)}
	if h.ExpiresAt != nil {
		v.ExpiresAt = *h.ExpiresAt
	}
	return v
}

// withdrawnOf is the last withdrawal of the owner's own question, while no
// later await-user replaced it.
func withdrawnOf(stateDir string) *withdrawnView {
	h, err := control.LoadSelfHold(stateDir, control.OwnQuestionFileName)
	if err != nil || h == nil || h.WithdrawnAt == nil {
		return nil
	}
	return &withdrawnView{At: *h.WithdrawnAt, Note: h.Withdrawn, Text: "the session withdrew its own question: " + h.Withdrawn}
}

// recordSelfHold saves the record of a hold just saved as saved, after
// reading the control record back: if anything else wrote it in between,
// nothing is recorded and the hold stays an ordinary one.
func recordSelfHold(stateDir, name string, h control.SelfHold, saved control.File) error {
	cur, err := control.Load(stateDir)
	if err != nil {
		return err
	}
	a, b := saved, cur
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		return control.ErrSelfHoldChanged
	}
	h.After = cur
	return control.SaveSelfHold(stateDir, name, h)
}

func ownerOf(f control.File) (string, uint64) {
	if f.Ownership == nil {
		return "", 0
	}
	return f.Ownership.ThreadID, f.Ownership.Generation
}

// waitCmd is looptender wait.
func waitCmd(e env, cfg *config.Config, until, note string, forDur time.Duration, now time.Time) int {
	if forDur == 0 {
		forDur = waitDefaultFor
	}
	if forDur < time.Minute || forDur > waitMaxFor {
		e.errf("wait: --for must be between 1m and %s", state.ShortDuration(waitMaxFor))
		return exitUsage
	}
	cond, at, err := parseWaitCondition(until, now)
	if err != nil {
		e.errf("wait refused, nothing changed: %v", err)
		return exitUsage
	}
	switch cond {
	case "network":
		if connectivity(cfg) == nil {
			e.errf("wait refused, nothing changed: this loop has no connectivity check to wait for")
			return exitFailed
		}
	case "limit-reset":
		st, err := state.Load(cfg.Loop.StateDir)
		if err != nil || !st.LimitResetsAt.After(now) {
			e.errf("wait refused, nothing changed: no future usage-limit reset is recorded for this loop")
			return exitFailed
		}
		r := st.LimitResetsAt.UTC()
		at = &r
	}
	expires := now.Add(forDur).UTC()
	if at != nil && at.After(expires) {
		e.errf("wait refused, nothing changed: %s is after the wait would end (%s); use --for", at.Format(time.RFC3339), expires.Format(time.RFC3339))
		return exitUsage
	}
	if _, archived := archivedNow(cfg.Loop.StateDir); archived {
		e.errf("wait refused, nothing changed: loop %s is archived", cfg.Loop.Name)
		return exitFailed
	}
	h := control.SelfHold{Caller: "terminal", SetAt: now.UTC(), Note: note, Until: cond, UntilTime: at, ExpiresAt: &expires}
	label := "waiting for " + cond
	if note != "" {
		label += ": " + note
	}
	f, err := updateControlAnnotated(cfg.Loop.StateDir, "wait", func(f *control.File) error {
		if err := control.CheckOwner(*f, os.Getenv("BB_THREAD_ID"), false); err != nil {
			return fmt.Errorf("wait refused: %w", err)
		}
		if !f.Supervising() {
			// A wait ends by itself; a hold already there does not, so a wait
			// on top of it would promise what it cannot do.
			return fmt.Errorf("wait refused, nothing changed: the loop is already %s, and a wait does not end a hold someone set; a person lifts it", f.Summary())
		}
		h.Before = *f
		h.Thread, h.Generation = ownerOf(*f)
		h.Caller = controlCaller(*f)
		f.AwaitingUser, f.Note = true, label
		return nil
	}, func(f control.File, c *incident.ControlChange) { c.Caller = controlCaller(f) })
	if err != nil && !errors.Is(err, errControlHistory) {
		e.errf("%v", err)
		return exitFailed
	}
	if err := recordSelfHold(cfg.Loop.StateDir, control.WaitFileName, h, f); err != nil {
		e.errf("wait: the hold was saved but not recorded as a wait (%v); it is an ordinary await-user, which a person resumes", err)
		return exitFailed
	}
	_ = state.Append(cfg.Loop.StateDir, state.Event{Loop: cfg.Loop.Name, Event: "control", Action: "wait", Reason: cond})
	saved, _ := control.LoadSelfHold(cfg.Loop.StateDir, control.WaitFileName)
	fmt.Fprintf(e.stdout, "%s: %s. Nothing is sent meanwhile; a stop, a pause, the deadline and the send limits still apply.\n", cfg.Loop.Name, waitText(saved))
	return exitOK
}

// waitMet checks the condition, within its own bound.
func waitMet(ctx context.Context, cfg *config.Config, h *control.SelfHold, now time.Time) bool {
	switch h.Until {
	case "time", "limit-reset":
		return h.UntilTime != nil && !now.Before(*h.UntilTime)
	case "host-session":
		s := hostcheck.System.ReadSession(ctx)
		return s.Keychain == hostcheck.OK && s.Console == hostcheck.OK
	case "network":
		online := connectivity(cfg)
		if online == nil {
			return false
		}
		c, cancel := context.WithTimeout(ctx, max(cfg.Loop.NetworkCheckTimeout(), time.Second))
		defer cancel()
		return online(c)
	}
	return false
}

// settleWait runs at the start of each check: a wait whose condition holds
// ends, restoring the control record as it was before the wait; one past its
// time becomes an ordinary await-user, its reason kept; one whose control
// record changed (a person resumed, paused or held it) is no longer a wait
// and its record goes, the record left as it is.
func settleWait(e env, cfg *config.Config, now time.Time) {
	dir := cfg.Loop.StateDir
	h, err := control.LoadSelfHold(dir, control.WaitFileName)
	if err != nil || h == nil {
		return
	}
	ctl, err := control.Load(dir)
	if err != nil {
		return
	}
	if !h.Unchanged(ctl) {
		_ = control.RemoveSelfHold(dir, control.WaitFileName)
		return
	}
	switch {
	case h.ExpiresAt != nil && !now.Before(*h.ExpiresAt):
		_, err = updateControlAnnotated(dir, "wait_expired", func(f *control.File) error {
			if !h.Unchanged(*f) {
				return control.ErrSelfHoldChanged
			}
			why := fmt.Sprintf("waited %s for %s, which did not come", state.ShortDuration(now.Sub(h.SetAt)), h.Until)
			if h.Note != "" {
				why += " (" + h.Note + ")"
			}
			// The note from before the wait is kept; the reason goes beside it.
			f.Note = why
			if h.Before.Note != "" {
				f.Note = h.Before.Note + "; " + why
			}
			return nil
		}, nil)
		if err == nil || errors.Is(err, errControlHistory) {
			_ = state.Append(dir, state.Event{Loop: cfg.Loop.Name, Event: "control", Action: "wait_expired", Reason: h.Until})
		}
		_ = control.RemoveSelfHold(dir, control.WaitFileName)
	case !lastControlIs(dir, "wait"):
		// The history does not show a wait as the last control change: the
		// record was not made by looptender wait, so it ends nothing.
		_ = control.RemoveSelfHold(dir, control.WaitFileName)
	case waitMet(e.ctx, cfg, h, now):
		_, err = updateControlAnnotated(dir, "wait_cleared", func(f *control.File) error { return h.Restore(f) }, nil)
		if err == nil || errors.Is(err, errControlHistory) {
			_ = state.Append(dir, state.Event{Loop: cfg.Loop.Name, Event: "control", Action: "wait_cleared", Reason: h.Until})
			e.errf("loop %s: the wait for %s is over; the loop carries on", cfg.Loop.Name, h.Until)
		}
		if err == nil || errors.Is(err, errControlHistory) || errors.Is(err, control.ErrSelfHoldChanged) || errors.Is(err, control.ErrArchived) {
			_ = control.RemoveSelfHold(dir, control.WaitFileName)
		}
	}
}

// noteOwnQuestion records, after an await-user, whether its owner thread
// set it: only then can that thread take it back. Any other await-user
// replaces the record.
func noteOwnQuestion(cfg *config.Config, before, saved control.File, note string) {
	dir := cfg.Loop.StateDir
	_ = control.RemoveSelfHold(dir, control.OwnQuestionFileName)
	if controlCaller(before) != "owner_thread" {
		return
	}
	thread, generation := ownerOf(before)
	_ = recordSelfHold(dir, control.OwnQuestionFileName, control.SelfHold{Thread: thread, Generation: generation, Caller: "owner_thread", SetAt: time.Now().UTC(), Note: note, Before: before}, saved)
}

// withdrawCmd is looptender await-user --withdraw: the owner thread that set
// an await-user takes back exactly that hold, when nobody changed the
// control record since and the history still shows it was that thread's.
func withdrawCmd(e env, cfg *config.Config, note string) int {
	dir := cfg.Loop.StateDir
	refuse := func(why string) int {
		e.errf("await-user --withdraw refused, nothing changed: %s. A person resumes it from the menu, the panel or a terminal outside BB.", why)
		return exitFailed
	}
	if strings.TrimSpace(note) == "" {
		e.errf("await-user --withdraw needs --note: why the question no longer stands")
		return exitUsage
	}
	if _, archived := archivedNow(dir); archived {
		return refuse("the loop is archived")
	}
	h, err := control.LoadSelfHold(dir, control.OwnQuestionFileName)
	if err != nil || h == nil || h.WithdrawnAt != nil {
		return refuse("no await-user set by this loop's own thread is on record")
	}
	caller := os.Getenv("BB_THREAD_ID")
	if caller == "" || caller != h.Thread {
		return refuse("only the thread that set the question can withdraw it")
	}
	if cur, err := control.Load(dir); err != nil || !h.Unchanged(cur) {
		return refuse("the control record changed after the question was set")
	}
	// The history is the source of who set it: the last control change must
	// be this await-user, saved by the owner thread, with the hold it left.
	last := lastControlChange(dir)
	if last == nil || last.ControlChange.Operation != "await-user" || last.ControlChange.Caller != "owner_thread" || last.ControlAfter == nil || !last.ControlAfter.AwaitingUser {
		return refuse("the control history does not show this hold as the thread's own last change (it may be older than the history kept)")
	}
	var restored control.File
	_, err = updateControlAnnotated(dir, "await-user_withdrawn", func(f *control.File) error {
		t, g := ownerOf(*f)
		if t != h.Thread || g != h.Generation {
			return errors.New("the loop's owner changed since the question was set")
		}
		if err := h.Restore(f); err != nil {
			return err
		}
		restored = *f
		return nil
	}, func(f control.File, c *incident.ControlChange) { c.Caller = controlCaller(f) })
	switch {
	case errors.Is(err, control.ErrSelfHoldChanged):
		return refuse("the control record changed after the question was set")
	case errors.Is(err, control.ErrArchived):
		return refuse("the loop is archived")
	case err != nil && !errors.Is(err, errControlHistory):
		return refuse(err.Error())
	}
	at := time.Now().UTC()
	h.WithdrawnAt, h.Withdrawn = &at, strings.Join(strings.Fields(note), " ")
	_ = control.SaveSelfHold(dir, control.OwnQuestionFileName, *h)
	_ = state.Append(dir, state.Event{Loop: cfg.Loop.Name, Event: "control", Action: "await-user_withdrawn", Reason: restored.Summary()})
	fmt.Fprintf(e.stdout, "%s: the session withdrew its own question (%s); the loop is %s. Nothing a person set changed.\n", cfg.Loop.Name, h.Withdrawn, restored.Summary())
	return exitOK
}

// lastControlChange is the journal's last control change, or nil.
func lastControlChange(dir string) *incident.Record {
	j, err := incident.Load(dir)
	if err != nil {
		return nil
	}
	for i := len(j.Records) - 1; i >= 0; i-- {
		if j.Records[i].Kind == "control" && j.Records[i].ControlChange != nil {
			return &j.Records[i]
		}
	}
	return nil
}

// lastControlIs reports whether the journal's last control change is op,
// with the hold it set.
func lastControlIs(dir, op string) bool {
	r := lastControlChange(dir)
	return r != nil && r.ControlChange.Operation == op && r.ControlAfter != nil && r.ControlAfter.AwaitingUser
}
