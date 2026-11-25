//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/looptender/looptender/internal/control"
)

func makeFakeRouterServiceHost(t *testing.T, platform string) (routerServiceHost, string, string, *[][]string, *sync.Mutex) {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	dir := filepath.Join(base, "router-state")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(base, "looptender-bin")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls [][]string
	h := routerServiceHost{
		platform:   platform,
		home:       home,
		executable: exe,
		searchPath: "/usr/bin:/bin",
		uid:        501,
		linger:     func() bool { return false },
		run: func(_ context.Context, args ...string) (int, error) {
			mu.Lock()
			calls = append(calls, append([]string(nil), args...))
			mu.Unlock()
			return 0, nil
		},
	}
	return h, dir, home, &calls, &mu
}

func runRouterServiceWithHost(h routerServiceHost, args ...string) (int, string, string) {
	var out, errBuf bytes.Buffer
	code := routerServiceCommand(env{ctx: context.Background(), stdout: &out, stderr: &errBuf}, args, h)
	return code, out.String(), errBuf.String()
}

func TestRouterServiceLabelsNeverCollide(t *testing.T) {
	for _, backend := range []string{"launchd", "systemd"} {
		core, front := routerServiceIdentities(backend, "/example/router")
		if core == front {
			t.Fatalf("%s core and front labels collided: %s", backend, core)
		}
		for _, label := range []string{core, front} {
			for _, reserved := range []string{"dev.looptender.loop.", "org.keepgoing.loop.", "dev.looptender.telegram-poll", "looptender-telegram-poll"} {
				if strings.HasPrefix(label, reserved) {
					t.Fatalf("%s label %q collides with reserved prefix %q", backend, label, reserved)
				}
			}
		}
	}
}

func TestRouterServiceSupervisedRefusal(t *testing.T) {
	h, dir, _, calls, _ := makeFakeRouterServiceHost(t, "darwin")
	for _, envPair := range [][2]string{{"BB_THREAD_ID", "thr_example_agent"}, {control.SupervisedLoopVar, "example-loop"}} {
		t.Setenv("BB_THREAD_ID", "")
		t.Setenv(control.SupervisedLoopVar, "")
		t.Setenv(envPair[0], envPair[1])

		for _, op := range []string{"install", "remove"} {
			code, _, stderr := runRouterServiceWithHost(h, op, "--dir", dir)
			if code != exitFailed || !strings.Contains(stderr, "refused") {
				t.Fatalf("%s under %s: got %d %q", op, envPair[0], code, stderr)
			}
		}
		// status remains allowed (read-only).
		if code, _, stderr := runRouterServiceWithHost(h, "status", "--dir", dir, "--json"); code != exitOK {
			t.Fatalf("status under %s failed: %d %q", envPair[0], code, stderr)
		}
	}
	if len(*calls) != 1 { // only the 2 status calls? actually 2 status calls * 2 print calls each; ensure no bootstrap/bootout
		for _, c := range *calls {
			if len(c) > 0 && c[0] != "print" {
				t.Fatalf("unexpected manager call under supervised refusal: %v", c)
			}
		}
	}
}

