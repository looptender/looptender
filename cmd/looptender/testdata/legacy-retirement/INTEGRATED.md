# Integrated owned-process fixture: execution held

This is test-only preparation for independent source/harness review. Neither the
new native bridge nor this fixture has been executed. Compilation does not grant
execution clearance. Run only an exact separately reviewed pinned command packet.

`fictional/main.go` is a newly built direct child with daemon-shaped argv. It is
not the real supervisor and never reads the configured prompt, runs a provider,
launches a child, or contacts BB. It holds its own daemon lock and serves the
actual authenticated `daemoninfo` protocol. SIGTERM either closes the endpoint
and exits gracefully or records the signal and remains alive. A retained stdin
pipe and independent twelve-second lifetime provide cleanup without target
signalling. The driver does not poll or reap it until every operation selecting
that incarnation has ended, preserving PID reservation even after graceful exit.

`run_owned_adapter.py` launches two such children sequentially, never accepts an
external PID, and derives each selection from its own child handle and ready
message. It uses the actual compiled CLI and native bridge for prepare, execute,
read-only reconcile and spent replay refusal. The ignore scenario also refuses a
new preparation after unknown observation. This is an accepted-but-alive unknown
case, not a simulated lost network reply. Controls retain all compound holds;
raw hash, native identity, mode and mtime must be identical afterward. One handled
SIGTERM marker is supplementary evidence, not a proof against signal coalescing.

Each process gets an explicit allowlisted environment. Both naming generations of
session/registry/routes paths, HOME/XDG/temp and provider config directories are
fictional. No inherited account context, loader overrides or executable search
is used to select either binary. Config names an absent fictional BB path; the
fixture and selected CLI commands never dispatch provider commands. The only
network use is authenticated loopback diagnostics. Artifacts are regular owned
read-only files under a parent disjoint from writable evidence. Pins are checked
before launches and after the set; this is not atomic exec pinning against an
external same-user actor replacing ancestors.

Bounds: two children, twelve seconds each; readiness four seconds; each of nine
CLI invocations at most five seconds with four KiB per output stream and a
one-second cooperative command deadline; driver operation budget forty seconds.
On failure it stops the sequence without retry. Cleanup closes only the retained
child pipe and waits up to thirteen seconds; it never signals the selected child.
The separately reviewed outer wrapper must impose a fifty-five-second driver
ceiling with four KiB streams, terminating only its directly spawned driver on
timeout. A surviving fixture child independently expires within twelve seconds.
The outer wrapper must not use process-group or target-PID cleanup.

The driver preserves private result/config/ticket/journal/control evidence under
an exclusive new root, including failed and incomplete runs. Command results are
saved after each invocation, with fsync; the final report and evidence directory
are synced. Artifact paths/hashes and output are recorded privately, not committed.
No cleanup of real metadata, legacy executable, production instance, installation,
manager service, automatic replacement or platform parity is part of this scope.
Even a pass will establish only this exact synthetic application/CLI/native path
on the tested Darwin host. It will not establish real legacy loaded-config or
launch-context provenance, all descendants, power-loss behavior, manager lifecycle,
forced PID reuse, remote effect cancellation, or live rollout acceptance.

Failure evidence correction: the shared `owned_process_evidence.py` recorder
saves a started command entry before pin verification and spawn, then updates that
same entry in `finally` with PID, exit, partial bounded streams, timeout/truncation
and cleanup state. Failure stops the sequence; later cleanup/save failures do not
replace the original failure. The outer packet uses that recorder and saves its
own exceptional pin/import/spawn/read result in `finally`. Its exclusive evidence
file must be opened before pin checks; failure to create that file launches nothing.
The previous prepared artifacts remain retained and are not execution candidates.

`test_owned_process_evidence.py` exercises only newly direct-owned Python children
and nonexistent fictional commands: partial-output timeout, overflow, launch/pin
refusal, success, and preservation of the original failure when final saving also
fails. It neither imports the native runner nor invokes the Go bridge.
