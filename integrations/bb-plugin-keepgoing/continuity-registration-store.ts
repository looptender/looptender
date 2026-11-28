import { z } from "zod";
import { randomUUID } from "node:crypto";
import type { BbPluginApi } from "@get-bb/plugin-sdk";
import { decodeContinuityEnrollments, selectContinuitySession, type ContinuityEnrollment } from "./continuity-session-binding.ts";
import { continuityEnrollmentSchema, continuityAttemptSchema, continuityRequestSchema, continuityReceiptSchema,
  enrollmentDigest, makeContinuityRequest, parseContinuityRequest, parseContinuityReceipt, type ContinuityReceipt } from "./continuity-registration-contract.ts";
export { enrollmentDigest } from "./continuity-registration-contract.ts";
type Database = ReturnType<BbPluginApi["storage"]["database"]>;
const safe = z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER);
const legacyPending = z.object({ hostId: z.string().min(1).max(256), attempt: continuityAttemptSchema }).strict();
const fields = { epoch: safe, enrollments: z.array(continuityEnrollmentSchema),
  history: z.array(z.object({ id: z.string(), revision: safe.positive(), digest: z.string().regex(/^[a-f0-9]{64}$/) }).strict()) };
const legacySchema = z.object({ version: z.literal(1), ...fields, pending: z.array(legacyPending) }).strict();
const pendingSchema = z.discriminatedUnion("kind", [
  legacyPending.extend({ kind: z.literal("legacy") }).strict(),
  legacyPending.extend({ kind: z.literal("receipt"), request: continuityRequestSchema }).strict(),
]);
const stateSchema = z.object({ version: z.literal(2), namespace: continuityAttemptSchema, ...fields,
  pending: z.array(pendingSchema), consumed: z.array(continuityReceiptSchema) }).strict();
