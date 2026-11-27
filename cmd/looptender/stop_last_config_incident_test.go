//go:build unix

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/incident"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/state"
)

// A daemon's stop-only path has its own admission guards. Each refused pass
// appends exactly one diagnostic record without changing operational state,
// the bound owner lease, or the previous completed check. An unavailable journal
// preserves the refusal and all existing bytes, with one fixed warning.
func TestLastConfigStopAdmissionRefusalEvents(t *testing.T) {
	cases := []struct {
		name, reason string
		code         int
	}{
		{"held lock", "lock_held", exitSkipped},
		{"unavailable lock", "lock_unavailable", exitFailed},
		{"changed ownership", "ownership_unverified", exitFailed},
		{"restored unchanged source", "sources_unverified", exitSkipped},
		{"restored changed source", "sources_unverified", exitSkipped},
		{"transferred target mismatch", "sources_unverified", exitSkipped},
		{"unreachable BB", "safety_guard", exitSkipped},
	}
	for _, tc := range cases {
		for _, historyUnavailable := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/history-unavailable=%t", tc.name, historyUnavailable), func(t *testing.T) {
				f := newFixture(t)
				if tc.name == "unreachable BB" {
					raw, err := os.ReadFile(f.cfg)
					if err != nil {
						t.Fatal(err)
					}
					write(t, f.cfg, strings.ReplaceAll(string(raw), filepath.Join(f.dir, "bin", "bb"), "bb-fixture-unavailable"), 0600)
				}
				cfg, err := config.Load(f.cfg)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(f.cfg)
				if err != nil {
					t.Fatal(err)
				}
				dir := cfg.Loop.StateDir
				ctl := control.File{Enabled: true, Stop: true, Paused: true, AwaitingUser: true, Completed: true, Note: "PRIVATE_STOP_NOTE", Ownership: &control.Ownership{ThreadID: "thr_fixture_owner", Generation: 1}}
				if err := control.Save(dir, ctl); err != nil {
					t.Fatal(err)
				}
				lease := leaseOf(ctl)
				bound := *lease
				if err := state.Save(dir, state.File{Reprompts: 2, Nudges: 1, LastCheck: &state.Check{At: time.Now().Add(-time.Minute), Target: state.Primary, ObservedState: "idle", Action: "none", Outcome: "succeeded"}}); err != nil {
					t.Fatal(err)
				}
				if err := incident.Append(dir, incident.Record{Kind: "daemon_start", Runtime: &incident.RuntimeEvent{Reason: "started"}, CheckStartedAt: time.Now(), Outcome: "succeeded"}); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(f.cfg); err != nil {
					t.Fatal(err)
				}
				if !cfg.SourcesUnavailable() {
					t.Fatal("preliminary eligibility did not see missing source")
				}
				switch tc.name {
				case "held lock":
					l, err := lock.Acquire(dir)
					if err != nil {
						t.Fatal(err)
					}
					defer l.Release()
				case "unavailable lock":
					if err := os.Mkdir(filepath.Join(dir, "tick.lock"), 0700); err != nil {
						t.Fatal(err)
					}
				case "changed ownership":
					ctl.Ownership = &control.Ownership{ThreadID: "thr_fixture_replacement", Generation: 2}
					if err := control.Save(dir, ctl); err != nil {
						t.Fatal(err)
					}
				case "restored unchanged source":
					write(t, f.cfg, string(raw), 0600)
				case "restored changed source":
					write(t, f.cfg, strings.Replace(string(raw), "thr_primary", "thr_fixture_changed", 1), 0600)
				case "transferred target mismatch":
					ctl.BBTarget = "thr_fixture_moved"
					if err := control.Save(dir, ctl); err != nil {
						t.Fatal(err)
					}
				}
				if historyUnavailable {
					l, err := lock.AcquireNamed(dir, "incident")
					if err != nil {
						t.Fatal(err)
					}
					defer l.Release()
				}
				before := snapshotRuntime(t, dir)
				var errs bytes.Buffer
				code := stopFromLastConfig(env{context.Background(), io.Discard, &errs}, cfg, lease)
				if code != tc.code {
					t.Fatalf("exit %d, want %d: %s", code, tc.code, &errs)
				}
				if f.read("calls") != "" || f.read("probes") != "" || f.hooks.types() != "" {
					t.Fatal("refused stop performed provider/probe/report work")
				}
				if *lease != bound {
					t.Fatal("refusal changed the daemon's bound ownership lease")
				}
				const warning = "warning: runtime incident history unavailable; diagnostic evidence is incomplete"
				if historyUnavailable {
					if !reflect.DeepEqual(before, snapshotRuntime(t, dir)) {
						t.Fatal("failed diagnostic write changed runtime records")
					}
					if strings.Count(errs.String(), warning) != 1 {
						t.Fatalf("missing or duplicate fixed history warning: %s", &errs)
					}
					return
				}
				if strings.Contains(errs.String(), warning) {
					t.Fatal(&errs)
				}
				assertRefusalOnlySnapshotChange(t, dir, before, tc.reason)
				j, err := incident.Load(dir)
				if err != nil {
					t.Fatal(err)
				}
				r := j.Records[len(j.Records)-1]
				if r.DryRun || r.Runtime.Signal != "unavailable" || r.Outcome != "failed" || r.Action != "none" || r.ControlBefore != nil || r.ControlAfter != nil || len(r.Decisions.Evaluations) != 0 || r.Nudges != 0 || r.Reprompts != 0 {
					t.Fatal("refusal invented execution evidence", r)
				}
				b, err := os.ReadFile(filepath.Join(dir, incident.FileName))
				if err != nil {
					t.Fatal(err)
				}
				for _, private := range []string{dir, "PRIVATE_STOP_NOTE", "thr_fixture_owner", "thr_fixture_replacement", "thr_fixture_moved"} {
					if bytes.Contains(b, []byte(private)) {
						t.Fatal("refusal journal disclosed private data")
					}
				}
			})
		}
	}
}
