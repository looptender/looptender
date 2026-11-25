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
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { LoopView, rpcContract } from "./server";
import { diagnosticLines, dateLabel, buildSummary, reasonLines, boundedTitle, recordsUnreadable, freshnessToShow, lastLabel, archivedLine, firstSentenceOf, observedRoleLabel } from "./status-details";
import { lineageDue, type LineageEntry } from "./lineage";
import { heldSentence, isWait, isObserved, waitFor, resumeCommand, PhoneResumeLink } from "./held-card";
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
const START_CHECKS_MAX_MS = 120_000;
type StartingEntry = { baselineCheckAt: string | null; refreshes: number; startedAt: number };
const cache = new Map<string, { loops: LoopView[]; at: number; inflight: boolean; failed: boolean; references: Map<string, number> }>();
const startingChecks = new Map<string, Map<string, StartingEntry>>();
const listeners = new Set<() => void>();
const notify = () => {
  for (const l of listeners) l();
};
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

function checkMarker(loop: LoopView): string | null {
  return loop.details?.lastCheck?.at ?? loop.checkedAt ?? null;
}

function isOverdueEnabledLoop(loop: LoopView): boolean {
  return (
    !loop.imported &&
    loop.controlState === "enabled" &&
    !recordsUnreadable(loop.details) &&
    !loop.details?.archived &&
    loop.details?.schedule?.overdue === true
  );
}

function markStartingChecks(threadId: string, loop: LoopView) {
  const next = new Map(startingChecks.get(threadId));
  next.set(loopKey(loop), { baselineCheckAt: checkMarker(loop), refreshes: 0, startedAt: Date.now() });
  startingChecks.set(threadId, next);
  notify();
}

function clearStartingChecks(threadId: string, key: string) {
  const current = startingChecks.get(threadId);
  if (!current?.has(key)) return;
  const next = new Map(current);
  next.delete(key);
  if (next.size === 0) startingChecks.delete(threadId);
  else startingChecks.set(threadId, next);
}

function reconcileStartingChecks(threadId: string, loops: LoopView[], failed: boolean) {
  const current = startingChecks.get(threadId);
  if (!current || current.size === 0) return;
  if (failed) {
    startingChecks.delete(threadId);
    return;
  }
  const byKey = new Map(loops.map(l => [loopKey(l), l]));
  const next = new Map(current);
  const now = Date.now();
  let changed = false;
  for (const [key, entry] of current.entries()) {
    const loop = byKey.get(key);
    if (!loop || !isOverdueEnabledLoop(loop) || now - entry.startedAt >= START_CHECKS_MAX_MS) {
      next.delete(key);
      changed = true;
      continue;
    }
    const marker = checkMarker(loop);
    if (marker !== null && marker !== entry.baselineCheckAt) {
      next.delete(key);
      changed = true;
      continue;
    }
    const refreshes = entry.refreshes + 1;
    // Keep the transitional label through the immediate refresh (#1) while the
    // service starts; clear on a later refresh if the daemon still isn't responsive.
    if (refreshes >= 2 && loop.details?.daemon?.state !== "responsive") {
      next.delete(key);
    } else {
      next.set(key, { ...entry, refreshes });
    }
    changed = true;
  }
  if (!changed) return;
  if (next.size === 0) startingChecks.delete(threadId);
  else startingChecks.set(threadId, next);
}

