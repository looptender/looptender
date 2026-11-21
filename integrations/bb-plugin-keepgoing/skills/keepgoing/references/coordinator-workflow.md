# Coordinator-owned new-child workflow (unreleased)

This is an agent-operated path for an **existing owner instruction authorizing
bounded delegation**. It is not a supervisor scheduler. The packaged keepgoing
skill consumes `scripts/coordinator.py`; no tick, plugin backend, timer or daemon
invokes it. Python 3 and Unix private-file locks are required. If unavailable,
report the concrete tooling gap and continue other authorized work; do not
install an interpreter, change hosts or bypass a refused read.

## Use under an existing grant

The coordinator first reviews the current owner instruction and task scope. A
private file written by an agent records that instruction; it cannot manufacture
person authority. Preserve the original caller/environment, including
`BB_THREAD_ID` and `BB_CLI`. Do not substitute another account, provider, model,
host or permission mode to get a refused operation through.

1. Read `keepgoing status -c <config> --json`, `assignment show --private` and
   `assignment inventory` using the same config. Assignment lifecycle must be
   enabled and its current review must cover every observed direct child.
   Existing incomplete rows stay in that review. Do not use a historic binding
   from `show` as proof of current owner/goal.
2. Create a private directory (0700) outside the checkout and a grant JSON (0600)
   with the schema below. Review its exact scope, source digests and local bounds.
   Use one persistent journal for this goal/grant; moving/deleting it is not a
   reset. An unknown attempt must survive process restarts and grant revisions.
3. From the installed skill directory run a dry-run first:

   ```sh
   python3 -B scripts/coordinator.py --grant <absolute-private-grant> --journal <absolute-private-journal> --assignment-revision <current-revision> --private
   ```

   Inspect typed blocked actions and independent work. Within the already
   authorized scope, run the same command with `--execute`. It advances at most
   one new child, with visible ownership and an assignment mapping. Review each
   resulting assignment revision before the next advance. Do not ask again for
   the same permission; do not exceed the recorded authorization.
4. On child/phase completion, independently inspect the deliverable. A worker's
   completion claim, idleness or duplicate receipt is not progress. Import and
   acknowledge its exact current assignment handoff using existing commands,
   then provide a private verification array through `--verification <file>`.
   A verified settlement can dispatch the next independent task in that advance.
5. Any lost/malformed/timeout spawn or mapping response holds that attempt. Do
   not rerun the spawn. Independently establish the exact returned child identity
   and reconcile with `--execute --reconcile-intent <intent> --child <exact-id>`.
   This verifies metadata and the exact mapping; it does not infer ownership from
   a title or a list difference. If identity cannot be established, retain the
   reservation and surface the unresolved obligation. A lost mapping response
   that did publish is recognized without another publication.

Default output contains counts/reason codes; `--private` includes task, assignment
and intent IDs. Never publish private grant sources, prompts, output or journals.
Task content travels to BB on stdin; stderr is discarded and never classified.

## Grant schema

All keys are required; unknown keys/duplicate JSON keys refuse. See
the repository `tools/coordinator-workflow/test_commands.py` for a complete fictional grant and actual
CLI assignment journey. Source paths and executables must be absolute regular
files; config/runtime bindings must be canonical. Digests are SHA-256 hex.

