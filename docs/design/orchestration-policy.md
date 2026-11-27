# Useful-work orchestration and native overview

Status: **planned policy and acceptance contract**. This document introduces no
configuration keys, profile defaults, pool allocator, new UI or runtime action.
The portable core, CLI and browser panel remain in scope. The next desktop
experience targets native macOS; Windows/Linux desktop equivalents are deferred.

## What exists and what remains planned

| Capability | Source status | Remaining boundary |
| --- | --- | --- |
| Selected Accounts catalog and route/account references | Implemented | A label or explicit assignment does not verify the login used by a turn. |
| Automatic selected-context usage (#572) | Implemented, unreleased; explicit per-selection grant, shared caches, bounded cadence/backoff and menu/panel worker ownership | Matching installed client/CLI, opt-ins, live provider and lifecycle acceptance are separate. No automatic grant on update. |
| Ordered routes, fallback probes and opt-in return | Implemented within their documented adapter bounds | Not a balanced account pool or general lane allocator. |
| Loop/run references, direct-lane observations and partial usage diagnostics | Implemented in their documented scopes | Not complete descendant coverage, a task planner or proof of useful delivery. |
| Native macOS client | Menu-bar accessory with native menu actions and panel opener | No Dock settings or native HOME window. Native HOME/Accounts/settings are planned. |
| Shared intensity and pool allocation | Planned | Require the explicit policy, admission and acceptance below. |

The [roadmap](../roadmap.md#owner-priorities-and-implementation-boundaries),
[Accounts contract](../accounts.md) and [release evidence](../release-verification.md)
remain authoritative for availability. A merge, package build, installed update,
opt-in and live acceptance are different events. None proves the next one.

## Useful-work continuation

The external coordinator chooses tasks from the owner's authorized work and
judges results. Looptender supplies structured state, bounded supervision and
controls; it does not decide which product feature to implement.

After a phase settles, the coordinator should revisit the authorized backlog,
identify independent buildable slices, existing assignments and overlap, and
report implementation versus review/check/merge/install/verification gaps.
Completed assignments do not prove the roadmap exhausted. Creating more lanes
or producing more wakes is not itself useful progress.

A gate applies to its action. A fictional release awaiting publication approval
can remain held while an already authorized offline regression review proceeds.
Do not infer approval from idle lanes, a thread title, a plan, a timer, a message
from another agent or arbitrary conversation/transcript text. Do not repeatedly
ask the same approval question without a material change. If all remaining
work requires a person, state the concrete gates and evidence.

This does not clear an existing runtime hold or authorize a supervised session
to resume itself. Assignment/completion and stage evidence must bind exact goal
and review revisions. A stale or duplicated terminal receipt cannot settle a
new revision. New assignments remain the coordinator's authorized action, not
background conversation creation by looptender.

## Account pools and lane distribution

A catalog entry names an explicitly selected context. A route configures a model
and effort on an account/context. A verified account identity, an allowance
window and a route's ability to use them require their own structured evidence.
Several entries or routes can share one effective context or allowance; adding
their percentages together would invent capacity. Separate subscriptions must
not be assumed to share a window either. Provider freshness can remain unknown
even after a successful retrieval; scheduler heartbeat is not quota evidence.

Planned pool admission must establish:

1. Explicit membership and eligible configured routes, including the work each
   may receive. A catalog entry never enrolls itself or changes a login.
2. Supported context/account binding and allowance identity with provenance,
   measurement/retrieval times, window/reset and declared validity. Missing
   identity, plan, cost or access stays unknown; names are not identifiers.
3. Per-allowance reserves and independent spend/effort/concurrency bounds. A
   recent sample is an admission input, not authority to spend or move work.
4. Durable reservations shared by loops using an allowance, with one owner,
   revision fencing, bounded expiry/reconciliation and crash/unknown handling.
   A failed or ambiguous dispatch cannot immediately refund/reuse capacity.
5. Supported safe-boundary handoff, a verified assignment transfer and retained
   loop/goal/hold/history identities. P6 first-class lane ownership precedes
   general lane distribution. Unverified or active obligations cannot be
   silently moved or discarded.

BB selects account context per project, not per thread. A route's `account`
annotation cannot make a BB thread use that account. Unsupported per-thread
selection must be shown as unavailable, never simulated by relabeling. Only
adapters with supported explicit binding can participate in an account-specific
allocation. No credential reading/copying, account creation, permission changes
or silent provider/CLI login switching is introduced.

Preferred configured routes remain the ordering rule within eligible choices.
An allocator may not weaken existing refusal/restriction policy, treat an
unknown failure as a limit, or use another identity to evade enforcement.
Cross-provider/billing and same-provider consumer-pair policy must be explicit;
open provider-terms decisions remain open. No pooling or lane-distribution
implementation, default quota threshold or automatic route change is authorized
by this design document.

## Shared work intensity

Low, Medium, High and Unleashed are planned **configurable profiles**, not one
scalar that silently raises every limit. The names express a person's desired
pace; they have no built-in numeric defaults in this proposal. Unleashed remains
bounded by the reviewed caps and existing authority.

| Independent dimension | Required explicit scope and behavior |
| --- | --- |
| Work scope | Allowed projects, loops, goals/revisions and action kinds. No widening to unrelated work. |
| Holds and approvals | Existing pause/stop/person gates win. A profile never clears them or turns an unresolved approval into permission. |
| Spend | Metered-route permission, billing basis, per-turn/period ceilings and existing attempt accounting. Unknown billing/cost cannot be presented as zero. Defaults remain a separate decision. |
| Allowance reserve | Named window/context, protected capacity and fresh-data policy. A reset timestamp alone cannot restore availability. |
| Model and effort | Explicit allowed routes and effort ceilings/floors. No silent downgrade or claim that configured identity is observed identity. |
| Concurrency | Explicit maximum active work/reservations per loop and shared allowance/device scope. No unbounded lane creation or autonomous expansion of reach. |

The planned effective policy is the intersection of applicable owner, device,
loop, route and action constraints. Conflicts refuse the affected action; a more
intense profile cannot override a narrower bound. Changing cadence, effort,
spend, reserve or concurrency must be independently visible and configurable.
A lower profile must not implicitly cancel active work; any cancellation uses
its own existing reviewed control contract.

Before applying a profile, preview current and proposed values for each dimension,
affected loops/routes/allowances, required authority, stale/unknown evidence,
existing obligations and which actions would be admitted or refused. Bind a
confirmation to exact policy/configuration/goal revisions and expire it on drift.
Applying a shared preference does not opt accounts into polling, authorize login,
release a hold, raise a budget or launch work as an accidental side effect.
Implementation must define storage/migration, reservations and rollback/partial
failure semantics before exposing a control.

Stale data permits truthful retained display, not increased admission. Unknown
or conflicting quota/identity/spend evidence refuses the affected new allocation;
independent already-authorized work follows its existing policy. No automatic
catch-up burst, new probe or provider refresh is implied by moving a slider.
Use the existing explicitly granted polling path where eligible; make missing
fresh evidence and the next known read visible.

## HOME and Accounts

HOME is a planned overview of loops, their recorded lanes, accounts and configured
fallback relations. Keep distinct visual and structured relations for:

- Observed supervision/ownership and verified recorded lane membership.
- Configured route order/fallback edges, which do not establish health or a move.
- Owner-assigned account references versus verified context/allowance bindings.
- Unknown/stale/unavailable relationships, incomplete coverage and retired history.

Do not invent relationships by matching titles, paths or account labels. A
fallback configured but never used remains a configured edge; an unsupervised
lane must not look supervised merely because its parent is registered. Preserve
reference identity across rename/reorder. Provide a keyboard/screen-reader list
with the same semantics, plus a legend and age/provenance details. Unknown is
not an empty graph or zero work. Bounds must cover large registries, partial
reads and offline snapshots without graph-driven provider work.

Accounts remains a separate useful list for sorting, filtering, grouping and
selection details. Persist explicit presentation choices separately from route
policy. A selection, filter or group cannot enroll a route or create a pool.
Quota retrieval, automatic-polling grants and sign-in retain their own reviewed
controls. Do not bury missing freshness or selection-change evidence in the graph.

Build native macOS HOME, Accounts and settings first. The existing accessory is
not already a Dock settings app. Define window/process ownership, reopen/quit,
sleep/Low Power Mode, accessibility, stale/offline behavior and explicit optional
login startup before claiming native acceptance. Opening/closing settings must
not create a supervisor or duplicate the existing usage worker. Preserve the
tracked-menu structural guard. Portable core and browser-panel work continue;
deferring other desktop equivalents does not defer their core correctness tests.

## Acceptance and build order

Every item remains open. Each slice needs focused fictional fixtures and a
separate record of released, installed, native and live evidence.

1. **Truthful projection first:** keep catalog/routes/identity separate; verify
   graph/list meanings with renamed, missing, stale, held, offline and incomplete
   records. Existing polling success/failure times remain unchanged by reads.
2. **Pure policy and preview:** version the independent intensity dimensions;
   table-test scope intersection, stop/hold precedence, unknown billing/quota,
   stale confirmation, policy conflict and no implied runtime effects.
3. **Pool admission and reservations:** establish supported bindings and P6 lane
   ownership; test shared versus independent windows, two loops competing,
   reserve exhaustion, reset without fresh evidence, crash/restart, duplicate
   receipts, failed/unknown dispatch and no identity/model relabeling.
4. **Reviewed application:** reuse existing guarded commands only where the
   adapter supports them. Verify no permission, hold, spend or concurrency cap
   can be widened implicitly by a profile, UI event or resumed process. Explicit
   profile changes stay within separately authorized hard ceilings. Conversation
   creation stays within existing explicit opt-in paths and caps.
5. **Native macOS journey:** implement HOME/list/settings after their contracts;
   verify keyboard/screen reader, stable menu tracking, window lifecycle, dark/
   light/narrow layouts, no duplicate workers and private metadata handling.
6. **Useful delivery and live acceptance:** a fictional backlog has completed
   assignments plus independent work, an action awaiting a person, a stale result
   and a failed verification. The coordinator continues only authorized work and
   reports gaps. Separately obtain authorization for installed/provider and
   lifecycle walkthroughs; no fixture or doc update checks these off.

Dependencies include [preferred routes and lanes](../roadmap.md#preferred-routes-planned),
[bounds and portable goals](../roadmap.md#8-bounded-work-and-portable-goals),
[run discovery](../run-discovery.md), [configured stages](../roadmap.md#9-independent-review-and-configured-stages-planned)
and [account polling](../accounts.md#automatic-selected-context-usage-unreleased).
This plan does not add a planner, review runner, approval oracle, automatic
archive policy, provider request or second scheduler.
