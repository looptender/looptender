import hostEntry from "./host.ts";
import { continuityHelperBase64 } from "./continuity-host-inspector-asset.ts";
import { mkdtemp, realpath, writeFile, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { test } from "node:test";
import { getEventListeners } from "node:events";
import assert from "node:assert/strict";
import { createFakePluginHost, makeThreadResponse, makeHostResponse, makePluginAgentConfigurationContext } from "@get-bb/plugin-sdk/testing";
import { registerContinuity } from "./continuity-registration.ts";
import { createContinuityRegistry, type ContinuityRegistryStore } from "./continuity-registration-store.ts";
import plugin from "./server.ts";
const identity = { threadId: "thr_example", projectId: "project_example", environmentId: "env_example", hostId: "host_example" };
const entry = () => ({ binding: { id: "work_example", revision: 1, ...identity }, paths: { work: "/private/example/WORK.md" } });
const summary = { verdict: "reconciliation_required" as const, objectiveComplete: false as const, workSha256: "a".repeat(64), registerRevision: 1, itemCount: 10, findingCount: 2, missingInputCount: 1 };
const thread = () => makeThreadResponse({ id: identity.threadId, projectId: identity.projectId, environmentId: identity.environmentId });
const host = () => makeHostResponse({ id: identity.hostId, status: "connected" });
const environment = () => ({ id: identity.environmentId, projectId: identity.projectId, hostId: identity.hostId, status: "ready", hostLifecycle: "active", lifecycle: { phase: "active" }, isGitRepo: false, path: null });
const sdk = () => ({ threads: { get: async () => thread() }, environments: { get: async () => environment() }, hosts: { get: async () => host() } });
const args = (epoch = 0, e = entry()) => ["attach", String(epoch), e.binding.id, String(e.binding.revision), e.binding.threadId, e.binding.projectId, e.binding.environmentId, e.binding.hostId, e.paths.work];
const tick = () => new Promise(r => setTimeout(r, 15));
async function until(fn: () => boolean) { for (let i = 0; i < 100; i++) { if (fn()) return; await tick(); } assert.fail("fixture did not settle"); }
function deferred<T>() { let resolve!: (x: T) => void, reject!: (e: Error) => void; const promise = new Promise<T>((a, b) => { resolve = a; reject = b; }); return { promise, resolve, reject }; }
const context = () => makePluginAgentConfigurationContext({ thread: { id: identity.threadId }, project: { id: identity.projectId }, environment: { id: identity.environmentId }, host: { id: identity.hostId } });

test("SQLite whole registry CAS, path revisions, tombstones and guard ownership across handles", async () => {
  const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity" });
  try {
    const a = createContinuityRegistry(bb.storage.database()), b = createContinuityRegistry(bb.storage.database());
    assert.equal(a.attach(0, entry()), 1); assert.throws(() => b.attach(0, entry()));
    for (const key of ["work", "context", "assignments", "previous"] as const) {
      const e = entry(); Object.assign(e.paths, { [key]: "/private/example/changed" }); assert.throws(() => a.attach(1, e));
    }
    const large = entry(); large.paths.work = "/" + "a".repeat(3500); assert.throws(() => a.attach(1, large));
    assert.equal(a.read().epoch, 1);
    const attempt = "00000000-0000-4000-8000-000000000001"; a.begin(1, entry(), attempt);
    assert.throws(() => b.begin(1, entry(), "00000000-0000-4000-8000-000000000002"));
    assert.throws(() => a.settled(identity.hostId, "00000000-0000-4000-8000-000000000002"));
    assert.equal(a.detach(1, "work_example", 1), 2); assert.throws(() => a.attach(2, entry()));
    const e = entry(); e.binding.revision = 2; assert.equal(a.attach(2, e), 3);
    b.settled(identity.hostId, attempt); assert.equal(a.read().pending.length, 0);
    assert.throws(() => a.settled(identity.hostId, attempt));
    assert.throws(() => a.attach(3, { ...e, extra: true }));
  } finally { await harness.lifecycle.dispose(); }
});

test("actual SDK CLI attach -> check -> minimized status -> exact next-session pointer, skills retained", async () => {
  let calls = 0;
  const { bb, harness } = createFakePluginHost({ pluginId: "keepgoing", agentSkillIds: ["keepgoing"], settings: { configs: "" }, sdk: sdk(),
    experimental_callHostRpc: async ({ method, input, hostId }) => {
      assert.equal(method, "continuityCheck"); assert.equal(hostId, identity.hostId); calls++;
      return { attempt: (input as any).attempt, settled: true, inspection: summary };
    } });
  try {
    await plugin(bb);
    assert.equal((await harness.behavior.runCli(["continuity", ...args()])).exitCode, 0);
    await until(() => calls === 1); await tick();
    const out = await harness.behavior.runCli(["continuity", "status"]), state = JSON.parse(out.stdout);
    assert.equal(state.enrollments[0].observation.phase, "available"); assert.deepEqual(state.enrollments[0].observation.summary, summary);
    assert.ok(!out.stdout.includes("/private")); assert.deepEqual(state.enrollments[0].binding, entry().binding);
    const selected = await harness.behavior.resolveAgentConfiguration(context()); assert.deepEqual(selected.skills, ["keepgoing"]); assert.ok(selected.instructions?.includes(entry().paths.work));
    for (const dimension of ["thread", "project", "environment", "host"] as const) {
      const ctx = context(); ctx[dimension].id = "foreign_example";
      assert.equal((await harness.behavior.resolveAgentConfiguration(ctx)).instructions, null);
    }
    assert.equal((await harness.behavior.runCli(["continuity", "check", "work_example", "1"])).exitCode, 0); await until(() => calls === 2);
    assert.equal((await harness.behavior.runCli(["continuity", "detach", "1", "work_example", "1"])).exitCode, 0);
    assert.equal((await harness.behavior.resolveAgentConfiguration(context())).instructions, null);
  } finally { await harness.lifecycle.dispose(); }
});

test("unknown transport persists CHECK quarantine through worker exit, detach/rebind and reload without blocking pointers", async () => {
  let calls = 0, exit!: (hostId: string) => void;
  const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", agentSkillIds: ["keepgoing"], sdk: sdk() });
  let currentHarness = harness;
  try {
    const store = createContinuityRegistry(bb.storage.database());
    let handle = registerContinuity(bb, async () => { calls++; throw new Error("private transport text"); }, listener => { exit = listener; return () => {}; }, store);
    assert.equal((await handle.cli(args())).exitCode, 0); await until(() => calls === 1); await tick();
    assert.equal(store.read().pending.length, 1); exit(identity.hostId);
    assert.ok(JSON.stringify(handle.status()).includes("unavailable_pending_or_unknown"));
    assert.equal((await handle.cli(["detach", "1", "work_example", "1"])).exitCode, 0);
    const e = entry(); e.binding.id = "replacement_example"; e.binding.revision = 2;
    assert.equal((await handle.cli(args(2, e))).exitCode, 0); await tick(); assert.equal(calls, 1);
    assert.ok((await currentHarness.behavior.resolveAgentConfiguration(context())).instructions?.includes("WORK.md"));
    const reloaded = await harness.lifecycle.reload(newBB => { handle = registerContinuity(newBB, async () => { calls++; return {}; }, () => () => {}); });
    currentHarness = reloaded.harness; await tick();
    assert.equal(calls, 1); assert.equal((await handle.cli(["check", "replacement_example", "2"])).exitCode, 0); await tick(); assert.equal(calls, 1);
    assert.ok((await currentHarness.behavior.resolveAgentConfiguration(context())).instructions?.includes("WORK.md"));
    assert.ok(!JSON.stringify(handle.status()).includes("private transport text"));
  } finally { await currentHarness.lifecycle.dispose(); }
});

test("synchronous invalidation precedes slow tuple reads; superseded resolver cannot restore old evidence", async () => {
  const gate = deferred<ReturnType<typeof thread>>(); let slow = false, calls = 0;
  const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", agentSkillIds: ["keepgoing"], sdk: { ...sdk(), threads: { get: async () => slow ? gate.promise : thread() } } });
  try {
    const handle = registerContinuity(bb, async input => { calls++; return { attempt: input.attempt, settled: true, inspection: summary }; }, () => () => {});
    assert.equal((await handle.cli(args())).exitCode, 0); await until(() => handle.status().enrollments[0]?.observation?.phase === "available");
    slow = true;
    await harness.behavior.emitThreadEvent("thread.idle", { thread: thread(), lastAssistantText: "ignored private prose" });
    assert.equal(handle.status().enrollments[0].observation?.phase, "checking");
    await harness.behavior.emitThreadEvent("thread.deleted", { thread: thread() });
    gate.resolve(thread()); await tick();
    assert.equal(handle.status().enrollments[0].observation?.phase, "withdrawn"); assert.equal(calls, 1);
  } finally { await harness.lifecycle.dispose(); }
});

test("atomic prepared guard survives crash-before-dispatch and settlement persistence failure", async () => {
  for (const scenario of ["prepared", "settlement-write", "begin-write"] as const) {
    const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", agentSkillIds: ["keepgoing"], sdk: sdk() });
    try {
      const base = createContinuityRegistry(bb.storage.database()); let calls = 0;
      const fault: ContinuityRegistryStore = { ...base,
        begin(...a) { if (scenario === "begin-write") throw Error("private failure"); base.begin(...a); if (scenario === "prepared") throw Error("crash before dispatch"); },
        settled(...a) { if (scenario === "settlement-write") throw Error("private database failure"); base.settled(...a); } };
      const handle = registerContinuity(bb, async input => { calls++; return { attempt: input.attempt, settled: true, inspection: summary }; }, () => () => {}, fault);
      assert.equal((await handle.cli(args())).exitCode, 0); await tick();
      assert.equal(calls, scenario === "prepared" || scenario === "begin-write" ? 0 : 1);
      assert.equal(base.read().pending.length, scenario === "begin-write" ? 0 : 1);
      assert.notEqual(handle.status().enrollments[0].observation?.phase, "available");
    } finally { await harness.lifecycle.dispose(); }
  }
});

test("new attachment lifecycle changes during metadata reads refuse commit", async () => {
  const gate = deferred<ReturnType<typeof thread>>(); let reads = 0;
  const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", agentSkillIds: ["keepgoing"], sdk: { ...sdk(), threads: { get: async () => { reads++; return gate.promise; } } } });
  try {
    const handle = registerContinuity(bb, async () => { assert.fail("no dispatch before enrollment"); }, () => () => {});
    const attachment = handle.cli(args()); await until(() => reads === 1);
    await harness.behavior.emitThreadEvent("thread.deleted", { thread: thread() });
    gate.resolve(thread()); assert.equal((await attachment).exitCode, 1); assert.equal(handle.status().epoch, 0);
    assert.equal(handle.status().enrollments.length, 0);
  } finally { await harness.lifecycle.dispose(); }
});

test("malformed or uncorrelated host replies retain quarantine, never expose private diagnostics", async () => {
  for (const response of [null, { attempt: "00000000-0000-4000-8000-000000000001", settled: true, inspection: summary },
    { settled: true, inspection: { ...summary, private: "private fixture diagnostic" } }]) {
    let calls = 0;
    const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", agentSkillIds: ["keepgoing"], sdk: sdk() });
    try {
      const handle = registerContinuity(bb, async () => { calls++; return response; }, () => () => {});
      assert.equal((await handle.cli(args())).exitCode, 0); await until(() => calls === 1); await tick();
      assert.equal(handle.status().enrollments[0].automaticCheck, "unavailable_pending_or_unknown");
      assert.ok(!JSON.stringify(handle.status()).includes("private fixture diagnostic"));
      await handle.cli(["check", "work_example", "1"]); await tick(); assert.equal(calls, 1);
    } finally { await harness.lifecycle.dispose(); }
  }
});

test("whole persisted value is bounded and malformed storage never yields a partial pointer", async () => {
  const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", agentSkillIds: ["keepgoing"], sdk: sdk() });
  try {
    const db = bb.storage.database(), store = createContinuityRegistry(db); store.attach(0, entry());
    for (const raw of ['{"version":1}', JSON.stringify({ version: 1, epoch: 1, enrollments: [entry()], history: [], pending: [] }), ' '.repeat(256 * 1024 + 1)]) {
      db.prepare("UPDATE continuity_registration_v1 SET value=? WHERE id=1").run(raw);
      assert.throws(() => store.read());
    }
    const handle = registerContinuity(bb, async () => assert.fail("invalid registry cannot dispatch"), () => () => {});
    assert.equal((await handle.cli(["status"])).exitCode, 1);
    assert.equal((await harness.behavior.resolveAgentConfiguration(context())).instructions, null);
  } finally { await harness.lifecycle.dispose(); }
});


test("real SDK enrollment -> real host handler -> packaged Python -> lifecycle status is read-only", { skip: process.platform === "win32" }, async () => {
  const dir = await realpath(await mkdtemp(join(tmpdir(), "example-registration-")));
  const e = entry(); e.paths.work = join(dir, "Fictional WORK.md");
  let calls = 0;
  const { bb, harness } = createFakePluginHost({ pluginId: "keepgoing", agentSkillIds: ["keepgoing"], settings: { configs: "" }, sdk: sdk(),
    experimental_callHostRpc: async ({ method, input }) => {
      assert.equal(method, "continuityCheck"); calls++;
      return hostEntry.handlers.continuityCheck(input as any, { signal: new AbortController().signal } as any);
    } });
  try {
    const helper = join(dir, "fixture.pyz"); await writeFile(helper, Buffer.from(continuityHelperBase64, "base64"), { mode: 0o600 });
    const init = spawnSync("python3", ["-I", "-B", helper, "init", "--work", e.paths.work, "--id", "example"], { encoding: "utf8" });
    assert.equal(init.status, 0, init.stderr); const before = await readFile(e.paths.work);
    await plugin(bb);
    assert.equal((await harness.behavior.runCli(["continuity", ...args(0, e)])).exitCode, 0);
    const state = async () => JSON.parse((await harness.behavior.runCli(["continuity", "status"])).stdout);
    let result: any;
    for (let i = 0; i < 100; i++) { result = await state(); if (result.enrollments[0]?.observation?.phase === "available") break; await tick(); }
    assert.equal(result.enrollments[0].observation.phase, "available");
    assert.equal(result.enrollments[0].observation.summary.verdict, "reconciliation_required");
    assert.equal(result.enrollments[0].observation.summary.objectiveComplete, false);
    assert.equal(result.enrollments[0].automaticCheck, "eligible"); assert.equal(calls, 1);
    await harness.behavior.emitThreadEvent("thread.idle", { thread: thread(), lastAssistantText: "ignored fictional completion claim" });
    await until(() => calls === 2);
    for (let i = 0; i < 100; i++) { result = await state(); if (result.enrollments[0]?.observation?.phase === "available") break; await tick(); }
    assert.equal(result.enrollments[0].observation.summary.objectiveComplete, false);
    assert.deepEqual(await readFile(e.paths.work), before);
    assert.ok((await harness.behavior.resolveAgentConfiguration(context())).instructions?.includes(e.paths.work));
    assert.ok(!JSON.stringify(result).includes(dir));
  } finally { await harness.lifecycle.dispose(); await rm(dir, { recursive: true, force: true }); }
});

test("all lifecycle handlers precede startup persistence reconciliation", async () => {
  const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", agentSkillIds: ["keepgoing"], sdk: sdk() });
  try {
    const base = createContinuityRegistry(bb.storage.database()); let read = false;
    const store: ContinuityRegistryStore = { ...base, read() {
      for (const name of ["thread.active", "thread.idle", "thread.failed", "thread.archived", "thread.deleted", "experimental_environment.removed", "experimental_host.deleted"] as const) {
        assert.equal(harness.inspection.registrations.threadEventHandlers[name], 1, name);
      }
      read = true; return base.read();
    } };
    registerContinuity(bb, async () => assert.fail("empty registry cannot dispatch"), () => () => {}, store);
    assert.equal(read, true);
  } finally { await harness.lifecycle.dispose(); }
});

const flush = () => new Promise<void>(r => setImmediate(r));
async function flushedUntil(fn: () => boolean) {
  for (let i = 0; i < 100; i++) { if (fn()) return; await flush(); }
  assert.fail("fixture did not settle");
}

test("known settlement releases original token after idle, replacement, detach or worker exit", async () => {
  for (const change of ["idle", "replace", "detach", "worker-exit"] as const) {
    const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", sdk: sdk() });
    try {
      const store = createContinuityRegistry(bb.storage.database()), reply = deferred<unknown>();
      let calls = 0, firstAttempt = "", exit!: (id: string) => void;
      const handle = registerContinuity(bb, async input => {
        calls++;
        if (calls === 1) { firstAttempt = input.attempt; return reply.promise; }
        return { attempt: input.attempt, settled: true, inspection: { ...summary, registerRevision: 2 } };
      }, fn => { exit = fn; return () => {}; }, store);
      await handle.cli(args()); await flushedUntil(() => calls === 1);
      if (change === "idle") {
        await harness.behavior.emitThreadEvent("thread.idle", { thread: thread(), lastAssistantText: null });
        await harness.behavior.emitThreadEvent("thread.active", { thread: thread() });
      } else if (change === "replace") {
        const e = entry(); e.binding.revision = 2; e.paths.work = "/private/example/replacement.md";
        assert.equal((await handle.cli(args(1, e))).exitCode, 0);
      } else if (change === "detach") await handle.cli(["detach", "1", "work_example", "1"]);
      else if (change === "worker-exit") exit(identity.hostId);
      assert.equal(store.read().pending[0].attempt, firstAttempt);
      reply.resolve({ attempt: firstAttempt, settled: true, inspection: summary });
      await flushedUntil(() => store.read().pending.length === 0);
      {
        if (change === "idle" || change === "replace") {
          await flushedUntil(() => handle.status().enrollments[0]?.observation?.phase === "available");
          assert.equal(calls, 2, "only the existing coalesced trigger dispatches");
          assert.equal(handle.status().enrollments[0].observation?.summary?.registerRevision, 2);
        } else assert.equal(calls, 1);
        assert.ok(!handle.status().enrollments.some(e => e.observation?.summary?.registerRevision === 1));
      }
    } finally { await harness.lifecycle.dispose(); }
  }
});

test("known settlement bounds never-settling logical metadata and permits coalesced current work", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  let reads = 0, calls = 0, capturedSignal!: AbortSignal;
  const hanging = deferred<ReturnType<typeof thread>>();
  const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", sdk: {
    ...sdk(), threads: { get: async () => ++reads === 3 ? hanging.promise : thread() },
  } });
  try {
    const store = createContinuityRegistry(bb.storage.database());
    const handle = registerContinuity(bb, async (input, options) => { capturedSignal = options.signal; calls++; return { attempt: input.attempt, settled: true, inspection: summary }; }, () => () => {}, store);
    await handle.cli(args()); await flushedUntil(() => reads === 3);
    assert.equal(store.read().pending.length, 0);
    assert.equal(getEventListeners(capturedSignal, "abort").length, 1);
    const oldSignal = capturedSignal;
    await harness.behavior.emitThreadEvent("thread.idle", { thread: thread(), lastAssistantText: null });
    await harness.behavior.emitThreadEvent("thread.active", { thread: thread() });
    assert.equal(calls, 1);
    t.mock.timers.tick(15_001);
    await flushedUntil(() => calls === 2);
    await flushedUntil(() => handle.status().enrollments[0]?.observation?.phase === "available");
    assert.equal(calls, 2); assert.equal(store.read().pending.length, 0);
    assert.equal(getEventListeners(oldSignal, "abort").length, 0);
    assert.equal(getEventListeners(capturedSignal, "abort").length, 0);
    hanging.reject(Error("private late metadata")); await flush(); await flush();
    assert.equal(handle.status().enrollments[0].automaticCheck, "eligible");
    assert.equal(handle.status().enrollments[0].observation?.phase, "available");
  } finally { await harness.lifecycle.dispose(); }
});

