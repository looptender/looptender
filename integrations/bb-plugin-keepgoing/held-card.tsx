// What a held loop's card says about getting it going again, and the one place
// a "resume from your phone" link will go. The card never offers a Resume of
// its own: BB gives a plugin no proof that a person acted, so resuming stays a
// person's, from the looptender panel or a terminal on their computer.
import type { LoopView } from "./server";

const WHERE = "from the looptender panel or a terminal on your computer";

/** One plain sentence for a held loop, true for its own state. Null for a loop that is not held. */
export function heldSentence(controlState: string): string | null {
  switch (controlState) {
    case "enabled": return null;
    case "awaiting user": return `Held for you. Resume it ${WHERE}.`;
    case "paused": return `Paused. Resume it ${WHERE}.`;
    // A stop is not resumed like a pause: it is carried out at the next check, and stays until a person lifts it.
    case "stop requested": return `Stop requested. It stays stopped until a person lifts the stop ${WHERE}.`;
    default: return `Held. Resume it ${WHERE}.`;
  }
}

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

/** The "resume from your phone" row: a link, rendered only when a phone path is configured. */
export function PhoneResumeLink({ loop, url = phoneResumeUrl(loop) }: { loop: LoopView; url?: string }) {
  return url ? <a data-kg="link" href={url} target="_blank" rel="noreferrer noopener" className="kg-t">Resume from your phone</a> : null;
}
