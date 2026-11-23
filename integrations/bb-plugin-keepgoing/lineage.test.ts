import { test } from "node:test";
import assert from "node:assert/strict";
import { walkLineage, lineageDue, lineageRetryMs, LINEAGE_TTL_MS, MAX_ANCESTORS } from "./lineage.ts";

const tree = (parents: Record<string, string | null>) => async (id: string) => parents[id] ?? null;

test("the walk follows each thread's own parent, so ancestor order from the SDK does not matter", async () => {
  const asked: string[] = [];
  const r = await walkLineage("lane", tree({ lane: "mid", mid: "top", top: null }), async id => { asked.push(id); return id === "top" ? "example-coordinator" : undefined; });
  assert.deepEqual(r, { child: true, coveredBy: { threadId: "top", loop: "example-coordinator" } });
  assert.deepEqual(asked, ["mid", "top"], "nearest first, by walking");
  const near = await walkLineage("lane", tree({ lane: "mid", mid: "top" }), async id => id === "mid" ? "near-loop" : "far-loop");
  assert.equal(near.coveredBy?.loop, "near-loop");
});

test("a root thread is not a child; a child with no loop above it has no cover; cycles and depth are bounded", async () => {
  assert.deepEqual(await walkLineage("root", tree({}), async () => "x"), { child: false });
  assert.deepEqual(await walkLineage("lane", tree({ lane: "p", p: null }), async () => undefined), { child: true });
  assert.deepEqual(await walkLineage("a", tree({ a: "b", b: "a" }), async () => undefined), { child: true });
  const deep: Record<string, string | null> = {}; for (let i = 0; i < 100; i++) deep[`t${i}`] = `t${i + 1}`;
  let asked = 0; await walkLineage("t0", tree(deep), async () => { asked++; return undefined; });
  assert.equal(asked, MAX_ANCESTORS);
});

test("a failed read throws, so it is never cached as 'not a child'", async () => {
  await assert.rejects(walkLineage("lane", async () => { throw new Error("down"); }, async () => undefined));
});

test("a good answer is refreshed after a few minutes; a failure is retried with growing, capped delay", () => {
  const now = 1_000_000;
  assert.equal(lineageDue(undefined, now), true);
  assert.equal(lineageDue({ at: now, failures: 0, inflight: false }, now + LINEAGE_TTL_MS - 1), false);
  assert.equal(lineageDue({ at: now, failures: 0, inflight: false }, now + LINEAGE_TTL_MS), true);
  assert.equal(lineageDue({ at: now - LINEAGE_TTL_MS * 2, failures: 0, inflight: true }, now), false, "never two at once");
  assert.deepEqual([1, 2, 3, 4, 5, 9].map(lineageRetryMs), [5000, 10000, 20000, 40000, 80000, 120000]);
  assert.equal(lineageDue({ at: now, failures: 2, inflight: false }, now + 9_999), false);
  assert.equal(lineageDue({ at: now, failures: 2, inflight: false }, now + 10_000), true);
});
