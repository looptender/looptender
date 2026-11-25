package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/envvar"
	"github.com/looptender/looptender/internal/registry"
	"github.com/looptender/looptender/internal/snapshot"
	"github.com/looptender/looptender/internal/state"
)

// accounts connect and accounts disconnect: connecting a subscription in one
// step from a terminal. Connect makes a new private directory, runs the
// provider's own sign-in in it and adds it to the catalog under a label. It
// is the panel's "Connect another account" as a command, built from the same
// parts: the same admission of the provider executable, the same private
// directory, the same sign-in and the same check that it worked. looptender
// reads no credential here, asks for none and copies none.

// accountConnectAntigravity is the whole answer for a second Antigravity
// account.
const accountConnectAntigravity = "Antigravity keeps one login per computer, so a second account cannot be connected beside the first."

// accountConnectIdentityNote is said when the new login could not be
// compared with the ones already connected: Codex reports nothing to compare,
// and a Claude Code check can fail.
const accountConnectIdentityNote = "Not checked: whether this is a different account from the ones already connected."

// accountConnectPerson is the whole answer when no person is at a terminal.
const accountConnectPerson = "Run this yourself in a terminal: it opens a sign-in on your screen, which an agent's session or a script cannot finish."

// accountConnectAgentVariables mark a shell as an agent's or a supervised
// session's, beyond envvar.SupervisorSession: Codex's own markers.
var accountConnectAgentVariables = []string{"CODEX_SANDBOX", "CODEX_TOOL_CALL_ID", "CODEX_THREAD_ID"}

// accountConnectInSession reports whether environ is an agent's or a
// supervised session's: the variables looptender already treats as "inside a
// session", Codex's markers, and anything Claude Code sets for its own
// sessions.
func accountConnectInSession(environ []string) bool {
	for _, pair := range environ {
		k, v, _ := strings.Cut(pair, "=")
		if v == "" {
			continue
		}
		if envvar.IsSupervisorSession(k) || strings.HasPrefix(strings.ToUpper(k), "CLAUDE_CODE_") {
			return true
		}
		for _, name := range accountConnectAgentVariables {
			if strings.EqualFold(k, name) {
				return true
			}
		}
	}
	return false
}

// accountConnectPassed are the variables the provider's tool is given, and
// no others: where it lives and writes, who and where the person is, what
// opening a browser needs, and how this network is reached. Proxy and
// certificate variables are passed on purpose: the sign-in has to reach the
// provider from this network, as the same tool does when the person runs it
// by hand. No token of any kind is on the list.
var accountConnectPassed = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TERM", "LANG", "LANGUAGE", "TZ", "TMPDIR", "TMP", "TEMP",
	"DISPLAY", "WAYLAND_DISPLAY", "DBUS_SESSION_BUS_ADDRESS", "BROWSER",
	"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
}

// accountConnectEnvironment keeps of environ only what accountConnectPassed
// names, in either letter case for the proxy variables, plus LC_* and XDG_*.
func accountConnectEnvironment(environ []string) []string {
	var out []string
	for _, pair := range environ {
		k, _, _ := strings.Cut(pair, "=")
		keep := strings.HasPrefix(k, "LC_") || strings.HasPrefix(k, "XDG_")
		for _, name := range accountConnectPassed {
			if k == name || (strings.HasSuffix(name, "_PROXY") && k == strings.ToLower(name)) {
				keep = true
			}
		}
		if keep {
			out = append(out, pair)
		}
	}
	return out
}

// accountConnectDeps are the parts of a connect a test replaces.
type accountConnectDeps struct {
	supported func(provider string) bool
	admit     accountConnectAdmit
	stage     func(ctx context.Context, path, digest string) (string, func(), error)
	run       accountConnectRun
	// check runs the catalog's own check of one entry, which records its
	// login fingerprint where the provider reports an identity.
	check   func(ctx context.Context, dir, id string) error
	environ []string
	// terminal says whether a person is at this terminal: standard input,
	// and standard output unless the output is JSON for a program.
	terminal func(asJSON bool) bool
	now      func() time.Time
}

