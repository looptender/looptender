package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sessionpipeLoop = `version: 1
loop:
  name: example-observed
  state_dir: state
  adapter: sessionpipe
  sessionpipe:
    session: claude-code:8c132906-8c3f-4814-870a-afc3d05e1d2e
    events_file: sessionpipe/events.jsonl
`

func loadText(t *testing.T, body string) (*Config, error) {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, "loop.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func TestSessionpipeTargetIsStandaloneAndPromptless(t *testing.T) {
	cfg, err := loadText(t, sessionpipeLoop)
	if err != nil {
		t.Fatalf("an observed target without a prompt: %v", err)
	}
	a := cfg.Loop.Agent
	if a.Sessionpipe == nil || a.Sessionpipe.CLI != "sessionpipe" || a.Provider != SessionpipeAdapter || a.Sessionpipe.StaleAfter.Duration != DefaultSessionpipeStaleAfter {
		t.Fatalf("defaults: %+v %+v", a, a.Sessionpipe)
	}
	if !filepath.IsAbs(a.Sessionpipe.EventsFile) || filepath.Base(a.Sessionpipe.EventsFile) != "events.jsonl" {
		t.Fatalf("events_file is not resolved against the configuration: %q", a.Sessionpipe.EventsFile)
	}
	if !ObservedOnly(a.Adapter) || ObservedOnly("claude") {
		t.Fatal("ObservedOnly")
	}
	if !strings.Contains(a.Describe(), "(observed)") {
		t.Fatal(a.Describe())
	}
	for name, tc := range map[string]struct{ extra, want string }{
		"prompt":      {"  prompt: Fictional task.\n", "loop.prompt and loop.prompt_file must not be set"},
		"prompt file": {"  prompt_file: task.md\n", "loop.prompt and loop.prompt_file must not be set"},
		"fallbacks":   {"  fallbacks: fallbacks.yaml\n", "cannot have fallbacks"},
		"switch-back": {"  switch_back:\n    enabled: true\n", "cannot have fallbacks"},
		"timing":      {"  timing:\n    enabled: true\n", "loop.timing is not supported for an observed sessionpipe target"},
		"bad session": {"", "sessionpipe.session must be <harness>:<id>"},
		"no file":     {"", "sessionpipe.events_file is required"},
		"short stale": {"", "sessionpipe.stale_after must be between"},
		"no settings": {"", "sessionpipe"},
	} {
		t.Run(name, func(t *testing.T) {
			body := sessionpipeLoop + tc.extra
			switch name {
			case "bad session":
				body = strings.Replace(body, "claude-code:8c132906", "a title with spaces", 1)
			case "no file":
				body = strings.Replace(body, "    events_file: sessionpipe/events.jsonl\n", "", 1)
			case "short stale":
				body = strings.Replace(sessionpipeLoop, "events.jsonl\n", "events.jsonl\n    stale_after: 30s\n", 1)
			case "no settings":
				body = strings.SplitAfter(body, "  adapter: sessionpipe\n")[0]
			}
			if _, err := loadText(t, body); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	// A sessionpipe target cannot be a fallback candidate either.
	f := Fallbacks{Candidates: []Candidate{{ID: "observed", Agent: Agent{Adapter: SessionpipeAdapter, Sessionpipe: &Sessionpipe{Session: "codex:example", EventsFile: "/example/events.jsonl", StaleAfter: Duration{time.Hour}}}}}}
	if err := f.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be a fallback candidate") {
		t.Fatalf("fallback candidate: %v", err)
	}
}

func TestSessionpipeRefs(t *testing.T) {
	for ref, ok := range map[string]bool{
		"claude-code:8c132906-8c3f-4814-870a-afc3d05e1d2e": true,
		"codex:01a10860-a43f-7ce2-b12f-a7911ae8a880":       true,
		"claude-code":     false,
		":abc":            false,
		"Claude:abc":      false,
		"claude-code:a b": false,
		"claude-code:" + strings.Repeat("a", 129): false,
	} {
		if _, _, got := SplitSessionpipeRef(ref); got != ok {
			t.Errorf("%q: %v, want %v", ref, got, ok)
		}
	}
	h, id, _ := SplitSessionpipeRef("codex:a:b")
	if h != "codex" || id != "a:b" {
		t.Fatal(h, id)
	}
}

func TestSessionpipeSchemaForbidsPromptAndFallback(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schema", "looptender.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	def, ok := s["$defs"].(map[string]any)["sessionpipe"].(map[string]any)
	if !ok || def["additionalProperties"] != false {
		t.Fatal("looptender.schema.json has no closed $defs.sessionpipe")
	}
	if !strings.Contains(string(raw), `"const": "sessionpipe"`) || !strings.Contains(string(raw), `"prompt": false`) {
		t.Fatal("the schema does not forbid a prompt on a sessionpipe target")
	}
	fb, err := os.ReadFile(filepath.Join("..", "..", "schema", "fallbacks.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fb), `"sessionpipe": false`) {
		t.Fatal("fallbacks.schema.json allows a sessionpipe candidate")
	}
}

// send: wake_only opts in to waking, and then the loop needs the text it
// sends; any other value is refused.
func TestSessionpipeSendWakeOnly(t *testing.T) {
	wake := strings.Replace(sessionpipeLoop, "    events_file: sessionpipe/events.jsonl\n", "    events_file: sessionpipe/events.jsonl\n    send: wake_only\n", 1)
	if _, err := loadText(t, wake); err == nil || !strings.Contains(err.Error(), "one of loop.prompt or loop.prompt_file is required") {
		t.Fatalf("wake_only without a prompt: %v", err)
	}
	cfg, err := loadText(t, wake+"  prompt: Fictional wake text.\n")
	if err != nil || !cfg.Loop.Agent.WakesObserved() {
		t.Fatalf("wake_only with a prompt: %v", err)
	}
	if _, err := loadText(t, strings.Replace(wake, "wake_only", "always", 1)+"  prompt: x\n"); err == nil || !strings.Contains(err.Error(), "sessionpipe.send must be wake_only") {
		t.Fatalf("another value: %v", err)
	}
	if cfg, err := loadText(t, sessionpipeLoop); err != nil || cfg.Loop.Agent.WakesObserved() {
		t.Fatalf("the default: %v", err)
	}
}