func TestRouterServiceLaunchdInstallStatusRemoveAndRollback(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	t.Setenv(control.SupervisedLoopVar, "")

	h, dir, home, calls, mu := makeFakeRouterServiceHost(t, "darwin")
	loaded := map[string]bool{}
	h.run = func(_ context.Context, args ...string) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		*calls = append(*calls, append([]string(nil), args...))
		switch args[0] {
		case "bootstrap":
			// Determine label from plist path.
			b, err := os.ReadFile(args[2])
			if err != nil {
				return 1, err
			}
			if strings.Contains(string(b), "router.core.") {
				loaded["core"] = true
			}
			if strings.Contains(string(b), "router.front.") {
				loaded["front"] = true
			}
			return 0, nil
		case "bootout":
			if strings.Contains(args[1], "router.core.") {
				delete(loaded, "core")
			}
			if strings.Contains(args[1], "router.front.") {
				delete(loaded, "front")
			}
			return 0, nil
		case "print":
			if args[1] == "user/501" {
				return 0, nil
			}
			if strings.Contains(args[1], "router.core.") && loaded["core"] {
				return 0, nil
			}
			if strings.Contains(args[1], "router.front.") && loaded["front"] {
				return 0, nil
			}
			return 113, nil
		}
		return 0, nil
	}

	// Sibling LaunchAgents and router.json must never be touched.
	agentsDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	unrelatedPlist := filepath.Join(agentsDir, "dev.looptender.telegram-poll.plist")
	if err := os.WriteFile(unrelatedPlist, []byte("keep-me"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dry run makes zero file changes and zero manager calls.
	code, stdout, stderr := runRouterServiceWithHost(h, "install", "--dir", dir, "--port", "43123", "--tls", "--dry-run", "--json")
	if code != exitOK {
		t.Fatalf("dry-run install = %d %s %s", code, stdout, stderr)
	}
	if len(*calls) != 0 {
		t.Fatalf("dry-run made manager calls: %v", *calls)
	}
	if _, err := os.Lstat(filepath.Join(dir, routerServiceRecordName)); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote service record")
	}

	// Real install.
	code, stdout, stderr = runRouterServiceWithHost(h, "install", "--dir", dir, "--port", "43123", "--tls", "--json")
	if code != exitOK {
		t.Fatalf("install = %d %s %s", code, stdout, stderr)
	}
	var rep routerServiceReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Backend != "launchd" || !rep.Recorded || rep.Core.Registration != "loaded" || rep.Front.Registration != "loaded" {
		t.Fatalf("unexpected install report: %+v", rep)
	}
	corePlist, err := os.ReadFile(rep.Core.Definition)
	if err != nil {
		t.Fatal(err)
	}
	frontPlist, err := os.ReadFile(rep.Front.Definition)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(corePlist), "<string>Background</string>") || !strings.Contains(string(corePlist), "<string>run</string>") {
		t.Fatalf("unexpected core plist: %s", corePlist)
	}
	if !strings.Contains(string(frontPlist), "<string>front</string>") || !strings.Contains(string(frontPlist), "<string>43123</string>") || !strings.Contains(string(frontPlist), "<string>--tls</string>") {
		t.Fatalf("unexpected front plist: %s", frontPlist)
	}

	// Status reports loaded.
	code, stdout, _ = runRouterServiceWithHost(h, "status", "--dir", dir, "--json")
	if code != exitOK || json.Unmarshal([]byte(stdout), &rep) != nil {
		t.Fatalf("status failed: %d %s", code, stdout)
	}
	if !rep.Recorded || !rep.Core.DefinitionPresent || !rep.Front.DefinitionPresent || rep.Core.Registration != "loaded" || rep.Front.Registration != "loaded" {
		t.Fatalf("unexpected status report: %+v", rep)
	}

	// Dry-run remove changes nothing.
	code, _, _ = runRouterServiceWithHost(h, "remove", "--dir", dir, "--dry-run")
	if code != exitOK {
		t.Fatalf("dry-run remove failed: %d", code)
	}
	if _, err := os.Stat(rep.Core.Definition); err != nil {
		t.Fatalf("dry-run remove deleted core plist")
	}

	// Real remove deletes only its own plists and record, leaving unrelated files intact.
	code, stdout, stderr = runRouterServiceWithHost(h, "remove", "--dir", dir, "--json")
	if code != exitOK {
		t.Fatalf("remove = %d %s %s", code, stdout, stderr)
	}
	for _, p := range []string{rep.Core.Definition, rep.Front.Definition, filepath.Join(dir, routerServiceRecordName)} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("remove left %s behind", p)
		}
	}
	if b, err := os.ReadFile(unrelatedPlist); err != nil || string(b) != "keep-me" {
		t.Fatalf("unrelated plist modified: %v %q", err, b)
	}

	// Rollback if front bootstrap fails after core bootstrap succeeds.
	h.run = func(_ context.Context, args ...string) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		*calls = append(*calls, append([]string(nil), args...))
		if args[0] == "bootstrap" && strings.Contains(args[2], "router.front.") {
			return 5, nil
		}
		return 0, nil
	}
	code, _, _ = runRouterServiceWithHost(h, "install", "--dir", dir)
	if code != exitFailed {
		t.Fatalf("expected failed install on front bootstrap failure, got %d", code)
	}
	for _, p := range []string{rep.Core.Definition, rep.Front.Definition, filepath.Join(dir, routerServiceRecordName)} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("rollback left %s behind", p)
		}
	}
}

