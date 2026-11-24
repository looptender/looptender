// Runs the real dock.js in jsdom against the data the panel server sends, with
// every loop state and optional field the server can send put into it, and
// fails on any exception while rendering any page.
//
//   node dock_page_check.mjs <dir>     (dir holds dock.js, index.html and fixture.json)
//
// jsdom comes from DOCK_JSDOM_DIR, the directory of the jsdom package.
import { createRequire } from "node:module";
import { readFileSync } from "node:fs";

const require = createRequire(import.meta.url);
const { JSDOM, VirtualConsole } = require(process.env.DOCK_JSDOM_DIR);
const dir = process.argv[2];
// The page catches a render exception and shows a banner; the test records the
// exception itself so a failure says what threw.
const source = readFileSync(`${dir}/dock.js`, "utf8");
const marker = "} catch (err) {\n      retainedRouteEditor = null;";
if (!source.includes(marker)) throw new Error("dock.js no longer has the render catch this test records the exception from: update the test");
const js = source.replace(marker, "} catch (err) {\n      window.__renderError = err;\n      retainedRouteEditor = null;");
const html = readFileSync(`${dir}/index.html`, "utf8");
const fixture = JSON.parse(readFileSync(`${dir}/fixture.json`, "utf8"));

const clone = (x) => JSON.parse(JSON.stringify(x));

// The loop fields the variants below set or delete. Each must be listed in
// dock_page_variant_keys.json, which a Go test checks against dockLoop's JSON
// tags, so a renamed field cannot leave a variant testing nothing.
const listed = new Set(JSON.parse(readFileSync(new URL("./dock_page_variant_keys.json", import.meta.url), "utf8")));
const unlisted = new Set();
function paths(prefix, v, out) {
  out.push(prefix);
  const items = Array.isArray(v) ? v : [v];
  for (const it of items) if (it && typeof it === "object") for (const [k, x] of Object.entries(it)) paths(`${prefix}.${k}`, x, out);
}
function track(l) {
  return new Proxy(l, {
    set(o, k, v) { const p = []; paths(String(k), v, p); for (const x of p) if (!listed.has(x)) unlisted.add(x); o[k] = v; return true; },
    deleteProperty(o, k) { if (!listed.has(String(k))) unlisted.add(String(k)); delete o[k]; return true; },
  });
}
const failures = [];
const long = "A note that goes on. ".repeat(60);

