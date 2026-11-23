// Shows keepgoing's state beside each supervised thread: a row in the thread
// context card when bb shows the card, and a segment in the context banner
// above the composer when it shows the banner. On the New thread screen it
// adds the Add to Looptender switch and its banner to the composer.
import {
  definePluginApp,
  experimental_THREAD_INFO_ROW_GROUPS as ROW_GROUPS,
  experimental_ThreadContextCardRow as ContextRow,
  experimental_useThreadContextKind as useContextKind,
  useBbNavigate,
  useComposer,
  useRealtime,
  useRpc,
} from "@get-bb/plugin-sdk/app";
import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import type { LoopView, rpcContract } from "./server";
import { diagnosticLines, dateLabel, buildSummary, reasonLines, boundedTitle, recordsUnreadable, freshnessToShow, lastLabel, archivedLine } from "./status-details";
import { lineageDue, type LineageEntry } from "./lineage";
import { SetupPanel } from "./setup-panel";
import { SetupStyles } from "./setup-style";
import { AttachPanel } from "./attach-panel";
import { TransferPanel } from "./transfer-panel";
import { KeepGoingBanner, KeepGoingSwitch, turnOn } from "./new-thread-control";
import { NewThreadOutcome, outcomeLabel, useOutcome } from "./new-thread-outcome";

type Rpc = ReturnType<typeof useRpc<typeof rpcContract>>;

// One cached lookup per thread, shared by the card row, the banner segment,
// and its visibility check, so a thread is fetched once per refresh.
const STALE_MS = 25_000;
const cache = new Map<string, { loops: LoopView[]; at: number; inflight: boolean; failed: boolean; references: Map<string, number> }>();
const listeners = new Set<() => void>();
const notify = () => {
  for (const l of listeners) l();
};
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

function fetchLoops(rpc: Rpc, threadId: string, force = false) {
  const entry = cache.get(threadId);
  if (entry?.inflight || (!force && entry && Date.now() - entry.at < STALE_MS)) return;
  cache.set(threadId, { loops: entry?.loops ?? [], at: entry?.at ?? 0, inflight: true, failed: entry?.failed ?? false, references: entry?.references ?? new Map() });
  rpc
    .call("forThread", { threadId })
    .then((result) => {
      const references = new Map(entry?.references);
      for (const loop of result.loops) if (!references.has(loopKey(loop))) references.set(loopKey(loop), references.size + 1);
      cache.set(threadId, { loops: result.loops, at: Date.now(), inflight: false, failed: result.errors.length > 0, references });
    })
    .catch(() => cache.set(threadId, { loops: entry?.loops ?? [], at: Date.now(), inflight: false, failed: true, references: entry?.references ?? new Map() }))
    .finally(notify);
}

// Whether a thread is a child, and which ancestor's loop covers it. Cached per
// thread, refreshed after a few minutes and whenever the thread's loop list
// changes; a failed lookup is retried with backoff and meanwhile reads as "not
// a child" (setup is offered as before).
const lineageCache = new Map<string, LineageEntry>();
function useLineage(threadId: string, loopsKey = "") {
  const rpc = useRpc<typeof rpcContract>();
  const [, bump] = useState(0);
  useEffect(() => {
    const check = (force: boolean) => {
      const entry = lineageCache.get(threadId);
      if (!force && !lineageDue(entry, Date.now())) return;
      if (entry?.inflight) return;
      lineageCache.set(threadId, { ...(entry ?? { at: 0, failures: 0 }), inflight: true, key: loopsKey });
      rpc.call("lineage", { threadId })
        .then(value => lineageCache.set(threadId, { value, at: Date.now(), failures: 0, inflight: false, key: loopsKey }))
        .catch(() => lineageCache.set(threadId, { value: entry?.value, at: Date.now(), failures: (entry?.failures ?? 0) + 1, inflight: false, key: loopsKey }))
        .finally(() => bump(n => n + 1));
    };
    const known = lineageCache.get(threadId);
    check(!!known && known.key !== loopsKey);
    const timer = setInterval(() => check(false), 5_000);
    return () => clearInterval(timer);
  }, [rpc, threadId, loopsKey]);
  const e = lineageCache.get(threadId);
  // While the first answer is pending nothing that depends on it is drawn, so the entry button never flashes in and out.
  return e?.value ? { ...e.value, pending: false } : { child: false as boolean, coveredBy: undefined, pending: !e || (e.inflight && e.failures === 0) };
}

