import { continuityHostInput, continuityHostOutput } from "./continuity-registration-contract.ts";
import { taskTextSchema } from "./task-text.ts";
import { publicationSchema } from "./registry-contract.ts";
import { defineRpcContract } from "@get-bb/plugin-sdk";
import { z } from "zod";
import { targetSchema, recordSchema, planSchema, refusalSchema, failureSchema, continuationCandidateSchema } from "./setup-contract.ts";
import { transferHostInput } from "./transfer-contract.ts";
import { attachHostInput, attachHostResult } from "./attach-contract.ts";
import { boundsSchema } from "./new-thread-contract.ts";
const text = z.string().min(1).max(4096);
// Only the server calls the host entry. Browser input never selects a binary,
// host, project path, config path, provider, or arbitrary command arguments.
export const hostContract = defineRpcContract({
  continuityCheck: { input: continuityHostInput, output: continuityHostOutput },
  attachment: { input: attachHostInput, output: attachHostResult },
  transfer: { input: transferHostInput, output: z.object({ code: z.number().int(), data: z.unknown(), registration: publicationSchema.optional() }) },
  init: {
    input: z.object({ bin: text, target: targetSchema, name: text, task: taskTextSchema, dryRun: z.boolean(), withGoal: z.boolean().optional(), bounds: boundsSchema.optional(), followStandard: z.literal(true).optional(),
      succeeds: text.optional(),
      // Provider and model labels from init's own offer, or none to decline.
      takeover: z.union([z.literal("none"), z.literal("detect"), z.object({ provider: z.string().regex(/^[A-Za-z0-9][A-Za-z0-9._:/-]{0,63}$/), model: z.string().regex(/^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$/) })]).optional() }),
    // A plan, or keepgoing's own structured refusal.
    output: z.union([z.object({ plan: planSchema, registration: publicationSchema.optional(), warnings: z.array(z.string()).optional() }), z.object({ refused: refusalSchema }), z.object({ noRoutes: z.literal(true) }), z.object({ failed: failureSchema })]),
  },
  // Existing loops on this host that a new setup can continue (--succeeds).
  continuationCandidates: {
    input: z.object({ bin: text, target: targetSchema, extraPaths: z.array(text).max(128).optional() }),
    output: z.object({ candidates: z.array(continuationCandidateSchema).max(128) }),
  },
  // Whether the host's looptender has a standard route list with routes.
  standardRoutes: {
    input: z.object({ bin: text, target: targetSchema }),
    output: z.object({ available: z.boolean() }),
  },
  command: {
    // `json` is honoured for start only, and only set when the CLI advertised it.
    input: z.object({ bin: text, record: recordSchema, operation: z.enum(["status", "doctor", "start", "startChecks", "pause", "resume", "stop"]), json: z.literal(true).optional() }),
    output: z.object({ code: z.number().int(), data: z.unknown(), registration: publicationSchema.optional(), method: z.enum(["service", "background"]).optional() }),
  },
});
