package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	// Account is the accounts catalog entry that reached the limit, as far
	// as it is known (usageAccount.Match says how). Absent when it is not:
	// the route states no account, and no catalog entry has the directory
	// this supervisor was started with.
	Account *usageAccount `json:"account,omitempty"`
	// AccountUnknown says why Account is absent: bb_directory_unknown (a BB
	// route: BB does not say which Claude directory its threads use) or
	// no_match; AccountUnknownText in fixed words.
	AccountUnknown     string `json:"accountUnknown,omitempty"`
	AccountUnknownText string `json:"accountUnknownText,omitempty"`
	// OtherAccounts counts the other catalog entries of a supported
	// provider. The list itself is said once per read, not per loop: in
	// panel list's accountQuotas and the dock report's accounts.
	OtherAccounts int `json:"otherAccounts"`
	// Others is that list, for status's own text only.
	Others  []usageAccount `json:"-"`
	Actions []usageAction  `json:"actions"`
	// configDir is the directory, for private surfaces only.
	configDir string
}

type usageAccount struct {
	// Handle is the account's handle, as in panel list's accountQuotas
	// (never its catalog ID). Label is its catalog label, on private reads
	// only (status, client status --private): a label can be an email
	// address.
	Handle   string `json:"handle"`
	Label    string `json:"label,omitempty"`
	Provider string `json:"provider"`
	// Match is route_account (the route states it: known) or
	// supervisor_directory (the catalog entry for the Claude directory this
	// supervisor was started with: not known to be the thread's; Caveat
	// says why).
	Match        string `json:"match,omitempty"`
	Caveat       string `json:"caveat,omitempty"`
	SameProvider bool   `json:"sameProvider"`
	// Quota is the last saved quota reading in one sentence, and QuotaAt
	// when it was read, on private reads only.
	Quota   string     `json:"quota,omitempty"`
	QuotaAt *time.Time `json:"quotaAt,omitempty"`
}

// usageAction is something a person can do, in fixed words. Command carries
// real paths and belongs on private surfaces (status); PublicCommand has a
// placeholder for them and is what the panel list and the menu show.
type usageAction struct {
	Kind          string `json:"kind"` // login_bb_directory or arm_takeover
	Text          string `json:"text,omitempty"`
	Command       string `json:"command,omitempty"`
	PublicCommand string `json:"publicCommand,omitempty"`
}

const bbClaudeDirPlaceholder = "<BB's Claude directory>"

var usageAccountUnknownText = map[string]string{
	"no_match":             "Its route states no account, and no catalog entry has the Claude directory this supervisor was started with.",
	"bb_directory_unknown": "Its route states no account, and BB does not say which Claude directory its threads run under; no catalog entry has the directory this supervisor was started with either.",
}

const usageLoginText = "Log BB's Claude directory in to another Claude account that has quota, then quit and reopen BB so its Claude threads restart under the new login. This moves every Claude thread BB runs, not only this loop's, and supervisors started from BB threads stop when BB quits: run them as a service first (looptender service install)."

func usageArmText(provider string) string {
	return "An armed takeover would move this work, with its lanes, to a new conversation on " + provider + " at a limit like this one. Arming it is your decision: it starts a conversation on another subscription."
}

// usageSupervisorDirCaveat is why a supervisor_directory match is not known
// to be the account that reached the limit.
const usageSupervisorDirCaveat = "This is the catalog entry for the Claude directory this supervisor was started with, not one the thread is known to run under: BB does not say which directory its threads use, and the directory may since have been logged in to another account."

