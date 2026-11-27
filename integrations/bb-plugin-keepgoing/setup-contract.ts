import { publicationSchema } from "./registry-contract.ts";
import { z } from "zod";
import { goalSummarySchema, goalStateSchema } from "./status-details.ts";

const text = z.string().min(1).max(4096);
export const targetSchema = z.object({
  threadId: text, environmentId: text, projectId: text, hostId: text, project: text, provider: text,
});
export type SetupTarget = z.infer<typeof targetSchema>;
// `startJson` and `startJsonBin` record that the preview this setup was created
// from, run by that binary, advertised `start --json`. Absent on older records.
// `followsStandard` records that the loop was set up to follow the person's
// standard route list. Absent on older records and on single-agent setups.
export const recordSchema = targetSchema.extend({ id: text, configPath: text, loop: text, publication: publicationSchema.optional(), registryId: z.string().regex(/^loop_[0-9a-f]{32}$/).optional(), registryStateDir: text.optional(), startJson: z.literal(true).optional(), startJsonBin: text.optional(), followsStandard: z.literal(true).optional() });
export type SetupRecord = z.infer<typeof recordSchema>;
/** The target a record was saved for: its target fields and no others, so a
 * field added to records later can never make the comparison fail. */
export function targetOf(r: SetupRecord): SetupTarget {
  // In the record's own key order: callers compare serialized targets.
  return Object.fromEntries(Object.entries(r).filter(([k]) => k in targetSchema.shape)) as SetupTarget;
}
/** Whether a loop's `status --json` says it follows the standard route list. */
export function statusFollowsStandard(status: unknown): boolean {
  const parsed = z.object({ routes: z.object({ source: z.literal("standard") }).passthrough() }).safeParse(status);
  return parsed.success;
}
export const continuationCandidateSchema = z.object({
  configPath: text,
  loop: z.string().regex(/^[a-z0-9][a-z0-9-]{0,63}$/),
  displayName: z.string().max(120).optional(),
  project: z.string().max(120).optional(),
  sameProject: z.boolean(),
  controlState: z.string().max(64),
  archived: z.boolean(),
});
export type ContinuationCandidate = z.infer<typeof continuationCandidateSchema>;

export const planSchema = z.object({
  withGoal: z.literal(true).optional(),
  loop: text, project: text, configPath: text, stateDir: text,
  threadId: text, provider: text,
  // Set when this setup continues an existing loop (--succeeds).
  succeeds: z.object({ configPath: text, loop: text, displayName: z.string().max(120).optional() }).optional(),
  // Advertised by a CLI whose `start` accepts --json and reports a refusal in it.
  startJson: z.literal(true).optional(),
  limits: z.object({ wakeEvery: text, stallAfter: text, interval: text, backoff: text, maxNudges: z.number().int().positive(), maxReprompts: z.number().int().positive(), maxSends: z.number().int().positive().optional(), noProgressTimeout: text.optional() }),
  // Set when the setup follows the standard route list: the routes that
  // apply to this loop now, by id, model and group only (never a path,
  // thread or login), and that handoff on a rate limit is turned on.
  routes: z.object({
    list: z.literal("standard"),
    apply: z.array(z.object({ id: text, model: z.string().max(256), group: z.string().max(256) })).min(1).max(64),
    handoffOn: z.literal("rate_limit"),
  }).optional(),
  // Set when the setup has a takeover: on a usage limit a new conversation
  // on this provider and model starts and owns the loop. `text` is the
  // CLI's own sentence for what happens.
  takeover: z.object({ provider: z.string().max(64), model: z.string().max(256), permission: z.string().max(32), text: z.string().max(4096) }).optional(),
  // A provider looptender found usable for a takeover, so the person can
  // turn it on or off. Provider and model labels only.
  takeoverOffer: z.object({ provider: z.string().max(64), model: z.string().max(256) }).optional(),
});
export type SetupPlan = z.infer<typeof planSchema>;
/**
 * The task "Keep this conversation going" sends: fixed text, never read from
 * the conversation. The agent already has that conversation when it is woken.
 * It asks for await-user when there is nothing to continue, because the
 * plugin cannot tell from metadata whether the conversation holds a task.
 */
