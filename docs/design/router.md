# The account router (design)

Status: design, approved by the owner on 2026-11-24 (see [Decisions](#decisions)). The routing decision and the forwarding front are built against stand-ins and used by nothing. The provider assumptions for Claude Code were checked on 2026-11-24 ([Verification results](#verification-results)): the premise holds, and several parts of this document were changed to match what was seen.

## Why

A person with two subscriptions of the same provider expects looptender to move work to the second one when the first runs out. Today it cannot do that for a conversation that is already running:

- Every Claude Code conversation started by BB runs on one configuration directory, so on one login. looptender has no per-conversation account to change ([accounts.md](../accounts.md): "BB thread routes have no per-thread account").
- The new-conversation takeover moves work to another provider, with a brief instead of the conversation.
- On 2026-11-23 the account behind every conversation reached its 5-hour and then its weekly limit while a second subscription sat almost unused. looptender waited and said nothing.

The router is looptender's own answer: a persistent per-user service that the agent is pointed at, which sends each request to a subscription with room. A similar thing exists in sessionpipe (its "pool"). It works, but while it is on, a stopped daemon means every session gets "connection refused". This design starts from that failure and works outwards.

Basis marks used below:

- **[observed]**: seen happening (the 2026-11-23 incident, or the isolated failure lab of sessionpipe's pool 0.18.2 against a stand-in upstream).
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
- Whether a plan's terms allow using several subscriptions this way is the person's decision. looptender gives no opinion on it, and the router is off until a person turns it on and names the subscriptions it may use. `router on`, `router account add` and the documentation say what to check, in these words: "Using several subscriptions through one router, and moving one conversation between them, may be governed by your plan's terms. Check them for each subscription you add." They also name the practical risk: a provider may treat requests whose login changes mid-conversation as unusual and restrict an account, and that is the one consequence looptender cannot undo.
- Nothing in the core path assumes macOS.

## The parts

```
 Claude Code session ──loopback──▶ front ──HTTPS──▶ provider
   (own login in every request)           │  ▲
                                          │  │ decision, bounded wait
                                          ▼  │
                                         router ── reads ── accounts catalog, usage records,
                                                            each subscription's own login store
```

**The front** is the only thing in the path of a request. It is deliberately dull:

- It accepts a request on loopback, checks it (see [Security](#security-review-of-the-local-listener)), and asks the router one question: "which subscription for this conversation and model?"
- It waits a bounded time for the answer (see [The decision bound](#the-decision-bound)). With an answer naming another subscription, it swaps the request's `Authorization` value for that subscription's and forwards. With no answer, an answer of "your own", or any error, it forwards the request unchanged.
- It holds the connection to the provider itself and streams the response back. The router is never in the byte path, so a router crash cannot cut a stream.
- It reports back to the router, without waiting, only: the conversation key, the model name, the HTTP status, `Retry-After`, and the provider's rate-limit header values.
- It reads no file except its start-up record, writes no file, and imports only the Go standard library and one small internal package. A test enforces the import list.

**The router** makes decisions and holds state:

- It knows the person's order of subscriptions, each one's room, which subscription each conversation is on, and each subscription's current access token (in memory only).
- It answers the front over a private channel (a Unix socket in a `0700` directory; on Windows a named pipe restricted to the user). The answer can contain an access token, so the channel is never a TCP port.
- It can be stopped, upgraded or crashed at any time without a session noticing more than "requests go to my own login for a while".

**The pointer** is the group of settings that sends a session to the front, written into the `env` block of a Claude Code `settings.json`:

- `ANTHROPIC_BASE_URL=https://127.0.0.1:<port>`: a fixed address with no secret in it, served with the front's own loopback certificate. A base URL is configuration that a client may show in its status screen, error texts, debug logs and reports that leave the machine, so it must not carry anything secret.
- The client secret in a custom header, through the client's variable for extra request headers (V2, holds). The front removes that header before forwarding.
- The entry that makes the client trust the front's own loopback certificate (V11, holds; see [The loopback certificate](#the-loopback-certificate)).
- `_CLAUDE_CODE_ASSUME_FIRST_PARTY_BASE_URL=1`, the setting sessionpipe's pool uses to keep the client behaving as it does against the provider's own address ([source]). Without it a pointed session was seen to send fewer feature flags (V3), so it is required.
- **No token.** The router does not set `ANTHROPIC_AUTH_TOKEN`, so Claude Code keeps using its own login and sends it with every request (V1, holds). This is the difference from sessionpipe's pool that makes rule 2 possible: every request already carries a credential that works without us.

They are separate processes of the same `looptender` binary: `looptender router front` and `looptender router run`.

## Decisions

Recorded 2026-11-24. The owner's answers are quoted; where the owner delegated, the decision is the coordinator's.

| # | Question | Decision | By |
|---|---|---|---|
| D1 | May the router read each subscription's access token? | Yes: read-only from the official tool's store, kept in memory only, as designed. The owner: "You decide. Lets copy sessionpipe?" | Owner delegated; coordinator decided |
| D2 | How far to follow sessionpipe's pool | As direction, not as code: where its mechanism is known to work, use the same mechanism instead of an unverified alternative; keep what makes ours different. None of its source text is copied: its licence is not established. See [What is taken from sessionpipe's pool](#what-is-taken-from-sessionpipes-pool). | Coordinator, from the owner's words in D1 |
| D3 | Plan terms: the wording shown at `router on` and in the docs, and the stated risk of an account being restricted | Accepted as written. The owner: "Fine. I dont think we have legal liability here." looptender still gives no opinion of its own. | Owner |
| D4 | The verification | No sitting is scheduled. The owner logs each spare subscription in to a directory of its own, and the checks are run against directories made for the test. The owner: "i have all my subscriptions we can connect." The real requests of part C are approved, at most four, only after part A shows the approach works, stopping at the first rejection or warning. | Owner; limits by the coordinator |
| D5 | The AGENTS.md sentence (open question 4) | Accepted; it lands with the first code that reads a login. | Coordinator |
| D6 | Another program taking the port (9) | Ship with the fixed port, the held socket and the pointer check; state the window; add the loopback certificate where verification shows it works. | Coordinator |
| D7 | Defaults (6) | Soft 90%, hard 98%, release under 85%, automatic switch-back at cold points. | Coordinator |
| D8 | Scope of the pointer (7) | Only the directory BB uses at first; others one at a time, by a person's command. | Coordinator |
| D9 | The front's last act (8) | Yes: when the front finds its own program gone, it removes the pointer before it stops. | Coordinator |
| D10 | Windows (10) | After the first release; `router on` refuses a multi-user machine there without a flag naming the risk. | Coordinator |
| D11 | A notice on every switch (11) | Off by default. | Coordinator |
| D12 | Which providers | Codex and Antigravity too, not only Claude Code. The owner: "also codex! and antigravity". Order: Claude Code, then Codex, then Antigravity; see [Providers](#providers) for what each allows. | Owner |
| D13 | What a person operates | One switch, not a set of modes. The owner: "we should integrate where it does the thing where it switches between subs when you hit limits etc behavior you can turn on or off to keep going". See [What the person sees](#what-the-person-sees). | Owner |
| D14 | Connecting a subscription | One easy step for every provider. The owner: "lmk when we ready to log into our subs easily". See [Connecting a subscription](#connecting-a-subscription). | Owner |
| Open | Idle subscriptions (2): a long-lived token held by looptender, or a spare that sometimes needs a person | Undecided until verification says whether it is needed (V7). It becomes necessary if V1 is false; see [If the session's own login does not reach the front](#if-the-sessions-own-login-does-not-reach-the-front). | |

Later notes:

- 2026-11-24: V11 held at launch; the loopback certificate is now the default (see [Verification results](#verification-results)). D6 is kept above as it was decided.

## What is taken from sessionpipe's pool

The pool's mechanism is in use and was exercised in the lab, so where this design needs the same thing it does it the same way. "Lab" means observed with the pool 0.18.2 between a scripted client and a stand-in upstream; "source" means read, and relied on by the pool's users, but not seen with Claude Code itself.

| What | The pool's mechanism | Basis | Here |
|---|---|---|---|
| Pointing Claude Code at a local address | `ANTHROPIC_BASE_URL` in the `env` block of `settings.json`, read at session start | source | The same. |
| Keeping the client's first-party behaviour behind a custom address | `_CLAUDE_CODE_ASSUME_FIRST_PARTY_BASE_URL=1` in the same block | source | The same setting is part of the pointer, instead of hoping none is needed (V3 then checks that nothing else differs). |
| Using another subscription for one request | Replace the request's `Authorization` with that subscription's access token | lab | The same. |
| Saying which account a request is for | Rewrite the account part of the body's `metadata.user_id` to the serving account, keeping the session part | lab | **Adopted, replacing the earlier "the front never changes a body".** With another subscription's token and the home account's id, a request names two accounts; the rewrite makes it name the one that serves it. It is the only change the front makes to a body, it is made only on a routed request, and the account id is read with the token and kept in memory only. The catalog still stores no identity. The limits on it are under [Hard rules](#hard-rules). |
| Knowing a subscription's room | The `anthropic-ratelimit-unified-*` headers of each answer, passed through to the client | lab | The same, as the first source. |
| Stickiness | The session id in `metadata.user_id` | lab | The same. |
| A limit answer before the first byte | One retry on the next account; none after the first byte | lab | The same, plus a visible count when the retry also fails. |
| Refusing dangerous paths | No key-making, sign-in or billing paths through the proxy | source | The same idea, narrowed to paths that mint a credential (V16). |

**What stays different, because it is the point:**

- The session's own login is not replaced: the pointer sets no token, so every request can go on unchanged when anything of ours fails. This rests on V1.
- The front and the router are separate; the front fails open and holds the streams.
- looptender does not refresh, store or import logins. The pool holds every account's refresh token.
- A server error never moves a conversation; a move at the soft threshold waits for a cold point.

## If the session's own login does not reach the front

This is the design if V1 is false: Claude Code, pointed at a custom address with no token set, does not send its subscription login there. Then the pointer has to be the pool's, and the first rule has to change. It is written now so that a "false" does not stop the work.

**The pointer** becomes: the base URL, the first-party setting, and `ANTHROPIC_AUTH_TOKEN` set to the client secret. The secret then travels as the request's bearer (no custom header is needed), and the client stops using its own login for requests, as under the pool.

**What follows from that:**

- Every request needs a credential from us, including one for the session's home subscription. "Forward unchanged" no longer exists.
- Nobody refreshes the home subscription's login any more, because the client has stopped using it. The idle-subscription problem then applies to every subscription, so reading the official store is not enough. Each subscription needs a long-lived token made by the official tool and kept by looptender in the system's secret store (the option left open in D-Open). That is a decision for the owner at that point, since looptender would hold credentials.
- The front must be able to serve without the router. At start, and again before expiry, it is given each client's home token and keeps it in memory; if the router has never answered, it reads its own clients' home tokens from the secret store itself. That one read-only reader is the only addition to the front.

**What replaces the first rule:** "A pointed session survives everything that stops sessionpipe's pool except the loss of the front, or of the home tokens the front holds." Concretely:

| Situation | Session |
|---|---|
| Router down, crashed mid-stream, restarting, state lost | Nothing visible: the front serves every request on the client's home subscription with the token it holds. |
| The front cannot get a home token (secret store unreadable, token revoked) | A 503 the client retries, never a 401, and the pointer check removes the pointer after 30 s so new sessions use their own login again. The person is told which subscription and why. |
| Front down | As in the main design: the held socket, the restart, then the pointer check. |
| All subscriptions full | The provider's own answer for the home subscription. |

**What is lost compared with the main design**, and said to the person at `router on`:

- A pointed session depends on the front holding a valid token; its own login is no longer a fallback.
- Whatever the client does only with its own login behaves as it does under the pool.
- looptender holds long-lived tokens.
- Losing the home tokens the front holds (the secret store unreadable when a token must be read again, a token revoked) stops a pointed session, where in the main design it would go on with its own login.
- `router off` is no longer "off at once, without a restart". A running pointed session has no login of its own in its requests, so the front has to keep serving it on its home token until that session ends; only sessions started after `router off` are back on their own login. `router off` says how many sessions are still being served and that the front stays until they end.

Everything else is unchanged: two processes, streams held by the front, the routing rules, the modes, the pointer check, the surfaces. In the code, the front's "own" decision becomes "the home token" and the secret is read from the bearer instead of a header.

## What the person sees

To a person this is one switch: **switch subscriptions at a limit, on or off**. It is off until turned on.

- **On:** `looptender router on`. **Off:** `looptender router off`. The same switch is in the panel and the menu (later slices).
- **What "on" needs first:** at least two connected subscriptions of one provider ([Connecting a subscription](#connecting-a-subscription)), and the background service. If either is missing, `router on` says which and the one command for it, and changes nothing.
- **How it is done is chosen once, in setup, not each time.** The three mechanisms below (looptender's own router, sessionpipe's pool, or nothing) stay underneath. `router on` uses looptender's own unless setup recorded the pool.
- **When it takes effect:**
  - A session started after `router on` is covered.
  - A session that was already running may be covered from its next turn: a running session was seen to follow a change of the base URL in its settings file (V15). But the trust entry for the loopback certificate may be read only when a client starts (V11 was tested at launch only), in which case a running session pointed at the front would fail its handshake. So `router on` does not rely on it: it says how many sessions were already running and that they are covered after a restart, and the front's count of failed handshakes ([The loopback certificate](#the-loopback-certificate)) tells the pointer check if pointing them did harm.
  - `router off` takes effect at once: the front stops routing and forwards everything on the session's own login. It then **rewrites** the pointer, in one write: the address becomes the provider's own, and the extra-headers variable is changed to a value without the client secret. The keys are removed only later. Change, not removal, because a running session was seen to follow a change of the address and of the headers variable but to keep the old address when its key was removed (V15). In the same moment the client secret is rotated, so a copy that any session still sends is already dead: without this, a session back on the provider's own address would send the secret to the provider on every request, which the design forbids. Sessions started after the removal do not touch looptender at all. Whether a session pointed at the provider's own address by this setting behaves exactly like one with no setting is not verified.
- **One line everywhere.** `status`, the panel and the menu each say, in one line: whether switching is on, which subscription this loop is on, and the last switch with its reason. For example: "Subscription switching: on. This loop: spare. Last switch 11:45, main to spare: weekly limit on main, back after Friday 04:00."

**What happens at a limit, in order.** The switch covers the first step. The later steps already exist and each keeps its own opt-in; the switch does not turn them on.

| Step | When | What happens | What the person is told |
|---|---|---|---|
| 1 | A usage window is full on the subscription a conversation is on, and another subscription of the same provider has room | The conversation continues on that one (this design) | "Moved to <label>: <which limit> on <label>, back after <time>." |
| 2 | Every subscription of that provider is full for the model in use, and model step-down is turned on for the loop (#512) | The conversation steps down to the next model, on the subscription with room for it | "All subscriptions are at the <model> limit. Stepped down to <model> until <time>." If step-down is off: "Step-down is off; `looptender model-step` turns it on." The router relies on this step only once #512's open review findings about a step back that was not confirmed are settled. |
| 3 | Nothing of that provider can serve, and a takeover to another provider is armed | A new conversation on the other provider takes over, with a brief | The existing takeover text, plus "every <provider> subscription is at its limit until <time>". If none is armed: the command that arms one. |
| 4 | None of the above | The loop waits for the earliest reset | "All <n> subscriptions are at a limit; the first to have room is <label> at <time>." |

A refusal or a restriction is not a limit and starts none of these steps.

## Connecting a subscription

The switch is only as useful as it is easy to give it a second subscription. Today that takes a person knowing which variable to set for which tool. This part reads no token and does not depend on the router; it is useful by itself for the manual switch.

**What exists and is built on:** the accounts catalog (`looptender accounts add|list|check|usage`), and the panel's unreleased **Connect another Claude account** and **Connect another Codex account**, which already make a private directory and run the provider's own sign-in in it.

**One command**, and later one button:

```
looptender accounts connect --provider claude-code --label spare
```

1. It makes a new private directory for that login (mode `0700`, under looptender's own configuration directory, never one a session already uses).
2. It runs the provider's **official** sign-in for that directory. The person signs in in their browser. looptender never sees, asks for or copies the credential.
3. It asks the official tool whether the sign-in worked.
4. It checks that the new login is not one already connected, by comparing the keyed login fingerprint from PR #498 (never the identity itself). If it is the same account, it says so plainly: "This is the same account as <label>, so it adds no second quota. Kept nothing." and removes the entry it was about to add.
5. It adds the entry to the catalog under the label, and to the router's set if switching is on. A Codex login is not added to the router's set until X7 holds (reading that login from another program without a prompt); until then it is a catalog entry like any other.

- **`looptender accounts disconnect --label spare`** takes the entry out of the catalog and the router's set and prints the official tool's own sign-out command for that directory. It does not sign out or delete the directory by itself.
- **`looptender accounts list`** shows, per subscription: provider, label, whether the login still works, and its usage windows with their age.
- **Per provider:** Claude Code and Codex each allow a login directory of their own (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`), so both can be connected this way. Antigravity documents no separate login directory, so a second Antigravity account cannot be connected beside the first; `connect` says so instead of pretending (see [Providers](#providers)).
- **The engineering rule** that looptender never logs in stands as it does for the panel's Connect: looptender starts the official tool at a person's explicit command, and the person signs in.

## Three modes, chosen per machine

These are how the one switch is carried out, not something a person operates day to day. The owner's requirement: looptender must be able to turn its own router off and use sessionpipe's pool instead. So there is one setting per machine with three values, chosen once in setup (`looptender router mode <own|sessionpipe|off>`) and shown in `status` and `doctor`. `router on` and `router off` move between the chosen mechanism and `off`.

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
| `off` | The pool is on and covers a supervised directory | Reads it as in mode `sessionpipe` so limits are not misread, without changing the recorded mode | On every `status` and every check, not once: "sessionpipe's pool is switching subscriptions for <directory>, and looptender's limit decisions follow it although the mode is off. Set `router mode sessionpipe` to make that the stated mode." |
| `off` | Our pointer present | As the `sessionpipe` row | The same |

Mode `sessionpipe` and the detection rows need no credential and no listener, so they can be built independently of the router itself; they are phase 2a in the plan.

## The failure model

### How sessionpipe's pool fails

Two inputs: its source, and an isolated lab that ran version 0.18.2 against a stand-in upstream with made-up accounts and broke it on purpose (20 of 21 cases; disk full was not run). "Our answer" points at the row or section of this design that deals with it.

| # | Situation | What a session gets | Basis | Our answer |
|---|---|---|---|---|
| S1 | Its daemon is not running while the pool is on | "connection refused" on every request, at once, until a person acts | [observed] | R1: invisible. The session's own login is in every request and the front forwards it. |
| S2 | A crash leaves `settings.json` naming the dead proxy | Every new and running session refused; only the pool's off command or a daemon start recovers, and a person must run it | [observed] | R6, R11: the pointer check runs in every looptender process, not in the router. |
| S3 | Daemon killed, or stopped politely, while a response is streaming | A 200 that ends early. A polite stop does not drain either. | [observed] | R2: the router is not in the byte path. R5 and [Stopping](#stopping-and-draining): the front drains. |
| S4 | The upstream drops a stream midway | A truncated 200; the pool's log records it as a success | [observed] | R23: the cut is passed on as a broken connection, never as a clean end, and counted as a failure. |
| S5 | The upstream hangs | The client hangs for as long as it is willing to (45 s in the lab): the pool has no time limit | [observed] | R24: bounded connect, first-byte and idle times. |
| S6 | Its port is taken at start | The pool moves to another port and rewrites settings. Sessions already running keep the old port, and their bearer is sent to whatever program now holds it. | [observed] | R25: the port never moves under running sessions; see the security review. This matters more for us, because what travels is the session's real login. |
| S7 | Its secrets file is missing | 401 "unknown bearer", which a client may treat as "sign in again" | [observed] | Rule: the front never answers 401. An internal failure becomes pass-through, or a 503 the client retries. |
| S8 | Its secrets store cannot be read (corrupt file, locked keychain) | 503 with a retry, for every session, although each session's own login might still work | [observed] for the file, [source] for the keychain | R14: own login. |
| S9 | A 429 before the first byte | Retried once on the next account, about 5 ms later. Only once, and never after the first byte; when the retry also fails nothing says so. | [observed] | Kept: one retry, never after the first byte. Added: an exhausted retry is counted and shown (R10). |
| S10 | Two new sessions arrive together while an account is about to cross the threshold | Both land on it, because its numbers arrive only with the answers | [observed] | [Routing](#routing): headroom is reserved for conversations placed since the last reading. |
| S11 | The state file is corrupt at start | It starts; pins and holds are reset; nothing is logged | [observed] | R21: started fresh, said so, file kept aside. |
| S12 | A 401 on an account | The pool refreshes that account's token itself and retries; with a fresh token it rests the account 60 s and moves on | [observed] | R8: we never refresh. Read the store again once, else the session's own login. |
| S13 | A 5xx | Passed through; the account rests 60 s, which moves its sessions to another account if one has room | [observed] pass-through, [source] the move | R18: a 5xx never causes a switch. It is counted per subscription. |
| S14 | Every account is full | The upstream's 429 when an account can still be asked; otherwise the pool's own 429 text | [observed], [source] | R10: always the provider's own answer for the session's own login. |
| S15 | Turning the pool on with no daemon installs a service as a side effect | A settings command that also registers a background job | [observed] | `router on` refuses until `router service install` has been run and the front answers. |
| S16 | The pool's listener lives inside a daemon that exits if an unrelated control socket cannot be bound | Pool on, nothing listening | [observed] | The front is its own process and shares no fate with the router or any other part of looptender. |
| S17 | Rotating the pool's bearer | Sessions holding the old one fail after 10 minutes | [source] | A client secret stays valid until its sessions end; an unknown secret is passed through, not refused. |
| S18 | The session's own login is replaced by the pool's bearer | Nothing to fall back to: a single point of failure by construction | [source] | The pointer sets no token. |
| S19 | It holds every account's refresh token and refreshes them itself | A second holder of each login; a refresh race with the official tool can sign a directory out [unverified] | [observed] that it refreshes | [Credentials](#credentials): the official tool refreshes. |
| S20 | The next account's plan lacks the model in use | The upstream's error passes through; the pin stays | [source] | R19. |
| S21 | A switch at 98% happens on the next request, mid-turn | No extra latency, but a full-price prompt on the new account [unverified: not observable with a stand-in] | [observed] timing | Soft threshold and cold points. |
| S22 | Config directories outside the home directory's `.claude*` names are not found | BB's directory is not pooled unless the person runs the command with a variable set | [source] | Directories come from the accounts catalog, never from scanning. |
| S23 | Request metadata is rewritten to name the serving account | Nothing visible; the request names the account that serves it | [observed] | Not a failure: adopted (D2). |

### How ours could fail, what the session experiences, and what the person is told

"Own login" means the request is forwarded unchanged on the login the session started with.

| # | Situation | A running session | The person is told |
|---|---|---|---|
| R1 | Router not running (never started, stopped, crashed) | Nothing visible. When the private channel refuses the connection (nobody is there), the front knows at once, forwards on the session's own login, and asks again only every 5 s. Conversations it already holds a decision for keep it until that subscription's token expires. | Notice `router_down`: "The router is not running; N sessions are on their own login. `looptender router start`". Status, doctor, panel, menu. |
| R2 | Router crashes while a response is streaming | Nothing. The front holds the stream. | As R1. |
| R3 | Router restarting during an install or upgrade | As R1 for the seconds it takes. Conversation-to-subscription pins are on disk, so each conversation returns to the subscription it was on. | Nothing unless it stays down longer than a minute. |
| R4 | Front crashes or is killed | In-flight streams through it are cut: the session sees a broken response and retries it by itself [unverified]. New requests: where the operating system holds the listening socket (launchd, systemd), the connection is accepted and waits for the restarted front, typically well under a second. Elsewhere, connections are refused until the service manager restarts it. | Notice `router_front_restarted` if it restarts more than twice in ten minutes, with the count. |
| R5 | Front replaced during an upgrade | The front is replaced only when its protocol number changed, which is rare, and then **at an idle moment**: the installer asks the running front whether any stream is open and restarts it at the first moment none is, waiting up to 24 h. Until then the old front does not understand the new router and treats it as absent, so requests go on their own login. The restart itself is a pause on a held socket (macOS, Linux) or a short refusal (elsewhere). If no idle moment comes in 24 h, the front is restarted with a 20 s drain and whatever is still open is cut. An earlier draft promised a handover in which the old front finishes its streams while a new one accepts; with the listening socket owned by the service manager's job that needs passing descriptors between processes outside the job, and it is not worth it for an event this rare. | Nothing in the normal case. "The router front is waiting for an idle moment to upgrade; routing is paused" after 10 minutes. If streams were cut: how many. |
| R6 | Front not running and not restartable (service removed, binary deleted by hand, port taken by another program) | Running sessions get "connection refused": this is the one case rule 2 cannot cover. Its real size is small if the repair is done the right way: a running session follows a change of the address in its settings file on its next turn, but keeps the old address when the key is removed (V15). So the pointer check repairs by rewriting the pointer to the provider's own address, and running sessions then return to the provider directly, as new ones do. What remains is the time until the check runs, and any session that does not re-read its settings (interactive sessions and other builds have not been checked). | Every looptender process that runs (supervisors, the menu, any command) checks the pointer. If the front does not answer for 30 s: it rewrites the pointer to the provider's own address, so running and new sessions go direct, and raises `router_front_gone`: "N running sessions point at a router front that is not running. Start it with `looptender router start`, or restart those sessions." See [Fail-open](#fail-open-concretely). |
| R7 | A subscription's access token has expired and nothing refreshed it | Nothing. That subscription is skipped; requests go to the next one with room, or the session's own login. | Notice `router_login_stale`: "<label>: its login has not been refreshed since <time>. Run `<the official tool's command>` in <directory>." |
| R8 | The provider rejects a routed subscription's token (401) | Before any byte reached the session: the front sends the request again once, unchanged, on the session's own login. | `router_login_stale` for that subscription, which leaves the rotation until its login is read again successfully. |
| R9 | A subscription is restricted or refused (403, policy) | The provider's answer passes through untouched. No other subscription is tried for that request. | "<label> was refused by the provider. looptender does not route around that; it is out of the rotation until you clear it." |
| R10 | Every subscription is full | The provider's own limit answer for the session's own login passes through, exactly as without a router. The front never invents a limit message. | The existing `limit` notice, extended: "All N subscriptions are at a limit; the first to have room is <label> at <time>." The loop's limit wait uses that time. |
| R11 | Settings left pointing at a dead front after a crash | As R6. | As R6. |
| R12 | Uninstall | `looptender router off` rewrites the pointer to the provider's own address first, then keeps the front running as a plain pass-through until no session has used it for an hour, then removes the service. Running sessions never see a refusal. | `router off` says how many sessions still use the front and when it will go. |
| R13 | The binary is deleted without `router off` | On Unix the running front keeps working until logout or restart, on its own login. After that, R6 with nothing of ours left to repair it. | Only the provider tool's own connection error. This is the residual risk; see [Open questions](#open-questions-for-the-owner) for the "last act" option. |
| R14 | The login session is gone (as on the evening of 2026-11-23: new processes could not read the keychain or resolve names, running ones were fine) | The front and router are already running with tokens in memory, so routed requests continue until those tokens expire; after that, own login. A session whose own process has the same problem fails as it would without us. | "Cannot read the login for <label> right now" (never "signed out"), using the distinction from PR #508. No one is told to sign in again unless a login was actually read as absent. |
| R15 | Disk full | The front needs no disk. The router keeps deciding from memory and stops saving; a restart then loses pins made since the last save, which costs at most a prompt-cache miss per conversation. The pointer is never written when a write cannot be completed atomically. | The existing `disk` notice, plus "router state not saved since <time>". |
| R16 | Two users on one machine | Each user has their own front, port, client secrets, router and state directory. The private channel is per user. One user's front cannot be asked for another user's tokens. | Nothing. See the security review for a port taken over by another user. |
| R17 | The router's decision is slow (machine overloaded) | A conversation the front already holds a decision for is not affected: no question is asked. A first request waits up to the bound, which grows with observed delay to at most 500 ms, then goes on its own login. If that answer is a 429, the second decision may wait 2 s. Slow answers never stop the front from asking. | Counted; shown in `router status` as "decisions missed". |
| R18 | The provider is unreachable or answers 5xx/529 | The provider's answer passes through; with no connection at all, the front answers 502 and says so. No switch: an unclassified error never causes a move. | Nothing new. |
| R19 | The next subscription cannot use the model | The first such answer passes through once; the pair (subscription, model) is then skipped for that model. | "<label> does not offer <model>" on `router status`. |
| R20 | The home subscription itself is logged out | The session cannot start a request at all; that is the provider tool's own "not logged in". The router is not involved. | The existing account check. |
| R21 | State file damaged or from a newer version | The router starts with empty pins and the order from configuration, and keeps the unreadable file aside. | "Router state could not be read; started fresh. Kept as <name>." |
| R23 | The provider drops a stream midway | The front has already sent a 200, so it cannot change the status. It breaks the connection to the session instead of ending the response cleanly, so the session sees a transport error and applies its own retry, never a response that looks complete. Not retried by the front. | Counted as `stream_cut` against that subscription in `router status`. Never recorded as a success. |
| R24 | The provider hangs | Bounded: 10 s to connect and 15 min to the first response byte. Silence inside a stream is not bounded unless a bound is set: a client was seen to wait four minutes on a silent stream without giving up (V12), and how often the provider sends something is not known, so a bound of two minutes, as first written here, would cut a stream the client would still have waited for. The first-byte bound is deliberately above the client's own request limit (about 10 minutes by default [unverified, V6]), so the front never fails a request the client would still have waited for: a request that is slow to start, such as one that is not streamed and has a long answer, succeeds through the router exactly when it would without it. The bound exists only so that a connection the client has abandoned cannot stay open for ever. On expiry before the first byte: a 504 that names the router and the bound. Inside a stream: as R23. All are configurable; the first-byte bound may never be set below the client's. | Counted as `upstream_timeout`. |
| R25 | The front's port is taken by another program | The front never picks another port and never rewrites the pointer to a new one while sessions run. It keeps trying its own port. Sessions that connect meanwhile reach the other program: where the loopback certificate is in use the handshake fails and no login is sent; otherwise this is the leak the lab observed, limited by the pointer check. | `router_front_gone`, naming the cause: "port <n> is held by another program". The pointer check rewrites the pointer to the provider's own address at once in this case, without the 30 s wait. |
| R26 | A stale private-channel socket file is left by a kill | The router removes it at start after confirming nobody answers on it. The front treats a dead channel as R1. | Nothing. |
| R22 | Clock jumps | Holds and resets are compared against both wall time and the time they were set; a hold set "in the future" is cut to a minute. (Taken from sessionpipe's policy, which handles this well.) | Nothing. |

## Fail-open, concretely

### The path of one request

1. The front accepts the connection and checks `Host`, the path and the client secret, and removes the secret's header.
2. It reads the body into memory (up to 32 MiB) and takes two things from it: the model name and the conversation id. Nothing else is parsed or kept.
3. If it holds no decision for this conversation, it asks the router and waits up to the decision bound.
4. It forwards to the provider: with the named subscription's token, or unchanged.
5. If the provider answers 429 or 401 **before any byte has been sent to the session** and the request was routed, the front reports it and tries once more: on the next subscription the router names, or unchanged on the session's own login. A 429 or 401 means the provider did not run the request, so sending it again is safe; this second decision may wait up to 2 s, because the request has failed already. The same applies when the session's own login is the one answered 429: that is the moment routing matters most. After the first byte, and after any connection error once the body was sent, nothing is retried by the front.
6. It streams the response. Rate-limit headers pass through, so Claude Code's own usage display shows the subscription that served the request.
7. It tells the router the outcome and forgets the request.

A request already in flight is never moved. If the router dies during step 6, nothing happens to it. If the front dies during step 6, the stream is cut and the session's own retry applies; no design that sits in the path can do better, which is why the front does so little.

### The decision bound

A fixed 150 ms would fail exactly when routing matters: on a heavily loaded machine every decision could miss it, and the session's own login, perhaps the one at its limit, would serve everything while a spare subscription sat idle. So:

- **Most requests ask nothing.** The front keeps the last decision for each conversation (the subscription, and its token until expiry). Only a conversation's first request, a request after the decision expired, and a retry ask the router. The router sees every outcome and replaces a held decision whenever its own answer changes (a threshold crossed, a cold point reached, a reset); a 429 or 401 drops it. With the router gone, a held decision lasts until its token expires.
- **The first wait adapts.** It starts at 150 ms and follows the router's recent answer times up to 500 ms.
- **The retry waits longer.** After a 429 or 401 before the first byte, up to 2 s.
- **A slow router is still asked.** Only a refused channel (no router at all) makes the front stop asking, for 5 s at a time.
- **Misses are counted** and shown, with the load at the time, so "routing was skipped because the machine was overloaded" is visible rather than silent.

### What the front may answer by itself

A client acts on status codes, so the front's own answers are few and fixed:

- **Never 401 and never 429.** Those mean "sign in again" and "you are at a limit" to the client, and only the provider may say them.
- **403** for a guard failure (wrong `Host`, an `Origin` header, a refused path): these are not the client's requests.
- **503 with `Retry-After`** when the front itself cannot forward (out of connections, shutting down past its drain).
- **502** when no connection to the provider can be made.
- **504** for R24.
- Everything else is the provider's answer, unchanged.

Any internal failure that is not one of these becomes pass-through.

### Stopping and draining

- On a stop signal the front stops accepting, and finishes its open streams before exiting: 20 s when the service manager stops it (an upgrade waits for an idle moment first, R5), 60 s for `looptender router stop` (`--now` skips it). It says how many streams it waited for and how many it cut.
- Connections arriving during the drain wait on the held socket (macOS, Linux) until the next front starts, and are refused briefly elsewhere. This is why the drain is short.
- The router stops at once; nothing depends on it.

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
- **What it does:** rewrites the pointer in that settings file, in one write: the address becomes the provider's own and the extra-headers variable loses the client secret, both of which running sessions follow (V15). It rotates the client secret at the same moment, so nothing a session still sends is worth anything, and records what it changed so `router on` can issue a new pointer. The keys themselves are removed, restoring the bytes that were there before when the rest of the file is unchanged, once no session that read the pointer is left, or at `router off`.
- **It also acts on failed handshakes.** The front counts connections that fail the TLS handshake. Several from loopback with none succeeding means clients are pointed at the front and do not trust its certificate (a build where the trust entry does not take, or a running session that read the address but not the trust entry). The check treats that as "front unusable", exactly as a front that does not answer, and repairs the same way.
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
- Another subscription's token is attached only to the provider's message paths (an allowlist). Only the provider's API paths are forwarded at all (an allowlist of prefixes); on those, every path that is not a message path goes through unchanged on the session's own login. Paths that mint a credential (making a key, exchanging or granting a token), and billing paths, are always refused. The refusal is by what a path does, not by a name it contains: Claude Code also makes read-only calls with the session's own login under the provider's sign-in prefix (account settings, roles, file upload) [unverified which, V16], and refusing those would leave a pointed session worse off than without the router.
- No credential is copied between stores. No token appears in `status`, `doctor`, a notice, the panel, the timeline or a report.
- The front changes one thing in a body and nothing else: on a routed request, the account named in `metadata.user_id` becomes the serving subscription's (see [What is taken from sessionpipe's pool](#what-is-taken-from-sessionpipes-pool)). It never rewrites the model or the prompt, and never changes a request that goes on the session's own login. The rewrite is bound by these rules:
  - Only the account part inside the `user_id` string is replaced, in place. The body is never decoded and written out again: every other byte is identical, and a test holds that.
  - A request is routed only when the rewrite is possible. A body that is not JSON, has no `user_id`, or has one of an unexpected shape goes on the session's own login, unchanged.
  - A request whose body is over the size cap is never routed.
  - An account id is never logged or stored, under the same rule as a token.
  - The rewrite covers `user_id` and nothing else. Anything else in a request that names the home account is V13, and V13 failing stops routing; the front does not grow more rewrites to get around it.

## Routing

The decision is a pure function with table tests, like the existing decision engine: `(order, state, conversation, model, now) → subscription or "own"`.

- **The person's order.** One ordered list of catalog entries, set with `looptender router order`. Where a person already keeps the standard route list ([routes.md](../routes.md)), the router's default order is the order in which its accounts first appear there.
- **Sticky per conversation.** A conversation stays on the subscription that last served it while that one has room. Pins are saved, expire after a week unused, and are capped in number.
- **New conversations** go to the first subscription in order that has room.
- **Two thresholds**, because a switch is not free:
  - **Soft (default 90% of any window):** new conversations go elsewhere. A pinned conversation moves only at a **cold point**: its first request after it has been quiet for longer than its prompt cache lasts. That lifetime is taken from the request's cache settings, never from a fixed guess: five minutes where a request names no longer one, and the longer one where it does. One hour is what is expected of the extended lifetime, not something verified here (see V9: the one observation that looked like a long-lived cache has another explanation). A rule by cost (move when what would be written anew is small) may replace the fixed time once the cost of a move has been measured across accounts.
  - **Hard (default 98%, or the provider answering 429 with its limit headers):** the conversation moves on its next request, mid-turn if need be. Stopping is worse than a cache miss.
- **Switch-back after a reset.** When a more preferred subscription has room again, new conversations use it at once, and pinned conversations return at their next cold point. Never mid-turn: nothing is gained by it.
- **What a switch costs a session:**
  - *Prompt cache:* a cache belongs to the account and the content, not to a conversation (V9): two routed requests of a new conversation were both full cache reads of something written earlier on the same subscription by another session. So a move costs less than the whole context when the serving subscription already holds the shared start of the prompt (system prompt and tools, about 18,000 tokens in the check) from its other sessions; what is always written anew is the conversation's own history. Whether a cache also follows a conversation across accounts was not observable; the provider documents caches as separate per organisation, so the design assumes it does not.
  - *Model availability:* plans differ. The router keeps, per subscription, the models it has seen refused as unavailable, and a catalog entry may declare its models. A subscription that cannot serve the conversation's model is skipped for it (R19).
  - *Account-level settings:* anything the provider applies per account (retention, organisation policy) follows the serving subscription request by request. `router status` names the serving subscription per conversation so this is never hidden.
  - *The person's settings, hooks and instructions:* unchanged. The session never leaves its configuration directory; only the credential on the wire changes. This is how the "carry the person's safeguards or refuse" rule is met: nothing has to be carried.
- **Room** is read from, in order of freshness: the rate-limit headers on that subscription's own responses (exact, free); the usage-limit record (PR #489) and the usage-burn readings (PR #496) that supervisors already write; the catalog's usage read for idle subscriptions. Every reading has an age, and an old reading is shown as old.
- **What never causes a switch:** a refusal, a policy answer, a 403, a 5xx or 529, a network error, an answer the front cannot classify.
- **Headroom for what has not been counted yet.** A subscription's numbers arrive only with its answers, so several conversations placed at the same moment can overshoot (S10). Each conversation placed on a subscription since its last reading counts as an estimated share of its tightest window, and when the reading is older than a few minutes or within five points of the soft threshold, new conversations are spread across subscriptions with room instead of all going to the first.
- **No trading back and forth.** A subscription that caused a move counts as full until its window resets or a fresh reading (from an answer, not an estimate) shows it below 85%. Two nearly full subscriptions therefore cannot pass conversations between them on noisy readings.
- **The cost of a hard switch is shown.** `router status` lists each switch with the size of the request that was sent uncached and the change in the new subscription's window around it, so a large context moving mid-turn is visible as the share of a window it spent.
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
- **The supervised conversation itself:** the front never adds anything to a request, so the agent is not told through the model. When a loop's own conversation was moved, the supervisor's next wake to it carries one fixed line: "looptender's router moved this conversation to another subscription at <time> because of <which limit>; the usage display now shows that subscription." Conversations without a loop get the notice only.
- **Notices:** new kinds `router_down`, `router_front_gone`, `router_login_stale`, `router_switched` (off by default; one per switch, grouped), alongside the extended `limit`.

## Model step-down

PR #512 adds an opt-in step-down: at one model's weekly limit, the loop changes the conversation's model through BB and changes it back after the reset. It is the right mechanism and stays.

- **The router decides, the client's own control acts.** The front never rewrites the model in a request: the client would believe it is talking to one model while another answers, and context size and features differ by model. A step-down is carried out as #512 does it, or by the launch arguments of a headless route.
- **Order of preference at a model-specific limit**, unless the person's order says otherwise:
  1. the same model on another subscription with room (the conversation keeps its model; costs a cache miss);
  2. the next model on the same subscription (#512; keeps the cache's account, changes the model);
  3. the next model on another subscription.
- **How they fit:** with the router off, #512 works exactly as written. With the router on, #512's check asks the router first, and steps down only when the router answers that no subscription has room for that model. Its record (`model-step.json`) is unchanged, and the step back waits for the earliest reset among subscriptions.
- **A condition:** the router makes step-backs more frequent, so #512's open review finding (a step back that overrides a model the person chose meanwhile) must be fixed before the two are connected.

## Providers

The owner wants switching for Codex and Antigravity subscriptions as well as Claude Code (D12). What follows for the two others comes from their public documentation and, for Codex, its public source, read on 2026-11-24. None of it has been run here, and every line of it is [unverified] until a stand-in check shows it.

### What is the same for every provider

- the decision (`internal/router/route`): order, pins, soft and hard thresholds, cold points, holds, the latch, spreading;
- the front's core (`internal/router/front`): the bounded wait for a decision, forwarding unchanged on any failure, holding the stream, one retry before the first byte, bounds, draining;
- state, the pointer check, the three mechanisms, the one switch, the surfaces, connecting a subscription.

### What each provider brings as its own adapter

A provider is described to the front and the router by a profile:

| A profile says | Claude Code | Codex | Antigravity |
|---|---|---|---|
| **How the agent is pointed at a local address** | `ANTHROPIC_BASE_URL` in the `env` block of `settings.json`, with the first-party setting | A custom model provider entry in the Codex home's `config.toml` with `requires_openai_auth = true` (keeps the ChatGPT sign-in), `supports_websockets = false` (the model request is then a POST, not a WebSocket upgrade) and `http_headers` for the client secret. The bare `openai_base_url` also keeps the sign-in but leaves WebSocket on. | **No documented way.** The only endpoint override is for API-key use, not for the Google-account subscription. |
| **Whether the session's own login reaches the front** | V1, unverified | The source reads as if the bearer and the account id are sent to whatever base URL is set; if so, rule 2 holds for Codex without the doubt Claude Code has | Does not apply |
| **The real upstream** | the provider's API host, message paths | `chatgpt.com/backend-api/codex`, path `/responses`, as a streamed POST and also as a WebSocket | Not documented |
| **Where the login lives** | per configuration directory: a keychain item on macOS, a credentials file elsewhere | per Codex home: `auth.json`, or the system keyring, by the `cli_auth_credentials_store` setting | the system keyring, as profiles shared by its app, CLI and editor extensions |
| **Who refreshes it** | Claude Code | the Codex CLI, shortly before expiry and at least about weekly; its documentation warns that a second user of the same login can invalidate the first one's refresh | Not documented |
| **Reading it without copying** | the access token and expiry, read-only, memory only | the same from `auth.json` or the keyring; the expiry is inside the token | Not possible in a supported way |
| **A login directory chosen at launch** | `CLAUDE_CONFIG_DIR` | `CODEX_HOME` | None documented: fixed paths under the home directory |
| **Naming the account in a request** | the account part of `metadata.user_id` in the body | the `chatgpt-account-id` header, which must change with the token; no body change | Not documented |
| **Identifying a conversation** | the session part of `metadata.user_id` (or a header) | the `session-id` and `thread-id` headers; no body is read | a conversation id in its own output, not on the wire as far as documented |
| **Room and resets** | `anthropic-ratelimit-unified-*` headers | `x-codex-primary-*` and `x-codex-secondary-*` headers (used percent, window minutes, reset time); a usage call on the ChatGPT backend | its own `/usage` command, which the catalog already reads; five-hour and weekly windows |
| **A limit answer** | 429 with the limit headers | 429 with error type `usage_limit_reached` and a reset time | Not documented |
| **A long-lived token from the official tool** | exists | only for business and enterprise workspaces | none for a subscription login |

### Codex

Codex fits the same shape, and on paper more easily than Claude Code: it keeps its own sign-in behind a custom base URL, names the conversation and the account in headers, and reports its windows in headers, so the front needs to read no body for it. Open points, its own list:

- X1: the ChatGPT bearer and account id are really sent to a custom base URL.
- X2: whether a custom base URL still uses the WebSocket form of the request, which the front does not forward today.
- X3: which other calls (usage, sign-in refresh) go to other hosts and so never pass the front.
- X4: the provider accepts a request whose bearer and `chatgpt-account-id` were both changed to another subscription's.
- X5: what a switch costs: whether the prompt cache key is per account.
- X6: the exact header names and the limit answer, against a real response.
- X7: that reading `auth.json` or the keyring from another program works without a prompt.

### Antigravity

Antigravity does not fit the proxy shape, and the design does not force it to.

- Its subscription traffic cannot be pointed at a local address in any documented way, so there is nothing for a front to forward.
- It documents no way to choose a login at launch either: one login, kept in the system keyring and shared by its app, CLI and extensions. The accounts catalog reflects this already: it accepts only the `inherited` selection for Antigravity, so two Antigravity entries are two labels for one login, not two subscriptions.
- **What switching means there, then:** a person signs out and in with the official tool (`/logout`, then sign in), and looptender's part is everything around that: it shows each window's use and reset from the usage command the catalog already reads, warns before a window runs out, names the moment and the exact command, and at the limit goes on to the later steps of the order in [What the person sees](#what-the-person-sees) (a smaller model, then a takeover to another provider). looptender does not sign out or in.
- If Antigravity later documents a login directory or profile chosen at launch, it becomes a catalog `explicit_directory` provider and gains switching **at launch** (a headless route starting on the subscription with room), which the catalog and routes already model. Switching inside a running conversation would still need an endpoint override.
- Its own list: A1, any supported way to select a login per launch; A2, any endpoint override for the subscription login; A3, what its limit answer looks like through `agy`.

### Order

Claude Code first, because it is the owner's daily limit and its first slice exists. Then Codex: the reading suggests it is cheaper than Claude Code was, as a second profile on the same front, provided X1 and X2 hold; its stand-in check can run as soon as a second Codex login exists. Antigravity last, and as the smaller job described above, not as a router provider.

### Between providers

Moving a conversation from one provider to another stays a takeover with a brief. A header swap cannot continue a conversation on another provider's model. The router and the takeover meet in one place: the takeover is considered only when the router reports no room on any subscription of the provider (step 3 of the order).

### What in the first slice is Claude-specific

The routing decision carries nothing of a provider. The front's types are neutral (`Ask`, `Decision`, `Outcome`, `Decider`), but its behaviour has Claude Code's facts written into it. These move behind a profile in the next slice:

- the paths a login may be attached to (`/v1/messages`, `/v1/messages/count_tokens`) and the forwarded prefixes (`/v1/`, `/api/`);
- the refused paths;
- the conversation header name and reading the model and conversation from a JSON body;
- the limit header prefix reported to the router (`anthropic-ratelimit-`);
- removing `X-Api-Key` when another login is attached, and which headers name the account;
- which statuses mean "the provider did not run this request" (429, and 401 on a routed login);
- in the test stand-in, the credential prefixes used to tell a kind.

## Security review of the local listener

| Threat | Defence |
|---|---|
| A web page calling the port | `Host` must be `127.0.0.1:<port>` or `localhost:<port>`; no CORS answers; requests carrying `Origin` are refused. Same guards as the panel's listener. |
| Another local program of the same user | It could already read that user's logins; the listener gives it nothing new. The client secret still stops accidental use. |
| Another user on the machine connecting to the port | Without a valid client secret a request is never given another subscription's token. It is forwarded unchanged or refused, which gives the caller nothing it did not bring. |
| Any other program holding the port when the front is not (observed with sessionpipe's pool, S6) | The serious one: sessions would send their own login to it. **The default defence is TLS on loopback with the front's own certificate** (V11, holds): a client told to trust that certificate was seen to refuse another certificate on the same port, with the handshake failing before any request or login was sent. A program without the key cannot stand in for the front. In addition: the port is fixed per user, outside the ephemeral range, and never moves under running sessions (R25); the system holds the socket from login where it can (D); the pointer check rewrites the pointer at once when the port is held by something that is not our front. |
| What the listener must never become, restated for the metadata rewrite | The rewrite makes a routed request name the account whose token it carries. It is never used to make a request name an account other than the one serving it. |
| The client secret leaking | It is never sent to the provider: the front removes its header, and when a session is pointed back at the provider's own address the same write takes the secret out of the headers variable and the secret is rotated. It travels in a request header, never in the URL, so it does not appear where a client shows or reports its base URL. The front removes the header before forwarding, so the provider never receives it. The settings file is made `0600` when the pointer is written; a directory others can write is refused. The secret only selects routing; it is not a provider credential and cannot mint one. `router rotate` issues a new one; the old stays valid until its sessions end. |
| Using the listener to reach other hosts or sensitive paths | One fixed upstream host per provider profile; a path allowlist; credential-minting and billing paths refused (V16). |
| Request smuggling and header leaks through the front | Ordinary reverse-proxy hygiene, each with a test: hop-by-hop and `Proxy-*` headers are removed; a request whose length is stated twice with different values is refused; one with both a chunked body and a stated length is read as chunked, as the standard says, and sent on as one request with one framing, so the provider can never read it two ways (the Go standard library drops the stated length before a handler can see it, so refusing it would mean parsing connections by hand in the one process that must stay dull); the upstream `Host` is fixed, never taken from the request; header size and count are capped; a session that goes silent while sending a request body is cut off after two minutes of silence, a bound on silence and not on the whole upload, so a large body on a slow connection is not cut; the client secret's header is never forwarded; nothing from the provider's answer is used to choose where to connect (no redirects followed); a proxy named in the environment is used only for an `https` upstream, where it carries an encrypted tunnel and sees no header (never using one would cut the front off on a network that requires it, where the session alone would still work). |
| Answer headers that identify the serving account | The provider's answers carry `anthropic-organization-id` and `anthropic-workspace-id`. The front passes answer headers through unchanged, these included, so the client sees the serving subscription's, as it sees its limit headers. The front never logs, stores or reports them: only the limit headers go to the router. |
| Tokens in memory | Access tokens only, until expiry; never in a core dump setting we control, a log or an error text. Errors name the subscription's label, never a value. |
| Large or slow requests exhausting the front | Body cap, header and idle time limits, a cap on concurrent requests per client. Over a cap the request is passed through unbuffered (no retry) rather than refused. |
| Private channel abuse | A Unix socket in a `0700` directory, peer user checked; a named pipe with a user-only access list on Windows. |
| The loopback certificate itself being misused | See below: it is a leaf that cannot sign anything, valid only for loopback names, and never enters a system trust store. |
| What the listener must never become | A way to disguise which account is asking. The front attaches a subscription's real token, makes the request name that subscription's account (the one rewrite above), and changes nothing else. |

### The loopback certificate

- **It can be set from settings.** It was expected that a client running on Node would read an extra trusted certificate only from its launch environment. Checked (V11): Claude Code accepted the certificate when it was named by `NODE_EXTRA_CA_CERTS` in a settings `env` value as well as in the launch environment. The settings value in that check was given per launch; a value in a `settings.json` file has not been tried, and neither has a running session that gains the entry after it started. V11 therefore holds at launch only.
- **When the trust entry does not take,** every pointed session would fail its handshake, which is worse than no router. The fallback is concrete: the front counts failed handshakes, the pointer check treats them as "front unusable" and un-points ([The pointer check](#the-pointer-check-c-in-detail)), and the person is told "sessions do not trust the router's certificate on this build; switching is off until this is fixed". `router on` first makes one connection of its own through the client's trust settings where it can, and refuses if that fails.
- **What the certificate is.** Trusting a file is a risk of its own: if it were a certificate authority, anyone who obtained its key could sign a certificate for the provider's own host and read every connection of that user's client. So:
  - a self-signed **leaf** certificate, marked as not a certificate authority, which cannot sign others;
  - valid only for `127.0.0.1` and `localhost`;
  - an ECDSA key, kept `0600` in the state directory, made on this machine at `router on` and never leaving it;
  - never added to the system's or the keychain's trust store; the only thing that trusts it is the one client setting;
  - `router off` and the pointer check remove the trust entry together with the pointer, and `router off` deletes the key.

## Phased plan

Each phase is one or two small draft pull requests.

1. **Decision function.** `internal/router/route`: order, pins, soft and hard thresholds, cold points, switch-back, holds, model availability, "what never causes a switch". Pure, table tests. No I/O.
2. **The front against stand-in upstreams.** `internal/router/front` and a hidden command: listener, guards, bounded decision call, pass-through on every failure, one retry before the first byte, streaming, drain. Tokens are test strings; upstreams and the router are local test stand-ins. Tests are the failure model: router absent, router killed mid-stream, slow decision, 429 and 401 before the first byte, 5xx, a stream cut by the upstream, a hung upstream, a stop signal with streams open, the port already taken, two conversations placed at once, body over the cap, wrong `Host`, wrong secret.
   - **2a, independent of 1 and 2:** the mode setting, mode `sessionpipe` (reading the pool, the decision changes, the standing warning, `pool_down`) and the detection table. No listener, no credential.
   - **Then, before anything else is built: the supervised verification** of the provider assumptions ([What must be verified](#what-must-be-verified)). V1 is the premise of rule 2; if it is false, the front, the pointer and the fail-open path all change, and phases 3 to 5 would be built on the wrong shape.
3. **Router process, private channel, state**, and `looptender router status`. Still stand-ins.
4. **The pointer**: write and remove with byte-exact restore, the pointer check, `router on|off`, the uninstall order. Tested in temporary directories only.
5. **Services** on macOS and Linux. Windows follows the first release.
6. **Reading a real login.** Before it: V1, V3, V8, V13 and V14 answered; AGENTS.md amended (open question 4); the owner's answers to questions 1 to 4. the per-system store readers, the idle-subscription decision, and the verifications below, run with the owner.
7. **Surfaces**: doctor, panel, menu, notices, the usage records.
8. **Per-conversation and per-lane choice**; the #512 hand-off.
9. **Other providers.**

**The smallest first slice** is phases 1 and 2: the front and the routing decision against stand-in upstreams. It reads no credential, writes no setting, registers no service and is reachable only from tests and a hidden command, so it can merge before any decision about credentials. It proves the claim the whole design rests on, that a dead or slow router is invisible to a session.

**Size, honestly.** sessionpipe's pool is about 2,700 lines of TypeScript without tests, for one provider and one process. Ours has two processes, three operating systems and the surfaces:

- phases 1 and 2: about 1,500 to 2,000 lines of Go including tests;
- phases 3 to 5: about 3,000 to 4,000, of which Windows service support is new ground for the project;
- phases 6 to 8: about 2,500 to 3,500;
- in all, expect the upper end of 10 to 14 pull requests and about five weeks of lane time at the current review and CI pace for macOS and Linux, not counting Windows or a second provider. The independent review judged the earlier "three to five weeks including Windows" optimistic, and it was. The live steps in phase 6 need the owner present, and each [unverified] item that turns out false costs a redesign of its part.

## Verification results

Checked on 2026-11-24 with a harness kept outside the repository: a stand-in for the provider's API that records names, kinds and lengths only, and a sink given to the client as its proxy for every other host, which refuses each connection and records the host name. Sessions were throwaway non-interactive sessions of Claude Code 2.1.326 in the directory of a second subscription ("the spare"), pointed by their launch environment or by a per-launch settings value. No directory that a real session uses was pointed or edited. No token, account id or e-mail address was written to any file or output.

In the stand-in checks nothing reached the provider: the stand-in was the only upstream, and the sink refused every other connection (about 11 per session, all to the provider's API host directly). Seven real requests were then sent in two short runs, described below.

**Three caveats on everything in this section:**

1. It was seen in non-interactive (print mode) sessions on one build of Claude Code. Interactive sessions and other builds are untested.
2. Where a result says "from settings", the value was given per launch with the client's settings flag, not written into a settings file of the spare's directory. Only V15 used a real `settings.json`, in a directory made for the test.
3. Accepted requests today say nothing about what the provider does over days of routed traffic. The first real use should be the owner's own loops, switched on by the owner, and watched.

| # | Result | What was seen | What it changed here |
|---|---|---|---|
| V1 | **Holds** | With no token variable, the session sent a bearer of the subscription-login kind to the custom address, from the launch environment and from a settings value. | The main design stands. [If the session's own login does not reach the front](#if-the-sessions-own-login-does-not-reach-the-front) is not needed for Claude Code. |
| V2 | **Holds** | A header named in the client's extra-headers variable arrived on every request. | The client secret travels as designed. |
| V3 | **False without the extra setting** | Without the first-party setting, four feature flags are missing from requests; with it they are sent. The reported context window was the same. | The setting is part of the pointer. Full parity with a direct session is still unproven: a direct request cannot be seen. |
| V4 | **Holds, in another shape** | The conversation is in a header, `x-claude-code-session-id`. `metadata.user_id` is a JSON object inside a string with three keys: an account id, a device id and a session id. | The front takes the conversation from the header. The rewrite is one value, the account id, replaced in place by one of the same length. |
| V5 | **Holds** | A request with the other subscription's token and the account id rewritten in place was accepted, four times in two runs. | Routing works as designed for Claude Code. |
| V6 | **Observed** | A cut stream: retried about ten times, then asked again without streaming. A 500: retried about eleven times over two and a half minutes. A refused connection: retried for about three minutes. A 401: one retry, then a failure within three seconds. A 429 with limit headers: no retry. No answer at all: still waiting after 130 s. | A front restart of seconds is covered by the client's own retries on every system. The front must never answer 401. |
| V7 | Not run | Needs an expired token. | The idle-subscription question stays open. |
| V8 | **Holds where tested** | macOS: reading the spare's login item (value discarded, exit status only) worked without a prompt from a terminal and from a launchd job in the logged-in domain. | A job with nobody at the screen was not established: starting one in the background domain did not work the way it was tried. Linux and Windows not run. |
| V9 | **Partly** | Routed requests are cached normally on the serving subscription. The first routed request of a new conversation was already a full cache read. What wrote that cache is not known: the earlier run of this check, about 25 minutes before, is one candidate, but the main subscription is the owner's live one and any of its sessions with the same start of prompt could have written it minutes earlier. So this shows a cache is shared across conversations of an account; it does not show how long a cache lives. | The cost of a switch was rewritten, and the cold point now takes the cache lifetime from the request ([Routing](#routing)). Whether a cache follows a conversation across accounts was not observable: the spare was at its weekly limit, so its own requests were refused and wrote nothing to compare. |
| V10 | **Holds for the names** | See the list below. An ordinary answer carried the weekly headers and not the five-hour ones. | A reading is merged into what is known of a subscription, not put in its place. The values were not recorded, so their forms are unverified; the reader accepts one form and treats anything else as unknown. |
| V11 | **Holds at launch** | A self-signed leaf certificate that cannot sign others, valid for loopback only, was accepted when named in the launch environment and in a settings value. Untrusted, the connection was opened, the handshake failed, and no request and no login were sent. | TLS on loopback is the default defence for the port ([Security review](#security-review-of-the-local-listener)), with a fallback when the trust entry does not take. A session that gains the entry while running was not tested. |
| V12 | **No bound seen on the client** | The client waited four minutes on a silent stream. | The front's silence bound is off unless set (R24). How often the provider sends something is still unknown. |
| V13 | **Holds as far as tested** | No request header names an account. Three headers carry an id that is neither account's: the session's, the prompt's and the single request's. The account is named only inside `metadata.user_id`, beside a device id. Routed requests that changed nothing else were accepted. | The one-value rewrite is enough. This rests on four requests. |
| V14 | **Partly** | A served 429 with limit headers is shown as the session's own limit; a new session afterwards worked, so nothing was stored across sessions. "Almost full" headers changed nothing visible. | Passing the serving subscription's headers through is right for a 429. Inside one long interactive session: not tested. |
| V15 | **Holds for a change, false for a removal** | A running session of two turns followed a change of the base URL in its `settings.json` on its second turn. In a second test the key was removed between the turns: the second turn still went to the old address. (Made-up token, a directory made for the test, the stand-in only.) | The pointer check and `router off` rewrite the pointer first (the provider's own address, and the headers variable without the client secret) and remove the keys later ([What the person sees](#what-the-person-sees), R6). A third test changed only the headers variable between two turns: the second turn carried the new value. |
| V16 | **Partly** | The only path asked of the base URL is the message path. Everything else the client does goes to the provider's host directly. | The front's path refusals cannot break those calls. The paths of an interactive session (status and usage screens, attaching a file) are not known, including whether any ends in a slash, which the front refuses. |

**The real requests.** One session in the spare's directory, pointed at the harness in a mode that forwards only to the provider's API host over TLS, with a fixed cap on requests and a check before each routed request that it names exactly one account. The spare was at its weekly limit.

| Run | # | Sent as | Status |
|---|---|---|---|
| 1 | 1 | the spare's own login, unchanged | 429 |
| 1 | 2 | the same request, the main subscription's token, account id rewritten | 200 |
| 1 | 3 | next turn, the main subscription again | 200 |
| 1 | 4 | next turn, back on the spare's own login | 429 |
| 2 | 1 | the spare's own login | 429 |
| 2 | 2 | the main subscription, rewritten; client-reported usage: cache write 0, cache read 18,004 | 200 |
| 2 | 3 | next turn, the main subscription; cache write 0, cache read 18,044 | 200 |

No answer carried a header that looked like a warning. The session did not notice the change of subscription: its own subscription was at its limit, and its turns succeeded.

**Limit header names on real answers**, all beginning `anthropic-ratelimit-unified-`: `status`, `reset`, `representative-claim`, `fallback-percentage`, `overage-status`, `overage-disabled-reason`, `7d-status`, `7d-reset`, `7d-utilization`; on a 429 also `5h-status`, `5h-reset`, `5h-utilization`, `7d-surpassed-threshold`, `upgrade-paths`, with `retry-after` and `x-should-retry`.

**Codex** (CLI 0.163.0, two throwaway sessions against the stand-in only; nothing reached a provider):

| # | Result | What was seen |
|---|---|---|
| X1 | **Holds** | With a custom base URL the session sent its ChatGPT login and a `chatgpt-account-id` header to the stand-in. |
| X2 | **Settled** | By default the model request is a WebSocket upgrade, with no fall back to a POST when refused. A custom provider entry with `supports_websockets = false` sends `POST /responses` instead, keeps the sign-in, and carries a client header. The conversation is named in `session-id` and `thread-id` headers. |
| X3 | Partly | Besides the model request the session asks the base URL for `/models`; everything else goes to the provider's host directly. |
| X7 | **Holds for the file store** | The login is a file readable by the same user, with tokens valid for days. The keyring store was not tried. |
| X4 to X6 | Not run | They need a request with a second account's login. The two Codex logins available were the same account signed in twice, so there was nothing to switch between. |

**Where the figures come from.** The statuses and header names are from the harness's record. The two cache-read counts, the "about 25 minutes" between the runs, the feature flags (including the extended-lifetime one) and the client versions are from the clients' own output or from the times the runs were made, not from a record file.

**Codex names the account in a header** (`chatgpt-account-id`), not in the body, so its rewrite is a header swapped with the login and its body is never changed.

**Still unverified:** how long a prompt cache lives; a session that gains the trust entry while running, and the trust entry in a settings file; a session pointed at the provider's own address by the setting; interactive sessions; other Claude Code builds; a job with nobody at the screen reading a login (V8); whether the official tool can refresh an idle login without a model request (V7); whether a cache follows a conversation across accounts (V9); the forms of the limit header values (V10); the paths an interactive session asks for (V16); everything about routed Codex requests (X4 to X6); and what the provider does with routed traffic over time.

## What must be verified

This is the list as it stood before the checks above; results are in [Verification results](#verification-results). No login, account switch or settings change was made to write it.

| # | Assumption | If false |
|---|---|---|
| V1 | Claude Code sends its own subscription login to a custom `ANTHROPIC_BASE_URL` when no token variable is set | The pointer must also set a token, as sessionpipe does; the front then needs the home login to go direct, which weakens rule 2. This is the most important one. |
| V2 | Claude Code adds headers named in a settings variable to every request it sends to the base URL | The client secret has no safe carrier; the front then treats every caller as one client (no per-client order), and a path secret is the worse fallback. |
| V3 | With the pointer's settings, a session has the same context window, models, features and usage display as against the provider's own address. sessionpipe's pool needs an extra, undocumented-looking setting for this, so the plain assumption is probably false without it. | Any difference that remains is a "worse off than without the router" case while the router is healthy, which breaks the first rule. Mode `own` does not ship until the difference is zero or the owner accepts a named one. |
| V4 | The conversation id is readable from a header or from `metadata.user_id` | Stickiness falls back to per client. |
| V5 | The provider accepts a routed request whose `metadata.user_id` was rewritten to the serving account, as sessionpipe's pool sends it | The router stops and the owner decides; an unrewritten request is not tried instead, since it is the less consistent of the two. |
| V6 | What Claude Code does when a stream is cut, and when a connection is refused (retries, how many, how long) | R4 and the Windows restart gap are worse than stated. |
| V7 | An invocation of the official tool refreshes a directory's login without a model request | Idle-subscription option 1 falls; see credentials. |
| V8 | Where the login is stored on Linux and Windows today; and that a background job with nobody at the screen can read the macOS keychain item Claude Code wrote, without a prompt (the item's access list may name only the program that wrote it, and a prompt cannot be answered at the login window) | Phase 6's readers change. If a prompt is needed, reading from the official store works only while someone is logged in at the screen, which pushes the idle-subscription choice towards option 2. |
| V9 | Prompt caches are not shared between accounts; the cache lifetime in use | The cold-point default changes; the rule stays. |
| V10 | The rate-limit header names and the 429 shape for each window | Room falls back to the usage read until corrected. |
| V11 | Claude Code can be told, from settings, to trust one extra leaf certificate for a loopback `https` base URL. Probably false from settings (see [The loopback certificate](#the-loopback-certificate)); also check it from the launch environment. | Expected. The port-takeover defence is the pointer check, with a residual window (R25), and the pointer uses plain `http` on loopback. |
| V12 | The provider sends something on an open stream at least every two minutes | The idle bound in R24 is raised or dropped. |
| V13 | Nothing else in a request ties it to the home account: no account, organisation or session-bound header beyond what V5 covers, and the provider does not bind a token to the client's context | A swapped request may be rejected or flagged. That is a provider safeguard acting on the person's accounts, not a routing failure: the router stops and the owner decides. |
| V14 | Claude Code does not act on the served subscription's rate-limit headers as if they were its own login's (warnings, slowing itself, a stored "limit reached") | A session could behave worse with the router than without. The front would then pass through the home subscription's last known values instead, or none. |
| V16 | Which paths Claude Code calls on the base URL besides the message paths, and which of them the front's refusals would block | A refusal that blocks a call the session makes for itself is narrowed to the paths that mint a credential. Until this is checked, the first slice's broader refusal (anything under the sign-in prefix) must not be pointed at by a real session. |
| V15 | Whether Claude Code re-reads the `env` block of `settings.json` during a session | If it does, the pointer check also repairs running sessions (R6). If not, the gap stays as stated. Either answer is acceptable; it sizes the gap. |

**How each is checked, and who must be there.** The stand-in upstream from phase 2 records, for each request, only header names, the kind of credential, the length of its prefix and its length: never a value.

| Check | How | Owner present |
|---|---|---|
| V2, V4 | Read from a stand-in session that has no login at all (a made-up token variable for that one run) | No |
| V1, V3, V6, V11, V12 (client side), V14, V15, V16 | One throwaway session with a real login, pointed at the stand-in on loopback. The login reaches our stand-in in a header, so this touches a credential. | Yes |
| V5, V9, V10, V12 (provider side), V13 | One real request to the provider on a spare subscription, sent through the front by hand | Yes |
| V7, V8 | Run by the owner in a terminal, and once from a background job | Yes |

Everything can be prepared beforehand. Only the first row runs without the owner.

## Questions that were put to the owner

Answered on 2026-11-24; see [Decisions](#decisions). Kept as asked, for the record. Question 2 is still open.


1. **Reading logins.** May the router read each opted-in subscription's access token from the official tool's own store, read-only, kept in memory only? This is the first time looptender would read a credential.
2. **Idle subscriptions.** If no free refresh by the official tool exists (V7): a long-lived token held by looptender in the system's secret store, or a spare subscription that sometimes needs a person to wake it?
3. **Plan terms.** Routing several subscriptions through one router is yours to decide per account. The design keeps the router off until turned on and each subscription out until added. Is anything more wanted, for example a line in `router on` that says so?
4. **AGENTS.md.** The engineering rules say looptender "never copies credentials". The router copies none between stores, but it reads them and attaches one subscription's token to another session's request, which is a new kind of handling. Proposed sentence, to land with phase 6: "The router reads a subscription's access token read-only from the official tool's own store, keeps it in memory only, and attaches it only to allow-listed provider paths; it never refreshes, stores or copies a credential." Agreed?
5. **The verification session** (the table under "What must be verified"): when, and on which subscription? It comes right after phase 2 and before anything else is built.
6. **Defaults.** Soft 90%, hard 98%, automatic switch-back at cold points, hysteresis at 85%, the decision bound as described. Any of these different?
7. **Scope of the pointer.** Only BB's directory at first, or every Claude Code directory in the catalog?
8. **The front's last act (R13).** Should the front, when it finds its own binary gone, remove the pointer itself before it stops? It is the only file write the front would ever do.
9. **Another program taking the port.** The lab showed sessionpipe's pool sending its bearer to whatever held the old port; here it would be the session's real login. The loopback certificate would close it but probably cannot be set from settings (V11). Proposed: ship with the fixed port, the held socket and the pointer check, state the window, and add the certificate wherever verification shows it works. Agreed?
10. **Windows.** Proposed: after the first release, and with a warning against multi-user machines unless the loopback certificate (V11) works there.
11. **`router_switched` notices.** Off by default as proposed, or on?
