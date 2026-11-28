// What a held loop's card says about getting it going again, and the one place
// a "resume from your phone" link will go. The card never offers a Resume of
// its own: BB gives a plugin no proof that a person acted, so resuming stays a
// person's, from the looptender panel (a plain `looptender panel` can resume
// after its review) or from a terminal outside BB (BB's own terminal refuses a
// resume for a thread's loop). The wording is agreed with the panel's own.
import type { LoopView } from "./server";

const WHERE = "from the looptender panel (looptender panel), or from a terminal outside BB with looptender resume";

/** The words for what a wait waits for, as the panel says them. */
const WAIT_FOR: Record<string, string> = { "host-session": "the login session", network: "the network", time: "a time", "limit-reset": "a usage-limit reset" };
export const waitFor = (until: string): string => WAIT_FOR[until.split(":")[0]!] ?? "a condition";

/** True when the loop observes an external sessionpipe session. */
export const isObserved = (loop: LoopView): boolean => !!loop.details?.observed || loop.details?.targetAdapter === "sessionpipe";

/** One plain sentence for a held loop, true for its own state. Null for a loop that is not held. */
export function heldSentence(loop: LoopView): string | null {
  if (loop.details?.deadline?.reached === true && loop.controlState !== "enabled") return "Deadline reached. Review loop.limits.deadline in this loop's configuration before resuming. Resume cannot change the deadline; existing holds remain.";
  // A wait is saved as an awaiting-user hold but needs nobody: it carries on by itself.
  if (loop.details?.wait && loop.controlState === "awaiting user") return `Waiting for ${waitFor(loop.details.wait.until)}. It carries on by itself; nothing to do.`;
  if (isObserved(loop)) {
    const wakes = !!loop.details?.observedWakes;
    switch (loop.controlState) {
      case "enabled": return null;
      case "awaiting user": return wakes
        ? `Held for you. Resume checking (nothing is sent by resuming; idle wakes continue afterwards) ${WHERE}.`
        : `Held for you. Resume checking (nothing is sent) ${WHERE}.`;
      case "paused": return wakes
        ? `Paused. Resume checking (idle wakes continue afterwards) ${WHERE}.`
        : `Paused. Resume checking (nothing is sent) ${WHERE}.`;
      case "stop requested": return wakes
        ? `Stop requested (checking ends; session is left alone). Resume checking ${WHERE}.`
        : `Stop requested (checking ends; session is left alone). Resume checking (nothing is sent) ${WHERE}.`;
      case "completed": return `Completion declared for this loop. Project and current thread work are not verified; new messages do not clear this hold. Review the configured task, routes, limits and other holds before a person resumes checking ${WHERE}; resume clears other holds too.`;
      case "disabled": return "Switched off. Turn it on from the looptender panel (looptender panel), or from a terminal outside BB with looptender enable.";
      default: return wakes ? `Held. Resume checking ${WHERE}.` : `Held. Resume checking (nothing is sent) ${WHERE}.`;
    }
  }
  switch (loop.controlState) {
    case "enabled": return null;
    case "awaiting user": return `Held for you. Resume it ${WHERE}.`;
    case "paused": return `Paused. Resume it ${WHERE}.`;
    // A stop is not resumed like a pause: it is carried out at the next check, and `resume` lifts it.
    case "stop requested": return `Stop requested. It stays stopped until a person resumes it ${WHERE}; resuming lifts the stop.`;
    case "completed": return `Completion declared for this loop. Project and current thread work are not verified; new messages do not clear this hold. Review the configured task, routes, limits and other holds before a person resumes it ${WHERE}; resume clears other holds too.`;
    case "disabled": return "Switched off. Turn it on from the looptender panel (looptender panel), or from a terminal outside BB with looptender enable.";
    default: return `Held. Resume it ${WHERE}.`;
  }
}

/** True when the loop waits on its own condition and needs no person. */
export const isWait = (loop: LoopView): boolean => !!loop.details?.wait && loop.controlState === "awaiting user";

/** The command a person runs in a terminal. Shown in the loop's details, not in the sentence above. */
export const resumeCommand = (loop: LoopView): string => `looptender resume -c ${JSON.stringify(loop.configPath)}`;

/**
 * Where a "resume from your phone" link comes from, once a phone path (a chat
 * the person already trusts, opened from the card) is set up and reviewed.
 * Returns undefined until then, and the card then shows nothing for it.
 * It is a link to that path, never a button that resumes from here.
 */
export function phoneResumeUrl(_loop: LoopView): string | undefined {
  return undefined;
}

/** Only an https URL is ever linked. */
export const isHttps = (url: string | undefined): url is string => { try { return !!url && new URL(url).protocol === "https:"; } catch { return false; } };

/** The "resume from your phone" row: a link, rendered only when a phone path is configured and is https. */
export function PhoneResumeLink({ loop, url = phoneResumeUrl(loop) }: { loop: LoopView; url?: string }) {
  return isHttps(url) ? <a data-kg="link" href={url} target="_blank" rel="noreferrer noopener" className="kg-t">Resume from your phone</a> : null;
}
