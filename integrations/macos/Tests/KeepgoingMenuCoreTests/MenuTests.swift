import AppKit
import XCTest
@testable import KeepgoingMenuCore

final class MenuTests: XCTestCase {
    let now = Date(timeIntervalSince1970: 1_900_000_000)

    func loop(_ index: Int, _ name: String, _ state: String, _ phrase: String, needsYou: Bool = false) -> PanelLoop {
        PanelLoop(key: String(format: "l%012x", index), name: name, state: state, phrase: phrase, needsYou: needsYou)
    }
    func list(_ loops: [PanelLoop], needsYou: Int = 0, unread: Int = 0, complete: Bool = true, registry: String = "available", omitted: Int = 0) -> PanelList {
        PanelList(schemaVersion: 1, registry: registry, complete: complete, allClear: registry == "available" && complete && needsYou == 0, needsYou: needsYou, unread: unread, omitted: omitted, loops: loops)
    }
    var three: PanelList {
        list([loop(1, "example-api", "waiting_for_you", "Waiting for you", needsYou: true),
              loop(2, "example-docs", "supervising", "Supervising"),
              loop(3, "example-site", "paused", "Paused")], needsYou: 1)
    }
    func text(_ model: MenuModel) -> [String] {
        model.rows.map { row in
            switch row.kind {
            case .separator: return "---"
            case .loop: return "\(row.title) | \(row.detail)"
            default: return row.title
            }
        }
    }

    func testThreeLoopsGiveTheAgreedLayout() {
        let model = MenuBuilder.build(.read(three, at: now.addingTimeInterval(-130)), now: now)
        XCTAssertEqual(text(model), [
            "Open looptender Panel",
            "1 loop needs you",
            "---",
            "Needs you",
            "example-api | Needs you",
            "Working",
            "example-docs | Working",
            "Other loops (1)",
            "---",
            "Quit looptender Menu",
            "---",
            "Read 2 min ago",
        ])
        XCTAssertEqual(model.badge, "1")
        XCTAssertEqual(model.iconAccessibilityLabel, "looptender, 1 loop needs you")
        XCTAssertEqual(model.rows.map(\.command), [
            .openPanel, .openPanel, nil, nil,
            .openLoop(key: "l000000000001"), nil, .openLoop(key: "l000000000002"), nil,
            nil, .quit, nil, nil,
        ])
        // A row says what it is; what choosing it does is offered as help.
        let api = model.rows[4]
        XCTAssertEqual(api.accessibilityLabel, "example-api, Needs you")
        XCTAssertEqual(api.accessibilityHelp, "Opens this loop in the panel.")
        XCTAssertEqual(model.rows[0].accessibilityLabel, "Open looptender Panel")
        XCTAssertEqual(model.rows[0].accessibilityHelp, "Opens the looptender panel in your browser.")
        // The paused loop is in the collapsed group, with its own row inside it.
        let other = model.rows[7]
        XCTAssertEqual(other.submenu.map { "\($0.title) | \($0.detail)" }, ["example-site | Paused"])
    }
    // Nothing that looks like a menu item is unclickable, except the one
    // status line, which is last.
    func testOnlyTheLastRowCannotBeChosen() {
        let readings: [MenuReading] = [.notRead, .reading, .unsupported, .unavailable, .read(three, at: now), .read(list([]), at: now),
                                       .read(list([], registry: "unavailable"), at: now)]
        for reading in readings {
            for refreshing in [false, true] {
                let model = MenuBuilder.build(reading, now: now, refreshing: refreshing)
                let rows = model.rows.filter { $0.kind != .separator }
                XCTAssertEqual(rows.last?.kind, .status)
                // What cannot be chosen is text: the section headings, the build line and the status line.
                XCTAssertTrue(rows.filter { !$0.isEnabled }.allSatisfy { $0.kind == .status }, "\(reading)")
                XCTAssertEqual(rows.first?.command, .openPanel, "the first row always opens the panel")
                XCTAssertNotEqual(model.rows.first?.kind, .separator)
                // No two separators in a row, and every row is labelled.
                for (a, b) in zip(model.rows, model.rows.dropFirst()) { XCTAssertFalse(a.kind == .separator && b.kind == .separator) }
                XCTAssertTrue(rows.allSatisfy { !$0.accessibilityLabel.isEmpty && !$0.title.isEmpty })
                XCTAssertTrue(rows.filter { $0.isEnabled }.allSatisfy { !$0.accessibilityHelp.isEmpty && !$0.symbol.isEmpty })
            }
        }
    }
    func testStopRowIsThereOnlyWhileThePanelRuns() {
        func rows(_ panel: PanelStatus?) -> [String] { text(MenuBuilder.build(.read(three, at: now), now: now, panel: panel)) }
        let stopped = PanelStatus(schemaVersion: 1, running: false, idleRemainingSeconds: nil)
        XCTAssertEqual(rows(nil), rows(stopped))
        XCTAssertFalse(rows(nil).contains { $0.contains("Stop") })
        let model = MenuBuilder.build(.read(three, at: now), now: now, panel: PanelStatus(schemaVersion: 1, running: true, idleRemainingSeconds: 1380))
        XCTAssertEqual(Array(text(model).suffix(5)), ["---", "Stop the Panel Page", "Quit looptender Menu", "---", "Read just now"])
        let stop = model.rows.first { $0.command == .stopPanel }!
        XCTAssertEqual(stop.detail, "stops by itself in 23 min")
        XCTAssertEqual(stop.accessibilityLabel, "Stop the Panel Page, stops by itself in 23 min")
        XCTAssertEqual(stop.accessibilityHelp, "Stops the panel's background process. Your loops keep running.")
        // It does not borrow the symbol of a stopped loop.
        XCTAssertNotEqual(stop.symbol, MenuBuilder.symbols["stopped"])
        XCTAssertEqual(MenuBuilder.stopsIn(59), "stops by itself in under a minute")
        XCTAssertEqual(MenuBuilder.stopsIn(60), "stops by itself in 1 min")
        XCTAssertEqual(MenuBuilder.stopsIn(7200), "stops by itself in 2 h")
        XCTAssertEqual(MenuBuilder.stopsIn(nil), "running")
        // It is offered even when the list could not be read.
        XCTAssertTrue(MenuBuilder.build(.unavailable, now: now, panel: PanelStatus(schemaVersion: 1, running: true, idleRemainingSeconds: 5)).rows.contains { $0.command == .stopPanel })
        // The status line stays last, and what cannot be chosen is text.
        XCTAssertEqual(model.rows.last?.kind, .status)
        XCTAssertTrue(model.rows.filter { $0.kind != .separator && !$0.isEnabled }.allSatisfy { $0.kind == .status })
    }

