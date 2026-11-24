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

	// Refused inside a BB thread.
	t.Setenv("BB_THREAD_ID", "thr_example_supervised")
	if code, _, stderr := runRouterCLI(t, "on", "--dir", routerDir, "--allow-unstarted"); code != exitFailed || !strings.Contains(stderr, "BB thread") {
		t.Fatalf("expected BB_THREAD_ID refusal for on, got %d %q", code, stderr)
	}
	if code, _, stderr := runRouterCLI(t, "off", "--dir", routerDir); code != exitFailed || !strings.Contains(stderr, "BB thread") {
		t.Fatalf("expected BB_THREAD_ID refusal for off, got %d %q", code, stderr)
	}

	t.Setenv("BB_THREAD_ID", "")

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