function useLoops(threadId: string) {
  const rpc = useRpc<typeof rpcContract>();
  const entry = useSyncExternalStore(subscribe, () => cache.get(threadId));
  const refresh = useCallback(() => fetchLoops(rpc, threadId, true), [rpc, threadId]);
  useEffect(() => {
    fetchLoops(rpc, threadId);
    const timer = setInterval(() => fetchLoops(rpc, threadId), 30_000);
    return () => clearInterval(timer);
  }, [rpc, threadId]);
  useRealtime("changed", refresh);
  return { loops: entry?.loops ?? [], references: entry?.references ?? new Map<string, number>(), refresh, failed: entry?.failed ?? false, loaded: !!entry?.at };
}

// The running build after the state word, so a narrow card never cuts the
// state off. A running build that differs from the installed one says so.
function buildSuffix(loop: LoopView): string {
  const b = buildSummary(loop.details?.runtime);
  // Short on purpose: the chip sits beside other chips. The installed build and what to do are in the Build line below.
  return !b.running ? "" : b.differs ? ` ${b.running} · build differs` : ` ${b.running}`;
}

function label(loop: LoopView): string {
  const base = stateLabel(loop);
  return base === `looptender: ${loop.controlState}` || base === "looptender: enabled" ? base + buildSuffix(loop) : base;
}

function stateLabel(loop: LoopView): string {
  // status exits 1 and says which record it could not read; the control state beside it cannot be trusted.
  if (recordsUnreadable(loop.details)) return "looptender: records unreadable";
  if (loop.details?.archived) return "looptender: archived";
  if (loop.imported && loop.controlState === "enabled" && ((loop.details?.failedChecks ?? 0) > 0 || ["failed", "error"].includes(loop.details?.lastCheck?.outcome ?? ""))) return "looptender: checks failing";
  if (loop.imported && loop.controlState === "enabled" && !loop.details?.lastCheck) return "looptender: no check recorded";
  if (loop.controlState === "enabled" && loop.details?.schedule?.overdue) return "looptender: checks have stopped";
  if (loop.details?.moved?.held && loop.controlState === "awaiting user") return "looptender: moved, resume to start";
  if (loop.controlState === "enabled") return loop.supervising ? "looptender: enabled" : `looptender: watching ${loop.details?.targetThread ?? loop.target}`;
  return `looptender: ${loop.controlState}`;
}

const sameBuild = (a: string, b: string) => a.startsWith(b) || b.startsWith(a);

