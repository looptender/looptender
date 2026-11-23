import { test } from "node:test";
import assert from "node:assert/strict";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createFakePluginHost } from "@get-bb/plugin-sdk/testing";
import { DEFAULT_PROGRAM, alternateProgram, configuredProgram, execProgram } from "./executable.ts";
import plugin from "./server.ts";

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
