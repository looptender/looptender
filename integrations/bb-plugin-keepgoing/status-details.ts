import { z } from "zod";

// Only diagnostic metadata crosses this boundary. No task text, paths,
// provider output, goal specifications or pending-transfer records are exposed.
export const checkSchema = z.object({
  at: z.string(), target: z.string(), action: z.string(), outcome: z.string(), dryRun: z.boolean().optional(),
});
export const runtimeSchema = z.object({
  state: z.string(), source: z.string(), freshness: z.string(),
  // Added by a later looptender; absent from older ones, and absent when a
  // label does not identify a build. Display-short labels, nothing more.
  recordedBuild: z.string().max(64).optional(), installedBuild: z.string().max(64).optional(), binaryComparison: z.string().optional(),
});
export const scheduleSchema = z.object({
  overdue: z.boolean(), message: z.string().optional(),
  // Present from the CLI that judges held loops and a fallback: "usual"
  // only when the loop's usual interval is known.
  judging: z.boolean().optional(), basis: z.string().optional(), slept: z.boolean().optional(),
  // From a later looptender: checkedBy "ticks" when no daemon answers yet
  // checks keep arriving, with how far apart recent ones were.
  checkedBy: z.string().max(32).optional(), cadence: z.string().max(32).optional(),
});
export const daemonSchema = z.object({
  state: z.string(), phase: z.string().optional(), observedAt: z.string().optional(),
  nextCheckAt: z.string().optional(), dryRun: z.boolean().optional(), binaryComparison: z.string().optional(),
});
// Set by a looptender that says a loop was moved to a new thread and has not
// been checked there; `held` while the awaiting-user hold the move left is in
// place (resuming is then the one action needed). No thread ID or title.
export const movedSchema = z.object({ held: z.boolean(), text: z.string().max(300) });
// A long limit wait with no handoff possible, from status --json limitWait.
// Only its state, reason and reset time cross this boundary.
export const limitWaitSchema = z.object({
  state: z.string(), resetsAt: z.string().optional(), resetPassed: z.boolean().optional(),
  reason: z.string().optional(), reportConfigured: z.boolean().optional(), resetIn: z.string().optional(),
  // Set when the loop is held: its recorded wait is from before the hold.
  held: z.string().optional(),
});
export const goalSummarySchema = z.object({
  id: z.string().regex(/^goal_[0-9a-f]{32}$/), revision: z.number().int().positive().max(Number.MAX_SAFE_INTEGER), state: z.literal("recorded"),
});
export const goalStateSchema = z.enum(["recorded", "unavailable", "not_reported"]);
export function goalDetails(goal: unknown, error: unknown) {
  const parsed = goalSummarySchema.safeParse(goal);
  if (error !== undefined || (goal !== undefined && !parsed.success)) return { goal: null, goalState: "unavailable" as const };
  if (parsed.success) return { goal: parsed.data, goalState: "recorded" as const };
  return { goal: null, goalState: "not_reported" as const };
}
export function goalLabel(goal: z.infer<typeof goalSummarySchema> | null | undefined, state: string | undefined): string {
  if (state === "recorded" && goal) return `Goal: ${goal.id} · revision ${goal.revision} (recorded; not verified completion)`;
  if (state === undefined) return "Goal: unavailable from this response";
  return state === "unavailable" ? "Goal: unavailable; inspect the goal record privately" : "Goal: not reported; no goal record or an older CLI";
}
export const detailsSchema = z.object({
  goal: goalSummarySchema.nullable().optional(), goalState: goalStateSchema.optional(),
  failedChecks: z.number().int().nonnegative().optional(),
  readAt: z.string(), completed: z.boolean(),
  targetThread: z.string().nullable(), targetAdapter: z.string(), targetConfigured: z.boolean().nullable(),
  runtime: runtimeSchema.nullable(), daemon: daemonSchema.nullable(), lastCheck: checkSchema.nullable(),
  schedule: scheduleSchema.nullable().optional(),
  // Added by a later looptender; absent from older ones. Configured labels of
  // the current target, not verified identity.
  targetProvider: z.string().max(128).optional(), targetModel: z.string().max(128).optional(),
  moved: movedSchema.optional(),
  limitWait: limitWaitSchema.nullable().optional(),
  // status --json control.supersededBy: the thread a person declared to
  // continue this loop's work in another loop. Present only on a superseded loop.
  supersededBy: z.string().max(128).optional(),
  // status --json deadline/deadlineReached, sendAllowance, sendRecord, diskSpace
  // and unreadable. Each is present only when there is something to say.
  deadline: z.object({ at: z.string().max(64), reached: z.boolean().optional() }).optional(),
  // Names of the fields above that status sent in a form this plugin could not read.
  unparsed: z.array(z.string().max(32)).max(8).optional(),
  // The no-progress flag, only while status says it is raised ("flagged"): status's own sentence.
  noProgress: z.object({ text: z.string().max(400) }).optional(),
  // status --json followsInstalls (yes, no, unknown, ticks) and buildAdoption: the daemon's last switch to an installed build.
  followsInstalls: z.string().max(16).optional(),
  buildAdoption: z.object({ outcome: z.string().max(24), at: z.string().max(40).optional(), from: z.string().max(80).optional(), to: z.string().max(80).optional() }).optional(),
  sends: z.object({ max: z.number().int().nonnegative(), used: z.number().int().nonnegative().optional(), remaining: z.number().int().nonnegative().optional(), reached: z.boolean(), unavailable: z.boolean().optional() }).optional(),
  sendRecord: z.string().max(600).optional(),
  disk: z.object({ level: z.enum(["low", "critical"]), message: z.string().max(400) }).optional(),
  unreadable: z.array(z.object({ file: z.string().max(64), problem: z.string().max(40), offset: z.number().int().nonnegative().optional(), remedy: z.string().max(1200) })).max(8).optional(),
});
export type StatusDetails = z.infer<typeof detailsSchema>;

