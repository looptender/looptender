import { useEffect, useState } from "react";
import { goalLabel } from "./status-details.ts";
import type { SetupResult } from "./setup-contract.ts";
import type { z } from "zod";
import type { setupInput } from "./setup-contract.ts";

type Call = (input: z.infer<typeof setupInput>) => Promise<SetupResult>;
// `hideStart` leaves starting to another control on the same thread, so a
// thread never shows two buttons that start the same loop.
export function SetupPanel({ threadId, call, onChanged, hideStart = false }: { threadId: string; call: Call; onChanged(): void; hideStart?: boolean }) {
  const [name, setName] = useState(`bb-${threadId.replace(/[^a-z0-9-]/g, "-").slice(0, 50)}`);
  const [withGoal, setWithGoal] = useState(true);
  // Off unless chosen: following the list also turns on handoff.
  const [followStandard, setFollowStandard] = useState(false);
  const [standardRoutes, setStandardRoutes] = useState(false);
  // The takeover by a new conversation on another provider: on unless
  // turned off. The offer is remembered from the last preview that made it,
  // since a preview with the takeover off carries none.
  const [takeover, setTakeover] = useState<"on" | "off">("on");
  const [offer, setOffer] = useState<{ provider: string; model: string }>();
  const [task, setTask] = useState("");
  // "Keep this conversation going" sends a fixed task; "own" sends the typed
  // one. Undecided until the thread's metadata is read: a thread with nothing
  // to continue starts on "own".
  // What BB's metadata said when the thread was first inspected. Kept apart
  // from `result`, which a preview or create replaces and which then carries
  // no such field: the default must not change under the person.
  const [conversation, setConversation] = useState<"none" | "unknown">("unknown");
  const [chosen, setChosen] = useState<"conversation" | "own" | undefined>();
  const [note, setNote] = useState("");
  const [result, setResult] = useState<SetupResult>();
  const [busy, setBusy] = useState(true);
  useEffect(() => {
    let mounted = true;
    call({ action: "inspect", threadId }).then(r => { if (mounted) { setResult(r); setStandardRoutes(r.standardRoutes === true); setConversation(r.conversation ?? "unknown"); } })
      .catch(() => { if (mounted) setResult({ ok: false, message: "Setup status is unavailable. Check the host connection and retry." }); })
      .finally(() => { if (mounted) setBusy(false); });
    return () => { mounted = false; };
  }, [threadId, call]);
  const empty = conversation === "none";
  const mode = chosen ?? (empty ? "own" : "conversation");
  async function act(action: "preview" | "create" | "inspect" | "start") {
    setBusy(true);
    try {
      const follow = followStandard && standardRoutes;
      const r = await call({ action, threadId, ...(action === "preview" || action === "create" ? { name, mode, ...(mode === "conversation" ? { note } : { task }), withGoal, ticket: result?.ticket, ...(follow ? { followStandard: true } : { takeover }) } : {}) });
      if (r.plan?.takeoverOffer) setOffer(r.plan.takeoverOffer);
      setResult({
        ...r,
        ...(!r.ok && action === "create" && !r.record ? { plan: result?.plan } : {}),
        ...(!r.record && (action === "inspect" || action === "start") ? { record: result?.record } : {}),
      });
      if (r.record) { setTask(""); setNote(""); onChanged(); }
    } catch { setResult({ ok: false, record: result?.record, plan: result?.plan, message: "The request could not be confirmed. Refresh status before retrying; a disabled setup may already exist." }); }
    finally { setBusy(false); }
  }
  const edit = () => setResult(undefined);
  return <section aria-label="Set up thread supervision" style={{ display: "grid", gap: 8, padding: 12, minWidth: 240, maxWidth: 560, fontSize: 12 }}>
    <strong>Add to Looptender</strong>
    <span>Target: {threadId}</span>
    {!result?.record && <>
      <label>Loop name<input aria-label="Loop name" value={name} disabled={busy} onChange={e => { setName(e.target.value); edit(); }} style={{ display: "block", width: "100%" }} /></label>
      <fieldset style={{ display: "grid", gap: 4, border: "none", padding: 0, margin: 0 }}>
        <legend>What should the loop keep going?</legend>
        <label><input type="radio" name="setup-mode" aria-label="Keep this conversation going" checked={mode === "conversation"} disabled={busy} onChange={() => { setChosen("conversation"); edit(); }} /> Keep this conversation going</label>
        <label><input type="radio" name="setup-mode" aria-label="Write my own task" checked={mode === "own"} disabled={busy} onChange={() => { setChosen("own"); edit(); }} /> Write my own task</label>
      </fieldset>
      {empty && chosen === undefined && <span>This thread may not have started yet, so there may be nothing to continue. Write a task.</span>}
      {mode === "conversation"
        ? <>
          <span>Continues the work already under way in this conversation, following your latest instructions here, and stops for you when it needs you. Nothing from the conversation is read by this plugin.</span>
          <label>Anything to add?<input aria-label="Anything to add?" value={note} disabled={busy} maxLength={300} onChange={e => { setNote(e.target.value.replace(/[\r\n]/g, " ")); edit(); }} style={{ display: "block", width: "100%" }} /></label>
        </>
        : <label>Task and completion criteria<textarea aria-label="Task and completion criteria" value={task} disabled={busy} maxLength={262144} rows={5} onChange={e => { setTask(e.target.value); edit(); }} style={{ display: "block", width: "100%" }} /></label>}
      <label><input type="checkbox" aria-label="Record a goal for this task" checked={withGoal} disabled={busy} onChange={e => { setWithGoal(e.target.checked); edit(); }} /> Record a goal for this task</label>
      <span>{withGoal ? "Use the loop name as the initial goal title. Save the task once in a private file and share current goal references in future prompts. Requires an updated looptender CLI; v0.1.4 does not support this setup option." : "Copy the task into wake instructions without creating a goal record."}</span>
      {standardRoutes && <label><input type="checkbox" aria-label="Follow my standard route list for handoffs" checked={followStandard} disabled={busy} onChange={e => { setFollowStandard(e.target.checked); edit(); }} /> Follow my standard route list for handoffs</label>}
      {offer && !(followStandard && standardRoutes) && <label><input type="checkbox" aria-label="Take over on a usage limit" checked={takeover === "on"} disabled={busy} onChange={e => { setTakeover(e.target.checked ? "on" : "off"); edit(); }} /> On a usage limit, start a new conversation on {offer.provider} ({offer.model}) that takes over</label>}
      <span>{followStandard && standardRoutes
        ? "One existing thread, with its current model and login, that hands off to the routes of your standard route list that apply to it on a rate limit. The preview names them. Creating the setup leaves supervision disabled."
        : "One existing thread, with its current model and login. When another provider is usable, the preview adds a takeover by a new conversation on it, which you can turn off. Creating the setup leaves supervision disabled."}</span>
      <button type="button" disabled={busy || (mode === "own" && !task.trim()) || !name} onClick={() => void act("preview")}>Preview setup</button>
    </>}
    {result?.plan && <div aria-label="Setup preview" style={{ display: "grid", gap: 4 }}>
      {result.task !== undefined && <div aria-label="Task to send" style={{ display: "grid", gap: 2 }}><span>Task that will be sent:</span><pre style={{ margin: 0, whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{result.task}</pre></div>}
      <span>Provider: {result.plan.provider}</span><span>Project: {result.plan.project}</span>
      <span>Configuration: {result.plan.configPath}</span>
      <span>{result.plan.withGoal ? "Goal: record revision 1 referencing a private task snapshot. Creation stays disabled." : "Task: copied into wake instructions; no goal record."}</span>
      <span>Check every {result.plan.limits.interval}. Idle threads can wake on a check; stall nudges are spaced at least {result.plan.limits.wakeEvery} apart.</span>
      <span>Stall after {result.plan.limits.stallAfter}; {result.plan.limits.maxNudges} nudges; {result.plan.limits.maxReprompts} error re-prompts with {result.plan.limits.backoff} backoff.</span>
      {result.plan.routes ? <div aria-label="Routes" style={{ display: "grid", gap: 2 }}>
        <span>Routes: follows your standard route list. Handoff on a rate limit is turned on. On a rate limit the loop hands off to, in order:</span>
        <ul style={{ margin: 0 }}>{result.plan.routes.apply.map(r => <li key={r.id}>{r.id}: {r.model || "model unchanged"}, usage limit {r.group}</li>)}</ul>
      </div> : result.plan.takeover ? <div aria-label="Takeover" style={{ display: "grid", gap: 2 }}>
        <span>Takeover: on (uncheck it above and preview again to turn it off).</span>
        <span>{result.plan.takeover.text}</span>
      </div> : <span>No fallbacks; automatic handoff is off.</span>}
      {result.ticket && <button type="button" disabled={busy} onClick={() => void act("create")}>Create disabled setup</button>}
    </div>}
    {result?.record && <>
      <span>{goalLabel(result.goal, result.goalState)}</span>
      <span>Configuration: {result.record.configPath}</span>
      {result.record.followsStandard && <span>Routes: follows your standard route list; hands off to it on a rate limit.</span>}
      <button type="button" disabled={busy} onClick={() => void act("inspect")}>Refresh diagnostics</button>
      {result.canStart && !hideStart && <>
        <span>Starting enables the loop and launches a background supervisor on this thread’s host. It may prompt the agent when eligible. Other timers are not detected or changed.</span>
        <button type="button" disabled={busy} onClick={() => void act("start")}>Start background supervision</button>
      </>}
    </>}
    {result?.checks && <details><summary>Diagnostics ({result.checks.length})</summary>
      <ul>{result.checks.map((c, i) => <li key={`${c.id}-${i}`}>{c.status}: {c.message}{c.fix && ` ${c.fix}`}</li>)}</ul>
    </details>}
    {busy && <span role="status">Checking setup…</span>}
    {result && <span role={result.ok ? "status" : "alert"}>{result.message}</span>}
  </section>;
}
