package control

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompletionBindsExactGoalRevision(t *testing.T) {
	dir := t.TempDir()
	at := time.Now().UTC()
	c, err := RecordCompletion(dir, "example", at)
	if err != nil || c.GoalID != "" || c.GoalRevision != 0 || c.DeclaredAt != at {
		t.Fatal(c, err)
	}
	spec := GoalSpec{Title: "Private example", ObjectiveRefs: []string{"example:objective"}, AcceptanceRefs: []string{"example:acceptance"}, EvidenceRefs: []string{"example:evidence"}}
	g, err := WriteGoal(dir, "example", "", spec, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err = RecordCompletion(dir, "example", at)
	if err != nil || c.GoalID != g.ID || c.GoalRevision != 1 {
		t.Fatal(c, err)
	}
	if err := Save(dir, File{Completed: true, Completion: c}); err != nil {
		t.Fatal(err)
	}
	g, err = WriteGoal(dir, "example", "", spec, g.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctl, err := Load(dir)
	if err != nil || ctl.Completion.GoalRevision != 1 || ctl.Completion.CompareGoal(&GoalSummary{ID: g.ID, Revision: 2}) != "different_revision" {
		t.Fatal(ctl, err)
	}
	if _, err := RecordCompletion(dir, "another-loop", at); err == nil {
		t.Fatal("wrong owning loop accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, GoalName), []byte("PRIVATE-CORRUPTION"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordCompletion(dir, "example", at); err != ErrGoal {
		t.Fatal(err)
	}
}

func TestCompletionComparison(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    *Completion
		g    *GoalSummary
		want string
	}{
		{"legacy", nil, nil, "unbound"},
		{"no-goal", &Completion{}, nil, "unbound"},
		{"unavailable", &Completion{GoalID: "goal_example", GoalRevision: 1}, nil, "unavailable"},
		{"same", &Completion{GoalID: "goal_example", GoalRevision: 1}, &GoalSummary{ID: "goal_example", Revision: 1}, "same_revision"},
		{"revised", &Completion{GoalID: "goal_example", GoalRevision: 1}, &GoalSummary{ID: "goal_example", Revision: 2}, "different_revision"},
		{"changed", &Completion{GoalID: "goal_example", GoalRevision: 1}, &GoalSummary{ID: "goal_other", Revision: 1}, "different_goal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.CompareGoal(tc.g); got != tc.want {
				t.Fatal(got)
			}
		})
	}
	// Existing control records do not acquire invented provenance.
	for _, raw := range []string{`{}`, `{"completed":true}`, `{"completed":false}`} {
		var f File
		if err := json.Unmarshal([]byte(raw), &f); err != nil || f.Completion != nil {
			t.Fatal(f, err)
		}
	}
}
