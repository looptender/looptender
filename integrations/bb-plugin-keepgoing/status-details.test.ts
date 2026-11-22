import { test } from "node:test";
import assert from "node:assert/strict";
import { createFakePluginHost } from "@get-bb/plugin-sdk/testing";
import plugin, { rpcContract } from "./server.ts";
import { diagnosticLines, dateLabel } from "./status-details.ts";

test("status RPC projects diagnostic metadata, preserves completion under other holds and supports legacy builds", async () => {
  const record = { id: "example", loop: "example", threadId: "thr_source", environmentId: "env_example", projectId: "proj_example", hostId: "host_example", project: "/projects/example", provider: "codex", configPath: "/private/example/keepgoing.yaml" };
  const agent = { adapter: "bb", bbThread: "thr_source", provider: "codex" };
  const base = { loop: "example", target: "fallback", controlState: "paused", control: { paused: true, completed: true }, window: "open", state: {}, primaryAgent: agent, targetAgent: { ...agent, bbThread: "thr_destination" } };
  let data: unknown = { ...base, task: "PRIVATE TASK", targetConfigured: true,
    runtime: { state: "paused", source: "control", freshness: "stale", reason: "PRIVATE REASON" },
    daemon: { state: "responsive", phase: "waiting", observedAt: "2026-01-01T00:00:00Z", nextCheckAt: "2026-01-01T00:05:00Z", dryRun: true, binaryComparison: "different", message: "PRIVATE OUTPUT" },
    state: { lastWakeAt: "0001-01-01T00:00:00Z", lastCheck: { at: "2025-12-31T23:55:00Z", target: "primary", action: "wake", outcome: "failed", reason: "PRIVATE REASON" } },
  };
  const { bb, harness } = createFakePluginHost({ pluginId: "keepgoing", settings: { configs: "", keepgoingPath: "keepgoing" },
    experimental_callHostRpc: async () => ({ code: 0, data }),
  });
  try {
    await plugin(bb); await bb.storage.kv.set("setups.v1", [record]);
    const read = async () => rpcContract.forThread.output.parse(await harness.behavior.callRpc("forThread", { threadId: record.threadId }));
    const result = await read(), loop = result.loops[0]!;
    assert.deepEqual(result.errors, []); assert.equal(loop.supervising, false);
    assert.equal(loop.controlState, "paused"); assert.equal(loop.details?.completed, true);
    assert.equal(loop.details?.targetThread, "thr_destination");
    assert.ok(!JSON.stringify(result).includes("PRIVATE"));
    const lines = diagnosticLines(loop.details, loop.target, loop.supervising).join("\n");
    assert.match(lines, /declared; not independently verified/);
    assert.match(lines, /thr_destination.*not this thread/);
    assert.match(lines, /stale/); assert.match(lines, /dry run/); assert.match(lines, /build comparison different/);
    assert.match(lines, /Last recorded action: wake · failed · target primary/);
    assert.match(lines, /External scheduler health: not inspected/);
    assert.equal(dateLabel(loop.lastWakeAt), "unavailable");
    for (const state of ["not_recorded", "unavailable", "skipped"]) {
      data = { ...base, daemon: { state } };
      assert.match(diagnosticLines((await read()).loops[0]?.details, "fallback", false).join("\n"), /running supervisor not confirmed/);
    }
    data = base;
    const legacy = (await read()).loops[0]!;
    assert.equal(legacy.details?.lastCheck, null); assert.equal(legacy.details?.runtime, null);
    assert.match(diagnosticLines(legacy.details, "fallback", false).join("\n"), /unavailable from this build/);
    data = { ...base, runtime: { state: 42 } };
    const invalid = await read(); assert.equal(invalid.loops.length, 0); assert.equal(invalid.errors.length, 1);
    assert.deepEqual(harness.inspection.sdk.callsTo("threads.send"), []);
  } finally { await harness.lifecycle.dispose(); }
});

test("historical dry-run, future and missing evidence never imply a successful send or healthy scheduler", () => {
  const lines = diagnosticLines({ readAt: "invalid", completed: false, targetThread: null, targetAdapter: "process", targetConfigured: false,
    runtime: { state: "unknown", source: "unavailable", freshness: "future" }, daemon: null,
    lastCheck: { at: "0001-01-01T00:00:00Z", target: "primary", action: "wake", outcome: "dry_run", dryRun: true },
  }, "primary", false).join("\n");
  assert.match(lines, /missing or disabled/); assert.match(lines, /future/); assert.match(lines, /wake · dry_run · dry run/);
  assert.match(lines, /not confirmed/); assert.match(lines, /unavailable/);
});