export const KEEP_GOING_TASK = [
  "Continue the work already under way in this conversation. Follow the person's most recent instructions here, and keep to any limits or approvals they set in this conversation.",
  "Finish the work and verify it. When it is all done, mark the loop complete.",
  "Use await-user when what remains needs the person, or when this conversation contains no task to continue.",
].join("\n\n");
/** The one-line note a person may add to it, kept to a single short line. */
export const noteSchema = z.string().max(600).regex(/^[^\r\n\u0085\u2028\u2029]*$/, "one line").refine(n => n.trim().length <= 300, "at most 300 characters");
export function conversationTask(note: string | undefined): string {
  const n = (note ?? "").trim();
  return n ? `${KEEP_GOING_TASK}\n\nNote from the person: ${n}` : KEEP_GOING_TASK;
}
/** What BB's thread metadata says about whether there is anything to continue. */
export const conversationSchema = z.enum(["none", "unknown"]);
export const setupInput = z.object({
  // "conversation" sends the built-in task above; "own" (the default, and
  // what every earlier client sends) sends `task` as typed.
  mode: z.enum(["conversation", "own"]).optional(), note: noteSchema.optional(),
  action: z.enum(["preview", "create", "inspect", "start"]), threadId: text,
  withGoal: z.boolean().optional(),
  name: z.string().regex(/^[a-z0-9][a-z0-9-]{0,63}$/).optional(),
  task: z.string().max(262144).optional(), ticket: text.optional(),
  // Configuration path of an existing loop this setup continues (--succeeds).
  succeeds: text.optional(),
  // Follow the person's standard route list for handoffs. Off unless chosen.
  followStandard: z.boolean().optional(),
  // The takeover by a new conversation on another provider, when one is
  // usable: on unless turned off.
  takeover: z.enum(["on", "off"]).optional(),
});
/**
 * A refusal keepgoing reports in a structured form. The plugin shows a fixed
 * sentence for each known code, with the other loop's name and nothing else:
 * no path, no account, no text from the CLI. The name must be a loop name.
 * A refusal is never overridden from the plugin.
 */
export const refusalSchema = z.object({
  code: z.literal("second_loop_on_session"),
  otherLoop: z.string().regex(/^[a-z0-9][a-z0-9-]{0,63}$/),
  held: z.enum(["paused", "stopped", "completed", "awaiting a person"]).optional(),
  archived: z.boolean().optional(),
});
export type Refusal = z.infer<typeof refusalSchema>;
/** What `keepgoing init --json` and `start` print on stdout when they refuse. */
export const refusedReportSchema = z.object({ schemaVersion: z.literal(1), outcome: z.literal("refused"), refusal: refusalSchema });
export function refusalMessage(r: Refusal, command: "init" | "start"): string {
  const state = r.held ? `registered and enabled, held: ${r.held}` : "registered and enabled; current activity is unverified";
  return `looptender refused to ${command === "init" ? "set up" : "start"} this loop: loop ${r.otherLoop} is ${state}${r.archived ? "; archived" : ""} for this thread's session. ${command === "init" ? "Nothing was created." : "Nothing was started."} ${r.archived ? "A person must unarchive it before reuse; unarchive resumes nothing. " : ""}To reuse it, a person must review its task, routes, limits and holds, then explicitly resume from the looptender panel or a terminal outside BB; resume clears other holds too. For a fresh attachment, disable ${r.otherLoop} first, then preview and attach the new loop. Pause or archive alone does not release this registration guard.`;
}
/**
 * Why a looptender command the plugin ran for setup did not give a plan, said
 * as what happened: it took too long, it exited non-zero (with the first line
 * it printed), or it answered in a form the plugin does not recognise. Only
 * these fields cross back from the host, never the command's other output.
 */
