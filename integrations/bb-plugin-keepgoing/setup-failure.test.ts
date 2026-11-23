import { test } from "node:test";
import assert from "node:assert/strict";
import { failureMessage, failureSchema } from "./setup-contract.ts";
import { createSetupController, type SetupDependencies } from "./setup-controller.ts";

test("a preview, which creates nothing, never says a setup may already exist; a creation that timed out does", () => {
  for (const f of [{ kind: "timeout", seconds: 20 }, { kind: "exit", code: 1, line: "init: fictional" }, { kind: "exit", code: 3 }, { kind: "output" }] as const) {
    const m = failureMessage(f, false, "example-loop");
    assert.ok(!/may (already )?(exist|have)/.test(m), m);
    assert.match(m, /Nothing was created/);
  }
  assert.match(failureMessage({ kind: "timeout", seconds: 20 }, false), /timed out: the looptender command was still running after 20 s/);
  assert.match(failureMessage({ kind: "exit", code: 1, line: "init: fictional line" }, false), /exited with status 1 and said: init: fictional line/);
  assert.match(failureMessage({ kind: "exit", code: 4 }, false), /printed nothing to explain it/);
  assert.match(failureMessage({ kind: "timeout", seconds: 20 }, true, "example-loop"), /switched-off setup named example-loop may have been written/);
  const exists = failureMessage({ kind: "exists", line: "init: directory already exists" }, false, "example-loop");
  assert.match(exists, /A setup named example-loop already exists on this host/); assert.match(exists, /Review or remove it, or choose another loop name/);
  assert.ok(failureSchema.safeParse({ kind: "exit", code: 1, line: "x".repeat(240) }).success);
  assert.ok(!failureSchema.safeParse({ kind: "exit", line: "x".repeat(241) }).success);
});

const target = { threadId: "thr_example", environmentId: "env_example", projectId: "proj_example", hostId: "host_example", project: "/projects/example", provider: "codex" };
function controller(init: SetupDependencies["init"]) {
  return createSetupController({
    target: async () => target, binary: async () => "looptender", records: async () => [], save: async () => {}, guard: async () => {},
    init, standardRoutes: async () => false, command: async () => ({ code: 0, data: null }), changed: () => {},
  });
}
const preview = { action: "preview" as const, threadId: "thr_example", name: "example-loop", mode: "own" as const, task: "Fictional task" };

test("setup says what the host reported when its command failed, and nothing is created", async () => {
  let creates = 0;
  const c = controller(async (_b, _t, _n, _task, dryRun) => { if (!dryRun) creates++; return { failed: { kind: "exit", code: 1, line: "init: fictional failure" } }; });
  const r = await c.call(preview);
  assert.equal(r.ok, false); assert.match(r.message, /exited with status 1 and said: init: fictional failure/); assert.ok(!/Goal creation needs/.test(r.message));
  assert.equal(creates, 0);
});

test("a host call that ran out of time says so; any other unexpected error says nothing but its name", async () => {
  const slow = Object.assign(new Error("PRIVATE path /private/x and task text"), { name: "TimeoutError" });
  let r = await controller(async () => { throw slow; }).call(preview);
  assert.match(r.message, /did not finish: the host did not answer within the plugin's time limit \(30 s\)/); assert.match(r.message, /Nothing was created/);
  assert.ok(!r.message.includes("PRIVATE"));
  r = await controller(async () => { throw Object.assign(new Error("PRIVATE"), { name: "WeirdError" }); }).call(preview);
  assert.match(r.message, /failed unexpectedly \(WeirdError\)/); assert.ok(!r.message.includes("PRIVATE")); assert.ok(!/may (already )?exist/.test(r.message));
});

test("a possible write is said as one on a creation and as a plain failure on a preview", () => {
  const f = { kind: "written", line: "init: could not write JSON result; setup files may already exist" } as const;
  const create = failureMessage(f, true, "example-loop");
  assert.match(create, /may have written files/); assert.match(create, /switched-off setup named example-loop may have been written: refresh status and review or remove it/);
  assert.ok(!/Nothing was created/.test(create));
  const preview = failureMessage(f, false, "example-loop");
  assert.match(preview, /exited with status 1/); assert.ok(!/may have been written/.test(preview));
  assert.match(failureMessage({ kind: "exists", line: "init: mkdir /p/x: file exists" }, true, "example-loop"), /already exists on this host/);
});
