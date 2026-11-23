import { test } from "node:test";
import assert from "node:assert/strict";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createFakePluginHost } from "@get-bb/plugin-sdk/testing";
import { DEFAULT_PROGRAM, alternateProgram, configuredProgram, execProgram } from "./executable.ts";
import plugin from "./server.ts";
import { execute } from "./setup-host.ts";

// The earlier name, built so this file does not spell it.
const earlier = ["keep", "going"].join("");

test("the bb command is looptender", async () => {
  const { bb, harness } = createFakePluginHost({ pluginId: earlier, settings: { configs: "" } });
  await plugin(bb);
  assert.equal(harness.registrations.cli?.name, "looptender");
  for (const c of harness.registrations.cli?.commands ?? []) assert.match(String(c.usage), /^bb looptender /);
});

test("the path setting: the current key wins, a value saved under the earlier key is kept, and the default is looptender", () => {
  assert.equal(configuredProgram({}), DEFAULT_PROGRAM);
  assert.equal(configuredProgram({ looptenderPath: " ", keepgoingPath: "" }), "looptender");
  assert.equal(configuredProgram({ keepgoingPath: " /opt/example/bin/tool " }), "/opt/example/bin/tool");
  assert.equal(configuredProgram({ looptenderPath: "/opt/new/looptender", keepgoingPath: "/opt/old/tool" }), "/opt/new/looptender");
});

test("a bare name that is not found is tried once under the other name", async () => {
  assert.equal(alternateProgram("looptender"), earlier);
  assert.equal(alternateProgram(earlier), "looptender");
  assert.equal(alternateProgram("/opt/example/looptender"), null);
  const dir = await mkdtemp(join(tmpdir(), "lt-exec-"));
  try {
    // An install from before the rename: only the earlier name is on PATH.
    await writeFile(join(dir, earlier), "#!/bin/sh\necho \"ran ${0##*/} $*\"\n");
    await chmod(join(dir, earlier), 0o755);
    const env = { PATH: dir };
    const r = await execProgram("looptender", ["version"], { env });
    assert.equal(r.error, null); assert.equal(r.stdout.trim(), `ran ${earlier} version`);
    // A current install: the name asked for is run, with stdin passed on.
    await writeFile(join(dir, "looptender"), "#!/bin/sh\nread line; echo \"ran looptender $line\"\n");
    await chmod(join(dir, "looptender"), 0o755);
    const s = await execProgram("looptender", [], { env, input: "from stdin\n" });
    assert.equal(s.stdout.trim(), "ran looptender from stdin");
    // A saved record naming the earlier program finds the current one.
    await rm(join(dir, earlier));
    const t = await execProgram(earlier, [], { env, input: "saved\n" });
    assert.equal(t.stdout.trim(), "ran looptender saved");
    // A configured path is never swapped for another program.
    const u = await execProgram(join(dir, "absent"), [], { env });
    assert.equal(u.error?.code, "ENOENT");
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("setup's commands are retried under the other program name, with their exit status and error output kept", async () => {
  const dir = await mkdtemp(join(tmpdir(), "lt-setup-"));
  const path = process.env.PATH;
  try {
    // Only the earlier program is installed; it echoes its stdin as JSON, or fails.
    await writeFile(join(dir, earlier), "#!/bin/sh\nread line\nif [ \"$1\" = fail ]; then echo \"warning: fictional\" >&2; echo \"init: fictional failure\" >&2; exit 3; fi\necho \"{\\\"got\\\": \\\"$line $BB_THREAD_ID\\\"}\"\n");
    await chmod(join(dir, earlier), 0o755);
    process.env.PATH = dir;
    const ok = await execute("looptender", ["init"], "task\n", "thr_example");
    assert.equal(ok.code, 0); assert.deepEqual(ok.data, { got: "task thr_example" });
    const bad = await execute("looptender", ["fail"], "task\n", "thr_example");
    assert.equal(bad.code, 3); assert.match(String(bad.stderr), /init: fictional failure/);
  } finally {
    process.env.PATH = path;
    await rm(dir, { recursive: true, force: true });
  }
});
