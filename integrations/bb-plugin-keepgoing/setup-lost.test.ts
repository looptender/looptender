import { test } from "node:test";
import assert from "node:assert/strict";
import { lostMessage } from "./setup-lost.ts";

test("a request BB's RPC layer refuses is said as that, with BB's own sentence; other error text is never shown", () => {
  const refused = new Error("rpc input at $.ticket is not a JSON value");
  assert.match(lostMessage("preview", refused, 0, "example-loop"), /^The preview did not finish: BB refused the request before it was sent \(rpc input at \$\.ticket is not a JSON value\); this is a fault in the plugin, not in looptender\. Nothing was created/);
  assert.match(lostMessage("preview", new Error('rpc "setup" input at $.a[0] is cyclic'), 1, "x"), /is cyclic/);
  const other = lostMessage("create", new Error("PRIVATE /private/path"), 3, "example-loop");
  assert.ok(!other.includes("PRIVATE")); assert.match(other, /request to the plugin failed after 3 s\. A switched-off setup named example-loop may have been written/);
  assert.ok(!lostMessage("preview", new Error("rpc input at $.x is not a JSON value; PRIVATE"), 0, "x").includes("PRIVATE"));
});
