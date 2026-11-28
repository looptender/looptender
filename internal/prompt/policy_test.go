package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
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
	"bd85cf2fbdb683264ff51a67a07502571034766db1b969407c6f1e18fa9dae40",
}

func TestCoordinatorQuestionGuidanceReachesOnlyFollowingPrompts(t *testing.T) {
	old := GeneratedPromptVersion(6, unixPrefix, quotedCfg, fictionTask)
	loop := writePrompt(t, old, true)
	got := sent(t, loop)
	for _, rule := range []string{
		"inventory remaining authorized work, including coordinator-owned and undelegated work",
		"Only the owner thread may withdraw its own now-obsolete question",
		"await-user --withdraw",
		"same owner and generation, unchanged control record and matching last control history",
		"restores any underlying holds",
		"Missing or stale obligation evidence is not proof that all work needs a person",
		"Chat text and child receipts do not automatically release controls",
		"A completed lane, an idle coordinator and a completed supervision loop are different states",
	} {
		if !strings.Contains(got, rule) {
			t.Errorf("following wake lacks %q", rule)
		}
	}
	if strings.Contains(got, "never clear or resume a runtime hold yourself") || !strings.HasSuffix(got, fictionTask) {
		t.Fatal("blanket prohibition or lost task in following prompt")
	}
	if raw, _ := os.ReadFile(loop.PromptFile); string(raw) != old {
		t.Fatal("following wake rewrote the saved prompt")
	}
	// An edited/unrecorded old prompt must not acquire the exception silently.
	unrecorded := writePrompt(t, old, false)
	if got := sent(t, unrecorded); got != strings.TrimSpace(old) || strings.Contains(got, "await-user --withdraw") {
		t.Fatal("unrecorded prompt acquired new guidance")
	}
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
	}
}

