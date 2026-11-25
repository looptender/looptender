import { publishReference } from "./registry-host.ts";
import type { Publication } from "./registry-contract.ts";
import { basename, dirname, isAbsolute, join } from "node:path";
import { homedir } from "node:os";
import { realpath } from "node:fs/promises";
import { z } from "zod";
import { refusesFlag, withClientFlag } from "./client-flag.ts";
import { execProgram } from "./executable.ts";
import { goalSummarySchema } from "./status-details.ts";
import { planSchema, refusedReportSchema, continuationCandidateSchema, type ContinuationCandidate, type Refusal, type SetupFailure, type SetupTarget, type SetupRecord } from "./setup-contract.ts";
import { durationMs, type Bounds } from "./new-thread-contract.ts";

// code is the real exit code (1 when there is none); emptyStdout and
// timedOut are set by execute, so a refusal of --client can be told apart.
// stderr is at most 4 KiB of what the command printed to its error output; the
// plugin reads one line of it, for a failure, and never logs it.
export type Execute = (bin: string, args: string[], input: string, threadId: string, signal?: AbortSignal) => Promise<{ code: number; data: unknown; emptyStdout?: boolean; timedOut?: boolean; stderr?: string }>;
export const EXECUTE_TIMEOUT_MS = 20_000;
// Bounded output and duration; task text travels only through stdin. Command
// output is never logged. A failed setup command returns exactly one line of its
// error output (see firstErrorLine: not a warning, home directory masked, bounded)
// and its exit status; child-process errors are never included.
export const execute: Execute = (bin, args, input, threadId, signal) => {
  const env = { ...process.env };
  if (threadId) env.BB_THREAD_ID = threadId;
  else delete env.BB_THREAD_ID;
  return execProgram(bin, args, { timeout: EXECUTE_TIMEOUT_MS, maxBuffer: 1 << 20, signal, env, input }).then(({ error, stdout, stderr }) => {
    let data: unknown = null;
    try { data = JSON.parse(stdout); } catch { /* text commands need no output */ }
    const code = error ? (typeof error.code === "number" && error.code !== 0 ? error.code : 1) : 0;
    return { code, data, emptyStdout: stdout === "", timedOut: !!error && error.killed === true, stderr: stderr.slice(0, 4096) };
  });
};

function maskLine(raw: string): string {
  let line = raw.replace(/[\u0000-\u001f\u007f\u0085\u2028\u2029]/g, " ").trim();
  const home = homedir();
  if (home.length > 1) line = line.split(home).join("~");
  return line.length > 200 ? `${line.slice(0, 197)}...` : line;
}

/**
 * Extract non-fatal warnings printed by `init` on stderr (for example when
 * `--succeeds` could not record the succession), masked and bounded.
 */
export function warningLines(stderr: string | undefined): string[] {
  const out: string[] = [];
  for (const raw of (stderr ?? "").split("\n")) {
    const line = maskLine(raw);
    if (!line || !/^(?:init:\s*)?warning:/i.test(line)) continue;
    out.push(line);
    if (out.length >= 8) break;
  }
  return out;
}
/**
 * The first line the command printed that says what went wrong: not a warning
 * (init warns about other loops and routes before it fails), no control
 * characters, short. Empty when it printed none.
 */
