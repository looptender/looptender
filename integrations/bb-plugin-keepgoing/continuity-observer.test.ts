import { test } from "node:test";
import assert from "node:assert/strict";
import { createContinuityObserver, type ContinuityBinding, type ContinuityEvent, type ContinuityInspection,
  type ContinuityObservation, type ContinuitySummary } from "./continuity-observer.ts";

const binding = (over: Partial<ContinuityBinding> = {}): ContinuityBinding => ({
  id: "example", revision: 1, threadId: "thr_example", projectId: "proj_example", environmentId: "env_example", hostId: "host_example", ...over,
});
const summary = (over: Partial<ContinuitySummary> = {}): ContinuitySummary => ({
  verdict: "reconciliation_required", objectiveComplete: false, workSha256: "a".repeat(64), registerRevision: 3,
  itemCount: 33, findingCount: 2, missingInputCount: 1, ...over,
});
const event = (kind: ContinuityEvent["kind"] = "idle"): ContinuityEvent => ({
  threadId: "thr_example", kind, identity: { projectId: "proj_example", environmentId: "env_example", hostId: "host_example" },
});
async function flush() { for (let i = 0; i < 16; i++) await Promise.resolve(); }
function fixture() {
  const observations: ContinuityObservation[] = [];
  const calls: { binding: ContinuityBinding; signal: AbortSignal; resolve(value: ContinuityInspection): void; reject(error: unknown): void }[] = [];
  const timers = new Map<number, () => void>();
  let timerId = 0, publisher: ((o: ContinuityObservation) => void) | undefined;
  const c = createContinuityObserver({ timeoutMs: 100,
    inspect: (b, signal) => new Promise((resolve, reject) => { calls.push({ binding: b, signal, resolve, reject }); }),
    publish: o => { observations.push(o); publisher?.(o); },
    clock: { schedule: (_ms, cb) => { const id = ++timerId; timers.set(id, cb); return () => { timers.delete(id); }; } },
  });
  return { c, calls, observations, timers, setPublisher(p: (o: ContinuityObservation) => void) { publisher = p; },
    expire() { const pending = [...timers.values()]; timers.clear(); for (const cb of pending) cb(); },
    last: () => observations.at(-1)!, available: () => observations.filter(o => o.phase === "available"),
    async start() { c.replaceBindings([binding()]); await flush(); },
  };
}

test("startup and repeated reconciliation preserve actual verdict and all supplied counts", async () => {
  const f = fixture(); await f.start(); assert.equal(f.calls.length, 1);
  f.calls[0].resolve(summary()); await flush();
  assert.equal(f.last().phase, "available");
  assert.deepEqual(f.last().summary, summary());
  assert.equal(f.last().summary?.objectiveComplete, false);
  f.c.reconcileAll(); await flush(); assert.equal(f.calls.length, 2);
  f.calls[1].resolve(summary({ verdict: "runtime_held", findingCount: 0, missingInputCount: 0 })); await flush();
  assert.equal(f.last().summary?.verdict, "runtime_held");
  assert.equal(f.last().summary?.objectiveComplete, false);
  assert.equal(f.timers.size, 0);
});

for (const ending of ["success", "error"] as const) test(`events invalidate pending ${ending} immediately and coalesce a burst`, async () => {
  const f = fixture(); await f.start(); const original = f.last();
  f.c.observe(event("active")); f.c.observe(event("idle")); f.c.refresh("example"); f.c.reconcileAll();
  assert.equal(f.last().phase, "checking"); assert.ok(f.last().serial > original.serial);
  if (ending === "success") f.calls[0].resolve(summary()); else f.calls[0].reject(new Error("PRIVATE failure"));
  await flush(); assert.equal(f.available().length, 0); assert.equal(f.calls.length, 2);
  assert.ok(!f.observations.some(o => o.reason === "inspection_failed"));
  f.c.observe(event("failed")); f.calls[1].resolve(summary()); await flush();
  assert.equal(f.available().length, 0); assert.equal(f.calls.length, 3);
  f.calls[2].resolve(summary()); await flush(); assert.equal(f.available().length, 1);
});

test("idle and failed are triggers, never completion or retry permissions", async () => {
  const f = fixture(); await f.start(); f.calls[0].resolve(summary({ findingCount: 0, missingInputCount: 0 })); await flush();
  for (const kind of ["idle", "failed"] as const) {
    f.c.observe(event(kind)); await flush();
    f.calls.at(-1)!.resolve(summary({ findingCount: 0, missingInputCount: 0 })); await flush();
    assert.equal(f.last().summary?.verdict, "reconciliation_required"); assert.equal(f.last().summary?.objectiveComplete, false);
  }
});