function describe(loop: LoopView): string[] {
  return [
    `Loop ${loop.loop}`,
    loop.referenceSource === "plugin_setting" ? "Reference: plugin setting; CLI menu registration checked separately" : loop.referenceSource === "registry_attachment" ? "Reference: explicitly attached from CLI registry" : loop.referenceSource === "plugin_setup" ? "Reference: saved BB setup" : "Reference source: unavailable",
    loop.publication?.state === "registered" ? "Last explicit CLI menu publication: registered; current health checked separately" : loop.referenceSource === "plugin_setup" ? "CLI menu publication: unverified; use explicit registry reconcile on the host" : null,
    // One Build line: status's, with looptender's freshness detail joined to it.
    ...diagnosticLines(loop.details, loop.target, loop.supervising).map(l => l.startsWith("Build: ") && freshnessToShow(loop.details)?.detail ? `${l} · ${freshnessToShow(loop.details)!.detail}` : l),
    // Which build is which: the supervisor's (above) and the plugin's own code.
    ...(loop.pluginBuild && loop.details?.runtime?.installedBuild && !sameBuild(loop.pluginBuild, loop.details.runtime.installedBuild) ? [`Plugin build: ${loop.pluginBuild} (the code drawing this card); the builds above are each supervisor's, read from looptender status`] : []),
    // The word bb looptender status prints as last=, beside the recorded action it does not mean.
    `Last check state: ${lastLabel(loop.lastState, loop.details)} (the listing's last= field)`,
    loop.note ? `Note: ${loop.note}` : null,
    loop.details?.schedule?.overdue ? `Needs you: ${loop.details.schedule.message ?? "no check has run for longer than usual"}` : null,
    dateLabel(loop.lastWakeAt) !== "unavailable" ? `Last sent ${dateLabel(loop.lastWakeAt)}` : "Last sent: unavailable",
    loop.handoffs > 0 ? `${loop.handoffs} handoffs` : null,
    loop.fallbacks ? `Fallbacks: ${loop.fallbacks}` : null,
  ].filter((line): line is string => line !== null);
}

// A target a finger or pointer can hit (at least 28 px high) and, through FocusStyle, a ring when reached by keyboard.
const buttonStyle = {
  background: "none",
  color: "inherit",
  font: "inherit",
  fontSize: 12,
  lineHeight: "18px",
  minHeight: 28,
  padding: "3px 10px",
  border: "1px solid currentColor",
  borderRadius: 6,
  opacity: 0.85,
  cursor: "pointer",
} as const;

// A loop a person can resume: held in any way but superseded, and its records readable.
function needsResume(loop: LoopView): boolean {
  if (loop.controlState === "enabled" || loop.controlState === "superseded" || recordsUnreadable(loop.details) || loop.details?.archived) return false;
  return !(loop.managedId && ["disabled", "move requires inspection"].includes(loop.controlState));
}
const RESUME_SHARED = "A person resumes a held loop from an ordinary terminal, or from the looptender panel started there. Each held loop's own command is under its name.";

/** The focus ring for this plugin's buttons; inline styles cannot say :focus-visible. */
function FocusStyle() {
  return <style>{"button[data-kg]:focus-visible, summary[data-kg]:focus-visible { outline: 2px solid #1a73e8; outline-offset: 2px; }"}</style>;
}
const kg = { "data-kg": "" } as const;

function ToggleButton({ loop, threadId, onDone, compactResume = false }: { loop: LoopView; threadId: string; onDone: () => void; compactResume?: boolean }) {
  const rpc = useRpc<typeof rpcContract>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState(false);
  const paused = loop.controlState !== "enabled";
  if (loop.managedId && ["disabled", "move requires inspection"].includes(loop.controlState)) return null;
  // Pause, stop and resume cannot be saved while the control record cannot be read.
  if (recordsUnreadable(loop.details)) return null;
  // An enabled loop whose current target is another thread: this card belongs
  // to its primary, so pausing it is allowed, but only after it is said which
  // loop and which thread are meant.
  const watching = !paused && !loop.supervising;
  const act = () => {
    setBusy(true);
    setError("");
    // A key left undefined is not JSON; send managedId only when there is one.
    rpc
      .call("control", { configPath: loop.configPath, action: "pause", ...(loop.managedId ? { managedId: loop.managedId } : {}), expectedThreadId: threadId })
      .then(result => { if (!result.ok) setError(result.message); })
      .catch(() => setError("Control could not be confirmed. Refresh status before retrying."))
      .finally(() => {
        setBusy(false);
        setConfirming(false);
        onDone();
      });
  };
  return <>
    {/* Resume is a person's, from the panel or a terminal: the plugin
        cannot tell a click from the supervised session. */}
    {paused
      ? <ResumeNote loop={loop} compact={compactResume} />
      : watching && confirming
        ? <span role="group" aria-label={`Confirm pausing ${loop.loop}`}>
            <span>Pause loop {loop.loop}? {loop.details?.targetThread ? `It supervises ${loop.details.targetThread}, not this thread.` : `Its current target is ${loop.details?.targetAdapter ?? loop.target}, not this thread.`}</span>{" "}
            <button type="button" {...kg} disabled={busy} onClick={act} style={buttonStyle}>Pause loop {loop.loop}</button>{" "}
            <button type="button" {...kg} disabled={busy} onClick={() => setConfirming(false)} style={buttonStyle}>Cancel</button>
          </span>
        : <button type="button" {...kg} aria-label={`${watching ? "Pause…" : "Pause"} ${loop.loop}`} disabled={busy} onClick={watching ? () => setConfirming(true) : act} style={buttonStyle}>{watching ? "Pause…" : "Pause"}</button>}
    {error && <span role="alert">{error}</span>}
  </>;
}

