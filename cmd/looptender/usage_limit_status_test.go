package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/state"
)

// usageCatalogFixture is a catalog with a Claude account in its own
// directory and a Codex account, both fictional.
func usageCatalogFixture(t *testing.T) (catalogDir, claudeDir string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	catalogDir = filepath.Join(root, "catalog")
	claudeDir = filepath.Join(root, "claude-example-a")
	cli := filepath.Join(root, "fixture-cli")
	for _, d := range []string{claudeDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(cli, []byte("fictional stand-in"), 0o700); err != nil {
		t.Fatal(err)
	}
	err = accounts.Update(catalogDir, func(f *accounts.File) error {
		a := accounts.NewEntry(*f, accounts.Entry{Label: "Example Claude A", Provider: "claude-code", Mode: "explicit_directory", Directory: claudeDir, CLI: cli, Workdir: root}, "", time.Now().UTC(), nil)
		f.Entries = append(f.Entries, a)
		c := accounts.NewEntry(*f, accounts.Entry{Label: "Example Codex", Provider: "codex", Mode: "inherited", CLI: cli, Workdir: root}, "", time.Now().UTC(), nil)
		f.Entries = append(f.Entries, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	usageAccountsDir = catalogDir
	t.Cleanup(func() { usageAccountsDir = "" })
	return catalogDir, claudeDir
}

// At a limit on a BB Claude thread, status names the window and its reset,
// the account that reached it (from the Claude directory its check ran
// under), the other configured account with its quota reading, and the two
// things a person can do: the real command on status, placeholders in the
// public form.
func TestUsageLimitSaysWhatIsLimitedAndWhatToDo(t *testing.T) {
	_, claudeDir := usageCatalogFixture(t)
	now := time.Date(2026, 11, 23, 5, 0, 0, 0, time.UTC)
	cfg := &config.Config{}
	cfg.Loop.Agent = config.Agent{Adapter: "bb", Provider: "claude-code", BB: &config.BB{Thread: "thr_example0001"}}
	u := &state.UsageLimit{Target: state.Primary, Provider: "claude-code", Window: decide.CleanLimitWindow("seven_day", "Weekly limit", "seven_day"),
		ResetsAt: now.Add(100 * time.Hour), ObservedAt: now.Add(-time.Minute), ConfigDir: claudeDir}
	st := describeUsageLimit(u, cfg, "", now)
	if st == nil || st.Window != "weekly" || st.Words != "weekly limit (all models)" || st.ResetPassed || st.Account == nil || st.Account.Label != "Example Claude A" || st.Account.Match != "config_dir" {
		t.Fatalf("%+v", st)
	}
	if len(st.Others) != 1 || st.Others[0].Label != "Example Codex" || st.Others[0].SameProvider || st.Others[0].Quota == "" {
		t.Fatalf("others %+v", st.Others)
	}
	if len(st.Actions) != 2 || st.Actions[0].Kind != "login_bb_directory" || st.Actions[0].Command != "CLAUDE_CONFIG_DIR="+claudeDir+" claude auth login" ||
		st.Actions[1].Kind != "arm_takeover" || !strings.Contains(st.Actions[1].Text, "codex") || st.Actions[1].Command != "looptender fallbacks arm -c CONFIG" {
		t.Fatalf("actions %+v", st.Actions)
	}
	pub := st.public()
	raw, _ := json.Marshal(pub)
	if strings.Contains(string(raw), claudeDir) || pub.Actions[0].Command != "CLAUDE_CONFIG_DIR='<BB'\\''s Claude directory>' claude auth login" {
		t.Fatalf("public form carries a path or the wrong command: %s", raw)
	}
	// An armed takeover is not offered again, and a held loop says its limit
	// is from before the hold.
	cfg.FallbackSet = &config.Fallbacks{Candidates: []config.Candidate{{ID: "new-conversation-codex", Agent: config.Agent{Adapter: "bb", Provider: "codex", BB: &config.BB{NewThread: true}}}}}
	if st := describeUsageLimit(u, cfg, "paused", now); len(st.Actions) != 1 || st.Held != "paused" {
		t.Fatalf("%+v", st)
	}
	// No limit, nothing to say.
	if describeUsageLimit(nil, cfg, "", now) != nil {
		t.Fatal("a status without a limit")
	}
}

// The panel list carries the limit with labels and placeholders only.
func TestPanelListUsageLimitCarriesNoPath(t *testing.T) {
	_, claudeDir := usageCatalogFixture(t)
	cfg := &config.Config{}
	cfg.Loop.Agent = config.Agent{Adapter: "bb", Provider: "claude-code", BB: &config.BB{Thread: "thr_example0001"}}
	u := &state.UsageLimit{Target: state.Primary, Provider: "claude-code", Window: decide.CleanLimitWindow("five_hour", "", ""),
		ResetsAt: dockNow.Add(2 * time.Hour), ObservedAt: dockNow.Add(-time.Minute), ConfigDir: claudeDir}
	in := dockEvidence{controls: &clientControls{Enabled: true}}.input()
	in.Client.Snapshot.UsageLimit = describeUsageLimit(u, cfg, "", dockNow) // a private read
	in.Daemon = daemonStatus{State: "responsive"}
	r := buildDockReport([]dockInput{in}, dockNow, time.Minute, defaultStaleAfter, false, true, 0, true)
	l := r.Loops[0]
	if l.State != "waiting_on_limit" || l.Waiting == nil || l.Waiting.Kind != "limit" || !strings.Contains(l.Detail, "5-hour limit") {
		t.Fatalf("%s %q %+v", l.State, l.Detail, l.Waiting)
	}
	raw, _ := json.Marshal(buildPanelList(r))
	row := buildPanelList(r).Loops[0]
	if strings.Contains(string(raw), claudeDir) || row.UsageLimit == nil || row.UsageLimit.Window != "five_hour" || row.UsageLimit.Account == nil || row.UsageLimit.Account.Label != "Example Claude A" {
		t.Fatalf("panel list: %s", raw)
	}
}