| Field | Meaning |
| --- | --- |
| `version`, `id`, `revision`, `direction` | Version 1, persistent grant ID, monotonically advancing revision and owner direction revision (1–128). Direction cannot exceed revision. |
| `instructionRef` | Source reference recording the existing owner delegation instruction. Agent declaration is cooperative evidence, not person proof. |
| `binding` | Exact current assignment binding: `loop`, `config`, `runtime`, `coordinator`, `owner`, `generation`, `goal`, `goalRevision`. Coordinator and current owner/caller must agree. |
| `recordId` | Current private assignment record identity. Expected record revision is supplied separately on every invocation. |
| `project`, `environment`, `host` | Explicit parent and child BB context, independently checked through typed metadata. Spawn uses the existing environment ID alone; it already selects the host and cannot combine with `--host`/`--machine`. The host remains bound and verified. |
| `provider`, `model`, `effort` | Exact authorized spawn arguments. No discovery/probe or fallback is performed. Requested model/effort are not proof of a provider's eventual execution choice. |
| `bb`, `effectiveBB`, `keepgoing` | Each has `path`, `sha256`; pin requested BB, effective `BB_CLI` re-exec target and CLI. Executable/source changes refuse. |
| `sources` | Up to 64 `{ref,path,sha256}` entries, including config, owner instruction and task briefs; each at most 1 MiB (brief at most 64 KiB). |
| `maxAttempts`, `maxChildren` | Local lifetime attempt bound (1–128), direct-child concurrency ceiling (1–32). Not tokens/currency/global host guarantees. |
| `tasks` | Up to 32 unique lowercase IDs; each has `id`, `scopeRefs`, `authorizationRefs`, `briefRef`, `resources`, `dependsOn`, `personGate`, `parkedAt`. References name sources. Up to 8 exclusive resource keys/task; at most 32 dependencies. |

A task is eligible only if never dispatched/mapped, dependencies are verified,
resources are free, no person gate is declared and `direction > parkedAt`.
A newer owner instruction can supersede an old coordinator park; it cannot clear
runtime holds. Scope/source changes to unfinished attempts refuse. A grant update
cannot adopt another journal or reset spent attempts. The workflow is cooperative:
it cannot detect an operator deleting every journal and inventing a new grant.

A verification receipt is
`{task,recordId,assignmentRevision,handoffRevision,accepted:true,artifacts:[{path,sha256}]}`.
It binds the current complete acknowledged handoff to coordinator acceptance and
1–16 still-matching local artifact files. The workflow does not judge arbitrary
natural-language requirements. Repeated identical receipts do not advance counts.
Changed/reopened verified obligations refuse with
`verified_obligation_changed_reconcile`; this first release does not automatically
migrate/reopen a dispatched task or revoke historical verification. Preserve the
journal and use explicit coordinator assignment reconciliation, not a fresh
journal or silent retry. Unfinished errored/archived/superseded rows remain visible
with reconciliation actions, while independent new work may proceed.

## Boundaries and remaining acceptance

- Spawn only: no `tell`, idle reuse, retry, stop, archive, reparent or implicit
  resume. Those remain explicit coordinator decisions under existing authority.
- Final source/owner/control rereads are **not atomic hold fencing**. A hold can
  arrive after BB submission and a child can still be created. The fixture
  demonstrates this, preserves the unknown attempt and never replays it. No
  host rollback or absolute no-create-after-hold guarantee is claimed.
- A nonblocking journal lock serializes this helper only. Local attempt/resource
  and direct-parent slot bounds are not global spend/concurrency guarantees;
  independent actors and host scheduling remain outside that transaction.
- Bounded streaming reads, local process-group cancellation and reaping do not
  cancel or prove failure of an already submitted host request. Cancellation signals
  only before reaping the leader; success never signals a reaped numeric identity.
  Neither group signalling nor pipe EOF proves all descendants drained. Journal capacity
  refuses; it never evicts unfinished work. See the repository `tools/coordinator-workflow/DESIGN.md`.
- No transcript-based decisions, owner-text parsing, automatic account creation,
  login, provider probes or supervision authority changes are added.
- Automatic delegation acceptance remains **INCOMPLETE**: actual coordinator
  agent continuation, installed packaging/behavior and tell/reuse are unverified.
  Offline fixtures do not close the roadmap or authorize rollout.

## Offline checks

Build the test CLI outside the checkout, then run:

```sh
KEEPGOING_TEST_BINARY=<absolute-built-cli> python3 -B -m unittest discover -s tools/coordinator-workflow -p 'test_*.py' -v
```

The same packaged consumer/streaming adapter calls explicit fictional executables;
no installed BB/provider is called. The optional real CLI test writes only an
isolated fictional config/runtime/registry and uses that fake BB. Without the test
binary this one case explicitly skips. The plugin test runs this suite on Unix;
Windows is explicitly unsupported, not execution acceptance. Tests include
unknown restart/new-grant reservations, mapping lost reply, current artifact
settlement, duplicates, newer-owner park, two concurrent advances, bounds,
source/owner/context drift, and the residual submitted-hold race.
