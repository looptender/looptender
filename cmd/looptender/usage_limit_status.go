package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/state"
)

// A provider usage limit, as status, the panel and the menu say it: which
// window is blocked and until when, which account in the accounts catalog
// reached it, which other configured accounts there are with their last
// quota reading, and what a person can do. Nothing here acts: looptender
// never logs in, switches an account or arms a takeover.

// usageLimitStatus is the limit the last check that observed the active
// target found on it. Absent when there is none.
type usageLimitStatus struct {
	Target   string `json:"target"`
	Provider string `json:"provider,omitempty"`
	// Window is five_hour, weekly (all models), weekly_model (one model's
	// weekly window, named by Model), other, or unknown. WindowKey is the
	// provider's own key; Words the window in one phrase.
	Window    string    `json:"window"`
	WindowKey string    `json:"windowKey,omitempty"`
	Model     string    `json:"model,omitempty"`
	Words     string    `json:"words"`
	ResetsAt  time.Time `json:"resetsAt"`
	// ResetPassed: the reset is behind this read; the next check sees
	// whether the limit lifted.
	ResetPassed bool      `json:"resetPassed,omitempty"`
	ObservedAt  time.Time `json:"observedAt"`
	// Held names the hold in force: a held loop observes nothing, so the
	// limit is from before it.
	Held string `json:"held,omitempty"`
	// Account is the accounts catalog entry that reached the limit: the one
	// the route states (match route_account), or the one whose directory is
	// the CLAUDE_CONFIG_DIR its check ran under (match config_dir). Absent
	// when neither is known.
	Account *usageAccount `json:"account,omitempty"`
	// Others are the other catalog entries of a supported provider, the same
	// provider first, each with its last saved quota reading.
	Others  []usageAccount `json:"others"`
	Actions []usageAction  `json:"actions"`
	// configDir is the directory, for private surfaces only.
	configDir string
}

type usageAccount struct {
	Label        string `json:"label"`
	Provider     string `json:"provider"`
	Match        string `json:"match,omitempty"`
	SameProvider bool   `json:"sameProvider"`
	// Quota is the last saved quota reading in one sentence, and QuotaAt
	// when it was read; absent when none is saved.
	Quota   string     `json:"quota"`
	QuotaAt *time.Time `json:"quotaAt,omitempty"`
}

// usageAction is something a person can do, in fixed words. Command carries
// real paths and belongs on private surfaces (status); PublicCommand has a
// placeholder for them and is what the panel list and the menu show.
type usageAction struct {
	Kind          string `json:"kind"` // login_bb_directory or arm_takeover
	Text          string `json:"text"`
	Command       string `json:"command"`
	PublicCommand string `json:"publicCommand"`
}

const bbClaudeDirPlaceholder = "<BB's Claude directory>"

// describeUsageLimit reads the catalog (never a provider) to say which
// account reached the limit and what else is configured.
func describeUsageLimit(u *state.UsageLimit, cfg *config.Config, held string, now time.Time) *usageLimitStatus {
	if u == nil || u.ResetsAt.IsZero() {
		return nil
	}
	out := &usageLimitStatus{Target: u.Target, Provider: u.Provider, Window: u.Window.Kind(), WindowKey: u.Window.Key, Model: u.Window.Model(), Words: u.Window.Words(),
		ResetsAt: u.ResetsAt, ResetPassed: !u.ResetsAt.After(now), ObservedAt: u.ObservedAt, Held: held, Others: []usageAccount{}, Actions: []usageAction{}, configDir: u.ConfigDir}
	if out.Window == "" {
		out.Window = "unknown"
	}
	agent, stated := usageLimitAgent(cfg, u.Target)
	if out.Provider == "" {
		out.Provider = agent.Provider
	}
	catalog, views := usageCatalog(now)
	matched := ""
	for _, e := range catalog.Entries {
		switch {
		case stated != "" && e.ID == stated:
			matched = e.ID
			out.Account = &usageAccount{Label: e.Label, Provider: e.Provider, Match: "route_account", SameProvider: true}
		case stated == "" && u.ConfigDir != "" && e.Mode == "explicit_directory" && e.Directory != "" && filepath.Clean(e.Directory) == u.ConfigDir:
			matched = e.ID
			out.Account = &usageAccount{Label: e.Label, Provider: e.Provider, Match: "config_dir", SameProvider: true}
		}
		if matched == e.ID {
			out.Account.Quota, out.Account.QuotaAt = usageQuota(views, e.ID, now)
			break
		}
	}
	for _, e := range catalog.Entries {
		if e.ID == matched || (e.Provider != "claude-code" && e.Provider != "codex" && e.Provider != "antigravity") {
			continue
		}
		a := usageAccount{Label: clip(e.Label, 120), Provider: e.Provider, SameProvider: e.Provider == out.Provider}
		a.Quota, a.QuotaAt = usageQuota(views, e.ID, now)
		out.Others = append(out.Others, a)
	}
	sort.SliceStable(out.Others, func(i, j int) bool { return out.Others[i].SameProvider && !out.Others[j].SameProvider })
	if agent.Adapter == "bb" && out.Provider == "claude-code" {
		dir := u.ConfigDir
		if dir == "" {
			dir = bbClaudeDirPlaceholder
		}
		out.Actions = append(out.Actions, usageAction{Kind: "login_bb_directory",
			Text:    "Log BB's Claude directory in to another Claude account that has quota, then quit and reopen BB so its Claude threads restart under the new login. This moves every Claude thread BB runs, not only this loop's, and supervisors started from BB threads stop when BB quits: run them as a service first (looptender service install).",
			Command: "CLAUDE_CONFIG_DIR=" + shellQuote(dir) + " claude auth login", PublicCommand: "CLAUDE_CONFIG_DIR=" + shellQuote(bbClaudeDirPlaceholder) + " claude auth login"})
	}
	if other := usageOtherProvider(out.Others, out.Provider); other != "" && !takeoverArmed(cfg) && agent.Adapter == "bb" {
		out.Actions = append(out.Actions, usageAction{Kind: "arm_takeover",
			Text:    "An armed takeover would move this work, with its lanes, to a new conversation on " + other + " at a limit like this one. Arming it is your decision: it starts a conversation on another subscription.",
			Command: "looptender fallbacks arm -c CONFIG", PublicCommand: "looptender fallbacks arm -c CONFIG"})
	}
	return out
}

