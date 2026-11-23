# Sessionpipe integration (unreleased)

Source has two separate sessionpipe integrations:

- **IN, [observed sessions](#observed-sessions-in-unreleased):** `adapter: sessionpipe`
  observes one agent session that sessionpipe reports, from a tier-0 file sink
  the person adds. The session is observed, never woken: looptender never sends
  to, wakes, re-prompts or stops it.
- **OUT, reporting:** opt-in reports to a receiver the person runs, described
  from [Setup](#setup) on. The report format is not a supervision adapter. It
  cannot observe, wake, send to, stop or own an agent session, answer a hold, or
  change routes. Supervision continues through the loop's existing adapter,
  controls, ownership and budgets. Delivery results never classify a
provider error or decide failover. Unknown, refused and restricted provider
results retain their existing handling.

## Observed sessions (IN, unreleased)

A loop with `adapter: sessionpipe` watches one session of any agent sessionpipe
hooks (Claude Code, Codex, Gemini CLI, Antigravity, Devin and the others its
README lists), including an interactive session looptender did not start and
cannot reach. It reads one file and runs nothing during a check.

### Try it in five minutes

You need sessionpipe 0.18.0 or later with its hooks installed
(`sessionpipe --version`; `sessionpipe install` if it was never run) and one
Claude Code session of your own that you can type into. Nothing below sends
anything to that session.

1. **Find the session's id.** In that Claude Code session, type
   `! echo $CLAUDE_CODE_SESSION_ID`. The session is `claude-code:` followed by
   the id it prints. For another agent, take `session.id` from
   `sessionpipe tail --tier 0 --json` while the session runs (the text form of
   `tail` shows only the id's first characters).
2. **Set up the loop.** At a terminal:

   ```sh
   looptender init --agent sessionpipe --session claude-code:<id>
   ```

   The loop is named after the session (`observed-claude-code-` and the id's
   first eight letters or digits) unless you pass `--name`. It writes a
   private setup directory and prints three commands. The events
   file defaults to `sessionpipe-events.jsonl` inside that directory, which
   only you can read. Pass `--events-file PATH` to choose another, and
   `--non-interactive` (with `--json` for a script) to be asked nothing.
3. **Add the sink.** Run the `sessionpipe sink add file:… --tier 0 --name looptender`
   line that init printed, exactly as printed, once, at the computer's own
   terminal. looptender never runs it: it changes sessionpipe's configuration,
   which is yours. sessionpipe may first write what its outbox already holds;
   looptender reads a backlog over several checks.
4. **Check it.** Run the `doctor -c …` command init printed. Its checks go in
   setup order (the sessionpipe command, its version, its hooks, the sink, the
   events file) and its last line, `next`, names the first step that needs
   you and what to do. Before the session's first event after the sink was
   added, it says to send the session one message.
5. **Start it.** Run the `start --foreground -c …` command init printed, then
   use the session as usual: `looptender status -c …` in another terminal says
   whether it is working, between turns or waiting on you, and that it is
   observed, never woken. Stop with Ctrl-C, or `looptender stop -c …`, which
   ends the checks and leaves the session alone.

To undo it, remove the sink with `sessionpipe sink remove looptender` and
delete the setup directory.

### Set up an observed session by hand

Requires sessionpipe 0.18.0 or later with its hooks installed for the agent
(`sessionpipe install`). Add a tier-0 file sink once, at the computer's own
terminal, with an absolute path in a folder only you can read:

```sh
sessionpipe sink add file:/home/example/.local/state/looptender-sessionpipe/events.jsonl --tier 0 --name looptender
```

On Windows use the same command with a Windows path. sessionpipe creates the
file with the first event after the sink is added. Find the session's full id
in `session.id` of `sessionpipe tail --tier 0 --json` while it runs (the text
form shows only its first characters), or in your receiver. Then:

```yaml
version: 1
loop:
  name: example-observed
  state_dir: ~/.local/state/looptender/example-observed
  adapter: sessionpipe
  sessionpipe:
    session: claude-code:8c132906-8c3f-4814-870a-afc3d05e1d2e
    events_file: /home/example/.local/state/looptender-sessionpipe/events.jsonl
    # stale_after: 10m
```

There is no `prompt`: nothing is ever sent. `doctor -c looptender.yaml` checks
the setup (below); `tick` and `daemon` then supervise it like any loop.
`looptender init --agent sessionpipe` writes the same configuration for you
and prints the sink line with your path.

### What it reads

The sink writes every session's events on this computer. looptender keeps the
lines of the one configured session and, of those, only these:

| Event (tier 0) | Reads as |
| --- | --- |
| `session.started` | idle |
| `turn.started`, `session.heartbeat` | active (a heartbeat arrives at most once a minute while a turn runs) |
| `turn.ended` with reason `stop` or `interrupt` | idle, with the `background` count it reports |
| `turn.ended` with reason `error`, `attention.needed` of kind `error` | error, of class unknown |
| `attention.needed` of kind `permission`, `question` or `elicitation` | waiting on a person, until `attention.cleared` or the session's next turn event |
| `attention.needed` of kind `idle` | idle |
| `session.ended`, `session.forgotten` | gone: the loop holds for a person |

- **Silence is unknown, never idle.** A session that has said nothing for
  `stale_after` reads unknown, so does one not yet seen, and so does the file
  while a backlog over 4 MiB is being read over several checks. Four harnesses
  never send `session.ended`.
- **No text is kept.** Each line is decoded into a fixed set of fields:
  protocol, type, time, tier, harness name, session id and sequence, and the
  data keys `reason`, `kind`, `attention_id`, `background` and `turn_id`. Each
  value must be one of the protocol's words or an id of its character set.
  Turn and attention ids are kept only as a short digest, never as written.
  The session's title (by default the first prompt, cut short), folder,
  repository, branch, model, account and every other key are never decoded,
  stored, logged or shown. Unknown and extension event types are ignored.
- **Tier 0 only.** Every line must say tier 0 and protocol 1. One that does not
  makes the whole file unreadable for this loop until it is replaced: a higher
  tier carries words and paths. A line over 64 KiB or that is not JSON is
  skipped.
- **Only a regular file only you can write.** The path is checked before it is
  opened, following a link: a FIFO, device or directory is refused without
  being opened, so a check never waits on it, and on Unix the open does not
  block either. On Unix a file that group or other users can write is not read,
  since they could make the session look waiting, ended or idle (`chmod go-w`).
  On Windows mode bits do not say who can write a file, so a check does not
  refuse one; doctor's DACL check reports it.
- **Bounded and restartable.** A check reads at most 4 MiB. The reader keeps
  its position, the file's size and identity (device and inode on Unix, volume
  and file index on Windows), a hash of its first 256 bytes and the session's
  state in `targets/<id>/sessionpipe-read.json` (owner-only). A file that got
  smaller, has another identity or starts differently was replaced, rotated or
  truncated and is read again from its start. Sequence numbers already applied
  are not applied again.
- **Never the outbox, never a command.** looptender does not read sessionpipe's
  outbox, which keeps every event at full detail, and runs no sessionpipe command
  during a check.

### Observed, never woken

Every rule that would wake, nudge, re-prompt, stop, hand off or switch the loop
records why it did not, and the reason ends "observed only: looptender never
sends to, wakes or stops this session". A gone session holds for a person, as
any gone target does. `status` prints an `observed:` line and reports `task:
none`; its JSON marks `targetAgent.observed`. The dock and panel JSON set
`observed` on the loop, and a loop's `can` never holds `stop` for an observed
target: it holds `pause` when the loop is not paused and `resume` when a hold
is set. Pause and resume change only whether looptender checks the session;
nothing is ever sent. The panel list carries that sentence as `observedNote`
on the loop's row, as the dock does, for a page or menu to show.
`looptender stop` is still accepted: it ends the checks and leaves the session
alone.

### Doctor

`doctor` runs only `sessionpipe --version` and `sessionpipe status --json`, and
prints neither output, the events file's path, nor anything in it. The checks
run in setup order, and the last, `next`, names the first one that failed
(an error before a warning) with its next step, or says every check passed:

| Check | Fails when |
| --- | --- |
| `agent.<id>.observed` | never: it says the target is observed, never woken |
| `agent.<id>.cli` | the `sessionpipe` command is not found (warning: the file is still checked) |
| `agent.<id>.version` | sessionpipe is older than 0.18.0 |
| `agent.<id>.hooks` | sessionpipe has no current hooks for the session's harness |
| `agent.<id>.sink` | no file sink writes the events file, or it sends above tier 0 (warning when it is paused) |
| `agent.<id>.events_file` | the file is missing (a warning when the sink is set up and only no event was written yet) or not a regular file, or other users can write it (warning when they can read it). Windows reports the DACL check instead of mode bits |
| `agent.<id>.limits` | never: it says usage and error classes are not read |
| `agent.<id>.next` | some step above needs attention: it repeats that step's fix |

Without the `sessionpipe` command, doctor warns and still checks the file.
`--offline` runs nothing.

### Not in this slice

- **Error classes and usage.** sessionpipe sends a turn's error class and its
  usage windows only at tier 1, which also carries tool names, file paths and
  the attention message. So every error is unknown, and no limit or reset is
  read. Reading them needs sessionpipe to offer those words or numbers at tier 0.
- **Send, stop and takeover.** sessionpipe's `ask` can reach an existing session
  and start a new one, but it waits for the answer and returns its text, has no
  local cancel and takes no account or effort. None of it is used.
- **Children, context window and per-turn usage.**
- **Fallbacks, routes, switch-back and timing**, which configuration refuses.
- **Live acceptance.** Every behaviour above is tested with fixture files and a
  stand-in for the sessionpipe command, on no real session.

## Setup

Run and configure your own Sessionpipe receiver separately. Supply its **origin**
(no path, query, fragment or embedded credentials) and sink bearer token through
the existing report secret environment or private secret file. HTTPS is required
except on localhost/127.0.0.1/::1. No hosted destination is selected automatically.
This change installs nothing, pairs nothing and touches no accounts or outbox.

```yaml
loop:
  # Other loop settings and the existing agent adapter remain required.
  report:
    format: sessionpipe
    scheme: bearer
    url_env: EXAMPLE_SESSIONPIPE_ORIGIN
    secret_env: EXAMPLE_SESSIONPIPE_TOKEN
    events: [await_user, complete, stop, error]
    timeout: 15s
```

Set the environment privately; do not put tokens in YAML or command arguments.
The configured URL identifies an origin such as `https://receiver.example.com`.
The receiver must publish `/.well-known/sessionpipe`, advertise protocol 1,
`events`, bearer authentication, a tier cap from 0 to 3, and a same-origin events
endpoint. Discovery is unauthenticated. Cross-origin endpoints and redirects are
refused, so discovery cannot forward the token elsewhere. Receivers requiring
authenticated discovery are not supported by this slice.

Preview without network or persistent state using the built source command:

```sh
keepgoing report test --dry-run -c looptender.yaml
```

The preview uses an all-zero example session identity and sequences 1 and 2;
event IDs are illustrative. To send a real test, explicitly run `keepgoing report
test -c looptender.yaml`. It sends an extension event, not a human attention or
an agent wake. That live command was not exercised for this implementation.

## Payload and mapping

Only tier 0 is supported. The harness is `looptender`, with a random opaque
session identity for this loop's report stream. Loop names, `report.source`,
task text, `Event.Text`, arbitrary `Event.Data`, paths, hostnames, model/account
identities, report error details and transcripts are omitted. No tier selection
can enable their transmission.

Each report is a two-event batch. `session.started` with source `resume` precedes
the mapped report so even a receiver that missed earlier batches sees a start.
It describes the report stream, not the creation or resumption of an agent.
Both events have distinct ULIDs and increasing sequences.

| Report | Sessionpipe event | Tier-0 data |
| --- | --- | --- |
| `await_user`, `error`, `child_error`, `hung_turn`, `refusal_streak` | `attention.needed` | opaque attention ID, kind `error` |
| `complete`, `stop` | `session.ended` | reason `other`; completion is a declaration, not verified task success |
| All other subscribed report types | `looptender.<type>` | empty |

Unknown Sessionpipe types must be stored by receivers, but a board may not render
their meaning. In particular, `limit_wait` remains `looptender.limit_wait`, not a
usage reading or a claim that work is running. The protocol's `limits.observed`
and attention words require tier 1, which this integration never sends. A board
can show needs-you/ended facts; it cannot explain the hold or certify work state
from this report stream. There is no periodic heartbeat, attention-answer flow,
phone push or GUI acceptance claim. Repeated starts and later events can affect a
receiver's attention display; use looptender status as the authority for holds.

## State and delivery limits

`sessionpipe.json` in the loop's private state directory holds schema 1, the
opaque identity and the next sequence. The existing nonwaiting `delivery` lock
serializes reservations across callers, including separate reporter instances.
Two sequences are reserved and atomically published before the POST. Contention,
corrupt/oversized/nonregular state or publication failure refuses that send. Existing snapshots require non-null version, identity and next-sequence fields; defaults apply only when the file is genuinely absent. Invalid snapshots remain unchanged.
Sequences consumed by failures are not reused. Retain this file across restart
and migration; do not copy the same loop state to concurrent independent owners.
Removing it starts a new report identity. Publication uses the existing private
snapshot primitives; it does not promise power-loss durability.

Discovery and POST share a maximum 15-second deadline (or the shorter configured
report timeout). Responses are bounded to 64 KiB. The two-event body must fit the
receiver's advertised batch limits; no splitting occurs. One POST is attempted,
without redirects or automatic retry. A network failure, unexpected status,
malformed response, rejected event or acknowledgement count mismatch leaves
delivery unverified and consumes its sequence reservation. Only HTTP 202 with
accepted plus duplicate counts totaling two, no rejected events and a valid tier
cap is recorded as HTTP accepted. No HTTP acknowledgement proves board or phone
receipt. Diagnostic history remains in `deliveries.json`; the new `protocol_failed` outcome makes older strict history readers report unavailable until upgraded (it does not change loop state); discovery/setup failure
has no POST attempt, and protocol failure is recorded separately.

This is **best-effort reporting**, not a conformant durable Sessionpipe sender:
it has no outbox, cursor, indefinite backoff, replay or daily discovery cache. Authentication failures now have the report-only pause described below. It does not retry uncertain delivery;
a later report is a new event with new sequences. Discovery runs per report.
An attempted Sessionpipe POST that fails or has an uncertain acknowledgement returns the shared transport marker `report.ErrNoRetry`. Once-per-episode supervision reports persist `retryStopped: true` while retaining `sent: false` and the actual attempt count; later checks and fresh reporter instances do not repeat that episode, even after receiver recovery. A new episode starts its own accounting. Independent per-check failed-check reports remain new events and can still send; this is not global notification deduplication. Failures before POST remain eligible for the existing bounded episode retry, as no delivery was attempted. An accepted POST followed by diagnostic-history failure also forbids retry. Ordinary webhook backend retry behavior is unchanged.

The optional `OnceReport.retryStopped` state field is ignored by older builds; downgrade does not preserve this reporting suppression. Persistence depends on the check successfully publishing its state. A crash after POST but before that publication is not protected by an atomic delivery journal; durable episode replay/suppression remains follow-up work. Report history failure never changes loop holds or authorizes another agent send.
The existing report event subscription still applies. No delivery outcome is
used as a provider limit, refusal, policy or availability signal.

## Authentication pause and explicit resume

A POST returning HTTP 401 persists `unauthorized` in private schema-1
`sessionpipe-gate.json`. Subsequent reports, including from fresh processes or
with changed credentials, refuse discovery and POST until a person explicitly
runs:

```sh
keepgoing report resume -c looptender.yaml
```

Inspect/fix receiver credentials first. The command sends nothing, does not test
credentials, replay failed batches or retry stopped episodes, and leaves loop
holds, ownership, budgets and sequence identity unchanged. Only future reports
are enabled. It requires Sessionpipe configuration and current caller ownership,
refuses transfer fences and a running check, and rejects dry-run/extra arguments.
`status` and `doctor` show the report gate without contacting the receiver.

A separate nonwaiting `sessionpipe` lock serializes network reporting and resume;
contention refuses a report rather than waiting. Before POST the gate is atomically
published as `pending`. Exit before `client.Do` returns, or failure to publish the final gate, leaves
it closed; explicit resume is required even if delivery is unknown. On return
from `client.Do` (response headers or a transport error), a 401 retains the pause;
other results immediately return the gate to `ready`, before acknowledgement-body
validation or history publication. Pending therefore does not protect a crash
during acknowledgement/history processing. The existing no-retry-per-episode
contract still applies; this is not a durable delivery journal.
Corrupt, oversized, nonregular or missing/null/duplicate or incorrectly cased gate fields refuse network
and resume without replacing the evidence. Genuine absence means ready for
compatibility with existing state. The gate contains no URL, token, payload
or raw error. Migration fences the new lock and moves this state with the loop.

Older builds ignore the new gate. All report writers must be upgraded to retain
the pause; downgrade is not compatible with that guarantee. The guarantee uses
existing atomic snapshot primitives, not a power-loss durability claim. Discovery
failure (including unauthenticated discovery rejection) still has no POST attempt
and does not create an authentication pause. HTTP 403, 429 and 5xx do not change
provider classifications or loop authority; this slice adds no automatic retries.

## Evidence and remaining scope

The integration follows the upstream draft
[protocol v1](https://github.com/spacesheep-dev/sessionpipe/blob/06b826852a8b71b0306e47598dab719bee8fb130/spec/PROTOCOL.md),
[HTTP binding](https://github.com/spacesheep-dev/sessionpipe/blob/06b826852a8b71b0306e47598dab719bee8fb130/spec/HTTP.md)
and [privacy rules](https://github.com/spacesheep-dev/sessionpipe/blob/06b826852a8b71b0306e47598dab719bee8fb130/spec/PRIVACY.md),
rechecked from the primary public repository at `06b826852a8b71b0306e47598dab719bee8fb130` after review. The specification is licensed CC BY 4.0;
fixtures here use fictional envelopes derived from its documented shapes, with
no copied transcripts. This is a narrow integration, not full upstream
conformance certification. Initial implementation read a local fork checkout at `baf8022`; that revision is not present in the primary repository, and the original pinned public links were unavailable. The published protocol/HTTP/privacy source at `06b8268` retains the tier-0 fields and transport used here; links now name that accessible revision. The earlier roadmap research used core `8eada32` and
GUI `c7a813d`; the current protocol was rechecked before implementation.

Regression fixtures cover privacy, restart identity/sequence, reservation
contention/concurrency, corrupted state, acknowledgement mismatch and bounded
transport. See [testing](testing.md#sessionpipe-reporting-integration-unreleased).
Live receiver, GUI, phone, installed artifacts and upstream conformance remain
unverified. Local native fixtures ran on macOS; record exact-head hosted native
fixture execution separately in the PR, without treating it as live platform
acceptance. No subprocess or OS control is added.

**IN is separate:** reading a configured filtered tier-0 file sink needs F8 typed
intake and an owner decision about observing a loop that cannot be woken. Never
read the full-detail outbox. Tier-0 input has no usage limits; silence is unknown,
not idle. No general Sessionpipe ingest CLI or API for agent observation/send/stop
was established by this work. Those functions are not exposed speculatively.

**BACK remains held by #233:** the signed control channel targets harnesses and
has no documented command for resuming a looptender hold. It requires an explicit
owner decision and upstream protocol/daemon/board support. The OUT slice grants no
remote-answer or coordinator-message authority.

## Protocol coverage matrix

| Surface | Supported and exercised | Deliberate limits / next work |
| --- | --- | --- |
| Compatibility | Protocol integer 1; published draft core source `06b8268`, local fork built core 0.11.0 schema/data/privacy validation | Earlier research pins are not acceptance evidence; future incompatible integers refuse. No release or installed-package claim. |
| Discovery | Origin-root GET, protocol/events/bearer/tier checks; same-origin endpoint; absent batch limits use 50/262144 | Per-report read, no 24-hour cache; no authenticated discovery, redirected discovery or foreign endpoint. |
| Event endpoint | POST `{events:[...]}`, content type, bearer and `sessionpipe-protocol: 1`; advertised two-event/byte bounds | No native endpoint, file sink, outbox, control or pairing endpoint. No webhook HMAC is needed for v1 bearer delivery. |
| Envelopes / fields | Protocol, ULID, fixed harness, opaque session ID, reserved seq, UTC milliseconds, tier 0, empty privacy rulesets, typed data | No source/loop/task names, transcript, arbitrary report data, account/model, machine/path/repo/title/URL or attention words. |
| Event vocabulary | `session.started`, `attention.needed`, `session.ended`; empty-data `looptender.<report>` extensions | No invented provider turns, limit readings, periodic heartbeat, attention-cleared/answer assertion, backfill or forget. Extensions may need a receiver view. |
| Authentication / errors | Explicit bearer config; token withheld from discovery and errors; HTTP/transport failures diagnostic only; persistent POST-401 pause; no redirects | No automatic reauth, login or credential copying. Resume affects future reports only; no durable replay. Error classification never reaches routing. |
| Acknowledgements | Only 202; accepted+duplicates exactly two, zero rejected, valid tier; duplicate acknowledgement accepted | Invalid/oversized/missing/rejected/mismatched responses remain unverified; no automatic resend or cursor advance. |
| Restart / concurrency | Fresh CLI processes retain identity and sequences; private two-sequence reservation under nonwaiting delivery lock; separate nonwaiting report gate serializes sends/resume; rejected/corrupt state refuses send | Contention can lose a best-effort report. No replay/outbox/indefinite retry, crash-before-POST recovery or power-loss guarantee. |
| Evidence | Native macOS fictional Go receivers, race tests, optional local demo with actual built core schema/data/filter | Local reference receiver package is a placeholder. This is not upstream receiver/conformance certification, GUI/phone or live provider acceptance. |
| IN | [Observed sessions](#observed-sessions-in-unreleased): one session from a person-added tier-0 file sink, allowlisted types and words, never woken | No error classes or usage (tier 1 only); no send, stop or takeover; no live acceptance. |
| BACK | None exposed; #233 remains held | Needs owner authorization plus upstream non-harness control dispatch and a looptender-hold command/board support. Existing prompt/cancel commands cannot grant loop authority. |

## Repeatable local demo and upstream documentation preparation

With an existing built Sessionpipe core checkout (no installation is performed):

```sh
go build -o /tmp/example-keepgoing ./cmd/looptender
node scripts/sessionpipe-demo.mjs /tmp/example-keepgoing /path/to/sessionpipe/packages/core
```

The fixture binds an ephemeral loopback receiver, imports the actual core
`Batch`, `EventData`, `BatchResponse` and `filterEvent`, and starts two bounded
`report test` CLI processes with fictional configuration. It validates the wire
shape, tier-0 filtering, private-field exclusion and identity/sequences across
process restart, then removes its temporary state. It does not start a provider,
Sessionpipe daemon or GUI. Node 20+ and already-built core dependencies are
prerequisites; no npm download or upstream build runs. Use only a trusted checkout.

The local demo was exercised against existing core 0.11.0 artifacts; those
artifacts were not rebuilt or certified against upstream HEAD. The primary
reference receiver's source entry was still a placeholder when read. The demo
therefore uses our fixture HTTP endpoint with real core validation, not a claim
that the upstream receiver accepted events.

The [upstream integration contribution draft](sessionpipe-upstream.md) is ready
for review with this change. It is not submitted or published. The next
maintainable OUT work is durable pending batches, restart replay with the same
identity/seq, bounded worker backoff, proved
with core delivery scenarios. Those are local implementation gaps, not missing
upstream protocol capabilities. IN requires local ownership/intake work; BACK has
an actual documented upstream capability gap as well as the owner hold.
