import { test } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import type { SetupResult } from "./setup-contract.ts";

const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "https://example.com" });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { value: dom.window.navigator, configurable: true });
const { render, fireEvent, waitFor, cleanup } = await import("@testing-library/react");
const { SetupPanel } = await import("./setup-panel.tsx");
const plan = { loop: "example-loop", project: "/projects/example", configPath: "/private/example-loop/keepgoing.yaml", stateDir: "/private/example-loop/state", threadId: "thr_example", provider: "codex", limits: { interval: "5m", wakeEvery: "1h", stallAfter: "30m", backoff: "10m", maxNudges: 3, maxReprompts: 3 } };
const record = { id: "example-record", loop: plan.loop, configPath: plan.configPath, threadId: plan.threadId, project: plan.project, provider: plan.provider, environmentId: "env_example", projectId: "proj_example", hostId: "host_example" };

test("UI requires preview, create, and start clicks; task edits invalidate preview", async () => {
  const calls: string[] = [];
  let changed = 0;
  const call = async (input: { action: string }): Promise<SetupResult> => {
    calls.push(input.action);
    if (input.action === "preview") return { ok: true, message: "Preview only", plan, ticket: "example-ticket" };
    if (input.action === "create") return { ok: true, message: "Saved disabled", record, canStart: true, checks: [] };
    if (input.action === "start") return { ok: true, message: "Background supervision started", record, canStart: false };
    return { ok: true, message: "No loop is set up for this thread yet." };
  };
  const ui = render(<SetupPanel threadId="thr_example" call={call} onChanged={() => changed++} />);
  try {
    await ui.findByText("No loop is set up for this thread yet.");
    fireEvent.click(ui.getByRole("radio", { name: "Write my own task" }));
    fireEvent.change(ui.getByLabelText("Task and completion criteria"), { target: { value: "Fictional task" } });
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByText("Preview only");
    assert.ok(ui.getByText(plan.configPath));
    fireEvent.change(ui.getByLabelText("Task and completion criteria"), { target: { value: "Revised fictional task" } });
    assert.equal(ui.queryByRole("button", { name: "Create setup" }), null);
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByRole("button", { name: "Create setup" });
    assert.ok(!calls.includes("create"));
    fireEvent.click(ui.getByRole("button", { name: "Create setup" }));
    await ui.findByText("Saved disabled");
    assert.ok(!calls.includes("start")); assert.equal(ui.queryByLabelText("Task and completion criteria"), null);
    fireEvent.click(ui.getByRole("button", { name: "Start background supervision" }));
    await ui.findByText("Background supervision started");
    assert.equal(calls.filter(c => c === "start").length, 1); assert.equal(changed, 2);
    assert.equal(ui.queryByRole("button", { name: "Start background supervision" }), null);
  } finally { cleanup(); }
});

test("unverified saved setup shows diagnostics refresh without a start action", async () => {
  const ui = render(<SetupPanel threadId="thr_example" call={async () => ({ ok: true, record, canStart: false, message: "Target unavailable" })} onChanged={() => {}} />);
  try {
    await ui.findByText("Target unavailable");
    assert.ok(ui.getByRole("button", { name: "Refresh diagnostics" }));
    assert.equal(ui.queryByRole("button", { name: "Start background supervision" }), null);
  } finally { cleanup(); }
});

test("transport errors are visible and are never retried automatically", async () => {
  let calls = 0;
  const ui = render(<SetupPanel threadId="thr_example" call={async () => { calls++; throw new Error("fictional-private-error"); }} onChanged={() => {}} />);
  try {
    await waitFor(() => assert.ok(ui.getByRole("alert")));
    assert.equal(calls, 1); assert.ok(!ui.container.textContent?.includes("fictional-private-error"));
  } finally { cleanup(); }
});

