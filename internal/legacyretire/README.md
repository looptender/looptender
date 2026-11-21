# Legacy retirement attempt foundation

The durable foundation is connected to a separate unreleased Darwin application
adapter and `legacy-retire` command. See [the command boundary](../../docs/legacy-retirement.md).
The adapter/native integration requires independent source review and owned-process
execution before use. Injected tests do not execute its native functions. It never
changes loop controls, starts a replacement, or manages a service.

Preparation creates a new private directory with canonical immutable intent,
existing reservation lock and ready record. The returned ticket pins directory
and record device/inode identities plus canonical intent digest. No missing file
is created on execution. Files must be private, owned, regular, without symlinks
or hardlink aliases. Copied paths/inodes fail closed. The caller must retain the
original reviewed ticket; it cannot recover authority from a PID or an absent
record. Same-user malicious rewriting of both ticket and all evidence is outside
this local trust boundary; no anti-rollback claim is made for arbitrary restore.

The adapter acquires tick then control exclusion and returns fresh full binding
facts. The package compares them before arming an incarnation observer, again
after arming, and once more after consuming the attempt immediately before
signal dispatch. Owner, generation, current configuration/source digest, raw
control digest/native identity, authenticated daemon build/start/state identity,
actual executable/argv and native token are explicit binding fields. Their
presence does not establish their authenticity: adapter implementation and tests
must establish that separately. Old in-memory sources cannot be authenticated
by the old diagnostics protocol.

Consumption uses an exclusive hard link of the existing ready record to spent,
then removes ready and syncs the parent directory. This is atomic publication of
immutable consumed evidence, with no default/missing intermediate: spent wins
even when both names exist. Failure after link, failed directory sync, final
validation drift, lost signal reply, missing native exit, failed completion write
or lost completion acknowledgement all prohibit re-entry. Failure before link
cannot have dispatched and leaves prepared evidence; it is not claimed to consume
an attempt whose publication never occurred. Power-loss persistence is not proven
by unit tests. A new helper cannot recover an old native observation capability;
unknown outcomes remain reserved. Read-only reconciliation reports retained journal
evidence without resetting or dispatching; it cannot recover a lost native observer.

One accepted request never means completed. Native exact-incarnation exit,
expected endpoint absence, daemon-lock release and unchanged raw/native controls
are all required. The adapter's guards stay held through this observation and
completion publication. No replacement is launched; replacement startup, manager
retirement, retired-predecessor policy and rollback are separate operations.

Application and journal tests use private files and injected fictional adapters only. They cover strict
canonical records, copied/missing/replaced evidence, publication failures,
consumption before final binding drift, concurrent helpers, lost reply and fresh
helper refusal, accepted-but-alive and final-save failures. A private negative
control omitting consumption causes the lost-reply regression to dispatch again.
These tests establish orchestration and filesystem behavior on the native test
host, not application/native authenticity, forced PID reuse, actual process exit,
manager behavior, platform parity, power-loss durability or live acceptance.
