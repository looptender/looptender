import { test } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "https://example.com" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
const { fireEvent, cleanup, waitFor } = await import("@testing-library/react");
const { loadPluginApp, renderSlot } = await import("@get-bb/plugin-sdk/testing/app");
const app = await loadPluginApp(() => import("./app.tsx"));
const loop = { loop: "example-loop", configPath: "/private/example/looptender.yaml", controlState: "enabled", note: null, target: "fallback-1", supervising: true, lastState: null, lastWakeAt: "0001-01-01T00:00:00Z", checkedAt: null, handoffs: 0, fallbacks: null, referenceSource: "plugin_setting",
  details: { readAt: "2026-01-01T00:00:00Z", completed: false, targetThread: "thr_target", targetAdapter: "bb", targetConfigured: true, runtime: null, daemon: null, lastCheck: null } };
const card = (l: object, threadId: string, rpc: Record<string, (i: unknown) => unknown> = {}) =>
  renderSlot(app.threadContextCardRows[0]!, { threadId, projectId: "proj_example", environment: null }, { rpc: { forThread: () => ({ loops: [l], errors: [] }), ...rpc } });

test("a superseded loop says resume is refused and names its successor; it never offers a resume command or button", async () => {
  const superseded = { ...loop, controlState: "superseded", details: { ...loop.details, supersededBy: "thr_successor" } };
  const ui = card(superseded, "thr_superseded_named");
  try {
    await ui.findByText("looptender: superseded");
    assert.ok(ui.getByText(/Resume is refused for a superseded loop; its successor continues the work \(thr_successor\)/));
    assert.equal(ui.queryByText(/A person resumes this/), null); assert.equal(ui.container.querySelector("code"), null);
    assert.equal(ui.queryByRole("button", { name: /resume/i }), null);
    const title = ui.container.querySelector("[title]")?.getAttribute("title") ?? "";
    assert.match(title, /Superseded: successor reference is recorded \(thr_successor\); resume and takeover are refused/);
  } finally { ui.lifecycle.unmount(); cleanup(); }
  // Without a successor in status, the same words without a name.
  const bare = card({ ...superseded, details: { ...loop.details } }, "thr_superseded_bare");
  try { assert.ok(await bare.findByText("Resume is refused for a superseded loop; its successor continues the work.")); } finally { bare.lifecycle.unmount(); cleanup(); }
});

test("pausing a loop whose target is another thread asks first, naming the loop and the thread it supervises", async () => {
  const calls: unknown[] = [];
  const watching = { ...loop, supervising: false };
  const ui = card(watching, "thr_primary", { control: (i) => { calls.push(i); return { ok: true, message: "pause done" }; } });
  try {
    fireEvent.click(await ui.findByRole("button", { name: "Pause…" }));
    assert.deepEqual(calls, [], "the first click only asks");
    assert.ok(ui.getByText("Pause loop example-loop? It supervises thr_target, not this thread."));
    fireEvent.click(ui.getByRole("button", { name: "Cancel" }));
    assert.deepEqual(calls, []); assert.ok(ui.getByRole("button", { name: "Pause…" }));
    fireEvent.click(ui.getByRole("button", { name: "Pause…" }));
    fireEvent.click(ui.getByRole("button", { name: "Pause loop example-loop" }));
    await waitFor(() => assert.equal(calls.length, 1));
    assert.deepEqual(calls[0], { configPath: loop.configPath, action: "pause", expectedThreadId: "thr_primary" });
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("a loop that supervises this thread pauses on one click, and an unmanaged loop sends no undefined key", async () => {
  const calls: unknown[] = [];
  const ui = card(loop, "thr_one_click", { control: (i) => { calls.push(i); return { ok: true, message: "pause done" }; } });
  try {
    fireEvent.click(await ui.findByRole("button", { name: "Pause" }));
    await waitFor(() => assert.equal(calls.length, 1));
    // The SDK's RPC boundary rejects a value that is not JSON; an undefined managedId would have been refused there.
    assert.equal(ui.queryByRole("alert"), null);
    assert.deepEqual(calls[0], { configPath: loop.configPath, action: "pause", expectedThreadId: "thr_one_click" });
    assert.ok(!Object.keys(calls[0] as object).includes("managedId"));
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("the confirmation for a target that is not a BB thread names the target, not an adapter string as a thread", async () => {
  const ui = card({ ...loop, supervising: false, target: "fallback-2", details: { ...loop.details, targetThread: null, targetAdapter: "example-adapter" } }, "thr_nonbb");
  try {
    fireEvent.click(await ui.findByRole("button", { name: "Pause…" }));
    assert.ok(ui.getByText("Pause loop example-loop? Its current target is example-adapter, not this thread."));
    assert.equal(ui.queryByText(/supervises example-adapter/), null);
  } finally { ui.lifecycle.unmount(); cleanup(); }
});
