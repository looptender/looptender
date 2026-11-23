import { useEffect, useRef, useState } from "react";
import { goalLabel } from "./status-details.ts";
import type { SetupResult } from "./setup-contract.ts";
import type { z } from "zod";
import type { setupInput } from "./setup-contract.ts";
import { SetupStyles } from "./setup-style.tsx";
import { lostMessage } from "./setup-lost.ts";

type Call = (input: z.infer<typeof setupInput>) => Promise<SetupResult>;
type Action = "preview" | "create" | "inspect" | "start";
const NAME = /^[a-z0-9][a-z0-9-]{0,63}$/;

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
  const [pending, setPending] = useState<Action>("inspect");
  // True from a create that saved a record until the panel is left: what was
  // just made is said as made and switched off, not as an existing setup.
  const [created, setCreated] = useState(false);
  const previewRef = useRef<HTMLDivElement>(null);
  const doneRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    let mounted = true;
    call({ action: "inspect", threadId }).then(r => { if (mounted) { setResult(r); setStandardRoutes(r.standardRoutes === true); setConversation(r.conversation ?? "unknown"); } })
      .catch(e => { if (mounted) setResult({ ok: false, message: lostMessage("inspect", e, 0, name) }); })
      .finally(() => { if (mounted) setBusy(false); });
    return () => { mounted = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [threadId, call]);
  const empty = conversation === "none";
  const mode = chosen ?? (empty ? "own" : "conversation");
  const nameOk = NAME.test(name);
  const taskNeeded = mode === "own" && !task.trim();
  async function act(action: Action) {
    setBusy(true); setPending(action);
    const began = Date.now();
    try {
      const follow = followStandard && standardRoutes;
      // BB's RPC layer refuses an input with an undefined value anywhere in it
      // ("is not a JSON value"), before the request leaves the browser; so a
      // key with nothing to say is left out, never set to undefined.
      const r = await call({ action, threadId, ...(action === "preview" || action === "create" ? { name, mode, ...(mode === "conversation" ? { note } : { task }), withGoal, ...(result?.ticket ? { ticket: result.ticket } : {}), ...(follow ? { followStandard: true } : { takeover }) } : {}) });
      if (r.plan?.takeoverOffer) setOffer(r.plan.takeoverOffer);
      setResult({
        ...r,
        ...(!r.ok && action === "create" && !r.record ? { plan: result?.plan } : {}),
        ...(!r.record && (action === "inspect" || action === "start") ? { record: result?.record } : {}),
      });
      if (r.record) { setTask(""); setNote(""); onChanged(); if (action === "create") setCreated(true); }
    } catch (e) { setResult({ ok: false, record: result?.record, plan: result?.plan, message: lostMessage(action, e, Math.round((Date.now() - began) / 1000), name) }); }
    finally { setBusy(false); }
  }
  // Moving the preview or the finished card into view for a keyboard or
  // screen-reader user: focus follows the step that just appeared.
  useEffect(() => { if (result?.ticket && !busy) previewRef.current?.focus(); }, [result?.ticket, busy]);
  useEffect(() => { if (created && result?.record) doneRef.current?.focus(); }, [created, result?.record]);
  const edit = () => setResult(undefined);
  const following = followStandard && standardRoutes;
  const message = result && <p className="msg" role={result.ok ? "status" : "alert"}>{result.message}</p>;
  if (result?.record) {
    const started = result.started === true;
    return <section className="kgs" aria-label="Set up thread supervision">
      <SetupStyles />
      <div className="card done">
        <h3 ref={doneRef} tabIndex={-1}>{started ? "Supervision started" : created ? "Setup created, switched off" : "Setup saved for this thread"}</h3>
        <dl>
          <dt>Loop</dt><dd>{result.record.loop}</dd>
          <dt>Configuration</dt><dd>{result.record.configPath}</dd>
          <dt>Goal</dt><dd>{goalLabel(result.goal, result.goalState)}</dd>
          {result.record.followsStandard && <><dt>Routes</dt><dd>Follows your standard route list; hands off to it on a rate limit.</dd></>}
          {(started || created) && <><dt>Running</dt><dd>{started ? "Yes: a background supervisor was launched on this thread’s host." : "No. The loop is saved switched off and sends nothing until it is started."}</dd></>}
        </dl>
        {result.canStart && !hideStart && <div className="actions">
          <p className="help">Starting enables the loop and launches a background supervisor on this thread’s host. It may prompt the agent when eligible. Other timers are not detected or changed.</p>
          <div className="row"><button type="button" className="btn primary" disabled={busy} onClick={() => void act("start")}>Start background supervision</button></div>
        </div>}
        {result.canStart && hideStart && <p className="help">Next: start it with the looptender control at the top of this thread.</p>}
        <div className="actions"><div className="row"><button type="button" className="btn" disabled={busy} onClick={() => void act("inspect")}>Refresh diagnostics</button></div></div>
      </div>
      {result.checks && <details><summary>Diagnostics ({result.checks.length})</summary>
        <ul>{result.checks.map((c, i) => <li key={`${c.id}-${i}`}>{c.status}: {c.message}{c.fix && ` ${c.fix}`}</li>)}</ul>
      </details>}
      {busy && <p role="status" className="help">Checking…</p>}
      {message}
    </section>;
  }
  const busyText = pending === "preview" ? "Previewing: this usually takes about 10 seconds and creates nothing." : pending === "create" ? "Creating the setup…" : "Checking setup…";
  return <section className="kgs" aria-label="Set up thread supervision" aria-busy={busy}>
    <SetupStyles />
    <div>
      <h3>Add to Looptender</h3>
      <p className="lead">Keep thread {threadId} going with a new loop. Nothing is created until you choose Create setup, and it stays switched off until you start it.</p>
    </div>
    <div className="field">
      <label htmlFor={`kgs-name-${threadId}`}>Loop name</label>
      <input id={`kgs-name-${threadId}`} type="text" aria-label="Loop name" aria-invalid={!nameOk} aria-describedby={`kgs-name-help-${threadId}`} value={name} disabled={busy} onChange={e => { setName(e.target.value); edit(); }} />
      <span id={`kgs-name-help-${threadId}`} className={nameOk ? "help" : "err"}>{nameOk ? "Lowercase letters, digits and hyphens. It names the loop’s configuration folder; this default comes from the thread." : "Use lowercase letters, digits and hyphens, starting with a letter or digit, up to 64 characters."}</span>
    </div>
    <fieldset>
      <legend>What should the loop keep going?</legend>
      <label className="choice" data-checked={mode === "conversation"} data-disabled={busy}><input type="radio" name="setup-mode" aria-label="Keep this conversation going" checked={mode === "conversation"} disabled={busy} onChange={() => { setChosen("conversation"); edit(); }} />
        <span className="t">Keep this conversation going</span>
        <span className="help">Continues the work already under way here, following your latest instructions, and stops for you when it needs you. The plugin reads nothing from the conversation.</span></label>
      <label className="choice" data-checked={mode === "own"} data-disabled={busy}><input type="radio" name="setup-mode" aria-label="Write my own task" checked={mode === "own"} disabled={busy} onChange={() => { setChosen("own"); edit(); }} />
        <span className="t">Write my own task</span>
        <span className="help">Give the loop its own task and completion criteria.</span></label>
      {empty && chosen === undefined && <p className="help">This thread may not have started yet, so there may be nothing to continue. Write a task.</p>}
      {mode === "conversation"
        ? <div className="field"><label htmlFor={`kgs-note-${threadId}`}>Anything to add? <span className="help">(optional, one line)</span></label>
          <input id={`kgs-note-${threadId}`} type="text" aria-label="Anything to add?" value={note} disabled={busy} maxLength={300} onChange={e => { setNote(e.target.value.replace(/[\r\n]/g, " ")); edit(); }} /></div>
        : <div className="field"><label htmlFor={`kgs-task-${threadId}`}>Task and completion criteria</label>
          <textarea id={`kgs-task-${threadId}`} aria-label="Task and completion criteria" value={task} disabled={busy} maxLength={262144} rows={5} onChange={e => { setTask(e.target.value); edit(); }} /></div>}
    </fieldset>
    <fieldset>
      <legend>Options</legend>
      <label className="choice" data-checked={withGoal} data-disabled={busy}><input type="checkbox" aria-label="Record a goal for this task" checked={withGoal} disabled={busy} onChange={e => { setWithGoal(e.target.checked); edit(); }} />
        <span className="t">Record a goal for this task</span>
        <span className="help">{withGoal ? "The loop name becomes the goal title. The task is saved once in a private file and future prompts share a short reference to the goal." : "The task is copied into the wake instructions without a goal record."}</span></label>
      {standardRoutes && <label className="choice" data-checked={followStandard} data-disabled={busy}><input type="checkbox" aria-label="Follow my standard route list for handoffs" checked={followStandard} disabled={busy} onChange={e => { setFollowStandard(e.target.checked); edit(); }} />
        <span className="t">Follow my standard route list for handoffs</span>
        <span className="help">On a rate limit the loop hands off to the routes of your standard list that apply to this thread. The preview names them.</span></label>}
      {offer && !following && <label className="choice" data-checked={takeover === "on"} data-disabled={busy}><input type="checkbox" aria-label="Take over on a usage limit" checked={takeover === "on"} disabled={busy} onChange={e => { setTakeover(e.target.checked ? "on" : "off"); edit(); }} />
        <span className="t">On a usage limit, start a new conversation on {offer.provider} ({offer.model}) that takes over</span>
        <span className="help">Another provider is usable. You can turn this off.</span></label>}
    </fieldset>
    {!result?.plan && <div className="actions">
      <div className="row"><button type="button" className="btn primary" disabled={busy || taskNeeded || !nameOk} aria-describedby={taskNeeded ? `kgs-why-${threadId}` : undefined} onClick={() => void act("preview")}>Preview setup</button></div>
      {taskNeeded && <span id={`kgs-why-${threadId}`} className="help">Write the task to preview the setup.</span>}
    </div>}
    {result?.plan && <div ref={previewRef} tabIndex={-1} className="card" role="group" aria-label="Setup preview">
      <h4>Preview: nothing has been created yet</h4>
      {result.task !== undefined && <div aria-label="Task to send"><span className="label">Task that will be sent</span><pre>{result.task}</pre></div>}
      <dl>
        <dt>Provider</dt><dd>{result.plan.provider}</dd>
        <dt>Project</dt><dd>{result.plan.project}</dd>
        <dt>Configuration</dt><dd>{result.plan.configPath}</dd>
        <dt>Task</dt><dd>{result.plan.withGoal ? "Recorded as goal revision 1 referencing a private task snapshot." : "Copied into the wake instructions; no goal record."}</dd>
        <dt>Checks</dt><dd>Every {result.plan.limits.interval}. Idle threads can wake on a check; stall nudges are spaced at least {result.plan.limits.wakeEvery} apart.</dd>
        <dt>Limits</dt><dd>Stall after {result.plan.limits.stallAfter}; {result.plan.limits.maxNudges} nudges; {result.plan.limits.maxReprompts} error re-prompts with {result.plan.limits.backoff} backoff.</dd>
        <dt>Handoff</dt><dd>{result.plan.routes ? <div aria-label="Routes">
          <span>Follows your standard route list. Handoff on a rate limit is turned on. On a rate limit the loop hands off to, in order:</span>
          <ul>{result.plan.routes.apply.map(r => <li key={r.id}>{r.id}: {r.model || "model unchanged"}, usage limit {r.group}</li>)}</ul>
        </div> : result.plan.takeover ? <div aria-label="Takeover">
          <span>Takeover: on (uncheck it above and preview again to turn it off).</span> <span>{result.plan.takeover.text}</span>
        </div> : "No fallbacks; automatic handoff is off."}</dd>
      </dl>
      {result.ticket && <div className="actions">
        <div className="row"><button type="button" className="btn primary" disabled={busy} onClick={() => void act("create")}>Create setup</button></div>
        <span className="help">Create setup saves the configuration and leaves supervision switched off. Starting it is a separate step.</span>
      </div>}
      <div className="row"><span className="help">Changed your mind? Edit anything above and preview again.</span></div>
    </div>}
    {busy && <p role="status" className="help">{busyText}</p>}
    {message}
  </section>;
}
