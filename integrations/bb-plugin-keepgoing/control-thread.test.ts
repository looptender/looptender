import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, readFile, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createFakePluginHost } from "@get-bb/plugin-sdk/testing";
import plugin, { NOT_THIS_THREAD } from "./server.ts";

// A pause or stop asked for from a thread's card reaches only a loop that
// thread belongs to: its primary or its current target.
async function world() {
  const dir = await realpath(await mkdtemp(join(tmpdir(), "keepgoing-control-thread-")));
  const log = join(dir, "calls.log");
  const config = join(dir, "example", "looptender.yaml");
  const status = { loop: "example-loop", target: "fallback-1", controlState: "enabled", control: {}, window: "open", state: {}, daemon: { state: "not_recorded" },
    primaryAgent: { adapter: "bb", bbThread: "thr_primary" }, targetAgent: { adapter: "bb", bbThread: "thr_target" } };
  const program = join(dir, "looptender-stand-in");
  await writeFile(program, `#!/bin/sh\necho "$@" >> "${log}"\ncase "$1" in status) cat <<'JSON'\n${JSON.stringify(status)}\nJSON\n;; *) echo "$1 done";; esac\n`, { mode: 0o700 });
  const hostCalls: string[] = [];
  const { bb, harness } = createFakePluginHost({
    pluginId: "keepgoing", settings: { keepgoingPath: program, configs: config },
    experimental_callHostRpc: async ({ method, input }) => { const i = input as { operation?: string }; hostCalls.push(`${method}:${i.operation ?? ""}`); return { code: 0, data: null }; },
  });
  await plugin(bb);
  const calls = async () => (await readFile(log, "utf8").catch(() => "")).split("\n").filter(l => l && !l.startsWith("status"));
  return { dir, config, bb, harness, calls, hostCalls, dispose: async () => { await harness.lifecycle.dispose(); await rm(dir, { recursive: true, force: true }); } };
}

test("an unmanaged loop is paused from its primary or current target thread, and from no other", async () => {
  const w = await world();
  try {
    const other = await w.harness.behavior.callRpc("control", { configPath: w.config, action: "pause", expectedThreadId: "thr_other" }) as { ok: boolean; message: string };
    assert.equal(other.ok, false); assert.equal(other.message, NOT_THIS_THREAD);
    assert.deepEqual(await w.calls(), [], "a refused control runs nothing");
    // No managedId key at all, as the card sends it for a loop from the plugin's own setting.
    const target = await w.harness.behavior.callRpc("control", { configPath: w.config, action: "pause", expectedThreadId: "thr_target" }) as { ok: boolean };
    assert.equal(target.ok, true);
    const primary = await w.harness.behavior.callRpc("control", { configPath: w.config, action: "stop", expectedThreadId: "thr_primary" }) as { ok: boolean };
    assert.equal(primary.ok, true);
    assert.deepEqual(await w.calls(), [`pause -c ${w.config}`, `stop -c ${w.config}`]);
  } finally { await w.dispose(); }
});

test("a control whose loop status cannot be read changes nothing", async () => {
  const w = await world();
  try {
    await writeFile(join(w.dir, "looptender-stand-in"), "#!/bin/sh\nexit 1\n", { mode: 0o700 });
    const r = await w.harness.behavior.callRpc("control", { configPath: w.config, action: "pause", expectedThreadId: "thr_target" }) as { ok: boolean; message: string };
    assert.equal(r.ok, false); assert.match(r.message, /could not be read/);
  } finally { await w.dispose(); }
});

test("a saved setup is paused only from the thread it was saved for", async () => {
  const w = await world();
  try {
    const record = { id: "setup_1", threadId: "thr_setup", environmentId: "env_example", projectId: "proj_example", hostId: "host_example", project: "/projects/example", provider: "example-provider", configPath: "/private/example/looptender.yaml", loop: "example-loop" };
    await w.bb.storage.kv.set("setups.v1", [record]);
    const refused = await w.harness.behavior.callRpc("control", { configPath: record.configPath, managedId: record.id, action: "pause", expectedThreadId: "thr_elsewhere" }) as { ok: boolean; message: string };
    assert.equal(refused.ok, false); assert.equal(refused.message, NOT_THIS_THREAD);
    assert.deepEqual(w.hostCalls, [], "nothing reached the host");
    const allowed = await w.harness.behavior.callRpc("control", { configPath: record.configPath, managedId: record.id, action: "pause", expectedThreadId: "thr_setup" }) as { ok: boolean };
    assert.equal(allowed.ok, true); assert.deepEqual(w.hostCalls, ["command:pause"]);
  } finally { await w.dispose(); }
});
