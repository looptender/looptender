import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, realpath, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createFakePluginHost, makeThreadResponse, makeHostResponse } from "@get-bb/plugin-sdk/testing";
import plugin from "./server.ts";
import { setupInput, setupResult } from "./setup-contract.ts";
import { hostContract } from "./host-contract.ts";
import { setupOnHost, execute } from "./setup-host.ts";
import { TASK_MAX_BYTES, taskBytes, taskTextProblem } from "./task-text.ts";

const target = { threadId: "thr_example", environmentId: "env_example", projectId: "proj_example", hostId: "host_example", project: "/projects/example", provider: "codex" };

test("all task schemas use UTF-8 bytes, preserve whitespace and reject malformed Unicode", () => {
  const cases: [string, boolean][] = [
    ["x", true], [" a\r\n界🙂\n", true], ["a".repeat(TASK_MAX_BYTES), true],
    ["界".repeat(349525) + "x", true], ["🙂".repeat(262144), true],
    ["a".repeat(TASK_MAX_BYTES + 1), false], ["界".repeat(349526), false],
    ["🙂".repeat(262144) + "x", false], ["bad\ud800text", false], ["bad\udc00text", false], [" ", false],
  ];
  for (const [task, valid] of cases) {
    assert.equal(taskTextProblem(task) === null, valid);
    assert.equal(taskBytes(task), Buffer.byteLength(task, "utf8"));
    const schemas = [
      [setupInput, { action: "preview", threadId: target.threadId, name: "example", task }],
      [setupResult, { ok: true, message: "Preview", task }],
      [hostContract.init.input, { bin: "looptender", target, name: "example", task, dryRun: true }],
    ] as const;
    for (const [schema, input] of schemas) {
      const parsed = schema.safeParse(input);
      assert.equal(parsed.success, valid);
      if (parsed.success) assert.equal(parsed.data.task, task);
    }
  }
});

test("actual SDK RPC and host schemas carry complete 1 MiB tasks into setup stdin, with JSON escape headroom", async () => {
  const root = await realpath(await mkdtemp(join(tmpdir(), "looptender-task-")));
  const host = makeHostResponse({ id: target.hostId, status: "connected" });
  let expected = ""; let initCalls = 0;
  const { bb, harness } = createFakePluginHost({
    pluginId: "keepgoing", settings: { keepgoingPath: "looptender", configs: "" },
    sdk: {
      threads: { get: async () => makeThreadResponse({ id: target.threadId, environmentId: target.environmentId, projectId: target.projectId, providerId: target.provider }) },
      environments: { get: async () => ({ id: target.environmentId, projectId: target.projectId, hostId: target.hostId, path: root, isGitRepo: true, status: "ready", hostLifecycle: "active", lifecycle: { phase: "active" } }) },
      hosts: { get: async () => host },
    },
    experimental_callHostRpc: async ({ method, input, hostId }) => {
      assert.equal(method, "init"); assert.equal(hostId, target.hostId); initCalls++;
      const serialized = JSON.stringify(input);
      // Audited BB host RPC cap: includes JSON escaping, not only task bytes.
      assert.ok(Buffer.byteLength(serialized) < 32 * 1024 * 1024);
      const parsed = hostContract.init.input.parse(JSON.parse(serialized));
      assert.equal(parsed.task, expected);
      const answer = await setupOnHost(parsed, undefined, async (_bin, args, stdin) => {
        assert.equal(stdin, expected); assert.ok(args.includes("--dry-run"));
        assert.equal(args[args.indexOf("--task-file") + 1], "-");
        assert.ok(!args.includes(expected));
        return { code: 0, data: { schemaVersion: 1, outcome: "preview", loop: parsed.name, project: root, configPath: join(root, "looptender.yaml"), stateDir: join(root, "state"), primary: { agent: { adapter: "bb", bbThread: target.threadId, provider: target.provider } }, fallbacks: [], limits: { interval: "5m", wakeEvery: "1h", stallAfter: "30m", backoff: "10m", maxNudges: 3, maxReprompts: 3, failoverOn: [] } } };
      });
      return hostContract.init.output.parse(answer);
    },
  });
  try {
    await plugin(bb);
    for (const task of ["a".repeat(TASK_MAX_BYTES), "界".repeat(349525) + "x", "🙂".repeat(262144), "\u0001".repeat(TASK_MAX_BYTES)]) {
      expected = task;
      const input = { action: "preview", threadId: target.threadId, name: "example", task };
      const r = setupResult.parse(await harness.behavior.callRpc("setup", input));
      assert.equal(r.ok, true, r.message); assert.equal(input.task, task);
      assert.equal(r.task, undefined, "custom task must not be echoed in the response");
    }
    const callsBeforeRejection = initCalls;
    for (const task of ["🙂".repeat(262144) + "x", "bad\ud800text"]) {
      const input = { action: "preview", threadId: target.threadId, name: "example", task };
      await assert.rejects(harness.behavior.callRpc("setup", input));
      assert.equal(input.task, task);
    }
    assert.equal(initCalls, callsBeforeRejection);
    assert.deepEqual(harness.inspection.sdk.callsTo("threads.spawn"), []);
    assert.equal(await bb.storage.kv.get("setups.v1"), undefined);
  } finally { await harness.lifecycle.dispose(); await rm(root, { recursive: true, force: true }); }
});

test("host rejects invalid tasks before filesystem or process access, and the stdin adapter keeps all accepted bytes", async () => {
  let runs = 0;
  for (const task of ["a".repeat(TASK_MAX_BYTES + 1), "界".repeat(400000), "bad\ud800text"]) {
    await assert.rejects(setupOnHost({ bin: "fictional-never-run", target, name: "example", task, dryRun: true }, undefined, async () => {
      runs++; throw new Error("must not run");
    }), /1048576|Unicode/);
  }
  assert.equal(runs, 0);
  const task = " \r\n" + "界🙂\"\\\n".repeat(104857) + "xxx";
  assert.equal(Buffer.byteLength(task), TASK_MAX_BYTES);
  const result = await execute(process.execPath, ["-e", `const chunks=[];process.stdin.on('data',x=>chunks.push(x));process.stdin.on('end',()=>{const b=Buffer.concat(chunks);process.stdout.write(JSON.stringify({bytes:b.length,hash:require('node:crypto').createHash('sha256').update(b).digest('hex')}))});`], task, "thr_example");
  assert.equal(result.code, 0);
  assert.deepEqual(result.data, { bytes: TASK_MAX_BYTES, hash: createHash("sha256").update(task).digest("hex") });
});