export function firstErrorLine(stderr: string | undefined): string {
  for (const raw of (stderr ?? "").split("\n")) {
    let line = raw.replace(/[\u0000-\u001f\u007f\u0085\u2028\u2029]/g, " ").trim();
    if (!line || /^warning:/i.test(line)) continue;
    // The person's home directory is not shown: paths read from it as "~".
    const home = homedir();
    if (home.length > 1) line = line.split(home).join("~");
    return line.length > 200 ? `${line.slice(0, 197)}...` : line;
  }
  return "";
}
function failureOf(result: { code: number; timedOut?: boolean; stderr?: string }, parsedOk: boolean): SetupFailure {
  const line = firstErrorLine(result.stderr);
  if (result.timedOut) return { kind: "timeout", seconds: Math.round(EXECUTE_TIMEOUT_MS / 1000) };
  // A directory that is already there fails with "mkdir ...: file exists" before
  // anything is written. "setup files may already exist" is the opposite: init
  // could not report after writing, so this attempt may have written files.
  if (/may already exist/i.test(line)) return { kind: "written", ...(line ? { line } : {}) };
  if (/file exists|already exists/i.test(line)) return { kind: "exists", ...(line ? { line } : {}) };
  if (result.code !== 0) return { kind: "exit", code: Math.min(255, Math.max(0, result.code)), ...(line ? { line } : {}) };
  return { kind: "output", ...(parsedOk ? {} : line ? { line } : {}) };
}
const cliPlan = z.object({
  schemaVersion: z.literal(1), outcome: z.enum(["preview", "created"]),
  goal: z.unknown().optional(), taskPath: z.string().optional(),
  // Only the literal true is believed; anything else means today's start.
  startJson: z.unknown().optional(),
  loop: z.string(), project: z.string(), configPath: z.string(), stateDir: z.string(),
  primary: z.object({ agent: z.object({ adapter: z.literal("bb"), bbThread: z.string(), provider: z.string() }) }),
  fallbacks: z.array(z.unknown()).length(0),
  limits: planSchema.shape.limits.extend({ failoverOn: z.array(z.unknown()).length(0) }),
  newThreadOffer: z.object({ provider: z.string().min(1).max(64), model: z.string().min(1).max(256) }).optional(),
});
// A setup with a takeover: one new-thread fallback, handoff on a rate limit.
const takeoverPlan = cliPlan.extend({
  routes: z.literal("own"),
  fallbacks: z.tuple([z.object({ id: z.string().min(1).max(64), newThread: z.object({ provider: z.string().min(1).max(64), model: z.string().min(1).max(256), permission: z.string().max(32), text: z.string().min(1).max(4096) }) })]),
  limits: planSchema.shape.limits.extend({ failoverOn: z.tuple([z.literal("rate_limit")]) }),
});
/** The takeover asked of init: a provider and model, none to decline, or
 * detect to ask init which provider is usable (it then names one). */
export type TakeoverChoice = { provider: string; model: string } | "none" | "detect";
// A setup that follows the standard route list: the routes that apply now,
// and handoff on a rate limit. Only id, model and group are read from each.
const followingPlan = cliPlan.extend({
  routes: z.literal("standard"),
  fallbacks: z.array(z.object({ id: z.string().min(1).max(64), target: z.object({ agent: z.object({ model: z.string().max(256).optional(), group: z.string().max(256).optional() }) }) })).min(1).max(64),
  limits: planSchema.shape.limits.extend({ failoverOn: z.tuple([z.literal("rate_limit")]) }),
});
// What `routes list --json` says about the standard list: its state and,
// for each route, only what decides whether it applies to a new loop on
// this thread. `can` is required because v0.2.0 lists routes without it,
// and its init cannot follow the list. A build from between the commands
// that change the list and init --routes standard reports `can` too; its
// init refuses the flag, and the checked preview then fails closed.
const routeLists = z.object({
  schemaVersion: z.literal(1), kind: z.literal("route_lists"),
  standard: z.object({
    state: z.enum(["ok", "missing", "unreadable"]), can: z.array(z.string()),
    routes: z.array(z.object({ provider: z.string(), enter: z.string(), group: z.string(), disabled: z.boolean() }).passthrough()),
  }),
});
/**
 * Whether a standard route could apply to a loop set up here: this thread
 * on another model, which is a switched-on same-conversation route on the
 * thread's provider that draws on another usage limit than the loop's own
 * (setup labels it with the provider). The preview, which resolves the
 * loop for real, still decides; this only keeps the choice from being
 * offered when nothing could apply.
 */
function couldApply(r: { provider: string; enter: string; group: string; disabled: boolean }, target: SetupTarget) {
  return !r.disabled && r.enter === "same_conversation" && r.provider === target.provider && r.group !== target.provider;
}
/**
 * Whether the host's looptender has a usable standard route list with
 * routes. An older CLI has no `routes list`, and v0.2.0 cannot follow the
 * list: any refusal or other shape means no, and setup is as it was.
 */