    func testUnaskedReadsAreSlowAndBackOff() {
        XCTAssertEqual(ReadSchedule.interval(consecutiveFailures: 0, lowPower: false), 60)
        XCTAssertEqual(ReadSchedule.interval(consecutiveFailures: 2, lowPower: false), 60)
        XCTAssertEqual(ReadSchedule.interval(consecutiveFailures: 3, lowPower: false), 300)
        XCTAssertEqual(ReadSchedule.interval(consecutiveFailures: 40, lowPower: false), 300)
        XCTAssertNil(ReadSchedule.interval(consecutiveFailures: 0, lowPower: true))
        XCTAssertNil(ReadSchedule.interval(consecutiveFailures: 5, lowPower: true))
    }

    func testPanelStatusContract() throws {
        XCTAssertEqual(try Contract.panelStatus(Data("{\"schemaVersion\":1,\"running\":false}".utf8), exit: 0), PanelStatus(schemaVersion: 1, running: false, idleRemainingSeconds: nil))
        let running = try Contract.panelStatus(Data("{\"schemaVersion\":1,\"running\":true,\"startedAt\":\"2030-01-01T12:00:00Z\",\"port\":49152,\"idleSeconds\":1800,\"idleRemainingSeconds\":1380}".utf8), exit: 0)
        XCTAssertEqual(running, PanelStatus(schemaVersion: 1, running: true, idleRemainingSeconds: 1380))
        XCTAssertThrowsError(try Contract.panelStatus(Data("{\"schemaVersion\":2,\"running\":true}".utf8), exit: 0)) { XCTAssertEqual(String(describing: $0), "unsupported") }
        for (body, exit) in [("{\"schemaVersion\":1,\"running\":true}", Int32(1)), ("fictional-private-bad-json", 0), ("{\"schemaVersion\":1,\"running\":true,\"idleRemainingSeconds\":-4}", 0), ("{\"schemaVersion\":1}", 0)] {
            XCTAssertThrowsError(try Contract.panelStatus(Data(body.utf8), exit: exit)) { error in
                XCTAssertFalse(String(describing: error).contains("fictional-private"))
            }
        }
    }

    // The whole allow-list, one assertion per command the menu can run.
    func testCommandsRunOnlyTheAllowList() {
        let key = "l0123456789ab", expect = "r1.1893499320." + String(repeating: "ab", count: 32) + "." + String(repeating: "cd", count: 16)
        XCTAssertEqual(MenuBuilder.arguments(.readList), ["panel", "list", "--json"])
        XCTAssertEqual(MenuBuilder.arguments(.readStatus), ["panel", "status", "--json"])
        XCTAssertEqual(MenuBuilder.arguments(.openPanel), ["panel"])
        XCTAssertEqual(MenuBuilder.arguments(.openLoop(key: key)), ["panel", "--loop", key])
        XCTAssertEqual(MenuBuilder.arguments(.openPanelWithChanges), ["panel", "--allow-changes"])
        XCTAssertEqual(MenuBuilder.arguments(.stopPanel), ["panel", "stop"])
        for action in LoopAction.allCases {
            XCTAssertEqual(MenuBuilder.arguments(.review(action, key: key)), ["client", "review", action.rawValue, "--loop", key, "--json"])
            XCTAssertEqual(MenuBuilder.arguments(.apply(action, key: key, expect: expect, lanes: false, immediately: false)),
                           ["client", "apply", action.rawValue, "--loop", key, "--expect", expect, "--json"])
        }
        XCTAssertEqual(MenuBuilder.arguments(.apply(.stop, key: key, expect: expect, lanes: true, immediately: true)),
                       ["client", "apply", "stop", "--loop", key, "--expect", expect, "--lanes", "--immediately", "--json"])
        XCTAssertEqual(MenuBuilder.arguments(.apply(.stop, key: key, expect: expect, lanes: false, immediately: true)),
                       ["client", "apply", "stop", "--loop", key, "--expect", expect, "--immediately", "--json"])
        XCTAssertNil(MenuBuilder.arguments(.quit))
        // Lanes and now belong to a stop only.
        XCTAssertNil(MenuBuilder.arguments(.apply(.pause, key: key, expect: expect, lanes: true, immediately: false)))
        XCTAssertNil(MenuBuilder.arguments(.apply(.resume, key: key, expect: expect, lanes: false, immediately: true)))
        // A key that is not a page key, or an expect value that is not a
        // digest, never reaches the command line.
        for bad in ["", "--idle", "l0123", "example-api", "l0123456789ab --no-open", "L0123456789AB"] {
            XCTAssertNil(MenuBuilder.arguments(.openLoop(key: bad)), bad)
            XCTAssertNil(MenuBuilder.arguments(.review(.stop, key: bad)), bad)
            XCTAssertNil(MenuBuilder.arguments(.apply(.stop, key: bad, expect: expect, lanes: false, immediately: false)), bad)
        }
        for bad in ["", "--immediately", String(repeating: "ab", count: 32), String(repeating: "AB", count: 32), "r1.1." + String(repeating: "a", count: 63) + "." + String(repeating: "c", count: 32), expect + " --lanes"] {
            XCTAssertNil(MenuBuilder.arguments(.apply(.stop, key: key, expect: bad, lanes: false, immediately: false)), bad)
        }
    }