// Variants of one loop: each is the loop as the server can send it.
const STATES = ["supervising", "waiting_for_you", "waiting_on_limit", "waiting_on_network", "checks_failing", "not_checked_recently", "outside_hours", "paused", "stopped", "finished", "off", "unknown"];
const variants = [];
for (const s of STATES) variants.push([`state ${s}`, (l) => { l.state = s; l.phrase = s; }]);
variants.push(["failed lane", (l) => { l.lanesFailed = "2 lanes failed"; }]);
variants.push(["finished with a failed lane and a provider and model", (l) => { l.state = "finished"; l.phrase = "Finished"; l.lanesFailed = "2 lanes failed"; l.provider = "claude-code"; l.model = "example-model"; }]);
variants.push(["finished with a provider and no model", (l) => { l.state = "finished"; l.phrase = "Finished"; l.provider = "codex"; delete l.model; }]);
variants.push(["failed lane with a provider", (l) => { l.lanesFailed = "1 lane failed"; l.provider = "codex"; l.model = "m"; }]);
variants.push(["no provider, no model", (l) => { delete l.provider; delete l.model; }]);
variants.push(["provider without model", (l) => { l.provider = "claude-code"; delete l.model; }]);
variants.push(["provider and model", (l) => { l.provider = "claude-code"; l.model = "example-model"; }]);
variants.push(["moved and held", (l) => { l.state = "waiting_for_you"; l.phrase = "Moved: resume to start"; l.moved = { held: true, text: "Moved to its new thread. Resuming it is the one action needed." }; }]);
variants.push(["moved, not held", (l) => { l.moved = { held: false, text: "Moved to its new thread; its first check there has not run yet." }; }]);
variants.push(["unsupervised", (l) => { l.state = "not_checked_recently"; l.phrase = "Not checked recently"; }]);
variants.push(["limit wait, not passed", (l) => { l.limitWait = { passed: false, resetsAt: "2030-01-05T12:00:00Z", text: [{ t: "Provider limit resets in 5 days" }] }; }]);
variants.push(["limit wait, passed", (l) => { l.limitWait = { passed: true, resetsAt: "2030-01-01T12:00:00Z", text: [{ t: "Provider limit reset" }] }; }]);
variants.push(["no limit wait", (l) => { delete l.limitWait; }]);
variants.push(["next check unknown with detail", (l) => { l.nextCheck = { availability: "unknown", detail: "the last check ran 9 min ago" }; }]);
variants.push(["next check known", (l) => { l.nextCheck = { availability: "known", text: [{ t: "in 5 minutes" }] }; }]);
variants.push(["next check in progress", (l) => { l.nextCheck = { availability: "in_progress", phrase: "A check is running" }; }]);
variants.push(["checked by ticks, paused", (l) => { l.state = "paused"; l.phrase = "Paused"; l.checkedBy = { kind: "ticks", text: "Checked by ticks from a scheduler that is not recorded." }; }]);
variants.push(["checked by nothing", (l) => { l.checkedBy = { kind: "none", text: "Nothing is on record to check it." }; }]);
variants.push(["fresh: updated", (l) => { l.buildFreshness = { state: "updated", text: "Updated 2 h ago", detail: "Installed 22 Nov 10:00 \u00B7 was v0.1.0 \u00B7 now v0.2.0" }; }]);
variants.push(["fresh: pending", (l) => { l.buildFreshness = { state: "pending", text: "Update pending \u00B7 switches at next check" }; }]);
variants.push(["fresh: older, paused", (l) => { l.state = "paused"; l.phrase = "Paused"; l.buildFreshness = { state: "older", text: "Older build \u00B7 restart the supervisor to update", detail: "Runs v0.1.0.", action: "Run this, then start supervision again the way it was started.", command: "looptender daemon-shutdown -c CONFIG" }; }]);
variants.push(["fresh: different, no command", (l) => { l.buildFreshness = { state: "different", text: "Different build from the installed one", detail: "Runs v0.1.0; installed v0.2.0.", action: "Stop its process, then start supervision again." }; }]);
variants.push(["fresh: none", (l) => { delete l.buildFreshness; }]);
variants.push(["long detail", (l) => { l.detail = long; }]);

function scenario(mutateData) {
  const d = clone(fixture.dock);
  mutateData(d);
  return d;
}
const scenarios = [["the data as served", (d) => d]];
// All variants at once, as separate loops of one report.
scenarios.push(["every variant at once", (d) => {
  const base = d.loops[0];
  variants.forEach(([name, fn], i) => {
    const l = clone(base);
    l.key = "l" + (0xa00000000000 + i).toString(16);
    l.name = `variant-${i}`;
    fn(track(l));
    d.loops.push(l);
  });
}]);
// Each variant on every real loop, one report each.
for (const [name, fn] of variants) {
  scenarios.push([`${name} on every loop`, (d) => { for (const l of d.loops) fn(track(l)); }]);
}
// Long and awkward need notes.
scenarios.push(["long and awkward notes", (d) => {
  for (const n of d.needsYou || []) { n.note = long + " docs/reference/FILE.md http://localhost:8080/x " + "x".repeat(400); n.noteMasked = true; }
}]);
scenarios.push(["no needs", (d) => { d.needsYou = []; d.needsLine = "Nothing needs you"; d.allClear = true; }]);

const routes = ["#/", "#/versions", "#/usage", "#/routes", "#/accounts"];

