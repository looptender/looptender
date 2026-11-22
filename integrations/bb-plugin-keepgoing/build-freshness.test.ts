import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createFakePluginHost } from "@get-bb/plugin-sdk/testing";
import plugin from "./server.ts";
import { buildFreshness, noProgressLine, olderCommand, reasonLines, type StatusDetails } from "./status-details.ts";

const now = new Date("2026-11-23T12:00:00Z");
const ago = (ms: number) => new Date(now.getTime() - ms).toISOString();
const details = (over: Partial<StatusDetails> = {}, comparison = "match", running = "v0.2.0", installed = "v0.2.0"): StatusDetails => ({ readAt: ago(0), completed: false, targetThread: "thr_x", targetAdapter: "bb", targetConfigured: true, lastCheck: null,
  runtime: { state: "recent", source: "state", freshness: "recent", recordedBuild: running, installedBuild: installed, binaryComparison: comparison }, daemon: { state: "responsive", binaryComparison: comparison }, ...over });
const adopted = (ms: number, extra = {}) => ({ buildAdoption: { outcome: "adopted", at: ago(ms), from: "v0.1.0", to: "v0.2.0", ...extra } });
const cfg = "/private/example/looptender.yaml";

test("up to date is just the version; an update shows for 24 hours and no longer", () => {
  assert.deepEqual(buildFreshness(details(), cfg, now), { state: "current" });
  const just = buildFreshness(details(adopted(60_000)), cfg, now)!;
  assert.equal(just.state, "updated"); assert.equal(just.text, "Updated 1 min ago");
  assert.match(just.detail!, /^Installed .+ · was v0\.1\.0 · now v0\.2\.0$/);
  assert.equal(buildFreshness(details(adopted(23 * 3600_000 + 59 * 60_000)), cfg, now)!.text, "Updated 23 h ago");
  assert.equal(buildFreshness(details(adopted(24 * 3600_000)), cfg, now)!.state, "current");
  assert.equal(buildFreshness(details(adopted(-3600_000)), cfg, now)!.state, "current", "a switch in the future is not shown");
  assert.equal(buildFreshness(details({ buildAdoption: { outcome: "refused", at: ago(60_000) } }), cfg, now)!.state, "current", "a refused switch is not an update");
});

test("a supervisor on another build is pending when it follows installs, older when it does not", () => {
  const pending = buildFreshness(details({ followsInstalls: "yes" }, "different", "v0.1.0"), cfg, now)!;
  assert.equal(pending.state, "pending"); assert.equal(pending.text, "Update pending · switches at next check"); assert.equal(pending.detail, "Runs v0.1.0; installed v0.2.0.");
  for (const follows of ["no", "unknown", undefined]) {
    const older = buildFreshness(details({ followsInstalls: follows }, "different", "v0.1.0"), cfg, now)!;
    assert.equal(older.state, "older"); assert.equal(older.text, "Older build · restart the supervisor to update");
    assert.equal(older.command, olderCommand(cfg)); assert.equal(older.command, `keepgoing daemon-shutdown -c "${cfg}"`);
    assert.match(older.detail!, follows === "no" ? /It does not follow installs\./ : /It does not report following installs\./);
    assert.ok(!/resume|start$/.test(older.command!));
  }
});

test("nothing is said when no daemon answered or a build is not reported", () => {
  assert.equal(buildFreshness(details({ daemon: { state: "not_recorded" } }), cfg, now), undefined);
  assert.equal(buildFreshness(details({ runtime: { state: "recent", source: "state", freshness: "recent" } }), cfg, now), undefined);
  assert.equal(buildFreshness(details({ daemon: { state: "responsive" } }), cfg, now), undefined);
  assert.equal(buildFreshness(undefined, cfg, now), undefined);
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
  await writeFile(program, `#!/bin/sh\ncat <<'JSON'\n${JSON.stringify(status)}\nJSON\n`, { mode: 0o700 });
  const { bb, harness } = createFakePluginHost({ pluginId: "keepgoing", settings: { keepgoingPath: program, configs: join(dir, "looptender.yaml") } });
  await plugin(bb);
  return { forThread: async () => await harness.behavior.callRpc("forThread", { threadId: "thr_example" }) as { loops: { details: StatusDetails; pluginBuild?: string }[]; errors: string[] }, dispose: async () => { await harness.lifecycle.dispose(); await rm(dir, { recursive: true, force: true }); } };
}
const agent = { adapter: "bb", bbThread: "thr_example" };
const healthy = { loop: "example-loop", target: "primary", controlState: "enabled", control: {}, window: "open", state: {}, daemon: { state: "responsive", binaryComparison: "match" }, primaryAgent: agent, targetAgent: agent };

test("status's switch, follows-installs and raised flag reach the card's details; labels come from status's own sentence", async () => {
  const w = await loopWith({ ...healthy, followsInstalls: "yes", noProgress: { availability: "flagged", text: "No progress: 4 checks." },
    buildAdoption: { outcome: "adopted", at: "2026-11-22T19:24:30Z", fromSha256: "5ec1f3593187", toSha256: "083ada595ab5", message: "The daemon switched from v0.2.0-dev.60ef5a9c646e (5ec1f3593187) to the installed v0.2.0-dev.072acc5eafe6 (083ada595ab5) at 2026-11-22T19:24:30Z, after a check; holds were kept." } });
  try {
    const { loops } = await w.forThread();
    const d = loops[0]!.details;
    assert.deepEqual(d.buildAdoption, { outcome: "adopted", at: "2026-11-22T19:24:30Z", from: "v0.2.0-dev.60ef5a9c646e", to: "v0.2.0-dev.072acc5eafe6" });
    assert.equal(d.followsInstalls, "yes"); assert.deepEqual(d.noProgress, { text: "No progress: 4 checks." });
  } finally { await w.dispose(); }
  const clear = await loopWith({ ...healthy, noProgress: { availability: "clear", text: "Clear: 0 messages." }, buildAdoption: { outcome: "adopted", at: "2026-11-22T19:24:30Z", fromSha256: "5ec1f3593187", toSha256: "083ada595ab5", message: "reworded" } });
  try {
    const d = (await clear.forThread()).loops[0]!.details;
    assert.equal(d.noProgress, undefined);
    assert.deepEqual(d.buildAdoption, { outcome: "adopted", at: "2026-11-22T19:24:30Z", from: "5ec1f3593187", to: "083ada595ab5" }, "the digests when the sentence is not the one expected");
  } finally { await clear.dispose(); }
  const bad = await loopWith({ ...healthy, followsInstalls: 5, buildAdoption: "garbage", noProgress: [] });
  try { const d = (await bad.forThread()).loops[0]!.details; assert.deepEqual(d.unparsed, ["followsInstalls", "buildAdoption", "noProgress"]); } finally { await bad.dispose(); }
});
