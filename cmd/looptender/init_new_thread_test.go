package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/health"
	"github.com/looptender/looptender/internal/probe"
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
			q := strings.Index(out, "On a usage limit, start a new conversation on codex that takes over this loop")
			plan := strings.Index(out, "On a usage limit on claude-code: a new conversation on codex")
			if q < 0 || plan < 0 || plan > q {
				t.Fatalf("the plan is not said before the question (plan at %d, question at %d):\n%s", plan, q, out)
			}
			for _, want := range []string{"successor threads on codex (at most 5 per handoff)", "At most 3 new conversations", "a paid turn and a send against max_sends", "may stop at a permission question and wait for a person"} {
				if !strings.Contains(out[:q], want) {
					t.Fatalf("before the question, the plan lacks %q:\n%s", want, out[:q])
				}
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
	ran := stubOffer(t, &newThreadOffer{Provider: "codex", Model: "example-codex-model"})
	f := newSetupFixture(t)
	// A script that does not ask runs no bb command, as before.
	if code, out, _ := f.run(t, append(bbInitArgs(f), "--dry-run"), ""); code != 0 || *ran || strings.Contains(out, "Takeover:") {
		t.Fatalf("detected without --detect-takeover: %d %t\n%s", code, *ran, out)
	}
	code, out, errOut := f.run(t, append(bbInitArgs(f), "--dry-run", "--detect-takeover"), "")
	if code != 0 || !strings.Contains(out, "Takeover: not set up. codex is usable") || !strings.Contains(out, "--new-thread-provider codex --new-thread-model example-codex-model") || !strings.Contains(errOut, "looptender fallbacks arm -c") {
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
	ran = stubOffer(t, &newThreadOffer{Provider: "codex", Model: "m"})
	if code, _, _ := f.run(t, append(f.args(), "--dry-run", "--detect-takeover"), ""); code != 0 || *ran {
		t.Fatalf("headless: %d detection ran %t", code, *ran)
	}
	if code, _, _ := f.run(t, append(f.args(), "--dry-run", "--new-thread-provider", "claude-code", "--new-thread-model", "m"), ""); code == 0 {
		t.Fatal("headless accepted a takeover")
	}
}

// Arming turns rate-limit handoff on loop-wide: the loop's other enabled
// fallbacks then take handoffs too, and the arm plan says so.
func TestArmCountsOtherFallbacksGainingHandoff(t *testing.T) {
	other := config.Candidate{ID: "other", Agent: config.Agent{Adapter: "codex"}}
	off := other
	off.ID, off.Disabled = "off", true
	nt := config.Candidate{ID: "new-conversation-codex", Agent: config.Agent{Adapter: "bb", BB: &config.BB{NewThread: true}}}
	cfg := &config.Config{FallbackSet: &config.Fallbacks{Candidates: []config.Candidate{other, off, nt}}}
	if n := otherFallbacksGainingHandoff(cfg); n != 1 {
		t.Fatalf("%d", n)
	}
	cfg.Loop.Errors.FailoverOn = []string{"rate_limit"}
	if n := otherFallbacksGainingHandoff(cfg); n != 0 {
		t.Fatalf("already on: %d", n)
	}
	if n := otherFallbacksGainingHandoff(&config.Config{}); n != 0 {
		t.Fatalf("none: %d", n)
	}
}

// What the loop's own probe records say about the offered provider, with no
// model call: nothing recorded, a usage limit still ahead, a failed check,
// a passed check; and only for fallbacks in the same quota group.
func TestTakeoverReadiness(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Loop.StateDir = dir
	// Another account on codex, in its own quota group, with a fresh limit.
	cfg.FallbackSet = &config.Fallbacks{Candidates: []config.Candidate{{ID: "codex-other-pool", Agent: config.Agent{Adapter: "codex", Provider: "codex", Group: "pool-b"}}}}
	now := time.Now()
	if got := takeoverReadiness(cfg, "codex", "codex", now); !strings.Contains(got, "is not known") || !strings.Contains(got, "checked with a probe only when a handoff happens") {
		t.Fatal(got)
	}
	if got := takeoverReadiness(nil, "codex", "codex", now); !strings.Contains(got, "is not known") {
		t.Fatal(got)
	}
	reset := now.Add(5 * time.Hour)
	other := probe.Result{At: now, ErrorClass: "rate_limit", ResetsAt: &reset}
	save := func(r *probe.Result) {
		reg := &health.Registry{Candidates: map[string]*health.Entry{"codex-other-pool": {Last: &other}, "new-conversation-claude-code": {Last: &probe.Result{At: now, OK: true}}}}
		if r != nil {
			reg.Candidates["new-conversation-codex"] = &health.Entry{Last: r}
		}
		if err := reg.Save(dir); err != nil {
			t.Fatal(err)
		}
	}
	save(nil)
	if got := takeoverReadiness(cfg, "codex", "codex", now); !strings.Contains(got, "is not known") {
		t.Fatalf("another quota group's limit was taken for this one: %s", got)
	}
	if got := takeoverReadiness(cfg, "codex", "pool-b", now); !strings.Contains(got, "codex cannot take over now") {
		t.Fatalf("its own group: %s", got)
	}
	save(&probe.Result{At: now.Add(-time.Hour), ErrorClass: "rate_limit", ResetsAt: &reset})
	if got := takeoverReadiness(cfg, "codex", "codex", now); !strings.Contains(got, "codex cannot take over now") || !strings.Contains(got, reset.UTC().Format(time.RFC3339)) || !strings.Contains(got, "(in 5h 0m)") || !strings.Contains(got, "1h 0m ago") {
		t.Fatal(got)
	}
	past := now.Add(-time.Minute)
	save(&probe.Result{At: now.Add(-3 * time.Hour), ErrorClass: "rate_limit", ResetsAt: &past})
	if got := takeoverReadiness(cfg, "codex", "codex", now); !strings.Contains(got, "failed (rate_limit)") || !strings.Contains(got, "3h 0m ago") {
		t.Fatal(got)
	}
	save(&probe.Result{At: now.Add(-50 * time.Hour), OK: true})
	if got := takeoverReadiness(cfg, "codex", "codex", now); !strings.Contains(got, "passed") || !strings.Contains(got, "2d 2h ago") || !strings.Contains(got, "in quota group codex") {
		t.Fatal(got)
	}
}