export async function standardRoutesOnHost(input: { bin: string; target: SetupTarget }, signal?: AbortSignal, run: Execute = execute): Promise<{ available: boolean }> {
  try {
    const result = await run(input.bin, ["routes", "list", "--json"], "", input.target.threadId, signal);
    const parsed = routeLists.safeParse(result.data);
    return { available: result.code === 0 && parsed.success && parsed.data.standard.state === "ok" && parsed.data.standard.routes.some(r => couldApply(r, input.target)) };
  } catch {
    return { available: false };
  }
}
export async function setupOnHost(input: { bin: string; target: SetupTarget; name: string; task: string; dryRun: boolean; withGoal?: boolean; bounds?: Bounds; followStandard?: true; takeover?: TakeoverChoice; succeeds?: string }, signal?: AbortSignal, run: Execute = execute): Promise<{ plan: z.infer<typeof planSchema>; registration?: Publication; warnings?: string[] } | { refused: Refusal } | { noRoutes: true } | { failed: SetupFailure }> {
  const { bin, target, name, task, dryRun, withGoal = false, bounds, followStandard, takeover, succeeds } = input;
  const project = await realpath(target.project);
  const args = ["init", "--non-interactive", "--json", "--name", name, "--agent", "bb", "--thread", target.threadId, "--provider", target.provider, "--project", project, "--task-file", "-"];
  if (withGoal) args.push("--with-goal");
  if (succeeds) args.push("--succeeds", succeeds);
  // Loop-wide bounds are fixed values from a closed list, never free text.
  if (bounds) args.push("--max-sends", String(bounds.maxSends), "--no-progress-timeout", bounds.noProgressTimeout);
  if (followStandard) args.push("--routes", "standard");
  // Provider and model labels only, from init's own offer.
  if (takeover === "none") args.push("--new-thread-provider", "none");
  else if (takeover === "detect") args.push("--detect-takeover");
  else if (takeover) args.push("--new-thread-provider", takeover.provider, "--new-thread-model", takeover.model);
  if (dryRun) args.push("--dry-run");
  // When continuing a predecessor (--succeeds), run `init` without
  // BB_THREAD_ID: the BB plugin acts outside any thread, and `init --succeeds`
  // checks BB_THREAD_ID against the predecessor's owner.
  const initThread = succeeds ? "" : target.threadId;
  let result = await run(bin, args, task, initThread, signal);
  // A CLI too old to know --detect-takeover refuses it as a usage error with
  // nothing printed: asked again without it, it offers nothing, as before.
  if (takeover === "detect" && refusesFlag({ code: result.code, emptyStdout: result.emptyStdout ?? false, timedOut: result.timedOut ?? false })) {
    result = await run(bin, args.filter(a => a !== "--detect-takeover"), task, initThread, signal);
  }
  // keepgoing's own refusal, when it reports one in a structured form. Only
  // the code and the other loop's name cross back; never its stderr.
  const refused = result.code !== 0 ? refusedReportSchema.safeParse(result.data) : undefined;
  if (refused?.success) return { refused: refused.data.refusal };
  // Following the list, but none of its routes applies to this thread: said
  // plainly, not as a setup failure.
  if (followStandard && result.code === 0 && followingPlan.extend({ fallbacks: z.array(z.unknown()).length(0) }).safeParse(result.data).success) return { noRoutes: true };
  const following = followStandard ? followingPlan.safeParse(result.data) : undefined;
  const taking = typeof takeover === "object" ? takeoverPlan.safeParse(result.data) : undefined;
  if (taking && typeof takeover === "object" && result.code === 0) {
    const nt = taking.success ? taking.data.fallbacks[0].newThread : undefined;
    if (!nt || nt.provider !== takeover.provider || nt.model !== takeover.model) throw new Error("Setup did not report the takeover that was asked for. Nothing was created.");
  }
  const parsed = following ?? taking ?? cliPlan.safeParse(result.data);
  if (result.code !== 0 || !parsed.success) return { failed: failureOf(result, parsed.success) };
  const p = parsed.data;
  if (!isAbsolute(p.configPath) || !["looptender.yaml", "keepgoing.yaml"].includes(basename(p.configPath)) || p.stateDir !== join(dirname(p.configPath), "state")) throw new Error("Setup paths could not be verified.");
  if (p.outcome !== (dryRun ? "preview" : "created") || p.loop !== name || p.project !== project || p.primary.agent.bbThread !== target.threadId || p.primary.agent.provider !== target.provider) throw new Error("Setup returned a different target. No supervisor was started.");
  if (bounds && (p.limits.maxSends !== bounds.maxSends || durationMs(p.limits.noProgressTimeout ?? "") !== durationMs(bounds.noProgressTimeout))) throw new Error("Setup did not report the requested loop-wide limits. No supervisor was started.");
  if (withGoal) {
    const goal = z.object({ promptContext: z.literal(true), titleSource: z.literal("loop_name"), specificationSource: z.literal("task_snapshot"), record: goalSummarySchema.optional() }).safeParse(p.goal);
    if (!goal.success || p.taskPath !== join(dirname(p.configPath), "task.md") || (dryRun ? goal.data.record !== undefined : goal.data.record?.revision !== 1)) throw new Error("Goal setup could not be verified. A disabled setup may exist; inspect the destination before retrying.");
  } else if (p.goal !== undefined) throw new Error("Setup unexpectedly included a goal. No supervisor was started.");
  const { startJson, fallbacks, routes: _routes, newThreadOffer, ...plan } = p as typeof p & { routes?: string; newThreadOffer?: { provider: string; model: string } };
  const nt = taking?.success ? taking.data.fallbacks[0].newThread : undefined;
  const takeoverFields = { ...(nt ? { takeover: { provider: nt.provider, model: nt.model, permission: nt.permission, text: nt.text } } : {}), ...(newThreadOffer ? { takeoverOffer: newThreadOffer } : {}) };
  const routes = following?.success ? { list: "standard" as const, handoffOn: "rate_limit" as const, apply: following.data.fallbacks.map(f => ({ id: f.id, model: f.target.agent.model ?? "", group: f.target.agent.group ?? "" })) } : undefined;
  void fallbacks;
  const warnings = dryRun ? [] : warningLines(result.stderr);
  return { ...(dryRun ? {} : { registration: await publishReference(bin, p.configPath, target.threadId, target.provider, signal, run) }), ...(warnings.length ? { warnings } : {}), plan: planSchema.parse({ ...plan, ...(startJson === true ? { startJson: true } : {}), ...(withGoal ? { withGoal: true } : {}), ...(routes ? { routes } : {}), ...takeoverFields, threadId: target.threadId, provider: target.provider }) };
}

