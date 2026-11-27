# PR readiness helper (unreleased)

`looptender pr-ready` is a read-only, single-inspection helper beside `ci-wait`. It has no loop configuration or runtime state, makes no model calls, and never merges, queues, marks ready, rebases or edits a PR. It uses an already authenticated `gh` on github.com; it never handles credentials itself.

```sh
looptender pr-ready --repo example-org/example-app --pr 42
looptender pr-ready --repo example-org/example-app --pr 42 --json
```

Exit 0 prints the full current PR head commit, suitable for the person's subsequent `--match-head-commit`. Exit 1 means nonready, unknown or unsupported evidence. Exit 2 means invalid arguments. JSON reports `ready`, `reason`, `head`, `base` and `merge` when known; a `head` in a nonready JSON report is **not** a recommendation to merge. `--gh PATH` selects the executable, without a shell.

## Supported contract

Every condition below must hold in two identical full evidence reads:

- The PR is open, is not a draft, and GitHub separately reports `MERGEABLE` and `CLEAN`. These fields are distinct; mergeability alone does not establish policy or check success.
- The current base ref target agrees with the PR base identity. The potential synthetic merge has exactly two parents, in order: current base commit, current PR head. A stale synthetic merge refuses readiness even if every reported check is green.
- Review and thread inventories are complete. Every thread is resolved, including outdated threads. Latest submitted review per author is APPROVED on the current head or DISMISSED. A later approval supersedes that author's earlier change request; duplicate IDs or equal timestamps are ambiguous. Unknown/pending review states, change requests and stale approvals refuse. GitHub's required-review decision must be APPROVED when present; configured required reviews without a decision refuse. Legacy branch protection without a structured review decision is also unsupported, even when that branch might not require approvals.
- General PR comments and latest COMMENTED reviews are unsupported: neither supplies a structured finding-resolution state. Their text is never read or classified. Even an all-clear prose comment can therefore prevent exit 0. The helper cannot establish independent-review acceptance or resolve findings recorded outside these structured interfaces.
- An explicit required-check policy is available from effective branch rules and/or legacy branch protection, with each required context bound to an app ID. Missing policy, wildcard/unknown app identity or missing required checks refuse. Unsupported branch-rule types refuse; policy is never weakened by omission.
- Both current-head and synthetic-merge check inventories are complete and nonempty. Every observed check must have a known positive ID, name and app ID, the exact queried commit, completed status and **success** conclusion. Each observed head name/app pair must also have a successful result explicitly bound to the verified synthetic merge. Additional merge checks must pass too. Required checks and all observed checks are separate obligations.
- Duplicate name/app pairs are unsupported, including replaced or rerun attempts: no attempt is silently selected as newest. Same names from different apps remain separate obligations. Duplicate IDs, pagination and mismatched counts refuse.
- Legacy commit statuses are unsupported because their identity cannot be matched to the required app-bound check identity here. Their inventories must be complete and empty. Skipped, neutral, cancelled, failed, pending and unknown check results refuse, including optional checks. `ci-wait` retains its existing success/skipped/neutral wait-completion policy; that policy does not establish readiness or explain a skip.

The [Checks API](https://docs.github.com/en/rest/checks/runs#list-check-runs-for-a-git-reference) binds each check run to `head_sha`. A green head-only Actions run or PR rollup does **not** prove which base or checkout was tested. This helper only supports checks explicitly attached to the verified synthetic merge commit; it does not infer checkout identity from a workflow name, run timestamp, latest run, PR association or log prose. The repository's present Actions setup may therefore be unsupported. Supporting another evidence mechanism requires its own reviewed contract and fixtures.

Effective `merge_queue` rules report `queue_required`. A queue-protected PR can be BLOCKED despite passing prequeue checks; this helper does not clear queue checks or submit to a queue. Other non-CLEAN states report `merge_policy_not_clean`. If queue policy cannot be read, the result is unknown rather than an inferred queue approval.

## Bounds and freshness

Each full read uses six structured API calls: GraphQL PR/reviews/threads/protection and synthetic parents, REST head checks/statuses, REST synthetic-merge checks/statuses, and effective branch rules. There are at most twelve calls, a shared 30-second deadline (plus at most one second of subprocess pipe cleanup), 256 KiB stdout per call, no retries and no polling. Stderr is discarded. Connections are limited to 100 records and must report complete counts/no next page; a full 100-rule page is unsupported because it cannot prove completeness. API errors, missing/null fields, permission failures or oversized output fail closed with `unknown_evidence` or an incomplete/unsupported reason.

The second full read must match all captured structured identities and revisions, including check IDs, attempt timestamps, review IDs, policy parameters, PR/review updates and head/base/merge identities. Check output, review bodies and comment prose are not compared. A concurrent movement or replacement reports `evidence_changed`. Responses can change during either read, and GitHub does not provide an atomic snapshot; matching reads cannot guarantee that nothing changes immediately afterward or detect a change-and-revert between observations. A person still owns review, current policy and the pinned merge decision. No local or remote repository mutation is performed by the helper.

## Evidence and gaps

`internal/prready/ready_test.go` and `cmd/looptender/prready_test.go` use fictional evidence for success, drafts/conflicts, stale merges, changing head/base/target/review/attempt/policy, missing/empty/truncated inventories, ambiguous check attempts, app identity, review states, queue policy, skips/cancellations and bounded failures. These establish source behavior only. No live successful readiness, queue integration, installed release, independent-review acceptance or native Windows/Linux execution is established by those fixtures. FC5 remains open for these unsupported contracts and live acceptance.
