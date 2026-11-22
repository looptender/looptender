import { test } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "https://example.com" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
const { cleanup, within } = await import("@testing-library/react");
const { loadPluginApp, renderSlot } = await import("@get-bb/plugin-sdk/testing/app");
const app = await loadPluginApp(() => import("./app.tsx"));
const details = { readAt: "2026-01-01T00:00:00Z", completed: false, targetThread: "thr_example", targetAdapter: "bb", targetConfigured: true, runtime: null, daemon: null, lastCheck: null };
const loop = (over: object = {}, d: object = {}) => ({ loop: "example-loop", configPath: "/private/example/looptender.yaml", controlState: "enabled", note: null, target: "primary", supervising: true, lastState: null, lastWakeAt: "0001-01-01T00:00:00Z", checkedAt: null, handoffs: 0, fallbacks: null, referenceSource: "plugin_setting", details: { ...details, ...d }, ...over });
const wait = { state: "waiting", reason: "limit_no_fallback", resetsAt: "2026-01-06T00:00:00Z", resetIn: "5 days", resetPassed: false };
let n = 0;
const card = (loops: object[]) => renderSlot(app.threadContextCardRows[0]!, { threadId: `thr_vis_${++n}`, projectId: "proj_example", environment: null }, { rpc: { forThread: () => ({ loops, errors: [] }) } });
const banner = (loops: object[], part: "summary" | "details") => renderSlot({ component: part === "summary" ? app.threadContextSegments[0]!.summary : app.threadContextSegments[0]!.details! }, { threadId: `thr_vis_${++n}`, projectId: "proj_example", environment: null, segments: [], isExpanded: true, onToggle() {} }, { rpc: { forThread: () => ({ loops, errors: [] }) } });

test("a loop in a long limit wait shows the wait on the card, not only in the tooltip", async () => {
  const ui = card([loop({}, { limitWait: wait, deadline: { at: "2026-01-03T00:00:00Z", reached: false } })]);
  try {
    await ui.findByText(/^looptender: enabled/);
    const why = within(ui.getByRole("list", { name: "Why" }));
    assert.ok(why.getByText(/Long limit wait: no handoff possible; resets .* \(in 5 days\); the loop sends nothing until then; the reset falls after this loop's deadline/));
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("deadline reached, send limit reached, low disk and a held note are visible on the card", async () => {
  const ui = card([loop({ controlState: "awaiting user", note: "Needs a decision on scope." }, { deadline: { at: "2026-01-01T00:00:00Z", reached: true }, sends: { max: 40, used: 40, remaining: 0, reached: true }, disk: { level: "low", message: "the volume that holds the state directory is filling (900 MiB free of 100 GiB, below 2.0 GiB): free space on it before records fail to save" }, schedule: { overdue: true, message: "No check has run for 3 hours." } })]);
  try {
    const why = within(await ui.findByRole("list", { name: "Why" }));
    for (const re of [/^Deadline reached \(.*\); evaluated on the next check$/, /^Send limit reached: 40\/40 attempts reserved; 0 remaining$/, /^Disk: the volume that holds the state directory is filling/, /^Needs you: No check has run for 3 hours\.$/, /^Note: Needs a decision on scope\.$/]) assert.ok(why.getByText(re), String(re));
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("a loop with an unreadable record says so, and offers no pause or resume", async () => {
  const ui = card([loop({ controlState: "disabled" }, { unreadable: [{ file: "control.json", problem: "not_json", remedy: "Checks send nothing, and pause, stop and resume cannot be saved, until it is repaired. Stop the daemon, then restore it." }] })]);
  try {
    await ui.findByText("looptender: records unreadable");
    assert.ok(ui.getByText("control.json could not be read (its content is not JSON). Checks send nothing, and pause, stop and resume cannot be saved, until it is repaired."));
    assert.equal(ui.queryByRole("button", { name: /pause/i }), null); assert.equal(ui.queryByText(/A person resumes/), null);
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("with several held loops the resume sentence is said once and each loop keeps its own command", async () => {
  const held = (name: string) => loop({ loop: name, configPath: `/private/example/${name}/looptender.yaml`, controlState: "paused" });
  const ui = card([held("first-example"), held("second-example"), held("third-example"), loop({ loop: "fourth-example", configPath: "/private/example/fourth/looptender.yaml", controlState: "superseded" })]);
  try {
    await ui.findByText(/^A person resumes a held loop from an ordinary terminal/);
    assert.equal(ui.getAllByText(/A person resumes/).length, 1);
    assert.equal(ui.container.querySelectorAll("code").length, 3, "one command per held loop; none for the superseded one");
    assert.ok(ui.getByText(/Resume is refused for a superseded loop/));
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("the expanded banner is a labelled list per loop with the state word once, and its tooltip is bounded", async () => {
  const loops = Array.from({ length: 14 }, (_, i) => loop({ loop: `example-${i}`, configPath: `/private/example/${i}/looptender.yaml`, controlState: i % 2 ? "awaiting user" : "enabled", note: "n".repeat(300) }, i % 2 ? {} : { limitWait: wait }));
  const summary = banner(loops, "summary");
  try {
    const button = await summary.findByRole("button", { name: "looptender: 14 matching loops" });
    assert.ok((button.getAttribute("title") ?? "").length <= 600);
  } finally { summary.lifecycle.unmount(); cleanup(); }
  const one = banner([loop({ controlState: "awaiting user", note: "Needs a decision." })], "details");
  try {
    const region = within(await one.findByRole("region", { name: "Loop example-loop" }));
    assert.ok(region.getByRole("list", { name: "Why" })); assert.ok(region.getByRole("list", { name: "Details" }));
    assert.equal((region.getByRole("list", { name: "Details" }).textContent ?? "").includes("Note:"), false, "the note is in the reasons only");
    assert.equal((one.container.textContent ?? "").split("looptender: awaiting user").length - 1, 1, "state word once");
  } finally { one.lifecycle.unmount(); cleanup(); }
});
