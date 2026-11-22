# No-signal exit observation fixture

The fixture compiles a new owned Go program. It accepts no external target PID,
manager label, state path or executable. Each child is a direct self-image child
with a private minimal environment and an independent eight-second lifetime.
The driver expires after twenty seconds; an outer reviewed recorder must impose
a twenty-four-second deadline and 4096-byte per-stream cap, preserving failures.
Cleanup closes only the retained stdin pipe and waits for the direct child. No
raw PID, group, signal-zero, SIGTERM or forced-kill request is made to children.
The outer recorder may terminate only its directly spawned driver on timeout;
child lifetimes independently bound descendants.

The first scenario acquires native identity from the direct child handle, arms
`ArmExitObservation`, then sends an exit byte through the retained pipe. Native
exit must be observed without a termination request. The second child acknowledges
a pipe request but remains alive: bounded observation must return unknown and the
handle must refuse reuse. The child must still answer another pipe request before
cleanup. A dynamic assertion refuses any exposed `Terminate` method. Child signal
handlers fail on unexpected SIGTERM/SIGINT; they do not send signals. Static review
must verify the observer's reachable native calls are token acquisition, kqueue
event read/registration and descriptor close only. This is not a claim that the
entire Go runtime contains no internal signal machinery.

The direct child is never polled/reaped until all native operations selecting it
are over. There is no forced PID reuse test. No provider, legacy executable,
launchctl, control file or replacement supervisor is involved. Compilation and
injected tests are not native execution evidence. Independent source/harness
review and explicit release of exact pins are required before running.

One reviewed native macOS run at `e486c5c3` passed both scenarios: external pipe
exit and accepted-but-alive unknown. The driver exited zero with empty stderr,
no timeout or truncation, and unchanged pinned artifacts. Independent retained
evidence review accepted only this owned-process scope. This does not establish
manager retirement, application ownership, external intent durability, replacement
readiness or live acceptance. No duplicate run is implied by this record.
