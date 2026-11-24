import { spawnSync } from "node:child_process";
import { writeFile } from "node:fs/promises";

// A stand-in program is a script written a moment ago and then run by the code
// under test, which gives each such run a deadline (15 s for a status read).
// The first run of a new file on macOS is checked by the system and, on a busy
// or cold machine, takes seconds; a read that misses its deadline comes back
// as an error entry with no loops, and a test then reads loops[0] of nothing.
// So the first run is paid here, outside any deadline, as the Go tests do
// (internal/testexec). Only for a stand-in whose run has no effect the test
// observes (it prints a fixed answer or exits).
export async function writeStandIn(path: string, body: string): Promise<void> {
  await writeFile(path, body, { mode: 0o700 });
  warmUp(path, ["version"]);
}

// Runs an executable once so the system's first-run check is over, retrying
// when it was ended by a signal. Output and exit status are ignored.
export function warmUp(path: string, args: string[] = []): void {
  for (let attempt = 0; attempt < 6; attempt++) {
    const r = spawnSync(path, args, { stdio: "ignore", timeout: 120_000 });
    if (!r.error && r.signal === null) return;
  }
}
