//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/looptender/looptender/internal/config"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/lock"
)

func TestControlFreezeDamagedPause(t *testing.T) {
	for _, raw := range [][]byte{{}, []byte(`{"enabled":true,"ownership":`), {0, 1, '{'}} {
		t.Run(freezeDigest(raw)[:8], func(t *testing.T) {
			f := newFixture(t)
			t.Setenv("BB_THREAD_ID", "thr_fictional_owner")
			if code, _, err := f.run("enable"); code != 0 {
				t.Fatal(err)
			}
			path := filepath.Join(f.dir, "state", control.FileName)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			calls := f.read("calls")
			t.Setenv("BB_THREAD_ID", "thr_fictional_other") // restrictive policy unchanged
			code, out, err := f.run("pause")
			if code != 0 || !strings.Contains(out, "restrictive freeze persisted") {
				t.Fatalf("pause failed: %d %s %s", code, out, err)
			}
			wire, _ := os.ReadFile(path)
			var records []controlFreeze
			if json.Unmarshal(wire, &records) != nil || len(records) != 1 || !bytes.Equal(records[0].Raw, raw) || records[0].Caller != "thr_fictional_other" || records[0].Authority != "unknown" {
				t.Fatal("lost evidence/provenance")
			}
			if _, err := control.Load(filepath.Dir(path)); err == nil {
				t.Fatal("freeze became normal control")
			}
			t.Setenv("BB_THREAD_ID", "thr_fictional_third")
			if code, _, err := f.run("pause"); code != 0 {
				t.Fatal(err)
			}
			again, _ := os.ReadFile(path)
			if !bytes.Equal(wire, again) {
				t.Fatal("repeat rewrote evidence")
			}
			for _, cmd := range []string{"enable", "resume", "stop", "tick", "validate", "daemon"} {
				if code, _, _ := f.run(cmd); code == 0 {
					t.Fatalf("%s accepted frozen authority", cmd)
				}
				after, _ := os.ReadFile(path)
				if !bytes.Equal(wire, after) {
					t.Fatalf("%s altered evidence", cmd)
				}
			}
			if f.read("calls") != calls {
				t.Fatal("provider invoked")
			}
		})
	}
}

func TestControlFreezeLocksAndHistory(t *testing.T) {
	for _, kind := range []string{"tick", "control", "history"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			if code, _, err := f.run("enable"); code != 0 {
				t.Fatal(err)
			}
			dir := filepath.Join(f.dir, "state")
			path := filepath.Join(dir, control.FileName)
			if kind == "history" {
				os.WriteFile(filepath.Join(dir, "incidents.json"), []byte("{"), 0600)
				code, _, err := f.run("pause")
				if code == 0 || !strings.Contains(err, "control saved") {
					t.Fatalf("history failure lost: %d %s", code, err)
				}
				ctl, err2 := control.Load(dir)
				if err2 != nil || !ctl.Paused {
					t.Fatal("healthy control frozen")
				}
				return
			}
			raw := []byte("{")
			os.WriteFile(path, raw, 0600)
			held, err := lock.AcquireNamed(dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Release()
			if code, _, _ := f.run("pause"); code == 0 {
				t.Fatal("pause without drain/lock")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(raw, after) {
				t.Fatal("busy evidence replaced")
			}
		})
	}
}