func TestRouterServiceSystemdAndValidation(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	t.Setenv(control.SupervisedLoopVar, "")

	h, dir, _, calls, mu := makeFakeRouterServiceHost(t, "linux")
	enabled := map[string]bool{}
	h.run = func(_ context.Context, args ...string) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		*calls = append(*calls, append([]string(nil), args...))
		if len(args) >= 3 && args[0] == "enable" && args[1] == "--now" {
			enabled[args[2]] = true
		}
		if len(args) >= 3 && args[0] == "disable" && args[1] == "--now" {
			delete(enabled, args[2])
		}
		return 0, nil
	}
	h.query = func(_ context.Context, args ...string) ([]byte, int, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(args) >= 3 && args[0] == "show" {
			unit := args[len(args)-1]
			canonDir, _ := canonicalFuturePath(dir)
			coreLabel, frontLabel := routerServiceIdentities("systemd", canonDir)
			def := filepath.Join(h.definitionDir(), unit)
			if unit != coreLabel && unit != frontLabel {
				return nil, 1, nil
			}
			if enabled[unit] {
				return []byte("Id=" + unit + "\nLoadState=loaded\nActiveState=active\nUnitFileState=enabled\nFragmentPath=" + def + "\nDropInPaths=\n"), 0, nil
			}
			return []byte("Id=" + unit + "\nLoadState=not-found\nActiveState=inactive\nUnitFileState=\nFragmentPath=\nDropInPaths=\n"), 0, nil
		}
		return nil, 0, nil
	}

	code, stdout, stderr := runRouterServiceWithHost(h, "install", "--dir", dir, "--json")
	if code != exitOK {
		t.Fatalf("systemd install = %d %s %s", code, stdout, stderr)
	}
	var rep routerServiceReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Backend != "systemd" || rep.Lingering == nil || *rep.Lingering {
		t.Fatalf("unexpected systemd install report: %+v", rep)
	}
	frontUnit, err := os.ReadFile(rep.Front.Definition)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frontUnit), "After="+rep.Core.Label) || !strings.Contains(string(frontUnit), "Restart=always") {
		t.Fatalf("unexpected systemd front unit: %s", frontUnit)
	}

	// Status shows loaded + lingering=false without enabling lingering.
	code, stdout, _ = runRouterServiceWithHost(h, "status", "--dir", dir, "--json")
	if code != exitOK || json.Unmarshal([]byte(stdout), &rep) != nil {
		t.Fatalf("systemd status failed: %d", code)
	}
	if rep.Core.Registration != "loaded" || rep.Front.Registration != "loaded" || rep.Lingering == nil || *rep.Lingering {
		t.Fatalf("unexpected systemd status report: %+v", rep)
	}

	// Remove cleans up units and record.
	if code, _, stderr := runRouterServiceWithHost(h, "remove", "--dir", dir); code != exitOK {
		t.Fatalf("systemd remove = %d %s", code, stderr)
	}

	// Usage / platform validation checks.
	if code, _, _ := runRouterServiceWithHost(h, "status", "--dir", dir, "--dry-run"); code != exitUsage {
		t.Fatalf("expected usage error for --dry-run on status, got %d", code)
	}
	if code, _, _ := runRouterServiceWithHost(h, "remove", "--dir", dir, "--port", "1234"); code != exitUsage {
		t.Fatalf("expected usage error for --port on remove, got %d", code)
	}
	if code, _, _ := runRouterServiceWithHost(h, "install", "--dir", dir, "--port", "70000"); code != exitUsage {
		t.Fatalf("expected usage error for out-of-range port, got %d", code)
	}
	if code, _, _ := runRouterServiceWithHost(h, "install", "--dir", dir, "--upstream", "ftp://example.com"); code != exitUsage {
		t.Fatalf("expected usage error for invalid upstream, got %d", code)
	}
	hWin := h
	hWin.platform = "windows"
	if code, _, _ := runRouterServiceWithHost(hWin, "status", "--dir", dir); code != exitFailed {
		t.Fatalf("expected unsupported platform failure on windows, got %d", code)
	}
}