// writePrompt writes a loop's prompt file and, when record is set, records
// it as following policy updates, as setup does.
func writePrompt(t *testing.T, text string, record bool) config.Loop {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "prompt.md")
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	loop := config.Loop{Name: "fictional", StateDir: filepath.Join(dir, "state"), PromptFile: file}
	if record {
		if err := RecordPolicy(loop.PromptFile, []byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	return loop
}

func sent(t *testing.T, loop config.Loop) string {
	t.Helper()
	m, err := ComposeWake(loop)
	if err != nil {
		t.Fatal(err)
	}
	return m.Text
}

func TestOnlyTheRecordedFileFollowsPolicyUpdates(t *testing.T) {
	old := oldPrompt(1, unixPrefix, quotedCfg, fictionTask)
	current := strings.TrimSpace(GeneratedPrompt(unixPrefix, quotedCfg, fictionTask))
	// Recorded: the current wording goes, and the file is not rewritten.
	loop := writePrompt(t, old, true)
	if got := sent(t, loop); got != current {
		t.Fatalf("recorded file sent:\n%s", got)
	}
	if b, _ := os.ReadFile(loop.PromptFile); string(b) != old {
		t.Fatal("prompt file was rewritten")
	}
	if s, err := DescribePolicy(loop); err != nil || s.Source != PolicyFollowing || s.FileVersion != 1 || !s.Recorded {
		t.Fatalf("%+v %v", s, err)
	}
	// No record: a file that reads as an old wording is sent as written.
	loop = writePrompt(t, old, false)
	if got := sent(t, loop); got != strings.TrimSpace(old) {
		t.Fatalf("unrecorded file upgraded:\n%s", got)
	}
	if s, _ := DescribePolicy(loop); s.Source != PolicyGenerated || s.FileVersion != 1 || s.Recorded {
		t.Fatalf("%+v", s)
	}
	// Edited after it was recorded, even only in its task: no longer followed.
	loop = writePrompt(t, old, true)
	edited := old + "One more line of task.\n"
	if err := os.WriteFile(loop.PromptFile, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sent(t, loop); got != strings.TrimSpace(edited) {
		t.Fatalf("edited file upgraded:\n%s", got)
	}
	if s, _ := DescribePolicy(loop); s.Source != PolicyGenerated || !s.Recorded {
		t.Fatalf("%+v", s)
	}
	// Hand-edited and recorded nowhere: custom.
	loop = writePrompt(t, "Keep going on the fictional task.", false)
	if s, _ := DescribePolicy(loop); s.Source != PolicyCustom {
		t.Fatalf("%+v", s)
	}
	// A prompt set in the configuration is never treated as generated.
	inline := config.Loop{Name: "fictional", StateDir: t.TempDir(), Prompt: strings.TrimSpace(old)}
	if got := sent(t, inline); got != strings.TrimSpace(old) {
		t.Fatal("inline prompt changed")
	}
	if s, _ := DescribePolicy(inline); s.Source != PolicyInline {
		t.Fatalf("%+v", s)
	}
}

// Deleting a paragraph from a current prompt can make it read like an older
// wording, or like none. Either way it is an edit: the removed guidance must
// not come back.
func TestTrimmedCurrentPromptIsNotUpgraded(t *testing.T) {
	current := GeneratedPrompt(unixPrefix, quotedCfg, fictionTask)
	const delegation = "At the start of substantial work"
	i := strings.Index(current, delegation)
	j := strings.Index(current[i:], "\n\n")
	trimmed := current[:i] + current[i+j+2:]
	if m, ok := MatchPolicy(trimmed); ok && m.Version == CurrentPolicyVersion() {
		t.Fatalf("the trim reads as the current wording: %+v", m)
	}
	for _, record := range []bool{false, true} {
		loop := writePrompt(t, current, record)
		if err := os.WriteFile(loop.PromptFile, []byte(trimmed), 0o600); err != nil {
			t.Fatal(err)
		}
		got := sent(t, loop)
		if strings.Contains(got, delegation) || got != strings.TrimSpace(trimmed) {
			t.Fatalf("record %v: removed paragraph sent again", record)
		}
		if s, _ := DescribePolicy(loop); s.Source == PolicyFollowing {
			t.Fatalf("record %v: trimmed prompt called following: %+v", record, s)
		}
	}
}

func TestPolicyRecordIsStrict(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "prompt.md")
	if r, err := LoadPolicyRecord(file); r != nil || err != nil {
		t.Fatalf("%+v %v", r, err)
	}
	if err := os.WriteFile(PolicyRecordPath(file), []byte(`{"version":1,"sha256":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicyRecord(file); err == nil {
		t.Fatal("malformed record accepted")
	}
	// A malformed record follows nothing.
	loop := config.Loop{Name: "fictional", StateDir: dir, PromptFile: file}
	old := oldPrompt(2, unixPrefix, quotedCfg, fictionTask)
	if err := os.WriteFile(loop.PromptFile, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sent(t, loop); got != strings.TrimSpace(old) {
		t.Fatal("upgraded with a malformed record")
	}
	if err := RecordPolicy(file, []byte(old)); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(PolicyRecordPath(file)); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("%v %v", info, err)
	}
}

// Every wording setup has written is still recognised as itself, so a loop
// whose prompt file an older build wrote is named and can follow updates.
// The current wording calls the product by its current name only.
func TestEveryEarlierWordingIsStillRecognised(t *testing.T) {
	for v := 1; v <= CurrentPolicyVersion(); v++ {
		text := GeneratedPromptVersion(v, unixPrefix, quotedCfg, fictionTask)
		if m, ok := MatchPolicy(text); !ok || m.Version != v || m.Task != fictionTask {
			t.Errorf("version %d: %+v %v", v, m, ok)
		}
	}
	head := PolicyHead(unixPrefix, quotedCfg)
	if !strings.Contains(head, "looptender owns this loop.") || strings.Contains(strings.ToLower(head), "keep"+"going") {
		t.Fatalf("current wording:\n%s", head)
	}
}
