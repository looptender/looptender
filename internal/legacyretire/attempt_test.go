//go:build darwin || linux

package legacyretire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func canonicalTemp(t *testing.T) string {
	t.Helper()
	p, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func fictional() Binding {
	h := strings.Repeat("a", 64)
	return Binding{ConfigPath: "/fictional/example/loop.yaml", StatePath: "/fictional/example/state", SourceDigest: h, ControlDigest: h,
		ControlIdentity: Identity{1, 2}, Owner: "thread_example", Generation: 2, PID: 123, Build: "fictional-build", StartedAt: "2026-01-01T00:00:00Z",
		StateID: h, Executable: "/fictional/example/looptender", ExecutableDigest: h, Arguments: []string{"daemon", "-c", "/fictional/example/loop.yaml"}, Token: [8]uint32{1, 2, 3, 4, 5, 123, 7, 8}}
}

type fakeGuard struct {
	b                  Binding
	held               bool
	verifies           int
	driftAt            int
	drift              func(*Binding)
	sent               int
	dead               bool
	endpoint, lockFree bool
	terminateErr       error
	observerErr        error
	retiredErr         error
	during             func()
}

func (g *fakeGuard) Verify(context.Context) (Binding, error) {
	if !g.held {
		return Binding{}, errors.New("guards not held")
	}
	g.verifies++
	b := g.b
	if g.verifies == g.driftAt {
		g.drift(&b)
	}
	return b, nil
}
func (g *fakeGuard) Arm(context.Context, Binding) (Observer, error) {
	if !g.held {
		return nil, ErrUnavailable
	}
	return g, nil
}
func (g *fakeGuard) Close() error { g.held = false; return nil }
func (g *fakeGuard) Terminate(context.Context) error {
	if !g.held || g.verifies != 3 {
		return errors.New("wrong final-check/guard ordering")
	}
	g.sent++
	if g.during != nil {
		g.during()
	}
	return g.terminateErr
}
func (g *fakeGuard) WaitExit(context.Context) (bool, error) { return g.dead, g.observerErr }
func (g *fakeGuard) Retired(context.Context, Binding) (Evidence, error) {
	if !g.held {
		return Evidence{}, ErrUnavailable
	}
	return Evidence{g.endpoint, g.lockFree, g.b.ControlDigest, g.b.ControlIdentity}, g.retiredErr
}
func ready(t *testing.T) (Ticket, *fakeGuard) {
	t.Helper()
	b := fictional()
	ticket, e := Prepare(filepath.Join(canonicalTemp(t), "attempt"), b)
	if e != nil {
		t.Fatal(e)
	}
	return ticket, &fakeGuard{b: b, dead: true, endpoint: true, lockFree: true}
}
func run(t *testing.T, ticket Ticket, g *fakeGuard, h hooks) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return retire(ctx, ticket, func(context.Context, Binding) (Guard, error) { g.held = true; return g, nil }, h)
}
func TestCompletionAndFreshHelperRefusesReplay(t *testing.T) {
	ticket, g := ready(t)
	if e := run(t, ticket, g, hooks{}); e != nil {
		t.Fatal(e)
	}
	if g.sent != 1 {
		t.Fatal(g.sent)
	}
	if e := run(t, ticket, &fakeGuard{b: g.b}, hooks{}); !errors.Is(e, ErrSpent) {
		t.Fatal(e)
	}
	if b, e := os.ReadFile(filepath.Join(ticket.Root, "completed")); e != nil || string(b) != ticket.Digest {
		t.Fatal(e)
	}
}
func TestUnknownNeverAuthorizesRetry(t *testing.T) {
	cases := []struct {
		name   string
		change func(*fakeGuard)
	}{
		{"lost-signal-reply", func(g *fakeGuard) { g.terminateErr = errors.New("lost reply") }},
		{"accepted-alive-even-with-endpoint-and-lock-gone", func(g *fakeGuard) { g.dead = false }},
		{"exec-or-observer-loss", func(g *fakeGuard) { g.observerErr = errors.New("exec") }},
		{"endpoint-replacement", func(g *fakeGuard) { g.endpoint = false }},
		{"daemon-lock-still-held", func(g *fakeGuard) { g.lockFree = false }},
		{"control-or-different-invocation", func(g *fakeGuard) { g.retiredErr = ErrUnavailable }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ticket, g := ready(t)
			c.change(g)
			if e := run(t, ticket, g, hooks{}); e == nil {
				t.Fatal("unknown completed")
			}
			if g.sent != 1 {
				t.Fatal(g.sent)
			}
			next := &fakeGuard{b: g.b}
			if e := run(t, ticket, next, hooks{}); !errors.Is(e, ErrSpent) || next.sent != 0 {
				t.Fatal(e, next.sent)
			}
			if _, e := os.Stat(filepath.Join(ticket.Root, "completed")); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("completion fabricated", e)
			}
		})
	}
}
func TestFinalBindingDriftSpendsAttemptWithoutDispatch(t *testing.T) {
	changes := map[string]func(*Binding){
		"owner": func(b *Binding) { b.Owner = "thread_other" }, "generation": func(b *Binding) { b.Generation++ },
		"source": func(b *Binding) { b.SourceDigest = strings.Repeat("b", 64) }, "control": func(b *Binding) { b.ControlDigest = strings.Repeat("b", 64) },
		"native-control": func(b *Binding) { b.ControlIdentity.Inode++ }, "incarnation": func(b *Binding) { b.Token[7]++ },
		"build": func(b *Binding) { b.Build = "other" }, "invocation": func(b *Binding) { b.StartedAt = "other" },
		"config": func(b *Binding) { b.ConfigPath = "/fictional/other" }, "state": func(b *Binding) { b.StatePath = "/fictional/other" },
		"executable": func(b *Binding) { b.ExecutableDigest = strings.Repeat("b", 64) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			ticket, g := ready(t)
			g.driftAt = 3
			g.drift = change
			if e := run(t, ticket, g, hooks{}); e == nil || g.sent != 0 {
				t.Fatal(e, g.sent)
			}
			if e := run(t, ticket, &fakeGuard{b: g.b}, hooks{}); !errors.Is(e, ErrSpent) {
				t.Fatal(e)
			}
		})
	}
}
func TestPublicationFailures(t *testing.T) {
	for _, stage := range []string{"consume-unlink", "consume-directory-sync", "completed-create", "completed-write", "completed-sync", "completed-directory-sync", "completion-ack"} {
		t.Run(stage, func(t *testing.T) {
			ticket, g := ready(t)
			e := run(t, ticket, g, hooks{before: func(at string) error {
				if at == stage {
					return errors.New("injected")
				}
				return nil
			}})
			if e == nil {
				t.Fatal("failure lost")
			}
			expected := 1
			if strings.HasPrefix(stage, "consume") {
				expected = 0
			}
			if g.sent != expected {
				t.Fatal(g.sent)
			}
			next := &fakeGuard{b: g.b}
			if e := run(t, ticket, next, hooks{}); !errors.Is(e, ErrSpent) || next.sent != 0 {
				t.Fatal(e, next.sent)
			}
		})
	}
	// Failure before publication dispatches nothing, retains prepared evidence.
	t.Run("before-consume-link", func(t *testing.T) {
		ticket, g := ready(t)
		if e := run(t, ticket, g, hooks{before: func(at string) error {
			if at == "consume-link" {
				return errors.New("no publication attempted")
			}
			return nil
		}}); e == nil || g.sent != 0 {
			t.Fatal(e, g.sent)
		}
		if _, e := os.Stat(filepath.Join(ticket.Root, "ready")); e != nil {
			t.Fatal(e)
		}
	})
}
func TestMissingCorruptCopiedAndReplacedFilesRefuse(t *testing.T) {
	for _, name := range []string{"intent", "reservation", "ready"} {
		for _, mutation := range []string{"missing", "corrupt", "copied", "symlink"} {
			t.Run(name+"/"+mutation, func(t *testing.T) {
				ticket, g := ready(t)
				p := filepath.Join(ticket.Root, name)
				raw, e := os.ReadFile(p)
				if e != nil {
					t.Fatal(e)
				}
				switch mutation {
				case "missing":
					e = os.Remove(p)
				case "corrupt":
					e = os.WriteFile(p, []byte("null"), 0600)
				case "copied":
					e = os.Rename(p, p+".old")
					if e == nil {
						e = os.WriteFile(p, raw, 0600)
					}
				case "symlink":
					e = os.Rename(p, p+".old")
					if e == nil {
						e = os.Symlink(p+".old", p)
					}
				}
				if e != nil {
					t.Fatal(e)
				}
				if e = run(t, ticket, g, hooks{}); e == nil || g.sent != 0 {
					t.Fatal(e, g.sent)
				}
				if mutation == "missing" {
					if _, e = os.Lstat(p); !errors.Is(e, os.ErrNotExist) {
						t.Fatal("recreated", e)
					}
				}
			})
		}
	}
	t.Run("copied-directory", func(t *testing.T) {
		ticket, g := ready(t)
		other := filepath.Join(canonicalTemp(t), "copy")
		if e := os.Mkdir(other, 0700); e != nil {
			t.Fatal(e)
		}
		for _, n := range []string{"intent", "reservation", "ready"} {
			b, _ := os.ReadFile(filepath.Join(ticket.Root, n))
			if e := os.WriteFile(filepath.Join(other, n), b, 0600); e != nil {
				t.Fatal(e)
			}
		}
		ticket.Root = other
		if e := run(t, ticket, g, hooks{}); e == nil || g.sent != 0 {
			t.Fatal(e, g.sent)
		}
	})
}
func TestReservationReplacementAfterConsumptionRefuses(t *testing.T) {
	ticket, g := ready(t)
	e := run(t, ticket, g, hooks{before: func(at string) error {
		if at == "consume-directory-sync" {
			p := filepath.Join(ticket.Root, "reservation")
			if e := os.Rename(p, p+".old"); e != nil {
				return e
			}
			return os.WriteFile(p, []byte(ticket.Digest), 0600)
		}
		return nil
	}})
	if e == nil || g.sent != 0 {
		t.Fatal(e, g.sent)
	}
}
func TestTwoHelpersDispatchAtMostOnce(t *testing.T) {
	ticket, g := ready(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	var sent atomic.Int32
	g.during = func() { sent.Add(1); close(entered); <-release }
	go func() { done <- run(t, ticket, g, hooks{}) }()
	<-entered
	other := &fakeGuard{b: g.b}
	if e := run(t, ticket, other, hooks{}); e == nil || other.sent != 0 {
		t.Fatal(e, other.sent)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if sent.Load() != 1 {
		t.Fatal(sent.Load())
	}
}
func TestPreparationFailureDoesNotReturnReusableTicket(t *testing.T) {
	for _, stage := range []string{"intent-write", "intent-sync", "reservation-write", "ready-sync", "prepare-directory-sync"} {
		t.Run(stage, func(t *testing.T) {
			p := filepath.Join(canonicalTemp(t), "attempt")
			ticket, e := prepare(p, fictional(), hooks{before: func(at string) error {
				if at == stage {
					return errors.New("injected")
				}
				return nil
			}})
			if e == nil || ticket.Root != "" {
				t.Fatal(e, ticket)
			}
			if _, e = Prepare(p, fictional()); e == nil {
				t.Fatal("repaired failed preparation")
			}
		})
	}
}

func TestCanonicalIntentRejectsAmbiguousJSONEvenWithMatchingDigest(t *testing.T) {
	for _, kind := range []string{"duplicate", "case-alias", "unknown", "null", "schema", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			ticket, g := ready(t)
			p := filepath.Join(ticket.Root, "intent")
			raw, e := os.ReadFile(p)
			if e != nil {
				t.Fatal(e)
			}
			s := string(raw)
			switch kind {
			case "duplicate":
				s = strings.Replace(s, `"schema":1`, `"schema":1,"schema":1`, 1)
			case "case-alias":
				s = strings.Replace(s, `"schema":1`, `"Schema":1`, 1)
			case "unknown":
				s = strings.Replace(s, `"schema":1`, `"schema":1,"futureHold":true`, 1)
			case "null":
				s = strings.Replace(s, `"generation":2`, `"generation":null`, 1)
			case "schema":
				s = strings.Replace(s, `"schema":1`, `"schema":2`, 1)
			case "trailing":
				s += " null"
			}
			ticket.Digest = digest([]byte(s))
			if e = os.WriteFile(p, []byte(s), 0600); e != nil {
				t.Fatal(e)
			}
			for _, name := range []string{"reservation", "ready"} {
				if e = os.WriteFile(filepath.Join(ticket.Root, name), []byte(ticket.Digest), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if e = run(t, ticket, g, hooks{}); e == nil || g.sent != 0 {
				t.Fatal(e, g.sent)
			}
		})
	}
}
func TestRemovedSpentDoesNotRecreatePrepared(t *testing.T) {
	ticket, g := ready(t)
	g.terminateErr = errors.New("lost reply")
	if e := run(t, ticket, g, hooks{}); e == nil {
		t.Fatal("expected unknown")
	}
	if e := os.Remove(filepath.Join(ticket.Root, "spent")); e != nil {
		t.Fatal(e)
	}
	next := &fakeGuard{b: g.b}
	if e := run(t, ticket, next, hooks{}); e == nil || next.sent != 0 {
		t.Fatal(e, next.sent)
	}
}
