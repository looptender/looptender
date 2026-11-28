import { makeContinuityRequest, makeContinuityReceipt, type ContinuityRequest } from "./continuity-registration-contract.ts";
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

const replyFor = (input: { request: ContinuityRequest }, inspection = summary) => ({ receipt: makeContinuityReceipt(input.request, "child_closed"), inspection });
function settle(store: ContinuityRegistryStore, hostId: string, attempt: string) {
  const p = store.read().pending.find(x => x.hostId === hostId && x.attempt === attempt);
  return store.settled(hostId, attempt, makeContinuityReceipt(p?.kind === "receipt" ? p.request : makeContinuityRequest(store.read().namespace, attempt, entry()), "child_closed"));
}
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
    assert.throws(() => settle(a, identity.hostId, "00000000-0000-4000-8000-000000000002"));
    assert.equal(a.detach(1, "work_example", 1), 2); assert.throws(() => a.attach(2, entry()));
    const e = entry(); e.binding.revision = 2; assert.equal(a.attach(2, e), 3);
    settle(b, identity.hostId, attempt); assert.equal(a.read().pending.length, 0);
    assert.throws(() => settle(a, identity.hostId, attempt));
    assert.throws(() => a.attach(3, { ...e, extra: true }));
  } finally { await harness.lifecycle.dispose(); }
});

