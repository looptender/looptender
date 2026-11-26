import { usageEvents } from "./usage-events.ts";
// bb-plugin-keepgoing: shows keepgoing's state beside the threads it
// supervises, and offers pause, resume, and stop. It only calls the keepgoing
// CLI; every decision stays in keepgoing.
import { publicationSchema } from "./registry-contract.ts";
import { BUILD_LABEL } from "./build-label.ts";
import { cutAtWord, recordsUnreadable, isBuildLabel, pluginBuildLine, shortNote, notShownLine, lastLabel, limitWaitListingTag, checkSchema, movedSchema, runtimeSchema, daemonSchema, detailsSchema, goalDetails, limitWaitSchema, scheduleSchema } from "./status-details.ts";
import { defineRpcContract, type BbPluginApi } from "@get-bb/plugin-sdk";
import { z } from "zod";
import { hostContract } from "./host-contract.ts";
import { targetOf, statusFollowsStandard, recordSchema, setupInput, setupResult, targetSchema } from "./setup-contract.ts";
import { createTransferController } from "./transfer-controller.ts";
import { transferInput, transferResult, moveIntentSchema } from "./transfer-contract.ts";
import { walkLineage } from "./lineage.ts";
import { createSetupController, MAX_SETUPS } from "./setup-controller.ts";
import { createNewThreadController } from "./new-thread-controller.ts";
import { newThreadInput, newThreadResult, startModeSchema, type Bounds } from "./new-thread-contract.ts";
import { attachInput, attachResult } from "./attach-contract.ts";
import { createAttachController } from "./attach-controller.ts";
import { randomUUID } from "node:crypto";
import { withClientFlag } from "./client-flag.ts";
import { configuredProgram, execProgram } from "./executable.ts";

/** The subset of `keepgoing status --json` this plugin reads. */
const agentRef = z.object({
  adapter: z.string(),
  bbThread: z.string().optional(),
  provider: z.string().optional(),
  model: z.string().optional(),
  session: z.string().optional(),
  observed: z.boolean().optional(),
  wakes: z.boolean().optional(),
});
const statusSchema = z.object({
  goal: z.unknown().optional(), goalError: z.unknown().optional(),
  loop: z.string(),
  target: z.string(),
  controlState: z.string(),
  // Left out of status --json when control.json cannot be read.
  control: z.object({ note: z.string().optional(), completed: z.boolean().optional(), supersededBy: z.unknown().optional() }).passthrough().optional(),
  window: z.string(),
  // Left out of status --json when state.json cannot be read.
  state: z
    .object({
      lastCheck: checkSchema.optional(),
      failedChecks: z.number().int().nonnegative().optional(),
      lastState: z.string().optional(),
      lastWakeAt: z.string().optional(),
      checkedAt: z.string().optional(),
      handoffs: z.number().optional(),
    })
    .passthrough().optional(),
  redundancy: z.object({ healthy: z.array(z.string()), distinct: z.number(), min: z.number(), meets: z.boolean() }).optional(),
  runtime: runtimeSchema.optional(),
  moved: movedSchema.optional(),
  schedule: scheduleSchema.optional(),
  limitWait: limitWaitSchema.optional(),
  daemon: daemonSchema.optional(),
  targetConfigured: z.boolean().optional(),
  primaryAgent: agentRef,
  targetAgent: agentRef.optional(),
  // Fields status adds later. Each is checked on its own in `lenient`: one that is
  // malformed is named in details.unparsed, never taken for "nothing to say".
  wait: z.unknown().optional(), archived: z.unknown().optional(), archiveLapsed: z.unknown().optional(), naming: z.unknown().optional(), buildFreshness: z.unknown().optional(), noProgress: z.unknown().optional(),
  deadline: z.unknown().optional(), deadlineReached: z.unknown().optional(), sendAllowance: z.unknown().optional(), sendRecord: z.unknown().optional(), diskSpace: z.unknown().optional(), unreadable: z.unknown().optional(), sessionpipeWake: z.unknown().optional(),
});

const loopSchema = z.object({
  details: detailsSchema.optional(),
  loop: z.string(),
  // naming.displayName from status --json: shown where the card shows a name; the slug stays as secondary text.
  displayName: z.string().max(120).optional(),
  configPath: z.string(),
  imported: z.boolean().optional(),
  referenceSource: z.enum(["plugin_setting", "plugin_setup", "registry_attachment"]).optional(),
  publication: publicationSchema.optional(),
  managedId: z.string().optional(),
  managedThreadId: z.string().optional(),
  controlState: z.string(),
  note: z.string().nullable(),
  target: z.string(),
  supervising: z.boolean(), // this thread is the loop's current target
  lastState: z.string().nullable(),
  lastWakeAt: z.string().nullable(),
  checkedAt: z.string().nullable(),
  handoffs: z.number(),
  fallbacks: z.string().nullable(),
  // The looptender build this plugin was built from: the card's own code, apart from each supervisor's build.
  pluginBuild: z.string().max(64).optional(),
});
export type LoopView = z.infer<typeof loopSchema>;

const sendAllowanceShape = z.object({ max: z.number(), used: z.number().optional(), remaining: z.number().optional(), reached: z.boolean(), error: z.string().optional() });
const unreadableShape = z.object({ file: z.string(), problem: z.string(), offset: z.number().optional(), remedy: z.string() });
const noProgressShape = z.object({ availability: z.string(), text: z.string() });
const archivedShape = z.object({ at: z.string().optional(), hold: z.string().optional(), unreadable: z.boolean().optional() }).passthrough();
const freshnessShape = z.object({ state: z.string(), text: z.string(), detail: z.string().optional(), command: z.string().optional(), action: z.string().optional() });
const waitShape = z.object({ until: z.string(), untilTime: z.string().optional(), expiresAt: z.string().optional() });
const diskShape = z.object({ level: z.string(), message: z.string().optional() });
const sessionpipeWakeShape = z.object({
  since: z.string().optional(),
  forSeconds: z.number().int().nonnegative().optional(),
  text: z.string().max(400),
  held: z.boolean().optional(),
  outcome: z.string().max(32).optional(),
  code: z.string().max(32).optional(),
  until: z.string().optional(),
});

