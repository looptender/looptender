import { test } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "https://example.com" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
const { cleanup, render, within } = await import("@testing-library/react");
const { loadPluginApp, renderSlot } = await import("@get-bb/plugin-sdk/testing/app");
const { cutAtWord, firstSentenceOf } = await import("./status-details.ts");
const { heldSentence, phoneResumeUrl, PhoneResumeLink } = await import("./held-card.tsx");
const React = await import("react");
const app = await loadPluginApp(() => import("./app.tsx"));
const seg = app.threadContextSegments[0]!;
const base = { readAt: "2026-01-01T00:00:00Z", completed: false, targetThread: "thr_example", targetAdapter: "bb", targetConfigured: true, lastCheck: null,
  runtime: { state: "recent", source: "state", freshness: "recent", recordedBuild: "v0.2.0-dev.aaaaaaa", installedBuild: "v0.2.0-dev.aaaaaaa", binaryComparison: "match" }, daemon: { state: "responsive", binaryComparison: "match" } };
const loop = (over: object = {}, d: object = {}) => ({ loop: "example-loop", configPath: "/private/example/looptender.yaml", controlState: "enabled", note: null, target: "primary", supervising: true, lastState: "ok", lastWakeAt: "0001-01-01T00:00:00Z", checkedAt: null, handoffs: 0, fallbacks: null, referenceSource: "plugin_setting", details: { ...base, ...d }, ...over });
let n = 0;
const details = (loops: object[]) => renderSlot({ component: seg.details! }, { threadId: `thr_held_${++n}`, projectId: "proj_example", environment: null, segments: [], isExpanded: true, onToggle() {} }, { rpc: { forThread: () => ({ loops, errors: [] }) } });
const WHERE = "from the looptender panel (looptender panel), or from a terminal outside BB with looptender resume";

test("a held loop says it once, in its own state's words, with the same structure; the command is in Details", async () => {
  const cases: [string, string][] = [
    ["awaiting user", `Held for you. Resume it ${WHERE}.`],
    ["paused", `Paused. Resume it ${WHERE}.`],
    ["stop requested", `Stop requested. It stays stopped until a person resumes it ${WHERE}; resuming lifts the stop.`],
    ["completed", `Marked finished. Resuming it reopens it ${WHERE}.`],
  ];
  for (const [controlState, sentence] of cases) {
    const ui = details([loop({ controlState })]);
    try {
      const section = within(await ui.findByRole("region", { name: "Loop example-loop" }));
      assert.ok(section.getByText(sentence), controlState);
      assert.equal(ui.container.textContent?.includes("A person resumes this from an ordinary terminal"), false);
      // Never a Resume button; the command sits in Details, not in the sentence.
      assert.equal(ui.queryByRole("button", { name: /resume/i }), null);
      const command = ui.container.querySelector("details code") as HTMLElement;
      assert.equal(command.textContent, 'looptender resume -c "/private/example/looptender.yaml"');
      assert.equal(ui.container.querySelector(".kg-actions code"), null, "no command in the sentence");
      assert.equal(heldSentence(loop({ controlState }) as never), sentence);
    } finally { ui.lifecycle.unmount(); cleanup(); }
  }
  assert.equal(heldSentence(loop() as never), null);
});

test("the stop sentence differs from the pause sentence: a stop is not resumed like a pause", () => {
  const stop = heldSentence(loop({ controlState: "stop requested" }) as never)!;
  assert.notEqual(stop, heldSentence(loop({ controlState: "paused" }) as never));
  assert.match(stop, /stays stopped until a person resumes it .*resuming lifts the stop/);
  assert.equal(heldSentence(loop({ controlState: "disabled" }) as never), "Switched off. Turn it on from the looptender panel (looptender panel), or from a terminal outside BB with looptender enable.");
});

