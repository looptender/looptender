//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/daemoninfo"
	"github.com/looptender/looptender/internal/lock"
	rd "github.com/looptender/looptender/internal/rundiscovery"
	"github.com/looptender/looptender/internal/state"
)

func TestStartPreservesHoldsAndClaimsOwner(t *testing.T) {
	for _, hold := range []string{"none", "paused", "stop", "awaiting", "completed"} {
		t.Run(hold, func(t *testing.T) {
			f := newFixture(t)
			t.Setenv("BB_THREAD_ID", "thr_start_owner")
			cfg, err := config.Load(f.cfg)
			if err != nil {
				t.Fatal(err)
			}
			before := control.File{Note: "fictional hold", Paused: hold == "paused", Stop: hold == "stop", AwaitingUser: hold == "awaiting", Completed: hold == "completed"}
			if err := control.Save(cfg.Loop.StateDir, before); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var errs bytes.Buffer
			ready := false
			code := startCommand(env{ctx, io.Discard, &errs}, []string{"--foreground", "-c", f.cfg}, func(got control.File, registered bool) error {
				ready = true
				if _, err := os.Stat(filepath.Join(os.Getenv(registryDirEnv), "registry.json")); err != nil {
					t.Fatalf("start did not publish reference: %v", err)
				}
				j := controlHistory(t, f)
				if len(j.Records) != 1 || j.Records[0].Kind != "control" || j.Records[0].ControlChange.Operation != "start" || j.Records[0].ControlBefore.Enabled || !j.Records[0].ControlAfter.Enabled {
					t.Fatal(j)
				}

				if !got.Enabled || got.Ownership == nil || got.Ownership.ThreadID != "thr_start_owner" || got.Ownership.Generation != 1 {
					t.Fatalf("not enabled/claimed: %+v", got)
				}
				got.Enabled, got.Ownership = false, nil
				got.UpdatedAt = before.UpdatedAt
				if !reflect.DeepEqual(got, before) {
					t.Fatalf("holds changed: %+v", got)
				}
				if _, err := daemoninfo.Query(ctx, cfg.Loop.StateDir); err != nil {
					t.Fatal(err)
				}
				if l, err := lock.AcquireNamed(cfg.Loop.StateDir, lock.Daemon); !errors.Is(err, lock.ErrHeld) {
					if l != nil {
						l.Release()
					}
					t.Fatalf("daemon lock: %v", err)
				}
				cancel()
				return nil
			})
			if code != 0 || !ready || f.read("calls") != "" || f.read("probes") != "" {
				t.Fatalf("start: %d ready=%v errors=%s", code, ready, &errs)
			}
			if _, err := os.Stat(filepath.Join(cfg.Loop.StateDir, daemoninfo.FileName)); !os.IsNotExist(err) {
				t.Fatalf("endpoint survived: %v", err)
			}
		})
	}
}

func TestStartRefusesBeforeEnabling(t *testing.T) {
	for _, kind := range []string{"daemon", "tick", "control", "owner", "task", "cli", "workdir", "account", "process", "state", "target", "health", "diagnostics", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			cfg, err := config.Load(f.cfg)
			if err != nil {
				t.Fatal(err)
			}
			dir := cfg.Loop.StateDir
			ctl := control.File{Paused: true, Completed: true, Note: "fictional hold", Ownership: &control.Ownership{ThreadID: "thr_existing", Generation: 1}}
			if err := control.Save(dir, ctl); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := exitFailed
			switch kind {
			case "daemon", "tick", "control":
				l, err := lock.AcquireNamed(dir, kind)
				if err != nil {
					t.Fatal(err)
				}
				defer l.Release()
				want = exitSkipped
			case "owner":
				t.Setenv("BB_THREAD_ID", "thr_conflicting")
			case "task":
				cfg.Loop.PromptFile = f.dir // non-regular file, never read
			case "cli":
				cfg.Loop.BB.CLI = filepath.Join(f.dir, "absent")
			case "workdir", "account":
				cfg.Loop.Agent = config.Agent{Adapter: "codex", Codex: &config.Codex{CLI: filepath.Join(f.dir, "bin", "codex"), Workdir: f.dir}}
				if kind == "workdir" {
					cfg.Loop.Codex.Workdir = filepath.Join(f.dir, "missing")
				} else {
					cfg.Loop.Codex.Env = map[string]string{"CODEX_HOME": filepath.Join(f.dir, "missing")}
				}
			case "process":
				cfg.Loop.Agent = config.Agent{Adapter: "process", Process: &config.Process{StatusCommand: []string{filepath.Join(f.dir, "bin", "bb")}, ResumeCommand: []string{filepath.Join(f.dir, "missing")}}}
			case "state":
				write(t, filepath.Join(dir, state.FileName), "broken", 0o600)
			case "target":
				if err := state.Save(dir, state.File{ActiveTarget: "removed"}); err != nil {
					t.Fatal(err)
				}
			case "health":
				write(t, filepath.Join(dir, "health.json"), "broken", 0o600)
			case "diagnostics":
				if err := os.Mkdir(filepath.Join(dir, daemoninfo.FileName), 0o700); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			}
			before, _ := os.ReadFile(filepath.Join(dir, control.FileName))
			var errs bytes.Buffer
			code := startForeground(env{ctx, io.Discard, &errs}, cfg, f.cfg, func(control.File, bool) error { t.Error("unexpected readiness"); return nil }, false)
			after, _ := os.ReadFile(filepath.Join(dir, control.FileName))
			if code != want || !bytes.Equal(before, after) || f.read("calls") != "" || f.read("probes") != "" {
				t.Fatalf("code=%d want=%d mutated=%v errs=%s", code, want, !bytes.Equal(before, after), &errs)
			}
		})
	}
}

