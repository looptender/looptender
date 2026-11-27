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
variants.push(["observed never woken", (l) => { l.observed = true; l.observedWakes = false; l.observedNote = "Observed, never woken: looptender reads this session's tier-0 sessionpipe events and never sends to, wakes, re-prompts or stops it."; l.can = ["pause"]; }]);
variants.push(["observed wake_only with held wake", (l) => { l.observed = true; l.observedWakes = true; l.observedNote = "Observed, woken only when idle: looptender wakes it through sessionpipe ask when idle."; l.can = ["pause", "resume"]; l.sessionpipeWake = { since: "2030-01-01T00:00:00Z", forSeconds: 0, held: true, outcome: "refused", code: "folder", until: "2030-01-01T01:00:00Z", text: "last wake refused: folder, next attempt after 2030-01-01T01:00:00Z or the session's next turn; nothing is sent until then" }; }]);
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

async function render(name, data, checks = [], server = fixture.server) {
  const errors = [];
  const vc = new VirtualConsole();
  vc.on("jsdomError", (e) => errors.push(String(e.detail && e.detail.stack || e.message || e)));
  vc.on("error", (e) => errors.push(String(e)));
  const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true, url: "http://127.0.0.1:1/s/token/", virtualConsole: vc });
  const w = dom.window;
  const body = { "api/v1/dock": data, "api/v1/server": server, "api/v1/usage": fixture.usage };
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

// Measurement labels and source text remain readable without becoming HTML.
const originalMeasurement = fixture.usage.loops[0].measurement;
fixture.usage.loops[0].measurement = { details: ["Measured export <img src=x onerror=alert(1)>"], trend: ["example-model: input 100; cached input 60; uncached input 40"], avoided: "Observed avoided redundant actions: 1", estimate: ["Estimated mean 10 tokens per accepted unit; n=2; subscription quota unknown"] };
await render("usage measurement evidence", fixture.dock, [{hash:"#/usage", fn(w) {
  const p = w.document.querySelector("#pane");
  if (!p.textContent.includes("uncached input 40") || !p.textContent.includes("Observed avoided redundant actions: 1") || !p.textContent.includes("subscription quota unknown")) return "measurement evidence missing";
  if (p.querySelector("img[src=x]")) return "measurement source interpreted as markup";
}}]);
fixture.usage.loops[0].measurement = originalMeasurement;

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
  ["an observed never-woken loop renders its plain label, observedNote and checking action labels", (d) => target(d, (l) => {
    l.observed = true; l.observedWakes = false;
    l.observedNote = "Observed, never woken: looptender reads this session's tier-0 sessionpipe events and never sends to, wakes, re-prompts or stops it.";
    l.can = ["pause", "resume"];
  }),
    [{ hash: "#/", fn: (w) => (/Observed, never woken/.test(text(w, "#pane .work-tree .work-observed")) && /never sends to, wakes/.test(text(w, "#pane .work-tree .observed-note")) ? null : "the Overview row lacks the observed label or note") },
     { hash: "loop", fn: (w, h) => {
       if (!h.endsWith(fixture.dock.loops[0].key)) return null;
       const head = text(w, "#pane .pane-head .work-observed");
       const note = text(w, "#pane .observed-note");
       const acts = [...w.document.querySelectorAll("#pane .change-buttons button")].map((b) => b.textContent);
       if (head !== "Observed, never woken") return "loop page heading lacks 'Observed, never woken': " + head;
       if (!/never sends to, wakes/.test(note)) return "loop page lacks observedNote near routes: " + note;
       if (!acts.includes("Pause checking\u2026") || !acts.includes("Resume checking\u2026") || acts.some((a) => /Stop/.test(a))) return "unexpected observed buttons: " + JSON.stringify(acts);
       return null;
     } }], { ...fixture.server, changes: "on", form: "form-value" }],
  ["an observed wake_only loop renders its wake_only label and outstanding wake line", (d) => target(d, (l) => {
    l.observed = true; l.observedWakes = true;
    l.observedNote = "Observed, woken only when idle: looptender wakes it through sessionpipe ask when it is between turns.";
    l.sessionpipeWake = { since: "2030-01-01T00:00:00Z", forSeconds: 0, held: true, outcome: "refused", code: "folder", until: "2030-01-01T01:00:00Z", text: "last wake refused: folder, next attempt after 2030-01-01T01:00:00Z or the session's next turn; nothing is sent until then" };
  }),
    [{ hash: "#/", fn: (w) => (/Observed, woken only when idle/.test(text(w, "#pane .work-tree .work-observed")) && /Wake: last wake refused: folder/.test(text(w, "#pane .work-tree .sessionpipe-wake")) ? null : "the Overview row lacks the wake_only label or wake line") },
     { hash: "loop", fn: (w, h) => h.endsWith(fixture.dock.loops[0].key) ? (/Observed, woken only when idle/.test(text(w, "#pane .pane-head .work-observed")) && /Wake: last wake refused: folder/.test(text(w, "#pane .sessionpipe-wake")) ? null : "loop page lacks wake_only label or wake line") : null }]],
  ["3-day inactive and archived loops fold into a collapsible group with archive and unarchive controls", (d) => {
    const old = d.loops[0];
    old.inactive = true;
    old.can = ["pause", "stop", "archive"];
    const arch = d.loops[1];
    arch.state = "archived";
    arch.phrase = "Archived";
    arch.pickerState = "Archived";
    arch.archived = { at: "2030-01-01T00:00:00Z", hold: "paused" };
    arch.can = ["unarchive"];
    return d;
  },
    [{ hash: "#/", fn: (w) => {
      const older = w.document.querySelector("#pane .work-older");
      if (!older) return "folded older/archived group missing";
      const sum = text(w, "#pane .work-older-summary");
      const btns = [...older.querySelectorAll(".work-quick-actions button")].map((b) => b.textContent);
      return /Inactive 3d\+ \(1\)/.test(sum) && /archived \(1\)/.test(sum) && btns.includes("Archive\u2026") && btns.includes("Unarchive\u2026")
        ? null : "older group missing expected summary or buttons: " + sum + " " + JSON.stringify(btns);
    } },
     { hash: "loop", fn: (w, h) => {
       if (h.endsWith(fixture.dock.loops[0].key)) {
         const acts = [...w.document.querySelectorAll("#pane .change-buttons button")].map((b) => b.textContent);
         return acts.includes("Archive\u2026") && /Auto-folded/.test(text(w, "#pane .work-inactive-note")) ? null : "inactive loop detail missing Archive or note";
       }
       if (h.endsWith(fixture.dock.loops[1].key)) {
         const acts = [...w.document.querySelectorAll("#pane .change-buttons button")].map((b) => b.textContent);
         return JSON.stringify(acts) === JSON.stringify(["Unarchive\u2026"]) ? null : "archived loop detail buttons: " + JSON.stringify(acts);
       }
       return null;
     } }], { ...fixture.server, changes: "on", form: "form-value" }],
  ["unconfigured router renders nothing on the Overview", (d) => { delete d.router; return d; },
    [{ hash: "#/", fn: (w) => (!w.document.querySelector("#pane .router-card") ? null : "router card appeared when report.router was absent") }]],
  ["null router renders nothing on the Overview", (d) => { d.router = null; return d; },
    [{ hash: "#/", fn: (w) => (!w.document.querySelector("#pane .router-card") ? null : "router card appeared when report.router was null") }]],
  ["configured answering router renders status, counts, and service without warning", (d) => {
    d.router = { directory: "private", answering: true, enabled: true, pointedCount: 2, service: "loaded" };
    return d;
  },
    [{ hash: "#/", fn: (w) => {
      const card = w.document.querySelector("#pane .router-card");
      if (!card) return "router card missing when configured";
      if (card.classList.contains("is-attn") || card.querySelector(".router-warning")) return "unexpected warning when router is answering";
      const t = card.textContent;
      return /Account Router/.test(t) && /Answering/.test(t) && /Enabled/.test(t) && /2 pointed/.test(t) && /service loaded/.test(t)
        ? null : "answering router card missing expected fields: " + t;
    } }]],
  ["configured router enabled/pointed while not answering renders advisory warning", (d) => {
    d.router = { directory: "private", answering: false, enabled: true, pointedCount: 1, service: "not_installed" };
    return d;
  },
    [{ hash: "#/", fn: (w) => {
      const card = w.document.querySelector("#pane .router-card");
      if (!card) return "router card missing when configured";
      if (!card.classList.contains("is-attn")) return "missing is-attn class on not-answering router card";
      const warn = text(w, "#pane .router-card .router-warning");
      const t = card.textContent;
      return /Not answering/.test(t) && /Enabled/.test(t) && /1 pointed/.test(t) && /service not installed/.test(t) && /socket is not answering/.test(warn)
        ? null : "not-answering router card missing expected fields or warning: " + t;
    } }]],
];
for (const [name, mutate, checks, server] of expectations) await render(name, scenario(mutate), checks, server || fixture.server);

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
// a repeat adds nothing, many that share a group are one line with the group's real
// total, an ended hold is listed, no browser notification is made without the person's
// click, the choices are the server's shared ones (they survive a reload and are saved
// through the notify route), a menu that is already notifying keeps the browser quiet,
// an opened list stays open, and a click on a notification focuses the loop.
async function notifyScenario(label, setup) {
  const errors = [];
  const vc = new VirtualConsole(); vc.on("jsdomError", () => {});
  const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true, url: "http://127.0.0.1:1/s/token/", virtualConsole: vc });
  const w = dom.window;
  const n = (id, kind, text, extra = {}) => ({ id, kind, severity: "high", loop: "l000000000001", name: "example-api", text, since: "2030-01-01T00:00:00Z", group: kind, groupText: "{n} loops are held for you.", ...extra });
  const env = { notices: [n("a", "needs_you", "example-api is held for you.")], complete: true, shared: { kinds: { needs_you: true, limit: true, supervisor_gone: true, lane_failed: false, hold: false, install: false, disk: false, bound: false }, browser: false, menu: { notifying: false } }, saved: [], made: [] };
  w.fetch = async (url, opts) => {
    const path = String(url).split("?")[0].replace(/^.*\/s\/[^/]+\//, "");
    if (opts && opts.method === "POST" && path === "api/v1/notify") {
      const b = JSON.parse(opts.body); env.saved.push(b);
      if (b.browser !== undefined) env.shared.browser = b.browser; else env.shared.kinds[b.kind] = b.on;
      return { ok: true, status: 200, json: async () => clone(env.shared), text: async () => "" };
    }
    const d = clone(fixture.dock); d.notices = clone(env.notices); d.complete = env.complete; d.notify = clone(env.shared);
    const server = { ...clone(fixture.server), notifyForm: "form-value" };
    const found = { "api/v1/dock": d, "api/v1/server": server, "api/v1/usage": fixture.usage }[path];
    return { ok: found !== undefined, status: found !== undefined ? 200 : 404, json: async () => clone(found), text: async () => JSON.stringify(found) };
  };
  w.Notification = function (title, o) { this.title = title; this.body = o && o.body; this.close = () => {}; env.made.push(this); };
  w.Notification.permission = "default";
  w.Notification.requestPermission = async () => { w.Notification.permission = "granted"; return "granted"; };
  w.TextEncoder = TextEncoder; w.scrollTo = () => {}; w.focus = () => {}; w.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} }); w.Element.prototype.scrollIntoView = () => {};
  if (setup) setup(env, w);
  w.eval(js);
  const tick = () => new Promise((r) => setTimeout(r, 25));
  const settle = async () => { await tick(); await tick(); await tick(); await tick(); };
  const read = async () => { w.document.getElementById("refresh").click(); await settle(); };
  const area = () => w.document.querySelector("#pane .notices");
  const button = (text) => [...w.document.querySelectorAll("#pane .notices button")].find((b) => b.textContent === text);
  await settle();
  return { w, env, n, read, area, button, settle, errors, fail: (m) => errors.push(`${label}: ${m}`) };
}

