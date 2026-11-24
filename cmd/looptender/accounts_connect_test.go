package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
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
		environ: []string{"HOME=/home/example", "PATH=/usr/bin"},
		now:     func() time.Time { return time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC) },
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
			text := g.out.String()
			for _, want := range []string{`Connected "Example spare"`, accountConnectIdentityNote, "https://auth.example.com/sign-in?attempt=one", "does not see or copy the login"} {
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
		{name: "no label", o: accountConnectOptions{provider: "codex"}, want: "--label"},
		{name: "a label in use", o: accountConnectOptions{provider: "codex", label: "example WORKSHOP"}, want: "--label"},
		{name: "an unsupported system", o: accountConnectOptions{provider: "codex", label: "Example spare"}, change: func(g *connectRig) { g.deps.supported = func(string) bool { return false } }, want: "not supported on this system"},
		{name: "a provider key in the shell", o: accountConnectOptions{provider: "codex", label: "Example spare"}, change: func(g *connectRig) { g.deps.environ = append(g.deps.environ, "OPENAI_API_KEY=example") }, want: "plain terminal"},
		{name: "an executable that is not admitted", o: accountConnectOptions{provider: "codex", label: "Example spare"}, change: func(g *connectRig) {
			g.deps.admit = func(context.Context, accounts.Entry) (string, error) { return "", errConnectProtocol }
		}, want: "CODEX_HOME set to it"},
		{name: "an executable that changed", o: accountConnectOptions{provider: "codex", label: "Example spare"}, change: func(g *connectRig) {
			g.deps.stage = func(context.Context, string, string) (string, func(), error) { return "", nil, errors.New("changed") }
		}, want: "changed while it was being checked"},
		{name: "a template of another provider", o: accountConnectOptions{provider: "claude-code", label: "Example spare", like: "-"}, want: "--id"},
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
			if text := g.err.String(); !strings.Contains(text, c.want) || (c.o.provider != "antigravity" && !strings.Contains(text, "No sign-in was started.")) {
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

// A sign-in that fails, is cancelled or runs out of time adds nothing and
// keeps the directory, since the provider may have written into it.
func TestAccountsConnectFailedSignInAddsNothing(t *testing.T) {
	for name, run := range map[string]accountConnectRun{
		"failed": func(context.Context, accounts.Entry, []string, func(string)) error { return errConnectProtocol },
	} {
		t.Run(name, func(t *testing.T) {
			g := newConnectRig(t, "codex")
			g.deps.run = run
			if code := g.connect(accountConnectOptions{provider: "codex", label: "Example spare"}); code != exitFailed {
				t.Fatalf("exit %d", code)
			}
			if len(g.entries(t)) != 1 || !strings.Contains(g.err.String(), "nothing was added") || !strings.Contains(g.err.String(), "was kept: "+g.dir) {
				t.Fatalf("entries %d, said %q", len(g.entries(t)), g.err.String())
			}
		})
	}
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
// prints the provider's own sign-out command.
func TestAccountsDisconnect(t *testing.T) {
	g := newConnectRig(t, "codex")
	g.connect(accountConnectOptions{provider: "codex", label: "Example spare"})
	added := g.entries(t)[1]
	run := func(args ...string) (int, string, string) {
		var out, errs bytes.Buffer
		code := accountsCommand(env{ctx: context.Background(), stdout: &out, stderr: &errs}, append(args, "--directory", g.dir), nil)
		return code, out.String(), errs.String()
	}
	if code, _, _ := run("disconnect"); code != exitUsage {
		t.Fatalf("no name: exit %d", code)
	}
	if code, _, said := run("disconnect", "--label", "Example nobody"); code != exitFailed || !strings.Contains(said, "nothing was changed") || len(g.entries(t)) != 2 {
		t.Fatalf("unknown label: exit %d, %q", code, said)
	}
	code, out, said := run("disconnect", "--label", "example SPARE")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, said)
	}
	if es := g.entries(t); len(es) != 1 || es[0].ID != g.template.ID {
		t.Fatalf("entries %+v", es)
	}
	if _, err := os.Stat(added.Directory); err != nil {
		t.Fatal("disconnect removed the login's directory")
	}
	for _, want := range []string{`Disconnected "Example spare"`, "CODEX_HOME=" + shellQuote(added.Directory) + " codex logout", "not signed out", "looptender doctor"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// By id, and an inherited entry's sign-out has no directory.
	if code, out, _ = run("disconnect", "--id", g.template.ID); code != exitOK || !strings.Contains(out, "\n  codex logout\n") || len(g.entries(t)) != 0 {
		t.Fatalf("by id: exit %d\n%s", code, out)
	}
}

// The accounts command reaches connect, and refuses flags that belong to
// other operations.
func TestAccountsConnectThroughTheCommand(t *testing.T) {
	dir, _ := accountFixture(t, "codex")
	var out, errs bytes.Buffer
	e := env{ctx: context.Background(), stdout: &out, stderr: &errs}
	if code := accountsCommand(e, []string{"connect", "--directory", dir, "--provider", "antigravity", "--label", "Example spare"}, nil); code != exitFailed || !strings.Contains(errs.String(), accountConnectAntigravity) {
		t.Fatalf("exit %d, said %q", code, errs.String())
	}
	for _, args := range [][]string{
		{"connect", "--directory", dir, "--provider", "codex", "--label", "x", "--account-dir", dir},
		{"connect", "--directory", dir, "--provider", "codex", "--label", "x", "--selection", "inherited"},
		{"disconnect", "--directory", dir, "--label", "x", "--provider", "codex"},
	} {
		if code := accountsCommand(e, args, nil); code != exitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}
