import XCTest
@testable import KeepgoingMenuCore

final class SectionsTests: XCTestCase {
    let now = Date(timeIntervalSince1970: 1_900_000_000)

    func loop(_ i: Int, _ name: String, _ state: String = "supervising", _ phrase: String = "Supervising", needs: Bool = false,
              display: String? = nil, short: String? = nil, archived: Bool = false, superseded: Bool? = nil) -> PanelLoop {
        PanelLoop(key: String(format: "l%012x", i), name: name, state: state, phrase: phrase, needsYou: needs,
                  naming: display.map { PanelNaming(displayName: $0, shortName: short) }, archived: archived ? PanelArchived(at: "2030-01-01T00:00:00Z") : nil, superseded: superseded)
    }
    func list(_ loops: [PanelLoop], needsYou: Int? = nil, unread: Int = 0) -> PanelList {
        let n = needsYou ?? loops.filter(\.needsYou).count
        return PanelList(schemaVersion: 1, registry: "available", complete: true, allClear: n == 0, needsYou: n, unread: unread, omitted: 0, loops: loops)
    }
    func build(_ l: PanelList) -> MenuModel { MenuBuilder.build(.read(l, at: now), now: now) }
    func titles(_ m: MenuModel) -> [String] { m.rows.filter { $0.kind != .separator }.map(\.title) }

    func testLoopsAreGroupedNeedsYouThenWorkingThenOneCollapsedItem() {
        let m = build(list([
            loop(1, "ci-watch"), loop(2, "docs", "waiting_for_you", "Waiting for you", needs: true),
            loop(3, "paused-one", "paused", "Paused"), loop(4, "off-one", "off", "Off"), loop(5, "done", "finished", "Finished"),
            loop(6, "stopped-one", "stopped", "Stopped"), loop(7, "old", archived: true), loop(8, "api", "waiting_on_limit", "Waiting on a limit"),
        ]))
        XCTAssertEqual(titles(m).prefix(8).map { $0 }, ["Open looptender Panel", "1 loop needs you", "Needs you", "docs", "Working", "ci-watch", "api", "Other loops (5)"])
        let other = m.rows.first { $0.title.hasPrefix("Other loops") }!
        XCTAssertEqual(other.submenu.map(\.title), ["paused-one", "off-one", "done", "stopped-one", "old"])
        XCTAssertEqual(other.submenu.map(\.detail), ["Paused", "Off", "Finished", "Stopped", "Archived"])
        // Only the first two groups are listed at the top level.
        XCTAssertEqual(m.rows.filter { $0.kind == .loop }.map(\.title), ["docs", "ci-watch", "api"])
    }

    func testAnArchivedOrReplacedLoopNeverCountsAsNeedingYou() {
        let stale = loop(1, "retired-coordinator", "waiting_for_you", "Waiting for you", needs: true, superseded: true)
        let archived = loop(2, "archived-one", "waiting_for_you", "Waiting for you", needs: true, archived: true)
        let live = loop(3, "live", "waiting_for_you", "Waiting for you", needs: true)
        // Display phrase "Superseded" alone without structured superseded field is not retired.
        let phraseOnly = loop(4, "phrase-only", "waiting_for_you", "Superseded", needs: true)
        XCTAssertEqual(MenuBuilder.group(phraseOnly), .needsYou)
        // The CLI's own count (3) includes the stale holds: the menu does not.
        let m = build(list([stale, archived, live], needsYou: 3))
        XCTAssertEqual(m.rows[1].title, "1 loop needs you")
        XCTAssertEqual(m.badge, "1")
        XCTAssertEqual(m.iconAccessibilityLabel, "looptender, 1 loop needs you")
        XCTAssertEqual(m.rows.filter { $0.kind == .loop }.map(\.title), ["live"], "never in the top two groups")
        XCTAssertEqual(m.rows.first { $0.title.hasPrefix("Other loops") }?.submenu.map(\.detail), ["Replaced", "Archived"])
        // With nothing but stale holds, nothing needs a person.
        let none = build(list([stale, archived], needsYou: 2))
        XCTAssertEqual(none.badge, "")
        XCTAssertFalse(titles(none).contains { $0.contains("need") })
        // A paused loop that still waits for a person is in Needs you.
        XCTAssertEqual(MenuBuilder.group(loop(5, "p", "paused", "Paused", needs: true)), .needsYou)
        // A 3-day inactive loop is folded into Other loops and excluded from needs-you count.
        var folded = loop(6, "inactive-old", "waiting_for_you", "Waiting for you", needs: true)
        folded.inactive = true
        XCTAssertEqual(MenuBuilder.group(folded), .other)
        let withFolded = build(list([folded], needsYou: 1))
        XCTAssertEqual(withFolded.badge, "")
        XCTAssertFalse(titles(withFolded).contains { $0.contains("need") })
    }