test("foreign events are ignored; observed identity loss withdraws only its matched target", async () => {
  const f = fixture(); await f.start(); const n = f.observations.length;
  f.c.observe({ ...event(), threadId: "thr_other" }); assert.equal(f.observations.length, n);
  f.c.observe({ ...event(), identity: { ...event().identity!, hostId: "host_other" } });
  assert.equal(f.last().phase, "withdrawn"); assert.equal(f.calls[0].signal.aborted, true);
  f.calls[0].resolve(summary()); await flush(); assert.equal(f.available().length, 0);
  f.c.refresh("example"); f.c.reconcileAll(); await flush(); assert.equal(f.calls.length, 1);
  f.c.observe(event("active")); await flush(); assert.equal(f.calls.length, 2);
  f.c.observe({ ...event(), identity: null }); f.calls[1].resolve(summary()); await flush();
  assert.equal(f.last().reason, "target_unavailable"); assert.equal(f.available().length, 0);
});

test("timeout quarantines a nonsettling inspector across triggers and replacement", async () => {
  const f = fixture(); await f.start(); f.expire();
  assert.equal(f.calls[0].signal.aborted, true); assert.equal(f.last().reason, "inspection_timeout");
  for (let i = 0; i < 40; i++) { f.c.refresh("example"); f.c.observe(event()); }
  f.c.replaceBindings([binding({ revision: 2, hostId: "host_new" })]); await flush();
  assert.equal(f.calls.length, 1, "abort is not proof the inspector has stopped");
  assert.equal(f.last().phase, "unavailable"); assert.equal(f.last().reason, "inspection_pending");
  f.calls[0].resolve(summary()); await flush();
  assert.equal(f.available().length, 0); assert.equal(f.calls.length, 2);
  assert.equal(f.calls[1].binding.hostId, "host_new");
  f.calls[1].resolve(summary()); await flush(); assert.equal(f.last().bindingRevision, 2);
});

test("timeout without a new trigger never automatically retries", async () => {
  const f = fixture(); await f.start(); f.expire(); f.calls[0].resolve(summary()); await flush();
  assert.equal(f.calls.length, 1); assert.equal(f.available().length, 0); assert.equal(f.last().reason, "inspection_timeout");
  f.c.refresh("example"); await flush(); assert.equal(f.calls.length, 2);
});

test("one quarantined binding does not stop another binding", async () => {
  const f = fixture(); f.c.replaceBindings([binding(), binding({ id: "second", threadId: "thr_second" })]); await flush();
  f.expire(); f.calls[1].resolve(summary()); await flush(); f.c.refresh("second"); await flush();
  assert.equal(f.calls.length, 3); assert.equal(f.calls[2].binding.id, "second");
  f.calls[2].resolve(summary()); await flush(); assert.equal(f.last().bindingId, "second"); assert.equal(f.last().phase, "available");
});

test("remove/readd same ID does not reset generation or overlap the old physical call", async () => {
  const f = fixture(); await f.start(); const before = f.last();
  f.c.replaceBindings([]); assert.equal(f.last().reason, "binding_removed");
  f.c.replaceBindings([binding()]); await flush(); assert.equal(f.calls.length, 1);
  f.calls[0].resolve(summary()); await flush(); assert.equal(f.calls.length, 2); assert.equal(f.available().length, 0);
  f.calls[1].resolve(summary()); await flush(); assert.ok(f.last().generation > before.generation); assert.ok(f.last().serial > before.serial);
});

test("complete binding validation is atomic and unchanged binding is a no-op", async () => {
  const f = fixture(); await f.start(); const n = f.observations.length;
  const badSets = [[binding(), binding()], [binding(), binding({ id: "second" })],
    [binding({ revision: 1, hostId: "host_other" })], [binding({ revision: 0 })],
    [binding({ id: "new", threadId: "thr_new" }), binding({ revision: NaN })]];
  for (const bad of badSets) assert.throws(() => f.c.replaceBindings(bad));
  f.c.replaceBindings([binding()]); await flush(); assert.equal(f.observations.length, n); assert.equal(f.calls.length, 1);
  f.calls[0].resolve(summary()); await flush(); assert.equal(f.last().bindingRevision, 1);
});

test("binding/summary snapshots are detached and frozen", async () => {
  const f = fixture(); const b = { ...binding() }; f.c.replaceBindings([b]); b.hostId = "host_changed"; await flush();
  assert.equal(f.calls[0].binding.hostId, "host_example"); assert.ok(Object.isFrozen(f.calls[0].binding));
  const s = { ...summary() }; f.calls[0].resolve(s); await flush(); s.itemCount = 0;
  assert.equal(f.last().summary?.itemCount, 33); assert.ok(Object.isFrozen(f.last())); assert.ok(Object.isFrozen(f.last().summary));
});

