import { execFile, type ExecFileOptions } from "node:child_process";

// The program this plugin runs, by bare name when no path is configured.
// Releases up to v0.2.0 installed it under the product's earlier name, and
// saved records may name it so: a bare name of either that is not found is
// tried once under the other, so either install keeps working.
export const DEFAULT_PROGRAM = "looptender";
const EARLIER_PROGRAM = "keepgoing";

/** The other bare name to try when bin is not found, or null. */
export function alternateProgram(bin: string): string | null {
  if (bin === DEFAULT_PROGRAM) return EARLIER_PROGRAM;
  if (bin === EARLIER_PROGRAM) return DEFAULT_PROGRAM;
  return null;
}

/**
 * The executable path settings. looptenderPath wins; the setting under the
 * earlier key is read when it is empty, so a value saved before is kept.
 */
export function configuredProgram(settings: { looptenderPath?: string; keepgoingPath?: string }): string {
  return settings.looptenderPath?.trim() || settings.keepgoingPath?.trim() || DEFAULT_PROGRAM;
}

export type ExecResult = { error: (Error & { code?: unknown; killed?: boolean }) | null; stdout: string; stderr: string };

/** execFile, retried once under the other bare name when bin is not found. */
export function execProgram(bin: string, args: string[], options: ExecFileOptions & { input?: string }): Promise<ExecResult> {
  const once = (name: string) => new Promise<ExecResult>((resolve) => {
    const { input, ...opts } = options;
    const child = execFile(name, args, { ...opts, encoding: "utf8" }, (error, stdout, stderr) => {
      resolve({ error: error as ExecResult["error"], stdout: String(stdout), stderr: String(stderr) });
    });
    if (input !== undefined) {
      child.stdin?.on("error", () => { /* early exit/EPIPE is reported by the callback */ });
      child.stdin?.end(input);
    }
  });
  return once(bin).then((r) => {
    const other = alternateProgram(bin);
    return r.error?.code === "ENOENT" && other !== null ? once(other) : r;
  });
}