function fetchLoops(rpc: Rpc, threadId: string, force = false) {
  const entry = cache.get(threadId);
  if (entry?.inflight || (!force && entry && Date.now() - entry.at < STALE_MS)) return;
  cache.set(threadId, { loops: entry?.loops ?? [], at: entry?.at ?? 0, inflight: true, failed: entry?.failed ?? false, references: entry?.references ?? new Map() });
  rpc
    .call("forThread", { threadId })
    .then((result) => {
      const references = new Map(entry?.references);
      for (const loop of result.loops) if (!references.has(loopKey(loop))) references.set(loopKey(loop), references.size + 1);
      const failed = result.errors.length > 0;
      cache.set(threadId, { loops: result.loops, at: Date.now(), inflight: false, failed, references });
      reconcileStartingChecks(threadId, result.loops, failed);
    })
    .catch(() => {
      cache.set(threadId, { loops: entry?.loops ?? [], at: Date.now(), inflight: false, failed: true, references: entry?.references ?? new Map() });
      reconcileStartingChecks(threadId, [], true);
    })
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
  const starting = useSyncExternalStore(subscribe, () => startingChecks.get(threadId));
  const refresh = useCallback(() => fetchLoops(rpc, threadId, true), [rpc, threadId]);
  const hasStarting = !!starting?.size;
  useEffect(() => {
    fetchLoops(rpc, threadId);
    const timer = setInterval(() => fetchLoops(rpc, threadId), 30_000);
    return () => clearInterval(timer);
  }, [rpc, threadId]);
  useEffect(() => {
    if (!hasStarting) return;
    const timer = setInterval(() => {
      const current = cache.get(threadId);
      if (!current?.at || Date.now() - current.at >= 3_500) fetchLoops(rpc, threadId, true);
    }, 4_000);
    return () => clearInterval(timer);
  }, [rpc, threadId, hasStarting]);
  useRealtime("changed", refresh);
  return { loops: entry?.loops ?? [], references: entry?.references ?? new Map<string, number>(), starting, refresh, failed: entry?.failed ?? false, loaded: !!entry?.at };
}

// The running build after the state word, so a narrow card never cuts the
// state off. A running build that differs from the installed one says so.
function buildSuffix(loop: LoopView): string {
  const b = buildSummary(loop.details?.runtime);
  // Short on purpose: the chip sits beside other chips. The installed build and what to do are in the Build line below.
  return !b.running ? "" : b.differs ? ` ${b.running} · build differs` : ` ${b.running}`;
}

function label(loop: LoopView, starting = false): string {
  const base = stateLabel(loop, starting);
  return base === `looptender: ${loop.controlState}` || base === "looptender: enabled" ? base + buildSuffix(loop) : base;
}

function stateLabel(loop: LoopView, starting = false): string {
  // status exits 1 and says which record it could not read; the control state beside it cannot be trusted.
  if (recordsUnreadable(loop.details)) return "looptender: records unreadable";
  if (loop.details?.archived) return loop.details.archived.unreadable || !loop.details.archived.at ? "looptender: archive record unreadable" : "looptender: archived";
  if (loop.imported && loop.controlState === "enabled" && ((loop.details?.failedChecks ?? 0) > 0 || ["failed", "error"].includes(loop.details?.lastCheck?.outcome ?? ""))) return "looptender: checks failing";
  if (loop.imported && loop.controlState === "enabled" && !loop.details?.lastCheck) return "looptender: no check recorded";
  if (loop.controlState === "enabled" && loop.details?.schedule?.overdue) return starting && !loop.imported ? "looptender: starting checks" : "looptender: checks have stopped";
  if (isWait(loop)) return `looptender: waiting for ${waitFor(loop.details!.wait!.until)}`;
  if (loop.details?.moved?.held && loop.controlState === "awaiting user") return "looptender: moved, resume to start";
  if (loop.controlState === "enabled") {
    if (isObserved(loop)) return loop.details?.observedWakes ? "looptender: observed (idle wakes)" : "looptender: observed";
    return loop.supervising ? "looptender: enabled" : `looptender: watching ${loop.details?.targetThread ?? loop.target}`;
  }
  return `looptender: ${loop.controlState}`;
}

const sameBuild = (a: string, b: string) => a.startsWith(b) || b.startsWith(a);

