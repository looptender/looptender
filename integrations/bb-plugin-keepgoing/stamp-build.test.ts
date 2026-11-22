import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFileSync } from "node:child_process";
import { labelIdentifier, stampedVersion, labelSource, stamp } from "./stamp-build.mjs";
import { BUILD_LABEL } from "./build-label.ts";
import { pluginBuildLine } from "./status-details.ts";

const semver = /^\d+\.\d+\.\d+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$/;

test("each looptender build gives a distinct, valid plugin version", () => {
  for (const [label, want] of [
    ["v0.2.0-dev.ccd2310fd946", "0.3.1-build.v0-2-0-dev-ccd2310fd946"],
    ["b1293f2b2115", "0.3.1-build.b1293f2b2115"],
    ["12345678", "0.3.1-build.b12345678"],
    ["0b4e12399667-dirty", "0.3.1-build.0b4e12399667-dirty"],
  ] as const) {
    const v = stampedVersion("0.3.1", label);
    assert.equal(v, want); assert.match(v, semver);
  }
  assert.notEqual(stampedVersion("0.3.1", "aaa1111"), stampedVersion("0.3.1", "bbb2222"));
  // Stamping twice from a stamped version starts again from the base.
  assert.equal(stampedVersion("0.3.1-build.old", "new1"), "0.3.1-build.new1");
  assert.throws(() => stampedVersion("0.3", "x")); assert.throws(() => labelIdentifier("///"));
  assert.throws(() => labelSource("a b")); assert.throws(() => labelSource('x"; evil'));
});

test("stamping rewrites only the staged copy's version and label", () => {
  const dir = mkdtempSync(join(tmpdir(), "stamp-"));
  try {
    writeFileSync(join(dir, "package.json"), JSON.stringify({ name: "x", version: "0.3.1", engines: { bb: ">=0.48" } }));
    writeFileSync(join(dir, "build-label.ts"), "export const BUILD_LABEL: string | null = null;\n");
    assert.equal(stamp(dir, "v0.2.0-dev.abc1234"), "0.3.1-build.v0-2-0-dev-abc1234");
    const pkg = JSON.parse(readFileSync(join(dir, "package.json"), "utf8"));
    assert.equal(pkg.version, "0.3.1-build.v0-2-0-dev-abc1234"); assert.deepEqual(pkg.engines, { bb: ">=0.48" });
    assert.match(readFileSync(join(dir, "build-label.ts"), "utf8"), /BUILD_LABEL: string \| null = "v0\.2\.0-dev\.abc1234";/);
    // The command line does the same and refuses a bad call.
    const script = new URL("./stamp-build.mjs", import.meta.url).pathname;
    assert.equal(execFileSync("node", [script, "zzz9999"], { cwd: dir }).toString().trim(), "0.3.1-build.zzz9999");
    assert.throws(() => execFileSync("node", [script], { cwd: dir, stdio: "pipe" }));
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test("the repository's own copy is never stamped", () => {
  assert.equal(BUILD_LABEL, null);
  assert.match(JSON.parse(readFileSync(new URL("./package.json", import.meta.url), "utf8")).version, /^\d+\.\d+\.\d+$/);
});

test("the plugin says which build it came from and whether that is the build it runs", () => {
  assert.match(pluginBuildLine(null, "v1"), /not stamped/);
  assert.match(pluginBuildLine("v1", null), /did not report/);
  assert.equal(pluginBuildLine("v1", "v1"), "Plugin: built from looptender v1, the build it runs.");
  const stale = pluginBuildLine("v1", "v2");
  assert.match(stale, /built from looptender v1, but the looptender it runs is v2/); assert.match(stale, /After installing a new build/);
  assert.doesNotMatch(stale, /newer than|older than/);
});
