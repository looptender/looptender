import Foundation

/// Fictional metadata and saved readings only; no app, CLI or provider is started.
@main struct MenuIdentitySmoke {
    static func main() throws {
        let now = Contract.date("2030-03-04T12:00:00Z")!
        let json = #"{"schemaVersion":1,"registry":"available","complete":true,"allClear":true,"needsYou":0,"unread":0,"omitted":0,"installedBuild":"v0.3.0","loops":[]}"#
        let list = try Contract.panelList(Data(json.utf8), exit: 0)
        var metadata: [String: Any] = ["CFBundleShortVersionString": "0.1.0", "LooptenderSourceRevision": String(repeating: "a", count: 40), "LooptenderSourceModified": true]
        let stamp = MenuBuildStamp.capture(metadata)
        metadata["LooptenderSourceRevision"] = String(repeating: "b", count: 40)
        precondition(stamp.text == "Menu 0.1.0 · build aaaaaaaa · modified source")
        precondition(MenuBuildStamp.capture([:]).text == "Menu unknown · build unknown")
        precondition(MenuBuildStamp(version: "bad\nvalue", revision: "../private").revision == nil)
        let menu = MenuBuilder.build(.read(list, at: now), now: now, notice: "Example action completed", buildStamp: stamp)
        precondition(menu.rows.first?.command == .openPanel)
        precondition(menu.rows[1].title == stamp.text && menu.rows[1].command == nil)
        precondition(menu.rows[2].title == "CLI v0.3.0 · last read")
        precondition(menu.rows.last?.title == "Example action completed")
        let failed = MenuBuilder.build(.unavailable, now: now, buildStamp: stamp)
        precondition(failed.rows[1].title == stamp.text && failed.rows[2].title == "CLI build unavailable")
        let offer = UpdateOffer(availableLabel: "bbbbbbbb", automatic: false)
        let changed = MenuBuilder.build(.read(list, at: now), now: now, update: offer, buildStamp: stamp)
        precondition(changed.rows.contains { $0.title == "Installed menu differs · bbbbbbbb" })
        precondition(MenuBuilder.updateMode(shown: menu.rows, next: changed.rows, itemCount: menu.rows.count, menuOpen: true, hasHighlight: true) == .deferred)
        let clock = MenuBuilder.build(.read(list, at: now), now: now.addingTimeInterval(60), notice: "Example action completed", buildStamp: stamp)
        precondition(MenuBuilder.sameShape(menu.rows, clock.rows))
        print("Native identity PASS: captured stamp, unknown/invalid metadata, separate receipt, CLI unavailable, installed mismatch and tracking guard")
    }
}
