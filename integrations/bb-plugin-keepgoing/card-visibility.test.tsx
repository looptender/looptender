import { test } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "https://example.com" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
const { cleanup, fireEvent, waitFor, within } = await import("@testing-library/react");
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
    for (const re of [/^Deadline reached \(.*\); evaluated on the next check$/, /^Send limit reached: 40\/40 attempts reserved; 0 remaining$/, /^State-volume disk:.*the volume that holds the state directory is filling/, /^Needs you: No check has run for 3 hours\.$/, /^Note: Needs a decision on scope\.$/]) assert.ok(why.getByText(re), String(re));
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
    await ui.findAllByText("Paused. Resume it from the looptender panel (looptender panel), or from a terminal outside BB with looptender resume.");
    assert.equal(ui.getAllByText(/^Paused\. Resume it from the looptender panel \(looptender panel\), or from a terminal outside BB with looptender resume\.$/).length, 3, "each held loop says it once, in its own words");
    assert.equal(ui.container.querySelectorAll("code").length, 3, "one command per held loop, in its Details; none for the superseded one");
    assert.equal(ui.container.querySelectorAll("details code").length, 3);
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
    assert.equal((one.container.textContent ?? "").split("looptender: awaiting user").length - 1, 0, "the chip says the state; the details do not say it again");
  } finally { one.lifecycle.unmount(); cleanup(); }
});

test("records status could not describe still read as unreadable on the card, with no pause", async () => {
  const ui = card([loop({}, { unparsed: ["unreadable"] })]);
  try {
    await ui.findByText("looptender: records unreadable");
    assert.ok(ui.getByText("status reported records it could not read, and their details could not be read"));
    assert.equal(ui.queryByRole("button", { name: /pause/i }), null);
  } finally { ui.lifecycle.unmount(); cleanup(); }
});

test("Start checks button appears only on enabled, unheld, non-archived overdue loops without a running daemon", async () => {
  const overdue = loop({ controlState: "enabled" }, { schedule: { overdue: true, message: "No check has run for 2 hours." }, daemon: { state: "not_recorded" } });
  const ui = card([overdue]);
  try {
    await ui.findByText("looptender: checks have stopped");
    assert.ok(ui.getByRole("button", { name: "Start checks example-loop" }));
  } finally { ui.lifecycle.unmount(); cleanup(); }

  for (const [label, candidate] of [
    ["paused", loop({ controlState: "paused" }, { schedule: { overdue: true } })],
    ["stop requested", loop({ controlState: "stop requested" }, { schedule: { overdue: true } })],
    ["awaiting user", loop({ controlState: "awaiting user" }, { schedule: { overdue: true } })],
    ["completed", loop({ controlState: "completed" }, { schedule: { overdue: true } })],
    ["disabled", loop({ controlState: "disabled" }, { schedule: { overdue: true } })],
    ["superseded", loop({ controlState: "superseded" }, { schedule: { overdue: true } })],
    ["archived", loop({ controlState: "enabled" }, { schedule: { overdue: true }, archived: { at: "2026-01-01T00:00:00Z" } })],
    ["records unreadable", loop({ controlState: "enabled" }, { schedule: { overdue: true }, unparsed: ["unreadable"] })],
    ["daemon responsive", loop({ controlState: "enabled" }, { schedule: { overdue: true }, daemon: { state: "responsive" } })],
    ["imported", loop({ controlState: "enabled", imported: true }, { schedule: { overdue: true } })],
  ] as const) {
    const neg = card([candidate]);
    try {
      await neg.findByRole("region", { name: /Loop example-loop/ });
      assert.equal(neg.queryByRole("button", { name: /Start checks/ }), null, `${label} must not offer Start checks`);
    } finally { neg.lifecycle.unmount(); cleanup(); }
  }
});

