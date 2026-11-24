# looptender menu preview

Unreleased source displays **looptender Menu** and **Open looptender Panel**. The app is `LooptenderMenu.app`, with its existing bundle identifier and CLI contract; rebuilding source does not replace a running menu.

An optional native macOS menu-bar item that opens the [looptender panel](../../docs/dock.md) and lists explicitly registered local loops. It can pause, stop and resume a loop, each only after a native confirmation that shows the panel's own review. This is a v0.2.0 **development preview**, built separately from the Go executable. It uses AppKit's native status item and menus. It is not installed by Homebrew or `keepgoing init`.

## Build and open explicitly

Requires macOS 13 or newer, Swift 5.9 or newer, and a trusted looptender executable with `keepgoing panel` and schema-1 `panel list`; pausing, stopping and resuming also need `client review` and `client apply`. CLI v0.2.0 includes those commands; the native menu preview is a separate development build and is not in the release archives. Every command executes the selected binary: choose a binary you trust, not one supplied by loop metadata.

```sh
# Registration is an explicit, separate operation. Use the same registry for both commands.
/path/to/keepgoing registry add -c /private/example-loop/keepgoing.yaml
swift build --package-path integrations/macos
integrations/macos/.build/debug/KeepgoingMenu --executable /path/to/keepgoing
```

For a non-default registry, pass `--directory /private/example-registry` to the CLI registration and `--registry-directory /private/example-registry` to the menu executable. Executable and directory arguments must be absolute. The preview does not discover binaries, scan for loops, register loops, retain configuration, or start at login. Quitting the menu leaves supervision alone.

## How the menu is laid out

Narrow, grouped, and quiet about what does not matter. A row is the loop's display name (as the panel shows it, cut at its end at 32 characters, never in the middle) and one short state word. Where it runs, its model, its build and when it was last checked are in the row's submenu, above the choices.

- **Needs you** lists only the loops a person can act on now, each with what it waits for (`Asked you`, `On hold`, `Checks stopped`). **Working** lists the rest that are running (including at a limit, offline or failing). One collapsed item, **Other loops (N)**, holds paused, off, stopped, finished, archived and replaced loops.
- An archived or replaced loop never counts in "N loops need you" or the icon, whatever hold it kept, and never appears in the first two groups.
- **Accounts** is one item with a submenu of all the accounts; only those at or above 80% used (at most two) are shown above it.
- One grey line, "2 supervisors not on the installed build", appears only when that is so. **Recent** is a submenu.
- At the bottom: Pause All, Stop All, Open Panel with Controls, Recent, Notify me when, Update automatically, Quit, and the status line. What each control does is unchanged.

## What the menu shows

```text
Open looptender Panel
1 loop needs you
---------------------------------------------
example-api     Waiting for you   >
example-docs    Supervising       >
example-site    Paused            >    Open in Panel
---------------------------------      -------------
Open Panel with Controls…              Resume…
Stop the Panel Page   stops by…        Stop…
Quit looptender Menu
---------------------------------
Read 2 min ago
```

