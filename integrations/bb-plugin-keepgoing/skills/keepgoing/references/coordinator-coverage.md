# Read-only coordinator coverage (unreleased)

Use this agent-operated command after a phase/handoff review and before declaring
that waiting is justified. It detects **declared** independent work left behind
while a coordinator waits. It does not replace the assignment lifecycle, catalog,
existing delegation grant or supervisor. It never invokes BB, GitHub, a provider,
a model, a control command or the spawn workflow. It reads explicit local files
only and makes no runtime/journal/repository writes.

```sh
python3 -B scripts/coordinator_coverage.py \
  --inventory /private/example/coverage.json \
  --context /private/example/context.json \
  --assignments /private/example/assignment-capture.json \
  --boundary wait --private
```

All three JSON files must be absolute, regular, nonsymlink, owner-private files
(0600 on Unix). Python 3 with the existing coordinator scripts is required;
Windows is unsupported. Inputs are explicitly prepared/reviewed private captures,
not automatically acquired or authenticated current host observations. Capture
`assignment show --private` under existing authority and record the matching
binding, revision, dated controls and health in `context.json`; do not substitute
an invented “enabled” capture for a real runtime hold. The helper does not call
those commands, update capture timestamps or repair records for you.

Default output is minimized counts, fixed reasons and opaque digests. `--private`
includes item/source IDs and references. Save a receipt outside repositories if
needed; never publish private receipts, authority sources or captures. `--previous
/private/example/previous.json` compares a previous receipt fingerprint only
**after** fresh input validation; `unchanged:true` neither marks progress nor
changes the verdict/exit status. No timer, extra wake, polling or replay is added.
`--execute`, `--child` and other mutation flags are rejected.

Exit 0 means only `declared_wait_supported`: every declared open obligation is
currently covered by a declared dependency/person/external wait. Exit 1 means
known work remains, completion review is needed, a hold applies, or inputs cannot
be assessed. Exit 2 is invocation misuse. Exit 0 is **not** objective completion,
merge approval, provider status, dispatch permission or proof of live idleness.
`--boundary phase` uses the same evidence and emits an availability finding in
place of a waiting finding. `objectiveComplete` is always false.

## Structured evidence and schema

The complete fictional fixture is `fixture()` in
`tools/coordinator-workflow/test_coverage.py` in the source repository. No Markdown
parser or default source discovery is included. The coordinator interprets prose
under existing instructions and supplies explicit IDs/edges; the helper validates
the resulting declarations without reading prose to infer intent.

`inventory` has exactly these keys:

| Field | Meaning |
| --- | --- |
| `version`, `id`, `revision` | Version 1; nonempty private inventory identity; positive scope revision. |
| `binding` | Exact existing assignment binding: `loop`, `config`, `runtime`, `coordinator`, `owner`, `generation`, `goal`, `goalRevision`. String identities and positive generations/revisions must match the context and assignment capture. This is cooperative binding, not authenticated authority. |
| `assignmentRecord`, `assignmentRevision` | Expected current record ID and revision from the private assignment capture. |
| `scopeComplete` | Boolean declaration that the **supplied** source-item set was reviewed for this scope. False means incomplete; true is not semantic completeness proof. |
| `workSources` | Up to 32 pinned `{id,revision,path,sha256,itemIds}` records. `itemIds` explicitly lists declared items interpreted from that source, independently of assigned lanes. IDs must have one coverage row each. Empty pinned `itemIds:[]` differs from missing/unreadable sources. |
| `authoritySources` | Up to 32 `{id,revision,path,sha256}` pins for existing owner/project authorization evidence. Source discovery alone does not authorize work. |
| `items` | At most 32 unique declared rows, including unassigned work and coordinator-owned actions. No new runtime ledger or claim is created. |

Source paths must be absolute regular nonsymlink files. The command hashes their
bytes; it does not parse Markdown, interpret checkboxes or execute repository
text. A source edit/drift is a finding requiring repinning and scope
reconciliation. It does not invalidate existing authority merely because an
ordinary progress edit changed a document, and never requests redundant approval
just because a work source is repository docs. Permission-relevant scope expansion
must remain held; neither `scopeComplete` nor untrusted text changes permissions,
spend, holds or accounts.

Each `items` row has exactly:

- `id`, positive `revision`, and `workSource:{id,revision,sha256}` matching its
  pinned source and declared source item set; nonempty `scopeRefs`.
- `authorization:{state,refs}` where state is `authorized`, `unknown` or
  `scope_expansion`. Authorized work requires nonempty refs to current pinned
  authority sources. This records already-granted authority; the detector grants
  none and does not judge prose.
- `dependsOn:[{id,revision}]` for explicit current dependencies. `[]` explicitly
  declares no dependencies; `null` means dependencies are unknown. Missing fields,
  unknown targets, stale revisions, cycles or unaccepted dependency results cannot
  justify waiting or unlock downstream work. No global CI barrier is inferred.
- `personGate:{state,refs}` where state is `none`, `held` or `unknown`. A held
  action requires evidence refs and blocks that item only. Actual runtime holds
  take precedence over the entire report.
