// What a thread shows after it was created with Add to Looptender on: that
// setup is under way, why it could not go ahead, or what exists, whether it is
// running, and the one next action. It uses the same finished card as the
// setup panel, so both entry points read the same.
import { useCallback, useEffect, useState } from "react";
import { boundsPhrase, type NewThreadResult, type Outcome } from "./new-thread-contract.ts";
import type { NewThreadCall } from "./new-thread-control.tsx";
import type { SetupResult } from "./setup-contract.ts";
import { SetupStyles } from "./setup-style.tsx";

export type StartCall = (input: { action: "start"; threadId: string }) => Promise<SetupResult>;
const NONE: Outcome = { state: "none", message: "" };
const button = { background: "none", color: "inherit", font: "inherit", fontSize: 12, lineHeight: "22px", padding: "0 10px", border: "1px solid currentColor", borderRadius: 6, cursor: "pointer", fontWeight: 600 } as const;

/** Reads the outcome for a thread, and keeps asking while setup is waiting for the thread's environment. */
export function useOutcome(threadId: string, call: NewThreadCall, refreshKey: unknown = 0, pollMs = 5_000) {
  const [outcome, setOutcome] = useState<Outcome>(NONE);
  const read = useCallback(() => call({ action: "outcome", threadId })
    .then((r: NewThreadResult) => { if (r.ok && r.outcome) setOutcome(r.outcome); })
    .catch(() => { /* the ordinary status row reports a lost connection */ }), [call, threadId]);
  useEffect(() => { setOutcome(NONE); }, [threadId]);
  useEffect(() => { void read(); }, [read, refreshKey]);
  useEffect(() => {
    if (outcome.state !== "waiting") return;
    const timer = setInterval(() => void read(), pollMs);
    return () => clearInterval(timer);
  }, [outcome.state, read, pollMs]);
  return { outcome, read };
}

/** A short label for the collapsed banner; null when this thread was not created with Add to Looptender on. */
export function outcomeLabel(outcome: Outcome, controlState: string | undefined, supervising: boolean | undefined): string | null {
  if (outcome.state === "waiting") return "looptender: setting up";
  if (outcome.state === "failed") return controlState ? null : "looptender: not set up";
  if (outcome.state !== "ready") return null;
  if (controlState === "disabled") return "looptender: ready, not started";
  if (controlState === "enabled" && supervising) return "looptender: keeping this going";
  return null;
}

export type ControlCall = (action: "pause" | "stop") => Promise<{ ok: boolean; message: string }>;

// `showLabel` is false where the row or banner button beside it already says the state word.
export function NewThreadOutcome({ threadId, outcome, controlState, supervising, start, control, onChanged, showLabel = true }: {
  threadId: string; outcome: Outcome; controlState: string | undefined; supervising: boolean | undefined; start: StartCall; control?: ControlCall; onChanged(): void; showLabel?: boolean;
}) {
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; message: string }>();
  if (outcome.state === "none") return null;
  const box = { padding: 0 } as const;
  if (outcome.state === "waiting") return <section className="kgs" aria-label="Add to Looptender for this thread" aria-live="polite"><SetupStyles /><div className="card"><h4>looptender: setting up</h4><p role="status">{outcome.message}</p></div></section>;
  // Once a loop exists for the thread, the ordinary status row speaks for it.
  if (outcome.state === "failed") return controlState ? null : <section className="kgs" aria-label="Add to Looptender for this thread" style={box}><SetupStyles />
    <div className="card"><h4>looptender could not set this thread up</h4><p className="msg" role="alert">{outcome.message}</p>
    {outcome.configPath && <dl><dt>Configuration</dt><dd>{outcome.configPath}</dd></dl>}</div>
  </section>;
  const label = outcomeLabel(outcome, controlState, supervising);
  if (!label) return null;
  const act = () => {
    setBusy(true); setResult(undefined);
    start({ action: "start", threadId })
      .then(r => setResult({ ok: r.ok, message: r.message }))
      .catch(() => setResult({ ok: false, message: "Start could not be confirmed. Refresh status before trying again." }))
      .finally(() => { setBusy(false); onChanged(); });
  };
  const hold = (action: "pause" | "stop") => {
    if (!control) return;
    setBusy(true); setResult(undefined);
    control(action)
      .then(r => setResult(r))
      .catch(() => setResult({ ok: false, message: "The control could not be confirmed. Refresh status before trying again." }))
      .finally(() => { setBusy(false); onChanged(); });
  };
  const started = controlState === "enabled";
  return <section className="kgs" aria-label="Add to Looptender for this thread" style={box}>
    <SetupStyles />
    <div className="card done">
      <h4>{started ? "Supervision started" : "Setup created, switched off"}</h4>
      <dl>
        <dt>Loop</dt><dd>{outcome.loop}</dd>
        <dt>Running</dt><dd>{started ? (outcome.startedAutomatically ? "Yes: started when this thread was created." : "Yes: started from this thread.") : "No. The loop is saved switched off and sends nothing until it is started."}</dd>
        {outcome.bounds && <><dt>Limits</dt><dd>{boundsPhrase(outcome.bounds)}</dd></>}
      </dl>
      {!started && <p className="help">{outcome.message}</p>}
      {!started && <div className="actions">
        <p className="help">Starting enables the loop and launches a background supervisor on this thread’s host. It may prompt the agent when eligible. Other timers are not detected or changed.</p>
        <div className="row"><button type="button" className="btn primary" disabled={busy} onClick={act}>Start background supervision</button></div></div>}
      {started && control && <div className="actions"><div className="row">
        <button type="button" className="btn" disabled={busy} onClick={() => hold("pause")}>Pause</button>
        <button type="button" className="btn" disabled={busy} onClick={() => hold("stop")}>Stop</button>
      </div></div>}
      {outcome.cadence && <details><summary>Details</summary><p className="help" style={{ paddingTop: 6 }}>{outcome.cadence} Starting launches a background supervisor on this thread’s host; it may prompt the agent when a check finds the thread idle. Other timers are not detected or changed.</p></details>}
      {result && <p className="msg" role={result.ok ? "status" : "alert"}>{result.message}</p>}
    </div>
  </section>;
}