const registryListShape = z.object({
  entries: z.array(z.object({ loop: z.string(), configPath: z.string() })).max(128),
});
const candidateStatusShape = z.object({
  loop: z.string().regex(/^[a-z0-9][a-z0-9-]{0,63}$/),
  controlState: z.string(),
  control: z.object({ supersededBy: z.unknown().optional() }).passthrough().optional(),
  archived: z.object({ continuedIn: z.unknown().optional() }).passthrough().optional(),
  naming: z.object({ displayName: z.string().optional(), project: z.string().optional() }).passthrough().optional(),
});

/**
 * List registered and configured loops on this host that a new thread can
 * continue (`--succeeds`), prioritizing non-archived loops in the same project.
 */
export async function continuationCandidatesOnHost(
  input: { bin: string; target: SetupTarget; extraPaths?: string[] },
  signal?: AbortSignal,
  run: Execute = execute,
): Promise<{ candidates: ContinuationCandidate[] }> {
  const paths = new Set<string>();
  try {
    const reg = await run(input.bin, ["registry", "list", "--private", "--json"], "", input.target.threadId, signal);
    const parsed = reg.code === 0 ? registryListShape.safeParse(reg.data) : undefined;
    if (parsed?.success) for (const e of parsed.data.entries) paths.add(e.configPath);
  } catch { /* registry may not exist yet */ }
  for (const p of input.extraPaths ?? []) paths.add(p);
  const allPaths = [...paths].slice(0, 128);
  const projectBase = basename(await realpath(input.target.project).catch(() => input.target.project));
  const candidates: ContinuationCandidate[] = [];
  const queue = [...allPaths];
  const worker = async () => {
    while (queue.length > 0) {
      const configPath = queue.shift()!;
      try {
        const res = await run(input.bin, ["status", "--json", "-c", configPath], "", input.target.threadId, signal);
        if (res.code !== 0) continue;
        const parsed = candidateStatusShape.safeParse(res.data);
        if (!parsed.success) continue;
        const s = parsed.data;
        if (s.controlState === "superseded" || s.archived?.continuedIn !== undefined || s.control?.supersededBy !== undefined) continue;
        const rawDisplay = s.naming?.displayName?.replace(/\s+/g, " ").trim().slice(0, 120);
        const displayName = rawDisplay && rawDisplay !== s.loop ? rawDisplay : undefined;
        const project = s.naming?.project?.replace(/\s+/g, " ").trim().slice(0, 120);
        const sameProject = !!projectBase && project === projectBase;
        const archived = s.archived !== undefined && s.archived !== null;
        candidates.push(continuationCandidateSchema.parse({
          configPath,
          loop: s.loop,
          ...(displayName ? { displayName } : {}),
          ...(project ? { project } : {}),
          sameProject,
          controlState: s.controlState.slice(0, 64),
          archived,
        }));
      } catch { /* skip unreadable loop */ }
    }
  };
  await Promise.all(Array.from({ length: Math.min(6, allPaths.length) }, worker));
  candidates.sort((a, b) => {
    const rank = (c: ContinuationCandidate) => (c.archived ? 2 : 0) + (c.sameProject ? 0 : 1);
    const diff = rank(a) - rank(b);
    if (diff !== 0) return diff;
    return (a.displayName ?? a.loop).localeCompare(b.displayName ?? b.loop);
  });
  return { candidates };
}
export async function commandOnHost(input: { bin: string; record: SetupRecord; operation: "status" | "doctor" | "start" | "pause" | "resume" | "stop"; json?: true }, signal?: AbortSignal, run: Execute = execute) {
  const { bin, record, operation } = input;
  const args: string[] = [operation];
  if (operation === "status" || operation === "doctor") args.push("--json");
  if (operation === "start") args.push("--background");
  // Passed only when the CLI advertised it in the setup preview; never guessed.
  if (operation === "start" && input.json === true) args.push("--json");
  if (operation === "status") {
    return withClientFlag(bin, (extra) => run(bin, [...args, ...extra, "-c", record.configPath], "", record.threadId, signal), (r) => ({ code: r.code, emptyStdout: r.emptyStdout ?? false, timedOut: r.timedOut ?? false }));
  }
  args.push("-c", record.configPath);
  const result = await run(bin, args, "", record.threadId, signal);
  // Only a confirmed start gets publication; failures and reads do not.
  const started = result.code === 0 && (input.json !== true || z.object({ schemaVersion: z.literal(1), outcome: z.literal("started") }).safeParse(result.data).success);
  return operation === "start" && started ? { ...result, registration: await publishReference(bin, record.configPath, record.threadId, record.provider, signal, run) } : result;
}