test("known settlement metadata failure cannot block or delete a newer token from another handle", async () => {
  let reads = 0, oldAttempt = "";
  const gate = deferred<ReturnType<typeof thread>>();
  const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", sdk: {
    ...sdk(), threads: { get: async () => ++reads === 3 ? gate.promise : thread() },
  } });
  try {
    const a = createContinuityRegistry(bb.storage.database()), b = createContinuityRegistry(bb.storage.database());
    const next = "00000000-0000-4000-8000-000000000002";
    const handle = registerContinuity(bb, async input => { oldAttempt = input.attempt; return { attempt: input.attempt, settled: true, inspection: summary }; }, () => () => {}, a);
    await handle.cli(args()); await flushedUntil(() => reads === 3);
    assert.equal(a.read().pending.length, 0);
    b.begin(1, entry(), next);
    gate.reject(Error("private metadata failure"));
    await flushedUntil(() => handle.status().enrollments[0]?.observation?.phase === "unavailable");
    assert.deepEqual(a.read().pending, [{ hostId: identity.hostId, attempt: next }]);
    assert.throws(() => a.settled(identity.hostId, oldAttempt));
    b.settled(identity.hostId, next);
    assert.equal(handle.status().enrollments[0].automaticCheck, "eligible");
  } finally { await harness.lifecycle.dispose(); }
});

