# Project work continuity (unreleased)

An external coordinator can keep unfinished project work in a private `WORK.md`
and run a local reconciliation command after a handoff, before waiting, or after
restarting. This is a usable source-checkout helper, not an installed supervisor
feature. It keeps work visible even when no goal, assignment inventory or current
runtime capture is available. Missing evidence remains explicit; an empty file,
completed lane, commit or green CI run never proves project completion.

`WORK.md` is the single work register. Its prose is retained verbatim but only one
strict fenced JSON block is interpreted. `PROJECT.md` and `DECISIONS.md` may hold
context and acceptance descriptions. A generated `HANDOFF.md` is a derived
snapshot of the full register and findings, not a second editable work store.
There is no database, daemon, source discovery, transcript reader, automatic
assignment, dispatch, host compaction or enforced turn-end hook.

## Start, edit, read and check

Python 3.9 or later on macOS/Linux is required; there are no third-party packages.
Run from a source checkout. Use an existing private directory (0700), absolute
paths and owner-private regular inputs (0600). Paths with symlinked components
are refused for the register, captures and output; use their real absolute paths.
Keep real project text outside the public repository.

```sh
python3 -B integrations/bb-plugin-keepgoing/skills/keepgoing/scripts/project_work.py \
  init --work /private/example/WORK.md --id example-project
python3 -B integrations/bb-plugin-keepgoing/skills/keepgoing/scripts/project_work.py \
  read --work /private/example/WORK.md --private
python3 -B integrations/bb-plugin-keepgoing/skills/keepgoing/scripts/project_work.py \
  check --work /private/example/WORK.md --private
```

`init` creates an empty template and refuses any existing destination. The
coordinator edits `WORK.md` under its existing permission, preserving stable IDs,
incrementing the register revision for semantic edits, and each changed item's
revision. Reconcile dependencies and acceptance when an item revision changes.
No command edits the register, fills in authority, or silently marks items done.
The initial check reports missing bindings/sources and empty scope rather than
inventing work or asking for a redundant grant.

A complete minimal fictional register is:

````markdown
# Example project

Keep full private notes here. Checkboxes in prose are not acceptance.

```looptender-work
{
  "version": 1,
  "id": "example-project",
  "revision": 1,
  "binding": null,
  "assignmentRecord": null,
  "assignmentRevision": null,
  "scopeComplete": false,
  "workSources": [],
  "authoritySources": [],
  "items": [{
    "id": "observer-visibility",
    "revision": 1,
    "title": "Review fictional ended-session visibility",
    "owner": null,
    "scopeRefs": ["PROJECT.md#visibility"],
    "acceptanceRefs": ["PROJECT.md#acceptance"],
    "state": "open",
    "dependsOn": [],
    "nextAction": "Review the board fixture and record remaining acceptance",
    "evidenceRefs": ["private:research-review"],
    "blockingObligation": null,
    "workSource": null,
    "authorization": {"state": "unknown", "refs": []},
    "personGate": {"state": "unknown", "refs": []},
    "waitRefs": [],
    "assignment": null,
    "acceptance": null
  }]
}
```
````

This incomplete example is valid and useful: it reports unfinished unowned work
and explicit unavailable coverage. It does not establish permission to execute
that work. References are opaque text, never fetched or executed. Research,
review, imagery and other artifacts can be evidence; commits are not required.

Default output minimizes private names/text to counts, fixed reason codes and
hashes. `--private` intentionally prints full IDs and next actions; `read --private`
also returns the exact Markdown. Preserve private output outside repositories.
Nothing silently truncates private text: exceeding a bound refuses the input.

## Schema and the existing coverage bridge

