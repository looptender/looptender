//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
)

func TestGoalCompletionRetargetRevisionAndReload(t *testing.T) {
	f := newFixture(t)
	cfg, err := config.Load(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(t.TempDir(), "spec.json")
	write(t, spec, `{"title":"PRIVATE-TITLE","objectiveRefs":["PRIVATE-OBJECTIVE"],"acceptanceRefs":["PRIVATE-ACCEPTANCE"],"evidenceRefs":["PRIVATE-EVIDENCE"]}`, 0600)
	if c, _, e := f.run("goal", "create", "--file", spec); c != exitOK {
		t.Fatal(e)
	}
	g, _ := control.LoadGoal(cfg.Loop.StateDir)
	if c, _, e := f.run("goal", "revise", "--id", g.ID, "--revision", "1", "--file", spec); c != exitOK {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(cfg.Loop.StateDir, control.GoalName))
	for _, target := range []string{"claude-other-account", "primary"} {
		if c, _, e := f.run("retarget", target); c != exitOK {
			t.Fatal(e)
		}
		after, err := os.ReadFile(filepath.Join(cfg.Loop.StateDir, control.GoalName))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("retarget changed goal identity/revisions/references", err)
		}
	}
	calls, probes := f.read("calls"), f.read("probes")
	if c, _, e := f.run("complete", "--note", "PRIVATE-COMPLETION"); c != exitOK {
		t.Fatal(e)
	}
	ctl, err := control.Load(cfg.Loop.StateDir)
	if err != nil || !ctl.Completed || ctl.Completion == nil || ctl.Completion.GoalID != g.ID || ctl.Completion.GoalRevision != 2 {
		t.Fatal(ctl, err)
	}
	if f.read("calls") != calls || f.read("probes") != probes {
		t.Fatal("completion called provider")
	}
	declared := *ctl.Completion
	if c, _, e := f.run("goal", "revise", "--id", g.ID, "--revision", "2", "--file", spec); c != exitOK {
		t.Fatal(e)
	}
	// An existing declaration, including its evidence revision, is immutable
	// across repeated completion calls and explicit goal edits.
	if c, _, e := f.run("complete"); c != exitOK {
		t.Fatal(e)
	}
	ctl, _ = control.Load(cfg.Loop.StateDir)
	if *ctl.Completion != declared {
		t.Fatal("repeat complete rebound historical declaration")
	}
	c, out, e := f.run("status", "--json")
	var status struct {
		Control    control.File `json:"control"`
		Comparison string       `json:"completionGoalComparison"`
	}
	if c != exitOK || json.Unmarshal([]byte(out), &status) != nil || status.Comparison != "different_revision" || status.Control.Completion.GoalRevision != 2 {
		t.Fatal(out, e)
	}
	c, out, e = f.run("status")
	if c != exitOK || !strings.Contains(out, "revision 2 (different_revision;") || strings.Contains(out, "PRIVATE-TITLE") || strings.Contains(out, "PRIVATE-EVIDENCE") {
		t.Fatal(out, e)
	}
	if c, _, e := f.run("resume"); c != exitOK {
		t.Fatal(e)
	}
	if c, _, e := f.run("complete"); c != exitOK {
		t.Fatal(e)
	}
	ctl, _ = control.Load(cfg.Loop.StateDir)
	if ctl.Completion.GoalRevision != 3 {
		t.Fatal("new completion did not bind latest revision")
	}

}

func TestGoalCompletionRefusesUnavailableAndCompetingGoal(t *testing.T) {
	for _, kind := range []string{"corrupt", "wrong-loop", "control-lock"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			cfg, _ := config.Load(f.cfg)
			if c, _, e := f.run("enable"); c != exitOK {
				t.Fatal(e)
			}
			before, _ := os.ReadFile(filepath.Join(cfg.Loop.StateDir, control.FileName))
			switch kind {
			case "corrupt":
				write(t, filepath.Join(cfg.Loop.StateDir, control.GoalName), "PRIVATE-CORRUPTION", 0600)
			case "wrong-loop":
				_, err := control.WriteGoal(cfg.Loop.StateDir, "other-loop", "", control.GoalSpec{Title: "Private", ObjectiveRefs: []string{"example:x"}, AcceptanceRefs: []string{"example:y"}}, "", 0)
				if err != nil {
					t.Fatal(err)
				}
			case "control-lock":
				l, err := lock.AcquireNamed(cfg.Loop.StateDir, lock.Control)
				if err != nil {
					t.Fatal(err)
				}
				defer l.Release()
			}
			c, out, e := f.run("complete")
			if c == exitOK || strings.Contains(out+e, "PRIVATE-") {
				t.Fatal(c, out, e)
			}
			after, _ := os.ReadFile(filepath.Join(cfg.Loop.StateDir, control.FileName))
			if !bytes.Equal(before, after) {
				t.Fatal("failed completion changed controls")
			}
		})
	}
}

func TestGoalCompletionReviewBinding(t *testing.T) {
	f, cfg, _ := completionFixture(t, false)
	file := completionInput(t, cfg)
	if c, _, e := f.run("complete", "--review-file", file); c != exitOK {
		t.Fatal(e)
	}
	ctl, _ := control.Load(cfg.Loop.StateDir)
	g, _ := control.LoadGoal(cfg.Loop.StateDir)
	r, _ := control.LoadAssignments(cfg.Loop.StateDir)
	if ctl.Completion == nil || ctl.Completion.GoalID != g.ID || ctl.Completion.GoalRevision != g.Summary().Revision || ctl.Completion.Assignments != r.ID || ctl.Completion.AssignmentRevision != uint64(len(r.Revisions)) {
		t.Fatal("review binding missing", ctl.Completion)
	}
}

func TestGoalCompletionLegacyRecordsRemainCompatible(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-declaration", true: "legacy-completed"}[completed], func(t *testing.T) {
			f := newFixture(t)
			cfg, _ := config.Load(f.cfg)
			if err := control.Save(cfg.Loop.StateDir, control.File{Completed: completed}); err != nil {
				t.Fatal(err)
			}
			if c, _, e := f.run("complete"); c != exitOK {
				t.Fatal(e)
			}
			ctl, err := control.Load(cfg.Loop.StateDir)
			if err != nil || !ctl.Completed {
				t.Fatal(ctl, err)
			}
			if completed {
				if ctl.Completion != nil {
					t.Fatal("invented provenance for legacy hold")
				}
			} else if ctl.Completion == nil || ctl.Completion.GoalID != "" || ctl.Completion.GoalRevision != 0 {
				t.Fatal("no-goal completion failed", ctl)
			}
			if c, _, e := f.run("status", "--json"); c != exitOK {
				t.Fatal(e)
			}
		})
	}
}