test("known settlement distinguishes rollback from ambiguous post-commit removal and fences this instance", async () => {
  for (const outcome of ["rollback", "post-commit"] as const) {
    const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", sdk: sdk() });
    let currentHarness = harness, calls = 0;
    try {
      const store = createContinuityRegistry(bb.storage.database());
      const fault: ContinuityRegistryStore = { ...store, settled(...a) {
        if (outcome === "post-commit") store.settled(...a);
        throw Error("private acknowledgement failure");
      } };
      let handle = registerContinuity(bb, async input => { calls++; return { attempt: input.attempt, settled: true, inspection: summary }; }, () => () => {}, fault);
      await handle.cli(args()); await flushedUntil(() => handle.status().enrollments[0]?.observation?.phase === "unavailable");
      assert.equal(store.read().pending.length, outcome === "rollback" ? 1 : 0);
      assert.equal(handle.status().enrollments[0].automaticCheck, "unavailable_pending_or_unknown");
      await handle.cli(["check", "work_example", "1"]); await flush(); await flush(); assert.equal(calls, 1);
      const reloaded = await harness.lifecycle.reload(newBB => {
        handle = registerContinuity(newBB, async input => { calls++; return { attempt: input.attempt, settled: true, inspection: summary }; }, () => () => {});
      });
      currentHarness = reloaded.harness;
      await flushedUntil(() => outcome === "rollback" ? handle.status().enrollments[0]?.observation?.phase === "unavailable" : calls === 2);
      assert.equal(calls, outcome === "rollback" ? 1 : 2, "ambiguous commit has no durable reload fence guarantee");
    } finally { await currentHarness.lifecycle.dispose(); }
  }
});

