package scenario

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// Setting up the automatic takeover, through the built binary: init gives a
// BB loop a new-thread fallback on another usable provider when asked, says
// what happens at a limit, and keeps a single route when no other provider
// is known or the takeover is declined. fallbacks arm gives an existing
// loop the same, after a preview, from its owner or a person only. The
// incident then replays on a loop init set up.

// initCandidate is the fallback init and fallbacks arm write for codex.
const initCandidate = "new-conversation-codex"

// codexUsable makes the stand-in bb report codex as usable, with a default
// model, beside the loop's own claude-code.
func codexUsable(w *World) {
	w.Providers(map[string]string{"claude-code": "example-claude-model", "codex": codexModel})
}

// initJSON runs init --non-interactive --json --dry-run with the flags.
func initJSON(w *World, flags ...string) map[string]any {
	w.t.Helper()
	r := w.Must(append([]string{"init", "--non-interactive", "--json", "--dry-run", "--name", "example-loop", "--agent", "bb", "--thread", primaryThread,
		"--provider", "claude-code", "--project", w.Project, "--directory", filepath.Join(w.Root, "preview"), "--task", scenarioTask}, flags...)...)
	var v map[string]any
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		w.t.Fatalf("init --json: %v\n%s", err, r.Stdout)
	}
	return v
}

// loopFallbacks reads the fallbacks file the configuration names, or nil.
func loopFallbacks(w *World) (failoverOn []any, candidates []map[string]any) {
	w.t.Helper()
	var cfg struct {
		Loop map[string]any `yaml:"loop"`
	}
	if err := yaml.Unmarshal([]byte(readFile(w, w.Config)), &cfg); err != nil {
		w.t.Fatal(err)
	}
	loop := cfg.Loop
	errs, _ := loop["errors"].(map[string]any)
	failoverOn, _ = errs["failover_on"].([]any)
	name, _ := loop["fallbacks"].(string)
	if name == "" {
		return failoverOn, nil
	}
	var fb struct {
		Candidates []map[string]any `yaml:"candidates"`
	}
	if err := yaml.Unmarshal([]byte(readFile(w, filepath.Join(filepath.Dir(w.Config), name))), &fb); err != nil {
		w.t.Fatal(err)
	}
	return failoverOn, fb.Candidates
}

// No other provider known: a single route, as before, and the warning names
// the command that arms the loop later.
func TestScenarioInitWithoutAnotherProvider(t *testing.T) {
	t.Parallel()
	w := NewWorld(t, scenarioStart)
	if v := initJSON(w); v["newThreadOffer"] != nil || len(v["fallbacks"].([]any)) != 0 {
		t.Fatalf("offer or fallback without a usable provider: %v", v)
	}
	r := w.InitWith()
	on, cands := loopFallbacks(w)
	if len(cands) != 0 || len(on) != 0 || !strings.Contains(r.Stderr, "single route") || !strings.Contains(r.Stderr, "keepgoing fallbacks arm -c") {
		t.Fatalf("failover_on %v candidates %v\n%s", on, cands, r.Stderr)
	}
}

// Another provider usable: a script is told about it and not given it; it
// gets it by asking, and declines it with none.
func TestScenarioInitOffersTheTakeoverAndScriptsChoose(t *testing.T) {
	t.Parallel()
	w := NewWorld(t, scenarioStart)
	codexUsable(w)
	v := initJSON(w)
	offer, _ := v["newThreadOffer"].(map[string]any)
	if offer["provider"] != "codex" || offer["model"] != codexModel || len(v["fallbacks"].([]any)) != 0 {
		t.Fatalf("offer: %v fallbacks %v", offer, v["fallbacks"])
	}
	if v := initJSON(w, "--new-thread-provider", "none"); v["newThreadOffer"] != nil || len(v["fallbacks"].([]any)) != 0 {
		t.Fatalf("declined: %v", v)
	}
	v = initJSON(w, "--new-thread-provider", "codex", "--new-thread-model", codexModel)
	fbs := v["fallbacks"].([]any)
	if len(fbs) != 1 || v["newThreadOffer"] != nil {
		t.Fatalf("asked: %v", v)
	}
	nt, _ := fbs[0].(map[string]any)["newThread"].(map[string]any)
	if nt["provider"] != "codex" || nt["model"] != codexModel || nt["permission"] != "accept-edits" || nt["childrenFollow"] != true || nt["successors"] != true ||
		!strings.Contains(nt["text"].(string), "a new conversation on codex") {
		t.Fatalf("newThread: %v", nt)
	}
	limits, _ := v["limits"].(map[string]any)
	if on, _ := limits["failoverOn"].([]any); len(on) != 1 || on[0] != "rate_limit" {
		t.Fatalf("failoverOn: %v", limits)
	}
	// A provider that is not another one, or no model, is refused.
	for _, bad := range [][]string{{"--new-thread-provider", "claude-code", "--new-thread-model", "m"}, {"--new-thread-provider", "codex"}} {
		r := w.Run(append([]string{"init", "--non-interactive", "--dry-run", "--name", "example-loop", "--agent", "bb", "--thread", primaryThread,
			"--provider", "claude-code", "--project", w.Project, "--directory", filepath.Join(w.Root, "bad"), "--task", scenarioTask}, bad...)...)
		if r.Code == 0 {
			t.Fatalf("%v accepted", bad)
		}
	}
}

