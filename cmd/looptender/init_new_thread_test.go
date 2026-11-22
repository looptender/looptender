package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/config"
)

// stubOffer makes the provider detection return offer, and records whether
// it ran.
func stubOffer(t *testing.T, offer *newThreadOffer) *bool {
	t.Helper()
	ran := false
	old := detectNewThreadOffer
	detectNewThreadOffer = func(context.Context, string, func(string) (string, error)) *newThreadOffer {
		ran = true
		return offer
	}
	t.Cleanup(func() { detectNewThreadOffer = old })
	return &ran
}

func bbInitArgs(f setupFixture) []string {
	return append(f.args(), "--agent", "bb", "--thread", "thr_example", "--provider", "claude-code")
}

// The wizard offers the takeover when another provider is usable; yes (the
// default) writes the fallback with handoff on, no leaves a single route.
func TestInitWizardOffersTheTakeover(t *testing.T) {
	for _, answer := range []string{"", "no"} {
		t.Run("answer "+answer, func(t *testing.T) {
			stubOffer(t, &newThreadOffer{Provider: "codex", Model: "example-codex-model"})
			f := newSetupFixture(t)
			args := []string{"--name", "example-loop", "--agent", "bb", "--thread", "thr_example", "--provider", "claude-code", "--group", "claude-code",
				"--project", f.project, "--directory", f.destination, "--task", "Complete the fictional task.", "--with-goal=false", "--routes", "own",
				"--wake-every", "1h", "--stall-after", "30m", "--backoff", "10m", "--interval", "5m", "--max-nudges", "3", "--max-reprompts", "3"}
			input := answer + "\n\n\n\n\n\n"
			code, out, errOut := f.run(t, args, input)
			if code != 0 {
				t.Fatalf("%d %s\n%s", code, errOut, out)
			}
			if !strings.Contains(out, "On a usage limit, start a new conversation on codex that takes over this loop") {
				t.Fatalf("not offered:\n%s", out)
			}
			cfg, err := config.Load(filepath.Join(f.destination, config.FileName))
			if err != nil {
				t.Fatal(err)
			}
			if answer == "no" {
				if cfg.FallbackSet != nil || len(cfg.Loop.Errors.FailoverOn) != 0 {
					t.Fatalf("declined, but %+v", cfg.FallbackSet)
				}
				return
			}
			if cfg.FallbackSet == nil || len(cfg.FallbackSet.Candidates) != 1 || strings.Join(cfg.Loop.Errors.FailoverOn, ",") != "rate_limit" {
				t.Fatalf("%+v %v", cfg.FallbackSet, cfg.Loop.Errors.FailoverOn)
			}
			c := cfg.FallbackSet.Candidates[0]
			if c.ID != "new-conversation-codex" || !c.BB.NewThread || c.BB.Model != "example-codex-model" || c.Provider != "codex" || c.BB.NewThreadPermission() != "accept-edits" ||
				!c.BB.ChildrenFollow() || !c.BB.ReplacesChildren() || c.Probe.Kind != "codex" {
				t.Fatalf("%+v %+v", c, c.BB)
			}
			if !strings.Contains(out, "On a usage limit on claude-code: a new conversation on codex (example-codex-model, permission accept-edits)") {
				t.Fatalf("plan not printed:\n%s", out)
			}
		})
	}
}

// Scripts: no flags means no takeover and a hint; the flags set it up; the
// flags refuse what does not fit.
func TestInitNewThreadFlags(t *testing.T) {
	stubOffer(t, &newThreadOffer{Provider: "codex", Model: "example-codex-model"})
	f := newSetupFixture(t)
	code, out, errOut := f.run(t, append(bbInitArgs(f), "--dry-run"), "")
	if code != 0 || !strings.Contains(out, "Takeover: not set up. codex is usable") || !strings.Contains(out, "--new-thread-provider codex --new-thread-model example-codex-model") || !strings.Contains(errOut, "keepgoing fallbacks arm -c") {
		t.Fatalf("%d\n%s\n%s", code, out, errOut)
	}
	code, out, _ = f.run(t, append(bbInitArgs(f), "--dry-run", "--new-thread-provider", "codex", "--new-thread-model", "m", "--new-thread-permission-mode", "auto"), "")
	if code != 0 || !strings.Contains(out, "a new conversation on codex (m, permission auto)") || strings.Contains(out, "Takeover: not set up") {
		t.Fatalf("%d\n%s", code, out)
	}
	for name, extra := range map[string][]string{
		"own provider":     {"--new-thread-provider", "claude-code", "--new-thread-model", "m"},
		"unknown provider": {"--new-thread-provider", "pi", "--new-thread-model", "m"},
		"no model":         {"--new-thread-provider", "codex"},
		"model alone":      {"--new-thread-model", "m"},
		"bad permission":   {"--new-thread-provider", "codex", "--new-thread-model", "m", "--new-thread-permission-mode", "plan"},
		"with fallback":    {"--new-thread-provider", "codex", "--new-thread-model", "m", "--fallback", "codex:pool"},
	} {
		if code, _, _ := f.run(t, append(bbInitArgs(f), append([]string{"--dry-run"}, extra...)...), ""); code == 0 {
			t.Errorf("%s accepted", name)
		}
	}
	// Headless loops are unchanged: no detection, and the flags are refused.
	ran := stubOffer(t, &newThreadOffer{Provider: "codex", Model: "m"})
	if code, _, _ := f.run(t, append(f.args(), "--dry-run"), ""); code != 0 || *ran {
		t.Fatalf("headless: %d detection ran %t", code, *ran)
	}
	if code, _, _ := f.run(t, append(f.args(), "--dry-run", "--new-thread-provider", "claude-code", "--new-thread-model", "m"), ""); code == 0 {
		t.Fatal("headless accepted a takeover")
	}
}
