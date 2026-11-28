package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
)

func setResumeDeadline(t *testing.T, f resumeFixture, at time.Time) {
	t.Helper()
	b := resumeBytes(t, f.entry.ConfigPath)
	resumeWrite(t, f.entry.ConfigPath, append(b, []byte("  limits: {deadline: '"+at.Format(time.RFC3339)+"'}\n")...))
}

// Check the actual admission paths with saved controls, state and a goal, not
// just the deadline predicate. Refusals must not reset an error episode either.
func TestClientResumeDeadline(t *testing.T) {
	for _, stage := range []string{"expired_prepare", "boundary_prepare", "during_prepare", "before_apply", "after_consume", "future", "absent"} {
		t.Run(stage, func(t *testing.T) {
			f := newResumeFixture(t)
			f.at = f.at.Truncate(time.Second)
			deadline := f.at.Add(30 * time.Second)
			if stage == "expired_prepare" {
				deadline = f.at.Add(-time.Second)
			}
			if stage == "boundary_prepare" {
				deadline = f.at
			}
			if stage != "absent" {
				setResumeDeadline(t, f, deadline)
			}
			if _, err := control.Update(f.entry.StateDir, func(c *control.File) { c.EpisodeHold = true }); err != nil {
				t.Fatal(err)
			}
			if err := state.Save(f.entry.StateDir, state.File{CheckedAt: f.at, ActiveTarget: "primary", Reprompts: 3, Nudges: 2, LastRepromptAt: f.at.Add(-time.Minute)}); err != nil {
				t.Fatal(err)
			}
			paths := []string{f.entry.ConfigPath, filepath.Join(f.entry.StateDir, control.FileName), filepath.Join(f.entry.StateDir, state.FileName), filepath.Join(f.entry.StateDir, "goal.json")}
			before := make([][]byte, len(paths))
			for i, p := range paths {
				before[i] = resumeBytes(t, p)
			}
			var ticket string
			if stage == "before_apply" || stage == "after_consume" || stage == "future" || stage == "absent" {
				ticket = f.prepare(t).Ticket
			}
			op := "prepare-resume"
			if ticket != "" {
				op = "resume"
			}
			if stage == "before_apply" {
				f.at = deadline
			}
			calls := 0
			now := func() time.Time {
				calls++
				if stage == "during_prepare" && calls >= 3 {
					return deadline
				}
				return f.at
			}
			hook := func() error {
				if stage == "after_consume" {
					f.at = deadline
				}
				return nil
			}
			r, code := clientResume(context.Background(), f.dir, f.entry.ID, op, ticket, false, "thr_example_owner", "", "", now, hook)
			if stage == "future" || stage == "absent" {
				if code != 0 || r.ControlSave != "confirmed" {
					t.Fatal(code, r)
				}
				return
			}
			if code != exitFailed || r.ControlSave != "not_attempted" || strings.Join(r.Issues, ",") != "deadline_reached" || r.Ticket != "" {
				t.Fatalf("expired resume accepted or wrong refusal: %d %+v", code, r)
			}
			for i, p := range paths {
				if !bytes.Equal(before[i], resumeBytes(t, p)) {
					t.Fatalf("refusal changed %s", filepath.Base(p))
				}
			}
			if ticket == "" {
				if _, err := os.Stat(filepath.Join(f.entry.StateDir, clientResumeFile)); !os.IsNotExist(err) {
					t.Fatalf("preparation wrote ticket: %v", err)
				}
			} else {
				saved, err := loadClientTicket(f.entry.StateDir)
				if err != nil || saved.Consumed != (stage == "after_consume") {
					t.Fatalf("ticket consumption: %+v %v", saved, err)
				}
			}
			if incidentCount(t, f.entry) != 0 {
				t.Fatal("refusal recorded a control change")
			}
		})
	}
}

func TestResumeDeadlineReviewAndOutcome(t *testing.T) {
	f := newResumeFixture(t)
	setResumeDeadline(t, f, f.at.Add(-time.Hour))
	before := resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))
	// A nil reader ensures expired review is refused before a daemon/status probe.
	_, refused := panelReviewForCaller(context.Background(), f.dir, nil, dockKey(f.entry.ID), "resume", "", f.at)
	if refused == nil || refused.Outcome != "refused" || !strings.Contains(refused.Text, "loop.limits.deadline") || !strings.Contains(refused.Text, "Nothing was changed") {
		t.Fatalf("review: %+v", refused)
	}
	reply, code := clientReview(context.Background(), f.dir, dockKey(f.entry.ID), "resume", f.at, time.UTC)
	if code != exitFailed || reply.Preview != nil || reply.Result == nil || reply.Result.Text != refused.Text || len(reply.Result.Text) > clientReviewMenuCap {
		t.Fatalf("native review: %d %+v", code, reply)
	}
	outcome := panelOutcome("resume", clientHoldReport{Issues: []string{"deadline_reached"}})
	if outcome.Outcome != "refused" || !strings.Contains(outcome.Text, "Resume cannot change the deadline") {
		t.Fatalf("outcome: %+v", outcome)
	}
	if !bytes.Equal(before, resumeBytes(t, filepath.Join(f.entry.StateDir, control.FileName))) {
		t.Fatal("review changed holds")
	}
}
