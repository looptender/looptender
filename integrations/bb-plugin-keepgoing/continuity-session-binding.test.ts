import { test } from "node:test";
import assert from "node:assert/strict";
import { decodeContinuityEnrollments, selectContinuitySession } from "./continuity-session-binding.ts";
const identity = { threadId: "thread-example", projectId: "project-example", environmentId: "environment-example", hostId: "host-example" };
const entry = () => ({ binding: { id: "work-example", revision: 1, ...identity }, paths: { work: "/private/example/WORK.md", previous: "/private/example/HANDOFF.md" } });
test("exact tuple selects intact pointer; every foreign dimension refuses", () => {
  const entries = decodeContinuityEnrollments([entry()]);
  const selected = selectContinuitySession(entries, identity);
  assert.equal(selected.state, "bound");
  if (selected.state !== "bound") assert.fail();
  assert.ok(selected.instructions.includes(JSON.stringify(entry().paths)));
  assert.ok(selected.instructions.includes("A child finishing is not project completion"));
  for (const key of Object.keys(identity) as (keyof typeof identity)[]) {
    assert.deepEqual(selectContinuitySession(entries, { ...identity, [key]: "foreign" }), { state: "unbound" });
  }
});
test("decoded bindings and paths are detached and immutable", () => {
  const raw = entry(), decoded = decodeContinuityEnrollments([raw]);
  raw.paths.work = "/changed"; raw.binding.hostId = "changed";
  assert.equal(decoded[0].paths.work, "/private/example/WORK.md");
  assert.equal(decoded[0].binding.hostId, identity.hostId);
  assert.ok(Object.isFrozen(decoded) && Object.isFrozen(decoded[0]) && Object.isFrozen(decoded[0].paths) && Object.isFrozen(decoded[0].binding));
});
test("ambiguous or malformed enrollment refuses whole replacement", () => {
  const raw = entry();
  for (const bad of [null, {}, Array(1), [raw, raw], [raw, { ...entry(), binding: { ...raw.binding, id: "second" } }],
    [{ ...raw, extra: true }], [{ ...raw, binding: { ...raw.binding, revision: 0 } }],
    [{ ...raw, paths: { work: "relative/WORK.md" } }], [{ ...raw, paths: { work: "/work\ncommand" } }],
    [{ ...raw, paths: { work: "/work", unknown: "/extra" } }], [{ ...raw, paths: { work: "/work", previous: null } }]]) {
    assert.throws(() => decodeContinuityEnrollments(bad));
  }
});
test("hundreds of enrollments retained; overflow refuses without modifying input", () => {
  const raw = Array.from({ length: 256 }, (_, i) => ({ ...entry(), binding: { ...entry().binding, id: `work-${i}`, threadId: `thread-${i}` } }));
  assert.equal(decodeContinuityEnrollments(raw).length, 256);
  const huge = [{ ...entry(), paths: { work: "/" + "x".repeat(256 * 1024) } }];
  const before = JSON.stringify(huge);
  assert.throws(() => decodeContinuityEnrollments(huge), /storage_bound/);
  assert.equal(JSON.stringify(huge), before);
});
test("oversized pointer keeps enrollment and exposes unavailability instead of clipping", () => {
  const entries = decodeContinuityEnrollments([{ ...entry(), paths: { work: "/" + "x".repeat(4096) } }]);
  assert.deepEqual(selectContinuitySession(entries, identity), { state: "unavailable", reason: "instruction_too_large", bindingId: "work-example", revision: 1 });
  assert.equal(entries[0].paths.work.length, 4097);
});
test("shell-like path remains quoted JSON data", () => {
  const paths = { work: '/example/"$(example) `example`/WORK.md' };
  const result = selectContinuitySession(decodeContinuityEnrollments([{ ...entry(), paths }]), identity);
  assert.equal(result.state, "bound");
  if (result.state !== "bound") assert.fail();
  const line = result.instructions.split("\n").find(l => l.startsWith("Host-local file references (JSON): "))!;
  assert.deepEqual(JSON.parse(line.slice("Host-local file references (JSON): ".length)), paths);
});

test("current SDK configuration selects only exact context and preserves existing skill selection", async () => {
  const { createFakePluginHost, makePluginAgentConfigurationContext } = await import("@get-bb/plugin-sdk/testing");
  const { bb, harness } = createFakePluginHost({ pluginId: "continuity-fixture", agentSkillIds: ["keepgoing"] });
  let entries = decodeContinuityEnrollments([entry()]);
  bb.agents.configure(context => {
    const selected = selectContinuitySession(entries, { threadId: context.thread.id, projectId: context.project.id,
      environmentId: context.environment.id, hostId: context.host.id });
    return { tools: [], skills: ["keepgoing"], ...(selected.state === "bound" ? { instructions: selected.instructions } : {}) };
  });
  const context = makePluginAgentConfigurationContext({ thread: { id: identity.threadId }, project: { id: identity.projectId },
    environment: { id: identity.environmentId }, host: { id: identity.hostId } });
  try {
    const selected = await harness.behavior.resolveAgentConfiguration(context);
    assert.ok(JSON.stringify(selected).includes("A child finishing is not project completion"));
    assert.ok(JSON.stringify(selected).includes("keepgoing"));
    const child = await harness.behavior.resolveAgentConfiguration({ ...context, thread: { ...context.thread, id: "child-example", parentThreadId: identity.threadId },
      pluginMetadata: { work: "/injected/WORK.md" } });
    assert.ok(!JSON.stringify(child).includes("Host-local file references"));
    entries = decodeContinuityEnrollments([]);
    const detached = await harness.behavior.resolveAgentConfiguration(context);
    assert.ok(!JSON.stringify(detached).includes("Host-local file references"));
  } finally { await harness.lifecycle.dispose(); }
});
