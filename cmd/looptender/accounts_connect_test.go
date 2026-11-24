package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/registry"
	"github.com/looptender/looptender/internal/state"
)

// connectRig is a catalog with one entry and stand-ins for everything a
// connect would run. Nothing here starts a provider.
type connectRig struct {
	dir      string
	template accounts.Entry
	deps     accountConnectDeps
	ran      []accounts.Entry
	ranEnv   [][]string
	out, err bytes.Buffer
}

func newConnectRig(t *testing.T, provider string) *connectRig {
	t.Helper()
	if runtime.GOOS == "windows" {
		// The real command refuses there before anything else
		// (panelConnectSupported); these tests replace that check.
		t.Skip("connecting an account is not supported on Windows")
	}
	dir, template := accountFixture(t, provider)
	g := &connectRig{dir: dir, template: template}
	g.deps = accountConnectDeps{
		supported: func(string) bool { return true },
		admit:     func(context.Context, accounts.Entry) (string, error) { return "digest", nil },
		stage: func(_ context.Context, path, _ string) (string, func(), error) {
			return path + ".staged", func() {}, nil
		},
		run: func(_ context.Context, e accounts.Entry, env []string, progress func(string)) error {
			g.ran, g.ranEnv = append(g.ran, e), append(g.ranEnv, env)
			progress("https://auth.example.com/sign-in?attempt=one")
			return nil
		},
		environ:  []string{"HOME=/home/example", "PATH=/usr/bin", "GITHUB_TOKEN=example-token", "HTTPS_PROXY=http://proxy.example.com:3128", "LC_ALL=C", "EXAMPLE_SECRET=x"},
		terminal: func(bool) bool { return true },
		now:      func() time.Time { return time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC) },
	}
	return g
}

func (g *connectRig) connect(o accountConnectOptions) int {
	g.out.Reset()
	g.err.Reset()
	return accountsConnect(env{ctx: context.Background(), stdout: &g.out, stderr: &g.err}, g.dir, o, g.deps)
}

func (g *connectRig) entries(t *testing.T) []accounts.Entry {
	t.Helper()
	f, err := accounts.Load(g.dir)
	if err != nil {
		t.Fatal(err)
	}
	return f.Entries
}

// One command connects a second subscription: a new private directory, the
// provider's sign-in run in it, and a new catalog entry under the label.
func TestAccountsConnectAddsASecondSubscription(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex"} {
		t.Run(provider, func(t *testing.T) {
			g := newConnectRig(t, provider)
			if code := g.connect(accountConnectOptions{provider: provider, label: "Example spare"}); code != exitOK {
				t.Fatalf("exit %d: %s", code, g.err.String())
			}
			es := g.entries(t)
			if len(es) != 2 {
				t.Fatalf("%d entries", len(es))
			}
			added := es[1]
			if added.Label != "Example spare" || added.Provider != provider || added.Mode != "explicit_directory" || added.CLI != g.template.CLI || added.Workdir != g.template.Workdir || added.ID == g.template.ID {
				t.Fatalf("added %+v", added)
			}
			st, err := os.Stat(added.Directory)
			if err != nil || !st.IsDir() || st.Mode().Perm() != 0700 || filepath.Dir(added.Directory) != g.dir {
				t.Fatalf("directory %q: %v %v", added.Directory, st, err)
			}
			if added.Directory == g.template.Directory || es[0].Revision != g.template.Revision {
				t.Fatal("the existing entry or its directory was touched")
			}
			// The sign-in ran once, on the staged executable, in the new
			// directory, with that directory as the provider's own.
			if len(g.ran) != 1 || g.ran[0].CLI != g.template.CLI+".staged" || g.ran[0].Directory != added.Directory {
				t.Fatalf("ran %+v", g.ran)
			}
			variable := "CLAUDE_CONFIG_DIR="
			if provider == "codex" {
				variable = "CODEX_HOME="
			}
			if !slices.Contains(g.ranEnv[0], variable+added.Directory) {
				t.Fatalf("the sign-in was not given the new directory: %v", g.ranEnv[0])
			}
			// The provider's tool gets a short list of variables: the proxy,
			// on purpose, and no token.
			for _, pair := range g.ranEnv[0] {
				if strings.HasPrefix(pair, "GITHUB_TOKEN=") || strings.HasPrefix(pair, "EXAMPLE_SECRET=") {
					t.Fatalf("%s was passed to the provider's tool", strings.SplitN(pair, "=", 2)[0])
				}
			}
			for _, want := range []string{"HOME=/home/example", "PATH=/usr/bin", "HTTPS_PROXY=http://proxy.example.com:3128", "LC_ALL=C"} {
				if !slices.Contains(g.ranEnv[0], want) {
					t.Fatalf("%s was not passed: %v", want, g.ranEnv[0])
				}
			}
			text := g.out.String()
			wants := []string{`Connected "Example spare"`, accountConnectIdentityNote, "https://auth.example.com/sign-in?attempt=one", "does not see or copy the login"}
			if provider == "claude-code" {
				wants = append(wants, "If no browser opens within a minute, press Ctrl-C", "CLAUDE_CONFIG_DIR="+shellQuote(added.Directory)+" claude auth login")
			}
			for _, want := range wants {
				if !strings.Contains(text, want) {
					t.Errorf("output lacks %q:\n%s", want, text)
				}
			}
		})
	}
}

