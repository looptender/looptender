import { useEffect, useState } from "react";
import type { z } from "zod";
import type { transferInput, TransferResult } from "./transfer-contract.ts";
import { SetupStyles } from "./setup-style.tsx";

type Call = (input: z.infer<typeof transferInput>) => Promise<TransferResult>;
export function TransferPanel({ threadId, call, onChanged }: { threadId: string; call: Call; onChanged(): void }) {
  const [choices, setChoices] = useState<NonNullable<TransferResult["choices"]>>([]);
  const [selected, setSelected] = useState("");
  const [result, setResult] = useState<TransferResult>();
  const [busy, setBusy] = useState(true);
  useEffect(() => {
    let mounted = true;
    call({ action: "list", threadId }).then(r => { if (mounted) { setChoices(r.choices ?? []); setResult(r); } })
      .catch(() => { if (mounted) setResult({ ok: false, message: "Move list unavailable. Check host access and refresh." }); })
      .finally(() => { if (mounted) setBusy(false); });
    return () => { mounted = false; };
  }, [threadId, call]);
  async function act(action: z.infer<typeof transferInput>["action"]) {
    setBusy(true);
    try {
      const r = await call({ action, threadId, ...(selected ? { recordId: selected } : {}), ...(action === "apply" && result?.ticket ? { ticket: result.ticket } : {}) });
      setResult(r);
      if (r.choices) setChoices(r.choices);
      if (r.moved || action === "apply" || action === "recover") onChanged();
    } catch (e) {
      const timedOut = e instanceof Error && (e.name === "TimeoutError" || e.name === "AbortError");
      setResult({ ok: false, message: `The move request did not finish: ${timedOut ? "the request timed out" : "the request to the plugin failed"}${e instanceof Error && e.name !== "Error" && !timedOut ? ` (${e.name})` : ""}. ${action === "apply" || action === "recover" ? "Check move status before retrying; a saved request or held move may already exist. " : "Nothing was changed. "}No automatic retry was made.` });
    } finally { setBusy(false); }
  }
  return <section className="kgs" aria-label="Move supervision here">
    <SetupStyles />
    <div>
      <h4>Move supervision here</h4>
      <p className="help">Use this when a loop already supervises another thread and you want this thread ({threadId}) to take it over. The loop stays held until you resume it.</p>
    </div>
    <div className="field"><label htmlFor={`kgs-move-${threadId}`}>Existing loop</label>
      <select id={`kgs-move-${threadId}`} aria-label="Existing loop" disabled={busy} value={selected} onChange={e => { setSelected(e.target.value); setResult(undefined); }}>
        <option value="">Choose a loop</option>
        {choices.map(c => <option key={c.id} value={c.id}>{c.loop} — {c.from}{c.pending ? " (move pending)" : ""}</option>)}
      </select></div>
    <div className="actions"><div className="row">
      <button type="button" className="btn" disabled={busy} onClick={() => void act("list")}>Refresh move list</button>
      {selected && !result?.moved && <>
        <button type="button" className="btn" disabled={busy || choices.find(c => c.id === selected)?.pending} onClick={() => void act("preview")}>Preview move</button>
        <button type="button" className="btn" disabled={busy} onClick={() => void act("inspect")}>Check move status</button>
      </>}
    </div></div>
    {result?.summary && <div className="card" role="group" aria-label="Move preview">
      <dl>
        <dt>Move</dt><dd>{result.summary.loop}: {result.summary.from} → {result.summary.to}</dd>
        <dt>Provider</dt><dd>{result.summary.provider}; model and account identity unverified.</dd>
        <dt>Controls</dt><dd>{Object.entries(result.summary.holds).map(([k, v]) => `${k}=${v}`).join(", ")}</dd>
        {result.summary.owner && <><dt>Owner</dt><dd>{result.summary.owner}</dd></>}
        {result.summary.goal && <><dt>Goal</dt><dd>{result.summary.goal.id}, revision {result.summary.goal.revision}</dd></>}
      </dl>
      <p className="help">The move transfers ownership, preserves existing holds, and adds an awaiting-user hold. It does not cancel the old thread’s active turn or change a timer. Resume is a separate decision.</p>
      {result.ticket && <div className="actions"><div className="row"><button type="button" className="btn primary" disabled={busy} onClick={() => void act("apply")}>Move and keep held</button></div></div>}
    </div>}
    {result?.canRecover && <div className="actions"><div className="row"><button type="button" className="btn" disabled={busy} onClick={() => void act("recover")}>Recover move</button></div></div>}
    {busy && <p role="status" className="help">Checking move…</p>}
    {result && <p className="msg" role={result.ok ? "status" : "alert"}>{result.message}</p>}
  </section>;
}