// Where a held loop is resumed, in the words `looptender status` uses for the
// same state. A superseded loop is never resumed: its successor continues.
function ResumeNote({ loop, compact }: { loop: LoopView; compact: boolean }) {
  // An archived loop is not resumed: looptender says so, and a person unarchives it first.
  if (loop.details?.archived) return <span>{archivedLine(loop.details.archived)}.</span>;
  if (loop.controlState === "superseded") {
    const successor = loop.details?.supersededBy;
    return <span>Resume is refused for a superseded loop; its successor continues the work{successor ? ` (${successor})` : ""}.</span>;
  }
  // With several held loops the sentence is said once for the card; each loop keeps only its command.
  if (compact) return <details><summary>Command to resume (run in a terminal)</summary><code>looptender resume -c {JSON.stringify(loop.configPath)}</code></details>;
  return <span>A person resumes this from an ordinary terminal: <code>looptender resume -c {JSON.stringify(loop.configPath)}</code>, or the looptender panel started there.</span>;
}

function ThreadSetup({ threadId, onChanged, hideStart = false }: { threadId: string; onChanged(): void; hideStart?: boolean }) {
  const rpc = useRpc<typeof rpcContract>();
  const call = useCallback((input: Parameters<typeof rpc.call<"setup">>[1]) => rpc.call("setup", input), [rpc]);
  const [generation, setGeneration] = useState(0);
  const attach = useCallback(async (input: Parameters<typeof rpc.call<"attachment">>[1]) => {
    const result = await rpc.call("attachment", input);
    if (result.attached) setGeneration(n => n + 1);
    return result;
  }, [rpc]);
  const transfer = useCallback(async (input: Parameters<typeof rpc.call<"transfer">>[1]) => {
    const result = await rpc.call("transfer", input);
    if (result.moved) setGeneration(n => n + 1);
    return result;
  }, [rpc]);
  // One primary flow first. Attaching and moving are for a loop that already
  // exists, so they sit behind a collapsed section of their own.
  return <><SetupPanel key={`${threadId}-${generation}`} threadId={threadId} call={call} onChanged={onChanged} hideStart={hideStart} />
    <details className="kgs-more"><SetupStyles /><summary>Already have a loop?</summary>
      <AttachPanel key={`attach-${threadId}`} threadId={threadId} call={attach} onChanged={onChanged} />
      <TransferPanel key={`move-${threadId}`} threadId={threadId} call={transfer} onChanged={onChanged} />
    </details></>;
}

function StatusWarning({ refresh }: { refresh(): void }) {
  return <div role="alert">Status could not be verified for every registered loop. Controls and setup are hidden until refreshed. <button type="button" {...kg} style={buttonStyle} onClick={refresh}>Refresh status</button></div>;
}

