package supervise

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/prompt"
	"github.com/looptender/looptender/internal/state"
)

// Wording only in the current standing wake policy, and wording only in the
// first one setup wrote.
const (
	currentOnlyPolicy = "For a coordinator, reconcile existing direct children"
	fictionalTaskText = "Finish the fictional-private-task and keep its tests green."
)

// versionOnePrompt is a prompt file as the first setup wrote it.
func versionOnePrompt(t *testing.T) string {
	t.Helper()
	text := prompt.GeneratedPromptVersion(1, `'/fictional/bin/looptender'`, `'/fictional/loop/looptender.yaml'`, fictionalTaskText)
	if v, ok := prompt.MatchPolicy(text); !ok || v.Version != 1 || strings.Contains(text, currentOnlyPolicy) {
		t.Fatalf("not version 1: %+v %v", v, ok)
	}
	return text
}

func TestGeneratedOldPromptSendsCurrentPolicyWithGoalAndTrueSize(t *testing.T) {
	w := newWorld(t, true, false)
	file := filepath.Join(t.TempDir(), "prompt.md")
	old := versionOnePrompt(t)
	if err := os.WriteFile(file, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	w.cfg.Loop.Prompt, w.cfg.Loop.PromptFile, w.cfg.Loop.PromptGoal = "", file, true
	// Recorded as setup records the file it writes.
	if err := prompt.RecordPolicy(file, []byte(old)); err != nil {
		t.Fatal(err)
	}
	p := w.primary(decide.Observation{State: decide.Idle})
	if _, err := control.WriteGoal(w.dir, w.cfg.Loop.Name, "", control.GoalSpec{Title: "fictional goal", ObjectiveRefs: []string{"objective"}, AcceptanceRefs: []string{"acceptance"}}, "", 0); err != nil {
		t.Fatal(err)
	}
	runTick(t, w, t0)
	if len(p.sent) != 1 {
		t.Fatalf("sent %d", len(p.sent))
	}
	sent := p.sent[0]
	if !strings.Contains(sent, currentOnlyPolicy) || !strings.Contains(sent, "Task:\n"+fictionalTaskText) || !strings.Contains(sent, "Current keepgoing goal context") {
		t.Fatalf("sent:\n%s", sent)
	}
	if strings.Index(sent, currentOnlyPolicy) > strings.Index(sent, fictionalTaskText) || strings.Index(sent, fictionalTaskText) > strings.Index(sent, "Current keepgoing goal context") {
		t.Fatal("policy, task and goal out of order")
	}
	st, err := state.Load(w.dir)
	if err != nil || st.LastPromptSend == nil || st.LastPromptSend.Bytes != len(sent) || st.LastPromptSend.GoalRevision != 1 {
		t.Fatalf("recorded send: %+v %v (sent %d bytes)", st.LastPromptSend, err, len(sent))
	}
	if b, _ := os.ReadFile(file); string(b) != old {
		t.Fatal("prompt file was rewritten")
	}
}

func TestHandEditedPromptIsSentExactlyAsWritten(t *testing.T) {
	w := newWorld(t, true, false)
	file := filepath.Join(t.TempDir(), "prompt.md")
	edited := strings.Replace(versionOnePrompt(t), "Continue the authorized task below.", "Continue the authorized task below, in small steps.", 1)
	if err := os.WriteFile(file, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	w.cfg.Loop.Prompt, w.cfg.Loop.PromptFile = "", file
	// Recorded before the edit: the edit ends policy updates.
	if err := prompt.RecordPolicy(file, []byte(versionOnePrompt(t))); err != nil {
		t.Fatal(err)
	}
	p := w.primary(decide.Observation{State: decide.Idle})
	runTick(t, w, t0)
	if len(p.sent) != 1 || p.sent[0] != strings.TrimSpace(edited) || strings.Contains(p.sent[0], currentOnlyPolicy) {
		t.Fatalf("sent:\n%v", p.sent)
	}
	st, err := state.Load(w.dir)
	if err != nil || st.LastPromptSend == nil || st.LastPromptSend.Bytes != len(p.sent[0]) {
		t.Fatalf("recorded send: %+v %v", st.LastPromptSend, err)
	}
	if b, _ := os.ReadFile(file); string(b) != edited {
		t.Fatal("prompt file was rewritten")
	}
}