test("actual SDK CLI attach -> check -> minimized status -> exact next-session pointer, skills retained", async () => {
  let calls = 0;
  const { bb, harness } = createFakePluginHost({ pluginId: "keepgoing", agentSkillIds: ["keepgoing"], settings: { configs: "" }, sdk: sdk(),
    experimental_callHostRpc: async ({ method, input, hostId }) => {
      assert.equal(method, "continuityCheckV2"); assert.equal(hostId, identity.hostId); calls++;
      return replyFor(input as any);
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
    const handle = registerContinuity(bb, async input => { calls++; return replyFor(input); }, () => () => {});
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
        begin(...a) { if (scenario === "begin-write") throw Error("private failure"); const result = base.begin(...a); if (scenario === "prepared") throw Error("crash before dispatch"); return result; },
        settled(...a) { if (scenario === "settlement-write") throw Error("private database failure"); base.settled(...a); } };
      const handle = registerContinuity(bb, async input => { calls++; return replyFor(input); }, () => () => {}, fault);
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
      assert.equal(method, "continuityCheckV2"); calls++;
      return hostEntry.handlers.continuityCheckV2(input as any, { signal: new AbortController().signal, experimental_paths: { dataDir: dir } } as any);
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
      let calls = 0, firstAttempt = "", firstInput: any, exit!: (id: string) => void;
      const handle = registerContinuity(bb, async input => {
        calls++;
        if (calls === 1) { firstAttempt = input.request.attempt; firstInput = input; return reply.promise; }
        return replyFor(input, { ...summary, registerRevision: 2 });
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
      reply.resolve(replyFor(firstInput));
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
    const handle = registerContinuity(bb, async (input, options) => { capturedSignal = options.signal; calls++; return replyFor(input); }, () => () => {}, store);
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
    const handle = registerContinuity(bb, async input => { oldAttempt = input.request.attempt; return replyFor(input); }, () => () => {}, a);
    await handle.cli(args()); await flushedUntil(() => reads === 3);
    assert.equal(a.read().pending.length, 0);
    b.begin(1, entry(), next);
    gate.reject(Error("private metadata failure"));
    await flushedUntil(() => handle.status().enrollments[0]?.observation?.phase === "unavailable");
    assert.equal(a.read().pending.length, 1); assert.equal(a.read().pending[0].attempt, next);
    assert.throws(() => settle(a, identity.hostId, oldAttempt));
    settle(b, identity.hostId, next);
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
      let handle = registerContinuity(bb, async input => { calls++; return replyFor(input); }, () => () => {}, fault);
      await handle.cli(args()); await flushedUntil(() => outcome === "rollback" ? handle.status().enrollments[0]?.automaticCheck === "unavailable_pending_or_unknown" : handle.status().enrollments[0]?.observation?.phase === "available");
      assert.equal(store.read().pending.length, outcome === "rollback" ? 1 : 0);
      assert.equal(handle.status().enrollments[0].automaticCheck, outcome === "rollback" ? "unavailable_pending_or_unknown" : "eligible");
      const reloaded = await harness.lifecycle.reload(newBB => {
        handle = registerContinuity(newBB, async input => { calls++; return replyFor(input); }, () => () => {});
      });
      currentHarness = reloaded.harness;
      await flushedUntil(() => outcome === "rollback" ? handle.status().enrollments[0]?.observation?.phase === "unavailable" : calls === 2);
      assert.equal(calls, outcome === "rollback" ? 1 : 2, "exact consumed receipt resolves ambiguous acknowledgement");
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
      const reply: any = replyFor(input);
      if (variant === "wrong-attempt") reply.receipt.request.attempt = "00000000-0000-4000-8000-000000000001";
      if (variant === "missing-settled") delete reply.receipt;
      if (variant === "false-settled") reply.receipt.settlement = "unknown";
      if (variant === "extra") reply.private = "private fixture";
      if (variant === "invalid-inspection") reply.inspection = { ...summary, objectiveComplete: true };
      if (variant === "reject") gate.reject(Error("private transport")); else gate.resolve(reply);
      await flush(); await flush();
      assert.equal(store.read().pending[0].attempt, input.request.attempt);
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
      if (++calls > 1) return replyFor(input);
      const result = await hostEntry.handlers.continuityCheckV2(input, { signal: options.signal, experimental_paths: { dataDir: dir } } as any);
      closed.resolve(result); await deliver.promise; return result;
    }, () => () => {}, store);
    await handle.cli(args(0, e)); const actual = await closed.promise;
    assert.equal(actual.receipt.settlement, "child_closed"); assert.equal(actual.inspection.verdict, "reconciliation_required");
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
    const handle = registerContinuity(bb, async input => (replyFor(input)), () => () => {}, a);
    await handle.cli(args()); await flushedUntil(() => handle.status().enrollments[0]?.observation?.phase === "unavailable");
    assert.equal(a.read().pending.length, 1); assert.equal(a.read().pending[0].attempt, next);
    settle(b, identity.hostId, next);
    assert.equal(handle.status().enrollments[0].automaticCheck, "eligible");
  } finally { await harness.lifecycle.dispose(); }
});

test("timeout is not physical settlement; a later exact settled reply releases without automatic retry", async t => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", sdk: sdk() });
  const reply = deferred<unknown>(); let attempt = "", firstInput: any, calls = 0;
  try {
    const store = createContinuityRegistry(bb.storage.database());
    const handle = registerContinuity(bb, async input => { attempt = input.request.attempt; firstInput = input; calls++; return reply.promise; }, () => () => {}, store);
    await handle.cli(args()); await flushedUntil(() => calls === 1);
    t.mock.timers.tick(15_001); await flush();
    assert.equal(store.read().pending[0].attempt, attempt);
    assert.equal(handle.status().enrollments[0].observation?.reason, "inspection_timeout");
    reply.resolve(replyFor(firstInput));
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
    assert.throws(() => settle(a, e.binding.hostId, first)); assert.deepEqual(a.read(), before);
    settle(a, identity.hostId, first);
    const consumed = b.read(); assert.deepEqual(consumed, { ...before, consumed: [makeContinuityReceipt((before.pending[0] as any).request, "child_closed")], pending: [before.pending[1]] });
    assert.throws(() => settle(a, identity.hostId, first));
  } finally { await harness.lifecycle.dispose(); }
});