- **Open looptender Panel** runs `keepgoing panel`, which starts the panel's background process when none is running and opens the page in your browser. It is always there and always tries, whatever the menu could read.
- **N loops need you** appears only when a loop is held for a person, its checks have stopped, or it can't be read ("1 loop needs you, 2 can't be read"). It opens the panel, whose first view is what needs you. The same count sits beside the icon in the menu bar. The icon is plain only when a complete read found that nothing needs a person. It shows `?` when looptender could not be read, when the list of loops could not be read, or when not every loop was read, so none of those is ever taken for "nothing needs you".
- **Build freshness.** When the CLI sends `buildFreshness` in `panel list --json`, a status line above the loops says `Build X installed <when> · N supervisors on it · M not on it`, and a loop whose supervisor is not simply on the installed build says so after its state, in the CLI's words (`Updated 2 h ago`, `Update pending · switches at next check`, `Not switching to the installed build`, `Different build from the installed one`, `Older build · restart the supervisor to update`). Its submenu shows the detail and, the words and, where one is true for that supervisor, the command a person would run, as text. Nothing in the menu restarts, resumes or re-enables a supervisor.
- **One row per registered loop**: its name and its state in the panel's own words, loops that want attention first. A row opens the loop's choices, below, whose first is its page (`keepgoing panel --loop`). At most 25 are listed; when there are more, **All N Loops in the Panel** opens the rest. A name longer than 28 characters is shortened in the middle, because loop names often share a beginning; the full name is in the row's tooltip and its accessibility label. A loop the panel flags for [no progress](../../docs/dock.md#the-no-progress-flag) adds "· no progress" to its row, as the panel's picker does, and the flag's sentence to its accessibility label; the flag never counts as needing a person.
- **A loop's choices.** When looptender says which changes fit a loop's control flags (`can` in `panel list --json`, as the page offers them), its row opens a submenu: **Open in Panel**, then **Pause…** when it is not paused, **Resume…** when any hold is set, and **Stop…** when no stop is requested, Stop last. A looptender without `can` gives the row of before, which opens the loop's page.
- **Pause All Loops…** appears while looptender says pause can be made to every loop at once (`canAll` in `panel list --json`). It reads one review of every loop (`client review pause --all`) and shows it in a native confirmation: a line per loop, up to eight, then "And N more.", as one block, then the total ("Pauses 3 of 4 loops; 1 is already paused.") and what pausing does. Cancel is the default. Confirming runs `client apply pause --all` once, which pauses each loop on its own and only if its record is the one reviewed. The result is always shown, with each loop's outcome, and a partial result never reads as success: "Paused 2 of 3; 1 needs a new review."
- **Stop All Loops…** works the same way for a stop, while looptender says any loop can be stopped. Its buttons are looptender's options for the whole, in its words: stop the conversations, or, when any loop has lanes, stop them and their lanes (`--lanes`). Each is styled as destructive, and Cancel is the default. Each loop is stopped as its own review offered it: at once only where it can be, its lanes only if it has them. Deleting queued messages is never offered for every loop. The result says which loops were stopped now and which wait for their next check.
- **Pause…, Resume… and Stop…** each read the review with `keepgoing client review ACTION --loop KEY --json`, which changes nothing, and show it in a native confirmation: the review's title, then each of its sentences. The sentences come from the code that builds the page's review, so the menu and the page say the same. **Cancel is the default** (Return and Escape choose it), and a stop's button is styled as destructive. Confirming runs `keepgoing client apply ACTION --loop KEY --expect VALUE --json` once. The value is one review for one action on one loop: looptender refuses it for any other loop or change, after the review expires (2 minutes, 5 for a resume; the menu does not run it then either), and when the loop's control record has changed since it was reviewed. Then nothing is changed and you are asked to review again. Times in a review are this computer's, with their offset (`06:45 +07`). The result is said in the page's words: a done change on the menu's last line, anything else in an alert. A lost or unreadable reply is "could not confirm", and nothing is sent again by itself. A looptender that could not be started, or that refused the arguments (a different version), is said as nothing having run.
- **Stop.** When looptender lists stop options (`client review stop`, for a loop whose agent has lanes), the confirmation offers one button for each, in looptender's words: stop the conversation; stop it and its lanes; and, only when messages are queued in lanes that have not started, stop it, its lanes and those queued messages, which deletes them and their text (`--drop-queued`, passed for that button alone). Each is carried out at once when looptender says it can be. The menu writes none of those words itself. It offers only the options it fully understands and still shows the review when one is new to it, so a later addition to the options cannot break Stop ([the shape and the rule](../../docs/local-clients.md#reviewed-pause-stop-and-resume-client-review-client-apply)). Without options, a stop stops the conversation only, at the loop's next check, and the review says so.
- **Open Panel with Controls…** starts the panel with `keepgoing panel --allow-changes`, so its page can pause, stop and resume, and offer a separate review to replace a loop’s own build. It asks first, and the question shows the page footer's own statement: anyone who can use the browser window can then pause, stop and resume loops. A read-only panel that is running is stopped and started again, which the question says first. A panel that already allows changes is opened as it is. Whether the panel runs, and in which mode, is read again when this is chosen, not taken from the menu's last read, which can be minutes old. It is offered only by an executable whose `panel status --json` gives that statement.
- **Stop the Panel Page** is there only while the panel's background process is running, and says when it would stop by itself. Choosing it runs `keepgoing panel stop`. It stops the page, never a loop.
- The last line is the only one that cannot be chosen. It says when the list was read, and anything a person should know about that read: no loops registered, not every loop was read, the list could not be read, looptender can't be reached, or the menu and looptender are different versions.

