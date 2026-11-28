import type { BbPluginApi } from "@get-bb/plugin-sdk";
import { randomUUID } from "node:crypto";
import { createContinuityObserver, type ContinuityBinding, type ContinuityInspection, type ContinuityObservation } from "./continuity-observer.ts";
import { decodeContinuityEnrollments, selectContinuitySession, type ContinuityEnrollment } from "./continuity-session-binding.ts";
import { createContinuityRegistry, enrollmentDigest, type ContinuityRegistryStore } from "./continuity-registration-store.ts";
import { continuityEnrollmentSchema, continuityHostV2Input, continuityHostV2Output, continuityAttemptStatusOutput, parseContinuityReceipt, type ContinuityRequest } from "./continuity-registration-contract.ts";
type Call = (input: ReturnType<typeof continuityHostV2Input.parse>, options: { hostId: string; signal: AbortSignal; timeoutMs: number }) => Promise<unknown>;
const unknown = (): ContinuityInspection => ({ verdict: "cannot_assess", objectiveComplete: false });
type Lookup = (request: ContinuityRequest, options: { hostId: string; timeoutMs: number }) => Promise<unknown>;
// Only a known-settled logical read may finish on cancellation. Never wrap host IO here.
function logicalRead(signal: AbortSignal, read: () => Promise<void>): Promise<void> {
  return new Promise((resolve, reject) => {
    const abort = () => { signal.removeEventListener("abort", abort); reject(Error()); };
    if (signal.aborted) { abort(); return; }
    signal.addEventListener("abort", abort, { once: true });
    void Promise.resolve().then(() => {
      if (signal.aborted) throw Error();
      return read();
    }).then(() => { signal.removeEventListener("abort", abort); resolve(); }, () => {
      signal.removeEventListener("abort", abort); reject(Error());
    }); // Both late success and rejection are contained; neither can change host ownership.
  });
}
const number = (x: string | undefined) => { if (!x || !/^(0|[1-9]\d*)$/.test(x) || !Number.isSafeInteger(Number(x))) throw Error(); return Number(x); };