const serviceUnsupportedPattern = /user-service management currently supports macOS GUI LaunchAgents and Linux systemd user services|unknown subcommand|expected install, remove, or status/i;
const serviceAlreadyRecordedPattern = /already recorded|existing or unknown registration/i;

/**
 * Start supervision using a reboot-persistent user service (`service install` +
 * optional `enable`) on macOS and Linux, falling back cleanly to
 * `start --background` on platforms where user services are unsupported.
 */
export async function startServiceOnHost(
  input: { bin: string; record: SetupRecord; enable: boolean; json?: true },
  signal?: AbortSignal,
  run: Execute = execute,
): Promise<{ code: number; data: unknown; registration?: Publication; method?: "service" | "background" }> {
  const { bin, record, enable, json } = input;
  let svc = await run(bin, ["service", "install", "--json", "-c", record.configPath], "", record.threadId, signal);
  if (svc.code !== 0) {
    if (serviceUnsupportedPattern.test(svc.stderr ?? "")) {
      const bg = await commandOnHost({ bin, record, operation: "start", ...(json ? { json } : {}) }, signal, run);
      return { ...bg, method: "background" };
    }
    if (serviceAlreadyRecordedPattern.test(svc.stderr ?? "")) {
      const st = await run(bin, ["status", "--json", "-c", record.configPath], "", record.threadId, signal);
      const daemonState = typeof st.data === "object" && st.data !== null ? (Reflect.get(st.data, "daemon") as { state?: unknown } | undefined)?.state : undefined;
      if (st.code !== 0 || daemonState === "responsive") {
        return { code: svc.code, data: svc.data };
      }
      const rm = await run(bin, ["service", "remove", "--json", "-c", record.configPath], "", record.threadId, signal);
      if (rm.code !== 0) {
        return { code: rm.code, data: rm.data };
      }
      svc = await run(bin, ["service", "install", "--json", "-c", record.configPath], "", record.threadId, signal);
      if (svc.code !== 0) {
        return { code: svc.code, data: svc.data };
      }
    } else {
      return { code: svc.code, data: svc.data };
    }
  }
  if (enable) {
    const en = await run(bin, ["enable", "-c", record.configPath], "", record.threadId, signal);
    if (en.code !== 0) {
      // Roll back the newly installed service so no orphaned LaunchAgent/systemd unit remains.
      await run(bin, ["service", "remove", "--json", "-c", record.configPath], "", record.threadId, signal).catch(() => {});
      return { code: en.code, data: en.data };
    }
  }
  const registration = await publishReference(bin, record.configPath, record.threadId, record.provider, signal, run);
  return { code: 0, data: { schemaVersion: 1, outcome: "started" }, registration, method: "service" };
}
