// Stamps a staged copy of this plugin with the looptender build it is built
// from, so each build is a distinct plugin version and the plugin can say
// which build it came from:
//
//   node stamp-build.mjs <build-label>      (run in the staged copy, before `bb plugin build`)
//
// It rewrites two files of that copy: package.json's version becomes
// <base>-build.<label> (a semver prerelease), and build-label.ts exports the
// label. Never run it in the repository's own checkout.
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

/** A label as a semver prerelease identifier: lowercase letters, digits and hyphens, never all digits. */
export function labelIdentifier(label) {
  const id = String(label).toLowerCase().replace(/[^0-9a-z-]+/g, "-").replace(/-+/g, "-").replace(/^-|-$/g, "").slice(0, 48);
  if (id === "") throw new Error("the build label has no usable characters");
  return /^[0-9]+$/.test(id) ? `b${id}` : id;
}

/** The plugin version for a base version and a build label, for example 0.3.1-build.v0-2-0-dev-ccd2310. */
export function stampedVersion(base, label) {
  const bare = String(base).split("-")[0].split("+")[0];
  if (!/^\d+\.\d+\.\d+$/.test(bare)) throw new Error(`the base version ${JSON.stringify(base)} is not major.minor.patch`);
  return `${bare}-build.${labelIdentifier(label)}`;
}

/** The text of build-label.ts for a label. */
export function labelSource(label) {
  if (!/^[0-9A-Za-z._+-]{1,64}$/.test(label)) throw new Error("the build label is not a plain label");
  return `// Written by stamp-build.mjs: the looptender build this plugin was built from.\nexport const BUILD_LABEL: string | null = ${JSON.stringify(label)};\n`;
}

export function stamp(dir, label) {
  const pkgPath = `${dir}/package.json`;
  const pkg = JSON.parse(readFileSync(pkgPath, "utf8"));
  const version = stampedVersion(pkg.version, label);
  const source = labelSource(label);
  pkg.version = version;
  writeFileSync(pkgPath, `${JSON.stringify(pkg, null, 2)}\n`);
  writeFileSync(`${dir}/build-label.ts`, source);
  return version;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const label = process.argv[2];
  if (!label || process.argv.length !== 3) {
    console.error("usage: node stamp-build.mjs <build-label>");
    process.exit(2);
  }
  try { console.log(stamp(process.cwd(), label)); } catch (e) { console.error(`stamp-build: ${e.message}`); process.exit(1); }
}