export const failureSchema = z.object({
  kind: z.enum(["timeout", "exit", "output", "exists", "written"]),
  seconds: z.number().int().positive().max(3600).optional(),
  code: z.number().int().min(0).max(255).optional(),
  line: z.string().max(240).optional(),
});
export type SetupFailure = z.infer<typeof failureSchema>;
/**
 * What to tell the person for a failed command. `creating` is true when the
 * failed command was the real one: a timeout there may have written the
 * disabled setup before it was cut off. A preview, which creates nothing, never
 * says that a setup may exist unless the command itself said so.
 */
export function failureMessage(f: SetupFailure, creating: boolean, name?: string): string {
  const what = creating ? "Creating the setup" : "The preview";
  switch (f.kind) {
    case "timeout":
      return creating
        ? `The looptender command timed out after ${f.seconds ?? 20} s and was stopped. A switched-off setup${name ? ` named ${name}` : ""} may have been written before that: refresh status and review or remove it before trying again.`
        : `The preview timed out: the looptender command was still running after ${f.seconds ?? 20} s and was stopped. Nothing was created. The machine may be busy; try again, and if it keeps timing out check \`looptender doctor\`.`;
    case "exists":
      return `A setup${name ? ` named ${name}` : ""} already exists on this host${f.line ? ` (looptender said: ${f.line})` : ""}. Review or remove it, or choose another loop name. Nothing was created by this ${creating ? "attempt" : "preview"}.`;
    case "written":
      // init says it may have written files before it failed: only a creation can have.
      if (!creating) return failureMessage({ ...f, kind: "exit", code: f.code ?? 1 }, false, name);
      return `The looptender command failed after it may have written files${f.line ? ` (looptender said: ${f.line})` : ""}. A switched-off setup${name ? ` named ${name}` : ""} may have been written: refresh status and review or remove it before trying again. The plugin saved nothing.`;
    case "exit":
      return `${what} failed: the looptender command exited with status ${f.code ?? 1}${f.line ? ` and said: ${f.line}` : " and printed nothing to explain it"}. ${creating ? "The plugin saved nothing." : "Nothing was created."}`;
    default:
      return `${what} failed: the looptender command ran but its answer was not in a form this plugin understands${f.line ? ` (it said: ${f.line})` : ""}. The installed looptender and this plugin may be different builds. ${creating ? "The plugin saved nothing; check the destination before trying again." : "Nothing was created."}`;
  }
}
export const checkSchema = z.object({ id: text, status: z.enum(["ok", "warning", "error", "unavailable"]), message: text, fix: z.string().max(4096).optional() });
export const threadSetupMetaSchema = z.object({
  provider: z.string().min(1).max(64),
  providerLabel: z.string().min(1).max(120).optional(),
  model: z.string().min(1).max(256).optional(),
  modelLabel: z.string().min(1).max(120).optional(),
  reasoningLevel: z.string().min(1).max(64).optional(),
});
export type ThreadSetupMeta = z.infer<typeof threadSetupMetaSchema>;
export const setupResult = z.object({
  ok: z.boolean(), message: z.string(), ticket: z.string().optional(), plan: planSchema.optional(),
  record: recordSchema.optional(), goal: goalSummarySchema.nullable().optional(), goalState: goalStateSchema.optional(), checks: z.array(checkSchema).optional(), canStart: z.boolean().optional(),
  // True only when this call ran the start command and it reported success.
  started: z.boolean().optional(),
  // Set when keepgoing itself refused, with a structured reason.
  refused: refusalSchema.optional(),
  // True when the host's looptender reports a usable standard route list
  // with routes, so setup can offer to follow it. Absent otherwise.
  standardRoutes: z.literal(true).optional(),
  // The exact task a preview would send, so the person sees it. Only for the
  // built-in task, whose text the plugin wrote.
  task: z.string().max(262144).optional(),
  // On a thread with no setup: "none" when BB's metadata says the thread has
  // not had a turn yet, "unknown" when it cannot tell. Absent from older builds.
  conversation: conversationSchema.optional(),
  // Thread title from BB metadata (never transcript content), used for a
  // readable default loop slug.
  threadTitle: z.string().max(200).optional(),
  // Running provider and model metadata from BB (never transcript content),
  // so Add to Looptender reflects the thread's actual setup.
  threadMeta: threadSetupMetaSchema.optional(),
  // Existing loops on this host that this thread can continue (--succeeds),
  // ordered with same-project non-archived loops first.
  continuationCandidates: z.array(continuationCandidateSchema).max(128).optional(),
  // Any non-fatal warnings printed by `init` (for example if --succeeds
  // could not be recorded on the predecessor).
  warnings: z.array(z.string().max(400)).max(16).optional(),
});
export type SetupResult = z.infer<typeof setupResult>;

