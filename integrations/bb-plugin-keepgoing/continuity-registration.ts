import type { BbPluginApi } from "@get-bb/plugin-sdk";
import { randomUUID } from "node:crypto";
import { createContinuityObserver, type ContinuityBinding, type ContinuityInspection, type ContinuityObservation } from "./continuity-observer.ts";
import { decodeContinuityEnrollments, selectContinuitySession, type ContinuityEnrollment } from "./continuity-session-binding.ts";
import { createContinuityRegistry, enrollmentDigest, type ContinuityRegistryStore } from "./continuity-registration-store.ts";
import { continuityEnrollmentSchema, continuityHostInput, continuityHostOutput } from "./continuity-registration-contract.ts";
type Call = (input: ReturnType<typeof continuityHostInput.parse>, options: { hostId: string; signal: AbortSignal; timeoutMs: number }) => Promise<unknown>;
const unknown = (): ContinuityInspection => ({ verdict: "cannot_assess", objectiveComplete: false });
const unresolved = () => new Promise<ContinuityInspection>(() => {});
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
  injectedStore?: ContinuityRegistryStore) {
  let disposed = false, serial = 0, store: ContinuityRegistryStore | undefined;
  let enrollments: readonly ContinuityEnrollment[] = [];
  const observations = new Map<string, ContinuityObservation>(), blocked = new Set<string>();
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
      const attempt = randomUUID(), input = continuityHostInput.parse({ attempt, enrollment: e });
      // All pre-dispatch validation/resolution precedes the atomic durable guard.
      try { store!.begin(epoch, e, attempt); } catch { return unknown(); }
      let result: ReturnType<typeof continuityHostOutput.parse>;
      try {
        result = continuityHostOutput.parse(await call(input, { hostId: binding.hostId, signal, timeoutMs: 20_000 }));
        if (result.attempt !== attempt) throw Error();
      } catch {
        // Rejection/abort is NOT child close. Unknown physical settlement keeps the durable fence.
        blocked.add(binding.hostId); return unresolved();
      }
      try {
        // Physical settlement belongs to the original host/token, even after enrollment changes.
        // No SDK awaits or old observation-freshness gates belong in this atomic removal.
        store!.settled(binding.hostId, attempt);
      } catch {
        // No acknowledged removal: fence this instance and refuse evidence. A rollback retains
        // the token; an ambiguous post-COMMIT exception does not prove persistence across reload.
        blocked.add(binding.hostId); return unknown();
      }
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

  const status = () => {
    const s = snapshot();
    return { version: 1, epoch: s.epoch, advisory: true, enrollments: s.enrollments.map(e => ({ binding: e.binding,
      automaticCheck: blocked.has(e.binding.hostId) || s.pending.some(p => p.hostId === e.binding.hostId) ? "unavailable_pending_or_unknown" : "eligible",
      ...(observations.get(e.binding.id)?.bindingRevision === e.binding.revision ? { observation: observations.get(e.binding.id) } : {}),
    })), note: "Observation only. Pending or unknown automatic checks do not hold work. Manual file-backed reconciliation remains available; no automatic reconnect or unknown-call reconciliation is supported." };
  };
  return {
    status,
    async cli(args: readonly string[]) {
      try {
        if (disposed) throw Error();
        const [operation, ...rest] = args;
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