test("checks have stopped banner chip stays single-line without build differs when build differs", async () => {
  const overdueDiff = loop({ controlState: "enabled" }, {
    schedule: { overdue: true, message: "No check has run for 2 hours." },
    daemon: { state: "not_recorded" },
    runtime: { state: "recent", source: "state", freshness: "recent", recordedBuild: "v0.2.0-dev.1111111", installedBuild: "v0.2.0-dev.aaaaaaa", binaryComparison: "different" },
    buildFreshness: { state: "different", text: "Different build from the installed one" },
  });
  const summary = banner([overdueDiff], "summary");
  try {
    const chip = await summary.findByRole("button", { name: "looptender: checks have stopped" });
    assert.equal(chip.style.whiteSpace, "nowrap");
    assert.equal(chip.style.overflow, "hidden");
    assert.equal(chip.style.textOverflow, "ellipsis");
    assert.equal(chip.style.minWidth, "0px");
    assert.equal(chip.style.maxWidth, "100%");
    assert.equal(chip.style.flexShrink, "1");
    assert.equal(chip.style.paddingLeft, "8px");
  } finally { summary.lifecycle.unmount(); cleanup(); }
  const expanded = banner([overdueDiff], "details");
  try {
    await expanded.findByText("Different build from the installed one");
  } finally { expanded.lifecycle.unmount(); cleanup(); }
});

test("expanded banner Add to Looptender supports Minimize on every step, Done on Supervision started, and hides Already have a loop once started", async () => {
  const plan = { loop: "example-loop", project: "/projects/example", configPath: "/private/example/looptender.yaml", stateDir: "/private/example/state", threadId: "thr_banner_min", provider: "codex", limits: { interval: "5m", wakeEvery: "1h", stallAfter: "30m", backoff: "10m", maxNudges: 3, maxReprompts: 3 } };
  const record = { id: "rec_1", loop: "example-loop", configPath: plan.configPath, threadId: "thr_banner_min", project: plan.project, provider: "codex", environmentId: "env_example", projectId: "proj_example", hostId: "host_example" };
  let toggles = 0;
  let started = false;
  const props = { threadId: `thr_vis_${++n}`, projectId: "proj_example", environment: null, segments: [], isExpanded: true, onToggle: () => { toggles++; } };
  const ui = renderSlot({ component: app.threadContextSegments[0]!.details! }, props, {
    rpc: {
      forThread: () => ({ loops: started ? [loop({ managedId: record.id, managedThreadId: props.threadId, controlState: "enabled" })] : [], errors: [] }),
      newThread: () => ({ ok: true, message: "", outcome: { state: "none", message: "" } }),
      lineage: () => ({ child: false }),
      transfer: () => ({ ok: true, message: "No moves", choices: [] }),
      attachment: () => ({ ok: true, candidates: [], message: "Registry read" }),
      setup: (raw: unknown) => {
        const input = raw as { action: string };
        if (input.action === "preview") return { ok: true, message: "Preview only", plan, ticket: "t1" };
        if (input.action === "create") return { ok: true, message: "Saved disabled", record, canStart: true, checks: [] };
        if (input.action === "start") { started = true; return { ok: true, message: "Background supervision started (user service installed; survives reboot).", record, canStart: false, started: true, checks: [{ id: "c1", status: "ok", message: "Verified" }] }; }
        return { ok: true, message: "No loop is set up yet." };
      },
    },
  });
  try {
    await ui.findByText("No loop is set up yet.");
    assert.ok(ui.container.querySelector(".kg-banner-body"));
    assert.ok(ui.getByText("Already have a loop?"));
    fireEvent.click(ui.getByRole("button", { name: "Minimize Add to Looptender" }));
    assert.equal(toggles, 1);

    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByRole("button", { name: "Create setup" });
    fireEvent.click(ui.getByRole("button", { name: "Minimize Add to Looptender" }));
    assert.equal(toggles, 2);

    fireEvent.click(ui.getByRole("button", { name: "Create setup" }));
    await ui.findByRole("button", { name: "Start background supervision" });
    assert.ok(ui.getByText("Already have a loop?"));

    fireEvent.click(ui.getByRole("button", { name: "Start background supervision" }));
    await ui.findByText("Supervision started");
    assert.equal(ui.queryByText("Already have a loop?"), null);
    assert.ok(ui.getByText("user service installed"));

    fireEvent.click(ui.getByRole("button", { name: "Done" }));
    assert.equal(toggles, 3);
  } finally {
    ui.lifecycle.unmount();
    cleanup();
  }
});

