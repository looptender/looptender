import { useState } from "react";
import type { z } from "zod";
import { attachInput, type AttachResult } from "./attach-contract.ts";
import { SetupStyles } from "./setup-style.tsx";

type Call = (input: z.infer<typeof attachInput>) => Promise<AttachResult>;
export function AttachPanel({ threadId, call, onChanged }: { threadId: string; call: Call; onChanged(): void }) {
  const [result, setResult] = useState<AttachResult>();
  const [candidates, setCandidates] = useState<{ registryId: string; loop: string }[]>([]);
  const [id, setId] = useState("");
  const [busy, setBusy] = useState(false);
  async function act(action: z.infer<typeof attachInput>["action"]) {
    setBusy(true);
    try {
      const r = await call({ action, threadId, ...(action !== "list" ? { registryId: id } : {}), ...(action === "attach" && result?.ticket ? { ticket: result.ticket } : {}) });
      setResult(r);
      if (r.candidates) { setCandidates(r.candidates); setId(""); }
      if (r.attached) onChanged();
    } catch (e) {
      const timedOut = e instanceof Error && (e.name === "TimeoutError" || e.name === "AbortError");
      setResult({ ok: false, message: `${action === "attach" ? "Attaching" : action === "list" ? "Listing registered loops" : "The check"} did not finish: ${timedOut ? "the request timed out" : "the request to the plugin failed"}${e instanceof Error && e.name !== "Error" && !timedOut ? ` (${e.name})` : ""}. ${action === "attach" ? "Check attachment status before trying again; the reference may have been saved. " : "Nothing was changed. "}No automatic retry or start was requested.` });
    } finally { setBusy(false); }
  }
  return <section className="kgs" aria-label="Attach an existing loop">
    <SetupStyles />
    <div>
      <h4>Attach an existing loop</h4>
      <p className="help">Use this when a loop for this thread already exists on this device (made with the looptender CLI) and you want BB to show it here. Attaching only saves a reference; it never starts or resumes supervision.</p>
    </div>
    <div className="actions"><div className="row"><button type="button" className="btn" disabled={busy} onClick={() => void act("list")}>Find registered loops</button></div></div>
    {candidates.length > 0 && <>
      <div className="field"><label htmlFor={`kgs-attach-${threadId}`}>Registered loop</label>
        <select id={`kgs-attach-${threadId}`} aria-label="Registered loop" value={id} disabled={busy} onChange={e => { setId(e.target.value); setResult(undefined); }}>
          <option value="">Choose a loop</option>
          {candidates.map(c => <option key={c.registryId} value={c.registryId}>{c.loop} · {c.registryId}</option>)}
        </select></div>
      <div className="actions"><div className="row">
        <button type="button" className="btn" disabled={busy || !id} onClick={() => void act("preview")}>Preview attachment</button>
        <button type="button" className="btn" disabled={busy || !id} onClick={() => void act("inspect")}>Check attachment status</button>
      </div></div>
    </>}
    {result?.candidates?.length === 0 && <p className="help">No loops are registered on this device. Register the existing loop with the CLI first; this action does not search for files.</p>}
    {result?.record && <dl><dt>Configuration</dt><dd>{result.record.configPath}</dd></dl>}
    {result?.ticket && <div className="actions"><div className="row"><button type="button" className="btn primary" disabled={busy} onClick={() => void act("attach")}>Attach existing loop</button></div></div>}
    {busy && <p role="status" className="help">Checking attachment…</p>}
    {result && <p className="msg" role={result.ok ? "status" : "alert"}>{result.message}</p>}
  </section>;
}
