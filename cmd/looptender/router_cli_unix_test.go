//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/router/pointer"
)

func runRouterCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := run(context.Background(), append([]string{"router"}, args...), &out, &errBuf)
	return code, out.String(), errBuf.String()
}

func TestRouterStatusReadOnly(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	base := t.TempDir()
	missing := filepath.Join(base, "not-created")

	code, stdout, stderr := runRouterCLI(t, "status", "--dir", missing, "--json")
	if code != exitOK {
		t.Fatalf("status missing = %d (%s)", code, stderr)
	}
	var st routerStatusOut
	if err := json.Unmarshal([]byte(stdout), &st); err != nil {
		t.Fatal(err)
	}
	if st.Directory != "missing" || st.Enabled || st.Answering || st.PointedCount != 0 {
		t.Fatalf("unexpected status for missing dir: %+v", st)
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatalf("router status must be read-only and not create %s", missing)
	}

	openDir := filepath.Join(base, "open")
	if err := os.MkdirAll(openDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(openDir, 0o755)
	code, stdout, _ = runRouterCLI(t, "status", "--dir", openDir, "--json")
	if code != exitOK {
		t.Fatalf("status open = %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &st); err != nil || st.Directory != "untrusted" {
		t.Fatalf("status open = %+v, err = %v", st, err)
	}

	privDir := filepath.Join(base, "priv")
	if err := os.MkdirAll(privDir, 0o700); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ = runRouterCLI(t, "status", "--dir", privDir)
	if code != exitOK || !strings.Contains(stdout, "router: off (directory private, answering false, pointed 0)") {
		t.Fatalf("human status = %d %q", code, stdout)
	}
}

func TestRouterOnOffPointerAndRollback(t *testing.T) {
	base := t.TempDir()
	// Keep the Unix socket path short for macOS's 104-byte sun_path limit.
	routerDir, err := os.MkdirTemp("", "rt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(routerDir) })
	if err := os.Chmod(routerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settingsDir := filepath.Join(base, "claude-a")
	if err := os.MkdirAll(settingsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settingsExisting := filepath.Join(settingsDir, "settings.json")
	origBytes := []byte("{\n  \"theme\": \"dark\"\n}\n")
	if err := os.WriteFile(settingsExisting, origBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	settingsNewDir := filepath.Join(base, "claude-b")
	if err := os.MkdirAll(settingsNewDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settingsNew := filepath.Join(settingsNewDir, "settings.json")

	// Refused inside a BB thread or supervised loop.
	t.Setenv("BB_THREAD_ID", "thr_example_supervised")
	if code, _, stderr := runRouterCLI(t, "on", "--dir", routerDir, "--allow-unstarted"); code != exitFailed || !strings.Contains(stderr, "supervised") {
		t.Fatalf("expected BB_THREAD_ID refusal for on, got %d %q", code, stderr)
	}
	if code, _, stderr := runRouterCLI(t, "off", "--dir", routerDir); code != exitFailed || !strings.Contains(stderr, "supervised") {
		t.Fatalf("expected BB_THREAD_ID refusal for off, got %d %q", code, stderr)
	}
	t.Setenv("BB_THREAD_ID", "")
	t.Setenv(control.SupervisedLoopVar, "example-loop")
	if code, _, stderr := runRouterCLI(t, "on", "--dir", routerDir, "--allow-unstarted"); code != exitFailed || !strings.Contains(stderr, "supervised") {
		t.Fatalf("expected %s refusal for on, got %d %q", control.SupervisedLoopVar, code, stderr)
	}
	if code, _, stderr := runRouterCLI(t, "off", "--dir", routerDir); code != exitFailed || !strings.Contains(stderr, "supervised") {
		t.Fatalf("expected %s refusal for off, got %d %q", control.SupervisedLoopVar, code, stderr)
	}
	t.Setenv(control.SupervisedLoopVar, "")

	// Without --allow-unstarted and no running router, `router on` refuses.
	if code, _, _ := runRouterCLI(t, "on", "--dir", routerDir); code != exitFailed {
		t.Fatalf("expected failure when router process is not answering, got %d", code)
	}

	// Start `looptender router run` in background so `router on` sees an answering socket.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runOut := &lockedBuffer{}
	runDone := make(chan int, 1)
	go func() {
		runDone <- run(ctx, []string{"router", "run", "--dir", routerDir}, runOut, runOut)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		code, stdout, _ := runRouterCLI(t, "status", "--dir", routerDir, "--json")
		var st routerStatusOut
		if code == exitOK && json.Unmarshal([]byte(stdout), &st) == nil && st.Answering {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Enable with two settings files.
	code, stdout, stderr := runRouterCLI(t, "on", "--dir", routerDir, "--front-url", "http://127.0.0.1:43123",
		"--settings", settingsExisting, "--settings", settingsNew)
	if code != exitOK {
		t.Fatalf("router on failed: %d %s %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, routerPlanTermsLine) || !strings.Contains(stdout, "router: on (2 pointed)") {
		t.Fatalf("unexpected router on output: %q", stdout)
	}

	// Verify pointer-state.json is 0600 and contains no raw secret value.
	psPath := filepath.Join(routerDir, pointerStateFileName)
	if st, err := os.Stat(psPath); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("pointer-state.json mode = %v, err = %v", st, err)
	}
	pointedBytes, err := os.ReadFile(settingsExisting)
	if err != nil || !strings.Contains(string(pointedBytes), pointer.KeyBaseURL) {
		t.Fatalf("settingsExisting not pointed: %s (%v)", pointedBytes, err)
	}
	if _, err := os.Stat(settingsNew); err != nil {
		t.Fatalf("settingsNew not created: %v", err)
	}

	// Verify status reflects enabled + answering + 2 pointed.
	code, stdout, _ = runRouterCLI(t, "status", "--dir", routerDir, "--json")
	if code != exitOK {
		t.Fatalf("status = %d", code)
	}
	var st routerStatusOut
	if err := json.Unmarshal([]byte(stdout), &st); err != nil || !st.Enabled || !st.Answering || st.PointedCount != 2 {
		t.Fatalf("unexpected status: %+v, err=%v", st, err)
	}

	// Turn off: restores original bytes on settingsExisting, removes settingsNew.
	code, stdout, stderr = runRouterCLI(t, "off", "--dir", routerDir)
	if code != exitOK || !strings.Contains(stdout, "router: off") {
		t.Fatalf("router off = %d %q %q", code, stdout, stderr)
	}
	afterBytes, err := os.ReadFile(settingsExisting)
	if err != nil || !bytes.Equal(afterBytes, origBytes) {
		t.Fatalf("settingsExisting not restored: got %q, want %q (err=%v)", afterBytes, origBytes, err)
	}
	if _, err := os.Lstat(settingsNew); !os.IsNotExist(err) {
		t.Fatalf("settingsNew should be removed by router off, err=%v", err)
	}

	// Rollback test: second file has foreign proxy setting; first file must be rolled back.
	foreignDir := filepath.Join(base, "foreign")
	if err := os.MkdirAll(foreignDir, 0o700); err != nil {
		t.Fatal(err)
	}
	foreignSettings := filepath.Join(foreignDir, "settings.json")
	if err := os.WriteFile(foreignSettings, []byte(`{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:9999"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, _ = runRouterCLI(t, "on", "--dir", routerDir, "--front-url", "http://127.0.0.1:43123",
		"--settings", settingsExisting, "--settings", foreignSettings)
	if code != exitFailed {
		t.Fatalf("expected failure on foreign settings, got %d", code)
	}
	rolledBack, err := os.ReadFile(settingsExisting)
	if err != nil || !bytes.Equal(rolledBack, origBytes) {
		t.Fatalf("settingsExisting not rolled back: got %q, want %q", rolledBack, origBytes)
	}

	cancel()
	if c := <-runDone; c != exitOK {
		t.Fatalf("router run exit = %d: %s", c, runOut.String())
	}
}

func TestValidateLoopbackURL(t *testing.T) {
	valid := []string{
		"http://127.0.0.1:43123",
		"https://127.0.0.1:43123/",
		"http://localhost:8080",
		"http://[::1]:8080",
	}
	for _, u := range valid {
		if err := validateLoopbackURL(u); err != nil {
			t.Errorf("validateLoopbackURL(%q) = %v, want nil", u, err)
		}
	}
	invalid := []string{
		"",
		"ftp://127.0.0.1:8080",
		"http://example.com:8080",
		"http://user:pass@127.0.0.1:8080",
		"http://127.0.0.1:8080/v1",
		"http://127.0.0.1:8080?x=1",
		"http://127.0.0.1:8080#frag",
	}
	for _, u := range invalid {
		if err := validateLoopbackURL(u); err == nil {
			t.Errorf("validateLoopbackURL(%q) = nil, want error", u)
		}
	}
}

func TestRouterOffRotatesDigestOnChangedAndOnRollsBackPointerState(t *testing.T) {
	t.Setenv("BB_THREAD_ID", "")
	t.Setenv(control.SupervisedLoopVar, "")
	base := t.TempDir()
	routerDir := filepath.Join(base, "router")
	if err := os.MkdirAll(routerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settingsDir := filepath.Join(base, "claude")
	if err := os.MkdirAll(settingsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settingsFile := filepath.Join(settingsDir, "settings.json")
	orig := []byte("{\"theme\":\"light\"}\n")
	if err := os.WriteFile(settingsFile, orig, 0o600); err != nil {
		t.Fatal(err)
	}

	// Point via router on --allow-unstarted.
	if code, _, stderr := runRouterCLI(t, "on", "--dir", routerDir, "--allow-unstarted", "--front-url", "http://127.0.0.1:43123", "--settings", settingsFile); code != exitOK {
		t.Fatalf("router on failed: %d %s", code, stderr)
	}
	psBefore, err := readPointerState(routerDir)
	if err != nil {
		t.Fatal(err)
	}
	absSettings, _ := filepath.Abs(settingsFile)
	entry := psBefore.Entries[absSettings]
	digestBefore := psBefore.Clients.Digests[entry.ClientID]
	if digestBefore == "" {
		t.Fatal("missing initial client digest")
	}

	// Simulate manual edit (ErrChanged) and run router off: entry must be dropped and digest rotated.
	if err := os.WriteFile(settingsFile, []byte("{\"edited\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runRouterCLI(t, "off", "--dir", routerDir); code != exitOK {
		t.Fatalf("router off failed: %d %s", code, stderr)
	}
	psAfter, err := readPointerState(routerDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(psAfter.Entries) != 0 {
		t.Fatalf("expected 0 entries after off, got %d", len(psAfter.Entries))
	}
	if psAfter.Clients.Digests[entry.ClientID] == digestBefore {
		t.Fatal("expected client digest to rotate on ErrChanged")
	}

	// Simulate router.json write failure (directory at router.json): router on must roll back pointer-state.json.
	if err := os.Remove(filepath.Join(routerDir, routerStateFileName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(routerDir, routerStateFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runRouterCLI(t, "on", "--dir", routerDir, "--allow-unstarted", "--front-url", "http://127.0.0.1:43123", "--settings", settingsFile); code != exitFailed {
		t.Fatalf("expected router on failure when router.json is a directory, got %d", code)
	}
	psRolled, err := readPointerState(routerDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(psRolled.Entries) != 0 {
		t.Fatalf("expected pointer-state.json rolled back to 0 entries, got %d", len(psRolled.Entries))
	}
}