test("card Add to Looptender collapses via Minimize and Done", async () => {
  const plan = { loop: "example-loop", project: "/projects/example", configPath: "/private/example/looptender.yaml", stateDir: "/private/example/state", threadId: "thr_card_min", provider: "codex", limits: { interval: "5m", wakeEvery: "1h", stallAfter: "30m", backoff: "10m", maxNudges: 3, maxReprompts: 3 } };
  const record = { id: "rec_2", loop: "example-loop", configPath: plan.configPath, threadId: "thr_card_min", project: plan.project, provider: "codex", environmentId: "env_example", projectId: "proj_example", hostId: "host_example" };
  const ui = renderSlot(app.threadContextCardRows[0]!, { threadId: `thr_vis_${++n}`, projectId: "proj_example", environment: null }, {
    rpc: {
      forThread: () => ({ loops: [], errors: [] }),
      newThread: () => ({ ok: true, message: "", outcome: { state: "none", message: "" } }),
      lineage: () => ({ child: false }),
      transfer: () => ({ ok: true, message: "No moves", choices: [] }),
      attachment: () => ({ ok: true, candidates: [], message: "Registry read" }),
      setup: (raw: unknown) => {
        const input = raw as { action: string };
        if (input.action === "preview") return { ok: true, message: "Preview only", plan, ticket: "t1" };
        if (input.action === "create") return { ok: true, message: "Saved disabled", record, canStart: true, checks: [] };
        if (input.action === "start") return { ok: true, message: "Background supervision started (user service installed; survives reboot).", record, canStart: false, started: true, checks: [] };
        return { ok: true, message: "No loop is set up yet." };
      },
    },
  });
  try {
    fireEvent.click(await ui.findByRole("button", { name: "Add to Looptender" }));
    await ui.findByText("No loop is set up yet.");
    fireEvent.click(ui.getByRole("button", { name: "Minimize Add to Looptender" }));
    assert.equal(ui.queryByRole("button", { name: "Preview setup" }), null);

    fireEvent.click(ui.getByRole("button", { name: "Add to Looptender" }));
    await ui.findByText("No loop is set up yet.");
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByRole("button", { name: "Create setup" });
    fireEvent.click(ui.getByRole("button", { name: "Create setup" }));
    await ui.findByRole("button", { name: "Start background supervision" });
    fireEvent.click(ui.getByRole("button", { name: "Start background supervision" }));
    await ui.findByText("Supervision started");
    fireEvent.click(ui.getByRole("button", { name: "Done" }));
    assert.equal(ui.queryByText("Supervision started"), null);
  } finally {
    ui.lifecycle.unmount();
    cleanup();
  }
});

