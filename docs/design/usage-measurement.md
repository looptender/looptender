# Usage measurement and comparable savings

Status: source-only diagnostic slice, stacked on the usage/compact draft. No
collector, scheduler, provider call, runtime installation or control change.
The Panel reads a bounded explicit `usage-evidence.json` export from each loop's
state directory during its existing status read. Missing evidence is unavailable.
The producer must publish an entire export atomically with private permissions.
Do not put private exports in the repository. The reader never follows source
labels as paths or URLs and never inspects transcript text.

See the complete [fictional export](../examples/usage-evidence.json) for the wire
format. It is sample data, not a collector or evidence of real savings.

## What this slice measures

The export declares version 1, exact loop name, UTC `from`/`to` (at most 31 days,
ending no later than the status read), an `evidence` label, `method` of
`observational` or `controlled-replay`, and `semantics` of
`cumulative-inclusive-v1`. The latter explicitly means input includes cached
input and output includes reasoning output. Exporters must establish those
provider semantics before using this format; other providers require a separate
normalization contract. Account percentages and API prices are not token counters.

`samples` contain opaque `stream` (one lane/provider session), `provider`, `model`,
`seq`, `at`, and `counters` (`input`, `cached`, `output`, `reasoning`; unknown values
are null or omitted). A stream cannot change provider/model. New sessions need
new stream identities. Identical stream/sequence records count once; conflicting
duplicates invalidate the export. Sort by sequence, require nondecreasing time,
and sum only consecutive nonnegative cumulative changes. First snapshots supply
baselines, not usage. A counter drop excludes the interval and invalidates complete
comparison coverage; it never becomes zero consumption. Uncached input is derived
only from compatible input/cached changes. Components are never summed twice.

The Panel shows observed intervals by ending UTC day, stream, provider and model,
with field-specific coverage. This is a trend of recorded intervals, not exact
midnight allocation or a complete accounting of all activity. Retention gaps,
missing snapshots outside the export and freshness beyond its displayed end time
remain unknown. There is no automatic BB or runner export in this slice. Existing
runner turn records remain separate because their semantics/coverage are different.

`actions` is optional. Omitted means unavailable; an explicit empty list means
zero supplied action records, not proof of zero global waste. Each action has
`id`, `original`, `kind` (`wake` or `retry`), `disposition` (`accepted` or
`suppressed_duplicate`) and `at` within the export period. A suppressed duplicate
must reference an accepted original of the same kind at an earlier/equal time.
Unique suppressed action identities are counted once. These supplied identity
records are evidence of avoided actions, not tokens; timer-withheld checks do not
qualify. The reader checks consistency, not independent authenticity.

## Savings estimate contract

Only `controlled-replay` exports can estimate token differences. Each `pairs`
entry names distinct `baseline` and `treatment` streams, a common `workload` and
`acceptance` version label, positive `baselineWork`/`treatmentWork` counts of useful
accepted work units, and true declarations for `qualityAccepted`,
`continuityAccepted`, `coverageComplete`, `cacheComparable`, `overheadIncluded`.
All pairs use the same provider/model, no stream is reused, and every included
field must cover every observed interval in both sides. A declaration cannot fix
an observed reset or missing field. Unknown quality or coverage leaves savings
unavailable. The Panel explicitly labels these declarations as author-attested;
it cannot validate task quality from counters.

For each component and pair, calculate baseline tokens per accepted work unit
minus treatment tokens per accepted work unit. Show the unweighted mean across
pairs, sample size, observed minimum/maximum and each baseline/workload/acceptance
reference. Negative differences remain visible. The range is descriptive, not a
confidence interval. One pair is an exploratory estimate, not a generalizable
saving. Preserve unsuccessful work, retries, compaction/handoff, measurement cost
and warmup within each side's counters; exclude neither merely to improve results.
Export authors must retain private provenance, exact trial periods, acceptance
results, cache conditions and the work-unit definition for review. Unmatched
workload, cache lifetime, input size or overhead invalidates comparability.

Raw token differences do not measure saved subscription quota or money. No API
price conversion or percentage claim is supplied. A causal claim needs replicated
controlled trials with equivalent work, quality and continuity and provider-specific
accounting evidence. The helper does not initiate those trials.

## Bounds and acceptance

One read is capped at 128 KiB, 512 snapshots, 16 streams, 32 daily stream rows,
128 action records and 8 comparison pairs, with at most 6 KiB of projected
details per loop (narrow the export period if exceeded). Invalid/oversized exports fail closed;
no raw input or filesystem path appears in the diagnostic. Rendering uses text
nodes and the existing responsive list layout. No extra polling, model requests,
account refreshes or runtime writes are added. Accounts acquisition remains a
separate implementation owned by its lane.

Open: supported model-free BB export with persisted cursors and verified counter
semantics; automatic identity-based avoided-action instrumentation; provider and
lane coverage; collection cost accounting; independently reviewed matched trials;
subscription-specific attribution; installed/native acceptance. No conservation
roadmap item is closed by fictional fixtures or author declarations.