test("lost creation response retains the destination without a replay button", async () => {
  const call = async (input: { action: string }): Promise<SetupResult> => {
    if (input.action === "preview") return { ok: true, plan, ticket: "example-ticket", message: "Preview only" };
    if (input.action === "create") throw new Error("lost response");
    return { ok: true, message: "No loop is set up for this thread yet." };
  };
  const ui = render(<SetupPanel threadId="thr_example" call={call} onChanged={() => {}} />);
  try {
    await ui.findByText("No loop is set up for this thread yet.");
    fireEvent.click(ui.getByRole("radio", { name: "Write my own task" }));
    fireEvent.change(ui.getByLabelText("Task and completion criteria"), { target: { value: "Fictional task" } });
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByRole("button", { name: "Create setup" });
    fireEvent.click(ui.getByRole("button", { name: "Create setup" }));
    await ui.findByRole("alert");
    assert.ok(ui.getByText(plan.configPath));
    assert.equal(ui.queryByRole("button", { name: "Create setup" }), null);
    assert.equal(ui.queryByRole("button", { name: "Start background supervision" }), null);
  } finally { cleanup(); }
});

test("goal choice is explicit, invalidates preview on change, and shows recorded revision after create", async () => {
  const calls: Array<{ action: string; withGoal?: boolean }> = [];
  const call = async (input: { action: string; withGoal?: boolean }): Promise<SetupResult> => {
    calls.push(input);
    if (input.action === "preview") return { ok: true, message: "Preview only", plan: { ...plan, ...(input.withGoal ? { withGoal: true } : {}) }, ticket: "ticket" };
    if (input.action === "create") return { ok: true, record, canStart: true, message: "Saved disabled", goalState: "recorded", goal: { id: "goal_0123456789abcdef0123456789abcdef", revision: 1, state: "recorded" } };
    return { ok: true, message: "No loop is set up for this thread yet." };
  };
  const ui = render(<SetupPanel threadId="thr_example" call={call} onChanged={() => {}} />);
  try {
    await ui.findByText("No loop is set up for this thread yet.");
    const choice = ui.getByRole("checkbox", { name: "Record a goal for this task" }) as HTMLInputElement;
    assert.equal(choice.checked, true);
    fireEvent.click(ui.getByRole("radio", { name: "Write my own task" }));
    fireEvent.change(ui.getByLabelText("Task and completion criteria"), { target: { value: "Fictional task and acceptance" } });
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByText(/Recorded as goal revision 1/);
    assert.ok(ui.getByText(/Idle threads can wake on a check/));
    fireEvent.click(choice);
    assert.equal(ui.queryByRole("button", { name: "Create setup" }), null);
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByText(/Copied into the wake instructions; no goal record/);
    assert.equal(calls.at(-1)?.withGoal, false);
    fireEvent.click(choice);
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByRole("button", { name: "Create setup" });
    fireEvent.click(ui.getByRole("button", { name: "Create setup" }));
    await ui.findByText(/Goal: goal_0123456789abcdef0123456789abcdef.*revision 1/);
    assert.equal(calls.at(-1)?.withGoal, true);
    assert.ok(!calls.some(c => c.action === "start"));
  } finally { cleanup(); }
});

test("keepgoing's refusal is shown in its words and the panel offers no way around it", async () => {
  const message = "looptender refused to set up this loop: loop other-loop already supervises this thread's session, and two loops on one session would both prompt it. Nothing was created. To have this thread supervised here instead, pause or disable other-loop first.";
  const call = async (input: { action: string }): Promise<SetupResult> => input.action === "preview"
    ? { ok: false, refused: { code: "second_loop_on_session", otherLoop: "other-loop" }, message }
    : { ok: true, message: "No loop is set up for this thread yet." };
  const ui = render(<SetupPanel threadId="thr_example" call={call} onChanged={() => {}} />);
  try {
    await ui.findByText("No loop is set up for this thread yet.");
    fireEvent.click(ui.getByRole("radio", { name: "Write my own task" }));
    fireEvent.change(ui.getByLabelText("Task and completion criteria"), { target: { value: "Fictional task" } });
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    assert.equal((await ui.findByRole("alert")).textContent, message);
    assert.equal(ui.queryByRole("button", { name: /Create|Start|anyway|override/i }), null);
    assert.ok(!ui.container.textContent?.includes("allow-second-loop"));
  } finally { ui.unmount(); cleanup(); }
});

