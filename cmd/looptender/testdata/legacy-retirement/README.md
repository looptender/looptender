# Darwin legacy retirement primitive fixture

Research only: this is not a production command, migration API or rollout plan.
It accepts no target PID. The driver forks its own fictional children with pipe
control, eight-second child alarms and a twenty-second driver alarm. Cleanup
closes the retained control pipe and waits for the direct child; it never sends
a raw-PID signal. The exec case executes only the driver's own absolute image,
with an explicit minimal environment. Run the driver itself only with an
allowlisted environment, private HOME/TMPDIR/workdir, a bounded external wrapper
and durable output. Compile and execution are separate review stages.

Proposed observations (not yet executed):

- Register `EVFILT_PROC` with `NOTE_EXIT | NOTE_EXEC` and check its successful
  receipt between two fresh, equal audit-token acquisitions. Registration happens
  before any termination request. Registration or acquisition failure refuses.
- Exec produces an exec event while the child remains responsive. Exec invalidates
  application identity; the fixture never signals the changed image. Only its
  owned control pipe requests cleanup, followed by a separate exit event.
- A token with the wrong incarnation must return ESRCH, leaving the owned child
  responsive. A correct token requests SIGTERM once; the cooperative child
  acknowledges its handler and exits, producing the pre-armed exit event.
- A child ignoring SIGTERM remains responsive and produces no exit event during
  the bounded observation. Accepted termination is not permission to replace it.
- A token retained after the direct child has been reaped must return ESRCH.

The source rationale is Apple's XNU commit
`f6217f891ac0bb64f3d375211650a4c1ff8ca1ea`, `bsd/kern/kern_event.c`:
`filt_procattach` attaches the knote to the selected process's list, and
`filt_procevent` delivers exit against that attachment. It does not perform a
fresh PID lookup on each event. The before/after acquisition is essential:
registration alone takes a PID and could select a reused PID. A task-name
port's death is not used as proof: `osfmk/kern/task.c` explains that tasks can die
during exec. Published source and the running kernel may differ; native tests
remain required and do not prove every kernel version.

This slice does not establish forced PID recycling, live application ownership,
source/config/control binding, remote accepted effects, manager lifecycle,
durable replay exclusion, recovery, or safe replacement. No production command
is exposed. Durable intent and final locked application validation remain separate
unimplemented prerequisites. No README product behavior changes are needed.