test("goal status excludes private fields and malformed metadata stays unavailable", async () => {
  const { goalDetails, goalLabel } = await import("./status-details.ts");
  const valid = { id: "goal_0123456789abcdef0123456789abcdef", revision: 3, state: "recorded" };
  const d = goalDetails({ ...valid, title: "PRIVATE", objectiveRefs: ["PRIVATE"] }, undefined);
  assert.deepEqual(d, { goal: valid, goalState: "recorded" });
  assert.match(goalLabel(d.goal, d.goalState), /revision 3.*not verified completion/);
  for (const bad of [{ ...valid, id: "PRIVATE" }, { ...valid, revision: -1 }, { ...valid, revision: 2**54 }, null]) {
    const result = goalDetails(bad, undefined);
    assert.equal(result.goalState, "unavailable"); assert.equal(result.goal, null);
    assert.ok(!JSON.stringify(result).includes("PRIVATE"));
  }
  assert.equal(goalDetails(valid, "PRIVATE ERROR").goalState, "unavailable");
  assert.match(goalLabel(null, goalDetails(undefined, undefined).goalState), /older CLI/);
});

test("an overdue schedule is shown with its message, and only a judged one is said to be on time", () => {
  const base = { readAt: "2030-01-01T00:00:00Z", completed: false, targetThread: "thr_example", targetAdapter: "bb", targetConfigured: true, runtime: null, daemon: null, lastCheck: null };
  const message = "no check has run for 3h10m (checks normally run every 10m): the computer may have slept, or the scheduler that runs `keepgoing tick` for this loop may have stopped";
  const overdue = diagnosticLines({ ...base, schedule: { overdue: true, judging: true, basis: "usual", message } }, "primary", true);
  assert.ok(overdue.includes("Schedule: " + message));
  const running = diagnosticLines({ ...base, schedule: { overdue: false, judging: true, basis: "usual" } }, "primary", true);
  assert.ok(running.includes("Schedule: checks are running at their usual interval"));
  for (const schedule of [{ overdue: false }, { overdue: false, judging: false }, { overdue: false, judging: true, basis: "fallback" }, { overdue: false, judging: true, basis: "usual", slept: true }]) {
    const lines = diagnosticLines({ ...base, schedule }, "primary", true);
    assert.ok(!lines.some((line) => line.startsWith("Schedule:") || line.startsWith("External scheduler health")), JSON.stringify(schedule));
  }
  const older = diagnosticLines(base, "primary", true);
  assert.ok(older.some((line) => line.startsWith("External scheduler health: not inspected")));
});

test("a long limit wait says why no handoff is possible and whether anyone was told", () => {
  const base = { readAt: "2030-01-01T00:00:00Z", completed: false, targetThread: "thr_example", targetAdapter: "bb", targetConfigured: true, runtime: null, daemon: null, lastCheck: null };
  const lines = diagnosticLines({ ...base, limitWait: { state: "not_subscribed", resetsAt: "2030-01-06T12:00:00Z", reason: "limit_failover_disabled", reportConfigured: false, resetIn: "5d 12h" } }, "primary", true);
  const line = lines.find((l) => l.startsWith("Long limit wait:"));
  assert.ok(line, lines.join("\n"));
  assert.match(line, /automatic handoff is off/);
  assert.match(line, /nobody was told: no report section/);
  const failed = diagnosticLines({ ...base, limitWait: { state: "report_failed", resetsAt: "2030-01-06T12:00:00Z", reportConfigured: true } }, "primary", true).find((l) => l.startsWith("Long limit wait:"));
  assert.match(failed ?? "", /delivery not confirmed/);
  assert.doesNotMatch(failed ?? "", /nobody/i);
  const held = diagnosticLines({ ...base, limitWait: { state: "not_subscribed", resetsAt: "2030-01-06T12:00:00Z", reportConfigured: false, held: "completed" } }, "primary", true).find((l) => l.startsWith("Long limit wait:"));
  assert.match(held ?? "", /recorded before the loop was held \(completed\)/);
  assert.doesNotMatch(held ?? "", /not held|nobody/i);
  assert.match(line, /in 5d 12h/);
  for (const limitWait of [null, { state: "none" }, { state: "reported", resetPassed: true }]) {
    assert.ok(!diagnosticLines({ ...base, limitWait }, "primary", true).some((l) => l.startsWith("Long limit wait:")), JSON.stringify(limitWait));
  }
});