    // A loop's row opens its choices: open it, and each change its flags
    // allow, as the page offers them, Stop last. A CLI that does not say
    // which changes fit gives the row of before, which opens the loop.
    func testLoopRowsOfferWhatTheirFlagsAllow() {
        func loop(_ can: [String]?) -> PanelLoop {
            var l = PanelLoop(key: "l000000000001", name: "example-api", state: "supervising", phrase: "Supervising", needsYou: false)
            l.can = can
            return l
        }
        func choices(_ can: [String]?) -> [String] {
            let row = MenuBuilder.build(.read(list([loop(can)]), at: now), now: now).rows.first { $0.kind == .loop }!
            return row.submenu.map { $0.kind == .separator ? "---" : $0.title }
        }
        XCTAssertEqual(choices(["pause", "stop"]), ["Open in Panel", "---", "Pause…", "Stop…"])
        XCTAssertEqual(choices(["stop", "resume"]), ["Open in Panel", "---", "Resume…", "Stop…"])
        XCTAssertEqual(choices(["pause", "resume"]), ["Open in Panel", "---", "Pause…", "Resume…"])
        XCTAssertEqual(choices(["resume", "enable", "take-over"]), ["Open in Panel", "---", "Resume…"], "a change this menu does not know is not offered")
        for can in [nil, [], ["enable"]] as [[String]?] {
            XCTAssertEqual(choices(can), [], "\(String(describing: can))")
            let row = MenuBuilder.build(.read(list([loop(can)]), at: now), now: now).rows.first { $0.kind == .loop }!
            XCTAssertEqual(row.command, .openLoop(key: "l000000000001"))
        }
        let row = MenuBuilder.build(.read(list([loop(["pause", "stop"])]), at: now), now: now).rows.first { $0.kind == .loop }!
        XCTAssertNil(row.command)
        XCTAssertTrue(row.isEnabled)
        XCTAssertEqual(row.accessibilityHelp, "Shows this loop's details and what you can do with it.")
        XCTAssertEqual(row.submenu.compactMap(\.command), [.openLoop(key: "l000000000001"), .review(.pause, key: "l000000000001"), .review(.stop, key: "l000000000001")])
        XCTAssertTrue(row.submenu.filter { $0.kind != .separator }.allSatisfy { !$0.accessibilityHelp.isEmpty && !$0.symbol.isEmpty })
        // Every change asks first; none runs from the menu directly.
        XCTAssertFalse(row.submenu.contains { if case .apply = $0.command { return true }; return false })
        // A change in what fits is a change of shape: the open menu is not
        // updated in place under the pointer.
        let other = MenuBuilder.build(.read(list([loop(["stop", "resume"])]), at: now), now: now).rows
        XCTAssertFalse(MenuBuilder.sameShape(MenuBuilder.build(.read(list([loop(["pause", "stop"])]), at: now), now: now).rows, other))
    }

    // The controls row is offered only by a CLI that gives the footer's
    // exposure statement, which its confirmation shows.
    func testOpenPanelWithControlsNeedsTheExposureStatement() {
        func rows(_ status: PanelStatus?) -> [MenuCommand?] { MenuBuilder.build(.read(three, at: now), now: now, panel: status).rows.map(\.command) }
        XCTAssertFalse(rows(nil).contains(.openPanelWithChanges))
        XCTAssertFalse(rows(PanelStatus(schemaVersion: 1, running: false, idleRemainingSeconds: nil)).contains(.openPanelWithChanges))
        var status = PanelStatus(schemaVersion: 1, running: false, idleRemainingSeconds: nil)
        status.exposure = "While changes are allowed, anyone who can use this browser window can pause, stop and resume loops."
        let model = MenuBuilder.build(.read(three, at: now), now: now, panel: status)
        XCTAssertEqual(Array(text(model).suffix(5)), ["---", "Open Panel with Controls…", "Quit looptender Menu", "---", "Read just now"])
        XCTAssertEqual(model.rows.first { $0.command == .openPanelWithChanges }?.accessibilityHelp, "Opens the panel so its page can pause, stop and resume loops and offer a separate review to replace a loop’s own build. Asks first.")
    }