{
  const t = await notifyScenario("notices");
  const { w, env, n, read, area, button, settle } = t;
  if (!area()) t.fail("the Overview has no notifications area");
  else if (/\d new/.test(area().textContent) || w.document.title.includes("new")) t.fail("the first read was not a baseline: " + w.document.title);
  button("Notify me").click(); await settle();
  if (JSON.stringify(env.saved) !== '[{"browser":true}]' || !env.shared.browser) t.fail("the browser choice was not saved through the server: " + JSON.stringify(env.saved));
  env.notices = [n("a", "needs_you", "example-api is held for you."), n("b", "limit", "example-docs stopped: usage limit until Fri 04:00.", { loop: "l000000000002" })];
  await read();
  if (env.made.length !== 1 || env.made[0].body !== "example-docs stopped: usage limit until Fri 04:00.") t.fail("one browser notification for the new event expected: " + JSON.stringify(env.made.map((x) => x.body)));
  if (!w.document.title.includes("1 new")) t.fail("the title does not count the unread event: " + w.document.title);
  // A click on it focuses the page on that loop.
  env.made[0].onclick(); if (w.location.hash !== "#/loop/l000000000002") t.fail("a click did not focus the loop: " + w.location.hash);
  w.location.hash = "#/"; w.dispatchEvent(new w.Event("hashchange")); await settle();
  await read(); await read();
  if (env.made.length !== 1) t.fail("a repeat read notified again");
  // One loop joins four already on one cause: the group is told by its real total.
  const g = (i) => n("g" + i, "limit", "example-" + i + " stopped.", { group: "limit.x", groupText: "{n} loops stopped: usage limit." });
  env.notices = env.notices.concat([1, 2, 3, 4].map(g)); await read();
  env.notices = env.notices.concat([g(5)]); await read();
  if (env.made.length !== 3 || env.made[1].body !== "4 loops stopped: usage limit." || env.made[2].body !== "5 loops stopped: usage limit.") t.fail("a group's count should be its real total: " + JSON.stringify(env.made.map((x) => x.body)));
  env.notices = env.notices.concat([n("c", "lane_failed", "example-api has a failed lane.")]); await read();
  if (env.made.length !== 3 || !/failed lane/.test(area().textContent)) t.fail("a quiet kind is listed and not pushed");
  env.notices = env.notices.concat([n("h", "hold", "example-api was held.", { endText: "example-api is no longer held." })]); await read();
  env.notices = env.notices.filter((x) => x.id !== "h"); env.complete = false; await read();
  if (/no longer held/.test(area().textContent)) t.fail("a cut-off read ended a hold");
  env.complete = true; await read();
  if (!/no longer held/.test(area().textContent)) t.fail("a complete read did not list the hold lifted");
  if ([...w.document.querySelectorAll("#pane .notices li")].length > 10) t.fail("more than ten listed");
  // A list the person opened stays open through the next read; it is not closed for them.
  area().open = true; area().dispatchEvent(new w.Event("toggle")); await read();
  if (!area().open) t.fail("a read closed the list the person had opened");
  area().open = false; area().dispatchEvent(new w.Event("toggle")); await read();
  if (area().open) t.fail("a read reopened a list the person closed with nothing new");
  // A choice is saved to the server, and a reload (a new page on the same server) shows it.
  const box = [...area().querySelectorAll("input[type=checkbox]")].find((c) => /disk/i.test(c.parentNode.textContent));
  box.checked = true; box.dispatchEvent(new w.Event("change")); await settle();
  if (!env.saved.some((x) => x.kind === "disk" && x.on === true)) t.fail("the choice was not saved: " + JSON.stringify(env.saved));
  // The host kind (this computer's session or load) is offered and on by default; it can be turned off.
  const hostBox = [...area().querySelectorAll("input[type=checkbox]")].find((c) => /session or load/.test(c.parentNode.textContent));
  if (!hostBox) t.fail("the host kind is not offered in the settings");
  else if (!hostBox.checked) t.fail("the host kind is not on by default");
  else if (![...area().querySelectorAll("input[type=checkbox]")].some((c) => /another model/.test(c.parentNode.textContent))) t.fail("the model step kind is not offered in the settings");
  else { hostBox.checked = false; hostBox.dispatchEvent(new w.Event("change")); await settle(); if (!env.saved.some((x) => x.kind === "host" && x.on === false)) t.fail("the host choice was not saved"); }
  w.close();
  for (const e of t.errors) failures.push("notices: " + e);
  // The reload: a new page against the same shared state remembers the choice and the browser opt-in.
  const again = await notifyScenario("reload", (e, win) => { e.shared = clone(env.shared); win.Notification.permission = "granted"; });
  const diskBox = [...again.area().querySelectorAll("input[type=checkbox]")].find((c) => /disk/i.test(c.parentNode.textContent));
  if (!diskBox || !diskBox.checked) again.fail("a reload lost the choice");
  if (!/Browser notifications are on/.test(again.area().textContent)) again.fail("a reload lost the browser opt-in");
  again.w.close();
  for (const e of again.errors) failures.push("notices: " + e);
}

{
  // The menu on this computer is notifying: the browser stays quiet and says why.
  const t = await notifyScenario("menu notifying", (e) => { e.shared.browser = true; e.shared.menu = { notifying: true }; });
  const { w, env, n, read, area } = t;
  w.Notification.permission = "granted";
  await read();
  env.notices = env.notices.concat([n("b", "limit", "example-docs stopped: usage limit.")]); await read();
  if (env.made.length !== 0) t.fail("the browser notified while the menu does: " + JSON.stringify(env.made.map((x) => x.body)));
  if (!/menu on this computer is already notifying/.test(area().textContent)) t.fail("the page does not say the menu is notifying");
  if (!/usage limit/.test(area().textContent)) t.fail("the list stopped filling");
  w.close();
  for (const e of t.errors) failures.push("notices: " + e);
}

{
  // Quick actions inside folded .work-older on #/ prepare, render review UI inline, and confirm cleanly.
  const vc = new VirtualConsole(); vc.on("jsdomError", () => {});
  const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true, url: "http://127.0.0.1:1/s/token/", virtualConsole: vc });
  const w = dom.window;
  const d = clone(fixture.dock);
  const old = d.loops[0];
  old.inactive = true;
  old.can = ["pause", "stop", "archive"];
  const key = old.key;
  const formHex = "a".repeat(64);
  w.fetch = async (url, opts) => {
    const path = String(url).split("?")[0].replace(/^.*\/s\/[^/]+\//, "");
    if (opts && opts.method === "POST" && path === `api/v1/loops/${key}/prepare`) {
      return { ok: true, status: 200, json: async () => ({ schemaVersion: 1, preview: { action: "archive", key, title: `Archive ${old.name}?`, lines: [[{ t: "Moves this loop into the archived list without deleting files." }]], confirm: `Archive ${old.name}`, form: formHex, expiresAt: "2030-01-01T12:02:00Z", lost: "Lost.", refused: "Refused." } }), text: async () => "" };
    }
    if (opts && opts.method === "POST" && path === `api/v1/loops/${key}/confirm`) {
      return { ok: true, status: 200, json: async () => ({ schemaVersion: 1, result: { outcome: "done", text: "Archived: moved into the archived list without deleting files." } }), text: async () => "" };
    }
    const server = { ...clone(fixture.server), changes: "on", form: "form-value" };
    const found = { "api/v1/dock": d, "api/v1/server": server, "api/v1/usage": fixture.usage }[path];
    return { ok: found !== undefined, status: found !== undefined ? 200 : 404, json: async () => clone(found), text: async () => JSON.stringify(found) };
  };
  w.TextEncoder = TextEncoder; w.scrollTo = () => {}; w.focus = () => {}; w.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} }); w.Element.prototype.scrollIntoView = () => {};
  w.eval(js);
  const tick = () => new Promise((r) => setTimeout(r, 25));
  const settle = async () => { await tick(); await tick(); await tick(); await tick(); };
  await settle();
  const quickBtn = w.document.querySelector(`#pane .work-older [data-loop="${key}"] .work-quick-actions [data-focus="act-archive"]`);
  if (!quickBtn) failures.push("folded quick action button missing on #/");
  else {
    quickBtn.click();
    await settle();
    const review = w.document.querySelector(`#pane .work-older [data-loop="${key}"] .change.is-review`);
    const cancel = review && review.querySelector('[data-focus="change-cancel"]');
    const confirm = review && review.querySelector('[data-focus="change-confirm"]');
    if (!review || !cancel || !confirm) failures.push("clicking Archive in folded row on #/ did not render inline review box with Cancel/Confirm buttons");
    else {
      confirm.click();
      await settle();
      const res = w.document.querySelector(`#pane .work-older [data-loop="${key}"] [data-focus="change-result"]`);
      if (!res || !/Archived: moved into the archived list/.test(res.textContent)) failures.push("confirming inline Archive on #/ did not render result: " + (res && res.textContent));
    }
  }
  w.close();
}