// --json carries the outcome and neither the directory nor the sign-in
// address.
func TestAccountsConnectJSON(t *testing.T) {
	g := newConnectRig(t, "codex")
	if code := g.connect(accountConnectOptions{provider: "codex", label: "Example spare", asJSON: true}); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	var got accountConnectResult
	if err := json.Unmarshal(g.out.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, g.out.String())
	}
	added := g.entries(t)[1]
	if got.State != "connected" || got.ID != added.ID || got.Label != "Example spare" || got.Provider != "codex" || got.SameAccount != "not_checked" {
		t.Fatalf("%+v", got)
	}
	if text := g.out.String(); strings.Contains(text, added.Directory) || strings.Contains(text, "auth.example.com") {
		t.Fatalf("the JSON carries a path or the sign-in address: %s", text)
	}
}

// Every refusal comes before a sign-in, changes nothing and says so.
func TestAccountsConnectRefusals(t *testing.T) {
	cases := []struct {
		name   string
		o      accountConnectOptions
		change func(*connectRig)
		want   string
	}{
		{name: "a second Antigravity account", o: accountConnectOptions{provider: "antigravity", label: "Example spare"}, want: accountConnectAntigravity},
		{name: "another provider", o: accountConnectOptions{provider: "cursor", label: "Example spare"}, want: "--provider claude-code or --provider codex"},
		{name: "no label", o: accountConnectOptions{provider: "codex"}, want: "plain text of at most 120 characters"},
		{name: "a label with a space at the end", o: accountConnectOptions{provider: "codex", label: "Example spare "}, want: "plain text of at most 120 characters"},
		{name: "a label with a control character", o: accountConnectOptions{provider: "codex", label: "Example\x1b[2Jspare"}, want: "plain text of at most 120 characters"},
		{name: "a label in use", o: accountConnectOptions{provider: "codex", label: "example WORKSHOP"}, want: `Another account is already labelled "example WORKSHOP"`},
		{name: "a supervisor's session", o: accountConnectOptions{provider: "codex", label: "Example spare"}, change: func(g *connectRig) { g.deps.environ = append(g.deps.environ, "BB_THREAD_ID=thr_example123") }, want: accountConnectPerson},
		{name: "a Codex agent's shell", o: accountConnectOptions{provider: "codex", label: "Example spare"}, change: func(g *connectRig) { g.deps.environ = append(g.deps.environ, "CODEX_SANDBOX=seatbelt") }, want: accountConnectPerson},
		{name: "a Claude Code session", o: accountConnectOptions{provider: "codex", label: "Example spare"}, change: func(g *connectRig) { g.deps.environ = append(g.deps.environ, "CLAUDE_CODE_SSE_PORT=1") }, want: accountConnectPerson},
		{name: "no terminal", o: accountConnectOptions{provider: "codex", label: "Example spare"}, change: func(g *connectRig) { g.deps.terminal = func(bool) bool { return false } }, want: accountConnectPerson},
		{name: "an unsupported system", o: accountConnectOptions{provider: "codex", label: "Example spare"}, change: func(g *connectRig) { g.deps.supported = func(string) bool { return false } }, want: "not supported on this system"},
		{name: "a provider key in the shell", o: accountConnectOptions{provider: "codex", label: "Example spare"}, change: func(g *connectRig) { g.deps.environ = append(g.deps.environ, "OPENAI_API_KEY=example") }, want: "plain terminal"},
		{name: "an executable that is not admitted", o: accountConnectOptions{provider: "codex", label: "Example spare"}, change: func(g *connectRig) {
			g.deps.admit = func(context.Context, accounts.Entry) (string, error) { return "", errConnectProtocol }
		}, want: "CODEX_HOME set to it"},
		{name: "an executable that changed", o: accountConnectOptions{provider: "codex", label: "Example spare"}, change: func(g *connectRig) {
			g.deps.stage = func(context.Context, string, string) (string, func(), error) { return "", nil, errors.New("changed") }
		}, want: "changed while it was being checked"},
		{name: "a template of another provider", o: accountConnectOptions{provider: "claude-code", label: "Example spare", like: "-"}, want: "--like"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newConnectRig(t, "codex")
			if c.change != nil {
				c.change(g)
			}
			if c.o.like == "-" {
				c.o.like = g.template.ID
			}
			before, _ := os.ReadDir(g.dir)
			if code := g.connect(c.o); code != exitFailed {
				t.Fatalf("exit %d", code)
			}
			if text := g.err.String(); !strings.Contains(text, c.want) || (c.o.provider != "antigravity" && c.want != accountConnectPerson && !strings.Contains(text, "No sign-in was started.")) {
				t.Fatalf("said %q", text)
			}
			after, _ := os.ReadDir(g.dir)
			if len(g.ran) != 0 || len(g.entries(t)) != 1 || len(after) != len(before) {
				t.Fatalf("a refusal ran a sign-in, added an entry or left a directory (%d → %d files)", len(before), len(after))
			}
		})
	}
}

