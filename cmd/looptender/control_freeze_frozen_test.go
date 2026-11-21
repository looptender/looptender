//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
	"github.com/looptender/looptender/internal/session"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func oldReader(t *testing.T, f *fixture, args ...string) int {
	t.Helper()
	if os.Getenv("FROZEN_CONTROL_READER") == "" {
		t.Skip("set FROZEN_CONTROL_READER to an independently built frozen executable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, output := frozenCommand(t, ctx, f, append(args, "-c", f.cfg)...)
	if err := cmd.Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			return e.ExitCode()
		}
		t.Fatal(err)
	}
	command := ""
	if len(args) > 0 {
		command = args[0]
	}
	if !frozenSuccessfulOutput(command, 0, output.text(), output.wasTruncated()) {
		t.Fatal("exit zero with unexpected warning/error or truncated output; see retained log")
	}
	if len(args) > 0 && args[0] == "enable" {
		published := false
		for _, entry := range session.Known(filepath.Join(f.dir, "sessions"), session.Identity{Kind: "bb", ID: "thr_primary"}) {
			if entry.Config == f.cfg && entry.StateDir == filepath.Join(f.dir, "state") {
				published = true
			}
		}
		if !published {
			t.Fatal("exit zero without confined session metadata publication; see retained log")
		}
	}
	return 0
}

// Exact frozen v0.2.0 config.UnboundedWarning, emitted by validate. Do not
// derive this allowance from current production wording or allow a substring.
const frozenUnboundedDiagnostic = "warning: no loop-wide limit is configured (loop.limits deadline, max_sends or no_progress_timeout); automatic sends repeat until complete, stop, pause or a hold\n"

func frozenSuccessfulOutput(command string, code int, output string, truncated bool) bool {
	if code != 0 || truncated {
		return false
	}
	ordinarySeen := false
	for _, line := range strings.SplitAfter(output, "\n") {
		if command == "validate" && line == frozenUnboundedDiagnostic && !ordinarySeen {
			ordinarySeen = true
			continue
		}
		lower := strings.ToLower(line)
		for _, problem := range []string{"warning", "error:", "fatal:", "panic:", "permission denied", "operation not permitted", "unavailable"} {
			if strings.Contains(lower, problem) {
				return false
			}
		}
	}
	return true
}

func TestFrozenWarningClassification(t *testing.T) {
	for _, tc := range []struct {
		name, command, output string
		code                  int
		truncated, want       bool
	}{
		{name: "ordinary validate", command: "validate", output: "ok: fictional config\n" + frozenUnboundedDiagnostic + "report: standard\n", want: true},
		{name: "quiet enable", command: "enable", output: "enabled\n", want: true},
		{name: "wrong command", command: "enable", output: frozenUnboundedDiagnostic},
		{name: "nonzero", command: "validate", output: frozenUnboundedDiagnostic, code: 1},
		{name: "truncated capture", command: "validate", output: frozenUnboundedDiagnostic, truncated: true},
		{name: "incomplete line", command: "validate", output: strings.TrimSuffix(frozenUnboundedDiagnostic, "\n")},
		{name: "case variant", command: "validate", output: strings.ToUpper(frozenUnboundedDiagnostic)},
		{name: "prefix", command: "validate", output: "unexpected " + frozenUnboundedDiagnostic},
		{name: "suffix", command: "validate", output: strings.TrimSuffix(frozenUnboundedDiagnostic, "\n") + "; publication failed\n"},
		{name: "duplicate", command: "validate", output: frozenUnboundedDiagnostic + frozenUnboundedDiagnostic},
		{name: "unknown warning", command: "validate", output: "warning: fictional unknown diagnostic\n"},
		{name: "session publication", command: "enable", output: "warning: session metadata publication failed\n"},
		{name: "mixed publication", command: "validate", output: frozenUnboundedDiagnostic + "warning: session metadata publication failed\n"},
		{name: "history", command: "validate", output: frozenUnboundedDiagnostic + "incident history unavailable\n"},
		{name: "claim", command: "validate", output: frozenUnboundedDiagnostic + "session claim: permission denied\n"},
		{name: "containment", command: "validate", output: frozenUnboundedDiagnostic + "sandbox: operation not permitted\n"},
		{name: "error", command: "validate", output: frozenUnboundedDiagnostic + "error: fictional failure\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := frozenSuccessfulOutput(tc.command, tc.code, tc.output, tc.truncated); got != tc.want {
				t.Fatalf("success classification=%v want %v", got, tc.want)
			}
		})
	}
}