// Account catalog removal: hidden unless --allow-changes sets accountRemove; when enabled,
// Remove opens a reviewed confirmation mentioning accounts.json and untouched disk logins,
// and confirming removes the card on reload and keeps a confirmation notice visible.
{
  const errors = [];
  const vc = new VirtualConsole(); vc.on("jsdomError", () => {});
  const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true, url: "http://127.0.0.1:1/s/token/", virtualConsole: vc });
  const w = dom.window;
  const acctA = { id: "0123456789abcdef0123456789abcdef", label: "Example primary", provider: "claude-code", selection: "inherited", auth: { text: "Selected CLI reports login", state: "reported_present", freshness: "fresh" }, plan: { text: "Max", state: "reported", freshness: "fresh" }, routes: "none", configuredModel: { missing: "not_recorded" }, observedModel: { missing: "not_recorded" } };
  const acctB = { id: "fedcba9876543210fedcba9876543210", label: "Example extra", provider: "claude-code", selection: "explicit_directory", auth: { text: "No login reported", state: "reported_absent", freshness: "fresh" }, plan: { text: "Unavailable", state: "unavailable", freshness: "fresh" }, routes: "none", configuredModel: { missing: "not_recorded" }, observedModel: { missing: "not_recorded" } };
  let cards = [clone(acctA), clone(acctB)];
  let ref = "ref-initial";
  let allowRemove = false;
  const calls = [];
  w.fetch = async (url, opts) => {
    const path = String(url).split("?")[0].replace(/^.*\/s\/[^/]+\//, "");
    if (opts && opts.method === "POST" && path === `api/v1/account-remove/${acctB.id}/prepare`) {
      calls.push("prepare");
      return { ok: true, status: 200, json: async () => ({
        schemaVersion: 1,
        preview: {
          action: "remove_account",
          key: acctB.id,
          title: `Remove ${acctB.label} from catalog?`,
          confirm: "Remove from catalog",
          form: "b".repeat(64),
          expiresAt: "2035-01-01T00:00:00Z",
          lines: [
            [{ t: `Removes only this entry from accounts.json: Claude Code \u00B7 ${acctB.label}.` }],
            [{ t: "Provider credentials and files on disk are untouched." }],
            [{ t: "Warning: registered loop routes still reference this account (loop example-loop, route spare)." }]
          ],
          lost: "lost",
          refused: "refused"
        }
      }) };
    }
    if (opts && opts.method === "POST" && path === `api/v1/account-remove/${acctB.id}/confirm`) {
      calls.push("confirm");
      cards = [clone(acctA)];
      ref = "ref-after-remove";
      return { ok: true, status: 200, json: async () => ({
        schemaVersion: 1,
        result: { outcome: "done", text: `Removed ${acctB.label} from the account catalog. Provider logins and files on disk were left untouched.` }
      }) };
    }
    if (path.startsWith("api/v1/saved-accounts")) {
      const payload = { schemaVersion: 1, reference: ref, accounts: { state: "available", complete: true, cards: clone(cards) } };
      return { ok: true, status: 200, json: async () => payload, text: async () => JSON.stringify(payload) };
    }
    const baseDock = clone(fixture.dock);
    baseDock.accountsReference = ref;
    baseDock.selectedAccounts = cards.length;
    baseDock.accounts = { state: "available", complete: true, cards: clone(cards) };
    const server = allowRemove ? { ...clone(fixture.server), changes: "on", accountRemove: true, form: "a".repeat(64) } : clone(fixture.server);
    const found = { "api/v1/dock": baseDock, "api/v1/server": server, "api/v1/usage": fixture.usage }[path];
    return { ok: found !== undefined, status: found !== undefined ? 200 : 404, json: async () => clone(found), text: async () => JSON.stringify(found) };
  };
  w.TextEncoder = TextEncoder; w.scrollTo = () => {}; w.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} }); w.Element.prototype.scrollIntoView = () => {};
  w.eval(js);
  const tick = () => new Promise((r) => setTimeout(r, 25));
  await tick(); await tick();
  w.location.hash = "#/accounts"; w.dispatchEvent(new w.Event("hashchange"));
  await tick(); await tick();
  if (w.document.querySelector(".account-remove")) errors.push("Remove button rendered without accountRemove enabled");
  allowRemove = true;
  w.document.querySelector("#refresh").click();
  await tick(); await tick(); await tick();
  const removeBtn = w.document.querySelector(`[data-focus="account-remove-${acctB.id}"]`);
  if (!removeBtn) errors.push("Remove from catalog button missing when accountRemove is enabled");
  else {
    removeBtn.click();
    await tick(); await tick();
    const review = w.document.querySelector(".account-remove-review");
    const reviewText = review ? review.textContent : "";
    if (!review || !/accounts\.json/.test(reviewText) || !/untouched/.test(reviewText) || !/loop example-loop, route spare/.test(reviewText)) {
      errors.push("remove review did not render safety note and route reference warning: " + reviewText);
    } else {
      const confirmBtn = w.document.querySelector(`[data-focus="remove-confirm-${acctB.id}"]`);
      if (!confirmBtn) errors.push("confirm button missing inside remove review");
      else {
        confirmBtn.click();
        await tick(); await tick(); await tick();
        const cardCount = w.document.querySelectorAll("#pane .account-card").length;
        const doneBanner = w.document.querySelector("#pane .account-remove-result");
        if (cardCount !== 1) errors.push(`expected 1 account card after remove, got ${cardCount}`);
        if (!doneBanner || !/left untouched/.test(doneBanner.textContent)) errors.push("confirmation banner missing after removed card disappeared");
      }
    }
  }
  if (calls.join(",") !== "prepare,confirm") errors.push("expected prepare,confirm calls, got " + calls.join(","));
  w.close();
  for (const e of errors) failures.push("account-remove: " + e);
}

if (unlisted.size) failures.push(`the variants set loop fields that dock_page_variant_keys.json does not list (add them, and they will be checked against dockLoop's JSON tags): ${[...unlisted].join(", ")}`);