If the panel cannot be opened or stopped, a small alert says so and names the command to run in a terminal; the menu has closed by then, so a line in the menu alone would not be seen.

Everything the earlier preview printed as lines (configured target, timestamps, goal revision, daemon evidence) is gone from the menu: the loop's page in the panel shows it, one choice away.

The state words are not written in this app. They come from `keepgoing panel list --json`, which is built by the code that builds the page, so the menu and the page cannot disagree. A state this build has never heard of is shown with the phrase the CLI gave and a question-mark symbol.

**When it reads.** When the app is started, each time the menu is opened, and once a minute while the app runs. The once-a-minute read **replaces the earlier rule that this preview had no timer**: without it, the count beside the icon could only change when the menu was opened, which is the moment a person no longer needs it. Each read is one `keepgoing panel list --json` and one `keepgoing panel status --json`: local files only, no provider call, and the panel is not started. After three failed reads in a row it slows to one every five minutes until a read succeeds. It does not read on the timer while the Mac is asleep (it reads once on wake) or in Low Power Mode, where it reads only when the menu is opened. One read runs at a time, and the status line says when the last one finished. Quitting the menu leaves supervision and the panel alone.

**While the menu is open** it is never rebuilt under the pointer: when a read finishes and the rows are the same rows, their text is updated in place; when the rows changed, the change waits until nothing is highlighted or the menu has closed. The count beside the icon is always updated.

**Accessibility.** Every row's spoken label says what it is ("example-api, Waiting for you") and its help says what choosing it does ("Opens this loop in the panel."), and the menu-bar item's label includes the count. State is carried by words and a symbol shape, never by colour alone; the symbols are decorative and are not read. Text and symbols use the system's menu colours. Loop names are laid out left to right whatever script they are in. Not yet checked by a person with VoiceOver, with Increase Contrast, or on a real menu's highlighted row.

## Updating the menu to the installed build

The menu compares the build it runs with the one installed now, from the file system only (nothing is fetched). They differ when the path it was started through (argv[0], for example a `current` link) now leads to another file, when the file at that path was replaced, or when it runs from `<root>/builds/<id>/...` and `<root>/current` now points at another build. Then:

