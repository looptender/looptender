import { test } from "node:test";
import assert from "node:assert/strict";
import type { BbPluginApi } from "@get-bb/plugin-sdk";
import { usageEvents } from "./usage-events.ts";
const row = { seq: 20, createdAt: Date.parse("2026-01-02T12:00:00Z"), type: "thread/tokenUsage/updated", threadId: "thr_example", data: { providerThreadId: "/private/example-session", tokenUsage: { total: { inputTokens: 100, cachedInputTokens: 60, outputTokens: 8, reasoningOutputTokens: 2 } } } };
function sdk(list: (input: any) => Promise<unknown>): BbPluginApi["sdk"] { return { threads: { get: async () => ({id:"thr_example",providerId:"codex"}), events: { list } } } as unknown as BbPluginApi["sdk"]; }
test("only filtered bounded usage metadata, exact target and cursor, no transcript reads", async () => {
  for (const after of ["latest", "19"]) {
    const result = await usageEvents(sdk(async args => {
      assert.deepEqual(args.types, ["thread/tokenUsage/updated"]);
      assert.equal(args.threadId, "thr_example"); assert.equal(args.afterSeq, after === "latest" ? undefined : after);
      assert.equal(args.limit, after === "latest" ? "1" : "32");assert.equal(args.order, after === "latest" ? "desc" : "asc");assert.ok(args.signal);
      return [row];
    }), ["thr_example", after]);
    assert.equal(result.exitCode, 0); const output = JSON.parse(result.stdout!);
    assert.deepEqual(output.samples[0].counters, { input:100,cached:60,output:8,reasoning:2 });
    assert.equal(output.samples[0].model,"unavailable");assert.ok(!result.stdout!.includes("/private"));assert.ok(!result.stdout!.includes("thr_example"));
  }
});
test("missing fields stay null; invalid, oversized and wrong-target sources unavailable", async () => {
  const missing=await usageEvents(sdk(async()=>[{...row,data:{...row.data,tokenUsage:{total:{}}}}]),["thr_example","latest"]);
  assert.equal(JSON.parse(missing.stdout!).samples[0].counters.input,null);
  for(const rows of [[{...row,threadId:"thr_other"}], [{...row,type:"item/agentMessage/delta"}],Array(33).fill(row), [{...row,data:{...row.data,tokenUsage:{total:{inputTokens:-1}}}}]]){
    const result=await usageEvents(sdk(async()=>rows),["thr_example","0"]);assert.equal(result.exitCode,1);assert.equal(result.stdout,undefined);
  }
  let calls=0;await usageEvents(sdk(async()=>{calls++;return []}),["thr_example","--all"]);assert.equal(calls,0);
  const failed=await usageEvents(sdk(async()=>{throw Error("private failure")}),["thr_example","0"]);assert.equal(failed.exitCode,1);assert.ok(!failed.stderr?.includes("private"));
});

test("unsupported provider never queries usage or guesses normalization", async()=>{
 let calls=0;const api=sdk(async()=>{calls++;return [row]});
 api.threads.get=async()=>({id:"thr_example",providerId:"example-other"}) as any;
 assert.equal((await usageEvents(api,["thr_example","latest"])).exitCode,1);assert.equal(calls,0);
});