export type ContinuityRegistry = z.infer<typeof stateSchema>;
function decode(raw: unknown): ContinuityRegistry {
  const s = stateSchema.parse(raw); decodeContinuityEnrollments(s.enrollments);
  if (new Set(s.history.map(x => x.id)).size !== s.history.length || new Set(s.pending.map(x => x.hostId)).size !== s.pending.length || new Set(s.pending.map(x => x.attempt)).size !== s.pending.length) throw Error("continuity_storage_invalid");
  for (const p of s.pending) if (p.kind === "receipt") {
    const r = parseContinuityRequest(p.request);
    if (r.namespace !== s.namespace || r.attempt !== p.attempt || r.binding.hostId !== p.hostId) throw Error("continuity_storage_invalid");
  }
  const seen = new Set<string>();
  for (const r of s.consumed) {
    parseContinuityReceipt(r, r.request);
    if (r.request.namespace !== s.namespace || seen.has(r.request.attempt) || s.pending.some(p => p.attempt === r.request.attempt)) throw Error("continuity_storage_invalid");
    seen.add(r.request.attempt);
  }
  for (const e of s.enrollments) {
    const h = s.history.find(h => h.id === e.binding.id);
    if (!h || h.revision !== e.binding.revision || h.digest !== enrollmentDigest(e)) throw Error("continuity_storage_invalid");
  }
  if (Buffer.byteLength(JSON.stringify(s)) > 256 * 1024) throw Error("continuity_storage_bound");
  return s;
}
export function createContinuityRegistry(db: Database) {
  db.exec("CREATE TABLE IF NOT EXISTS continuity_registration_v1 (id INTEGER PRIMARY KEY CHECK(id=1), value TEXT NOT NULL)");
  db.prepare("INSERT OR IGNORE INTO continuity_registration_v1(id,value) VALUES(1,?)").run(JSON.stringify({ version: 2, namespace: randomUUID(), epoch: 0, enrollments: [], history: [], pending: [], consumed: [] }));
  // Update the authoritative row, so an older strict reader cannot ignore receipt fences.
  db.transaction(() => {
    const raw = (db.prepare("SELECT value FROM continuity_registration_v1 WHERE id=1").get() as { value: string }).value;
    if (typeof raw !== "string" || Buffer.byteLength(raw) > 256 * 1024) throw Error("continuity_storage_bound");
    const value = JSON.parse(raw);
    if (value.version === 1) {
      const old = legacySchema.parse(value);
      const next = decode({ ...old, version: 2, namespace: randomUUID(), pending: old.pending.map(p => ({ ...p, kind: "legacy" })), consumed: [] });
      db.prepare("UPDATE continuity_registration_v1 SET value=? WHERE id=1").run(JSON.stringify(next));
    } else decode(value);
  }).immediate();
  const read = () => {
    const raw = (db.prepare("SELECT value FROM continuity_registration_v1 WHERE id=1").get() as { value: string }).value;
    if (typeof raw !== "string" || Buffer.byteLength(raw) > 256 * 1024) throw Error("continuity_storage_bound");
    return decode(JSON.parse(raw));
  };
  const change = <T>(fn: (s: ContinuityRegistry) => T): T => db.transaction(() => {
    const state = read(), result = fn(state); decode(state);
    db.prepare("UPDATE continuity_registration_v1 SET value=? WHERE id=1").run(JSON.stringify(state)); return result;
  }).immediate();
  return {
    read,
    attach(expectedEpoch: number, value: unknown) {
      const e = decodeContinuityEnrollments([continuityEnrollmentSchema.parse(value)])[0];
      if (selectContinuitySession([e], e.binding).state !== "bound") throw Error("continuity_pointer_too_large");
      return change(s => {
        if (s.epoch !== expectedEpoch || s.epoch === Number.MAX_SAFE_INTEGER) throw Error("continuity_revision_changed");
        const history = s.history.find(h => h.id === e.binding.id), previous = s.enrollments.find(x => x.binding.id === e.binding.id);
        const digest = enrollmentDigest(e);
        if (history && (e.binding.revision < history.revision || e.binding.revision === history.revision && (!previous || digest !== history.digest))) throw Error("continuity_revision_changed");
        s.enrollments = s.enrollments.filter(x => x.binding.id !== e.binding.id); s.enrollments.push(e);
        decodeContinuityEnrollments(s.enrollments);
        if (history) { history.revision = e.binding.revision; history.digest = digest; }
        else s.history.push({ id: e.binding.id, revision: e.binding.revision, digest });
        return ++s.epoch;
      });
    },
    detach(expectedEpoch: number, id: string, revision: number) {
      return change(s => {
        if (s.epoch !== expectedEpoch || !s.enrollments.some(e => e.binding.id === id && e.binding.revision === revision) || s.epoch === Number.MAX_SAFE_INTEGER) throw Error("continuity_revision_changed");
        s.enrollments = s.enrollments.filter(e => e.binding.id !== id); return ++s.epoch;
      });
    },
    begin(epoch: number, e: ContinuityEnrollment, attempt: string) {
      continuityAttemptSchema.parse(attempt);
      return change(s => {
        if (s.epoch !== epoch || !s.enrollments.some(x => enrollmentDigest(x) === enrollmentDigest(e))) throw Error("continuity_revision_changed");
        if (s.pending.some(p => p.hostId === e.binding.hostId || p.attempt === attempt)) throw Error("continuity_check_quarantined");
        if (s.consumed.some(r => r.request.attempt === attempt)) throw Error("continuity_check_quarantined");
        const request = makeContinuityRequest(s.namespace, attempt, e);
        s.pending.push({ kind: "receipt", hostId: e.binding.hostId, attempt, request });
        return request;
      });
    },
    settled(hostId: string, attempt: string, value: ContinuityReceipt) {
      return change(s => {
        const p = s.pending.find(p => p.hostId === hostId && p.attempt === attempt);
        if (!p || p.kind !== "receipt") throw Error("continuity_check_quarantined");
        const receipt = parseContinuityReceipt(value, p.request);
        s.consumed.push(receipt);
        s.pending = s.pending.filter(p => !(p.hostId === hostId && p.attempt === attempt));
      });
    },
    consumed(value: ContinuityReceipt) {
      const r = parseContinuityReceipt(value, value.request);
      return read().consumed.some(x => x.request.requestDigest === r.request.requestDigest && x.receiptDigest === r.receiptDigest);
    },
  };
}
export type ContinuityRegistryStore = ReturnType<typeof createContinuityRegistry>;