- `state` (`open`, `waiting`, `closed`, `unknown`) and `waitRefs` (explicit external
  wait declaration, required for `waiting` without unresolved dependencies).
  External waits remain declarations; this helper does not certify a CI run.
- `assignment`: null for undelegated work, or `{id,revision,handoffRevision}`
  matching a current acknowledged handoff and observed idle lane in the capture.
  An unfinished mapped assignment retains its owner and produces a reconciliation
  finding, never a duplicate-work recommendation. Assignment outcome and objective
  obligation state are separate.
- `acceptance`: null or `{accepted,itemRevision,workSourceRevision,
  workSourceDigest,evidenceRefs}` with boolean `accepted` and nonempty refs. A
  closed item requires an explicit true mapping to its exact scope/source
  revision. A completed assignment, checkbox, commit or green CI result alone
  supplies no such acceptance mapping. Mapping is a declaration, not independent
  verification of arbitrary requirements.

`context` has exactly `version:1`, matching `binding`, `observedAt` (timezone-aware
ISO date), `health` (`complete`, `partial`, `unknown`), `coordinatorState`
(`waiting`, `working`, `unknown`), `controls`, and `objectiveAcceptance`.
`controls` requires explicit boolean `enabled`, `paused`, `stop`, `awaitingUser`,
`completed`, `superseded`, `pendingTransfer`. Any hold wins; none is cleared.
`objectiveAcceptance` is null or an explicit `{goal,goalRevision,scopeRevision,
inventoryDigest,accepted,evidenceRefs}` mapping. It must bind the exact inventory
digest, all closed item mappings and current source evidence; it still never
makes this detector declare the objective complete.

`assignments` is a private capture of the existing `assignment show --private`
record (versions 1 or 2), not a new runtime schema. The latest revision must match
all expected binding/record/revision fields and carry current
`inventory.coverage:observed_direct_children`, `observedAt`, `lanes` and `rows`.
Claims not mapped by a coverage row become `missing_claimed_item` findings;
absence is never interpreted as completion. Partial, stale, missing or malformed
inventories cannot certify that no work remains. This reader checks the fields
it uses; it is not a replacement for Go's strict runtime record loader.

## Findings and assessability

Each finding includes a deterministic ID/class/subject, evidence digests and
revisions, declared-evidence confidence, unknowns, one concrete recommended action,
`autoAction:none`, `estimatedWaste:unknown`, a verification metric and `status:open`.
No finding is automatically acted on, dismissed or called an improvement.

- The counterexample with completed idle implementation lanes, explicit delivery
  CI wait, and independent authorized undelegated visibility work produces
  `unjustified_coordinator_wait` and `continue_authorized_work`. The recommendation
  is to advance that existing item without duplicating an assignment; it is not a
  spawn grant or relaxation of resource/spend/concurrency policy.
- Known independent work stays visible beside unknown unrelated coverage. Unknown
  evidence cannot certify a global wait, but does not silently erase known work.
- `assessability` always separates work availability from objective acceptance.
  Missing source evidence recommends selecting/pinning an **already authorized**
  source. Missing dependencies/bindings/acceptance explicitly report
  `cannot_assess`; no finding/no work is not equivalent to “could not look.”
- A current healthy complete snapshot with all observed lanes idle, complete
  acknowledged handoffs, declared coordinator waiting and missing objective
  acceptance can produce only `suspected_stranded_objective`. Missing/partial
  health or inventories withhold this suspicion. The action is reconcile
  acceptance/remaining work, never completion or dispatch inference.
- A valid empty reviewed item set leads to `completion_review_required`, not
  completion. A genuine runtime hold yields `runtime_held` with no advance-work
  finding. Scope/authority/gate uncertainty remains explicit.

## Bounds, adoption and remaining gaps

Inputs together are limited to 1 MiB; source bytes together to 1 MiB. Per-record
bounds include 32 items, 32 sources of each kind, 32 dependencies/item and the
existing 32 assignment revisions/200-row bound. Dependency evaluation is bounded
and memoized. Source reads reuse the existing coordinator hash/regular-file
checks and cooperative ten-second operation budget between reads, not a guarantee
against a blocked filesystem syscall. The three private files and all source pins
are reread before output; changed evidence refuses. This is not an atomic snapshot,
a hostile-writer isolation boundary or proof of current host state after capture.

Invoke at real phase/wait boundaries, not repeatedly to manufacture status-only
turns. The command can reject an unjustified helper-produced wait receipt, but
cannot enforce that a host invokes it before ending a turn. Current captures and
existing authorization must be supplied; packaging/source tests do not establish
installed adoption, actual coordinator continuation, cross-loop claim isolation,
Windows support, reduced context costs or measured throughput improvement.

A future work-source adapter may select configured repository charter/ROADMAP/
TODO/AGENTS candidates, pin source identity/revision/hash and preserve explicit
item/acceptance/claim mappings. Discovery must remain separate from authorization;
semantic interpretation belongs to the coordinator. Cross-loop shared-source
claims need an independently reviewed identity/isolation contract. This PR adds
none of that parser/discovery/ledger machinery or automatic repository writes.
