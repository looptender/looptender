//go:build unix

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/router/channel"
	"github.com/looptender/looptender/internal/router/core"
)

func shortRouterTestDir(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRouterVisibilitySummaryDoctorAndDock(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	t.Setenv(control.SupervisedLoopVar, "")

	routerDir := shortRouterTestDir(t, "rv-")
	settingsDir := filepath.Join(t.TempDir(), "settings")
	if err := os.MkdirAll(settingsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"theme":"light"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Untrusted router dir exposes directory state "untrusted" with no paths or secrets.
	untrusted := filepath.Join(t.TempDir(), "open-router")
	if err := os.MkdirAll(untrusted, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(untrusted, 0o755)
	s, ok := readRouterSummary(context.Background(), untrusted, true)
	if !ok || s.Directory != "untrusted" || s.Enabled || s.Answering || s.PointedCount != 0 {
		t.Fatalf("unexpected untrusted summary: ok=%v %+v", ok, s)
	}

	// Enabled and pointed while socket is NOT answering -> advisory warning in doctor (not error).
	if code, _, stderr := runRouterCLI(t, "on", "--allow-unstarted", "--dir", routerDir,
		"--front-url", "http://127.0.0.1:43124", "--settings", settingsPath); code != exitOK {
		t.Fatalf("router on --allow-unstarted failed: %d %s", code, stderr)
	}
	t.Cleanup(func() { _, _, _ = runRouterCLI(t, "off", "--dir", routerDir) })

	rWarn := doctorReport{SchemaVersion: 1, Checks: []doctorCheck{}}
	doctorRouter(context.Background(), &rWarn, routerDir, true)
	if rWarn.Router == nil || !rWarn.Router.Enabled || rWarn.Router.Answering || rWarn.Router.PointedCount != 1 || rWarn.Router.Service != "not_installed" {
		t.Fatalf("unexpected warn router summary: %+v", rWarn.Router)
	}
	if len(rWarn.Checks) != 1 || rWarn.Checks[0].ID != "router" || rWarn.Checks[0].Status != "warning" {
		t.Fatalf("expected single advisory warning check, got %+v", rWarn.Checks)
	}

	// Dock snapshot includes router summary without leaking paths, secrets, or client IDs.
	d, _ := dockFixtureReader(t)
	d.routerDir = routerDir
	d.routerOffline = true
	raw, err := d.json(false)
	if err != nil {
		t.Fatalf("dock json with router: %v", err)
	}
	rawStr := string(raw)
	for _, forbidden := range []string{routerDir, settingsPath, "clientId", "secretDigest", "X-Looptender-Client"} {
		if strings.Contains(rawStr, forbidden) {
			t.Fatalf("dock JSON leaked %q", forbidden)
		}
	}
	var decoded struct {
		Router *routerSummary `json:"router"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Router == nil {
		t.Fatalf("expected router summary in dock JSON: err=%v router=%+v", err, decoded.Router)
	}
	if !decoded.Router.Enabled || decoded.Router.Answering || decoded.Router.PointedCount != 1 || decoded.Router.Directory != "private" || decoded.Router.Service != "not_installed" {
		t.Fatalf("unexpected dock router summary: %+v", decoded.Router)
	}

	// Socket answering -> answering=true and doctor reports "ok".
	srv, err := channel.Listen(routerDir)
	if err != nil {
		t.Fatalf("channel listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(core.New(core.Config{}))
	}()
	defer func() {
		srv.Close()
		<-done
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := readRouterSummary(context.Background(), routerDir, true); ok && st.Answering {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	rOK := doctorReport{SchemaVersion: 1, Checks: []doctorCheck{}}
	doctorRouter(context.Background(), &rOK, routerDir, true)
	if rOK.Router == nil || !rOK.Router.Answering {
		t.Fatalf("expected answering=true, got %+v", rOK.Router)
	}
	if len(rOK.Checks) != 1 || rOK.Checks[0].ID != "router" || rOK.Checks[0].Status != "ok" {
		t.Fatalf("expected router ok check, got %+v", rOK.Checks)
	}

	// Turn router off -> no checks added even though dir exists.
	if code, _, stderr := runRouterCLI(t, "off", "--dir", routerDir); code != exitOK {
		t.Fatalf("router off failed: %d %s", code, stderr)
	}
	rOff := doctorReport{SchemaVersion: 1, Checks: []doctorCheck{}}
	doctorRouter(context.Background(), &rOff, routerDir, true)
	if rOff.Router == nil || rOff.Router.Enabled || rOff.Router.PointedCount != 0 {
		t.Fatalf("unexpected router off summary: %+v", rOff.Router)
	}
	if len(rOff.Checks) != 0 {
		t.Fatalf("expected no doctor check when router is off, got %+v", rOff.Checks)
	}
}

func TestDoctorCLIUnconfiguredRouterNeverFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))

	f := newDoctorFixture(t)
	code, rep, raw := f.run(t, successfulDoctorRun, "--offline")
	if code != exitOK {
		t.Fatalf("doctor exited %d with unconfigured router: %s", code, raw)
	}
	if rep.Router != nil {
		t.Fatalf("expected nil router when unconfigured, got %+v", rep.Router)
	}
	for _, c := range rep.Checks {
		if c.ID == "router" {
			t.Fatalf("did not expect router check when router is unconfigured: %+v", c)
		}
	}
}