func accountConnectDefaults() accountConnectDeps {
	return accountConnectDeps{
		supported: panelConnectSupported,
		admit:     accountConnectDigest,
		stage:     accountClaudeQuotaStage,
		run:       accountConnectProvider,
		check: func(ctx context.Context, dir, id string) error {
			return accountCheck(ctx, dir, id, os.Getenv("BB_THREAD_ID"), accountExec, time.Now().UTC())
		},
		environ: os.Environ(),
		terminal: func(asJSON bool) bool {
			return isTerminal(os.Stdin) && (asJSON || isTerminal(os.Stdout))
		},
		now: func() time.Time { return time.Now().UTC() },
	}
}

// accountConnectOptions are the command's flags.
type accountConnectOptions struct {
	provider, label, like, cli, workdir string
	asJSON                              bool
}

// accountConnectResult is the command's --json output. It carries no path
// and no address.
type accountConnectResult struct {
	SchemaVersion int    `json:"schemaVersion"`
	State         string `json:"state"`
	Reason        string `json:"reason,omitempty"`
	ID            string `json:"id,omitempty"`
	Label         string `json:"label,omitempty"`
	Provider      string `json:"provider,omitempty"`
	// SameAccount is "different", "same" or "not_checked". SameAs are the
	// labels of the entries with the same login.
	SameAccount string   `json:"sameAccount,omitempty"`
	SameAs      []string `json:"sameAs,omitempty"`
	// DirectoryKept says a private directory made for this attempt was left
	// in place, possibly signed in. The text output names it.
	DirectoryKept bool `json:"directoryKept,omitempty"`
}

// accountConnectTemplate finds the executable and working directory for the
// new entry: the entry named by --like, else the first of the provider that
// has them, else --cli and --workdir as accounts add resolves them.
func accountConnectTemplate(f accounts.File, o accountConnectOptions) (accounts.Entry, error) {
	if o.like != "" {
		e := accounts.Find(&f, o.like)
		if e == nil || e.Provider != o.provider || e.CLI == "" {
			return accounts.Entry{}, errors.New("the entry named by --like is not a " + o.provider + " entry with an executable")
		}
		return *e, nil
	}
	if o.cli == "" && o.workdir == "" {
		for _, e := range f.Entries {
			if e.Provider == o.provider && e.CLI != "" && e.Workdir != "" {
				return e, nil
			}
		}
	}
	name := o.cli
	if name == "" {
		name = "claude"
		if o.provider == "codex" {
			name = "codex"
		}
	}
	resolved, err := exec.LookPath(name)
	if err == nil {
		resolved, err = filepath.Abs(resolved)
	}
	if err == nil {
		resolved, err = filepath.EvalSymlinks(resolved)
	}
	if err != nil {
		return accounts.Entry{}, errors.New("the provider's executable was not found; name it with --cli")
	}
	wd := o.workdir
	if wd == "" {
		wd, err = os.Getwd()
	}
	if err == nil {
		wd, err = existingDirectory(wd)
	}
	if err != nil {
		return accounts.Entry{}, errors.New("the working directory is unavailable; name one with --workdir")
	}
	return accounts.Entry{Provider: o.provider, CLI: resolved, Workdir: wd}, nil
}

