package state

import "testing"

func TestDisplayNameFromTitleDropsOnlyTrailingRouteQualifiers(t *testing.T) {
	labels := []string{"codex", "claude-code", "rocinante", "astra"}
	for _, c := range []struct {
		title, want string
		trim        bool
	}{
		// Trailing qualifiers made of route words: dropped whole.
		{"Looptender coordinator · Sonnet medium", "Looptender coordinator", true},
		{"Looptender coordinator (Claude 4)", "Looptender coordinator", true},
		{"Review PR 478 - Opus 4.5 xhigh", "Review PR 478", true},
		{"Migrate to gpt-5 · codex high", "Migrate to gpt-5", true},
		{"Lane work (Antigravity, Neon high)", "Lane work", true},
		{"Usage check · Ultra 5x", "Usage check", true},
		{"Docs refresh · Rocinante", "Docs refresh", true},
		{"Coordinator · 4th conversation", "Coordinator", true},
		{"Coordinator · Opus (Claude 4)", "Coordinator", true},
		// The reviewer's real titles: unchanged.
		{"looptender · panel coordinator (2) (retired, see successor)", "looptender · panel coordinator (2) (retired, see successor)", false},
		{"looptender · device defaults review (advice) (successor of thr_k48nc2prt7)", "looptender · device defaults review (advice) (successor of thr_k48nc2prt7)", false},
		{"looptender coordinator · Rocinante (retired, see successor)", "looptender coordinator · Rocinante (retired, see successor)", false},
		{"TalkQTE coordinator · Rocinante (successor of thr_m5hefgrih2)", "TalkQTE coordinator · Rocinante (successor of thr_m5hefgrih2)", false},
		{"looptender coordinator · retired to Astra", "looptender coordinator · retired to Astra", false},
		{"Build gamified speech app with Claude 3.5", "Build gamified speech app with Claude 3.5", false},
		{"Automate LinkedIn posts with Grok", "Automate LinkedIn posts with Grok", false},
		{"Integrate Ultra 5x and antigravity", "Integrate Ultra 5x and antigravity", false},
		{"lane · F: adopt roadmap v2", "lane · F: adopt roadmap v2", false},
		{"alignment with the coordinator model", "alignment with the coordinator model", false},
		{"Write code", "Write code", false},
		{"Pick a model", "Pick a model", false},
		{"Improve thinking", "Improve thinking", false},
		{"Make the CLI fast", "Make the CLI fast", false},
		{"Speed: fast", "Speed: fast", false},
		{"Plan v2", "Plan v2", false},
		{"Release plan · v2", "Release plan · v2", false},
		{"looptender coordinator claude 4", "looptender coordinator claude 4", false},
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
