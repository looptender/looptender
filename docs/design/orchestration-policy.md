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
| Configurable work sources and coverage | Planned; existing goal references are opaque descriptive metadata | First slice is read-only bound inventory, not source discovery, prose parsing, a new ledger or task admission. |
| Inefficiency findings and shared intensity | Planned; existing diagnostics supply partial evidence only | Findings/recommendations need the bounded evidence contract below; no new adjustment authority. |
| Pooling first slice | Planned pure `internal/poolplan` preview only | Codex structured fresh bound evidence, explicit account/allowance IDs and complete occupancy input; no reader, CLI, reservation, adapter binding or dispatch. |

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

## Configurable work sources and bound coverage

**Planned:** repository docs are the default work source. Optional adapters may
later expose issues, projects, structured manifests or an existing work ledger.
This describes a product direction, not an implemented Markdown reader or source
adapter. Discovery can suggest sources/items; suggestions establish no authority.
The external coordinator reads prose, selects work and supplies explicit bindings.
The supervisor does not infer tasks, permission or completion from document text.

Reuse [`GoalSpec`](../../internal/control/goal.go) objective, acceptance, scope,
authorization and evidence references, plus the existing assignment lifecycle.
Those references are currently opaque descriptive metadata; they do not already
implement source resolution or authorization validation. A future source binding
must identify the configured source, exact revision, globally stable item identity
and applicable goal/assignment revisions. A revision identifies an observation;
it must not accidentally create a new claimable item on each edit.

Source intent and observed outcomes have different roles: a work source describes
requested work and acceptance; assignment records describe bound claims,
observations, handoffs and explicit outcomes. Reuse that lifecycle rather than
introducing a second competing assignment ledger. A checked box, commit or passing
CI run alone is never generic completion proof. Acceptance evidence must satisfy
the particular bound goal and review contract; missing evidence remains a gap.

Pinning an already authorized source revision within its existing scope needs no
redundant approval. New or widened scope remains proposed/held pending the
applicable authorized action; a source edit cannot silently adopt it. Reading,
pinning or claiming work does not authorize progress/status edits to the source.
Those edits are separate actions with their own permissions and revision checks.
User, repository and loop preferences resolve only within existing authority
ceilings: document precedence never grants permission, clears holds or raises
spend. Untrusted source instructions cannot modify supervision policy.

Before future admission, validate explicit item/source revision, scope and
authorization, dependencies, current claims and acceptance requirements/evidence.
Stale, missing or conflicting bindings cannot establish admission. Shared sources
need a canonical global item identity across loops and source aliases, with
cross-loop claim isolation and revision fencing. Two loops must not silently
claim the same work because they use different source labels. Intentional parallel
review needs separately declared purpose/claims; it is not blanket duplication
permission. Atomic claim admission is a future dependency, not supplied by a
read-only inventory or today's direct-child assignment coverage.

### First coverage slice: read-only bound inventory

