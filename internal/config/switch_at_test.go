package config

import (
	"strings"
	"testing"
)

func TestSwitchAtConfigValidation(t *testing.T) {
	base := `
version: 1
loop:
  name: example-loop
  state_dir: /fictional/state
  adapter: bb
  provider: claude-code
  bb: { thread: thr_example }
  prompt: continue
  fallbacks: /fictional/fallbacks.yaml
`
	t.Run("off by default", func(t *testing.T) {
		cfg, err := Parse([]byte(base))
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if cfg.Loop.SwitchAt != 0 || cfg.Loop.PrimaryCandidate().SwitchAt != 0 {
			t.Fatalf("expected SwitchAt 0 by default, got %v", cfg.Loop.SwitchAt)
		}
	})

	t.Run("valid threshold", func(t *testing.T) {
		cfg, err := Parse([]byte(base + "  switch_at: 0.85\n"))
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if cfg.Loop.SwitchAt != 0.85 || cfg.Loop.PrimaryCandidate().SwitchAt != 0.85 {
			t.Fatalf("SwitchAt = %v", cfg.Loop.SwitchAt)
		}
	})

	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "negative",
			yaml: base + "  switch_at: -0.1\n",
			want: "loop.switch_at",
		},
		{
			name: "above 1",
			yaml: base + "  switch_at: 1.01\n",
			want: "loop.switch_at",
		},
		{
			name: "NaN",
			yaml: base + "  switch_at: .nan\n",
			want: "loop.switch_at",
		},
		{
			name: "requires route list",
			yaml: `
version: 1
loop:
  name: example-loop
  state_dir: /fictional/state
  adapter: bb
  provider: claude-code
  bb: { thread: thr_example }
  prompt: continue
  switch_at: 0.9
`,
			want: "loop.switch_at needs",
		},
		{
			name: "rejected for observed target",
			yaml: `
version: 1
loop:
  name: example-loop
  state_dir: /fictional/state
  adapter: sessionpipe
  sessionpipe: { session: codex:example, events_file: /fictional/events.jsonl, stale_after: 1h }
  switch_at: 0.9
`,
			want: "not supported",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse() err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFallbacksAndRoutesSwitchAt(t *testing.T) {
	fbYAML := `
version: 1
candidates:
  - id: alt
    adapter: bb
    provider: claude-code
    group: alt-group
    bb: { thread: thr_alt }
    switch_at: 0.9
`
	fb, err := ParseFallbacks([]byte(fbYAML))
	if err != nil {
		t.Fatalf("ParseFallbacks() error = %v", err)
	}
	if fb.Candidates[0].SwitchAt != 0.9 {
		t.Fatalf("Candidate.SwitchAt = %v", fb.Candidates[0].SwitchAt)
	}
	if _, err := ParseFallbacks([]byte(strings.ReplaceAll(fbYAML, "0.9", "1.5"))); err == nil {
		t.Fatal("expected ParseFallbacks to reject switch_at > 1")
	}

	rtYAML := `
version: 1
routes:
  - id: r1
    provider: claude-code
    model: claude-sonnet-4-5
    group: alt-group
    enter: new_conversation
    switch_at: 0.8
`
	rt, err := ParseRoutes([]byte(rtYAML))
	if err != nil {
		t.Fatalf("ParseRoutes() error = %v", err)
	}
	if rt.Routes[0].SwitchAt != 0.8 {
		t.Fatalf("Route.SwitchAt = %v", rt.Routes[0].SwitchAt)
	}
	if _, err := ParseRoutes([]byte(strings.ReplaceAll(rtYAML, "0.8", "-0.2"))); err == nil {
		t.Fatal("expected ParseRoutes to reject switch_at < 0")
	}
}
