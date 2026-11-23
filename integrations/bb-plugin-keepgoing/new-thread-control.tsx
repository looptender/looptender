// The "Add to Looptender" control on BB's New thread screen: a switch in the
// composer's action row, and a banner that says exactly what will happen while
// it is on and holds the one send that carries the request.
import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import { SetupStyles } from "./setup-style.tsx";
import { DEFAULT_BOUNDS, TIMEOUT_CHOICES, boundsPhrase, boundsSchema, marker, type Bounds, type NewThreadInput, type NewThreadResult, type StartMode } from "./new-thread-contract.ts";

export type NewThreadCall = (input: NewThreadInput) => Promise<NewThreadResult>;
/** The part of BB's composer handle this control uses. */
export interface ComposerHandle {
  readonly scope: { kind: string; projectId?: string | null };
  readonly key: string;
  readonly isSubmittingBlocked: boolean;
  readonly submittingBlockedReason: string | null;
  submit(options?: { experimental_data?: { v: 1; intent: string } }): Promise<void>;
  /** Successful sends from this composer, however they were made. */
  onSubmitted?(listener: () => void): () => void;
}

// The switch and the banner are separate components mounted by BB, so the
// state they share is kept per draft, outside React. It holds no task text.
interface DraftState { on: boolean; bounds: Bounds; notice: string }
const OFF: DraftState = { on: false, bounds: DEFAULT_BOUNDS, notice: "" };
const drafts = new Map<string, DraftState>();
const listeners = new Set<() => void>();
const subscribe = (l: () => void) => { listeners.add(l); return () => { listeners.delete(l); }; };
export function setDraft(key: string, patch: Partial<DraftState>) {
  drafts.set(key, { ...(drafts.get(key) ?? OFF), ...patch });
  for (const l of listeners) l();
}
const useDraft = (key: string) => useSyncExternalStore(subscribe, () => drafts.get(key) ?? OFF);
/** For the row in the menu beside Send, which BB draws itself. */
export const turnOn = (key: string) => setDraft(key, { on: true, notice: "" });
export const resetDrafts = () => drafts.clear();

// What pressing the control will do, from the plugin's setting. Known once the
// plugin has been asked (the switch asks as it appears); until then the words
// say only that the new thread is added to looptender.
let knownMode: StartMode | undefined;
export const resetKnownMode = () => { knownMode = undefined; };
const useKnownMode = () => useSyncExternalStore(subscribe, () => knownMode, () => undefined);
/** One sentence for what sending with the control on does, under each setting. */
export function sendEffect(mode: StartMode | undefined): string {
  return mode === "automatic" ? "Sends your message, creates a looptender setup for the new thread and starts supervising it."
    : mode === "manual" ? "Sends your message and creates a looptender setup for the new thread, switched off until you start it."
    : "Sends your message and adds the new thread to looptender.";
}
/** The label of the one button that sends, under each setting. */
export const sendLabel = (mode: StartMode) => mode === "automatic" ? "Send and start supervising" : "Send and create setup";

const button = { background: "none", color: "inherit", font: "inherit", fontSize: 12, lineHeight: "22px", padding: "0 10px", border: "1px solid currentColor", borderRadius: 6, cursor: "pointer" } as const;
const card = { display: "grid", gap: 6, padding: "10px 12px", border: "1px solid currentColor", borderRadius: 10, fontSize: 12, marginBottom: 6 } as const;

export function KeepGoingSwitch({ composer, call }: { composer: ComposerHandle; call?: NewThreadCall }) {
  const draft = useDraft(composer.key);
  const mode = useKnownMode();
  // Ask once, so the tooltip can say what will happen. A failure leaves the
  // general sentence; turning the switch on asks again and reports it.
  useEffect(() => {
    if (!call || knownMode || composer.scope.kind !== "new-thread") return;
    let live = true;
    call({ action: "check" }).then(r => { if (live && r.ok && r.mode) { knownMode = r.mode; for (const l of listeners) l(); } }).catch(() => {});
    return () => { live = false; };
  }, [call, composer.scope.kind]);
  if (composer.scope.kind !== "new-thread") return null;
  const effect = sendEffect(mode);
  return <><SetupStyles /><button type="button" role="switch" className="kgs-chip" aria-checked={draft.on} title={effect} aria-description={effect}
    onClick={() => setDraft(composer.key, { on: !draft.on, notice: "" })}
    style={{ opacity: draft.on ? 1 : 0.75, fontWeight: draft.on ? 600 : 400 }}>
    Add to Looptender
  </button></>;
}