// describeUsageLimit reads the catalog (never a provider) to say which
// account reached the limit and what else is configured.
func describeUsageLimit(u *state.UsageLimit, cfg *config.Config, held string, now time.Time) *usageLimitStatus {
	if u == nil || u.ResetsAt.IsZero() {
		return nil
	}
	out := &usageLimitStatus{Target: u.Target, Provider: u.Provider, Window: u.Window.Kind(), WindowKey: u.Window.Key, Model: u.Window.Model(), Words: u.Window.Words(),
		ResetsAt: u.ResetsAt, ResetPassed: !u.ResetsAt.After(now), ObservedAt: u.ObservedAt, Held: held, Actions: []usageAction{}, configDir: u.ConfigDir}
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
			out.Account = &usageAccount{Handle: catalog.Handle(e.ID), Label: clip(e.Label, 120), Provider: e.Provider, Match: "route_account", SameProvider: true}
		case stated == "" && u.ConfigDir != "" && e.Mode == "explicit_directory" && e.Directory != "" && filepath.Clean(e.Directory) == u.ConfigDir:
			matched = e.ID
			out.Account = &usageAccount{Handle: catalog.Handle(e.ID), Label: clip(e.Label, 120), Provider: e.Provider, Match: "supervisor_directory", SameProvider: true, Caveat: usageSupervisorDirCaveat}
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
		a := usageAccount{Handle: catalog.Handle(e.ID), Label: clip(e.Label, 120), Provider: e.Provider, SameProvider: e.Provider == out.Provider}
		a.Quota, a.QuotaAt = usageQuota(views, e.ID, now)
		out.Others = append(out.Others, a)
	}
	out.OtherAccounts = len(out.Others)
	sort.SliceStable(out.Others, func(i, j int) bool { return out.Others[i].SameProvider && !out.Others[j].SameProvider })
	if out.Account == nil {
		out.AccountUnknown = "no_match"
		if agent.Adapter == "bb" {
			out.AccountUnknown = "bb_directory_unknown"
		}
		out.AccountUnknownText = usageAccountUnknownText[out.AccountUnknown]
	}
	if agent.Adapter == "bb" && out.Provider == "claude-code" {
		dir := u.ConfigDir
		if dir == "" {
			dir = bbClaudeDirPlaceholder
		}
		out.Actions = append(out.Actions, usageAction{Kind: "login_bb_directory",
			Text:    usageLoginText,
			Command: "CLAUDE_CONFIG_DIR=" + shellQuote(dir) + " claude auth login", PublicCommand: "CLAUDE_CONFIG_DIR=" + shellQuote(bbClaudeDirPlaceholder) + " claude auth login"})
	}
	if other := usageOtherProvider(out.Others, out.Provider); other != "" && !takeoverArmed(cfg) && agent.Adapter == "bb" {
		out.Actions = append(out.Actions, usageAction{Kind: "arm_takeover",
			Text:    usageArmText(other),
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
// One read (status, client status, the dock report, panel list) describes
// every loop at the same time, so the catalog is read once for it: the
// last result is kept for the same time and catalog.
func usageCatalog(now time.Time) (accounts.File, map[string]accounts.QuotaView) {
	usageMemo.Lock()
	defer usageMemo.Unlock()
	if usageMemo.set && usageMemo.now.Equal(now) && usageMemo.dir == usageAccountsDir {
		return usageMemo.f, usageMemo.views
	}
	f, views := usageCatalogRead(now)
	usageMemo.set, usageMemo.now, usageMemo.dir, usageMemo.f, usageMemo.views = true, now, usageAccountsDir, f, views
	return f, views
}

var usageMemo struct {
	sync.Mutex
	set   bool
	now   time.Time
	dir   string
	f     accounts.File
	views map[string]accounts.QuotaView
}

// usageCatalogReads counts real reads, for tests.
var usageCatalogReads int

func usageCatalogRead(now time.Time) (accounts.File, map[string]accounts.QuotaView) {
	usageCatalogReads++
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
	if a := u.Account; a != nil && a.Match == "route_account" {
		fmt.Fprintf(w, "  account at the limit: %s (the account its route states)\n", a.Label)
	} else if a != nil {
		fmt.Fprintf(w, "  account at the limit: probably %s. %s\n", a.Label, a.Caveat)
	} else {
		fmt.Fprintf(w, "  account at the limit: unknown. %s\n", u.AccountUnknownText)
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

// public is the limit without real paths or labels: the commands carry
// placeholders and the account is named by its handle. It is what client
// status, the dock and panel list get. A reader that shows labels takes
// them from panel list's accountQuotas, which has them by the same handle.
func (u *usageLimitStatus) public() *usageLimitStatus {
	if u == nil {
		return nil
	}
	c := *u
	c.configDir, c.Others = "", nil
	if u.Account != nil {
		a := *u.Account
		// The label and the quota reading are in panel list's accountQuotas
		// and the dock report's accounts, by the same handle.
		a.Label, a.Quota, a.QuotaAt = "", "", nil
		c.Account = &a
	}
	c.Actions = make([]usageAction, len(u.Actions))
	for i, a := range u.Actions {
		a.Command = a.PublicCommand
		c.Actions[i] = a
	}
	return &c
}

// compact is the limit as the dock report and panel list carry it on each
// loop: codes only. The words are said once per read, in usageLimitWords,
// so a read of many loops at one limit does not repeat them per loop.
func (u *usageLimitStatus) compact() *usageLimitStatus {
	c := u.public()
	if c == nil {
		return nil
	}
	c.AccountUnknownText = ""
	if c.Account != nil {
		c.Account.Caveat = ""
	}
	for i := range c.Actions {
		c.Actions[i] = usageAction{Kind: c.Actions[i].Kind}
	}
	return c
}

// usageLimitWords are the fixed words behind compact's codes, carried once
// at the top of the dock report and panel list when a loop has a limit.
type usageLimitWords struct {
	// Accounts says where the accounts the handles name are listed.
	Accounts       string                     `json:"accounts"`
	Match          map[string]string          `json:"match"`
	AccountUnknown map[string]string          `json:"accountUnknown"`
	Actions        map[string]usageActionWord `json:"actions"`
}

type usageActionWord struct {
	Text    string `json:"text"`
	Command string `json:"command"`
}

func usageLimitGlossary() *usageLimitWords {
	return &usageLimitWords{
		Accounts: "Each account is named by its handle; the accounts, with labels and their last quota reading, are listed once in accountQuotas (panel list) or accounts (the dock report).",
		Match: map[string]string{
			"route_account":        "The account its route states.",
			"supervisor_directory": usageSupervisorDirCaveat,
		},
		AccountUnknown: usageAccountUnknownText,
		Actions: map[string]usageActionWord{
			"login_bb_directory": {Text: usageLoginText, Command: "CLAUDE_CONFIG_DIR=" + shellQuote(bbClaudeDirPlaceholder) + " claude auth login"},
			"arm_takeover":       {Text: usageArmText("another configured provider"), Command: "looptender fallbacks arm -c CONFIG"},
		},
	}
}