test("physical unknown replies never release token even when freshness changes", async () => {
  for (const variant of ["wrong-attempt", "missing-settled", "false-settled", "extra", "invalid-inspection", "reject"] as const) {
    const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", sdk: sdk() });
    try {
      const store = createContinuityRegistry(bb.storage.database()), gate = deferred<unknown>(); let input: any, calls = 0;
      const handle = registerContinuity(bb, async value => { input = value; calls++; return gate.promise; }, () => () => {}, store);
      await handle.cli(args()); await flushedUntil(() => calls === 1);
      await harness.behavior.emitThreadEvent("thread.idle", { thread: thread(), lastAssistantText: null });
      const reply: any = { attempt: input.attempt, settled: true, inspection: summary };
      if (variant === "wrong-attempt") reply.attempt = "00000000-0000-4000-8000-000000000001";
      if (variant === "missing-settled") delete reply.settled;
      if (variant === "false-settled") reply.settled = false;
      if (variant === "extra") reply.private = "private fixture";
      if (variant === "invalid-inspection") reply.inspection = { ...summary, objectiveComplete: true };
      if (variant === "reject") gate.reject(Error("private transport")); else gate.resolve(reply);
      await flush(); await flush();
      assert.equal(store.read().pending[0].attempt, input.attempt);
      assert.equal(handle.status().enrollments[0].automaticCheck, "unavailable_pending_or_unknown");
      assert.equal(calls, 1);
    } finally { await harness.lifecycle.dispose(); }
  }
});