/**
 * The fields status adds later, read one by one. A malformed one is left out
 * and named in `unparsed`, so the card says it could not be read; it is never
 * dropped as though there were nothing to say, and never costs the loop.
 */
function lenient(s: { wait?: unknown; archived?: unknown; archiveLapsed?: unknown; buildFreshness?: unknown; noProgress?: unknown; control?: { supersededBy?: unknown }; deadline?: unknown; deadlineReached?: unknown; sendAllowance?: unknown; sendRecord?: unknown; diskSpace?: unknown; unreadable?: unknown; sessionpipeWake?: unknown }) {
  const unparsed: string[] = [];
  const pick = <T>(name: string, schema: z.ZodType<T>, raw: unknown): T | undefined => {
    if (raw === undefined) return undefined;
    const r = schema.safeParse(raw);
    if (r.success) return r.data;
    unparsed.push(name);
    return undefined;
  };
  // An archive record that is malformed, flagged unreadable or has no usable date fails safe: it is kept as
  // "unreadable", so the card offers no resume command and never prints an empty date.
  let archived: { at?: string; hold?: string; unreadable?: boolean } | undefined;
  if (s.archived !== undefined && s.archived !== null) {
    const a = archivedShape.safeParse(s.archived);
    const dated = a.success && !a.data.unreadable && !!a.data.at && !Number.isNaN(Date.parse(a.data.at)) && !a.data.at.startsWith("0001-");
    if (a.success && dated) archived = { at: a.data.at, ...(a.data.hold ? { hold: a.data.hold } : {}) };
    else { archived = { unreadable: true }; if (!a.success) unparsed.push("archived"); }
  }
  const archiveLapsed = pick("archiveLapsed", z.object({ at: z.string().optional() }).passthrough(), s.archiveLapsed);
  const wait = pick("wait", waitShape, s.wait);
  const freshness = pick("buildFreshness", freshnessShape, s.buildFreshness);
  const noProgress = pick("noProgress", noProgressShape, s.noProgress);
  const supersededBy = pick("supersededBy", z.string(), s.control?.supersededBy);
  const deadline = pick("deadline", z.string(), s.deadline);
  const deadlineReached = pick("deadlineReached", z.boolean(), s.deadlineReached);
  const sends = pick("sendAllowance", sendAllowanceShape, s.sendAllowance);
  const sendRecord = pick("sendRecord", z.string(), s.sendRecord);
  const disk = pick("diskSpace", diskShape, s.diskSpace);
  if (disk && (disk.level === "low" || disk.level === "critical") && !disk.message) unparsed.push("diskSpace");
  const sessionpipeWake = pick("sessionpipeWake", sessionpipeWakeShape, s.sessionpipeWake);
  // Entries are kept one by one: the well-formed ones are shown, and a bad one is named.
  let unreadable: z.infer<typeof unreadableShape>[] = [];
  if (s.unreadable !== undefined) {
    if (!Array.isArray(s.unreadable)) unparsed.push("unreadable");
    else {
      unreadable = s.unreadable.flatMap(e => { const r = unreadableShape.safeParse(e); return r.success ? [r.data] : []; });
      if (unreadable.length !== s.unreadable.length) unparsed.push("unreadable");
    }
  }
  return { wait, archived, archiveLapsed, freshness, noProgress, supersededBy, deadline, deadlineReached, sends, sendRecord, disk, sessionpipeWake, unreadable, unparsed };
}

/** The loop's display name from status --json naming.displayName, when it is a readable name that differs from the slug. */
function displayName(naming: unknown, slug: string): string | undefined {
  const n = typeof naming === "object" && naming !== null ? Reflect.get(naming, "displayName") : undefined;
  if (typeof n !== "string") return undefined;
  const clean = n.replace(/\s+/g, " ").trim().slice(0, 120);
  return clean && clean !== slug ? clean : undefined;
}

const actionSchema = z.enum(["pause", "resume", "stop"]);

// Whether status output, parsed or not, reports a record it could not read.
function hasUnreadable(output: unknown): boolean {
  let data = output;
  if (typeof output === "string") { try { data = JSON.parse(output); } catch { return false; } }
  const u = typeof data === "object" && data !== null ? Reflect.get(data, "unreadable") : undefined;
  return Array.isArray(u) && u.length > 0;
}

// What a control says when the thread it was asked from is neither the loop's primary nor its current target.
export const NOT_THIS_SETUP = "This setup does not belong to this thread (it is neither the thread it was saved for nor the destination of its move), so nothing was changed.";
export const NOT_THIS_THREAD = "This loop does not belong to this thread (it is neither the loop's primary nor its current target), so nothing was changed.";

// What the plugin says instead of resuming: where a person resumes the loop.
export const resumeByPerson = (configPath: string) =>
  "looptender does not resume a loop from this plugin, because it cannot tell a person's click from the supervised session. " +
  `From an ordinary terminal, not one BB opened on this loop's thread, run looptender resume -c ${JSON.stringify(configPath)}, or open the looptender panel there with looptender panel and resume it from the loop's page.`;

export const rpcContract = defineRpcContract({
  attachment: { input: attachInput, output: attachResult },
  transfer: { input: transferInput, output: transferResult },
  setup: { input: setupInput, output: setupResult },
  newThread: { input: newThreadInput, output: newThreadResult },
  forThread: {
    input: z.object({ threadId: z.string().min(1).max(200) }),
    output: z.object({ loops: z.array(loopSchema), errors: z.array(z.string()) }),
  },
  // Whether the thread is a child, and the nearest ancestor with a loop this plugin can see.
  lineage: {
    input: z.object({ threadId: z.string().min(1).max(200) }),
    output: z.object({ child: z.boolean(), coveredBy: z.object({ threadId: z.string(), loop: z.string() }).optional() }),
  },
  control: {
    input: z.object({ configPath: z.string().min(1).max(1000), action: actionSchema, managedId: z.string().optional(), expectedThreadId: z.string().min(1).max(200).optional() }),
    output: z.object({ ok: z.boolean(), message: z.string() }),
  },
  startChecks: {
    input: z.object({ configPath: z.string().min(1).max(1000), managedId: z.string().optional(), expectedThreadId: z.string().min(1).max(200).optional() }),
    output: z.object({ ok: z.boolean(), message: z.string() }),
  },
});

