//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
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
)

func TestDockAccountsClarityStaticInvariants(t *testing.T) {
	js := dockUIText(t, "dock.js")
	css := dockUIText(t, "dock.css")
	help := dockUIText(t, "routes-help.html")

	if strings.Contains(help, "\u2014") {
		t.Error("routes-help.html contains an em dash")
	}
	for _, want := range []string{"Open Management Panel", "Manage Account in Panel", "--allow-changes"} {
		if !strings.Contains(help, want) {
			t.Errorf("routes-help.html missing %q", want)
		}
	}

	newHelpers := []string{
		"panelAccountMode",
		"accountProviderLabel",
		"accountConnectUnsupportedNote",
		"openAccountMgmtPanel",
		"closeAccountMgmtPanel",
		"renderAccountMgmtPanel",
		"failedCheckLine",
		"accountQuotaBox",
	}
	for _, fn := range newHelpers {
		body := funcBody(t, js, fn)
		if strings.Contains(body, "\u2014") {
			t.Errorf("%s contains an em dash", fn)
		}
		if regexp.MustCompile(`\b(?:node|nodes)\b`).MatchString(body) {
			t.Errorf("%s uses forbidden word node/nodes", fn)
		}
		if strings.Contains(strings.ToLower(body), "panel server") {
			t.Errorf("%s uses forbidden phrase panel server", fn)
		}
	}

	accountsBody := funcBody(t, js, "renderAccounts")
	if regexp.MustCompile(`\b(?:node|nodes)\b`).MatchString(accountsBody) {
		t.Error("renderAccounts uses forbidden word node/nodes")
	}
	if strings.Contains(strings.ToLower(accountsBody), "panel server") {
		t.Error("renderAccounts uses forbidden phrase panel server")
	}
	for _, tok := range []string{
		"acct-mode-bar",
		"acct-help-fold",
		"acct-details",
		"acct-cli-fold",
		"How to reconnect",
		"How to review",
		"Open Full mode",
	} {
		if !strings.Contains(accountsBody, tok) && !strings.Contains(js, tok) {
			t.Errorf("dock.js missing Accounts clarity token %q", tok)
		}
	}

	for _, sel := range []string{".acct-mode-bar", ".acct-mgmt-panel", ".acct-help-fold", ".acct-details"} {
		if !strings.Contains(css, sel) {
			t.Errorf("dock.css missing %q", sel)
		}
	}
	fcIdx := strings.LastIndex(css, "@media (forced-colors: active)")
	if fcIdx < 0 || !strings.Contains(css[fcIdx:], ".acct-mgmt-panel") || !strings.Contains(css[fcIdx:], ".acct-mode-bar") {
		t.Error("dock.css missing forced-colors rules for Accounts mode bar and management panel")
	}
}