type Runtime = StatusDetails["runtime"];

// The build the loop's supervisor last recorded running, and the installed
// looptender's. Never a guess: a missing label is "version unknown", and no
// claim is made about which build is newer.
export function buildSummary(runtime: Runtime | undefined): { running?: string; installed?: string; differs: boolean; text: string } {
  const running = runtime?.recordedBuild || undefined, installed = runtime?.installedBuild || undefined;
  const differs = !!running && !!installed && runtime?.binaryComparison === "different";
  const text = !running ? "version unknown" : differs ? `last check ran ${running} · installed ${installed}` : `last check ran ${running}`;
  return { running, installed, differs, text };
}

/**
 * The line `bb keepgoing status` adds when the CLI's registry lists loops this
 * plugin does not show: how many, which, and how to show them. Empty when
 * every registered loop is shown. Names only; loops are matched by name.
 */
/**
 * The line `bb keepgoing status` ends with about the plugin's own build, so a
 * plugin BB kept serving after an install is obvious. built is the looptender
 * build the plugin was stamped with (null for a source build) and running is
 * what the looptender it runs reports (null when it could not be read).
 * Labels are compared whole; nothing says which is newer.
 */
/** The shape of a build label, as the Go side checks it before showing one. */
export const isBuildLabel = (s: string): boolean => /^[0-9A-Za-z][0-9A-Za-z.+_-]{0,63}$/.test(s);

export function pluginBuildLine(built: string | null, running: string | null): string {
  if (!built) return "Plugin: not stamped with a looptender build (a source build), so it cannot say whether BB is serving a copy from before an install.";
  if (!running) return `Plugin: built from looptender ${built}; the looptender it runs did not report its version.`;
  if (built === running) return `Plugin: built from looptender ${built}, the build it runs.`;
  return `Plugin: built from looptender ${built}; the looptender it runs is ${running}. These are different builds. If one of them was installed recently, BB may still be serving the plugin from before it; see the plugin README, "After installing a new build".`;
}

/** A control note cut to one short line for a listing; the full note stays in the loop's own detail. */
export function shortNote(note: string, max = 120): string {
  const one = note.replace(/\s+/g, " ").trim();
  return one.length > max ? `${one.slice(0, max - 1)}…` : one;
}

export function notShownLine(registered: readonly string[], shown: readonly string[]): string {
  const have = new Set(shown);
  const missing = [...new Set(registered)].filter(name => !have.has(name));
  if (missing.length === 0) return "";
  const names = missing.length > 8 ? `${missing.slice(0, 8).join(", ")}, and ${missing.length - 8} more` : missing.join(", ");
  return `${missing.length} registered loop${missing.length === 1 ? " is" : "s are"} not shown here (${names}). To show ${missing.length === 1 ? "it" : "one"}, add its configuration path to this plugin's configs setting, one path per line; a loop created from the CLI is not added there for you, and its thread's card needs the same entry. looptender registry list names every registered loop.`;
}

/** What `bb keepgoing status` prints after `last=`: a moved loop has history
 * but nothing checked on its new target, which is not "never". */