test("actual packaged child close survives lifecycle supersession without accepting its stale summary", { skip: process.platform === "win32" }, async () => {
  const dir = await realpath(await mkdtemp(join(tmpdir(), "example-settled-host-")));
  const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", sdk: sdk() });
  const closed = deferred<any>(), deliver = deferred<void>(); let calls = 0;
  try {
    const e = entry(); e.paths.work = join(dir, "Fictional WORK.md");
    const value = { version: 1, id: "example", revision: 1, binding: null, assignmentRecord: null, assignmentRevision: null,
      scopeComplete: false, workSources: [], authoritySources: [], items: [] };
    await writeFile(e.paths.work, '# Fictional\n```looptender-work\n' + JSON.stringify(value) + '\n```\n', { mode: 0o600 });
    const before = await readFile(e.paths.work), store = createContinuityRegistry(bb.storage.database());
    const handle = registerContinuity(bb, async (input, options) => {
      assert.equal(options.hostId, identity.hostId);
      if (++calls > 1) return { attempt: input.attempt, settled: true, inspection: summary };
      const result = await hostEntry.handlers.continuityCheck(input, { signal: options.signal } as any);
      closed.resolve(result); await deliver.promise; return result;
    }, () => () => {}, store);
    await handle.cli(args(0, e)); const actual = await closed.promise;
    assert.equal(actual.settled, true); assert.equal(actual.inspection.verdict, "reconciliation_required");
    assert.equal(store.read().pending.length, 1, "not released until the correlated response arrives");
    await harness.behavior.emitThreadEvent("thread.active", { thread: thread() });
    await harness.behavior.emitThreadEvent("thread.idle", { thread: thread(), lastAssistantText: null });
    deliver.resolve();
    await until(() => handle.status().enrollments[0]?.observation?.phase === "available");
    assert.equal(calls, 2); assert.equal(store.read().pending.length, 0);
    assert.equal(handle.status().enrollments[0].observation?.summary?.itemCount, summary.itemCount);
    assert.deepEqual(await readFile(e.paths.work), before);
  } finally { await harness.lifecycle.dispose(); await rm(dir, { recursive: true, force: true }); }
});

