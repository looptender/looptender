import { test, mock } from "node:test";
import assert from "node:assert/strict";
import { writeFile } from "node:fs/promises";
import { JSDOM } from "jsdom";
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "https://example.com" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
const { cleanup, act, within } = await import("@testing-library/react");
const { loadPluginApp, renderSlot } = await import("@get-bb/plugin-sdk/testing/app");
const app = await loadPluginApp(() => import("./app.tsx"));
const seg = app.threadContextSegments[0]!;
const fixture = { loop: "example-loop", configPath: "/private/example/looptender.yaml", controlState: "completed", note: null, target: "primary", supervising: true, lastState: "ok", lastWakeAt: "0001-01-01T00:00:00Z", checkedAt: null, handoffs: 0, fallbacks: null,
  details: { readAt: "2099-01-01T00:00:00Z", completed: true, targetThread: "thr_example", targetAdapter: "bb", targetConfigured: true, lastCheck: null, runtime: null, daemon: null,
    disk: { level: "critical", message: "Fictional volume nearly full; free space before records fail to save", source: "filesystem", measuredAt: "2026-01-02T03:04:05Z" } } };

test("collapsed banner separates loop completion from critical disk; failed refresh retains stale evidence without controls", async () => {
  const clock = mock.method(Date, "now", () => Date.parse("2026-01-02T03:04:15Z"));
  let fail = false;
  const props = { threadId: "thr_diagnostic_render", projectId: "proj_example", environment: null, segments: [], isExpanded: true, onToggle() {} };
  const rpc = { forThread: () => { if (fail) throw new Error("PRIVATE fixture failure"); return { loops: [fixture], errors: [] }; } };
  const summary = renderSlot({ component: seg.summary }, props, { rpc });
  const details = renderSlot({ component: seg.details! }, props, { rpc });
  try {
    const button = await summary.findByRole("button", { name: "looptender: loop completion declared" });
    const disk = summary.getByRole("status", { name: "State-volume disk" });
    assert.match(disk.textContent ?? "", /disk: critical \(recent\)/);
    assert.equal(button.contains(disk), false, "disk is independent of the completion state");
    assert.equal((disk as HTMLElement).style.overflowWrap, "anywhere");
    assert.ok(await details.findByText(/^Completion declared for this loop\. Project and current thread work are not verified/));
    assert.ok(details.getByText(/measured 2026-01-02T03:04:05Z.*filesystem reading/));
    assert.equal(details.queryByRole("button", { name: /resume|disable|archive/i }), null);
    if (process.env.DIAGNOSTICS_RENDER_PATH) await writeFile(process.env.DIAGNOSTICS_RENDER_PATH, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Fictional loop diagnostics</title><style>body{font:16px system-ui;margin:20px;max-width:600px;color:#222}button{font:inherit}summary{cursor:pointer}[role=status]{display:block;margin:8px 0}*{box-sizing:border-box}</style><body><main aria-label="Fictional diagnostics fixture">${summary.container.innerHTML}${details.container.innerHTML}</main></body></html>`);
    fail = true; await summary.behavior.emitRealtime("changed", {});
    await summary.findByRole("button", { name: "looptender: status unavailable" });
    assert.match(summary.getByRole("status", { name: "State-volume disk" }).textContent ?? "", /critical \(stale; refresh failed\)/);
    await details.findByRole("alert");
    assert.ok(details.getByText(/stale; refresh failed.*measured 2026-01-02T03:04:05Z/));
    assert.equal(details.queryByRole("button", { name: /resume|disable|archive/i }), null);
    assert.ok(!details.container.textContent?.includes("PRIVATE"));
  } finally { summary.lifecycle.unmount(); details.lifecycle.unmount(); cleanup(); clock.mock.restore(); }
});

test("card retains a stale disk warning on refresh failure without reviving other reasons or actions", async () => {
  let fail = false;
  const ui = renderSlot(app.threadContextCardRows[0]!, { threadId: "thr_card_disk_failure", projectId: "proj_example", environment: null }, { rpc: {
    forThread: () => { if (fail) throw new Error("PRIVATE fixture failure"); return { loops: [{ ...fixture, note: "Fictional held note" }], errors: [] }; },
  } });
  try {
    await ui.findByText("looptender: loop completion declared");
    assert.ok(ui.getByText(/State-volume disk: critical/));
    fail = true; await ui.behavior.emitRealtime("changed", {});
    await ui.findByRole("alert");
    assert.ok(ui.getByText(/State-volume disk: critical \(stale; refresh failed\).*measured 2026-01-02T03:04:05Z/));
    assert.equal(ui.queryByText("Note: Fictional held note"), null);
    assert.equal(ui.queryByRole("button", { name: /resume|disable|archive|pause/i }), null);
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("settled disk displays cross age thresholds during one pending refresh without more reads", async () => {
  let now = Date.parse("2026-01-02T03:04:15Z"), calls = 0, lineageCalls = 0;
  const clock = mock.method(Date, "now", () => now);
  const intervals = new Map<ReturnType<typeof setInterval>, () => void>();
  const originalSet = globalThis.setInterval, originalClear = globalThis.clearInterval;
  const timer = mock.method(globalThis, "setInterval", ((callback: () => void, ms?: number) => {
    const id = originalSet(callback, ms);
    if (ms === 1_000) intervals.set(id, callback);
    return id;
  }) as typeof setInterval);
  const clear = mock.method(globalThis, "clearInterval", (id: Parameters<typeof clearInterval>[0]) => {
    intervals.delete(id as ReturnType<typeof setInterval>);
    originalClear(id);
  });
  let release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  const props = { threadId: "thr_pending_disk_ages", projectId: "proj_example", environment: null, segments: [], isExpanded: true, onToggle() {} };
  const rpc = { forThread: async () => { if (++calls > 1) await pending; return { loops: [fixture], errors: [] }; }, lineage: () => { lineageCalls++; return { child: false }; } };
  const summary = renderSlot({ component: seg.summary }, props, { rpc });
  const details = renderSlot({ component: seg.details! }, props, { rpc });
  const card = renderSlot(app.threadContextCardRows[0]!, props, { rpc });
  const detailView = within(details.container), cardView = within(card.container);
  const state = () => summary.getByRole("status", { name: "State-volume disk" }).textContent ?? "";
  try {
    await summary.findByRole("button", { name: "looptender: loop completion declared" });
    await detailView.findByText(/State-volume disk: critical/);
    await cardView.findByText(/State-volume disk: critical/);
    // Settle lineage/effects: otherwise an unrelated initial repaint can hide the bug.
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 25)); });
    assert.equal(calls, 1); assert.match(state(), /recent/);
    const metadataReads = lineageCalls;
    now += 20_000; // 30-second-old sample; start the second read and leave it pending.
    await act(async () => { await summary.behavior.emitRealtime("changed", {}); });
    assert.equal(calls, 2); assert.match(state(), /cached/);
    now += 100_000; // No new read or external-store notification at this boundary.
    await act(async () => { for (const callback of intervals.values()) callback(); });
    assert.match(state(), /stale/);
    assert.ok(intervals.size > 0, "mounted disk displays have a bounded display clock");
    for (const ui of [detailView, cardView]) assert.ok(ui.getByText(/critical \(stale\).*2m old; measured 2026-01-02T03:04:05Z.*filesystem reading/));
    now += 120_000; // Timers can be throttled while hidden or asleep.
    await act(async () => { document.dispatchEvent(new dom.window.Event("visibilitychange")); });
    assert.ok(detailView.getByText(/4m old; measured 2026-01-02T03:04:05Z/));
    now += 120_000;
    await act(async () => { window.dispatchEvent(new dom.window.Event("pageshow")); });
    assert.ok(cardView.getByText(/6m old; measured 2026-01-02T03:04:05Z/));
    assert.equal(calls, 2, "display aging never reads providers or retries the pending request");
    assert.equal(lineageCalls, metadataReads, "display aging adds no BB metadata reads");
    assert.match(state(), /critical \(stale\)/);
    assert.doesNotMatch(state(), /refresh failed/, "pending is not a failed refresh");
    assert.equal(detailView.queryByRole("alert"), null);
    assert.equal(detailView.queryByRole("button", { name: /resume|disable|archive/i }), null);
  } finally {
    summary.lifecycle.unmount(); details.lifecycle.unmount(); card.lifecycle.unmount(); cleanup();
    assert.equal(intervals.size, 0, "display timers are removed with the views");
    release(); await act(async () => {});
    clear.mock.restore(); timer.mock.restore(); clock.mock.restore();
  }
});