    func testARowIsTheDisplayNameCutAtTheEndAndOneStateWord() {
        let long = "looptender-coordinator-claude-3-with-a-long-tail-name"
        let m = build(list([loop(1, "slug-one", display: long, short: "looptender-coordinator-claude-…"), loop(2, long), loop(3, "bb-thr-ab12cd34ef"), loop(4, "thr_cd56ef78gh"), loop(5, "plain", display: "Billing cleanup")]))
        let rows = m.rows.filter { $0.kind == .loop }
        XCTAssertEqual(rows[0].title, "looptender-coordinator-claude-…", "the CLI's own short form, cut at its end")
        XCTAssertEqual(rows[1].title.count, MenuBuilder.maxName)
        XCTAssertEqual(MenuBuilder.maxName, 32, "as long as the panel's own short name")
        XCTAssertTrue(rows[1].title.hasPrefix("looptender-coordinator-claude-3"))
        XCTAssertTrue(rows[1].title.hasSuffix("…"))
        XCTAssertFalse(rows[1].title.dropLast().contains("…"), "never in the middle")
        XCTAssertEqual(rows[1].toolTip, long)
        XCTAssertEqual(rows[2].title, "A loop", "a bb-thr- thread ID in disguise is never shown")
        XCTAssertEqual(rows[3].title, "A loop", "a thr_ thread ID in disguise is never shown")
        XCTAssertEqual(rows[4].title, "Billing cleanup", "the display name, not the registered one")
        XCTAssertTrue(rows.allSatisfy { $0.detail == "Working" })
        XCTAssertEqual(MenuBuilder.shortened("abcdefghij", to: 5), "abcd…")
        XCTAssertEqual(MenuBuilder.shortened("abcde", to: 5), "abcde")
    }

    func testCollidingTruncatedNamesKeepADistinguishingTail() {
        let prefix = "looptender-coordinator-claude-3-shared-"
        let m = build(list([loop(1, "a", display: prefix + "alpha"), loop(2, "b", display: prefix + "beta")]))
        let titles = m.rows.filter { $0.kind == .loop }.map(\.title)
        XCTAssertEqual(titles.count, 2)
        XCTAssertNotEqual(titles[0], titles[1])
        XCTAssertTrue(titles[0].hasSuffix("alpha"))
        XCTAssertTrue(titles[1].hasSuffix("beta"))
        XCTAssertTrue(titles.allSatisfy { $0.count <= MenuBuilder.maxName && $0.contains("…") })
    }

    func testAllLoopsRowAppearsWheneverAGroupIsTruncatedEvenBelowFiftyTotal() {
        let loops = (1...26).map { loop($0, "work-\($0)") }
        let m = build(list(loops))
        XCTAssertEqual(m.rows.filter { $0.kind == .loop }.count, 25)
        XCTAssertTrue(titles(m).contains("All 26 Loops in the Panel"))
    }

    func testALoopThatNeedsYouSaysWhatItWaitsForNotThatItNeedsYou() {
        func word(_ kind: String?) -> String {
            var l = loop(1, "a", "waiting_for_you", "Waiting for you", needs: true)
            l.waiting = kind.map { PanelWaiting(kind: $0, by: "agent") }
            return MenuBuilder.stateWord(l)
        }
        XCTAssertEqual(word("asked"), "Asked you")
        XCTAssertEqual(word("held"), "On hold")
        XCTAssertEqual(word("checks"), "Checks stopped")
        XCTAssertEqual(word(nil), "Needs you")
        XCTAssertEqual(word("a_kind_from_a_newer_build"), "Needs you")
    }

