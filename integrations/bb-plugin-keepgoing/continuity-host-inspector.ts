/** Disconnected, explicit-path host foundation. No registration, discovery or checkpoint writes. */
import { spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { isAbsolute, join } from "node:path";
import { createHash } from "node:crypto";
import { TextDecoder } from "node:util";
import type { ContinuityInspection } from "./continuity-observer";
import { continuityHelperBase64, continuityHelperSha256, continuityHelperTokens, continuityHelperCodes, continuityHelperActions } from "./continuity-host-inspector-asset";

export type ContinuityHostPaths = Readonly<{ work: string; context?: string; assignments?: string; previous?: string }>;
const unavailable = (): ContinuityInspection => Object.freeze({ verdict: "cannot_assess", objectiveComplete: false });
export const continuityOutputLimit = 1024 * 1024;
const vocabulary = new Set(continuityHelperTokens);
const codes = new Set(continuityHelperCodes), actions = new Set(continuityHelperActions);
const hash = (x: unknown): x is string => typeof x === "string" && /^[a-f0-9]{64}$/.test(x);
const count = (x: unknown): x is number => typeof x === "number" && Number.isSafeInteger(x) && x >= 0;
const plain = (x: unknown): x is Record<string, unknown> => !!x && typeof x === "object" && !Array.isArray(x) && Object.getPrototypeOf(x) === Object.prototype;
const keys = (x: Record<string, unknown>, names: string) => Object.keys(x).sort().join(" ") === names.split(" ").sort().join(" ");
const token = (x: unknown) => typeof x === "string" && vocabulary.has(x);
const limitations = ["no_authority_or_completion_inferred", "no_runtime_mutation", "no_automatic_turn_end_hook", "single_coordinator_editor", "captures_not_live_authenticated"];

// JSON.parse alone loses duplicate keys. Scan the complete bounded JSON grammar before parsing.
function unambiguousJSON(text: string): unknown {
  const ts = text.match(/"(?:[^"\\\u0000-\u001f]|\\(?:["\\/bfnrt]|u[0-9a-fA-F]{4}))*"|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?|true|false|null|[{}\[\],:]|\s+|./gs) ?? [];
  const parts = ts.filter(t => !/^\s+$/.test(t)); let at = 0;
  function value(depth: number): void {
    if (depth > 16) throw 0;
    const t = parts[at++];
    if (t === "{") {
      const seen = new Set<string>(); if (parts[at] === "}") { at++; return; }
      for (;;) { const k = parts[at++]; if (!k?.startsWith('"')) throw 0; const key = JSON.parse(k); if (seen.has(key)) throw 0; seen.add(key);
        if (parts[at++] !== ":") throw 0; value(depth + 1); const end = parts[at++]; if (end === "}") break; if (end !== ",") throw 0; }
    } else if (t === "[") {
      if (parts[at] === "]") { at++; return; }
      for (;;) { value(depth + 1); const end = parts[at++]; if (end === "]") break; if (end !== ",") throw 0; }
    } else if (!t || !/^(?:"|-?\d|true$|false$|null$)/.test(t)) throw 0;
  }
  value(0); if (at !== parts.length) throw 0;
  return JSON.parse(text);
}

/** Normalize only the actual fixed non-private `check` report. Never retain raw text or errors. */
export function normalizeContinuityHelper(stdout: Uint8Array, exitCode: number | null): ContinuityInspection {
  if (stdout.byteLength > continuityOutputLimit || (exitCode !== 0 && exitCode !== 1)) return unavailable();
  try {
    const r = unambiguousJSON(new TextDecoder("utf-8", { fatal: true }).decode(stdout));
    if (!plain(r) || r.version !== 1 || r.objectiveComplete !== false) return unavailable();
    if (r.verdict === "cannot_assess") {
      // A valid helper refusal is unavailable too; it is never a reconciled/empty inventory.
      if (!keys(r, "version verdict objectiveComplete reason autoAction") || !token(r.reason) || r.autoAction !== "none" || exitCode !== 1) return unavailable();
      return unavailable();
    }
    if (!keys(r, "version verdict objectiveComplete assessability evidence itemCount findings missingInputs limitations") ||
        !["runtime_held", "reconciliation_required"].includes(r.verdict as string) || !count(r.itemCount) ||
        !plain(r.assessability) || !keys(r.assessability, "localRegister runtimeCoverage objectiveAcceptance") ||
        r.assessability.localRegister !== "declared_items_only" || r.assessability.objectiveAcceptance !== "cannot_assess" ||
        !["cannot_assess", "partially_assessable", "assessable_declared_scope"].includes(r.assessability.runtimeCoverage as string) ||
        !plain(r.evidence) || !keys(r.evidence, "workSha256 registerRevision context assignments pins") ||
        !hash(r.evidence.workSha256) || !count(r.evidence.registerRevision) || r.evidence.registerRevision === 0 ||
        ![r.evidence.context, r.evidence.assignments, r.evidence.pins].every(hash) ||
        !Array.isArray(r.findings) || !Array.isArray(r.missingInputs) || !r.missingInputs.every(x => typeof x === "string" && codes.has(x)) ||
        new Set(r.missingInputs).size !== r.missingInputs.length ||
        !Array.isArray(r.limitations) || JSON.stringify(r.limitations) !== JSON.stringify(limitations)) return unavailable();
    for (const f of r.findings) if (!plain(f) || !keys(f, "class subjectDigest recommendedAction autoAction") ||
      !(typeof f.class === "string" && codes.has(f.class)) || !hash(f.subjectDigest) || !(typeof f.recommendedAction === "string" && actions.has(f.recommendedAction)) || f.autoAction !== "none") return unavailable();
    if (exitCode !== (r.findings.length || r.missingInputs.length ? 1 : 0)) return unavailable();
    return Object.freeze({ verdict: r.verdict as "runtime_held" | "reconciliation_required", objectiveComplete: false,
      workSha256: r.evidence.workSha256, registerRevision: r.evidence.registerRevision,
      itemCount: r.itemCount, findingCount: r.findings.length, missingInputCount: r.missingInputs.length });
  } catch { return unavailable(); }
}

type Spawn = (file: string, args: string[], options: { detached: boolean; stdio: ["ignore", "pipe", "pipe"] }) => ChildProcess;
/** Test seam is process behavior, never a caller-supplied executable or helper source. No SDK caller exists. */
export type ContinuityHostProcess = { spawn: Spawn; platform: string; kill(child: ChildProcess, signal: NodeJS.Signals): void };
const nativeProcess: ContinuityHostProcess = {
  spawn, platform: process.platform,
  kill(child, signal) { if (child.pid) { try { process.kill(-child.pid, signal); } catch { /* Close, not kill success, settles flight. */ } } },
};
function validPaths(p: ContinuityHostPaths): boolean {
  return plain(p) && Object.keys(p).every(k => ["work", "context", "assignments", "previous"].includes(k)) &&
    typeof p.work === "string" && Object.values(p).every(v => typeof v === "string" && v.length <= 4096 && isAbsolute(v) && !/[\u0000-\u001f\u007f]/.test(v));
}

/** Caller must resolve the authorized host and explicit private paths separately. Nothing binds identities here. */
export async function inspectContinuityHost(paths: ContinuityHostPaths, signal: AbortSignal,
  processIO: ContinuityHostProcess = nativeProcess): Promise<ContinuityInspection> {
  if (signal.aborted || !["darwin", "linux"].includes(processIO.platform) || !validPaths(paths)) return unavailable();
  // Freeze invocation paths before async preparation. Optional omitted captures remain unknown.
  const args = ["check", "--work", paths.work];
  for (const k of ["context", "assignments", "previous"] as const) if (paths[k] !== undefined) args.push(`--${k}`, paths[k]!);
  args.push("--max-age", "300");
  let directory: string | undefined;
  try {
    const asset = Buffer.from(continuityHelperBase64, "base64");
    if (createHash("sha256").update(asset).digest("hex") !== continuityHelperSha256) return unavailable();
    directory = await mkdtemp(join(tmpdir(), "looptender-continuity-"));
    const helper = join(directory, "helper.pyz");
    await writeFile(helper, asset, { mode: 0o600, flag: "wx" });
    if (signal.aborted) return unavailable();
    return await new Promise<ContinuityInspection>(resolve => {
      let child: ChildProcess;
      try { child = processIO.spawn("python3", ["-I", "-B", helper, ...args], { detached: true, stdio: ["ignore", "pipe", "pipe"] }); }
      catch { resolve(unavailable()); return; }
      let rejected = false, size = 0, stderrSize = 0, chunks: Buffer[] = [], force: ReturnType<typeof setTimeout> | undefined;
      const stop = () => {
        if (rejected) return; rejected = true; chunks = [];
        force = setTimeout(() => { try { processIO.kill(child, "SIGKILL"); } catch { /* Still await close. */ } }, 250);
        try { processIO.kill(child, "SIGTERM"); } catch { /* Unconfirmed until close. */ }
      };
      const timer = setTimeout(stop, 12_000);
      signal.addEventListener("abort", stop, { once: true });
      child.stdout?.on("data", (chunk: Buffer) => { size += chunk.length; if (size > continuityOutputLimit) stop(); else if (!rejected) chunks.push(Buffer.from(chunk)); });
      child.stderr?.on("data", (chunk: Buffer) => { stderrSize += chunk.length; if (stderrSize > 65536) stop(); });
      child.on("error", stop); // Node spawn errors are followed by close; error alone is not reaping evidence.
      child.once("close", (code, killedBy) => {
        clearTimeout(timer); if (force) clearTimeout(force); signal.removeEventListener("abort", stop);
        resolve(rejected || signal.aborted || killedBy ? unavailable() : normalizeContinuityHelper(Buffer.concat(chunks), code));
      });
      if (signal.aborted) stop();
    });
  } catch { return unavailable(); }
  finally { if (directory) { try { await rm(directory, { recursive: true, force: true }); } catch { /* Fixed unavailable output; no private error/log. */ } } }
}
