//go:build unix

package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/looptender/looptender/internal/accounts"
	"github.com/looptender/looptender/internal/config"
	"github.com/looptender/looptender/internal/control"
	"github.com/looptender/looptender/internal/registry"
	"github.com/looptender/looptender/internal/state"
)

func TestDockLoopRoutesUIStaticInvariants(t *testing.T) {
	js := dockUIText(t, "dock.js")
	css := dockUIText(t, "dock.css")
	help := dockUIText(t, "routes-help.html")

	if strings.Contains(help, "\u2014") {
		t.Error("routes-help.html contains an em dash")
	}
	if !strings.Contains(help, "Enable") && !strings.Contains(help, "--allow-changes") {
		t.Error("routes-help.html missing panel mode documentation")
	}
	for _, fn := range []string{"loopRouteRequest", "loopRouteAddForm", "loopRouteManagePanel", "loopRouteOwnPanel", "loopRouteMgmtPanel", "loopRouteFlowBox", "loopRouteFeedbackCard"} {
		body := funcBody(t, js, fn)
		if strings.Contains(body, "\u2014") {
			t.Errorf("%s contains an em dash", fn)
		}
		if regexp.MustCompile(`\b(?:node|nodes)\b`).MatchString(body) {
			t.Errorf("%s uses forbidden word node/nodes", fn)
		}
	}
	// Model discovery must never run on page load or form open automatically.
	flowBody := funcBody(t, js, "loopRouteFlowBox")
	if strings.Contains(flowBody, `"models"`) {
		t.Error("loopRouteFlowBox triggers models lookup on render/open")
	}
	// Confirm for selected loop add sends strictly op and loop.
	fbBody := funcBody(t, js, "loopRouteFeedbackCard")
	if !strings.Contains(fbBody, `loopRouteRequest("confirm", { op: opName, loop: l.key }`) {
		t.Error("confirm payload does not match strict { op, loop } contract")
	}
	// Request lifetime counters and unresolved save tracking exist in dock.js.
	reqBody := funcBody(t, js, "loopRouteRequest")
	for _, tok := range []string{"editorSeq", "reqSeq", "modelSeq", "unresolvedSave"} {
		if !strings.Contains(reqBody, tok) && !strings.Contains(js, tok) {
			t.Errorf("dock.js missing lifetime/reconciliation guard %q", tok)
		}
	}
	// Progressive disclosure summaries and boilerplate partitioning exist in dock.js.
	for _, tok := range []string{"loop-route-details", "How headless routes and limit labels work", "Policy and verification details", "loopRoutePartitionLines"} {
		if !strings.Contains(js, tok) {
			t.Errorf("dock.js missing P1 progressive disclosure element %q", tok)
		}
	}
	// Terminal commands remain collapsed inside .how-advanced.
	if !strings.Contains(js, `el("details", "how-advanced")`) || !strings.Contains(js, `Terminal commands (advanced)`) {
		t.Error("dock.js missing collapsed Terminal commands (advanced) wrapper")
	}
	if !strings.Contains(css, ".loop-route-bar") || !strings.Contains(css, ".how-advanced") || !strings.Contains(css, ".loop-route-details") || !strings.Contains(css, ".loop-route-result.is-reconciled") {
		t.Error("dock.css missing per-loop route editor styles")
	}
}