// What a thread created with Add to Looptender on shows about that request.
function useThreadOutcome(threadId: string, loops: LoopView[]) {
  const rpc = useRpc<typeof rpcContract>();
  const call = useCallback((input: Parameters<typeof rpc.call<"newThread">>[1]) => rpc.call("newThread", input), [rpc]);
  const start = useCallback((input: { action: "start"; threadId: string }) => rpc.call("setup", input), [rpc]);
  const { outcome } = useOutcome(threadId, call, JSON.stringify(loops.map(l => [l.configPath, l.managedId, l.controlState, l.checkedAt])));
  // New-thread controls belong only to one unambiguous saved setup for this
  // thread, never the first reference or a loop inferred from its display name.
  const setups = loops.filter(l => l.managedThreadId === threadId && l.managedId && !l.imported && (!l.referenceSource || l.referenceSource === "plugin_setup"));
  const loop = setups.length === 1 ? setups[0] : undefined;
  // Pause and stop go to the loop's own recorded configuration, as the ordinary controls do.
  const control = useCallback((action: "pause" | "stop") => loop
    ? rpc.call("control", { configPath: loop.configPath, action, managedId: loop.managedId, expectedThreadId: threadId })
    : Promise.resolve({ ok: false, message: "Status is not loaded yet." }), [rpc, loop, threadId]);
  return { outcome, start, control, loop };
}

const READY_NOT_STARTED = "looptender: ready, not started";
// A thread started from the New thread screen carries its own Pause and Stop.
const KEEPING_GOING = "looptender: keeping this going";

// Stable record/config identity keeps pending controls attached to the same loop
// when a status refresh changes order or removes another reference.
function loopKey(loop: LoopView) { return JSON.stringify([loop.configPath, loop.managedId ?? null]); }

function referenceName(loop: LoopView, loops: LoopView[], references: Map<string, number>) {
  return loops.filter(l => l.loop === loop.loop).length > 1 ? `${loop.loop} · reference ${references.get(loopKey(loop))}` : loop.loop;
}

function ReferenceDetails({ loop, name }: { loop: LoopView; name: string }) {
  return name === loop.loop ? null : <details><summary>Configuration reference</summary><code>{loop.configPath}</code><div>Reference numbers identify rows in this plugin session; they are not loop IDs.</div></details>;
}

// What the reasons already say is not said again in the details.
function detailLines(l: LoopView, reasons: string[]): string[] {
  const said = (prefix: string, reason: string) => reasons.some(r => r.startsWith(reason)) && prefix;
  const dropped = ["Note: ", "Needs you: ", "Disk: ", "Send record: ", "Unreadable record: ", said("Deadline: ", "Deadline reached"), said("Sends: ", "Send limit reached")];
  return describe(l).slice(1).filter(line => !dropped.some(prefix => prefix && line.startsWith(prefix)));
}

// The build line: how this loop's supervisor compares with the installed build, visible, never a tooltip.
function BuildFreshness({ loop }: { loop: LoopView }) {
  const f = freshnessToShow(loop.details);
  if (!f) return null;
  // looptender's own words: the chip, and either the command that is true for that supervisor or what to do in words.
  return <div aria-label="Build" style={{ margin: 0, padding: "0 12px 4px 12px", fontSize: 12, overflowWrap: "anywhere" }}>
    <span>{f.text}</span>{f.command && <> — <code>{f.command}</code></>}{!f.command && f.action && <> — {f.action}</>}
  </div>;
}

const plainList = { margin: 0, padding: 0, listStyle: "none" } as const;

function Reasons({ loop }: { loop: LoopView }) {
  const lines = reasonLines(loop.controlState, loop.note, loop.details);
  return lines.length ? <ul aria-label="Why" style={{ margin: 0, padding: "0 12px 4px 28px", fontSize: 12, opacity: 0.85, overflowWrap: "anywhere" }}>{lines.map((l, i) => <li key={i}>{l}</li>)}</ul> : null;
}

