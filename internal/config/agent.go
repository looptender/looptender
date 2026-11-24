package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Agent describes how to reach one agent session. The primary loop and
// every fallback candidate use the same shape.
type Agent struct {
	Adapter     string       `yaml:"adapter"`
	Provider    string       `yaml:"provider,omitempty"` // claude-code, codex, or any label
	Group       string       `yaml:"group,omitempty"`    // account or quota pool; defaults to provider
	Account     string       `yaml:"account,omitempty"`  // accounts catalog ID a person states this route uses; never derived
	BB          *BB          `yaml:"bb,omitempty"`
	Process     *Process     `yaml:"process,omitempty"`
	Claude      *Claude      `yaml:"claude,omitempty"`
	Codex       *Codex       `yaml:"codex,omitempty"`
	Cursor      *Cursor      `yaml:"cursor,omitempty"`
	Antigravity *Antigravity `yaml:"antigravity,omitempty"`
	Sessionpipe *Sessionpipe `yaml:"sessionpipe,omitempty"`
	// Custom is an agent defined in configuration.
	Custom *Custom `yaml:"custom,omitempty"`
}

// BB configures the BB thread adapter. Model and Reasoning, when set, are
// applied to the thread before the first message after a handoff.
type BB struct {
	// Thread is an existing thread. A fallback candidate may instead set
	// NewThread, and then has no thread until a handoff creates one.
	Thread string `yaml:"thread,omitempty"`
	// NewThread opts a fallback candidate in to "start a new BB thread on this
	// provider and model" at handoff. It is the only case in which keepgoing
	// creates a thread. Project and Environment, when unset, are taken from the
	// conversation the new thread continues.
	NewThread   bool   `yaml:"new_thread,omitempty"`
	Project     string `yaml:"project,omitempty"`
	Environment string `yaml:"environment,omitempty"`
	// PermissionMode is the permission mode of a thread created for
	// NewThread: accept-edits (the default), auto or full. It is always
	// passed to BB, so the new thread never inherits a project default.
	PermissionMode string `yaml:"permission_mode,omitempty"`
	// MoveChildren, with NewThread, says whether the direct child threads of
	// the conversation being left are moved to the created thread after the
	// handoff (and back on an automatic switch-back). Unset means yes.
	MoveChildren *bool `yaml:"move_children,omitempty"`
	// ReplaceBlockedChildren, with NewThread, says whether child lanes the
	// handoff brought over that are blocked on a usage limit of another
	// provider get successor threads on this candidate's provider. Unset
	// means yes; it needs the children moved. ChildModel and ChildReasoning
	// are the successors' settings; unset, the candidate's own.
	ReplaceBlockedChildren *bool  `yaml:"replace_blocked_children,omitempty"`
	ChildModel             string `yaml:"child_model,omitempty"`
	ChildReasoning         string `yaml:"child_reasoning,omitempty"`
	// ChildPermissionMode is the successors' permission mode: accept-edits
	// unless set, and never above the replaced lane's own when BB reports it.
	ChildPermissionMode string `yaml:"child_permission_mode,omitempty"`
	CLI                 string `yaml:"cli,omitempty"`
	Model               string `yaml:"model,omitempty"`
	Reasoning           string `yaml:"reasoning,omitempty"`
	// RetrySignalTTL bounds how long a provider retry notice on an errored
	// thread suppresses error recovery. Zero uses the adapter default (15m).
	RetrySignalTTL Duration `yaml:"retry_signal_ttl,omitempty"`
	// Children configures what keepgoing may do about the supervised thread's
	// lanes (its direct child threads). Nil, or retry_errored false, is the
	// default: lanes are observed and announced, never acted on.
	Children *BBChildren `yaml:"children,omitempty"`
}

// BBChildren is the opt-in recovery of failed lanes.
type BBChildren struct {
	// RetryErrored lets keepgoing ask BB to retry a lane's failed turn by
	// reference. It is the only action keepgoing ever takes on a lane.
	RetryErrored bool `yaml:"retry_errored,omitempty"`
	// MaxPerCheck is how many lanes one check may work on. Zero means the default.
	MaxPerCheck int `yaml:"max_per_check,omitempty"`
	// MaxPerEpisode is how many lane retries one episode may make, where an
	// episode lasts until every retried lane has stayed out of error for
	// progress.min_wake_interval. Zero means the default.
	MaxPerEpisode int `yaml:"max_per_episode,omitempty"`
}