// The Antigravity refusal is one sentence and gives no opinion.
func TestAccountsConnectAntigravitySentence(t *testing.T) {
	g := newConnectRig(t, "antigravity")
	g.connect(accountConnectOptions{provider: "antigravity", label: "Example spare"})
	if got := strings.TrimSpace(g.err.String()); got != "accounts connect: "+accountConnectAntigravity {
		t.Fatalf("said %q", got)
	}
	g.connect(accountConnectOptions{provider: "antigravity", label: "Example spare", asJSON: true})
	if !strings.Contains(g.out.String(), `"reason":"one_login_per_computer"`) {
		t.Fatalf("JSON %s", g.out.String())
	}
}

// A sign-in that fails or is cancelled adds nothing. A directory the
// provider wrote into is kept and named, in text and in JSON; one nothing
// was written into is removed.
func TestAccountsConnectFailedSignInAddsNothing(t *testing.T) {
	wrote := func(_ context.Context, e accounts.Entry, _ []string, _ func(string)) error {
		if err := os.WriteFile(filepath.Join(e.Directory, "provider-file"), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		return errConnectProtocol
	}
	t.Run("failed after the provider wrote", func(t *testing.T) {
		g := newConnectRig(t, "codex")
		g.deps.run = wrote
		if code := g.connect(accountConnectOptions{provider: "codex", label: "Example spare"}); code != exitFailed {
			t.Fatalf("exit %d", code)
		}
		if len(g.entries(t)) != 1 || !strings.Contains(g.err.String(), "nothing was added") || !strings.Contains(g.err.String(), "was kept: "+g.dir) {
			t.Fatalf("entries %d, said %q", len(g.entries(t)), g.err.String())
		}
		g = newConnectRig(t, "codex")
		g.deps.run = wrote
		g.connect(accountConnectOptions{provider: "codex", label: "Example spare", asJSON: true})
		var got accountConnectResult
		if err := json.Unmarshal(g.out.Bytes(), &got); err != nil || got.State != "failed" || !got.DirectoryKept {
			t.Fatalf("%v %+v", err, got)
		}
	})
	t.Run("failed before anything was written", func(t *testing.T) {
		g := newConnectRig(t, "codex")
		g.deps.run = func(context.Context, accounts.Entry, []string, func(string)) error { return errConnectProtocol }
		before, _ := os.ReadDir(g.dir)
		g.connect(accountConnectOptions{provider: "codex", label: "Example spare"})
		after, _ := os.ReadDir(g.dir)
		if len(after) != len(before) || strings.Contains(g.err.String(), "was kept") {
			t.Fatalf("an empty directory was left or said to be kept: %q", g.err.String())
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		g := newConnectRig(t, "codex")
		ctx, cancel := context.WithCancel(context.Background())
		g.deps.run = func(c context.Context, _ accounts.Entry, _ []string, _ func(string)) error {
			cancel()
			<-c.Done()
			return c.Err()
		}
		code := accountsConnect(env{ctx: ctx, stdout: &g.out, stderr: &g.err}, g.dir, accountConnectOptions{provider: "codex", label: "Example spare"}, g.deps)
		if code != exitFailed || len(g.entries(t)) != 1 || !strings.Contains(g.err.String(), "cancelled") {
			t.Fatalf("exit %d, said %q", code, g.err.String())
		}
	})
}

// A directory that is no longer private, or no longer the one that was
// made, when the sign-in returns is not added to the catalog.
func TestAccountsConnectDirectoryChangedDuringSignIn(t *testing.T) {
	for name, change := range map[string]func(dir string) error{
		"readable by others": func(dir string) error { return os.Chmod(dir, 0755) },
		"replaced by another directory": func(dir string) error {
			if err := os.Remove(dir); err != nil {
				return err
			}
			return os.Mkdir(dir, 0700)
		},
		"replaced by a link": func(dir string) error {
			if err := os.Remove(dir); err != nil {
				return err
			}
			return os.Symlink(filepath.Dir(dir), dir)
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := newConnectRig(t, "codex")
			g.deps.run = func(_ context.Context, e accounts.Entry, _ []string, _ func(string)) error {
				return change(e.Directory)
			}
			if code := g.connect(accountConnectOptions{provider: "codex", label: "Example spare"}); code != exitFailed {
				t.Fatalf("exit %d: %s", code, g.out.String())
			}
			if len(g.entries(t)) != 1 || !strings.Contains(g.err.String(), "was not added") {
				t.Fatalf("entries %d, said %q", len(g.entries(t)), g.err.String())
			}
		})
	}
}

// A label taken while the person was signing in is not overwritten.
func TestAccountsConnectLabelTakenMeanwhile(t *testing.T) {
	g := newConnectRig(t, "codex")
	g.deps.run = func(context.Context, accounts.Entry, []string, func(string)) error {
		return accounts.Update(g.dir, func(f *accounts.File) error {
			f.Entries = append(f.Entries, accounts.NewEntry(*f, accounts.Entry{Label: "Example spare", Provider: "other", Mode: "externally_managed"}, "", time.Now().UTC(), nil))
			return nil
		})
	}
	if code := g.connect(accountConnectOptions{provider: "codex", label: "Example spare"}); code != exitFailed {
		t.Fatalf("exit %d", code)
	}
	if es := g.entries(t); len(es) != 2 || es[1].Provider != "other" || !strings.Contains(g.err.String(), "was not added") {
		t.Fatalf("entries %+v, said %q", es, g.err.String())
	}
}

// With no entry of the provider yet, --cli and --workdir name the executable
// as accounts add does.
func TestAccountsConnectFirstOfAProvider(t *testing.T) {
	g := newConnectRig(t, "claude-code")
	if code := g.connect(accountConnectOptions{provider: "codex", label: "Example first", cli: g.template.CLI, workdir: g.template.Workdir}); code != exitOK {
		t.Fatalf("exit %d: %s", code, g.err.String())
	}
	if added := g.entries(t)[1]; added.Provider != "codex" || added.CLI != g.template.CLI || added.Workdir != g.template.Workdir {
		t.Fatalf("added %+v", added)
	}
	g = newConnectRig(t, "claude-code")
	if code := g.connect(accountConnectOptions{provider: "codex", label: "Example first", cli: filepath.Join(g.dir, "no-such-cli")}); code != exitFailed || !strings.Contains(g.err.String(), "--cli") {
		t.Fatalf("exit %d, said %q", code, g.err.String())
	}
}

// Disconnect takes the entry out, leaves its directory and login alone, and
// prints the provider's own sign-out command for a directory of its own. It
// refuses an account a loop uses.
func TestAccountsDisconnect(t *testing.T) {
	g := newConnectRig(t, "codex")
	g.connect(accountConnectOptions{provider: "codex", label: "Example spare"})
	added := g.entries(t)[1]
	used := map[string][]string{}
	complete := true
	run := func(force bool, id, label string) (int, string, string) {
		var out, errs bytes.Buffer
		code := accountsDisconnect(env{ctx: context.Background(), stdout: &out, stderr: &errs}, g.dir, id, label, force, false, func(_, id string) ([]string, bool) { return used[id], complete })
		return code, out.String(), errs.String()
	}
	if code, _, _ := run(false, "", ""); code != exitUsage {
		t.Fatalf("no name: exit %d", code)
	}
	if code, _, said := run(false, "", "Example nobody"); code != exitFailed || !strings.Contains(said, "nothing was changed") || len(g.entries(t)) != 2 {
		t.Fatalf("unknown label: exit %d, %q", code, said)
	}

	// Used by a loop: refused, naming it.
	used[added.ID] = []string{"loop example-loop, route primary", "loop example-loop, usage watch"}
	if code, _, said := run(false, "", "example SPARE"); code != exitFailed || !strings.Contains(said, `"Example spare" is used by loop example-loop, route primary; loop example-loop, usage watch. Nothing was changed.`) || !strings.Contains(said, "--force") || len(g.entries(t)) != 2 {
		t.Fatalf("in use: exit %d, %q", code, said)
	}
	// Its use could not be read: refused too.
	used, complete = map[string][]string{}, false
	if code, _, said := run(false, "", "example SPARE"); code != exitFailed || !strings.Contains(said, "could not read every loop") || len(g.entries(t)) != 2 {
		t.Fatalf("unreadable: exit %d, %q", code, said)
	}
	complete = true

	code, out, said := run(false, "", "example SPARE")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, said)
	}
	if es := g.entries(t); len(es) != 1 || es[0].ID != g.template.ID {
		t.Fatalf("entries %+v", es)
	}
	if _, err := os.Stat(added.Directory); err != nil {
		t.Fatal("disconnect removed the login's directory")
	}
	for _, want := range []string{`Disconnected "Example spare"`, "CODEX_HOME=" + shellQuote(added.Directory) + " codex logout", "not signed out"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// The computer's own login, forced although a loop names it: no
	// sign-out command at all, and the reason.
	used[g.template.ID] = []string{"loop example-loop, route primary"}
	code, out, _ = run(true, g.template.ID, "")
	if code != exitOK || len(g.entries(t)) != 0 {
		t.Fatalf("forced: exit %d", code)
	}
	if strings.Contains(out, "codex logout") || !strings.Contains(out, "this computer's own login") || !strings.Contains(out, "looptender doctor") {
		t.Fatalf("an inherited entry's output:\n%s", out)
	}
}

