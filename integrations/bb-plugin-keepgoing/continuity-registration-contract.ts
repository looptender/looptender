import { z } from "zod";
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