// Lane recovery defaults and ceilings.
const (
	DefaultLanesPerCheck   = 5
	DefaultLanesPerEpisode = 20
	MaxLanesPerCheck       = 20
	MaxLanesPerEpisode     = 100
)

// RetryErroredLanes reports whether lane recovery is switched on.
func (b *BB) RetryErroredLanes() bool {
	return b != nil && b.Children != nil && b.Children.RetryErrored
}

// LanesPerCheck is the configured or default number of lanes per check.
func (b *BB) LanesPerCheck() int {
	if b == nil || b.Children == nil || b.Children.MaxPerCheck <= 0 {
		return DefaultLanesPerCheck
	}
	return b.Children.MaxPerCheck
}

// LanesPerEpisode is the configured or default number of lane retries per episode.
func (b *BB) LanesPerEpisode() int {
	if b == nil || b.Children == nil || b.Children.MaxPerEpisode <= 0 {
		return DefaultLanesPerEpisode
	}
	return b.Children.MaxPerEpisode
}

// DefaultNewThreadPermission is the permission mode of a thread keepgoing
// creates when the candidate sets none: the most restrictive BB offers.
const DefaultNewThreadPermission = "accept-edits"

// NewThreadPermission is the permission mode a created thread gets.
// ReplacesChildren reports whether a created thread gets successors for the
// blocked child lanes it takes over.
func (b BB) ReplacesChildren() bool {
	return b.NewThread && b.ChildrenFollow() && (b.ReplaceBlockedChildren == nil || *b.ReplaceBlockedChildren)
}

// SuccessorModel is the model and reasoning of successor threads.
func (b BB) SuccessorModel() (model, reasoning string) {
	if b.ChildModel != "" {
		return b.ChildModel, b.ChildReasoning
	}
	return b.Model, b.Reasoning
}

// SuccessorPermission is the successors' permission mode before it is
// lowered to a replaced lane's own.
func (b BB) SuccessorPermission() string {
	if b.ChildPermissionMode != "" {
		return b.ChildPermissionMode
	}
	return DefaultNewThreadPermission
}

// PermissionRank orders BB permission modes from the most restrictive;
// an unknown mode ranks as unknown (-1).
func PermissionRank(mode string) int {
	switch mode {
	case "accept-edits":
		return 0
	case "auto":
		return 1
	case "full":
		return 2
	}
	return -1
}

// ChildrenFollow reports whether a created thread takes the direct children
// of the conversation it continues.
func (b BB) ChildrenFollow() bool { return b.MoveChildren == nil || *b.MoveChildren }

func (b BB) NewThreadPermission() string {
	if b.PermissionMode == "" {
		return DefaultNewThreadPermission
	}
	return b.PermissionMode
}

// Process configures the generic process adapter. Each command is argv.
type Process struct {
	StatusCommand []string          `yaml:"status_command"`
	ResumeCommand []string          `yaml:"resume_command"`
	StopCommand   []string          `yaml:"stop_command,omitempty"`
	ExitCodes     map[int]string    `yaml:"exit_codes,omitempty"`
	Env           map[string]string `yaml:"env,omitempty"`
	Timeout       Duration          `yaml:"timeout,omitempty"`
}

// Claude configures a headless Claude Code session that keepgoing runs
// itself, one turn at a time, resuming the same session each turn.
type Claude struct {
	CLI            string            `yaml:"cli,omitempty"`
	Workdir        string            `yaml:"workdir"`
	Model          string            `yaml:"model,omitempty"`
	Effort         string            `yaml:"effort,omitempty"`
	PermissionMode string            `yaml:"permission_mode,omitempty"`
	MaxBudgetUSD   float64           `yaml:"max_budget_usd,omitempty"`
	ExtraArgs      []string          `yaml:"extra_args,omitempty"`
	Env            map[string]string `yaml:"env,omitempty"`
}

