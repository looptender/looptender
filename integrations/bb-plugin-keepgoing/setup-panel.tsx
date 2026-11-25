import { useEffect, useRef, useState } from "react";
import { goalLabel } from "./status-details.ts";
import { fallbackSlug, slugFromPredecessor, slugFromTitle, type ContinuationCandidate, type SetupResult, type ThreadSetupMeta } from "./setup-contract.ts";
import type { z } from "zod";
import type { setupInput } from "./setup-contract.ts";
import { SetupStyles } from "./setup-style.tsx";
import { lostMessage } from "./setup-lost.ts";

type Call = (input: z.infer<typeof setupInput>) => Promise<SetupResult>;
type Action = "preview" | "create" | "inspect" | "start";
const NAME = /^[a-z0-9][a-z0-9-]{0,63}$/;

function formatThreadSetup(meta: ThreadSetupMeta | undefined): string | undefined {
  if (!meta) return undefined;
  const provider = meta.providerLabel ?? meta.provider;
  const model = meta.modelLabel ?? meta.model;
  const base = [provider, model].filter(Boolean).join(" · ");
  if (!base) return undefined;
  return meta.reasoningLevel ? `${base} (${meta.reasoningLevel})` : base;
}

// `hideStart` leaves starting to another control on the same thread, so a
// thread never shows two buttons that start the same loop.
export function SetupPanel({ threadId, call, onChanged, onClose, hideStart = false }: { threadId: string; call: Call; onChanged(): void; onClose?(): void; hideStart?: boolean }) {
  const [intent, setIntent] = useState<"new" | "continue">("new");
  const [threadTitle, setThreadTitle] = useState<string | undefined>();
  const [threadMeta, setThreadMeta] = useState<ThreadSetupMeta | undefined>();
  const [candidates, setCandidates] = useState<ContinuationCandidate[]>([]);
  const [succeedsPath, setSucceedsPath] = useState("");
  const [name, setName] = useState(() => fallbackSlug(threadId));
  const [nameEdited, setNameEdited] = useState(false);
  const [withGoal, setWithGoal] = useState(true);
  // Off unless chosen: following the list also turns on handoff.
  const [followStandard, setFollowStandard] = useState(false);
  const [standardRoutes, setStandardRoutes] = useState(false);
  // Opt-in cross-provider takeover ("off" by default so non-Claude threads
  // never default to Claude/Fable/Opus). Remembered once offered so it can
  // be toggled in Options.
  const [takeover, setTakeover] = useState<"on" | "off">("off");
  const [offer, setOffer] = useState<{ provider: string; model: string }>();
  const [task, setTask] = useState("");
  // "Keep this conversation going" sends a fixed task; "own" sends the typed
  // one. Undecided until the thread's metadata is read: a thread with nothing
  // to continue starts on "own".
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
    call({ action: "inspect", threadId }).then(r => {
      if (!mounted) return;
      setResult(r);
      setStandardRoutes(r.standardRoutes === true);
      setConversation(r.conversation ?? "unknown");
      setThreadTitle(r.threadTitle);
      if (r.threadMeta) setThreadMeta(r.threadMeta);
      if (r.continuationCandidates) setCandidates(r.continuationCandidates);
      if (r.threadTitle) {
        setName(prev => prev === fallbackSlug(threadId) ? slugFromTitle(r.threadTitle, threadId) : prev);
      }
    })
      .catch(e => { if (mounted) setResult({ ok: false, message: lostMessage("inspect", e, 0, name) }); })
      .finally(() => { if (mounted) setBusy(false); });
    return () => { mounted = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [threadId, call]);
  const empty = conversation === "none";
  const mode = chosen ?? (empty ? "own" : "conversation");
  const nameOk = NAME.test(name);
  const taskNeeded = mode === "own" && !task.trim();
  const continueMissing = intent === "continue" && !succeedsPath;
  const selectedCandidate = candidates.find(c => c.configPath === succeedsPath);

  function selectIntent(next: "new" | "continue") {
    if (next === intent) return;
    setIntent(next);
    edit();
    if (!nameEdited) {
      if (next === "continue" && selectedCandidate) {
        setName(slugFromPredecessor(selectedCandidate, candidates.map(c => c.loop)));
      } else if (next === "new") {
        setName(slugFromTitle(threadTitle, threadId));
      }
    }
  }

  function selectPredecessor(configPath: string) {
    setSucceedsPath(configPath);
    edit();
    const match = candidates.find(c => c.configPath === configPath);
    if (match && !nameEdited) {
      setName(slugFromPredecessor(match, candidates.map(c => c.loop)));
    }
  }

  async function act(action: Action) {
    setBusy(true); setPending(action);
    const began = Date.now();
    try {
      const follow = followStandard && standardRoutes;
      const succeeds = intent === "continue" && succeedsPath ? succeedsPath : undefined;
      // BB's RPC layer refuses an input with an undefined value anywhere in it
      // ("is not a JSON value"), before the request leaves the browser; so a
      // key with nothing to say is left out, never set to undefined.
      const r = await call({
        action,
        threadId,
        ...(action === "preview" || action === "create"
          ? {
              name,
              mode,
              ...(mode === "conversation" ? { note } : { task }),
              withGoal,
              ...(succeeds ? { succeeds } : {}),
              ...(result?.ticket ? { ticket: result.ticket } : {}),
              ...(follow ? { followStandard: true } : { takeover }),
            }
          : {}),
      });
      if (r.plan?.takeoverOffer) setOffer(r.plan.takeoverOffer);
      if (r.threadMeta) setThreadMeta(r.threadMeta);
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
  const threadSummary = formatThreadSetup(result?.threadMeta ?? threadMeta);
  const message = result && <p className="msg" role={result.ok ? "status" : "alert"}>{result.message}</p>;
  const minimizeButton = onClose ? (
    <button type="button" className="kgs-min" aria-label="Minimize Add to Looptender" title="Minimize Add to Looptender" onClick={onClose}>
      Minimize
    </button>
  ) : null;
  if (result?.record) {
    const started = result.started === true;
    const savedSetup = threadSummary ?? result.record.provider;
    const runningSummary = /user service installed/i.test(result.message) ? "user service installed" : "background supervisor launched";
    if (started) {
      return <section className="kgs" aria-label="Set up thread supervision">
        <SetupStyles />
        <div className="card done">
          <div className="kgs-head">
            <h3 ref={doneRef} tabIndex={-1}>Supervision started</h3>
            {minimizeButton}
          </div>
          <dl>
            <dt>Loop</dt><dd>{result.record.loop}</dd>
            <dt>Setup</dt><dd>{savedSetup}</dd>
            <dt>Running</dt><dd>{runningSummary}</dd>
          </dl>
          {result.warnings && result.warnings.length > 0 && <div className="warn" role="status">
            {result.warnings.map((w, i) => <p key={i}>{w}</p>)}
          </div>}
          <div className="actions"><div className="row"><button type="button" className="btn primary" onClick={() => onClose?.()}>Done</button></div></div>
          <details><summary>Details &amp; diagnostics{result.checks ? ` (${result.checks.length})` : ""}</summary>
            <dl>
              <dt>Goal</dt><dd>{goalLabel(result.goal, result.goalState)}</dd>
              <dt>Configuration</dt><dd>{result.record.configPath}</dd>
              {result.record.followsStandard && <><dt>Routes</dt><dd>Follows your standard route list; hands off to it on a rate limit.</dd></>}
            </dl>
            <p className="help">Next: nothing to do. looptender now checks on its own; use Pause or Stop in the looptender controls on this thread to hold it.</p>
            {message}
            <div className="actions"><div className="row"><button type="button" className="btn" disabled={busy} onClick={() => void act("inspect")}>Refresh diagnostics</button></div></div>
            {result.checks && <ul>{result.checks.map((c, i) => <li key={`${c.id}-${i}`}>{c.status}: {c.message}{c.fix && ` ${c.fix}`}</li>)}</ul>}
          </details>
        </div>
        {busy && <p role="status" className="help">Checking…</p>}
      </section>;
    }
    return <section className="kgs" aria-label="Set up thread supervision">
      <SetupStyles />
      <div className="card done">
        <div className="kgs-head">
          <h3 ref={doneRef} tabIndex={-1}>{created ? "Setup created, switched off" : "Setup saved for this thread"}</h3>
          {minimizeButton}
        </div>
        <dl>
          <dt>Loop</dt><dd>{result.record.loop}</dd>
          <dt>Setup</dt><dd>{savedSetup}</dd>
          <dt>Goal</dt><dd>{goalLabel(result.goal, result.goalState)}</dd>
          {created && <><dt>Running</dt><dd>No. The loop is saved switched off and sends nothing until it is started.</dd></>}
        </dl>
        {result.warnings && result.warnings.length > 0 && <div className="warn" role="status">
          {result.warnings.map((w, i) => <p key={i}>{w}</p>)}
        </div>}
        {result.canStart && !hideStart && <div className="actions">
          <p className="help">Enables the loop and starts a background supervisor on this host.</p>
          <div className="row">
            <button type="button" className="btn primary" disabled={busy} onClick={() => void act("start")}>Start background supervision</button>
            <button type="button" className="btn" disabled={busy} onClick={() => void act("inspect")}>Refresh diagnostics</button>
          </div>
        </div>}
        {result.canStart && hideStart && <p className="help">Next: start it with the looptender control at the top of this thread.</p>}
        {!(result.canStart && !hideStart) && <div className="actions"><div className="row"><button type="button" className="btn" disabled={busy} onClick={() => void act("inspect")}>Refresh diagnostics</button></div></div>}
        <details><summary>Configuration &amp; paths</summary>
          <dl>
            <dt>Configuration</dt><dd>{result.record.configPath}</dd>
            {result.record.followsStandard && <><dt>Routes</dt><dd>Follows your standard route list; hands off to it on a rate limit.</dd></>}
          </dl>
        </details>
        {message}
      </div>
      {result.checks && <details><summary>Diagnostics ({result.checks.length})</summary>
        <ul>{result.checks.map((c, i) => <li key={`${c.id}-${i}`}>{c.status}: {c.message}{c.fix && ` ${c.fix}`}</li>)}</ul>
      </details>}
      {busy && <p role="status" className="help">Checking…</p>}
    </section>;
  }
  const busyText = pending === "preview" ? "Previewing (creates nothing)…" : pending === "create" ? "Creating setup…" : "Checking setup…";
  const optionSummary = [withGoal ? "goal on" : "goal off", following ? "standard routes" : takeover === "on" && offer ? `takeover: ${offer.provider}` : null].filter(Boolean).join(" · ");
  const previewSetupLabel = formatThreadSetup(result?.threadMeta ?? threadMeta) ?? result?.plan?.provider;
  const handoffSummary = result?.plan?.routes
    ? `Standard route list (${result.plan.routes.apply.length} route${result.plan.routes.apply.length === 1 ? "" : "s"}) on rate limit.`
    : result?.plan?.takeover
      ? `Takeover on ${result.plan.takeover.provider} (${result.plan.takeover.model}) on usage limit.`
      : "No fallbacks; automatic handoff is off.";
  return <section className="kgs" aria-label="Set up thread supervision" aria-busy={busy}>
    <SetupStyles />
    <div className="kgs-head">
      <div>
        <h3>Add to Looptender</h3>
        <p className="lead">{threadSummary ? `Running on ${threadSummary}. Starts switched off.` : "Preview first; new loops start switched off."}</p>
      </div>
      {minimizeButton}
    </div>
    <div className="tabs" role="tablist" aria-label="Setup mode">
      <button type="button" role="tab" className="tab" aria-selected={intent === "new"} disabled={busy} onClick={() => selectIntent("new")}>
        New loop
      </button>
      <button type="button" role="tab" className="tab" aria-selected={intent === "continue"} disabled={busy} onClick={() => selectIntent("continue")}>
        Continue existing{candidates.length > 0 ? ` (${candidates.length})` : ""}
      </button>
    </div>
    <div className="kgs-intent-help">
      <p className="help"><strong>New loop:</strong> For a brand-new goal on this thread. (If you&apos;re continuing an existing supervised BB thread directly, it&apos;s already covered.)</p>
      <p className="help"><strong>Continue existing:</strong> You opened a new BB thread to take over an older loop&apos;s goal (e.g. after a model/context switch) — inherits its name and archives the old loop after the first check.</p>
    </div>
    {intent === "continue" && <div className="field">
      <label htmlFor={`kgs-succeeds-${threadId}`}>Loop to continue</label>
      {candidates.length > 0 ? <>
        <select id={`kgs-succeeds-${threadId}`} aria-label="Loop to continue" value={succeedsPath} disabled={busy} onChange={e => selectPredecessor(e.target.value)}>
          <option value="">Choose an existing loop</option>
          {candidates.map(c => <option key={c.configPath} value={c.configPath}>
            {c.displayName ? `${c.displayName} (${c.loop})` : c.loop}
            {!c.sameProject && c.project ? ` · ${c.project}` : ""}
            {` · ${c.archived ? "archived" : c.controlState}`}
          </option>)}
        </select>
        <span className="help">The old loop will be archived automatically once this new loop completes its first check; nothing is deleted.</span>
      </> : <p className="help">No existing loops found on this host.</p>}
    </div>}
    <div className="field">
      <label htmlFor={`kgs-name-${threadId}`}>Loop name</label>
      <input id={`kgs-name-${threadId}`} type="text" aria-label="Loop name" aria-invalid={!nameOk} aria-describedby={`kgs-name-help-${threadId}`} value={name} disabled={busy} onChange={e => { setName(e.target.value); setNameEdited(true); edit(); }} />
      {!nameOk && <span id={`kgs-name-help-${threadId}`} className="err">Use lowercase letters, digits and hyphens, starting with a letter or digit, up to 64 characters.</span>}
    </div>
    <fieldset>
      <legend>What should the loop keep going?</legend>
      <label className="choice" data-checked={mode === "conversation"} data-disabled={busy}><input type="radio" name="setup-mode" aria-label="Keep this conversation going" aria-describedby={`kgs-h-conv-${threadId}`} checked={mode === "conversation"} disabled={busy} onChange={() => { setChosen("conversation"); edit(); }} />
        <span className="t">Keep this conversation going</span>
        <span className="help" id={`kgs-h-conv-${threadId}`}>Continues this thread; never reads conversation text.</span></label>
      <label className="choice" data-checked={mode === "own"} data-disabled={busy}><input type="radio" name="setup-mode" aria-label="Write my own task" aria-describedby={`kgs-h-own-${threadId}`} checked={mode === "own"} disabled={busy} onChange={() => { setChosen("own"); edit(); }} />
        <span className="t">Write my own task</span>
        <span className="help" id={`kgs-h-own-${threadId}`}>Custom task and completion criteria.</span></label>
      {empty && chosen === undefined && <p className="help">This thread may not have started yet, so there may be nothing to continue. Write a task.</p>}
      {mode === "conversation"
        ? <div className="field"><label htmlFor={`kgs-note-${threadId}`}>Anything to add? <span className="help">(optional, one line)</span></label>
          <input id={`kgs-note-${threadId}`} type="text" aria-label="Anything to add?" value={note} disabled={busy} maxLength={300} onChange={e => { setNote(e.target.value.replace(/[\r\n]/g, " ")); edit(); }} /></div>
        : <div className="field"><label htmlFor={`kgs-task-${threadId}`}>Task and completion criteria</label>
          <textarea id={`kgs-task-${threadId}`} aria-label="Task and completion criteria" value={task} disabled={busy} maxLength={262144} rows={3} onChange={e => { setTask(e.target.value); edit(); }} /></div>}
    </fieldset>
    <details>
      <summary>Options ({optionSummary})</summary>
      <fieldset>
        <legend>Options</legend>
        <label className="choice" data-checked={withGoal} data-disabled={busy}><input type="checkbox" aria-label="Record a goal for this task" aria-describedby={`kgs-h-goal-${threadId}`} checked={withGoal} disabled={busy} onChange={e => { setWithGoal(e.target.checked); edit(); }} />
          <span className="t">Record a goal for this task</span>
          <span className="help" id={`kgs-h-goal-${threadId}`}>{withGoal ? "Saves task privately and shares goal revisions." : "Copies task into wake instructions without a goal."}</span></label>
        {standardRoutes && <label className="choice" data-checked={followStandard} data-disabled={busy}><input type="checkbox" aria-label="Follow my standard route list for handoffs" aria-describedby={`kgs-h-std-${threadId}`} checked={followStandard} disabled={busy} onChange={e => { setFollowStandard(e.target.checked); edit(); }} />
          <span className="t">Follow my standard route list for handoffs</span>
          <span className="help" id={`kgs-h-std-${threadId}`}>Hands off on a rate limit to matching standard routes.</span></label>}
        {offer && !following && <label className="choice" data-checked={takeover === "on"} data-disabled={busy}><input type="checkbox" aria-label="Take over on a usage limit" aria-describedby={`kgs-h-take-${threadId}`} checked={takeover === "on"} disabled={busy} onChange={e => { setTakeover(e.target.checked ? "on" : "off"); edit(); }} />
          <span className="t">On a usage limit, start a new conversation on {offer.provider} ({offer.model}) that takes over</span>
          <span className="help" id={`kgs-h-take-${threadId}`}>Optional cross-provider takeover when this provider hits a limit.</span></label>}
      </fieldset>
    </details>
    {!result?.plan && <div className="actions">
      <div className="row"><button type="button" className="btn primary" disabled={busy || taskNeeded || !nameOk || continueMissing} aria-describedby={taskNeeded ? `kgs-why-${threadId}` : continueMissing ? `kgs-why-cont-${threadId}` : undefined} onClick={() => void act("preview")}>Preview setup</button></div>
      {continueMissing && <span id={`kgs-why-cont-${threadId}`} className="help">Choose a loop to continue.</span>}
      {!continueMissing && taskNeeded && <span id={`kgs-why-${threadId}`} className="help">Write the task to preview the setup.</span>}
    </div>}
    {result?.plan && <div ref={previewRef} tabIndex={-1} className="card" role="group" aria-label="Setup preview">
      <h4>Preview: nothing created yet</h4>
      <dl>
        {result.plan.succeeds && <><dt>Continues</dt><dd>{result.plan.succeeds.displayName ? `${result.plan.succeeds.displayName} (${result.plan.succeeds.loop})` : result.plan.succeeds.loop} — archived automatically after first completed check</dd></>}
        <dt>Setup</dt><dd>{previewSetupLabel}</dd>
        <dt>Loop</dt><dd>{result.plan.loop}</dd>
        <dt>Handoff</dt><dd>{handoffSummary}</dd>
      </dl>
      {result.ticket && <div className="actions">
        <div className="row"><button type="button" className="btn primary" disabled={busy} onClick={() => void act("create")}>Create setup</button></div>
        <span className="help">Create setup saves the configuration and leaves supervision switched off.</span>
      </div>}
      <details>
        <summary>Details: paths, task &amp; limits</summary>
        {result.task !== undefined && <div aria-label="Task to send"><span className="label">Task that will be sent</span><pre>{result.task}</pre></div>}
        <dl>
          <dt>Provider</dt><dd>{result.plan.provider}</dd>
          {(result.threadMeta ?? threadMeta)?.model && <><dt>Model</dt><dd>{(result.threadMeta ?? threadMeta)?.modelLabel ? `${(result.threadMeta ?? threadMeta)?.modelLabel} (${(result.threadMeta ?? threadMeta)?.model})` : (result.threadMeta ?? threadMeta)?.model}</dd></>}
          <dt>Project</dt><dd>{result.plan.project}</dd>
          <dt>Configuration</dt><dd>{result.plan.configPath}</dd>
          <dt>Task</dt><dd>{result.plan.withGoal ? "Recorded as goal revision 1 referencing a private task snapshot." : "Copied into the wake instructions; no goal record."}</dd>
          <dt>Checks</dt><dd>Every {result.plan.limits.interval}. Idle threads can wake on a check; stall nudges are spaced at least {result.plan.limits.wakeEvery} apart.</dd>
          <dt>Limits</dt><dd>Stall after {result.plan.limits.stallAfter}; {result.plan.limits.maxNudges} nudges; {result.plan.limits.maxReprompts} error re-prompts with {result.plan.limits.backoff} backoff.</dd>
          {result.plan.routes && <><dt>Routes</dt><dd><div aria-label="Routes">
            <span>Follows your standard route list. Handoff on a rate limit is turned on. On a rate limit the loop hands off to, in order:</span>
            <ul>{result.plan.routes.apply.map(r => <li key={r.id}>{r.id}: {r.model || "model unchanged"}, usage limit {r.group}</li>)}</ul>
          </div></dd></>}
          {result.plan.takeover && <><dt>Takeover</dt><dd><div aria-label="Takeover">
            <span>Takeover: on (uncheck it in Options and preview again to turn it off).</span> <span>{result.plan.takeover.text}</span>
          </div></dd></>}
        </dl>
      </details>
    </div>}
    {busy && <p role="status" className="help">{busyText}</p>}
    {message}
  </section>;
}
