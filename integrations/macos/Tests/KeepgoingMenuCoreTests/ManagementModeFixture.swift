import Foundation
#if canImport(KeepgoingMenuCore)
@testable import KeepgoingMenuCore
#endif

/// Fictional wire statuses/menu models only. Never opens a panel or runs a CLI.
/// Shared with the standalone smoke on hosts without XCTest.
enum ManagementModeFixture {
    static let exposure = "While changes are allowed, anyone who can use this browser window can pause, stop and resume loops, replace the file that runs a loop's checks with this panel's build, edit preferred routes and their account selections, and read selected-context Codex subscription quota."
    static let id = "0123456789abcdef0123456789abcdef"
    static let listJSON = #"{"schemaVersion":1,"registry":"available","complete":true,"allClear":true,"needsYou":0,"unread":0,"omitted":0,"loops":[],"accountQuotas":{"state":"available","complete":true,"omitted":0,"accounts":[{"id":"0123456789abcdef0123456789abcdef","label":"Example Codex","provider":"codex","summary":{"state":"unavailable","text":"Saved quota unavailable.","freshness":"unavailable","rows":[]}}]}}"#
    struct Failure: Error { let text: String }
    static func require(_ condition: Bool, _ message: String) throws {
        if !condition { throw Failure(text: message) }
    }
    static func status(mode: Any? = nil, running: Bool = true, changes: Any? = true, disclosure: String? = exposure) throws -> PanelStatus {
        var object: [String: Any] = ["schemaVersion": 1, "running": running]
        if let mode { object["mode"] = mode }
        if let changes { object["changesAllowed"] = changes }
        if let disclosure { object["exposure"] = disclosure }
        return try Contract.panelStatus(JSONSerialization.data(withJSONObject: object), exit: 0)
    }
    static func run() throws {
        // Actual current wire all is the only full mode; aliases/legacy/null do
        // not inherit management from changesAllowed, which also means controls.
        for mode: Any? in [nil, NSNull(), "read_only", "controls", "all", "full", "ALL", "", " all", "all ", "future_mode"] {
            for running in [false, true] {
                for changes: Any? in [nil, false, true] {
                    let value = try status(mode: mode, running: running, changes: changes)
                    let confirmation = ConfirmationBuilder.openWithControls(value)
                    let reuse = running && (mode as? String) == "all"
                    try require(value.runningInFullMode == reuse && value.requiresManagementRestart == (running && !reuse), "Launcher restart/reuse plan must use the exact mode")
                    try require((confirmation == nil) == reuse, "Only a running explicit all mode may skip confirmation")
                    if let confirmation {
                        try require(confirmation.defaultButton.command == nil && confirmation.buttons.first?.title == "Cancel", "Cancel must remain default")
                        try require(confirmation.buttons.count == 2 && !confirmation.buttons[1].destructive && confirmation.buttons[1].command == .openPanelWithChanges, "Management must use the existing confirmed launcher")
                        try require(confirmation.informativeText.contains(exposure), "Complete authority disclosure lost")
                        try require(confirmation.informativeText.contains("Opening does not sign in or run a model probe."), "Opening must not imply sign-in/probe")
                        try require(confirmation.informativeText.contains("current panel is stopped first") == running, "Restart warning must describe any weaker/unknown running panel")
                    }
                }
            }
        }
        for mode: Any in [true, 1, ["all"], ["value": "all"]] {
            var rejected = false
            do { _ = try status(mode: mode) } catch { rejected = true }
            try require(rejected, "Wrong-type mode accepted")
        }
        let list = try Contract.panelList(Data(listJSON.utf8), exit: 0)
        let now = Contract.date("2030-03-04T12:00:00Z")!
        let withAuthority = MenuBuilder.build(.read(list, at: now), now: now, panel: try status(mode: "controls"))
        try require(withAuthority.rows.first?.command == .openPanel, "Ordinary loop-controls entry removed")
        try require(withAuthority.rows.filter { $0.command == .openPanelWithChanges }.map(\.title) == ["Open Management Panel…"], "Management entry absent/ambiguous")
        for missing in [nil, try status(disclosure: nil)] {
            let menu = MenuBuilder.build(.read(list, at: now), now: now, panel: missing)
            try require(!menu.rows.contains { $0.command == .openPanelWithChanges }, "Missing disclosure must not offer management upgrade")
        }
        let account = MenuBuilder.quotaRows(list.accountQuotas, now: now)[1]
        try require(account.submenu.compactMap(\.command) == [.openAccount(id: id), .connectAccount(id: id)], "Keep view navigation separate from confirmed account management")
        let manage = account.submenu.first { $0.command == .connectAccount(id: id) }
        try require(manage?.title == "Manage Account in Panel…" && manage?.accessibilityLabel == "Manage account Example Codex in panel" && manage?.accessibilityHelp.contains("confirmed management launcher") == true, "Account management name/help must be clear without inferring disconnected auth")
        try require(!account.title.contains("reconnect") && account.detail == "Quota unavailable", "Unavailable quota is not sign-in evidence")
        try require(MenuBuilder.arguments(.openAccountWithChanges(id: id)) == ["panel", "--allow-changes", "--account", id], "Scoped full-mode opener changed")
        try require(MenuBuilder.arguments(.openAccountWithChanges(id: "../example")) == nil, "Invalid account ID accepted")
        // A real successful stand-in makes this fail if the intent is ever
        // admitted to direct execution; current behavior rejects before launch.
        let client = LocalClient(executable: URL(fileURLWithPath: "/usr/bin/true"), menuVersion: nil)
        var rejected = false
        do { try client.open(.connectAccount(id: id)) } catch { rejected = true }
        try require(rejected, "Account management intent bypassed the confirmed launcher")
        let old = MenuBuilder.build(.read(list, at: now), now: now)
        try require(MenuBuilder.updateMode(shown: old.rows, next: withAuthority.rows, itemCount: old.rows.count, menuOpen: true, hasHighlight: true) == .deferred, "New action must respect tracked-menu structural guard")
    }
}
