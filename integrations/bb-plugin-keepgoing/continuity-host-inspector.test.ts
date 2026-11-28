import { test } from "node:test";
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
import type { ChildProcess } from "node:child_process";
import { spawn, spawnSync } from "node:child_process";
import { mkdtemp, writeFile, readFile, rm, readdir, realpath, mkdir, copyFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { inspectContinuityHost, normalizeContinuityHelper, continuityOutputLimit, type ContinuityHostProcess } from "./continuity-host-inspector";
import { continuityHelperBase64, continuityHelperSha256, continuityHelperSources } from "./continuity-host-inspector-asset";
import { createHash } from "node:crypto";

const bytes = (x: unknown) => Buffer.from(JSON.stringify(x));
const nil = { verdict: "cannot_assess", objectiveComplete: false };
const tick = () => new Promise<void>(r => setImmediate(r));
const report = () => ({ version: 1, verdict: "reconciliation_required", objectiveComplete: false,
  assessability: { localRegister: "declared_items_only", runtimeCoverage: "cannot_assess", objectiveAcceptance: "cannot_assess" },
  evidence: { workSha256: "a".repeat(64), registerRevision: 1, context: "b".repeat(64), assignments: "c".repeat(64), pins: "d".repeat(64) },
  itemCount: 1234, findings: [{ class: "empty_declared_inventory", subjectDigest: "e".repeat(64), recommendedAction: "inventory_existing_authorized_work_without_inventing_completeness", autoAction: "none" }],
  missingInputs: ["empty_declared_inventory"], limitations: ["no_authority_or_completion_inferred", "no_runtime_mutation", "no_automatic_turn_end_hook", "single_coordinator_editor", "captures_not_live_authenticated"] });
function fake() {
  const child = Object.assign(new EventEmitter(), { stdout: new PassThrough(), stderr: new PassThrough(), pid: 12345 }) as unknown as ChildProcess;
  const calls: { file: string; args: string[]; options: unknown }[] = [], kills: string[] = [];
  let started!: () => void; const ready = new Promise<void>(r => { started = r; });
  const io: ContinuityHostProcess = { platform: "linux", spawn(file, args, options) { calls.push({ file, args, options }); started(); return child; }, kill(_c, signal) { kills.push(signal); } };
  return { child, calls, kills, ready, io };
}

test("actual minimized schema retains complete counts and exit1 reconciliation; no completion inference", () => {
  const r = normalizeContinuityHelper(bytes(report()), 1); assert.equal(r.verdict, "reconciliation_required");
  assert.ok("itemCount" in r); assert.equal(r.itemCount, 1234); assert.equal(r.findingCount, 1); assert.equal(r.missingInputCount, 1);
  assert.equal(r.objectiveComplete, false); assert.ok(Object.isFrozen(r));
  const held = report(); held.verdict = "runtime_held"; assert.equal(normalizeContinuityHelper(bytes(held), 1).verdict, "runtime_held");
  const clean = report(); clean.findings = []; clean.missingInputs = []; assert.equal(normalizeContinuityHelper(bytes(clean), 0).verdict, "reconciliation_required");
  assert.deepEqual(normalizeContinuityHelper(bytes(clean), 1), nil);
});

test("malformed, unknown, duplicate, oversized, exit and UTF8 controls refuse whole report", () => {
  for (const mutate of [
    (r: any) => { r.version = 2; }, (r: any) => { delete r.evidence.context; }, (r: any) => { r.private = "/private/example"; },
    (r: any) => { r.objectiveComplete = true; }, (r: any) => { r.itemCount = -1; }, (r: any) => { r.itemCount = 1.5; },
    (r: any) => { r.evidence.registerRevision = 0; }, (r: any) => { r.evidence.workSha256 = null; },
    (r: any) => { r.findings[0].recommendedAction = "private unexpected text"; }, (r: any) => { r.findings[0].class = "new_unknown_code"; },
    (r: any) => { r.missingInputs.push("unknown_private_error"); }, (r: any) => { r.limitations.pop(); }, (r: any) => { r.findings[0].class = "enabled"; },
    (r: any) => { r.findings[0].recommendedAction = "source_drift"; },
    (r: any) => { r.assessability.runtimeCoverage = "complete"; },
  ]) { const r = report(); mutate(r); assert.deepEqual(normalizeContinuityHelper(bytes(r), 1), nil); }
  for (const raw of [Buffer.from('{"version":1,"version":2}'), Buffer.from(JSON.stringify(report()).replace('"version":1', '"version":2,"version":1')),
    Buffer.from('{"nested":{"x":1,"x":1}}'), Buffer.alloc(continuityOutputLimit + 1, 32), Buffer.from([0xff]), Buffer.from(JSON.stringify(report()) + "private trailing text")])
    assert.deepEqual(normalizeContinuityHelper(raw, 1), nil);
  assert.deepEqual(normalizeContinuityHelper(bytes(report()), 0), nil);
  assert.deepEqual(normalizeContinuityHelper(bytes(report()), 2), nil);
  assert.deepEqual(normalizeContinuityHelper(bytes(report()), null), nil);
  assert.deepEqual(normalizeContinuityHelper(bytes({ version: 1, verdict: "cannot_assess", objectiveComplete: false, reason: "input_or_output_unavailable", autoAction: "none" }), 1), nil);
});

test("fixed argv, snapshot paths, late close and no raw stderr exposure", async () => {
  const f = fake(), abort = new AbortController(), paths = { work: "/private/example/WORK.md", context: "/private/example/context.json" };
  const pending = inspectContinuityHost(paths, abort.signal, f.io); paths.work = "/private/changed.md";
  await f.ready;
  assert.equal(f.calls[0].file, "python3"); assert.deepEqual(f.calls[0].args.slice(0, 2), ["-I", "-B"]);
  assert.deepEqual(f.calls[0].args.slice(3), ["check", "--work", "/private/example/WORK.md", "--context", "/private/example/context.json", "--max-age", "300"]);
  assert.deepEqual(f.calls[0].options, { detached: true, stdio: ["ignore", "pipe", "pipe"] });
  f.child.stderr!.emit("data", Buffer.from("private diagnostic")); f.child.stdout!.emit("data", bytes(report()));
  let settled = false; void pending.then(() => { settled = true; }); await tick(); assert.equal(settled, false);
  const helper = f.calls[0].args[2]; assert.equal(createHash("sha256").update(await readFile(helper)).digest("hex"), continuityHelperSha256);
  f.child.emit("close", 1, null); assert.equal((await pending).verdict, "reconciliation_required");
  await assert.rejects(readFile(helper));
});

test("cancel before spawn and unsupported platform/invalid paths never spawn", async () => {
  const f = fake(), a = new AbortController(); a.abort();
  assert.deepEqual(await inspectContinuityHost({ work: "/private/example/WORK.md" }, a.signal, f.io), nil);
  assert.deepEqual(await inspectContinuityHost({ work: "/private/example/WORK.md" }, new AbortController().signal, { ...f.io, platform: "win32" }), nil);
  for (const work of ["relative", "/private/example\nsecret", "", "/" + "a".repeat(4096)]) assert.deepEqual(await inspectContinuityHost({ work }, new AbortController().signal, f.io), nil);
  assert.equal(f.calls.length, 0);
});

test("cancel during child never settles until physical close, even after SIGKILL", async () => {
  const f = fake(), a = new AbortController(), pending = inspectContinuityHost({ work: "/private/example/WORK.md" }, a.signal, f.io);
  await f.ready; let settled = false; void pending.then(() => { settled = true; }); a.abort(); await tick(); assert.deepEqual(f.kills, ["SIGTERM"]); assert.equal(settled, false);
  await new Promise(r => setTimeout(r, 300)); assert.deepEqual(f.kills, ["SIGTERM", "SIGKILL"]); assert.equal(settled, false);
  f.child.stdout!.emit("data", bytes(report())); f.child.emit("exit", 1, null); await tick(); assert.equal(settled, false);
  f.child.emit("close", 1, null); assert.deepEqual(await pending, nil); assert.equal(a.signal.onabort, null);
});

test("spawn error/absent Python and overflow await close and never return prefix", async () => {
  for (const mode of ["error", "stdout", "stderr"] as const) {
    const f = fake(), pending = inspectContinuityHost({ work: "/private/example/WORK.md" }, new AbortController().signal, f.io); await f.ready;
    let settled = false; void pending.then(() => { settled = true; });
    if (mode === "error") f.child.emit("error", Object.assign(new Error("private executable path"), { code: "ENOENT" }));
    else if (mode === "stdout") { f.child.stdout!.emit("data", bytes(report())); f.child.stdout!.emit("data", Buffer.alloc(continuityOutputLimit)); }
    else f.child.stderr!.emit("data", Buffer.alloc(65537));
    await tick(); assert.equal(settled, false); assert.deepEqual(f.kills, ["SIGTERM"]);
    f.child.emit("close", mode === "error" ? null : 1, null); assert.deepEqual(await pending, nil);
  }
});

test("deterministic asset is exact canonical source, drift checker and isolated actual helper preserve inputs", { skip: process.platform === "win32" }, async () => {
  const root = fileURLToPath(new URL(".", import.meta.url)), dir = await realpath(await mkdtemp(join(tmpdir(), "example-continuity-fixture-")));
  try {
    const generated = spawnSync("python3", ["-I", "-B", join(root, "continuity-host-inspector-generate.py"), "--check"], { encoding: "utf8" }); assert.equal(generated.status, 0, generated.stderr);
    for (const [name, digest] of Object.entries(continuityHelperSources)) assert.equal(createHash("sha256").update(await readFile(join(root, "skills/keepgoing/scripts", name))).digest("hex"), digest);
    const helper = join(dir, "helper.pyz"), work = join(dir, "Exámple 工作 WORK.md"); await writeFile(helper, Buffer.from(continuityHelperBase64, "base64"), { mode: 0o600 });
    const archive = spawnSync("python3", ["-I", "-B", "-c", "import pathlib,sys,zipfile; z=zipfile.ZipFile(sys.argv[1]); root=pathlib.Path(sys.argv[2]); names=['project_work.py','coordinator_coverage.py','coordinator.py','coordinator_state.py']; assert set(z.namelist())==set(names+['__main__.py']); assert all(z.read(n)==(root/n).read_bytes() for n in names)", helper, join(root, "skills/keepgoing/scripts")], { encoding: "utf8" }); assert.equal(archive.status, 0, archive.stderr);
    const init = spawnSync("python3", ["-I", "-B", helper, "init", "--work", work, "--id", "example"], { encoding: "utf8" }); assert.equal(init.status, 0, init.stdout + init.stderr);
    const before = await readFile(work), names = await readdir(dir);
    const result = await inspectContinuityHost({ work }, new AbortController().signal); assert.equal(result.verdict, "reconciliation_required"); assert.ok("itemCount" in result); assert.equal(result.itemCount, 0); assert.ok(result.findingCount > 0); assert.equal(result.objectiveComplete, false);
    const actual = spawnSync("python3", ["-I", "-B", helper, "check", "--work", work, "--max-age", "300"], { encoding: "buffer" }); assert.equal(actual.status, 1); assert.deepEqual(normalizeContinuityHelper(actual.stdout, actual.status), result);
    assert.deepEqual(await readFile(work), before); assert.deepEqual(await readdir(dir), names);
    assert.deepEqual(await inspectContinuityHost({ work: join(dir, "missing-WORK.md") }, new AbortController().signal), nil);
    assert.deepEqual(await readFile(work), before); assert.deepEqual(await readdir(dir), names);
  } finally { await rm(dir, { recursive: true, force: true }); }
});


test("owned real child cancellation is reaped and actual missing executable refuses", { skip: process.platform === "win32" }, async () => {
  const a = new AbortController();
  let ready!: () => void; const started = new Promise<void>(r => { ready = r; });
  let child: ChildProcess | undefined, closed = false;
  const io: ContinuityHostProcess = { platform: process.platform, spawn(_file, _args, options) {
    child = spawn("python3", ["-I", "-B", "-c", "import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); print('ready',flush=True); time.sleep(30)"], options);
    child.stdout!.once("data", ready); child.once("close", () => { closed = true; }); return child;
  }, kill(c, signal) { if (c.pid) process.kill(-c.pid, signal); } };
  try {
    const pending = inspectContinuityHost({ work: "/private/example/WORK.md" }, a.signal, io);
    await started; a.abort(); assert.equal(closed, false); assert.deepEqual(await pending, nil); assert.equal(closed, true);
    assert.throws(() => process.kill(-child!.pid!, 0));
  } finally { if (child && !closed && child.pid) { try { process.kill(-child.pid, "SIGKILL"); } catch {} } }
  const absent: ContinuityHostProcess = { ...io, spawn(_file, args, options) { return spawn("/nonexistent-example/python3", args, options); }, kill() {} };
  assert.deepEqual(await inspectContinuityHost({ work: "/private/example/WORK.md" }, new AbortController().signal, absent), nil);
});

test("source drift refuses rather than regenerating or accepting stale bundled source", async () => {
  const root = fileURLToPath(new URL(".", import.meta.url)), dir = await mkdtemp(join(tmpdir(), "example-helper-drift-"));
  try {
    await mkdir(join(dir, "skills/keepgoing/scripts"), { recursive: true });
    for (const name of ["continuity-host-inspector-generate.py", "continuity-host-inspector-asset.ts"]) await copyFile(join(root, name), join(dir, name));
    for (const name of Object.keys(continuityHelperSources)) await copyFile(join(root, "skills/keepgoing/scripts", name), join(dir, "skills/keepgoing/scripts", name));
    const script = join(dir, "continuity-host-inspector-generate.py");
    assert.equal(spawnSync("python3", ["-I", "-B", script, "--check"]).status, 0);
    const source = join(dir, "skills/keepgoing/scripts/project_work.py"); await writeFile(source, Buffer.concat([await readFile(source), Buffer.from("\n# fictional drift\n")]));
    const before = await readFile(join(dir, "continuity-host-inspector-asset.ts"));
    const refused = spawnSync("python3", ["-I", "-B", script, "--check"], { encoding: "utf8" }); assert.equal(refused.status, 1); assert.match(refused.stderr, /continuity_helper_source_drift/);
    assert.deepEqual(await readFile(join(dir, "continuity-host-inspector-asset.ts")), before);
  } finally { await rm(dir, { recursive: true, force: true }); }
});


test("abort during asynchronous asset preparation never starts a child", async () => {
  const f = fake(), a = new AbortController(); const pending = inspectContinuityHost({ work: "/private/example/WORK.md" }, a.signal, f.io);
  a.abort(); assert.deepEqual(await pending, nil); assert.equal(f.calls.length, 0);
});

test("deadline requests termination but still requires actual close", async () => {
  const f = fake(); let stopped!: () => void; const stop = new Promise<void>(r => { stopped = r; });
  f.io.kill = (_child, signal) => { f.kills.push(signal); stopped(); };
  const pending = inspectContinuityHost({ work: "/private/example/WORK.md" }, new AbortController().signal, f.io);
  await f.ready; let settled = false; void pending.then(() => { settled = true; });
  await stop; assert.equal(settled, false); assert.equal(f.kills[0], "SIGTERM");
  f.child.stdout!.emit("data", bytes(report())); f.child.emit("close", 1, null); assert.deepEqual(await pending, nil);
});

test("actual packaged canonical bound/unbound scenarios preserve verdict, all counts and evidence", { skip: process.platform === "win32" }, async () => {
  const tools = fileURLToPath(new URL("../../tools/coordinator-workflow/", import.meta.url));
  const dir = await realpath(await mkdtemp(join(tmpdir(), "example-canonical-host-")));
  const prepare = `from pathlib import Path
import sys,json,datetime
sys.path.insert(0,sys.argv[1])
from test_project_work import register,work
root=Path(sys.argv[2]);root.mkdir(mode=0o700);case=sys.argv[3]
i,c,r=register(root)
now=datetime.datetime.now(datetime.timezone.utc).isoformat().replace('+00:00','Z')
c['observedAt']=now;r['revisions'][-1]['inventory']['observedAt']=now
if case=='held':c['controls']['paused']=True
if case=='unbound':i.update(binding=None,assignmentRecord=None,assignmentRevision=None)
if case=='open-child':i['items'][0].update(state='open',acceptance=None)
if case=='source-drift':(root/'scope.md').write_text('Fictional source drift')
if case=='missing-assignment':r=None
if case=='person-gate':i['items'][0]['personGate']={'state':'held','refs':['example:person']}
if case=='unknown-item':i['items'][0].update(state='unknown',acceptance=None)
for name,data in [('WORK.md',work.markdown(i,'looptender-work','# Fictional canonical parity')),('context.json',json.dumps(c).encode()),('assignments.json',json.dumps(r).encode())]:
 p=root/name;p.write_bytes(data);p.chmod(0o600)
`;
  try {
    const helper = join(dir, "helper.pyz"); await writeFile(helper, Buffer.from(continuityHelperBase64, "base64"), { mode: 0o600 });
    for (const scenario of ["base", "held", "unbound", "open-child", "source-drift", "missing-assignment", "person-gate", "unknown-item"]) {
      const fixture = join(dir, scenario), init = spawnSync("python3", ["-I", "-B", "-c", prepare, tools, fixture, scenario], { encoding: "utf8" });
      assert.equal(init.status, 0, init.stderr);
      const paths = { work: join(fixture, "WORK.md"), context: join(fixture, "context.json"), ...(scenario === "missing-assignment" ? {} : { assignments: join(fixture, "assignments.json") }) };
      const before = await Promise.all(Object.values(paths).map(p => readFile(p)));
      const checked = spawnSync("python3", ["-I", "-B", helper, "check", "--work", paths.work, "--context", paths.context, ...(paths.assignments ? ["--assignments", paths.assignments] : []), "--max-age", "300"], { encoding: "utf8" });
      assert.equal(checked.status, 1, scenario); const canonical = JSON.parse(checked.stdout);
      assert.ok(["runtime_held", "reconciliation_required"].includes(canonical.verdict), scenario);
      const expected = { verdict: canonical.verdict, objectiveComplete: false, workSha256: canonical.evidence.workSha256,
        registerRevision: canonical.evidence.registerRevision, itemCount: canonical.itemCount,
        findingCount: canonical.findings.length, missingInputCount: canonical.missingInputs.length };
      assert.deepEqual(normalizeContinuityHelper(Buffer.from(checked.stdout), checked.status), expected, scenario);
      assert.deepEqual(await inspectContinuityHost(paths, new AbortController().signal), expected, scenario);
      if (scenario === "held" || scenario === "base") assert.ok(canonical.missingInputs.includes("objective_acceptance_unavailable"), scenario);
      assert.deepEqual(await Promise.all(Object.values(paths).map(p => readFile(p))), before, scenario);
      const unknown = structuredClone(canonical); unknown.missingInputs.push("unrecognized_example_reason");
      assert.deepEqual(normalizeContinuityHelper(bytes(unknown), 1), nil, scenario);
    }
  } finally { await rm(dir, { recursive: true, force: true }); }
});
