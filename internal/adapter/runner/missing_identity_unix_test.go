//go:build unix

package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/adapter"
	"github.com/looptender/looptender/internal/decide"
	"github.com/looptender/looptender/internal/lock"
)

func TestHeadlessMissingIdentityRefusesFreshReplay(t *testing.T) {
	for _, spec := range []*Spec{claudeSpec, codexSpec} {
		t.Run(spec.Name, func(t *testing.T) {
			root := t.TempDir()
			cli := fakeCLI(t, `if [ "$1" = app-server ]; then exit 1; fi; cat >/dev/null; printf 'turn\n' >> "$PWD/calls"; printf '%s\n' "$@" > "$PWD/args"; exit 1`)
			h := &Headless{spec: spec, set: Settings{CLI: cli, Workdir: root}, dir: Dir(filepath.Join(root, "target"))}
			ctx := context.Background()
			if o, e := h.Observe(ctx); e != nil || o.SessionLost || o.State != decide.Idle {
				t.Fatalf("first: %+v %v", o, e)
			}
			if e := h.Send(ctx, "fictional task", adapter.ModeAuto); e != nil {
				t.Fatal(e)
			}
			waitPhase(t, h.dir, Finished)
			if e := h.Send(ctx, "direct replay without observation", adapter.ModeAuto); !errors.Is(e, errSessionLost) {
				if e == nil {
					waitPhase(t, h.dir, Finished)
				}
				t.Fatalf("direct replay: %v", e)
			}
			for i := 0; i < 3; i++ {
				// Reconstruct as after restart; no in-memory flag grants permission.
				h = &Headless{spec: spec, set: h.set, dir: h.dir}
				if o, e := h.Observe(ctx); e != nil || !o.SessionLost {
					t.Fatalf("finished: %+v %v", o, e)
				}
				if e := h.Send(ctx, "must not replay", adapter.ModeAuto); !errors.Is(e, errSessionLost) {
					t.Fatalf("send: %v", e)
				}
			}
			if b, _ := os.ReadFile(filepath.Join(root, "calls")); string(b) != "turn\n" {
				t.Fatalf("replayed: %q", b)
			}
			if _, e := h.ResetSession(); e != nil {
				t.Fatal(e)
			}
			if o, e := h.Observe(ctx); e != nil || o.SessionLost {
				t.Fatalf("reset: %+v %v", o, e)
			}
			if e := h.Send(ctx, "explicit fresh attempt", adapter.ModeAuto); e != nil {
				t.Fatal(e)
			}
			waitPhase(t, h.dir, Finished)
			h = &Headless{spec: spec, set: h.set, dir: h.dir}
			if e := h.Send(ctx, "must not reuse reset", adapter.ModeAuto); !errors.Is(e, errSessionLost) {
				t.Fatal(e)
			}
			if b, _ := os.ReadFile(filepath.Join(root, "calls")); string(b) != "turn\nturn\n" {
				t.Fatalf("reset replayed: %q", b)
			}
			// An existing verified identifier still selects exact resume, even when
			// the last failure has no usable output. No replacement ID is invented.
			id := "00000000-0000-4000-8000-0000000000aa"
			if e := h.dir.SaveSession(id); e != nil {
				t.Fatal(e)
			}
			if e := h.Send(ctx, "continue known session", adapter.ModeAuto); e != nil {
				t.Fatal(e)
			}
			waitPhase(t, h.dir, Finished)
			if b, _ := os.ReadFile(filepath.Join(root, "args")); !strings.Contains(string(b), id) || !strings.Contains(string(b), "resume") {
				t.Fatalf("not resumed: %q", b)
			}
		})
	}
}

func TestHeadlessMissingIdentityEvidenceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		output    string
		exit      string
		abandoned bool
		wantLost  bool
	}{
		{name: "crashed", wantLost: true},
		{name: "successful without identity", output: `{"type":"result","subtype":"success"}`, exit: "0", wantLost: true},
		{name: "malformed output", output: `{`, exit: "1", wantLost: true},
		{name: "oversized output", output: strings.Repeat("x", 256), exit: "1", wantLost: true},
		{name: "abandoned before agent start", abandoned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := Record{StartedAt: time.Now().Add(-time.Hour), Liveness: livenessLock, Abandoned: tc.abandoned}
			d := writeRecord(t, rec)
			if tc.exit != "" {
				if e := os.WriteFile(d.path("exit"), []byte(tc.exit), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if e := os.WriteFile(d.path("stdout"), []byte(tc.output), 0600); e != nil {
				t.Fatal(e)
			}
			spec := *claudeSpec
			spec.MaxOutput = 128
			h := &Headless{spec: &spec, dir: d}
			o, e := h.Observe(context.Background())
			if e != nil || o.SessionLost != tc.wantLost {
				t.Fatalf("%+v %v", o, e)
			}
		})
	}
}

