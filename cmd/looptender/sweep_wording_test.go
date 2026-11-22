package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
)

// A held loop is not called "not held": the child-error record is from the
// last check before the hold.
func TestChildErrorReportOnAHeldLoopDoesNotSayItIsNotHeld(t *testing.T) {
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s := childReportStatus{State: "not_subscribed", DetectedAt: &at, ErroredChildren: 2, ListingComplete: true}
	var held, free bytes.Buffer
	printChildReport(&held, s, "paused")
	printChildReport(&free, s, "")
	if strings.Contains(held.String(), "The loop is not held") || !strings.Contains(held.String(), "The loop is held now (paused); this record is from the last check before the hold.") {
		t.Errorf("held:\n%s", held.String())
	}
	if !strings.Contains(free.String(), "The loop is not held.") {
		t.Errorf("free:\n%s", free.String())
	}
}

// A reset after the loop's own deadline is pointed out, held or not.
func TestLimitWaitSaysWhenTheResetFallsAfterTheDeadline(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	rec := &state.LimitWaitReport{Target: "primary", ResetsAt: now.Add(100 * time.Hour), DetectedAt: now.Add(-time.Hour), Outcome: state.LimitWaitNotSubscribed, Reason: "limit_no_fallback"}
	loop := config.Loop{}
	loop.Limits.Deadline = now.Add(48 * time.Hour).Format(time.RFC3339)
	s := describeLimitWait(rec, loop, "paused", now)
	if !s.ResetAfterDeadline {
		t.Fatalf("%+v", s)
	}
	var out bytes.Buffer
	printLimitWait(&out, s)
	if !strings.Contains(out.String(), "the reset falls after this loop's deadline") {
		t.Errorf("%s", out.String())
	}
	loop.Limits.Deadline = now.Add(200 * time.Hour).Format(time.RFC3339)
	if describeLimitWait(rec, loop, "", now).ResetAfterDeadline {
		t.Error("a reset before the deadline is flagged")
	}
	loop.Limits.Deadline = ""
	if describeLimitWait(rec, loop, "", now).ResetAfterDeadline {
		t.Error("no deadline, but flagged")
	}
}

// doctor mentions a recorded wait on a held loop, as information.
func TestDoctorMentionsAWaitRecordedOnAHeldLoop(t *testing.T) {
	f := newDoctorFixture(t)
	cfg, err := config.Load(f.config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	st := state.File{LimitWaitReport: &state.LimitWaitReport{Target: "primary", ResetsAt: now.Add(60 * time.Hour), DetectedAt: now.Add(-time.Hour), Outcome: state.LimitWaitNotSubscribed, Reason: "limit_no_fallback"}}
	r := &doctorReport{Checks: []doctorCheck{}}
	doctorRoutes(r, cfg, "loop.yaml", st, true, "paused", now)
	var found *doctorCheck
	for i := range r.Checks {
		if r.Checks[i].ID == "limit_wait" {
			found = &r.Checks[i]
		}
	}
	if found == nil || found.Status != "ok" || !strings.Contains(found.Message, "recorded before the hold") {
		t.Fatalf("%+v", r.Checks)
	}
	r = &doctorReport{Checks: []doctorCheck{}}
	doctorRoutes(r, cfg, "loop.yaml", st, true, "", now)
	for _, c := range r.Checks {
		if c.ID == "limit_wait" && c.Status != "warning" {
			t.Errorf("an unheld loop's wait is %s", c.Status)
		}
	}
}

// A disabled loop is not judged: a stale check or another build label is
// information, not a warning; an enabled loop still gets the warnings.
func TestDoctorDoesNotWarnAboutADisabledLoopsChecksOrBuild(t *testing.T) {
	st := state.File{CheckedAt: time.Now().Add(-48 * time.Hour)}
	level := func(r *doctorReport, id string) string {
		for _, c := range r.Checks {
			if c.ID == id {
				return c.Status
			}
		}
		return ""
	}
	r := &doctorReport{CheckedAt: time.Now(), Version: "v2", Checks: []doctorCheck{}}
	doctorHeartbeat(r, st, control.File{Enabled: false}, true, time.Hour)
	doctorCheckVersion(r, &state.Check{Version: "v1"}, true, true)
	if level(r, "heartbeat") != "ok" || level(r, "supervisor.version") != "ok" {
		t.Errorf("disabled: %+v", r.Checks)
	}
	r = &doctorReport{CheckedAt: time.Now(), Version: "v2", Checks: []doctorCheck{}}
	doctorHeartbeat(r, st, control.File{Enabled: true}, true, time.Hour)
	doctorCheckVersion(r, &state.Check{Version: "v1"}, true, false)
	if level(r, "heartbeat") != "warning" || level(r, "supervisor.version") != "warning" {
		t.Errorf("enabled: %+v", r.Checks)
	}
}

// A hand-edited prompt with no `Task:` line is told what apply needs, not
// offered a command that would refuse.
func TestHandEditedPromptWithoutATaskLineIsToldWhatApplyNeeds(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "prompt.md")
	if err := os.WriteFile(file, []byte("Fictional hand written prompt with no marker line.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := describeWakePolicy(config.Loop{PromptFile: file})
	if s == nil || s.Source != "custom" || !strings.Contains(s.Message, "put the task after a line reading exactly `Task:`") || strings.Contains(s.Message, "rewrites the text above the line") {
		t.Fatalf("%+v", s)
	}
	if err := os.WriteFile(file, []byte("Fictional preamble.\nTask:\nFictional task.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := describeWakePolicy(config.Loop{PromptFile: file}); s == nil || !strings.Contains(s.Message, "rewrites the text above the line `Task:`") {
		t.Fatalf("%+v", s)
	}
}

// Dev build labels with a suffix (such as -agy) are shortened like the rest,
// so every surface shows the same length.
func TestShortBuildLabelHandlesASuffix(t *testing.T) {
	for in, want := range map[string]string{
		"v0.2.0-dev.3f8c4d7ccc37-agy":   "v0.2.0-dev.3f8c4d7-agy",
		"v0.2.0-dev.3f8c4d7ccc37-dirty": "v0.2.0-dev.3f8c4d7-dirty",
		"v0.2.0-dev.0b4e12399667":       "v0.2.0-dev.0b4e123",
	} {
		if got := shortBuildLabel(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// `--version` prints the version like `version`.
func TestDashDashVersionPrintsTheVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"--version"}, &out, &errb); code != 0 || strings.TrimSpace(out.String()) != version {
		t.Fatalf("%d %q %q", code, out.String(), errb.String())
	}
}

// A completed or disabled loop is not warned that a limit can stop its work.
func TestSingleRouteWarningIsNotShownForACompletedOrDisabledLoop(t *testing.T) {
	f := newDoctorFixture(t)
	cfg, err := config.Load(f.config)
	if err != nil {
		t.Fatal(err)
	}
	has := func(held string) bool {
		r := &doctorReport{Checks: []doctorCheck{}}
		doctorRoutes(r, cfg, "loop.yaml", state.File{}, true, held, time.Now())
		for _, c := range r.Checks {
			if c.ID == "routes.single" {
				return true
			}
		}
		return false
	}
	if !has("") || !has("paused") || !has("awaiting a person") {
		t.Fatal("a loop that can run lost its single-route warning")
	}
	if has("completed") || has("disabled") {
		t.Fatal("a completed or disabled loop is told a limit can stop its work")
	}
}
