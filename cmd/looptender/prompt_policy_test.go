package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/prompt"
)

const policyFixtureTask = "Finish the fictional widget."

// policyFixture is a doctor fixture whose prompt file setup wrote with the
// first wording, as an older build did.
func policyFixture(t *testing.T) (*doctorFixture, string) {
	t.Helper()
	f := newDoctorFixture(t)
	file := filepath.Join(f.root, "prompt.md")
	text := prompt.GeneratedPrompt(`'/fictional/bin/looptender'`, commandQuote(f.config), policyFixtureTask)
	for _, p := range []string{"At the start of substantial work", "After a phase finishes", "For a coordinator, reconcile"} {
		i := strings.Index(text, p)
		j := strings.Index(text[i:], "\n\n")
		text = text[:i] + text[i+j+2:]
	}
	if m, ok := prompt.MatchPolicy(text); !ok || m.Version != 1 {
		t.Fatalf("fixture is not version 1: %+v", m)
	}
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	delete(f.loop, "prompt")
	f.loop["prompt_file"] = file
	f.save(t)
	return f, file
}

func statusWakePolicy(t *testing.T, f *doctorFixture) (*wakePolicyStatus, string) {
	t.Helper()
	cfg, err := config.Load(f.config)
	if err != nil {
		t.Fatal(err)
	}
	var js, text bytes.Buffer
	if status(env{context.Background(), &js, &js}, cfg, time.Now(), true, defaultStaleAfter) != exitOK {
		t.Fatal(js.String())
	}
	if status(env{context.Background(), &text, &text}, cfg, time.Now(), false, defaultStaleAfter) != exitOK {
		t.Fatal(text.String())
	}
	var parsed statusOut
	if err := json.Unmarshal(js.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed.WakePolicy, text.String()
}

func TestStatusAndDoctorSayWhereTheWakePolicyComesFrom(t *testing.T) {
	f, file := policyFixture(t)
	s, text := statusWakePolicy(t, f)
	if s == nil || s.Source != prompt.PolicyBuiltIn || s.FileVersion != 1 || s.CurrentVersion != prompt.CurrentPolicyVersion() || !strings.Contains(text, "standing wake policy: Built in: the prompt file holds version 1") {
		t.Fatalf("%+v\n%s", s, text)
	}
	old, _ := os.ReadFile(file)
	edited := strings.Replace(string(old), "Continue the authorized task below.", "Continue the task below, carefully.", 1)
	if err := os.WriteFile(file, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	s, text = statusWakePolicy(t, f)
	if s == nil || s.Source != prompt.PolicyCustom || !strings.Contains(text, "does not receive policy updates") || !strings.Contains(text, "looptender prompt-policy apply -c CONFIG") {
		t.Fatalf("%+v\n%s", s, text)
	}
	var out bytes.Buffer
	run(context.Background(), []string{"doctor", "-c", f.config, "--json"}, &out, &out)
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(out.String())
	}
	found := false
	for _, c := range report.Checks {
		if c.ID == "wake_policy" {
			found = c.Status == "warning" && strings.Contains(c.Message, "does not receive policy updates")
		}
	}
	if !found {
		t.Fatalf("doctor: %s", out.String())
	}
	if b, _ := os.ReadFile(file); string(b) != edited {
		t.Fatal("status or doctor wrote the prompt file")
	}
}

func TestPromptPolicyApplyKeepsTheTaskAndThePreviousFile(t *testing.T) {
	f, file := policyFixture(t)
	old, _ := os.ReadFile(file)
	// A loop on a built-in wording needs nothing written.
	var out bytes.Buffer
	if code := run(context.Background(), []string{"prompt-policy", "apply", "-c", f.config}, &out, &out); code != exitOK || !strings.Contains(out.String(), "Nothing was written") {
		t.Fatalf("%d %s", code, out.String())
	}
	if b, _ := os.ReadFile(file); !bytes.Equal(b, old) {
		t.Fatal("rewritten")
	}
	edited := strings.Replace(string(old), "Continue the authorized task below.", "Continue the task below, carefully.", 1)
	if err := os.WriteFile(file, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := run(context.Background(), []string{"prompt-policy", "apply", "-c", f.config, "--json"}, &out, &out); code != exitOK {
		t.Fatalf("%d %s", code, out.String())
	}
	var r promptPolicyApplied
	if err := json.Unmarshal(out.Bytes(), &r); err != nil || !r.Changed || r.Backup == "" {
		t.Fatalf("%v %s", err, out.String())
	}
	if b, _ := os.ReadFile(r.Backup); string(b) != edited {
		t.Fatal("previous file not kept")
	}
	b, _ := os.ReadFile(file)
	m, ok := prompt.MatchPolicy(string(b))
	if !ok || m.Version != prompt.CurrentPolicyVersion() || m.Task != policyFixtureTask || m.ConfigPath != commandQuote(f.config) {
		t.Fatalf("%+v %v\n%s", m, ok, b)
	}
	if info, _ := os.Stat(file); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
}

func TestPromptPolicyApplyRefusesWithoutATaskLine(t *testing.T) {
	f, file := policyFixture(t)
	if err := os.WriteFile(file, []byte("Keep going on the fictional widget.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := run(context.Background(), []string{"prompt-policy", "apply", "-c", f.config}, &out, &out); code != exitFailed || !strings.Contains(out.String(), "Nothing was written") {
		t.Fatalf("%d %s", code, out.String())
	}
	if b, _ := os.ReadFile(file); string(b) != "Keep going on the fictional widget.\n" {
		t.Fatal("written")
	}
	entries, _ := os.ReadDir(f.root)
	for _, e := range entries {
		if strings.Contains(e.Name(), "before-policy") {
			t.Fatal("backup written for a refused apply")
		}
	}
}