func TestHeadlessMissingIdentityRunningAndUnavailableRecords(t *testing.T) {
	d := writeRecord(t, Record{StartedAt: time.Now(), Liveness: livenessLock})
	h := &Headless{spec: claudeSpec, dir: d}
	guard, e := lock.AcquireNamed(string(d), turnLock)
	if e != nil {
		t.Fatal(e)
	}
	o, e := h.Observe(context.Background())
	guard.Release()
	if e != nil || o.State != decide.Active || o.SessionLost {
		t.Fatalf("running: %+v %v", o, e)
	}
	if e := os.WriteFile(d.path("run.json"), []byte("{"), 0600); e != nil {
		t.Fatal(e)
	}
	if o, e := h.Observe(context.Background()); e == nil || o.State != decide.Unknown || o.SessionLost {
		t.Fatalf("unavailable: %+v %v", o, e)
	}
	if e := h.Send(context.Background(), "must not run", adapter.ModeAuto); e == nil {
		t.Fatal("unavailable record allowed send")
	}
}

func TestHeadlessMissingIdentityHoldsBeforeAvailableFallback(t *testing.T) {
	for _, spec := range []*Spec{claudeSpec, codexSpec} {
		t.Run(spec.Name, func(t *testing.T) {
			d := writeRecord(t, Record{StartedAt: time.Now().Add(-time.Hour), Liveness: livenessLock})
			output := `{"type":"result","is_error":true,"api_error_status":429}`
			if spec.Name == "codex" {
				output = `{"type":"turn.failed","error":{"code":"usage_limit_reached"}}`
			}
			for name, value := range map[string]string{"exit": "1", "stdout": output} {
				if e := os.WriteFile(d.path(name), []byte(value), 0600); e != nil {
					t.Fatal(e)
				}
			}
			h := &Headless{spec: spec, dir: d}
			o, e := h.Observe(context.Background())
			if e != nil || !o.SessionLost || o.ErrorClass != decide.ErrRateLimit {
				t.Fatalf("observation: %+v %v", o, e)
			}
			in := decide.Input{Obs: o, Control: decide.Control{Enabled: true}, InWindow: true, Failover: decide.Failover{Eligible: true, Available: true}}
			if got := decide.Decide(in); got.Action != decide.AwaitUser || got.Reason != decide.SessionLostReason {
				t.Fatalf("identity did not hold: %+v", got)
			}
			// The same structured limit could hand off if identity were known.
			// This guards ordering, not just an input that could never move anyway.
			in.Obs.SessionLost = false
			if got := decide.Decide(in); got.Action != decide.Handoff {
				t.Fatalf("negative control: %+v", got)
			}
		})
	}
}

// An active first turn can be interrupted before any identity is published.
// This remains a recovery hold after reconstructing the adapter; the normal
// initialized-session conformance journey must not conceal this edge case.
func TestHeadlessMissingIdentityAfterInterruptedFirstTurn(t *testing.T) {
	for _, spec := range []*Spec{claudeSpec, codexSpec} {
		t.Run(spec.Name, func(t *testing.T) {
			root := t.TempDir()
			cli := fakeCLI(t, `cat >/dev/null; printf 'turn\n' >> "$PWD/calls"; sleep 30`)
			h := &Headless{spec: spec, set: Settings{CLI: cli, Workdir: root}, dir: Dir(filepath.Join(root, "target"))}
			t.Cleanup(func() { _ = h.Stop(context.Background()) })
			ctx := context.Background()
			if err := h.Send(ctx, "fictional first task", adapter.ModeAuto); err != nil {
				t.Fatal(err)
			}
			h = &Headless{spec: spec, set: h.set, dir: h.dir}
			if o, e := h.Observe(ctx); e != nil || o.State != decide.Active || o.SessionLost {
				t.Fatalf("active before identity: %+v %v", o, e)
			}
			if e := h.Stop(ctx); e != nil {
				t.Fatal(e)
			}
			waitIdentityTurnTerminated(t, h.dir)
			prior, err := os.ReadFile(filepath.Join(string(h.dir), "run.json"))
			if err != nil {
				t.Fatal(err)
			}
			h = &Headless{spec: spec, set: h.set, dir: h.dir}
			if o, e := h.Observe(ctx); e != nil || !o.SessionLost {
				t.Fatalf("interrupted before identity: %+v %v", o, e)
			}
			if e := h.Send(ctx, "refuse fresh replay", adapter.ModeAuto); !errors.Is(e, errSessionLost) {
				t.Fatalf("replay: %v", e)
			}
			after, err := os.ReadFile(filepath.Join(string(h.dir), "run.json"))
			if err != nil || string(after) != string(prior) {
				t.Fatalf("refused send launched another turn: %v", err)
			}
			if _, e := h.ResetSession(); e != nil {
				t.Fatal(e)
			}
			if e := h.Send(ctx, "explicit fresh attempt", adapter.ModeAuto); e != nil {
				t.Fatal(e)
			}
			if e := h.Stop(ctx); e != nil {
				t.Fatal(e)
			}
			waitIdentityTurnTerminated(t, h.dir)
		})
	}
}

// Termination does not establish whether the provider completed any work.
func waitIdentityTurnTerminated(t *testing.T, d Dir) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st, err := d.Status()
		if err != nil {
			t.Fatal(err)
		}
		if st.Phase == Finished || st.Phase == Crashed {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("turn did not terminate")
}
