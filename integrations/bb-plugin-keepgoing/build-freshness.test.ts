import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createFakePluginHost } from "@get-bb/plugin-sdk/testing";
import plugin from "./server.ts";
import { freshnessToShow, noProgressLine, reasonLines, type StatusDetails } from "./status-details.ts";
import { writeStandIn } from "./stand-in.ts";

const fresh = (over: object = {}) => ({ state: "updated", text: "Updated 3 h ago", detail: "Installed 23 Nov 09:00 · was v0.2.0-dev.1111111 · now v0.2.0-dev.aaaaaaa", ...over });
const details = (over: Partial<StatusDetails> = {}): StatusDetails => ({ readAt: "2026-01-01T00:00:00Z", completed: false, targetThread: "thr_x", targetAdapter: "bb", targetConfigured: true, lastCheck: null, runtime: null, daemon: null, ...over });

test("looptender's words are shown as sent; a current build and a looptender that sends none show nothing", () => {
  assert.equal(freshnessToShow(details({ buildFreshness: fresh() }))?.text, "Updated 3 h ago");
  assert.equal(freshnessToShow(details({ buildFreshness: { state: "current", text: "v0.2.0" } })), undefined);
  assert.equal(freshnessToShow(details()), undefined, "a binary that does not send the field: nothing is said");
  assert.equal(freshnessToShow(undefined), undefined);
});

test("a raised no-progress flag is qualified for a held loop as status qualifies it", () => {
  const d = details({ noProgress: { text: "No progress: 4 checks that found an error or retried the turn with no recorded change to its repositories in 11 h." } });
  assert.equal(noProgressLine("enabled", d), d.noProgress!.text);
  assert.equal(noProgressLine("paused", d), `${d.noProgress!.text} (recorded before the hold; the loop is held (paused), so this is not current)`);
  assert.match(noProgressLine("awaiting user", d)!, /held \(awaiting a person\)/);
  assert.match(noProgressLine("stop requested", d)!, /held \(stopped\)/);
  assert.equal(noProgressLine("enabled", details()), undefined);
  assert.ok(reasonLines("paused", null, d).some(l => l.includes("recorded before the hold")));
});

async function loopWith(status: object) {
  const dir = await realpath(await mkdtemp(join(tmpdir(), "keepgoing-freshness-")));
  const program = join(dir, "looptender-stand-in");
  await writeStandIn(program, `#!/bin/sh\ncat <<'JSON'\n${JSON.stringify(status)}\nJSON\n`);
  const { bb, harness } = createFakePluginHost({ pluginId: "keepgoing", settings: { keepgoingPath: program, configs: join(dir, "looptender.yaml") } });
  await plugin(bb);
  return { forThread: async () => await harness.behavior.callRpc("forThread", { threadId: "thr_example" }) as { loops: { details: StatusDetails; pluginBuild?: string }[]; errors: string[] }, dispose: async () => { await harness.lifecycle.dispose(); await rm(dir, { recursive: true, force: true }); } };
}
const agent = { adapter: "bb", bbThread: "thr_example" };
const healthy = { loop: "example-loop", target: "primary", controlState: "enabled", control: {}, window: "open", state: {}, daemon: { state: "responsive", binaryComparison: "match" }, primaryAgent: agent, targetAgent: agent };

test("status's buildFreshness and raised flag reach the card's details as sent; malformed ones are named; none sent is none shown", async () => {
  const sent = { state: "older", text: "Different build from the installed one", action: "Restart the supervisor to update.", command: "looptender daemon-shutdown -c CONFIG", running: "v0.2.0-dev.1111111", installed: "v0.2.0-dev.aaaaaaa" };
  const w = await loopWith({ ...healthy, buildFreshness: sent, noProgress: { availability: "flagged", text: "No progress: 4 checks." } });
  try {
    const d = (await w.forThread()).loops[0]!.details;
    assert.deepEqual(d.buildFreshness, { state: "older", text: sent.text, action: sent.action, command: sent.command });
    assert.deepEqual(d.noProgress, { text: "No progress: 4 checks." });
  } finally { await w.dispose(); }
  const clear = await loopWith({ ...healthy, noProgress: { availability: "clear", text: "Clear: 0 messages." } });
  try { const d = (await clear.forThread()).loops[0]!.details; assert.equal(d.noProgress, undefined); assert.equal(d.buildFreshness, undefined); } finally { await clear.dispose(); }
  const bad = await loopWith({ ...healthy, buildFreshness: "garbage", noProgress: [] });
  try { const d = (await bad.forThread()).loops[0]!.details; assert.deepEqual(d.unparsed, ["buildFreshness", "noProgress"]); assert.equal(d.buildFreshness, undefined); } finally { await bad.dispose(); }
});

test("a long freshness action is cut at a whole word with an ellipsis, not mid-word", async () => {
  const action = ("end its process yourself and start it again with the daemon command in a terminal you leave open, ").repeat(8).trim();
  const w = await loopWith({ ...healthy, buildFreshness: { state: "different", text: "Different build from the installed one", action } });
  try {
    const got = (await w.forThread()).loops[0]!.details.buildFreshness!.action!;
    assert.ok(got.length <= 600 && got.endsWith("…"), got.slice(-30));
    assert.ok(action.startsWith(got.slice(0, -1).trimEnd()) && /\s/.test(action[got.slice(0, -1).trimEnd().length]!), "ends on a whole word");
  } finally { await w.dispose(); }
});
