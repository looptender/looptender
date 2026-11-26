package bb

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestContinuationTitleMetadata(t *testing.T) {
	const fallback = "keepgoing: example-loop (continued, 0a1b2c3d)"
	const useful = "From “Repair the example index” · " + fallback
	for _, tc := range []struct {
		name, out string
		fail      bool
		want      string
	}{
		{"custom source name", `{"thread":{"id":"thr_source","title":"Repair the example index","titleFallback":"Ignore this fallback"}}`, false, useful},
		{"fallback name", `{"thread":{"id":"thr_source","title":null,"titleFallback":"Repair the example index"}}`, false, useful},
		{"default name", `{"thread":{"id":"thr_source","title":"Continue from @thread:thr_before.","titleFallback":"Repair the example index"}}`, false, useful},
		{"generic fallback", `{"thread":{"id":"thr_source","titleFallback":"Continue from @thread:thr_before."}}`, false, fallback},
		{"no metadata", `{"thread":{"id":"thr_source"}}`, false, fallback},
		{"empty metadata", `{"thread":{"id":"thr_source","title":"  ","titleFallback":null}}`, false, fallback},
		{"wrong thread", `{"thread":{"id":"thr_other","title":"Repair the example index"}}`, false, fallback},
		{"missing thread", `{}`, false, fallback},
		{"malformed", `{`, false, fallback},
		{"wrong title type", `{"thread":{"id":"thr_source","title":3}}`, false, fallback},
		{"structured error", `{"ok":false,"error":{"code":"thread_not_found","message":"fixture detail"}}`, false, fallback},
		{"command error", `{"thread":{"id":"thr_source","title":"Repair the example index"}}`, true, fallback},
		{"control characters", `{"thread":{"id":"thr_source","title":"Repair\nindex"}}`, false, fallback},
		{"legacy generated", `{"thread":{"id":"thr_source","title":"keepgoing: example-loop (continued, 12345678)"}}`, false, fallback},
		{"successive handoff", `{"thread":{"id":"thr_source","title":"From “Repair the example index” · keepgoing: example-loop (continued, 12345678)"}}`, false, useful},
		{"model is attributed to source", `{"thread":{"id":"thr_source","title":"Coordinator (example-model high)"}}`, false, "From “Coordinator (example-model high)” · " + fallback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newFake(`{}`, `[]`, false)
			calls := 0
			a.run = func(_ context.Context, stdin string, args ...string) ([]byte, error) {
				calls++
				if stdin != "" || !reflect.DeepEqual(args, []string{"thread", "show", "thr_source", "--json"}) {
					t.Fatalf("unexpected call: %q %v", stdin, args)
				}
				if tc.fail {
					return []byte(tc.out), errors.New("fixture failure")
				}
				return []byte(tc.out), nil
			}
			if got := a.ContinuationTitle(context.Background(), "thr_source", fallback); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			if calls != 1 {
				t.Fatalf("calls %d", calls)
			}
		})
	}
}

func TestContinuationTitleBoundsAndInvalidSource(t *testing.T) {
	a, _ := newFake(`{}`, `[]`, false)
	calls := 0
	a.run = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		calls++
		return []byte(`{"thread":{"id":"thr_source","title":"` + strings.Repeat("界", 200) + `"}}`), nil
	}
	const fallback = "keepgoing: example-loop (continued, 0a1b2c3d)"
	if got := a.ContinuationTitle(context.Background(), "--help", fallback); got != fallback || calls != 0 {
		t.Fatalf("invalid source %q calls %d", got, calls)
	}
	want := "From “" + strings.Repeat("界", 120) + "…” · " + fallback
	if got := a.ContinuationTitle(context.Background(), "thr_source", fallback); got != want {
		t.Fatalf("bounded title %q", got)
	}
}

func TestSpawnPreservesExplicitCustomTitle(t *testing.T) {
	a, _ := newFake(`{}`, `[]`, false)
	calls := 0
	a.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls++
		if args[1] != "spawn" {
			t.Fatalf("custom title triggered another action: %v", args)
		}
		for i, arg := range args {
			if arg == "--title" && args[i+1] != "Owner-selected task name" {
				t.Fatalf("custom name changed: %v", args)
			}
		}
		return []byte(`{"id":"thr_successor"}`), nil
	}
	_, err := a.Spawn(context.Background(), SpawnRequest{Place: Place{Project: "proj_example", Environment: "env_example"}, Provider: "codex", Permission: "accept-edits", Title: "Owner-selected task name", Prompt: "fictional task"})
	if err != nil || calls != 1 {
		t.Fatalf("spawn %v calls %d", err, calls)
	}
}
