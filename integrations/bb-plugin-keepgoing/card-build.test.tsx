import { test } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "https://example.com" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
const { cleanup, within } = await import("@testing-library/react");
const { loadPluginApp, renderSlot } = await import("@get-bb/plugin-sdk/testing/app");
const { noteSaysRetired } = await import("./status-details.ts");
const app = await loadPluginApp(() => import("./app.tsx"));
const base = { readAt: "2026-01-01T00:00:00Z", completed: false, targetThread: "thr_example", targetAdapter: "bb", targetConfigured: true, lastCheck: null };
const dets = (over: object = {}, comparison = "match", running = "v0.2.0-dev.aaaaaaa", installed = "v0.2.0-dev.aaaaaaa") => ({ ...base, runtime: { state: "recent", source: "state", freshness: "recent", recordedBuild: running, installedBuild: installed, binaryComparison: comparison }, daemon: { state: "responsive", binaryComparison: comparison }, ...over });
const loop = (over: object = {}, d: object = {}) => ({ loop: "example-loop", configPath: "/private/example/looptender.yaml", controlState: "enabled", note: null, target: "primary", supervising: true, lastState: "ok", lastWakeAt: "0001-01-01T00:00:00Z", checkedAt: null, handoffs: 0, fallbacks: null, referenceSource: "plugin_setting", details: dets(d), ...over });
let n = 0;
const card = (loops: object[]) => renderSlot(app.threadContextCardRows[0]!, { threadId: `thr_build_${++n}`, projectId: "proj_example", environment: null }, { rpc: { forThread: () => ({ loops, errors: [] }) } });

