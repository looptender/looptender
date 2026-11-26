import { createHash } from "node:crypto";
import { z } from "zod";
import type { BbPluginApi } from "@get-bb/plugin-sdk";

// This bridge reads only server-filtered token metadata. Never fall back to
// timeline/log reads. Historical provider/model attribution is not supplied.
const count = z.number().int().min(0).max(1e12).nullable().optional();
const event = z.object({ seq: z.number().int().positive().max(Number.MAX_SAFE_INTEGER), createdAt: z.number().int().positive(), type: z.literal("thread/tokenUsage/updated"), threadId: z.string(), data: z.object({ providerThreadId: z.string().min(1).max(1024), tokenUsage: z.object({ total: z.object({ inputTokens: count, cachedInputTokens: count, outputTokens: count, reasoningOutputTokens: count }) }) }) });
export async function usageEvents(sdk: BbPluginApi["sdk"], args: string[]): Promise<{exitCode:number;stdout?:string;stderr?:string}> {
  const [threadId, after] = args;
  const fail = { exitCode: 1, stderr: "Structured usage source unavailable." };
  if (args.length !== 2 || !/^thr_[a-zA-Z0-9_-]{1,100}$/.test(threadId ?? "") || !/^(latest|[0-9]{1,15})$/.test(after ?? "")) return fail;
  try {
    // Codex total.inputTokens includes cachedInputTokens; other providers'
    // normalization is not established by this collector. Do not guess.
    const metadata = await sdk.threads.get({threadId, signal: AbortSignal.timeout(500)});
    if (metadata.id !== threadId || metadata.providerId !== "codex") return fail;
    const raw = await sdk.threads.events.list({ threadId, types: ["thread/tokenUsage/updated"], order: after === "latest" ? "desc" : "asc", limit: after === "latest" ? "1" : "32", ...(after === "latest" ? {} : { afterSeq: after }), signal: AbortSignal.timeout(1500) });
    const rows = z.array(event).max(32).parse(raw);
    if (rows.some(r => r.threadId !== threadId)) return fail;
    return { exitCode: 0, stdout: JSON.stringify({ version: 1, samples: rows.map(r => ({ seq: r.seq, at: new Date(r.createdAt).toISOString(), stream: createHash("sha256").update(threadId + "\0" + r.data.providerThreadId).digest("hex").slice(0, 32), provider: "unavailable", model: "unavailable", counters: { input: r.data.tokenUsage.total.inputTokens ?? null, cached: r.data.tokenUsage.total.cachedInputTokens ?? null, output: r.data.tokenUsage.total.outputTokens ?? null, reasoning: r.data.tokenUsage.total.reasoningOutputTokens ?? null } })) }) };
  } catch { return fail; }
}