test("known settlement post-metadata reentrancy preserves a newly acquired host token", async () => {
  let reads = 0, b!: ContinuityRegistryStore;
  const next = "00000000-0000-4000-8000-000000000002";
  const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", sdk: {
    ...sdk(), threads: { get: async () => {
      if (++reads === 3) { b.begin(1, entry(), next); throw Error("private reentrant metadata failure"); }
      return thread();
    } },
  } });
  try {
    const a = createContinuityRegistry(bb.storage.database()); b = createContinuityRegistry(bb.storage.database());
    const handle = registerContinuity(bb, async input => ({ attempt: input.attempt, settled: true, inspection: summary }), () => () => {}, a);
    await handle.cli(args()); await flushedUntil(() => handle.status().enrollments[0]?.observation?.phase === "unavailable");
    assert.deepEqual(a.read().pending, [{ hostId: identity.hostId, attempt: next }]);
    b.settled(identity.hostId, next);
    assert.equal(handle.status().enrollments[0].automaticCheck, "eligible");
  } finally { await harness.lifecycle.dispose(); }
});

test("timeout is not physical settlement; a later exact settled reply releases without automatic retry", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", sdk: sdk() });
  const reply = deferred<unknown>(); let attempt = "", calls = 0;
  try {
    const store = createContinuityRegistry(bb.storage.database());
    const handle = registerContinuity(bb, async input => { attempt = input.attempt; calls++; return reply.promise; }, () => () => {}, store);
    await handle.cli(args()); await flushedUntil(() => calls === 1);
    t.mock.timers.tick(15_001); await flush();
    assert.equal(store.read().pending[0].attempt, attempt);
    assert.equal(handle.status().enrollments[0].observation?.reason, "inspection_timeout");
    reply.resolve({ attempt, settled: true, inspection: summary });
    await flushedUntil(() => store.read().pending.length === 0); await flush();
    assert.equal(calls, 1); assert.equal(handle.status().enrollments[0].automaticCheck, "eligible");
    assert.notEqual(handle.status().enrollments[0].observation?.phase, "available");
  } finally { await harness.lifecycle.dispose(); }
});

test("exact settled removal preserves unrelated epoch, enrollment, history and host tokens", async () => {
  const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", sdk: sdk() });
  try {
    const a = createContinuityRegistry(bb.storage.database()), b = createContinuityRegistry(bb.storage.database());
    a.attach(0, entry());
    const first = "00000000-0000-4000-8000-000000000001", other = "00000000-0000-4000-8000-000000000002";
    a.begin(1, entry(), first);
    const e = entry(); e.binding.id = "other_example"; e.binding.threadId = "thr_other_example"; e.binding.hostId = "host_other_example";
    b.attach(1, e); b.begin(2, e, other);
    const before = b.read();
    assert.throws(() => a.settled(e.binding.hostId, first)); assert.deepEqual(a.read(), before);
    a.settled(identity.hostId, first);
    assert.deepEqual(b.read(), { ...before, pending: [{ hostId: e.binding.hostId, attempt: other }] });
    assert.throws(() => a.settled(identity.hostId, first));
  } finally { await harness.lifecycle.dispose(); }
});