test("explicit receipt reconciliation retires the exact lost flight and dirty same-host bindings without redispatch", async () => {
  const { bb, harness } = createFakePluginHost({ pluginId: "example-recovery", sdk: { ...sdk(), threads: { get: async ({threadId}: any) => ({ ...thread(), id: threadId }) } } });
  const old = deferred<unknown>(); let calls = 0, input: any;
  try {
    const store = createContinuityRegistry(bb.storage.database());
    const handle = registerContinuity(bb, async r => { calls++; if (calls === 1) { input = r; return old.promise; } return replyFor(r); }, () => () => {}, store,
      async request => ({ state: "settled", receipt: makeContinuityReceipt(request, "child_closed") }));
    await handle.cli(args()); await until(() => calls === 1);
    const second = entry(); second.binding.id = "work_second"; second.binding.threadId = "thr_second";
    await handle.cli(args(1, second));
    await harness.behavior.emitThreadEvent("thread.idle", { thread: thread(), lastAssistantText: null });
    await harness.behavior.emitThreadEvent("thread.idle", { thread: { ...thread(), id: "thr_second" }, lastAssistantText: null });
    const original = store.read().pending[0]; assert.equal(original.kind, "receipt");
    const info = JSON.parse((await handle.cli(["attempt-status", identity.hostId, original.attempt])).stdout);
    assert.equal(info.state, "receipt_available"); assert.equal(calls, 1); assert.equal(store.read().pending.length, 1);
    const apply = ["reconcile", identity.hostId, original.attempt, info.requestDigest, info.receiptDigest];
    assert.equal((await handle.cli([...apply.slice(0,3), "f".repeat(64), info.receiptDigest])).exitCode, 1);
    assert.equal((await handle.cli(apply)).exitCode, 0); await tick(); await tick();
    assert.equal(calls, 1, "retirement must suppress all queued same-host checks"); assert.equal(store.read().pending.length, 0);
    assert.ok(handle.status().enrollments.every(e => e.observation?.phase !== "available"));
    old.resolve(replyFor(input)); await tick(); assert.equal(calls, 1, "late original reply cannot replay or publish");
    assert.equal((await handle.cli(apply)).exitCode, 0); assert.equal(store.read().consumed.length, 1);
    await handle.cli(["check", second.binding.id, "1"]); await until(() => calls === 2);
    await until(() => handle.status().enrollments.find(e => e.binding.id === second.binding.id)?.observation?.phase === "available");
  } finally { await harness.lifecycle.dispose(); }
});

test("reconcile handles rollback and committed-but-unacknowledged consumption without rerunning helper", async () => {
  for (const phase of ["rollback", "after-commit", "after-commit-read-failure"] as const) {
    const { bb, harness } = createFakePluginHost({ pluginId: "example-recovery", sdk: sdk() });
    try {
      const base = createContinuityRegistry(bb.storage.database()); let calls = 0, fault = true, failedRead = false;
      const store: ContinuityRegistryStore = { ...base, settled(...a) {
        if (!fault) return base.settled(...a);
        if (phase !== "rollback") base.settled(...a);
        throw Error("fictional acknowledgement");
      }, consumed(r) { if (phase === "after-commit-read-failure" && !failedRead) { failedRead = true; throw Error(); } return base.consumed(r); } };
      const handle = registerContinuity(bb, async () => { calls++; throw Error("lost reply"); }, () => () => {}, store,
        async r => ({ state: "settled", receipt: makeContinuityReceipt(r, "child_closed") }));
      await handle.cli(args()); await until(() => calls === 1); await tick();
      const p = base.read().pending[0]; assert.equal(p.kind, "receipt"); if (p.kind !== "receipt") assert.fail();
      const r = makeContinuityReceipt(p.request, "child_closed"), argv = ["reconcile", p.hostId, p.attempt, p.request.requestDigest, r.receiptDigest];
      assert.equal((await handle.cli(argv)).exitCode, phase === "after-commit" ? 0 : 1);
      assert.equal(base.read().pending.length, phase === "rollback" ? 1 : 0);
      fault = false; assert.equal((await handle.cli(argv)).exitCode, 0); await tick();
      assert.equal(calls, 1); assert.equal(base.read().consumed.length, 1);
      assert.equal(handle.status().enrollments[0].automaticCheck, "eligible");
    } finally { await harness.lifecycle.dispose(); }
  }
});

