# The account router (design)

Status: design, not built. Nothing described here exists in looptender yet. Code that reads a real credential does not merge before the owner has approved this document.

## Why

A person with two subscriptions of the same provider expects looptender to move work to the second one when the first runs out. Today it cannot do that for a conversation that is already running:

- Every Claude Code conversation started by BB runs on one configuration directory, so on one login. looptender has no per-conversation account to change ([accounts.md](../accounts.md): "BB thread routes have no per-thread account").
- The new-conversation takeover moves work to another provider, with a brief instead of the conversation.
- On 2026-11-23 the account behind every conversation reached its 5-hour and then its weekly limit while a second subscription sat almost unused. looptender waited and said nothing.

The router is looptender's own answer: a persistent per-user service that the agent is pointed at, which sends each request to a subscription with room. A similar thing exists in sessionpipe (its "pool"). It works, but while it is on, a stopped daemon means every session gets "connection refused". This design starts from that failure and works outwards.

Basis marks used below:

- **[observed]**: seen on a real machine (the 2026-11-23 incident, or the isolated failure lab of sessionpipe's pool).
- **[source]**: read in sessionpipe's pool source (`packages/cli/src/pool/`), not run.
- **[unverified]**: an assumption about a provider's tool that this design depends on and that nobody has checked yet. Each one is listed again under [What must be verified](#what-must-be-verified).

## The rule everything else follows

**With the router installed, a session is never worse off than without it.**

In practice:

1. The session keeps its own login. The router adds other subscriptions; it never replaces the one the session started with.
2. When any part of the router is missing, slow, crashed, upgrading or confused, the request goes to the provider unchanged, on the session's own login. The session then behaves exactly as it does today, including the provider's own limit message when that login is out of room.
3. When that is not possible either (nothing is listening at all), the person is told which sessions are affected and the one command that fixes it, and new sessions are pointed back at the provider directly.

Rules that also do not bend:

- A person logs in to each subscription with the provider's official tool. looptender never logs in, never creates an account, and never copies a credential from one store to another.
- Nothing of a request or a response is logged or stored: no body, no header value, no query string, no path beyond its fixed name.
- The listener is loopback only.
- A refusal, a policy block or an account restriction is not a limit. It never causes a move to another account or model (AGENTS.md, "never a way around a provider's safeguards").
- Whether a plan's terms allow using several subscriptions this way is the person's decision. looptender gives no opinion on it, and the router is off until a person turns it on and names the subscriptions it may use.
- Nothing in the core path assumes macOS.

## The parts

```
 Claude Code session ──HTTP, loopback──▶ front ──HTTPS──▶ provider
   (own login in every request)           │  ▲
                                          │  │ decision, bounded wait
                                          ▼  │
                                         router ── reads ── accounts catalog, usage records,
                                                            each subscription's own login store
```

**The front** is the only thing in the path of a request. It is deliberately dull:

- It accepts a request on loopback, checks it (see [Security](#security-review-of-the-local-listener)), and asks the router one question: "which subscription for this conversation and model?"
- It waits a bounded time for the answer (default 150 ms). With an answer naming another subscription, it swaps the request's `Authorization` value for that subscription's and forwards. With no answer, an answer of "your own", or any error, it forwards the request unchanged.
- It holds the connection to the provider itself and streams the response back. The router is never in the byte path, so a router crash cannot cut a stream.
- It reports back to the router, without waiting, only: the conversation key, the model name, the HTTP status, `Retry-After`, and the provider's rate-limit header values.
- It reads no file except its start-up record, writes no file, and imports only the Go standard library and one small internal package. A test enforces the import list.

**The router** makes decisions and holds state:

- It knows the person's order of subscriptions, each one's room, which subscription each conversation is on, and each subscription's current access token (in memory only).
- It answers the front over a private channel (a Unix socket in a `0700` directory; on Windows a named pipe restricted to the user). The answer can contain an access token, so the channel is never a TCP port.
- It can be stopped, upgraded or crashed at any time without a session noticing more than "requests go to my own login for a while".

**The pointer** is the setting that sends a session to the front: `ANTHROPIC_BASE_URL=http://127.0.0.1:<port>/c/<client secret>` in the `env` block of a Claude Code `settings.json`. Only that. In particular the router does **not** set `ANTHROPIC_AUTH_TOKEN`, so Claude Code keeps using its own login and sends it with every request [unverified: that Claude Code sends its subscription login to a custom base URL when no token is set]. This is the difference from sessionpipe's pool that makes rule 2 possible: every request already carries a credential that works without us.

They are separate processes of the same `looptender` binary: `looptender router front` and `looptender router run`.

## Three modes, chosen per machine

The owner's requirement: looptender must be able to turn its own router off and use sessionpipe's pool instead. So subscription switching has one setting per machine with three values, set with `looptender router mode <own|sessionpipe|off>` and shown first in `status` and `doctor`.

| Mode | Who switches subscriptions | What looptender does |
|---|---|---|
| **`own`** | looptender's router | Everything in this document. |
| **`sessionpipe`** | sessionpipe's pool | Runs no front and no router and writes no pointer. Reads the pool, read-only, and reports it as it would report its own data. |
| **`off`** (the default) | Nobody | Today's behaviour: which limit, which account, the login command, the burn warning, an armed takeover to another provider. |

### Mode `sessionpipe`

- **Detection, read-only.** `sessionpipe pool --json` under a time limit (on, port, listening, accounts with status, room, windows and resets), plus reading whether the configuration directory's settings name the pool's port. looptender never runs `pool on`, `add`, `off` or `rotate`, never edits what the pool wrote, and reads none of its secrets.
- **Shown as our own data would be.** The pool's accounts, the one new sessions get now, each one's room and reset, in `status`, the panel's Accounts view and the menu. Each line says "from sessionpipe's pool" and the reading's age.
- **The pool's exhaustion is the real limit.** One pooled account at its limit is not the loop's limit: no limit wait, no takeover and no model step-down while the pool reports room. When every account is limited, the loop waits until the earliest time the pool gives.
- **Switches.** The pool does not report which account serves a given session ([source]), so looptender can say "the pool's current account changed from <label> to <label>", not "this conversation moved". Saying more needs a small upstream change in sessionpipe.
- **The known failure, said plainly.** `doctor` and `router status` carry a standing line: "In this mode, if sessionpipe's daemon stops, every session pointed at the pool cannot connect. looptender cannot route around that." A check that finds the pool on, settings pointing at it and nothing listening raises the notice `pool_down` with the affected directories and sessionpipe's own command to start it. looptender does not start it, and does not edit the settings sessionpipe wrote.
- **Never fighting it.** In this mode looptender's pointer check is off, its front is not registered as a service, and it never writes a base URL anywhere.

**What "use sessionpipe for the adapter" means per target:**

- **BB targets:** a BB conversation is switched by the pool only if the pool covers the directory BB uses, which the pool does not discover by itself ([source], S14). `doctor` says whether that directory's settings name the pool and, if not, the command the person would run. looptender then supervises as today and applies the three decision changes above.
- **Headless targets** (`adapter: claude`): the agent looptender launches reads the same settings, so it is pooled when its directory is. A route's own `account_dir` still selects the directory; inside a pooled directory the pool, not the route's `account`, decides the subscription, and `status` says so instead of naming the route's account as the one in use. Account fallbacks between two pooled directories are skipped as pointless, with the reason shown.
- **The sessionpipe target** (`adapter: sessionpipe`, observe-only): nothing changes in what looptender may do; it still never sends, wakes or stops. It gains the pool's readings as context on the observed loop ("this session's agent is behind sessionpipe's pool; N accounts, room until <time>"), which today's tier-0 file cannot give. It does not become a way to control the session.

### Switching modes safely

Three properties hold across every change of mode:

1. **Settings always point somewhere that works.** Leaving `own` uses the uninstall order (R12): remove our pointer first, keep the front as a pass-through until no session uses it, then remove the service. The mode is recorded as changed only after the pointer is confirmed gone from every file we wrote.
2. **Never chained, never both.** For one configuration directory there is at most one base URL, so two proxies cannot both be active; the danger is one being pointed at the other, or one overwriting the other.
   - Entering `own`: if a directory's settings already carry a base URL or token we did not write (the pool's, or any other local proxy's), `router on` refuses that directory, names what it found, and changes nothing. It never points our front at another proxy as its upstream; the front's upstream host is fixed.
   - Entering `sessionpipe`: refused while our pointer is still present in any settings file or any session is still using our front; the command says which, and to run `router mode off` first and wait. looptender does not turn the pool on; the person does, afterwards.
   - The pool behaves the same way in the other direction: it skips and reports a settings file whose base URL someone else set ([source]).
3. **The other one enabled by hand is detected.** Every check compares the recorded mode with what the settings and the two listeners actually show:

| Recorded mode | Found | looptender does | The person is told |
|---|---|---|---|
| `own` | A directory's base URL now names the pool or another proxy | Stops routing for that directory (the front passes through anything still arriving); does not rewrite the setting | "<directory> was pointed at <what> outside looptender. It is no longer routed by looptender. Choose: `router mode sessionpipe`, or remove that setting and run `router on`." |
| `own` | The pool is on for other directories | Nothing; they are not ours | One line in `router status` |
| `sessionpipe` | Our pointer in a settings file (an old build, a restore from backup) | Removes our own pointer after confirming the front is not serving it, never touching the pool's | "Removed a leftover looptender router setting from <directory>." |
| `sessionpipe` | The pool is off or has no accounts | Nothing | "Mode is sessionpipe but its pool is off: nothing is switching subscriptions." |
| `off` | The pool is on and covers a supervised directory | Reads it as in mode `sessionpipe` so limits are not misread, without changing the recorded mode | "sessionpipe's pool is switching subscriptions for <directory>. Set `router mode sessionpipe` to make that the stated mode." |
| `off` | Our pointer present | As the `sessionpipe` row | The same |

Mode `sessionpipe` and the detection rows need no credential and no listener, so they can be built independently of the router itself; they are phase 2a in the plan.

## The failure model

### How sessionpipe's pool fails

| # | Situation | What a session gets | Basis |
|---|---|---|---|
| S1 | Its daemon is not running while the pool is on | "connection refused" on every request, every session, until a person notices | [source], and its own README |
| S2 | Daemon killed while a response is streaming | the stream is cut; the proxy and the upstream connection die together | [source]; lab pending |
| S3 | Daemon restarts (upgrade, crash loop) | refused connections for the gap; in-flight streams cut | [source]; lab pending |
| S4 | The secrets store cannot be read (locked keychain, lost login session) | 503 with a 30 s retry, for every session, although each session's own login might still work | [source] |
| S5 | The token endpoint is down, or one refresh is refused | 503 for all, or that account silently leaves the rotation as "sign in again" | [source] |
| S6 | Every account is full | the pool's own 429 text, not the provider's | [source] |
| S7 | The pool is turned off but the listener is up and settings still point at it | 503 "the pool is off" | [source] |
| S8 | Settings point at the pool after it is uninstalled or its port moved | refused connections until the settings are repaired; sessions read the setting at start, so running ones keep the dead address | [source] |
| S9 | The session's own login is ignored | the pool's bearer replaces it, so there is nothing to fall back to: the pool is a single point of failure by construction | [source] |
| S10 | It holds a copy of every account's refresh token and refreshes them itself | a second holder of each login; a refresh race with Claude Code can sign a directory out [unverified] | [source] |
| S11 | The next account's plan lacks the model in use | the upstream's error passes through; the pin stays | [source] |
| S12 | A switch at 98% happens on the next request, mid-turn | a full-price prompt on the new account (no prompt cache there) [unverified] | [source] |
| S13 | An upstream 5xx moves the session to another account for 60 s | an account switch for a fault that is not a limit | [source] |
| S14 | Config directories outside the home directory's `.claude*` names are not found | BB's directory is not pooled unless the person knows to run the command with a variable set | [source] |

The lab's observed results replace "lab pending" when they arrive; any that contradict the source reading change this table and the design, and the change is named in the pull request.

### How ours could fail, what the session experiences, and what the person is told

"Own login" means the request is forwarded unchanged on the login the session started with.

| # | Situation | A running session | The person is told |
|---|---|---|---|
| R1 | Router not running (never started, stopped, crashed) | Nothing visible. Each request waits at most the decision bound once (150 ms), then goes on its own login. After 3 misses the front stops asking for 5 s at a time, so the wait is not paid per request. | Notice `router_down`: "The router is not running; N sessions are on their own login. `looptender router start`". Status, doctor, panel, menu. |
| R2 | Router crashes while a response is streaming | Nothing. The front holds the stream. | As R1. |
| R3 | Router restarting during an install or upgrade | As R1 for the seconds it takes. Conversation-to-subscription pins are on disk, so each conversation returns to the subscription it was on. | Nothing unless it stays down longer than a minute. |
| R4 | Front crashes or is killed | In-flight streams through it are cut: the session sees a broken response and retries it by itself [unverified]. New requests: where the operating system holds the listening socket (launchd, systemd), the connection is accepted and waits for the restarted front, typically well under a second. Elsewhere, connections are refused until the service manager restarts it. | Notice `router_front_restarted` if it restarts more than twice in ten minutes, with the count. |
| R5 | Front replaced during an upgrade | Nothing in the normal case: the front is only replaced when its own protocol number changed, the new one takes over the listening socket, and the old one finishes its open streams (up to 30 min) before exiting. | Nothing. |
| R6 | Front not running and not restartable (service removed, binary deleted by hand, port taken by another program) | Running sessions get "connection refused": this is the one case rule 2 cannot cover, because sessions read the setting only at start. | Every looptender process that runs (supervisors, the menu, any command) checks the pointer. If the front does not answer for 30 s: it removes the pointer so new sessions go direct, and raises `router_front_gone`: "N running sessions point at a router front that is not running. Start it with `looptender router start`, or restart those sessions." See [Fail-open](#fail-open-concretely). |
| R7 | A subscription's access token has expired and nothing refreshed it | Nothing. That subscription is skipped; requests go to the next one with room, or the session's own login. | Notice `router_login_stale`: "<label>: its login has not been refreshed since <time>. Run `<the official tool's command>` in <directory>." |
| R8 | The provider rejects a routed subscription's token (401) | Before any byte reached the session: the front sends the request again once, unchanged, on the session's own login. | `router_login_stale` for that subscription, which leaves the rotation until its login is read again successfully. |
| R9 | A subscription is restricted or refused (403, policy) | The provider's answer passes through untouched. No other subscription is tried for that request. | "<label> was refused by the provider. looptender does not route around that; it is out of the rotation until you clear it." |
| R10 | Every subscription is full | The provider's own limit answer for the session's own login passes through, exactly as without a router. The front never invents a limit message. | The existing `limit` notice, extended: "All N subscriptions are at a limit; the first to have room is <label> at <time>." The loop's limit wait uses that time. |
| R11 | Settings left pointing at a dead front after a crash | As R6. | As R6. |
| R12 | Uninstall | `looptender router off` removes the pointer first, then keeps the front running as a plain pass-through until no session has used it for an hour, then removes the service. Running sessions never see a refusal. | `router off` says how many sessions still use the front and when it will go. |
| R13 | The binary is deleted without `router off` | On Unix the running front keeps working until logout or restart, on its own login. After that, R6 with nothing of ours left to repair it. | Only the provider tool's own connection error. This is the residual risk; see [Open questions](#open-questions-for-the-owner) for the "last act" option. |
| R14 | The login session is gone (as on the evening of 2026-11-23: new processes could not read the keychain or resolve names, running ones were fine) | The front and router are already running with tokens in memory, so routed requests continue until those tokens expire; after that, own login. A session whose own process has the same problem fails as it would without us. | "Cannot read the login for <label> right now" (never "signed out"), using the distinction from PR #508. No one is told to sign in again unless a login was actually read as absent. |
| R15 | Disk full | The front needs no disk. The router keeps deciding from memory and stops saving; a restart then loses pins made since the last save, which costs at most a prompt-cache miss per conversation. The pointer is never written when a write cannot be completed atomically. | The existing `disk` notice, plus "router state not saved since <time>". |
| R16 | Two users on one machine | Each user has their own front, port, client secrets, router and state directory. The private channel is per user. One user's front cannot be asked for another user's tokens. | Nothing. See the security review for a port taken over by another user. |
| R17 | The router's decision is slow (machine overloaded) | Each affected request goes on its own login after 150 ms. No queueing behind the router. | Counted; shown in `router status` as "decisions missed". |
| R18 | The provider is unreachable or answers 5xx/529 | The provider's answer or the connection error passes through. No switch: an unclassified error never causes a move. | Nothing new. |
| R19 | The next subscription cannot use the model | The first such answer passes through once; the pair (subscription, model) is then skipped for that model. | "<label> does not offer <model>" on `router status`. |
| R20 | The home subscription itself is logged out | The session cannot start a request at all; that is the provider tool's own "not logged in". The router is not involved. | The existing account check. |
| R21 | State file damaged or from a newer version | The router starts with empty pins and the order from configuration, and keeps the unreadable file aside. | "Router state could not be read; started fresh. Kept as <name>." |
| R22 | Clock jumps | Holds and resets are compared against both wall time and the time they were set; a hold set "in the future" is cut to a minute. (Taken from sessionpipe's policy, which handles this well.) | Nothing. |

## Fail-open, concretely

### The path of one request

1. The front accepts the connection and checks `Host`, the path and the client secret.
2. It reads the body into memory (up to 32 MiB) and takes two things from it: the model name and the conversation id. Nothing else is parsed or kept.
3. It asks the router for a decision and waits at most 150 ms.
4. It forwards to the provider: with the named subscription's token, or unchanged.
5. If the provider answers 429 or 401 **before any byte has been sent to the session** and the request was routed, the front reports it and tries once more: on the next subscription the router names, or unchanged on the session's own login. After the first byte nothing is retried by the front.
6. It streams the response. Rate-limit headers pass through, so Claude Code's own usage display shows the subscription that served the request.
7. It tells the router the outcome and forgets the request.

A request already in flight is never moved. If the router dies during step 6, nothing happens to it. If the front dies during step 6, the stream is cut and the session's own retry applies; no design that sits in the path can do better, which is why the front does so little.

### Alternatives compared

| Approach | Router down | Front down | In-flight request | Verdict |
|---|---|---|---|---|
| **A. One process that routes and forwards** (sessionpipe's shape) | Refused connections | same thing | Cut on any restart, including every upgrade | Rejected: this is the failure the owner named. |
| **B. A front that forwards, and goes direct when the router does not answer in a bound** | Invisible | Refused until restarted | Survives router restarts; cut only if the front dies | Adopted, with C for what it cannot cover. Needs the session's own login in each request to have somewhere to go; see the pointer. |
| **C. Supervisors rewrite the setting back to direct** | Does not help running sessions (the setting is read at start) | Helps new sessions only | Not affected | Adopted as the backstop for R6 and R11, not as the main mechanism. |
| **D. The operating system holds the listening socket** (launchd `Sockets`, a systemd `.socket` unit) | n/a | Connections wait instead of being refused while the front restarts | Not affected | Adopted where the system offers it. Not available on Windows or without a service manager; nothing depends on it. |
| **E. No listener: swap the login inside the directory** | n/a | n/a | n/a | Rejected earlier: copies credentials between stores, switches every session at once, needs restarts. |
| **F. No listener: a credential helper setting that prints another subscription's token** | n/a | n/a | n/a | Rejected: hands a credential to the client as if it were its own, cannot react to a limit before the request fails, and changes what kind of login the client believes it has [unverified]. |

So: **B + C + D**. B makes a dead router invisible. D shrinks a dead front to a pause on macOS and Linux. C limits a permanently dead front to the sessions already running, and tells the person about exactly those.

### The pointer check (C in detail)

- **Who runs it:** every supervisor on its normal check, the menu's heartbeat, `doctor`, and any `looptender router` command. It is a loopback request to the front's health path with a 1 s limit.
- **When it acts:** the pointer in a settings file names our front (our port and a secret we issued), and the front has not answered for 30 s, and one attempt to start it through the service manager has failed.
- **What it does:** removes our one key from that settings file, restoring the bytes that were there before when the rest of the file is unchanged. It records what it removed so `router on` can put it back.
- **What it never does:** touch a base URL it did not write (other local proxies also live on loopback), touch any other setting, or restart a session.
- **Putting the pointer back** after a repair is automatic only once the front has answered continuously for 10 minutes; otherwise it waits for a person.

## Keeping it alive

The commands `looptender router front` and `looptender router run` are the product. Registration with a service manager is a thin helper around them (`looptender router service install|remove|status`), following the existing `looptender service` code.

| | Front | Router |
|---|---|---|
| **macOS** | A launchd job with a `Sockets` entry for the loopback port, so launchd holds the socket and starts the front on demand and at login. Same launchd domain as the background supervisor in PR #498, so it outlives BB and a logout. | A launchd job with `KeepAlive`. |
| **Linux** | A systemd user `.socket` unit plus a `.service`. Surviving logout needs lingering, which is the person's setting; `router service status` says whether it is on, and never turns it on. | A systemd user service with `Restart=always`. |
| **Windows** | A per-user scheduled task at logon with restart on failure, as proposed in [windows-services.md](../windows-services.md). No socket holding: a front restart is a short refusal that the client's own retry covers [unverified]. looptender has no Windows service code today, so this is new work. | The same kind of task. |
| **Anything else** (containers, other init systems) | Run the two commands under whatever supervises processes there. `router status` reports them by answering, not by how they were started. | |

An upgrade replaces the router at once. It replaces the front only when the front's protocol number differs, and then by the handover in R5.

## Credentials

### Where each login lives and who refreshes it

- **Each subscription has its own configuration directory**, created by a person: `CLAUDE_CONFIG_DIR=<directory> claude auth login`. These are the accounts catalog's `explicit_directory` entries (and `inherited` for the default directory).
- **The login stays in the store the official tool chose**: on macOS a keychain item named for the directory; elsewhere a credentials file inside the directory [unverified for current versions on Linux and Windows].
- **The official tool refreshes it.** The router never calls a token endpoint and never writes to any login store. A second party refreshing the same login can invalidate the first one's copy [unverified], which would sign a directory out; not refreshing avoids the whole class.

### What the router reads, and when

- Only when the decision is "another subscription" does the router need that subscription's access token. It reads it from that subscription's own store at that moment, keeps it in memory until the expiry stored next to it, and reads again after that.
- It reads the access token and its expiry. It does not read the refresh token into its own state, and never passes it anywhere.
- The front receives a token only inside a decision, uses it for that one request, and may keep it in memory for the same conversation until its expiry so that a router restart does not move the conversation. Neither process writes a token to disk, a log, an environment variable, a command line or a notice.
- When the session's own subscription is chosen (the common case), nothing is read at all: the request passes through.

### The idle-subscription problem

A subscription nobody is running sessions in has nobody refreshing its token. Access tokens are short-lived, so a spare subscription's token will usually be expired at the moment it is first needed. Three ways to deal with it:

1. **Ask the official tool to refresh.** The router runs the official tool in that directory, with a time limit, in a way that makes it refresh its own login without a model request. Whether such an invocation exists is [unverified]; the accounts check and the usage read already run the tool in a directory and are the candidates.
2. **A long-lived token made by the official tool.** The person runs the tool's own long-lived token command once per subscription and gives the result to looptender, which keeps it in the operating system's secret store. No refresh, no shared store, the same on every system. But looptender then holds a credential, which it has never done.
3. **Refresh it ourselves** with the stored refresh token and write the result back. Rejected: it writes into a store we do not own and races the official tool.

**Proposed:** 1, if the verification finds a refresh that costs no model request. Otherwise the owner chooses between 2 and living with R7 (a spare subscription that needs a person to wake it). This is the first open question.

### Hard rules

- Loopback only; the token-carrying channel between front and router is not a network port.
- A per-client secret gates everything except plain pass-through (below).
- Nothing of a request or response is logged or stored. The only per-request facts that leave the front are: conversation key, model name, status code, `Retry-After`, rate-limit header values. On disk the conversation id is kept only as a keyed hash.
- Another subscription's token is attached only to the provider's message paths (an allowlist). Every other path is passed through unchanged on the session's own login or refused; token-minting, sign-in and billing paths are always refused.
- No credential is copied between stores. No token appears in `status`, `doctor`, a notice, the panel, the timeline or a report.
- The front never changes a request's body. It does not rewrite the model, the metadata or the prompt.

## Routing

The decision is a pure function with table tests, like the existing decision engine: `(order, state, conversation, model, now) → subscription or "own"`.

- **The person's order.** One ordered list of catalog entries, set with `looptender router order`. Where a person already keeps the standard route list ([routes.md](../routes.md)), the router's default order is the order in which its accounts first appear there.
- **Sticky per conversation.** A conversation stays on the subscription that last served it while that one has room. Pins are saved, expire after a week unused, and are capped in number.
- **New conversations** go to the first subscription in order that has room.
- **Two thresholds**, because a switch is not free:
  - **Soft (default 90% of any window):** new conversations go elsewhere. A pinned conversation moves only at a **cold point**: its first request after it has been quiet for longer than the prompt cache lasts (default 5 minutes). The cache is gone by then anyway, so the move costs nothing extra.
  - **Hard (default 98%, or the provider answering 429 with its limit headers):** the conversation moves on its next request, mid-turn if need be. Stopping is worse than a cache miss.
- **Switch-back after a reset.** When a more preferred subscription has room again, new conversations use it at once, and pinned conversations return at their next cold point. Never mid-turn: nothing is gained by it.
- **What a switch costs a session:**
  - *Prompt cache:* caches belong to the account that wrote them [unverified], so the first request after a switch pays for the whole context uncached, which spends more of the new subscription's window. This is why the soft threshold waits for a cold point.
  - *Model availability:* plans differ. The router keeps, per subscription, the models it has seen refused as unavailable, and a catalog entry may declare its models. A subscription that cannot serve the conversation's model is skipped for it (R19).
  - *Account-level settings:* anything the provider applies per account (retention, organisation policy) follows the serving subscription request by request. `router status` names the serving subscription per conversation so this is never hidden.
  - *The person's settings, hooks and instructions:* unchanged. The session never leaves its configuration directory; only the credential on the wire changes. This is how the "carry the person's safeguards or refuse" rule is met: nothing has to be carried.
- **Room** is read from, in order of freshness: the rate-limit headers on that subscription's own responses (exact, free); the usage-limit record (PR #489) and the usage-burn readings (PR #496) that supervisors already write; the catalog's usage read for idle subscriptions. Every reading has an age, and an old reading is shown as old.
- **What never causes a switch:** a refusal, a policy answer, a 403, a 5xx or 529, a network error, an answer the front cannot classify.
- **Holds.** A 429 holds a subscription until the reset it names (believed for at most an hour without a named reset time); the hold is saved.

## State that persists

In the per-user state directory, under `router/`, written with the existing private-file, lock and atomic-write helpers:

| File | Holds | Never holds |
|---|---|---|
| `router.json` | on/off, the order, thresholds, the front's port, the pointer's record of what it wrote where | secrets |
| `clients.json` | one entry per client: a hash of its secret, the settings file it was written to, its home subscription | the secret itself after first issue |
| `pins.json` | keyed hash of conversation id → subscription, last used | conversation ids, anything of a request |
| `rooms.json` | per subscription: windows with used percentage and reset, holds, models seen unavailable, each with its time | tokens, identities |
| `events.jsonl` | switches and failures as fixed kinds with counts and labels, size-capped | paths, prompts, tokens |

The front's own start-up record (port, channel path, protocol number) is the only file it reads.

## Per-conversation and per-lane choice, without BB changing

BB starts every conversation in one directory, so the pointer and client secret are the same for all of them. What differs per request is the conversation id. Three grains follow:

1. **Per launch.** A headless route starts the agent itself, so it sets its own client secret in the launch environment, and a client can carry its own order ("this loop: spare subscription first").
2. **Per conversation.** `looptender router pin --conversation <id> --account <label>`, and the same from the panel. A supervisor already knows which conversation it supervises; mapping a BB thread to its Claude Code conversation id uses names only (the session file's name under the directory's `projects/`), never transcript text.
3. **Per lane.** A coordinator's loop can give its lanes an order of their own ("lanes on the spare subscription, the coordinator on the main one"), resolved to per-conversation pins by the supervisor. This is also the answer to sixteen lanes draining one window: the router spreads new lane conversations by room, and the burn warning says so before the window ends.

BB needs no change. If BB later offers a per-thread account, the router does not conflict with it: a thread in another directory is simply another client.

## How it uses what exists

- **Accounts catalog** (`internal/accounts`): the list of subscriptions is the catalog's entries; the router adds no second list. An entry opts in with `looptender router account add --id <id>`.
- **Login fingerprint** (PR #498): two entries with the same login fingerprint are one subscription. The router counts them once and says so, instead of "switching" between two names for one quota (the state the catalog was in after 2026-11-23). An entry whose fingerprint changed since it was added leaves the rotation until a person confirms it.
- **Usage-limit record** (`usage-limit.json`): the router writes the same record when it sees a limit, so status, the panel and the `limit` notice keep one source. With the router on, a supervisor reads "is there room anywhere?" from the router before treating one subscription's limit as the loop's limit.
- **Usage-burn** (`internal/usageburn`): burn is projected per subscription and for the set ("at this rate all subscriptions are used up in about 9 h").
- **status:** a `router` block: running or not, front and router separately, the pointer per settings file, each subscription's room and age, the serving subscription for this loop's conversation, decisions missed. `status --json` carries the same.
- **doctor:** checks, each with its fix: front answers; router answers; pointer present and ours; pointer not stale; each subscription's login readable; two entries are not one login; service registered; lingering (Linux).
- **Panel and menu:** an Accounts view row per subscription with "serving N conversations"; the menu's reason line names a switch ("moved to <label> at 11:45: weekly limit on <label>; back after <time>").
- **Notices:** new kinds `router_down`, `router_front_gone`, `router_login_stale`, `router_switched` (off by default; one per switch, grouped), alongside the extended `limit`.

## Model step-down

PR #512 adds an opt-in step-down: at one model's weekly limit, the loop changes the conversation's model through BB and changes it back after the reset. It is the right mechanism and stays.

- **The router decides, the client's own control acts.** The front never rewrites the model in a request: the client would believe it is talking to one model while another answers, and context size and features differ by model. A step-down is carried out as #512 does it, or by the launch arguments of a headless route.
- **Order of preference at a model-specific limit**, unless the person's order says otherwise:
  1. the same model on another subscription with room (the conversation keeps its model; costs a cache miss);
  2. the next model on the same subscription (#512; keeps the cache's account, changes the model);
  3. the next model on another subscription.
- **How they fit:** with the router off, #512 works exactly as written. With the router on, #512's check asks the router first, and steps down only when the router answers that no subscription has room for that model. Its record (`model-step.json`) is unchanged, and the step back waits for the earliest reset among subscriptions.

## Providers

- **Claude Code first.** Everything above.
- **Codex subscriptions** would need: a second front profile (a different API shape, limit signals and login store under its own home directory), confirmation that its CLI can be pointed at a loopback base URL while keeping its own login [unverified], and its own conversation key. The decision function, state, pointer check and surfaces are shared.
- **Gemini subscriptions** would need the same three facts established; nothing is known yet.
- **Between providers stays a takeover.** A conversation cannot be continued by another provider's model through a header swap. Moving from Claude to Codex remains the new-conversation takeover with a brief. The router and the takeover meet in one place: the takeover is considered only when the router reports no room on any subscription of the provider.

## Security review of the local listener

| Threat | Defence |
|---|---|
| A web page calling the port | `Host` must be `127.0.0.1:<port>` or `localhost:<port>`; no CORS answers; requests carrying `Origin` are refused. Same guards as the panel's listener. |
| Another local program of the same user | It could already read that user's logins; the listener gives it nothing new. The client secret still stops accidental use. |
| Another user on the machine connecting to the port | Without a valid client secret a request is never given another subscription's token. It is forwarded unchanged or refused, which gives the caller nothing it did not bring. |
| Another user's program taking the port when the front is not running | The serious one: sessions would send their own login to it. Defences: the system holds the socket from login where it can (D); a port outside the ephemeral range, fixed per user; the pointer check removes the pointer when the listener on the port is not our front (the health answer is signed with a key from the private state directory). Residual on systems without D: the window before the check runs. A loopback TLS certificate pinned for the client would close it; see open questions. |
| The client secret leaking (it is in a settings file) | The file is made `0600` when the pointer is written; a directory others can write is refused. The secret only selects routing; it is not a provider credential and cannot mint one. `router rotate` issues a new one; the old stays valid until its sessions end. |
| Using the listener to reach other hosts or sensitive paths | One fixed upstream host per provider profile; a path allowlist; minting, sign-in and billing paths refused. |
| Tokens in memory | Access tokens only, until expiry; never in a core dump setting we control, a log or an error text. Errors name the subscription's label, never a value. |
| Large or slow requests exhausting the front | Body cap, header and idle time limits, a cap on concurrent requests per client. Over a cap the request is passed through unbuffered (no retry) rather than refused. |
| Private channel abuse | A Unix socket in a `0700` directory, peer user checked; a named pipe with a user-only access list on Windows. |
| What the listener must never become | A way to disguise which account is asking. The front attaches a subscription's real token and changes nothing else. |

## Phased plan

Each phase is one or two small draft pull requests.

1. **Decision function.** `internal/router/route`: order, pins, soft and hard thresholds, cold points, switch-back, holds, model availability, "what never causes a switch". Pure, table tests. No I/O.
2. **The front against stand-in upstreams.** `internal/router/front` and a hidden command: listener, guards, bounded decision call, pass-through on every failure, one retry before the first byte, streaming, drain. Tokens are test strings; upstreams and the router are local test stand-ins. Tests are the failure model: router absent, router killed mid-stream, slow decision, 429 and 401 before the first byte, 5xx, stream cut, body over the cap, wrong `Host`, wrong secret.
   - **2a, independent of 1 and 2:** the mode setting, mode `sessionpipe` (reading the pool, the decision changes, the standing warning, `pool_down`) and the detection table. No listener, no credential.
3. **Router process, private channel, state**, and `looptender router status`. Still stand-ins.
4. **The pointer**: write and remove with byte-exact restore, the pointer check, `router on|off`, the uninstall order. Tested in temporary directories only.
5. **Services** on macOS and Linux, then Windows.
6. **Reading a real login** (needs the owner's approval of this design): the per-system store readers, the idle-subscription decision, and the verifications below, run with the owner.
7. **Surfaces**: doctor, panel, menu, notices, the usage records.
8. **Per-conversation and per-lane choice**; the #512 hand-off.
9. **Other providers.**

**The smallest first slice** is phases 1 and 2: the front and the routing decision against stand-in upstreams. It reads no credential, writes no setting, registers no service and is reachable only from tests and a hidden command, so it can merge before any decision about credentials. It proves the claim the whole design rests on, that a dead or slow router is invisible to a session.

**Size, honestly.** sessionpipe's pool is about 2,700 lines of TypeScript without tests, for one provider and one process. Ours has two processes, three operating systems and the surfaces:

- phases 1 and 2: about 1,500 to 2,000 lines of Go including tests;
- phases 3 to 5: about 3,000 to 4,000, of which Windows service support is new ground for the project;
- phases 6 to 8: about 2,500 to 3,500;
- in all, 10 to 14 pull requests and roughly three to five weeks of lane time at the current review and CI pace, not counting a second provider. The live steps in phase 6 need the owner present, and each [unverified] item that turns out false costs a redesign of its part.

## What must be verified

None of these has been tested; no login, account switch or settings change was made for this design.

| # | Assumption | If false |
|---|---|---|
| V1 | Claude Code sends its own subscription login to a custom `ANTHROPIC_BASE_URL` when no token variable is set | The pointer must also set a token, as sessionpipe does; the front then needs the home login to go direct, which weakens rule 2. This is the most important one. |
| V2 | The base URL may carry a path prefix (for the client secret) | The secret moves to a custom header variable. |
| V3 | A custom base URL does not change which features the client offers (context size, usage display) | An extra setting may be needed, as sessionpipe uses. |
| V4 | The conversation id is readable from a header or from `metadata.user_id` | Stickiness falls back to per client. |
| V5 | The provider accepts a request whose metadata names one account while the token is another's | The front would have to change the body, which this design forbids; the owner decides. |
| V6 | What Claude Code does when a stream is cut, and when a connection is refused (retries, how many, how long) | R4 and the Windows restart gap are worse than stated. |
| V7 | An invocation of the official tool refreshes a directory's login without a model request | Idle-subscription option 1 falls; see credentials. |
| V8 | Where the login is stored on Linux and Windows today, and whether another program of the same user can read the macOS keychain item without a prompt | Phase 6's readers change; a prompt would need a one-time consent step. |
| V9 | Prompt caches are not shared between accounts; the cache lifetime in use | The cold-point default changes; the rule stays. |
| V10 | The rate-limit header names and the 429 shape for each window | Room falls back to the usage read until corrected. |

V1 to V6 can be answered with a stand-in upstream on loopback and one throwaway session; that session's real login would reach our stand-in in a header, so it is a credential-touching test and waits for the owner.

## Open questions for the owner

1. **Reading logins.** May the router read each opted-in subscription's access token from the official tool's own store, read-only, kept in memory only? This is the first time looptender would read a credential.
2. **Idle subscriptions.** If no free refresh by the official tool exists (V7): a long-lived token held by looptender in the system's secret store, or a spare subscription that sometimes needs a person to wake it?
3. **Plan terms.** Routing several subscriptions through one router is yours to decide per account. The design keeps the router off until turned on and each subscription out until added. Is anything more wanted, for example a line in `router on` that says so?
4. **AGENTS.md.** The engineering rules say looptender "never copies credentials" and holds none. The router copies none but reads them. Should the rules be amended to say exactly that, in the same pull request as phase 6?
5. **The verification session** (V1 to V6): when, and on which subscription?
6. **Defaults.** Soft 90%, hard 98%, automatic switch-back at cold points, decision bound 150 ms. Any of these different?
7. **Scope of the pointer.** Only BB's directory at first, or every Claude Code directory in the catalog?
8. **The front's last act (R13).** Should the front, when it finds its own binary gone, remove the pointer itself before it stops? It is the only file write the front would ever do.
9. **A second machine user taking the port.** Is the pointer check enough, or is a pinned loopback certificate worth its setup cost?
10. **Windows.** Before or after the surfaces?
11. **`router_switched` notices.** Off by default as proposed, or on?
