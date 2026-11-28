import hostEntry from "./host.ts";
import { continuityHelperBase64 } from "./continuity-host-inspector-asset.ts";
import { mkdtemp, realpath, writeFile, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { test } from "node:test";
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
    assert.throws(() => a.settled(identity.hostId, "00000000-0000-4000-8000-000000000002", 1, entry()));
    assert.equal(a.detach(1, "work_example", 1), 2); assert.throws(() => a.attach(2, entry()));
    const e = entry(); e.binding.revision = 2; assert.equal(a.attach(2, e), 3);
    assert.throws(() => a.settled(identity.hostId, attempt, 1, entry())); assert.equal(b.read().pending.length, 1);
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

test("atomic prepared guard survives crash-before-dispatch, late stale reply, and settlement persistence failure", async () => {
  for (const scenario of ["prepared", "stale", "settlement-write", "begin-write"] as const) {
    const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", agentSkillIds: ["keepgoing"], sdk: sdk() });
    try {
      const base = createContinuityRegistry(bb.storage.database()); let calls = 0;
      const reply = deferred<unknown>();
      const fault: ContinuityRegistryStore = { ...base,
        begin(...a) { if (scenario === "begin-write") throw Error("private failure"); base.begin(...a); if (scenario === "prepared") throw Error("crash before dispatch"); },
        settled(...a) { if (scenario === "settlement-write") throw Error("private database failure"); base.settled(...a); } };
      const handle = registerContinuity(bb, async input => { calls++; if (scenario === "stale") return reply.promise; return { attempt: input.attempt, settled: true, inspection: summary }; }, () => () => {}, fault);
      assert.equal((await handle.cli(args())).exitCode, 0); await tick();
      if (scenario === "stale") {
        const e = entry(); e.binding.revision = 2; e.paths.work = "/private/example/next-WORK.md";
        assert.equal((await handle.cli(args(1, e))).exitCode, 0);
        reply.resolve({ attempt: base.read().pending[0].attempt, settled: true, inspection: summary }); await tick();
      }
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

test("a physically settled but lifecycle-stale reply cannot remove its persisted guard", async () => {
  const reply = deferred<unknown>(); let input: any, calls = 0;
  const { bb, harness } = createFakePluginHost({ pluginId: "example-continuity", agentSkillIds: ["keepgoing"], sdk: sdk() });
  try {
    const store = createContinuityRegistry(bb.storage.database());
    const handle = registerContinuity(bb, async value => { input = value; calls++; return reply.promise; }, () => () => {}, store);
    await handle.cli(args()); await until(() => calls === 1);
    await harness.behavior.emitThreadEvent("thread.idle", { thread: thread(), lastAssistantText: "ignored" });
    reply.resolve({ attempt: input.attempt, settled: true, inspection: summary }); await tick();
    assert.equal(store.read().pending.length, 1); assert.equal(calls, 1);
    assert.notEqual(handle.status().enrollments[0].observation?.phase, "available");
    assert.equal(handle.status().enrollments[0].automaticCheck, "unavailable_pending_or_unknown");
  } finally { await harness.lifecycle.dispose(); }
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
