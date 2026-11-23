package state

import "testing"

func TestDisplayNameFromTitleDropsRouteWords(t *testing.T) {
	for _, c := range []struct {
		title  string
		labels []string
		want   string
		trim   bool
	}{
		{"Looptender coordinator · Sonnet medium", nil, "Looptender coordinator", true},
		{"Looptender coordinator (Claude 4)", nil, "Looptender coordinator", true},
		{"looptender coordinator claude 4", nil, "looptender coordinator", true},
		{"Review PR 478 - Opus 4.5 xhigh", nil, "Review PR 478", true},
		{"Fix (a) thing: archive state", nil, "Fix (a) thing: archive state", false},
		{"Docs refresh · Rocinante", []string{"Rocinante"}, "Docs refresh", true},
		{"Sonnet", nil, "", true},
		{"Plan the release", nil, "Plan the release", false},
		{"Coordinator, 4th conversation", nil, "Coordinator", true},
		{"Migrate to gpt-5 · codex high", nil, "Migrate to", true},
	} {
		got, trimmed := DisplayNameFromTitle(c.title, c.labels)
		if got != c.want || trimmed != c.trim {
			t.Errorf("DisplayNameFromTitle(%q) = %q, %v; want %q, %v", c.title, got, trimmed, c.want, c.trim)
		}
	}
}
