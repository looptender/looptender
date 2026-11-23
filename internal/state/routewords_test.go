package state

import "testing"

func TestDisplayNameFromTitleDropsOnlyTrailingRouteQualifiers(t *testing.T) {
	labels := []string{"codex", "claude-code", "harbor", "lantern"}
	for _, c := range []struct {
		title, want string
		trim        bool
	}{
		// Trailing qualifiers made of route words: dropped whole.
		{"Example coordinator · Sonnet medium", "Example coordinator", true},
		{"Example coordinator (Claude 4)", "Example coordinator", true},
		{"Review PR 478 - Opus 4.5 xhigh", "Review PR 478", true},
		{"Migrate to gpt-5 · codex high", "Migrate to gpt-5", true},
		{"Lane work (Antigravity, Neon high)", "Lane work", true},
		{"Usage check · Ultra 5x", "Usage check", true},
		{"Docs refresh · Harbor", "Docs refresh", true},
		{"Coordinator · 4th conversation", "Coordinator", true},
		{"Coordinator · Opus (Claude 4)", "Coordinator", true},
		// Titles that must not change.
		{"example · widget coordinator (2) (retired, see successor)", "example · widget coordinator (2) (retired, see successor)", false},
		{"example · settings review (advice) (successor of thr_example000)", "example · settings review (advice) (successor of thr_example000)", false},
		{"example coordinator · Harbor (retired, see successor)", "example coordinator · Harbor (retired, see successor)", false},
		{"Widget coordinator · Harbor (successor of thr_example001)", "Widget coordinator · Harbor (successor of thr_example001)", false},
		{"example coordinator · retired to Lantern", "example coordinator · retired to Lantern", false},
		{"Build a recipe app with Claude 3.5", "Build a recipe app with Claude 3.5", false},
		{"Draft release notes with Grok", "Draft release notes with Grok", false},
		{"Compare Ultra 5x and antigravity", "Compare Ultra 5x and antigravity", false},
		{"lane · B: update roadmap v2", "lane · B: update roadmap v2", false},
		{"agree on the scoring model", "agree on the scoring model", false},
		{"Write code", "Write code", false},
		{"Pick a model", "Pick a model", false},
		{"Improve thinking", "Improve thinking", false},
		{"Make the CLI fast", "Make the CLI fast", false},
		{"Speed: fast", "Speed: fast", false},
		{"Plan v2", "Plan v2", false},
		{"Release plan · v2", "Release plan · v2", false},
		{"example coordinator claude 4", "example coordinator claude 4", false},
		// Nothing usable left: the title as it is, never the slug.
		{"Sonnet", "Sonnet", false},
		{"(Claude 4)", "(Claude 4)", false},
	} {
		got, trimmed := DisplayNameFromTitle(c.title, labels)
		if got != c.want || trimmed != c.trim {
			t.Errorf("DisplayNameFromTitle(%q) = %q, %v; want %q, %v", c.title, got, trimmed, c.want, c.trim)
		}
	}
}
