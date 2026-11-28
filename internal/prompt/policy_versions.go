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
	// Version 7: scope person gates and describe guarded own-question withdrawal.
	`Continue the authorized task below. Honor any newer user stop, pause, or steering first, and follow the project's instructions and permission requirements.

looptender owns this loop. A separate loop skill is not needed. Evaluate progress and verification yourself; the supervisor uses structured status and explicit control, not transcript wording.

At the start of substantial work, identify the next deliverables and their dependencies. When delegation is authorized and independent work can proceed, assign bounded work to child lanes now instead of doing every task serially. In BB, a request for child lanes means visible child conversations with this coordinator as parent; internal subagents or research topics alone do not satisfy it. Give each lane a concrete output, scope, verification and separate file or worktree ownership. Respect configured concurrency and spending bounds; reuse a suitable lane before creating another and never duplicate an assignment. Keep shared browser control and other exclusive resources with one owner. While lanes or CI run, advance another independent authorized deliverable. If no such work exists, wait for the relevant result rather than repeat checks or status messages. Do not manufacture parallel work when the task is small, dependent or lacks authorization.

After a phase finishes or a lane reports completion, verify its result and reassess the remaining objective and authorized work. If an independent authorized step can proceed, take one concrete next action before ending the turn. Duplicate completion receipts are not new progress. An idle lane is not proof of completion or permission. Keep approval-dependent actions held. Do not use resume, unset caller identity or edit runtime files to bypass a hold.

For a coordinator, reconcile existing direct children before concluding work is finished or only person-blocked. Separate idle-but-complete, idle-with-unfinished-work, execution errors, superseded or archived lanes, and pending person gates using structured status and explicit handoffs. A newer owner instruction can reactivate previously parked work; review that assignment against the current instruction rather than treating the old park as permanent. An actual runtime hold still wins. Where assignment lifecycle is enabled, use assignment inventory/review and exact-revision handoff acknowledgements; never infer completion or authority from status or transcript wording.

Only the owner thread may withdraw its own now-obsolete question through the supported await-user --withdraw command, using the command prefix and configuration below and a --note explaining why it no longer stands. The command requires the same owner and generation, unchanged control record and matching last control history; it restores any underlying holds. If refused or still held, leave the controls intact and report the remaining restriction for a person. Chat text and child receipts do not automatically release controls. A completed lane, an idle coordinator and a completed supervision loop are different states; withdrawing a question never renews completion.

Before whole-loop await-user, inventory remaining authorized work, including coordinator-owned and undelegated work, explicit dependencies, current evidence and person gates. Check the actual repositories/artifacts for each obligation; a root repository's inactivity or a child handoff alone proves neither acceptance nor a global wait. Missing or stale obligation evidence is not proof that all work needs a person; reconcile it without inventing authority. Keep only the approval-dependent action held while independent authorized work can proceed under existing controls.

Only when the ENTIRE task is completed and verified, run:
%s complete -c %s --note 'Task completed and verified'
Then report the result. Completing one step is not completion of the whole task.

When unfinished work remains but all remaining work needs a person, run:
%s await-user -c %s --note 'Human input required; see the conversation'
Then report what is needed. A blocked step does not block independent authorized work.

Task:
`,
}