export function KeepGoingBanner({ composer, call }: { composer: ComposerHandle; call: NewThreadCall }) {
  const key = composer.key;
  const draft = useDraft(key);
  const [mode, setMode] = useState<StartMode>();
  const [setups, setSetups] = useState<{ used: number; max: number }>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const check = useCallback(() => {
    let live = true;
    setMode(undefined); setError("");
    call({ action: "check" })
      .then(r => { if (!live) return; if (r.ok && r.mode) { setMode(r.mode); knownMode = r.mode; setSetups(r.setups); } else setDraft(key, { on: false, notice: r.message || "Add to Looptender is unavailable." }); })
      .catch(() => { if (live) setDraft(key, { on: false, notice: "Add to Looptender could not be checked. Check the connection and try again." }); });
    return () => { live = false; };
  }, [call, key]);
  // Turning the switch on is what asks whether keepgoing can be used here.
  useEffect(() => { if (draft.on) return check(); }, [draft.on, check]);
  // Any send from this composer ends the switch and its edited limits, so
  // neither carries silently into the next draft. A plain Enter is such a send.
  useEffect(() => composer.onSubmitted?.(() => setDraft(key, { on: false, notice: "", bounds: DEFAULT_BOUNDS })), [composer, key]);
  if (composer.scope.kind !== "new-thread") return null;
  if (!draft.on) {
    if (!draft.notice) return null;
    return <section className="kgs" aria-label="Add to Looptender" aria-live="polite">
      <SetupStyles />
      <div className="card"><h4>Add to Looptender is unavailable here</h4>
        <p className="msg" role="alert">{draft.notice}</p>
        <div className="actions"><div className="row"><button type="button" className="btn" onClick={() => turnOn(key)}>Check again</button></div></div></div>
    </section>;
  }
  if (!mode) return <section className="kgs" aria-label="Add to Looptender" aria-live="polite"><SetupStyles /><p role="status" className="help">Checking looptender…</p></section>;

  const send = async () => {
    setBusy(true); setError("");
    try {
      const projectId = composer.scope.projectId;
      if (!projectId) { setError("Choose a project first."); return; }
      const bounds = boundsSchema.safeParse(draft.bounds);
      if (!bounds.success) { setError("Set a limit of 1 to 1000 automatic messages."); return; }
      // The mode shown at this moment is the most that may happen.
      const armed = await call({ action: "arm", draftKey: key, projectId, bounds: bounds.data, shownMode: mode });
      if (!armed.ok || !armed.intent) { setError(armed.message || "Add to Looptender could not be turned on. The message was not sent."); return; }
      try {
        await composer.submit({ experimental_data: marker(armed.intent) });
        setDraft(key, { on: false, notice: "", bounds: DEFAULT_BOUNDS });
      } catch (failure) {
        // The message did not go; the request must not outlive it.
        await call({ action: "cancel", intent: armed.intent }).catch(() => {});
        // BB may already have delivered it: say what is known, not more.
        setError(failure instanceof Error && failure.message ? `${failure.message} The message may not have been sent; Add to Looptender was withdrawn for it.` : "The message may not have been sent; Add to Looptender was withdrawn for it.");
      }
    } catch { setError("Add to Looptender could not be confirmed. The message was not sent."); }
    finally { setBusy(false); }
  };
  const blocked = composer.isSubmittingBlocked;
  return <section className="kgs" aria-label="Add to Looptender" aria-live="polite">
    <SetupStyles />
    <div className="card">
      <h4>Add the new thread to Looptender</h4>
      <p id={`kgs-effect-${key}`}>{sendEffect(mode)}</p>
      <p className="help">{mode === "automatic" ? "Supervision starts once the thread exists. If anything about the start is uncertain, the setup is left switched off with the reason." : "Nothing runs until you start it from the new thread."}</p>
      <p className="help">{boundsPhrase(draft.bounds)}</p>
      <div className="actions">
        <div className="row">
          <button type="button" className="btn primary" aria-describedby={`kgs-effect-${key}`} disabled={busy || blocked} title={blocked ? composer.submittingBlockedReason ?? sendEffect(mode) : sendEffect(mode)} onClick={() => void send()}>{sendLabel(mode)}</button>
          <button type="button" className="btn" disabled={busy} onClick={() => setDraft(key, { on: false, notice: "", bounds: DEFAULT_BOUNDS })}>Turn off</button>
        </div>
        <span className="help">A plain Enter sends without looptender.</span>
      </div>
      <details>
        <summary>Details and limits</summary>
        <div className="actions" style={{ paddingTop: 8 }}>
          <p className="help">Your first message is the task, recorded as the loop’s goal. One thread, with the model and login it already has; no fallbacks.</p>
          <div className="field"><label htmlFor={`kgs-max-${key}`}>Stop for me after this many automatic messages</label>
            <input id={`kgs-max-${key}`} type="number" aria-label="Automatic messages before stopping" min={1} max={1000} value={draft.bounds.maxSends} disabled={busy}
              onChange={e => setDraft(key, { bounds: { ...draft.bounds, maxSends: Number(e.target.value) } })} style={{ width: 96 }} /></div>
          <div className="field"><label htmlFor={`kgs-timeout-${key}`}>Stop for me after this long without a Git change</label>
            <select id={`kgs-timeout-${key}`} aria-label="Time without a Git change before stopping" value={draft.bounds.noProgressTimeout} disabled={busy}
              onChange={e => setDraft(key, { bounds: { ...draft.bounds, noProgressTimeout: e.target.value as Bounds["noProgressTimeout"] } })}>
              {TIMEOUT_CHOICES.map(choice => <option key={choice} value={choice}>{choice}</option>)}
            </select></div>
          <p className="help">The loop is named after the project, with a short suffix; it does not use the message or the thread’s title. Later wakes point the agent at the saved task; the first message is not sent again.</p>
          {setups && <p className="help">This plugin holds {setups.used} of {setups.max} setups and cannot remove one yet.</p>}
          <p className="help">How often it checks, and how many times it nudges a stalled turn or re-prompts after an error, are looptender’s defaults. The thread shows the exact numbers once the loop is created.</p>
        </div>
      </details>
      {error && <p className="msg" role="alert">{error}</p>}
    </div>
  </section>;
}