function CardLoop({ loop, name, threadId, refresh, failed, created, showName, compactResume }: { loop: LoopView; name: string; threadId: string; refresh(): void; failed: boolean; created: string | null; showName: boolean; compactResume: boolean }) {
  return <section aria-label={`Loop ${name}`} style={{ overflowWrap: "anywhere", minWidth: 0 }}>
    {showName && <strong style={{ fontSize: 12 }}>{name}</strong>}
    <ReferenceDetails loop={loop} name={name} />
    <ContextRow icon="Repeat" label={failed ? "looptender: status unavailable (previous snapshot)" : created ?? label(loop)}
      title={boundedTitle(describe(loop).join("\n"), 800)} trailing={loop.handoffs > 0 ? `${loop.handoffs} handoffs` : undefined}
      action={<>{!failed && created !== KEEPING_GOING && <ToggleButton loop={loop} threadId={threadId} onDone={refresh} compactResume={compactResume} />}</>} />
    {!failed && <BuildFreshness loop={loop} />}
    {!failed && <Reasons loop={loop} />}
    {/* Everything the tooltip holds, in the card itself: reachable by keyboard and touch. */}
    <details style={{ padding: "0 12px 6px", fontSize: 12 }}><summary {...kg}>Details</summary>
      <ul aria-label="Details" style={plainList}>{detailLines(loop, reasonLines(loop.controlState, loop.note, loop.details)).map((line, i) => <li key={i}>{line}</li>)}</ul>
    </details>
  </section>;
}

function CardRow({ threadId }: { threadId: string }) {
  const { loops, references, refresh, failed, loaded } = useLoops(threadId);
  const { outcome, start, control, loop } = useThreadOutcome(threadId, loops);
  const created = outcomeLabel(outcome, loop?.controlState, loop?.supervising);
  const [open, setOpen] = useState(false);
  const sharedResume = !failed && loops.filter(needsResume).length > 1;
  const lineage = useLineage(threadId, loops.map(l => `${loopKey(l)}:${l.controlState}`).join(","));
  const nav = useBbNavigate();
  // A lane is covered through its parent's loop, and a session has one loop: no
  // entry button, one quiet line, and setup kept in a collapsed "more" below.
  const quiet = loaded && !failed && !loops.length && lineage.child;
  const waitingOnLineage = loaded && !failed && !loops.length && lineage.pending;
  if (quiet) return <div>
    <SetupStyles />
    {lineage.coveredBy && <ContextRow icon="Repeat" label={<>Covered by <a href={`#thread-${lineage.coveredBy.threadId}`} onClick={e => { e.preventDefault(); nav.toThread(lineage.coveredBy!.threadId); }}>{lineage.coveredBy.loop}</a></>} title="This thread is covered through its parent thread's loop" />}
    <details className="kgs-more"><summary>Set up a loop for this thread anyway</summary>
      <ThreadSetup key={threadId} threadId={threadId} onChanged={refresh} hideStart={false} />
    </details>
  </div>;
  if (waitingOnLineage) return <div><SetupStyles /></div>;
  return <div>
    <SetupStyles />
    <FocusStyle />
    {failed && <StatusWarning refresh={refresh} />}
    {loaded && !failed && <NewThreadOutcome threadId={threadId} outcome={outcome} controlState={loop?.controlState} supervising={loop?.supervising} start={start} control={control} onChanged={refresh} showLabel={false} />}
    {sharedResume && <div style={{ padding: "0 12px", fontSize: 12 }}>{RESUME_SHARED}</div>}
    {!loaded ? <ContextRow icon="Repeat" label="looptender: checking status" /> : !loops.length ? <ContextRow icon="Repeat" label="looptender" action={!failed && <button type="button" className="kgs-chip" {...kg} style={buttonStyle} onClick={() => setOpen(!open)} aria-expanded={open}>Add to Looptender</button>} /> : loops.map(l => <CardLoop key={loopKey(l)} loop={l} name={referenceName(l, loops, references)} threadId={threadId} refresh={refresh} failed={failed} created={l === loop ? created : null} showName={loops.length > 1} compactResume={sharedResume} />)}
    {!failed && loops.some(l => l.managedId) && <button type="button" className="kgs-chip" {...kg} style={buttonStyle} onClick={() => setOpen(!open)} aria-expanded={open}>{loops.length > 1 ? "Thread setup" : "Setup"}</button>}
    {open && <div hidden={failed}><ThreadSetup key={threadId} threadId={threadId} onChanged={refresh} hideStart={created === READY_NOT_STARTED} /></div>}
  </div>;
}