// Codex configures a headless Codex session run with `codex exec`.
type Codex struct {
	CLI              string            `yaml:"cli,omitempty"`
	Workdir          string            `yaml:"workdir"`
	Model            string            `yaml:"model,omitempty"`
	Sandbox          string            `yaml:"sandbox,omitempty"`
	SkipGitRepoCheck bool              `yaml:"skip_git_repo_check,omitempty"`
	ExtraArgs        []string          `yaml:"extra_args,omitempty"`
	Env              map[string]string `yaml:"env,omitempty"`
}

// Cursor configures a primary-only Cursor CLI print session. Permissions remain
// those configured by the user in Cursor; no approval-bypass flags are added.
type Cursor struct {
	CLI     string            `yaml:"cli,omitempty"`
	Workdir string            `yaml:"workdir"`
	Model   string            `yaml:"model,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	// FreshStartVersions lists further CLI versions, beyond the inspected ones,
	// for which the operator has confirmed that the CLI writes its first stream
	// records before it submits a prompt. See docs/cursor.md.
	FreshStartVersions []string `yaml:"fresh_start_versions,omitempty"`
}

// CursorVersionPattern is the only version token shape accepted from the CLI
// or from configuration.
var CursorVersionPattern = regexp.MustCompile(`^[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9a-f]{7,40}$`)

// Antigravity configures a primary-only Antigravity ACP stdio session
// (`agy acp --stdio`). Permissions remain those configured by the user in
// Antigravity; no approval-bypass flags are ever added.
type Antigravity struct {
	CLI     string            `yaml:"cli,omitempty"`
	Workdir string            `yaml:"workdir"`
	Agent   string            `yaml:"agent,omitempty"`
	Sandbox bool              `yaml:"sandbox,omitempty"`
	AddDirs []string          `yaml:"add_dir,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
}

// Sessionpipe configures an observed-only target: one agent session that
// sessionpipe reports, read from a tier-0 events file the person told
// sessionpipe to write (`sessionpipe sink add file:PATH --tier 0`).
// looptender reads that file and nothing else. It never sends to, wakes or
// stops the session, and it never runs sessionpipe during a check.
type Sessionpipe struct {
	// Session is the session as sessionpipe names it: <harness>:<id>.
	Session string `yaml:"session"`
	// EventsFile is the tier-0 file sink's path.
	EventsFile string `yaml:"events_file"`
	// StaleAfter is how long the session may be silent in the events file
	// before its state reads unknown. Silence is never idle.
	StaleAfter Duration `yaml:"stale_after,omitempty"`
	// CLI is the sessionpipe command doctor runs to check the version and
	// the sink, and, with Send, the one a wake runs (`sessionpipe ask`).
	// Observing never runs it.
	CLI string `yaml:"cli,omitempty"`
	// Send opts the loop in to waking the session: "wake_only" lets the
	// decision engine wake it when it is idle, through `sessionpipe ask`.
	// Empty, the default, never sends. Nudges, re-prompts, stops and
	// handoffs stay held either way.
	Send string `yaml:"send,omitempty"`
}

// SessionpipeSendWakeOnly is the one value of sessionpipe.send.
const SessionpipeSendWakeOnly = "wake_only"

// WakesObserved reports whether an observed target of this agent may be
// woken: a sessionpipe target with send: wake_only.
func (a Agent) WakesObserved() bool {
	return a.Sessionpipe != nil && a.Sessionpipe.Send == SessionpipeSendWakeOnly
}

// SessionpipeAdapter is the observed-only target kind.
const SessionpipeAdapter = "sessionpipe"

// DefaultSessionpipeStaleAfter is used when stale_after is unset. sessionpipe
// writes a heartbeat at most once a minute while a turn runs.
const DefaultSessionpipeStaleAfter = 10 * time.Minute

