- Status and the Usage page no longer multiply withheld-check counts by context size to claim token savings. Counts and context readings remain available; subscription savings are explicitly unavailable, including when displaying older status data.

- Add a source-only explicit coordinator compact helper with private grants, idle-boundary checks, durable attempt/cooldown bounds and no replay after uncertain delivery. Matching lifecycle and a continuity receipt are required for reconciliation; no automatic compact policy or successor behavior is enabled.
