/** Host-owned tombstones. Existing/uncertain records never authorize another spawn. */
import { constants as C, openSync, closeSync, fstatSync, lstatSync, mkdirSync, writeSync, readSync, fsyncSync, unlinkSync, opendirSync, linkSync, type Stats } from "node:fs";
import { isAbsolute, join, parse, relative } from "node:path";
import { continuityDigest, parseContinuityRequest, parseContinuityReceipt, type ContinuityRequest, type ContinuityReceipt } from "./continuity-registration-contract.ts";
export const attemptRecordLimit = 16 * 1024;
export const attemptAggregateLimit = 8 * 1024 * 1024;
type Phase = "before_claim_write" | "before_claim_sync" | "claim_synced" | "before_terminal" | "before_terminal_sync" | "before_terminal_publish" | "after_terminal_publish" | "terminal_synced";
type Options = { aggregateLimit?: number; platform?: string; fault?: (phase: Phase) => void };
const fail = () => { throw Error("continuity_attempt_storage_unavailable"); };
function privateStat(s: Stats, directory: boolean) {
  if ((directory ? !s.isDirectory() : !s.isFile()) || s.uid !== process.getuid?.() || (s.mode & 0o077) !== 0 || (!directory && s.nlink !== 1)) fail();
}
function openDirectory(path: string) {
  if (!isAbsolute(path)) fail();
  let current = parse(path).root;
  for (const part of relative(current, path).split("/")) { current = join(current, part); if (!lstatSync(current).isDirectory()) fail(); }
  const fd = openSync(path, C.O_RDONLY | C.O_DIRECTORY | C.O_NOFOLLOW);
  try { privateStat(fstatSync(fd), true); return fd; } catch (e) { closeSync(fd); throw e; }
}
function readRecord(path: string): unknown {
  const fd = openSync(path, C.O_RDONLY | C.O_NOFOLLOW | C.O_NONBLOCK);
  try {
    const before = fstatSync(fd); privateStat(before, false);
    if (before.size > attemptRecordLimit) fail();
    const data = Buffer.alloc(attemptRecordLimit + 1); let size = 0, n: number;
    while ((n = readSync(fd, data, size, data.length - size, null)) > 0) { size += n; if (size > attemptRecordLimit) fail(); }
    const after = fstatSync(fd);
    if (before.ino !== after.ino || before.size !== after.size || before.mtimeMs !== after.mtimeMs || before.ctimeMs !== after.ctimeMs) fail();
    // Our records have one canonical serialization, so duplicate keys/alternate encodings refuse.
    const text = new TextDecoder("utf-8", { fatal: true }).decode(data.subarray(0, size)), value = JSON.parse(text);
    if (JSON.stringify(value) !== text) fail();
    return value;
  } finally { closeSync(fd); }
}
function writeExclusive(path: string, value: unknown, beforeSync?: () => void) {
  const bytes = Buffer.from(JSON.stringify(value)); if (bytes.length > attemptRecordLimit) fail();
  const fd = openSync(path, C.O_WRONLY | C.O_CREAT | C.O_EXCL | C.O_NOFOLLOW | C.O_NONBLOCK, 0o600);
  try { privateStat(fstatSync(fd), false); let at = 0; while (at < bytes.length) at += writeSync(fd, bytes, at, bytes.length - at); beforeSync?.(); fsyncSync(fd); }
  finally { closeSync(fd); }
}
export function createAttemptStore(dataDir: string, options: Options = {}) {
  if (!["darwin", "linux"].includes(options.platform ?? process.platform)) throw Error("continuity_receipts_unsupported");
  const limit = options.aggregateLimit ?? attemptAggregateLimit;
  if (!Number.isSafeInteger(limit) || limit < 3 * attemptRecordLimit || limit > attemptAggregateLimit) fail();
  const root = join(dataDir, "continuity-attempts-v2");
  const key = (r: ContinuityRequest) => continuityDigest([r.namespace, r.attempt]);
  const rootFd = (create: boolean) => {
    const parent = openDirectory(dataDir);
    try {
      if (create) { try { mkdirSync(root, { mode: 0o700 }); fsyncSync(parent); } catch (e) { if ((e as NodeJS.ErrnoException).code !== "EEXIST") throw e; } }
      return openDirectory(root);
    } finally { closeSync(parent); }
  };
  return {
    lookup(value: ContinuityRequest): { state: "unknown" } | { state: "settled"; receipt: ContinuityReceipt } {
      let rootHandle: number | undefined, dir: number | undefined;
      try {
        const r = parseContinuityRequest(value); rootHandle = rootFd(false);
        const path = join(root, key(r)); dir = openDirectory(path);
        if (parseContinuityRequest(readRecord(join(path, "claim.json"))).requestDigest !== r.requestDigest) fail();
        const receipt = parseContinuityReceipt(readRecord(join(path, "terminal.json")), r);
        fsyncSync(dir); fsyncSync(rootHandle);
        return { state: "settled", receipt };
      } catch { return { state: "unknown" }; }
      finally { if (dir !== undefined) closeSync(dir); if (rootHandle !== undefined) closeSync(rootHandle); }
    },
    reserve(value: ContinuityRequest): null | { commit(receipt: ContinuityReceipt): void } {
      const r = parseContinuityRequest(value), rootHandle = rootFd(true), path = join(root, key(r));
      let gate: number | undefined, dir: number | undefined;
      try {
        // A crash while holding admission fences new reservations; lookup never requires this gate.
        gate = openSync(join(root, ".admission"), C.O_WRONLY | C.O_CREAT | C.O_EXCL | C.O_NOFOLLOW, 0o600);
        let used = 0; const entries = opendirSync(root);
        try { for (let e = entries.readSync(); e; e = entries.readSync()) {
          if (e.name === ".admission") continue;
          if (!/^[a-f0-9]{64}$/.test(e.name) || !e.isDirectory()) fail();
          const children = opendirSync(join(root, e.name)); let count = 0;
          try { for (let child = children.readSync(); child; child = children.readSync()) {
            if (++count > 3 || !["claim.json", "terminal.pending", "terminal.json"].includes(child.name) || !child.isFile() || lstatSync(join(root, e.name, child.name)).size > attemptRecordLimit) fail();
          } } finally { children.closeSync(); }
          used += 3 * attemptRecordLimit;
          if (used > limit) throw Error("continuity_attempt_capacity");
        } } finally { entries.closeSync(); }
        try { lstatSync(path); return null; } catch (e) { if ((e as NodeJS.ErrnoException).code !== "ENOENT") throw e; }
        if (used + 3 * attemptRecordLimit > limit) throw Error("continuity_attempt_capacity");
        mkdirSync(path, { mode: 0o700 }); fsyncSync(rootHandle); dir = openDirectory(path);
        options.fault?.("before_claim_write");
        writeExclusive(join(path, "claim.json"), r, () => options.fault?.("before_claim_sync")); fsyncSync(dir); options.fault?.("claim_synced");
        let committed = false;
        const identity = fstatSync(dir);
        return { commit(receipt) {
          if (committed) fail(); committed = true;
          const parsed = parseContinuityReceipt(receipt, r), handle = openDirectory(path);
          try {
            const now = fstatSync(handle); if (now.ino !== identity.ino || now.dev !== identity.dev) fail();
            if (parseContinuityRequest(readRecord(join(path, "claim.json"))).requestDigest !== r.requestDigest) fail();
            options.fault?.("before_terminal"); writeExclusive(join(path, "terminal.pending"), parsed, () => options.fault?.("before_terminal_sync"));
            options.fault?.("before_terminal_publish");
            linkSync(join(path, "terminal.pending"), join(path, "terminal.json")); options.fault?.("after_terminal_publish"); unlinkSync(join(path, "terminal.pending"));
            fsyncSync(handle); options.fault?.("terminal_synced");
          } finally { closeSync(handle); }
        } };
      } finally {
        if (dir !== undefined) closeSync(dir);
        if (gate !== undefined) { closeSync(gate); unlinkSync(join(root, ".admission")); fsyncSync(rootHandle); }
        closeSync(rootHandle);
      }
    },
  };
}