// Accounts page UX: eliminate duplicate "Not checked", surface local login file
// hints, expose direct Review selection on context_changed, and run one bounded
// auto-check pass on #/accounts open when accountCheck is enabled.
{
  const errs = [];
  const vc = new VirtualConsole(); vc.on("jsdomError", (e) => errs.push(String(e.message || e)));
  const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true, url: "http://127.0.0.1:1/s/token/#/accounts", virtualConsole: vc });
  const w = dom.window;
  const checkPosts = [];
  const acctState = [
    {
      id: "00000000000000000000000000000001", label: "Work Claude", provider: "claude-code", selection: "inherited",
      auth: { text: "Not checked", state: "unchecked", freshness: "unavailable" },
      plan: { text: "Not checked", state: "unavailable", freshness: "unavailable", missing: "not_checked" },
      configuredModel: { text: "Not recorded", state: "unavailable", freshness: "unavailable", missing: "not_recorded" },
      observedModel: { text: "Not recorded", state: "unavailable", freshness: "unavailable", missing: "not_recorded" },
      uses: "unavailable", identity: "selected_context_only", needsPerson: false
    },
    {
      id: "00000000000000000000000000000002", label: "Codex Local", provider: "codex", selection: "explicit_directory",
      localAuth: "file_present",
      auth: { text: "Not checked", state: "unchecked", freshness: "unavailable" },
      plan: { text: "Not checked", state: "unavailable", freshness: "unavailable", missing: "not_checked" },
      configuredModel: { text: "Not recorded", state: "unavailable", freshness: "unavailable", missing: "not_recorded" },
      observedModel: { text: "Not recorded", state: "unavailable", freshness: "unavailable", missing: "not_recorded" },
      uses: "unavailable", identity: "selected_context_only", needsPerson: false
    },
    {
      id: "00000000000000000000000000000003", label: "Drifted Claude", provider: "claude-code", selection: "inherited",
      auth: { text: "Unavailable", state: "unavailable", freshness: "unavailable" },
      plan: { text: "Unavailable", state: "unavailable", freshness: "unavailable" },
      configuredModel: { text: "Not recorded", state: "unavailable", freshness: "unavailable", missing: "not_recorded" },
      observedModel: { text: "Not recorded", state: "unavailable", freshness: "unavailable", missing: "not_recorded" },
      reason: "context_changed", attention: "context_changed", uses: "unavailable", identity: "selected_context_only", needsPerson: true
    }
  ];
  let curRef = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
  const makeDock = () => {
    const d = clone(fixture.dock);
    d.accountsReference = curRef;
    d.selectedAccounts = acctState.length;
    d.accounts = { state: "available", complete: true, cards: [] };
    return d;
  };
  const makeSaved = () => ({ schemaVersion: 1, reference: curRef, readAt: fixture.dock.readAt, accounts: { state: "available", complete: true, cards: clone(acctState) } });
  const srvCfg = { ...clone(fixture.server), servedBy: "panel", changes: "on", form: "a".repeat(64), accountCheck: true, accountSelectionReview: true, accountQuota: true, accountQuotaProviders: ["codex", "claude-code"] };
  w.fetch = async (url, opts) => {
    const path = String(url).split("?")[0].replace(/^.*\/s\/[^/]+\//, "");
    if (opts && opts.method === "POST" && path.startsWith("api/v1/account-checks/")) {
      const cid = path.split("/")[3];
      checkPosts.push(cid);
      curRef = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
      const item = acctState.find((x) => x.id === cid);
      if (item) {
        item.auth = { text: "Selected CLI reports login", state: "reported_present", freshness: "fresh", ageSeconds: 0, source: item.provider + " status" };
        item.plan = { text: "Max", state: "reported", value: "max", freshness: "fresh", source: item.provider + " status" };
        delete item.localAuth;
      }
      return { ok: true, status: 200, json: async () => ({ schemaVersion: 1, result: { outcome: "done", text: "Account login check completed." } }), text: async () => "" };
    }
    if (path === "api/v1/saved-accounts") {
      const body = makeSaved();
      return { ok: true, status: 200, json: async () => body, text: async () => JSON.stringify(body) };
    }
    const found = { "api/v1/dock": makeDock(), "api/v1/server": srvCfg, "api/v1/usage": fixture.usage }[path];
    return { ok: found !== undefined, status: found !== undefined ? 200 : 404, json: async () => clone(found), text: async () => JSON.stringify(found) };
  };
  w.TextEncoder = TextEncoder; w.scrollTo = () => {}; w.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} }); w.Element.prototype.scrollIntoView = () => {};
  w.addEventListener("error", (e) => errs.push(String(e.message || e)));
  w.addEventListener("unhandledrejection", (e) => errs.push(String(e.reason || e)));
  w.eval(js);
  const tick = () => new Promise((r) => setTimeout(r, 25));
  await tick(); await tick(); await tick(); await tick();
  // Wait for bounded auto-check pass and reload.
  await tick(); await tick(); await tick(); await tick();
  if (checkPosts.length !== 2 || checkPosts.includes("00000000000000000000000000000003")) {
    errs.push(`expected 2 bounded auto-checks (skipping context_changed), got ${JSON.stringify(checkPosts)}`);
  }
  const cards = [...w.document.querySelectorAll("#pane .account-card")];
  if (cards.length !== 3) errs.push(`expected 3 account cards, got ${cards.length}`);
  else {
    if (!/Selected CLI reports login/.test(cards[0].textContent) || !/Max/.test(cards[0].textContent)) errs.push("auto-checked first card did not refresh login/plan");
    if (!/Review selection/.test(cards[2].textContent)) errs.push("context_changed card lacks Review selection action");
  }
  // Read-only view: verify no duplicate "Not checked" per unchecked row & localAuth file hint shows cleanly.
  const roRef = "cccccccccccccccccccccccccccccccc";
  const roDom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true, url: "http://127.0.0.1:1/s/token/#/accounts", virtualConsole: vc });
  const rw = roDom.window;
  const roPosts = [];
  const roCards = [
    {
      id: "00000000000000000000000000000001", label: "Work Claude", provider: "claude-code", selection: "inherited",
      auth: { text: "Not checked", state: "unchecked", freshness: "unavailable" },
      plan: { text: "Not checked", state: "unavailable", freshness: "unavailable", missing: "not_checked" },
      configuredModel: { text: "Not recorded", state: "unavailable", freshness: "unavailable", missing: "not_recorded" },
      observedModel: { text: "Not recorded", state: "unavailable", freshness: "unavailable", missing: "not_recorded" },
      uses: "unavailable", identity: "selected_context_only", needsPerson: false
    },
    {
      id: "00000000000000000000000000000002", label: "Codex Local", provider: "codex", selection: "explicit_directory",
      localAuth: "file_present",
      auth: { text: "Not checked", state: "unchecked", freshness: "unavailable" },
      plan: { text: "Not checked", state: "unavailable", freshness: "unavailable", missing: "not_checked" },
      configuredModel: { text: "Not recorded", state: "unavailable", freshness: "unavailable", missing: "not_recorded" },
      observedModel: { text: "Not recorded", state: "unavailable", freshness: "unavailable", missing: "not_recorded" },
      uses: "unavailable", identity: "selected_context_only", needsPerson: false
    }
  ];
  rw.fetch = async (url, opts) => {
    if (opts && opts.method === "POST") roPosts.push(url);
    const path = String(url).split("?")[0].replace(/^.*\/s\/[^/]+\//, "");
    if (path === "api/v1/saved-accounts") {
      const body = { schemaVersion: 1, reference: roRef, readAt: fixture.dock.readAt, accounts: { state: "available", complete: true, cards: roCards } };
      return { ok: true, status: 200, json: async () => body, text: async () => JSON.stringify(body) };
    }
    const roDock = makeDock();
    roDock.accountsReference = roRef;
    roDock.selectedAccounts = roCards.length;
    const found = { "api/v1/dock": roDock, "api/v1/server": fixture.server, "api/v1/usage": fixture.usage }[path];
    return { ok: found !== undefined, status: found !== undefined ? 200 : 404, json: async () => clone(found), text: async () => JSON.stringify(found) };
  };
  rw.TextEncoder = TextEncoder; rw.scrollTo = () => {}; rw.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} }); rw.Element.prototype.scrollIntoView = () => {};
  rw.eval(js);
  await tick(); await tick(); await tick(); await tick();
  if (roPosts.length !== 0) errs.push("read-only panel sent POST requests on Accounts load");
  const roEls = [...rw.document.querySelectorAll("#pane .account-card")];
  const roNotChecked = (roEls[0]?.textContent.match(/Not checked/g) || []).length;
  if (roNotChecked !== 1) errs.push(`unchecked row repeated 'Not checked' ${roNotChecked} times, want 1`);
  if (!/Local login file present/.test(roEls[1]?.textContent || "")) errs.push("read-only Codex card lacked Local login file present hint");
  rw.close();
  w.close();
  for (const e of errs) failures.push("accounts: " + e);
}
// Ongoing usage is a reviewed, revocable setting, never a render-time provider call.
{
  const errs = [], vc = new VirtualConsole(); vc.on("jsdomError", e => errs.push(String(e.message)));
  const id = "a".repeat(32); let ref = "b".repeat(32), revision = 1;
  const dom = new JSDOM(html, {runScripts:"outside-only",pretendToBeVisual:true,url:"http://127.0.0.1:1/s/token/#/accounts/"+id,virtualConsole:vc});
  const w=dom.window, posts=[];
  const card={id,label:"Example usage updates",provider:"codex",selection:"inherited",auth:{text:"Login reported",state:"reported_present",freshness:"fresh"},plan:{text:"Plan unknown",state:"unavailable",freshness:"unavailable"},configuredModel:{text:"Not recorded",state:"unavailable",freshness:"unavailable"},observedModel:{text:"Not recorded",state:"unavailable",freshness:"unavailable"},uses:"unavailable",identity:"selected_context_only",needsPerson:false,usageUpdating:{state:"off",text:"Keep usage updated: off"}};
  card.quota={state:"reported",summary:{state:"reported",text:"Saved selected-context quota",source:"codex account/rateLimits/read",observedAt:"2030-03-04T12:00:00Z",freshness:"fresh",rows:[{name:"5-hour",text:"60% used · 40% remaining",usedPercent:60,remainingPercent:40,resetsAt:"2030-03-04T13:00:00Z"}]}};
  let accountClockNow=Date.parse("2030-03-04T12:00:00Z");w.Date.now=()=>accountClockNow;
  const clockTicks=[];const realInterval=w.setInterval.bind(w);w.setInterval=(fn,ms,...args)=>{if(ms===15000)clockTicks.push(fn);return realInterval(fn,ms,...args);};
  const server={...clone(fixture.server),servedBy:"panel",changes:"on",form:"a".repeat(64),accountQuota:true,accountUsageUpdating:true,accountCheck:false};
  w.fetch=async(url,opts)=>{
    const path=String(url).split("?")[0];let found;
    if(opts?.method==="POST"){
      const action=JSON.parse(opts.body).action, step=path.split("/").pop();posts.push({action,step});
      if(!path.startsWith("api/v1/account-usage-updates/")) errs.push("unasked provider/control request");
      found={schemaVersion:1};
      if(step==="prepare")found.preview={action,key:id,title:"Review usage updates",confirm:action==="enable_usage_updates"?"Keep usage updated":"Pause usage updates",form:"c".repeat(64),expiresAt:"2030-01-01T00:00:00Z",lines:[[{t:"Only this selection. Keep a panel running; no per-poll confirmation."}]],lost:"Read settings again",refused:"Unchanged"};
      else{ref=(++revision).toString(16).padStart(32,"0");card.usageUpdating=action==="enable_usage_updates"?{state:"enabled",text:"Keep usage updated: enabled",nextAttemptAt:"2030-01-01T01:00:00Z"}:{state:"off",text:"Keep usage updated: off"};found.result={outcome:"done",text:"Usage update setting saved"};}
    }else if(path==="api/v1/server")found=server;
    else if(path==="api/v1/saved-accounts")found={schemaVersion:1,reference:ref,readAt:fixture.dock.readAt,accounts:{state:"available",complete:true,cards:[clone(card)]}};
    else if(path==="api/v1/dock"){found=clone(fixture.dock);found.accountsReference=ref;found.selectedAccounts=1;found.accounts={state:"available",complete:true,cards:[]};}
    else if(path==="api/v1/usage")found=fixture.usage;
    return{ok:found!==undefined,status:found===undefined?404:200,json:async()=>clone(found),text:async()=>JSON.stringify(found)};
  };
  w.TextEncoder=TextEncoder;w.scrollTo=()=>{};w.matchMedia=()=>({matches:false,addEventListener(){},removeEventListener(){}});w.Element.prototype.scrollIntoView=()=>{};
  w.eval(js);const settle=()=>new Promise(r=>setTimeout(r,150));await settle();
  const button=text=>[...w.document.querySelectorAll("button")].find(b=>b.textContent===text);
  const click=async text=>{const b=button(text);if(!b){errs.push("missing button "+text);return;}b.click();await settle();};
  if(posts.length)errs.push("render started acquisition");
  await click("Keep usage updated…");
  if(posts.length!==1||posts[0].step!=="prepare")errs.push("enable skipped review");
  await click("Cancel");if(posts.length!==1)errs.push("cancel changed setting");
  await click("Keep usage updated…");await click("Keep usage updated");await click("Close");
  if(!w.document.querySelector(".account-usage-updating").textContent.includes("enabled"))errs.push("enabled status missing");
  const disclosure=w.document.querySelector(".account-card details");disclosure.open=true;const summary=disclosure.querySelector("summary");summary.focus();
  if(!w.document.querySelector(".account-quota").textContent.includes("40% remaining"))errs.push("usage controls hid saved quota");
  accountClockNow+=3600000;const postCount=posts.length;clockTicks.forEach(fn=>fn());
  const quotaBox=w.document.querySelector(".account-quota");
  if(!quotaBox.textContent.includes("Reset time passed")||!quotaBox.querySelector(".quota-stamp").textContent.includes("Older reading")||quotaBox.querySelector("meter:not([hidden])"))errs.push("enabled updates hid reset/stale evidence or assumed replenishment");
  if(!disclosure.open||w.document.activeElement!==summary||posts.length!==postCount)errs.push("clock tick changed focus/disclosure or submitted provider/settings action");
  if(!quotaBox.querySelector(".account-usage-updating").textContent.includes("enabled"))errs.push("clock tick hid enabled status");
  await click("Pause usage updates…");await click("Pause usage updates");await click("Close");
  if(card.usageUpdating.state!=="off"||posts.filter(p=>p.step==="confirm").length!==2)errs.push("revocation missing");
  for(const state of ["waiting","backoff","paused"]){card.usageUpdating={state,text:"Fixture "+state};ref=(++revision).toString(16).padStart(32,"0");w.document.getElementById("refresh").click();await settle();if(!w.document.querySelector(".account-usage-updating").textContent.includes("Fixture "+state))errs.push(state+" not displayed");}
  server.changes="off";w.document.getElementById("refresh").click();await settle();if(button("Pause usage updates…")||button("Keep usage updated…"))errs.push("read-only setting action offered");
  if(w.__renderError)errs.push(String(w.__renderError));dom.window.close();for(const e of errs)failures.push("usage updates: "+e);
}

