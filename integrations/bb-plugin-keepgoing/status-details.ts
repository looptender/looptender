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
    lines.push(`Long limit wait: no handoff possible (${why[wait.reason ?? ""] ?? "reason not recorded"}); resets ${dateLabel(wait.resetsAt)}${wait.resetIn ? ` (in ${wait.resetIn})` : ""}; ${told}. The loop is not held and sends nothing until then.`);
  }
  if ((d.failedChecks ?? 0) > 0) lines.push(`Checks failing: ${d.failedChecks} failed checks recorded; a responsive daemon does not establish operational supervision`);
  const check = d.lastCheck;
  lines.push(check
    ? `Last recorded action: ${check.action} · ${check.outcome}${check.dryRun ? " · dry run" : ""} · target ${check.target} · ${dateLabel(check.at)}`
    : "Last recorded action: unavailable");
  return lines;
}
