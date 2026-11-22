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
variants.push(["no next check, no decision, no last check", (l) => { delete l.nextCheck; delete l.decision; delete l.lastCheck; }]);
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
    fn(l);
    d.loops.push(l);
  });
}]);
// Each variant on every real loop, one report each.
for (const [name, fn] of variants) {
  scenarios.push([`${name} on every loop`, (d) => { for (const l of d.loops) fn(l); }]);
}
// Long and awkward need notes.
scenarios.push(["long and awkward notes", (d) => {
  for (const n of d.needsYou || []) { n.note = long + " docs/reference/FILE.md http://localhost:8080/x " + "x".repeat(400); n.noteMasked = true; }
}]);
scenarios.push(["no needs", (d) => { d.needsYou = []; d.needsLine = "Nothing needs you"; d.allClear = true; }]);

const routes = ["#/", "#/versions", "#/usage", "#/routes", "#/accounts"];

async function render(name, data) {
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
  }
  w.close();
  for (const e of new Set(errors)) failures.push(`${name}: ${e.split("\n").slice(0, 3).join(" | ")}`);
}

for (const [name, fn] of scenarios) await render(name, scenario(fn));

if (failures.length) {
  console.error(`dock.js failed while rendering ${failures.length} time(s):`);
  for (const f of failures.slice(0, 30)) console.error("  " + f);
  process.exit(1);
}
console.log(`dock.js rendered ${scenarios.length} scenarios on every page without an exception`);