function describe(loop: LoopView): string[] {
  return [
    `Loop ${nameOf(loop)}${loop.displayName ? ` (${loop.loop})` : ""}`,
    loop.referenceSource === "plugin_setting" ? "Reference: plugin setting; CLI menu registration checked separately" : loop.referenceSource === "registry_attachment" ? "Reference: explicitly attached from CLI registry" : loop.referenceSource === "plugin_setup" ? "Reference: saved BB setup" : "Reference source: unavailable",
    loop.publication?.state === "registered" ? "Last explicit CLI menu publication: registered; current health checked separately" : loop.referenceSource === "plugin_setup" ? "CLI menu publication: unverified; use explicit registry reconcile on the host" : null,
    // One Build line: status's, with looptender's freshness detail joined to it.
    ...diagnosticLines(loop.details, loop.target, loop.supervising).map(l => l.startsWith("Build: ") && freshnessToShow(loop.details)?.detail ? `${l} · ${freshnessToShow(loop.details)!.detail}` : l),
    // Which build is which: the supervisor's (above) and the plugin's own code.
    ...(loop.pluginBuild && loop.details?.runtime?.installedBuild && !sameBuild(loop.pluginBuild, loop.details.runtime.installedBuild) ? [`Plugin build: ${loop.pluginBuild} (the code drawing this card); the builds above are each supervisor's, read from looptender status`] : []),
    // The word bb looptender status prints as last=, beside the recorded action it does not mean.
    `Last check state: ${lastLabel(loop.lastState, loop.details)} (the word looptender status lists after last=)`,
    freshnessToShow(loop.details)?.action ? `Build note: ${freshnessToShow(loop.details)!.action}` : null,
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

const actionsStyle = { display: "flex", gap: 8, flexWrap: "wrap" } as const;

// A loop a person can resume: held in any way but superseded, and its records readable.
function needsResume(loop: LoopView): boolean {
  if (loop.controlState === "enabled" || loop.controlState === "superseded" || recordsUnreadable(loop.details) || loop.details?.archived) return false;
  return !(loop.managedId && ["disabled", "move requires inspection"].includes(loop.controlState));
}

/** The focus ring for this plugin's buttons, and the phone rules inline styles cannot say: a 44 px touch target
 *  and readable text wherever the pointer is a finger or the screen is narrow. */
const PHONE = "(max-width: 640px), (pointer: coarse)";
const STYLE = [
  "button[data-kg]:focus-visible, summary[data-kg]:focus-visible { outline: 2px solid #1a73e8; outline-offset: 2px; }",
  `@media ${PHONE} {`,
  "  button[data-kg]:not(.kgs-chip) { min-height: 44px !important; min-width: 44px; box-sizing: border-box; display: inline-flex !important; align-items: center; font-size: 14px !important; line-height: 20px !important; padding: 0 14px !important; }",
  "  summary[data-kg] { display: list-item !important; min-height: 44px; box-sizing: border-box; padding: 12px 0; font-size: 14px !important; line-height: 20px !important; cursor: pointer; }",
  "  button[data-kg=chip] { padding: 0 0 0 8px !important; min-width: 0; }",
  "  .kg-actions button[data-kg] { flex: 1 1 140px; justify-content: center; }",
  "  .kg-t { font-size: 14px !important; line-height: 20px !important; }",
  "}",
].join("\n");
function FocusStyle() {
  return <style>{STYLE}</style>;
}
/** True where the details open closed: a phone-sized or touch screen. Unknown (a test DOM) reads as wide. */
const isPhone = () => typeof matchMedia === "function" && matchMedia(PHONE).matches;
const kg = { "data-kg": "" } as const;

function canStartChecks(loop: LoopView, starting = false): boolean {
  return !starting && isOverdueEnabledLoop(loop) && loop.details?.daemon?.state !== "responsive";
}

function StartChecksButton({ loop, threadId, starting = false, onDone }: { loop: LoopView; threadId: string; starting?: boolean; onDone: () => void }) {
  const rpc = useRpc<typeof rpcContract>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  if (!canStartChecks(loop, starting)) return null;
  const act = () => {
    setBusy(true);
    setError("");
    rpc
      .call("startChecks", { configPath: loop.configPath, ...(loop.managedId ? { managedId: loop.managedId } : {}), expectedThreadId: threadId })
      .then(result => {
        if (result.ok) {
          markStartingChecks(threadId, loop);
        } else {
          clearStartingChecks(threadId, loopKey(loop));
          setError(result.message);
        }
      })
      .catch(() => {
        clearStartingChecks(threadId, loopKey(loop));
        setError("Could not start checks. Refresh status before retrying.");
      })
      .finally(() => { setBusy(false); onDone(); });
  };
  return <>
    <button type="button" {...kg} aria-label={`Start checks ${nameOf(loop)}`} title="Install user service (or start background checks) so checks resume and survive reboot" disabled={busy} onClick={act} style={buttonStyle}>{busy ? "Starting…" : "Start checks"}</button>
    {error && <span role="alert">{error}</span>}
  </>;
}

function ToggleButton({ loop, threadId, onDone }: { loop: LoopView; threadId: string; onDone: () => void }) {
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
  // A full-width 44 px Pause is one stray tap away, and only a person at a terminal undoes it: on a phone, ask first, as for another thread's loop.
  const askFirst = watching || isPhone();
  const pauseVerb = isObserved(loop) ? "Pause checking" : "Pause";
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
      ? <ResumeNote loop={loop} />
      : askFirst && confirming
        ? <span role="group" aria-label={`Confirm pausing ${nameOf(loop)}`} style={{ display: "grid", gap: 8 }}>
            <span>{isObserved(loop) ? `Pause checking ${nameOf(loop)}?` : `Pause loop ${nameOf(loop)}?`} {!watching ? "A person resumes it from a terminal." : loop.details?.targetThread ? `It supervises ${loop.details.targetThread}, not this thread.` : `Its current target is ${loop.details?.targetSession ?? loop.details?.targetAdapter ?? loop.target}, not this thread.`}</span>
            <span className="kg-actions" style={actionsStyle}>
              <button type="button" {...kg} disabled={busy} onClick={act} style={buttonStyle}>{isObserved(loop) ? `Pause checking ${nameOf(loop)}` : `Pause loop ${nameOf(loop)}`}</button>
              <button type="button" {...kg} disabled={busy} onClick={() => setConfirming(false)} style={buttonStyle}>Cancel</button>
            </span>
          </span>
        : <button type="button" {...kg} aria-label={`${askFirst ? `${pauseVerb}…` : pauseVerb} ${nameOf(loop)}`} disabled={busy} onClick={askFirst ? () => setConfirming(true) : act} style={buttonStyle}>{askFirst ? `${pauseVerb}…` : pauseVerb}</button>}
    {error && <span role="alert">{error}</span>}
  </>;
}

// Where a held loop is resumed, in the words `looptender status` uses for the
// same state. A superseded loop is never resumed: its successor continues.
const wrap = { minWidth: 0, overflowWrap: "anywhere" } as const;
function ResumeNote({ loop }: { loop: LoopView }) {
  // An archived loop is not resumed: looptender says so, and a person unarchives it first.
  if (loop.details?.archived) return <span className="kg-t" style={wrap}>{archivedLine(loop.details.archived)}.</span>;
  if (loop.controlState === "superseded") {
    const successor = loop.details?.supersededBy;
    return <span className="kg-t" style={wrap}>Resume is refused for a superseded loop; its successor continues the work{successor ? ` (${successor})` : ""}.</span>;
  }
  // The same structure for every held state, each with its own true sentence; the command is in Details.
  return <span className="kg-t" style={{ ...wrap, display: "grid", gap: 8 }}><span>{heldSentence(loop)}</span>{!isWait(loop) && <PhoneResumeLink loop={loop} />}</span>;
}

function ThreadSetup({ threadId, onChanged, onClose, hideStart = false }: { threadId: string; onChanged(): void; onClose?(): void; hideStart?: boolean }) {
  const rpc = useRpc<typeof rpcContract>();
  const [started, setStarted] = useState(false);
  const call = useCallback(async (input: Parameters<typeof rpc.call<"setup">>[1]) => {
    const result = await rpc.call("setup", input);
    if (result.started) setStarted(true);
    return result;
  }, [rpc]);
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
  // exists, so they sit behind a collapsed section of their own (hidden once started).
  return <><SetupPanel key={`${threadId}-${generation}`} threadId={threadId} call={call} onChanged={onChanged} onClose={onClose} hideStart={hideStart} />
    {!started && <details className="kgs-more"><SetupStyles /><summary>Already have a loop?</summary>
      <AttachPanel key={`attach-${threadId}`} threadId={threadId} call={attach} onChanged={onChanged} />
      <TransferPanel key={`move-${threadId}`} threadId={threadId} call={transfer} onChanged={onChanged} />
    </details>}</>;
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

// The name the card shows: looptender's display name when it has one, else the slug.
const nameOf = (l: LoopView) => l.displayName ?? l.loop;

function referenceName(loop: LoopView, loops: LoopView[], references: Map<string, number>) {
  return loops.filter(l => nameOf(l) === nameOf(loop)).length > 1 ? `${nameOf(loop)} · reference ${references.get(loopKey(loop))}` : nameOf(loop);
}

// The slug, as secondary text beside a display name.
function Slug({ loop }: { loop: LoopView }) {
  return loop.displayName ? <span style={{ opacity: 0.7 }}> ({loop.loop})</span> : null;
}

function ReferenceDetails({ loop, name }: { loop: LoopView; name: string }) {
  return name === nameOf(loop) ? null : <details><summary {...kg}>Configuration reference</summary><code>{loop.configPath}</code><div>Reference numbers identify rows in this plugin session; they are not loop IDs.</div></details>;
}

// What the reasons already say is not said again in the details.
function detailLines(l: LoopView, reasons: string[]): string[] {
  const said = (prefix: string, reason: string) => reasons.some(r => r.startsWith(reason)) && prefix;
  const dropped = ["Note: ", "Needs you: ", "Disk: ", "Send record: ", "Unreadable record: ", said("Deadline: ", "Deadline reached"), said("Sends: ", "Send limit reached")];
  return describe(l).slice(1).filter(line => !dropped.some(prefix => prefix && line.startsWith(prefix)));
}

// Lines about how looptender works inside (records, ids, daemon, build comparison): kept, behind "Technical details".
const TECHNICAL = ["Reference", "Last explicit CLI menu publication", "CLI menu publication", "Status read:", "Check evidence:", "Daemon answered:", "Plugin build:", "Last check state:", "Build note:"];

/** The details of one loop: plain lines, the technical ones apart, and the resume command a held loop's person runs. */
function splitDetails(l: LoopView, reasons: string[]) {
  const all = detailLines(l, reasons);
  const command = needsResume(l) && !isWait(l) && heldSentence(l) ? resumeCommand(l) : undefined;
  return { lines: all.filter(x => !TECHNICAL.some(p => x.startsWith(p))), technical: all.filter(x => TECHNICAL.some(p => x.startsWith(p))), command };
}

// The build line: how this loop's supervisor compares with the installed build, visible, never a tooltip.
function BuildFreshness({ loop }: { loop: LoopView }) {
  const f = freshnessToShow(loop.details);
  if (!f) return null;
  // looptender's own words: the chip, and either the command that is true for that supervisor or what to do in words.
  return <div aria-label="Build" className="kg-t" style={{ margin: 0, padding: "0 12px 4px 12px", fontSize: 12, overflowWrap: "anywhere" }}>
    <span>{f.text}</span>{f.command && <> — <code>{f.command}</code></>}{!f.command && f.action && <> — {firstSentenceOf(f.action)}</>}
  </div>;
}

const plainList = { margin: 0, padding: 0, listStyle: "none" } as const;

function Reasons({ loop }: { loop: LoopView }) {
  const lines = reasonLines(loop.controlState, loop.note, loop.details);
  return lines.length ? <ul aria-label="Why" className="kg-t" style={{ margin: 0, padding: "0 12px 4px 28px", fontSize: 12, opacity: 0.85, overflowWrap: "anywhere" }}>{lines.map((l, i) => <li key={i}>{l}</li>)}</ul> : null;
}

// The rest of a loop's status, behind one disclosure. Closed on a phone, where
// the lines are many and the reason and Pause above it are what matter.
function LoopDetails({ lines, technical = [], command, padding = "0 12px 6px" }: { lines: string[]; technical?: string[]; command?: string; padding?: string }) {
  const [open, setOpen] = useState(() => !isPhone());
  return <details open={open} onToggle={e => setOpen(e.currentTarget.open)} className="kg-t" style={{ padding, fontSize: 12 }}><summary {...kg}>Details</summary>
    <ul aria-label="Details" style={{ ...plainList, overflowWrap: "anywhere" }}>
      {command && <li>To resume from a terminal: <code>{command}</code></li>}
      {lines.map((line, i) => <li key={i}>{line}</li>)}
    </ul>
    {technical.length > 0 && <details style={{ paddingTop: 4 }}><summary {...kg}>Technical details</summary>
      <ul aria-label="Technical details" style={{ ...plainList, overflowWrap: "anywhere" }}>{technical.map((line, i) => <li key={i}>{line}</li>)}</ul>
    </details>}
  </details>;
}

function ObservedRole({ loop }: { loop: LoopView }) {
  const role = observedRoleLabel(loop.details);
  if (!role) return null;
  return <div className="kg-t" style={{ margin: 0, padding: "0 12px 4px 12px", fontSize: 12, opacity: 0.85, overflowWrap: "anywhere" }}>{role}</div>;
}

function CardLoop({ loop, name, threadId, starting, refresh, failed, created, showName }: { loop: LoopView; name: string; threadId: string; starting: boolean; refresh(): void; failed: boolean; created: string | null; showName: boolean }) {
  return <section aria-label={`Loop ${name}`} style={{ overflowWrap: "anywhere", minWidth: 0 }}>
    {(showName || loop.displayName) && <strong style={{ fontSize: 12 }}>{name}<Slug loop={loop} /></strong>}
    <ReferenceDetails loop={loop} name={name} />
    <ContextRow icon="Repeat" label={failed ? "looptender: status unavailable (previous snapshot)" : created ?? label(loop, starting)}
      title={boundedTitle(describe(loop).filter(line => !line.startsWith("Note: ")).join("\n"), 800)} trailing={loop.handoffs > 0 ? `${loop.handoffs} handoffs` : undefined}
      action={<>{!failed && created !== KEEPING_GOING && <><StartChecksButton loop={loop} threadId={threadId} starting={starting} onDone={refresh} /><ToggleButton loop={loop} threadId={threadId} onDone={refresh} /></>}</>} />
    {!failed && <ObservedRole loop={loop} />}
    {!failed && <BuildFreshness loop={loop} />}
    {!failed && <Reasons loop={loop} />}
    {/* Everything the tooltip holds, in the card itself: reachable by keyboard and touch. */}
    <LoopDetails {...splitDetails(loop, reasonLines(loop.controlState, loop.note, loop.details))} />
  </section>;
}

function CardRow({ threadId }: { threadId: string }) {
  const { loops, references, starting, refresh, failed, loaded } = useLoops(threadId);
  const { outcome, start, control, loop } = useThreadOutcome(threadId, loops);
  const created = outcomeLabel(outcome, loop?.controlState, loop?.supervising);
  const [open, setOpen] = useState(false);
  const lineage = useLineage(threadId, loops.map(l => `${loopKey(l)}:${l.controlState}`).join(","));
  const nav = useBbNavigate();
  const childMoreRef = useRef<HTMLDetailsElement>(null);
  // A lane is covered through its parent's loop, and a session has one loop: no
  // entry button, one quiet line, and setup kept in a collapsed "more" below.
  const quiet = loaded && !failed && !loops.length && lineage.child;
  const waitingOnLineage = loaded && !failed && !loops.length && lineage.pending;
  if (quiet) return <div>
    <SetupStyles />
    {lineage.coveredBy && <ContextRow icon="Repeat" label={<>Covered by <a href={`#thread-${lineage.coveredBy.threadId}`} onClick={e => { e.preventDefault(); nav.toThread(lineage.coveredBy!.threadId); }}>{lineage.coveredBy.loop}</a></>} title="This thread is covered through its parent thread's loop" />}
    <details ref={childMoreRef} className="kgs-more"><summary>Set up a loop for this thread anyway</summary>
      <ThreadSetup key={threadId} threadId={threadId} onChanged={refresh} onClose={() => { if (childMoreRef.current) childMoreRef.current.open = false; }} hideStart={false} />
    </details>
  </div>;
  if (waitingOnLineage) return <div><SetupStyles /></div>;
  return <div>
    <SetupStyles />
    <FocusStyle />
    {failed && <StatusWarning refresh={refresh} />}
    {loaded && !failed && <NewThreadOutcome threadId={threadId} outcome={outcome} controlState={loop?.controlState} supervising={loop?.supervising} start={start} control={control} onChanged={refresh} showLabel={false} />}
    {!loaded ? <ContextRow icon="Repeat" label="looptender: checking status" /> : !loops.length ? <ContextRow icon="Repeat" label="looptender" action={!failed && <button type="button" className="kgs-chip" {...kg} style={buttonStyle} onClick={() => setOpen(!open)} aria-expanded={open}>Add to Looptender</button>} /> : loops.map(l => <CardLoop key={loopKey(l)} loop={l} name={referenceName(l, loops, references)} threadId={threadId} starting={!!starting?.has(loopKey(l))} refresh={refresh} failed={failed} created={l === loop ? created : null} showName={loops.length > 1} />)}
    {!failed && loops.some(l => l.managedId) && <button type="button" className="kgs-chip" {...kg} style={buttonStyle} onClick={() => setOpen(!open)} aria-expanded={open}>{loops.length > 1 ? "Thread setup" : "Setup"}</button>}
    {open && <div hidden={failed}><ThreadSetup key={threadId} threadId={threadId} onChanged={refresh} onClose={() => setOpen(false)} hideStart={created === READY_NOT_STARTED} /></div>}
  </div>;
}

// The collapsed banner's tooltip: each loop's state and its reasons, cut to a
// length a person reads. Everything in it is also in the expanded details, so
// nothing is reachable only by hover.
function summaryTitle(loops: LoopView[], starting?: Map<string, StartingEntry>): string {
  // Never the raw note: it is an agent's free text. The expanded details carry it.
  return boundedTitle(loops.map(l => [label(l, !!starting?.has(loopKey(l))), ...reasonLines(l.controlState, null, l.details)].join("\n")).join("\n\n"), 600);
}

// Build differences belong on the collapsed chip only when the loop is in an
// active/resumable state and its daemon is not known to be stopped; stopped or
// terminal states keep build details inside the expanded card.
const CHIP_BUILD_DIFFERS_STATES = new Set(["looptender: enabled", "looptender: paused", "looptender: awaiting user"]);
function showBuildDiffersOnChip(loop: LoopView, base: string): boolean {
  if (!CHIP_BUILD_DIFFERS_STATES.has(base)) return false;
  const daemonState = loop.details?.daemon?.state;
  return !daemonState || daemonState === "responsive";
}

// The one reason worth a few words beside the state on a collapsed chip: the one
// that matters most now, from a fixed set of phrases, never free text (a note may
// carry paths, ids or names; the expanded card holds it). A reason that needs a
// person comes before one that resolves by itself: a send limit, a passed
// deadline (held until a person acts, and it wins over a limit wait), a low disk,
// then a usage limit that resets on its own, then a build that differs.
function shortReason(loop: LoopView, base: string): string {
  const d = loop.details;
  if (recordsUnreadable(d) || d?.archived || isWait(loop)) return "";
  if (d?.sends?.reached) return "send limit reached";
  if (d?.deadline?.reached === true) return "deadline passed";
  if (d?.disk) return "disk low";
  const wait = d?.limitWait;
  if (wait && wait.state !== "none" && !wait.resetPassed) return "waiting on a usage limit";
  if (buildSummary(d?.runtime).differs && showBuildDiffersOnChip(loop, base)) return "build differs";
  return "";
}

/** The collapsed chip: the state said once, then at most one reason. No version: the details carry it. */
function chipLabel(loop: LoopView, starting = false): string {
  const base = stateLabel(loop, starting);
  return [base, shortReason(loop, base)].filter(Boolean).join(" · ");
}

const chipButtonStyle = {
  background: "none",
  border: 0,
  padding: "0 0 0 8px",
  boxSizing: "border-box",
  font: "inherit",
  color: "inherit",
  cursor: "pointer",
  textAlign: "left",
  display: "inline-flex",
  alignItems: "center",
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
  minWidth: 0,
  maxWidth: "100%",
  flexShrink: 1,
} as const;

const chipTextStyle = {
  display: "block",
  minWidth: 0,
  maxWidth: "100%",
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
} as const;

function BannerSummary({ threadId, isExpanded, onToggle }: { threadId: string; isExpanded: boolean; onToggle(): void }) {
  const { loops, starting, failed, loaded } = useLoops(threadId);
  const { outcome, loop } = useThreadOutcome(threadId, loops);
  const created = outcomeLabel(outcome, loop?.controlState, loop?.supervising);
  const only = loops.length === 1 ? loops[0] : undefined;
  const lineage = useLineage(threadId, loops.map(l => `${loopKey(l)}:${l.controlState}`).join(","));
  const noLoop = loaded && !failed && !loops.length;
  // Nothing while the lineage is pending; on a child, no "Add to Looptender" entry.
  if (noLoop && lineage.pending) return null;
  const childText = noLoop && lineage.child ? (lineage.coveredBy ? `Covered by ${lineage.coveredBy.loop}` : "looptender") : undefined;
  const text = failed ? "looptender: status unavailable" : !loaded ? "looptender: checking status" : loops.length > 1 ? `looptender: ${loops.length} matching loops` : created ?? (only ? chipLabel(only, !!starting?.has(loopKey(only))) : childText ?? "Add to Looptender");
  return <><FocusStyle />
    <button type="button" data-kg="chip" onClick={onToggle} aria-expanded={isExpanded} title={failed ? "Status could not be refreshed. Open details to retry." : loops.length ? summaryTitle(loops, starting) : childText ? "This thread is a child; setup is in the details" : "Set up a loop for this thread"}
      style={chipButtonStyle}>
      <span style={chipTextStyle}>{text}</span>
    </button></>;
}


function BannerDetails({ threadId, isExpanded, onToggle }: { threadId: string; isExpanded: boolean; onToggle(): void }) {
  const { loops, references, starting, refresh, failed, loaded } = useLoops(threadId);
  const { outcome, start, control, loop } = useThreadOutcome(threadId, loops);
  const created = outcomeLabel(outcome, loop?.controlState, loop?.supervising);
  const lineage = useLineage(threadId, loops.map(l => `${loopKey(l)}:${l.controlState}`).join(","));
  const nav = useBbNavigate();
  if (!isExpanded) return null;
  const childNoLoop = loaded && !failed && !loops.length && lineage.child;
  if (loaded && !failed && !loops.length && lineage.pending) return null;
  if (childNoLoop) return <div className="kg-banner-body">
    <SetupStyles />
    {lineage.coveredBy && <div style={{ padding: "4px 12px", fontSize: 12 }}>Covered by <a href={`#thread-${lineage.coveredBy.threadId}`} onClick={e => { e.preventDefault(); nav.toThread(lineage.coveredBy!.threadId); }}>{lineage.coveredBy.loop}</a>, a loop on a parent thread.</div>}
    <details className="kgs-more"><summary>Set up a loop for this thread anyway</summary>
      <ThreadSetup key={threadId} threadId={threadId} onChanged={refresh} onClose={onToggle} hideStart={false} />
    </details>
  </div>;
  return <div className="kg-banner-body">
    <FocusStyle />
    <SetupStyles />
    {failed && <StatusWarning refresh={refresh} />}
    {loaded && !failed && <NewThreadOutcome threadId={threadId} outcome={outcome} controlState={loop?.controlState} supervising={loop?.supervising} start={start} control={control} onChanged={refresh} showLabel={false} />}
    {loops.map(l => {
      const name = referenceName(l, loops, references);
      const isStarting = !!starting?.has(loopKey(l));
      const reasons = reasonLines(l.controlState, l.note, l.details);
      const split = splitDetails(l, reasons);
      return <section key={loopKey(l)} aria-label={`Loop ${name}`} style={{ display: "grid", gap: 8, padding: "4px 12px 8px", fontSize: 12, overflowWrap: "anywhere", minWidth: 0 }}>
        <div className="kg-t" style={{ minWidth: 0, opacity: 0.9 }}>
          {failed && <div>Previous snapshot</div>}
          <div><strong>Loop {name}</strong><Slug loop={l} />{loops.length > 1 || failed ? <>: {l === loop ? created ?? label(l, isStarting) : label(l, isStarting)}</> : null}</div>
          {!failed && observedRoleLabel(l.details) && <div>{observedRoleLabel(l.details)}</div>}
          <ReferenceDetails loop={l} name={name} />
          <BuildFreshness loop={l} />
          {reasons.length > 0 && <ul aria-label="Why" style={{ ...plainList, paddingTop: 4 }}>{reasons.map((line, i) => <li key={i}>{line}</li>)}</ul>}
        </div>
        {/* Under the reason, full width on a phone: not beside the text, where it takes a fifth of the column. */}
        {!failed && !(l === loop && created === KEEPING_GOING) && <div className="kg-actions" style={actionsStyle}><StartChecksButton loop={l} threadId={threadId} starting={isStarting} onDone={refresh} /><ToggleButton loop={l} threadId={threadId} onDone={refresh} /></div>}
        <LoopDetails {...split} padding="0" />
      </section>;
    })}
    {loaded && (!loops.length || loops.some(l => l.managedId)) && <div hidden={failed}><ThreadSetup key={threadId} threadId={threadId} onChanged={refresh} onClose={onToggle} hideStart={created === READY_NOT_STARTED} /></div>}
  </div>;
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