async function render(name, data, checks = []) {
  const errors = [];
  const vc = new VirtualConsole();
  vc.on("jsdomError", (e) => errors.push(String(e.detail && e.detail.stack || e.message || e)));
  vc.on("error", (e) => errors.push(String(e)));
  const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true, url: "http://127.0.0.1:1/s/token/", virtualConsole: vc });
  const w = dom.window;
  const body = { "api/v1/dock": data, "api/v1/server": fixture.server, "api/v1/usage": fixture.usage };
  w.fetch = async (url) => {
    const path = String(url).split("?")[0].replace(/^.*\/s\/[^/]+\//, "");
    const found = body[path];
    return { ok: found !== undefined, status: found !== undefined ? 200 : 404, json: async () => clone(found), text: async () => JSON.stringify(found) };
  };
  w.TextEncoder = TextEncoder;
  w.scrollTo = () => {};
  w.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  w.Element.prototype.scrollIntoView = () => {};
  w.addEventListener("error", (e) => errors.push(String(e.error && e.error.stack || e.message)));
  w.addEventListener("unhandledrejection", (e) => errors.push(String(e.reason && e.reason.stack || e.reason)));
  w.eval(js);
  const tick = () => new Promise((r) => setTimeout(r, 15));
  await tick(); await tick();
  const keys = (data.loops || []).map((l) => l.key);
  for (const hash of [...routes, ...keys.map((k) => "#/loop/" + k)]) {
    w.__renderError = null;
    w.location.hash = hash;
    w.dispatchEvent(new w.Event("hashchange"));
    await tick();
    const pane = w.document.getElementById("pane");
    if (w.__renderError) errors.push(`${hash}: ${String(w.__renderError.stack || w.__renderError).split("\n").slice(0, 2).join(" | ")}`);
    else if (pane && /could not show the reply/.test(pane.textContent)) errors.push(`${hash}: the pane shows the render failure`);
    else if (pane && pane.children.length === 0) errors.push(`${hash}: the pane is empty`);
    for (const c of checks) {
      if (c.hash !== hash && !(c.hash === "loop" && hash.startsWith("#/loop/"))) continue;
      const bad = c.fn(w, hash);
      if (bad) errors.push(`${hash}: ${bad}`);
    }
  }
  w.close();
  for (const e of new Set(errors)) failures.push(`${name}: ${e.split("\n").slice(0, 3).join(" | ")}`);
}

for (const [name, fn] of scenarios) await render(name, scenario(fn));