// The collapsed banner's tooltip: each loop's state and its reasons, cut to a
// length a person reads. The expanded view holds the rest.
function summaryTitle(loops: LoopView[]): string {
  return boundedTitle(loops.map(l => [label(l), ...reasonLines(l.controlState, l.note, l.details)].join("\n")).join("\n\n"), 600);
}

function BannerSummary({ threadId, isExpanded, onToggle }: { threadId: string; isExpanded: boolean; onToggle(): void }) {
  const { loops, failed, loaded } = useLoops(threadId);
  const { outcome, loop } = useThreadOutcome(threadId, loops);
  const created = outcomeLabel(outcome, loop?.controlState, loop?.supervising);
  const only = loops.length === 1 ? loops[0] : undefined;
  const lineage = useLineage(threadId, loops.map(l => `${loopKey(l)}:${l.controlState}`).join(","));
  const noLoop = loaded && !failed && !loops.length;
  // Nothing while the lineage is pending; on a child, no "Add to Looptender" entry.
  if (noLoop && lineage.pending) return null;
  const childText = noLoop && lineage.child ? (lineage.coveredBy ? `Covered by ${lineage.coveredBy.loop}` : "looptender") : undefined;
  return <button type="button" onClick={onToggle} aria-expanded={isExpanded} title={failed ? "Status could not be refreshed. Open details to retry." : loops.length ? summaryTitle(loops) : childText ? "This thread is a child; setup is in the details" : "Set up a loop for this thread"}
    style={{ background: "none", border: 0, padding: 0, font: "inherit", color: "inherit", cursor: "pointer" }}>
    {failed ? "looptender: status unavailable" : !loaded ? "looptender: checking status" : loops.length > 1 ? `looptender: ${loops.length} matching loops` : created ?? (only ? label(only) : childText ?? "Add to Looptender")}
  </button>;
}


