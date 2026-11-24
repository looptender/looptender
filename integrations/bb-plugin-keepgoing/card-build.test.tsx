import { test } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "https://example.com" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
const { cleanup, within } = await import("@testing-library/react");
const { loadPluginApp, renderSlot } = await import("@get-bb/plugin-sdk/testing/app");
const app = await loadPluginApp(() => import("./app.tsx"));
const base = { readAt: "2026-01-01T00:00:00Z", completed: false, targetThread: "thr_example", targetAdapter: "bb", targetConfigured: true, lastCheck: null };
const dets = (over: object = {}, comparison = "match", running = "v0.2.0-dev.aaaaaaa", installed = "v0.2.0-dev.aaaaaaa") => ({ ...base, runtime: { state: "recent", source: "state", freshness: "recent", recordedBuild: running, installedBuild: installed, binaryComparison: comparison }, daemon: { state: "responsive", binaryComparison: comparison }, ...over });
const loop = (over: object = {}, d: object = {}) => ({ loop: "example-loop", configPath: "/private/example/looptender.yaml", controlState: "enabled", note: null, target: "primary", supervising: true, lastState: "ok", lastWakeAt: "0001-01-01T00:00:00Z", checkedAt: null, handoffs: 0, fallbacks: null, referenceSource: "plugin_setting", details: dets(d), ...over });
let n = 0;
const card = (loops: object[]) => renderSlot(app.threadContextCardRows[0]!, { threadId: `thr_build_${++n}`, projectId: "proj_example", environment: null }, { rpc: { forThread: () => ({ loops, errors: [] }) } });

const fresh = (over: object) => ({ state: "updated", text: "Updated 2 h ago", detail: "Installed 23 Nov 09:00 · was v0.2.0-dev.1111111 · now v0.2.0-dev.aaaaaaa", ...over });

test("looptender's freshness words are visible on the card as sent, the detail in the expanded card; no button, no new badge", async () => {
  const ui = card([loop({}, { buildFreshness: fresh({}) })]);
  try {
    assert.ok(await ui.findByText("Updated 2 h ago"));
    assert.equal(within(ui.getByRole("list", { name: "Details" })).getAllByText(/^Build: /).length, 1, "one Build line");
    assert.ok(within(ui.getByRole("list", { name: "Details" })).getByText(/^Build: .* · Installed 23 Nov 09:00 · was v0\.2\.0-dev\.1111111 · now v0\.2\.0-dev\.aaaaaaa$/));
    assert.equal(ui.queryByText(/\bnew\b/i), null); assert.equal(ui.queryByRole("button", { name: /restart|update|resume/i }), null);
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("a command is shown only when looptender sent one, otherwise its words; neither is ever a button", async () => {
  const pending = card([loop({}, { buildFreshness: { state: "pending", text: "Update pending · switches at next check" } })]);
  try { assert.ok(await pending.findByText("Update pending · switches at next check")); assert.equal(pending.container.querySelector("code"), null); } finally { pending.lifecycle.unmount(); cleanup(); }
  const withCommand = card([loop({}, { buildFreshness: { state: "older", text: "Older build · restart the supervisor to update", command: "looptender daemon-shutdown -c CONFIG" } })]);
  try {
    assert.ok(await withCommand.findByText("Older build · restart the supervisor to update"));
    assert.equal(withCommand.container.querySelector("[aria-label=Build] code")?.textContent, "looptender daemon-shutdown -c CONFIG");
    assert.equal(withCommand.queryByRole("button", { name: /restart|update|resume|shutdown/i }), null);
  } finally { withCommand.lifecycle.unmount(); cleanup(); }
  const withAction = card([loop({}, { buildFreshness: { state: "different", text: "Different build from the installed one", action: "Restart supervision the way it was started." } })]);
  try {
    assert.ok(await withAction.findByText("Different build from the installed one")); assert.ok(within(withAction.getByLabelText("Build")).getByText(/Restart supervision the way it was started\./));
    assert.equal(withAction.container.querySelector("code"), null);
  } finally { withAction.lifecycle.unmount(); cleanup(); }
});

test("a current build shows only its version; a looptender that sends no freshness shows nothing; the chip stays short", async () => {
  const cur = card([loop({}, { buildFreshness: { state: "current", text: "v0.2.0-dev.aaaaaaa" } })]);
  try { await cur.findByText("looptender: enabled v0.2.0-dev.aaaaaaa"); assert.equal(cur.container.querySelector("[aria-label=Build]"), null); } finally { cur.lifecycle.unmount(); cleanup(); }
  const none = card([loop({}, {})].map(l => ({ ...l, details: dets({}, "different", "v0.2.0-dev.1111111", "v0.2.0-dev.aaaaaaa") })));
  try {
    const chip = await none.findByText(/^looptender: enabled v0\.2\.0-dev\.1111111 · build differs$/);
    assert.ok((chip.textContent ?? "").length < 60); assert.equal(none.container.querySelector("[aria-label=Build]"), null, "nothing is derived from other fields");
  } finally { none.lifecycle.unmount(); cleanup(); }
});

test("the plugin's own build is named apart from the supervisor's when they differ", async () => {
  const ui = card([loop({ pluginBuild: "v0.2.0-dev.bbbbbbbbbbbb" })]);
  try { assert.ok(await within(await ui.findByRole("list", { name: "Technical details" })).findByText(/^Plugin build: v0\.2\.0-dev\.bbbbbbbbbbbb \(the code drawing this card\); the builds above are each supervisor's/)); } finally { ui.lifecycle.unmount(); cleanup(); }
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
    assert.ok(within(details as HTMLElement).getByText("Last check state: error (the word looptender status lists after last=)"));
    assert.ok(within(details as HTMLElement).getByText(/^Last recorded action: none · succeeded/));
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("a raised no-progress flag is on the card, qualified for a held loop", async () => {
  const text = "No progress: 4 checks that found an error or retried the turn with no recorded change to its repositories in 11 h.";
  const ui = card([loop({ controlState: "paused" }, { noProgress: { text } })]);
  try { assert.ok(await ui.findByText(`${text} (recorded before the hold; the loop is held (paused), so this is not current)`)); } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("a note never hides the resume command; an archived loop gets none and says how to use it again", async () => {
  for (const note of ["This thread is retired; do not resume this loop.", "Needs a decision on whether to remove the retired endpoints", "This is NOT retired; please resume"]) {
    const ui = card([loop({ controlState: "awaiting user", note })]);
    try { await ui.findByText("Held for you. Resume it from the looptender panel (looptender panel), or from a terminal outside BB with looptender resume."); assert.equal(ui.container.querySelectorAll("code").length, 1, note); } finally { ui.lifecycle.unmount(); cleanup(); }
  }
  const at = "2026-01-01T00:00:00Z";
  const archived = card([loop({ controlState: "awaiting user", note: "continued elsewhere" }, { archived: { at, hold: "awaiting user" } })]);
  try {
    await archived.findByText("looptender: archived");
    assert.ok(archived.getByText(new RegExp(`^Archived since .* · unarchive to use it again\\.$`)));
    assert.equal(archived.container.querySelector("code"), null); assert.equal(archived.queryByText(/resumes this/), null);
    assert.equal(archived.queryByRole("button", { name: /resume|unarchive/i }), null);
  } finally { archived.lifecycle.unmount(); cleanup(); }
});
