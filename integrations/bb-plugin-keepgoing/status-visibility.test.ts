import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createFakePluginHost } from "@get-bb/plugin-sdk/testing";
import plugin from "./server.ts";
import { reasonLines, boundedTitle, diagnosticLines, recordsUnreadable, type StatusDetails } from "./status-details.ts";

// A stand-in looptender that prints the given status JSON and exits with the given code.
async function loopWith(status: object, exit = 0) {
  const dir = await realpath(await mkdtemp(join(tmpdir(), "keepgoing-visibility-")));
  const program = join(dir, "looptender-stand-in");
  await writeFile(program, `#!/bin/sh\ncat <<'JSON'\n${JSON.stringify(status)}\nJSON\nexit ${exit}\n`, { mode: 0o700 });
  const config = join(dir, "looptender.yaml");
  const { bb, harness } = createFakePluginHost({ pluginId: "keepgoing", settings: { keepgoingPath: program, configs: config } });
  await plugin(bb);
  const forThread = async () => await harness.behavior.callRpc("forThread", { threadId: "thr_example" }) as { loops: { controlState: string; details: StatusDetails }[]; errors: string[] };
  const listing = async () => String((await harness.behavior.runCli(["status"])).stdout);
  return { forThread, listing, dispose: async () => { await harness.lifecycle.dispose(); await rm(dir, { recursive: true, force: true }); } };
}
const agent = { adapter: "bb", bbThread: "thr_example" };
const healthy = { loop: "example-loop", target: "primary", controlState: "enabled", control: {}, window: "open", state: {}, daemon: { state: "not_recorded" }, primaryAgent: agent, targetAgent: agent };

test("deadline, send allowance and a low disk reach the card's details; an ok disk and an unreached limit add nothing", async () => {
  const w = await loopWith({ ...healthy, deadline: "2026-01-01T00:00:00Z", deadlineReached: true, sendAllowance: { max: 40, used: 40, remaining: 0, reached: true }, diskSpace: { level: "low", message: "the volume that holds the state directory is filling (900 MiB free of 100 GiB, below 2.0 GiB): free space on it before records fail to save" } });
  try {
    const { loops } = await w.forThread();
    const d = loops[0]!.details;
    assert.deepEqual(d.deadline, { at: "2026-01-01T00:00:00Z", reached: true });
    assert.deepEqual(d.sends, { max: 40, used: 40, remaining: 0, reached: true });
    assert.equal(d.disk?.level, "low");
    assert.deepEqual(reasonLines("enabled", null, d), [
      `Deadline reached (${new Date("2026-01-01T00:00:00Z").toLocaleString()}); evaluated on the next check`,
      "Send limit reached: 40/40 attempts reserved; 0 remaining",
      "Disk: the volume that holds the state directory is filling (900 MiB free of 100 GiB, below 2.0 GiB): free space on it before records fail to save",
    ]);
  } finally { await w.dispose(); }
  const ok = await loopWith({ ...healthy, deadline: "2099-01-01T00:00:00Z", deadlineReached: false, sendAllowance: { max: 40, used: 3, remaining: 37, reached: false }, diskSpace: { level: "ok" } });
  try {
    const d = (await ok.forThread()).loops[0]!.details;
    assert.equal(d.disk, undefined); assert.deepEqual(reasonLines("enabled", null, d), []);
    const lines = diagnosticLines(d, "primary", true).filter(l => /^(Deadline|Sends):/.test(l));
    assert.deepEqual(lines, [`Deadline: ${new Date("2099-01-01T00:00:00Z").toLocaleString()} (reached: false; evaluated on next check)`, "Sends: 3/40 attempts reserved; 37 remaining (reached: false)"]);
  } finally { await ok.dispose(); }
});