function BannerDetails({ threadId, isExpanded }: { threadId: string; isExpanded: boolean }) {
  const { loops, references, refresh, failed, loaded } = useLoops(threadId);
  const { outcome, start, control, loop } = useThreadOutcome(threadId, loops);
  const created = outcomeLabel(outcome, loop?.controlState, loop?.supervising);
  const lineage = useLineage(threadId, loops.map(l => `${loopKey(l)}:${l.controlState}`).join(","));
  const nav = useBbNavigate();
  if (!isExpanded) return null;
  const sharedResume = !failed && loops.filter(needsResume).length > 1;
  const childNoLoop = loaded && !failed && !loops.length && lineage.child;
  if (loaded && !failed && !loops.length && lineage.pending) return null;
  if (childNoLoop) return <>
    <SetupStyles />
    {lineage.coveredBy && <div style={{ padding: "4px 12px", fontSize: 12 }}>Covered by <a href={`#thread-${lineage.coveredBy.threadId}`} onClick={e => { e.preventDefault(); nav.toThread(lineage.coveredBy!.threadId); }}>{lineage.coveredBy.loop}</a>, a loop on a parent thread.</div>}
    <details className="kgs-more"><summary>Set up a loop for this thread anyway</summary>
      <ThreadSetup key={threadId} threadId={threadId} onChanged={refresh} hideStart={false} />
    </details>
  </>;
  return <>
    <FocusStyle />
    {failed && <StatusWarning refresh={refresh} />}
    {loaded && !failed && <NewThreadOutcome threadId={threadId} outcome={outcome} controlState={loop?.controlState} supervising={loop?.supervising} start={start} control={control} onChanged={refresh} showLabel={false} />}
    {sharedResume && <div style={{ padding: "4px 12px", fontSize: 12 }}>{RESUME_SHARED}</div>}
    {loops.map(l => {
      const name = referenceName(l, loops, references);
      const reasons = reasonLines(l.controlState, l.note, l.details);
      const rest = detailLines(l, reasons);
      return <section key={loopKey(l)} aria-label={`Loop ${name}`} style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap", padding: "4px 12px 6px", fontSize: 12, opacity: 0.8, overflowWrap: "anywhere", minWidth: 0 }}>
        <div style={{ minWidth: 0, flex: "1 1 220px" }}>
          {failed && <div>Previous snapshot</div>}
          <div><strong>Loop {name}</strong>: {l === loop ? created ?? label(l) : label(l)}</div>
          <ReferenceDetails loop={l} name={name} />
          <BuildFreshness loop={l} />
          {reasons.length > 0 && <ul aria-label="Why" style={plainList}>{reasons.map((line, i) => <li key={i}>{line}</li>)}</ul>}
          <ul aria-label="Details" style={plainList}>{rest.map((line, i) => <li key={i}>{line}</li>)}</ul>
        </div>
        {!failed && !(l === loop && created === KEEPING_GOING) && <ToggleButton loop={l} threadId={threadId} onDone={refresh} compactResume={sharedResume} />}
      </section>;
    })}
    {loaded && (!loops.length || loops.some(l => l.managedId)) && <div hidden={failed}><ThreadSetup key={threadId} threadId={threadId} onChanged={refresh} hideStart={created === READY_NOT_STARTED} /></div>}
  </>;
}

function ComposerSwitch() {
  const rpc = useRpc<typeof rpcContract>();
  const call = useCallback((input: Parameters<typeof rpc.call<"newThread">>[1]) => rpc.call("newThread", input), [rpc]);
  return <KeepGoingSwitch composer={useComposer()} call={call} />;
}

function ComposerBanner() {
  const rpc = useRpc<typeof rpcContract>();
  const call = useCallback((input: Parameters<typeof rpc.call<"newThread">>[1]) => rpc.call("newThread", input), [rpc]);
  return <KeepGoingBanner composer={useComposer()} call={call} />;
}

export default definePluginApp((app) => {
  // New thread screen only. The banner draws its own frame, so nothing shows
  // while the switch is off.
  app.composer.customize({
    id: "keepgoing-new-thread",
    scopes: ["new-thread"],
    actions: [{ id: "keep-going", component: ComposerSwitch }],
    banners: [{ id: "keep-going", chrome: "bare", component: ComposerBanner }],
    // The action row is not drawn in the compact layout; this row reaches the
    // same switch from the menu beside Send.
    sendMenu: [{
      id: "keep-going",
      label: "Add to Looptender…",
      icon: "Repeat",
      description: "Turns on Add to Looptender for this new thread without sending. A plain Send still sends with no looptender; the banner's send button then sends your message and adds the thread to looptender, as your setting says.",
      run: ({ composer }) => { if (composer.scope.kind === "new-thread") turnOn(composer.key); },
    }],
  });
  app.slots.experimental_threadContextCardRow({
    id: "keepgoing",
    group: ROW_GROUPS.lifecycle,
    order: 50,
    component: CardRow,
  });
  app.slots.experimental_threadContextSegment({
    id: "keepgoing",
    title: "looptender",
    group: "6_keepgoing",
    summary: BannerSummary,
    details: BannerDetails,
    // Offer setup on unsupervised threads using whichever context UI is active.
    useVisible: () => {
      const kind = useContextKind();
      return kind === "banner";
    },
  });
});