func freezeEnvelope(t *testing.T, raw []byte) []byte {
	t.Helper()
	b, err := json.Marshal([]any{map[string]any{"format": "fictional-emergency-freeze-v1", "raw": raw, "identity": "fictional-state", "action": "pause"}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestFrozenReaderCanonicalAndSidecar(t *testing.T) {
	for _, prior := range []string{"enabled", "transferred", "retired"} {
		t.Run(prior, func(t *testing.T) {
			f := newFrozenFixture(t)
			dir := filepath.Join(f.dir, "state")
			path := filepath.Join(dir, control.FileName)
			ctl := control.File{Enabled: true, Ownership: &control.Ownership{ThreadID: "thr_owner", Generation: 4}}
			if prior == "transferred" {
				ctl.BBTarget = "thr_transferred"
			}
			if prior == "retired" {
				ctl.SupersededBy = "thr_successor"
			}
			if err := control.Save(dir, ctl); err != nil {
				t.Fatal(err)
			}
			original, _ := os.ReadFile(path)
			raw := append(append([]byte{}, original...), byte('{')) // undecodable but preserves all unknown evidence
			envelope := freezeEnvelope(t, raw)
			// Crash-before-publication: original damage stays canonical, staging has no authority.
			os.WriteFile(path, raw, 0600)
			staged := filepath.Join(dir, ".fictional-stage")
			os.WriteFile(staged, envelope, 0600)
			if oldReader(t, f, "tick") == 0 {
				t.Fatal("damaged old reader acted")
			}
			before := f.read("calls")
			// Exercise the production Pause, not just the proposed wire shape.
			if code, _, err := f.run("pause"); code != 0 {
				t.Fatal(err)
			}
			envelope, _ = os.ReadFile(path)
			os.Remove(staged)
			for _, cmd := range []string{"tick", "pause", "stop", "enable", "resume", "validate", "daemon"} {
				if oldReader(t, f, cmd) == 0 {
					t.Fatal("old reader accepted canonical envelope", cmd)
				}
			}
			got, _ := os.ReadFile(path)
			if !bytes.Equal(got, envelope) {
				t.Fatal("old command rewrote envelope")
			}
			var decoded []struct {
				Raw []byte `json:"raw"`
			}
			if json.Unmarshal(got, &decoded) != nil || !bytes.Equal(decoded[0].Raw, raw) {
				t.Fatal("raw evidence lost")
			}
			if f.read("calls") != before {
				t.Fatal("envelope queried provider")
			}
			// Sidecar cannot protect old readers after arbitrary valid restore.
			os.WriteFile(filepath.Join(dir, "fictional-emergency-fence.json"), envelope, 0600)
			os.WriteFile(path, original, 0600)
			code := oldReader(t, f, "validate")
			if prior == "enabled" && code != 0 {
				t.Fatal("expected old reader to ignore sidecar after valid restore")
			}
			if prior != "enabled" && code == 0 {
				if os.Getenv("FROZEN_LEGACY_BINDINGS") == "1" {
					t.Log("legacy reader accepts valid restore with newer binding metadata")
				} else {
					t.Fatal("valid restore lost original target/retirement fence")
				}
			}
			// Restore a valid but older enabled copy: metadata freshness is unprovable.
			old := control.File{Enabled: true}
			if err := control.Save(dir, old); err != nil {
				t.Fatal(err)
			}
			if oldReader(t, f, "validate") != 0 {
				t.Fatal("expected stale valid restore to bypass sidecar")
			}
			os.Remove(path)
			if oldReader(t, f, "enable") != 0 {
				t.Fatal("expected removal then old enable to bypass sidecar")
			}
			t.Log("old reader excludes canonical envelope; valid stale restore/removal defeats sidecar")
		})
	}
}
func TestFrozenReaderHonorsExistingTickLock(t *testing.T) {
	f := newFrozenFixture(t)
	dir := filepath.Join(f.dir, "state")
	if err := control.Save(dir, control.File{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	l, err := lock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if code := oldReader(t, f, "tick"); code != 75 {
		t.Fatal("old reader did not honor tick exclusion", code)
	}
	if f.read("calls") != "" {
		t.Fatal("blocked tick called provider")
	}
}
func TestFrozenDaemonAlreadyLoadedControl(t *testing.T) {
	if os.Getenv("FROZEN_CONTROL_READER") == "" {
		t.Skip("requires frozen executable")
	}
	f := newFrozenFixture(t)
	if code, _, e := f.run("enable"); code != 0 {
		t.Fatal(e)
	}
	write(t, filepath.Join(f.bbDir, "thr_primary.show"), `{"thread":{"id":"thr_primary","status":"idle"}}`, 0600)
	reached := filepath.Join(f.dir, "reached")
	released := filepath.Join(f.dir, "released")
	wrapper := filepath.Join(f.dir, "blocked-bb")
	originalCLI := filepath.Join(f.dir, "bin", "bb")
	script := "#!/bin/sh\nif [ \"$1\" = thread ] && [ \"$2\" = show ] && [ \"$3\" = thr_primary ]; then\n  touch " + commandQuote(reached) + "\n  attempt=0\n  while [ ! -e " + commandQuote(released) + " ]; do\n    attempt=$((attempt + 1))\n    [ \"$attempt\" -lt 600 ] || exit 124\n    sleep 0.01\n  done\nfi\nexec " + commandQuote(originalCLI) + " \"$@\"\n"
	write(t, wrapper, script, 0700)
	cfg, _ := os.ReadFile(f.cfg)
	write(t, f.cfg, strings.Replace(string(cfg), originalCLI, wrapper, 1), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd, log := frozenCommand(t, ctx, f, "daemon", "-c", f.cfg)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var runErr error
	go func() { runErr = cmd.Wait(); close(done) }()
	// Release the owned wrapper even when an assertion fails before Observe.
	// Its independent loop bound also survives loss of this test process.
	t.Cleanup(func() {
		os.WriteFile(released, nil, 0600)
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("owned frozen CLI cleanup did not finish")
		}
	})
	diagnostic := log.text
	deadline := time.NewTimer(6 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	reachedObserve := false
	for !reachedObserve {
		if _, err := os.Stat(reached); err == nil {
			reachedObserve = true
			break
		}
		select {
		case <-done:
			t.Fatalf("frozen daemon exited before Observe: %v; output: %s", runErr, diagnostic())
		case <-deadline.C:
			t.Fatalf("frozen daemon never reached Observe before deadline; output: %s", diagnostic())
		case <-poll.C:
		}
	}
	dir := filepath.Join(f.dir, "state")
	l, err := lock.Acquire(dir)
	if err == nil {
		l.Release()
		t.Fatal("cannot claim drain while old daemon is observing")
	}
	// Deliberately unsafe publication: demonstrates the need to drain first.
	path := filepath.Join(dir, control.FileName)
	raw, _ := os.ReadFile(path)
	os.WriteFile(path, freezeEnvelope(t, raw), 0600)
	os.WriteFile(released, nil, 0600)
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatalf("frozen daemon did not finish; output: %s", diagnostic())
	}
	sent, _ := os.ReadFile(filepath.Join(f.bbDir, "thr_primary.told"))
	if len(sent) == 0 {
		t.Fatal("expected in-flight old daemon send after undrained publication")
	}
	t.Log("frozen daemon sent after unsafe publication, then exited; tick-lock drain must precede acknowledgement")
}