test("legacy migration preserves opaque token and cannot manufacture a receipt binding", async () => {
  const { bb, harness } = createFakePluginHost({ pluginId: "example-legacy", sdk: sdk() });
  try {
    const db = bb.storage.database(), current = createContinuityRegistry(db); current.attach(0, entry());
    const state = current.read(), pending = [{ hostId: identity.hostId, attempt: "00000000-0000-4000-8000-000000000001" }];
    db.prepare("UPDATE continuity_registration_v1 SET value=? WHERE id=1").run(JSON.stringify({ version: 1, epoch: state.epoch, enrollments: state.enrollments, history: state.history, pending }));
    const migrated = createContinuityRegistry(db); assert.equal(migrated.read().version, 2);
    assert.deepEqual(migrated.read().pending, pending.map(p => ({ ...p, kind: "legacy" })));
    assert.deepEqual(migrated.read().history, state.history); assert.deepEqual(migrated.read().enrollments, state.enrollments);
    const handle = registerContinuity(bb, async () => assert.fail("legacy must not dispatch"), () => () => {}, migrated, async () => assert.fail("legacy has no lookup binding"));
    const before = migrated.read();
    assert.equal(JSON.parse((await handle.cli(["attempt-status", identity.hostId, pending[0].attempt])).stdout).state, "legacy_receipt_unavailable");
    assert.equal((await handle.cli(["reconcile", identity.hostId, pending[0].attempt, "a".repeat(64), "b".repeat(64)])).exitCode, 1);
    assert.deepEqual(migrated.read(), before);
  } finally { await harness.lifecycle.dispose(); }
});

test("actual host persists terminal before lost reply and explicit lookup/apply preserves fictional WORK", { skip: process.platform === "win32" }, async () => {
  const dir = await realpath(await mkdtemp(join(tmpdir(), "example-receipt-live-fixture-")));
  const e = entry(); e.paths.work = join(dir, "WORK.md");
  const value = { version: 1, id: "example", revision: 1, binding: null, assignmentRecord: null, assignmentRevision: null,
    scopeComplete: false, workSources: [], authoritySources: [], items: [] };
  await writeFile(e.paths.work, '# Fictional\n```looptender-work\n' + JSON.stringify(value) + '\n```\n', { mode: 0o600 });
  const before = await readFile(e.paths.work), { bb, harness } = createFakePluginHost({ pluginId: "example-receipt", sdk: sdk() });
  let calls = 0, receipt: any;
  const ctx = { signal: new AbortController().signal, experimental_paths: { dataDir: dir } } as any;
  try {
    const store = createContinuityRegistry(bb.storage.database());
    const handle = registerContinuity(bb, async input => {
      calls++; const running = hostEntry.handlers.continuityCheckV2(input, ctx);
      await assert.rejects(async () => hostEntry.handlers.continuityCheckV2(input, ctx), /continuity_attempt_unknown/);
      const out = await running; receipt = out.receipt;
      throw Error("fictional reply lost after durable terminal");
    }, () => () => {}, store, (r) => Promise.resolve(hostEntry.handlers.continuityAttemptStatus(r, ctx)));
    await handle.cli(args(0, e)); await until(() => !!receipt); await tick();
    assert.equal(receipt.settlement, "child_closed"); assert.equal(store.read().pending.length, 1);
    const r = receipt.request;
    assert.equal((await handle.cli(["reconcile", r.binding.hostId, r.attempt, r.requestDigest, receipt.receiptDigest])).exitCode, 0);
    await tick(); assert.equal(calls, 1); assert.equal(store.read().pending.length, 0); assert.equal(store.read().consumed.length, 1);
    assert.deepEqual(await readFile(e.paths.work), before);
    const duplicate = await hostEntry.handlers.continuityCheckV2({ request: r, enrollment: e }, ctx);
    assert.deepEqual(duplicate.receipt, receipt); assert.equal(duplicate.inspection.verdict, "cannot_assess", "duplicate cannot supply a new observation");
  } finally { await harness.lifecycle.dispose(); await rm(dir, { recursive: true, force: true }); }
});