export function lastLabel(lastState: string | null | undefined, details: { moved?: unknown } | null | undefined): string {
  return details?.moved ? "none yet (new target, not yet checked)" : lastState || "never";
}

export function dateLabel(value: string | null | undefined): string {
  if (!value || value.startsWith("0001-")) return "unavailable";
  const time = new Date(value);
  return Number.isNaN(time.getTime()) ? "unavailable" : time.toLocaleString();
}

// The reason `looptender status` gives for a record it could not read.
const UNREADABLE_WHAT: Record<string, string> = {
  frozen_control: "it contains a restrictive freeze with preserved evidence",
  unsupported_control: "it contains an unsupported or unverifiable control envelope",
  not_json: "its content is not JSON",
  wrong_shape: "its content is JSON of the wrong shape",
  not_a_file: "it is not a regular file",
  cannot_open: "it could not be opened",
};
type Unreadable = NonNullable<StatusDetails["unreadable"]>[number];
/** `<file> could not be read (<why>)`, as status prints it. */
export const unreadableHead = (u: Unreadable): string => `${u.file} could not be read (${UNREADABLE_WHAT[u.problem] ?? "reason not recorded"}${u.offset !== undefined ? `, at byte ${u.offset}` : ""})`;
const firstSentence = (text: string): string => { const m = /^.*?[.!?](?=\s|$)/s.exec(text.trim()); const one = m ? m[0] : text.trim(); return one.length > 240 ? `${one.slice(0, 239).trimEnd()}…` : one; };

/** status: "the reset falls after this loop's deadline: the loop will be held by the deadline on its first check after it, before this wait can end". */
const AFTER_DEADLINE = "the reset falls after this loop's deadline: the loop will be held by the deadline on its first check after it, before this wait can end";
const resetAfterDeadline = (d: StatusDetails | undefined): boolean => {
  const reset = Date.parse(d?.limitWait?.resetsAt ?? ""), deadline = Date.parse(d?.deadline?.at ?? "");
  return !!d?.deadline && d.deadline.reached === false && Number.isFinite(reset) && Number.isFinite(deadline) && reset > deadline;
};

/** The send allowance as status prints it: `used/max attempts reserved; N remaining`. */
const sendsText = (s: NonNullable<StatusDetails["sends"]>) => s.unavailable || s.used === undefined || s.remaining === undefined ? `${s.max} allowed; the count is unavailable` : `${s.used}/${s.max} attempts reserved; ${s.remaining} remaining`;

