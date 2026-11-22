# Manual legacy supervisor retirement (unreleased)

`legacy-retire` is a separate Darwin migration command for one explicitly
selected manual daemon that predates the owned shutdown endpoint. A new binary
cannot add an endpoint to an old running process. This command retires the
supervisor; it does not perform the loop's Stop action, stop its BB conversation,
resume a loop, install a binary, or start a replacement.

The source has independent review and one bounded integrated owned-process fixture
on native Darwin. That fixture exercised the actual CLI, authenticated fictional
daemon and native bridge for graceful retirement and accepted-but-alive refusal.
It does not establish real legacy provenance or live migration acceptance. Do not
treat the command's presence as rollout readiness.

## Explicit selection and preparation

The caller retains its normal BB identity and environment. The existing owner
check applies; unknown ownership, a pending transfer, and retired control refuse.
Known service markers and managed invocation forms refuse. A separately reviewed
rollout manifest must establish manual launch ownership and absence of an external
respawner; absence of a known marker alone cannot prove that.

A private selection JSON contains exactly these fields, with independently
reviewed values: `configPath`, `statePath`, `pid`, `build`, `startedAt`,
`executable`, `executableDigest`, and `arguments`. Paths must be absolute and
canonical, `startedAt` is the precise authenticated RFC3339 daemon start time,
`executableDigest` is SHA256, and arguments must be the exact four native argv
entries for `daemon -c CONFIG` or `__start-background -c CONFIG`, including argv0.
A saved PID alone is never authority. Unknown keys, duplicates, case aliases,
null scalars, unsafe files, and unavailable evidence refuse.

The source command interface is:

```text
looptender legacy-retire prepare --selection /private/example/selection.json --ticket /private/example/ticket.json --timeout 20s
looptender legacy-retire execute --selection /private/example/selection.json --ticket /private/example/ticket.json --timeout 20s
looptender legacy-retire reconcile --ticket /private/example/ticket.json --timeout 20s
```

These paths are fictional. The ticket's existing parent directory must be private;
prepare exclusively creates the ticket and never overwrites one. The operation
has a 1–60 second context deadline. Native calls and filesystem calls themselves
are not forcibly interrupted; the deadline bounds cooperative lock waiting and
exit observation, not arbitrary kernel or storage stalls.

Preparation drains the existing tick lock, then holds the control lock. It reads
raw controls without rewriting them, verifies ownership and target, and binds
current configuration/routes, executable image, state directory, lock identities,
authenticated diagnostics, native argv and fresh full audit token. The current
caller's selected context is hashed without logging its values. This is not a
reconstruction of the old process's environment, account context or mapped source
bytes. Disk executable hash plus native image path and reported build do not prove
the running mapped image equals today's disk bytes. Missing provenance remains a
rollout limitation, never permission to invent replacement context.

One canonical attempt directory per selected incarnation lives beneath the state
directory's private `legacy-retirement` directory. An exclusive durable reservation
outside that attempt survives a missing attempt directory. Preparation never
recreates a ticket after a reservation already exists. Preserve the separately
retained ticket, reservation and all partial evidence on any failure. Arbitrary
same-user rollback or deletion of the whole reservation hierarchy is outside this
local trust boundary; this is not a tamper-proof external ledger.

## Dispatch and unknown outcomes

Execution requires the original native-bound ticket. It keeps reservation,
tick and control locks, verifies the full binding, arms native exit observation
before dispatch, verifies again, durably consumes the attempt, then validates once
more immediately before a single token-directed SIGTERM. There is no raw-PID,
signal-zero, process-group, forced-kill, privilege escalation, or retarget fallback.
An exec observation invalidates the selected invocation. Unsupported APIs and
permission failures refuse.

Successful dispatch is not death. Completion requires exact prearmed native exit,
old endpoint absence, acquisition of the exact old daemon lock, and unchanged
control bytes and native identity. Failed acknowledgement, expired observation,
changed invocation, exec, or failed completion publication leaves consumed or
unknown evidence. A fresh helper cannot dispatch again with that ticket, and
prepare cannot manufacture another ticket for the same reserved incarnation.
Tick drain excludes cooperating future ticks; it does not cancel a provider's
already-accepted work or prove that remote effects stopped.

`reconcile` only reads the retained journal under its reservation lock. It reports
prepared evidence, consumed/unknown evidence, or recorded completion. It never
signals, resets, repairs, deletes evidence, or upgrades unknown using PID absence.
Recorded completion is historical evidence, not current absence or replacement
launch authority. Recovery of unknown outcomes into a new dispatch is unsupported.

Replacement is a separate reviewed operation under the actual owner and selected
context. It must preserve every control byte and hold and must not use enable,
resume, start, takeover, or a blind launch retry. A replacement may act before
readiness is observed when controls are enabled. Lost readiness cannot authorize a
second launch. Managed jobs use their verified original manager definition and
manager-specific retirement plan; a retired predecessor receives retirement only.

## Evidence and platform limits

- Pure/injected fixtures cover actual private file locks, immutable control bytes,
  ownership/build/invocation/source drift, final consumed validation, bounded lock
  waiting, canonical reservation, missing/corrupt/copied intent, contention,
  publication faults, lost replies, and non-dispatch reconciliation.
- A separate owned-child Darwin C fixture demonstrated token rejection, graceful
  exit, exec distinction, and accepted-but-alive behavior. Earlier failed fixture
  runs remain failures; the corrected fresh result supports only its exact scope.
  It does not exercise this Go native adapter or authenticate a real daemon.
- One separately reviewed integrated Darwin fixture completed nine CLI commands
  across two newly direct-owned fictional daemons. It verified graceful completion,
  accepted-but-alive consumed/unknown, spent replay and new-ticket refusal, unchanged
  compound controls, and clean owned-child exit. It is not a lost-acknowledgement
  test. No legacy executable, provider operation or live retirement was used. Both
  legacy/current storage variables and HOME/XDG defaults were fictional. Retained
  failure-evidence regressions cover timeout, output overflow and launch failure.
- Linux and Darwin without cgo refuse native use; Windows and other platforms
  refuse the command. Cross-compilation is not native platform acceptance.
- No power-loss certification, forced PID reuse, arbitrary manager behavior,
  descendant termination, old loaded-daemon test, automatic recovery or safe
  replacement claim follows from these tests.
