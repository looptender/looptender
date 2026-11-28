import { createAttemptStore } from "./continuity-attempt-store.ts";
import { makeContinuityRequest, makeContinuityReceipt, parseContinuityRequest, type ContinuityReceipt } from "./continuity-registration-contract.ts";
import { inspectContinuityHost } from "./continuity-host-inspector.ts";
import { experimental_defineHostEntry } from "@get-bb/plugin-sdk/host";
import { hostContract } from "./host-contract.ts";
import { setupOnHost, commandOnHost, startServiceOnHost, standardRoutesOnHost, continuationCandidatesOnHost } from "./setup-host.ts";
import { transferOnHost } from "./transfer-host.ts";
import { attachmentOnHost } from "./attach-host.ts";
export default experimental_defineHostEntry({
  contract: hostContract,
  handlers: {
    continuityAttemptStatus: (input, context) => {
      if (!["darwin", "linux"].includes(process.platform)) return { state: "unsupported" as const };
      return createAttemptStore(context.experimental_paths.dataDir).lookup(parseContinuityRequest(input));
    },
    continuityCheckV2: async (input, context) => {
      const request = parseContinuityRequest(input.request);
      if (makeContinuityRequest(request.namespace, request.attempt, input.enrollment).requestDigest !== request.requestDigest) throw Error("continuity_request_mismatch");
      const store = createAttemptStore(context.experimental_paths.dataDir), claim = store.reserve(request);
      if (!claim) {
        const prior = store.lookup(request);
        if (prior.state !== "settled") throw Error("continuity_attempt_unknown");
        return { receipt: prior.receipt, inspection: { verdict: "cannot_assess" as const, objectiveComplete: false as const } };
      }
      let receipt: ContinuityReceipt | undefined;
      const inspection = await inspectContinuityHost(input.enrollment.paths, context.signal, undefined, kind => {
        const value = makeContinuityReceipt(request, kind); claim.commit(value); receipt = value;
      });
      if (!receipt) throw Error("continuity_attempt_unknown");
      return { receipt, inspection };
    },
    continuityCheck: async (input, context) => ({ attempt: input.attempt, settled: true as const, inspection: await inspectContinuityHost(input.enrollment.paths, context.signal) }),
    attachment: (input, context) => attachmentOnHost(input, context.signal),
    transfer: (input, context) => transferOnHost(input, context.signal),
    init: (input, context) => setupOnHost(input, context.signal),
    continuationCandidates: (input, context) => continuationCandidatesOnHost(input, context.signal),
    command: (input, context) =>
      input.operation === "start"
        ? startServiceOnHost({ bin: input.bin, record: input.record, enable: true, ...(input.json ? { json: input.json } : {}) }, context.signal)
        : input.operation === "startChecks"
          ? startServiceOnHost({ bin: input.bin, record: input.record, enable: false }, context.signal)
          : commandOnHost({ ...input, operation: input.operation }, context.signal),
    standardRoutes: (input, context) => standardRoutesOnHost(input, context.signal),
  },
});