const CHANGED = "changed";

function run(bin: string, args: string[]): Promise<{ code: number; stdout: string; stderr: string; timedOut: boolean }> {
  return execProgram(bin, args, { timeout: 15_000, maxBuffer: 1 << 20 }).then(({ error, stdout, stderr }) => {
    const timedOut = !!error && error.killed === true;
    const code = error ? (typeof error.code === "number" ? error.code : 1) : 0;
    return { code, stdout, stderr, timedOut };
  });
}

export default async function plugin(bb: BbPluginApi) {
  const settings = bb.settings.define({
    looptenderPath: {
      type: "string",
      label: "looptender binary",
      description: "Absolute path to the looptender executable. Empty: looptender on the BB server's PATH. A bare name is looked up on that PATH.",
      default: "",
    },
    // The setting's earlier key. A value saved under it before is still used
    // while the one above is empty.
    keepgoingPath: {
      type: "string",
      label: "looptender binary (earlier setting)",
      description: "Used only while the setting above is empty. Leave it empty.",
      default: "",
    },
    configs: {
      type: "string",
      label: "Loop configs",
      description: "One existing server-local loop configuration file (looptender.yaml or keepgoing.yaml) per line. Host-bound UI-created setups and explicitly attached registry loops are registered separately.",
      experimental_multiline: true,
      default: "",
    },
    newThreadStart: {
      type: "select",
      label: "Add to Looptender from the New thread screen",
      description: "What happens after a thread is created with the New thread screen's Add to Looptender switch on. Start automatically: supervision starts once the thread exists; if anything about the start is uncertain, the setup is left switched off with the reason. Review first: a switched-off setup is created and you start it from the thread. Both create the loop with a loop-wide limit.",
      options: ["automatic", "manual"],
      experimental_optionLabels: { automatic: "Start automatically", manual: "Review first; I start it" },
      default: "automatic",
    },
  });

  const configured = async () => {
    const values = await settings.get();
    const { configs } = values;
    const paths = configs
      .split("\n")
      .map((line) => line.trim())
      .filter((line) => line !== "" && !line.startsWith("#"));
    return { bin: configuredProgram(values), paths };
  };

  // Serialize setup, moves and resume across clients. Restrictive pause/stop
  // remain independent so they can reach the CLI's short control lock.
  let mutation: Promise<unknown> = Promise.resolve();
  const serialize = <T>(fn: () => Promise<T>): Promise<T> => {
    const next = mutation.then(fn, fn); mutation = next.catch(() => {}); return next;
  };
  const intents = async () => z.array(moveIntentSchema).max(32).parse(await bb.storage.kv.get("moves.v1") ?? []);
  const host = bb.hosts.experimental_client({ contract: hostContract });
  const records = async () => z.array(recordSchema).max(32).parse(await bb.storage.kv.get("setups.v1") ?? []);
  const hostCommand = (bin: string, record: z.infer<typeof recordSchema>, operation: "status" | "doctor" | "start" | "startChecks" | "pause" | "resume" | "stop", json?: true) =>
    host.call("command", { bin, record, operation, ...(json ? { json } : {}) }, { hostId: record.hostId, signal: AbortSignal.timeout(30_000) });
  const resolveTarget = async (threadId: string) => {
    const signal = AbortSignal.timeout(10_000);
    const t = await bb.sdk.threads.get({ threadId, signal });
    if (t.id !== threadId || t.archivedAt !== null || t.deletedAt !== null || !t.environmentId) throw new Error("Target unavailable");
    const e = await bb.sdk.environments.get({ environmentId: t.environmentId, signal });
    if (e.id !== t.environmentId || e.projectId !== t.projectId || e.status !== "ready" || e.lifecycle.phase !== "active" || e.hostLifecycle !== "active" || !e.isGitRepo || !e.path) throw new Error("Environment unavailable");
    const h = await bb.sdk.hosts.get({ hostId: e.hostId, signal });
    if (h.id !== e.hostId || h.status !== "connected" || h.lifecycle.phase !== "active") throw new Error("Host unavailable");
    return targetSchema.parse({ threadId, environmentId: e.id, projectId: t.projectId, hostId: e.hostId, project: e.path, provider: t.providerId });
  };

  const status = async (bin: string, configPath: string) => {
    const result = await withClientFlag(bin, (extra) => run(bin, ["status", "--json", ...extra, "-c", configPath]), (r) => ({ code: r.code, emptyStdout: r.stdout === "", timedOut: r.timedOut }));
    // status exits 1 and still prints what it could read when a record cannot
    // be read; that is shown, not hidden behind an unavailable status.
    if (result.code !== 0 && !(result.code === 1 && hasUnreadable(result.stdout))) throw new Error(`${configPath}: looptender status exited ${result.code}`);
    let raw: unknown;
    try {
      raw = JSON.parse(result.stdout);
    } catch {
      throw new Error(`${configPath}: looptender status did not print JSON`);
    }
    const parsed = statusSchema.safeParse(raw);
    if (!parsed.success) {
      const old = typeof raw === "object" && raw !== null && !("primaryAgent" in raw);
      throw new Error(old ? `${configPath}: this looptender is too old for the plugin; it needs a build with primaryAgent in status --json (after 0.1.0)` : `${configPath}: unexpected looptender status JSON`);
    }
    return parsed.data;
  };

  const view = (s: z.infer<typeof statusSchema>, configPath: string, threadId: string | null): LoopView => {
    const control: NonNullable<typeof s.control> = s.control ?? {}, state = s.state ?? {}, target = s.targetAgent ?? s.primaryAgent;
    const f = lenient({ ...s, control });
    const sa = f.sends;
    return {
    details: {
      ...goalDetails(s.goal, s.goalError),
      readAt: new Date().toISOString(), completed: control.completed ?? false,
      targetThread: target.bbThread ?? null, targetAdapter: target.adapter,
      targetConfigured: s.targetConfigured ?? null, runtime: s.runtime ?? null,
      failedChecks: state.failedChecks ?? 0,
      daemon: s.daemon ?? null, lastCheck: state.lastCheck ?? null,
      schedule: s.schedule ?? null,
      ...(target.provider ? { targetProvider: target.provider.slice(0, 128) } : {}),
      ...(target.model ? { targetModel: target.model.slice(0, 128) } : {}),
      ...(s.moved ? { moved: s.moved } : {}),
      ...(f.supersededBy ? { supersededBy: f.supersededBy.slice(0, 128) } : {}),
      limitWait: s.limitWait ?? null,
      // What status prints only when there is something to say.
      ...(f.deadline ? { deadline: { at: f.deadline.slice(0, 64), ...(f.deadlineReached !== undefined ? { reached: f.deadlineReached } : {}) } } : {}),
      ...(sa ? { sends: { max: sa.max, ...(sa.used !== undefined ? { used: sa.used } : {}), ...(sa.remaining !== undefined ? { remaining: sa.remaining } : {}), reached: sa.reached, ...(sa.error ? { unavailable: true } : {}) } } : {}),
      ...(f.sendRecord ? { sendRecord: f.sendRecord.slice(0, 600) } : {}),
      ...((f.disk?.level === "low" || f.disk?.level === "critical") && f.disk.message ? { disk: { level: f.disk.level, message: f.disk.message.slice(0, 400) } } : {}),
      ...(f.unreadable.length ? { unreadable: f.unreadable.slice(0, 8).map(u => ({ file: u.file.slice(0, 64), problem: u.problem.slice(0, 40), ...(u.offset !== undefined ? { offset: u.offset } : {}), remedy: u.remedy.slice(0, 1200) })) } : {}),
      ...(f.unparsed.length ? { unparsed: f.unparsed.slice(0, 8) } : {}),
      ...(f.noProgress?.availability === "flagged" ? { noProgress: { text: f.noProgress.text.slice(0, 400) } } : {}),
      ...(f.wait ? { wait: { until: f.wait.until.slice(0, 60), ...(f.wait.untilTime ? { untilTime: f.wait.untilTime.slice(0, 40) } : {}), ...(f.wait.expiresAt ? { expiresAt: f.wait.expiresAt.slice(0, 40) } : {}) } } : {}),
      ...(f.archived ? { archived: { ...(f.archived.at ? { at: f.archived.at.slice(0, 40) } : {}), ...(f.archived.hold ? { hold: f.archived.hold.slice(0, 40) } : {}), ...(f.archived.unreadable ? { unreadable: true } : {}) } } : {}),
      ...(f.archiveLapsed ? { archiveLapsed: { ...(f.archiveLapsed.at ? { at: f.archiveLapsed.at.slice(0, 40) } : {}) } } : {}),
      ...(f.freshness ? { buildFreshness: { state: f.freshness.state.slice(0, 24), text: cutAtWord(f.freshness.text, 200), ...(f.freshness.detail ? { detail: cutAtWord(f.freshness.detail, 600) } : {}), ...(f.freshness.command ? { command: f.freshness.command.slice(0, 400) } : {}), ...(f.freshness.action ? { action: cutAtWord(f.freshness.action, 600) } : {}) } } : {}),
      ...(target.observed || target.adapter === "sessionpipe" ? { observed: true, ...(target.wakes ? { observedWakes: true } : {}) } : {}),
      ...(target.session ? { targetSession: target.session.slice(0, 160) } : {}),
      ...(f.sessionpipeWake ? { sessionpipeWake: { text: cutAtWord(f.sessionpipeWake.text, 400), ...(f.sessionpipeWake.held ? { held: true } : {}), ...(f.sessionpipeWake.outcome ? { outcome: f.sessionpipeWake.outcome.slice(0, 32) } : {}), ...(f.sessionpipeWake.code ? { code: f.sessionpipeWake.code.slice(0, 32) } : {}) } } : {}),
    },
    ...(BUILD_LABEL && isBuildLabel(BUILD_LABEL) ? { pluginBuild: BUILD_LABEL } : {}),
    loop: s.loop,
    ...(displayName(s.naming, s.loop) ? { displayName: displayName(s.naming, s.loop) } : {}),
    configPath,
    controlState: s.controlState,
    note: control.note ?? null,
    target: s.target,
    supervising: threadId !== null && target.bbThread === threadId,
    lastState: state.lastState ?? null,
    lastWakeAt: state.lastWakeAt ?? null,
    checkedAt: state.checkedAt ?? null,
    handoffs: state.handoffs ?? 0,
    fallbacks: s.redundancy ? `${s.redundancy.healthy.length} healthy, need ${s.redundancy.min}` : null,
    };
  };

  const verifyImported = async (bin: string, record: z.infer<typeof recordSchema>) => {
    if (!record.registryId) return;
    const target = await resolveTarget(record.threadId);
    if (JSON.stringify(target) !== JSON.stringify(targetOf(record))) throw new Error("Imported target changed");
    const result = await host.call("attachment", { bin, target, registryId: record.registryId }, { hostId: record.hostId, signal: AbortSignal.timeout(30_000) });
    if (result.snapshot?.entry.configPath !== record.configPath || result.snapshot?.entry.stateDir !== record.registryStateDir || result.snapshot?.entry.loop !== record.loop) throw new Error("Imported binding changed");
  };
  const allLoops = async (threadId: string | null) => {
    const { bin, paths } = await configured();
    const loops: LoopView[] = [];
    const errors: string[] = [];
    for (const configPath of paths) {
      try {
        const s = await status(bin, configPath);
        const matches = threadId === null || s.primaryAgent.bbThread === threadId || s.targetAgent?.bbThread === threadId;
        if (matches) loops.push({ ...view(s, configPath, threadId), referenceSource: "plugin_setting" });
      } catch {
        errors.push("A configured loop reference is unavailable; inspect the private Loop configs setting on its host.");
      }
    }
    const moving = await intents();
    for (const record of await records()) {
      const move = moving.find(i => i.record.id === record.id);
      if (threadId !== null && record.threadId !== threadId && move?.destination.threadId !== threadId) continue;
      try {
        await verifyImported(bin, record);
        const result = await hostCommand(bin, record, "status");
        if (result.code !== 0 && !(result.code === 1 && hasUnreadable(result.data))) throw new Error("status failed");
        const displayed = view(statusSchema.parse(result.data), record.configPath, threadId);
        // A partial move is not a normal Resume action, even if the host has
        // already committed but the plugin reference is not reconciled yet.
        if (move) { displayed.controlState = "move requires inspection"; displayed.note = `Move requested to ${move.destination.threadId}; check move status there before resuming.`; }
        loops.push({ ...displayed, referenceSource: record.registryId ? "registry_attachment" : "plugin_setup", ...(record.publication ? { publication: record.publication } : {}), ...(record.registryId ? { imported: true } : {}), managedId: record.id, managedThreadId: record.threadId });
      } catch { errors.push(`${record.loop}: host status unavailable`); }
    }
    return { loops, errors };
  };

  // Control only registered loops: the path comes from the browser,
  // so it must never choose an arbitrary config file.
  const control = async (configPath: string, action: z.infer<typeof actionSchema>, managedId?: string, expectedThreadId?: string) => {
    // A Resume click, an agent's `bb plugin rpc call` and `bb looptender
    // resume` all reach this plugin the same way, so it cannot tell a person
    // from the session the loop supervises. Supervision never clears its own
    // hold: the plugin never resumes and says where a person can.
    if (action === "resume") return { ok: false, message: resumeByPerson(configPath) };
    const { bin, paths } = await configured();
    if (managedId) {
      const record = (await records()).find(r => r.id === managedId && r.configPath === configPath);
      if (!record) return { ok: false, message: "That setup is not registered with this plugin." };
      // A guard against a card acting on the wrong loop, not access control: the caller names the thread itself.
      if (expectedThreadId !== undefined && record.threadId !== expectedThreadId && !(await intents()).some(i => i.record.id === record.id && i.destination.threadId === expectedThreadId)) return { ok: false, message: NOT_THIS_SETUP };
      try {
        await verifyImported(bin, record);
        const result = await hostCommand(bin, record, action);
        bb.realtime.publish(CHANGED, { at: Date.now() });
        return { ok: result.code === 0, message: result.code === 0 ? `${action} done` : `looptender ${action} failed; inspect host status` };
      } catch { return { ok: false, message: "Host control unavailable or target changed. No transfer was performed." }; }
    }
    if (!paths.includes(configPath)) return { ok: false, message: "That loop is not in the plugin's configured list." };
    if (expectedThreadId !== undefined) {
      // The loop's own status says which threads it belongs to: its primary and its current target.
      let s: z.infer<typeof statusSchema>;
      try { s = await status(bin, configPath); } catch { return { ok: false, message: "The loop's status could not be read, so it was not changed." }; }
      if (s.primaryAgent.bbThread !== expectedThreadId && s.targetAgent?.bbThread !== expectedThreadId) return { ok: false, message: NOT_THIS_THREAD };
    }
    const result = await run(bin, [action, "-c", configPath]);
    bb.realtime.publish(CHANGED, { at: Date.now() });
    return result.code === 0
      ? { ok: true, message: result.stdout.trim() || `${action} done` }
      : { ok: false, message: `looptender ${action} exited ${result.code}` };
  };

  // Start checks for an enabled, unheld, non-archived loop whose daemon is not
  // running ("checks have stopped"). Never clears a hold or resumes a held loop.
  const startChecks = async (configPath: string, managedId?: string, expectedThreadId?: string) => {
    const { bin, paths } = await configured();
    const eligible = (s: z.infer<typeof statusSchema>) => {
      if (expectedThreadId !== undefined && s.primaryAgent.bbThread !== expectedThreadId && s.targetAgent?.bbThread !== expectedThreadId) return NOT_THIS_THREAD;
      if (s.controlState !== "enabled" || s.archived !== undefined) return "This action is only available for an enabled, unheld, non-archived loop.";
      if (s.daemon?.state === "responsive") return "A daemon is already running for this loop.";
      return null;
    };
    if (managedId) {
      const record = (await records()).find(r => r.id === managedId && r.configPath === configPath);
      if (!record) return { ok: false, message: "That setup is not registered with this plugin." };
      if (expectedThreadId !== undefined && record.threadId !== expectedThreadId) return { ok: false, message: NOT_THIS_SETUP };
      if (record.registryId) return { ok: false, message: "Imported loops are started from the host CLI." };
      try {
        await verifyImported(bin, record);
        const st = await hostCommand(bin, record, "status");
        const parsed = st.code === 0 ? statusSchema.safeParse(st.data) : undefined;
        if (!parsed?.success) return { ok: false, message: "The loop's status could not be read, so nothing was started." };
        const err = eligible(parsed.data);
        if (err) return { ok: false, message: err };
        const res = await hostCommand(bin, record, "startChecks");
        bb.realtime.publish(CHANGED, { at: Date.now() });
        return res.code === 0
          ? { ok: true, message: res.method === "service" ? "Checks started via user service (survives reboot)." : "Checks started in background." }
          : { ok: false, message: "Could not start checks; inspect looptender service status on the host." };
      } catch {
        return { ok: false, message: "Host unavailable or target changed; nothing was started." };
      }
    }
    if (!paths.includes(configPath)) return { ok: false, message: "That loop is not in the plugin's configured list." };
    let s: z.infer<typeof statusSchema>;
    try { s = await status(bin, configPath); } catch { return { ok: false, message: "The loop's status could not be read, so nothing was started." }; }
    const err = eligible(s);
    if (err) return { ok: false, message: err };
    let svc = await run(bin, ["service", "install", "--json", "-c", configPath]);
    if (svc.code !== 0 && /already recorded|existing or unknown registration/i.test(svc.stderr)) {
      const rm = await run(bin, ["service", "remove", "--json", "-c", configPath]);
      if (rm.code === 0) {
        svc = await run(bin, ["service", "install", "--json", "-c", configPath]);
      }
    }
    if (svc.code === 0) {
      bb.realtime.publish(CHANGED, { at: Date.now() });
      return { ok: true, message: "Checks started via user service (survives reboot)." };
    }
    if (/user-service management currently supports macOS GUI LaunchAgents and Linux systemd user services|unknown subcommand|expected install, remove, or status/i.test(svc.stderr)) {
      const bg = await run(bin, ["start", "--background", "-c", configPath]);
      bb.realtime.publish(CHANGED, { at: Date.now() });
      return bg.code === 0
        ? { ok: true, message: "Checks started in background." }
        : { ok: false, message: `looptender start exited ${bg.code}` };
    }
    return { ok: false, message: "Could not install user service; inspect looptender service status on the host." };
  };

  const attachment = createAttachController({
    target: resolveTarget, binary: async () => (await configured()).bin, records,
    save: async value => { await bb.storage.kv.set("setups.v1", value); },
    list: async (bin, target) => (await host.call("attachment", { bin, target }, { hostId: target.hostId, signal: AbortSignal.timeout(30_000) })).candidates ?? [],
    inspect: async (bin, target, registryId) => {
      const r = await host.call("attachment", { bin, target, registryId }, { hostId: target.hostId, signal: AbortSignal.timeout(30_000) });
      if (!r.snapshot) throw new Error("Attachment snapshot unavailable");
      return r.snapshot;
    },
    guard: async (target, snapshot, record) => {
      if ((await configured()).paths.includes(snapshot.entry.configPath)) return "This configuration is already listed in Loop configs. Remove that manual reference before importing; attachment does not edit settings.";
      if ((await intents()).some(i => i.record.id === record?.id || i.record.configPath === snapshot.entry.configPath || i.destination.threadId === target.threadId || i.record.threadId === target.threadId)) return "A saved move requires recovery before attachment. Inspect the move on its host; nothing was attached.";
    },
    changed: () => bb.realtime.publish(CHANGED, { at: Date.now() }),
  });
  const setup = createSetupController({
    target: resolveTarget,
    binary: async () => (await configured()).bin,
    records,
    save: async value => { await bb.storage.kv.set("setups.v1", value); },
    guard: async threadId => {
      const result = await allLoops(threadId);
      if (result.loops.length || result.errors.length) throw new Error("Existing or unverified supervision");
    },
    init: (bin, target, name, task, dryRun, withGoal, bounds, followStandard, takeover, succeeds) => host.call("init", { bin, target, name, task, dryRun, withGoal, ...(bounds ? { bounds } : {}), ...(followStandard ? { followStandard: true as const } : {}), ...(takeover ? { takeover } : {}), ...(succeeds ? { succeeds } : {}) }, { hostId: target.hostId, signal: AbortSignal.timeout(30_000) }),
    standardRoutes: async (bin, target) => (await host.call("standardRoutes", { bin, target }, { hostId: target.hostId, signal: AbortSignal.timeout(30_000) })).available,
    command: hostCommand,
    // Only thread metadata, never content: a thread BB reports as pending has
    // not started its first turn. Anything else, the plugin cannot tell.
    conversation: async threadId => {
      const t = await bb.sdk.threads.get({ threadId, signal: AbortSignal.timeout(10_000) });
      return t.status === "pending" || t.runtime?.displayStatus === "pending" || t.runtime?.displayStatus === "provisioning" ? "none" : "unknown";
    },
    threadTitle: async threadId => {
      const t = await bb.sdk.threads.get({ threadId, signal: AbortSignal.timeout(10_000) });
      const raw = (t.title ?? t.titleFallback ?? "").trim();
      return raw ? raw.slice(0, 200) : undefined;
    },
    threadMeta: async threadId => {
      const signal = AbortSignal.timeout(10_000);
      const t = await bb.sdk.threads.get({ threadId, signal });
      const provider = t.providerId.slice(0, 64);
      let model: string | undefined;
      let reasoningLevel: string | undefined;
      try {
        const opts = await bb.sdk.threads.defaultExecutionOptions({ threadId, signal });
        if (opts?.model) model = opts.model.slice(0, 256);
        if (opts?.reasoningLevel) reasoningLevel = opts.reasoningLevel.slice(0, 64);
      } catch { /* optional metadata */ }
      if (!model) {
        try {
          const proj = await bb.sdk.projects.defaultExecutionOptions({ projectId: t.projectId, signal });
          if (proj && proj.providerId === provider && proj.model) {
            model = proj.model.slice(0, 256);
            if (!reasoningLevel && proj.reasoningLevel) reasoningLevel = proj.reasoningLevel.slice(0, 64);
          }
        } catch { /* optional metadata */ }
      }
      let providerLabel: string | undefined;
      try {
        const providers = await bb.sdk.providers.list({ signal });
        const match = providers.find(p => p.id === provider);
        if (match?.displayName) providerLabel = match.displayName.slice(0, 120);
      } catch { /* optional metadata */ }
      let modelLabel: string | undefined;
      if (model && t.environmentId) {
        try {
          const res = await bb.sdk.providers.models({ environmentId: t.environmentId, providerId: provider, signal });
          const match = res.models.find(m => m.id === model || m.model === model);
          if (match?.displayName) modelLabel = match.displayName.slice(0, 120);
        } catch { /* optional metadata */ }
      }
      return {
        provider,
        ...(providerLabel ? { providerLabel } : {}),
        ...(model ? { model } : {}),
        ...(modelLabel ? { modelLabel } : {}),
        ...(reasoningLevel ? { reasoningLevel } : {}),
      };
    },
    continuationCandidates: async (bin, target) => {
      const recs = await records();
      const sameHostRecs = recs.filter(r => r.hostId === target.hostId);
      const extraPaths = [...new Set([...sameHostRecs.map(r => r.configPath), ...(await configured()).paths])].slice(0, 128);
      const res = await host.call("continuationCandidates", { bin, target, extraPaths }, { hostId: target.hostId, signal: AbortSignal.timeout(30_000) });
      const sameProjectPaths = new Set(sameHostRecs.filter(r => r.projectId === target.projectId || r.project === target.project).map(r => r.configPath));
      const enriched = res.candidates.map(c => sameProjectPaths.has(c.configPath) ? { ...c, sameProject: true } : c);
      enriched.sort((a, b) => {
        const rank = (c: typeof a) => (c.archived ? 2 : 0) + (c.sameProject ? 0 : 1);
        const diff = rank(a) - rank(b);
        return diff !== 0 ? diff : (a.displayName ?? a.loop).localeCompare(b.displayName ?? b.loop);
      });
      return enriched;
    },
    changed: () => bb.realtime.publish(CHANGED, { at: Date.now() }),
  });
  const transfer = createTransferController({
    target: resolveTarget,
    binary: async () => (await configured()).bin,
    records, intents,
    save: async value => { await bb.storage.kv.set("setups.v1", value); },
    saveIntents: async value => { await bb.storage.kv.set("moves.v1", value); },
    guard: async (threadId, record) => {
      const { paths } = await configured();
      if (paths.includes(record.configPath)) throw new Error("Duplicate manual reference");
      if ((await records()).some(r => r.id !== record.id && r.threadId === threadId) || (await intents()).some(i => i.record.id !== record.id && i.destination.threadId === threadId)) throw new Error("Destination reserved");
      const result = await allLoops(threadId);
      if (result.errors.length || result.loops.some(l => l.managedId !== record.id)) throw new Error("Existing or unverified supervision");
    },
    command: hostCommand,
    transfer: (intent, operation, recoveryId) => host.call("transfer", { bin: intent.bin, record: intent.record, destination: intent.destination, operation, ...(recoveryId ? { recoveryId } : {}) }, { hostId: intent.record.hostId, signal: AbortSignal.timeout(30_000) }),
    changed: () => bb.realtime.publish(CHANGED, { at: Date.now() }),
  });
  // Browser setup requests never carry loop-wide limits; the New thread flow
  // supplies them from its own closed list.
  const setupCall = (input: z.infer<typeof setupInput>, bounds?: Bounds) => serialize(async () => {
    if (input.action === "start" && (await intents()).some(i => i.record.threadId === input.threadId || i.destination.threadId === input.threadId)) return { ok: false, canStart: false, message: "A saved move requires inspection before starting supervision." };
    return setup.call(input, bounds);
  });
  let hookAvailable = false;
  const newThread = createNewThreadController({
    now: () => Date.now(),
    id: () => randomUUID(),
    // An unreadable setting falls back to the mode that starts nothing.
    mode: async () => {
      try { return startModeSchema.catch("manual").parse((await settings.get()).newThreadStart); }
      catch { return "manual"; }
    },
    unavailable: async () => {
      if (!hookAvailable) return "This version of BB does not offer the hook Add to Looptender needs on the New thread screen to learn which thread a message created, so it is unavailable here. Add to Looptender still works on an existing thread.";
      const { bin } = await configured();
      const result = await run(bin, ["version"]);
      return result.code === 0 ? null : "The looptender program was not found or did not run. Install it, or set its path in this plugin's settings, then check again.";
    },
    ready: async threadId => {
      try {
        const signal = AbortSignal.timeout(10_000);
        const t = await bb.sdk.threads.get({ threadId, signal });
        if (t.archivedAt !== null || t.deletedAt !== null) return { reason: "This thread was archived or deleted before looptender could set it up. Nothing was created." };
        // A thread still pending has not started its first turn: BB has not
        // delivered the message, and may never.
        if (t.status === "pending" || !t.environmentId) return null;
        const e = await bb.sdk.environments.get({ environmentId: t.environmentId, signal });
        if (e.status !== "ready") return null;
        if (!e.isGitRepo || !e.path) return { reason: "This thread does not work in a Git repository, which looptender needs to tell whether work is progressing. Nothing was created." };
        const target = await resolveTarget(threadId);
        return { project: target.project };
      } catch { return null; }
    },
    capacity: async () => ({ used: (await records()).length, max: MAX_SETUPS }),
    supervision: async threadId => {
      const result = await allLoops(threadId);
      return result.errors.length ? "unverified" : result.loops.length ? "supervised" : "none";
    },
    setup: (input, bounds) => setupCall(input, bounds),
    changed: () => bb.realtime.publish(CHANGED, { at: Date.now() }),
  });
  // The dispatch hook is how BB reports which thread a marked message
  // created. It never waits for anything and always lets the send proceed.
  // The hook is an experimental BB API: if this BB does not have it, or
  // refuses the registration, only the New thread control is lost. Status,
  // Pause, Stop and setup on existing threads do not depend on it.
  try {
    bb.experimental_hooks.on("message.dispatch", ctx => {
      try {
        const s = ctx.experimental_submission;
        newThread.dispatched({
          data: s && s.pluginId === bb.pluginId ? s.data : null,
          threadId: ctx.thread.id, projectId: ctx.thread.projectId, threadCreatedAt: ctx.thread.createdAt,
          attempt: ctx.attempt, initiator: ctx.initiator, senderThreadId: ctx.senderThreadId, parentThreadId: ctx.thread.parentThreadId, sourceThreadId: ctx.thread.sourceThreadId,
          queued: ctx.queuedMessages.length, text: s && s.pluginId === bb.pluginId ? ctx.input.text : "",
          threadStatus: ctx.thread.status,
        });
      } catch { /* a send is never delayed or failed by this plugin */ }
      return { action: "proceed" };
    });
    hookAvailable = true;
  } catch { hookAvailable = false; }
  // Setup waits for the first turn: a running or finished turn is what shows
  // that BB delivered the message.
  bb.events.on("thread.active", ({ thread }) => { void newThread.advance(thread.id); });
  bb.events.on("thread.idle", ({ thread }) => { void newThread.advance(thread.id); });
  bb.events.on("thread.failed", ({ thread }) => { void newThread.advance(thread.id); });
  bb.events.on("thread.archived", ({ thread }) => { newThread.forget(thread.id); });
  bb.events.on("thread.deleted", ({ thread }) => { newThread.forget(thread.id); });
  bb.onDispose(() => { setup.dispose(); transfer.dispose(); attachment.dispose(); newThread.dispose(); });
  bb.rpc.register(rpcContract, {
    attachment: input => serialize(() => attachment.call(input)),
    transfer: input => serialize(() => transfer.call(input)),
    setup: input => setupCall(input),
    newThread: input => newThread.call(input),
    forThread: ({ threadId }) => allLoops(threadId),
    // Thread metadata only (each thread's parent id), never another thread's content.
    // A failed read throws, so the app retries instead of caching "not a child".
    lineage: ({ threadId }) => walkLineage(
      threadId,
      async id => (await bb.sdk.threads.get({ threadId: id, signal: AbortSignal.timeout(10_000) })).parentThreadId ?? null,
      async id => (await allLoops(id)).loops[0]?.loop,
    ),
    control: ({ configPath, action, managedId, expectedThreadId }) => action === "resume" ? serialize(() => control(configPath, action, managedId, expectedThreadId)) : control(configPath, action, managedId, expectedThreadId),
    startChecks: ({ configPath, managedId, expectedThreadId }) => serialize(() => startChecks(configPath, managedId, expectedThreadId)),
  });

  bb.cli.register({
    name: "looptender",
    summary: "Show and control the looptender loops this plugin is configured for",
    commands: [
      { name: "usage-events", summary: "Bounded exact-thread structured usage metadata for configured collectors", usage: "bb looptender usage-events <thread> <latest|after-seq>" },
      { name: "status", summary: "List configured loops with their control state and target", usage: "bb looptender status" },
      { name: "pause", summary: "Pause a loop by name", usage: "bb looptender pause <loop>" },
      { name: "resume", summary: "Refused: says where a person resumes the loop", usage: "bb looptender resume <loop>" },
      { name: "stop", summary: "Stop a loop's running session by name", usage: "bb looptender stop <loop>" },
    ],
    async run(argv) {
      const [command, name, ...rest] = argv;
      if (command === "usage-events") return usageEvents(bb.sdk, argv.slice(1));
      if (command === "status" && name === undefined) {
        const { loops, errors } = await allLoops(null);
        const lines = loops.map((l) => `${l.loop}\t${recordsUnreadable(l.details) ? "records unreadable" : l.controlState}\treference=${l.referenceSource ?? "unavailable"}\ttarget=${l.target}\tlast=${lastLabel(l.lastState, l.details)}${l.details?.schedule?.overdue ? "\tschedule=overdue" : ""}${l.details?.disk ? `\tdisk=${l.details.disk.level}` : ""}${limitWaitListingTag(l.details)}${l.note ? `\t(${shortNote(l.note)})` : ""}`);
        // Registered loops this plugin was not told about: said, with how to add one.
        let notShown = "";
        try {
          const { bin } = await configured();
          const listed = await run(bin, ["registry", "list", "--json"]);
          const parsed = z.object({ entries: z.array(z.object({ loop: z.string() })).max(1024) }).safeParse(JSON.parse(listed.stdout));
          if (listed.code === 0 && parsed.success) notShown = notShownLine(parsed.data.entries.map(e => e.loop), loops.map(l => l.loop));
        } catch { /* the registry could not be read: nothing is claimed */ }
        let running: string | null = null;
        try {
          const { bin } = await configured();
          const v = await run(bin, ["version"]);
          if (v.code === 0 && isBuildLabel(v.stdout.trim())) running = v.stdout.trim();
        } catch { /* not read */ }
        const built = pluginBuildLine(BUILD_LABEL, running);
        return { exitCode: errors.length > 0 ? 1 : 0, stdout: `${lines.join("\n")}\n${notShown ? `${notShown}\n` : ""}${built}\nReferences: explicit plugin settings and saved host references; CLI registry/menu coverage is separate. This is not a complete process inventory.\n`, stderr: errors.join("\n") };
      }
      const parsed = actionSchema.safeParse(command);
      if (!parsed.success || !name || rest.length > 0) return { exitCode: 2, stderr: "Usage: bb looptender status | pause <loop> | resume <loop> | stop <loop>" };
      const { loops } = await allLoops(null);
      const matches = loops.filter((l) => l.loop === name);
      if (matches.length > 1) return { exitCode: 1, stderr: "That loop name is ambiguous across hosts/configs; use the thread's controls or the CLI on its host." };
      const loop = matches[0];
      if (!loop) return { exitCode: 1, stderr: `No configured loop named ${name}` };
      const result = await (parsed.data === "resume" ? serialize(() => control(loop.configPath, parsed.data, loop.managedId, loop.managedThreadId)) : control(loop.configPath, parsed.data, loop.managedId, loop.managedThreadId));
      return { exitCode: result.ok ? 0 : 1, stdout: `${result.message}\n` };
    },
  });
}
