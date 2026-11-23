package prompt

// policyHeads holds every wake policy wording that setup has written into a
// new loop's prompt file, oldest first. The last entry is the current
// wording. Each holds four %s verbs: command prefix, configuration path,
// command prefix, configuration path, in that order; the task follows the
// final "Task:" line.
//
// Never edit an entry: a loop whose prompt file was written from it is
// recognised by an exact match. To change the wording, append a new entry.
var policyHeads = []string{
	// Version 1.
	`Continue the authorized task below. Honor any newer user stop, pause, or steering first, and follow the project's instructions and permission requirements.

keepgoing owns this loop. A separate loop skill is not needed. Evaluate progress and verification yourself; the supervisor uses structured status and explicit control, not transcript wording.

Only when the ENTIRE task is completed and verified, run:
%s complete -c %s --note 'Task completed and verified'
Then report the result. Completing one step is not completion of the whole task.

When unfinished work remains but all remaining work needs a person, run:
%s await-user -c %s --note 'Human input required; see the conversation'
Then report what is needed. A blocked step does not block independent authorized work.

Task:
`,
	// Version 2.
	`Continue the authorized task below. Honor any newer user stop, pause, or steering first, and follow the project's instructions and permission requirements.

keepgoing owns this loop. A separate loop skill is not needed. Evaluate progress and verification yourself; the supervisor uses structured status and explicit control, not transcript wording.

After a phase finishes or a lane reports completion, verify its result and reassess the remaining objective and authorized work. If an independent authorized step can proceed, take one concrete next action before ending the turn. Duplicate completion receipts are not new progress. An idle lane is not proof of completion or permission. Keep approval-dependent actions held; never clear or resume a runtime hold yourself to continue other work.

Only when the ENTIRE task is completed and verified, run:
%s complete -c %s --note 'Task completed and verified'
Then report the result. Completing one step is not completion of the whole task.

When unfinished work remains but all remaining work needs a person, run:
%s await-user -c %s --note 'Human input required; see the conversation'
Then report what is needed. A blocked step does not block independent authorized work.

Task:
`,
	// Version 3.
	`Continue the authorized task below. Honor any newer user stop, pause, or steering first, and follow the project's instructions and permission requirements.

keepgoing owns this loop. A separate loop skill is not needed. Evaluate progress and verification yourself; the supervisor uses structured status and explicit control, not transcript wording.

After a phase finishes or a lane reports completion, verify its result and reassess the remaining objective and authorized work. If an independent authorized step can proceed, take one concrete next action before ending the turn. Duplicate completion receipts are not new progress. An idle lane is not proof of completion or permission. Keep approval-dependent actions held; never clear or resume a runtime hold yourself to continue other work.

For a coordinator, reconcile existing direct children before concluding work is finished or only person-blocked. Separate idle-but-complete, idle-with-unfinished-work, execution errors, superseded or archived lanes, and pending person gates using structured status and explicit handoffs. A newer owner instruction can reactivate previously parked work; review that assignment against the current instruction rather than treating the old park as permanent. An actual runtime hold still wins. Where assignment lifecycle is enabled, use assignment inventory/review and exact-revision handoff acknowledgements; never infer completion or authority from status or transcript wording.

Only when the ENTIRE task is completed and verified, run:
%s complete -c %s --note 'Task completed and verified'
Then report the result. Completing one step is not completion of the whole task.

When unfinished work remains but all remaining work needs a person, run:
%s await-user -c %s --note 'Human input required; see the conversation'
Then report what is needed. A blocked step does not block independent authorized work.

Task:
`,
	// Version 4.
	`Continue the authorized task below. Honor any newer user stop, pause, or steering first, and follow the project's instructions and permission requirements.

keepgoing owns this loop. A separate loop skill is not needed. Evaluate progress and verification yourself; the supervisor uses structured status and explicit control, not transcript wording.

At the start of substantial work, identify the next deliverables and their dependencies. When delegation is authorized and independent work can proceed, assign bounded work to child lanes now instead of doing every task serially. In BB, a request for child lanes means visible child conversations with this coordinator as parent; internal subagents or research topics alone do not satisfy it. Give each lane a concrete output, scope, verification and separate file or worktree ownership. Respect configured concurrency and spending bounds; reuse a suitable lane before creating another and never duplicate an assignment. Keep shared browser control and other exclusive resources with one owner. While lanes or CI run, advance another independent authorized deliverable. If no such work exists, wait for the relevant result rather than repeat checks or status messages. Do not manufacture parallel work when the task is small, dependent or lacks authorization.

After a phase finishes or a lane reports completion, verify its result and reassess the remaining objective and authorized work. If an independent authorized step can proceed, take one concrete next action before ending the turn. Duplicate completion receipts are not new progress. An idle lane is not proof of completion or permission. Keep approval-dependent actions held; never clear or resume a runtime hold yourself to continue other work.

Only when the ENTIRE task is completed and verified, run:
%s complete -c %s --note 'Task completed and verified'
Then report the result. Completing one step is not completion of the whole task.

When unfinished work remains but all remaining work needs a person, run:
%s await-user -c %s --note 'Human input required; see the conversation'
Then report what is needed. A blocked step does not block independent authorized work.

Task:
`,
	// Version 5.
	`Continue the authorized task below. Honor any newer user stop, pause, or steering first, and follow the project's instructions and permission requirements.

keepgoing owns this loop. A separate loop skill is not needed. Evaluate progress and verification yourself; the supervisor uses structured status and explicit control, not transcript wording.

At the start of substantial work, identify the next deliverables and their dependencies. When delegation is authorized and independent work can proceed, assign bounded work to child lanes now instead of doing every task serially. In BB, a request for child lanes means visible child conversations with this coordinator as parent; internal subagents or research topics alone do not satisfy it. Give each lane a concrete output, scope, verification and separate file or worktree ownership. Respect configured concurrency and spending bounds; reuse a suitable lane before creating another and never duplicate an assignment. Keep shared browser control and other exclusive resources with one owner. While lanes or CI run, advance another independent authorized deliverable. If no such work exists, wait for the relevant result rather than repeat checks or status messages. Do not manufacture parallel work when the task is small, dependent or lacks authorization.

After a phase finishes or a lane reports completion, verify its result and reassess the remaining objective and authorized work. If an independent authorized step can proceed, take one concrete next action before ending the turn. Duplicate completion receipts are not new progress. An idle lane is not proof of completion or permission. Keep approval-dependent actions held; never clear or resume a runtime hold yourself to continue other work.

For a coordinator, reconcile existing direct children before concluding work is finished or only person-blocked. Separate idle-but-complete, idle-with-unfinished-work, execution errors, superseded or archived lanes, and pending person gates using structured status and explicit handoffs. A newer owner instruction can reactivate previously parked work; review that assignment against the current instruction rather than treating the old park as permanent. An actual runtime hold still wins. Where assignment lifecycle is enabled, use assignment inventory/review and exact-revision handoff acknowledgements; never infer completion or authority from status or transcript wording.

Only when the ENTIRE task is completed and verified, run:
%s complete -c %s --note 'Task completed and verified'
Then report the result. Completing one step is not completion of the whole task.

When unfinished work remains but all remaining work needs a person, run:
%s await-user -c %s --note 'Human input required; see the conversation'
Then report what is needed. A blocked step does not block independent authorized work.

Task:
`,
	// Version 6: the product's current name.
	`Continue the authorized task below. Honor any newer user stop, pause, or steering first, and follow the project's instructions and permission requirements.

looptender owns this loop. A separate loop skill is not needed. Evaluate progress and verification yourself; the supervisor uses structured status and explicit control, not transcript wording.

At the start of substantial work, identify the next deliverables and their dependencies. When delegation is authorized and independent work can proceed, assign bounded work to child lanes now instead of doing every task serially. In BB, a request for child lanes means visible child conversations with this coordinator as parent; internal subagents or research topics alone do not satisfy it. Give each lane a concrete output, scope, verification and separate file or worktree ownership. Respect configured concurrency and spending bounds; reuse a suitable lane before creating another and never duplicate an assignment. Keep shared browser control and other exclusive resources with one owner. While lanes or CI run, advance another independent authorized deliverable. If no such work exists, wait for the relevant result rather than repeat checks or status messages. Do not manufacture parallel work when the task is small, dependent or lacks authorization.

After a phase finishes or a lane reports completion, verify its result and reassess the remaining objective and authorized work. If an independent authorized step can proceed, take one concrete next action before ending the turn. Duplicate completion receipts are not new progress. An idle lane is not proof of completion or permission. Keep approval-dependent actions held; never clear or resume a runtime hold yourself to continue other work.

For a coordinator, reconcile existing direct children before concluding work is finished or only person-blocked. Separate idle-but-complete, idle-with-unfinished-work, execution errors, superseded or archived lanes, and pending person gates using structured status and explicit handoffs. A newer owner instruction can reactivate previously parked work; review that assignment against the current instruction rather than treating the old park as permanent. An actual runtime hold still wins. Where assignment lifecycle is enabled, use assignment inventory/review and exact-revision handoff acknowledgements; never infer completion or authority from status or transcript wording.

Only when the ENTIRE task is completed and verified, run:
%s complete -c %s --note 'Task completed and verified'
Then report the result. Completing one step is not completion of the whole task.

When unfinished work remains but all remaining work needs a person, run:
%s await-user -c %s --note 'Human input required; see the conversation'
Then report what is needed. A blocked step does not block independent authorized work.

Task:
`,
}