    func testStateWordsAreShortAndFixed() {
        func word(_ state: String, _ phrase: String = "x", needs: Bool = false) -> String { MenuBuilder.stateWord(loop(1, "a", state, phrase, needs: needs)) }
        XCTAssertEqual(word("supervising"), "Working")
        XCTAssertEqual(word("waiting_for_you", needs: true), "Needs you")
        XCTAssertEqual(word("paused"), "Paused")
        XCTAssertEqual(word("waiting_on_limit"), "At a limit")
        XCTAssertEqual(word("a_state_from_a_newer_build", String(repeating: "word ", count: 20)).count, 24, "an unknown state keeps the CLI's phrase, cut at its end")
        XCTAssertTrue(["supervising", "paused", "off", "stopped", "finished", "waiting_on_limit", "waiting_on_network", "checks_failing", "not_checked_recently", "outside_hours"].allSatisfy { word($0).count <= 16 })
    }

    func testAdapterModelBuildAndTimesAreInTheRowsSubmenu() {
        var l = loop(1, "api")
        l.provider = "claude-code"; l.model = "example-model"; l.recordedBuild = "v0.1.0"; l.buildDiffers = true
        l.checkedAt = "2030-03-17T17:43:20Z"
        l.reason = "It is working."
        let at = Contract.date(l.checkedAt)!
        let m = MenuBuilder.build(.read(PanelList(schemaVersion: 1, registry: "available", complete: true, allClear: true, needsYou: 0, unread: 0, omitted: 0, loops: [l], installedBuild: "v0.2.0"), at: at.addingTimeInterval(1620)), now: at.addingTimeInterval(1620))
        let row = m.rows.first { $0.kind == .loop }!
        XCTAssertEqual(row.title, "api")
        XCTAssertEqual(row.detail, "Working")
        XCTAssertFalse(row.detail.contains("claude-code") || row.detail.contains("v0.1.0") || row.detail.contains("ago"))
        XCTAssertEqual(row.submenu.prefix(4).map(\.title), ["It is working.", "claude-code example-model", "last check ran v0.1.0 · installed v0.2.0", "Checked 27 min ago"])
    }

    func testAccountsAreOneItemAndOnlyTheOnesAtALimitAreOnTop() throws {
        let base = #"{"state":"reported","text":"Saved selected context; identity not verified.","source":"codex account/rateLimits/read","observedAt":"2030-03-04T12:00:00Z","freshness":"stale","partial":true,"rows":[{"name":"Codex · 5-hour","text":"37% used · 63% remaining","usedPercent":37,"remainingPercent":63,"resetsAt":"2030-03-04T17:00:00Z"},{"name":"Codex · Credits","text":"Balance 12.75 provider units; currency not reported"}],"failedRefreshAt":"2030-03-04T13:00:00Z","failedRefreshText":"Timed out; earlier evidence retains its time."}"#
        func quota(_ label: String, _ used: Int) -> String {
            let summary = base.replacingOccurrences(of: "37% used · 63% remaining", with: "\(used)% used · \(100 - used)% remaining").replacingOccurrences(of: "\"usedPercent\":37", with: "\"usedPercent\":\(used)").replacingOccurrences(of: "\"remainingPercent\":63", with: "\"remainingPercent\":\(100 - used)")
            return "{\"label\":\"\(label)\",\"provider\":\"codex\",\"summary\":" + summary + "}"
        }
        func doc(_ accounts: [String]) -> Data {
            Data(("{\"schemaVersion\":1,\"registry\":\"available\",\"complete\":true,\"allClear\":true,\"needsYou\":0,\"unread\":0,\"omitted\":0,\"loops\":[],\"accountQuotas\":{\"state\":\"available\",\"complete\":true,\"omitted\":0,\"accounts\":[" + accounts.joined(separator: ",") + "]}}").utf8)
        }
        let json = doc([quota("Quiet One", 10), quota("Near Limit", 92), quota("Quiet Two", 30), quota("At Limit", 99), quota("Third Full", 95)])
        let m = build(try Contract.panelList(json, exit: 0))
        let top = m.rows.filter { $0.symbol == "gauge.with.dots.needle.33percent" }.map(\.title)
        XCTAssertEqual(top, ["At Limit", "Third Full"], "top two by highest usage window descending")
        let accounts = try XCTUnwrap(m.rows.first { $0.title == "Accounts" })
        XCTAssertEqual(accounts.submenu.filter { $0.symbol == "gauge.with.dots.needle.33percent" }.count, 5, "all of them are in the submenu")
        // An account whose first window is low but weekly window is 95% is promoted, and ties keep list order.
        let weekly = #"{"label":"Weekly High","provider":"codex","summary":{"state":"reported","text":"Saved selected context; identity not verified.","source":"codex account/rateLimits/read","observedAt":"2030-03-04T12:00:00Z","freshness":"fresh","rows":[{"name":"Codex · 5-hour","text":"10% used · 90% remaining","usedPercent":10,"remainingPercent":90},{"name":"Codex · Weekly","text":"95% used · 5% remaining","usedPercent":95,"remainingPercent":5}]}}"#
        let ended = #"{"label":"Ended High","provider":"codex","summary":{"state":"reported","text":"Saved selected context; identity not verified.","source":"codex account/rateLimits/read","observedAt":"2030-03-04T12:00:00Z","freshness":"fresh","rows":[{"name":"Codex · 5-hour","text":"100% used · 0% remaining","usedPercent":100,"remainingPercent":0,"ended":true}]}}"#
        let tieA = quota("Tie A", 95)
        let ranked = build(try Contract.panelList(doc([ended, quota("Low", 85), tieA, weekly]), exit: 0))
        let rankedTop = ranked.rows.filter { $0.symbol == "gauge.with.dots.needle.33percent" }
        XCTAssertEqual(rankedTop.map(\.title), ["Tie A", "Weekly High"], "ties keep list order; ended windows are ignored")
        XCTAssertTrue(rankedTop[1].detail.contains("95.0% used"))
        // With none near a limit, nothing but the one item.
        let calm = build(try Contract.panelList(doc([quota("Quiet One", 10), quota("Quiet Two", 30), quota("Half", 79)]), exit: 0))
        XCTAssertEqual(calm.rows.filter { $0.symbol == "gauge.with.dots.needle.33percent" }.count, 0)
        XCTAssertNotNil(calm.rows.first { $0.title == "Accounts" })
    }

