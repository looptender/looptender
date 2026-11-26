# Usage conservation: evidence, useful work and safe context lifecycle

Status: proposed automatic lifecycle contract for review. An explicit
[source-only coordinator helper](../../integrations/bb-plugin-keepgoing/skills/keepgoing/references/compaction-workflow.md)
implements the bounded manual slice; nothing invokes it automatically. The
[high-priority roadmap](../roadmap.md#high-priority-conserve-subscription-usage)
keeps implementation, fixture evidence and live acceptance separate. This design
does not change the current engineering prohibition on automatic context
lifecycle or the fallback-only conversation creation rule.

## Planned continuous optimization capability

Keep useful work moving while reducing avoidable subscription usage. This is a
product objective, not a claim of achieved subscription savings. Context lifecycle
is one intervention within a broader measurement and improvement process.

For each declared measurement period, aggregate structured usage by loop, lane
and provider with explicit coverage and counter semantics. Separate total input,
cached input, uncached input, output and reasoning where exposed. Derive uncached
input only when total and cached counters cover the same provider session and
interval; missing fields remain unknown. Never add reasoning or cached components
to totals that already contain them, or convert raw counts into subscription charge.

Rank avoidable work using structured evidence: duplicate wake/retry identities,
repeated status requests, task/revision ownership collisions, and lane startup,
handoff and review overhead. A long context or an expensive lane alone does not
prove waste. Do not inspect transcript text to classify work. When structured
ownership or acceptance evidence cannot distinguish duplication from useful
parallel work, leave that classification unresolved for the coordinator.

Choose one bounded intervention, compare equivalent work before and after, and
record useful completions, acceptance results, latency and rework alongside usage.
Include the audit and intervention's own overhead. Reassess on new evidence at
useful work boundaries; no extra recurring model/status loop is implied. Quality
and throughput must remain acceptable to the person's declared criteria. Retain
missing coverage, changed workload and cache effects as limits on attribution.

Model and effort policies remain explicitly user-selected, scoped and revisioned.
Neither a conservation target nor a raw-token ranking authorizes a downgrade.
Any future policy comparison requires authorization and the same useful-work
acceptance evidence. Successor startup and compaction need their own continuity
and cache/overhead accounting below; neither promises cheaper subscription use.

**Implemented in this source change:** corrected usage diagnostics and the explicit
journaled compact helper with fictional tests. **Planned:** integrated per-loop/lane/
provider measurement, duplicate-work attribution, continuous intervention evaluation
and automatic lifecycle policy. Existing suppression and retry bounds remain
available; this design does not claim new batching, live savings or automatic tuning.

## Evidence and the decision to act

Use bounded structured BB metadata, never transcript text, to distinguish:

- **Consumption:** provider token-counter changes in a dated interval, with
  input, cached input, output and reasoning fields separate. Cached input is
  generally a component of input and reasoning a component of output; do not
  add components twice. Provider definitions and missing baselines must be
  explicit. Counter drops, provider-session changes and retention gaps make
  totals partial, not zero. Raw tokens do not price a subscription allowance.
- **Occupancy:** the latest context reading, provider estimated flag, timestamp,
  window and any supported automatic-compaction threshold. A context reduction
  is not negative consumption or measured savings. Window capacity does not
  establish the provider's automatic trigger.
- **Work avoided:** observed sends or turns suppressed by a deterministic rule,
  compared with the same workload without that rule. Withheld checks depend on
  cadence and must not be multiplied by context size to claim tokens saved.
- **Continuity:** exact goal/revision, tasks and lane obligations, ownership,
  approvals with source, holds, unresolved decisions and result pointers.
  Preservation of files is necessary but does not prove the agent used them.

Recent structured observations demonstrate why these distinctions matter:
provider input can be largely cached, context can fall while a conversation is
active, and an accepted compact request can precede completion by a substantial
interval. Private audits retain identities, dates and raw numeric evidence;
public fixtures use fictional conversations only. None of those observations
establishes an optimal quota-saving threshold or cadence.

Prefer suppressing proven duplicate supervisor work first. Reuse an existing
handoff/result pointer and reconcile each revision once. Batch routine completion
receipts at the coordinator's next useful boundary; pending owner input, failures
and person gates must not disappear into a batch. Existing queued/active/child,
retry and idle-spacing safeguards remain in force. BB owns delivery of its own
parent notifications; this design does not suppress them by changing settings.

## Current manual trial protocol

A person may authorize exact-target trials using BB's supported structured
`bb thread compact <id> --json`. BB enqueues a compact turn; success from this
command means request acceptance, not completed compaction. Never clear context
as a substitute. Never stop active work to manufacture an idle boundary.

1. Record the explicit authorization and exact target outside the checkout.
   Resolve its current project/environment/provider identity and expected owner.
2. Save current continuity under private thread storage. Obtain the coordinator's
   current handoff if its existing ledger may be stale. Retain files and hashes
   plus their source/revision; do not rewrite another coordinator's obligations.
3. At a safe idle boundary, refresh exact-target status, queue, background work,
   pending interactions and context immediately before submission. Unknown,
   active, pending, stopping, archived or held-for-person targets defer the
   trial. Do not change controls, settings, model or effort. BB must still
   enforce eligibility at submission; these separate reads are not a transaction.
4. Persist an attempted intent before the one request. Bind the exact target,
   authorization, baseline, continuity and start time. An accepted response is
   `accepted`; a transport/lost response is `unknown`, never retry permission.
5. Read only structured lifecycle metadata to match the compact start/completion
   to the exact target and attempt boundary. Depending on provider, completion
   may be `thread/compacted` or a matching `item/completed` whose item type is
   `contextCompaction`. A context reduction or an idle status alone is insufficient.
   Older unrelated completion items must not satisfy a newer attempt.
6. Record a fresh post-completion context snapshot, timing and any available
   numeric usage/cache evidence. Separate subsequent work from compact overhead;
   if no compact-only counters exist, overhead remains unknown. Preserve context
   before/after as occupancy evidence only.
7. Verify continuity files still exist and reconcile their current revisions,
   pending tasks/lanes/approvals/holds and ownership with the resumed coordinator.
   Record file-integrity evidence separately from behavioral reconciliation.
   Send one result pointer after completion, not repeated status messages.

Use bounded waits rather than repeated status polling. If a target never becomes
idle, record its exact blocking status and queued work. If a request was accepted
but no matching completion is visible, retain the intent and hold the lifecycle
attempt for a person. Do not replay it or infer failure from a stale context
snapshot. The loop's independent progress/control behavior is unchanged.

## Source-only explicit compact helper

The packaged keepgoing skill supplies `scripts/compact.py`: dry-run by default,
explicit grant and `--execute`, one durable reservation per request before the
structured BB compact operation, nonwaiting private journal lock, fixed grant
bindings, explicit attempt/cooldown bounds, and no request replay after uncertainty.
Repeated pending/capped/cooldown invocations do no status/provider work. A
separate invocation reconciles a unique matching item lifecycle, post-context
and a declared continuity receipt. This declaration is not semantic verification.
It performs no supervision control writes and no creation/archival action.

The first slice requires complete assignment inventory, at most 32 direct lanes,
all idle with no queued/background work or pending input. Only a primary target
with a recorded owner/goal and existing BB cursor is supported. Reads catch up
at most 2,000 new events; transcript text is never inspected or retained. Missing
coverage or an oversized page refuses. BB readings without provider measurement
time remain freshness-unknown. Cooldown covers helper attempts, not all natural
compacts; the coordinator must review those before authorizing a request.

The workflow remains under review. Fictional native fixtures do not establish
installed/live acceptance or an automatic policy.

## Proposed opt-in compact lifecycle

Automatic compaction requires a dedicated reviewed implementation/contract
change. A config flag or preview is not that approval. The proposed minimum is:

- Default off; explicit person authorization binds loop, BB target/provider,
  continuity revision, bounds and policy revision. No implicit enrollment of
  other projects. Keep provider safeguards, owner settings and all route choices.
- A pure planner consumes structured evidence and returns defer, eligible or
  needs-person with stable reasons. Side effects stay in an adapter/command
  layer. Stop/pause, ownership drift, pending transfer, limits and person gates
  win before provider work. Unknown evidence always defers.
- Require an idle boundary, complete queue/interaction/child evidence and a
  verified continuity manifest. Active lanes may still create obligations and
  completion notices: explicitly preserve them and revalidate just before the
  attempt. Do not claim atomic fencing against independent host submissions.
- Use context growth, remaining useful work and expected future turns as reviewed
  inputs, with unsupported estimates labelled. Consider existing cache lifetime,
  cache-hit input, compact work and handoff/startup overhead. A recent natural
  compact is a reason to defer another automatic compact. No hourly/daily compact
  timer and no occupancy-only promise of savings.
- Persist one target-bound intent under existing ownership/serialization rules
  before invoking compact. Hard attempt caps and cooldown include unknown
  attempts; restarts and policy revisions cannot erase reservations. A policy
  cannot authorize unlimited retries. Exact limits require reviewed evidence;
  no numeric defaults are selected by this proposal.
- Reconcile matching structured completion, then post-context and continuity.
  Success does not resume a held loop. Unknown outcomes hold only lifecycle
  authority and require explicit reconciliation, never an automatic retry.
- Test changed target/model/account, controls arriving during preparation,
  concurrent submissions, interrupted publication, lost response, stale/duplicate
  completion and provider incompatibility with fictional fixtures. Prove the
  no-send/no-replay assertions fail without the relevant guards.

The current `context_window.reset_at` preview remains separate and cannot invoke
compact or create a conversation. No daemon, service or account change follows
from this document or the manual trials.

## Successors require a separate contract

Keep current fallback-only `bb.new_thread` behavior unchanged. A later successor
feature must explicitly revise that contract and define one owner at every stage,
opt-in scope, immutable attempt identity, capped/spaced creation, goal/task/approval
and lane-obligation transfer, unknown-outcome reconciliation, target verification
and continuity acceptance. Starting a fresh conversation and retiring supervision
are separate operations. Never blanket archive, infer completion from idleness,
or discard work because a lane disappeared.

Compare successor startup plus handoff work and lost cache reuse against continuing
or compacting the existing conversation. Without subscription-specific accounting,
report raw counters, occupancy and useful delivery separately; savings stay unknown.

## Acceptance still open

A diagnostics correction does not implement automatic compact policy. Manual
trials do not establish restart-safe automation, platform acceptance, cache/quota
savings, correct successor transfer or optimal thresholds. Record each trial's
exact identities and evidence privately; publish only fictional regression data.