// The incident on a loop init set up with the takeover: the handoff starts
// one conversation on codex, which owns the loop and takes the coordinator's
// children; the coordinator's commands are refused.
func TestScenarioIncidentOnAnInitTakeover(t *testing.T) {
	t.Parallel()
	w := NewWorld(t, scenarioStart)
	codexUsable(w)
	r := w.InitWith("--new-thread-provider", "codex", "--new-thread-model", codexModel)
	if !strings.Contains(r.Stdout, "On a usage limit on claude-code: a new conversation on codex ("+codexModel+", permission accept-edits) is started with a handoff brief and owns the loop") ||
		!strings.Contains(r.Stdout, "child lanes move to it") || !strings.Contains(r.Stdout, "--new-thread-provider none") {
		t.Fatalf("init does not say what happens:\n%s", r.Stdout)
	}
	if on, cands := loopFallbacks(w); len(cands) != 1 || cands[0]["id"] != initCandidate || len(on) != 1 {
		t.Fatalf("failover_on %v candidates %v", on, cands)
	}
	w.EditConfig(func(l map[string]any) {
		l["report"] = map[string]any{"events": incidentEvents, "timeout": "5s"}
	})
	s := w.record("enable as owner", w.RunAs(primaryThread, "enable", "-c", w.Config), nil)
	w.Check(s, "enable", all(exit(s, 0), ownerIs(w, primaryThread, 1)))
	w.Advance(time.Minute)
	s = w.Tick()
	w.Check(s, "first wake", action(s, "wake"))
	w.Advance(5 * time.Minute)
	startLanes(w)
	w.Advance(5 * time.Minute)
	hitLimit(w)
	w.Advance(time.Minute)
	s = w.Tick()
	w.Check(s, "handoff to a conversation on codex", all(
		action(s, "handoff"), spawns(w, 1), targetIs(s, initCandidate, successor), newThreads(s, 1),
		ownerIs(w, successor, 2), childrenOf(w, successor, lanes...), childrenOf(w, primaryThread), noSendsToLanes(w),
	))
	if len(s.Sent) == 1 {
		w.Check(s, "the first prompt", check(strings.Contains(s.Sent[0].Message, `this conversation owns the "example-loop" loop`), "does not say ownership follows"))
	}
	oldThreadRefused(w)
}

// fallbacks arm: previews, cancels without a yes, writes with one, says an
// armed loop is armed, and is refused to a thread that does not own the loop.
func TestScenarioFallbacksArm(t *testing.T) {
	t.Parallel()
	w := NewWorld(t, scenarioStart)
	codexUsable(w)
	w.InitWith("--new-thread-provider", "none")
	before := readFile(w, w.Config)
	r := w.Run("fallbacks", "arm", "-c", w.Config, "--dry-run")
	if r.Code != 0 || !strings.Contains(r.Stdout, "a new conversation on codex ("+codexModel) || !strings.Contains(r.Stdout, "Dry run: nothing written.") || readFile(w, w.Config) != before {
		t.Fatalf("dry run %d:\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	r = w.Run("fallbacks", "arm", "-c", w.Config) // no answer: declined
	if r.Code != 0 || !strings.Contains(r.Stdout, "Cancelled: nothing written.") || readFile(w, w.Config) != before {
		t.Fatalf("declined %d:\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	// Owned by the coordinator: another thread may not arm it.
	w.Must("pause", "-c", w.Config)
	s := w.record("enable as owner", w.RunAs(primaryThread, "enable", "-c", w.Config), nil)
	w.Check(s, "enable", exit(s, 0))
	// An enabled single-route loop: doctor names the command.
	if r := w.Run("doctor", "--offline", "--json", "-c", w.Config); !strings.Contains(r.Stdout, "keepgoing fallbacks arm -c") {
		t.Fatalf("doctor does not name the command:\n%s", r.Stdout)
	}
	if r := w.RunAs("thr_example_other", "fallbacks", "arm", "-c", w.Config, "--yes"); r.Code == 0 {
		t.Fatalf("armed by a thread that does not own the loop:\n%s", r.Stdout)
	}
	if _, cands := loopFallbacks(w); len(cands) != 0 {
		t.Fatalf("refused arm wrote %v", cands)
	}
	r = w.RunAs(primaryThread, "fallbacks", "arm", "-c", w.Config, "--yes")
	on, cands := loopFallbacks(w)
	if r.Code != 0 || len(cands) != 1 || cands[0]["id"] != initCandidate || len(on) != 1 || on[0] != "rate_limit" {
		t.Fatalf("arm %d: %v %v\n%s%s", r.Code, on, cands, r.Stdout, r.Stderr)
	}
	if bb, _ := cands[0]["bb"].(map[string]any); bb["new_thread"] != true || bb["model"] != codexModel {
		t.Fatalf("candidate %v", cands[0])
	}
	if r := w.Run("fallbacks", "arm", "-c", w.Config, "--yes"); r.Code != 0 || !strings.Contains(r.Stdout, "Already armed") {
		t.Fatalf("again: %d\n%s", r.Code, r.Stdout)
	}
}
