# Guided setup

looptender is the product name; the released v0.2.0 executable and commands are still `keepgoing`. The directory and filename changes below describe **unreleased source**. A v0.2.0 CLI creates `keepgoing.yaml` under its existing `keepgoing` user directory. Keep using the path printed by your installed build; a documentation rename does not migrate existing loops. See the [name and release boundary](../README.md).

`keepgoing init` builds a disabled loop without editing YAML or installing a loop skill. This command requires v0.1.2 or newer; the v0.1.1 release does not include it. Follow the [installation instructions](../README.md#install), then check `keepgoing version` before using this guide. If using a separate development binary, substitute its absolute path for `keepgoing`.

## First loop

From an existing Git project with at least one commit, run:

```sh
keepgoing init
```

Published v0.1.5 detects `codex`, `claude`, and `bb` on `PATH`. v0.2.0 also discovers Cursor through the `agent` executable, preserving the existing selection order when other CLIs are installed; the desktop `cursor` launcher is not a headless CLI. Choose a task file or enter a task with clear completion criteria, choose an agent, and accept or adjust the operating limits. The default is one agent with no fallbacks. A BB target requires an existing thread ID and its provider label; headless targets start a new session on their first wake and resume that session afterward. Setup does not attach to existing headless or IDE conversations.

The wizard shows the target, project, account directory when explicitly selected, quota groups, fallback order, operating limits, and future probe behavior. It does not run providers, test authentication, create accounts, alter permissions, enable supervision, or install a scheduler.

It writes `looptender.yaml` (releases up to v0.2.0 wrote `keepgoing.yaml`, which is still read), `prompt.md`, and, if needed, `fallbacks.yaml` into a **new** directory beneath the OS user configuration directory:

- macOS: `~/Library/Application Support/looptender/loops/<name>`
- Linux: `$XDG_CONFIG_HOME/looptender/loops/<name>` or `~/.config/looptender/loops/<name>`
- Windows: `%AppData%\looptender\loops\<name>`

On a computer where the tool already keeps its files in a `keepgoing` directory from before the rename, the directory is `keepgoing/loops/<name>` in the same place; see [per-user directories](configuration.md#per-user-directories).

Runtime state will live in that directory's `state` subdirectory. `--directory` selects a different new directory. Setup refuses paths inside the project or any Git checkout, including paths reached through existing symlinks, and refuses existing destinations even if empty. New directories use mode `0700` and files use `0600` on Unix; existing parent permissions are preserved. On Windows, filesystem ACLs govern access. Tasks and account configuration paths stay outside the checkout; account files are never copied.

**Task files are imported once.** Setup copies the entered task, stdin, or `--task-file` contents into the generated `prompt.md`. Later edits to the original task file do not update that snapshot. Supervision rereads the saved `loop.prompt_file` (generated as `prompt.md`) when preparing task prompts; edit that saved file to revise future prompts while preserving its control instructions. Already-sent messages are unchanged. looptender does not follow referenced documents or detect whether the task wording has become stale.

### Standing wake policy

The guidance above the `Task:` line in a generated `prompt.md` (how to plan, delegate, finish, and ask for a person) is the standing wake policy. It can come from the looptender build instead of the file: then each wake sends the running build's current wording, followed by the text after the file's `Task:` line, and a loop gets newer guidance from its next wake after an upgrade without anyone editing its files. A wake never rewrites the file.

A loop follows policy updates only while its `prompt.md` is, byte for byte, the file setup or `prompt-policy apply` last wrote. They record it in `prompt.md.wake-policy.json` beside it, a SHA-256 of the file and the wording version. Any edit to the file, even to the task, and even deleting a paragraph so that it reads like an older wording, ends the updates: the file is then sent exactly as written. A prompt set inline with `loop.prompt` is always sent as written.

Loops set up before this have no record and are not changed. `status` and `doctor` say which case a loop is in (`wakePolicy` in `status --json`): it follows policy updates; it matches the wording some version of setup wrote (version N) but does not follow them; it was edited after it was recorded; or it is hand-edited and matches no generated wording.

```sh
looptender prompt-policy show -c CONFIG
looptender prompt-policy apply -c CONFIG
looptender prompt-policy apply -c CONFIG --refresh-prefix        # preview only
looptender prompt-policy apply -c CONFIG --refresh-prefix --yes  # write it
```

A prompt that follows policy updates keeps the command prefix it was written with, so a prompt written for another copy of the program can tell the agent to run that copy. `status`, `doctor` and `prompt-policy show` say when the prefix runs a program file other than the one running now, or one that does not exist (`wakePolicy.commandPrefix`: `installed`, `other_build`, `missing` or `unknown`), and `doctor` warns. `apply --refresh-prefix` previews rewriting the prefix of the policy part's commands to the running program's path and writes nothing; add `--yes` to write it, with the previous file kept beside it. The same path appearing in the task text is counted in the preview and never rewritten. A hand-edited prompt with no `Task:` line is told what `apply` needs instead of being offered a command that refuses.

The same check covers any prompt, generated, hand-edited or set inline (`loop.prompt`): `status`, `doctor` and `prompt-policy show` read the prompt text for programs it names for the loop's commands, either a word that is an absolute or home-relative path named `looptender` or `keepgoing`, or any program followed by a control command (`complete`, `await-user`, `resume`, `status`, ...) and `-c` with this loop's configuration path. A program that is another file than the one running now, or does not exist, is listed in `wakePolicy.commandPrograms` (path, verdict, how often, how long ago the file changed, and `source`: `policy_prefix` for the command prefix of a generated prompt's policy part, `prompt_text` for any other place in the prompt; a generated prompt with an old prefix and another old program in its task lists both) and warned about, with the reason: a build older than the running one does not read a newer state directory safely, so its control commands can fail and it drops fields from `state.json`. Links resolve (`~/.local/bin/looptender` to `current` to `builds/<id>`, and the old `keepgoing` link name, are the same program), and a copy with the same bytes counts as the same. The named programs are never run to ask their version: they are files a prompt names, which anyone who can edit the prompt could have chosen. Only file identity, age and bytes are compared. The scan is bounded: at most 1 MiB of the prompt is read, 64 distinct programs are looked at, 16 are listed (with a count of the rest in `commandProgramsMore`), at most four same-size files are hashed, and the whole check stops after three seconds, saying it was cut short (`commandProgramsUnchecked`). Programs not looked at (past the 64, or a file of this build's size that the hashing budget did not allow comparing) are counted in `commandProgramsNotLooked`; with nothing found that still reads as "not an all-clear", and `doctor` warns, so a scan that did not finish is never reported as clean. A directory that happens to be named `looptender` or `keepgoing` is not a program and is not listed, and Windows drive paths are ignored on other systems. A prompt that is not generated wording is never rewritten; the warning says to change those commands by hand.

`apply` is how a loop starts following policy updates. It rewrites the prompt file to the current wording followed by the file's task, then records it. A file that reads as a generated wording keeps its own command path and configuration path. For any other file, the task is the text after its first line that reads exactly `Task:`, and the commands get the path of the looptender that ran `apply`, which it prints. The previous file is kept beside it as `prompt.md.before-policy-<time>`. It refuses a file with no `Task:` line, a caller other than the BB thread that owns the loop, and a moment when a check is running. Moving a loop from an older wording adds the guidance that wording lacked (see the changelog). Nothing else rewrites the file.

Review the generated task and configuration, then run the exact `start --foreground` command printed by setup. Commands include the resolved config path; Windows output uses PowerShell syntax. Start validates local files and executable availability, enables the loop, and runs the daemon in the terminal. Choose `--background` to detach it instead; the command waits for a startup acknowledgement and prints the daemon PID. Existing holds remain in effect until `resume`. See [starting supervision](operations.md#starting-supervision) for validation limits, controls, and background lifecycle. `enable` alone still does not start a supervisor. Background mode installs no service and does not restart after a crash or reboot. On macOS and Linux, a [service](services.md#the-recommended-way-to-keep-checks-running) is the recommended way to keep checks running across restarts, and `init` prints its install command; external schedulers remain available on every platform. Whatever runs the checks, `status` says when they have [stopped](operations.md#external-schedulers-can-stop-on-their-own).

The generated prompt tells the agent to honor newer user instructions, finish and verify the entire task before calling `complete`, and call `await-user` when only human-dependent work remains. The agent needs permission to run these control commands and write the private state directory. Setup preserves the provider's current sandbox and permission settings; it does not bypass a permission request. Completion holds across supervisor restarts until an explicit `resume`.

After a phase or lane finishes, new prompts ask the coordinator to verify the result and take a concrete independent authorized next step when one is available. Duplicate receipts and idle lanes are not completion or permission evidence. This is guidance for newly generated prompts, not automatic scheduling or a change to existing saved prompts; runtime holds still win.

<a id="optional-goal-during-setup-unreleased"></a>

## Optional goal during setup

In v0.1.5, `keepgoing init --with-goal` records a durable goal while creating the disabled setup. The interactive wizard asks whether to record and share a goal, suggesting yes; answer no or pass `--with-goal=false` to retain the existing prompt-only setup. Noninteractive callers must explicitly pass `--with-goal`; existing scripts retain their previous behavior. The rebuilt BB plugin exposes an explicit goal checkbox using the same flag; older plugins retain task-only setup. Published v0.1.4 does not support this flag.

The task and completion criteria are copied once into private `task.md`. The goal title starts as the loop name; both objective and acceptance references point to that absolute snapshot path. `state/goal.json` holds the generated identity and revision 1, using the existing goal store. `prompt.md` contains stable operating instructions, and `loop.prompt_goal` explicitly shares the current goal specification in future automatic messages. Task text is not repeated in that scaffold or ordinary setup output. The agent must be able to read the private snapshot; setup does not grant filesystem access or change provider permissions. The supervisor does not read the goal's references or validate completion criteria.

Review the printed `doctor` command, then explicitly start. Goal creation never enables supervision, claims an owner, starts a daemon, or makes a provider call. Preview creates no files and allocates no goal ID. A normal write failure attempts to remove only this new setup's files; a crash or failed cleanup can leave a partial disabled directory. Inspect it privately before retrying; existing destinations are never overwritten.

Use [goal revise](goals.md#revise-explicitly) to change the title or references with the expected ID/revision. Editing the original task file does not update the snapshot. Editing a referenced file does not increment the goal revision or track its contents; preserve new specifications in separate files and revise their references when history matters. Goal metadata does not bind completion to a revision or prove useful progress. The rebuilt BB setup flow now uses this contract; its live deployment, the timed first-loop walkthrough and revision-bound results remain unfinished.

Goal task input is bounded to 1 MiB of UTF-8 text; file input must be a regular file. Offline fixtures cover the released BB/Claude Code/Codex setup paths and the v0.2.0 Cursor path, preview/create consistency, source-copy behavior, fresh composition after goal revision, preserved holds, private output and cleanup. No live provider or installed loop was changed.

## Scripted setup

Every wizard choice has a flag. `--non-interactive` never asks questions; it requires a name, agent, and task or task file. It reads stdin only when `--task-file -` is explicit. Project defaults to the current directory. Prefer `--task-file` to avoid putting private task text in shell history or process arguments.

```sh
keepgoing init --non-interactive \
  --name example-loop --agent codex \
  --project /projects/example-app \
  --task-file /private/example-task.md \
  --directory /private/example-loop \
  --wake-every 1h --stall-after 30m --max-nudges 3 \
  --max-reprompts 3 --backoff 10m --interval 5m
```

Add `--dry-run` to preview without writing files. `keepgoing init --help` lists all flags. Input ending or cancellation while answering the wizard writes no setup. Existing setups are never overwritten; edit their private configuration or choose a new name/directory.

In v0.1.3 or newer, optional `--no-progress-timeout 2h` holds after two hours without verified Git metadata changes. The baseline survives turns, restart and resume. Downtime counts through v0.1.5; v0.2.0 does not count time in which no check ran or the network was down. Choose it only when Git activity is a suitable progress signal. See [no-progress timeout](operations.md#no-progress-timeout).

In v0.1.3 or newer, optional `--max-sends 20` caps automatic send attempts with a persistent counter; failed attempts count and resume does not reset it. Zero or omission leaves accounting disabled. See [send allowance](operations.md#total-send-allowance).

In v0.1.3 or newer, optional `--deadline "2030-01-01T18:00:00Z"` sets an absolute cutoff, shown in both text and JSON previews. It requires a timezone. The next ordinary check at or after the deadline holds future supervision; it does not stop a running agent. Changing the cutoff does not clear an existing hold. See [deadline behavior](operations.md#loop-deadline). `--wake-every` controls stall-nudge spacing; idle wakes follow the check interval and other gates. Neither it nor `--max-nudges` is a loop-wide turn budget.

For an existing BB thread:

```sh
keepgoing init --non-interactive \
  --name example-chat --agent bb \
  --thread thr_example --provider codex \
  --project /projects/example-app --task-file /private/example-task.md
```

This records the supplied target; it does not verify the thread's identity, switch its model/login, or transfer another supervisor. Use [doctor](diagnostics.md) for target diagnostics. Existing loops can use the separate [BB transfer commands](transfer.md), released in v0.1.4 with fixture tests; live transfer verification remains open. Unreleased source also supports an [explicit provider change](transfer.md#explicit-provider-change-unreleased-source) to an existing BB destination, preserving goal/history and budgets while adding awaiting-user. This CLI repair does not attach a manually created loop to the plugin, start supervision, migrate lanes or detect a handoff automatically. Review the destination labels/account settings before a person explicitly resumes and starts checks.

<a id="cursor-setup-unreleased-source"></a>

## Cursor setup

With v0.2.0 or newer, choose `cursor` in the wizard or pass `--agent cursor`. Published v0.1.5 does not support this choice.

```sh
keepgoing-dev init --non-interactive --agent cursor \
  --name example-cursor --project /projects/example-app \
  --task-file /private/example-task.md --with-goal --max-sends 10
```

Setup resolves `agent` on `PATH` and saves that executable, the project directory and optional `--model`. It never runs the provider or checks/changes its login; run `keepgoing doctor` afterwards to check the CLI version and stored login. Existing Cursor credentials and permissions are inherited, and Cursor must already trust the project directory or the first turn fails before it starts. `--account-dir` and `--fallback` are rejected for Cursor without writing a setup; the wizard skips those questions. Cursor is also unavailable as a fallback for other agents. `--dry-run` and the private stdin/JSON path below work as for other targets.

This starts a separate CLI conversation on the first wake. Existing IDE chats, interactive permission bridging, quota/reset detection and automatic fallback recovery are unavailable. Text/JSON previews and doctor disclose these limits; reading a version string does not establish a supported version range. Review [Cursor evidence and remaining live checks](cursor.md) before starting. Creation stays disabled, including with an optional goal.

## Structured setup for integrations

`init --non-interactive --json` prints one versioned JSON result on success. Add `--dry-run` for a preview; omit it to create the private setup. Both use the same validation and configuration generation as the wizard. The ordinary text output remains the default. JSON requires noninteractive mode so wizard questions cannot corrupt stdout.

For task entry from an integration, pass `--task-file -`, write UTF-8 task text to the child's stdin, and close the pipe. This keeps task text out of command arguments and avoids a temporary task file. Input is limited to 1 MiB before whitespace trimming; empty, oversized, invalid UTF-8, and failed reads are rejected before any setup files are written. Signal cancellation interrupts a blocked stdin read. `--task-file -` also works with ordinary text output, but always requires `--non-interactive`. Use `./-` to read a file literally named `-`.

For example, in a POSIX shell:

```sh
keepgoing init --non-interactive --json --dry-run \
  --name example-chat --agent bb \
  --thread thr_example --provider codex \
  --project /projects/example-app --task-file - < /private/example-task.md
```

Schema version 1 has these fields:

| Field | Meaning |
| --- | --- |
| `schemaVersion`, `version` | Contract version and CLI build label. Reject unsupported contract versions. |
| `outcome` | `preview` means no files written; `created` means private setup files were written. Neither starts supervision. |
| `startJson` | v0.2.0. `true` when this build's `start` accepts `--background --json`, which reports a [second loop on one session](operations.md#one-loop-per-session) as a structured refusal. Absent in older results: do not pass the flag. |
| `loop`, `project`, `configPath`, `stateDir`, `promptPath` | Resolved setup identity and paths. These paths are private metadata; handle the response accordingly. |
| `taskPath`, `goal` | Additive fields in v0.1.5, present only with `--with-goal`. `taskPath` identifies the private snapshot; `goal` describes `promptContext: true`, `titleSource: "loop_name"`, `specificationSource: "task_snapshot"`, and, after creation only, `record` with generated ID/revision/state. Preview has no saved record. Private task text and goal references are omitted. |
| `taskBinding` | `snapshot`: task input is imported once into `promptPath` (or `taskPath` for goal setup in v0.1.5); the original input is not tracked. Added in v0.1.4; absent in older results means this metadata is unavailable. |
| `taskSource` | `inline`, `file`, or `stdin`; never the task text or source filename. |
| `primary` | An `agent` reference (adapter, provider, group, and applicable thread/workdir/model fields) and `account` selection (`inherited` or `explicit_directory`). BB inherits the existing thread's login/model. |
| `fallbacks` | Ordered array of `{id, target}`, each target with the same shape as `primary`; empty array for a single agent. |
| `limits` | `wakeEvery`, `stallAfter`, `interval`, `backoff` as Go duration strings; `maxNudges`, `maxReprompts`, and `failoverOn` (empty or rate-limit-only); optional `deadline`, `maxSends` and `noProgressTimeout` (v0.1.3+). |
| `probes` | Present only with fallbacks: `every`, `ttl`, `dailyCapPerCandidate`, `exactOutsideCap`, and `manualOutsideCap`. The cap does not include exact/manual calls. No probe runs during setup. |
| `next` | `doctor`, `startForeground`, and `startBackground` argv arrays, including the executable and exact config path. Execute as argument arrays, never concatenate into a shell command. These are suggestions, not actions performed by init. |
| `limitations` | Human-readable scope and review reminders; do not parse these strings to make decisions. |

**Refused (v0.2.0).** When another enabled loop already supervises the same session (see [one loop per session](operations.md#one-loop-per-session)), `init --json` writes nothing, exits 1, and prints exactly `{"schemaVersion":1,"outcome":"refused","refusal":{"code":"second_loop_on_session","otherLoop":"example-loop"}}` on stdout: `outcome: "refused"`, a `refusal.code`, and the other loop's name in `refusal.otherLoop`, never its path. The sentence for people, which does name the other loop's configuration, is on stderr. Show `otherLoop`; do not parse stderr. `--allow-second-loop` sets the loop up anyway. Other codes may be added; treat an unknown code as a refusal without a reason.

The result omits task/prompt text, raw YAML, environment maps, and account-directory values. It does not verify the supplied BB identity, provider/model availability, authenticated subscription, independent quota, or scheduler state. After creation, an integration can use the existing `doctor --json` flow to display diagnostics, then offer an explicit start choice. Review the resolved target and limits before starting; do not automatically run `next` commands when parsing a result. Preview paths do not yet exist, and a preview does not reserve them or guarantee later creation will succeed.

Validation, input, and setup-write failures exit nonzero with stderr diagnostics and no success JSON. A stdout failure may occur after successful file creation and may leave partial JSON: require both exit 0 and a complete supported result, inspect the destination before retrying, and never overwrite it automatically. Help retains the normal CLI help behavior.

Offline fixtures cover preview/create consistency, BB identity fields, ordered account selections, omitted private input, bounded/cancelled stdin, failed writes, and unchanged existing setups. The separately distributed [BB setup UI](../integrations/bb-plugin-keepgoing/README.md#supervise-this-thread) uses this shared CLI contract and is fixture-tested; live deployment and install-to-completion verification remain open. CLI setup alone does not install the plugin or attach/transfer a thread. Unreleased plugin source adds an [explicit registry attachment flow](../integrations/bb-plugin-keepgoing/README.md#attach-an-existing-loop-unreleased-source) for an already-owned existing BB target; this imports only the plugin reference. It does not start/resume a loop or repair startup permissions. Inspect first-check evidence separately from daemon responsiveness.

## A takeover for a BB loop (unreleased)

**Offline fixtures and stand-in scenarios only; nothing created live.** A BB loop with a single route waits out any usage limit, which can be days. `init` can instead set up a takeover: one [new-thread fallback](fallbacks.md#hand-off-to-a-new-conversation) on another provider, with handoff on for rate limits. At a usage limit on the loop's provider, a new conversation on the other provider is started with a handoff brief and owns the loop from then on; this conversation's child lanes move to it; lanes blocked by the limit get successor threads on the new provider (at most 5 per handoff). The existing caps apply: at most 3 new conversations over the loop's life (`loop.limits.max_new_threads`), never two within an hour, and the permission mode is `accept-edits` unless you give another. Each new conversation and each successor is a paid turn and a send against `loop.limits.max_sends`. With `accept-edits`, an unattended new conversation or successor asks before anything but file edits, so it may stop at a permission question and wait for a person.

**When another provider counts as usable.** Only from metadata, with no login check and no model call:

- `bb provider list --json` reports it as available, it is not the loop's own provider, and it is `claude-code` or `codex`;
- its local CLI (`claude` or `codex`) is on `PATH`, since a new-thread fallback is checked with it before any handoff; and
- `bb provider models <provider> --json` names a default model, which is offered.

Any failure or other answer means no provider is known, and setup is as before: a single route, with the warning that a limit stops the loop.

**The wizard** offers it after the [standard route list](routes.md) offer, and only when that offer was not taken or not made. It first prints what a yes sets up (the sentence below), then asks "On a usage limit, start a new conversation on codex that takes over this loop, with its child lanes? (yes/no) [yes]", then the model, defaulting to the one BB names. **A script** gets it only by asking: `--new-thread-provider claude-code|codex --new-thread-model M`, with optional `--new-thread-reasoning` and `--new-thread-permission-mode`. Without them it keeps today's single route and runs no bb command at all, as before. With `--detect-takeover` it asks bb which provider is usable (the two metadata reads above, nothing else); when one is, it prints a `Takeover: not set up` line naming the flags, and `--json` carries `newThreadOffer` (provider and model). The wizard always asks bb, since it offers the takeover. `--new-thread-provider none` declines it. These flags are for BB loops only, and exclude `--fallback` and `--routes standard`. The preview and the result say exactly what happens at a limit; in `--json`, each fallback that starts a new conversation carries `newThread` (provider, model, permission, whether children follow and blocked lanes get successors, and that sentence).

**An existing loop** is not changed by an update. Arm it deliberately with `keepgoing fallbacks arm -c looptender.yaml`: it finds the usable provider the same way (or takes `--provider` and `--model`), says what will happen, previews the `fallbacks add` change, and writes it after a yes (or with `--yes`; `--dry-run` only previews). It turns handoff on for rate limits for the whole loop: when the loop already has other fallbacks with handoff off, they start taking handoffs too, in their order and before the new conversation, and arm says so. It keeps `fallbacks add`'s ownership check (the loop's owner, or a terminal with no BB identity), and says so when the loop is already armed. `doctor`'s `routes.single` warning for a BB loop names this command.

## Existing accounts and ordered fallbacks

`--account-dir` selects an existing headless account configuration directory using `CODEX_HOME` or `CLAUDE_CONFIG_DIR`. Omitting it inherits the launching environment and the provider's normal login. `--model` chooses a primary headless model; fallback models inherit their respective CLI configurations. Advanced model/probe settings remain available in [configuration](configuration.md) and [fallbacks](fallbacks.md).

**Named accounts** (unreleased). `--account ID` names an [accounts catalog](accounts.md#say-which-account-a-route-uses) entry for the first route: a Claude Code or Codex entry's directory is used when `--account-dir` is not given, the entry's name becomes the default `--group`, and `account: ID` is written to `loop.account`. An unknown ID, an unreadable catalog, an entry for another provider, an entry whose directory differs from `--account-dir`, and an entry on the normal login for a route given its own `--account-dir` are refused, naming the entries that fit, and nothing is written. For a BB thread the entry must be for BB or for the thread's provider; beyond that the statement is taken as given, since BB chooses the account per project. The wizard lists the catalog's entries for the agent by name (for a BB thread, those for its provider and for BB) and takes a number, a directory or a blank answer; it does the same for each fallback it asks about. A typed directory states no account. The JSON result carries the ID as `primary.catalogAccount`. The catalog is only read. A build from before the `account` field refuses a configuration that has it: see [Upgrade first](accounts.md#say-which-account-a-route-uses) before naming an account.

Give each independent quota pool a label using letters, digits, hyphens, underscores, or dots. Accounts sharing limits must share a label: changing a label or using a different directory does not create independent quota. Setup cannot verify which subscription a directory authenticates. It refuses fallback labels already used by an earlier target, since those targets cannot provide independent recovery.

Repeat `--fallback ADAPTER[:GROUP][=ACCOUNT_DIR]` in preference order. Without an explicit group, the provider label is used (`codex` or `claude-code`). The wizard asks these fields separately. For three already authenticated independent accounts:

```sh
keepgoing init --non-interactive \
  --name example-accounts --agent claude \
  --account-dir /private/accounts/claude-a --group pool-a \
  --fallback codex:pool-b=/private/accounts/codex-b \
  --fallback claude:pool-c=/private/accounts/claude-c \
  --probe-daily-cap 2 \
  --project /projects/example-app --task-file /private/example-task.md
```

**Following your standard route list instead.** If you keep a [standard route list](routes.md), the wizard asks `Follow your standard route list for handoffs? N of its M switched-on routes apply to this loop. (yes/no) [yes]` for a `bb`, `claude` or `codex` loop, before the fallback questions, and only when at least one route applies; yes skips them, and the loop gets `routes: standard` with handoff on for rate limits. A script follows the list only with `--routes standard`, never because a list exists; `--routes standard` and `--fallback` exclude each other, and `--routes standard` is refused while no usable list is written or the list has no routes (`routes: []`). The JSON `limits.failoverOn` is what is written: `[rate_limit]` for a following loop, even while no route applies yet. The JSON result says `routes: "standard"` and lists the routes that apply to the loop now as its `fallbacks`. Probes follow the list's policy, not `--probe-daily-cap`.

To add a fallback to a loop that already exists, v0.2.0 provides the guided [`keepgoing fallbacks add`](fallbacks.md#add-a-fallback-with-guided-setup).

Generated fallbacks use redundancy `min_healthy: 1`, `span: group`. After you enable and run supervision, scheduled probes run at most every 12 hours per candidate, with a 24-hour health TTL and a default cap of two scheduled calls per candidate per UTC day. Active-group candidates are skipped. Exact handoff probes and manually requested probes are additional calls outside that cap. No calls occur during setup. Handoffs are allowed only for structured `rate_limit` errors and require an exact target probe. Switch-back stays off.

The generated setup and account routing are covered by tests with fake provider discovery and temporary Git repositories. Real login verification, paid multi-account handoff, and a fresh install-to-completion walkthrough remain separate acceptance checks; setup success does not claim those passed.

## Telegram remote alerts (unreleased)

Unreleased source adds zero-infrastructure local pairing for a personal `@BotFather` bot over outbound HTTPS:

```sh
looptender telegram setup --token-stdin
looptender telegram status
looptender telegram test --dry-run
looptender telegram disconnect
```

Pairing writes the bot token to a private `0600` `token.env` file and locks `pairing.json` strictly to the paired `allowed_user_id` and `allowed_chat_id` via a one-time `?start=pair_<nonce>` deep link. Default `alerts` mode sends redacted event summaries only (`loop.report.format: telegram`); `--mode chat` requires explicit `--accept-non-e2e` because Telegram Bot API traffic is not end-to-end encrypted. Current coverage uses offline HTTP stand-ins and terminal QR decoding only; phone scanning, delivery/poller diagnostics, background service wiring, `TG5` and live Telegram verification remain open. Add `--qr` to setup for a local terminal QR of the same link; the plain link is always printed. QR needs ANSI black/white colors and Unicode half-block characters without wrapping. Encoding failure or an unsuitable terminal leaves the text link as the fallback. `doctor --telegram --offline` supplies local pairing/token-file-presence diagnostics only. See [Telegram remote channel](telegram.md).

