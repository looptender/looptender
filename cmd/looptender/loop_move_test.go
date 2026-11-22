//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
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

func markedControl(held bool) control.File {
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	return control.File{Enabled: true, AwaitingUser: held, MovedAt: &at}
}

func withControl(f control.File, edit func(*control.File)) control.File {
	edit(&f)
	return f
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
		// The move's own mark, with and without the handoff record.
		{"marked by transfer apply, held", markedControl(true), state.File{}, "held"},
		{"marked, held, but a check has run", markedControl(true), state.File{CheckedAt: checked}, ""},
		// Holds that are not a plain resume: the real need must show.
		{"a transfer pending", withControl(movedControl(true), func(f *control.File) {
			f.PendingTransfer = &control.PendingBBTransfer{Version: 1, ID: "move_x", From: "thr_a", To: "thr_b"}
		}), state.File{}, ""},
		{"a second move interrupted after an earlier one", withControl(markedControl(true), func(f *control.File) {
			f.PendingTransfer = &control.PendingBBTransfer{Version: 1, ID: "move_y", From: "thr_b", To: "thr_c"}
		}), state.File{}, ""},
		{"superseded", withControl(movedControl(true), func(f *control.File) { f.SupersededBy = "thr_other" }), state.File{}, ""},
		{"stopped", withControl(movedControl(true), func(f *control.File) { f.Stop = true }), state.File{}, ""},
		{"completed", withControl(markedControl(true), func(f *control.File) { f.Completed = true }), state.File{}, ""},
		{"an agent's hold with a note", withControl(movedControl(true), func(f *control.File) { f.Note = "need a decision on X" }), state.File{}, "unheld"},
		{"an open question", withControl(markedControl(true), func(f *control.File) { f.Question = &control.Question{} }), state.File{}, "unheld"},
		{"an open question that outlived completion", withControl(markedControl(true), func(f *control.File) { f.OpenQuestion = &control.OpenQuestion{} }), state.File{}, "unheld"},
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
	// A moved loop whose hold carries something else (a note, a question) keeps
	// the real need's reason: not held for the move, so not overridden.
	other := dockEvidence{controls: &clientControls{Enabled: true, AwaitingUser: true}}.input()
	other.Client.Snapshot.Moved = &clientMoved{Held: false, Text: movedUnheldText}
	if o, need := projectDockLoop(other, dockNow, defaultStaleAfter, false); o.Phrase != "Waiting for you" || need == nil || need.Reason == movedHeldText || need.Action == "Resume the loop; that is the one action needed." {
		t.Errorf("%+v %+v", o, need)
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
	// A prompt file: only its age is said, with no claim about when it was written.
	raw, _ := os.ReadFile(path)
	raw = bytes.Replace(raw, []byte("prompt: PRIVATE-PROMPT"), []byte("prompt_file: ./prompt.md"), 1)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "prompt.md"), []byte("PRIVATE-TASK"), 0600); err != nil {
		t.Fatal(err)
	}
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
	if m := check(r, "prompt_review"); !strings.Contains(m, "review it after a provider change") || strings.Contains(m, "before this move") {
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
	// BB could not be reached for the destination: said apart from "BB did not report".
	path, _ = crossProviderFixture(t)
	_, r, _ = runTransfer(t, path, crossProviderRunner(t, nil), "--allow-provider-change", "--provider", "codex", "--quota-group", "example-codex", "--model=", "--reasoning=", "--offline")
	if m := check(r, "model_label"); !strings.Contains(m, "offline") || strings.Contains(m, "did not report") {
		t.Errorf("offline: %q", m)
	}
	path, _ = crossProviderFixture(t)
	failing := func(ctx context.Context, cli string, args []string, env map[string]string, workdir string) ([]byte, error) {
		return nil, errors.New("unreachable")
	}
	_, r, _ = runTransfer(t, path, failing, crossProviderArgs()...)
	if m := check(r, "model_label"); !strings.Contains(m, "could not be reached") || strings.Contains(m, "did not report") {
		t.Errorf("unreachable: %q", m)
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

// transfer apply marks its own hold when it finishes, and only then.
func TestTransferApplyMarksItsOwnHold(t *testing.T) {
	path, dir := crossProviderFixture(t)
	// A loop held for a person with nothing else on the hold, owned by the source.
	if err := control.Save(dir, control.File{Enabled: true, AwaitingUser: true, Ownership: &control.Ownership{ThreadID: "thr_source", Generation: 1}}); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"apply", "--from", "thr_source", "--thread", "thr_destination", "--take-over"}, crossProviderArgs()...)
	interrupted := true
	code, _ := callProviderTransfer(t, path, crossProviderRunner(t, nil), func(step string) error {
		if step == "state" && interrupted {
			return errors.New("fixture interruption")
		}
		return nil
	}, args...)
	ctl, _ := control.Load(dir)
	if code == 0 || ctl.PendingTransfer == nil || ctl.MovedAt != nil {
		t.Fatalf("pending: %d %+v", code, ctl)
	}
	// Mid-transfer the hold is not the move's: recover, not resume, is needed.
	if st, _ := state.Load(dir); movedOf(ctl, st) != nil {
		t.Fatal("a pending transfer reads as moved")
	}
	interrupted = false
	code, out := callProviderTransfer(t, path, crossProviderRunner(t, nil), nil, "recover", "--id", ctl.PendingTransfer.ID, "--take-over")
	ctl, _ = control.Load(dir)
	st, _ := state.Load(dir)
	if code != 0 || ctl.MovedAt == nil || ctl.PendingTransfer != nil {
		t.Fatalf("%d %s %+v", code, out, ctl)
	}
	if m := movedOf(ctl, st); m == nil || !m.Held {
		t.Fatalf("finished move: %+v awaiting=%v note=%q q=%v oq=%v briefs=%d ep=%v stop=%v checked=%v last=%v", m, ctl.AwaitingUser, ctl.Note, ctl.Question, ctl.OpenQuestion, len(ctl.BriefHold), ctl.EpisodeHold, ctl.Stop, st.CheckedAt, st.LastCheck)
	}
}
