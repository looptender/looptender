// Which loop covers a child thread: walk its parents one by one and take the
// nearest ancestor with a loop. The walk follows each thread's own parent, so
// it does not depend on the order the SDK lists ancestors in (the SDK says
// nearest first; this does not rely on it). Metadata only: no thread content.
export const MAX_ANCESTORS = 32;
export interface Lineage { child: boolean; coveredBy?: { threadId: string; loop: string } }
export async function walkLineage(
  threadId: string,
  parentOf: (id: string) => Promise<string | null>,
  loopOf: (id: string) => Promise<string | undefined>,
): Promise<Lineage> {
  const seen = new Set([threadId]);
  let child = false;
  let id = await parentOf(threadId);
  for (let hops = 0; id && !seen.has(id) && hops < MAX_ANCESTORS; hops++) {
    child = true; seen.add(id);
    const loop = await loopOf(id);
    if (loop) return { child, coveredBy: { threadId: id, loop } };
    id = await parentOf(id);
  }
  return { child };
}

// How long a cached answer is kept. A good answer is refreshed after a few
// minutes so a re-parented thread or an archived parent loop does not show a
// stale line until reload; a failed lookup is retried with a growing delay.
export const LINEAGE_TTL_MS = 3 * 60_000;
export const lineageRetryMs = (failures: number) => Math.min(120_000, 5_000 * 2 ** Math.max(0, failures - 1));
export interface LineageEntry { value?: Lineage; at: number; failures: number; inflight: boolean; key?: string }
export function lineageDue(e: LineageEntry | undefined, now: number): boolean {
  if (!e) return true;
  if (e.inflight) return false;
  return now - e.at >= (e.failures > 0 ? lineageRetryMs(e.failures) : LINEAGE_TTL_MS);
}
