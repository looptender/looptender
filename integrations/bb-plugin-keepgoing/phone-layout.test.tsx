import { test } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "https://example.com" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
// A phone: the query that says a finger or a narrow screen matches.
let phone = true;
Object.defineProperty(globalThis, "matchMedia", { value: () => ({ matches: phone }), configurable: true, writable: true });
const { cleanup, within } = await import("@testing-library/react");
const { loadPluginApp, renderSlot } = await import("@get-bb/plugin-sdk/testing/app");
const app = await loadPluginApp(() => import("./app.tsx"));
const seg = app.threadContextSegments[0]!;
const base = { readAt: "2026-01-01T00:00:00Z", completed: false, targetThread: "thr_example", targetAdapter: "bb", targetConfigured: true, lastCheck: null,
  runtime: { state: "recent", source: "state", freshness: "recent", recordedBuild: "v0.2.0-dev.aaaaaaa", installedBuild: "v0.2.0-dev.aaaaaaa", binaryComparison: "match" }, daemon: { state: "responsive", binaryComparison: "match" } };
const loop = (over: object = {}, d: object = {}) => ({ loop: "example-loop", configPath: "/private/example/looptender.yaml", controlState: "enabled", note: null, target: "primary", supervising: true, lastState: "ok", lastWakeAt: "0001-01-01T00:00:00Z", checkedAt: null, handoffs: 0, fallbacks: null, referenceSource: "plugin_setting", details: { ...base, ...d }, ...over });
let n = 0;
const mount = (loops: object[]) => {
  const props = { threadId: `thr_phone_${++n}`, projectId: "proj_example", environment: null, segments: [], isExpanded: true, onToggle() {} };
  const rpc = { forThread: () => ({ loops, errors: [] }) };
  return { summary: renderSlot({ component: seg.summary }, props, { rpc }), details: renderSlot({ component: seg.details! }, props, { rpc }) };
};
const done = (...uis: { lifecycle: { unmount(): void } }[]) => { for (const u of uis) u.lifecycle.unmount(); cleanup(); };

test("the collapsed chip says the state and, when a person is needed, the reason, in words and not only in a tooltip", async () => {
  const cases: [object, string][] = [
    [loop({}, { disk: { level: "low", message: "the volume is filling" } }), "looptender: enabled"],
    [loop({ controlState: "awaiting user", note: "Needs a decision in /private/example/path." }), "looptender: awaiting user"],
    [loop({ controlState: "awaiting user", note: "x" }, { deadline: { at: "2026-01-01T00:00:00Z", reached: true } }), "looptender: awaiting user · deadline passed"],
    [loop({}, { sends: { max: 40, used: 40, remaining: 0, reached: true } }), "looptender: enabled · send limit reached"],
    [loop({}, { limitWait: { state: "waiting", reason: "limit_no_fallback", resetsAt: "2099-01-01T00:00:00Z", resetPassed: false } }), "looptender: enabled · waiting on a usage limit"],
    [loop({}), "looptender: enabled"],
    // One reason, the one that matters most now: a limit before a passed deadline before a build difference.
    [loop({ controlState: "paused" }, { deadline: { at: "2026-01-01T00:00:00Z", reached: true }, limitWait: { state: "waiting", reason: "limit_no_fallback", resetsAt: "2099-01-01T00:00:00Z", resetPassed: false }, runtime: { ...base.runtime, recordedBuild: "v0.2.0-dev.1111111", binaryComparison: "different" } }), "looptender: paused · deadline passed"],
    [loop({ controlState: "paused" }, { sends: { max: 40, used: 40, remaining: 0, reached: true }, deadline: { at: "2026-01-01T00:00:00Z", reached: true }, disk: { level: "low", message: "x" } }), "looptender: paused · send limit reached"],
    [loop({ controlState: "paused" }, { disk: { level: "low", message: "x" }, limitWait: { state: "waiting", reason: "limit_no_fallback", resetsAt: "2099-01-01T00:00:00Z", resetPassed: false } }), "looptender: paused · waiting on a usage limit"],
    [loop({ controlState: "paused" }, { limitWait: { state: "waiting", reason: "limit_no_fallback", resetsAt: "2099-01-01T00:00:00Z", resetPassed: false }, runtime: { ...base.runtime, recordedBuild: "v0.2.0-dev.1111111", binaryComparison: "different" } }), "looptender: paused · waiting on a usage limit"],
    [loop({ controlState: "awaiting user" }, { wait: { until: "network" } }), "looptender: waiting for the network"],
    [loop({ controlState: "paused" }, { deadline: { at: "2026-01-01T00:00:00Z", reached: true }, runtime: { ...base.runtime, recordedBuild: "v0.2.0-dev.1111111", binaryComparison: "different" } }), "looptender: paused · deadline passed"],
  ];
  for (const [l, text] of cases) {
    const ui = mount([l]);
    try {
      const chip = await ui.summary.findByRole("button", { name: text });
      assert.ok((chip.textContent ?? "").length <= 64, `chip stays short: ${chip.textContent}`);
      // Short enough to sit beside BB's own chips: no version, which the details carry.
      assert.equal(/v0\.2\.0/.test(chip.textContent ?? ""), false);
    } finally { done(ui.summary, ui.details); }
  }
});

