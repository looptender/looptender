package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/daemoninfo"
	"github.com/looptender/looptender/internal/lock"
)

// On a platform without adoption the daemon keeps its build: nothing is
// probed, run or recorded, whatever is installed.
func TestAdoptionIsOffWithoutPlatformSupport(t *testing.T) {
	if adoptSupported {
		if runtime.GOOS == "windows" {
			t.Fatal("adoption claims support on Windows")
		}
		t.Skip("adoption is supported here")
	}
	dir := t.TempDir()
	l, err := lock.AcquireNamed(dir, lock.Daemon)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	a := newBuildAdopter(filepath.Join(dir, "looptender.yaml"), dir, "daemon", "", l)
	if a.off == "" {
		t.Fatal("adoption on")
	}
	a.run = func(context.Context, *adoptCandidate, ...string) ([]byte, int, error) {
		t.Fatal("ran a build")
		return nil, 0, nil
	}
	a.exec = func(*adoptCandidate, []string, []string, []*os.File) error { t.Fatal("exec"); return nil }
	a.atSafePoint(context.Background(), &config.Config{}, nil)
	if _, err := os.Stat(filepath.Join(dir, adoptRecordName)); !os.IsNotExist(err) {
		t.Fatal("recorded")
	}
}

func TestBuildAdoptionStatusWords(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	old := adoptBuildID{Label: "v9.0.0-dev.fictional1", SHA256: strings.Repeat("a", 64)}
	installed := adoptBuildID{Label: "v9.0.0-dev.fictional2", SHA256: strings.Repeat("b", 64)}
	if describeBuildAdoption(dir, daemonStatus{}, now) != nil {
		t.Fatal("status without a record")
	}
	answering := daemonStatus{State: "responsive"}
	silent := daemonStatus{State: "not_recorded"}
	dead := daemonStatus{State: "unavailable"}
	var held *lock.Lock
	for _, c := range []struct {
		r          adoptRecord
		d          daemonStatus
		supervised bool
		words      []string
		// endpoint, when set, is when the daemon record was written,
		// relative to the change.
		endpoint time.Duration
	}{
		{adoptRecord{Outcome: adoptAdopted, At: now, From: old, To: installed}, answering, true, []string{"switched from v9.0.0-dev.fictional1 (aaaaaaaaaaaa) to the installed v9.0.0-dev.fictional2 (bbbbbbbbbbbb)", "holds, owner"}, 0},
		{adoptRecord{Outcome: adoptSwitching, At: now.Add(-time.Minute), From: old, To: installed}, silent, true, []string{"began switching"}, 0},
		{adoptRecord{Outcome: adoptSwitching, At: now.Add(-time.Hour), From: old, To: installed}, silent, false, []string{"no supervisor may be running", "Start supervision again"}, 0},
		{adoptRecord{Outcome: adoptSwitching, At: now.Add(-time.Hour), From: old, To: installed}, answering, true, []string{"began switching"}, 0},
		{adoptRecord{Outcome: adoptRefused, At: now, From: old, To: installed, Reason: "the installed build does not validate this loop's configuration"}, answering, true, []string{"stays on v9.0.0-dev.fictional1", "does not validate"}, 0},
		{adoptRecord{Outcome: adoptReturned, At: now, From: installed, To: old, Reason: "the installed build changed during the switch"}, answering, true, []string{"returned to v9.0.0-dev.fictional1"}, 0},
		{adoptRecord{Outcome: adoptFailed, At: now, From: old, To: installed, Reason: "x; no supervisor is running, start supervision again"}, silent, false, []string{"failed", "no supervisor"}, 0},
		// The build switched to never came up: the recorded daemon is the
		// one from before the change, and it does not answer. Loud.
		{adoptRecord{Outcome: adoptAdopted, At: now.Add(-time.Hour), From: old, To: installed}, dead, false, []string{"switched from", "no supervisor may be running"}, -time.Minute},
		{adoptRecord{Outcome: adoptReturned, At: now.Add(-time.Hour), From: installed, To: old}, dead, false, []string{"returned to", "no supervisor may be running"}, -time.Minute},
		// It came up and stopped later (a crash, a reboot): not the switch's.
		{adoptRecord{Outcome: adoptAdopted, At: now.Add(-time.Hour), From: old, To: installed}, dead, true, []string{"switched from"}, time.Minute},
		// One that ended on purpose removed its record: quiet.
		{adoptRecord{Outcome: adoptAdopted, At: now.Add(-time.Hour), From: old, To: installed}, silent, true, []string{"switched from"}, 0},
		// Just switched: not yet.
		{adoptRecord{Outcome: adoptAdopted, At: now.Add(-time.Second), From: old, To: installed}, dead, true, []string{"switched from"}, -time.Minute},
		// The build came up but could not publish its diagnostics: it holds
		// the daemon lock, so it is up. (held marks the case.)
		{adoptRecord{Outcome: adoptAdopted, At: now.Add(-time.Hour), From: old, To: installed, Mode: "held"}, dead, true, []string{"switched from"}, -time.Minute},
		// The owner changed during the switch: the daemon exited as at any check.
		{adoptRecord{Outcome: adoptHandedOff, At: now.Add(-time.Hour), From: old, To: installed}, silent, true, []string{"handed off"}, 0},
	} {
		if err := saveAdoptRecord(dir, c.r); err != nil {
			t.Fatal(err)
		}
		endpoint := filepath.Join(dir, daemoninfo.FileName)
		os.Remove(endpoint)
		if c.endpoint != 0 {
			if err := os.WriteFile(endpoint, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(endpoint, c.r.At.Add(c.endpoint), c.r.At.Add(c.endpoint)); err != nil {
				t.Fatal(err)
			}
		}
		if c.r.Mode == "held" {
			l, err := lock.AcquireNamed(dir, lock.Daemon)
			if err != nil {
				t.Fatal(err)
			}
			held = l
		}
		s := describeBuildAdoption(dir, c.d, now)
		if held != nil {
			held.Release()
			held = nil
		}
		if s == nil || s.Outcome != c.r.Outcome || s.Supervised != c.supervised {
			t.Fatalf("%s: %+v", c.r.Outcome, s)
		}
		for _, w := range c.words {
			if !strings.Contains(s.Message, w) {
				t.Errorf("%s: %q lacks %q", c.r.Outcome, s.Message, w)
			}
		}
		if strings.Contains(s.Message, dir) {
			t.Errorf("%s: path in message", c.r.Outcome)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, adoptRecordName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := describeBuildAdoption(dir, answering, now); s == nil || s.Outcome != "unreadable" {
		t.Fatalf("%+v", s)
	}
}

func TestAdoptPayloadMustBeComplete(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "looptender.yaml")
	good := adoptPayload{Config: abs, StateDir: abs, From: abs, FromSHA: "aa", ExpectSHA: "bb", LockFD: 5}
	if !good.valid() {
		t.Fatal("complete payload refused")
	}
	for _, bad := range []func(*adoptPayload){
		func(p *adoptPayload) { p.Config = "relative.yaml" },
		func(p *adoptPayload) { p.StateDir = "" },
		func(p *adoptPayload) { p.From = "" },
		func(p *adoptPayload) { p.ExpectSHA = "" },
		func(p *adoptPayload) { p.FromSHA = "" },
		func(p *adoptPayload) { p.LockFD = 1 << 40 },
	} {
		p := good
		bad(&p)
		if p.valid() {
			t.Errorf("accepted %+v", p)
		}
	}
	var out bytes.Buffer
	// The configuration path after the descriptor is optional; nothing else is.
	for _, args := range [][]string{nil, {"--handover-fd"}, {"--handover-fd", "x"}, {"-c", "/x"}, {"--handover-fd", "x", "-c", "/x"},
		{"-c", "/x", "--handover-fd", "5"}, {"--handover-fd", "5", "-c", "relative.yaml"}, {"--handover-fd", "5", "--config", "/x"}, {"--handover-fd", "5", "-c"}} {
		if code := adoptBuildCommand(env{context.Background(), &out, &out}, args); code != exitUsage {
			t.Errorf("%v: %d", args, code)
		}
	}
}

// The candidate's probe and validate run without the BB_THREAD_ID of the
// thread that started the daemon: after a handoff the supervisor made, that
// thread no longer owns the loop, and validating as it refused every
// installed build.
func TestAdoptCommandEnvDropsTheStartingThread(t *testing.T) {
	got := adoptCommandEnv([]string{"PATH=/example/bin", "BB_THREAD_ID=thr_example_left", "HOME=/example/home", "BB_THREAD_ID_OTHER=kept"})
	want := []string{"PATH=/example/bin", "HOME=/example/home", "BB_THREAD_ID_OTHER=kept"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("env %v, want %v", got, want)
	}
}
