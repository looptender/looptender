package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/config"
)

// Each wording setup ever wrote, by digest. A loop's prompt file is matched
// against these exactly, so an entry must never change: add a new version.
var frozenPolicyHeads = []string{
	"a370eee31b0063e24df94c9151bc434b11f1fe09f10cbd1c7833016fc46265b4",
	"3677baca888fc5da28483f7a4628b3d3989da06c86b35d5e8d698d80d4683fc6",
	"814de92acaf0e2d0951871f8439bda1e33187ba2553ac52b3a8e507ffc0596c4",
	"aaf25014203140f353a18ea1025548fdce6070214c84cb5b7369ceec0987aceb",
	"30e658cf218750f0b84a9c2f915d979bb7bfb84df7a1b35bc6be3a862abe878f",
}

func TestPolicyWordingsAreFrozen(t *testing.T) {
	if len(policyHeads) < len(frozenPolicyHeads) {
		t.Fatalf("a wording was removed: %d < %d", len(policyHeads), len(frozenPolicyHeads))
	}
	for i, want := range frozenPolicyHeads {
		sum := sha256.Sum256([]byte(policyHeads[i]))
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("version %d changed: %s; add a new version instead", i+1, got)
		}
	}
	for i, h := range policyHeads {
		if strings.Count(h, "%s") != 4 || strings.Count(h, "%") != 4 || !strings.HasSuffix(h, "\n\nTask:\n") {
			t.Errorf("version %d has an unexpected shape", i+1)
		}
	}
}

const (
	unixPrefix  = `'/home/fictional/.local/bin/looptender'`
	winPrefix   = `& 'C:\fictional\looptender.exe'`
	quotedCfg   = `'/home/fictional/loops/it'\''s/looptender.yaml'`
	fictionTask = "Ship the fictional widget.\n\nKeep the README current."
)

func oldPrompt(version int, prefix, cfg, task string) string {
	return renderHead(version, prefix, cfg) + task + "\n"
}

func TestMatchPolicyRecognisesEveryGeneratedVersion(t *testing.T) {
	for v := 1; v <= CurrentPolicyVersion(); v++ {
		for _, prefix := range []string{unixPrefix, winPrefix} {
			m, ok := MatchPolicy(oldPrompt(v, prefix, quotedCfg, fictionTask))
			if !ok || m.Version != v || m.Prefix != prefix || m.ConfigPath != quotedCfg || m.Task != fictionTask {
				t.Fatalf("version %d %s: %+v %v", v, prefix, m, ok)
			}
		}
	}
}

func TestCurrentWordingReplacesOnlyThePolicyPart(t *testing.T) {
	current := GeneratedPrompt(unixPrefix, quotedCfg, fictionTask)
	for v := 1; v <= CurrentPolicyVersion(); v++ {
		got, m, ok := CurrentWording(oldPrompt(v, unixPrefix, quotedCfg, fictionTask))
		if !ok || m.Version != v || got != strings.TrimSpace(current) {
			t.Fatalf("version %d:\n%q", v, got)
		}
	}
	// The task part may be edited freely: it is the loop's own.
	edited := oldPrompt(1, unixPrefix, quotedCfg, "A different task, edited by hand.\nTask:\nwith its own Task: line")
	got, _, ok := CurrentWording(edited)
	if !ok || got != PolicyHead(unixPrefix, quotedCfg)+"A different task, edited by hand.\nTask:\nwith its own Task: line" {
		t.Fatalf("edited task: %q", got)
	}
}

func TestHandEditedPolicyIsSentAsWritten(t *testing.T) {
	base := oldPrompt(2, unixPrefix, quotedCfg, fictionTask)
	edits := map[string]string{
		"word changed":       strings.Replace(base, "Honor any newer", "Honour any newer", 1),
		"paragraph added":    strings.Replace(base, "\n\nTask:\n", "\n\nAlways run the tests.\n\nTask:\n", 1),
		"two config paths":   strings.Replace(base, " await-user -c "+quotedCfg, " await-user -c '/other.yaml'", 1),
		"two prefixes":       strings.Replace(base, unixPrefix+" await-user", "'/other' await-user", 1),
		"no task line":       strings.Replace(base, "Task:\n", "", 1),
		"text before policy": "Read me first.\n" + base,
		"not a policy":       "Keep going on the fictional task.",
	}
	for name, text := range edits {
		if _, ok := MatchPolicy(text); ok {
			t.Errorf("%s: matched", name)
		}
		if got, _, ok := CurrentWording(text); ok || got != text {
			t.Errorf("%s: changed", name)
		}
		if s := DescribePolicy(text, true); s.Source != PolicyCustom || s.CurrentVersion != CurrentPolicyVersion() {
			t.Errorf("%s: %+v", name, s)
		}
	}
	if s := DescribePolicy(base, false); s.Source != PolicyInline {
		t.Errorf("inline: %+v", s)
	}
	if s := DescribePolicy(base, true); s.Source != PolicyBuiltIn || s.FileVersion != 2 {
		t.Errorf("built in: %+v", s)
	}
}

func TestComposeWakeSendsCurrentPolicyWithoutRewritingTheFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "prompt.md")
	old := oldPrompt(1, unixPrefix, quotedCfg, fictionTask)
	if err := os.WriteFile(file, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	loop := config.Loop{Name: "fictional", StateDir: dir, PromptFile: file}
	m, err := ComposeWake(loop)
	if err != nil {
		t.Fatal(err)
	}
	if m.Text != strings.TrimSpace(GeneratedPrompt(unixPrefix, quotedCfg, fictionTask)) {
		t.Fatalf("sent:\n%s", m.Text)
	}
	if b, _ := os.ReadFile(file); string(b) != old {
		t.Fatal("prompt file was rewritten")
	}
	// A prompt set in the configuration is never treated as generated.
	inline := config.Loop{Name: "fictional", StateDir: dir, Prompt: strings.TrimSpace(old)}
	if m, err := ComposeWake(inline); err != nil || m.Text != strings.TrimSpace(old) {
		t.Fatalf("inline prompt changed: %v", err)
	}
}
