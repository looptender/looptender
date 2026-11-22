//go:build darwin || linux

package legacyretire

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/daemoninfo"
	"golang.org/x/sys/unix"
)

type appFixture struct {
	sel       Selection
	deps      appDeps
	root      string
	daemon    int
	calls     int
	control   []byte
	queryHook func()
	death     bool
}
type appObserver struct{ f *appFixture }

func (o *appObserver) Terminate(context.Context) error {
	o.f.calls++
	if !o.f.death {
		return nil
	}
	if e := os.Remove(filepath.Join(o.f.sel.StatePath, daemoninfo.FileName)); e != nil {
		return e
	}
	unix.Close(o.f.daemon)
	o.f.daemon = -1
	return nil
}
func (o *appObserver) WaitExit(context.Context) (bool, error) { return o.f.death, nil }
func (o *appObserver) Close() error                           { return nil }
func appFixtureNew(t *testing.T) *appFixture {
	t.Helper()
	root := canonicalTemp(t)
	// No subprocess or native API is used. Both resolver generations and platform
	// defaults are fictional nevertheless; no inherited account/path selection.
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "TMPDIR", "LOOPTENDER_SESSION_DIR", "KEEPGOING_SESSION_DIR", "LOOPTENDER_REGISTRY_DIR", "KEEPGOING_REGISTRY_DIR", "LOOPTENDER_ROUTES_FILE", "KEEPGOING_ROUTES_FILE", "CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		p := filepath.Join(root, key)
		t.Setenv(key, p)
	}
	for _, key := range []string{"BB_CLI", "BB_PROJECT_ID", "BB_ENVIRONMENT_ID", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"} {
		t.Setenv(key, "")
	}
	t.Setenv("BB_THREAD_ID", "thr_example")
	st := filepath.Join(root, "state")
	if e := os.Mkdir(st, 0700); e != nil {
		t.Fatal(e)
	}
	write := func(path string, b []byte) {
		t.Helper()
		if e := os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	cfg := filepath.Join(root, "loop.yaml")
	write(cfg, []byte("version: 1\nloop:\n  name: example-loop\n  state_dir: state\n  adapter: bb\n  provider: codex\n  bb: {thread: thr_example, cli: /fictional/absent-bb}\n  prompt: Continue the fictional loop.\n"))
	image := filepath.Join(root, "fictional-image")
	write(image, []byte("not an executable; never invoked"))
	ctl := []byte(`{"enabled":true,"paused":true,"stop":true,"awaitingUser":true,"completed":true,"ownership":{"threadId":"thr_example","generation":2}}`)
	write(filepath.Join(st, "control.json"), ctl)
	write(filepath.Join(st, "state.json"), []byte(`{"activeTarget":"primary"}`))
	write(filepath.Join(st, daemoninfo.FileName), []byte(`{"fictional":true}`))
	for _, name := range []string{"tick.lock", "control.lock", "daemon.lock"} {
		write(filepath.Join(st, name), nil)
	}
	fd, e := unix.Open(filepath.Join(st, "daemon.lock"), unix.O_RDWR, 0)
	if e != nil || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		t.Fatal(e)
	}
	f := &appFixture{root: root, daemon: fd, control: ctl, death: true}
	t.Cleanup(func() {
		if f.daemon >= 0 {
			unix.Close(f.daemon)
		}
	})
	f.sel = Selection{ConfigPath: cfg, StatePath: st, PID: 123, Build: "fictional-build", StartedAt: "2026-01-01T00:00:00Z", Executable: image, ExecutableDigest: digest([]byte("not an executable; never invoked")), Arguments: []string{image, "daemon", "-c", cfg}}
	f.deps = appDeps{
		inspect: func(pid int) (Process, error) {
			if pid != 123 {
				return Process{}, ErrUnavailable
			}
			return Process{Token: [8]uint32{1, 2, 3, 4, 5, 123, 7, 8}, Executable: image, Arguments: append([]string(nil), f.sel.Arguments...)}, nil
		},
		query: func(context.Context, string) (daemoninfo.Snapshot, error) {
			if f.queryHook != nil {
				f.queryHook()
			}
			at, _ := time.Parse(time.RFC3339, f.sel.StartedAt)
			return daemoninfo.Snapshot{ProcessID: 123, Version: f.sel.Build, StartedAt: at, StateID: digest([]byte(st))}, nil
		},
		arm: func(context.Context, Binding) (Observer, error) {
			for _, name := range []string{"tick.lock", "control.lock"} {
				fd, e := unix.Open(filepath.Join(st, name), unix.O_RDWR, 0)
				if e != nil {
					return nil, e
				}
				e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
				unix.Close(fd)
				if e == nil {
					return nil, fmt.Errorf("%s not retained", name)
				}
			}
			return &appObserver{f}, nil
		},
	}
	return f
}
func appContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}
func TestApplicationRetirementPreservesAllControlBytes(t *testing.T) {
	f := appFixtureNew(t)
	ctx := appContext(t)
	ticket, e := prepareApplication(ctx, f.sel, f.deps)
	if e != nil {
		t.Fatal(e)
	}
	e = Retire(ctx, ticket, func(ctx context.Context, _ Binding) (Guard, error) { return acquireApp(ctx, f.sel, f.deps) })
	if e != nil || f.calls != 1 {
		t.Fatal(e, f.calls)
	}
	raw, _ := os.ReadFile(filepath.Join(f.sel.StatePath, "control.json"))
	if !bytes.Equal(raw, f.control) {
		t.Fatal("controls changed")
	}
	outcome, e := Reconcile(ctx, ticket)
	if e != nil || outcome != "recorded-completion-not-launch-authority" {
		t.Fatal(outcome, e)
	}
}
func TestApplicationFinalSourceOrControlDriftRefuses(t *testing.T) {
	for _, name := range []string{"control", "source"} {
		t.Run(name, func(t *testing.T) {
			f := appFixtureNew(t)
			ctx := appContext(t)
			ticket, e := prepareApplication(ctx, f.sel, f.deps)
			if e != nil {
				t.Fatal(e)
			}
			queries := 0
			f.queryHook = func() {
				queries++
				// Acquire, initial validation, post-arm, then final consumed validation.
				if queries != 4 {
					return
				}
				p := f.sel.ConfigPath
				if name == "control" {
					p = filepath.Join(f.sel.StatePath, "control.json")
				}
				raw, _ := os.ReadFile(p)
				if e := os.WriteFile(p, append(raw, ' '), 0600); e != nil {
					t.Error(e)
				}
			}
			e = Retire(ctx, ticket, func(ctx context.Context, _ Binding) (Guard, error) { return acquireApp(ctx, f.sel, f.deps) })
			if e == nil || f.calls != 0 {
				t.Fatal(e, f.calls)
			}
		})
	}
}
func TestApplicationRefusesForeignOwnerManagedOrWrongInvocation(t *testing.T) {
	for _, kind := range []string{"foreign-owner", "manager-marker", "manager-argv", "wrong-build", "wrong-pid", "wrong-image", "wrong-state", "wrong-start"} {
		t.Run(kind, func(t *testing.T) {
			f := appFixtureNew(t)
			sel := f.sel
			switch kind {
			case "foreign-owner":
				t.Setenv("BB_THREAD_ID", "thr_other")
			case "manager-marker":
				if e := os.WriteFile(filepath.Join(sel.StatePath, "service-launchd.json"), []byte("{}"), 0600); e != nil {
					t.Fatal(e)
				}
			case "manager-argv":
				sel.Arguments = append([]string(nil), sel.Arguments...)
				sel.Arguments[1] = "__service-daemon"
			case "wrong-build":
				sel.Build = "other"
			case "wrong-pid":
				sel.PID = 124
			case "wrong-image":
				sel.ExecutableDigest = digest([]byte("other"))
			case "wrong-state":
				sel.StatePath = f.root
			case "wrong-start":
				sel.StartedAt = "2025-01-01T00:00:00Z"
			}
			if _, e := prepareApplication(appContext(t), sel, f.deps); e == nil || f.calls != 0 {
				t.Fatal(e, f.calls)
			}
		})
	}
}
func TestApplicationUnknownAndMissingAttemptNeverPrepareReplacement(t *testing.T) {
	f := appFixtureNew(t)
	ctx := appContext(t)
	ticket, e := prepareApplication(ctx, f.sel, f.deps)
	if e != nil {
		t.Fatal(e)
	}
	f.death = false
	e = Retire(ctx, ticket, func(ctx context.Context, _ Binding) (Guard, error) { return acquireApp(ctx, f.sel, f.deps) })
	if e == nil || f.calls != 1 {
		t.Fatal(e, f.calls)
	}
	if status, e := Reconcile(ctx, ticket); e != nil || status != "consumed-unknown" {
		t.Fatal(status, e)
	}
	if _, e := prepareApplication(ctx, f.sel, f.deps); !errors.Is(e, ErrSpent) {
		t.Fatal(e)
	}
	if e = os.Rename(ticket.Root, ticket.Root+".retained"); e != nil {
		t.Fatal(e)
	}
	if _, e := prepareApplication(ctx, f.sel, f.deps); !errors.Is(e, ErrSpent) {
		t.Fatal("replacement ticket", e)
	}
	if f.calls != 1 {
		t.Fatal(f.calls)
	}
}
func TestApplicationDrainDeadlineNeverArmsOrDispatches(t *testing.T) {
	f := appFixtureNew(t)
	fd, e := unix.Open(filepath.Join(f.sel.StatePath, "tick.lock"), unix.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer unix.Close(fd)
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, e = prepareApplication(ctx, f.sel, f.deps); !errors.Is(e, context.DeadlineExceeded) || f.calls != 0 {
		t.Fatal(e, f.calls)
	}
}

func TestApplicationCallerContextDriftAndUnavailableDaemonRefuse(t *testing.T) {
	for _, kind := range []string{"context", "endpoint", "daemon-lock", "owner-alias", "owner-null", "control-fifo"} {
		t.Run(kind, func(t *testing.T) {
			f := appFixtureNew(t)
			ctx := appContext(t)
			ticket, e := prepareApplication(ctx, f.sel, f.deps)
			if e != nil {
				t.Fatal(e)
			}
			p := filepath.Join(f.sel.StatePath, "control.json")
			switch kind {
			case "context":
				t.Setenv("BB_CLI", "/fictional/different-bb")
			case "endpoint":
				if e = os.Remove(filepath.Join(f.sel.StatePath, daemoninfo.FileName)); e != nil {
					t.Fatal(e)
				}
			case "daemon-lock":
				unix.Close(f.daemon)
				f.daemon = -1
			case "owner-alias":
				if e = os.WriteFile(p, []byte(`{"ownership":{"threadId":"thr_example","ThreadId":"thr_other","generation":2}}`), 0600); e != nil {
					t.Fatal(e)
				}
			case "owner-null":
				if e = os.WriteFile(p, []byte(`{"ownership":null}`), 0600); e != nil {
					t.Fatal(e)
				}
			case "control-fifo":
				if e = os.Remove(p); e != nil {
					t.Fatal(e)
				}
				if e = unix.Mkfifo(p, 0600); e != nil {
					t.Fatal(e)
				}
			}
			e = Retire(ctx, ticket, func(ctx context.Context, _ Binding) (Guard, error) { return acquireApp(ctx, f.sel, f.deps) })
			if e == nil || f.calls != 0 {
				t.Fatal(e, f.calls)
			}
		})
	}
}
func TestReconciliationNeverDispatchesOrClearsUnknown(t *testing.T) {
	ticket, g := ready(t)
	ctx := appContext(t)
	status, e := Reconcile(ctx, ticket)
	if e != nil || status != "prepared-no-dispatch-recorded" {
		t.Fatal(status, e)
	}
	g.terminateErr = errors.New("lost reply")
	if e = run(t, ticket, g, hooks{}); e == nil {
		t.Fatal("expected unknown")
	}
	for i := 0; i < 2; i++ {
		status, e = Reconcile(ctx, ticket)
		if e != nil || status != "consumed-unknown" {
			t.Fatal(status, e)
		}
	}
	if g.sent != 1 {
		t.Fatal(g.sent)
	}
	if e = os.WriteFile(filepath.Join(ticket.Root, "completed"), []byte("null"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Reconcile(ctx, ticket); e == nil {
		t.Fatal("corrupt completion accepted")
	}
}

func TestApplicationReservedAnchorDriftNeverDispatches(t *testing.T) {
	for _, kind := range []string{"missing", "replaced", "final-replaced"} {
		t.Run(kind, func(t *testing.T) {
			f := appFixtureNew(t)
			ctx := appContext(t)
			ticket, e := prepareApplication(ctx, f.sel, f.deps)
			if e != nil {
				t.Fatal(e)
			}
			anchor := ticket.Root + ".reserved"
			change := func() {
				raw, e := os.ReadFile(anchor)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.Rename(anchor, anchor+".preserved"); e != nil {
					t.Fatal(e)
				}
				if kind != "missing" {
					if e = os.WriteFile(anchor, raw, 0600); e != nil {
						t.Fatal(e)
					}
				}
			}
			if kind == "final-replaced" {
				queries := 0
				f.queryHook = func() {
					queries++
					if queries == 4 {
						change()
					}
				}
			} else {
				change()
			}
			e = Retire(ctx, ticket, func(ctx context.Context, b Binding) (Guard, error) {
				if e := validateApplicationTicket(ticket, b); e != nil {
					return nil, e
				}
				return acquireApp(ctx, f.sel, f.deps)
			})
			if e == nil || f.calls != 0 {
				t.Fatal(e, f.calls)
			}
			if _, e = Reconcile(ctx, ticket); e == nil {
				t.Fatal("replaced anchor accepted")
			}
		})
	}
}

func TestApplicationVaultParentSyncFailureNeverReturnsTicket(t *testing.T) {
	f := appFixtureNew(t)
	ctx := appContext(t)
	ticket, e := prepareApplicationWith(ctx, f.sel, f.deps, hooks{before: func(stage string) error {
		if stage == "incarnation-vault-parent-sync" {
			return errors.New("fictional parent sync failure")
		}
		return nil
	}})
	if e == nil || ticket.Root != "" || f.calls != 0 {
		t.Fatal(ticket, e, f.calls)
	}
	entries, e := os.ReadDir(filepath.Join(f.sel.StatePath, "legacy-retirement"))
	if e != nil {
		t.Fatal(e)
	}
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".reserved" {
		t.Fatal("partial reservation not retained", entries)
	}
	if _, e = prepareApplication(ctx, f.sel, f.deps); !errors.Is(e, ErrSpent) {
		t.Fatal("fresh prepare reconstructed ticket", e)
	}
	if f.calls != 0 {
		t.Fatal("dispatched", f.calls)
	}
}