test("the build lines say what was recorded and never guess", async () => {
  const { buildSummary } = await import("./status-details.ts");
  const rt = { state: "recent", source: "state", freshness: "recent" };
  assert.deepEqual(buildSummary({ ...rt, recordedBuild: "v0.2.0-dev.ccd2310", installedBuild: "v0.2.0-dev.ccd2310", binaryComparison: "match" }),
    { running: "v0.2.0-dev.ccd2310", installed: "v0.2.0-dev.ccd2310", differs: false, text: "last check ran v0.2.0-dev.ccd2310" });
  assert.equal(buildSummary({ ...rt, recordedBuild: "v0.1.0", installedBuild: "v0.2.0", binaryComparison: "different" }).text, "last check ran v0.1.0 · installed v0.2.0");
  // An older looptender sends no labels; a dev build sends none either.
  for (const runtime of [rt, { ...rt, installedBuild: "v0.2.0" }, null, undefined]) assert.equal(buildSummary(runtime).text, "version unknown");
  assert.equal(buildSummary({ ...rt, recordedBuild: "v0.1.0", binaryComparison: "different" }).differs, false);
  const lines = diagnosticLines({ readAt: "2026-01-01T00:00:00Z", completed: false, targetThread: null, targetAdapter: "bb", targetConfigured: true,
    runtime: { ...rt, recordedBuild: "v0.1.0", installedBuild: "v0.2.0", binaryComparison: "different" }, daemon: null, lastCheck: null }, "primary", true).join("\n");
  assert.match(lines, /Build: last check ran v0\.1\.0 · installed v0\.2\.0/);
  assert.match(diagnosticLines({ readAt: "2026-01-01T00:00:00Z", completed: false, targetThread: null, targetAdapter: "bb", targetConfigured: true, runtime: null, daemon: null, lastCheck: null }, "primary", true).join("\n"), /Build: version unknown/);
});

test("a moved loop carries its target's provider and model and a moved note, and older looptenders send neither", async () => {
  const record = { id: "example", loop: "example", threadId: "thr_source", environmentId: "env_example", projectId: "proj_example", hostId: "host_example", project: "/projects/example", provider: "claude-code", configPath: "/private/example/keepgoing.yaml" };
  const agent = { adapter: "bb", bbThread: "thr_source", provider: "claude-code", model: "example-model" };
  const base = { loop: "example", target: "primary", controlState: "awaiting user", control: {}, window: "open", state: {}, primaryAgent: agent, targetAgent: agent, targetConfigured: true };
  let data: unknown = { ...base, moved: { held: true, text: "Moved to its new thread. Resuming it is the one action needed.", threadId: "thr_private" } };
  const { bb, harness } = createFakePluginHost({ pluginId: "keepgoing", settings: { configs: "", keepgoingPath: "keepgoing" }, experimental_callHostRpc: async () => ({ code: 0, data }) });
  try {
    await plugin(bb); await bb.storage.kv.set("setups.v1", [record]);
    const read = async () => rpcContract.forThread.output.parse(await harness.behavior.callRpc("forThread", { threadId: record.threadId })).loops[0]!;
    const moved = await read();
    assert.deepEqual(moved.details?.moved, { held: true, text: "Moved to its new thread. Resuming it is the one action needed." });
    assert.ok(!JSON.stringify(moved).includes("thr_private"));
    const lines = diagnosticLines(moved.details, moved.target, moved.supervising).join("\n");
    assert.match(lines, /Provider: claude-code · model example-model \(configured\)/);
    assert.match(lines, /Moved: Moved to its new thread/);
    data = { ...base, primaryAgent: { adapter: "bb", bbThread: "thr_source", provider: "claude-code" }, targetAgent: { adapter: "bb", bbThread: "thr_source", provider: "claude-code" } };
    const older = await read();
    assert.equal(older.details?.moved, undefined); assert.equal(older.details?.targetModel, undefined);
    assert.doesNotMatch(diagnosticLines(older.details, older.target, older.supervising).join("\n"), /Moved:/);
  } finally { await harness.lifecycle.dispose(); }
});

test("status after a move says the target is new, not that it was never checked", async () => {
  const { lastLabel } = await import("./status-details.ts");
  assert.equal(lastLabel(null, { moved: { held: true, text: "x" } }), "none yet (new target, not yet checked)");
  assert.equal(lastLabel("", { moved: {} }), "none yet (new target, not yet checked)");
  assert.equal(lastLabel(null, undefined), "never");
  assert.equal(lastLabel("idle", null), "idle");
});

test("status says how many registered loops the plugin does not show, and how to show them", async () => {
  const { notShownLine } = await import("./status-details.ts");
  assert.equal(notShownLine(["a", "b"], ["a", "b", "c"]), "");
  const one = notShownLine(["a", "b"], ["a"]);
  assert.match(one, /^1 registered loop is not shown here \(b\)\./); assert.match(one, /configs setting, one path per line/); assert.match(one, /registry list/);
  const many = notShownLine(Array.from({ length: 11 }, (_, i) => `loop-${i}`), []);
  assert.match(many, /^11 registered loops are not shown here \(loop-0, .*loop-7, and 3 more\)\./);
  assert.equal(notShownLine([], ["a"]), "");
});

test("a long control note is one short line in the listing", async () => {
  const { shortNote } = await import("./status-details.ts");
  assert.equal(shortNote("short note"), "short note");
  const cut = shortNote("word ".repeat(100));
  assert.ok(cut.length <= 120 && cut.endsWith("…") && !cut.includes("\n"));
  assert.equal(shortNote("two\nlines  here"), "two lines here");
});
