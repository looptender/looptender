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
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
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

func doctorWakePolicy(t *testing.T, f *doctorFixture) doctorCheck {
	t.Helper()
	var out bytes.Buffer
	run(context.Background(), []string{"doctor", "-c", f.config, "--json"}, &out, &out)
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(out.String())
	}
	for _, c := range report.Checks {
		if c.ID == "wake_policy" {
			return c
		}
	}
	t.Fatalf("no wake_policy check: %s", out.String())
	return doctorCheck{}
}

func TestStatusAndDoctorSayWhereTheWakePolicyComesFrom(t *testing.T) {
	f, file := policyFixture(t)
	old, _ := os.ReadFile(file)
	// An existing loop: no record. Named, not followed.
	s, text := statusWakePolicy(t, f)
	if s == nil || s.Source != prompt.PolicyGenerated || s.FileVersion != 1 || s.Recorded || !strings.Contains(text, "matches generated wording version 1 but is not recorded as following policy updates") || !strings.Contains(text, "prompt-policy apply -c CONFIG") {
		t.Fatalf("%+v\n%s", s, text)
	}
	if c := doctorWakePolicy(t, f); c.Status != "warning" {
		t.Fatalf("%+v", c)
	}
	// Recorded: followed.
	if err := prompt.RecordPolicy(file, old); err != nil {
		t.Fatal(err)
	}
	s, text = statusWakePolicy(t, f)
	if s == nil || s.Source != prompt.PolicyFollowing || !strings.Contains(text, "Follows policy updates: the prompt file holds version 1") {
		t.Fatalf("%+v\n%s", s, text)
	}
	if c := doctorWakePolicy(t, f); c.Status != "ok" {
		t.Fatalf("%+v", c)
	}
	// Edited after the record: no longer followed.
	edited := strings.Replace(string(old), policyFixtureTask, policyFixtureTask+" Carefully.", 1)
	if err := os.WriteFile(file, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	s, text = statusWakePolicy(t, f)
	if s == nil || s.Source != prompt.PolicyGenerated || !s.Recorded || !strings.Contains(text, "edited after it was recorded") {
		t.Fatalf("%+v\n%s", s, text)
	}
	// Hand-edited policy part.
	custom := strings.Replace(string(old), "Continue the authorized task below.", "Continue the task below, carefully.", 1)
	if err := os.WriteFile(file, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	s, text = statusWakePolicy(t, f)
	if s == nil || s.Source != prompt.PolicyCustom || !strings.Contains(text, "hand-edited") || !strings.Contains(text, "does not receive policy updates") {
		t.Fatalf("%+v\n%s", s, text)
	}
	if c := doctorWakePolicy(t, f); c.Status != "warning" || !strings.Contains(c.Message, "hand-edited") {
		t.Fatalf("%+v", c)
	}
	if b, _ := os.ReadFile(file); string(b) != custom {
		t.Fatal("status or doctor wrote the prompt file")
	}
}

func applyPolicy(t *testing.T, f *doctorFixture, extra ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := run(context.Background(), append([]string{"prompt-policy", "apply", "-c", f.config}, extra...), &out, &out)
	return code, out.String()
}

func TestPromptPolicyApplyRecordsAndKeepsTheFilesPrefix(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	f, file := policyFixture(t)
	old, _ := os.ReadFile(file)
	code, out := applyPolicy(t, f, "--json")
	if code != exitOK {
		t.Fatalf("%d %s", code, out)
	}
	var r promptPolicyApplied
	if err := json.Unmarshal([]byte(out), &r); err != nil || !r.Changed || r.Backup == "" || r.Prefix != `'/fictional/bin/looptender'` {
		t.Fatalf("%v %s", err, out)
	}
	if b, _ := os.ReadFile(r.Backup); !bytes.Equal(b, old) {
		t.Fatal("previous file not kept")
	}
	b, _ := os.ReadFile(file)
	m, ok := prompt.MatchPolicy(string(b))
	if !ok || m.Version != prompt.CurrentPolicyVersion() || m.Task != policyFixtureTask || m.ConfigPath != commandQuote(f.config) || m.Prefix != `'/fictional/bin/looptender'` {
		t.Fatalf("%+v %v\n%s", m, ok, b)
	}
	if info, _ := os.Stat(file); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	if s, _ := statusWakePolicy(t, f); s.Source != prompt.PolicyFollowing {
		t.Fatalf("%+v", s)
	}
	// Again: nothing to rewrite.
	if code, out := applyPolicy(t, f); code != exitOK || !strings.Contains(out, "The file was not changed") {
		t.Fatalf("%d %s", code, out)
	}
	// A hand-edited file gets this command's path, and says so.
	custom := "Keep the fictional widget working.\n\nTask:\n" + policyFixtureTask + "\n"
	if err := os.WriteFile(file, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out = applyPolicy(t, f)
	if code != exitOK || !strings.Contains(out, "its commands run "+initCommandPrefix()) {
		t.Fatalf("%d %s", code, out)
	}
	b, _ = os.ReadFile(file)
	if m, ok := prompt.MatchPolicy(string(b)); !ok || m.Prefix != initCommandPrefix() || m.Task != policyFixtureTask {
		t.Fatalf("%+v %v", m, ok)
	}
}

func TestPromptPolicyApplyRefusals(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	unchanged := func(t *testing.T, f *doctorFixture, file string, want []byte) {
		t.Helper()
		if b, _ := os.ReadFile(file); !bytes.Equal(b, want) {
			t.Fatal("prompt written")
		}
		if _, err := os.Stat(prompt.PolicyRecordPath(file)); !os.IsNotExist(err) {
			t.Fatal("record written")
		}
		entries, _ := os.ReadDir(f.root)
		for _, e := range entries {
			if strings.Contains(e.Name(), "before-policy") {
				t.Fatal("backup written")
			}
		}
	}
	t.Run("no task line", func(t *testing.T) {
		f, file := policyFixture(t)
		text := []byte("Keep going on the fictional widget.\n")
		if err := os.WriteFile(file, text, 0o600); err != nil {
			t.Fatal(err)
		}
		if code, out := applyPolicy(t, f); code != exitFailed || !strings.Contains(out, "Nothing was written") {
			t.Fatalf("%d %s", code, out)
		}
		unchanged(t, f, file, text)
	})
	t.Run("another thread owns the loop", func(t *testing.T) {
		f, file := policyFixture(t)
		old, _ := os.ReadFile(file)
		if err := control.Save(f.state, control.File{Ownership: &control.Ownership{ThreadID: "thr_owner00001", Generation: 1}}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("BB_THREAD_ID", "thr_other00001")
		if code, out := applyPolicy(t, f); code != exitFailed || !strings.Contains(out, "owned by") || !strings.Contains(out, "nothing written") {
			t.Fatalf("%d %s", code, out)
		}
		unchanged(t, f, file, old)
		// The owner may.
		t.Setenv("BB_THREAD_ID", "thr_owner00001")
		if code, out := applyPolicy(t, f); code != exitOK {
			t.Fatalf("owner refused: %d %s", code, out)
		}
	})
	t.Run("a check is running", func(t *testing.T) {
		f, file := policyFixture(t)
		old, _ := os.ReadFile(file)
		l, err := lock.Acquire(f.state)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Release()
		if code, out := applyPolicy(t, f); code != exitFailed || !strings.Contains(out, "a check is running") {
			t.Fatalf("%d %s", code, out)
		}
		unchanged(t, f, file, old)
	})
}