test("mismatched receipt identities refuse without changing original pending or history", async () => {
  for (const change of ["namespace", "attempt", "host", "tuple", "helper", "digest", "settlement", "extra"] as const) {
    const { bb, harness } = createFakePluginHost({ pluginId: "example-receipt-identity", sdk: sdk() });
    try {
      const store = createContinuityRegistry(bb.storage.database());
      const handle = registerContinuity(bb, async () => { throw Error(); }, () => () => {}, store, async r => {
        const receipt: any = makeContinuityReceipt(r, "child_closed");
        if (change === "namespace" || change === "attempt") receipt.request[change] = "00000000-0000-4000-8000-000000000099";
        if (change === "host") receipt.request.binding.hostId = "host_foreign";
        if (change === "tuple") receipt.request.binding.threadId = "thr_foreign";
        if (change === "helper") receipt.request.helperDigest = "f".repeat(64);
        if (change === "digest") receipt.receiptDigest = "f".repeat(64);
        if (change === "settlement") receipt.settlement = "worker_exited";
        if (change === "extra") receipt.private = "fictional secret";
        return { state: "settled", receipt };
      });
      await handle.cli(args()); await until(() => store.read().pending.length === 1);
      const before = store.read(), p = before.pending[0];
      assert.equal((await handle.cli(["reconcile", p.hostId, p.attempt, "a".repeat(64), "b".repeat(64)])).exitCode, 1);
      assert.deepEqual(store.read(), before);
    } finally { await harness.lifecycle.dispose(); }
  }
});

test("valid foreign receipt cannot consume a pending request and registry capacity preserves old state", async () => {
  const { bb, harness } = createFakePluginHost({ pluginId: "example-receipt-cas", sdk: sdk() });
  try {
    const db = bb.storage.database(), store = createContinuityRegistry(db); store.attach(0, entry());
    const attempt = "00000000-0000-4000-8000-000000000001", request = store.begin(1, entry(), attempt), before = store.read();
    const other = entry(); other.binding.threadId = "thr_foreign";
    const foreign = makeContinuityReceipt(makeContinuityRequest(request.namespace, attempt, other), "child_closed");
    assert.throws(() => store.settled(identity.hostId, attempt, foreign)); assert.deepEqual(store.read(), before);
    // Capacity refusal is transactional; no pending token or consumed identity may be evicted.
    const full = { ...before, history: [...before.history] };
    while (Buffer.byteLength(JSON.stringify(full)) < 255 * 1024) full.history.push({ id: `fictional_${full.history.length}`, revision: 1, digest: "a".repeat(64) });
    full.history[full.history.length - 1].id += "x".repeat(256 * 1024 - Buffer.byteLength(JSON.stringify(full)) - 16);
    db.prepare("UPDATE continuity_registration_v1 SET value=? WHERE id=1").run(JSON.stringify(full));
    const bounded = store.read();
    assert.throws(() => store.settled(identity.hostId, attempt, makeContinuityReceipt(request, "child_closed")), /bound/);
    assert.deepEqual(store.read(), bounded);
  } finally { await harness.lifecycle.dispose(); }
});