func TestDockAccountsClarityProductionWiring(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("DOCK_PAGE_REQUIRED") != "" {
			t.Fatal("node is required and not available")
		}
		t.Skip("node unavailable: Accounts clarity wiring test skipped")
	}
	jsdom := os.Getenv("DOCK_JSDOM_DIR")
	if jsdom == "" {
		jsdom = filepath.Join("..", "..", "integrations", "bb-plugin-keepgoing", "node_modules", "jsdom")
	}
	if info, err := os.Stat(jsdom); err != nil || !info.IsDir() {
		if os.Getenv("DOCK_PAGE_REQUIRED") != "" {
			t.Fatalf("jsdom is required and not found at %s", jsdom)
		}
		t.Skip("jsdom unavailable: Accounts clarity wiring test skipped")
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

	binDir := filepath.Join(root, "bin")
	_ = os.MkdirAll(binDir, 0o700)
	unexpectedTrap := filepath.Join(root, "unexpected-trap")
	t.Setenv("TEST_UNEXPECTED_TRAP", unexpectedTrap)
	for _, name := range []string{"claude", "codex", "agy"} {
		write(t, filepath.Join(binDir, name), "#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"$TEST_UNEXPECTED_TRAP\"\nexit 99\n", 0o700)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		if b, readErr := os.ReadFile(unexpectedTrap); !os.IsNotExist(readErr) {
			t.Errorf("unexpected provider call: %q (%v)", b, readErr)
		}
	})

	project := filepath.Join(root, "project")
	_ = os.MkdirAll(project, 0o700)
	acctDir := filepath.Join(root, "codex-explicit")
	_ = os.MkdirAll(acctDir, 0o700)

	catDir := filepath.Join(root, "catalog")
	_ = os.MkdirAll(catDir, 0o700)
	oldPanelCat := panelAccountsDir
	panelAccountsDir = catDir
	t.Cleanup(func() { panelAccountsDir = oldPanelCat })

	var claudeID, codexID string
	if err := accounts.Update(catDir, func(all *accounts.File) error {
		c1 := accounts.NewEntry(*all, accounts.Entry{
			Label:    "Studio Claude",
			Provider: "claude-code",
			Mode:     "inherited",
			CLI:      filepath.Join(binDir, "claude"),
			Workdir:  project,
		}, "", time.Now().UTC(), os.Environ())
		claudeID = c1.ID
		c2 := accounts.NewEntry(*all, accounts.Entry{
			Label:     "Core Codex",
			Provider:  "codex",
			Mode:      "explicit_directory",
			CLI:       filepath.Join(binDir, "codex"),
			Workdir:   project,
			Directory: acctDir,
		}, "", time.Now().UTC(), os.Environ())
		codexID = c2.ID
		all.Entries = append(all.Entries, c1, c2)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	regDir := filepath.Join(root, "registry")
	loopDir := filepath.Join(root, "loops", "alpha")
	_ = os.MkdirAll(regDir, 0o700)
	_ = os.MkdirAll(loopDir, 0o700)
	cfgPath := filepath.Join(loopDir, config.FileName)
	write(t, cfgPath, `version: 1
loop:
  name: fictional-alpha
  state_dir: state
  adapter: codex
  provider: codex
  group: pool-alpha
  codex:
    cli: `+filepath.Join(binDir, "codex")+`
    workdir: `+project+`
    model: gpt-5.2-codex
  prompt: Continue fictional loop.
  errors:
    failover_on: []
`, 0o600)
	cfgLoaded := loadFixture(t, cfgPath)
	canonState, _ := canonicalFuturePath(cfgLoaded.Loop.StateDir)
	_ = control.Save(cfgLoaded.Loop.StateDir, control.File{Enabled: true, Paused: true})
	if _, err := registry.Add(regDir, cfgLoaded.Loop.Name, cfgPath, canonState); err != nil {
		t.Fatal(err)
	}

	startPanelMode := func(modeName string, changes, controls bool) string {
		pdir, err := panelDirectory(filepath.Join(panelShortDir(t), "p-"+modeName))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan int, 1)
		go func() {
			done <- runPanelServer(ctx, panelServerConfig{
				dir:        pdir,
				registry:   regDir,
				idle:       time.Hour,
				refresh:    dockDefaultRefresh,
				staleAfter: defaultStaleAfter,
				offline:    true,
				changes:    changes,
				controls:   controls && !changes,
				now:        time.Now,
			})
		}()
		t.Cleanup(func() {
			cancel()
			<-done
		})
		var rec panelRecord
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if r, ok := panelLive(context.Background(), pdir); ok {
				rec = r
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if rec.Port == 0 {
			t.Fatalf("panel %s did not start", modeName)
		}
		openURL, err := panelRequestOpening(context.Background(), pdir, rec, "", false)
		if err != nil {
			t.Fatal(err)
		}
		return openURL
	}

	ctrlOpen := startPanelMode("controls", false, true)
	roOpen := startPanelMode("readonly", false, false)

	script := `
const assert = require("node:assert/strict");
const fs = require("node:fs");
const { JSDOM } = require(process.env.DOCK_JSDOM_DIR);
const cfg = JSON.parse(fs.readFileSync(process.env.CLARITY_CFG, "utf8"));

async function openSession(openUrl) {
  const res1 = await fetch(openUrl, { redirect: "manual" });
  await res1.text();
  const setCookie = res1.headers.get("set-cookie") || "";
  const cookiePair = setCookie.split(";")[0];
  const pathMatch = setCookie.match(/Path=([^;]+)/i);
  assert(cookiePair.startsWith("keepgoing_panel=") && pathMatch, "missing panel session cookie");
  const u = new URL(openUrl);
  const sBase = u.origin + pathMatch[1];
  const html = await (await fetch(sBase, { headers: { Cookie: cookiePair } })).text();
  const js = await (await fetch(sBase + "dock.js", { headers: { Cookie: cookiePair } })).text();
  const helpHtml = await (await fetch(sBase + "routes-help.html", { headers: { Cookie: cookiePair } })).text();
  return { origin: u.origin, sBase, cookiePair, html, js, helpHtml };
}

function makeFakeCards() {
  return [
    {
      id: cfg.claudeID, label: "Studio Claude", provider: "claude-code", selection: "inherited",
      auth: { text: "Selected CLI reports no login", state: "reported_absent", freshness: "fresh", observedAt: "2030-03-04T11:50:00Z", source: "claude status" },
      plan: { text: "Unavailable", state: "unavailable", freshness: "unavailable" },
      configuredModel: { missing: "not_recorded" }, observedModel: { missing: "not_recorded" },
      routes: "none", needsPerson: true, attention: "no_login",
      usageUpdating: { state: "off", text: "Keep usage updated: off" }
    },
    {
      id: cfg.codexID, label: "Core Codex", provider: "codex", selection: "explicit_directory",
      localAuth: "file_expired",
      auth: { text: "Not checked", state: "unchecked", freshness: "unavailable" },
      plan: { text: "Not checked", state: "unavailable", freshness: "unavailable", missing: "not_checked" },
      configuredModel: { missing: "not_recorded" }, observedModel: { missing: "not_recorded" },
      routes: "none",
      usageUpdating: { state: "off", text: "Keep usage updated: off" }
    },
    {
      id: "44444444444444444444444444444444", label: "Reserve Codex", provider: "codex", selection: "explicit_directory",
      auth: { text: "Unavailable", state: "unavailable", freshness: "unavailable" },
      plan: { text: "Unavailable", state: "unavailable", freshness: "unavailable" },
      configuredModel: { missing: "not_recorded" }, observedModel: { missing: "not_recorded" },
      routes: "none", attention: "context_changed", reason: "context_changed", failedCheckAt: "2030-03-04T11:45:00Z", needsPerson: true,
      quota: { state: "unavailable", reason: "selection_review_required" },
      usageUpdating: { state: "paused", text: "Updates paused \u00B7 selection changed" }
    },
    {
      id: "66666666666666666666666666666666", label: "Malformed Codex", provider: "codex", selection: "explicit_directory",
      auth: { text: "Selected CLI reports login", state: "reported_present", freshness: "fresh" },
      plan: { text: "Pro", state: "reported", value: "pro", freshness: "fresh" },
      configuredModel: { missing: "not_recorded" }, observedModel: { missing: "not_recorded" },
      routes: "none",
      quota: { state: "reported", summary: { state: "reported", source: "codex account/rateLimits/read", observedAt: "2030-03-04T11:52:00Z", freshness: "fresh", provenance: { version: 2, retrievedAt: "2030-03-04T11:52:00Z", providerObservedAt: null }, rows: [{ name: "5-hour", text: "91% used", usedPercent: 91, remainingPercent: 9 }] } }
    }
  ];
}

const settle = (ms = 40) => new Promise((r) => setTimeout(r, ms));

(async () => {
  // 1. Real Controls-mode HTTP panelHandler + modified card states (sign-in needed & drift)
  const ctrl = await openSession(cfg.ctrlOpen);
  {
    const posts = [];
    const dom = new JSDOM(ctrl.html, { url: ctrl.sBase + "#/accounts", runScripts: "outside-only", pretendToBeVisual: true });
    try {
    const w = dom.window;
    w.TextEncoder = TextEncoder; w.scrollTo = () => {};
    w.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
    w.Element.prototype.scrollIntoView = () => {};
    const cards = makeFakeCards();
    w.fetch = async (url, init = {}) => {
      const rel = String(url).split("?")[0];
      if (init.method === "POST") { posts.push(rel); return new Response("{}", { status: 403 }); }
      const target = new URL(String(url), ctrl.sBase).toString();
      const headers = Object.assign({}, init.headers || {}, {
        Cookie: ctrl.cookiePair, Origin: ctrl.origin, "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"
      });
      if (rel === "api/v1/saved-accounts") {
        return new Response(JSON.stringify({ schemaVersion: 1, reference: "ref_ctrl_1", accounts: { state: "available", complete: true, cards } }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      if (rel === "api/v1/dock") {
        const realDock = await (await fetch(target, { headers })).json();
        realDock.accountsReference = "ref_ctrl_1";
        realDock.selectedAccounts = cards.length;
        realDock.accounts = { state: "available", complete: true, cards: [] };
        return new Response(JSON.stringify(realDock), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      return fetch(target, { method: init.method || "GET", headers });
    };
    assert(ctrl.helpHtml.includes("Open Management Panel"), "routes-help missing macOS menu entry guidance");
    w.eval(ctrl.js);
    await settle(80);
    // Real panelHandler server config returned changes="on" and no Full-mode flags => Controls mode.
    const modeBar = w.document.querySelector(".acct-mode-bar");
    assert(modeBar && modeBar.textContent.includes("Controls mode"), "missing Controls mode bar");
    assert.equal(w.document.querySelector(".acct-help-fold")?.open, false, "top help fold must start collapsed");
    const btns = [...w.document.querySelectorAll("#pane button")].map((b) => b.textContent);
    assert(!btns.some((t) => /^Connect (Claude|Codex)$/.test(t)), "dead-end Connect button rendered in Controls mode: " + JSON.stringify(btns));
    const helpBtn = w.document.querySelector('[data-focus="account-connect-help-' + cfg.claudeID + '"]');
    assert(helpBtn && helpBtn.textContent.includes("How to reconnect"), "Studio Claude card missing How to reconnect… button");
    helpBtn.click();
    await settle();
    const mgmt = w.document.querySelector(".acct-mgmt-panel");
    assert(mgmt, "clicking How to reconnect… did not open management guidance panel");
    assert(mgmt.textContent.includes("Manage Account in Panel"), "mgmt panel missing macOS Manage Account in Panel item");
    assert(mgmt.textContent.includes("looptender panel stop"), "mgmt panel missing panel stop command");
    assert(mgmt.textContent.includes("--account CONNECTION_ID"), "mgmt panel leaked raw ID when Identifiers off");
    // Toggle Identifiers ON -> fills real fictional ID
    w.document.getElementById("identifiers").click();
    await settle(60);
    assert(w.document.querySelector(".acct-mgmt-panel").textContent.includes("--account " + cfg.claudeID), "turning Identifiers on did not fill in ID");
    assert.equal(posts.length, 0, "Controls mode guidance click must issue zero POSTs");
    // Press Escape -> closes panel and restores focus to trigger
    w.document.dispatchEvent(new w.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await settle();
    assert.equal(w.document.querySelector(".acct-mgmt-panel"), null, "Escape did not close mgmt panel");
    assert.equal(w.document.activeElement?.dataset.focus, "account-connect-help-" + cfg.claudeID, "Escape did not restore focus");
    // Reserve Codex keeps context_changed != quota limit & offers How to review…
    const revHelp = w.document.querySelector('[data-focus="account-review-help-44444444444444444444444444444444"]');
    assert(revHelp && revHelp.textContent.includes("How to review"), "context_changed card missing How to review in Controls mode");
    revHelp.click();
    await settle();
    assert(w.document.querySelector(".acct-mgmt-panel").textContent.includes("Open Full mode to review Reserve Codex"), "wrong mgmt heading for review");
    } finally {
      dom.window.close();
    }
  }

  // 2. Real Read-only HTTP panelHandler
  const ro = await openSession(cfg.roOpen);
  {
    const posts = [];
    const dom = new JSDOM(ro.html, { url: ro.sBase + "#/accounts", runScripts: "outside-only", pretendToBeVisual: true });
    try {
    const w = dom.window;
    w.TextEncoder = TextEncoder; w.scrollTo = () => {};
    w.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
    w.Element.prototype.scrollIntoView = () => {};
    const uncheckedCards = [
      {
        id: cfg.claudeID, label: "Unchecked Claude", provider: "claude-code", selection: "inherited",
        auth: { text: "Not checked", state: "unchecked", freshness: "unavailable" },
        plan: { text: "Not checked", state: "unavailable", freshness: "unavailable", missing: "not_checked" },
        configuredModel: { missing: "not_recorded" }, observedModel: { missing: "not_recorded" }, routes: "none"
      },
      ...makeFakeCards().slice(0, 2)
    ];
    w.fetch = async (url, init = {}) => {
      if (init.method === "POST") { posts.push(url); return new Response("{}", { status: 403 }); }
      const rel = String(url).split("?")[0];
      if (rel === "api/v1/saved-accounts") {
        return new Response(JSON.stringify({ schemaVersion: 1, reference: "ref_ro_1", accounts: { state: "available", complete: true, cards: uncheckedCards } }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      const target = new URL(String(url), ro.sBase).toString();
      const headers = Object.assign({}, init.headers || {}, {
        Cookie: ro.cookiePair, Origin: ro.origin, "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"
      });
      if (rel === "api/v1/dock") {
        const realDock = await (await fetch(target, { headers })).json();
        realDock.accountsReference = "ref_ro_1";
        realDock.selectedAccounts = uncheckedCards.length;
        realDock.accounts = { state: "available", complete: true, cards: [] };
        return new Response(JSON.stringify(realDock), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      return fetch(target, { headers });
    };
    w.eval(ro.js);
    await settle(80);
    assert(w.document.querySelector(".acct-mode-bar")?.textContent.includes("Read-only mode"), "missing Read-only mode bar");
    assert.equal(posts.length, 0, "read-only issued POST");
    const firstCard = w.document.querySelector("#pane .account-card");
    const ncCount = (firstCard.textContent.match(/Not checked/g) || []).length;
    assert.equal(ncCount, 1, "Not checked count on unchecked read-only card should stay 1, got " + ncCount);
    assert(![...w.document.querySelectorAll("#pane button")].some((b) => /Check login|Keep usage|Remove from catalog|^Connect /.test(b.textContent)), "read-only exposed write/check buttons");
    } finally {
      dom.window.close();
    }
  }

  // 3. Full mode: Codex supported (Reconnect Codex… -> preview -> lost start preserves attempt -> cancel -> Connect another focus), Claude unsupported, Check accounts busy
  {
    const posts = [];
    const dom = new JSDOM(ctrl.html, { url: ctrl.sBase + "#/accounts", runScripts: "outside-only", pretendToBeVisual: true });
    try {
    const w = dom.window;
    w.TextEncoder = TextEncoder; w.scrollTo = () => {};
    w.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
    w.Element.prototype.scrollIntoView = () => {};
    const cards = makeFakeCards();
    const attempt = "c".repeat(32);
    let resolveStart = null;
    let resolveCheck = null;
    w.fetch = async (url, init = {}) => {
      const rel = String(url).split("?")[0];
      if (init.method === "POST") {
        posts.push(rel);
        if (rel === "api/v1/account-connect/" + cfg.codexID + "/prepare") {
          return new Response(JSON.stringify({
            schemaVersion: 1,
            preview: {
              attemptId: attempt, action: "connect_account", key: cfg.codexID, title: "Reconnect Core Codex?", confirm: "Open Codex sign-in",
              form: "e".repeat(64), expiresAt: "2035-01-01T00:00:00Z",
              lines: [[{ t: "Reconnects only Core Codex." }]], lost: "Lost reply", refused: "Refused"
            }
          }), { status: 200, headers: { "Content-Type": "application/json" } });
        }
        if (rel === "api/v1/account-connect/" + cfg.codexID + "/start") {
          await new Promise((r) => { resolveStart = r; });
          return new Response("not-json", { status: 500 });
        }
        if (rel === "api/v1/account-connect/" + cfg.codexID + "/cancel") {
          return new Response(JSON.stringify({
            schemaVersion: 1,
            connect: { id: attempt, accountId: cfg.codexID, provider: "codex", state: "cancelled", expiresAt: "2035-01-01T00:00:00Z" }
          }), { status: 200, headers: { "Content-Type": "application/json" } });
        }
        if (rel.startsWith("api/v1/account-checks/")) {
          await new Promise((r) => { resolveCheck = r; });
          return new Response(JSON.stringify({ schemaVersion: 1, result: { outcome: "done", text: "Checked" } }), { status: 200, headers: { "Content-Type": "application/json" } });
        }
        return new Response("{}", { status: 404 });
      }
      if (rel === "api/v1/server") {
        return new Response(JSON.stringify({
          schemaVersion: 1, servedBy: "panel", version: "v0.3.0-dev", changes: "on", form: "f".repeat(64),
          routeChanges: "person", accountQuota: true, accountQuotaProviders: ["codex"], accountSelectionReview: true,
          accountUsageUpdating: true, accountCheck: true, accountRemove: true, accountConnectProviders: ["codex"]
        }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      if (rel === "api/v1/saved-accounts") {
        return new Response(JSON.stringify({ schemaVersion: 1, reference: "ref_full_1", accounts: { state: "available", complete: true, cards } }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      if (rel === "api/v1/dock") {
        return new Response(JSON.stringify({ schemaVersion: 1, readAt: "2030-03-04T12:00:00Z", refreshSeconds: 3600, complete: true, registry: { availability: "available" }, loops: [], needsYou: [], selectedAccounts: cards.length, accountsReference: "ref_full_1", accounts: { state: "available", complete: true, cards: [] } }), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      return new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } });
    };
    w.eval(ctrl.js);
    await settle(60);
    if (resolveCheck) {
      const busyCheckAll = w.document.querySelector('[data-focus="account-check-all"]');
      assert(busyCheckAll && busyCheckAll.disabled && busyCheckAll.textContent.includes("Checking accounts"), "Check accounts button did not show busy state");
    }
    while (resolveCheck) { const fn = resolveCheck; resolveCheck = null; fn(); await settle(40); }
    assert(w.document.querySelector(".acct-mode-bar")?.textContent.includes("Full mode"), "missing Full mode bar");
    // Studio Claude (unsupported on non-arm64 here) must NOT tell user to pass --allow-changes and must NOT show Connect Claude
    const claudeCard = [...w.document.querySelectorAll("#pane .account-card")].find((x) => x.textContent.includes("Studio Claude"));
    assert(claudeCard, "missing Studio Claude card");
    assert(claudeCard.textContent.includes("macOS Apple Silicon"), "unsupported Full-mode Claude card missing honest platform note");
    assert(!claudeCard.textContent.includes("--allow-changes"), "unsupported Full-mode Claude card wrongly told user --allow-changes");
    assert.equal(w.document.querySelector('[data-focus="account-connect-' + cfg.claudeID + '"]'), null, "unsupported Full-mode card rendered Connect Claude button");
    // Core Codex (file_expired, supported) renders Reconnect Codex… -> prepare -> start busy -> lost start reply error
    const reconBtn = w.document.querySelector('[data-focus="account-connect-' + cfg.codexID + '"]');
    assert(reconBtn && reconBtn.textContent.includes("Reconnect Codex"), "file_expired Codex card missing Reconnect Codex button");
    reconBtn.click();
    await settle(60);
    assert(w.document.querySelector('[data-focus="connect-start-' + cfg.codexID + '"]'), "connect prepare did not render start preview");
    w.document.querySelector('[data-focus="connect-start-' + cfg.codexID + '"]').click();
    await settle(20);
    const busyBtn = w.document.querySelector('[data-focus="account-connect-' + cfg.codexID + '"]');
    assert(busyBtn && busyBtn.textContent === "Connecting\u2026" && busyBtn.disabled, "summary button did not reflect Connecting… busy state");
    resolveStart();
    await settle(60);
    const errRes = w.document.querySelector('[data-focus="connect-result-' + cfg.codexID + '"]');
    assert(errRes && errRes.textContent.includes("Sign-in may still be running"), "lost start reply missing conservative message");
    // Malformed Codex never leaks 91%
    const malText = w.document.body.textContent;
    assert(!malText.includes("91%"), "malformed provenance leaked 91%");
    assert(malText.includes("Quota provenance rejected"), "missing provenance rejected badge");
    // Technical details start collapsed; active/waiting sign-in disables add-another input until closed, then Connect another opens details and focuses label input
    const codexDet = [...w.document.querySelectorAll("#pane .account-card details.acct-details")][1];
    assert.equal(codexDet.open, false, "secondary .acct-details must start collapsed");
    assert.equal(w.document.querySelector('[data-focus="connect-label-' + cfg.codexID + '"]').disabled, true, "add-another input must stay disabled while sign-in waits");
    w.document.querySelector('[data-focus="connect-cancel-' + cfg.codexID + '"]').click();
    await settle(40);
    w.document.querySelector('[data-focus="connect-close-' + cfg.codexID + '"]').click();
    await settle(40);
    w.document.querySelector('[data-focus="connect-another-codex"]').click();
    await settle(40);
    const reopenedDet = [...w.document.querySelectorAll("#pane .account-card details.acct-details")][1];
    assert.equal(reopenedDet.open, true, "Connect another Codex account did not open secondary details for focus");
    assert.equal(w.document.activeElement?.dataset.focus, "connect-label-" + cfg.codexID, "focus did not move to new label input");
    } finally {
      dom.window.close();
    }
  }
  console.log("ACCOUNTS_CLARITY_WIRING_OK");
  process.exit(0);
})().catch((e) => { console.error(e); process.exit(1); });
`
	cfgBytes, _ := json.Marshal(map[string]string{
		"ctrlOpen": ctrlOpen,
		"roOpen":   roOpen,
		"claudeID": claudeID,
		"codexID":  codexID,
	})
	cfgFile := filepath.Join(root, "clarity-cfg.json")
	scriptFile := filepath.Join(root, "clarity-check.cjs")
	write(t, cfgFile, string(cfgBytes), 0o600)
	write(t, scriptFile, script, 0o600)

	ctxRun, cancelRun := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelRun()
	cmd := exec.CommandContext(ctxRun, node, scriptFile)
	cmd.Env = append(os.Environ(), "DOCK_JSDOM_DIR="+absJSDOM, "CLARITY_CFG="+cfgFile)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	if err := cmd.Run(); err != nil {
		t.Fatalf("Accounts clarity wiring check failed: %v\n%s", err, combined.String())
	}
	t.Log(combined.String())
}