test("a loop whose record cannot be read is shown with what status says, not as an unavailable status; the loop's own words survive", async () => {
  const unreadable = [{ file: "state.json", problem: "not_json", offset: 12, remedy: "Checks fail and send nothing until it is repaired. Stop the daemon, service or scheduler, then restore state.json from a copy." }];
  // With state.json unreadable, status leaves out state and targetAgent and exits 1.
  const { state: _s, targetAgent: _t, ...rest } = healthy;
  const w = await loopWith({ ...rest, unreadable, sendRecord: "sends.json cannot be read, so no check sends anything; restore it from a copy. Deleting it loses the record of sends." }, 1);
  try {
    const r = await w.forThread();
    assert.deepEqual(r.errors, []); assert.equal(r.loops.length, 1);
    assert.deepEqual(reasonLines("enabled", null, r.loops[0]!.details), [
      "state.json could not be read (its content is not JSON, at byte 12). Checks fail and send nothing until it is repaired.",
      "Send record: sends.json cannot be read, so no check sends anything; restore it from a copy.",
    ]);
  } finally { await w.dispose(); }
  // A failure with no unreadable record is still an unavailable status.
  const broken = await loopWith({ ...healthy }, 1);
  try { const r = await broken.forThread(); assert.equal(r.loops.length, 0); assert.equal(r.errors.length, 1); } finally { await broken.dispose(); }
});