test("clicking Start checks updates the composer chip immediately to starting checks, preserves #541 chip styles, and clears when the next check lands", async () => {
  const overdueLoop = loop({ controlState: "enabled" }, { schedule: { overdue: true, message: "No check has run for 2 hours." }, daemon: { state: "not_recorded" }, lastCheck: { at: "2026-01-01T00:00:00Z", target: "primary", action: "none", outcome: "ok" } });
  let current = overdueLoop;
  let forThreadCalls = 0;
  let holdImmediateRefresh: (() => void) | null = null;
  const props = { threadId: `thr_vis_${++n}`, projectId: "proj_example", environment: null, segments: [], isExpanded: true, onToggle() {} };
  const rpc = {
    forThread: () => {
      forThreadCalls++;
      if (forThreadCalls === 2 && holdImmediateRefresh) {
        return new Promise(resolve => {
          const done = holdImmediateRefresh!;
          holdImmediateRefresh = () => { done(); resolve({ loops: [current], errors: [] }); };
        });
      }
      return { loops: [current], errors: [] };
    },
    startChecks: () => ({ ok: true, message: "Checks started via user service (survives reboot)." }),
    newThread: () => ({ ok: true, message: "", outcome: { state: "none", message: "" } }),
    lineage: () => ({ child: false }),
  };
  const summary = renderSlot({ component: app.threadContextSegments[0]!.summary }, props, { rpc });
  const details = renderSlot({ component: app.threadContextSegments[0]!.details! }, props, { rpc });
  try {
    const chip = await summary.findByRole("button", { name: "looptender: checks have stopped" });
    const startBtn = await details.findByRole("button", { name: "Start checks example-loop" });
    const initialCalls = forThreadCalls;

    // Hold the immediate post-startChecks forThread response to verify the chip updates
    // immediately on startChecks success before status refresh even resolves.
    holdImmediateRefresh = () => {};
    current = loop({ controlState: "enabled" }, { schedule: { overdue: true, message: "No check has run for 2 hours." }, daemon: { state: "responsive" }, lastCheck: { at: "2026-01-01T00:00:00Z", target: "primary", action: "none", outcome: "ok" } });
    fireEvent.click(startBtn);

    await summary.findByRole("button", { name: "looptender: starting checks" });
    await waitFor(() => assert.ok(forThreadCalls > initialCalls, "immediately triggers status refresh"));

    // Preserve #541 single-line ellipsis + 8px left-padding constraints.
    assert.equal(chip.style.whiteSpace, "nowrap");
    assert.equal(chip.style.overflow, "hidden");
    assert.equal(chip.style.textOverflow, "ellipsis");
    assert.equal(chip.style.minWidth, "0px");
    assert.equal(chip.style.maxWidth, "100%");
    assert.equal(chip.style.flexShrink, "1");
    assert.equal(chip.style.paddingLeft, "8px");
    const chipSpan = chip.querySelector("span") as HTMLElement;
    assert.ok(chipSpan);
    assert.equal(chipSpan.style.whiteSpace, "nowrap");
    assert.equal(chipSpan.style.overflow, "hidden");
    assert.equal(chipSpan.style.textOverflow, "ellipsis");

    // Immediate refresh arrives with daemon responsive, but overdue still true (no fresh check yet):
    // stays truthfully on "looptender: starting checks".
    holdImmediateRefresh!();
    await new Promise(r => setTimeout(r, 20));
    assert.equal(chip.textContent, "looptender: starting checks");

    // Once the first check lands and clears schedule.overdue, chip updates to enabled.
    current = loop({ controlState: "enabled" }, { schedule: { overdue: false }, daemon: { state: "responsive" }, lastCheck: { at: "2026-01-01T02:00:00Z", target: "primary", action: "none", outcome: "ok" } });
    await summary.behavior.emitRealtime("changed", {});
    await summary.findByRole("button", { name: "looptender: enabled" });
  } finally {
    summary.lifecycle.unmount();
    details.lifecycle.unmount();
    cleanup();
  }
});