func TestStartFlagsAndBackgroundLaunchFailure(t *testing.T) {
	f := newFixture(t)
	for _, args := range [][]string{
		{"--foreground", "--background"}, {"--take-over"}, {"--dry-run"}, {"unexpected"},
		{"--now", "2026-01-01T00:00:00Z"}, {"--routine", ""}, {"--routine", "PRIVATE value"}, {"--routine", "a/b/c/d"},
	} {
		code, out, errs := f.run(append([]string{"start"}, args...)...)
		if code != exitUsage || strings.Contains(out+errs, "PRIVATE") || strings.Contains(out+errs, "a/b/c/d") {
			t.Fatalf("args %v code %d out=%s err=%s", args, code, out, errs)
		}
	}
	var jsonOut, jsonErr bytes.Buffer
	if code := startCommand(env{context.Background(), &jsonOut, &jsonErr}, []string{"--background", "--json", "-c", f.cfg, "--routine", "PRIVATE bad"}, nil); code != exitUsage {
		t.Fatalf("json invalid routine code %d", code)
	} else {
		var res startJSON
		if err := json.Unmarshal(jsonOut.Bytes(), &res); err != nil || res.Outcome != "failed" || res.Reason != "invalid_routine" || strings.Contains(jsonOut.String()+jsonErr.String(), "PRIVATE") {
			t.Fatalf("json invalid routine: %s %s", jsonOut.String(), jsonErr.String())
		}
	}
	var errs bytes.Buffer
	code := startBackground(env{context.Background(), io.Discard, &errs}, filepath.Join(f.dir, "missing"), f.cfg, false)
	if code != exitFailed || !strings.Contains(errs.String(), "cannot launch") {
		t.Fatalf("code %d: %s", code, &errs)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "state", control.FileName)); !os.IsNotExist(err) {
		t.Fatalf("launch failure wrote control: %v", err)
	}
}

func TestStartForegroundRecordsRoutineOpeningAndChildForwards(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var errs bytes.Buffer
	code := startCommand(env{ctx, io.Discard, &errs}, []string{"--foreground", "-c", f.cfg, "--routine", "example-scheduler/nightly/inv-start"}, func(control.File, bool) error {
		cancel()
		return nil
	})
	if code != 0 {
		t.Fatalf("start --routine: %d %s", code, errs.String())
	}
	g := graphOf(t, f)
	if len(g.Runs) != 1 || g.Runs[0].Origin != rd.Explicit || g.Runs[0].Routine == nil || g.Runs[0].Routine.Source != "example-scheduler" || g.Runs[0].Routine.Invocation != "inv-start" {
		t.Fatalf("start run routine: %#v", g.Runs)
	}

	// Background child path forwards --routine through __start-background.
	f2 := newFixture(t)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	var childOut, childErr bytes.Buffer
	var ackBytes []byte
	origRun := func() int {
		args, _, err := launchedChildArgs([]string{"-c", f2.cfg, "--routine", "example-scheduler/nightly/inv-bg"})
		if err != nil {
			t.Fatal(err)
		}
		return startCommand(env{ctx2, io.Discard, &childErr}, args, func(got control.File, _ bool) error {
			ackBytes, _ = json.Marshal(startReply{PID: os.Getpid(), Control: got.Summary()})
			cancel2()
			return nil
		})
	}
	if got := origRun(); got != 0 || len(ackBytes) == 0 || childOut.Len() != 0 {
		t.Fatalf("child start: %d %s", got, childErr.String())
	}
	g2 := graphOf(t, f2)
	if len(g2.Runs) != 1 || g2.Runs[0].Routine == nil || g2.Runs[0].Routine.Invocation != "inv-bg" {
		t.Fatalf("child routine: %#v", g2.Runs)
	}
}