// sessionpipeRefRE is <harness>:<id>: a harness name as sessionpipe's
// protocol allows it and a session id of at most 128 characters.
var sessionpipeRefRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}:[A-Za-z0-9._:-]{1,128}$`)

// SplitSessionpipeRef splits <harness>:<id>; ok is false when s is not one.
func SplitSessionpipeRef(s string) (harness, id string, ok bool) {
	if !sessionpipeRefRE.MatchString(s) {
		return "", "", false
	}
	harness, id, _ = strings.Cut(s, ":")
	return harness, id, true
}

// ObservedOnly reports whether a target of this adapter kind is observed and
// never woken: looptender never sends to it, never re-prompts it and never
// stops it, whatever its state.
func ObservedOnly(adapter string) bool { return adapter == SessionpipeAdapter }

// Adapters lists the supported adapter names.
var Adapters = []string{"bb", "process", "claude", "codex", "cursor", "antigravity", SessionpipeAdapter, CustomAdapter}

func (a *Agent) applyDefaults() {
	if a.Cursor != nil && a.Cursor.CLI == "" {
		a.Cursor.CLI = "agent"
	}
	if a.Antigravity != nil && a.Antigravity.CLI == "" {
		a.Antigravity.CLI = "agy"
	}
	if a.Sessionpipe != nil {
		if a.Sessionpipe.CLI == "" {
			a.Sessionpipe.CLI = "sessionpipe"
		}
		if a.Sessionpipe.StaleAfter.Duration == 0 {
			a.Sessionpipe.StaleAfter.Duration = DefaultSessionpipeStaleAfter
		}
	}
	if a.Provider == "" {
		switch a.Adapter {
		case "claude":
			a.Provider = "claude-code"
		case "codex":
			a.Provider = "codex"
		case "cursor":
			a.Provider = "cursor"
		case "antigravity":
			a.Provider = "antigravity"
		case SessionpipeAdapter:
			a.Provider = SessionpipeAdapter
		case CustomAdapter:
			if a.Custom != nil {
				a.Provider = a.Custom.Name
			}
		}
	}
	if a.Group == "" {
		a.Group = a.Provider
	}
	if a.Custom != nil {
		a.Custom.applyDefaults()
	}
	if a.BB != nil && a.BB.CLI == "" {
		a.BB.CLI = DefaultBBCLI
	}
	if a.Process != nil && a.Process.Timeout.Duration == 0 {
		a.Process.Timeout.Duration = DefaultProcessTimeout
	}
	if a.Claude != nil && a.Claude.CLI == "" {
		a.Claude.CLI = "claude"
	}
	if a.Codex != nil && a.Codex.CLI == "" {
		a.Codex.CLI = "codex"
	}
}

var accountIDRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidAccountID reports whether s has the form of an accounts catalog ID.
// It says nothing about whether the catalog holds that entry.
func ValidAccountID(s string) bool { return accountIDRE.MatchString(s) }

// validate reports problems with field names prefixed by where.
func (a Agent) validate(where string) []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(where+format, args...))
	}
	set := map[string]bool{"bb": a.BB != nil, "process": a.Process != nil, "claude": a.Claude != nil, "codex": a.Codex != nil, "cursor": a.Cursor != nil, "antigravity": a.Antigravity != nil, SessionpipeAdapter: a.Sessionpipe != nil, CustomAdapter: a.Custom != nil}
	known := false
	for _, n := range Adapters {
		if a.Adapter == n {
			known = true
		}
	}
	if !known {
		add("adapter must be one of %s", strings.Join(Adapters, ", "))
		return errs
	}
	if a.Account != "" && !ValidAccountID(a.Account) {
		add("account must be an accounts catalog ID: the 32 lowercase hexadecimal characters accounts add prints")
	}
	if !set[a.Adapter] {
		add("%s is required for the %s adapter", a.Adapter, a.Adapter)
	}
	for name, on := range set {
		if on && name != a.Adapter {
			add("%s is set but the adapter is %s", name, a.Adapter)
		}
	}
	switch {
	case a.Adapter == "bb" && a.BB != nil:
		switch {
		case a.BB.NewThread && a.BB.Thread != "":
			add("bb.thread and bb.new_thread cannot both be set")
		case a.BB.NewThread && a.Provider == "":
			add("bb.new_thread needs provider: the new thread is created on that BB provider")
		case !a.BB.NewThread && a.BB.Thread == "":
			add("bb.thread is required (or bb.new_thread for a fallback that starts a new thread at handoff)")
		case !a.BB.NewThread && (a.BB.Project != "" || a.BB.Environment != "" || a.BB.PermissionMode != "" || a.BB.MoveChildren != nil || a.BB.ReplaceBlockedChildren != nil || a.BB.ChildModel != "" || a.BB.ChildReasoning != "" || a.BB.ChildPermissionMode != ""):
			add("bb.project, bb.environment, bb.permission_mode, bb.move_children, bb.replace_blocked_children and bb.child_* apply only with bb.new_thread")
		case a.BB.NewThread && a.BB.PermissionMode != "" && a.BB.PermissionMode != "accept-edits" && a.BB.PermissionMode != "auto" && a.BB.PermissionMode != "full":
			add("bb.permission_mode must be accept-edits, auto or full")
		}
		if a.BB.ChildReasoning != "" && a.BB.ChildModel == "" {
			add("bb.child_reasoning needs bb.child_model")
		}
		if m := a.BB.ChildPermissionMode; m != "" && PermissionRank(m) < 0 {
			add("bb.child_permission_mode must be accept-edits, auto or full")
		}
		if d := a.BB.RetrySignalTTL.Duration; d != 0 && d < time.Minute {
			add("bb.retry_signal_ttl must be at least 1m")
		}
		if c := a.BB.Children; c != nil {
			if c.MaxPerCheck < 0 || c.MaxPerCheck > MaxLanesPerCheck {
				add("bb.children.max_per_check must be between 0 and %d (zero means %d)", MaxLanesPerCheck, DefaultLanesPerCheck)
			}
			if c.MaxPerEpisode < 0 || c.MaxPerEpisode > MaxLanesPerEpisode {
				add("bb.children.max_per_episode must be between 0 and %d (zero means %d)", MaxLanesPerEpisode, DefaultLanesPerEpisode)
			}
		}
	case a.Adapter == "process" && a.Process != nil:
		if len(a.Process.StatusCommand) == 0 {
			add("process.status_command is required")
		}
		if len(a.Process.ResumeCommand) == 0 {
			add("process.resume_command is required")
		}
		for code, class := range a.Process.ExitCodes {
			if !ValidErrorClass(class) {
				add("process.exit_codes[%d]: unknown error class %q", code, class)
			}
		}
	case a.Adapter == "cursor" && a.Cursor != nil:
		if a.Cursor.Workdir == "" {
			add("cursor.workdir is required")
		}
		if len(a.Cursor.FreshStartVersions) > 16 {
			add("cursor.fresh_start_versions: at most 16 versions")
		}
		for _, v := range a.Cursor.FreshStartVersions {
			if !CursorVersionPattern.MatchString(v) {
				add("cursor.fresh_start_versions: each entry must be a Cursor CLI version such as 2030.01.02-abcdef0")
				break
			}
		}
	case a.Adapter == "antigravity" && a.Antigravity != nil:
		if a.Antigravity.Workdir == "" {
			add("antigravity.workdir is required")
		}
		if len(a.Antigravity.AddDirs) > 16 {
			add("antigravity.add_dir: at most 16 directories")
		}
		for _, d := range a.Antigravity.AddDirs {
			if strings.TrimSpace(d) == "" {
				add("antigravity.add_dir: entries must be non-empty")
				break
			}
		}
	case a.Adapter == SessionpipeAdapter && a.Sessionpipe != nil:
		if _, _, ok := SplitSessionpipeRef(a.Sessionpipe.Session); !ok {
			add("sessionpipe.session must be <harness>:<id> as sessionpipe names the session, for example claude-code:8c132906-8c3f-4814-870a-afc3d05e1d2e")
		}
		if strings.TrimSpace(a.Sessionpipe.EventsFile) == "" {
			add("sessionpipe.events_file is required: the file given to `sessionpipe sink add file:PATH --tier 0`")
		}
		if d := a.Sessionpipe.StaleAfter.Duration; d != 0 && (d < 2*time.Minute || d > 7*24*time.Hour) {
			add("sessionpipe.stale_after must be between 2m and 168h")
		}
		if s := a.Sessionpipe.Send; s != "" && s != SessionpipeSendWakeOnly {
			add("sessionpipe.send must be %s or absent (absent never sends)", SessionpipeSendWakeOnly)
		}
	case a.Adapter == "claude" && a.Claude != nil:
		if a.Claude.Workdir == "" {
			add("claude.workdir is required")
		}
	case a.Adapter == "codex" && a.Codex != nil:
		if a.Codex.Workdir == "" {
			add("codex.workdir is required")
		}
	case a.Adapter == CustomAdapter && a.Custom != nil:
		a.Custom.validate(add)
	}
	return errs
}

// Model returns the model this agent targets, if configured.
func (a Agent) Model() string {
	switch {
	case a.BB != nil:
		return a.BB.Model
	case a.Cursor != nil:
		return a.Cursor.Model
	case a.Claude != nil:
		return a.Claude.Model
	case a.Codex != nil:
		return a.Codex.Model
	case a.Custom != nil:
		return a.Custom.Values["model"]
	}
	return ""
}

// Describe is a short human label such as "bb thr_x (claude-fable-5-1)".
func (a Agent) Describe() string {
	s := a.Adapter
	switch {
	case a.BB != nil && a.BB.NewThread:
		s += " new thread"
	case a.BB != nil:
		s += " " + a.BB.Thread
	case a.Cursor != nil:
		s += " " + a.Cursor.Workdir
	case a.Antigravity != nil:
		s += " " + a.Antigravity.Workdir
	case a.Sessionpipe != nil:
		s += " " + a.Sessionpipe.Session + " (observed)"
	case a.Claude != nil:
		s += " " + a.Claude.Workdir
	case a.Codex != nil:
		s += " " + a.Codex.Workdir
	case a.Custom != nil:
		s += " " + a.Custom.Name + " " + a.Custom.Workdir
	}
	if m := a.Model(); m != "" {
		s += " (" + m + ")"
	}
	return s
}

func (a *Agent) resolvePaths(base string) {
	if a.Custom != nil {
		a.Custom.resolvePaths(base)
	}
	if a.Cursor != nil {
		a.Cursor.Workdir = resolve(base, a.Cursor.Workdir)
		a.Cursor.CLI = resolveExec(base, a.Cursor.CLI)
	}
	if a.Antigravity != nil {
		a.Antigravity.Workdir = resolve(base, a.Antigravity.Workdir)
		a.Antigravity.CLI = resolveExec(base, a.Antigravity.CLI)
		for i, d := range a.Antigravity.AddDirs {
			a.Antigravity.AddDirs[i] = resolve(base, d)
		}
	}
	if a.Sessionpipe != nil {
		a.Sessionpipe.EventsFile = resolve(base, a.Sessionpipe.EventsFile)
		a.Sessionpipe.CLI = resolveExec(base, a.Sessionpipe.CLI)
	}
	if a.BB != nil {
		a.BB.CLI = resolveExec(base, a.BB.CLI)
	}
	if a.Process != nil {
		resolveArgv(base, a.Process.StatusCommand)
		resolveArgv(base, a.Process.ResumeCommand)
		resolveArgv(base, a.Process.StopCommand)
	}
	if a.Claude != nil {
		a.Claude.Workdir = resolve(base, a.Claude.Workdir)
		a.Claude.CLI = resolveExec(base, a.Claude.CLI)
	}
	if a.Codex != nil {
		a.Codex.Workdir = resolve(base, a.Codex.Workdir)
		a.Codex.CLI = resolveExec(base, a.Codex.CLI)
	}
}

// RouteLabels are the labels a route carries that say which provider,
// model, effort or account it uses: its provider, group and, for a BB
// thread, model and reasoning. A display name leaves them out.
func (a Agent) RouteLabels() []string {
	out := []string{a.Provider, a.Group}
	if a.BB != nil {
		out = append(out, a.BB.Model, a.BB.Reasoning)
	}
	return out
}

// RouteLabels are the route labels of the loop's primary and of each of its
// own fallbacks.
func (c *Config) RouteLabels() []string {
	out := c.Loop.Agent.RouteLabels()
	if c.FallbackSet != nil {
		for _, cand := range c.FallbackSet.Candidates {
			out = append(out, cand.ID)
			out = append(out, cand.Agent.RouteLabels()...)
		}
	}
	return out
}