test("unavailable and malformed projections never become successful observations or leak private fields", async () => {
  const f = fixture(); await f.start();
  const bad: unknown[] = [null, { ...summary(), objectiveComplete: true }, { ...summary(), itemCount: -1 },
    { ...summary(), findingCount: Infinity }, { ...summary(), missingInputCount: 0.5 }, { ...summary(), registerRevision: 0 },
    { ...summary(), workSha256: "PRIVATE PATH" }, { ...summary(), nextAction: "PRIVATE TASK" },
    { ...summary(), verdict: "reviewed_snapshot_current" }];
  for (const value of bad) {
    f.calls.at(-1)!.resolve(value as ContinuityInspection); await flush(); assert.equal(f.last().reason, "invalid_summary");
    f.c.refresh("example"); await flush();
  }
  f.calls.at(-1)!.resolve({ verdict: "cannot_assess", objectiveComplete: false }); await flush(); assert.equal(f.last().reason, "cannot_assess");
  assert.equal(f.available().length, 0); assert.doesNotMatch(JSON.stringify(f.observations), /PRIVATE/);
});

test("missing evidence remains null rather than fabricated", async () => {
  const f = fixture(); await f.start(); f.calls[0].resolve(summary({ workSha256: null, registerRevision: null })); await flush();
  assert.equal(f.last().summary?.workSha256, null); assert.equal(f.last().summary?.registerRevision, null);
});

test("publisher throws are contained without recursive publication or retries", async () => {
  const f = fixture(); f.setPublisher(() => { throw new Error("PRIVATE publisher failure"); }); await f.start();
  f.calls[0].resolve(summary()); await flush(); assert.equal(f.available().length, 1); assert.equal(f.calls.length, 1);
  assert.doesNotMatch(JSON.stringify(f.observations), /PRIVATE/);
});

test("synchronous publisher refresh invalidates a result before another inspection", async () => {
  const f = fixture(); let once = true;
  f.setPublisher(o => { if (once && o.phase === "available") { once = false; f.c.refresh("example"); } });
  await f.start(); f.calls[0].resolve(summary()); await flush();
  assert.equal(f.last().phase, "checking"); assert.equal(f.calls.length, 2);
  f.calls[1].resolve(summary()); await flush(); assert.equal(f.last().phase, "available");
  assert.ok(f.observations.every((o, i, all) => i === 0 || o.serial >= all[i - 1].serial));
});

test("dispose cancels timers and excludes late success/error and all future work", async () => {
  for (const reject of [false, true]) {
    const f = fixture(); await f.start(); f.c.dispose(); const n = f.observations.length;
    assert.equal(f.calls[0].signal.aborted, true); assert.equal(f.timers.size, 0);
    if (reject) f.calls[0].reject(new Error("late")); else f.calls[0].resolve(summary());
    f.c.observe(event()); f.c.refresh("example"); f.c.reconcileAll(); await flush();
    assert.equal(f.observations.length, n); assert.equal(f.calls.length, 1); assert.throws(() => f.c.replaceBindings([binding()]));
  }
});

test("publisher disposal before invocation does not start an inspector", async () => {
  const f = fixture(); f.setPublisher(() => f.c.dispose()); f.c.replaceBindings([binding()]); await flush();
  assert.equal(f.calls.length, 0); assert.equal(f.timers.size, 0);
});

test("reentrant replacement cannot publish a removed generation over a new binding", async () => {
  const f = fixture(); const second = binding({ id: "second", threadId: "thr_second" });
  f.c.replaceBindings([binding(), second]); await flush();
  for (const call of f.calls) call.resolve(summary()); await flush();
  let once = true;
  f.setPublisher(o => {
    if (once && o.phase === "withdrawn") {
      once = false;
      f.c.replaceBindings([binding({ revision: 2 }), { ...second, revision: 2 }]);
    }
  });
  const start = f.observations.length;
  f.c.replaceBindings([]); await flush();
  assert.equal(f.calls.length, 4);
  const tail = f.observations.slice(start);
  assert.deepEqual(tail.map(o => [o.bindingId, o.bindingRevision, o.phase]), [
    ["example", 1, "withdrawn"], ["example", 2, "checking"], ["second", 2, "checking"],
  ]);
  for (const call of f.calls.slice(2)) call.resolve(summary()); await flush();
  assert.equal(f.last().bindingRevision, 2);
});

test("abort listeners see the complete binding replacement and cannot resurrect its older batch", async () => {
  const f = fixture(); const second = binding({ id: "second", threadId: "thr_second" });
  f.c.replaceBindings([binding(), second]); await flush();
  f.calls[0].signal.addEventListener("abort", () => {
    // Both revisions must already be committed before the first abort callback.
    assert.throws(() => f.c.replaceBindings([binding({ revision: 2 }), second]), /stale_continuity_binding/);
    f.c.replaceBindings([binding({ revision: 3 }), { ...second, revision: 3 }]);
  }, { once: true });
  const start = f.observations.length;
  f.c.replaceBindings([binding({ revision: 2 }), { ...second, revision: 2 }]);
  assert.ok(f.observations.slice(start).every(o => o.bindingRevision === 3));
  for (const call of f.calls) call.resolve(summary()); await flush();
  assert.equal(f.calls.length, 4);
  assert.deepEqual(f.calls.slice(2).map(call => call.binding.revision), [3, 3]);
});