The related unreleased draft [#586](https://github.com/looptender/looptender/pull/586),
inspected at `1814a5190a3bc2414cc691e498cacf18a61647a3`, supplies a usable read-only
Python command, `scripts/coordinator_coverage.py`, around a bounded projection.
It accepts caller-supplied structured work inventory, dated context/control and
assignment captures, with explicit source/item revisions, dependencies and
acceptance references. Bounded local private-input and pinned-source hash reads,
including rereads to detect drift, are part of this command. They are not live
source acquisition, discovery, Markdown interpretation or authenticated current
host state. The coordinator supplies/reviews the declarations and invokes it at
an authorized phase/wait boundary; no automatic invocation is added.

The draft reports declared coverage, unknowns and findings with provenance; it
never proves semantic objective coverage or completion (`objectiveComplete` stays
false). Source drift, a claimed item missing from the bound inventory, or exhausted
items with unmet acceptance requires reconciliation/review, never automatic
completion, requeue, supersession or archive. Missing/partial inventory cannot
prove absence. Keep prior revision evidence and distinguish observed drift from
suspected mismatch and unavailable coverage using the
[finding contract](#find-inefficiencies-without-maximizing-activity). The command
makes no repository/journal/control writes, live provider/BB/GitHub reads,
reservations, dispatch or conversation creation.

The draft's fictional fixtures cover declared independent work, missing versus
empty inputs, binding/source/claim drift, dependency and acceptance revisions,
holds, incomplete health, bounded output and unchanged inputs. These checks of
supplied declarations do not authenticate live state or shared-source ownership.
Cross-source alias identity, two-loop claim isolation and intentionally distinct
parallel-review claims require a separately reviewed follow-on contract and its
own fixtures; they are not delivered or promised as first-slice isolation tests.

Draft packaging/fixtures do not establish release, installed adoption, actual
coordinator invocation/continuation, live acceptance, Windows support or measured
throughput improvement. Source discovery/parsing, atomic shared claims, pool
admission and HOME remain separate work; this command grants them no authority.

### Planned use case: reusable documentation upkeep

A project may explicitly adopt reusable documentation obligations in its
configured authorized work source. Adoption must bind the project/repository,
policy/source revision, allowed change scope, responsible agent/assignment and
acceptance references. Installing a looptender update, discovering a repository
or reading its docs does not adopt or enable this policy. Cross-project reuse
shares the obligation template, not permission or claims between projects.

For each bound change, the agent records documentation impact and affected guides,
or a justified no-change decision tied to that exact change/review revision.
Release-facing work must align documented behavior, version/release evidence and
limitations, distinguishing source implementation, published artifacts, installed
builds and native/live verification. A successful build, version string or docs
edit alone cannot close acceptance. Missing or stale verification and unmatched
release/version evidence produce scoped findings; incomplete coverage remains
`cannot_assess`, never inferred compliance or a demand to publish.

For looptender, coordinator-owned upkeep explicitly includes the actual public
product website, [looptender.com](https://looptender.com), alongside repository
guides. Behavior and release changes trigger an impact review of product claims,
feature availability, downloads/install instructions, version/platform support,
screenshots and links, or a justified no-change result for those surfaces. Website
drift belongs in per-change impact and release acceptance evidence. A feature
implemented only in source must not become a released or installed product claim;
screenshots must identify the evidenced experience rather than imply missing
native, provider or rollout acceptance.

Maintaining website content is within the coordinator's authorized duties, with
agent-owned edits and the applicable review checks. This does not request a
deployment or authorize automatic publication. Record the canonical website
source/repository, relevant revision and deployment owner when verified; those
bindings are not established by this contract. Do not infer the site architecture,
repository or publisher from the domain or this supervisor checkout. Missing
bindings/verification remain an explicit upkeep gap, not permission to deploy.

**Bounded follow-on:** represent these obligations as structured work items in
the same bound inventory and goal/assignment lifecycle, with impact/no-change and
release/verification evidence references supplied by the authorized agent. Add a
read-only coverage projection for missing, stale or mismatched references; reuse
source/item identities, revision checks, claim isolation and existing findings
rather than creating a docs ledger, parser, scheduler or second policy engine.
Looptender reports coverage; the agent owns repository edits, review and any
separately authorized publication. The supervisor never edits repositories,
auto-enables policy or infers acceptance. Source progress edits remain separate
authorized actions; a finding grants no new scope or spend.

Fictional fixtures must cover explicit versus absent adoption, installation
without adoption, two projects sharing a template with isolated claims, changed
source/change/review revisions, a bound justified no-change result, missing/stale
verification and source-versus-release/installed version mismatch. Include website
claims, download links and screenshots drifting from bound release evidence, plus
unverified source/deployment ownership that cannot authorize publication. A docs edit or
passing CI with unmet acceptance must remain open. This is planned follow-on to
work-source/coverage mechanisms, not delivered upkeep automation, site implementation,
scheduling or a new recurring paid lane.

## Find inefficiencies without maximizing activity

Finding inefficiencies is a core looptender responsibility in the planned
orchestration work. The purpose is more useful, verified work with less avoidable
rework and overhead, not more tokens, higher utilization or more active lanes.
Current usage/status diagnostics are partial inputs, not this complete detector.

| Finding | Structured evidence needed | What remains suspected or unknown |
| --- | --- | --- |
| Idle configured capacity with authorized backlog | Current explicit authorized backlog item/revision, eligible route/account evidence, fresh allowance and complete occupancy/hold snapshot | Free quota or an idle process alone proves neither usable capacity nor permission to start work. |
| Stranded loops | Known owner/run binding, completed-check/heartbeat ages, next-check source and explicit control/hold/transfer state | Missing or old snapshots are unavailable evidence, not proof the loop is abandoned or may be taken over. |
| Duplicate assignments, overlap or repeated checks | Exact assignment/goal/revision/action IDs, configured repository/ref path intersections and completed-check records | Shared paths suggest collision risk; they do not prove duplicated intent. Repeated reads may be necessary. |
| Status-only wakes and context reload costs | Typed wake/check outcome, send kind, delivered goal form/bytes, supported usage counters and correlation IDs | A wake with no recorded change is observable; wasted tokens or lost quality require compatible measurement. Never inspect prompt/transcript text to decide. |
| Model mismatch and rework | Configured policy and supported observed route/model identity, revision-bound external acceptance/rework results | A name/alias mismatch or failed result does not prove the model caused it. Causal attribution needs comparable accepted work and cost/quality evidence. |
| Review/CI bottlenecks | Pinned head/base, structured review/check identities and states, dependency and queue ages | Waiting is observed; the reason it could safely be shortened may be unknown. Do not infer review acceptance from comments or head-only CI. |
| Stale or redundant polling | Grant/selection/window identity, attempt/retrieval/provider-freshness times, scheduler records and cadence/backoff | Scheduler liveness is not a successful read, and retrieval age does not establish provider freshness. A reset clock alone does not restore capacity. |

Each finding must carry its kind, exact scoped identities/revisions, observation
period, source and capture/retrieval times, coverage/completeness and missing or
contradictory inputs. Separate an **observed fact** from a **suspected explanation**.
State confidence and its basis (direct structured observation, correlation or
controlled comparison); unknown is a valid result. Do not invent numerical
confidence, convert a heuristic into an attestation or silently promote an old
finding to current state. Age, deduplicate and retire findings by identity/revision.

Publish detector coverage and assessability **per loop and detector class**,
bound to the snapshot/goal/source revisions and observation period. A missing
work source, dependency observation or CI observation must display `cannot_assess`
with the missing input, not healthy, no work or no finding. A detector that was
not run is also unassessed. An assessable class with no finding says only that
its specific predicate was not observed within the declared coverage.

All recorded lanes terminal, an explicit declared wait and missing acceptance
may support a suspicion of stalled useful work only with a complete, fresh,
healthy structured snapshot of the relevant work, dependencies, claims and
controls. Even then, these facts never prove abandonment, completion, wasted
work or permission to dispatch. Partial or unhealthy coverage is `cannot_assess`,
not a stronger suspicion. Do not infer a declared wait from prose or silence.

A recommendation must name the affected action, evidence, expected useful-work
benefit, uncertainty, prerequisites, authority needed and the check that would
confirm improvement. Examples include reviewing an apparently stranded owner,
consolidating overlapping assignments, reducing a separately authorized check
cadence, or trying an already allowed route at a safe boundary. These remain
recommendations until an applicable existing policy explicitly permits the exact
adjustment. A detector does not select new scope, grant spending, raise effort or
concurrency, clear a hold, move accounts, create lanes or change polling grants.

Measure the detector too: count source reads, CPU/time, bytes, wake/context reload
work, provider requests and any metered overhead where known. Prefer already
recorded bounded evidence; detection must not generate unbounded status polling
or spend on probes to find waste. Publish retention/sampling bounds and unknown
coverage; stale inputs cannot justify a more aggressive adjustment.

Evaluate useful outcomes where possible: revision-bound accepted work, time to
verification, rework/reopened findings, duplicate actions avoided and comparable
resource/measurement overhead. Show before/after periods, workload/quality changes
and coverage; use controlled comparisons before causal savings claims. Throughput
or rework improvements can remain unmeasured. Token volume, idle capacity and
utilization are supporting observations, never the optimization target by themselves.

### Planned retrospective missed-detection records

A future local-first retrospective may record a suspected missed detection:
detector class/version, loop and bound revision, expected assessability, the
specific missing input or predicate gap, minimized structured evidence and a
fictional replay describing the expected observable result. A replay is a
reviewable hypothesis: the supervisor must not manufacture semantic truth for a
fixture or treat a report as acceptance. Preserve unknowns and contradictory
evidence, use local deduplication and explicit retention/size/rate limits, and
avoid recurring paid lanes or new polling to populate the record. Never publish
private evidence automatically; fictionalization and disclosure need review.

An optional improvement proposal can enter the explicit authorized work-source
workflow through an agent. The proposal identifies the gap, authority needed,
minimized evidence, candidate acceptance and tests; a gap proposal itself is not
authorization. Source progress edits remain separate actions. The supervisor
never edits repositories, opens/merges PRs or installs fixes. An external agent
may implement/review/merge only under the applicable repository and owner rules;
this research introduces no blanket human-only merge requirement. In this
repository, existing authorization for agent merge still requires exact-head/base
checks and resolved review under the documented merge contract.

This retrospective/proposal pipeline is follow-on planned work, not part of the
current coverage-command and documentation scope. It adds no worker, automatic
source edits, recurring paid review lane or automatic remediation. Focused future
fixtures must distinguish missing input from healthy/no-work, deduplicate repeated
gaps locally, enforce bounds/redaction, and reject a proposal or fictional replay
as authority or completion evidence. No private transcript inference is allowed.

### Reconcile research with existing source

External orchestration research is design input, not implementation evidence or
authority. The research did not inspect these internals. Reuse the following
source contracts rather than importing a replacement task store or state machine:

| Research assumption/proposal | Source correction and retained boundary |
| --- | --- |
| No durable account inventory | [`internal/accounts`](../../internal/accounts/accounts.go) already stores a bounded private selected-context catalog with revisions and observations. This is not verified turn identity, an allowance reservation ledger or permission to pool. Automatic polling is source-implemented, not merely an unverified proposed collector. |
| No assignment ledger or completion evidence | [`control/assignment.go`](../../internal/control/assignment.go) already records bindings, revisioned rows, scope/authorization references, lane inventory, handoffs and acknowledgements. [`assignment_wake.go`](../../internal/control/assignment_wake.go) records readiness/attempt/reconciliation; [`assignment_completion.go`](../../internal/control/assignment_completion.go) and the [command guard](../../cmd/looptender/assignment_completion.go) enforce explicit completion evidence. Reuse these records; they are not a general leased task queue or automatic acceptance oracle. |
| Every wake has a complete outcome and measurable reload cost | [`state.IdleSpacing`](../../internal/state/state.go) records unchanged-wake evidence only where enabled; the [incident journal](../../internal/incident/record.go) has bounded retention and collapsed repeated checks. Existing usage fields have source/coverage limits. Missing history or cost stays unavailable; retained counts do not establish total tokens, idle-capacity hours or waste. |
| A completed assignment can automatically become superseded; replacements can be retired or archived | Do not import these transitions. Existing explicit revision/owner-bound assignment and transfer operations retain their own contracts. A finding cannot change complete to superseded, infer acceptance, cancel/archive work or classify silence as terminal. |
| Read-only findings can immediately unlock automatic dispatch | Initial findings are observation/recommendation only. No report action tier, intensity label or inferred efficiency creates thread/lane dispatch authority. Existing conversation creation remains limited to authorized opt-in paths and their bounds; no background creation or automatic backlog dispatcher is added. |

Useful research requirements retained here are evidence-linked findings, explicit
confidence and unknowns, one actionable recommendation, degraded-source gating,
measured analysis overhead, and outcome/guard metrics. Do not import example
lane-count defaults, numerical waste estimates, provider claims or full graph
coverage without source and acceptance evidence. In particular, an unsuccessful
assignment is not automatically wasted work, and no reserved-capacity denominator
exists merely because a report proposes one.

### Next independent implementation slice: read-only efficiency findings

**Planned first slice:** a pure `internal/efficiency` analyzer, separate from
`internal/poolplan`. It accepts caller-supplied bounded snapshots and returns
findings; it has no reader, CLI, background worker, persistent findings store,
provider/CI request, recommendation execution or control mutation. This can be
implemented independently of pool reservations, native HOME and the Sessionpipe
lane. It must not add another assignment/catalog store.

Exact existing inputs and initial outputs:

- **Recorded unchanged wakes:** the existing `state.IdleSpacing` fields
  `LastWakeAt`, `Unchanged`, `Facts`, `WaitUntil` and `Wait`, with the containing
  state's `CheckedAt`/`CheckFinishedAt`, target identity and an explicit current
  caller-supplied binding/capture time. [`supervise/idlespacing.go`](../../internal/supervise/idlespacing.go)
  defines the structured digest and when counts are recorded. At an explicitly
  supplied threshold, report the recorded count and period/scope, with suspected
  inefficiency rather than proven wasted work. Feature off, missing digest,
  unavailable/future/stale timestamps or unverifiable binding means cannot
  assess, not zero waste. Do not compare or expose private prompt text.
- **Assignment attempt awaiting reconciliation:** reuse
  `control.AssignmentSummaryOf` and `AssignmentWakeSummaryOf` projections from
  already validated current-binding records: summary availability/revision,
  inventory coverage/observed time, and coordination `State`, `AttemptedAt` and
  `ReconciledAt`. `attempted_unreconciled` with a valid timestamp can establish
  the age of a recorded attempt awaiting reconciliation, not that delivery
  failed or a lane is stranded. Recommend the existing private assignment-review/
  `reconcile-wake` flow; never consume a grant, resend, renew a claim or mark an
  assignment complete/superseded. Direct-child coverage is not full occupancy.

The caller supplies `now`, explicit age/count thresholds, an opaque subject key
and binding/revision provenance; the library grants these no authority. Missing
or contradictory provenance yields an explicit unavailable reason. Initial
results contain deterministic finding identity, class, observed facts, suspected
explanation/confidence basis, source times/revisions, coverage/unknowns, primary
recommendation, verification metric and bounded analysis counters. Auto action
is absent. Cost/throughput estimates remain absent unless compatible evidence
actually supplies a denominator. Do not mint a second cost or acceptance model;
use the [measurement contract](usage-measurement.md) where applicable.

Proposed focused tests in `internal/efficiency/*_test.go`:

1. Threshold and age boundaries with injected time; feature off, zero/missing/
   future/rollback/stale evidence and changed owner/target/goal/revision return
   cannot assess, never a claim of idle capacity or abandonment.
2. Unchanged-wake observation reports only the recorded counter; differing
   digest/revision cannot be merged into one finding. Missing counters do not
   become zero, and context bytes never become estimated tokens or money.
3. Unreconciled attempt reports its age; reconciled/changed revision suppresses
   that exact finding. Partial/missing inventory and unknown availability retain
   their unknowns; no assertion of failed dispatch, completion or available lane.
4. Repeated identical input produces one stable finding; changed evidence makes
   a distinct revision. Deterministic ordering, explicit bounds, input deep-copy
   invariance and secret canaries prove no mutation or reference/text leakage.
5. Negative controls that remove freshness/binding checks or treat missing data
   as zero must fail the boundary fixtures. No IO/process/clock access belongs
   in the pure package; no provider fixture or broad suite is needed initially.

This first slice measures its own inspected-record/finding counts and keeps
outcome improvement unmeasured. Later reader/UI wiring must prove snapshot
binding, bounded overhead and privacy at that boundary. Additional classes need
specific structured sources: backlog/occupancy for idle capacity, explicit work
identity for duplicate assignments, and pinned repository/CI records for waits.
They do not become assessable because this first analyzer exists.

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
   Ingestion must retain immutable observation/reset provenance and explicit
   window-instance identity. After a known boundary, require an observation captured
   no earlier than that ended reset; unknown provenance/boundary is
   incomplete evidence. A reset alone never clears recorded exhaustion.
3. Per-allowance reserves and independent spend/effort/concurrency bounds. A
   recent sample is an admission input, not authority to spend or move work.
4. Durable reservations shared by loops using an allowance, with one owner,
   revision fencing and bounded reconciliation for expiry/crash/unknown outcomes.
   A failed or ambiguous dispatch, expired lease or missing heartbeat cannot
   release charges or authorize requeue. Selection/authentication changes
   invalidate bound observations and grants but retain existing charged
   reservations under their original identity/generation until explicit
   reconciliation; never project old charges through a newly selected route.
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

### First pooling slice: pure preview

The first implementation slice is scoped to `internal/poolplan`, a pure preview
accepting caller-supplied structured evidence. Its initial supported evidence is
Codex only: fresh quota bound to the explicit account/context and allowance IDs,
with compatible window identities and a complete occupancy snapshot for the
proposed scope. Unknown/stale/conflicting identity, quota or occupancy must refuse
an allocation preview or return explicit unavailable reasons. A partial list of
busy loops is not a complete occupancy snapshot; a selected catalog label is not
an account binding. No other provider is inferred compatible.

In existing Codex source, `QuotaAttempt.At` is local post-RPC/parse time,
projected as `observedAt` and age-classified `fresh`/`stale`. It is not a verified
provider measurement timestamp, and a recent retrieval does not prove provider
freshness. Here, fresh bound evidence means evidence satisfying its explicitly
stated age/provenance policy; it must not silently promote that local timestamp
to provider attestation. The additive provenance contract is separate planned
work; no runtime evidence assembler or operational pooling is established here.

Preview capacity is measured in managed-work concurrency slots, charged against
the global/account/shared-allowance bounds. Never convert remaining quota
percentages into estimated turns, tokens or slot inventory. A reserve threshold
is an admission condition, not a spend limit or a guaranteed residual balance;
unknown/metered billing remains excluded from this initial preview. Outcome per
reserved capacity requires an explicit compatible unit and scope, not summed
provider percentages.

The pure planner cannot authenticate caller-generated evidence or establish that
a reset/window identity was not rewritten around an old observation. The future
ingestion boundary must enforce immutable provenance and the known-boundary
observation fence above. A complete local ledger can fence managed writers only
after atomic reservation exists; unrelated clients can still consume the same
account. Fresh observations may become obsolete between read, reserve and
dispatch, so neither a preview nor local reservation guarantees external quota.

This slice has no evidence reader, CLI, worker, configuration write, runtime caller,
account selection or dispatch. A suggested allocation is not a reservation,
successful switch or promise of available capacity at execution time. Atomic reservations,
actual supported adapter/account binding, execution-time revalidation and P6 lane
ownership remain future dependencies before any account switching or work moves.
Test deterministic previews with stale/unknown evidence, shared windows, reserves,
complete/partial occupancy, conflicting IDs and concurrent-demand examples.
Future ingestion/ledger fixtures must reject rewritten reset provenance and
pre-boundary observations, retain charges across selection/authentication drift,
and refuse release on timeout/unknown outcomes. Pure caller-input fixtures do
not authenticate observations or establish a live pool/account switching.

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
| Allowance reserve | Named window/context, explicit admission threshold and fresh-data policy, without a guaranteed external balance. A reset timestamp alone cannot restore availability. |
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

### Interface research reconciliation and unresolved actions

Interface research supplies recommendations and fixture ideas, not new
permissions or proof of the installed experience. The existing owner requirements
are useful HOME/Accounts views, honest freshness/build identity and actionable
status within the authority bounds above. Proposed issue ledgers/tombstones,
transport changes, numerical latency/resource targets and generalized recovery
are additional design candidates; they require scoped contracts and measurement
before adoption, not an automatic expansion of this plan.

The browser-panel presentation in draft [#587](https://github.com/looptender/looptender/pull/587),
inspected at `f6d0d292d2f30e92df2293e73f5df1d5828be4bf`, adds a setup map,
categorized attention/task drawers, Accounts summaries and client reconciliation
against supplied snapshots. This is a source-only draft, not native HOME, a new
collector or verified external resolution. Optional fields and fictional DOM tests
cannot establish that the backend emits those fields or enforces their semantics.
The native menu/accessory and proposed app window remain distinct surfaces.

| Contract gap | Remaining implementation and acceptance boundary |
| --- | --- |
| G9: question identity and stale-action preflight | Structured question/target/owner revisions and supported question links must come from authoritative backend evidence. Client fingerprints are presentation guards, not server-enforced question freshness. Existing control-record tickets do not supply the missing question revision contract. |
| G10: external question resolution | The client cannot itself clear BB `awaiting_user`. A separate explicitly enabled supervision policy would need a supported structured signal for the exact awaited question and must preserve every independent manual, stop, deadline or other hold. Missing/unreadable resolution stays unconfirmed; no timeout, click, prose match or disappeared item in an incomplete read establishes clearance. |
| G11: deadline and completion actions | Reviewed deadline extension and panel completion are not delivered by task drawers or links to existing commands. They need separately authorized backend operations, exact effects/revision checks and acceptance evidence; deadline or finished status is never inferred from UI interaction. |
| G12: recovery and diagnosis | Rendering a supplied `recovering` field does not implement recovery policy, scheduler restart or a reliable overdue-cause contract. Keep unknown cause explicit; diagnostic reads and any later recovery action need their own bounds, ownership and verification. |

Bind actions to the affected issue/question, target, owner, control and relevant
policy revisions, with backend preflight and locked apply where supported. Reject
replaced questions and relevant drift; an unrelated loop update or global
snapshot sequence increment must not by itself invalidate an otherwise unchanged
action. Preserve independent holds and do not silently retry an unknown outcome.
A fresh complete authoritative observation may reconcile a displayed condition;
it cannot grant a missing auto-clear policy or erase a different condition.

Keep view-read/render time separate from source observation, retrieval, last
success/failure and scheduler state. Preserve each source's unknown age basis;
reset passed is not available capacity, and a build label is not an embedded
source revision. Running-process identity and installed files are separate facts.
Do not replace existing bounded caches, selected-context grants or single-owner
polling with a new collector because a report proposes one. SSE, a new snapshot
sequence, tombstones and leader-tab coordination are options to evaluate only
against demonstrated gaps, authentication, ordering, privacy, power and fallback
requirements. No streaming endpoint or new worker is required by this document.

Proposed acceptance must distinguish old responses, replaced questions, separate
remaining holds, partial/unreadable snapshots and unsupported resolution APIs.
Include the negative case where a client sees optional future fields but the
backend cannot enforce them, plus an unrelated-loop update that leaves a valid
target-scoped action intact. Native menu tracking, wake/Low Power Mode, window
reopen, VoiceOver/contrast and installed/live provider results remain separate
acceptance. Report latency, graph-scale and CPU/memory numbers as unverified
proposed targets until measured; presentation fixtures do not close these gaps.

## Acceptance and build order

Every item remains open. Each slice needs focused fictional fixtures and a
separate record of released, installed, native and live evidence.

1. **Truthful projection first:** keep catalog/routes/identity separate; verify
   graph/list meanings with renamed, missing, stale, held, offline and incomplete
   records. Existing polling success/failure times remain unchanged by reads.
2. **Bound coverage, inefficiency evidence and pure preview:** fixture-test the
   read-only bound inventory before any source reader or claim admission; retain
   global item identity, partial-coverage unknowns and unmet acceptance findings.
   Separately fixture-test observed versus suspected
   findings for every class above, incomplete/stale inputs, recommendation authority
   and measurement overhead without new provider work. Keep `internal/poolplan`
   pure and Codex-evidence-only with complete occupancy; no reader/CLI/dispatch.
   Version the independent intensity dimensions;
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
