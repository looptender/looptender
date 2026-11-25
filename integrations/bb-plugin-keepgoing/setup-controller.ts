import { publicationMessage, type Publication } from "./registry-contract.ts";
import { createHash, randomUUID } from "node:crypto";
import { z } from "zod";
import { goalDetails } from "./status-details.ts";
import { conversationTask, checkSchema, failureMessage, refusalMessage, refusedReportSchema, setupInput, targetOf, statusFollowsStandard, type ContinuationCandidate, type Refusal, type SetupFailure, type SetupTarget, type SetupPlan, type SetupRecord, type SetupResult } from "./setup-contract.ts";
import { planBounds, sameBounds, type Bounds } from "./new-thread-contract.ts";
import type { TakeoverChoice } from "./setup-host.ts";

export interface SetupDependencies {
  target(threadId: string): Promise<SetupTarget>;
  binary(): Promise<string>;
  records(): Promise<SetupRecord[]>;
  save(records: SetupRecord[]): Promise<void>;
  guard(threadId: string): Promise<void>;
  init(bin: string, target: SetupTarget, name: string, task: string, dryRun: boolean, withGoal: boolean, bounds?: Bounds, followStandard?: boolean, takeover?: TakeoverChoice, succeeds?: string): Promise<{ plan: SetupPlan; registration?: Publication; warnings?: string[] } | { refused: Refusal } | { noRoutes: true } | { failed: SetupFailure }>;
  // Whether the host's looptender has a standard route list with routes.
  standardRoutes(bin: string, target: SetupTarget): Promise<boolean>;
  command(bin: string, record: SetupRecord, operation: "status" | "doctor" | "start", json?: true): Promise<{ code: number; data: unknown; registration?: Publication; method?: "service" | "background" }>;
  // What BB's metadata for the thread says about whether it has had a turn.
  // Optional: absent means "unknown", and the built-in task covers that.
  conversation?(threadId: string): Promise<"none" | "unknown">;
  // Thread title from BB metadata (never transcript content), for default slug.
  threadTitle?(threadId: string): Promise<string | undefined>;
  // Existing loops on this host that a new setup can continue (--succeeds).
  continuationCandidates?(bin: string, target: SetupTarget): Promise<ContinuationCandidate[]>;
  changed(): void;
}
const agent = z.object({ adapter: z.literal("bb"), bbThread: z.string(), provider: z.string() });
const statusSchema = z.object({
  target: z.string(), primaryAgent: agent, targetAgent: agent, controlState: z.string(),
  goal: z.unknown().optional(), goalError: z.unknown().optional(),
  redundancy: z.unknown().optional(),
  daemon: z.object({ state: z.string() }).optional(),
});
const doctorSchema = z.object({ schemaVersion: z.literal(1), targetAgent: agent, checks: z.array(checkSchema).max(300) });
const startedReport = z.object({ schemaVersion: z.literal(1), outcome: z.literal("started") });
const equal = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
const digest = (s: string) => createHash("sha256").update(s).digest("hex");

/** The most setups the plugin keeps a reference to. There is no removal yet. */
export const MAX_SETUPS = 32;