    func testBadgeAndNeedsLine() {
        func model(_ needs: Int, _ unread: Int) -> MenuModel { MenuBuilder.build(.read(list([], needsYou: needs, unread: unread), at: now), now: now) }
        XCTAssertEqual(model(0, 0).badge, "")
        XCTAssertEqual(model(0, 0).iconAccessibilityLabel, "looptender")
        XCTAssertFalse(text(model(0, 0)).contains { $0.contains("need") })
        XCTAssertEqual(text(model(3, 0))[1], "3 loops need you")
        XCTAssertEqual(text(model(1, 1))[1], "1 loop can't be read")
        XCTAssertEqual(text(model(3, 2))[1], "1 loop needs you, 2 can't be read")
        // The symbol says whether a person is needed or only that a read failed.
        XCTAssertEqual(model(1, 1).rows[1].symbol, "questionmark.circle")
        XCTAssertEqual(model(3, 2).rows[1].symbol, "exclamationmark.circle.fill")
        XCTAssertEqual(model(3, 2).badge, "3")
        XCTAssertEqual(model(250, 0).badge, "99+")
        // The icon is plain only when a complete read found nothing: the CLI's
        // allClear, never a count of zero alone.
        func badge(_ l: PanelList) -> String { MenuBuilder.build(.read(l, at: now), now: now).badge }
        XCTAssertEqual(badge(list(three.loops)), "")
        XCTAssertEqual(badge(list([])), "", "no loops registered, read completely, is all clear")
        XCTAssertEqual(badge(list([], registry: "unavailable")), "?")
        XCTAssertEqual(MenuBuilder.build(.read(list([], registry: "unavailable"), at: now), now: now).iconAccessibilityLabel, "looptender, the list of loops can't be read")
        XCTAssertEqual(badge(list(three.loops, complete: false)), "?")
        XCTAssertEqual(MenuBuilder.build(.read(list(three.loops, complete: false), at: now), now: now).iconAccessibilityLabel, "looptender, not every loop was read")
        XCTAssertEqual(badge(PanelList(schemaVersion: 1, registry: "available", complete: true, allClear: false, needsYou: 0, unread: 0, omitted: 3, loops: three.loops)), "?",
                       "a list that leaves loops out cannot say all clear unless the CLI does")
        XCTAssertEqual(badge(list(three.loops, needsYou: 2, complete: false)), "2", "a count is shown whenever there is one")
        // Nothing is claimed before a read. A failed read shows on the icon,
        // so it cannot be taken for "nothing needs you".
        for reading in [MenuReading.notRead, .reading] {
            XCTAssertEqual(MenuBuilder.build(reading, now: now).badge, "")
        }
        for reading in [MenuReading.unsupported, .unavailable] {
            XCTAssertEqual(MenuBuilder.build(reading, now: now).badge, "?")
            XCTAssertEqual(MenuBuilder.build(reading, now: now).iconAccessibilityLabel, "looptender, can't be read")
        }
    }

    func testStatusLineSaysWhatWasRead() {
        func last(_ reading: MenuReading, notice: String? = nil, refreshing: Bool = false) -> String {
            MenuBuilder.build(reading, now: now, notice: notice, refreshing: refreshing).rows.last!.title
        }
        XCTAssertEqual(last(.read(three, at: now)), "Read just now")
        XCTAssertEqual(last(.read(three, at: now.addingTimeInterval(-59))), "Read just now")
        XCTAssertEqual(last(.read(three, at: now.addingTimeInterval(-3599))), "Read 59 min ago")
        XCTAssertEqual(last(.read(three, at: now.addingTimeInterval(-7300))), "Read 2 h ago")
        XCTAssertEqual(last(.read(three, at: now.addingTimeInterval(-200_000))), "Read more than a day ago")
        XCTAssertEqual(last(.read(three, at: now.addingTimeInterval(30))), "Read just now", "a clock that moved back is not a negative age")
        XCTAssertEqual(last(.read(list([]), at: now)), "No loops are registered. Read just now.")
        XCTAssertEqual(last(.read(list([], registry: "unavailable"), at: now)), "The list of loops could not be read. Read just now.")
        XCTAssertEqual(last(.read(list(three.loops, needsYou: 1, complete: false), at: now)), "Not every loop was read. Read just now.")
        // Loops the CLI left out are reachable through one row, not a sentence.
        XCTAssertEqual(last(.read(list(three.loops, omitted: 4), at: now)), "Read just now")
        XCTAssertTrue(MenuBuilder.build(.read(list(three.loops, omitted: 4), at: now), now: now).rows.contains { $0.title == "All 7 Loops in the Panel" && $0.command == .openPanel })
        XCTAssertEqual(last(.unsupported), "The menu and looptender are different versions. Use the panel.")
        XCTAssertEqual(last(.unavailable), "Can't reach looptender. Run keepgoing doctor.")
        XCTAssertEqual(last(.read(three, at: now), refreshing: true), "Reading…")
        XCTAssertEqual(last(.read(three, at: now), notice: "The panel could not be opened. Run keepgoing panel in a terminal."), "The panel could not be opened. Run keepgoing panel in a terminal.")
        // While a read is under way the last read's rows stay in place.
        XCTAssertEqual(MenuBuilder.build(.read(three, at: now), now: now, refreshing: true).rows.filter { $0.kind == .loop }.count, 2)
    }

