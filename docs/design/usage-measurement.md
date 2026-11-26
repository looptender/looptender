# Usage measurement and comparable savings

Status: source-only diagnostic slice, stacked on the usage/compact draft. An opt-in collector is
implemented below; no runtime installation or live control change is implied.
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
remain unknown. The opt-in BB collector is described below. Existing
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
nodes and the existing responsive list layout. Report reads add no polling, model requests, account refreshes or runtime writes. Accounts acquisition remains a
separate implementation owned by its lane.

Open: installed acceptance of the bounded BB Codex collector; broader provider
normalization; automatic identity-based avoided-action instrumentation; provider and
lane coverage; collection cost accounting; independently reviewed matched trials;
subscription-specific attribution; installed/native acceptance. No conservation
roadmap item is closed by fictional fixtures or author declarations.

### Strict ingestion boundary

On macOS/Linux the actual open uses no-follow and nonblocking flags. Before any
read, descriptor metadata must identify the same regular file seen in the initial
path check and remain within the byte limit. A FIFO, symlink, different replacement
or oversized descriptor is refused. An ordinary concurrent atomic publication may
therefore produce an unavailable reading until the next existing refresh; it never
starts a retry loop. Byte limits do not promise a deadline for arbitrary stalled
filesystem I/O. Other platforms currently fail closed with unavailable evidence;
a native safe-reader implementation and acceptance remain open.

A bounded token pass checks exact JSON field names and rejects duplicate keys,
including escaped duplicates, unknown fields and case aliases at every nesting
level before typed decoding or estimate qualification. Labels are 1–80 ASCII
characters: start with a letter/digit, followed only by letters/digits, spaces,
periods, underscores or hyphens. Path separators, colons and URL forms are refused;
exporters must supply safe aliases for private source references.

## Automatic collection (unreleased, opt-in)

Set `loop.usage_collection: true` only after installing matching reviewed CLI and
BB plugin builds. No installation is performed by this feature. The existing check
owns collection under its tick lock and BB ownership/configuration guards; no new
service, scheduler, Accounts polling or provider/model request is started. Stop,
pause, holds, dry runs, transfers and handoffs skip collection. Only the configured
primary BB thread is supported; lanes, fallbacks and other adapters are unavailable.
The configured BB CLI and its existing endpoint/environment select the device;
no accounts, credentials or other devices are discovered.

The plugin command `bb looptender usage-events <thread> <latest|after-seq>` reads
exact-thread metadata, requires the Codex provider (whose total input includes
cached input), and calls SDK `threads.events.list` with only
`thread/tokenUsage/updated`, a sequence cursor, and fixed limits. No CLI log or
timeline fallback is allowed. Other providers fail closed until their normalization
has been reviewed. Current thread metadata does not establish historical model or
account attribution; the export labels those unavailable. The source is BB's
normalized cumulative `total` fields, never `last`, context occupancy or API cost.
Provider session IDs become opaque hashes; paths, titles and raw errors are not
published. No accepted work, avoided actions or savings pairs are invented.

The first read takes only the latest snapshot as a baseline and skips history.
Subsequent reads take at most 32 events ascending from the durable cursor, once
per five minutes at most (the existing loop interval may be longer). One attempt
has a two-second command deadline plus at most 100 ms pipe cleanup, 32 KiB stdout,
one metadata read and one filtered page. No immediate retry/backlog draining occurs.
This optional work follows the recorded check and can add that bounded command
latency to it; stop is not cancelled or delayed by a separate background collector.
Arbitrary stalled filesystem I/O is not covered by the command deadline.

A single private 0600 atomic `usage-collection.json` stores cursor and rolling
export together. The attempt is flushed before querying, so failed queries and
restarts retain spacing. Failed commits replay only from the previous durable
cursor; counters are recomputed, never added to an independent accumulator. Damage
holds collection rather than rescanning. Exact configured thread, CLI, provider,
account or BB endpoint changes reset the baseline after spacing; source changes
cannot join old and new totals. An unseen replacement behind the same endpoint or
an event-sequence rewind cannot be identified automatically: coverage remains
unknown and the old cursor is never reset on a guessed empty result.

Retain at most 128 snapshots, 16 sessions and 24 hours. First snapshots, counter
resets, unknown fields and removed history are not counted as zero. Sequence gaps
are normal in a filtered event stream and cannot prove retention completeness.
Full pages are marked potentially backlogged; no extra query is made to prove an
empty tail. No exact midnight allocation is claimed. The Panel displays last
successful read, stale after ten minutes, latest outcome, request attempts, latest
read duration and returned bytes. Those are collection overhead measurements, not
saved tokens. A successful empty page confirms the query, not provider coverage.

When enabled, the Panel reads this automatic record; otherwise it reads the
imported export. Imported evidence is never overwritten. Both remain bounded,
nonblocking regular-file reads on macOS/Linux; other platforms are unavailable.
The Panel never invokes collection and remains readable while a writer holds the
tick lock. No verified subscription savings or automatic optimization policy is
claimed. Native Linux, installed BB integration and wider attribution remain open.
