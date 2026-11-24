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
	"github.com/looptender/looptender/internal/snapshot"
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

// accountConnectIdentityNote is said after every connect until the catalog
// can compare logins.
const accountConnectIdentityNote = "Not checked: whether this is a different account from the ones already connected."

// accountConnectDeps are the parts of a connect a test replaces.
type accountConnectDeps struct {
	supported func(provider string) bool
	admit     accountConnectAdmit
	stage     func(ctx context.Context, path, digest string) (string, func(), error)
	run       accountConnectRun
	environ   []string
	now       func() time.Time
}

func accountConnectDefaults() accountConnectDeps {
	return accountConnectDeps{
		supported: panelConnectSupported,
		admit:     accountConnectDigest,
		stage:     accountClaudeQuotaStage,
		run:       accountConnectProvider,
		environ:   os.Environ(),
		now:       func() time.Time { return time.Now().UTC() },
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
	// SameAccount is "not_checked" until logins can be compared.
	SameAccount string `json:"sameAccount,omitempty"`
}

// accountConnectTemplate finds the executable and working directory for the
// new entry: the entry named by --like, else the first of the provider that
// has them, else --cli and --workdir as accounts add resolves them.
func accountConnectTemplate(f accounts.File, o accountConnectOptions) (accounts.Entry, error) {
	if o.like != "" {
		e := accounts.Find(&f, o.like)
		if e == nil || e.Provider != o.provider || e.CLI == "" {
			return accounts.Entry{}, errors.New("the entry named by --id is not a " + o.provider + " entry with an executable")
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
	if !accountConnectLabelAvailable(catalog, o.label) {
		return refuse("label", "Give the subscription a --label that no other account has.")
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
	}
	err = d.run(ctx, running, accountQuotaExecutionEnvironment(running, d.environ), func(address string) {
		// The provider's own address for this one sign-in: shown to the
		// person who started it, and kept nowhere.
		if !o.asJSON {
			fmt.Fprintf(e.stdout, "If no browser opened, open this address to sign in:\n%s\n", address)
		}
	})
	// From here the provider may have written a login into the directory, so
	// it is kept whatever happened: a person can inspect or remove it.
	kept := " The private directory made for it was kept: " + contextDir
	if err != nil || ctx.Err() != nil {
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
		entry = accounts.NewEntry(*f, entry, os.Getenv("BB_THREAD_ID"), d.now(), d.environ)
		f.Entries = append(f.Entries, entry)
		return nil
	})
	if err != nil {
		out.State, out.Reason = "failed", "catalog_changed"
		return finish(exitFailed, "You are signed in, but the catalog or the label changed meanwhile, so the account was not added."+kept)
	}
	out.State, out.Reason, out.ID, out.SameAccount = "connected", "", entry.ID, "not_checked"
	return finish(exitOK, fmt.Sprintf("Connected %q (%s) as account %s.\n%s\nNo route or loop was changed.", o.label, o.provider, entry.ID, accountConnectIdentityNote))
}

// accountsDisconnect takes one entry out of the catalog. It signs nothing
// out and deletes nothing: it prints the provider's own command for that.
func accountsDisconnect(e env, dir, id, label string, asJSON bool) int {
	if (id == "") == (label == "") {
		e.errf("accounts disconnect: name the account with --id or with --label")
		return exitUsage
	}
	var removed accounts.Entry
	err := accounts.Update(dir, func(f *accounts.File) error {
		at := -1
		for i, x := range f.Entries {
			if x.ID == id || (label != "" && strings.EqualFold(x.Label, label)) {
				if at >= 0 {
					return accounts.ErrInvalid
				}
				at = i
			}
		}
		if at < 0 {
			return accounts.ErrInvalid
		}
		removed = f.Entries[at]
		f.Entries = append(f.Entries[:at:at], f.Entries[at+1:]...)
		return nil
	})
	if err != nil {
		e.errf("accounts disconnect: no single account has that id or label; nothing was changed")
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
	if signOut := accountSignOutCommand(removed); signOut != "" {
		fmt.Fprintf(e.stdout, "Its login was not signed out and its directory was not removed. To sign it out yourself:\n  %s\n", signOut)
	}
	fmt.Fprintln(e.stdout, "A route that names this account still names it; `looptender doctor` reports such a route.")
	return exitOK
}

// accountSignOutCommand is the provider's own sign-out for an entry's login,
// as a person would type it. Empty when looptender knows none.
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
		return command
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