const LOOP_SLUG = /^[a-z0-9][a-z0-9-]{0,63}$/;
const QUALIFIER_WORD = /\b(claude|codex|gpt|gemini|opus|sonnet|haiku|cursor|antigravity|openai|anthropic|grok|llama|deepseek|qwen|mistral|o1|o3|o4|medium|low|high|max|fast|mini|pro|flash|lite|hydrogen)\b/i;

export function fallbackSlug(threadId: string): string {
  return `bb-${threadId.replace(/[^a-z0-9-]/g, "-").slice(0, 50)}`;
}

function toSlug(raw: string): string {
  return raw
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 52)
    .replace(/-+$/g, "");
}

/**
 * Derive a clean lowercase hyphenated loop slug from a BB thread title,
 * stripping trailing provider/model qualifiers and falling back to `bb-<id>`
 * when no usable title exists.
 */
export function slugFromTitle(title: string | null | undefined, threadId: string): string {
  if (!title) return fallbackSlug(threadId);
  let cleaned = title.trim();
  // Strip trailing parenthetical or separator provider/model qualifiers.
  for (;;) {
    const paren = cleaned.match(/\s*\(([^()]{1,60})\)\s*$/);
    if (paren && QUALIFIER_WORD.test(paren[1]!)) {
      cleaned = cleaned.slice(0, paren.index).trim();
      continue;
    }
    const sep = cleaned.match(/\s+(?:·|—|-)\s+([^·—-]{1,60})$/);
    if (sep && QUALIFIER_WORD.test(sep[1]!)) {
      cleaned = cleaned.slice(0, sep.index).trim();
      continue;
    }
    break;
  }
  if (/^(new thread|untitled)$/i.test(cleaned)) return fallbackSlug(threadId);
  const slug = toSlug(cleaned);
  return LOOP_SLUG.test(slug) ? slug : fallbackSlug(threadId);
}

/**
 * Derive a clean default slug for a loop that continues an existing loop,
 * avoiding collisions with existing loop names on the host.
 */
export function slugFromPredecessor(
  predecessor: { loop: string; displayName?: string },
  existingLoops: string[] = [],
): string {
  const taken = new Set(existingLoops);
  let base = predecessor.loop;
  if (/^bb-thr[-_]/.test(base) && predecessor.displayName) {
    const fromDisplay = slugFromTitle(predecessor.displayName, "");
    if (fromDisplay && fromDisplay !== "bb-" && !/^(?:bb-)?thr[-_]/.test(fromDisplay)) {
      base = fromDisplay;
    }
  }
  const numbered = base.match(/^(.*)-(\d+)$/);
  const stem = toSlug(numbered ? numbered[1]! : base).slice(0, 56).replace(/-+$/g, "") || "loop";
  let n = numbered ? Number(numbered[2]) + 1 : 2;
  for (let i = 0; i < 100; i++, n++) {
    const candidate = `${stem}-${n}`;
    if (LOOP_SLUG.test(candidate) && !taken.has(candidate)) return candidate;
  }
  return fallbackSlug(stem);
}