// What the page shows, not only that it renders.
const text = (w, sel) => [...w.document.querySelectorAll(sel)].map((n) => n.textContent).join(" | ");
const outsideDetails = (w, re) => [...w.document.querySelectorAll("#pane p, #pane div")].some((n) => re.test(n.textContent) && !n.closest("details"));
const target = (d, fn) => { const l = d.loops[0]; fn(track(l)); return l; };
const expectations = [
  ["a paused loop's long limit wait, with the deadline and hold notes, is shown outside the folds", (d) => target(d, (l) => {
    l.state = "paused"; l.phrase = "Paused";
    l.limitWait = { passed: false, text: [{ t: "Provider limit resets in 5 days" }], note: [{ t: "The reset falls after this loop's deadline: the loop will be held by the deadline on its first check after it, before this wait can end. The loop is held (paused), so it observes and sends nothing." }] };
  }), [{ hash: "loop", fn: (w, h) => h.endsWith(target(clone(fixture.dock), () => {}).key) ? (outsideDetails(w, /reset falls after this loop's deadline/) ? null : "the deadline note is not shown outside the folds") : null }]],
  ["a wake-policy warning is shown", (d) => target(d, (l) => { l.wakePolicy = [{ t: "Its prompt file is hand-edited, so it is sent exactly as written. `looptender prompt-policy show` says more." }]; }),
    [{ hash: "loop", fn: (w, h) => h.endsWith(fixture.dock.loops[0].key) ? (/hand-edited/.test(text(w, "#pane .wake-policy")) ? null : "the wake-policy warning is not shown") : null }]],
  ["the provider header says the model is not configured", (d) => target(d, (l) => { l.provider = "claude-code"; delete l.model; }),
    [{ hash: "loop", fn: (w, h) => h.endsWith(fixture.dock.loops[0].key) ? (/Configured provider: claude-code · model not configured/.test(text(w, "#pane .build-line")) ? null : "the provider header is not the agreed wording") : null }]],
  ["a wait that is in effect is not tagged Reached", (d) => target(d, (l) => { l.limitWait = { passed: false, text: [{ t: "Provider limit resets in 5 days" }] }; l.limits = [{ label: "Time without Git progress", text: [{ t: "26 h of 24 h" }], reached: true }]; l.noProgress = { availability: "clear", text: "Progress: Clear", lines: [] }; l.hold = { state: "held", phrase: "Held" }; }),
    [{ hash: "loop", fn: (w, h) => { if (!h.endsWith(fixture.dock.loops[0].key)) return null; const t = text(w, "#pane"); return /In effect/.test(t) && /no-progress limit is reached/.test(t) ? null : "the wait tag or the progress note is wrong: " + t.slice(0, 200); } }]],
  ["a paused loop checked by ticks says how it is checked on its page and in the tree", (d) => target(d, (l) => { l.state = "paused"; l.phrase = "Paused"; l.checkedBy = { kind: "ticks", text: "Checked by ticks from a scheduler not on record." }; }),
    [{ hash: "loop", fn: (w, h) => h.endsWith(fixture.dock.loops[0].key) ? (/Checked by ticks/.test(text(w, "#pane .checked-by")) ? null : "the loop page does not say how the loop is checked") : null },
     { hash: "#/", fn: (w) => (/Checked by ticks/.test(text(w, "#pane .work-tree")) ? null : "the Overview tree does not say how the loop is checked") }]],
  ["a paused loop that still waits for a person has the needs-you pill on its page and in the picker", (d) => target(d, (l) => { l.state = "paused"; l.phrase = "Paused \u00B7 also waiting for you"; l.alsoWaiting = "A question is open."; }),
    [{ hash: "loop", fn: (w, h) => h.endsWith(fixture.dock.loops[0].key) ? (w.document.querySelector("#pane .state.tone-attn") ? null : "the loop page's pill is not the needs-you tone") : null },
     { hash: "#/", fn: (w) => (w.document.querySelector("#picker .dot-attn") ? null : "the picker row is not the attention dot") }]],
  ["an older daemon's loop shows the state, the detail and the command as text, with no button", (d) => { d.buildFreshness = { text: "Build v0.2.0 installed 3 h ago \u00B7 1 supervisor on it \u00B7 1 older", onIt: 1, notOnIt: 1 }; return target(d, (l) => { l.buildFreshness = { state: "older", text: "Older build \u00B7 restart the supervisor to update", detail: "Runs v0.1.0.", action: "Run this, then start supervision again the way it was started.", command: "looptender daemon-shutdown -c CONFIG" }; }); },
    [{ hash: "loop", fn: (w, h) => h.endsWith(fixture.dock.loops[0].key) ? (/Older build \u00B7 restart the supervisor to update/.test(text(w, "#pane .fresh-line")) && /looptender daemon-shutdown -c CONFIG/.test(text(w, "#pane .fresh-command code")) && /start supervision again/.test(text(w, "#pane .fresh-action")) && !/Restart|Resume|Update now/i.test(text(w, "#pane button")) ? null : "the older build is not worded, or a button offers a restart: " + text(w, "#pane")) : null },
     { hash: "#/", fn: (w) => (/Build v0.2.0 installed 3 h ago/.test(text(w, "#pane .fresh-summary")) && /Older build/.test(text(w, "#pane .work-tree .fresh")) ? null : "the Overview lacks the summary line or the loop's chip") }]],
  ["an up-to-date loop's chip is just the version, and a loop with no state shows no chip", (d) => target(d, (l) => { l.buildFreshness = { state: "current", text: "v0.2.0" }; }),
    [{ hash: "#/", fn: (w) => (text(w, "#pane .work-tree .fresh") === "v0.2.0" ? null : "the chip is not just the version: " + text(w, "#pane .work-tree .fresh")) }]],
  ["no freshness, no chip and no summary", (d) => target(d, (l) => { delete l.buildFreshness; }),
    [{ hash: "#/", fn: (w) => (!w.document.querySelector("#pane .fresh") && !w.document.querySelector("#pane .fresh-summary") ? null : "a chip or summary appeared with nothing known") }]],
  ["a waiting kind or by that is an Object.prototype name says nothing, not a function's source", (d) => {
    for (const [i, name] of ["constructor", "toString", "__proto__", "hasOwnProperty", "valueOf"].entries()) {
      const l = d.loops[i % d.loops.length];
      l.waiting = { kind: i % 2 ? "held" : name, by: i % 2 ? name : "terminal", reason: "r", since: "2030-01-01T00:00:00Z", sinceEvidence: "hold_recorded" };
    }
    return d;
  },
    [{ hash: "#/", fn: (w) => { const t = text(w, "#pane .work-tree"); return /function|\[object|native code|\{\s*\}|undefined|NaN/.test(t) ? "a prototype name printed garbage: " + t.match(/.{0,40}(function|\[object|native code|undefined|NaN).{0,40}/)[0] : null; } }]],
  ["the Overview tree names an unsupervised loop", (d) => target(d, (l) => { l.state = "not_checked_recently"; l.phrase = "Not supervised"; }),
    [{ hash: "#/", fn: (w) => (/Not supervised/.test(text(w, "#pane .work-tree")) ? null : "no 'Not supervised' row in the Overview tree") }]],
];
for (const [name, mutate, checks] of expectations) await render(name, scenario(mutate), checks);

// A failed render's banner ends with the next render that works: the Overview
// is made to throw (as it did on real data), then a page that renders is opened
// without a new read in between.
{
  const errors = [];
  const vc = new VirtualConsole(); vc.on("jsdomError", () => {});
  const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true, url: "http://127.0.0.1:1/s/token/", virtualConsole: vc });
  const w = dom.window;
  w.fetch = async (url) => {
    const path = String(url).split("?")[0].replace(/^.*\/s\/[^/]+\//, "");
    const found = { "api/v1/dock": fixture.dock, "api/v1/server": fixture.server, "api/v1/usage": fixture.usage }[path];
    return { ok: found !== undefined, status: found !== undefined ? 200 : 404, json: async () => clone(found), text: async () => JSON.stringify(found) };
  };
  w.TextEncoder = TextEncoder; w.scrollTo = () => {}; w.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} }); w.Element.prototype.scrollIntoView = () => {};
  const create = w.document.createElement.bind(w.document);
  let breakHome = true;
  w.document.createElement = (tag, ...rest) => {
    if (breakHome && w.location.hash.replace(/^#\/?/, "") === "" && ["details", "li", "dl"].includes(tag)) throw new Error("injected Overview failure");
    return create(tag, ...rest);
  };
  w.eval(js);
  const tick = () => new Promise((r) => setTimeout(r, 15));
  await tick(); await tick();
  const banner = () => w.document.getElementById("banner");
  if (banner().hidden || !/could not show the reply/.test(banner().textContent)) errors.push("the failed Overview did not raise the banner (the test needs it)");
  w.location.hash = "#/versions"; w.dispatchEvent(new w.Event("hashchange")); await tick();
  if (!banner().hidden) errors.push("the failure banner outlived the page that rendered");
  w.close();
  for (const e of errors) failures.push("banner: " + e);
}

// Notifications: the first read is a baseline, a later read adds each new id once,
// a repeat adds nothing, many that share a group are one line, an ended hold is listed,
// and no browser notification is made without the person's click.
{
  const errors = [];
  const vc = new VirtualConsole(); vc.on("jsdomError", () => {});
  const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true, url: "http://127.0.0.1:1/s/token/", virtualConsole: vc });
  const w = dom.window;
  const n = (id, kind, text, extra = {}) => ({ id, kind, severity: "high", loop: "l000000000001", name: "example-api", text, since: "2030-01-01T00:00:00Z", group: kind, groupText: "{n} loops are held for you.", ...extra });
  let notices = [n("a", "needs_you", "example-api is held for you.")];
  let completeRead = true; let fetches = 0;
  w.fetch = async (url) => {
    if (String(url).includes("api/v1/dock")) fetches++;
    const path = String(url).split("?")[0].replace(/^.*\/s\/[^/]+\//, "");
    const d = clone(fixture.dock); d.notices = clone(notices); d.complete = completeRead;
    const found = { "api/v1/dock": d, "api/v1/server": fixture.server, "api/v1/usage": fixture.usage }[path];
    return { ok: found !== undefined, status: found !== undefined ? 200 : 404, json: async () => clone(found), text: async () => JSON.stringify(found) };
  };
  const made = [];
  w.Notification = function (title, o) { made.push([title, o && o.body]); };
  w.Notification.permission = "default";
  w.Notification.requestPermission = async () => { w.Notification.permission = "granted"; return "granted"; };
  w.TextEncoder = TextEncoder; w.scrollTo = () => {}; w.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} }); w.Element.prototype.scrollIntoView = () => {};
  w.eval(js);
  const tick = () => new Promise((r) => setTimeout(r, 25));
  const read = async () => { w.document.getElementById("refresh").click(); await tick(); await tick(); await tick(); await tick(); };
  const area = () => w.document.querySelector("#pane .notices");
  await tick(); await tick();
  if (!area()) errors.push("the Overview has no notifications area");
  else if (/\d new/.test(area().textContent) || w.document.title.includes("new")) errors.push("the first read was not a baseline: " + w.document.title);
  // The person opts in to browser notifications by clicking, then a new event arrives.
  [...w.document.querySelectorAll("#pane .notices button")].find((b) => b.textContent === "Notify me").click(); await tick(); await tick();
  notices = [n("a", "needs_you", "example-api is held for you."), n("b", "limit", "example-docs stopped: usage limit until Fri 04:00.")];
  await read();
  if (made.length !== 1 || made[0][1] !== "example-docs stopped: usage limit until Fri 04:00.") errors.push("one browser notification for the new event expected: " + JSON.stringify(made));
  if (!w.document.title.includes("1 new")) errors.push("the title does not count the unread event: " + w.document.title);
  await read(); await read();
  if (made.length !== 1) errors.push("a repeat read notified again");
  // Five loops on one limit are one notification and five lines.
  notices = notices.concat([1, 2, 3, 4, 5].map((i) => n("g" + i, "limit", "example-" + i + " stopped: usage limit.", { group: "limit.x", groupText: "{n} loops stopped: usage limit." })));
  await read();
  if (made.length !== 2 || made[1][1] !== "5 loops stopped: usage limit.") errors.push("five on one cause should be one notification: " + JSON.stringify(made));
  // A kind that is off is listed and not pushed.
  notices = notices.concat([n("c", "lane_failed", "example-api has a failed lane.")]);
  await read();
  if (made.length !== 2 || !/failed lane/.test(area().textContent)) errors.push("a quiet kind is listed and not pushed: " + "fetches=" + fetches + " " + made.length + " " + (area() && area().textContent.slice(0, 500)));
  // A cut-off read cannot end a hold; a complete one can.
  notices = notices.concat([n("h", "hold", "example-api was held.", { endText: "example-api is no longer held." })]); await read();
  notices = notices.filter((x) => x.id !== "h"); completeRead = false; await read();
  if (/no longer held/.test(area().textContent)) errors.push("a cut-off read ended a hold");
  completeRead = true; await read();
  if (!/no longer held/.test(area().textContent)) errors.push("a complete read did not list the hold lifted");
  if ([...w.document.querySelectorAll("#pane .notices li")].length > 10) errors.push("more than ten listed");
  w.close();
  for (const e of errors) failures.push("notices: " + e);
}

if (unlisted.size) failures.push(`the variants set loop fields that dock_page_variant_keys.json does not list (add them, and they will be checked against dockLoop's JSON tags): ${[...unlisted].join(", ")}`);
if (failures.length) {
  console.error(`dock.js failed while rendering ${failures.length} time(s):`);
  for (const f of failures.slice(0, 30)) console.error("  " + f);
  process.exit(1);
}
console.log(`dock.js rendered ${scenarios.length} scenarios on every page without an exception`);