test("the standard list is a choice only when offered, off by default, and its routes are shown before Create", async () => {
  const inputs: Record<string, unknown>[] = [];
  const routes = { list: "standard" as const, handoffOn: "rate_limit" as const, apply: [{ id: "codex-large", model: "example-large", group: "codex-large" }] };
  const call = (offered: boolean) => async (input: Record<string, unknown>): Promise<SetupResult> => {
    inputs.push(input);
    if (input.action === "preview") return { ok: true, message: "Preview only", plan: input.followStandard ? { ...plan, routes } : plan, ticket: "example-ticket" };
    return { ok: true, message: "No loop is set up for this thread yet.", ...(offered ? { standardRoutes: true as const } : {}) };
  };
  let ui = render(<SetupPanel threadId="thr_example" call={call(false)} onChanged={() => {}} />);
  try {
    await ui.findByText("No loop is set up for this thread yet.");
    assert.equal(ui.queryByLabelText("Follow my standard route list for handoffs"), null);
  } finally { cleanup(); }
  ui = render(<SetupPanel threadId="thr_example" call={call(true)} onChanged={() => {}} />);
  try {
    await ui.findByText("No loop is set up for this thread yet.");
    const box = ui.getByLabelText("Follow my standard route list for handoffs") as HTMLInputElement;
    assert.equal(box.checked, false);
    fireEvent.click(ui.getByRole("radio", { name: "Write my own task" }));
    fireEvent.change(ui.getByLabelText("Task and completion criteria"), { target: { value: "Fictional task" } });
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByText("No fallbacks; automatic handoff is off.");
    assert.equal(inputs.at(-1)?.followStandard, undefined);
    fireEvent.click(box);
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByText(/Handoff on a rate limit is turned on/);
    assert.equal(inputs.at(-1)?.followStandard, true);
    assert.ok(ui.getByText("codex-large: example-large, usage limit codex-large"));
    assert.ok(ui.getByRole("button", { name: "Create setup" }));
  } finally { cleanup(); }
});

test("Keep this conversation going is the default, needs no typing, and the preview shows the task", async () => {
  const seen: Array<Record<string, unknown>> = [];
  const call = async (input: Record<string, unknown>): Promise<SetupResult> => {
    seen.push(input);
    if (input.action === "preview") return { ok: true, message: "Preview only", plan, ticket: "example-ticket", task: "Built-in fictional task text" };
    return { ok: true, message: "No loop is set up for this thread yet.", conversation: "unknown" };
  };
  const ui = render(<SetupPanel threadId="thr_example" call={call as never} onChanged={() => {}} />);
  try {
    await ui.findByText("No loop is set up for this thread yet.");
    assert.equal((ui.getByRole("radio", { name: "Keep this conversation going" }) as HTMLInputElement).checked, true);
    assert.equal(ui.queryByLabelText("Task and completion criteria"), null);
    fireEvent.change(ui.getByLabelText("Anything to add?"), { target: { value: "Skip the docs" } });
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByText("Preview only");
    assert.ok(ui.getByText("Built-in fictional task text"));
    const sent = seen.find(i => i.action === "preview")!;
    assert.equal(sent.mode, "conversation"); assert.equal(sent.note, "Skip the docs"); assert.equal("task" in sent, false);
    // Changing the note invalidates the preview, as editing the task does.
    fireEvent.change(ui.getByLabelText("Anything to add?"), { target: { value: "Other" } });
    assert.equal(ui.queryByRole("button", { name: "Create setup" }), null);
    // The second choice is the typed task, as before.
    fireEvent.click(ui.getByRole("radio", { name: "Write my own task" }));
    assert.ok(ui.getByLabelText("Task and completion criteria"));
    assert.equal((ui.getByRole("button", { name: "Preview setup" }) as HTMLButtonElement).disabled, true);
  } finally { cleanup(); }
});

test("a thread with no turn yet starts on the typed task and says why; a choice made by hand is kept", async () => {
  const call = async (): Promise<SetupResult> => ({ ok: true, message: "No loop is set up for this thread yet.", conversation: "none" });
  const ui = render(<SetupPanel threadId="thr_example" call={call as never} onChanged={() => {}} />);
  try {
    await ui.findByText("No loop is set up for this thread yet.");
    assert.equal((ui.getByRole("radio", { name: "Write my own task" }) as HTMLInputElement).checked, true);
    assert.ok(ui.getByText("This thread may not have started yet, so there may be nothing to continue. Write a task."));
    assert.ok(ui.getByLabelText("Task and completion criteria"));
    fireEvent.click(ui.getByRole("radio", { name: "Keep this conversation going" }));
    assert.ok(ui.getByLabelText("Anything to add?"));
    assert.equal(ui.queryByText(/may not have started yet/), null);
  } finally { cleanup(); }
});

