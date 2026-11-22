//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/prompt"
)

func promptLoop(t *testing.T, prefix, task string) (config.Loop, string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "prompt.md")
	text := prompt.GeneratedPrompt(prefix, commandQuote(filepath.Join(dir, "loop.yaml")), task)
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Loop{PromptFile: file, StateDir: filepath.Join(dir, "state")}, file
}

func fakeProgram(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n# "+name+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// A wake prompt keeps the command prefix it was written with. Status says
// when that is not the program running now, or does not exist.
func TestWakePolicyFlagsACommandPrefixThatIsNotThisProgram(t *testing.T) {
	current := fakeProgram(t, "current")
	old := fakeProgram(t, "old")
	missing := filepath.Join(t.TempDir(), "gone")
	defer func(f func() string) { promptExecutable = f }(promptExecutable)
	promptExecutable = func() string { return current }
	// A link to the current file is the same program.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(current, link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ prefix, verdict, say string }{
		{commandQuote(current), prefixInstalled, ""},
		{commandQuote(link), prefixInstalled, ""},
		{commandQuote(old), prefixOtherBuild, "not the program file running now"},
		{commandQuote(missing), prefixMissing, "does not exist or is not executable"},
		{"not a path at all with spaces", prefixUnknown, ""},
	} {
		l, _ := promptLoop(t, tc.prefix, "Fictional task")
		s := describeWakePolicy(l)
		if s == nil || s.CommandPrefix != tc.verdict {
			t.Errorf("%q: %+v", tc.prefix, s)
			continue
		}
		if (tc.say == "") != !strings.Contains(s.Message, "--refresh-prefix") || (tc.say != "" && !strings.Contains(s.Message, tc.say)) {
			t.Errorf("%q: %s", tc.prefix, s.Message)
		}
	}
	// An inline prompt, or one setup did not write, has no prefix to judge.
	if s := describeWakePolicy(config.Loop{}); s == nil || s.CommandPrefix != "" {
		t.Errorf("inline: %+v", s)
	}
}

// apply keeps the prefix unless asked; --refresh-prefix previews, and
// --refresh-prefix --yes rewrites only the policy part's commands.
func TestPromptPolicyApplyRefreshesThePrefixOnlyWhenAskedAndOnlyAfterAPreview(t *testing.T) {
	current := fakeProgram(t, "current")
	old := fakeProgram(t, "old")
	defer func(f func() string) { promptExecutable = f }(promptExecutable)
	promptExecutable = func() string { return current }
	task := "Fictional task. Earlier notes ran " + commandQuote(old) + " complete by hand."
	l, file := promptLoop(t, commandQuote(old), task)
	if err := os.MkdirAll(l.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Loop: l}
	cfgPath := filepath.Join(filepath.Dir(file), "loop.yaml")
	before, _ := os.ReadFile(file)
	now := time.Now()

	// Preview: nothing written, the old path in the task counted but untouched.
	r, err := applyPromptPolicy(cfg, cfgPath, now, promptApplyOptions{RefreshPrefix: true, Write: false})
	if err != nil || !r.Preview || !r.Changed || r.TaskOccurrences != 1 || !strings.Contains(r.Message, "Nothing was written") {
		t.Fatalf("%v %+v", err, r)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Fatal("the preview wrote the file")
	}
	if matches, _ := filepath.Glob(file + ".before-policy-*"); len(matches) != 0 {
		t.Fatal("the preview kept a backup")
	}
	// Write: the policy part's commands run the current program; the task is as it was.
	r, err = applyPromptPolicy(cfg, cfgPath, now, promptApplyOptions{RefreshPrefix: true, Write: true})
	if err != nil || r.Preview || !r.Changed || r.TaskOccurrences != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	after, _ := os.ReadFile(file)
	m, ok := prompt.MatchPolicy(string(after))
	if !ok || m.Prefix != commandQuote(current) || !strings.Contains(m.Task, commandQuote(old)) {
		t.Fatalf("%v\n%s", ok, after)
	}
	if matches, _ := filepath.Glob(file + ".before-policy-*"); len(matches) != 1 {
		t.Fatal("the previous file was not kept")
	}
	// And the loop now follows policy updates with commands that run this program.
	if s := describeWakePolicy(l); s == nil || s.CommandPrefix != prefixInstalled || s.Source != prompt.PolicyFollowing {
		t.Fatalf("%+v", s)
	}
}