    func testLongNamesHundredLoopsAndUnknownStates() {
        let long = "example-monorepo-migration-of-the-billing-service-phase-one"
        let sibling = "example-monorepo-migration-of-the-billing-service-phase-two"
        let hostile = "example\u{0007}-loop\nsecond\u{2028}line"
        var loops = [loop(1, long, "supervising", "Supervising"), loop(2, sibling, "supervising", "Supervising"),
                     loop(3, hostile, "a_state_from_a_newer_build", String(repeating: "A very long phrase from a newer build ", count: 4))]
        for i in 4...100 { loops.append(loop(i, "example-loop-\(i)", "off", "Off")) }
        let model = MenuBuilder.build(.read(list(loops), at: now), now: now)
        let rows = model.rows.filter { $0.kind == .loop }
        // The three that are working are listed; the 97 that are off are in one collapsed group.
        XCTAssertEqual(rows.count, 3)
        let other = model.rows.first { $0.title.hasPrefix("Other loops") }
        XCTAssertEqual(other?.title, "Other loops (97)")
        XCTAssertEqual(other?.submenu.count, MenuBuilder.maxLoops * 2)
        // One more row leads to the rest.
        XCTAssertEqual(model.rows.first { $0.title == "All 100 Loops in the Panel" }?.command, .openPanel)
        XCTAssertNil(MenuBuilder.build(.read(list(Array(loops.prefix(25))), at: now), now: now).rows.first { $0.title.hasPrefix("All ") })
        XCTAssertEqual(MenuBuilder.build(.read(list(Array(loops.prefix(10)), omitted: 30), at: now), now: now).rows.first { $0.title.hasPrefix("All ") }?.title, "All 40 Loops in the Panel")
        // Non-colliding long names are cut at the end; colliding ones keep a distinguishing tail.
        let singleLong = MenuBuilder.build(.read(list([loop(1, long, "supervising", "Supervising")]), at: now), now: now).rows.first { $0.kind == .loop }!
        XCTAssertEqual(singleLong.title.count, MenuBuilder.maxName)
        XCTAssertTrue(singleLong.title.hasSuffix("…"))
        XCTAssertTrue(singleLong.title.hasPrefix("example-monorepo-migration-"))
        XCTAssertFalse(singleLong.title.contains("phase"), "the end is what is cut")
        XCTAssertEqual(rows[0].title.count, MenuBuilder.maxName)
        XCTAssertEqual(rows[1].title.count, MenuBuilder.maxName)
        XCTAssertNotEqual(rows[0].title, rows[1].title)
        XCTAssertTrue(rows[0].title.hasPrefix("example-monorepo-migrati"))
        XCTAssertTrue(rows[0].title.hasSuffix("one"))
        XCTAssertTrue(rows[1].title.hasSuffix("two"))
        XCTAssertEqual(rows[0].toolTip, long, "the full name is still reachable")
        XCTAssertEqual(rows[1].toolTip, sibling)
        XCTAssertEqual(rows[0].accessibilityLabel, long + ", Working")
        XCTAssertEqual(rows[2].title, "example-loopsecondline", "control characters and line separators never reach the menu")
        XCTAssertEqual(rows[2].symbol, "questionmark.circle", "a state this build does not know gets the unknown symbol")
        XCTAssertEqual(rows[2].detail.count, 24, "a phrase cannot set the menu's width")
        XCTAssertTrue(rows.allSatisfy { $0.isEnabled && !$0.symbol.isEmpty })
        // Joiners that scripts and emoji sequences need are kept.
        XCTAssertEqual(Contract.text("a\u{200D}b\u{200C}c\u{200B}\u{0000}"), "a\u{200D}b\u{200C}c")
        // Every state the panel has gets a symbol, and only needs-you is filled.
        for state in ["waiting_for_you", "unknown", "checks_failing", "not_checked_recently", "waiting_on_limit", "supervising", "outside_hours", "paused", "off", "stopped", "finished"] {
            XCTAssertNotNil(MenuBuilder.symbols[state], state)
        }
        XCTAssertEqual(MenuBuilder.symbols.values.filter { $0.hasSuffix(".fill") }, ["exclamationmark.circle.fill"])
    }
    func testAnOpenMenuCanBeUpdatedInPlaceOnlyWhenItsRowsAreTheSame() {
        let before = MenuBuilder.build(.read(three, at: now.addingTimeInterval(-600)), now: now)
        let reading = MenuBuilder.build(.read(three, at: now.addingTimeInterval(-600)), now: now, refreshing: true)
        let after = MenuBuilder.build(.read(three, at: now), now: now)
        XCTAssertTrue(MenuBuilder.sameShape(before.rows, reading.rows))
        XCTAssertTrue(MenuBuilder.sameShape(before.rows, after.rows))
        // A loop that starts needing a person, a different set of loops, and a
        // panel that started all change the rows.
        var changed = three.loops
        changed.removeLast()
        XCTAssertFalse(MenuBuilder.sameShape(before.rows, MenuBuilder.build(.read(list(changed, needsYou: 1), at: now), now: now).rows))
        XCTAssertFalse(MenuBuilder.sameShape(before.rows, MenuBuilder.build(.read(list(three.loops), at: now), now: now).rows))
        XCTAssertFalse(MenuBuilder.sameShape(before.rows, MenuBuilder.build(.read(three, at: now), now: now, panel: PanelStatus(schemaVersion: 1, running: true, idleRemainingSeconds: 60)).rows))
    }