- a dot follows the icon, and the icon's spoken label adds "menu update available";
- **Update available: relaunch to use <build>** appears (the build is the directory name under `builds/`, else the app's version). Choosing it relaunches from the installed build;
- one notification, "looptender menu: update available (<build>)", is posted for each installed build and never again for it, even after the menu is restarted. Clicking it does what the menu item does. The menu asks for notification permission where it already did (when started from an app bundle); without permission it relies on the dot and the menu item and does not ask again;
- **Update the menu automatically** (on by default; kept in the app's preferences) relaunches by itself while the menu is closed and no change is being read, confirmed or applied. With it on, the notification above is not posted unless the attempt failed; the new menu says "looptender menu updated to <build>" once instead.

**Quitting is gated twice more, at the moment each step happens.** The menu is idle only when it is closed and nothing is in flight: a change being read, its confirmation window open, a change being applied, and the panel being restarted (**Open Panel with Controls…**, from its status read to the start) all count, from their first step to their last. The check runs again just before the new menu is started (still idle, and the file about to run has the same real path, size and modification time as the one checked) and again 1.5 seconds later (still idle, and the new menu still running); if the person opened the menu or started something in between, the new menu is stopped, this one keeps running, and the attempt is given back, so it is tried again later. Choosing the update, or clicking its notification, while work is open says "Finish or cancel the open action first" and does nothing. `codesign` runs off the main thread. Turning on **Update the menu automatically** while an update is waiting does not relaunch under the open menu; it happens once the menu is closed. At most three automatic attempts are made in any hour, whatever the builds, so a path rewritten in place cannot be retried without limit. The checks-have-stopped notices already told are handed to the new menu, so a relaunch does not post them again. When `<root>/current` leads to a build with no menu app, the menu says so in a line instead of saying nothing.

**What the checks prove.** `codesign --verify --deep --strict` shows that the bundle's seal is intact (its resources and executable match what was sealed); it does not show who signed it, and these bundles are signed ad hoc, so anyone who can write the bundle can re-seal it. The bundle-identifier and program-name checks catch an app that is not this one by accident, not on purpose. A bare executable outside an app bundle is checked only for being executable. The program that runs is whatever your own install directory holds: only the path the menu was started through and `<root>/current` beside its `builds/<id>` are looked at, and the update is as trustworthy as who can write there. It is integrity, not provenance.

Before quitting, the menu checks the installed program: it must exist and be runnable and, in an app bundle, have a readable `Info.plist` that names that program and this app's bundle identifier, and pass `codesign --verify --deep --strict`. It then starts the new one with the same arguments, and quits only if that one is still running 1.5 seconds later. If a check fails, or the new menu does not stay up, this menu keeps running and says why in its last line (and in an alert when you chose the update). An automatic relaunch is tried once for each installed build, so a failing one never repeats; choosing the item yourself can be tried again. Open at Login is never read or changed: the relaunched menu is an ordinary start of the same program. Not testable without the real app: the relaunch itself, the notification's permission and click, and a login-item start; the decisions and the file checks are tested.

## Notifications for events

The CLI sends one list of events in `panel list --json` (`notices`; see [the dock guide](../../docs/dock.md)), the same one the panel page reads. The menu keeps the ids it has seen: its first read is a baseline and posts nothing, and a later read posts each new id once. Events that begin together and share a group are one notification (`5 loops stopped: usage limit until Fri 04:00.`); one event's notification opens that loop in the panel, a group's opens the panel. A read that was cut off, failed or came from an older CLI changes nothing.

- **Pushed by default:** a loop needs you (an agent's question is the most urgent), a usage limit is hit, a supervisor stops answering. **Listed but quiet:** a failed lane, a hold set or lifted, an install adopted or refused, a low or critical disk, a deadline or send limit reached. **Notify me when…** turns each kind on or off in the settings the CLI keeps for the menu, the panel and push together (`panel notify set`), so a choice made on the panel page is the same here.
- The menu tells the CLI it runs (`panel notify heartbeat`, after each read and when it quits), so the panel page does not send a second banner for the same event on this computer. Started after being down, it tells once about an urgent question (an agent asked) that began since it last ran, and nothing else of what was already open.
- A group's count is its real total ("5 loops stopped", though one is new).
- **Recent** lists the last ten events, pushed or not, with their age. It is kept in memory: a restart starts empty.
- The words come from the CLI and carry no note text, path, thread ID or account name. The count beside the icon is still the loops that need you, and only that.
- Permission is the one the menu already asks for (when started from an app bundle); without it nothing is posted, nothing is asked again, and Recent still lists the events.

## Notifications when checks stop

When a loop's [checks have stopped](../../docs/operations.md#external-schedulers-can-stop-on-their-own), its row says so and the count beside the icon includes it. The menu can also post a macOS notification, which stays in Notification Center until it is seen. macOS posts notifications only for an app bundle, so this needs the menu built as one:

```sh
integrations/macos/scripts/make-app.sh
integrations/macos/.build/LooptenderMenu.app/Contents/MacOS/LooptenderMenu --executable /path/to/keepgoing
```

The script builds `integrations/macos/.build/LooptenderMenu.app` (bundle identifier `local.keepgoing.menu-preview`, signed ad hoc for this computer), and beside it `KeepgoingMenu.app`, a link to it under the app's earlier name, and does nothing else: it does not install, register, open or start it at login.

**Installing a build elsewhere.** Copy the app and the link together, for example `cp -R integrations/macos/.build/LooptenderMenu.app integrations/macos/.build/KeepgoingMenu.app <destination>/` (BSD `cp -R` and `ditto` copy the link as a link). A copy of the link alone points at nothing: a menu of an earlier build then reports that the build has no menu and does not relaunch, and a login item that names `KeepgoingMenu.app` there finds nothing.

Start it with the command above, with the same arguments as the bare executable. macOS asks once whether it may show notifications; if you decline, none are posted.

The menu's version is kept in one place, [`VERSION`](VERSION). The script writes it into the app as `CFBundleShortVersionString`, and the menu reports it with `--client macos-menu@VERSION` on its regular `panel list` read, for the panel's Versions view. A looptender too old to know the flag answers with a usage error and no output. The menu then reads again without it, and stops sending it to that executable only if that read succeeds; a timeout is never retried. An executable replaced in place (new size or modification time) is given the flag again. Run without the app bundle (`swift run`), the menu has no version and reports none.

- **One per gap.** A loop is told about once for each gap, named by the loop and the time of its last check (`overdueSince` in `panel list --json`). When checks run and then stop again, that is a new gap and a new notification. More than three new gaps in one read are one notification.
- **What it says.** The loop's name and when it was last checked, and that the computer may have slept or its scheduler stopped. A loop that is paused, waiting for a person or complete is never overdue, so it is never notified about. No identifiers, reasons or notes: `panel list` carries none.
- **When.** After the reads the menu already makes: when it starts, when it is opened, and once a minute. In Low Power Mode there is no timed read, so a notification waits until the menu is opened.
- **Kept in memory only.** The preview keeps nothing on disk, so quitting and starting it again during a gap that is still open posts that gap once more.
- **Turning it off.** Start the menu with `--no-notifications`, or turn notifications off for keepgoing Menu in System Settings. Run as a bare executable (`.build/debug/KeepgoingMenu`), the menu never asks for or posts notifications and says so once on standard error.
- **Removing it.** Quit the menu and delete the bundle: `rm -rf integrations/macos/.build/LooptenderMenu.app integrations/macos/.build/KeepgoingMenu.app`. System Settings may still list keepgoing Menu under Notifications.

Covered by `NoticesTests` (the one-per-gap rule, its text and decoding) and `TestPanelListGivesTheLastCheckOfAnOverdueLoop`. Not verified: a notification shown on a real screen.

## Bounds and failures

Every subprocess uses a fixed argument array without a shell and a 1 MiB cap per output stream. The menu runs exactly nine commands, each listed in `MenuBuilder.arguments` with a test, and nothing else:

| Command | Why | Limit |
|---|---|---|
| `keepgoing panel list --json` | read the loops | 25 s (the CLI bounds its own read at 20) |
| `keepgoing panel status --json` | read whether the panel runs | 5 s |
| `keepgoing panel [--loop KEY]` | open the panel or a loop's page | 20 s |
| `keepgoing panel --allow-changes` | open the panel with controls, after a confirmation | 20 s |
| `keepgoing panel stop` | stop the panel's page | 20 s |
| `keepgoing client review pause\|stop\|resume --loop KEY --json` | read a review; changes nothing | 25 s |
| `keepgoing client review pause\|stop --all --json` | read the review of pausing or stopping every loop; changes nothing | 25 s |
| `keepgoing client apply pause\|stop --all --expect KEY=VALUE … [--lanes] --json` | pause or stop each loop of a confirmed review, each only if its record is the one reviewed; `--lanes` for a stop whose option says so | 120 s |
| `keepgoing client apply pause\|stop\|resume --loop KEY --expect VALUE [--lanes] [--immediately] [--drop-queued] --json` | make a confirmed change; `--lanes`, `--immediately` and `--drop-queued` only for a stop, and only when the review offered them; `--drop-queued` only with `--lanes`, for the one option that says so | 25 s |

The only values ever taken from a reply and passed back are a loop's page key and a review's `expect` value (with `--all`, each loop's, as `KEY=VALUE`), each only after it matches its pattern, and a stop option's choice of `--lanes`. There is no command path that enables a loop, takes it over, or reaches a provider; the reads never start the panel. **The menu acts as no BB thread:** every command gets the environment the panel gives its own server (a fixed `PATH`, `HOME`, `USER`, `LOGNAME`, `TMPDIR`, `TZ`, the locale and the XDG directories) and nothing else, so `BB_THREAD_ID` never reaches looptender wherever the menu was started, and a resume acts as a person at a terminal outside BB. That also drops `LOOPTENDER_REGISTRY_DIR`, `LOOPTENDER_SESSION_DIR` and `LOOPTENDER_PANEL_OPENER`, and their earlier `KEEPGOING_` names: give a non-default registry with `--registry-directory`, which the menu passes as `--directory` to every command that reads the registry (the list, opening the panel, review and apply), and a panel directory with `--panel-directory`, which it passes to the panel commands. A change is recorded in the loop's history as an ordinary `pause`, `stop` or `resume`. Subprocess errors and stderr are never shown as raw text. A missing binary, malformed reply, timeout or failed read shows one plain line and leaves **Open looptender panel** working. Loop names and review sentences are shown as inert text with control characters removed. New additive JSON fields are tolerated.

`keepgoing panel` starts the panel's background process, for this computer only, which stops by itself when unused; see [the panel](../../docs/dock.md#opening-it-with-one-command-keepgoing-panel). The menu never starts it except when a row that opens the panel is chosen.

These bounds apply to the directly invoked CLI and its output. An arbitrary replacement binary is not sandboxed, and process termination is not a process-tree containment mechanism. Use a trusted looptender binary. The earlier status reader (`client status`) is still in the library and its tests; the menu no longer shows it.

## Verification and remaining work

```sh
swift build --package-path integrations/macos
swift test --package-path integrations/macos
# Pass an already-built current Go executable:
python3 integrations/macos/scripts/smoke-contract.py /path/to/keepgoing
```

XCTest requires a toolchain containing that framework, such as full Xcode; some Command Line Tools installations can build the app but cannot run XCTest. macOS CI builds the app and runs the tests plus the contract smoke check. The smoke compiles the reader without XCTest and checks the real Go CLI against an isolated fictional disabled loop, unchanged files and a provider-command trap, including the menu's own read and a stop review (which leaves only the empty control lock file). It then stops the loop as the menu does, review then confirmed apply with a BB thread in the caller's environment, and checks that the stop was saved, no owner was taken, no provider command ran, and the same review applied again is refused. Unit fixtures cover the menu's rows for each kind of read (which rows, which can be chosen, which command each runs), each loop's choices for its flags, every command in the allow-list and the values it refuses, each confirmation (its text, Cancel as the default, which buttons are destructive, the stop with and without lanes, the controls question with the exposure statement), review expiry, the review and apply replies, the scrubbed environment, the count and the failed-read mark, the read schedule and its back-off, the stop row, when an open menu may be updated in place, long names, a hundred loops, unknown states, malformed/version-mismatched replies, inert text, argument separation, output caps and timeouts.

To look at the rows without opening a menu, draw them to a picture:

```sh
integrations/macos/.build/debug/KeepgoingMenu --executable /path/to/keepgoing --render-menu /private/example/menu.png [--dark]
```

This draws the menu-bar item and each row with the fonts, symbols and colours the menu uses. It is a drawing of the rows, not a capture of the screen.

Native build and the real-CLI fixture smoke pass locally. Automated evidence does not establish how the open menu or its confirmations look or read on a real screen (no confirmation was opened and answered by a person; the AppKit code that shows them is built but not run by any test), VoiceOver behavior, Spaces/full-screen behavior, live provider compatibility or secure distribution. No installed loop was used for these fixture checks. Start at login, app-bundle signing/notarization and distribution remain planned. The existing CLI controls remain available separately; the preview does not add another supervisor.

## License

This preview is part of looptender and is source-available under the [Sustainable Use License 1.0](../../LICENSE); see the [licensing guide](../../LICENSING.md).

## Saved account quota (unreleased)

With a matching source CLI, the menu also shows selected Claude/Codex account
rows with saved quota detail in submenus. The same minimized summary powers
Accounts in the web panel: named windows, used/remaining values, independent
reset times, source/read time, stale/ended/partial and failed-refresh evidence.
Credits and individual limits retain provider units. No currency, identity,
account equivalence or access is inferred. Claude summaries show retrieval time
and provider freshness unknown, even just after retrieval: the inspected native
reply may contain cached or seeded usage. Strict decoding binds the provider,
source and timestamp type; contradictory evidence refuses rather than becoming
fresh or zero. Route readings stay separate.

The additive `panel list --json` field `accountQuotas` is optional: an older CLI
keeps the old loop menu. Each account's `id` there is a handle, not the catalog ID; the menu
passes it back to `panel --account` unchanged, and its shape is the one the
menu already validates (32 lowercase hex). New data is validated before rendering (16 projected
accounts, 41 rows per account, bounded inert text/percent/date/state values);
menus show six accounts and eight details each with explicit counts for more.
Long menu text is shortened visibly, with complete accessible labels/tooltips.
The existing 180-character generic and 1024-byte authority bounds are unchanged.
Saved quota does not alter loop badges, grant controls or run provider reads.
Choose Open Panel, then Accounts for full details and an explicit reviewed provider
refresh. Unsupported or corrupt data never becomes zero quota. Native menu
polling remains saved-evidence only; rebuilding does not replace or restart a
running menu. See [quota capability limits](../../docs/accounts.md#read-selected-context-subscription-quota).