/** Registers only explicit advisory enrollment. A persisted guard fences CHECKS, never work or session instructions. */
export function registerContinuity(bb: BbPluginApi, call: Call, onWorkerExit: (fn: (hostId: string) => void) => () => void,
  injectedStore?: ContinuityRegistryStore, lookup?: Lookup) {
  let disposed = false, serial = 0, store: ContinuityRegistryStore | undefined;
  let enrollments: readonly ContinuityEnrollment[] = [];
  const observations = new Map<string, ContinuityObservation>(), blocked = new Map<string, string>();
  const flights = new Map<string, { signal: AbortSignal; retired: boolean; release(): void }>();
  const attaching = new Set<ContinuityBinding>(), events = new Map<string, number>();
  const invalidateAttachment = (matches: (b: ContinuityBinding) => boolean) => { if ([...attaching].some(matches)) ++serial; };
  const snapshot = () => { if (!store) throw Error(); return store.read(); };
  const current = (e: ContinuityEnrollment, epoch: number) => {
    if (disposed) return false;
    const s = snapshot(); return s.epoch === epoch && s.enrollments.some(x => enrollmentDigest(x) === enrollmentDigest(e));
  };
  const resolve = async (b: ContinuityBinding) => {
    const signal = AbortSignal.timeout(10_000), t = await bb.sdk.threads.get({ threadId: b.threadId, signal });
    if (t.id !== b.threadId || t.projectId !== b.projectId || t.environmentId !== b.environmentId || t.archivedAt !== null || t.deletedAt !== null) throw Error();
    const e = await bb.sdk.environments.get({ environmentId: b.environmentId, signal });
    if (e.id !== b.environmentId || e.projectId !== b.projectId || e.hostId !== b.hostId || e.status !== "ready" || e.lifecycle.phase !== "active" || e.hostLifecycle !== "active") throw Error();
    const h = await bb.sdk.hosts.get({ hostId: b.hostId, signal });
    if (h.id !== b.hostId || h.status !== "connected" || h.lifecycle.phase !== "active") throw Error();
    // No git-repository requirement: continuity is a file-backed workflow.
  };
  const observer = createContinuityObserver({
    clock: { schedule(ms, fn) { const timer = setTimeout(fn, ms); return () => clearTimeout(timer); } }, timeoutMs: 15_000,
    publish(value) {
      const e = enrollments.find(e => e.binding.id === value.bindingId && e.binding.revision === value.bindingRevision);
      if (e && !disposed) observations.set(value.bindingId, value);
    },
    async inspect(binding, signal) {
      let e: ContinuityEnrollment, epoch: number;
      const event = events.get(binding.id);
      const valid = () => !signal.aborted && events.get(binding.id) === event && current(e, epoch);
      try {
        const s = snapshot(); epoch = s.epoch;
        e = s.enrollments.find(x => (["id", "revision", "threadId", "projectId", "environmentId", "hostId"] as const).every(k => x.binding[k] === binding[k]))!;
        if (!e || blocked.has(binding.hostId) || s.pending.some(p => p.hostId === binding.hostId)) return unknown();
        await resolve(binding);
        if (!valid()) return unknown();
      } catch { return unknown(); }
      const attempt = randomUUID();
      let request: ContinuityRequest;
      try { request = store!.begin(epoch, e, attempt); } catch { return unknown(); }
      const input = continuityHostV2Input.parse({ request, enrollment: e });
      let release!: () => void;
      const retired = new Promise<{ kind: "retired" }>(resolve => { release = () => resolve({ kind: "retired" }); });
      const flight = { signal, retired: false, release }; flights.set(attempt, flight);
      let result: ReturnType<typeof continuityHostV2Output.parse>;
      try {
        // Only correlated receipt consumption can win this race, never abort/timeout/worker exit.
        const outcome = await Promise.race([
          Promise.resolve().then(() => call(input, { hostId: binding.hostId, signal, timeoutMs: 20_000 }))
            .then(value => ({ kind: "reply" as const, value }), () => ({ kind: "unknown" as const })), retired,
        ]);
        if (flight.retired || outcome.kind === "retired") return unknown();
        if (outcome.kind === "unknown") throw Error();
        result = continuityHostV2Output.parse(outcome.value);
        parseContinuityReceipt(result.receipt, request);
      } catch {
        if (flight.retired) return unknown();
        blocked.set(binding.hostId, attempt);
        await retired; return unknown();
      }
      try {
        try { store!.settled(binding.hostId, attempt, result.receipt); }
        catch (error) { if (!store!.consumed(result.receipt)) throw error; }
      } catch {
        if (!flight.retired) blocked.set(binding.hostId, attempt);
        // A later explicit reconciliation can retire this exact physical exclusion.
        await retired; return unknown();
      }
      flights.delete(attempt);
      try {
        if (!valid()) return unknown();
        // Bound only logical metadata after physical settlement and acknowledged token removal.
        // Late metadata cannot re-block the host or remove a newer attempt.
        await logicalRead(signal, () => resolve(binding));
        return valid() ? result.inspection : unknown();
      } catch { return unknown(); }
    },
  });
  const replace = () => {
    const s = snapshot(); enrollments = decodeContinuityEnrollments(s.enrollments);
    serial++; observations.clear(); observer.replaceBindings(enrollments.map(e => e.binding));
  };
  const invalidate = (e: ContinuityEnrollment) => {
    events.set(e.binding.id, ++serial);
    observer.observe({ threadId: e.binding.threadId, kind: "withdrawn", identity: null });
    return serial;
  };
  const refresh = (e: ContinuityEnrollment, kind: "active" | "idle" | "failed") => {
    events.set(e.binding.id, ++serial);
    // This tuple is an enrollment, not a verified observation. The inspector resolves
    // it before dispatch; synchronous observer invalidation happens before that await.
    observer.observe({ threadId: e.binding.threadId, kind, identity: e.binding });
  };
  // Install all lifecycle handlers before reading startup persistence. Event text is never consumed.
  for (const kind of ["active", "idle", "failed"] as const) bb.events.on(`thread.${kind}`, ({ thread }) => {
    invalidateAttachment(b => b.threadId === thread.id);
    const e = enrollments.find(e => e.binding.threadId === thread.id);
    if (e) {
      if (thread.projectId !== e.binding.projectId || thread.environmentId !== e.binding.environmentId || thread.archivedAt !== null || thread.deletedAt !== null) invalidate(e);
      else refresh(e, kind);
    }
  });
  for (const kind of ["archived", "deleted"] as const) bb.events.on(`thread.${kind}`, ({ thread }) => {
    invalidateAttachment(b => b.threadId === thread.id);
    const e = enrollments.find(e => e.binding.threadId === thread.id); if (e) invalidate(e);
  });
  bb.events.on("experimental_environment.removed", ({ removal }) => {
    invalidateAttachment(b => b.environmentId === removal.environmentId);
    for (const e of enrollments) if (e.binding.environmentId === removal.environmentId) invalidate(e);
  });
  bb.events.on("experimental_host.deleted", ({ host }) => {
    invalidateAttachment(b => b.hostId === host.id);
    for (const e of enrollments) if (e.binding.hostId === host.id) invalidate(e);
  });
  const unsubscribe = onWorkerExit(hostId => {
    invalidateAttachment(b => b.hostId === hostId);
    for (const e of enrollments) if (e.binding.hostId === hostId) invalidate(e);
    // The persisted guard is never removed by a worker-exit announcement.
  });
  bb.agents.configure(ctx => {
    let selection: ReturnType<typeof selectContinuitySession> = { state: "unbound" };
    try { selection = selectContinuitySession(decodeContinuityEnrollments(snapshot().enrollments), {
      threadId: ctx.thread.id, projectId: ctx.project.id, environmentId: ctx.environment.id, hostId: ctx.host.id }); } catch { /* No unverified pointer. */ }
    return { tools: [], skills: ["keepgoing"], ...(selection.state === "bound" ? { instructions: selection.instructions } : {}) };
  });
  bb.onDispose(() => { disposed = true; serial++; observer.dispose(); observations.clear(); enrollments = []; unsubscribe(); });
  try { store = injectedStore ?? createContinuityRegistry(bb.storage.database()); replace(); } catch { store = undefined; }

  const retire = (hostId: string, attempt: string) => {
    if (snapshot().pending.some(p => p.hostId === hostId)) throw Error("continuity_new_attempt_preserved");
    const flight = flights.get(attempt);
    if (flight) flight.retired = true;
    for (const e of enrollments) if (e.binding.hostId === hostId) events.set(e.binding.id, ++serial);
    if (blocked.get(hostId) === attempt) blocked.delete(hostId);
    observer.retireSettledHost(hostId, flight?.signal ?? null);
    flight?.release(); flights.delete(attempt);
  };
  const status = () => {
    const s = snapshot();
    return { version: 2, epoch: s.epoch, advisory: true, attempts: s.pending.map(p => ({ hostId: p.hostId, attempt: p.attempt,
      state: p.kind === "legacy" ? "legacy_receipt_unavailable" : "attempt_settlement_unknown",
      ...(p.kind === "receipt" ? { requestDigest: p.request.requestDigest } : {}) })), enrollments: s.enrollments.map(e => ({ binding: e.binding,
      automaticCheck: blocked.has(e.binding.hostId) || s.pending.some(p => p.hostId === e.binding.hostId) ? "unavailable_pending_or_unknown" : "eligible",
      ...(observations.get(e.binding.id)?.bindingRevision === e.binding.revision ? { observation: observations.get(e.binding.id) } : {}),
    })), note: "Observation only. Pending or unknown automatic checks do not hold work. Manual file-backed reconciliation remains available; only exact durable prospective receipts support explicit reconciliation; legacy or unreceipted attempts remain unknown." };
  };
  return {
    status,
    async cli(args: readonly string[]) {
      try {
        if (disposed) throw Error();
        const [operation, ...rest] = args;
        if ((operation === "attempt-status" && rest.length === 2) || (operation === "reconcile" && rest.length === 4)) {
          const [hostId, attempt, expected, receiptDigest] = rest;
          const s = snapshot(), p = s.pending.find(p => p.hostId === hostId && p.attempt === attempt);
          const consumed = s.consumed.find(r => r.request.binding.hostId === hostId && r.request.attempt === attempt);
          if (!p && consumed) {
            if (operation === "reconcile" && (expected !== consumed.request.requestDigest || receiptDigest !== consumed.receiptDigest)) throw Error();
            if (operation === "reconcile" && (flights.has(attempt) || blocked.get(hostId) === attempt)) retire(hostId, attempt);
            return { exitCode: 0, stdout: JSON.stringify({ state: "already_consumed", requestDigest: consumed.request.requestDigest, receiptDigest: consumed.receiptDigest }) + "\n", stderr: "" };
          }
          if (!p || p.kind !== "receipt" || !lookup) return { exitCode: 1, stdout: JSON.stringify({ state: p?.kind === "legacy" ? "legacy_receipt_unavailable" : "attempt_settlement_unknown" }) + "\n", stderr: "" };
          const answer = continuityAttemptStatusOutput.parse(await lookup(p.request, { hostId, timeoutMs: 10_000 }));
          if (answer.state !== "settled") return { exitCode: 1, stdout: JSON.stringify({ state: answer.state }) + "\n", stderr: "" };
          const receipt = parseContinuityReceipt(answer.receipt, p.request);
          if (operation === "attempt-status") return { exitCode: 0, stdout: JSON.stringify({ state: "receipt_available", requestDigest: p.request.requestDigest, receiptDigest: receipt.receiptDigest }) + "\n", stderr: "" };
          if (disposed || expected !== p.request.requestDigest || receiptDigest !== receipt.receiptDigest) throw Error();
          // No SDK await between atomic consumption and exact local retirement.
          try { store!.settled(hostId, attempt, receipt); }
          catch (error) { if (!store!.consumed(receipt)) throw error; }
          retire(hostId, attempt);
          return { exitCode: 0, stdout: JSON.stringify({ state: "reconciled_observation_unavailable", requestDigest: expected, receiptDigest }) + "\n", stderr: "" };
        }
        if (operation === "status" && !rest.length) return { exitCode: 0, stdout: JSON.stringify(status()) + "\n", stderr: "" };
        if (operation === "attach") {
          if (rest.length < 8) throw Error();
          const [epochText, id, revisionText, threadId, projectId, environmentId, hostId, work, ...optional] = rest;
          const paths: Record<string, string> = { work };
          for (let i = 0; i < optional.length; i += 2) {
            const name = optional[i]?.slice(2); if (!optional[i]?.startsWith("--") || !["context", "assignments", "previous"].includes(name) || !optional[i + 1] || Object.hasOwn(paths, name)) throw Error();
            paths[name] = optional[i + 1];
          }
          const e = continuityEnrollmentSchema.parse({ binding: { id, revision: number(revisionText), threadId, projectId, environmentId, hostId }, paths });
          const token = serial, expected = number(epochText);
          if (selectContinuitySession([e], e.binding).state !== "bound") throw Error();
          attaching.add(e.binding);
          try {
            await resolve(e.binding);
            if (disposed || token !== serial) throw Error();
            store!.attach(expected, e); replace();
          } finally { attaching.delete(e.binding); }
        } else if (operation === "detach" && rest.length === 3) {
          store!.detach(number(rest[0]), rest[1], number(rest[2])); replace();
        } else if (operation === "check" && rest.length === 2) {
          const e = snapshot().enrollments.find(e => e.binding.id === rest[0] && e.binding.revision === number(rest[1])); if (!e) throw Error();
          await refresh(e, "active");
        } else throw Error();
        return { exitCode: 0, stdout: JSON.stringify(status()) + "\n", stderr: "" };
      } catch { return { exitCode: 1, stdout: "", stderr: "Continuity request unavailable or changed. Use continuity status, verify the exact tuple/revision and explicit private paths. No loop controls or provider work changed.\n" }; }
    },
  };
}