test("an update shows as a chip for 24 hours, with the detail in the expanded card; never as a tooltip alone or a new badge", async () => {
  const at = new Date(Date.now() - 2 * 3600_000).toISOString();
  const ui = card([loop({}, { buildAdoption: { outcome: "adopted", at, from: "v0.2.0-dev.1111111", to: "v0.2.0-dev.aaaaaaa" } })]);
  try {
    assert.ok(await ui.findByText("Updated 2 h ago"));
    const details = within(ui.getByRole("list", { name: "Details" }));
    assert.ok(details.getByText(/^Build: Installed .+ · was v0\.2\.0-dev\.1111111 · now v0\.2\.0-dev\.aaaaaaa$/));
    assert.equal(ui.queryByText(/\bnew\b/i), null); assert.equal(ui.queryByRole("button", { name: /restart|update|resume/i }), null);
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("a pending update and an older build say so in text, the older one with the exact command and no button", async () => {
  const pending = card([loop({}, { followsInstalls: "yes" })].map(l => ({ ...l, details: dets({ followsInstalls: "yes" }, "different", "v0.2.0-dev.1111111", "v0.2.0-dev.aaaaaaa") })));
  try { assert.ok(await pending.findByText("Update pending · switches at next check")); assert.equal(pending.container.querySelector("code"), null); } finally { pending.lifecycle.unmount(); cleanup(); }
  const older = card([loop({}, {})].map(l => ({ ...l, details: dets({ followsInstalls: "no" }, "different", "v0.2.0-dev.1111111", "v0.2.0-dev.aaaaaaa") })));
  try {
    assert.ok(await older.findByText("Older build · restart the supervisor to update"));
    assert.equal(older.container.querySelector("[aria-label=Build] code")?.textContent, 'keepgoing daemon-shutdown -c "/private/example/looptender.yaml"');
    assert.equal(older.queryByRole("button", { name: /restart|update|resume|shutdown/i }), null);
    assert.ok(within(older.getByRole("list", { name: "Details" })).getByText(/It does not follow installs\./));
  } finally { older.lifecycle.unmount(); cleanup(); }
});

test("an up to date loop shows only its version; the chip stays short when builds differ", async () => {
  const cur = card([loop()]);
  try { await cur.findByText("looptender: enabled v0.2.0-dev.aaaaaaa"); assert.equal(cur.container.querySelector("[aria-label=Build]"), null); } finally { cur.lifecycle.unmount(); cleanup(); }
  const diff = card([loop({}, {})].map(l => ({ ...l, details: dets({ followsInstalls: "yes" }, "different", "v0.2.0-dev.1111111", "v0.2.0-dev.aaaaaaa") })));
  try { const chip = await diff.findByText(/^looptender: enabled v0\.2\.0-dev\.1111111 · build differs$/); assert.ok((chip.textContent ?? "").length < 60); } finally { diff.lifecycle.unmount(); cleanup(); }
});

test("the plugin's own build is named apart from the supervisor's when they differ", async () => {
  const ui = card([loop({ pluginBuild: "v0.2.0-dev.bbbbbbbbbbbb" })]);
  try { assert.ok(await within(await ui.findByRole("list", { name: "Details" })).findByText(/^Plugin build: v0\.2\.0-dev\.bbbbbbbbbbbb \(the code drawing this card\); the builds above are each supervisor's/)); } finally { ui.lifecycle.unmount(); cleanup(); }
  const same = card([loop({ pluginBuild: "v0.2.0-dev.aaaaaaaffff" })]);
  try { await same.findByText(/^looptender: enabled/); assert.equal(same.queryByText(/Plugin build:/), null); } finally { same.lifecycle.unmount(); cleanup(); }
});

test("Pause is a real target with the loop in its name, a focus ring, and its details are in the card", async () => {
  const ui = card([loop({ lastState: "error" }, { lastCheck: { at: "2026-01-01T00:00:00Z", target: "primary", action: "none", outcome: "succeeded" } })]);
  try {
    const pause = await ui.findByRole("button", { name: "Pause example-loop" }) as HTMLButtonElement;
    assert.ok(parseInt(pause.style.minHeight) >= 28); assert.equal(pause.textContent, "Pause");
    assert.ok(ui.container.querySelector("style")?.textContent?.includes(":focus-visible"));
    assert.ok(pause.hasAttribute("data-kg"));
    const details = ui.container.querySelector("details")!;
    assert.equal(details.querySelector("summary")?.textContent, "Details");
    assert.ok(within(details as HTMLElement).getByText("Last check state: error (as in bb keepgoing status)"));
    assert.ok(within(details as HTMLElement).getByText(/^Last recorded action: none · succeeded/));
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("a raised no-progress flag is on the card, qualified for a held loop", async () => {
  const text = "No progress: 4 checks that found an error or retried the turn with no recorded change to its repositories in 11 h.";
  const ui = card([loop({ controlState: "paused" }, { noProgress: { text } })]);
  try { assert.ok(await ui.findByText(`${text} (recorded before the hold; the loop is held (paused), so this is not current)`)); } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("a held loop whose note says it is retired shows no resume command; every other held loop keeps it", async () => {
  assert.equal(noteSaysRetired({ controlState: "awaiting user", note: "Retired: do not resume this loop." }), true);
  assert.equal(noteSaysRetired({ controlState: "awaiting user", note: "Needs a decision on the release." }), false);
  assert.equal(noteSaysRetired({ controlState: "paused", note: "Retired." }), false, "only a hold for a person");
  assert.equal(noteSaysRetired({ controlState: "awaiting user", note: null }), false);
  const retired = card([loop({ controlState: "awaiting user", note: "This thread is retired; do not resume this loop." })]);
  try {
    assert.ok(await retired.findByText(/Its note says it is retired or not to be resumed, so no resume command is shown here/));
    assert.equal(retired.container.querySelector("code"), null); assert.equal(retired.queryByText(/keepgoing resume/), null);
    assert.ok(within(retired.getByRole("list", { name: "Why" })).getByText(/^Note: This thread is retired; do not resume this loop\.$/));
  } finally { retired.lifecycle.unmount(); cleanup(); }
  const ordinary = card([loop({ controlState: "awaiting user", note: "Needs a decision on the release." })]);
  try { await ordinary.findByText(/A person resumes this from an ordinary terminal/); assert.equal(ordinary.container.querySelectorAll("code").length, 1); } finally { ordinary.lifecycle.unmount(); cleanup(); }

});