test("on a thread with no turn yet, the typed task stays the default through preview and create", async () => {
  const seen: Array<Record<string, unknown>> = [];
  const call = async (input: Record<string, unknown>): Promise<SetupResult> => {
    seen.push(input);
    // Preview and create results carry no `conversation` field.
    if (input.action === "preview") return { ok: true, message: "Preview only", plan, ticket: "example-ticket" };
    if (input.action === "create") return { ok: true, message: "Saved disabled", record, canStart: true, checks: [] };
    return { ok: true, message: "No loop is set up for this thread yet.", conversation: "none" };
  };
  const ui = render(<SetupPanel threadId="thr_example" call={call as never} onChanged={() => {}} />);
  try {
    await ui.findByText("No loop is set up for this thread yet.");
    fireEvent.change(ui.getByLabelText("Task and completion criteria"), { target: { value: "Fictional task" } });
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByText("Preview only");
    assert.equal((ui.getByRole("radio", { name: "Write my own task" }) as HTMLInputElement).checked, true);
    assert.ok(ui.getByLabelText("Task and completion criteria"));
    fireEvent.click(ui.getByRole("button", { name: "Create setup" }));
    await ui.findByText("Saved disabled");
    const create = seen.find(i => i.action === "create")!;
    assert.equal(create.mode, "own"); assert.equal(create.task, "Fictional task"); assert.equal("note" in create, false);
  } finally { cleanup(); }
});

test("the preview shows the takeover, and the person can turn it off and preview again", async () => {
  const sent: Array<string | undefined> = [];
  const offer = { provider: "claude-code", model: "example-claude-model" };
  const call = async (input: { action: string; takeover?: "on" | "off" }): Promise<SetupResult> => {
    if (input.action !== "preview") return { ok: true, message: "No loop is set up for this thread yet." };
    sent.push(input.takeover);
    const takeover = input.takeover !== "off" ? { takeover: { ...offer, permission: "accept-edits", text: "On a usage limit on codex: a new conversation on claude-code takes over." } } : {};
    return { ok: true, message: input.takeover === "off" ? "Preview without takeover" : "Preview with takeover", plan: { ...plan, ...takeover, takeoverOffer: offer }, ticket: "example-ticket" };
  };
  const ui = render(<SetupPanel threadId="thr_example" call={call} onChanged={() => {}} />);
  try {
    await ui.findByText("No loop is set up for this thread yet.");
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByText("Preview with takeover");
    assert.ok(ui.getByText("On a usage limit on codex: a new conversation on claude-code takes over."));
    const box = ui.getByRole("checkbox", { name: "Take over on a usage limit" }) as HTMLInputElement;
    assert.equal(box.checked, true);
    fireEvent.click(box);
    assert.equal(ui.queryByRole("button", { name: "Create setup" }), null, "turning it off invalidates the preview");
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByText("Preview without takeover");
    assert.ok(ui.getByText("No fallbacks; automatic handoff is off."));
    assert.deepEqual(sent, ["on", "off"]);
  } finally { cleanup(); }
});

const primaries = (ui: { container: HTMLElement }) => [...ui.container.querySelectorAll("button.primary")].map(b => b.textContent);

