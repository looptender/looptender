import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { JSDOM } from "jsdom";
import { createFakePluginHost } from "@get-bb/plugin-sdk/testing";
import { writeStandIn } from "./stand-in.ts";
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "https://example.com" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
const { cleanup, within } = await import("@testing-library/react");
const { loadPluginApp, renderSlot } = await import("@get-bb/plugin-sdk/testing/app");
const { default: plugin } = await import("./server.ts");
const { ARCHIVE_UNREADABLE } = await import("./status-details.ts");
const app = await loadPluginApp(() => import("./app.tsx"));
const base = { readAt: "2026-01-01T00:00:00Z", completed: false, targetThread: "thr_example", targetAdapter: "bb", targetConfigured: true, lastCheck: null, runtime: null, daemon: null };
const loop = (over: object = {}, d: object = {}) => ({ loop: "example-loop", configPath: "/private/example/looptender.yaml", controlState: "awaiting user", note: "Needs a decision.", target: "primary", supervising: true, lastState: null, lastWakeAt: "0001-01-01T00:00:00Z", checkedAt: null, handoffs: 0, fallbacks: null, referenceSource: "plugin_setting", details: { ...base, ...d }, ...over });
let n = 0;
const card = (loops: object[]) => renderSlot(app.threadContextCardRows[0]!, { threadId: `thr_an_${++n}`, projectId: "proj_example", environment: null }, { rpc: { forThread: () => ({ loops, errors: [] }) } });

async function forThread(status: object) {
  const dir = await realpath(await mkdtemp(join(tmpdir(), "keepgoing-archive-")));
  const program = join(dir, "stand-in");
  await writeStandIn(program, `#!/bin/sh\ncat <<'JSON'\n${JSON.stringify(status)}\nJSON\n`);
  const { bb, harness } = createFakePluginHost({ pluginId: "keepgoing", settings: { keepgoingPath: program, configs: join(dir, "looptender.yaml") } });
  await plugin(bb);
  try { return await harness.behavior.callRpc("forThread", { threadId: "thr_example" }) as { loops: { details: any; displayName?: string; loop: string }[]; errors: string[] }; }
  finally { await harness.lifecycle.dispose(); await rm(dir, { recursive: true, force: true }); }
}
const agent = { adapter: "bb", bbThread: "thr_example" };
const healthy = { loop: "example-loop", target: "primary", controlState: "awaiting user", control: {}, window: "open", state: {}, daemon: { state: "not_recorded" }, primaryAgent: agent, targetAgent: agent };

test("a malformed, unreadable or dateless archive record fails safe: no resume command, no empty date", async () => {
  for (const archived of ["garbage", { at: "not a date" }, { at: "0001-01-01T00:00:00Z" }, { unreadable: true }, { at: "2026-01-01T00:00:00Z", unreadable: true }, {}]) {
    const { loops, errors } = await forThread({ ...healthy, archived });
    assert.deepEqual(errors, [], JSON.stringify(archived)); assert.equal(loops.length, 1);
    assert.equal(loops[0]!.details.archived.at, undefined, JSON.stringify(archived)); assert.equal(loops[0]!.details.archived.unreadable, true);
    const ui = card([loop({}, { archived: loops[0]!.details.archived })]);
    try {
      await ui.findByText("looptender: archive record unreadable");
      assert.ok(ui.getByText(ARCHIVE_UNREADABLE + "."));
      assert.equal(ui.container.querySelector("code"), null); assert.equal(ui.queryByText(/Archived since/), null); assert.equal(ui.queryByText(/A person resumes/), null);
      assert.equal(ui.queryByRole("button", { name: /resume|pause/i }), null);
    } finally { ui.lifecycle.unmount(); cleanup(); }
  }
  const good = await forThread({ ...healthy, archived: { at: "2026-01-01T00:00:00Z", hold: "awaiting user", note: "continued elsewhere" } });
  assert.deepEqual(good.loops[0]!.details.archived, { at: "2026-01-01T00:00:00Z", hold: "awaiting user" }, "the archive note is dropped");
  assert.ok(!JSON.stringify(good).includes("continued elsewhere"));
});

test("archiveLapsed is one quiet line in Details; a malformed one is named, not shown", async () => {
  const { loops } = await forThread({ ...healthy, controlState: "enabled", archiveLapsed: { at: "2026-01-01T00:00:00Z", note: "private" } });
  assert.deepEqual(loops[0]!.details.archiveLapsed, { at: "2026-01-01T00:00:00Z" });
  const ui = card([loop({ controlState: "enabled" }, { archiveLapsed: { at: "2026-01-01T00:00:00Z" } })]);
  try {
    assert.ok(within(await ui.findByRole("list", { name: "Details" })).getByText(/^An earlier archive record \(.*\) is left on this loop, which supervises again; it has no effect$/));
    assert.equal(ui.queryByText("looptender: archived"), null);
  } finally { ui.lifecycle.unmount(); cleanup(); }
  const bad = await forThread({ ...healthy, archiveLapsed: "x" });
  assert.deepEqual(bad.loops[0]!.details.unparsed, ["archiveLapsed"]); assert.equal(bad.loops[0]!.details.archiveLapsed, undefined);
});

test("the display name is shown where the card shows a name, with the slug as secondary text", async () => {
  const { loops } = await forThread({ ...healthy, naming: { displayName: "Release notes for 2.4", nameSource: "thread_title", slug: "release-2-4" }, loop: "release-2-4" });
  assert.equal(loops[0]!.displayName, "Release notes for 2.4"); assert.equal(loops[0]!.loop, "release-2-4");
  assert.equal((await forThread({ ...healthy, naming: { displayName: "example-loop", nameSource: "slug" } })).loops[0]!.displayName, undefined, "a name equal to the slug adds nothing");
  assert.equal((await forThread({ ...healthy, naming: "garbage" })).loops[0]!.displayName, undefined);
  const ui = card([loop({ loop: "release-2-4", displayName: "Release notes for 2.4", controlState: "enabled", note: null })]);
  try {
    const region = await ui.findByRole("region", { name: "Loop Release notes for 2.4" });
    assert.ok(within(region).getByText("Release notes for 2.4")); assert.ok(within(region).getByText("(release-2-4)"));
    assert.ok(within(region).getByRole("button", { name: "Pause Release notes for 2.4" }));
    assert.ok(ui.container.querySelector("[title]")!.getAttribute("title")!.includes("Loop Release notes for 2.4 (release-2-4)"));
  } finally { ui.lifecycle.unmount(); cleanup(); }
});
