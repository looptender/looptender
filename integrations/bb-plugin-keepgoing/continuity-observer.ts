/** Disconnected continuity observation. No SDK, files, timers or controls are registered here. */
export type ContinuityBinding = Readonly<{
  id: string; revision: number; threadId: string; projectId: string; environmentId: string; hostId: string;
}>;
export type ContinuitySummary = Readonly<{
  verdict: "reconciliation_required" | "runtime_held";
  objectiveComplete: false;
  workSha256: string | null;
  registerRevision: number | null;
  itemCount: number;
  findingCount: number;
  missingInputCount: number;
}>;
export type ContinuityInspection = ContinuitySummary | Readonly<{ verdict: "cannot_assess"; objectiveComplete: false }>;
export type ContinuityEvent = Readonly<{
  threadId: string;
  kind: "active" | "idle" | "failed" | "withdrawn";
  identity: Readonly<{ projectId: string; environmentId: string; hostId: string }> | null;
}>;
export type ContinuityObservation = Readonly<{
  bindingId: string; bindingRevision: number; generation: number; serial: number;
  phase: "checking" | "available" | "unavailable" | "withdrawn";
  checkId: string | null;
  summary?: ContinuitySummary;
  reason?: "inspection_failed" | "inspection_timeout" | "inspection_pending" | "invalid_summary" |
    "cannot_assess" | "binding_removed" | "target_unavailable";
}>;
export type ContinuityDependencies = {
  /** A cooperative adapter returns a normalized projection, not raw helper JSON. */
  inspect(binding: ContinuityBinding, signal: AbortSignal): Promise<ContinuityInspection>;
  /** Synchronous ordered delivery only. Throws are contained; errors are never recursively published. */
  publish(observation: ContinuityObservation): void;
  clock: { schedule(milliseconds: number, callback: () => void): () => void };
  timeoutMs: number;
};

type Entry = { binding: ContinuityBinding; generation: number; serial: number; dirty: boolean; suspended: boolean };
type Flight = { hostId: string; generation: number; serial: number; checkId: string; abort: AbortController; expired: boolean; cancel?: () => void };
type Slot = { entry: Entry | null; flight: Flight | null };
const bindingKeys = ["id", "revision", "threadId", "projectId", "environmentId", "hostId"];
const summaryKeys = ["verdict", "objectiveComplete", "workSha256", "registerRevision", "itemCount", "findingCount", "missingInputCount"];
const plain = (x: unknown): x is Record<string, unknown> => !!x && typeof x === "object" && !Array.isArray(x) && Object.getPrototypeOf(x) === Object.prototype;
const keysAre = (x: Record<string, unknown>, keys: string[]) => Object.keys(x).length === keys.length && keys.every(k => Object.hasOwn(x, k));
const count = (x: unknown): x is number => typeof x === "number" && Number.isSafeInteger(x) && x >= 0;
const identity = (x: unknown): x is string => typeof x === "string" && x.length > 0 && !/[\u0000-\u001f\u007f]/.test(x);

function copyBinding(value: unknown): ContinuityBinding {
  if (!plain(value) || !keysAre(value, bindingKeys) || !count(value.revision) || value.revision === 0 ||
      !bindingKeys.filter(k => k !== "revision").every(k => identity(value[k]))) throw new Error("invalid_continuity_binding");
  return Object.freeze({ id: value.id as string, revision: value.revision, threadId: value.threadId as string,
    projectId: value.projectId as string, environmentId: value.environmentId as string, hostId: value.hostId as string });
}
function sameBinding(a: ContinuityBinding, b: ContinuityBinding): boolean {
  return bindingKeys.every(k => a[k as keyof ContinuityBinding] === b[k as keyof ContinuityBinding]);
}
function copySummary(value: unknown): ContinuityInspection | null {
  if (!plain(value) || value.objectiveComplete !== false) return null;
  if (value.verdict === "cannot_assess") return keysAre(value, ["verdict", "objectiveComplete"])
    ? Object.freeze({ verdict: "cannot_assess", objectiveComplete: false }) : null;
  if (!keysAre(value, summaryKeys) || !["runtime_held", "reconciliation_required"].includes(value.verdict as string) ||
      !(value.workSha256 === null || (typeof value.workSha256 === "string" && /^[a-f0-9]{64}$/.test(value.workSha256))) ||
      !(value.registerRevision === null || (count(value.registerRevision) && value.registerRevision > 0)) ||
      ![value.itemCount, value.findingCount, value.missingInputCount].every(count)) return null;
  return Object.freeze({ verdict: value.verdict as ContinuitySummary["verdict"], objectiveComplete: false,
    workSha256: value.workSha256 as string | null, registerRevision: value.registerRevision as number | null,
    itemCount: value.itemCount as number, findingCount: value.findingCount as number, missingInputCount: value.missingInputCount as number });
}