func TestControlFreezePublicationFailures(t *testing.T) {
	for _, stage := range []string{"write", "short", "sync", "close", "verify", "rename", "directory", "success"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "control.json")
			raw := []byte("{")
			wire := []byte(`[{"freeze":"fixture"}]`)
			os.WriteFile(path, raw, 0600)
			injected := errors.New("injected")
			ops := freezePublication{(*os.File).Write, (*os.File).Sync, (*os.File).Close, os.Rename, syncFreezeDirectory}
			verify := func() error { return nil }
			switch stage {
			case "write":
				ops.write = func(*os.File, []byte) (int, error) { return 0, injected }
			case "short":
				ops.write = func(*os.File, []byte) (int, error) { return 1, nil }
			case "sync":
				ops.sync = func(*os.File) error { return injected }
			case "close":
				ops.close = func(f *os.File) error { f.Close(); return injected }
			case "verify":
				verify = func() error { return injected }
			case "rename":
				ops.replace = func(string, string) error { return injected }
			case "directory":
				ops.syncDir = func(string) error { return injected }
			}
			err := publishControlFreezeWith(path, wire, verify, ops)
			if (err == nil) != (stage == "success") {
				t.Fatalf("outcome %v", err)
			}
			after, _ := os.ReadFile(path)
			want := raw
			if stage == "directory" || stage == "success" {
				want = wire
			}
			if !bytes.Equal(want, after) {
				t.Fatal("missing/default/intermediate or rollback")
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 1 {
				t.Fatal("staging leaked")
			}
		})
	}
}

func TestControlFreezeIdentityAndStrictEnvelope(t *testing.T) {
	for _, change := range []string{"bytes", "file", "directory"} {
		t.Run(change, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "state")
			os.Mkdir(dir, 0700)
			path := filepath.Join(dir, control.FileName)
			os.WriteFile(path, []byte("{"), 0600)
			s, err := readFreezeSnapshot(dir)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "bytes":
				os.WriteFile(path, []byte("["), 0600)
			case "file":
				os.Rename(path, path+".old")
				os.WriteFile(path, []byte("{"), 0600)
			case "directory":
				os.Rename(dir, dir+".old")
				os.Mkdir(dir, 0700)
				os.WriteFile(path, []byte("{"), 0600)
			}
			if s.unchanged() == nil {
				t.Fatal("changed evidence accepted")
			}
		})
	}
	f := newFixture(t)
	f.run("enable")
	path := filepath.Join(f.dir, "state", control.FileName)
	os.WriteFile(path, []byte("{"), 0600)
	if code, _, err := f.run("pause"); code != 0 {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)
	for _, change := range []string{"duplicate", "binding", "truncated", "null", "nested"} {
		t.Run(change, func(t *testing.T) {
			wire := append([]byte{}, original...)
			switch change {
			case "duplicate":
				wire = bytes.Replace(wire, []byte(`"action":"pause"`), []byte(`"action":"pause","action":"pause"`), 1)
			case "binding":
				wire = bytes.Replace(wire, []byte(`"directory":"`), []byte(`"directory":"wrong`), 1)
			case "truncated":
				wire = wire[:len(wire)/2]
			case "null":
				wire = bytes.Replace(wire, []byte(`"raw":"ew=="`), []byte(`"raw":null`), 1)
			case "nested":
				var r []controlFreeze
				json.Unmarshal(wire, &r)
				r[0].Raw = original
				r[0].Length = len(original)
				r[0].SHA256 = freezeDigest(original)
				wire, _ = json.Marshal(r)
			}
			os.WriteFile(path, wire, 0600)
			if code, _, _ := f.run("pause"); code == 0 {
				t.Fatal("invalid envelope accepted")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, wire) {
				t.Fatal("invalid envelope replaced")
			}
		})
	}
}

func TestControlFreezeUnsafeInputs(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "oversized", "unreadable", "schema", "array", "null", "duplicate", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			f.run("enable")
			dir := filepath.Join(f.dir, "state")
			path := filepath.Join(dir, control.FileName)
			raw := []byte("{")
			switch kind {
			case "symlink":
				os.Rename(path, path+".saved")
				os.Symlink(path+".saved", path)
			case "fifo":
				os.Remove(path)
				if err := unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				raw = bytes.Repeat([]byte("{"), freezeRawLimit+1)
				os.WriteFile(path, raw, 0600)
			case "unreadable":
				os.WriteFile(path, raw, 0600)
				os.Chmod(path, 0)
				defer os.Chmod(path, 0600)
				if os.Geteuid() == 0 {
					t.Skip("root can read permission-denied fixtures")
				}
			case "null":
				raw = []byte(`null`)
				os.WriteFile(path, raw, 0600)
			case "duplicate":
				raw = []byte(`{"enabled":true,"enabled":false}`)
				os.WriteFile(path, raw, 0600)
			case "unknown":
				raw = []byte(`{"futureHold":true}`)
				os.WriteFile(path, raw, 0600)
			case "schema":
				raw = []byte(`{"paused":"invalid"}`)
				os.WriteFile(path, raw, 0600)
			case "array":
				raw = []byte(`[]`)
				os.WriteFile(path, raw, 0600)
			}
			if code, _, _ := f.run("pause"); code == 0 {
				t.Fatal("unsafe input accepted")
			}
			if kind != "fifo" && kind != "symlink" {
				os.Chmod(path, 0600)
				after, _ := os.ReadFile(path)
				if !bytes.Equal(after, raw) {
					t.Fatal("unsafe input rewritten")
				}
			}
		})
	}
}

