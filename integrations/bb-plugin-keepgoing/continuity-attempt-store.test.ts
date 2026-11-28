import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, realpathSync, rmSync, readFileSync, writeFileSync, readdirSync, symlinkSync, mkdirSync, renameSync } from "node:fs";
import { spawn, spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createAttemptStore, attemptRecordLimit } from "./continuity-attempt-store.ts";
import { makeContinuityRequest, makeContinuityReceipt } from "./continuity-registration-contract.ts";
const enrollment = { binding: { id: "work_example", revision: 1, hostId: "host_example", threadId: "thr_example", projectId: "project_example", environmentId: "env_example" }, paths: { work: "/private/example/WORK.md" } };
const namespace = "00000000-0000-4000-8000-000000000001";
const request = (n = 2) => makeContinuityRequest(namespace, `00000000-0000-4000-8000-${String(n).padStart(12,"0")}`, enrollment);
function fixture() { const path = realpathSync(mkdtempSync(join(tmpdir(), "continuity-receipt-"))); return { path, close: () => rmSync(path, { recursive: true, force: true }) }; }
test("exclusive durable reservation fences duplicate handlers and exact terminal lookup survives reply loss", { skip: process.platform === "win32" }, () => {
  const f = fixture(); try {
    const a = createAttemptStore(f.path), b = createAttemptStore(f.path), r = request();
    assert.equal(a.lookup(r).state, "unknown");
    const claim = a.reserve(r); assert.ok(claim); assert.equal(b.reserve(r), null);
    assert.equal(b.lookup(r).state, "unknown");
    const receipt = makeContinuityReceipt(r, "child_closed"); claim.commit(receipt);
    assert.deepEqual(b.lookup(r), { state: "settled", receipt });
    assert.equal(b.reserve(r), null); assert.throws(() => claim.commit(receipt));
    assert.equal(b.lookup(makeContinuityRequest(namespace, r.attempt, { ...enrollment, binding: { ...enrollment.binding, revision: 2 } })).state, "unknown");
  } finally { f.close(); }
});
test("aggregate charged capacity refuses without evicting old evidence", { skip: process.platform === "win32" }, () => {
  const f = fixture(); try {
    const s = createAttemptStore(f.path, { aggregateLimit: 3 * attemptRecordLimit });
    const claim = s.reserve(request()); assert.ok(claim); claim.commit(makeContinuityReceipt(request(), "no_child_started"));
    assert.throws(() => s.reserve(request(3)), /capacity/);
    assert.equal(s.lookup(request()).state, "settled");
  } finally { f.close(); }
});
test("failed claim durability fences retry and cannot produce terminal proof", { skip: process.platform === "win32" }, () => {
  const f = fixture(); try {
    const s = createAttemptStore(f.path, { fault: phase => { if (phase === "claim_synced") throw Error("crash"); } });
    assert.throws(() => s.reserve(request()));
    assert.equal(createAttemptStore(f.path).reserve(request()), null);
    assert.equal(createAttemptStore(f.path).lookup(request()).state, "unknown");
  } finally { f.close(); }
});
test("malformed, oversized, symlink and missing terminal records never prove settlement", { skip: process.platform === "win32" }, () => {
  const f = fixture(); try {
    const s = createAttemptStore(f.path); s.reserve(request());
    const dir = join(f.path, "continuity-attempts-v2", readdirSync(join(f.path, "continuity-attempts-v2")).find(x => x !== ".admission")!);
    const terminal = join(dir, "terminal.json");
    for (const raw of ["{", " ".repeat(attemptRecordLimit + 1)]) { writeFileSync(terminal, raw, { mode: 0o600 }); assert.equal(s.lookup(request()).state, "unknown"); }
    rmSync(terminal); symlinkSync(join(dir, "claim.json"), terminal); assert.equal(s.lookup(request()).state, "unknown");
    assert.ok(readFileSync(join(dir, "claim.json")).length); assert.throws(() => s.reserve(request()));
  } finally { f.close(); }
});

test("FIFO/directory/partial records fail closed promptly", { skip: process.platform === "win32" }, () => {
  for (const kind of ["fifo", "directory", "partial", "duplicate-key"] as const) {
    const f = fixture(); try {
      const s = createAttemptStore(f.path); s.reserve(request());
      const dir = join(f.path, "continuity-attempts-v2", readdirSync(join(f.path, "continuity-attempts-v2"))[0]), path = join(dir, "terminal.json");
      if (kind === "fifo") assert.equal(spawnSync("mkfifo", [path]).status, 0);
      if (kind === "directory") mkdirSync(path, { mode: 0o700 });
      if (kind === "partial") writeFileSync(path, '{"request":', { mode: 0o600 });
      if (kind === "duplicate-key") writeFileSync(path, '{"request":{},"request":{}}', { mode: 0o600 });
      const start = performance.now(); assert.equal(s.lookup(request()).state, "unknown"); assert.ok(performance.now()-start < 1000);
    } finally { f.close(); }
  }
});