// accountsConnect runs one connect. Every refusal happens before a sign-in
// starts and says that none was started.
func accountsConnect(e env, dir string, o accountConnectOptions, d accountConnectDeps) int {
	out := accountConnectResult{SchemaVersion: 1, State: "refused", Provider: o.provider, Label: o.label}
	finish := func(code int, text string) int {
		if o.asJSON {
			if json.NewEncoder(e.stdout).Encode(out) != nil {
				return exitFailed
			}
			return code
		}
		if code == exitOK {
			fmt.Fprintln(e.stdout, text)
		} else {
			e.errf("accounts connect: %s", text)
		}
		return code
	}
	refuse := func(reason, text string) int {
		out.Reason = reason
		return finish(exitFailed, text+" No sign-in was started.")
	}
	switch o.provider {
	case "claude-code", "codex":
	case "antigravity":
		out.Reason = "one_login_per_computer"
		return finish(exitFailed, accountConnectAntigravity)
	default:
		return refuse("unsupported_provider", "Choose --provider claude-code or --provider codex.")
	}
	if accountConnectInSession(d.environ) || d.terminal == nil || !d.terminal(o.asJSON) {
		out.Reason = "not_a_person"
		return finish(exitFailed, accountConnectPerson)
	}
	if !d.supported(o.provider) {
		return refuse("unsupported_platform", "Connecting "+o.provider+" is not supported on this system yet.")
	}
	if !accountConnectEnvironmentAllowed(d.environ) {
		return refuse("environment", "This shell sets an authentication variable for a provider, so a new login could not be kept apart from it. Run this from a plain terminal.")
	}
	catalog, err := accounts.Load(dir)
	if err != nil {
		return refuse("catalog", "The account catalog could not be read.")
	}
	switch {
	case !accounts.Text(o.label, 120) || strings.TrimSpace(o.label) != o.label:
		return refuse("label_invalid", "Give the subscription a --label: plain text of at most 120 characters, with no space at either end.")
	case len(catalog.Entries) >= accounts.MaxEntries:
		return refuse("catalog_full", "The account catalog is full.")
	case !accountConnectLabelAvailable(catalog, o.label):
		return refuse("label_taken", fmt.Sprintf("Another account is already labelled %q. Choose another --label.", o.label))
	}
	entry, err := accountConnectTemplate(catalog, o)
	if err != nil {
		return refuse("executable", strings.ToUpper(err.Error()[:1])+err.Error()[1:]+".")
	}
	original := entry.CLI
	entry = accounts.Entry{Label: o.label, Provider: o.provider, Mode: "explicit_directory", CLI: original, Workdir: entry.Workdir}

	admission, cancel := context.WithTimeout(e.ctx, 5*time.Second)
	digest, err := d.admit(admission, entry)
	cancel()
	if err != nil {
		text := "The provider's executable is not one looptender can run a sign-in with: it must be the provider's own standalone program."
		manual := "codex login"
		if o.provider == "claude-code" {
			text = "looptender runs the sign-in only with Claude Code " + accountClaudeQuotaVersion + ", the build it was checked against, and this is another one."
			manual = "claude auth login"
		}
		return refuse("unverified_cli", text+" You can still sign in yourself in a directory of its own ("+manual+", with "+accountDirectoryVariable(o.provider)+" set to it) and add it with `looptender accounts add --selection explicit_directory --account-dir`.")
	}
	contextDir, identity, err := accountConnectDirectory(dir)
	if err != nil {
		return refuse("directory", "A private directory for the new login could not be made.")
	}
	entry.Directory = contextDir
	staging, cancel := context.WithTimeout(e.ctx, 5*time.Second)
	image, cleanup, err := d.stage(staging, original, digest)
	cancel()
	if err != nil {
		os.Remove(contextDir) // still empty: nothing has run in it
		return refuse("unverified_cli", "The provider's executable changed while it was being checked.")
	}
	defer cleanup()

	running := entry
	running.CLI = image
	ctx, cancel := context.WithTimeout(e.ctx, accountConnectLifetime)
	defer cancel()
	if !o.asJSON {
		fmt.Fprintf(e.stdout, "Starting the provider's own sign-in for %q. Finish it in your browser; looptender does not see or copy the login.\n", o.label)
		if o.provider == "claude-code" {
			// Claude Code's sign-in output is not read, so its address
			// cannot be shown here as Codex's is.
			fmt.Fprintln(e.stdout, "If no browser opens within a minute, press Ctrl-C: the directory is kept and the two commands to finish by hand are shown.")
		}
	}
	err = d.run(ctx, running, accountQuotaExecutionEnvironment(running, accountConnectEnvironment(d.environ)), func(address string) {
		// The provider's own address for this one sign-in: shown to the
		// person who started it, and kept nowhere.
		if !o.asJSON {
			fmt.Fprintf(e.stdout, "If no browser opened, open this address to sign in:\n%s\n", address)
		}
	})
	// From here the provider may have written a login into the directory, so
	// it is kept whatever happened: a person can inspect or remove it.
	kept := " The private directory made for it was kept: " + contextDir
	out.DirectoryKept = true
	if err != nil || ctx.Err() != nil {
		cancelled := ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded)
		switch {
		case o.provider == "claude-code" && cancelled:
			// The person stopped a Claude Code sign-in, most likely
			// because no browser opened: the directory stays, private, so
			// the sign-in can be finished by hand in it.
			kept += fmt.Sprintf("\nTo finish by hand, sign in in that directory and add it:\n  %s=%s claude auth login\n  looptender accounts add --provider claude-code --selection explicit_directory --account-dir %s --label %s",
				accountDirectoryVariable(o.provider), shellQuote(contextDir), shellQuote(contextDir), shellQuote(o.label))
		default:
			// With no sign-in, what looptender itself wrote goes, and then
			// the directory: Remove fails on anything but an empty one, so
			// whatever the provider wrote keeps it.
			accountConnectRemoveOwn(contextDir, o.provider)
			if os.Remove(contextDir) == nil {
				kept, out.DirectoryKept = "", false
			}
		}
		out.State, out.Reason = "failed", "sign_in_unavailable"
		text := "The sign-in did not complete, so nothing was added."
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			out.State, out.Reason = "expired", "timeout"
			text = "The sign-in was not finished in " + accountConnectLifetime.String() + ", so nothing was added."
		case ctx.Err() != nil:
			out.State, out.Reason = "cancelled", "cancelled"
			text = "The sign-in was cancelled, so nothing was added."
		}
		return finish(exitFailed, text+kept)
	}

	err = accounts.Update(dir, func(f *accounts.File) error {
		st, e1 := snapshot.Lstat(contextDir)
		real, e2 := filepath.EvalSymlinks(contextDir)
		if e1 != nil || e2 != nil || real != contextDir || !os.SameFile(identity, st) || !st.IsDir() || !config.OwnedByUser(st) || st.Mode().Perm()&0077 != 0 {
			return accounts.ErrChanged
		}
		if !accountConnectLabelAvailable(*f, o.label) {
			return accounts.ErrChanged
		}
		entry = accounts.NewEntry(*f, entry, "", d.now(), d.environ)
		f.Entries = append(f.Entries, entry)
		return nil
	})
	if err != nil {
		out.State, out.Reason = "failed", "catalog_changed"
		return finish(exitFailed, "You are signed in, but the catalog or the label changed meanwhile, so the account was not added."+kept)
	}
	out.State, out.Reason, out.ID, out.DirectoryKept = "connected", "", entry.ID, false
	same, unchecked, compared := accountConnectCompare(e.ctx, dir, entry, d)
	note := accountConnectIdentityNote
	switch {
	case len(same) > 0:
		// The same account under a second label is one quota, not two: the
		// entry is taken out again. Its directory stays, signed in.
		removeErr := accounts.Update(dir, func(f *accounts.File) error {
			for i, x := range f.Entries {
				if x.ID == entry.ID {
					f.Entries = append(f.Entries[:i:i], f.Entries[i+1:]...)
					return nil
				}
			}
			return nil
		})
		out.State, out.Reason, out.SameAccount, out.SameAs, out.DirectoryKept = "same_account", "same_account", "same", same, true
		kept += "\nIt is still signed in. To sign it out: " + accountSignOutCommand(entry)
		if removeErr != nil {
			return finish(exitFailed, fmt.Sprintf("This is the same account as %s, so it adds no second quota, but its entry %s could not be taken out again; remove it with `looptender accounts disconnect --id %s`.%s", quotedList(same), entry.ID, entry.ID, kept))
		}
		out.ID = ""
		return finish(exitFailed, fmt.Sprintf("This is the same account as %s, so it adds no second quota. It was not added.%s", quotedList(same), kept))
	case compared && unchecked == 0:
		out.SameAccount = "different"
		note = "A different account from the other " + o.provider + " logins connected."
	case compared:
		out.SameAccount = "different"
		note = fmt.Sprintf("A different account from the %s logins that could be checked; %d could not be.", o.provider, unchecked)
	default:
		out.SameAccount = "not_checked"
	}
	return finish(exitOK, fmt.Sprintf("Connected %q (%s) as account %s.\n%s\nNo route or loop was changed.", o.label, o.provider, entry.ID, note))
}