test("old receipt after rebind cannot release or re-block a newer flight, including late rejection", async () => {
  for (const late of ["reply", "rejection"] as const) {
    const { bb, harness } = createFakePluginHost({ pluginId: "example-rebound-receipt", sdk: sdk() });
    const old = deferred<unknown>(), newer = deferred<unknown>(); let calls = 0, original: any, current: any;
    try {
      const store = createContinuityRegistry(bb.storage.database());
      const handle = registerContinuity(bb, async input => {
        calls++; if (calls === 1) { original = input; return old.promise; } current = input; return newer.promise;
      }, () => () => {}, store, async request => ({ state: "settled", receipt: makeContinuityReceipt(request, "child_closed") }));
      await handle.cli(args()); await until(() => calls === 1);
      await handle.cli(["detach", "1", "work_example", "1"]);
      const changed = entry(); changed.binding.revision = 2; changed.paths.work = "/private/example/revised-WORK.md";
      await handle.cli(args(2, changed));
      const receipt = makeContinuityReceipt(original.request, "child_closed");
      const apply = ["reconcile", identity.hostId, original.request.attempt, original.request.requestDigest, receipt.receiptDigest];
      assert.equal((await handle.cli(apply)).exitCode, 0); await tick(); assert.equal(calls, 1);
      await handle.cli(["check", "work_example", "2"]); await until(() => calls === 2);
      const pending = store.read().pending; assert.equal(pending.length, 1); assert.equal(pending[0].attempt, current.request.attempt);
      if (late === "reply") old.resolve(replyFor(original)); else old.reject(Error("late old rejection"));
      await tick(); assert.deepEqual(store.read().pending, pending); assert.equal(calls, 2);
      assert.equal((await handle.cli(apply)).exitCode, 0); assert.deepEqual(store.read().pending, pending);
      newer.resolve(replyFor(current)); await until(() => store.read().pending.length === 0);
      await until(() => handle.status().enrollments[0].observation?.phase === "available");
      assert.equal(store.read().consumed.length, 2);
    } finally { await harness.lifecycle.dispose(); }
  }
});

test("attempt identity cannot be reused for an unrelated host", async () => {
  const { bb, harness } = createFakePluginHost({ pluginId: "example-attempt-identity" });
  try {
    const store = createContinuityRegistry(bb.storage.database()), other = entry();
    other.binding.id = "work_other"; other.binding.hostId = "host_other"; other.binding.threadId = "thr_other";
    store.attach(0, entry()); store.attach(1, other);
    const attempt = "00000000-0000-4000-8000-000000000001";
    store.begin(2, entry(), attempt); const before = store.read();
    assert.throws(() => store.begin(2, other, attempt)); assert.deepEqual(store.read(), before);
  } finally { await harness.lifecycle.dispose(); }
});


test("actual cancelled-before-start host request persists only no-child receipt and changed request refuses", { skip: process.platform === "win32" }, async () => {
  const dir = await realpath(await mkdtemp(join(tmpdir(), "example-no-child-receipt-")));
  try {
    const abort = new AbortController(); abort.abort();
    const context = { signal: abort.signal, experimental_paths: { dataDir: dir } } as any;
    const request = makeContinuityRequest("00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", entry());
    const out = await hostEntry.handlers.continuityCheckV2({ request, enrollment: entry() }, context);
    assert.equal(out.receipt.settlement, "no_child_started"); assert.equal(out.inspection.verdict, "cannot_assess");
    assert.deepEqual(hostEntry.handlers.continuityAttemptStatus(request, context), { state: "settled", receipt: out.receipt });
    const other = entry(); other.paths.work = "/private/example/different.md";
    await assert.rejects(async () => hostEntry.handlers.continuityCheckV2({ request, enrollment: other }, context), /mismatch/);
    const changed = makeContinuityRequest(request.namespace, request.attempt, other);
    await assert.rejects(async () => hostEntry.handlers.continuityCheckV2({ request: changed, enrollment: other }, context), /unknown/);
    assert.deepEqual(hostEntry.handlers.continuityAttemptStatus(request, context), { state: "settled", receipt: out.receipt });
  } finally { await rm(dir, { recursive: true, force: true }); }
});