test("terminal write fault cannot replay and post-write acknowledgement loss permits exact lookup", { skip: process.platform === "win32" }, () => {
  for (const phase of ["before_terminal", "terminal_synced"] as const) {
    const f = fixture(); try {
      const s = createAttemptStore(f.path, { fault: p => { if (p === phase) throw Error("fictional crash"); } });
      const claim = s.reserve(request()); assert.ok(claim);
      assert.throws(() => claim.commit(makeContinuityReceipt(request(), "child_closed")));
      assert.equal(createAttemptStore(f.path).lookup(request()).state, phase === "terminal_synced" ? "settled" : "unknown");
      assert.equal(createAttemptStore(f.path).reserve(request()), null);
    } finally { f.close(); }
  }
});


test("two independent processes competing for one attempt acquire at most one reservation", { skip: process.platform === "win32" }, async () => {
  const f = fixture(); try {
    const script = `import { createAttemptStore } from ${JSON.stringify(new URL("./continuity-attempt-store.ts", import.meta.url).href)};
      try { const c = createAttemptStore(process.env.FIXTURE_DIR).reserve(JSON.parse(process.env.FIXTURE_REQUEST)); process.stdout.write(c ? "claimed" : "fenced"); }
      catch { process.stdout.write("fenced"); }`;
    const run = () => new Promise<string>((resolve, reject) => {
      const child = spawn(process.execPath, ["--import", "tsx", "--input-type=module", "-e", script], {
        env: { ...process.env, FIXTURE_DIR: f.path, FIXTURE_REQUEST: JSON.stringify(request()) }, stdio: ["ignore", "pipe", "pipe"] });
      let out = "", err = ""; child.stdout.on("data", b => out += b); child.stderr.on("data", b => err += b);
      child.on("error", reject); child.on("close", code => code === 0 ? resolve(out) : reject(Error(err)));
    });
    const outcomes = await Promise.all([run(), run()]); assert.equal(outcomes.filter(x => x === "claimed").length, 1);
    assert.equal(createAttemptStore(f.path).lookup(request()).state, "unknown");
  } finally { f.close(); }
});


test("durability fault windows refuse replay and incomplete terminal publication", { skip: process.platform === "win32" }, () => {
  for (const phase of ["before_claim_write", "before_claim_sync", "before_terminal_sync", "before_terminal_publish", "after_terminal_publish"] as const) {
    const f = fixture(); try {
      const s = createAttemptStore(f.path, { fault: p => { if (p === phase) throw Error("fictional storage failure"); } });
      if (phase.startsWith("before_claim")) assert.throws(() => s.reserve(request()));
      else { const claim = s.reserve(request()); assert.ok(claim); assert.throws(() => claim.commit(makeContinuityReceipt(request(), "child_closed"))); }
      assert.equal(createAttemptStore(f.path).lookup(request()).state, "unknown");
      assert.equal(createAttemptStore(f.path).reserve(request()), null);
    } finally { f.close(); }
  }
  assert.throws(() => createAttemptStore("/private/example/unread", { platform: "win32" }), /unsupported/);
});

test("unsupported receipt platform refuses before accessing storage", () => {
  assert.throws(() => createAttemptStore("/private/example/unused", { platform: "win32" }), /unsupported/);
});


test("replaced attempt directory and orphan admission gate never authorize replay", { skip: process.platform === "win32" }, () => {
  const f = fixture(); try {
    const store = createAttemptStore(f.path), claim = store.reserve(request()); assert.ok(claim);
    const root = join(f.path, "continuity-attempts-v2"), dir = join(root, readdirSync(root)[0]);
    const original = readFileSync(join(dir, "claim.json"));
    renameSync(dir, join(f.path, "retained-original")); mkdirSync(dir, { mode: 0o700 });
    writeFileSync(join(dir, "claim.json"), original, { mode: 0o600 });
    assert.throws(() => claim.commit(makeContinuityReceipt(request(), "child_closed")));
    assert.equal(store.lookup(request()).state, "unknown"); assert.equal(store.reserve(request()), null);
    writeFileSync(join(root, ".admission"), "", { mode: 0o600 });
    assert.throws(() => store.reserve(request(3))); assert.equal(store.lookup(request()).state, "unknown");
    assert.deepEqual(readFileSync(join(dir, "claim.json")), original);
  } finally { f.close(); }
});
