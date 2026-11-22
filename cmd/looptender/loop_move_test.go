//go:build unix

package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/state"
)

func movedControl(held bool) control.File {
	return control.File{Enabled: true, AwaitingUser: held, Ownership: &control.Ownership{ThreadID: "thr_destination", Generation: 2,
		Previous: &control.Handoff{ThreadID: "thr_source", Status: "handed_off", At: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}}}
}

// The note is derived from the records a move leaves, and only while no check
// has run on the new target.
func TestMovedIsDerivedFromTheMoveRecords(t *testing.T) {
	checked := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		ctl  control.File
		st   state.File
		want string // "", held or unheld
	}{
		{"moved and held", movedControl(true), state.File{}, "held"},
		{"moved, resumed, not yet checked", movedControl(false), state.File{}, "unheld"},
		{"checked since the move", movedControl(true), state.File{CheckedAt: checked}, ""},
		{"an ordinary hold on a loop that never moved", control.File{Enabled: true, AwaitingUser: true}, state.File{}, ""},
		{"an owner with no handoff recorded", control.File{Enabled: true, AwaitingUser: true, Ownership: &control.Ownership{ThreadID: "thr_destination", Generation: 1}}, state.File{}, ""},
	} {
		m := movedOf(tc.ctl, tc.st)
		switch {
		case tc.want == "" && m != nil, tc.want != "" && m == nil:
			t.Errorf("%s: %+v", tc.name, m)
		case tc.want == "held" && (!m.Held || m.Text != movedHeldText), tc.want == "unheld" && (m.Held || m.Text != movedUnheldText):
			t.Errorf("%s: %+v", tc.name, m)
		}
	}
	b, _ := json.Marshal(movedOf(movedControl(true), state.File{}))
	if strings.Contains(string(b), "thr_") {
		t.Errorf("a thread ID is in the note: %s", b)
	}
}

// status, client status and the panel say a moved loop was moved, that
// resuming is the one action needed, and that its target is new, not "never".
func TestStatusAndSurfacesSayALoopWasMoved(t *testing.T) {
	f := newFixture(t)
	if code, _, errs := f.run("enable"); code != 0 {
		t.Fatal(errs)
	}
	f.tick()
	dir := filepath.Join(f.dir, "state")
	if err := control.Save(dir, movedControl(true)); err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load(dir)
	st.CheckedAt, st.LastCheck, st.LastState = time.Time{}, nil, ""
	if err := state.Save(dir, st); err != nil {
		t.Fatal(err)
	}
	code, out, errs := f.run("status")
	if code != 0 && code != exitFailed {
		t.Fatalf("%d %s", code, errs)
	}
	for _, want := range []string{"last check: " + movedNoCheckText, "moved:     " + movedHeldText} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "last check: never") || strings.Contains(out, "thr_") && strings.Contains(out, "moved:     Moved to thr_") {
		t.Errorf("status:\n%s", out)
	}
	_, js, _ := f.run("status", "--json")
	var s struct {
		Moved *movedStatus `json:"moved"`
	}
	if err := json.Unmarshal([]byte(js), &s); err != nil || s.Moved == nil || !s.Moved.Held {
		t.Errorf("status --json: %v %s", err, js)
	}

	// The panel's words for the hold: a distinct phrase and reason, and the
	// target's provider and model beside the name.
	in := dockEvidence{controls: &clientControls{Enabled: true, AwaitingUser: true}}.input()
	in.Client.Snapshot.Moved = &clientMoved{Held: true, Text: movedHeldText}
	in.Client.Snapshot.Target = &clientTarget{Alias: "primary", Adapter: "bb", Provider: "claude-code", Model: "example-model"}
	l, need := projectDockLoop(in, dockNow, defaultStaleAfter, false)
	if l.Phrase != movedHeldPhrase || l.Detail != movedHeldText || need == nil || need.Reason != movedHeldText || l.Provider != "claude-code" || l.Model != "example-model" || l.State != "waiting_for_you" {
		t.Errorf("%+v %+v", l, need)
	}
	list := buildPanelList(dockReport{Loops: []dockLoop{l}})
	if r := list.Loops[0]; r.Phrase != movedHeldPhrase || r.Provider != "claude-code" || r.Model != "example-model" || r.Moved == nil || !r.Moved.Held {
		t.Errorf("%+v", r)
	}
	// An ordinary hold keeps its wording.
	plain, _ := projectDockLoop(dockEvidence{controls: &clientControls{Enabled: true, AwaitingUser: true}}.input(), dockNow, defaultStaleAfter, false)
	if plain.Phrase != "Waiting for you" || plain.Moved != nil {
		t.Errorf("%+v", plain)
	}
}

// A preview of a provider change says a blank model or reasoning label will
// be blank, offers what BB reports for the destination and nothing when it
// reports null, and warns that the task was written for the old provider.
func TestTransferPreviewWarnsAboutBlankLabelsAndThePrompt(t *testing.T) {
	check := func(r transferPreview, id string) string {
		for _, c := range r.Checks {
			if c.ID == id {
				if c.Status != "warning" {
					t.Errorf("%s is %s", id, c.Status)
				}
				return c.Message
			}
		}
		return ""
	}
	path, _ := crossProviderFixture(t)
	_, r, _ := runTransfer(t, path, crossProviderRunner(t, func(id string, v map[string]any) {
		if id == "thr_destination" {
			v["thread"].(map[string]any)["model"] = "example-dest-model"
			v["thread"].(map[string]any)["reasoningLevel"] = nil
		}
	}), crossProviderArgs()...)
	if m := check(r, "model_label"); !strings.Contains(m, "will be blank") || !strings.Contains(m, "pass --model example-dest-model") {
		t.Errorf("model: %q", m)
	}
	if m := check(r, "reasoning_label"); !strings.Contains(m, "will be blank") || !strings.Contains(m, "did not report") || strings.Contains(m, "pass --reasoning") {
		t.Errorf("reasoning: %q", m)
	}
	if m := check(r, "prompt_review"); !strings.Contains(m, "previous provider") {
		t.Errorf("prompt: %q", m)
	}
	// Nothing is offered when BB returns null for both, and a model given on
	// the command line is not called blank.
	path, _ = crossProviderFixture(t)
	_, r, _ = runTransfer(t, path, crossProviderRunner(t, nil), "--allow-provider-change", "--provider", "codex", "--quota-group", "example-codex", "--model", "example-model", "--reasoning=")
	if m := check(r, "model_label"); m != "" {
		t.Errorf("model was given: %q", m)
	}
	if m := check(r, "reasoning_label"); !strings.Contains(m, "did not report") {
		t.Errorf("reasoning: %q", m)
	}
	// No provider change, no warnings.
	path, _ = transferFixture(t)
	_, r, _ = runTransfer(t, path, transferRunner(t, nil))
	for _, id := range []string{"model_label", "reasoning_label", "prompt_review"} {
		if check(r, id) != "" {
			t.Errorf("%s without a provider change", id)
		}
	}
}
