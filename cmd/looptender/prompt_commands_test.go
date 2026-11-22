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

// layout is the normal install: ~/.local/bin/looptender -> current -> builds/<id>,
// the old keepgoing link name, an older build left in another loop's directory,
// and a program that no longer exists.
type layout struct {
	home, current, link, oldLink, oldBuild, missing, cfg string
}

func newLayout(t *testing.T) layout {
	t.Helper()
	home := t.TempDir()
	build := filepath.Join(home, ".local", "share", "looptender", "builds", "abc123def456")
	if err := os.MkdirAll(build, 0o700); err != nil {
		t.Fatal(err)
	}
	cur := filepath.Join(build, "looptender")
	if err := os.WriteFile(cur, []byte("#!/bin/sh\n# current build\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cdir := filepath.Join(home, ".local", "share", "looptender", "current")
	if err := os.Symlink(build, cdir); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	link, oldLink := filepath.Join(bin, "looptender"), filepath.Join(bin, "keepgoing")
	if err := os.Symlink(filepath.Join(cdir, "looptender"), link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(cdir, "keepgoing-missing-name"), oldLink); err != nil {
		t.Fatal(err)
	}
	// The old name links to the same binary.
	os.Remove(oldLink)
	if err := os.Symlink(link, oldLink); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home, ".config", "keepgoing", "other-loop")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(other, "keepgoing")
	if err := os.WriteFile(old, []byte("#!/bin/sh\n# an older build, longer than the current one\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-14 * 24 * time.Hour)
	os.Chtimes(old, past, past)
	return layout{home: home, current: cur, link: link, oldLink: oldLink, oldBuild: old, missing: filepath.Join(home, ".config", "keepgoing", "gone", "keepgoing"), cfg: filepath.Join(home, "loops", "example", "looptender.yaml")}
}

func (l layout) promptFor(t *testing.T, body string) config.Loop {
	t.Helper()
	file := filepath.Join(filepath.Dir(l.cfg), "prompt.md")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Loop{PromptFile: file}
}

func handEdited(program, cfg string) string {
	return "Keep the fictional widget working. Finish it, then run:\n" +
		"  '" + program + "' complete -c '" + cfg + "' --note 'done'\n" +
		"If you need a person:\n  " + program + " await-user -c " + cfg + " --note 'help'\n"
}

func TestHandEditedPromptNamingAnotherProgramIsFlaggedNotRewritten(t *testing.T) {
	l := newLayout(t)
	defer func(f func() string) { promptExecutable = f }(promptExecutable)
	promptExecutable = func() string { return l.current }
	loop := l.promptFor(t, handEdited(l.oldBuild, l.cfg))
	before, _ := os.ReadFile(loop.PromptFile)
	s := describeWakePolicy(loop, l.cfg)
	if s == nil || s.Source != prompt.PolicyCustom || len(s.CommandPrograms) != 1 {
		t.Fatalf("%+v", s)
	}
	f := s.CommandPrograms[0]
	if f.Path != l.oldBuild || f.Verdict != prefixOtherBuild || f.Count != 2 || !strings.Contains(f.LastChanged, "14 days") && !strings.Contains(f.LastChanged, "13 days") {
		t.Fatalf("%+v", f)
	}
	for _, want := range []string{"is not the program running now", "does not read a newer state directory safely", "state format", "were not run to ask their version", "Change those commands by hand", "never rewrites a prompt that is not generated wording"} {
		if !strings.Contains(s.Message, want) {
			t.Errorf("message lacks %q:\n%s", want, s.Message)
		}
	}
	if after, _ := os.ReadFile(loop.PromptFile); string(after) != string(before) {
		t.Fatal("a hand-edited prompt was rewritten")
	}
}

func TestPromptNamingAProgramThatDoesNotExistIsFlagged(t *testing.T) {
	l := newLayout(t)
	defer func(f func() string) { promptExecutable = f }(promptExecutable)
	promptExecutable = func() string { return l.current }
	s := describeWakePolicy(l.promptFor(t, handEdited(l.missing, l.cfg)), l.cfg)
	if s == nil || len(s.CommandPrograms) != 1 || s.CommandPrograms[0].Verdict != prefixMissing || !strings.Contains(s.Message, "does not exist or is not executable") {
		t.Fatalf("%+v", s)
	}
}

// The normal layout, and the old link name, are not flagged.
func TestNormalInstallLayoutIsNotFlagged(t *testing.T) {
	l := newLayout(t)
	defer func(f func() string) { promptExecutable = f }(promptExecutable)
	promptExecutable = func() string { return l.current }
	for _, program := range []string{l.link, l.oldLink, l.current, filepath.Join(l.home, ".local", "share", "looptender", "current", "looptender")} {
		for _, body := range []string{handEdited(program, l.cfg), "Run `" + program + "` doctor sometimes."} {
			if s := describeWakePolicy(l.promptFor(t, body), l.cfg); s == nil || len(s.CommandPrograms) != 0 || strings.Contains(s.Message, "other programs") {
				t.Errorf("%s: %+v", program, s)
			}
		}
	}
	// A copy of the same bytes elsewhere is the same program.
	copyPath := filepath.Join(l.home, "copy", "looptender")
	os.MkdirAll(filepath.Dir(copyPath), 0o700)
	b, _ := os.ReadFile(l.current)
	os.WriteFile(copyPath, b, 0o700)
	if s := describeWakePolicy(l.promptFor(t, handEdited(copyPath, l.cfg)), l.cfg); len(s.CommandPrograms) != 0 {
		t.Errorf("a copy of the same bytes: %+v", s.CommandPrograms)
	}
	// Prose that mentions no program, a relative word, and another loop's config are not findings.
	for _, body := range []string{"Finish the task. Ask a person with await-user when stuck.", "run ./looptender complete", "'" + l.oldBuild + "' complete -c '/somewhere/else.yaml'"} {
		s := describeWakePolicy(l.promptFor(t, body), l.cfg)
		// Only the last names an absolute program by its name, which is flagged on the name alone.
		if strings.Contains(body, "else.yaml") != (len(s.CommandPrograms) == 1) {
			t.Errorf("%q: %+v", body, s.CommandPrograms)
		}
	}
}

// A program with another name is found by its invocation with this loop's config.
func TestAnyProgramRunWithThisLoopsConfigIsFound(t *testing.T) {
	l := newLayout(t)
	defer func(f func() string) { promptExecutable = f }(promptExecutable)
	promptExecutable = func() string { return l.current }
	odd := filepath.Join(l.home, "tools", "kg")
	os.MkdirAll(filepath.Dir(odd), 0o700)
	os.WriteFile(odd, []byte("#!/bin/sh\n# something else\n"), 0o700)
	s := describeWakePolicy(l.promptFor(t, "Then run "+odd+" complete -c "+l.cfg+" --note x"), l.cfg)
	if s == nil || len(s.CommandPrograms) != 1 || s.CommandPrograms[0].Path != odd {
		t.Fatalf("%+v", s)
	}
	// Without this loop's config the same word is not taken for a looptender program.
	if s := describeWakePolicy(l.promptFor(t, "Then run "+odd+" complete -c /other/looptender.yaml"), l.cfg); len(s.CommandPrograms) != 0 {
		t.Fatalf("%+v", s.CommandPrograms)
	}
}

// A generated prompt: the prefix is reported once, as before, and the same
// path elsewhere in the task text is counted; another program in the task is found.
func TestGeneratedPromptReportsThePrefixOnceAndOtherProgramsInTheTask(t *testing.T) {
	l := newLayout(t)
	defer func(f func() string) { promptExecutable = f }(promptExecutable)
	promptExecutable = func() string { return l.current }
	text := prompt.GeneratedPrompt("'"+l.oldBuild+"'", "'"+l.cfg+"'", "Fictional task. Earlier notes ran '"+l.missing+"' complete by hand.")
	s := describeWakePolicy(l.promptFor(t, text), l.cfg)
	if s == nil || s.CommandPrefix != prefixOtherBuild {
		t.Fatalf("%+v", s)
	}
	if len(s.CommandPrograms) != 1 || s.CommandPrograms[0].Path != l.missing {
		t.Fatalf("the prefix program is reported once, and only the task's other program is listed: %+v", s.CommandPrograms)
	}
	if !strings.Contains(s.Message, "--refresh-prefix") {
		t.Fatalf("%s", s.Message)
	}
}

// An inline prompt (loop.prompt) is read too.
func TestInlinePromptIsReadToo(t *testing.T) {
	l := newLayout(t)
	defer func(f func() string) { promptExecutable = f }(promptExecutable)
	promptExecutable = func() string { return l.current }
	s := describeWakePolicy(config.Loop{Prompt: handEdited(l.oldBuild, l.cfg)}, l.cfg)
	if s == nil || len(s.CommandPrograms) != 1 {
		t.Fatalf("%+v", s)
	}
}
