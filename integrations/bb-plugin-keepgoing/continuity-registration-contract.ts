import { z } from "zod";
import { createHash } from "node:crypto";
import { continuityHelperSha256 } from "./continuity-host-inspector-asset.ts";
const identity = z.string().min(1).max(256).regex(/^[^\u0000-\u001f\u007f]+$/);
export const continuityBindingSchema = z.object({ id: identity, revision: z.number().int().positive().max(Number.MAX_SAFE_INTEGER),
  threadId: identity, projectId: identity, environmentId: identity, hostId: identity }).strict();
const path = z.string().min(1).max(4096).regex(/^\/[^\u0000-\u001f\u007f]*$/);
export const continuityPathsSchema = z.object({ work: path, context: path.optional(), assignments: path.optional(), previous: path.optional() }).strict();
export const continuityEnrollmentSchema = z.object({ binding: continuityBindingSchema, paths: continuityPathsSchema }).strict();
export const continuityAttemptSchema = z.string().uuid();
const count = z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER);
export const continuityInspectionSchema = z.union([
  z.object({ verdict: z.literal("cannot_assess"), objectiveComplete: z.literal(false) }).strict(),
  z.object({ verdict: z.enum(["runtime_held", "reconciliation_required"]), objectiveComplete: z.literal(false),
    workSha256: z.string().regex(/^[a-f0-9]{64}$/).nullable(), registerRevision: count.positive().nullable(),
    itemCount: count, findingCount: count, missingInputCount: count }).strict(),
]);
export const continuityHostInput = z.object({ attempt: continuityAttemptSchema, enrollment: continuityEnrollmentSchema }).strict();
export const continuityHostOutput = z.object({ attempt: continuityAttemptSchema, settled: z.literal(true), inspection: continuityInspectionSchema }).strict();

// Prospective execution evidence; it grants neither inspection nor loop authority.
const digest = z.string().regex(/^[a-f0-9]{64}$/);
export const continuityRequestSchema = z.object({ version: z.literal(2), namespace: continuityAttemptSchema,
  attempt: continuityAttemptSchema, binding: continuityBindingSchema, enrollmentDigest: digest, helperDigest: digest, requestDigest: digest }).strict();
export type ContinuityRequest = z.infer<typeof continuityRequestSchema>;
export const continuityReceiptSchema = z.object({ request: continuityRequestSchema,
  settlement: z.enum(["no_child_started", "child_closed"]), receiptDigest: digest }).strict();
export type ContinuityReceipt = z.infer<typeof continuityReceiptSchema>;
export const continuityHostV2Input = z.object({ request: continuityRequestSchema, enrollment: continuityEnrollmentSchema }).strict();
export const continuityHostV2Output = z.object({ receipt: continuityReceiptSchema, inspection: continuityInspectionSchema }).strict();
export const continuityAttemptStatusOutput = z.union([
  z.object({ state: z.literal("settled"), receipt: continuityReceiptSchema }).strict(),
  z.object({ state: z.enum(["unknown", "unsupported"]) }).strict(),
]);

export const continuityDigest = (value: unknown) => createHash("sha256").update(JSON.stringify(value)).digest("hex");
export const enrollmentDigest = (value: unknown) => {
  const e = continuityEnrollmentSchema.parse(value);
  return continuityDigest([e.binding.id, e.binding.revision, e.binding.threadId, e.binding.projectId, e.binding.environmentId,
    e.binding.hostId, e.paths.work, e.paths.context ?? null, e.paths.assignments ?? null, e.paths.previous ?? null]);
};
const requestBody = (r: ContinuityRequest) => [r.version, r.namespace, r.attempt, r.binding.id, r.binding.revision,
  r.binding.threadId, r.binding.projectId, r.binding.environmentId, r.binding.hostId, r.enrollmentDigest, r.helperDigest];
export function makeContinuityRequest(namespace: string, attempt: string, value: unknown): ContinuityRequest {
  const e = continuityEnrollmentSchema.parse(value);
  const r = { version: 2 as const, namespace, attempt, binding: e.binding, enrollmentDigest: enrollmentDigest(e), helperDigest: continuityHelperSha256, requestDigest: "" };
  r.requestDigest = continuityDigest(requestBody(r)); return parseContinuityRequest(r);
}
export function parseContinuityRequest(value: unknown): ContinuityRequest {
  const r = continuityRequestSchema.parse(value);
  if (r.requestDigest !== continuityDigest(requestBody(r))) throw Error("continuity_request_mismatch");
  return r;
}
export function makeContinuityReceipt(request: ContinuityRequest, settlement: ContinuityReceipt["settlement"]): ContinuityReceipt {
  const r = parseContinuityRequest(request);
  return { request: r, settlement, receiptDigest: continuityDigest([r.requestDigest, settlement]) };
}
export function parseContinuityReceipt(value: unknown, expected: ContinuityRequest): ContinuityReceipt {
  const r = continuityReceiptSchema.parse(value); parseContinuityRequest(r.request); parseContinuityRequest(expected);
  if (r.request.requestDigest !== expected.requestDigest || r.receiptDigest !== continuityDigest([expected.requestDigest, r.settlement])) throw Error("continuity_receipt_mismatch");
  return r;
}
