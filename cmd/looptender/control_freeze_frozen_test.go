//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
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
	cmd := exec.CommandContext(ctx, os.Getenv("FROZEN_CONTROL_READER"), append(args, "-c", f.cfg)...)
	if err := cmd.Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			return e.ExitCode()
		}
		t.Fatal(err)
	}
	return 0
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
			f := newFixture(t)
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
	f := newFixture(t)
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
	f := newFixture(t)
	if code, _, e := f.run("enable"); code != 0 {
		t.Fatal(e)
	}
	write(t, filepath.Join(f.bbDir, "thr_primary.show"), `{"thread":{"id":"thr_primary","status":"idle"}}`, 0600)
	reached := filepath.Join(f.dir, "reached")
	released := filepath.Join(f.dir, "released")
	wrapper := filepath.Join(f.dir, "blocked-bb")
	originalCLI := filepath.Join(f.dir, "bin", "bb")
	script := "#!/bin/sh\nif [ \"$1\" = thread ] && [ \"$2\" = show ] && [ \"$3\" = thr_primary ]; then\n  touch " + commandQuote(reached) + "\n  while [ ! -e " + commandQuote(released) + " ]; do sleep 0.01; done\nfi\nexec " + commandQuote(originalCLI) + " \"$@\"\n"
	write(t, wrapper, script, 0700)
	cfg, _ := os.ReadFile(f.cfg)
	write(t, f.cfg, strings.Replace(string(cfg), originalCLI, wrapper, 1), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("FROZEN_CONTROL_READER"), "daemon", "-c", f.cfg)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waitFor(t, func() bool { _, err := os.Stat(reached); return err == nil }, "frozen daemon loaded control and entered Observe")
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
		t.Fatal("frozen daemon did not finish")
	}
	sent, _ := os.ReadFile(filepath.Join(f.bbDir, "thr_primary.told"))
	if len(sent) == 0 {
		t.Fatal("expected in-flight old daemon send after undrained publication")
	}
	t.Log("frozen daemon sent after unsafe publication, then exited; tick-lock drain must precede acknowledgement")
}