    func testTheBuildLineIsOneShortLineOnlyWhenSomethingIsNotOnTheInstalledBuild() {
        func fresh(_ state: String) -> PanelFreshness { PanelFreshness(state: state, text: state) }
        var a = loop(1, "a"); a.buildFreshness = fresh("current")
        var b = loop(2, "b"); b.buildFreshness = fresh("pending")
        var c = loop(3, "c", "paused", "Paused"); c.buildFreshness = fresh("different")
        var gone = loop(4, "gone", archived: true); gone.buildFreshness = fresh("older")
        XCTAssertTrue(titles(build(list([a, b, c, gone]))).contains("2 supervisors not on the installed build"), "an archived loop is not counted")
        XCTAssertTrue(titles(build(list([a, b]))).contains("1 supervisor not on the installed build"))
        XCTAssertFalse(titles(build(list([a]))).contains { $0.contains("supervisor") })
        XCTAssertFalse(titles(build(list([a]))).contains { $0.hasPrefix("Build ") })
    }

    func testTheBottomIsTidyAndRecentIsASubmenu() {
        var l = list([loop(1, "a")])
        l.canAll = ["pause", "stop"]
        let view = NoticeView(recent: [RecentNotice(text: "a stopped.", at: now.addingTimeInterval(-60))], on: NoticeSettings.map(nil))
        let m = MenuBuilder.build(.read(l, at: now), now: now, panel: PanelStatus(schemaVersion: 1, running: true, idleRemainingSeconds: 600, exposure: "x"), update: UpdateOffer(availableLabel: nil, automatic: true), notices: view)
        let bottom = titles(m).drop { $0 != "Pause All Loops…" }
        XCTAssertEqual(Array(bottom), ["Pause All Loops…", "Stop All Loops…", "Stop the Panel Page", "Recent", "Notify me when…", "Update the menu automatically", "Quit looptender Menu", "Read just now"])
        XCTAssertEqual(m.rows.first { $0.title == "Recent" }?.submenu.map(\.title), ["a stopped. · 1 min ago"])
        // Every bottom row that can be chosen has an icon.
        XCTAssertTrue(m.rows.filter { $0.isEnabled && $0.kind == .action }.allSatisfy { !$0.symbol.isEmpty })
    }

    func testNoRowCarriesAThreadIDPathNoteOrAccountIdentity() {
        let l = loop(1, "bb-thr-ab12cd34ef", "waiting_for_you", "Waiting for you", needs: true)
        let m = build(list([l]))
        func all(_ rows: [MenuRow]) -> [String] { rows.flatMap { [$0.title, $0.detail, $0.toolTip, $0.accessibilityLabel] + all($0.submenu) } }
        for text in all(m.rows) { XCTAssertFalse(text.contains("bb-thr-") || text.contains("thr_") || text.contains("/Users/"), text) }
    }
}