    func testPanelListContract() throws {
        func reply(_ loops: String, version: Int = 1, registry: String = "available", needs: Int = 1, unread: Int = 0, extra: String = "") -> Data {
            Data("{\"schemaVersion\":\(version),\"readAt\":\"2030-01-01T12:00:00Z\",\"registry\":\"\(registry)\",\"complete\":true,\"allClear\":false,\"needsYou\":\(needs),\"unread\":\(unread),\"omitted\":0,\"loops\":[\(loops)]\(extra)}".utf8)
        }
        let one = "{\"key\":\"l0123456789ab\",\"name\":\"example-api\",\"state\":\"waiting_for_you\",\"phrase\":\"Waiting for you\",\"needsYou\":true,\"addedLater\":1}"
        let parsed = try Contract.panelList(reply(one, extra: ",\"addedLater\":true"), exit: 0)
        XCTAssertEqual(parsed.loops, [PanelLoop(key: "l0123456789ab", name: "example-api", state: "waiting_for_you", phrase: "Waiting for you", needsYou: true)])
        XCTAssertNil(parsed.loops[0].can, "a CLI from before can is still read")
        let withCan = try Contract.panelList(reply(one.replacingOccurrences(of: "\"addedLater\":1", with: "\"can\":[\"pause\",\"stop\"]")), exit: 0)
        XCTAssertEqual(withCan.loops[0].can, ["pause", "stop"])
        XCTAssertEqual(try Contract.panelList(reply("", registry: "unavailable", needs: 0), exit: 1).loops, [])
        XCTAssertThrowsError(try Contract.panelList(reply(one, version: 2), exit: 0)) { XCTAssertEqual(String(describing: $0), "unsupported") }
        let invalid: [(Data, Int32)] = [
            (reply(one), 1), (reply("", registry: "unavailable", needs: 0), 0), (reply(one + "," + one), 0),
            (reply(one.replacingOccurrences(of: "l0123456789ab", with: "loop_0123456789abcdef0123456789abcdef")), 0),
            (reply(one.replacingOccurrences(of: "example-api", with: "")), 0), (reply(one.replacingOccurrences(of: "example-api", with: "\\u0007\\u2028")), 0),
            (reply(one, needs: 0, unread: 1), 0),
            (Data("fictional-private-bad-json".utf8), 0), (Data(), 0),
        ]
        for (data, exit) in invalid {
            XCTAssertThrowsError(try Contract.panelList(data, exit: exit)) { error in
                XCTAssertEqual(String(describing: error), "invalidResponse")
            }
        }
    }

    func testNoProgressIsShownOnTheRowAndIsNeverANeed() throws {
        let json = #"{"schemaVersion":1,"registry":"available","complete":true,"allClear":true,"needsYou":0,"unread":0,"omitted":0,"loops":[{"key":"l0123456789ab","name":"example-api","state":"supervising","phrase":"Supervising","needsYou":false,"noProgress":"No progress: 8 messages with no recorded change to its repositories in 3 h."},{"key":"l0123456789ac","name":"example-web","state":"supervising","phrase":"Supervising","needsYou":false}]}"#
        let list = try JSONDecoder().decode(PanelList.self, from: Data(json.utf8))
        XCTAssertEqual(list.loops[0].noProgress, "No progress: 8 messages with no recorded change to its repositories in 3 h.")
        XCTAssertNil(list.loops[1].noProgress)
        let model = MenuBuilder.build(.read(list, at: now), now: now)
        let rows = model.rows.filter { $0.kind == .loop }
        XCTAssertEqual(rows.map { $0.detail }, ["Working", "Working"])
        XCTAssertEqual(rows[0].accessibilityLabel, "example-api, Working")
        // The flag is in the loop's submenu, in the page's words, and is never a need.
        XCTAssertTrue(rows[0].submenu.contains { $0.title == "No progress: 8 messages with no recorded change to its repositories in 3 h." })
        XCTAssertTrue(rows[1].submenu.isEmpty || !rows[1].submenu.contains { $0.title.hasPrefix("No progress") })
        // It is never a need: no badge, and the icon says nothing needs you.
        XCTAssertEqual(model.badge, "")
    }

