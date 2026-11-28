import { z } from "zod";

// Matches looptender init --task-file - (initTaskLimit). A task is sent once
// in each RPC request; even JSON's worst-case sixfold escaping fits the BB
// host RPC's 32 MiB envelope. The CLI's stdout cap is independent of stdin.
export const TASK_MAX_BYTES = 1 << 20;
export const TASK_SIZE_MESSAGE = "Task exceeds 1048576 UTF-8 bytes (1 MiB). Keep a copy of the full text before revising it.";
export const taskBytes = (task: string): number => new TextEncoder().encode(task).byteLength;

/** Never trim, clip or replace input. Reject lone surrogates before UTF-8
 * encoding could silently replace them with a different character. */
export function taskTextProblem(task: string): string | null {
  if (taskBytes(task) > TASK_MAX_BYTES) return TASK_SIZE_MESSAGE;
  if (!task.trim()) return "Enter a task and its completion criteria.";
  for (const character of task) {
    const point = character.codePointAt(0)!;
    if (point >= 0xd800 && point <= 0xdfff) return "Task contains an incomplete Unicode character. Repair it before trying again; the text has not been changed.";
  }
  return null;
}

export const taskTextSchema = z.string().superRefine((task, context) => {
  const message = taskTextProblem(task);
  if (message) context.addIssue({ code: "custom", message });
});
