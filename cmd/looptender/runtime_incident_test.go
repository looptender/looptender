//go:build unix

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/incident"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/supervise"
)

func TestRefusedCheckEvents(t *testing.T) {
	for _, reason := range []string{"lock_held", "lock_unavailable", "ownership_unverified", "sources_unverified", "safety_guard"} {
		t.Run(reason, func(t *testing.T) {
			f := newFixture(t)
			cfg, err := config.Load(f.cfg)
			if err != nil {
				t.Fatal(err)
			}
			dir := cfg.Loop.StateDir
			t.Setenv("BB_THREAD_ID", "thr_fixture_intruder")
			switch reason {
			case "lock_held":
				l, err := lock.Acquire(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer l.Release()
			case "lock_unavailable":
				if err := os.MkdirAll(filepath.Join(dir, "tick.lock"), 0700); err != nil {
					t.Fatal(err)
				}
			case "ownership_unverified":
				if err := control.Save(dir, control.File{Ownership: &control.Ownership{ThreadID: "thr_fixture_owner", Generation: 1}}); err != nil {
					t.Fatal(err)
				}
			case "sources_unverified":
				write(t, f.cfg, "invalid", 0600)
			}
			var errs bytes.Buffer
			if reason == "safety_guard" {
				_, err = supervise.Tick(context.Background(), cfg, supervise.Options{LastConfigReadAt: time.Now(), DryRun: true})
				if err == nil {
					t.Fatal("guard accepted")
				}
			} else {
				if code := tickOnce(env{context.Background(), io.Discard, &errs}, cfg, time.Now(), true, false); code == exitOK {
					t.Fatal("check accepted", errs.String())
				}
			}
			j, err := incident.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(j.Records) != 1 {
				t.Fatal(j)
			}
			r := j.Records[0]
			if r.Kind != "check_refused" || r.Runtime.Reason != reason || !r.DryRun || r.Observation.Availability != "not_observed" {
				t.Fatal(r)
			}
			if f.read("calls") != "" || f.read("probes") != "" {
				t.Fatal("provider work on refusal")
			}
		})
	}
}

func TestDaemonLifecycleEvents(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		for _, reason := range []string{"cancelled", "signal", "error_exit", "clean_exit"} {
			t.Run(fmt.Sprintf("%s/dry=%t", reason, dryRun), func(t *testing.T) {
				f := newFixture(t)
				cfg, err := config.Load(f.cfg)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				receivedSignal.Store("")
				defer receivedSignal.Store("")
				lease := &ownerLease{}
				switch reason {
				case "cancelled":
					cancel()
				case "signal":
					receivedSignal.Store("SIGTERM")
					cancel()
				case "error_exit":
					lease.generation = 99
				case "clean_exit":
					if code, _, errs := f.run("archive"); code != 0 {
						t.Fatal(errs)
					}
				}
				var errs bytes.Buffer
				code := daemonLoop(env{ctx, io.Discard, &errs}, cfg, f.cfg, dryRun, lease, nil, nil)
				if (code != exitOK) != (reason == "error_exit") {
					t.Fatal(code, errs.String())
				}
				j, err := incident.Load(cfg.Loop.StateDir)
				if err != nil {
					t.Fatal(err)
				}
				first, last := j.Records[0], j.Records[len(j.Records)-1]
				if first.Kind != "daemon_start" || last.Kind != "daemon_stop" || last.Runtime.Reason != reason || first.DryRun != dryRun || last.DryRun != dryRun {
					t.Fatal(j)
				}
				if !containsString(timelineKinds(last), "daemon_stop") || !strings.Contains(describeIncident(last), reason) {
					t.Fatal("timeline lost lifecycle")
				}
			})
		}
	}
}

