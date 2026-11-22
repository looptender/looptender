import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFileSync } from "node:child_process";
import { labelIdentifier, stampedVersion, labelSource, stamp, insideGitWorkTree } from "./stamp-build.mjs";
import { BUILD_LABEL } from "./build-label.ts";
import { isBuildLabel, pluginBuildLine } from "./status-details.ts";

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

test("stamping refuses a git work tree, in the directory or any parent", () => {
  const root = mkdtempSync(join(tmpdir(), "stamp-git-"));
  try {
    mkdirSync(join(root, ".git"));
    const nested = join(root, "a", "b");
    mkdirSync(nested, { recursive: true });
    for (const dir of [root, nested]) {
      writeFileSync(join(dir, "package.json"), JSON.stringify({ name: "x", version: "0.3.1" }));
      assert.equal(insideGitWorkTree(dir), true);
      assert.throws(() => stamp(dir, "abc1234"), /refusing to stamp inside a git work tree/);
      assert.equal(JSON.parse(readFileSync(join(dir, "package.json"), "utf8")).version, "0.3.1");
    }
    const script = new URL("./stamp-build.mjs", import.meta.url).pathname;
    assert.throws(() => execFileSync("node", [script, "abc1234"], { cwd: nested, stdio: "pipe" }));
    assert.equal(JSON.parse(readFileSync(join(nested, "package.json"), "utf8")).version, "0.3.1");
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test("labels differing only in case, punctuation or after 48 characters give one version", () => {
  assert.equal(stampedVersion("0.3.1", "ABC.1"), stampedVersion("0.3.1", "abc-1"));
  assert.equal(stampedVersion("0.3.1", "x".repeat(60) + "1"), stampedVersion("0.3.1", "x".repeat(60) + "2"));
});

// In a stamped staged copy this checks nothing: the repository copy is what
// tests run on, and a stamped copy fails it by design.
test("the repository's own copy is not stamped", (t) => {
  const version = JSON.parse(readFileSync(new URL("./package.json", import.meta.url), "utf8")).version as string;
  if (version.includes("-build.")) { t.skip("this is a stamped staged copy"); return; }
  assert.equal(BUILD_LABEL, null);
  assert.match(version, /^\d+\.\d+\.\d+$/);
});

test("the plugin says which build it came from and whether that is the build it runs", () => {
  assert.match(pluginBuildLine(null, "v1"), /not stamped/);
  assert.match(pluginBuildLine("v1", null), /did not report/);
  assert.equal(pluginBuildLine("v1", "v1"), "Plugin: built from looptender v1, the build it runs.");
  const stale = pluginBuildLine("v1", "v2");
  assert.match(stale, /built from looptender v1; the looptender it runs is v2\. These are different builds\./); assert.match(stale, /After installing a new build/);
  // No claim about which side is newer or which is stale, in either direction.
  for (const line of [stale, pluginBuildLine("v2", "v1"), pluginBuildLine(null, "v1"), pluginBuildLine("v1", null), pluginBuildLine("v1", "v1")]) {
    assert.doesNotMatch(line, /newer|older|latest|outdated|stale|upgrade|behind|ahead/i);
  }
  assert.match(stale, /If one of them was installed recently, BB may still be serving the plugin from before it/);
});

test("the running build is only printed when it has the shape of a build label", () => {
  for (const ok of ["v0.2.0", "v0.2.0-dev.ccd2310fd946", "b1293f2b2115", "0b4e12399667-dirty"]) assert.equal(isBuildLabel(ok), true, ok);
  for (const bad of ["", "/Users/example/looptender", "two words", "x\ny", "-leading", "a".repeat(65), "$(rm)", "line one\nline two"]) assert.equal(isBuildLabel(bad), false, JSON.stringify(bad));
});
