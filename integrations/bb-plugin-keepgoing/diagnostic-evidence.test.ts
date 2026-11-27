import { test } from "node:test";
import assert from "node:assert/strict";
import { diskEvidence, diskLine, diskSummary, type StatusDetails } from "./status-details.ts";
import { refusalMessage, refusalSchema } from "./setup-contract.ts";

const at = "2026-01-02T03:04:05Z", clock = Date.parse(at);
const details = (disk: object) => ({ readAt: "2099-01-01T00:00:00Z", disk } as StatusDetails);
const measured = { level: "critical", message: "Fictional volume nearly full", measuredAt: at, source: "filesystem" };

test("disk ages use measurement time, not view readAt; cached and stale remain distinct", () => {
  const d = details(measured);
  assert.deepEqual(diskEvidence(d, false, clock + 10_000), { state: "recent", age: `10s old; measured ${at}` });
  assert.equal(diskEvidence(d, false, clock + 30_000).state, "cached");
  assert.equal(diskEvidence(d, false, clock + 91_000).state, "stale");
  assert.equal(diskEvidence(d, false, clock + 86_400_000).age, `1d old; measured ${at}`);
  assert.match(diskSummary(d, true, clock), /critical \(stale; refresh failed\)/);
  assert.match(diskLine(d, true, clock + 30_000), /30s old; measured 2026-01-02T03:04:05Z.*filesystem reading for the device volume holding loop state/);
  assert.match(diskSummary(details({ ...measured, level: "low" }), false, clock), /low \(recent\)/);
});

test("legacy, failed probes, malformed and future clocks never acquire a sample from readAt", () => {
  for (const disk of [
    { level: "low", message: "Legacy warning" },
    { ...measured, source: undefined },
    { ...measured, measuredAt: "not-a-time" },
    { ...measured, measuredAt: "0001-01-01T00:00:00Z" },
    { ...measured, measuredAt: "2026-01-02T03:04:06Z" },
    { ...measured, level: "unknown" },
  ]) {
    assert.deepEqual(diskEvidence(details(disk), false, clock), { state: "unavailable", age: "measurement time/provenance unavailable" });
    assert.doesNotMatch(diskLine(details(disk), false, clock), /2099|0s old/);
  }
  assert.match(diskSummary(details({ level: "unknown", message: "No sample" })), /disk: unavailable$/);
});

test("held registration refusal explains person-controlled choices without calling it active", () => {
  for (const held of ["completed", "paused", "stopped", "awaiting a person"] as const) {
    const r = refusalSchema.parse({ code: "second_loop_on_session", otherLoop: "example-loop", held, archived: true });
    const text = refusalMessage(r, "init");
    assert.match(text, new RegExp(`registered and enabled, held: ${held}; archived`));
    assert.match(text, /unarchive resumes nothing/);
    assert.match(text, /person must review its task, routes, limits and holds/);
    assert.match(text, /resume clears other holds too/);
    assert.match(text, /disable example-loop first, then preview and attach/);
    assert.match(text, /Pause or archive alone does not release/);
    assert.doesNotMatch(text, /already supervises|pause or disable|allow-second-loop/i);
  }
  assert.match(refusalMessage({ code: "second_loop_on_session", otherLoop: "example-loop" }, "start"), /current activity is unverified.*Nothing was started/);
});