// Overview Setup Map above the fold, categorized caution consistency, task-flow
// actions (question, deadline, send allowance, stale supervision, account drift),
// cross-surface resolution/replacement/supersession guards, and Accounts 5-state clarity.
{
  const errs = [];
  const vc = new VirtualConsole(); vc.on("jsdomError", (e) => errs.push(String(e.message || e)));
  const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true, url: "http://127.0.0.1:1/s/token/#/", virtualConsole: vc });
  const w = dom.window;
  const acctCardsFixture = [
    {
      id: "10000000000000000000000000000001", label: "Core Codex", provider: "codex", selection: "explicit_directory",
      auth: { text: "Selected CLI reports login", state: "reported_present", freshness: "fresh" },
      plan: { text: "Pro", state: "reported", value: "pro", freshness: "fresh" },
      quota: { state: "reported", summary: { state: "reported", text: "Saved", source: "codex account/rateLimits/read", observedAt: "2030-03-04T12:00:00Z", freshness: "fresh", rows: [{ name: "5-hour", text: "38% used · 62% remaining", usedPercent: 38, remainingPercent: 62, resetsAt: "2099-01-01T00:00:00Z" }] } },
      usageUpdating: { state: "enabled", text: "Keep usage updated: enabled" }
    },
    {
      id: "10000000000000000000000000000002", label: "Studio Claude", provider: "claude-code", selection: "inherited",
      auth: { text: "Selected CLI reports login", state: "reported_present", freshness: "fresh" },
      plan: { text: "Max", state: "reported", value: "max", freshness: "fresh" },
      quota: { state: "reported", summary: { state: "reported", text: "Retrieved", source: "claude:get_usage", retrievedAt: "2030-03-04T12:00:00Z", freshness: "provider_unknown", rows: [{ name: "7-day", text: "100% used · 0% remaining", usedPercent: 100, remainingPercent: 0, resetsAt: "2099-01-02T00:00:00Z" }] } },
      usageUpdating: { state: "off", text: "Keep usage updated: off" }
    },
    {
      id: "10000000000000000000000000000003", label: "Ended Agy", provider: "antigravity", selection: "inherited",
      auth: { text: "Selected CLI reports login", state: "reported_present", freshness: "fresh" },
      plan: { text: "Pro", state: "reported", value: "pro", freshness: "fresh" },
      quota: { state: "reported", summary: { state: "reported", text: "Retrieved", source: "agy:/usage", retrievedAt: "2030-03-01T12:00:00Z", freshness: "provider_unknown", rows: [{ name: "5-hour", text: "82% used", usedPercent: 82, remainingPercent: 18, resetsAt: "2020-01-01T00:00:00Z", ended: true }] } }
    },
    {
      id: "10000000000000000000000000000004", label: "Reserve Codex", provider: "codex", selection: "explicit_directory",
      auth: { text: "Unavailable", state: "unavailable", freshness: "unavailable" },
      plan: { text: "Unavailable", state: "unavailable", freshness: "unavailable" },
      attention: "context_changed", reason: "context_changed",
      quota: { state: "unavailable", reason: "selection_review_required" },
      usageUpdating: { state: "paused", text: "Updates paused · selection changed" }
    }
  ];
  const baseDoc = clone(fixture.dock);
  const kQ = "l111111111111", kDl = "l222222222222", kSup = "l333333333333", kLim = "l444444444444", kRet = "l555555555555";
  const r1 = "r111111111111", r2 = "r222222222222", r3 = "r333333333333";
  let currentDock = {
    ...baseDoc,
    accountsReference: "ref_ux_v1",
    selectedAccounts: acctCardsFixture.length,
    accounts: { state: "available", complete: true, cards: [] },
    allClear: false, complete: true, needsLine: "3 loops need you",
    buildFreshness: { text: "Panel v0.3.0 · 1 daemon older", onIt: 4, notOnIt: 1 },
    versions: { newest: "v0.3.0", reference: "this_panel", summary: "1 place differs", behind: 1, lede: "Lede", rows: [], unrecorded: [], clients: "none" },
    needsYou: [
      { kind: "held", waiting: "asked", by: "check", key: kQ, loop: "atlas-question", since: "2026-11-27T11:00:00Z", sinceEvidence: "hold_recorded", reason: "Lane 1 is waiting for an answer.", note: "Approve schema change in docs/api.md", noteMasked: true, maskedNote: "Approve schema change in docs/api.md", waited: "Waiting 12 min", action: "Answer in Lane 1.", resume: "looptender resume -c CONFIG" },
      { kind: "held", waiting: "limit", by: "check", key: kDl, loop: "atlas-deadline", reason: "It reached its deadline.", waited: "Waiting 2 h", action: "Review deadline or mark finished.", resume: "looptender resume -c CONFIG" },
      { kind: "overdue", waiting: "checks", by: "check", key: kSup, loop: "drift-stale", reason: "Its checks have stopped: no check ran in awake window 1h 40m.", waited: "Waiting 2 d 10 h", action: "Inspect scheduler status." }
    ],
    routes: [
      { key: r1, provider: "codex", account: "codex-main", model: "gpt-5.2-codex", title: "gpt-5.2-codex", meta: ["Codex CLI", "group codex-main"], readiness: "ready", phrase: "Ready", usage: { availability: "unavailable", readLine: "No reading" }, runningNow: [], recordedFor: [], standby: [], unknown: [], leftBy: [] },
      { key: r2, provider: "claude-code", account: "claude-studio", model: "claude-sonnet-4-6", title: "claude-sonnet-4-6", meta: ["BB thread", "group claude-studio"], readiness: "ready", phrase: "Ready", usage: { availability: "unavailable", readLine: "No reading" }, runningNow: [], recordedFor: [], standby: [], unknown: [], leftBy: [] },
      { key: r3, provider: "codex", model: "gpt-5.2-codex", title: "BB default", meta: ["BB thread"], readiness: "unknown", phrase: "Unassigned", usage: { availability: "unavailable", readLine: "No reading" }, runningNow: [], recordedFor: [], standby: [], unknown: [], leftBy: [] }
    ],
    accountUses: [
      { card: acctCardsFixture[0].id, routes: [{ route: r1, title: "gpt-5.2-codex", loops: [kQ] }] },
      { context: true, selection: "bb_thread", routes: [{ route: r3, title: "BB default", loops: [kLim] }] }
    ],
    loops: [
      { ...clone(baseDoc.loops[0]), key: kQ, name: "atlas-question", state: "waiting_for_you", phrase: "Waiting for you", can: ["pause", "stop", "resume"], hold: { state: "held", phrase: "Held" }, currentRoute: { availability: "known", role: "main", name: "codex-a", tool: "codex", provider: "codex", model: "gpt-5.2-codex" }, identifiers: { registrationId: "reg_atlas_1", thread: "thr_alpha_1" }, lineage: { count: 1, from: kRet }, device: { text: "workstation-alpha" }, lastCheck: { availability: "recent", text: "Checked 2 min ago" }, lanes: { availability: "current", summary: "1 waiting · 1 working", lanes: [{ number: 1, state: "waiting", phrase: "Waiting for answer", title: "Schema bump" }] }, chain: { availability: "known", handoffOn: ["usage_limit"], how: "auto", brief: "1 ready", entries: [{ position: 0, route: r1, title: "gpt-5.2-codex", current: true, inUse: false, readiness: "current", meta: ["Codex CLI"], usage: { availability: "unavailable", readLine: "No reading" }, catalogAccount: { assigned: "by_you", card: acctCardsFixture[0].id, label: "Core Codex", check: "consistent" } }, { position: 1, route: r2, title: "claude-sonnet-4-6", current: false, inUse: false, readiness: "ready", meta: ["BB thread"], usage: { availability: "unavailable", readLine: "No reading" }, catalogAccount: { assigned: "by_you", card: acctCardsFixture[1].id, label: "Studio Claude", check: "not_verified" } }] } },
      { ...clone(baseDoc.loops[0]), key: kDl, name: "atlas-deadline", state: "waiting_for_you", phrase: "Deadline reached", can: ["archive", "stop", "resume"], hold: { state: "held", phrase: "Held" }, limits: [{ label: "Deadline", phrase: "Reached", reached: true, text: [{ t: "2026-11-25 14:00" }], effect: "Loop held at deadline" }], device: { text: "workstation-alpha" }, lastCheck: { availability: "recent", text: "Checked 5 min ago" }, chain: { availability: "known", entries: [{ position: 0, route: r1, title: "gpt-5.2-codex", current: true, inUse: false, meta: ["Codex CLI"], usage: { availability: "unavailable", readLine: "No reading" } }] } },
      { ...clone(baseDoc.loops[0]), key: kSup, name: "drift-stale", state: "not_checked_recently", phrase: "Checks stopped", supervisor: "not_answering", checkedBy: { kind: "ticks", text: "Checked by ticks" }, device: { text: "workstation-alpha" }, lastCheck: { availability: "stale", text: "Checked 2 d 10 h ago" }, chain: { availability: "known", entries: [{ position: 0, route: r3, title: "BB default", current: true, inUse: false, meta: ["BB thread"], usage: { availability: "unavailable", readLine: "No reading" }, catalogAccount: { assigned: "none" } }] } },
      { ...clone(baseDoc.loops[0]), key: kLim, name: "cedar-limit", state: "waiting_on_limit", phrase: "Waiting on limit", device: { text: "workstation-alpha" }, lastCheck: { availability: "recent", text: "Checked 1 min ago" }, chain: { availability: "known", entries: [{ position: 0, route: r3, title: "BB default", current: true, inUse: false, meta: ["BB thread"], usage: { availability: "unavailable", readLine: "No reading" }, catalogAccount: { assigned: "none" } }] } },
      { ...clone(baseDoc.loops[0]), key: kRet, name: "fable-legacy", state: "off", phrase: "Superseded", hold: { state: "superseded", phrase: "Superseded" }, archived: { at: "2026-11-20T00:00:00Z", continuedIn: { key: kQ, loop: "atlas-question" } }, device: { text: "workstation-alpha" }, chain: { availability: "known", entries: [] } }
    ]
  };
  const posts = [];
  let failDock = false;
  let delayDock = null;
  let deferredPrepare = Object.create(null);
  let deferredConfirm = Object.create(null);
  const srv = { ...clone(fixture.server), servedBy: "panel", version: "v0.3.0", changes: "on", sendAllowanceEdit: true, accountSelectionReview: true, accountQuota: true, accountQuotaProviders: ["codex", "claude-code", "antigravity"], form: "a".repeat(64) };
  w.fetch = async (url, opts) => {
    const rawUrl = String(url);
    const withIds = rawUrl.includes("identifiers=1");
    const path = rawUrl.split("?")[0].replace(/^.*\/s\/[^/]+\//, "");
    if (opts && opts.method === "POST") {
      posts.push(path);
      if (deferredPrepare[path]) {
        return deferredPrepare[path]();
      }
      if (deferredConfirm[path]) {
        return deferredConfirm[path]();
      }
      if (path === `api/v1/loops/${kQ}/prepare`) {
        return { ok: true, status: 200, json: async () => ({ schemaVersion: 1, preview: { action: "resume", key: kQ, title: "Resume?", confirm: "Confirm resume", form: "c".repeat(64), expiresAt: "2035-01-01T00:00:00Z", lines: [[{ t: "Confirm parent resume." }]], lost: "lost", refused: "refused" } }) };
      }
      if (path === `api/v1/loops/${kQ}/confirm`) {
        return { ok: true, status: 200, json: async () => ({ schemaVersion: 1, result: { outcome: "done", text: "Resumed" } }) };
      }
      return { ok: false, status: 404, json: async () => ({}) };
    }
    if (path === "api/v1/saved-accounts") {
      const p = { schemaVersion: 1, reference: currentDock.accountsReference, accounts: { state: "available", complete: true, cards: clone(acctCardsFixture) } };
      return { ok: true, status: 200, json: async () => p, text: async () => JSON.stringify(p) };
    }
    if (path === "api/v1/dock") {
      if (delayDock) await delayDock();
      if (failDock) return Promise.reject(new Error("simulated fetch failure"));
      const snap = clone(currentDock);
      snap.identifiers = withIds;
      if (!withIds) {
        for (const n of snap.needsYou) {
          if (n.noteMasked && n.maskedNote) { n.note = n.maskedNote; delete n.maskedNote; }
        }
        for (const lp of snap.loops) {
          delete lp.identifiers;
          if (lp.currentRoute) delete lp.currentRoute.name;
          if (lp.lanes && Array.isArray(lp.lanes.lanes)) {
            for (const ln of lp.lanes.lanes) {
              if (ln.title || ln.titleEvidence === "recorded") ln.titleEvidence = "withheld";
              delete ln.title; delete ln.key;
            }
          }
        }
      }
      return { ok: true, status: 200, json: async () => snap, text: async () => JSON.stringify(snap) };
    }
    const found = { "api/v1/server": srv, "api/v1/usage": fixture.usage }[path];
    return { ok: found !== undefined, status: found !== undefined ? 200 : 404, json: async () => clone(found), text: async () => JSON.stringify(found) };
  };
  w.TextEncoder = TextEncoder; w.scrollTo = () => {}; w.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} }); w.Element.prototype.scrollIntoView = () => {};
  w.eval(js);
  const tick = () => new Promise((r) => setTimeout(r, 30));
  const settle = async () => { await tick(); await tick(); await tick(); await tick(); };
  await settle();

  if (posts.length !== 0) errs.push("Home render fired unexpected POST calls: " + JSON.stringify(posts));
  const readlineText = w.document.getElementById("readline")?.textContent || "";
  if (!/^Saved records read at /.test(readlineText)) errs.push("readline lacks 'Saved records read at': " + readlineText);
  if ((w.document.getElementById("build-pill")?.textContent || "").includes("v0.3.0") === false) errs.push("header build-pill missing v0.3.0");
  if (/\bnodes?\b/i.test(w.document.body.textContent || "")) errs.push("rendered panel UI contains banned word node(s)");

  // Setup Map precedes Attention section in DOM
  const paneChildren = [...w.document.getElementById("pane").children];
  const mapIdx = paneChildren.findIndex((x) => x.id === "setup-map");
  const attnIdx = paneChildren.findIndex((x) => x.id === "attention-section");
  if (mapIdx < 0 || attnIdx < 0 || mapIdx > attnIdx) errs.push(`Setup Map (${mapIdx}) must precede Attention (${attnIdx})`);

  // Consistent categorized counts across picker badge and map filters
  const pickCount = w.document.querySelector("#picker .pick-needs .count")?.textContent;
  const pickSub = w.document.querySelector("#picker .pick-needs .pick-state")?.textContent || "";
  if (pickCount !== "2" || !pickSub.includes("1 checks stopped") || !pickSub.includes("1 at limit")) errs.push(`picker categorized counts wrong: count=${pickCount} sub=${pickSub}`);
  const decChip = w.document.querySelector('[data-focus="filter-decisions"]')?.textContent;
  const chkChip = w.document.querySelector('[data-focus="filter-checks"]')?.textContent;
  if (decChip !== "Decisions & bounds (2)" || chkChip !== "Checks stopped (1)") errs.push(`filter chips mismatch: ${decChip} | ${chkChip}`);

  // Select card in Setup Map -> highlight + inspector + Esc clear
  w.document.querySelector('[data-focus="map-loop-' + kQ + '"]').click(); await tick();
  if (!w.document.getElementById("map-inspector")?.textContent.includes("BB unverified") || !w.document.querySelector('[data-focus="map-loop-' + kSup + '"]')?.classList.contains("is-dimmed")) {
    errs.push("selecting loop in Setup Map did not highlight/inspector/dim properly");
  }
  w.document.dispatchEvent(new w.KeyboardEvent("keydown", { key: "Escape" })); await tick();
  if (w.document.querySelector('[data-focus="map-loop-' + kQ + '"]')?.classList.contains("is-selected")) errs.push("Escape did not clear map selection");

  // Task flow 1: Question -> Open question drawer with honest G9 copy
  const qCard = w.document.querySelector(`#attention-section [data-issue-key="${kQ}"]`);
  const qPrim = qCard?.querySelector(".task-actions .btn.is-primary")?.textContent;
  if (qPrim !== "Open question") errs.push("question primary action wrong: " + qPrim);
  qCard?.querySelector(`[data-focus="open-question-${kQ}"]`)?.click(); await tick();
  const qDrawer = w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"][data-drawer-mode="question"]`);
  if (!qDrawer || !qDrawer.textContent.includes("not yet emitted") || !qDrawer.textContent.includes("contract gap G9")) errs.push("Open question drawer missing honest G9 disclosure");

  // Adversarial Test A: Emitted currentRoute changes ONLY (reason, note, since unchanged) -> invalidates open drawer + pending Confirm
  w.document.querySelector(`[data-focus="act-resume-${kQ}"]`)?.click(); await settle();
  if (!w.document.querySelector('[data-focus="change-confirm"]')) errs.push("expected Confirm resume button after prepare");
  currentDock.loops[0].currentRoute = { availability: "known", role: "fallback", name: "claude-b", tool: "claude-code", provider: "claude-code", model: "claude-sonnet-4-6" };
  w.document.getElementById("refresh").click(); await settle();
  if (w.document.querySelector('[data-focus="change-confirm"]') || w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"]`)) {
    errs.push("stale confirm/drawer stayed open after emitted currentRoute changed");
  }
  if (!w.document.querySelector(".issue-stale-banner")?.textContent.includes("updated")) errs.push("missing stale-issue banner when currentRoute changed");

  // Adversarial Test B: Emitted identifiers.thread changes ONLY -> invalidates open drawer + pending Confirm
  w.document.getElementById("identifiers").click(); await settle();
  w.document.querySelector(`[data-focus="open-question-${kQ}"]`)?.click(); await tick();
  if (!w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"]`)?.textContent.includes("bb thread open thr_alpha_1")) {
    errs.push("expected bb thread open thr_alpha_1 in question drawer with identifiers on");
  }
  w.document.querySelector(`[data-focus="act-resume-${kQ}"]`)?.click(); await settle();
  currentDock.loops[0].identifiers.thread = "thr_beta_2";
  w.document.getElementById("refresh").click(); await settle();
  if (w.document.querySelector('[data-focus="change-confirm"]') || w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"]`)) {
    errs.push("stale confirm/drawer stayed open after emitted identifiers.thread changed");
  }
  w.document.getElementById("identifiers").click(); await settle();

  // Adversarial Test C: Failed refresh immediately marks retained cards & open drawer Unconfirmed without any extra click/render
  w.document.querySelector(`[data-focus="open-question-${kQ}"]`)?.click(); await tick();
  failDock = true;
  w.document.getElementById("refresh").click(); await settle();
  const failedBadge = w.document.querySelector(`#attention-section [data-issue-key="${kQ}"] .unconfirmed-badge`);
  const failedDrawerBadge = w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"] .unconfirmed-badge`);
  const failedResumeBtn = w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"] [data-focus="act-resume-${kQ}"]`);
  if (!failedBadge || !failedBadge.textContent.includes("last read failed") || !failedDrawerBadge || !failedResumeBtn?.disabled) {
    errs.push("failed refresh must immediately show Unconfirmed on retained card/drawer and disable writes");
  }
  failDock = false;
  w.document.getElementById("refresh").click(); await settle();

  // Adversarial Test D: complete=true + loop.evidence="partial" immediately renders Unconfirmed
  currentDock.loops[0].evidence = "partial";
  w.document.getElementById("refresh").click(); await settle();
  const partialBadge = w.document.querySelector(`#attention-section [data-issue-key="${kQ}"] .unconfirmed-badge`);
  if (!partialBadge || !partialBadge.textContent.includes("partial")) errs.push("partial loop evidence lacked immediate Unconfirmed badge");
  currentDock.loops[0].evidence = "available";
  w.document.getElementById("refresh").click(); await settle();

  // Adversarial Test E: complete=false omitting an active need must retain it as Unconfirmed and fail-close pending confirm (never falsely resolve), then clear on complete=true
  if (!w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"]`)) {
    w.document.querySelector(`[data-focus="open-question-${kQ}"]`)?.click(); await tick();
  }
  w.document.querySelector(`[data-focus="act-resume-${kQ}"]`)?.click(); await settle();
  if (!w.document.querySelector('[data-focus="change-confirm"]')) errs.push("expected prepared confirm before incomplete test");
  const savedQNeed = clone(currentDock.needsYou[0]);
  const savedQLoop = clone(currentDock.loops[0]);
  currentDock.needsYou = currentDock.needsYou.filter((x) => x.key !== kQ);
  currentDock.loops = currentDock.loops.filter((x) => x.key !== kQ);
  currentDock.complete = false;
  w.document.getElementById("refresh").click(); await settle();
  const incCardBadge = w.document.querySelector(`#attention-section [data-issue-key="${kQ}"] .unconfirmed-badge`);
  const incResumeBtn = w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"] [data-focus="act-resume-${kQ}"]`);
  if (!incCardBadge || !incCardBadge.textContent.includes("omitted from incomplete read") || !incResumeBtn?.disabled || w.document.querySelector('[data-focus="change-confirm"]')) {
    errs.push("complete=false omitting need must retain Unconfirmed card/drawer, disable writes, and cancel prepared confirm");
  }
  // Once complete=true confirms kQ is gone, it clears cleanly
  currentDock.complete = true;
  w.document.getElementById("refresh").click(); await settle();
  if (w.document.querySelector(`#attention-section [data-issue-key="${kQ}"]`)) errs.push("need omitted on complete=true read stayed stuck after complete read");
  currentDock.needsYou.unshift(savedQNeed);
  currentDock.loops.unshift(savedQLoop);
  w.document.getElementById("refresh").click(); await settle();

  // Adversarial Test F: Out-of-order deferred prepare race — slow A must not overwrite newer B
  let resA, resB;
  deferredPrepare[`api/v1/loops/${kQ}/prepare`] = () => new Promise((r) => { resA = r; });
  deferredPrepare[`api/v1/loops/${kDl}/prepare`] = () => new Promise((r) => { resB = r; });
  w.document.querySelector(`[data-focus="open-question-${kQ}"]`)?.click(); await tick();
  w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"] [data-focus="act-resume-${kQ}"]`)?.click(); await tick();
  w.document.querySelector(`[data-focus="review-deadline-${kDl}"]`)?.click(); await tick();
  w.document.querySelector(`.task-drawer[data-drawer-key="${kDl}"] [data-focus="act-archive-${kDl}"]`)?.click(); await tick();
  resB({ ok: true, status: 200, json: async () => ({ schemaVersion: 1, preview: { action: "archive", key: kDl, title: "Archive deadline loop?", confirm: "Confirm archive B", form: "b".repeat(64), expiresAt: "2035-01-01T00:00:00Z", lines: [[{ t: "Archive B." }]], lost: "lost", refused: "refused" } }) });
  await settle();
  resA({ ok: true, status: 200, json: async () => ({ schemaVersion: 1, preview: { action: "resume", key: kQ, title: "Late stale A", confirm: "Stale confirm A", form: "c".repeat(64), expiresAt: "2035-01-01T00:00:00Z", lines: [[{ t: "Stale A." }]], lost: "lost", refused: "refused" } }) });
  await settle();
  if (w.document.querySelector('[data-focus="change-confirm"]')?.textContent !== "Confirm archive B") {
    errs.push("out-of-order deferred prepare A overwrote newer review B: " + w.document.querySelector('[data-focus="change-confirm"]')?.textContent);
  }
  deferredPrepare = Object.create(null);
  w.document.querySelector('[data-focus="change-cancel"]')?.click(); await tick();
  w.document.dispatchEvent(new w.KeyboardEvent("keydown", { key: "Escape" })); await tick();

  // Adversarial Test G: P3 privacy-only toggle preserves baseFp & keeps drawer open
  currentDock.loops[0].lanes.lanes[0] = { number: 1, state: "waiting", phrase: "Waiting for answer", title: "FICTIONAL-PRIVATE-LANE", titleEvidence: "recorded", key: "lane_priv_1" };
  currentDock.needsYou[0].note = "Approve /private/path/secret.md";
  currentDock.needsYou[0].noteMasked = true;
  currentDock.needsYou[0].maskedNote = "Approve [hidden]";
  w.document.getElementById("identifiers").click(); await settle();
  w.document.querySelector(`[data-focus="open-question-${kQ}"]`)?.click(); await tick();
  if (!w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"]`)?.textContent.includes("FICTIONAL-PRIVATE-LANE")) {
    errs.push("expected FICTIONAL-PRIVATE-LANE when identifiers are on");
  }
  w.document.getElementById("identifiers").click(); await settle();
  if (!w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"]`)) errs.push("privacy-only toggle closed question drawer");
  if (w.document.querySelector(".issue-stale-banner")) errs.push("privacy-only toggle showed stale issue banner");
  if (w.document.body.textContent.includes("FICTIONAL-PRIVATE-LANE") || w.document.body.textContent.includes("/private/path/secret.md")) {
    errs.push("private lane title or note leaked after identifiers toggled off");
  }

  // Adversarial Test H: P2 retained private lane title scrubbed on incomplete omission AND hidden during delayed identifiers-on reply
  w.document.getElementById("identifiers").click(); await settle();
  if (w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"]`)) {
    w.document.dispatchEvent(new w.KeyboardEvent("keydown", { key: "Escape" })); await tick();
  }
  const savedHNeed = clone(currentDock.needsYou[0]);
  const savedHLoop = clone(currentDock.loops[0]);
  currentDock.complete = false;
  currentDock.needsYou = currentDock.needsYou.filter((x) => x.key !== kQ);
  currentDock.loops = currentDock.loops.filter((x) => x.key !== kQ);
  w.document.getElementById("identifiers").click(); await settle();
  if (!w.document.querySelector(`#attention-section [data-issue-key="${kQ}"]`)) errs.push("incomplete read omitted kQ before Test H");
  w.document.querySelector(`[data-focus="open-question-${kQ}"]`)?.click(); await tick();
  if (w.document.body.textContent.includes("FICTIONAL-PRIVATE-LANE") || w.document.body.textContent.includes("thr_alpha_1") || !w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"]`)?.textContent.includes("title withheld")) {
    errs.push("retained omitted drawer leaked private identifiers or omitted title withheld label");
  }
  currentDock.complete = true;
  currentDock.needsYou.unshift(savedHNeed);
  currentDock.loops.unshift(savedHLoop);
  w.document.getElementById("refresh").click(); await settle();
  // Delayed identifiers-on reply when toggled off mid-flight
  w.document.getElementById("identifiers").click(); await settle();
  if (!w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"]`)) {
    w.document.querySelector(`[data-focus="open-question-${kQ}"]`)?.click(); await tick();
  }
  let resDelayed;
  delayDock = () => new Promise((r) => { resDelayed = r; });
  w.document.getElementById("refresh").click(); await tick();
  w.document.getElementById("identifiers").click(); await tick();
  if (w.document.body.textContent.includes("FICTIONAL-PRIVATE-LANE")) {
    errs.push("private lane title remained visible immediately after toggling identifiers off while refresh in flight");
  }
  delayDock = null; resDelayed(); await settle();
  if (w.document.body.textContent.includes("FICTIONAL-PRIVATE-LANE")) {
    errs.push("delayed identifiers-on reply leaked private lane title after identifiers turned off");
  }
  if (w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"]`)) {
    w.document.dispatchEvent(new w.KeyboardEvent("keydown", { key: "Escape" })); await tick();
  }

  // Adversarial Test I: P2 confirmChange serial guard prevents late confirm A from overwriting B or resurrecting cancelled review
  let confResolveA, confRejectA;
  w.document.querySelector(`[data-focus="open-question-${kQ}"]`)?.click(); await tick();
  w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"] [data-focus="act-resume-${kQ}"]`)?.click(); await settle();
  deferredConfirm[`api/v1/loops/${kQ}/confirm`] = () => new Promise((r) => { confResolveA = r; });
  w.document.querySelector('[data-focus="change-confirm"]')?.click(); await tick();
  deferredPrepare[`api/v1/loops/${kDl}/prepare`] = () => Promise.resolve({ ok: true, status: 200, json: async () => ({ schemaVersion: 1, preview: { action: "archive", key: kDl, title: "Archive B?", confirm: "Confirm archive B", form: "b".repeat(64), expiresAt: "2035-01-01T00:00:00Z", lines: [[{ t: "Archive B." }]], lost: "lost", refused: "refused" } }) });
  w.document.querySelector(`[data-focus="review-deadline-${kDl}"]`)?.click(); await tick();
  w.document.querySelector(`.task-drawer[data-drawer-key="${kDl}"] [data-focus="act-archive-${kDl}"]`)?.click(); await settle();
  confResolveA({ ok: true, status: 200, json: async () => ({ schemaVersion: 1, result: { outcome: "done", text: "Late A finished" } }) });
  await settle();
  if (w.document.querySelector('[data-focus="change-confirm"]')?.textContent !== "Confirm archive B") {
    errs.push("late confirm A overwrote active review B");
  }
  if (!w.document.querySelector('[data-bg-outcome="1"]')?.textContent.includes("Late A finished")) {
    errs.push("superseded confirm A outcome was not preserved separately");
  }
  w.document.querySelector('[data-focus="dismiss-bg-outcome"]')?.click(); await tick();
  w.document.querySelector('[data-focus="change-cancel"]')?.click(); await tick();

  // Uncertain late confirm A must not resurrect a cancelled review
  w.document.querySelector(`[data-focus="open-question-${kQ}"]`)?.click(); await tick();
  w.document.querySelector(`.task-drawer[data-drawer-key="${kQ}"] [data-focus="act-resume-${kQ}"]`)?.click(); await settle();
  deferredConfirm[`api/v1/loops/${kQ}/confirm`] = () => new Promise((_, rej) => { confRejectA = rej; });
  w.document.querySelector('[data-focus="change-confirm"]')?.click(); await tick();
  w.document.querySelector(`[data-focus="review-deadline-${kDl}"]`)?.click(); await tick();
  w.document.querySelector(`.task-drawer[data-drawer-key="${kDl}"] [data-focus="act-archive-${kDl}"]`)?.click(); await settle();
  w.document.querySelector('[data-focus="change-cancel"]')?.click(); await tick();
  confRejectA(new Error("timeout"));
  await settle();
  if (w.document.querySelector('[data-focus="change-result"]')) errs.push("late failed confirm A resurrected cancelled changeBox result");
  if (!w.document.querySelector('[data-bg-outcome="1"]')?.textContent.includes("uncertain")) {
    errs.push("late failed confirm A did not surface preserved uncertain outcome");
  }
  w.document.querySelector('[data-focus="dismiss-bg-outcome"]')?.click(); await tick();
  w.document.dispatchEvent(new w.KeyboardEvent("keydown", { key: "Escape" })); await tick();
  deferredPrepare = Object.create(null);
  deferredConfirm = Object.create(null);
  currentDock.loops[0].lanes.lanes[0] = { number: 1, state: "waiting", phrase: "Waiting for answer", title: "Schema bump" };
  currentDock.needsYou[0].note = "Approve schema change in docs/api.md";
  currentDock.needsYou[0].maskedNote = "Approve schema change in docs/api.md";

  // Transition 1b: Original question resolved while loop stays held for a separate bound reason (send allowance)
  currentDock.needsYou[0] = { kind: "held", waiting: "limit", by: "check", key: kQ, loop: "atlas-question", reason: "It reached its limit on messages sent.", waited: "Waiting 1 min", action: "Review send allowance." };
  currentDock.loops[0].limits = [{ label: "Send allowance", phrase: "50 / 50", reached: true, text: [{ t: "50 / 50 sends" }] }];
  w.document.getElementById("refresh").click(); await settle();
  if (!w.document.querySelector(`[data-focus="act-set_send_allowance-${kQ}"]`)) errs.push("separate remaining hold did not render Review send allowance action");

  // Transition 1c: Direct lane loopKey target on Open question
  currentDock.needsYou[0] = { kind: "held", waiting: "asked", by: "check", key: kQ, loop: "atlas-question", reason: "Waiting on sub-loop.", waited: "Waiting 2 min", action: "Answer in sub-loop." };
  currentDock.loops[0].lanes = { availability: "current", summary: "1 waiting", lanes: [{ number: 1, state: "waiting", phrase: "Waiting for answer", loop: "cedar-limit", loopKey: kLim }] };
  w.document.getElementById("refresh").click(); await settle();
  const qDirect = w.document.querySelector(`[data-focus="open-question-${kQ}"]`);
  if (!qDirect || qDirect.tagName !== "A" || !qDirect.getAttribute("href")?.endsWith("#/loop/" + kLim)) errs.push("Open question did not link directly to registered waiting lane loop");

  // Transition 2: Superseded loop clears pending actions & shows non-resumable guidance on complete read
  currentDock.needsYou = currentDock.needsYou.filter((x) => x.key !== kQ);
  currentDock.loops[0].state = "off"; currentDock.loops[0].phrase = "Superseded";
  currentDock.loops[0].hold = { state: "superseded", phrase: "Superseded" };
  currentDock.loops[0].archived = { at: "2030-03-04T12:05:00Z", continuedIn: { key: kRet, loop: "fable-legacy" } };
  w.document.getElementById("refresh").click(); await settle();
  if (w.document.querySelector(`#attention-section [data-issue-key="${kQ}"]`)) errs.push("externally resolved/superseded question remained after complete read");
  w.document.querySelector('[data-focus="filter-retired"]')?.click(); await tick();
  if (!w.document.querySelector(`[data-focus="map-loop-${kQ}"]`)?.textContent.includes("Superseded — do not resume")) errs.push("superseded loop lacked Superseded — do not resume guidance");

  // Transition 3: Deadline card Review deadline + Stale supervision Inspect supervision
  w.document.querySelector(`[data-focus="review-deadline-${kDl}"]`)?.click(); await tick();
  const dlDr = w.document.querySelector(`.task-drawer[data-drawer-key="${kDl}"][data-drawer-mode="deadline"]`);
  if (!dlDr || !dlDr.textContent.includes("2026-11-25 14:00") || !dlDr.textContent.includes("G11")) errs.push("deadline drawer missing deadline value or G11 gap note");
  w.document.querySelector(`[data-focus="inspect-supervision-${kSup}"]`)?.click(); await tick();
  const supDr = w.document.querySelector(`.task-drawer[data-drawer-key="${kSup}"][data-drawer-mode="supervision"]`);
  if (!supDr || !supDr.textContent.includes("Supervisor did not answer")) errs.push("supervision drawer missing verified cause");

  // Transition 4: Recovering structured state renders calmly without fabricated controls
  currentDock.complete = true;
  currentDock.loops[3].state = "recovering"; currentDock.loops[3].phrase = "Recovering"; currentDock.loops[3].recovering = true;
  w.document.getElementById("refresh").click(); await settle();
  if (!w.document.querySelector(`[data-focus="map-loop-${kLim}"]`)?.textContent.includes("Recovering")) errs.push("Recovering structured state not rendered");

  // Transition 5: Accounts view 5-category strip, context_changed vs 100% vs ended windows folded into Previous readings
  w.location.hash = "#/accounts"; w.dispatchEvent(new w.Event("hashchange")); await settle();
  const stripTxt = w.document.querySelector(".acct-summary-strip")?.textContent || "";
  if (!stripTxt.includes("Recent local read · provider time unknown") || !stripTxt.includes("Context review needed") || stripTxt.includes("Fresh measured quota")) errs.push("Accounts summary strip missing expected provenance-aware buckets: " + stripTxt);
  const endedFold = w.document.querySelectorAll(".acct-prev");
  if (!endedFold.length || !w.document.body.textContent.includes("Window ended · current quota unknown")) errs.push("ended Antigravity window was not folded behind Previous readings");
  if (!w.document.body.textContent.includes("Context review needed — not a quota limit") || !w.document.body.textContent.includes("Updates not reported by this panel build")) errs.push("context_changed or missing usageUpdating label missing on Accounts");

  // Transition 6: Read-only boundary disables send allowance editing with explicit reason
  w.location.hash = "#/"; w.dispatchEvent(new w.Event("hashchange")); await settle();
  srv.changes = "off"; srv.sendAllowanceEdit = false;
  currentDock.needsYou = [{ kind: "held", waiting: "limit", by: "check", key: kQ, loop: "atlas-question", reason: "It reached its limit on messages sent.", waited: "Waiting 1 min", action: "Review send allowance." }];
  currentDock.loops[0].state = "waiting_for_you"; currentDock.loops[0].phrase = "Waiting for you"; currentDock.loops[0].hold = { state: "held" }; delete currentDock.loops[0].archived;
  currentDock.loops[0].limits = [{ label: "Send allowance", phrase: "50 / 50", reached: true, text: [{ t: "50 / 50 sends" }] }];
  w.document.getElementById("refresh").click(); await settle();
  const roAllowBtn = w.document.querySelector(`[data-focus="act-set_send_allowance-${kQ}"]`);
  if (!roAllowBtn || !roAllowBtn.disabled || !w.document.querySelector(`#attention-section [data-issue-key="${kQ}"]`)?.textContent.includes("Read-only or in-thread")) {
    errs.push("read-only send allowance button should be disabled with explicit explanation");
  }

  w.close();
  for (const e of errs) failures.push("overview-ux: " + e);
}