func TestControlFreezeStaleSource(t *testing.T) {
	f := newFixture(t)
	f.run("enable")
	cfg, err := config.Load(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.dir, "state", control.FileName)
	os.WriteFile(path, []byte("{"), 0600)
	b, _ := os.ReadFile(f.cfg)
	os.WriteFile(f.cfg, append(b, '\n'), 0600)
	var out, errs bytes.Buffer
	handled, code := pauseDamagedControl(env{context.Background(), &out, &errs}, cfg)
	if !handled || code == 0 {
		t.Fatal("stale source accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != "{" {
		t.Fatal("stale source changed evidence")
	}
}

func TestControlFreezeCrashPublication(t *testing.T) {
	if stage := os.Getenv("FICTIONAL_FREEZE_CRASH"); stage != "" {
		path := os.Getenv("FICTIONAL_FREEZE_PATH")
		ops := freezePublication{(*os.File).Write, (*os.File).Sync, (*os.File).Close, os.Rename, syncFreezeDirectory}
		if stage == "before" {
			ops.replace = func(string, string) error { os.Exit(23); return nil }
		} else {
			ops.syncDir = func(string) error { os.Exit(23); return nil }
		}
		publishControlFreezeWith(path, []byte(`[{"fictional":"freeze"}]`), func() error { return nil }, ops)
		os.Exit(24)
	}
	for _, stage := range []string{"before", "after"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.json")
			os.WriteFile(path, []byte("{"), 0600)
			child := exec.Command(os.Args[0], "-test.run=^TestControlFreezeCrashPublication$")
			child.Env = append(os.Environ(), "FICTIONAL_FREEZE_CRASH="+stage, "FICTIONAL_FREEZE_PATH="+path)
			err := child.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 23 {
				t.Fatalf("child outcome %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal("canonical disappeared", err)
			}
			want := "{"
			if stage == "after" {
				want = `[{"fictional":"freeze"}]`
			}
			if string(after) != want {
				t.Fatal("permissive crash intermediate")
			}
		})
	}
}

func TestControlFreezeRecheckAfterPreflight(t *testing.T) {
	for _, change := range []string{"healthy", "replacement", "directory"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			f.run("enable")
			cfg, err := config.Load(f.cfg)
			if err != nil {
				t.Fatal(err)
			}
			dir := cfg.Loop.StateDir
			path := filepath.Join(dir, control.FileName)
			os.WriteFile(path, []byte("{"), 0600)
			snapshot, err := readFreezeSnapshot(dir)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "healthy":
				os.WriteFile(path, []byte(`{"enabled":true}`), 0600)
			case "replacement":
				os.Rename(path, path+".old")
				os.WriteFile(path, []byte("{"), 0600)
			case "directory":
				os.Rename(dir, dir+".old")
				os.Mkdir(dir, 0700)
				os.WriteFile(path, []byte("{"), 0600)
			}
			before, _ := os.ReadFile(path)
			var out, errs bytes.Buffer
			handled, code := pauseDamagedSnapshot(env{context.Background(), &out, &errs}, cfg, snapshot, false)
			if !handled || code == 0 {
				t.Fatal("changed preflight evidence accepted")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("changed preflight evidence replaced")
			}
		})
	}
}
