type Action = "preview" | "create" | "inspect" | "start";

/**
 * What to say when the call to the plugin itself fails (it never reached an
 * answer). Said from what is known: which step, how long it ran, the error's
 * name when it has one (never its text, which can carry paths or task input).
 * "May already exist" is said only for a creation, the one step that writes.
 */
export function lostMessage(action: Action, error: unknown, seconds: number, name: string): string {
  const errName = error instanceof Error ? error.name : "";
  const timedOut = errName === "TimeoutError" || errName === "AbortError";
  const after = `after ${seconds} s`;
  // BB's RPC layer refuses a malformed request with a fixed sentence about a
  // path in it; that sentence carries no data of the person's, so it is shown.
  // No other error text is.
  const refused = error instanceof Error && /^rpc( "[A-Za-z]+")? (input|result) at [\w$.[\]]{1,80} (is not a JSON value|contains a non-finite number|is cyclic)$/.test(error.message) ? error.message : "";
  const why = refused ? `BB refused the request before it was sent (${refused}); this is a fault in the plugin, not in looptender` : timedOut ? `the request timed out ${after}` : `the request to the plugin failed ${after}${errName && errName !== "Error" ? ` (${errName})` : ""}`;
  switch (action) {
    case "preview":
      return `The preview did not finish: ${why}. Nothing was created; a preview never writes anything. Try again, and if it keeps failing run \`looptender doctor\` on the host.`;
    case "create":
      return `Creating the setup did not finish: ${why}. A switched-off setup named ${name} may have been written before that. Refresh status and review or remove it before trying again.`;
    case "start":
      return `Starting did not finish: ${why}. Refresh diagnostics to see whether supervision started; do not start it twice.`;
    default:
      return `Setup status could not be read: ${why}. Check the host connection and try again.`;
  }
}