test("each step has exactly one prominent button, and the choices are form controls, not buttons", async () => {
  const call = async (input: { action: string }): Promise<SetupResult> => {
    if (input.action === "preview") return { ok: true, message: "Preview only", plan, ticket: "example-ticket" };
    if (input.action === "create") return { ok: true, message: "Saved disabled", record, canStart: true, checks: [] };
    if (input.action === "start") return { ok: true, message: "Background supervision started", record, canStart: false, started: true };
    return { ok: true, message: "Nothing set up yet" };
  };
  const ui = render(<SetupPanel threadId="thr_example" call={call} onChanged={() => {}} />);
  try {
    await ui.findByText("Nothing set up yet");
    assert.deepEqual(primaries(ui), ["Preview setup"]);
    // Selection is by radio and checkbox, grouped under headings; the only buttons are actions.
    assert.deepEqual([...ui.container.querySelectorAll("fieldset > legend")].map(l => l.textContent), ["What should the loop keep going?", "Options"]);
    assert.equal(ui.container.querySelectorAll("input[type=radio]").length, 2); assert.equal(ui.container.querySelectorAll("input[type=checkbox]").length, 1);
    assert.deepEqual([...ui.container.querySelectorAll("button")].map(b => b.textContent), ["Preview setup"]);
    assert.ok(ui.container.querySelector("label[for]"), "labelled fields");
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByRole("button", { name: "Create setup" });
    assert.deepEqual(primaries(ui), ["Create setup"]);
    assert.ok(ui.getByText(/Create setup saves the configuration and leaves supervision switched off/));
    fireEvent.click(ui.getByRole("button", { name: "Create setup" }));
    await ui.findByText("Setup created, switched off");
    assert.ok(ui.getByText(/No\. The loop is saved switched off and sends nothing until it is started\./));
    assert.deepEqual(primaries(ui), ["Start background supervision"]);
    fireEvent.click(ui.getByRole("button", { name: "Start background supervision" }));
    await ui.findByText("Supervision started");
    assert.ok(ui.getByText(/Yes: a background supervisor was launched/)); assert.deepEqual(primaries(ui), []);
  } finally { cleanup(); }
});

test("when another control starts the loop, the finished state points to it and offers no second Start", async () => {
  const ui = render(<SetupPanel threadId="thr_example" hideStart call={async (input: { action: string }): Promise<SetupResult> => input.action === "inspect" ? { ok: true, record, canStart: true, message: "Review the diagnostics" } : { ok: true, message: "x" }} onChanged={() => {}} />);
  try {
    await ui.findByText("Setup saved for this thread");
    assert.equal(ui.queryByRole("button", { name: "Start background supervision" }), null);
    assert.ok(ui.getByText(/start it with the looptender control at the top of this thread/));
  } finally { cleanup(); }
});

test("a lost request says which step, how long, and that a preview created nothing; only a lost creation says a setup may exist", async () => {
  let phase: "preview" | "create" = "preview";
  const call = async (input: { action: string }): Promise<SetupResult> => {
    if (input.action === "preview") { if (phase === "preview") throw Object.assign(new Error("PRIVATE text"), { name: "TimeoutError" }); return { ok: true, message: "Preview only", plan, ticket: "example-ticket" }; }
    if (input.action === "create") throw new Error("PRIVATE text");
    return { ok: true, message: "Nothing set up yet" };
  };
  const ui = render(<SetupPanel threadId="thr_example" call={call} onChanged={() => {}} />);
  try {
    await ui.findByText("Nothing set up yet");
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    const lost = await ui.findByRole("alert");
    assert.match(lost.textContent ?? "", /The preview did not finish: the request timed out after \d+ s\. Nothing was created; a preview never writes anything\./);
    assert.ok(!/may already exist/.test(ui.container.textContent ?? "")); assert.ok(!(ui.container.textContent ?? "").includes("PRIVATE"));
    phase = "create";
    fireEvent.click(ui.getByRole("button", { name: "Preview setup" }));
    await ui.findByRole("button", { name: "Create setup" });
    fireEvent.click(ui.getByRole("button", { name: "Create setup" }));
    await waitFor(() => assert.match(ui.getByRole("alert").textContent ?? "", /Creating the setup did not finish: the request to the plugin failed after \d+ s\. A switched-off setup named bb-thr-example may have been written/));
  } finally { cleanup(); }
});

test("an invalid loop name is said next to the field and cannot be previewed", async () => {
  const ui = render(<SetupPanel threadId="thr_example" call={async () => ({ ok: true, message: "Nothing set up yet" })} onChanged={() => {}} />);
  try {
    await ui.findByText("Nothing set up yet");
    fireEvent.change(ui.getByLabelText("Loop name"), { target: { value: "Bad Name" } });
    assert.ok(ui.getByText(/Use lowercase letters, digits and hyphens, starting with a letter or digit/));
    assert.equal((ui.getByRole("button", { name: "Preview setup" }) as HTMLButtonElement).disabled, true);
    assert.equal(ui.getByLabelText("Loop name").getAttribute("aria-invalid"), "true");
  } finally { cleanup(); }
});

