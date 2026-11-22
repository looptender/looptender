package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The Go and DOM-text tests check what the server sends and that dock.js
// contains the right lines; none of them runs dock.js. This test runs the real
// dock.js in jsdom against the data the panel server sends, with every loop
// state and optional field put into it, and fails on any exception while
// rendering any page. A loop with a provider chip and a failed lane threw
// "tag is not a function" (a local variable hid the tag helper) and the
// Overview showed nothing; no test had that combination, and none executed the
// code, so it shipped.
//
// jsdom is the BB plugin's test dependency: DOCK_JSDOM_DIR names its directory,
// or it is found under integrations/bb-plugin-keepgoing/node_modules. Without
// it the test skips, unless DOCK_PAGE_REQUIRED is set (the plugin CI job sets
// it, where jsdom is installed).
func TestDockPageRendersEveryLoopStateWithoutAnException(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("DOCK_PAGE_REQUIRED") != "" {
			t.Fatal("node is required and not available")
		}
		t.Skip("node unavailable: dock.js was not executed")
	}
	jsdom := os.Getenv("DOCK_JSDOM_DIR")
	if jsdom == "" {
		jsdom = filepath.Join("..", "..", "integrations", "bb-plugin-keepgoing", "node_modules", "jsdom")
	}
	if info, err := os.Stat(jsdom); err != nil || !info.IsDir() {
		if os.Getenv("DOCK_PAGE_REQUIRED") != "" {
			t.Fatalf("jsdom is required and not found at %s", jsdom)
		}
		t.Skip("jsdom unavailable: dock.js was not executed")
	}
	abs, _ := filepath.Abs(jsdom)

	base, _, _ := dockServe(t)
	get := func(path string) json.RawMessage {
		resp, body := dockRequest(t, http.MethodGet, base+path, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		return json.RawMessage(body)
	}
	dir := t.TempDir()
	fixture, _ := json.Marshal(map[string]json.RawMessage{"dock": get("api/v1/dock"), "server": get(dockServerPath), "usage": get("api/v1/usage")})
	for name, content := range map[string][]byte{"fixture.json": fixture, "dock.js": []byte(dockUIText(t, "dock.js")), "index.html": []byte(dockUIText(t, "index.html"))} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script, _ := filepath.Abs(filepath.Join("testdata", "dock_page_check.mjs"))
	cmd := exec.Command(node, script, dir)
	cmd.Env = append(os.Environ(), "DOCK_JSDOM_DIR="+abs)
	done := make(chan struct{})
	var out []byte
	go func() { out, err = cmd.CombinedOutput(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Minute):
		_ = cmd.Process.Kill()
		t.Fatal("dock.js rendering took more than three minutes")
	}
	if err != nil {
		t.Fatalf("dock.js did not render cleanly: %v\n%s", err, out)
	}
	t.Log(string(out))
}

// dock.js must not call one of its own helper functions through a local
// variable or parameter of the same name (the shadowing that broke the
// Overview). Resolved with the TypeScript compiler's scope analysis; the same
// skip rules as above.
func TestDockPageNeverCallsAHelperThroughAShadowingVariable(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("DOCK_PAGE_REQUIRED") != "" {
			t.Fatal("node is required and not available")
		}
		t.Skip("node unavailable")
	}
	tsDir := os.Getenv("TS_DIR")
	if tsDir == "" {
		tsDir = filepath.Join("..", "..", "integrations", "bb-plugin-keepgoing", "node_modules", "typescript")
	}
	if info, err := os.Stat(tsDir); err != nil || !info.IsDir() {
		if os.Getenv("DOCK_PAGE_REQUIRED") != "" {
			t.Fatalf("typescript is required and not found at %s", tsDir)
		}
		t.Skip("typescript unavailable")
	}
	abs, _ := filepath.Abs(tsDir)
	js := filepath.Join(t.TempDir(), "dock.js")
	if err := os.WriteFile(js, []byte(dockUIText(t, "dock.js")), 0o600); err != nil {
		t.Fatal(err)
	}
	script, _ := filepath.Abs(filepath.Join("testdata", "dock_page_lint.mjs"))
	cmd := exec.Command(node, script, js)
	cmd.Env = append(os.Environ(), "TS_DIR="+abs)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