// A held loop whose own note says it is retired or not to be resumed. Deliberately narrow: only an
// awaiting-user hold, and only those words in the note; every other held loop keeps its command.
export const noteSaysRetired = (loop: { controlState: string; note: string | null }): boolean =>
  loop.controlState === "awaiting user" && !!loop.note && /\b(retired|do not resume|don't resume|not to be resumed)\b/i.test(loop.note);

/** What status calls a hold, in the sentence it puts after a no-progress flag recorded before the hold. */
const HELD_WORD: Record<string, string> = { "stop requested": "stopped", completed: "completed", "awaiting user": "awaiting a person", paused: "paused", disabled: "disabled" };
/** status's own sentence for a raised no-progress flag, qualified as status qualifies it for a held loop. */
export function noProgressLine(controlState: string, d: StatusDetails | undefined): string | undefined {
  if (!d?.noProgress) return undefined;
  const held = HELD_WORD[controlState];
  return held ? `${d.noProgress.text} (recorded before the hold; the loop is held (${held}), so this is not current)` : d.noProgress.text;
}

// ---- Build freshness: how a loop's supervisor build compares with the installed one ----
// The words are the ones the dock and the menu use (cmd/looptender/build_freshness.go, #468).
const FRESH_RECENT_MS = 24 * 3600_000;
export const olderCommand = (configPath: string) => `keepgoing daemon-shutdown -c ${JSON.stringify(configPath)}`;
export type Freshness = { state: "current" | "updated" | "pending" | "older"; text?: string; detail?: string; command?: string };
/** "1 min ago", "23 h ago": the unit a person would say. */
export function agoLabel(now: Date, at: Date): string {
  const s = Math.max(0, Math.floor((now.getTime() - at.getTime()) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  return `${Math.floor(s / 3600)} h ago`;
}
export function buildFreshness(d: StatusDetails | undefined, configPath: string, now = new Date()): Freshness | undefined {
  const running = d?.runtime?.recordedBuild, installed = d?.runtime?.installedBuild, comparison = d?.daemon?.binaryComparison;
  // Nothing is said when no daemon answered or a build is not reported; a missing field never makes a state.
  if (d?.daemon?.state !== "responsive" || !running || !installed || (comparison !== "match" && comparison !== "different")) return undefined;
  if (comparison === "different") {
    if (d.followsInstalls === "yes") return { state: "pending", text: "Update pending · switches at next check", detail: `Runs ${running}; installed ${installed}.` };
    const command = olderCommand(configPath);
    return { state: "older", text: "Older build · restart the supervisor to update", command,
      detail: `Runs ${running}; installed ${installed}. ${d.followsInstalls === "no" ? "It does not follow installs." : "It does not report following installs."} A person runs ${command}, then starts supervision again; holds and controls are kept.` };
  }
  const b = d.buildAdoption, at = b?.at ? new Date(b.at) : undefined;
  if (b?.outcome === "adopted" && at && !Number.isNaN(at.getTime())) {
    const age = now.getTime() - at.getTime();
    if (age >= 0 && age < FRESH_RECENT_MS) {
      const when = at.toLocaleString(undefined, { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
      return { state: "updated", text: `Updated ${agoLabel(now, at)}`, detail: `Installed ${when} · was ${b.from ?? "unknown build"} · now ${b.to ?? installed}` };
    }
  }
  return { state: "current" };
}

/** status reported unreadable records, whether or not their details could be read. */
export const recordsUnreadable = (d: StatusDetails | undefined): boolean => !!d?.unreadable?.length || !!d?.unparsed?.includes("unreadable");
const unparsedLine = (names: readonly string[]) => `Status could not be read for: ${names.join(", ")}`;

/**
 * Short, visible reasons a loop is not simply "enabled", most important first,
 * in the words `looptender status` uses. The card shows these under the state
 * word so nothing that matters is only in a tooltip. Empty for a loop with
 * nothing to say.
 */
export function reasonLines(controlState: string, note: string | null, d: StatusDetails | undefined): string[] {
  const out: string[] = [];
  for (const u of d?.unreadable ?? []) out.push(`${unreadableHead(u)}. ${firstSentence(u.remedy)}`);
  if (recordsUnreadable(d) && !d?.unreadable?.length) out.push("status reported records it could not read, and their details could not be read");
  if (d?.unparsed?.length) out.push(unparsedLine(d.unparsed));
  if (d?.sendRecord) out.push(`Send record: ${firstSentence(d.sendRecord)}`);
  if (d?.deadline?.reached === true) out.push(`Deadline reached (${dateLabel(d.deadline.at)}); evaluated on the next check`);
  if (d?.sends?.reached) out.push(`Send limit reached: ${sendsText(d.sends)}`);
  if (d?.disk) out.push(`Disk: ${d.disk.message}`);
  const wait = d?.limitWait;
  if (wait && wait.state !== "none" && !wait.resetPassed) {
    out.push(wait.held
      ? `Long limit wait recorded before the loop was held (${wait.held}); it may be out of date`
      : `Long limit wait: no handoff possible; resets ${dateLabel(wait.resetsAt)}${wait.resetIn ? ` (in ${wait.resetIn})` : ""}; the loop sends nothing until then${resetAfterDeadline(d) ? `; ${AFTER_DEADLINE}` : ""}`);
  }
  if (d?.schedule?.overdue) out.push(`Needs you: ${d.schedule.message ?? "no check has run for longer than usual"}`);
  const np = noProgressLine(controlState, d);
  if (np) out.push(np);
  if (note && controlState !== "enabled") out.push(`Note: ${shortNote(note, 160)}`);
  return out;
}

/** A tooltip cut to a length a person can read; the full text stays in the expanded view. */
export function boundedTitle(text: string, max = 600): string {
  return text.length <= max ? text : `${text.slice(0, max - 1).trimEnd()}…`;
}

export function diagnosticLines(d: StatusDetails | undefined, target: string, supervising: boolean): string[] {
  if (!d) return ["Detailed status unavailable from this build"];
  const lines = [
    `Status read: ${dateLabel(d.readAt)} (snapshot)`,
    goalLabel(d.goal, d.goalState),
    `Task completion: ${d.completed ? "declared; not independently verified" : "not declared"}`,
    `Current target: ${d.targetThread ?? d.targetAdapter} (${target})${supervising ? "; this thread" : "; not this thread"}${d.targetConfigured === false ? "; missing or disabled in config" : ""}`,
    `Build: ${buildSummary(d.runtime).text}${d.runtime?.installedBuild && !buildSummary(d.runtime).differs ? ` · installed ${d.runtime.installedBuild}` : ""}`,
    ...(d.targetProvider ? [`Provider: ${d.targetProvider}${d.targetModel ? ` · model ${d.targetModel}` : ""} (configured)`] : []),
    ...(d.moved ? [`Moved: ${d.moved.text}`] : []),
    // The words `looptender status` prints, with the successor when it names one.
    ...(d.supersededBy !== undefined ? [`Superseded: successor reference is recorded${d.supersededBy ? ` (${d.supersededBy})` : ""}; resume and takeover are refused`] : []),
    d.runtime ? `Check evidence: ${d.runtime.state} · ${d.runtime.freshness} · source ${d.runtime.source}` : "Check evidence: unavailable from this build",
  ];
  const daemon = d.daemon;
  if (daemon?.state === "responsive") {
    lines.push(`Daemon answered: ${dateLabel(daemon.observedAt)} · ${daemon.phase ?? "phase unavailable"}${daemon.dryRun ? " · dry run" : ""} · build comparison ${daemon.binaryComparison ?? "unknown"}`);
    lines.push(`Next daemon check: ${daemon.nextCheckAt ? dateLabel(daemon.nextCheckAt) + " (expected, not guaranteed)" : "not scheduled yet"}`);
  } else {
    lines.push(`Daemon: ${daemon?.state ?? "unavailable from this build"}; running supervisor not confirmed`);
  }
  if (d.schedule?.overdue) lines.push(`Schedule: ${d.schedule.message ?? "no check has run for longer than usual; the loop's scheduler may have stopped"}`);
  // Only a judged loop whose usual interval is known is said to be on time.
  else if (d.schedule?.checkedBy === "ticks") lines.push(`Schedule: checked by ticks run without a daemon (an external scheduler or by hand), about every ${d.schedule.cadence ?? "unknown interval"}`);
  else if (d.schedule?.judging && d.schedule.basis === "usual" && !d.schedule.slept) lines.push("Schedule: checks are running at their usual interval");
  else if (!d.schedule) lines.push("External scheduler health: not inspected; a daemon reply does not establish agent health");
  const wait = d.limitWait;
  if (wait && wait.state !== "none" && !wait.resetPassed && wait.held) {
    lines.push(`Long limit wait: recorded before the loop was held (${wait.held}); a held loop observes nothing, so it may be out of date.`);
  } else if (wait && wait.state !== "none" && !wait.resetPassed) {
    const why: Record<string, string> = {
      limit_failover_disabled: "automatic handoff is off for this error",
      limit_no_fallback: "no other route is configured or none passed its check",
      limit_refusal_unrecovered: "a refusal on this route is unrecovered",
    };
    const told = wait.state === "reported" ? "a report was requested (receipt unverified)"
      : wait.state === "report_failed" ? "the report failed (delivery not confirmed)"
      : wait.reportConfigured === false ? "nobody was told: no report section when it was recorded"
      : wait.reportConfigured === true ? "nobody was told: limit_wait was not in report.events" : "no report was sent";
    lines.push(`Long limit wait: no handoff possible (${why[wait.reason ?? ""] ?? "reason not recorded"}); resets ${dateLabel(wait.resetsAt)}${wait.resetIn ? ` (in ${wait.resetIn})` : ""}; ${told}. The loop is not held and sends nothing until then.${resetAfterDeadline(d) ? ` ${AFTER_DEADLINE[0]!.toUpperCase()}${AFTER_DEADLINE.slice(1)}.` : ""}`);
  }
  for (const u of d.unreadable ?? []) lines.push(`Unreadable record: ${unreadableHead(u)}. ${u.remedy}`);
  if (d.sendRecord) lines.push(`Send record: ${d.sendRecord}`);
  if (d.deadline) lines.push(`Deadline: ${dateLabel(d.deadline.at)} (reached: ${d.deadline.reached ?? "could not be read"}; evaluated on next check)`);
  if (d.unparsed?.length) lines.push(unparsedLine(d.unparsed));
  if (d.sends) lines.push(`Sends: ${sendsText(d.sends)} (reached: ${d.sends.reached})`);
  if (d.disk) lines.push(`Disk: ${d.disk.message}`);
  if ((d.failedChecks ?? 0) > 0) lines.push(`Checks failing: ${d.failedChecks} failed checks recorded; a responsive daemon does not establish operational supervision`);
  const check = d.lastCheck;
  lines.push(check
    ? `Last recorded action: ${check.action} · ${check.outcome}${check.dryRun ? " · dry run" : ""} · target ${check.target} · ${dateLabel(check.at)}`
    : "Last recorded action: unavailable");
  return lines;
}