// accountConnectCodexConfig is the one file a Codex connect writes itself
// (accountConnectCodex), with exactly this content.
const accountConnectCodexConfig = "cli_auth_credentials_store = \"file\"\n"

// accountConnectRemoveOwn removes from dir what looptender wrote there, and
// only that: Codex's config.toml while it still has the content it was
// written with.
func accountConnectRemoveOwn(dir, provider string) {
	if provider != "codex" {
		return
	}
	path := filepath.Join(dir, "config.toml")
	if b, err := os.ReadFile(path); err == nil && string(b) == accountConnectCodexConfig {
		os.Remove(path)
	}
}

// accountConnectCompare says whether the new entry's login is one already
// connected, by the catalog's keyed login fingerprints: it never sees an
// identity. It checks the new entry, and any other entry of the provider
// that has no fingerprint yet, with the catalog's own check. same are the
// labels with the same login; unchecked counts the other entries that could
// not be compared; compared is false when the new entry has no fingerprint.
func accountConnectCompare(ctx context.Context, dir string, added accounts.Entry, d accountConnectDeps) (same []string, unchecked int, compared bool) {
	if d.check == nil || added.Provider != "claude-code" {
		// Only Claude Code's status names an account to fingerprint.
		return nil, 0, false
	}
	_ = d.check(ctx, dir, added.ID)
	f, err := accounts.Load(dir)
	if err != nil {
		return nil, 0, false
	}
	logins, _ := accounts.LoadLogins(dir)
	mine := logins[added.ID].Last.Fingerprint
	if mine == "" {
		return nil, 0, false
	}
	for _, other := range f.Entries {
		if other.ID == added.ID || other.Provider != added.Provider {
			continue
		}
		if logins[other.ID].Last.Fingerprint == "" {
			_ = d.check(ctx, dir, other.ID)
			if again, err := accounts.LoadLogins(dir); err == nil {
				logins = again
			}
		}
		switch logins[other.ID].Last.Fingerprint {
		case "":
			unchecked++
		case mine:
			same = append(same, other.Label)
		}
	}
	return same, unchecked, true
}