test("the phone path slot renders nothing until a phone path is configured, and is a link, never a button", async () => {
  assert.equal(phoneResumeUrl(loop() as never), undefined);
  const none = render(React.createElement(PhoneResumeLink, { loop: loop() as never }));
  assert.equal(none.container.innerHTML, ""); none.unmount();
  const some = render(React.createElement(PhoneResumeLink, { loop: loop() as never, url: "https://example.com/resume-here" }));
  const link = some.container.querySelector("a")!;
  assert.equal(link.getAttribute("href"), "https://example.com/resume-here"); assert.equal(link.textContent, "Resume from your phone");
  assert.equal(some.container.querySelector("button"), null); some.unmount(); cleanup();
  const ui = details([loop({ controlState: "paused" })]);
  try { await ui.findByRole("region", { name: "Loop example-loop" }); assert.equal(ui.container.querySelector("a"), null, "nothing for the phone path yet"); } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("text is cut at a whole word with an ellipsis, or to its first sentence, never mid-word", () => {
  const t = "This supervisor was built before it could be asked to exit, so no command ends it: end its process yourself";
  for (const max of [20, 41, 60, 77]) { const c = cutAtWord(t, max); assert.ok(c.length <= max, c); assert.ok(c.endsWith("…")); assert.ok(t.startsWith(c.slice(0, -1).trimEnd()) && /\s/.test(t[c.slice(0, -1).trimEnd().length]!), `whole word: ${c}`); }
  assert.equal(cutAtWord("short", 20), "short");
  assert.equal(firstSentenceOf("End its process yourself. Until you start it again with a daemon; start would enable the loop."), "End its process yourself.");
  assert.ok(firstSentenceOf("word ".repeat(100), 40).endsWith("word…"));
});

test("an older-build note shows its first sentence on the card, whole, and the rest under Technical details", async () => {
  const action = "This supervisor was built before it could be asked to exit, so no command ends it: end its process yourself. Until you start it again with looptender daemon -c CONFIG in a terminal you leave open (it starts supervision with the loop's controls as they are, and ends with the terminal; start would ena";
  const ui = details([loop({ controlState: "paused" }, { buildFreshness: { state: "different", text: "Different build from the installed one", action } })]);
  try {
    const build = await ui.findByLabelText("Build");
    assert.match(build.textContent ?? "", /end its process yourself\.$/);
    assert.equal((build.textContent ?? "").includes("start would ena"), false, "the long tail is not on the card face");
    assert.ok(within(ui.getByRole("list", { name: "Technical details" })).getByText(/^Build note: This supervisor was built before/));
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("jargon lines are under Technical details, in plain words where they stay in Details", async () => {
  const ui = details([loop({ controlState: "paused", lastState: "error" })]);
  try {
    await ui.findByRole("region", { name: "Loop example-loop" });
    const plain = within(ui.getByRole("list", { name: "Details" })), tech = within(ui.getByRole("list", { name: "Technical details" }));
    for (const re of [/^Reference: /, /^Status read: /, /^Check evidence: /, /^Daemon answered: /, /^Last check state: /]) { assert.ok(tech.getByText(re), String(re)); }
    assert.equal(ui.container.textContent?.includes("the listing's last= field"), false);
    assert.equal(plain.queryByText(/^Check evidence: /), null); assert.equal(plain.queryByText(/^Daemon answered: /), null);
    assert.ok(plain.getByText(/^Current target: /)); assert.ok(plain.getByText(/^Last sent/));
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("a wait is not held for a person: its own sentence, the state said as a wait, no resume command or phone link", async () => {
  const waiting = loop({ controlState: "awaiting user", note: "waiting" }, { wait: { until: "limit-reset", expiresAt: "2099-01-01T00:00:00Z" } });
  assert.equal(heldSentence(waiting as never), "Waiting for a usage-limit reset. It carries on by itself; nothing to do.");
  for (const [until, phrase] of [["host-session", "the login session"], ["network", "the network"], ["time:2099-01-01T00:00:00Z", "a time"], ["odd", "a condition"]]) {
    assert.equal(heldSentence(loop({ controlState: "awaiting user" }, { wait: { until } }) as never), `Waiting for ${phrase}. It carries on by itself; nothing to do.`);
  }
  const ui = details([waiting]);
  try {
    const section = within(await ui.findByRole("region", { name: "Loop example-loop" }));
    assert.ok(section.getByText("Waiting for a usage-limit reset. It carries on by itself; nothing to do."));
    assert.equal(ui.container.textContent?.includes("Held for you"), false);
    assert.equal(ui.container.querySelector("code"), null, "nothing to run");
  } finally { ui.lifecycle.unmount(); cleanup(); }
  // Without a wait record the same control state is a hold for a person.
  assert.match(heldSentence(loop({ controlState: "awaiting user" }) as never)!, /^Held for you\./);
});

test("the phone link takes only an https URL", () => {
  for (const url of ["http://example.com/x", "javascript:alert(1)", "data:text/html,x", "//example.com", "not a url", ""]) {
    const r = render(React.createElement(PhoneResumeLink, { loop: loop() as never, url })); assert.equal(r.container.innerHTML, "", url); r.unmount();
  }
  const ok = render(React.createElement(PhoneResumeLink, { loop: loop() as never, url: "https://example.com/r" })); assert.ok(ok.container.querySelector("a")); ok.unmount(); cleanup();
});

test("cutAtWord never splits a character; firstSentenceOf skips abbreviations and short openings", () => {
  const e = "😀 ".repeat(10).trim();
  for (let max = 2; max < 22; max++) { const c = cutAtWord(e, max); assert.equal(/[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/.test(c), false, `${max}: ${JSON.stringify(c)}`); }
  assert.equal(cutAtWord("😀😀😀😀😀😀", 4).includes("\ud83d…"), false);
  assert.equal(firstSentenceOf("See e.g. the docs for the long form. Then go on."), "See e.g. the docs for the long form.");
  assert.equal(firstSentenceOf("Dr. Smith is away until Monday. Wait for them."), "Dr. Smith is away until Monday.");
  assert.equal(firstSentenceOf("Ok. Then restart the supervisor from a terminal you leave open."), "Ok. Then restart the supervisor from a terminal you leave open.");
  assert.equal(firstSentenceOf("Restart it. Then wait."), "Restart it.");
});

test("Goal and Task completion are plain facts in Details, not under Technical details", async () => {
  const ui = details([loop({ controlState: "paused" })]);
  try {
    await ui.findByRole("region", { name: "Loop example-loop" });
    const plain = within(ui.getByRole("list", { name: "Details" })), tech = within(ui.getByRole("list", { name: "Technical details" }));
    assert.ok(plain.getByText(/^Goal: /)); assert.ok(plain.getByText(/^Task completion: /));
    assert.equal(tech.queryByText(/^Goal: /), null); assert.equal(tech.queryByText(/^Task completion: /), null);
  } finally { ui.lifecycle.unmount(); cleanup(); }
});