test("starting checks clears truthfully on a later stopped refresh, a hold, or a status error, and reverts with error if startChecks fails", async () => {
  const overdueLoop = loop({ controlState: "enabled" }, { schedule: { overdue: true, message: "No check has run for 2 hours." }, daemon: { state: "not_recorded" } });

  // 1. startChecks returns ok:false -> never shows starting checks, shows alert error, allows retry.
  {
    let calls = 0;
    const ui = renderSlot(app.threadContextCardRows[0]!, { threadId: `thr_vis_${++n}`, projectId: "proj_example", environment: null }, {
      rpc: {
        forThread: () => { calls++; return { loops: [overdueLoop], errors: [] }; },
        startChecks: () => ({ ok: false, message: "Could not install user service; inspect looptender service status on the host." }),
      },
    });
    try {
      await ui.findByText("looptender: checks have stopped");
      const before = calls;
      fireEvent.click(ui.getByRole("button", { name: "Start checks example-loop" }));
      const alert = await ui.findByRole("alert");
      assert.match(alert.textContent ?? "", /Could not install user service/);
      assert.ok(ui.getByText("looptender: checks have stopped"));
      assert.equal(ui.queryByText("looptender: starting checks"), null);
      assert.equal((ui.getByRole("button", { name: "Start checks example-loop" }) as HTMLButtonElement).disabled, false);
      await waitFor(() => assert.ok(calls > before));
    } finally { ui.lifecycle.unmount(); cleanup(); }
  }

  // 2. startChecks rejects -> shows fallback alert error and keeps checks have stopped.
  {
    const ui = renderSlot(app.threadContextCardRows[0]!, { threadId: `thr_vis_${++n}`, projectId: "proj_example", environment: null }, {
      rpc: {
        forThread: () => ({ loops: [overdueLoop], errors: [] }),
        startChecks: () => Promise.reject(new Error("rpc down")),
      },
    });
    try {
      await ui.findByText("looptender: checks have stopped");
      fireEvent.click(ui.getByRole("button", { name: "Start checks example-loop" }));
      const alert = await ui.findByRole("alert");
      assert.match(alert.textContent ?? "", /Could not start checks/);
      assert.ok(ui.getByText("looptender: checks have stopped"));
      assert.equal(ui.queryByText("looptender: starting checks"), null);
    } finally { ui.lifecycle.unmount(); cleanup(); }
  }

  // 3. startChecks succeeds, immediate refresh still overdue & not_recorded -> shows starting checks;
  //    later refresh still stopped -> reverts to checks have stopped and re-shows Start checks.
  {
    const ui = renderSlot(app.threadContextCardRows[0]!, { threadId: `thr_vis_${++n}`, projectId: "proj_example", environment: null }, {
      rpc: {
        forThread: () => ({ loops: [overdueLoop], errors: [] }),
        startChecks: () => ({ ok: true, message: "Checks started in background." }),
      },
    });
    try {
      await ui.findByText("looptender: checks have stopped");
      fireEvent.click(ui.getByRole("button", { name: "Start checks example-loop" }));
      await ui.findByText("looptender: starting checks");
      await new Promise(r => setTimeout(r, 20));
      assert.ok(ui.getByText("looptender: starting checks"));
      await ui.behavior.emitRealtime("changed", {});
      await ui.findByText("looptender: checks have stopped");
      assert.ok(ui.getByRole("button", { name: "Start checks example-loop" }));
    } finally { ui.lifecycle.unmount(); cleanup(); }
  }

  // 4. startChecks succeeds, then refresh reports a hold -> never claims hold cleared.
  {
    let current = overdueLoop;
    const ui = renderSlot(app.threadContextCardRows[0]!, { threadId: `thr_vis_${++n}`, projectId: "proj_example", environment: null }, {
      rpc: {
        forThread: () => ({ loops: [current], errors: [] }),
        startChecks: () => { current = loop({ controlState: "awaiting user" }, { schedule: { overdue: true } }); return { ok: true, message: "Checks started." }; },
      },
    });
    try {
      await ui.findByText("looptender: checks have stopped");
      fireEvent.click(ui.getByRole("button", { name: "Start checks example-loop" }));
      await ui.findByText("looptender: awaiting user");
      assert.equal(ui.queryByText("looptender: starting checks"), null);
    } finally { ui.lifecycle.unmount(); cleanup(); }
  }
});