// Quota provenance v1 consumer migration with actual #588 fixtures, legacy absent
// fallback, malformed fail-closed matrix, clock progression, reset, and collection modes.
{
  const errs = [];
  const vc = new VirtualConsole(); vc.on("jsdomError", (e) => errs.push(String(e.message || e)));
  const prov588 = JSON.parse(readFileSync(new URL("./quota_provenance_588_fixture.json", import.meta.url), "utf8"));
  const cards588 = JSON.parse(readFileSync(new URL("./quota_provenance_588_cards.json", import.meta.url), "utf8"));
  const baseCards = clone(cards588.accounts.cards);
  const claude588 = clone(prov588.accountQuotas.accounts.find((x) => x.provider === "claude-code"));
  const agy588 = clone(prov588.accountQuotas.accounts.find((x) => x.provider === "antigravity"));
  const mkCard = (id, label, provider, summary, extra = {}) => ({
    id, label, provider, selection: "explicit_directory",
    auth: { text: "Selected CLI reports login", state: "reported_present", freshness: "fresh", source: provider + " status", observedAt: "2030-03-04T12:00:00Z" },
    plan: { text: "Pro", state: "reported", value: "pro", freshness: "fresh", source: provider + " status", observedAt: "2030-03-04T12:00:00Z" },
    configuredModel: { text: "Not recorded", state: "unavailable", freshness: "unavailable", missing: "not_recorded" },
    observedModel: { text: "Not recorded", state: "unavailable", freshness: "unavailable", missing: "not_recorded" },
    quota: { state: summary.state || "reported", source: summary.source, freshness: summary.freshness, summary },
    usageUpdating: { state: "off", text: "Keep usage updated: off" },
    ...extra,
  });
  const legacyCodex = clone(baseCards[0].quota.summary);
  delete legacyCodex.provenance;
  const badVerSummary = clone(baseCards[0].quota.summary);
  badVerSummary.provenance = { version: 2, retrievedAt: "2030-03-04T11:59:00Z", providerObservedAt: null };
  badVerSummary.text = "SHOULD_NEVER_APPEAR_BAD_VERSION";
  badVerSummary.rows = [{ name: "Bad row", text: "77% used", usedPercent: 77, remainingPercent: 23, resetsAt: "2030-03-17T17:46:40Z" }];
  const badNonNullProv = clone(baseCards[0].quota.summary);
  badNonNullProv.provenance = { version: 1, retrievedAt: "2030-03-04T11:59:00Z", providerObservedAt: "2030-03-04T11:59:00Z" };
  badNonNullProv.text = "SHOULD_NEVER_APPEAR_NONNULL_PROVIDER";
  const badSubMs = clone(baseCards[0].quota.summary);
  badSubMs.observedAt = "2030-03-04T11:59:00Z";
  badSubMs.provenance = { version: 1, retrievedAt: "2030-03-04T11:59:00.000000001Z", providerObservedAt: null };
  badSubMs.text = "SHOULD_NEVER_APPEAR_SUBMS_CONFLICT";
  const badPresentNull = clone(claude588.summary);
  badPresentNull.provenance = null;
  badPresentNull.text = "SHOULD_NEVER_APPEAR_PRESENT_NULL";

  const provCards = [
    { ...baseCards[0], usageUpdating: { state: "enabled", text: "Keep usage updated: enabled" } }, // #588 v1 codex fresh + resets 2030-03-17
    baseCards[1], // #588 v1 codex stale + ended primary + failedRefreshAt
    baseCards[2], // #588 unavailable no_windows
    mkCard("99000000000000000000000000000001", "Legacy Codex", "codex", legacyCodex),
    mkCard("99000000000000000000000000000002", "V1 Claude", "claude-code", clone(claude588.summary)),
    mkCard("99000000000000000000000000000003", "V1 Antigravity", "antigravity", clone(agy588.summary)),
    mkCard("99000000000000000000000000000004", "Bad Version Codex", "codex", badVerSummary),
    mkCard("99000000000000000000000000000005", "Bad NonNull Provider", "codex", badNonNullProv),
    mkCard("99000000000000000000000000000006", "Bad SubMs Conflict", "codex", badSubMs),
    mkCard("99000000000000000000000000000007", "Bad Present Null", "claude-code", badPresentNull),
  ];
  const provDock = clone(fixture.dock);
  delete provDock.accountsReference;
  provDock.readAt = "2030-03-04T12:00:00Z";
  provDock.accounts = { state: "available", complete: true, cards: provCards };
  const provClockNowStart = Date.parse("2030-03-04T12:00:00Z");
  let provNow = provClockNowStart;
  const provIntervals = [];
  const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true, url: "http://127.0.0.1:1/s/token/#/", virtualConsole: vc });
  const w = dom.window;
  w.Date = class extends Date {
    constructor(...args) { super(...(args.length ? args : [provNow])); }
    static now() { return provNow; }
  };
  w.setInterval = (fn) => { provIntervals.push(fn); return provIntervals.length; };
  w.clearInterval = () => {};
  w.fetch = async (url, opts) => {
    const path = String(url).split("?")[0].replace(/^.*\/s\/[^/]+\//, "");
    if (opts && opts.method === "POST") { errs.push("unsolicited POST: " + path); return { ok: false, status: 500, json: async () => ({}), text: async () => "{}" }; }
    let payload;
    if (path === "api/v1/server") payload = { schemaVersion: 1, servedBy: "panel", version: "v0.3.0-dev", changes: "off", accountQuota: true, accountQuotaProviders: ["codex", "claude-code", "antigravity"] };
    else if (path === "api/v1/dock") payload = clone(provDock);
    else if (path === "api/v1/usage") payload = clone(fixture.usage);
    return { ok: payload !== undefined, status: payload === undefined ? 404 : 200, json: async () => clone(payload), text: async () => JSON.stringify(payload) };
  };
  w.TextEncoder = TextEncoder; w.scrollTo = () => {}; w.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} }); w.Element.prototype.scrollIntoView = () => {};
  w.eval(js);
  const settle = () => new Promise((r) => setTimeout(r, 150));
  await settle();

  // Check Home Setup Map cards do not claim Measured and reject malformed provenance.
  const colAcctsText = w.document.getElementById("col-accounts")?.textContent || "";
  if (colAcctsText.includes("Measured") || colAcctsText.includes("Older snapshot")) errs.push("Home map showed legacy Measured/Older snapshot claim: " + colAcctsText);
  if (!colAcctsText.includes("Retrieved (provider freshness unknown)") || !colAcctsText.includes("Local read (provider freshness unknown)")) errs.push("Home map missing explicit provider-freshness-unknown labels");
  if (!colAcctsText.includes("Quota rejected · invalid provenance metadata") || colAcctsText.includes("77%")) errs.push("Home map failed to reject malformed provenance card cleanly");

  // Navigate to #/accounts and inspect summary strip + expanded v1/legacy/malformed cards.
  w.location.hash = "#/accounts"; w.dispatchEvent(new w.Event("hashchange")); await settle();
  const stripText = w.document.querySelector(".acct-summary-strip")?.textContent || "";
  if (!stripText.includes("Recent local read · provider time unknown") || !stripText.includes("Older local read · provider time unknown") || !stripText.includes("Window ended, unavailable, or rejected") || stripText.includes("Fresh measured quota")) {
    errs.push("Accounts summary strip wrong provenance buckets: " + stripText);
  }
  const acctsBody = w.document.body.textContent;
  for (const leaked of ["SHOULD_NEVER_APPEAR_BAD_VERSION", "SHOULD_NEVER_APPEAR_NONNULL_PROVIDER", "SHOULD_NEVER_APPEAR_SUBMS_CONFLICT", "SHOULD_NEVER_APPEAR_PRESENT_NULL", "77%"]) {
    if (acctsBody.includes(leaked)) errs.push("malformed provenance card leaked content: " + leaked);
  }
  const acctsHtmlCards = [...w.document.querySelectorAll(".account-card")];
  for (const li of acctsHtmlCards) li.querySelector("details").open = true;
  if (!w.document.body.textContent.includes("Provider observation time is unavailable (provenance v1; local retrieval time only)")) errs.push("expanded v1 provenance notice missing");
  if (!w.document.body.textContent.includes("Provider observation time is unavailable (legacy local read timestamp)")) errs.push("expanded legacy fallback notice missing");
  if (!w.document.body.textContent.includes("Quota provenance metadata was invalid or unsupported. No usage is assumed.")) errs.push("expanded malformed rejection message missing");

  // Clock advancement across reset and collection state changes keep retrieval provenance untouched.
  provNow = Date.parse("2030-03-18T00:00:00Z");
  provIntervals.forEach((fn) => fn());
  const firstWin = acctsHtmlCards[0].querySelectorAll(".quota-window")[0];
  if (!firstWin || !firstWin.textContent.includes("Window ended · Last reading")) errs.push("clock advancement across reset failed to end v1 window");
  if (firstWin && firstWin.querySelector("meter:not([hidden])")) errs.push("ended v1 window left active meter visible");

  // Future device clock relative to v1 retrievedAt displays Time unavailable, never recent.
  provNow = Date.parse("2030-03-04T10:00:00Z");
  provIntervals.forEach((fn) => fn());
  if (!w.document.body.textContent.includes("Time unavailable · device clock is before the reading")) errs.push("future clock relative to retrievedAt missing safety warning");

  for (const colState of ["off", "waiting", "enabled", "backoff"]) {
    provDock.accounts.cards[0].usageUpdating = { state: colState, text: "State " + colState };
    w.document.getElementById("refresh").click(); await settle();
    if (!w.document.body.textContent.includes("Provider observation time is unavailable (provenance v1; local retrieval time only)")) errs.push("collection mode " + colState + " erased v1 provenance notice");
  }

  if (w.__renderError) errs.push(String(w.__renderError));
  dom.window.close();
  for (const e of errs) failures.push("quota-provenance: " + e);
}

if (failures.length) {
  console.error(`dock.js failed while rendering ${failures.length} time(s):`);
  for (const f of failures.slice(0, 30)) console.error("  " + f);
  process.exit(1);
}
console.log(`dock.js rendered ${scenarios.length} scenarios on every page without an exception`);