// usageLimitAgent is the agent of the target the limit is on, and the
// catalog entry its route states, if any.
func usageLimitAgent(cfg *config.Config, target string) (config.Agent, string) {
	if target == state.Primary || cfg.FallbackSet == nil {
		return cfg.Loop.Agent, cfg.Loop.Agent.Account
	}
	if c, ok := cfg.FallbackSet.Find(target); ok {
		return c.Agent, c.Agent.Account
	}
	return cfg.Loop.Agent, ""
}

func usageOtherProvider(others []usageAccount, provider string) string {
	for _, a := range others {
		if !a.SameProvider && a.Provider != provider {
			return a.Provider
		}
	}
	return ""
}

func takeoverArmed(cfg *config.Config) bool {
	if cfg.FallbackSet == nil {
		return false
	}
	for _, c := range cfg.FallbackSet.Candidates {
		if !c.Disabled && c.BB != nil && c.BB.NewThread {
			return true
		}
	}
	return false
}

// usageAccountsDir is the accounts catalog a usage limit is described
// against: "" is the default catalog; only tests set another.
var usageAccountsDir string

// usageCatalog reads the accounts catalog and its saved quota readings; an
// unreadable catalog is empty.
func usageCatalog(now time.Time) (accounts.File, map[string]accounts.QuotaView) {
	dir, err := accountsDirectory(usageAccountsDir)
	if err != nil {
		return accounts.File{}, nil
	}
	f, err := accounts.Load(dir)
	if err != nil {
		return accounts.File{}, nil
	}
	return f, accounts.QuotaViews(dir, f, os.Environ(), now)
}

func usageQuota(views map[string]accounts.QuotaView, id string, now time.Time) (string, *time.Time) {
	v, ok := views[id]
	if !ok {
		return "No quota reading is saved.", nil
	}
	s := accounts.SummarizeQuota(v, now)
	at := s.ObservedAt
	if at == nil {
		at = s.RetrievedAt
	}
	text := strings.TrimSpace(s.Text)
	if text == "" {
		text = "No quota reading is saved."
	}
	return text, at
}

// printUsageLimit is status's text for it.
func printUsageLimit(w io.Writer, u *usageLimitStatus, now time.Time) {
	if u == nil {
		return
	}
	when := "resets " + u.ResetsAt.UTC().Format(time.RFC3339)
	if u.ResetPassed {
		when = "its reset (" + u.ResetsAt.UTC().Format(time.RFC3339) + ") has passed; the next check sees whether it lifted"
	}
	fmt.Fprintf(w, "usage limit: %s on %s (%s), %s; seen at the check of %s", u.Words, u.Target, u.Provider, when, u.ObservedAt.UTC().Format(time.RFC3339))
	if u.Held != "" {
		fmt.Fprintf(w, "; the loop is held (%s), so this is from before the hold", u.Held)
	}
	fmt.Fprintln(w)
	if a := u.Account; a != nil {
		fmt.Fprintf(w, "  account at the limit: %s (%s)\n", a.Label, map[string]string{"route_account": "the account its route states", "config_dir": "the catalog entry for the Claude directory its check ran under"}[a.Match])
	} else {
		fmt.Fprintln(w, "  account at the limit: unknown (its route states none, and its check did not run under a catalog entry's directory)")
	}
	for _, a := range u.Others {
		at := "no reading saved"
		if a.QuotaAt != nil {
			at = "read " + dockAgo(now, *a.QuotaAt)
		}
		fmt.Fprintf(w, "  other account: %s (%s): %s (%s)\n", a.Label, a.Provider, a.Quota, at)
	}
	for _, x := range u.Actions {
		fmt.Fprintf(w, "  you can: %s\n    %s\n", x.Text, x.Command)
	}
}

// public is the limit without real paths: the commands carry placeholders.
// It is what the panel list, the menu and any non-private read get.
func (u *usageLimitStatus) public() *usageLimitStatus {
	if u == nil {
		return nil
	}
	c := *u
	c.configDir = ""
	c.Actions = make([]usageAction, len(u.Actions))
	for i, a := range u.Actions {
		a.Command = a.PublicCommand
		c.Actions[i] = a
	}
	return &c
}