export function createSetupController(d: SetupDependencies) {
  const previews = new Map<string, { target: SetupTarget; name: string; taskHash: string; withGoal: boolean; followStandard: boolean; takeover: "on" | "off"; choice?: TakeoverChoice; succeeds?: string; succeedsMeta?: { configPath: string; loop: string; displayName?: string }; bounds?: Bounds; bin: string; plan: SetupPlan; expires: number }>();
  let queue: Promise<unknown> = Promise.resolve();
  const serialize = <T>(fn: () => Promise<T>): Promise<T> => {
    const next = queue.then(fn, fn);
    queue = next.catch(() => {});
    return next;
  };
  const sameTarget = async (r: SetupRecord) => {
    const now = await d.target(r.threadId);
    if (!equal(now, targetOf(r))) throw new SetupPolicyError("The thread's environment or provider changed. Setup does not transfer an existing loop.");
  };
  const inspect = async (bin: string, saved: SetupRecord): Promise<SetupResult> => {
    let r = saved;
    await sameTarget(r);
    const status = await d.command(bin, r, "status");
    const s = statusSchema.safeParse(status.data);
    const matches = (a: z.infer<typeof agent>) => a.bbThread === r.threadId && a.provider === r.provider;
    // Only a loop that follows the standard route list has other routes, and
    // can be on one of them: its routes are this thread on another model, so
    // the target agent is still this thread. Status says which it is, so a
    // flag an older plugin build dropped from the record is restored here.
    const follows = statusFollowsStandard(status.data);
    if (status.code !== 0 || !s.success || (!follows && (s.data.redundancy !== undefined || s.data.target !== "primary")) || !matches(s.data.primaryAgent) || !matches(s.data.targetAgent)) throw new SetupPolicyError("The saved loop target could not be verified. No start or transfer was performed.");
    if (follows && r.followsStandard !== true) {
      const restored = { ...r, followsStandard: true as const };
      try { await d.save((await d.records()).map(x => x.id === r.id ? restored : x)); r = restored; } catch { /* shown as following; saved next time */ }
    }
    const result = await d.command(bin, r, "doctor");
    const report = doctorSchema.safeParse(result.data);
    if (!report.success || !matches(report.data.targetAgent)) return { ok: true, record: r, canStart: false, message: "Setup is saved, but diagnostics could not verify this target. Check host access and retry diagnostics." };
    const checks = report.data.checks;
    const goal = goalDetails(s.data.goal, s.data.goalError);
    // "Unsupervised" (supervisor.schedule) is the problem Start fixes, so it
    // alone never blocks Start; every other error does.
    const blocking = checks.filter(c => c.status === "error" && c.id !== "supervisor.schedule");
    const onlyUnsupervised = result.code !== 0 && blocking.length === 0 && checks.some(c => c.status === "error");
    const canStart = !r.registryId && goal.goalState !== "unavailable" && (result.code === 0 || onlyUnsupervised) && checks.some(c => c.id === "agent.primary.target" && c.status === "ok") && blocking.length === 0 && ["disabled", "enabled"].includes(s.data.controlState) && s.data.daemon?.state !== "responsive";
    return { ok: true, record: r, ...goal, checks, canStart, message: r.registryId ? "Imported loop reference saved. This panel does not start imported loops; inspect check evidence and use the host CLI separately when appropriate." : canStart ? "Review the diagnostics, then start background supervision when ready." : "Setup is saved. Existing holds, a responding daemon, or diagnostic failures prevent another start here." };
  };
  const handle = async (raw: z.infer<typeof setupInput>, bounds?: Bounds): Promise<SetupResult> => {
    const input = setupInput.parse(raw);
    const bin = await d.binary();
    const records = await d.records();
    const existing = records.find(r => r.threadId === input.threadId);
    if (input.action === "inspect" || input.action === "start") {
      if (!existing) return { ok: true, message: "No loop is set up for this thread yet." };
      let result: SetupResult;
      try { result = await inspect(bin, existing); }
      catch { return { ok: false, record: existing, canStart: false, message: "The saved target could not be verified. Refresh diagnostics after checking the host and configuration; no transfer was performed." }; }
      if (input.action === "inspect" || !result.canStart) return result;
      await sameTarget(existing);
      // --json only when the preview this setup came from advertised it, and
      // the same binary is configured now. Otherwise start exactly as before.
      const json = existing.startJson === true && existing.startJsonBin === bin ? true as const : undefined;
      const start = await d.command(bin, existing, "start", json);
      const startRefused = start.code !== 0 ? refusedReportSchema.safeParse(start.data) : undefined;
      if (startRefused?.success) { d.changed(); return { ...result, ok: false, canStart: false, started: false, refused: startRefused.data.refusal, message: refusalMessage(startRefused.data.refusal, "start") }; }
      d.changed();
      // With --json, a start is confirmed by its own report as well as its exit code.
      const confirmed = start.code === 0 && (!json || startedReport.safeParse(start.data).success);
      let publicationSaved = true;
      if (confirmed) {
        try {
          await sameTarget(existing);
          const current = await d.records();
          const saved = current.find(r => r.id === existing.id);
          if (!saved || !equal(targetOf(saved), targetOf(existing)) || saved.configPath !== existing.configPath) throw new Error("Reference changed");
          const updated = { ...saved, publication: start.registration ?? { state: "repair_needed" as const, source: "cli_registry" as const } };
          await d.save(current.map(r => r.id === updated.id ? updated : r));
          result = { ...result, record: updated };
        } catch { publicationSaved = false; }
      }
      const startedText = start.method === "service"
        ? "Background supervision started (user service installed; survives reboot). Existing timers were not changed."
        : "Background supervision started. Existing timers were not changed.";
      return { ...result, canStart: false, ok: confirmed, started: confirmed, message: confirmed ? startedText + publicationMessage(start.registration) + (publicationSaved ? "" : " The plugin could not record this publication result; inspect references separately before retrying anything.") : "Start could not be confirmed. Check loop status before retrying; no holds were cleared." };
    }
    if (existing) throw new SetupPolicyError("This thread already has a saved setup. Use its diagnostics and controls.");
    await d.guard(input.threadId);
    const target = await d.target(input.threadId);
    const builtIn = input.mode === "conversation";
    const task = builtIn ? conversationTask(input.note) : input.task ?? "";
    const withGoal = input.withGoal ?? false;
    const followStandard = input.followStandard ?? false;
    // A takeover is offered only when the setup does not follow the list.
    const takeover = followStandard ? "off" : input.takeover ?? "on";
    if (!task.trim() || Buffer.byteLength(task) > 262144) throw new SetupPolicyError("Enter a task of at most 256 KiB.");
    if (input.action === "preview") {
      if (!input.name) throw new SetupPolicyError("Enter a loop name using lowercase letters, digits, and hyphens.");
      for (const [key, p] of previews) if (p.expires < Date.now()) previews.delete(key);
      if (previews.size >= 64) throw new SetupPolicyError("Too many pending previews. Wait five minutes and retry.");
      let succeedsMeta: { configPath: string; loop: string; displayName?: string } | undefined;
      if (input.succeeds) {
        const candidates = await d.continuationCandidates?.(bin, target) ?? [];
        const match = candidates.find(c => c.configPath === input.succeeds);
        if (!match) throw new SetupPolicyError("The selected loop to continue is not registered or configured on this host.");
        succeedsMeta = { configPath: match.configPath, loop: match.loop, ...(match.displayName ? { displayName: match.displayName } : {}) };
      }
      // init is asked first which provider is usable (an older CLI that does
      // not know how is asked nothing). When it offers a takeover, it is
      // previewed for real when on, or declined explicitly when off.
      let choice: TakeoverChoice | undefined;
      let previewed = await d.init(bin, target, input.name, task, true, withGoal, bounds, followStandard, followStandard ? undefined : "detect", input.succeeds);
      if ("failed" in previewed) return { ok: false, message: failureMessage(previewed.failed, false, input.name) };
      if ("noRoutes" in previewed) return { ok: false, message: "None of your standard route list's routes applies to this thread, so this setup cannot follow it. Clear the choice to set it up without other routes." };
      if ("refused" in previewed) return { ok: false, refused: previewed.refused, message: refusalMessage(previewed.refused, "init") };
      const offer = previewed.plan.takeoverOffer;
      if (offer) {
        choice = takeover === "on" ? { provider: offer.provider, model: offer.model } : "none";
        previewed = await d.init(bin, target, input.name, task, true, withGoal, bounds, followStandard, choice, input.succeeds);
        if ("failed" in previewed) return { ok: false, message: failureMessage(previewed.failed, false, input.name) };
        if ("noRoutes" in previewed || "refused" in previewed) throw new SetupPolicyError("The resolved setup changed. Preview again before creating it.");
      }
      const plan: SetupPlan = { ...previewed.plan, ...(offer ? { takeoverOffer: offer } : {}), ...(succeedsMeta ? { succeeds: succeedsMeta } : {}) };
      if (typeof choice === "object" && (plan.takeover?.provider !== choice.provider || plan.takeover?.model !== choice.model)) throw new SetupPolicyError("Setup did not report the takeover that was asked for. Nothing was created.");
      if (typeof choice !== "object" && plan.takeover) throw new SetupPolicyError("Setup reported a takeover that was not asked for. Nothing was created.");
      // Asked-for loop-wide limits must be exactly what keepgoing reports it
      // will write. Creation is then bound to this plan, so they cannot change.
      if (bounds) { const reported = planBounds(plan.limits); if (!reported || !sameBounds(reported, bounds)) throw new SetupPolicyError("Setup did not report the loop-wide limits that were asked for. Nothing was created."); }
      const ticket = randomUUID();
      // Following the list must come back as exactly that, with the routes
      // to show; not following must come back with none.
      if (followStandard !== (plan.routes !== undefined)) throw new SetupPolicyError("Setup did not report the route choice that was asked for. Nothing was created.");
      previews.set(ticket, { target, name: input.name, taskHash: digest(task), withGoal, followStandard, takeover, choice, ...(input.succeeds ? { succeeds: input.succeeds, succeedsMeta } : {}), bounds, bin, plan, expires: Date.now() + 300_000 });
      return { ok: true, ticket, plan, ...(builtIn ? { task } : {}), message: followStandard ? "Preview only. Creating this setup leaves supervision disabled. It follows your standard route list and hands off to it on a rate limit." : plan.takeover ? `Preview only. Creating this setup leaves supervision disabled. ${plan.takeover.text}` : "Preview only. Creating this setup leaves supervision disabled; fallbacks and automatic handoffs are off." };
    }
    const preview = previews.get(input.ticket ?? "");
    previews.delete(input.ticket ?? ""); // one attempt, including failure; never replay a creation
    if (!preview || preview.expires < Date.now() || preview.bin !== bin || preview.withGoal !== withGoal || preview.followStandard !== followStandard || preview.takeover !== takeover || preview.succeeds !== input.succeeds || !equal(preview.bounds, bounds) || preview.taskHash !== digest(task) || !equal(preview.target, target)) throw new SetupPolicyError("The preview expired or its task, goal choice, route choice, target, or binary changed. Preview again.");
    if (records.length >= MAX_SETUPS) throw new SetupPolicyError(`The plugin already has ${MAX_SETUPS} saved setups; manage existing loops before adding another.`);
    const fresh = await d.init(bin, target, preview.name, task, true, withGoal, bounds, followStandard, preview.choice, preview.succeeds);
    if ("failed" in fresh) return { ok: false, message: failureMessage(fresh.failed, false, preview.name) };
    if ("noRoutes" in fresh) throw new SetupPolicyError("The resolved setup changed. Preview again before creating it.");
    if ("refused" in fresh) return { ok: false, refused: fresh.refused, message: refusalMessage(fresh.refused, "init") };
    const { takeoverOffer: _o, succeeds: _s1, ...previewedPlan } = preview.plan;
    void _o; void _s1;
    const same = (p: SetupPlan) => { const { takeoverOffer: _x, succeeds: _s2, ...rest } = p; void _x; void _s2; return equal(rest, previewedPlan); };
    if (!same(fresh.plan)) throw new SetupPolicyError("The resolved setup changed. Preview again before creating it.");
    const created = await d.init(bin, target, preview.name, task, false, withGoal, bounds, followStandard, preview.choice, preview.succeeds);
    if ("failed" in created) return { ok: false, message: failureMessage(created.failed, true, preview.name) };
    if ("noRoutes" in created) throw new SetupPolicyError("Setup result changed. A disabled configuration may exist; inspect the destination before retrying.");
    // Refused at creation, after a preview that passed: keepgoing checks
    // before it writes anything, so nothing was created.
    if ("refused" in created) return { ok: false, refused: created.refused, message: refusalMessage(created.refused, "init") };
    if (!same(created.plan)) throw new SetupPolicyError("Setup result changed. A disabled configuration may exist; inspect the destination before retrying.");
    const record = { ...target, ...(created.registration ? { publication: created.registration } : {}), id: randomUUID(), configPath: created.plan.configPath, loop: created.plan.loop, ...(created.plan.startJson === true ? { startJson: true as const, startJsonBin: bin } : {}), ...(created.plan.routes ? { followsStandard: true as const } : {}) };
    try { await d.save([...records, record]); }
    catch { return { ok: false, record, ...(created.warnings?.length ? { warnings: created.warnings } : {}), message: "A disabled setup was created, but the plugin could not save its reference. Keep the displayed config path for recovery; do not recreate or start it automatically." + publicationMessage(created.registration) }; }
    d.changed();
    try { const result = await inspect(bin, record); return { ...result, ...(created.warnings?.length ? { warnings: created.warnings } : {}), message: result.message + publicationMessage(created.registration) }; }
    catch { return { ok: true, record, canStart: false, ...(created.warnings?.length ? { warnings: created.warnings } : {}), message: "Setup was saved, but target diagnostics are unavailable. Retry diagnostics before starting." + publicationMessage(created.registration) }; }
  };
  return {
    // Serialize mutations and previews so concurrent clients cannot create two
    // loops for one thread or overwrite the stored record list.
    // `bounds` are loop-wide limits chosen by a server-side caller. They are
    // not part of the browser's input, and a preview made with one set
    // cannot be created with another.
    call: async (input: z.infer<typeof setupInput>, bounds?: Bounds): Promise<SetupResult> => {
      const result = await serialize(async (): Promise<SetupResult> => {
        try { return await handle(input, bounds); }
        catch (error) {
          // Dependency/SDK errors can contain paths, command arguments, or task
          // input, so only the error's name is used (see unexpectedMessage). A
          // failed looptender command is not an error here: it comes back as a
          // `failed` result carrying its status and one masked line of its output.
          return { ok: false, message: error instanceof SetupPolicyError ? error.message : unexpectedMessage(error, input.action) };
        }
      });
      // A thread with no setup yet: ask whether the standard route list can
      // be offered, outside the queue, so it never holds up Resume or a move.
      // Offered only when the host's CLI says a route applies to this thread;
      // any failure to tell means setup is offered exactly as before.
      if (input.action === "inspect" && result.ok && !result.record) {
        // Asked outside the queue and each defaults to "not said":
        // the standard route list is offered only when the host's CLI says a
        // route applies here, and the conversation is "unknown" unless BB's
        // metadata says the thread has not started.
        let standardRoutes = false;
        let resolvedBin: string | undefined;
        let resolvedTarget: SetupTarget | undefined;
        try {
          resolvedBin = await d.binary();
          resolvedTarget = await d.target(input.threadId);
          standardRoutes = await d.standardRoutes(resolvedBin, resolvedTarget);
        } catch { /* not offered */ }
        let conversation: "none" | "unknown" = "unknown";
        try { conversation = (await d.conversation?.(input.threadId)) ?? "unknown"; } catch { /* cannot tell */ }
        let threadTitle: string | undefined;
        try { threadTitle = await d.threadTitle?.(input.threadId); } catch { /* fallback slug */ }
        let continuationCandidates: ContinuationCandidate[] | undefined;
        try {
          if (resolvedBin && resolvedTarget && d.continuationCandidates) {
            continuationCandidates = await d.continuationCandidates(resolvedBin, resolvedTarget);
          }
        } catch { /* none listed */ }
        return {
          ...result,
          ...(standardRoutes ? { standardRoutes: true as const } : {}),
          conversation,
          ...(threadTitle ? { threadTitle } : {}),
          ...(continuationCandidates ? { continuationCandidates } : {}),
        };
      }
      return result;
    },
    dispose: () => previews.clear(),
  };
}

class SetupPolicyError extends Error {}

/**
 * For an error nothing above anticipated. Only the error's name is used, never
 * its text, which can carry paths or task input. A call to the host that ran
 * out of time says so; anything else says it was unexpected and, for a preview,
 * that nothing was created.
 */
function unexpectedMessage(error: unknown, action: z.infer<typeof setupInput>["action"]): string {
  const name = error instanceof Error ? error.name : "";
  const timedOut = name === "TimeoutError" || name === "AbortError" || name === "HostOnlineRpcTimeoutError";
  const creating = action === "create";
  const effect = creating ? "The plugin saved nothing, but a switched-off setup may exist on the host: refresh status before trying again." : action === "preview" ? "Nothing was created." : "Nothing was changed.";
  const what = action === "preview" ? "The preview" : creating ? "Creating the setup" : action === "start" ? "Starting" : "Checking the setup";
  return timedOut
    ? `${what} did not finish: the host did not answer within the plugin's time limit (30 s). The machine may be busy or looptender may be unresponsive. ${effect}`
    : `${what} failed unexpectedly${name && name !== "Error" ? ` (${name})` : ""}. ${effect} Refresh status and try again; if it keeps failing, run \`looptender doctor\` on the host.`;
}