// quotedList writes labels as "a", "b" and "c".
func quotedList(labels []string) string {
	q := make([]string, len(labels))
	for i, l := range labels {
		q[i] = fmt.Sprintf("%q", l)
	}
	if len(q) < 2 {
		return strings.Join(q, "")
	}
	return strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
}

// accountUses lists what names the account with this id among the loops
// registered on this computer: each one whose routes or usage watch name it.
// A loop that is not registered here is not seen. complete is false when a loop
// could not be read, so "nothing uses it" is not established.
func accountUses(catalogDir, id string) (uses []string, complete bool) {
	dir, err := registryDirectory("")
	if err != nil {
		return nil, false
	}
	return accountUsesIn(dir, catalogDir, id)
}

// accountUsesIn is accountUses for the loops registered in dir.
func accountUsesIn(dir, catalogDir, id string) (uses []string, complete bool) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, true // no loop was ever registered
	}
	reg, err := registry.Load(dir)
	if err != nil {
		return nil, false
	}
	complete = true
	for _, loop := range reg.Entries {
		cfg, err := config.Load(loop.ConfigPath)
		if err != nil {
			complete = false
			continue
		}
		results, _ := routeAccountChecks(cfg, catalogDir)
		for _, r := range results {
			if r.Account == id {
				uses = append(uses, fmt.Sprintf("loop %s, route %s", loop.Loop, r.ID))
			}
		}
		if w, err := state.LoadUsageWatchSetting(loop.StateDir); err == nil && w != nil && w.Account == id {
			uses = append(uses, fmt.Sprintf("loop %s, usage watch", loop.Loop))
		}
	}
	return uses, complete
}