test("a differing build says so in two words on the chip; a long note is cut", async () => {
  const ui = mount([loop({ controlState: "paused" }, { runtime: { ...base.runtime, recordedBuild: "v0.2.0-dev.1111111", binaryComparison: "different" } })]);
  try { await ui.summary.findByRole("button", { name: "looptender: paused · build differs" }); } finally { done(ui.summary, ui.details); }
  const long = mount([loop({ controlState: "awaiting user", note: "SECRET-PATH /private/example " .repeat(8) })]);
  try {
    const chip = await long.summary.findByRole("button", { name: "looptender: awaiting user" });
    assert.equal(/SECRET-PATH/.test(chip.title), false, "the raw note is not in the chip's title");
    assert.equal(/SECRET-PATH/.test(long.summary.container.textContent ?? ""), false);
    assert.ok(await long.details.findByText(/SECRET-PATH/), "the expanded details carry the note");
  } finally { done(long.summary, long.details); }
});

test("phone rules: 44 px targets and readable text under a coarse pointer or narrow screen, applied to every plugin control", async () => {
  const ui = mount([loop()]);
  try {
    const pause = await ui.details.findByRole("button", { name: "Pause… example-loop" });
    const css = ui.details.container.querySelector("style")?.textContent ?? "";
    assert.match(css, /@media \(max-width: 640px\), \(pointer: coarse\)/);
    assert.match(css, /button\[data-kg\]:not\(\.kgs-chip\) \{ min-height: 44px !important/);
    assert.match(css, /summary\[data-kg\] \{[^}]*min-height: 44px/);
    for (const el of [pause, ui.details.container.querySelector("summary"), await ui.summary.findByRole("button", { name: /^looptender:/ })]) assert.ok(el?.hasAttribute("data-kg"), "styled by the phone rules");
    assert.ok(ui.summary.container.querySelector("style")?.textContent?.includes("min-height: 44px"), "the collapsed chip carries the rules too");
  } finally { done(ui.summary, ui.details); }
});

test("expanded banner: reason and Pause first, full-width under the text; the rest behind Details, closed on a phone and open on a wide screen", async () => {
  const l = loop({ controlState: "enabled" }, { disk: { level: "low", message: "the volume that holds the state directory is filling" } });
  phone = true;
  const narrow = mount([l]);
  try {
    const section = await narrow.details.findByRole("region", { name: "Loop example-loop" });
    assert.ok(within(section).getByText(/State-volume disk:.*the volume that holds the state directory is filling/));
    assert.equal((section.querySelector("details") as HTMLDetailsElement).open, false);
    const order = [...section.querySelectorAll("[aria-label=Why], .kg-actions, :scope > details")].map(e => e.tagName === "DETAILS" ? "details" : e.getAttribute("aria-label") ?? e.className);
    assert.deepEqual(order, ["Why", "kg-actions", "details"]);
    // Pause is not beside the text in a flex row.
    assert.equal((section as HTMLElement).style.display, "grid");
    assert.ok(section.querySelector(".kg-actions button")?.textContent === "Pause…");
  } finally { done(narrow.summary, narrow.details); }
  phone = false;
  const wide = mount([l]);
  try { const section = await wide.details.findByRole("region", { name: "Loop example-loop" }); assert.equal((section.querySelector("details") as HTMLDetailsElement).open, true); } finally { done(wide.summary, wide.details); phone = true; }
});

test("long names, versions and paths wrap: every text container allows a break anywhere, and the resume command can shrink", async () => {
  const long = loop({ loop: "a-very-long-loop-name-with-no-natural-break-points-".repeat(3) + "end", configPath: "/private/example/" + "very-long-directory-name/".repeat(8) + "looptender.yaml", controlState: "awaiting user" });
  const ui = mount([long]);
  try {
    const chip = await ui.summary.findByRole("button", { name: /^looptender: awaiting user/ });
    assert.equal((chip as HTMLElement).style.whiteSpace, "nowrap");
    assert.equal((chip as HTMLElement).style.overflow, "hidden");
    assert.equal((chip as HTMLElement).style.textOverflow, "ellipsis");
    assert.equal((chip as HTMLElement).style.minWidth, "0px");
    assert.equal((chip as HTMLElement).style.maxWidth, "100%");
    assert.equal((chip as HTMLElement).style.flexShrink, "1");
    assert.equal((chip as HTMLElement).style.overflowWrap, "");
    assert.equal((chip as HTMLElement).style.paddingLeft, "8px");
    const chipText = chip.querySelector("span") as HTMLElement;
    assert.ok(chipText);
    assert.equal(chipText.style.whiteSpace, "nowrap");
    assert.equal(chipText.style.overflow, "hidden");
    assert.equal(chipText.style.textOverflow, "ellipsis");
    assert.equal(chipText.style.minWidth, "0px");
    assert.equal(chipText.style.maxWidth, "100%");
    const css = ui.summary.container.querySelector("style")?.textContent ?? "";
    assert.match(css, /button\[data-kg=chip\] \{ padding: 0 0 0 8px !important; min-width: 0; \}/);
    const section = await ui.details.findByRole("region", { name: /Loop a-very-long/ });
    assert.equal((section as HTMLElement).style.overflowWrap, "anywhere");
    const sentence = section.querySelector(".kg-actions .kg-t") as HTMLElement;
    assert.match(sentence.textContent ?? "", /^Held for you\. Resume it from the looptender panel \(looptender panel\), or from a terminal outside BB with looptender resume\./);
    assert.equal(sentence.style.overflowWrap, "anywhere"); assert.equal(sentence.style.minWidth, "0px");
    // The command, a long path, is in Details, in a list that may break anywhere.
    assert.equal((section.querySelector("details code") as HTMLElement).textContent?.startsWith("looptender resume -c "), true);
    assert.equal((section.querySelector("details ul") as HTMLElement).style.overflowWrap, "anywhere");
  } finally { done(ui.summary, ui.details); }
});

test("two Pause buttons of a confirm group are separated, and each is a target", async () => {
  const ui = mount([loop({ supervising: false }, { targetThread: "thr_other" })]);
  try {
    const open = await ui.details.findByRole("button", { name: "Pause… example-loop" });
    open.click();
    const group = await ui.details.findByRole("group", { name: "Confirm pausing example-loop" });
    const row = group.querySelector(".kg-actions") as HTMLElement;
    assert.equal(row.style.gap, "8px");
    assert.deepEqual([...row.querySelectorAll("button")].map(b => b.textContent), ["Pause loop example-loop", "Cancel"]);
  } finally { done(ui.summary, ui.details); }
});

test("on a phone Pause is two steps even for this thread's own loop; on a wide screen it is one", async () => {
  phone = true;
  const ui = mount([loop()]);
  const calls: unknown[] = [];
  try {
    const first = await ui.details.findByRole("button", { name: "Pause… example-loop" });
    first.click();
    const group = await ui.details.findByRole("group", { name: "Confirm pausing example-loop" });
    assert.deepEqual([...group.querySelectorAll("button")].map(b => b.textContent), ["Pause loop example-loop", "Cancel"]);
    assert.match(group.textContent ?? "", /resumes it from a terminal/);
  } finally { done(ui.summary, ui.details); }
  phone = false;
  const wide = mount([loop()]);
  try { assert.ok(await wide.details.findByRole("button", { name: "Pause example-loop" })); } finally { done(wide.summary, wide.details); phone = true; }
  void calls;
});

test("checks have stopped omits build differs on the collapsed chip while keeping build details in the expanded card", async () => {
  const diffRuntime = { ...base.runtime, recordedBuild: "v0.2.0-dev.1111111", binaryComparison: "different" };
  const diffFreshness = { state: "different", text: "Different build from the installed one", action: "Start checks to run the installed build." };
  for (const daemonState of ["not_recorded", "responsive"]) {
    const ui = mount([loop({ controlState: "enabled" }, { schedule: { overdue: true, message: "No check has run for 2 hours." }, daemon: { state: daemonState }, runtime: diffRuntime, buildFreshness: diffFreshness })]);
    try {
      const chip = await ui.summary.findByRole("button", { name: "looptender: checks have stopped" });
      assert.equal(chip.textContent, "looptender: checks have stopped");
      assert.equal((chip.textContent ?? "").includes("build differs"), false);
      assert.ok(await ui.details.findByText("Different build from the installed one"));
      assert.ok(within(ui.details.getByRole("list", { name: "Details" })).getByText(/^Build: last check ran v0\.2\.0-dev\.1111111 · installed v0\.2\.0-dev\.aaaaaaa/));
    } finally { done(ui.summary, ui.details); }
  }

  for (const [label, candidate, expected] of [
    ["enabled with stopped daemon", loop({ controlState: "enabled" }, { daemon: { state: "not_recorded" }, runtime: diffRuntime }), "looptender: enabled"],
    ["completed", loop({ controlState: "completed" }, { runtime: diffRuntime }), "looptender: loop completion declared"],
    ["moved", loop({ controlState: "awaiting user" }, { moved: { held: true, text: "Moved." }, runtime: diffRuntime }), "looptender: moved, resume to start"],
    ["watching", loop({ controlState: "enabled", supervising: false }, { runtime: diffRuntime }), "looptender: watching thr_example"],
    ["enabled with responsive daemon", loop({ controlState: "enabled" }, { daemon: { state: "responsive" }, runtime: diffRuntime }), "looptender: enabled · build differs"],
  ] as const) {
    const ui = mount([candidate]);
    try {
      await ui.summary.findByRole("button", { name: expected });
    } finally {
      done(ui.summary, ui.details);
    }
    void label;
  }
});

test("every composer-bar chip variant shares the single-line ellipsis and left-spacing constraints", async () => {
  const assertSingleLineChip = (chip: HTMLElement) => {
    assert.equal(chip.style.whiteSpace, "nowrap");
    assert.equal(chip.style.overflow, "hidden");
    assert.equal(chip.style.textOverflow, "ellipsis");
    assert.equal(chip.style.minWidth, "0px");
    assert.equal(chip.style.maxWidth, "100%");
    assert.equal(chip.style.flexShrink, "1");
    assert.equal(chip.style.paddingLeft, "8px");
    assert.equal(chip.style.overflowWrap, "");
    const text = chip.querySelector("span") as HTMLElement;
    assert.ok(text);
    assert.equal(text.style.whiteSpace, "nowrap");
    assert.equal(text.style.overflow, "hidden");
    assert.equal(text.style.textOverflow, "ellipsis");
    assert.equal(text.style.minWidth, "0px");
    assert.equal(text.style.maxWidth, "100%");
  };
  const props = (id: string) => ({ threadId: id, projectId: "proj_example", environment: null, segments: [], isExpanded: false, onToggle() {} });
  const variants: [string, Record<string, () => unknown>, string][] = [
    ["covered child", { forThread: () => ({ loops: [], errors: [] }), newThread: () => ({ ok: true, message: "", outcome: { state: "none", message: "" } }), lineage: () => ({ child: true, coveredBy: { threadId: "thr_parent_example", loop: "looptender-coordinator-example" } }) }, "Covered by looptender-coordinator-example"],
    ["bare child", { forThread: () => ({ loops: [], errors: [] }), newThread: () => ({ ok: true, message: "", outcome: { state: "none", message: "" } }), lineage: () => ({ child: true }) }, "looptender"],
    ["add entry", { forThread: () => ({ loops: [], errors: [] }), newThread: () => ({ ok: true, message: "", outcome: { state: "none", message: "" } }), lineage: () => ({ child: false }) }, "Add to Looptender"],
    ["checking status", { forThread: () => new Promise(() => {}), newThread: () => ({ ok: true, message: "", outcome: { state: "none", message: "" } }), lineage: () => ({ child: false }) }, "looptender: checking status"],
    ["status unavailable", { forThread: () => { throw new Error("offline"); }, newThread: () => ({ ok: true, message: "", outcome: { state: "none", message: "" } }), lineage: () => ({ child: false }) }, "looptender: status unavailable"],
    ["multiple loops", { forThread: () => ({ loops: [loop({ loop: "one", configPath: "/private/example/1.yaml" }), loop({ loop: "two", configPath: "/private/example/2.yaml" })], errors: [] }), lineage: () => ({ child: false }) }, "looptender: 2 matching loops"],
    ["observed never woken", { forThread: () => ({ loops: [loop({ supervising: false }, { targetAdapter: "sessionpipe", targetThread: null, observed: true, targetSession: "claude-code:example" })], errors: [] }), lineage: () => ({ child: false }) }, "looptender: observed"],
    ["observed wake_only", { forThread: () => ({ loops: [loop({ supervising: false }, { targetAdapter: "sessionpipe", targetThread: null, observed: true, observedWakes: true, targetSession: "claude-code:example" })], errors: [] }), lineage: () => ({ child: false }) }, "looptender: observed (idle wakes)"],
  ];
  for (const [kind, rpc, name] of variants) {
    const ui = renderSlot({ component: seg.summary }, props(`thr_chip_${++n}`), { rpc });
    try {
      const chip = await ui.findByRole("button", { name });
      assertSingleLineChip(chip);
    } finally {
      done(ui);
    }
    void kind;
  }
});

test("observed sessionpipe loops show their role in expanded details, use Pause checking, and omit Stop", async () => {
  const obsLoop = loop(
    { supervising: false, controlState: "enabled" },
    { targetAdapter: "sessionpipe", targetThread: null, observed: true, observedWakes: false, targetSession: "claude-code:8c132906" },
  );
  const ui = mount([obsLoop]);
  try {
    await ui.summary.findByRole("button", { name: "looptender: observed" });
    await ui.details.findByText("Observed, never woken");
    await ui.details.findByText(/Current target: claude-code:8c132906 \(primary\); Observed, never woken/);
    assert.ok(await ui.details.findByRole("button", { name: "Pause checking… example-loop" }));
    assert.equal(ui.details.queryByRole("button", { name: /Stop/i }), null);
  } finally {
    done(ui.summary, ui.details);
  }
});