func TestDockLoopRoutesUIProductionWiring(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("DOCK_PAGE_REQUIRED") != "" {
			t.Fatal("node is required and not available")
		}
		t.Skip("node unavailable: loop routes UI test skipped")
	}
	jsdom := os.Getenv("DOCK_JSDOM_DIR")
	if jsdom == "" {
		jsdom = filepath.Join("..", "..", "integrations", "bb-plugin-keepgoing", "node_modules", "jsdom")
	}
	if info, err := os.Stat(jsdom); err != nil || !info.IsDir() {
		if os.Getenv("DOCK_PAGE_REQUIRED") != "" {
			t.Fatalf("jsdom is required and not found at %s", jsdom)
		}
		t.Skip("jsdom unavailable: loop routes UI test skipped")
	}
	absJSDOM, _ := filepath.Abs(jsdom)

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fakeHome := filepath.Join(root, "home")
	fakeCfg := filepath.Join(root, "xdg")
	_ = os.MkdirAll(fakeHome, 0o700)
	_ = os.MkdirAll(fakeCfg, 0o700)
	t.Setenv("HOME", fakeHome)
	t.Setenv("XDG_CONFIG_HOME", fakeCfg)
	t.Setenv("BB_THREAD_ID", "")

	unexpectedTrap := filepath.Join(root, "unexpected-trap")
	modelsLog := filepath.Join(root, "models-log")
	t.Setenv("TEST_UNEXPECTED_TRAP", unexpectedTrap)
	t.Setenv("TEST_MODELS_LOG", modelsLog)
	binDir := filepath.Join(root, "bin")
	_ = os.MkdirAll(binDir, 0o700)
	for _, name := range []string{"claude", "codex"} {
		write(t, filepath.Join(binDir, name), "#!/bin/sh\nprintf '%s\n' \"$0 $*\" >> \"$TEST_UNEXPECTED_TRAP\"\nexit 99\n", 0o700)
	}
	write(t, filepath.Join(binDir, "bb"), `#!/bin/sh
if [ "$1" = "provider" ] && [ "$2" = "models" ]; then
  printf '%s\n' "$*" >> "$TEST_MODELS_LOG"
  printf '[{"model":"gpt-5.2-codex","displayName":"GPT-5.2 Codex","isDefault":true,"supportedReasoningEfforts":[{"reasoningEffort":"medium"}]}]\n'
  exit 0
fi
printf '%s\n' "bb $*" >> "$TEST_UNEXPECTED_TRAP"
exit 99
`, 0o700)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		if b, err := os.ReadFile(unexpectedTrap); !os.IsNotExist(err) {
			t.Errorf("unexpected provider/bb call: %q (%v)", b, err)
		}
	})

	project := filepath.Join(root, "project")
	_ = os.MkdirAll(project, 0o700)
	for _, args := range [][]string{{"init", "-q"}, {"-c", "commit.gpgsign=false", "commit", "--allow-empty", "-q", "-m", "test: init fictional repo"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = project
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Example", "GIT_AUTHOR_EMAIL=example@example.com", "GIT_COMMITTER_NAME=Example", "GIT_COMMITTER_EMAIL=example@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", out, err)
		}
	}

	acctDirB := filepath.Join(root, "acct-codex-b")
	_ = os.MkdirAll(acctDirB, 0o700)
	catalogMain := filepath.Join(root, "catalog-main")
	catalogExtOnly := filepath.Join(root, "catalog-ext")
	_ = os.MkdirAll(catalogMain, 0o700)
	_ = os.MkdirAll(catalogExtOnly, 0o700)
	oldPanelCat, oldSetupCat := panelAccountsDir, setupAccountsDir
	panelAccountsDir, setupAccountsDir = catalogMain, catalogMain
	t.Cleanup(func() {
		panelAccountsDir, setupAccountsDir = oldPanelCat, oldSetupCat
	})

	var claudeID, codexID string
	if err := accounts.Update(catalogMain, func(all *accounts.File) error {
		c1 := accounts.NewEntry(*all, accounts.Entry{
			Label:    "Work Claude",
			Provider: "claude-code",
			Mode:     "inherited",
			CLI:      filepath.Join(binDir, "claude"),
			Workdir:  project,
		}, "", time.Now().UTC(), os.Environ())
		claudeID = c1.ID
		c2 := accounts.NewEntry(*all, accounts.Entry{
			Label:     "Alt Codex",
			Provider:  "codex",
			Mode:      "explicit_directory",
			CLI:       filepath.Join(binDir, "codex"),
			Workdir:   project,
			Directory: acctDirB,
		}, "", time.Now().UTC(), os.Environ())
		codexID = c2.ID
		all.Entries = append(all.Entries, c1, c2)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := accounts.Update(catalogExtOnly, func(all *accounts.File) error {
		cExt := accounts.NewEntry(*all, accounts.Entry{
			Label:    "BB External Only",
			Provider: "bb",
			Mode:     "externally_managed",
		}, "", time.Now().UTC(), os.Environ())
		all.Entries = append(all.Entries, cExt)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	routesPath := filepath.Join(root, "routes.yaml")
	t.Setenv(config.RoutesFileEnv, routesPath)
	write(t, routesPath, `version: 1
routes:
  - id: std-codex
    provider: codex
    model: gpt-5.1-codex
    group: pool-std
    enter: headless
    account_dir: `+acctDirB+`
`, 0o600)

	regDir := filepath.Join(root, "registry")
	_ = os.MkdirAll(regDir, 0o700)

	// Loop 1: first-file loop with no fallbacks.yaml yet
	firstDir := filepath.Join(root, "loops", "first-loop")
	_ = os.MkdirAll(firstDir, 0o700)
	firstCfgPath := filepath.Join(firstDir, config.FileName)
	write(t, firstCfgPath, `version: 1
loop:
  name: first-loop
  state_dir: state
  adapter: claude
  provider: claude-code
  group: pool-first-primary
  claude:
    cli: `+filepath.Join(binDir, "claude")+`
    workdir: `+project+`
    model: claude-opus-4-5
  prompt: Continue fictional first loop.
  errors:
    failover_on: []
`, 0o600)
	firstCfg := loadFixture(t, firstCfgPath)
	firstStateCanon, _ := canonicalFuturePath(firstCfg.Loop.StateDir)
	if err := control.Save(firstCfg.Loop.StateDir, control.File{Enabled: true, Paused: true}); err != nil {
		t.Fatal(err)
	}
	firstEntry, err := registry.Add(regDir, firstCfg.Loop.Name, firstCfgPath, firstStateCanon)
	if err != nil {
		t.Fatal(err)
	}

	// Loop 2: own fallbacks.yaml with 2 standby routes (current set to primary, plus standby active test)
	ownDir := filepath.Join(root, "loops", "own-loop")
	_ = os.MkdirAll(ownDir, 0o700)
	ownCfgPath := filepath.Join(ownDir, config.FileName)
	ownFbPath := filepath.Join(ownDir, "fallbacks.yaml")
	write(t, ownFbPath, `version: 1
probe:
  scheduled_disabled: true
candidates:
  - id: standby-claude
    adapter: claude
    provider: claude-code
    group: pool-claude
    account: `+claudeID+`
    claude:
      cli: `+filepath.Join(binDir, "claude")+`
      workdir: `+project+`
      model: claude-sonnet-4-5
  - id: standby-codex
    adapter: codex
    provider: codex
    group: pool-codex
    account: `+codexID+`
    codex:
      cli: `+filepath.Join(binDir, "codex")+`
      workdir: `+project+`
      env:
        CODEX_HOME: `+acctDirB+`
      model: gpt-5.1-codex
`, 0o600)
	write(t, ownCfgPath, `version: 1
loop:
  name: own-loop
  state_dir: state
  adapter: claude
  provider: claude-code
  group: pool-own-primary
  claude:
    cli: `+filepath.Join(binDir, "claude")+`
    workdir: `+project+`
    model: claude-opus-4-5
  prompt: Continue fictional own loop.
  fallbacks: fallbacks.yaml
  errors:
    failover_on: []
`, 0o600)
	ownCfg := loadFixture(t, ownCfgPath)
	ownStateCanon, _ := canonicalFuturePath(ownCfg.Loop.StateDir)
	if err := control.Save(ownCfg.Loop.StateDir, control.File{Enabled: true, Paused: true}); err != nil {
		t.Fatal(err)
	}
	// Mark standby-claude as currently active in state.json to verify Turn-off protection in Manage order.
	if err := state.Save(ownCfg.Loop.StateDir, state.File{ActiveTarget: "standby-claude"}); err != nil {
		t.Fatal(err)
	}
	ownEntry, err := registry.Add(regDir, ownCfg.Loop.Name, ownCfgPath, ownStateCanon)
	if err != nil {
		t.Fatal(err)
	}

	// Loop 3: follows standard route list
	stdDir := filepath.Join(root, "loops", "std-loop")
	_ = os.MkdirAll(stdDir, 0o700)
	stdCfgPath := filepath.Join(stdDir, config.FileName)
	write(t, stdCfgPath, `version: 1
loop:
  name: std-loop
  state_dir: state
  adapter: claude
  provider: claude-code
  group: pool-std-primary
  claude:
    cli: `+filepath.Join(binDir, "claude")+`
    workdir: `+project+`
    model: claude-opus-4-5
  prompt: Continue fictional std loop.
  routes: standard
`, 0o600)
	stdCfg := loadFixture(t, stdCfgPath)
	stdStateCanon, _ := canonicalFuturePath(stdCfg.Loop.StateDir)
	if err := control.Save(stdCfg.Loop.StateDir, control.File{Enabled: true, Paused: true}); err != nil {
		t.Fatal(err)
	}
	stdEntry, err := registry.Add(regDir, stdCfg.Loop.Name, stdCfgPath, stdStateCanon)
	if err != nil {
		t.Fatal(err)
	}

	// Loop 4: dedicated fictional loop for P1/P2 async lifetime and unknown-save regressions
	edgeDir := filepath.Join(root, "loops", "edge-loop")
	_ = os.MkdirAll(edgeDir, 0o700)
	edgeCfgPath := filepath.Join(edgeDir, config.FileName)
	write(t, edgeCfgPath, `version: 1
loop:
  name: edge-loop
  state_dir: state
  adapter: claude
  provider: claude-code
  group: pool-edge-primary
  claude:
    cli: `+filepath.Join(binDir, "claude")+`
    workdir: `+project+`
    model: claude-opus-4-5
  prompt: Continue fictional edge loop.
  errors:
    failover_on: []
`, 0o600)
	edgeCfg := loadFixture(t, edgeCfgPath)
	edgeStateCanon, _ := canonicalFuturePath(edgeCfg.Loop.StateDir)
	if err := control.Save(edgeCfg.Loop.StateDir, control.File{Enabled: true, Paused: true}); err != nil {
		t.Fatal(err)
	}
	edgeEntry, err := registry.Add(regDir, edgeCfg.Loop.Name, edgeCfgPath, edgeStateCanon)
	if err != nil {
		t.Fatal(err)
	}

	panelRoot := filepath.Join(root, "panel-dirs")
	_ = os.MkdirAll(panelRoot, 0o700)

	startServer := func(mode, catDir string, keys []string) ([]string, int, func()) {
		pdir := filepath.Join(panelRoot, mode+"-"+filepath.Base(catDir))
		_ = os.MkdirAll(pdir, 0o700)
		ln, err := dockListen(0)
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		st, err := newPanelState(port, time.Hour, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		reader := &dockReader{ctx: ctx, directory: regDir, refresh: time.Hour, staleAfter: defaultStaleAfter, query: func(ctx context.Context, dir, current string, _ bool) daemonStatus {
			return inspectDaemon(ctx, dir, current, true)
		}, versions: false, panelDir: pdir, accountsDir: catDir}
		page := newDockServer(port, st, reader)
		page.served = dockServed{SchemaVersion: 1, ServedBy: "panel", IdleSeconds: 3600, Version: "v0.3.0-dev.example01", VersionText: "v0.3.0-dev.example01"}
		nsecret, _ := panelRandom(32)
		h := &panelHandler{state: st, guard: newDockGuard(port), page: page, notifySecret: []byte(nsecret), notifyDir: pdir}
		if mode != "read_only" {
			c, err := newPanelChanges(ctx, regDir, reader, time.Now().UTC(), time.Now)
			if err != nil {
				t.Fatal(err)
			}
			c.controlsOnly = (mode == "controls")
			c.routeCatalog = catDir
			if mode == "bb_caller" {
				c.caller = "thr_example_other"
			} else {
				c.caller = ""
			}
			h.changes = c
		}
		go func() { _ = dockServeHandler(ctx, ln, h, io.Discard) }()
		var urls []string
		for _, k := range keys {
			u, _, err := st.mint(k, false)
			if err != nil {
				t.Fatal(err)
			}
			urls = append(urls, u)
		}
		return urls, port, func() {
			cancel()
			_ = ln.Close()
		}
	}

	roURLs, _, stopRO := startServer("read_only", catalogMain, []string{dockKey(ownEntry.ID)})
	defer stopRO()
	ctrlURLs, _, stopCtrl := startServer("controls", catalogMain, []string{dockKey(ownEntry.ID)})
	defer stopCtrl()
	bbURLs, _, stopBB := startServer("bb_caller", catalogMain, []string{dockKey(ownEntry.ID)})
	defer stopBB()
	allURLs, _, stopAll := startServer("all", catalogMain, []string{
		dockKey(firstEntry.ID),
		dockKey(ownEntry.ID),
		dockKey(stdEntry.ID),
		dockKey(edgeEntry.ID),
		dockKey(edgeEntry.ID),
		dockKey(edgeEntry.ID),
		dockKey(edgeEntry.ID),
	})
	defer stopAll()
	extURLs, _, stopExt := startServer("all", catalogExtOnly, []string{dockKey(firstEntry.ID)})
	defer stopExt()

	script := `
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";

const require = createRequire(path.join(process.env.DOCK_JSDOM_DIR, "package.json"));
const { JSDOM } = require(process.env.DOCK_JSDOM_DIR);
const cfg = JSON.parse(fs.readFileSync(process.env.LOOP_UI_CFG, "utf8"));

async function openSession(openUrl) {
  const res1 = await fetch(openUrl, { redirect: "manual" });
  const openHtml = await res1.text();
  const setCookie = res1.headers.get("set-cookie") || "";
  const cookiePair = setCookie.split(";")[0];
  assert(cookiePair.startsWith("keepgoing_panel="), "expected session cookie, got " + setCookie);
  const pathMatch = setCookie.match(/Path=([^;]+)/i);
  assert(pathMatch, "expected session cookie Path, got " + setCookie);
  const u = new URL(openUrl);
  const origin = u.origin;
  const sBase = origin + pathMatch[1];
  const hashMatch = openHtml.match(/#\/loop\/[A-Za-z0-9_-]+/);
  const curHash = hashMatch ? hashMatch[0] : "#/";
  const html = await (await fetch(sBase, { headers: { Cookie: cookiePair } })).text();
  const js = await (await fetch(sBase + "dock.js", { headers: { Cookie: cookiePair } })).text();

  const dom = new JSDOM(html, { url: sBase + curHash, runScripts: "outside-only", pretendToBeVisual: true });
  const { window } = dom;
  window.scrollTo = () => {};
  if (window.Element && !window.Element.prototype.scrollIntoView) {
    window.Element.prototype.scrollIntoView = () => {};
  }
  Object.assign(window, {
    fetch: async (url, init = {}) => {
      const target = new URL(String(url), sBase).toString();
      const headers = Object.assign({}, init.headers || {}, {
        Cookie: cookiePair,
        Origin: origin,
        "Sec-Fetch-Site": "same-origin",
        "Sec-Fetch-Mode": "cors",
        "Sec-Fetch-Dest": "empty"
      });
      return fetch(target, Object.assign({}, init, { headers }));
    }
  });
  window.eval(js);
  window.document.dispatchEvent(new window.Event("DOMContentLoaded"));
  return {
    dom,
    window,
    doc: window.document,
    async settle(checkFn, tries = 40) {
      for (let i = 0; i < tries; i++) {
        await new Promise(r => setTimeout(r, 25));
        if (checkFn()) return;
      }
      throw new Error("condition timed out");
    },
    destroy() {
      dom.window.close();
    }
  };
}

// 1 & 2 & 3: Non-full modes state mode before click, show Enable guidance, never fake + Add route
for (const [label, url, wantText] of [
  ["read_only", cfg.roOpen, "Read-only mode"],
  ["controls", cfg.ctrlOpen, "Controls mode"],
  ["bb_caller", cfg.bbOpen, "Opened from BB"]
]) {
  const s = await openSession(url);
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-mgmt-open"]'));
  assert(!s.doc.querySelector('[data-focus="loop-route-add-open"]'), label + " must not show + Add route");
  const adv = s.doc.querySelector("details.how-advanced");
  assert(adv && !adv.open, label + " advanced Terminal commands must start collapsed");
  assert(s.doc.querySelector(".loop-route-bar").textContent.includes(wantText), label + " mode label");
  s.doc.querySelector('[data-focus="loop-route-mgmt-open"]').click();
  await s.settle(() => s.doc.querySelector(".loop-route-mgmt"));
  assert(s.doc.querySelector(".loop-route-mgmt").textContent.includes("looptender panel --allow-changes"));
  if (label === "bb_caller") {
    const srv = await (await s.window.fetch("api/v1/server")).json();
    const prep = await (await s.window.fetch("api/v1/routes/prepare", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Keepgoing-Form": srv.form },
      body: JSON.stringify({ op: "add", loop: cfg.firstLoopKey, account: cfg.codexID, model: "gpt-5.2-codex", limit: "pool-bb", behavior: "headless", at: 1 })
    })).json();
    assert.equal(prep.outcome, "refused", "bb_caller prepare must be refused server-side");
  }
  s.destroy();
}

// 4: First-file headless add + optional BB model listing + review + save
{
  const s = await openSession(cfg.allFirstOpen);
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-add-open"]'));
  s.doc.querySelector('[data-focus="loop-route-add-open"]').click();
  await s.settle(() => s.doc.querySelector("#loop-route-account"));
  assert(!fs.existsSync(cfg.modelsLog), "models lookup must not run before button click");
  const addDetails = s.doc.querySelector(".loop-route-panel details.loop-route-details");
  assert(addDetails && !addDetails.open, "Add form caveats disclosure must start collapsed");
  assert(addDetails.textContent.includes("How headless routes and limit labels work"));

  const acctSel = s.doc.querySelector("#loop-route-account");
  acctSel.value = cfg.codexID;
  acctSel.dispatchEvent(new s.window.Event("change", { bubbles: true }));
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-load-models"]') && !s.doc.querySelector('[data-focus="loop-route-load-models"]').disabled);

  s.doc.querySelector('[data-focus="loop-route-load-models"]').click();
  await s.settle(() => s.doc.querySelector("select#loop-route-model"));
  assert.equal(fs.readFileSync(cfg.modelsLog, "utf8").trim(), "provider models codex --json");

  // Toggle between loaded BB model list and manual entry
  s.doc.querySelector('[data-focus="loop-route-model-manual"]').click();
  await s.settle(() => s.doc.querySelector("input#loop-route-model"));
  s.doc.querySelector('[data-focus="loop-route-model-manual"]').click();
  await s.settle(() => s.doc.querySelector("select#loop-route-model"));

  const modSel = s.doc.querySelector("select#loop-route-model");
  assert(Array.from(modSel.options).some(o => o.value === "gpt-5.2-codex"));
  modSel.value = "gpt-5.2-codex";
  modSel.dispatchEvent(new s.window.Event("change", { bubbles: true }));

  const limSel = s.doc.querySelector("#loop-route-limit-choice");
  if (limSel) {
    limSel.value = "new";
    limSel.dispatchEvent(new s.window.Event("change", { bubbles: true }));
    await s.settle(() => s.doc.querySelector("#loop-route-limit"));
  }
  const limInput = s.doc.querySelector("#loop-route-limit");
  limInput.value = "pool-added-headless";
  limInput.dispatchEvent(new s.window.Event("input", { bubbles: true }));

  s.doc.querySelector(".loop-route-form").dispatchEvent(new s.window.Event("submit", { bubbles: true, cancelable: true }));
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-save"]'));
  const reviewEl = s.doc.querySelector(".loop-route-review");
  assert(reviewEl.textContent.includes("Alt Codex"));
  const effectMain = reviewEl.querySelector(".loop-route-effect-main");
  assert(effectMain && effectMain.textContent.includes("New route:") && effectMain.textContent.includes("(Choice "), "Review must show chosen route and order first");
  assert(reviewEl.textContent.includes("Before:") && reviewEl.textContent.includes("After:"));
  const revDetails = reviewEl.querySelector("details.loop-route-details");
  assert(revDetails && !revDetails.open, "Review policy disclosure must start collapsed");
  assert(revDetails.textContent.includes("Policy and verification details"));
  assert(effectMain.compareDocumentPosition(revDetails) & s.window.Node.DOCUMENT_POSITION_FOLLOWING, "Review effect summary must precede collapsed details");

  s.doc.querySelector('[data-focus="loop-route-save"]').click();
  await s.settle(() => s.doc.querySelector(".loop-route-result.is-changed"));
  assert(s.doc.querySelector(".loop-route-next").textContent.includes("looptender fallbacks check --id route-"));
  s.destroy();
}

// 5: Manage order (current route Turn off protected, Move earlier saved, stale review_again caught)
{
  const s = await openSession(cfg.allOwnOpen);
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-manage-open"]'));
  s.doc.querySelector('[data-focus="loop-route-manage-open"]').click();
  await s.settle(() => s.doc.querySelector(".loop-route-order"));

  const disabledOff = s.doc.querySelector('[data-focus="loop-route-off-disabled-0"]');
  assert(disabledOff && disabledOff.disabled, "current standby route must not allow Turn off");

  s.doc.querySelector('[data-focus="loop-route-move-1-1"]').click();
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-save"]'));
  s.doc.querySelector('[data-focus="loop-route-save"]').click();
  await s.settle(() => s.doc.querySelector(".loop-route-result.is-changed") && s.doc.querySelector(".loop-route-order"));

  // Turn off then Turn on non-current standby route (now at idx 0: standby-codex)
  s.doc.querySelector('[data-focus="loop-route-off-0-0"]').click();
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-save"]'));
  s.doc.querySelector('[data-focus="loop-route-save"]').click();
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-on-0-0"]'));
  s.doc.querySelector('[data-focus="loop-route-on-0-0"]').click();
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-save"]'));
  s.doc.querySelector('[data-focus="loop-route-save"]').click();
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-off-0-0"]'));

  // Prepare Remove and mutate fallbacks.yaml on disk before Save to verify review_again
  s.doc.querySelector('[data-focus^="loop-route-remove-"]').click();
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-save"]'));
  fs.appendFileSync(cfg.ownFbPath, "\n# external modification after review\n");
  s.doc.querySelector('[data-focus="loop-route-save"]').click();
  await s.settle(() => s.doc.querySelector(".loop-route-result.is-review_again"));
  assert(s.doc.querySelector('[data-focus="loop-route-review-again"]'), "review_again must offer refresh button");
  s.destroy();
}

// 6: Standard-list loop "Use own list..." conversion
{
  const s = await openSession(cfg.allStdOpen);
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-own-open"]'));
  s.doc.querySelector('[data-focus="loop-route-own-open"]').click();
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-own-prepare"]'));
  s.doc.querySelector('[data-focus="loop-route-own-prepare"]').click();
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-save"]'));
  s.doc.querySelector('[data-focus="loop-route-save"]').click();
  await s.settle(() => s.doc.querySelector(".loop-route-result.is-changed"));
  await s.settle(() => fs.existsSync(cfg.stdFbPath));
  s.destroy();
}

// 7: Catalog with only externally managed accounts shows empty-state link instead of broken form
{
  const s = await openSession(cfg.extFirstOpen);
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-add-open"]'));
  s.doc.querySelector('[data-focus="loop-route-add-open"]').click();
  await s.settle(() => s.doc.querySelector(".loop-route-panel .empty"));
  assert(s.doc.querySelector('.loop-route-panel a[href="#/accounts"]'));
  assert(!s.doc.querySelector("#loop-route-account"));
  s.destroy();
}

async function fillAddForm(s, acctId, modelVal, limitVal) {
  const acctSel = s.doc.querySelector("#loop-route-account");
  acctSel.value = acctId;
  acctSel.dispatchEvent(new s.window.Event("change", { bubbles: true }));
  await s.settle(() => s.doc.querySelector("#loop-route-model"));
  const modInput = s.doc.querySelector("#loop-route-model");
  modInput.value = modelVal;
  modInput.dispatchEvent(new s.window.Event("input", { bubbles: true }));
  const limChoice = s.doc.querySelector("#loop-route-limit-choice");
  if (limChoice) {
    limChoice.value = "new";
    limChoice.dispatchEvent(new s.window.Event("change", { bubbles: true }));
    await s.settle(() => s.doc.querySelector("#loop-route-limit"));
  }
  const limInput = s.doc.querySelector("#loop-route-limit");
  limInput.value = limitVal;
  limInput.dispatchEvent(new s.window.Event("input", { bubbles: true }));
}

// 8: P2 stale prepare across navigate-away/back same loop + P1 unknown warning stays outside <details>
{
  const s = await openSession(cfg.allStaleOpen);
  const origFetch = s.window.fetch.bind(s.window);
  let holdPrepare = true;
  let prepStarted = false;
  let releasePrepare;
  const prepGate = new Promise(r => { releasePrepare = r; });
  let injectWarning = false;
  s.window.fetch = async (url, init = {}) => {
    if (String(url).includes("api/v1/routes/prepare") && holdPrepare) {
      prepStarted = true;
      const realRes = await origFetch(url, init);
      await prepGate;
      return realRes;
    }
    const res = await origFetch(url, init);
    if (String(url).includes("api/v1/routes/prepare") && injectWarning) {
      const data = await res.json();
      if (data && Array.isArray(data.lines)) {
        data.lines.push("Account capacity warning: status unknown until verified.");
      }
      return new Response(JSON.stringify(data), { status: res.status, headers: { "Content-Type": "application/json" } });
    }
    return res;
  };

  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-add-open"]'));
  s.doc.querySelector('[data-focus="loop-route-add-open"]').click();
  await s.settle(() => s.doc.querySelector("#loop-route-account"));
  await fillAddForm(s, cfg.codexID, "gpt-5.1-codex", "pool-stale-check");
  s.doc.querySelector(".loop-route-form").dispatchEvent(new s.window.Event("submit", { bubbles: true, cancelable: true }));
  await s.settle(() => prepStarted);

  // Navigate away to overview, return to the SAME loop, and open a fresh Add form before prepare resolves.
  s.window.location.hash = "#/";
  s.window.dispatchEvent(new s.window.HashChangeEvent("hashchange"));
  await s.settle(() => !s.doc.querySelector(".loop-route-panel"));
  s.window.location.hash = "#/loop/" + cfg.edgeLoopKey;
  s.window.dispatchEvent(new s.window.HashChangeEvent("hashchange"));
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-add-open"]'));
  holdPrepare = false;
  s.doc.querySelector('[data-focus="loop-route-add-open"]').click();
  await s.settle(() => s.doc.querySelector("#loop-route-account"));

  releasePrepare();
  await new Promise(r => setTimeout(r, 80));
  assert(!s.doc.querySelector(".loop-route-review"), "superseded prepare must not attach review after navigate-away/back same loop");
  assert(!s.doc.querySelector('[data-focus="loop-route-save"]'), "superseded prepare must not attach Save action");
  assert(s.doc.querySelector("#loop-route-account"), "fresh Add form must stay open");
  assert(!s.doc.querySelector('[data-focus="loop-route-review"]').disabled, "fresh Add form must not remain stuck busy");

  // Verify unknown warning lines on the new review stay outside collapsed <details>.
  injectWarning = true;
  await fillAddForm(s, cfg.codexID, "gpt-5.1-codex", "pool-warn-check");
  s.doc.querySelector(".loop-route-form").dispatchEvent(new s.window.Event("submit", { bubbles: true, cancelable: true }));
  await s.settle(() => s.doc.querySelector(".loop-route-review"));
  const warnText = "Account capacity warning: status unknown until verified.";
  const revDetails = s.doc.querySelector(".loop-route-review details.loop-route-details");
  assert(s.doc.querySelector(".loop-route-review").textContent.includes(warnText), "unknown warning line must be visible on review");
  assert(!revDetails.textContent.includes(warnText), "unknown warning line must not be folded into collapsed details");
  s.destroy();
}

// 9: P2 delayed Codex models reply must not attach after switching to Claude
{
  const s = await openSession(cfg.allModelsOpen);
  const origFetch = s.window.fetch.bind(s.window);
  let codexStarted = false;
  let modelsCalls = 0;
  let releaseCodexModels;
  const codexModelsGate = new Promise(r => { releaseCodexModels = r; });
  s.window.fetch = async (url, init = {}) => {
    if (String(url).includes("api/v1/routes/models") && String((init && init.body) || "").includes('"codex"')) {
      codexStarted = true;
      modelsCalls++;
      const realRes = await origFetch(url, init);
      await codexModelsGate;
      return realRes;
    }
    return origFetch(url, init);
  };

  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-add-open"]'));
  s.doc.querySelector('[data-focus="loop-route-add-open"]').click();
  await s.settle(() => s.doc.querySelector("#loop-route-account"));

  const acctSel = s.doc.querySelector("#loop-route-account");
  acctSel.value = cfg.codexID;
  acctSel.dispatchEvent(new s.window.Event("change", { bubbles: true }));
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-load-models"]') && !s.doc.querySelector('[data-focus="loop-route-load-models"]').disabled);

  s.doc.querySelector('[data-focus="loop-route-load-models"]').click();
  await s.settle(() => codexStarted);

  // Switch selected account to Claude while Codex model lookup is still in flight.
  acctSel.value = cfg.claudeID;
  acctSel.dispatchEvent(new s.window.Event("change", { bubbles: true }));
  await s.settle(() => s.doc.querySelector("#loop-route-account").value === cfg.claudeID);

  releaseCodexModels();
  await new Promise(r => setTimeout(r, 80));

  assert.equal(modelsCalls, 1, "expected exactly 1 codex models lookup in flight");
  assert(!s.doc.querySelector("select#loop-route-model"), "delayed Codex models must not replace Claude model input with select");
  assert(!s.doc.querySelector(".loop-route-panel").textContent.includes("gpt-5.2-codex"), "delayed Codex model id must not appear under Claude");
  assert(!s.doc.querySelector('[data-focus="loop-route-load-models"]').disabled, "load models button under Claude must stay enabled");
  s.destroy();
}

// 10: P2 partial outcome preserved across failed reconciliation read
{
  const s = await openSession(cfg.allPartialOpen);
  const origFetch = s.window.fetch.bind(s.window);
  let readMode = "normal";
  let confirmCalls = 0;
  let readCalls = 0;
  s.window.fetch = async (url, init = {}) => {
    if (String(url).includes("api/v1/routes/confirm")) {
      confirmCalls++;
      return new Response(JSON.stringify({ schemaVersion: 1, outcome: "partial", text: "Simulated partial route save" }), {
        status: 200,
        headers: { "Content-Type": "application/json" }
      });
    }
    if (String(url).includes("api/v1/routes/read")) {
      readCalls++;
      if (readMode === "network_fail") {
        throw new Error("simulated read failure after partial");
      }
    }
    return origFetch(url, init);
  };

  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-add-open"]'));
  s.doc.querySelector('[data-focus="loop-route-add-open"]').click();
  await s.settle(() => s.doc.querySelector("#loop-route-account"));
  await fillAddForm(s, cfg.codexID, "gpt-5.1-codex", "pool-partial-check");
  s.doc.querySelector(".loop-route-form").dispatchEvent(new s.window.Event("submit", { bubbles: true, cancelable: true }));
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-save"]'));

  s.doc.querySelector('[data-focus="loop-route-save"]').click();
  await s.settle(() => s.doc.querySelector(".loop-route-result.is-partial"));

  readMode = "network_fail";
  const prevRead = readCalls;
  s.doc.querySelector('[data-focus="loop-route-read-again"]').click();
  await s.settle(() => {
    const h = s.doc.querySelector("#loop-route-feedback h4");
    return readCalls > prevRead && h && h.textContent !== "Working...";
  });
  assert(s.doc.querySelector(".loop-route-result.is-partial"), "failed read must preserve partial card");
  assert(s.doc.querySelector(".loop-route-reconcile-err"), "failed read must show reconcile error");
  assert(!s.doc.body.textContent.includes("Nothing was changed"), "failed read after partial must never claim Nothing was changed");
  assert.equal(confirmCalls, 1, "confirm must not replay");
  s.destroy();
}

// 11: P2 lost confirm reply preserves unknown across failed and refused reads, never replays confirm, reconciles on authoritative read
{
  const s = await openSession(cfg.allUnknownOpen);
  const origFetch = s.window.fetch.bind(s.window);
  let readMode = "normal";
  let confirmCalls = 0;
  let readCalls = 0;
  s.window.fetch = async (url, init = {}) => {
    if (String(url).includes("api/v1/routes/confirm")) {
      confirmCalls++;
      await origFetch(url, init);
      throw new Error("simulated dropped confirm reply");
    }
    if (String(url).includes("api/v1/routes/read")) {
      readCalls++;
      if (readMode === "network_fail") {
        throw new Error("simulated read failure");
      }
      if (readMode === "refused") {
        return new Response(JSON.stringify({ schemaVersion: 1, outcome: "refused", text: "Simulated read refusal" }), {
          status: 200,
          headers: { "Content-Type": "application/json" }
        });
      }
    }
    return origFetch(url, init);
  };

  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-add-open"]'));
  s.doc.querySelector('[data-focus="loop-route-add-open"]').click();
  await s.settle(() => s.doc.querySelector("#loop-route-account"));
  await fillAddForm(s, cfg.codexID, "gpt-5.1-codex", "pool-unknown-check");
  s.doc.querySelector(".loop-route-form").dispatchEvent(new s.window.Event("submit", { bubbles: true, cancelable: true }));
  await s.settle(() => s.doc.querySelector('[data-focus="loop-route-save"]'));

  s.doc.querySelector('[data-focus="loop-route-save"]').click();
  await s.settle(() => s.doc.querySelector(".loop-route-result.is-unknown"));
  assert(!s.doc.body.textContent.includes("Nothing was changed"));

  readMode = "network_fail";
  const prevRead1 = readCalls;
  s.doc.querySelector('[data-focus="loop-route-read-again"]').click();
  await s.settle(() => {
    const h = s.doc.querySelector("#loop-route-feedback h4");
    return readCalls > prevRead1 && h && h.textContent !== "Working...";
  });
  assert(s.doc.querySelector(".loop-route-result.is-unknown"), "failed read must keep unknown save state");
  assert(s.doc.querySelector(".loop-route-reconcile-err"), "failed read must show reconcile error");
  assert(!s.doc.body.textContent.includes("Nothing was changed"), "failed read must not claim Nothing was changed");
  assert.equal(confirmCalls, 1, "confirm must not replay after failed read");

  readMode = "refused";
  const prevRead2 = readCalls;
  s.doc.querySelector('[data-focus="loop-route-read-again"]').click();
  await s.settle(() => {
    const h = s.doc.querySelector("#loop-route-feedback h4");
    return readCalls > prevRead2 && h && h.textContent !== "Working...";
  });
  assert(s.doc.querySelector(".loop-route-result.is-unknown"), "refused read must keep unknown save state");
  assert(s.doc.querySelector(".loop-route-reconcile-err"), "refused read must show reconcile error");
  assert(!s.doc.body.textContent.includes("Nothing was changed"), "refused read must not claim Nothing was changed");
  assert.equal(confirmCalls, 1, "confirm must not replay after refused read");

  readMode = "normal";
  s.doc.querySelector('[data-focus="loop-route-read-again"]').click();
  await s.settle(() => s.doc.querySelector(".loop-route-result.is-reconciled"));
  assert(!s.doc.querySelector(".loop-route-result.is-unknown"), "authoritative read clears unknown state");
  assert(s.doc.querySelector(".loop-route-result.is-reconciled").textContent.includes("Current list loaded"));
  assert(s.doc.body.textContent.includes("gpt-5.1-codex"), "authoritative read refreshes route list");
  assert.equal(confirmCalls, 1, "confirm must never have replayed");
  s.destroy();
}

console.log("LOOP_ROUTES_JSDOM_OK");
`
	scriptPath := filepath.Join(root, "run.mjs")
	cfgPath := filepath.Join(root, "cfg.json")
	write(t, scriptPath, script, 0o600)
	cfgBytes, _ := json.Marshal(map[string]any{
		"roOpen":         roURLs[0],
		"ctrlOpen":       ctrlURLs[0],
		"bbOpen":         bbURLs[0],
		"allFirstOpen":   allURLs[0],
		"allOwnOpen":     allURLs[1],
		"allStdOpen":     allURLs[2],
		"allStaleOpen":   allURLs[3],
		"allModelsOpen":  allURLs[4],
		"allPartialOpen": allURLs[5],
		"allUnknownOpen": allURLs[6],
		"extFirstOpen":   extURLs[0],
		"firstLoopKey":   dockKey(firstEntry.ID),
		"edgeLoopKey":    dockKey(edgeEntry.ID),
		"claudeID":       claudeID,
		"codexID":        codexID,
		"modelsLog":      modelsLog,
		"ownFbPath":      ownFbPath,
		"stdFbPath":      filepath.Join(stdDir, "fallbacks.yaml"),
	})
	write(t, cfgPath, string(cfgBytes), 0o600)

	cmd := exec.Command(node, scriptPath)
	cmd.Env = append(os.Environ(), "DOCK_JSDOM_DIR="+absJSDOM, "LOOP_UI_CFG="+cfgPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("jsdom wiring run failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "LOOP_ROUTES_JSDOM_OK") {
		t.Fatalf("missing ok marker: %s", out)
	}

	// Verify disk persistence invariants on first-loop after panel headless add:
	firstAfter := loadFixture(t, firstCfgPath)
	if firstAfter.FallbackSet == nil || len(firstAfter.FallbackSet.Candidates) != 1 {
		t.Fatalf("expected 1 added candidate in first-loop, got %+v", firstAfter.FallbackSet)
	}
	added := firstAfter.FallbackSet.Candidates[0]
	if added.Provider != "codex" || added.Model() != "gpt-5.2-codex" || added.Group != "pool-added-headless" || added.Account != codexID {
		t.Fatalf("unexpected added candidate: %+v", added)
	}
	if !firstAfter.FallbackSet.Probe.Disabled {
		t.Fatal("first panel-created fallbacks file must keep probe.scheduled_disabled=true")
	}
	if len(firstAfter.Loop.Errors.FailoverOn) != 0 {
		t.Fatalf("panel add must never enable automatic handoff, got %v", firstAfter.Loop.Errors.FailoverOn)
	}

	// Verify Move earlier persisted on own-loop (standby-codex moved before standby-claude), and stale Remove did not remove anything.
	ownAfter := loadFixture(t, ownCfgPath)
	if ids := candidateIDs(ownAfter.FallbackSet); len(ids) != 2 || ids[0] != "standby-codex" || ids[1] != "standby-claude" {
		t.Fatalf("expected moved order [standby-codex standby-claude], got %v", ids)
	}

	// Verify edge-loop persisted exactly once (lost confirm never replayed, stale prepare never wrote).
	edgeAfter := loadFixture(t, edgeCfgPath)
	if edgeAfter.FallbackSet == nil || len(edgeAfter.FallbackSet.Candidates) != 1 {
		t.Fatalf("expected exactly 1 candidate in edge-loop (no confirm replay), got %+v", edgeAfter.FallbackSet)
	}
	if edgeAfter.FallbackSet.Candidates[0].Group != "pool-unknown-check" {
		t.Fatalf("expected group pool-unknown-check in edge-loop, got %+v", edgeAfter.FallbackSet.Candidates[0].Group)
	}
}