// accountsDisconnect takes one entry out of the catalog. It signs nothing
// out and deletes nothing. It refuses an account that a loop names, or
// whose use could not be read, unless forced.
func accountsDisconnect(e env, dir, id, label string, force, asJSON bool, uses func(catalogDir, id string) ([]string, bool)) int {
	if (id == "") == (label == "") {
		e.errf("accounts disconnect: name the account with --id or with --label")
		return exitUsage
	}
	find := func(f *accounts.File) int {
		at := -1
		for i, x := range f.Entries {
			if x.ID == id || (label != "" && strings.EqualFold(x.Label, label)) {
				if at >= 0 {
					return -1
				}
				at = i
			}
		}
		return at
	}
	catalog, err := accounts.Load(dir)
	at := -1
	if err == nil {
		at = find(&catalog)
	}
	if at < 0 {
		e.errf("accounts disconnect: no single account has that id or label; nothing was changed")
		return exitFailed
	}
	target := catalog.Entries[at]
	if !force {
		used, complete := uses(dir, target.ID)
		if len(used) > 0 {
			e.errf("accounts disconnect: %q is used by loops registered on this computer: %s. Nothing was changed. Change those first, or pass --force to disconnect it anyway.", target.Label, strings.Join(used, "; "))
			return exitFailed
		}
		if !complete {
			e.errf("accounts disconnect: could not read every loop registered on this computer to see whether one uses %q. Nothing was changed. Pass --force to disconnect it anyway.", target.Label)
			return exitFailed
		}
	}
	var removed accounts.Entry
	err = accounts.Update(dir, func(f *accounts.File) error {
		at := find(f)
		if at < 0 || f.Entries[at].ID != target.ID {
			return accounts.ErrChanged
		}
		removed = f.Entries[at]
		f.Entries = append(f.Entries[:at:at], f.Entries[at+1:]...)
		return nil
	})
	if err != nil {
		e.errf("accounts disconnect: the catalog changed meanwhile; nothing was changed")
		return exitFailed
	}
	if asJSON {
		out := accountConnectResult{SchemaVersion: 1, State: "disconnected", ID: removed.ID, Label: removed.Label, Provider: removed.Provider}
		if json.NewEncoder(e.stdout).Encode(out) != nil {
			return exitFailed
		}
		return exitOK
	}
	fmt.Fprintf(e.stdout, "Disconnected %q (%s). looptender no longer lists or uses it.\n", removed.Label, removed.Provider)
	switch signOut := accountSignOutCommand(removed); {
	case removed.Mode != "explicit_directory":
		fmt.Fprintln(e.stdout, "It was this computer's own login for that provider, which other sessions use. It is still signed in, and no sign-out command is shown: signing it out would sign those sessions out too.")
	case signOut != "":
		fmt.Fprintf(e.stdout, "Its login was not signed out and its directory was not removed. To sign it out yourself:\n  %s\n", signOut)
	}
	if force {
		fmt.Fprintln(e.stdout, "A loop that names this account still names it; `looptender doctor` reports such a route.")
	}
	return exitOK
}

// accountSignOutCommand is the provider's own sign-out for an entry's login,
// as a person would type it. Empty when looptender knows none, and for an
// entry that is the computer's own login: that command would sign out every
// session using it.
func accountSignOutCommand(e accounts.Entry) string {
	variable, command := "", ""
	switch e.Provider {
	case "claude-code":
		variable, command = "CLAUDE_CONFIG_DIR", "claude auth logout"
	case "codex":
		variable, command = "CODEX_HOME", "codex logout"
	default:
		return ""
	}
	if e.Mode != "explicit_directory" || e.Directory == "" {
		return ""
	}
	return variable + "=" + shellQuote(e.Directory) + " " + command
}

// accountDirectoryVariable is the variable that selects a provider's login
// directory.
func accountDirectoryVariable(provider string) string {
	if provider == "codex" {
		return "CODEX_HOME"
	}
	return "CLAUDE_CONFIG_DIR"
}
