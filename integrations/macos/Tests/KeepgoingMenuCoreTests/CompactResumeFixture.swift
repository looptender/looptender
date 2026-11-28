import Foundation
#if canImport(KeepgoingMenuCore)
@testable import KeepgoingMenuCore
#endif

/// Shared actual-core fictional fixture; no command is executed.
enum CompactResumeFixture {
    struct Failure: Error { let message: String }
    static func check(_ value: Bool, _ message: String) throws {
        if !value { throw Failure(message: message) }
    }
    static let key = "l0123456789ab"
    static let expect = "r1.1893499320." + String(repeating: "c1", count: 32) + "." + String(repeating: "d2", count: 16)
    static let boilerplate = [
        "Now: Waiting for you. Last check: checked; sent nothing · 3 min ago.",
        "Its current route: BB thread · model not configured.",
        "Confirming prepares this resume's own ticket, so a resume another client prepared and has not used yet can no longer be used.",
        "If the loop's control record changes before you confirm, nothing is changed and you are asked to review again.",
        "This review expires in 2 min (12:02 UTC)."
    ]
    static func review(_ lines: [String], action: String = "resume", title: String = "Resume example-project?") throws -> Review {
        let object: [String: Any] = ["schemaVersion": 1, "preview": ["action": action, "key": key, "title": title, "lines": lines,
            "confirm": "Resume example-project", "expect": expect, "expiresAt": "2030-01-01T12:02:00Z",
            "lost": "Outcome unknown. Check this loop before trying again.", "refused": "Nothing was changed.", "expired": "Review expired. Review again."]]
        guard case .review(let value) = try Contract.review(JSONSerialization.data(withJSONObject: object), exit: 0, action: LoopAction(rawValue: action)!, key: key) else { throw Failure(message: "Missing review") }
        return value
    }
    static func loop(key: String = key, name: String = "Example project") -> PanelLoop {
        PanelLoop(key: key, name: "example-project", state: "awaiting_user", phrase: "Waiting for you", needsYou: true, naming: PanelNaming(displayName: name))
    }
    static func run() throws {
        let cases = [
            ["Resume clears: paused and stop requested.", "No turn starts now; the next check decides what to send.", "Next check: In 2 min (12:02 UTC)."],
            ["Resume clears: waiting for you and the note left with the hold.", "Supervision is off for this loop, and resume does not turn it on."],
            ["Resume clears: finished.", "It also removes the record of the question that was open for a person when the loop was marked finished."],
            ["No hold is recorded, so resume clears none.", "Resume continues checking; nothing is ever sent to this session."],
            ["Resume clears: paused.", "Resume continues checking and idle wakes; nothing is sent by resuming, and the next check decides whether the idle session should be woken."],
            ["Resume clears: waiting for you.", "Next check: unknown; the supervisor has not reported a time."],
            ["Deadline reached. Review loop.limits.deadline before resuming. Resume cannot change the deadline."],
            ["The hold was raised because re-prompts or nudges were spent, so resume starts a new error episode: those counts start over. Loop-wide limits are unchanged."]
        ]
        for lines in cases {
            let r = try review(lines + boilerplate)
            let c = ConfirmationBuilder.build(r, loop: loop())
            try check(c.messageText == "Resume Example project?", "Human display name missing")
            try check(c.buttons.map(\.title) == ["Cancel", "Resume"] && c.defaultButton.command == nil, "Concise labels/default cancel lost")
            try check(c.buttons[1].command == .apply(.resume, key: key, expect: expect, lanes: false, immediately: false), "Intent identity/ticket changed")
            try check(!c.informativeText.contains("ticket") && !c.informativeText.contains("Last check:") && !c.informativeText.contains("current route:"), "Generic mechanics still primary")
            for line in lines where !line.hasPrefix("It also removes") { try check(c.informativeText.contains(Contract.text(line)), "Actual state/consequence lost: \(line)") }
            try check(!ConfirmationBuilder.current(r, now: r.expiresAt) && ConfirmationBuilder.current(r, now: r.expiresAt.addingTimeInterval(-1)), "Expiry safeguard changed")
        }
        let r = try review(["Resume does not turn supervision on or start a turn; the next check decides what to send.", "This loop is supervised for a conversation. Resuming here clears the holds that conversation or looptender set; it does not answer any question the conversation asked."])
        let c = ConfirmationBuilder.build(r)
        try check(c.informativeText == "No turn starts now; the next check decides what to send.\nThis does not answer a question in the conversation.", "Question/turn meaning changed")
        try check(ConfirmationBuilder.build(r, loop: loop(key: "l000000000000")).messageText == r.title, "Another loop's name used")
        try check(ConfirmationBuilder.build(r, loop: loop(name: String(repeating: "Long name ", count: 30))).messageText.count <= 89, "Long heading unbounded")
        let observed = try review(["Resume continues checking; nothing is ever sent to this session."], title: "Resume checking example-project?")
        try check(ConfirmationBuilder.build(observed, loop: loop()).messageText == "Resume checking Example project?", "Observed-session checking scope lost")
        let future = try review(["New safety consequence from a future CLI."])
        try check(ConfirmationBuilder.build(future).informativeText == future.lines[0], "Unknown future consequence hidden")
        let pause = try review(boilerplate, action: "pause")
        try check(ConfirmationBuilder.build(pause).informativeText == boilerplate.joined(separator: "\n\n"), "Other action copy changed")
        let json = try JSONSerialization.data(withJSONObject: ["schemaVersion": 1, "result": ["outcome": "refused", "text": "Deadline reached. Review loop.limits.deadline. Nothing was changed."]])
        try check(try Contract.review(json, exit: 1, action: .resume, key: key) == .refused(ChangeResult(outcome: "refused", text: "Deadline reached. Review loop.limits.deadline. Nothing was changed.")), "Actionable refusal changed")
    }
}