test("a long limit wait says how long, and that the reset falls after the deadline when it does", () => {
  const d = (deadline: string): StatusDetails => ({ readAt: "2026-01-01T00:00:00Z", completed: false, targetThread: "thr_x", targetAdapter: "bb", targetConfigured: true, runtime: null, daemon: null, lastCheck: null,
    limitWait: { state: "waiting", reason: "limit_no_fallback", resetsAt: "2026-01-06T00:00:00Z", resetIn: "5 days", resetPassed: false }, deadline: { at: deadline, reached: false } });
  const after = reasonLines("enabled", null, d("2026-01-03T00:00:00Z"));
  assert.match(after[0]!, /^Long limit wait: no handoff possible; resets .* \(in 5 days\); the loop sends nothing until then; the reset falls after this loop's deadline: the loop will be held by the deadline on its first check after it, before this wait can end$/);
  const before = reasonLines("enabled", null, d("2026-02-01T00:00:00Z"));
  assert.doesNotMatch(before[0]!, /deadline/);
});

test("a held loop's note is a visible reason, cut short; an enabled loop's note is not", () => {
  const note = "Needs a decision. " + "x".repeat(400);
  const lines = reasonLines("awaiting user", note, undefined);
  assert.equal(lines.length, 1); assert.ok(lines[0]!.startsWith("Note: Needs a decision.")); assert.ok(lines[0]!.length <= 170);
  assert.deepEqual(reasonLines("enabled", note, undefined), []);
});

test("a title is cut to the length asked", () => { assert.equal(boundedTitle("a".repeat(2000), 600).length, 600); assert.equal(boundedTitle("short"), "short"); });

test("a malformed optional field is named as unreadable and the loop is kept, with exit 0 or 1", async () => {
  const bad = { ...healthy, diskSpace: "garbage", sendAllowance: { max: "x" }, unreadable: "nope", deadline: 5, deadlineReached: "yes", sendRecord: 7, control: { supersededBy: 3 } };
  for (const [exit, kept] of [[0, true], [1, false]] as const) {
    const w = await loopWith(bad, exit);
    try {
      const r = await w.forThread();
      // Exit 1 is accepted only when status names an unreadable list; "nope" is not one, so that stays an unavailable status.
      assert.equal(r.loops.length, kept ? 1 : 0, `exit ${exit}`);
      if (!kept) continue;
      assert.deepEqual(r.errors, []); assert.equal(r.loops[0]!.controlState, "enabled"); assert.ok(recordsUnreadable(r.loops[0]!.details));
      const d = r.loops[0]!.details as Record<string, unknown>;
      for (const key of ["disk", "sends", "unreadable", "deadline", "sendRecord", "supersededBy"]) assert.equal(d[key], undefined, key);
      assert.deepEqual(d.unparsed, ["supersededBy", "deadline", "deadlineReached", "sendAllowance", "sendRecord", "diskSpace", "unreadable"]);
      // A malformed unreadable field still says records are unreadable, never healthy.
      assert.deepEqual(reasonLines("enabled", null, d as unknown as StatusDetails), ["status reported records it could not read, and their details could not be read", "Status could not be read for: supersededBy, deadline, deadlineReached, sendAllowance, sendRecord, diskSpace, unreadable"]);
    } finally { await w.dispose(); }
  }
});

test("exit 1 with unreadable entries keeps the good ones and flags the bad; nothing parsed still reads as unreadable, never healthy", async () => {
  const good = { file: "state.json", problem: "not_json", remedy: "Checks fail and send nothing until it is repaired. Restore it." };
  const { state: _s, targetAgent: _t, ...rest } = healthy;
  const mixed = await loopWith({ ...rest, unreadable: [good, { file: "control.json", problem: "not_json" }] }, 1);
  try {
    const d = (await mixed.forThread()).loops[0]!.details;
    assert.deepEqual(d.unreadable?.map(u => u.file), ["state.json"]); assert.deepEqual(d.unparsed, ["unreadable"]);
    assert.ok(recordsUnreadable(d));
    assert.deepEqual(reasonLines("enabled", null, d), ["state.json could not be read (its content is not JSON). Checks fail and send nothing until it is repaired.", "Status could not be read for: unreadable"]);
  } finally { await mixed.dispose(); }
  const none = await loopWith({ ...rest, unreadable: [{ file: "control.json", problem: "not_json" }] }, 1);
  try {
    const { loops, errors } = await none.forThread();
    assert.deepEqual(errors, []); assert.equal(loops.length, 1);
    const d = loops[0]!.details;
    assert.equal(d.unreadable, undefined); assert.ok(recordsUnreadable(d));
    assert.deepEqual(reasonLines("enabled", null, d), ["status reported records it could not read, and their details could not be read", "Status could not be read for: unreadable"]);
  } finally { await none.dispose(); }
});

test("a malformed deadlineReached never prints reached: false, and the reset-after-deadline claim is not made", () => {
  const base: StatusDetails = { readAt: "2026-01-01T00:00:00Z", completed: false, targetThread: "thr_x", targetAdapter: "bb", targetConfigured: true, runtime: null, daemon: null, lastCheck: null, unparsed: ["deadlineReached"],
    deadline: { at: "2026-01-03T00:00:00Z" }, limitWait: { state: "waiting", reason: "limit_no_fallback", resetsAt: "2026-01-06T00:00:00Z", resetIn: "5 days", resetPassed: false } };
  const lines = diagnosticLines(base, "primary", true);
  assert.ok(lines.some(l => l.includes("(reached: could not be read; evaluated on next check)")));
  assert.ok(!lines.some(l => l.includes("reached: false")));
  assert.ok(!lines.some(l => /falls after this loop's deadline/i.test(l)));
  assert.ok(!reasonLines("enabled", null, base).some(l => l.startsWith("Deadline reached")));
});

test("an unpunctuated remedy is cut to 240 characters in the reasons", () => {
  const d: StatusDetails = { readAt: "2026-01-01T00:00:00Z", completed: false, targetThread: "thr_x", targetAdapter: "bb", targetConfigured: true, runtime: null, daemon: null, lastCheck: null,
    unreadable: [{ file: "state.json", problem: "not_json", remedy: "word ".repeat(200).trim() }] };
  const line = reasonLines("enabled", null, d)[0]!;
  const remedy = line.slice("state.json could not be read (its content is not JSON). ".length);
  assert.ok(remedy.length <= 240 && remedy.length > 200); assert.ok(remedy.endsWith("word…"), "cut at a whole word, with an ellipsis");
});

test("the listing says a low disk once, as disk=low, and nothing for an ok one", async () => {
  const message = "the volume that holds the state directory is filling (900 MiB free of 100 GiB, below 2.0 GiB): free space on it before records fail to save";
  const low = await loopWith({ ...healthy, diskSpace: { level: "low", message } });
  try { assert.equal((await low.listing()).match(/disk=low/g)?.length, 1); } finally { await low.dispose(); }
  const ok = await loopWith({ ...healthy, diskSpace: { level: "ok" } });
  try { assert.ok(!(await ok.listing()).includes("disk=")); } finally { await ok.dispose(); }
});

test("the listing says records unreadable, as the card does, where the control state beside it cannot be trusted", async () => {
  const { state: _s, targetAgent: _t, ...rest } = healthy;
  const w = await loopWith({ ...rest, controlState: "enabled", unreadable: [{ file: "control.json", problem: "not_json", remedy: "Checks send nothing until it is repaired." }] }, 1);
  try {
    const line = (await w.listing()).split("\n").find(l => l.startsWith("example-loop"))!;
    assert.match(line, /^example-loop\trecords unreadable\t/); assert.ok(!/\tenabled\t/.test(line));
  } finally { await w.dispose(); }
  const ok = await loopWith(healthy);
  try { assert.match((await ok.listing()).split("\n").find(l => l.startsWith("example-loop"))!, /^example-loop\tenabled\t/); } finally { await ok.dispose(); }
});
