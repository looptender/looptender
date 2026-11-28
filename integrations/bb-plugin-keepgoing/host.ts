import { inspectContinuityHost } from "./continuity-host-inspector.ts";
import { experimental_defineHostEntry } from "@get-bb/plugin-sdk/host";
import { hostContract } from "./host-contract.ts";
import { setupOnHost, commandOnHost, startServiceOnHost, standardRoutesOnHost, continuationCandidatesOnHost } from "./setup-host.ts";
import { transferOnHost } from "./transfer-host.ts";
import { attachmentOnHost } from "./attach-host.ts";
export default experimental_defineHostEntry({
  contract: hostContract,
  handlers: {
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