export function createContinuityObserver(deps: ContinuityDependencies) {
  if (!Number.isSafeInteger(deps.timeoutMs) || deps.timeoutMs <= 0) throw new Error("invalid_continuity_timeout");
  const slots = new Map<string, Slot>();
  let disposed = false, generation = 0, serial = 0, attempts = 0, configuration = 0;
  function cancel(flight: Flight | null) {
    try { flight?.cancel?.(); } catch { /* Timer cleanup cannot release or strand an inspection slot. */ }
  }
  function emit(entry: Entry, phase: ContinuityObservation["phase"], checkId: string | null,
    extra: Pick<ContinuityObservation, "summary" | "reason"> = {}) {
    if (disposed) return;
    const snapshot = Object.freeze({ bindingId: entry.binding.id, bindingRevision: entry.binding.revision,
      generation: entry.generation, serial: entry.serial, phase, checkId, ...extra });
    try { deps.publish(snapshot); } catch { /* A publisher cannot initiate retries or expose exception text. */ }
  }
  function current(slot: Slot, flight: Flight): slot is Slot & { entry: Entry } {
    return !disposed && slot.flight === flight && !flight.expired && slot.entry !== null &&
      !slot.entry.suspended && slot.entry.generation === flight.generation && slot.entry.serial === flight.serial;
  }
  function launch(id: string, slot: Slot) {
    if (disposed || slot.flight || !slot.entry || slot.entry.suspended || !slot.entry.dirty) return;
    const entry = slot.entry;
    entry.dirty = false;
    const flight: Flight = { hostId: entry.binding.hostId, generation: entry.generation, serial: entry.serial, checkId: `inspection-${++attempts}`,
      abort: new AbortController(), expired: false };
    slot.flight = flight;
    emit(entry, "checking", flight.checkId);
    // Defer invocation so a synchronous publisher may withdraw/dispose without starting work.
    void Promise.resolve().then(async () => {
      if (!current(slot, flight)) return;
      flight.cancel = deps.clock.schedule(deps.timeoutMs, () => {
        if (disposed || slot.flight !== flight) return;
        flight.expired = true;
        flight.abort.abort();
        // Keep the physical slot until settlement, even when cancellation is ignored.
        if (slot.entry && !slot.entry.suspended) {
          slot.entry.serial = ++serial;
          emit(slot.entry, "unavailable", slot.entry.generation === flight.generation ? flight.checkId : null,
            { reason: slot.entry.generation === flight.generation ? "inspection_timeout" : "inspection_pending" });
        }
      });
      const result = await deps.inspect(entry.binding, flight.abort.signal);
      if (!current(slot, flight)) return;
      const summary = copySummary(result);
      if (!summary) emit(slot.entry, "unavailable", flight.checkId, { reason: "invalid_summary" });
      else if (summary.verdict === "cannot_assess") emit(slot.entry, "unavailable", flight.checkId, { reason: "cannot_assess" });
      else emit(slot.entry, "available", flight.checkId, { summary });
    }).catch(() => {
      if (current(slot, flight)) emit(slot.entry, "unavailable", flight.checkId, { reason: "inspection_failed" });
    }).finally(() => {
      cancel(flight);
      if (slot.flight !== flight) return;
      slot.flight = null;
      if (disposed) return;
      if (!slot.entry) slots.delete(id);
      else launch(id, slot);
    });
  }
  function invalidate(id: string, slot: Slot) {
    const entry = slot.entry;
    if (disposed || !entry || entry.suspended) return;
    entry.serial = ++serial;
    entry.dirty = true;
    if (slot.flight) {
      // Invalidate old success AND old failure immediately, not after the next inspection starts.
      emit(entry, slot.flight.expired ? "unavailable" : "checking", null,
        slot.flight.expired ? { reason: "inspection_pending" } : {});
    } else launch(id, slot);
  }
  function withdraw(slot: Slot, reason: "binding_removed" | "target_unavailable") {
    if (!slot.entry) return;
    slot.entry.serial = ++serial;
    slot.entry.dirty = false;
    slot.entry.suspended = true;
    const flight = slot.flight;
    emit(slot.entry, "withdrawn", null, { reason });
    flight?.abort.abort();
  }
  return {
    replaceBindings(values: readonly ContinuityBinding[]) {
      if (disposed) throw new Error("continuity_observer_disposed");
      if (!Array.isArray(values)) throw new Error("invalid_continuity_bindings");
      const next = new Map<string, ContinuityBinding>(), threads = new Set<string>();
      // Validate the entire replacement before changing configuration or publishing anything.
      for (const value of values) {
        const binding = copyBinding(value), previous = slots.get(binding.id)?.entry?.binding;
        if (next.has(binding.id) || threads.has(binding.threadId)) throw new Error("duplicate_continuity_binding");
        if (previous && (binding.revision < previous.revision ||
            (binding.revision === previous.revision && !sameBinding(binding, previous)))) throw new Error("stale_continuity_binding");
        next.set(binding.id, binding); threads.add(binding.threadId);
      }
      const epoch = ++configuration;
      const changed: [string, Slot, Entry][] = [], removed: Slot[] = [], cancelled = new Set<Flight>();
      // Commit all bindings before synchronous notifications can observe/reenter the controller.
      for (const [id, slot] of slots) if (slot.entry && !next.has(id)) {
        const old: Slot = { entry: slot.entry, flight: slot.flight };
        slot.entry = null; removed.push(old);
        if (slot.flight) cancelled.add(slot.flight);
        if (!slot.flight) slots.delete(id);
      }
      for (const [id, binding] of next) {
        let slot = slots.get(id);
        if (slot?.entry && sameBinding(slot.entry.binding, binding)) {
          // Retain work committed by a superseded notification batch. An abort
          // listener or publisher may reenter before that batch reaches this entry.
          if (slot.entry.serial === 0 && slot.entry.dirty) changed.push([id, slot, slot.entry]);
          continue;
        }
        if (!slot) { slot = { entry: null, flight: null }; slots.set(id, slot); }
        slot.entry = { binding, generation: ++generation, serial: 0, dirty: true, suspended: false };
        if (slot.flight) cancelled.add(slot.flight);
        changed.push([id, slot, slot.entry]);
      }
      // Abort listeners and publishers may synchronously replace configuration. Never finish
      // an older notification batch after that replacement has committed.
      for (const flight of cancelled) flight.abort.abort();
      for (const slot of removed) {
        if (disposed || configuration !== epoch) return;
        withdraw(slot, "binding_removed");
      }
      for (const [id, slot, entry] of changed) {
        if (disposed || configuration !== epoch) return;
        if (slots.get(id) === slot && slot.entry === entry) invalidate(id, slot);
      }
    },
    observe(event: ContinuityEvent) {
      if (disposed) return;
      for (const [id, slot] of slots) {
        const entry = slot.entry;
        if (!entry || entry.binding.threadId !== event.threadId) continue;
        if (event.kind === "withdrawn" || !event.identity ||
            event.identity.projectId !== entry.binding.projectId || event.identity.environmentId !== entry.binding.environmentId ||
            event.identity.hostId !== entry.binding.hostId) withdraw(slot, "target_unavailable");
        else if (["active", "idle", "failed"].includes(event.kind)) { entry.suspended = false; invalidate(id, slot); }
        return;
      }
    },
    retireSettledHost(hostId: string, signal: AbortSignal | null) {
      // Only registration's validated consumed receipt may call this. Commit invalidation
      // for every same-host binding before abort listeners or notifications can reenter.
      const changed: Slot[] = [], aborts: Flight[] = [];
      for (const slot of slots.values()) {
        if (slot.entry?.binding.hostId === hostId) {
          slot.entry.serial = ++serial; slot.entry.dirty = false; slot.entry.suspended = true; changed.push(slot);
        }
        if (signal && slot.flight?.hostId === hostId && slot.flight.abort.signal === signal) {
          const old = slot.flight; slot.flight = null; cancel(old); aborts.push(old);
        }
      }
      for (const f of aborts) f.abort.abort();
      for (const slot of changed) if (slot.entry?.suspended) emit(slot.entry, "unavailable", null, { reason: "cannot_assess" });
    },
    refresh(id: string) { const slot = slots.get(id); if (slot) invalidate(id, slot); },
    reconcileAll() { for (const [id, slot] of slots) invalidate(id, slot); },
    dispose() {
      if (disposed) return;
      disposed = true;
      for (const slot of slots.values()) { slot.entry = null; cancel(slot.flight); slot.flight?.abort.abort(); }
      slots.clear();
    },
  };
}
