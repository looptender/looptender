# Explicit safe-boundary compaction workflow (unreleased source)

This is a coordinator-operated helper for an **existing person instruction**,
not a daemon action or an automatic context policy. Review the
[design contract](../../../../../docs/design/context-efficiency.md) before use.
Run from a separate authorized lane/terminal while the exact target is idle;
a running coordinator cannot compact itself from its active turn. No service,
timer, automatic downgrade, successor, clear, stop, archive or hold release is
provided. Do not install or roll it out as a side effect of an audit.

## Preconditions and grant

Use Python 3 on Unix and a private 0700 directory outside the checkout and loop
runtime. Grant, receipt and journal files must be regular 0600 files; preserve the journal and lock
across invocations. The source requires an assignment-lifecycle-capable CLI and
complete `assignment inventory` coverage for at most 32 direct lanes, all idle,
nonarchived, without queued/background work or pending interactions. Partial,
unknown, busy or larger inventories defer. This is deliberately narrower than
BB's manual compact command. Nothing is dispatched to a lane.

A private grant records prior person authority; an agent-written file cannot
create that authority. Preserve `BB_THREAD_ID` and `BB_CLI`, never impersonate the
owner or swap credentials/permission settings. See
`tools/context-compaction/test_compact.py` for a complete fictional grant.

All keys are required; duplicate and unknown keys refuse:

| Field | Required value |
|---|---|
| `version`, `id`, `request`, `caller` | Version 1; stable grant ID; unique reviewed request/direction; authorized calling BB thread. |
| `binding` | Exact `loop`, canonical absolute `config` and `runtime`, target `thread`, `project`, `environment`, `host`, `provider`, recorded `owner`, positive ownership `generation`, current `goal` and `goalRevision`. Only the primary target is supported. |
| `bb`, `effectiveBB`, `keepgoing` | Each `{path,sha256}` pins the absolute executable and effective BB re-exec target. |
| `maxAttempts`, `cooldownSeconds` | Explicit lifetime cap 1–8 and spacing 3600–604800 seconds. These are safety bounds for this helper, not an optimal cadence. Unknown attempts count. Bounds cannot change within a journal. |
| `continuity` | Exactly seven roles: `authorization`, `goal`, `tasks`, `obligations`, `ownership`, `approvals`, `holds`, each `{path,sha256}` for a bounded absolute regular file. Include the config itself as one source. Multiple roles can refer to one reviewed manifest. Files retain the actual content; the journal stores references/hashes only. |

The manifest must preserve actual goal/tasks, lane ownership and unfinished work,
approvals with their sources, holds and pending obligations. Save these as durable, versioned checkpoint files before execution and retain
them afterward, including older versions referenced by an attempt. The helper
records references and hashes; it neither backs up files nor verifies their
semantic contents. Do not point task/obligation roles only at mutable files whose
previous versions will disappear. Hash equality proves
file integrity, not semantic completeness. Keep real identities and task content
private. A grant/journal is cooperative local evidence, not protection against a
person deleting every record and inventing new authority.

## Dry run, one attempt, then reconcile

1. Review current structured status and assignment evidence, save continuity and
   prepare the exact grant. Run from the skill directory:

   ```sh
   python3 -B scripts/compact.py --grant <absolute-grant> --journal <absolute-journal>
   ```

   Eligibility performs bounded model-free reads only. No grant means no action.
   `--execute` is required to send the one structured
   `bb thread compact <exact-thread> --json` request. It is not a scheduled wake.
2. The helper verifies owner/goal/target/host/control/allowance bindings, complete
   idle-child coverage and sources twice, catches up at most 2,000 events from
   the existing exact-target BB cursor, and reads context immediately before
   reservation. It never examines transcript text or emits raw command output.
   An absent/damaged cursor, oversized event page or incomplete catch-up refuses.
   A compact encountered during preparation defers the new request.
3. Under a nonwaiting private workflow lock it persists an `unknown` attempt,
   with context and continuity, **before** asking BB. It rechecks sources and the
   target/control gate after reservation. Lost/error/malformed responses remain
   unknown. An accepted response remains accepted, not completed. Repeating an
   unresolved or already-completed request never submits compact again; unresolved
   repeats perform no provider/status queries. Retain the journal even on failure.
4. Inspect the private journal and exact-target lifecycle metadata once useful
   completion evidence is available. Supply a private receipt:

   ```json
   {"attempt":"<journal-attempt-id>","priorContinuity":"<SHA-256 of original canonical continuity object>","currentContinuity":"<SHA-256 of current canonical continuity object>","accepted":true}
   ```

   Digest objects with the same sorted, compact JSON encoding as
   `coordinator_state.digest`. The coordinator must actually reconcile the current
   goal/tasks/ownership/approvals/holds and pending obligations before declaring
   `accepted`. A receipt is a declaration, not automated task verification.
5. Run the same command with `--execute --receipt <absolute-receipt>`. Reconciliation
   requires exactly one post-baseline matching contextCompaction start/completion
   pair, same item/turn, fresh post-context, unchanged owner/goal/target binding
   and current continuity files matching the receipt. No new compact is sent.
   Current runtime holds may remain while completion is recorded; none is cleared.
   Missing, stale, duplicate or ambiguous completion retains the pending attempt.
   This first slice supports the observed item lifecycle, not every provider's
   alternate `thread/compacted` taxonomy; unsupported evidence stays unknown.

The default output has action/reason classes only. IDs, source paths, context
figures and receipts remain in the private journal. CLI failures are minimized to
one refusal code; inspect the private inputs, never dump transcripts for diagnosis.

## Limits

There is no atomic transaction spanning local files, BB, the target's daemon and
independent messages. A hold or work can arrive after final revalidation; BB's own
idle eligibility remains the last gate. An unknown reservation is conservative
and may include a request that never reached BB. Do not delete it or retry it.
Ambiguous/out-of-bound history requires person reconciliation outside this first
slice, retaining the attempt.

A newly fetched BB context reading has no provider measurement timestamp on some
providers. The dry-run labels that freshness unknown; it does not claim a newly
measured sample. This explicit helper selects no occupancy threshold and cannot
infer cache expiry, natural-compaction cadence or billed savings. Review recent
natural compacts in the continuity manifest before authorizing a new request.
Cooldown counts this journal's attempts, not unrelated provider compactions.

Changing the request/direction cannot clear pending attempts, change the target,
owner/goal revision or increase bounds. Once complete, a new authorized request
must still respect the lifetime cap/cooldown. Target/goal migration and corrupt
journal repair are separate manual reviews. No Linux/Windows native or installed
acceptance follows from local fixtures; Windows locking is unsupported. The
helper is not an automatic compact policy or a successor implementation.

### Admission and retained checkpoint verification

Preparation and final admission both check for intervening structured compaction
items. Final admission repeats the complete lane inventory and child eligibility
checks. A separate durable dispatch timestamp excludes preparation/admission time
from cooldown and prevents a known pre-dispatch refusal from reconciling an
unrelated completion. Pending repeats remain query-free. Original checkpoint
versions must remain readable and hash-matching during reconciliation and before
later attempts, even when current continuity references newer versions.

These gates do not provide atomic host request identity or fencing: independent
work can arrive after the final read. Competing requests still require person
review. The source-only journal now requires the dispatch field; older experimental
journals fail closed and must not be deleted or reset to bypass an unknown attempt.