func TestRuntimeHistoryFailureDoesNotChangeRefusal(t *testing.T) {
	f := newFixture(t)
	cfg, err := config.Load(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	l, err := lock.Acquire(cfg.Loop.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	h, err := lock.AcquireNamed(cfg.Loop.StateDir, "incident")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Release()
	var errs bytes.Buffer
	if code := tickOnce(env{context.Background(), io.Discard, &errs}, cfg, time.Now(), true, false); code != exitSkipped {
		t.Fatal(code)
	}
	if !strings.Contains(errs.String(), "diagnostic evidence is incomplete") || strings.Contains(errs.String(), filepath.Dir(f.cfg)) {
		t.Fatal(errs.String())
	}
}

// The child runs the actual signal handler and command boundary with disposable
// state. Only this test's owned process is signalled.
func TestDaemonSignalFixture(t *testing.T) {
	if cfg := os.Getenv("LOOPTENDER_TEST_SIGNAL_CONFIG"); cfg != "" {
		os.Args = []string{os.Args[0], "daemon", "-c", cfg, "--dry-run"}
		main()
	}
}

func TestDaemonSignalEventsNative(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(signalName(sig), func(t *testing.T) {
			f := newFixture(t)
			cfg, err := config.Load(f.cfg)
			if err != nil {
				t.Fatal(err)
			}
			bin, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, "-test.run=^TestDaemonSignalFixture$")
			cmd.Env = append(os.Environ(), "LOOPTENDER_TEST_SIGNAL_CONFIG="+f.cfg)
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			// Keep a single waiter, even if readiness or Signal fails.
			joined := false
			defer func() {
				if !joined {
					cancel()
					<-done
				}
			}()
			waitFor(t, func() bool {
				j, err := incident.Load(cfg.Loop.StateDir)
				return err == nil && len(j.Records) > 0 && j.Records[0].Kind == "daemon_start"
			}, "daemon start record")
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				joined = true
				t.Fatalf("daemon exit: %v: %s", err, out.String())
			}
			joined = true
			j, err := incident.Load(cfg.Loop.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			last := j.Records[len(j.Records)-1]
			if last.Kind != "daemon_stop" || last.Runtime.Reason != "signal" || last.Runtime.Signal != signalName(sig) || !last.DryRun {
				t.Fatal(last)
			}
			if held, err := lock.AcquireNamed(cfg.Loop.StateDir, lock.Daemon); err != nil {
				t.Fatal("daemon lock not released", err)
			} else {
				held.Release()
			}
		})
	}
}

func TestRejectedDaemonHasNoLifecycleEvent(t *testing.T) {
	f := newFixture(t)
	cfg, err := config.Load(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	l, err := lock.AcquireNamed(cfg.Loop.StateDir, lock.Daemon)
	if err != nil {
		t.Fatal(err)
	}
	code := daemon(env{context.Background(), io.Discard, io.Discard}, cfg, f.cfg, true)
	l.Release()
	if code != exitSkipped {
		t.Fatal(code)
	}
	j, err := incident.Load(cfg.Loop.StateDir)
	if err != nil || len(j.Records) != 0 {
		t.Fatal(j, err)
	}
}

func TestDaemonHistoryFailureKeepsExitCode(t *testing.T) {
	f := newFixture(t)
	cfg, err := config.Load(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h, err := lock.AcquireNamed(cfg.Loop.StateDir, "incident")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var errs bytes.Buffer
	if code := daemonLoop(env{ctx, io.Discard, &errs}, cfg, f.cfg, false, nil, nil, nil); code != exitOK {
		t.Fatal(code)
	}
	if strings.Count(errs.String(), "diagnostic evidence is incomplete") != 2 || strings.Contains(errs.String(), filepath.Dir(f.cfg)) {
		t.Fatal(errs.String())
	}
}

func TestRuntimeTimelineDoesNotInventCompletedChecks(t *testing.T) {
	dir := t.TempDir()
	at := time.Now().UTC()
	if err := incident.Append(dir, incident.Record{Kind: "tick", CheckStartedAt: at, Action: "none", Outcome: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"daemon_start", "daemon_stop", "check_refused"} {
		recordRuntimeEvent(env{context.Background(), io.Discard, io.Discard}, dir, kind, "unavailable", "", true, "failed")
	}
	timeline := readTimeline(dir, at, time.Hour, 100, timelineFilter{})
	if timeline.Summary.RetainedChecks != 1 || timeline.Summary.LatestCheck == nil || timeline.Summary.LatestCheck.Sequence != 1 {
		t.Fatal(timeline.Summary)
	}
	for _, kind := range []string{"daemon_start", "daemon_stop", "check_refused"} {
		filter, ok := parseTimelineFilter(kind, "", "", "", at)
		if !ok {
			t.Fatal(kind)
		}
		filtered := readTimeline(dir, at, time.Hour, 100, filter)
		if len(filtered.Events) != 1 || filtered.Events[0].TimeBasis != "runtime_observed_at" || filtered.Events[0].Incident.Kind != kind || containsString(filtered.Events[0].Kinds, "check") {
			t.Fatal(filtered.Events)
		}
	}
}