// What uses an account is read from the registered loops: here a usage
// watch on it. A loop whose configuration cannot be read makes the answer
// incomplete.
func TestAccountUses(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	f := newManageFixture(t)
	cfg, err := config.Load(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	reg := filepath.Join(root, "registry")
	if uses, complete := accountUsesIn(reg, root, id); len(uses) != 0 || !complete {
		t.Fatalf("with no registry: %v %v", uses, complete)
	}
	if err := os.Mkdir(reg, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(reg, cfg.Loop.Name, f.cfg, cfg.Loop.StateDir); err != nil {
		t.Fatal(err)
	}
	if uses, complete := accountUsesIn(reg, root, id); len(uses) != 0 || !complete {
		t.Fatalf("a loop that does not name it: %v %v", uses, complete)
	}
	if err := os.MkdirAll(cfg.Loop.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveUsageWatchSetting(cfg.Loop.StateDir, &state.UsageWatchSetting{Account: id, Every: "1h", WarnWithin: "24h", SetAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	uses, complete := accountUsesIn(reg, root, id)
	if !complete || len(uses) != 1 || uses[0] != "loop "+cfg.Loop.Name+", usage watch" {
		t.Fatalf("a usage watch on it: %v %v", uses, complete)
	}
	if uses, _ := accountUsesIn(reg, root, "ffffffffffffffffffffffffffffffff"); len(uses) != 0 {
		t.Fatalf("another account: %v", uses)
	}
	if err := os.WriteFile(f.cfg, []byte("not: [a configuration"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, complete := accountUsesIn(reg, root, id); complete {
		t.Fatal("an unreadable loop still counted as a complete answer")
	}
}

// The accounts command reaches connect, and refuses flags that belong to
// other operations.
func TestAccountsConnectThroughTheCommand(t *testing.T) {
	dir, _ := accountFixture(t, "codex")
	if runtime.GOOS == "windows" {
		// Unsupported there: the command says so and starts nothing.
		var out, errs bytes.Buffer
		code := accountsCommand(env{ctx: context.Background(), stdout: &out, stderr: &errs}, []string{"connect", "--directory", dir, "--provider", "codex", "--label", "Example spare"}, nil)
		if code != exitFailed || (!strings.Contains(errs.String(), "not supported on this system") && !strings.Contains(errs.String(), accountConnectPerson)) {
			t.Fatalf("exit %d, said %q", code, errs.String())
		}
	}
	var out, errs bytes.Buffer
	e := env{ctx: context.Background(), stdout: &out, stderr: &errs}
	if code := accountsCommand(e, []string{"connect", "--directory", dir, "--provider", "antigravity", "--label", "Example spare"}, nil); code != exitFailed || !strings.Contains(errs.String(), accountConnectAntigravity) {
		t.Fatalf("exit %d, said %q", code, errs.String())
	}
	for _, args := range [][]string{
		{"connect", "--directory", dir, "--provider", "codex", "--label", "x", "--account-dir", dir},
		{"connect", "--directory", dir, "--provider", "codex", "--label", "x", "--selection", "inherited"},
		{"disconnect", "--directory", dir, "--label", "x", "--provider", "codex"},
		{"connect", "--directory", dir, "--provider", "codex", "--label", "x", "--id", "0123"},
		{"connect", "--directory", dir, "--provider", "codex", "--label", "x", "--force"},
		{"list", "--directory", dir, "--like", "0123"},
	} {
		if code := accountsCommand(e, args, nil); code != exitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

// The new login is compared with the ones already connected by the
// catalog's login fingerprints. The same account under a second label is
// not added.
func TestAccountsConnectSameAccountCheck(t *testing.T) {
	// logins maps a label to the fingerprint its check records; "" records
	// none, as a check that could not read the login.
	withLogins := func(g *connectRig, logins map[string]string) *[]string {
		checked := []string{}
		g.deps.check = func(_ context.Context, dir, id string) error {
			f, err := accounts.Load(dir)
			if err != nil {
				return err
			}
			e := accounts.Find(&f, id)
			checked = append(checked, e.Label)
			if fp := logins[e.Label]; fp != "" {
				return accounts.RecordLogin(dir, id, fp, time.Now().UTC())
			}
			return nil
		}
		return &checked
	}
	opts := accountConnectOptions{provider: "claude-code", label: "Example spare"}

	t.Run("the same account", func(t *testing.T) {
		g := newConnectRig(t, "claude-code")
		checked := withLogins(g, map[string]string{"Example workshop": "aaaa", "Example spare": "aaaa"})
		if code := g.connect(opts); code != exitFailed {
			t.Fatalf("exit %d: %s", code, g.out.String())
		}
		said := g.err.String()
		if !strings.Contains(said, `the same account as "Example workshop", so it adds no second quota. It was not added.`) || !strings.Contains(said, "was kept: "+g.dir) || !strings.Contains(said, "It is still signed in. To sign it out: CLAUDE_CONFIG_DIR=") || !strings.Contains(said, " claude auth logout") {
			t.Fatalf("said %q", said)
		}
		if es := g.entries(t); len(es) != 1 || es[0].Label != "Example workshop" {
			t.Fatalf("entries %+v", es)
		}
		if !slices.Equal(*checked, []string{"Example spare", "Example workshop"}) {
			t.Fatalf("checked %v: the new entry, then the one with no fingerprint yet", *checked)
		}
	})
	t.Run("the same account, as JSON", func(t *testing.T) {
		g := newConnectRig(t, "claude-code")
		withLogins(g, map[string]string{"Example workshop": "aaaa", "Example spare": "aaaa"})
		o := opts
		o.asJSON = true
		g.connect(o)
		var got accountConnectResult
		if err := json.Unmarshal(g.out.Bytes(), &got); err != nil || got.State != "same_account" || got.SameAccount != "same" || got.ID != "" || !slices.Equal(got.SameAs, []string{"Example workshop"}) {
			t.Fatalf("%v %+v", err, got)
		}
	})
	t.Run("a different account", func(t *testing.T) {
		g := newConnectRig(t, "claude-code")
		withLogins(g, map[string]string{"Example workshop": "aaaa", "Example spare": "bbbb"})
		if code := g.connect(opts); code != exitOK || len(g.entries(t)) != 2 || !strings.Contains(g.out.String(), "A different account from the other claude-code logins connected.") {
			t.Fatalf("exit %d, %d entries: %s%s", code, len(g.entries(t)), g.out.String(), g.err.String())
		}
	})
	t.Run("another login could not be checked", func(t *testing.T) {
		g := newConnectRig(t, "claude-code")
		withLogins(g, map[string]string{"Example spare": "bbbb"})
		if code := g.connect(opts); code != exitOK || !strings.Contains(g.out.String(), "that could be checked; 1 could not be.") {
			t.Fatalf("exit %d: %s", code, g.out.String())
		}
	})
	t.Run("the new login could not be checked", func(t *testing.T) {
		g := newConnectRig(t, "claude-code")
		withLogins(g, map[string]string{"Example workshop": "aaaa"})
		if code := g.connect(opts); code != exitOK || len(g.entries(t)) != 2 || !strings.Contains(g.out.String(), accountConnectIdentityNote) {
			t.Fatalf("exit %d: %s", code, g.out.String())
		}
	})
	t.Run("Codex names no account to compare", func(t *testing.T) {
		g := newConnectRig(t, "codex")
		checked := withLogins(g, map[string]string{"Example workshop": "aaaa", "Example spare": "aaaa"})
		if code := g.connect(accountConnectOptions{provider: "codex", label: "Example spare"}); code != exitOK || len(*checked) != 0 || !strings.Contains(g.out.String(), accountConnectIdentityNote) {
			t.Fatalf("exit %d, checked %v: %s", code, *checked, g.out.String())
		}
	})
}

// The command is for a person at a terminal: through the real command, in a
// test process (no terminal), it refuses with the one sentence.
func TestAccountsConnectNeedsAPersonAtATerminal(t *testing.T) {
	dir, _ := accountFixture(t, "codex")
	var out, errs bytes.Buffer
	code := accountsCommand(env{ctx: context.Background(), stdout: &out, stderr: &errs}, []string{"connect", "--directory", dir, "--provider", "codex", "--label", "Example spare"}, nil)
	if code != exitFailed || strings.TrimSpace(errs.String()) != "accounts connect: "+accountConnectPerson {
		t.Fatalf("exit %d, said %q", code, errs.String())
	}
	for _, environ := range [][]string{{"BB_THREAD_ID=thr_example123"}, {"CLAUDECODE=1"}, {"CODEX_SANDBOX=seatbelt"}, {"codex_tool_call_id=x"}, {"CLAUDE_CODE_ENTRYPOINT=cli"}} {
		if !accountConnectInSession(environ) {
			t.Errorf("%v is not seen as a session", environ)
		}
	}
	if accountConnectInSession([]string{"HOME=/home/example", "CLAUDE_CONFIG_DIR=/home/example/c", "CODEX_HOME=/home/example/x", "BB_THREAD_ID="}) {
		t.Error("a plain shell is seen as a session")
	}
}