The top-level fields and each item's existing coverage fields reuse the
[coverage inventory contract](../integrations/bb-plugin-keepgoing/skills/keepgoing/references/coordinator-coverage.md#structured-evidence-and-schema).
This helper adds a file-backed input to that existing projection. It does not
create a second runtime assignment or completion lifecycle.

- `id` is a stable register/item ID of 1–64 ASCII letters, digits, `_` or `-`,
  starting with a letter; case is significant. `revision` is a positive integer.
  Duplicate IDs, JSON fields (including escaped equivalents), unknown fields,
  malformed types and contradictory owner/wait fields are refused.
- `binding` is null until the existing goal/assignment binding is available.
  Otherwise use the exact existing loop/config/runtime/coordinator/owner,
  generation, goal ID and goal revision. `assignmentRecord` and
  `assignmentRevision` are either both null or the existing capture's identity.
  No synthetic runtime identity is generated.
- `workSources` and `authoritySources` retain coverage's explicit source IDs,
  revisions, absolute paths and SHA256 pins. `workSource` on an item can be null
  until pinned. Use a separate scope/acceptance source such as `PROJECT.md`, not
  a self-referential hash of `WORK.md`. Source contents are hashed, never
  interpreted. Source drift calls for scope reconciliation and repinning existing
  authority, not automatic reapproval. New scope still needs its own authority.
- `title`, nonempty `acceptanceRefs`, `nextAction` and `evidenceRefs` keep the
  human task, acceptance contract and follow-through visible. References are
  descriptive pointers. A true `scopeComplete` is a declaration of reviewed
  scope, never semantic proof that every project obligation was found.
- `owner` is null (unassigned), `{kind:"coordinator",id:"thr_example_parent"}`
  matching a supplied coordinator binding, or `{kind:"assignment",id:"build"}`
  with the matching existing `assignment:{id,revision,handoffRevision}`. These
  are declared responsibility and references, not a new claim/reservation store.
- `state` is `open`, `waiting`, `closed` or `unknown`. `dependsOn:[]` explicitly
  declares no dependencies; null means unknown. Edges are `{id,revision}`.
  Unknown, changed, cyclic or unaccepted dependencies cannot release work.
- A `waiting` item must have nonempty `waitRefs` and a concrete
  `blockingObligation:{id,revision}` pointing to another item, for example a
  release decision or review result. That obligation has its own next action,
  person gate and acceptance. Non-waiting rows must have null blocking obligation
  and empty wait refs. A person-only decision remains a held action; other
  authorized work stays visible. General policies belong in context, not endless
  actionable obligations. There are no aggregation or “non-counting rollup”
  semantics: every structured row is counted, regardless of prose labels.
- `acceptance` uses the existing coverage mapping: `accepted`, `itemRevision`,
  `workSourceRevision`, `workSourceDigest`, `evidenceRefs`. It is allowed only on
  a closed row with a source binding. A closed declaration without a current
  positive mapping produces an acceptance gap. Changing acceptance criteria
  requires a new item revision and corresponding mapping. The helper does not
  inspect the evidence or certify that it satisfies the criteria.

Provide current private captures to activate the bridge:

```sh
python3 -B integrations/bb-plugin-keepgoing/skills/keepgoing/scripts/project_work.py \
  check --work /private/example/WORK.md \
  --context /private/example/context.json \
  --assignments /private/example/assignments.json --private
```

The capture formats, 300-second default age (1–3600 seconds), exact goal/owner
bindings, assignment revisions and acknowledged handoffs are unchanged from
[coordinator coverage](../integrations/bb-plugin-keepgoing/skills/keepgoing/references/coordinator-coverage.md).
The helper never obtains captures from BB or refreshes timestamps. Malformed
provided input refuses the report, including malformed assignment captures when
local runtime bindings are absent. Capture validation checks the consumed structure;
it does not replace the runtime lifecycle validator or authenticate a live host.
Partial observations remain explicitly incomplete. Omitted metadata produces
useful local findings plus `coverage_inputs_unavailable`. Every item remains listed, even if
one missing source binding prevents the runtime projection. Existing source pins
are reread and the coverage projection checks the remaining claims for drift.

Findings include unfinished unowned work; missing/changed/unaccepted dependencies;
stale assignment acknowledgement; completed child handoff with project acceptance
or follow-through still open; stale checkpoints; changed items without revision
increments; and items removed since a checkpoint, including rows previously
labelled closed. Removal always requires explicit reconciliation; a prior closed
label cannot silently erase missing acceptance. A wait linked
to a current, accepted declared obligation produces `wait_obligation_resolved`.
This recommends reconciling the wait only: it changes no item, permission or hold.
All findings bind the exact register bytes/revision and relevant capture/source
hashes, include unknowns, and have `autoAction:none`. Independent work remains
visible beside unknowns. Actual supplied runtime holds win over coverage advice.

## Checkpoint and restart

Write a separate generated snapshot after reviewing a phase, before a context
reset, or when handing responsibility back. This is explicit coordinator work;
there is no automatic host hook.

```sh
python3 -B integrations/bb-plugin-keepgoing/skills/keepgoing/scripts/project_work.py \
  checkpoint --work /private/example/WORK.md --output /private/example/HANDOFF.md
python3 -B integrations/bb-plugin-keepgoing/skills/keepgoing/scripts/project_work.py \
  check --work /private/example/WORK.md --previous /private/example/HANDOFF.md --private
```

A checkpoint contains the full original Markdown, its SHA256 and revision, and
the reconciliation report in a strict `looptender-handoff` block. It preserves
unknowns; writing a checkpoint is not progress or acceptance. After restarting,
read `WORK.md`, compare the checkpoint, review its next actions and refresh any
needed captures. The checkpoint is not imported as live goal/control state.
Manual handoff notes are not native checkpoints; choose a separate output path.

For a later checkpoint at the same path, pass the previous successful command's
`checkpointSha256` using `--expect-previous <exact-sha256>`. The command compares
that digest under a cooperative nonblocking lock, retains the previous complete
bytes as `HANDOFF.md.<sha256>.md`, then replaces `HANDOFF.md`. A stale/missing
expected digest refuses overwrite. Existing checkpoints are used automatically
for revision comparison; `--previous` cannot select a different checkpoint when
replacing an existing output. For a new output, it may select a prior native
checkpoint. Archives are never evicted. A conflicting archive, malformed prior
snapshot or different register ID refuses replacement; no deletion is needed
for ordinary updates. Both the report and archive preserve removed obligations
for review rather than silently erasing them.

Only explicit `init`/`checkpoint` write local private artifacts. Initial creation
publishes a complete same-directory temporary file via exclusive hard link;
updates archive before atomic replacement. Failed publication may leave a valid
archive; abrupt interruption can leave a temporary file. This is cooperative
single-coordinator editing, not a cross-loop lock/claim service, authenticated
filesystem isolation, atomic multi-file transaction or power-loss durability
promise. No command clears actual holds, deadlines, budgets, questions or
completion flags, or changes a provider/account/permission setting.

## One-time adoption and recovery

The coordinator first reconciles available project context, roadmap, prior handoffs
and relevant older decisions under the existing authorization. Record each
still-relevant unfinished obligation with a stable ID, declared owner or unassigned
state, acceptance references and concrete next action. Keep cancelled or superseded
decisions and their rationale in supporting context; do not silently lose their
history or revive them as new authority. Disclose unavailable history and uncertain
scope with `scopeComplete:false` and explicit next actions to resolve those gaps.
This is a coordinator procedure, not automated transcript discovery.

Pin the reviewed scope and existing authorization sources, run `read` and `check`,
and review useful local findings beside unavailable runtime coverage. Preserve any
manual handoff and choose a separate native checkpoint output. After restarting,
repeat the read/check comparison and update the checkpoint with its exact prior
digest. Actual adoption in each project requires its coordinator to use this
workflow; a generated checkpoint does not establish that adoption happened or
that the backlog is complete. Existing holds and permissions remain unchanged.

## Bounds, exit codes and verification

`WORK.md` is limited to 256 KiB, including all prose. There is no local item-count,
source item-ID-count or dependency-count ceiling. All declared items are checked
and retained in checkpoints; oversized input is refused without changing the
register or existing checkpoint. The practical item count depends on the size of
its text and edges. A fictional compact 256-item chain exercises the full command
and checkpoint round-trip, including an unresolved last item. Dependency analysis
uses an iterative O(items + edges) pass; source membership is indexed.

Other field bounds remain explicit: each reference list has at most 32 entries,
titles are at most 256 UTF-8 bytes, next actions 2048 bytes and references 256
bytes. Existing coverage bounds apply to source records and captures: source
bytes together at most 1 MiB; work plus current captures at most 1 MiB.

The existing runtime coverage projection still accepts at most 32 items, 32 IDs
per source and 32 dependencies per item. Exceeding any of these produces
`runtime_coverage_capacity_unavailable` for the entire bridge. It never projects a
prefix or claims complete runtime coverage: every local obligation and local
finding remains present. This compatibility limit does not restrict the register
or require splitting it into competing files.

Checkpoints are at most 4 MiB and never discard text to fit. Input/source reads use existing nonblocking no-follow descriptor opens and regular-file
validation. Files are reread to detect changes during inspection. This is not an
atomic or live-authenticated snapshot, or a hard deadline for blocked filesystem
syscalls; the existing source reader has a cooperative ten-second budget.

`init`, `read` and `checkpoint` exit 0 on successful local IO/validation, even if a
checkpoint reports gaps. `check` exits 1 when findings or missing evidence require
reconciliation, 0 only for a report without them. It never certifies objective
completion (`objectiveComplete:false` always), permission, dispatch or a justified
whole-loop wait. Invalid input/IO exits 1; invalid CLI syntax exits 2. Inspect the
structured report rather than treating exit 0 as an acceptance gate.

Fictional tests exercise completed-child Sessionpipe follow-through, fulfilled
waits with independent work, current/stale source and assignment bindings,
dependencies, person/runtime holds, missing versus empty inventory, lossless
restart/checkpoints, overwrite/history refusal, publication failure, privacy,
FIFO/nonregular/oversized inputs, byte-bounded inventories above 32 items, dense
and deep dependency graphs, removed closed rows, malformed captures without
bindings, and trapped runtime commands. Run:

```sh
python3 -B -m unittest discover -s tools/coordinator-workflow -p test_project_work.py -v
python3 -B -m unittest discover -s tools/coordinator-workflow -p test_coverage.py -v
```

The existing plugin coordinator test wrapper also discovers these tests in CI.
Native macOS fixture execution, Linux CI execution, released packaging, installed
adoption, actual coordinator invocation/continuation and measured context-cost
improvement are distinct evidence. Windows is explicitly unsupported. The helper
cannot enforce invocation, validate arbitrary acceptance semantics, discover
forgotten prose tasks, or prevent two unrelated coordinators claiming shared work.