    func testFreshnessIsShownInTheCLIsWordsAndOffersNothingToRun() throws {
        let json = """
        {"schemaVersion":1,"readAt":"2030-01-01T12:00:00Z","registry":"available","complete":true,"allClear":true,"needsYou":0,"unread":0,"omitted":0,
         "installedBuild":"v0.2.0","buildFreshness":{"text":"Build v0.2.0 installed 3 h ago · 1 supervisor on it · 2 not on it","onIt":1,"notOnIt":2},
         "loops":[{"key":"l000000000001","name":"example-api","state":"supervising","phrase":"Supervising","needsYou":false,"buildFreshness":{"state":"current","text":"v0.2.0"}},
                  {"key":"l000000000002","name":"example-docs","state":"supervising","phrase":"Supervising","needsYou":false,"buildFreshness":{"state":"pending","text":"Update pending · switches at next check"}},
                  {"key":"l000000000003","name":"example-site","state":"paused","phrase":"Paused","needsYou":false,"can":["resume"],"buildFreshness":{"state":"older","text":"Older build · restart the supervisor to update","detail":"Runs v0.1.0.","action":"This ends the supervisor and starts nothing, so until you start it again the loop has no supervisor. The loop stays held; this does not resume it.","command":"looptender daemon-shutdown -c CONFIG"}}]}
        """
        let decoded = try JSONDecoder().decode(PanelList.self, from: Data(json.utf8))
        let model = MenuBuilder.build(.read(decoded, at: now), now: now)
        let lines = text(model)
        // One short line, only because something is not on the installed build; the long summary is gone.
        XCTAssertTrue(lines.contains("2 supervisors not on the installed build"))
        XCTAssertFalse(lines.contains { $0.hasPrefix("Build v0.2.0 installed") })
        XCTAssertTrue(lines.contains("example-api | Working"))
        XCTAssertTrue(lines.contains("example-docs | Working"), "the build is in the submenu, not on the row")
        let docs = try XCTUnwrap(model.rows.first { $0.title == "example-docs" })
        XCTAssertTrue(docs.submenu.contains { $0.title == "Update pending · switches at next check" })
        let other = try XCTUnwrap(model.rows.first { $0.title.hasPrefix("Other loops") })
        let older = try XCTUnwrap(other.submenu.first { $0.title == "example-site" })
        XCTAssertEqual(older.detail, "Paused")
        XCTAssertEqual(older.submenu.filter { $0.kind == .status }.map(\.title), ["Older build · restart the supervisor to update", "Runs v0.1.0.", "This ends the supervisor and starts nothing, so until you start it again the loop has no supervisor. The loop stays held; this does not resume it.", "Run: looptender daemon-shutdown -c CONFIG"])
        // The command is only shown: every action in the submenu is one the menu already had.
        XCTAssertEqual(older.submenu.compactMap(\.command), [.openLoop(key: "l000000000003"), .review(.resume, key: "l000000000003")])
        // A CLI that sends none of it keeps the plain menu.
        let plain = MenuBuilder.build(.read(three, at: now.addingTimeInterval(-130)), now: now)
        XCTAssertFalse(text(plain).contains { $0.contains("installed") || $0.contains("Older build") })
    }
    func testBuildsAreShownPerLoopAndOnceAtTheFoot() throws {
        let json = """
        {"schemaVersion":1,"readAt":"2030-01-01T12:00:00Z","registry":"available","complete":true,"allClear":true,"needsYou":0,"unread":0,"omitted":0,
         "installedBuild":"v0.2.0","futureField":1,
         "loops":[{"key":"l000000000001","name":"example-api","state":"supervising","phrase":"Supervising","needsYou":false,"recordedBuild":"v0.1.0","buildDiffers":true},
                  {"key":"l000000000002","name":"example-docs","state":"supervising","phrase":"Supervising","needsYou":false,"recordedBuild":"v0.2.0","buildDiffers":false},
                  {"key":"l000000000003","name":"example-site","state":"supervising","phrase":"Supervising","needsYou":false}]}
        """
        let decoded = try JSONDecoder().decode(PanelList.self, from: Data(json.utf8))
        let model = MenuBuilder.build(.read(decoded, at: now), now: now)
        XCTAssertEqual(text(model), [
            "Open looptender Panel", "---", "Working",
            "example-api | Working", "example-docs | Working", "example-site | Working",
            "---", "Quit looptender Menu", "---", "Read just now · looptender v0.2.0"])
        // The build is in each loop's submenu, with the installed one named when they differ.
        XCTAssertEqual(model.rows.first { $0.title == "example-api" }?.submenu.first?.title, "last check ran v0.1.0 · installed v0.2.0")
        XCTAssertEqual(model.rows.first { $0.title == "example-docs" }?.submenu.first?.title, "v0.2.0")
        XCTAssertTrue(model.rows.first { $0.title == "example-site" }!.submenu.isEmpty)
        // A CLI that sends none of it keeps the plain menu.
        let plain = MenuBuilder.build(.read(three, at: now.addingTimeInterval(-130)), now: now)
        XCTAssertFalse(text(plain).contains { $0.contains("looptender v") || $0.contains("last check ran") })
    }
    func testProviderModelAndAMovedHoldAreInTheSubmenu() throws {
        let json = """
        {"schemaVersion":1,"readAt":"2030-01-01T12:00:00Z","registry":"available","complete":true,"allClear":false,"needsYou":2,"unread":0,"omitted":0,
         "loops":[{"key":"l000000000001","name":"example-codex-loop","state":"waiting_for_you","phrase":"Moved: resume to start","needsYou":true,"provider":"claude-code","model":"example-model","reason":"It was moved to a new thread and waits for you to resume it.",
                   "moved":{"held":true,"text":"Moved to its new thread. Resuming it is the one action needed."}},
                  {"key":"l000000000002","name":"example-docs","state":"waiting_for_you","phrase":"Waiting for you","needsYou":true,"provider":"codex"},
                  {"key":"l000000000003","name":"example-site","state":"supervising","phrase":"Supervising","needsYou":false}]}
        """
        let decoded = try JSONDecoder().decode(PanelList.self, from: Data(json.utf8))
        let model = MenuBuilder.build(.read(decoded, at: now), now: now)
        let rows = text(model)
        // The row is the name and one word; where it runs and what it waits for are in its submenu.
        XCTAssertTrue(rows.contains("example-codex-loop | Needs you"))
        XCTAssertTrue(rows.contains("example-docs | Needs you"))
        let moved = try XCTUnwrap(model.rows.first { $0.title == "example-codex-loop" })
        XCTAssertEqual(moved.submenu.prefix(2).map(\.title), ["It was moved to a new thread and waits for you to resume it.", "claude-code example-model"])
        XCTAssertTrue(try XCTUnwrap(model.rows.first { $0.title == "example-docs" }).submenu.contains { $0.title == "codex" })
        // VoiceOver says the state; the target is configuration, said in the submenu.
        XCTAssertEqual(moved.accessibilityLabel, "example-codex-loop, Needs you")
        XCTAssertTrue(rows.contains("example-site | Working"))
    }
    func testAPausedLoopThatStillWaitsForYouHasTheNeedsYouSymbol() throws {
        let json = """
        {"schemaVersion":1,"readAt":"2030-01-01T12:00:00Z","registry":"available","complete":true,"allClear":false,"needsYou":1,"unread":0,"omitted":0,
         "loops":[{"key":"l000000000001","name":"example-paused-waiting","state":"paused","phrase":"Paused \u{00B7} also waiting for you","needsYou":true},
                  {"key":"l000000000002","name":"example-paused","state":"paused","phrase":"Paused","needsYou":false}]}
        """
        let decoded = try JSONDecoder().decode(PanelList.self, from: Data(json.utf8))
        let model = MenuBuilder.build(.read(decoded, at: now), now: now)
        let rows = model.rows.filter { $0.kind == .loop }
        // A paused loop that still waits for a person is in Needs you, with the amber symbol.
        XCTAssertEqual(rows.count, 1)
        XCTAssertEqual(rows[0].symbol, "exclamationmark.circle.fill")
        XCTAssertEqual(rows[0].tone, .needsYou)
        XCTAssertEqual(rows[0].detail, "Needs you")
        // The plain paused loop is in the collapsed group, quiet.
        let inside = try XCTUnwrap(model.rows.first { $0.title.hasPrefix("Other loops") }).submenu
        XCTAssertEqual(inside.map(\.symbol), ["pause.circle"])
        XCTAssertEqual(inside.map(\.tone), [.quiet])
    }
    func testEveryStateHasAToneAndOnlyWaitingForYouIsNeedsYou() {
        XCTAssertEqual(Set(MenuBuilder.tones.keys), Set(MenuBuilder.symbols.keys))
        for state in MenuBuilder.symbols.keys {
            XCTAssertNotNil(MenuBuilder.tones[state], state)
        }
        XCTAssertEqual(MenuBuilder.tones.filter { $0.value == .needsYou }.map(\.key), ["waiting_for_you"])
        XCTAssertEqual(MenuBuilder.tones["supervising"], .supervising)
        XCTAssertEqual(MenuBuilder.tones["waiting_on_limit"], .limit)
        XCTAssertEqual(MenuBuilder.tones["checks_failing"], .failing)
        for quiet in ["unknown", "not_checked_recently", "outside_hours", "paused", "off", "stopped", "finished"] {
            XCTAssertEqual(MenuBuilder.tones[quiet], .quiet, quiet)
        }
        let model = MenuBuilder.build(.read(three, at: now), now: now)
        XCTAssertEqual(model.rows[0].tone, .quiet)
        XCTAssertEqual(model.rows[1].tone, .needsYou)
        let loops = model.rows.filter { $0.kind == .loop }
        XCTAssertEqual(loops.map(\.tone), [.needsYou, .supervising])
        XCTAssertEqual(model.rows.first { $0.title.hasPrefix("Other loops") }?.submenu.map(\.tone), [.quiet])
        // A needs line for unread loops only is not amber.
        let unreadOnly = MenuBuilder.build(.read(list([], needsYou: 1, unread: 1), at: now), now: now)
        XCTAssertEqual(unreadOnly.rows[1].tone, .quiet)
    }

    func testBrandMarkIsATemplateImageAtTheRequestedSize() {
        let standard = BrandMark.image(size: 18)
        XCTAssertTrue(standard.isTemplate)
        XCTAssertEqual(standard.size, NSSize(width: 18, height: 18))
        let larger = BrandMark.image(size: 24)
        XCTAssertTrue(larger.isTemplate)
        XCTAssertEqual(larger.size, NSSize(width: 24, height: 24))
    }
}
