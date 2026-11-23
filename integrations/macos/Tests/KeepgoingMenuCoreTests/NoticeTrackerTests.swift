import XCTest
@testable import KeepgoingMenuCore

final class NoticeTrackerTests: XCTestCase {
    let t0 = Date(timeIntervalSince1970: 1_900_000_000)

    func notice(_ id: String, kind: String = "needs_you", loop: String = "l000000000001", name: String = "example-api", text: String? = nil,
                group: String? = nil, groupText: String = "{n} loops are held for you.", endText: String? = nil) -> PanelNotice {
        PanelNotice(id: id, kind: kind, loop: loop, name: name, text: text ?? "\(name) is held for you.", group: group ?? kind, groupText: groupText, endText: endText)
    }
    func read(_ notices: [PanelNotice]?, complete: Bool = true) -> MenuReading {
        .read(PanelList(schemaVersion: 1, registry: "available", complete: complete, allClear: false, needsYou: 0, unread: 0, omitted: 0, loops: [], notices: notices), at: t0)
    }
    func on(_ kind: String) -> Bool { NoticeKinds.defaultOn.contains(kind) }

    func testTheFirstReadIsABaselineAndSaysNothing() {
        var t = NoticeTracker()
        XCTAssertEqual(t.next(read([notice("a"), notice("b", loop: "l000000000002", name: "example-docs")]), now: t0, isOn: on), [])
        XCTAssertEqual(t.recent, [], "what was already open is not news")
    }

    func testAnEventIsPostedOnceNotOnEveryRead() {
        var t = NoticeTracker()
        _ = t.next(read([]), now: t0, isOn: on)
        let n = notice("a")
        let first = t.next(read([n]), now: t0, isOn: on)
        XCTAssertEqual(first, [PostedNotice(identifier: "keepgoing.notice.a", body: "example-api is held for you.", loop: "l000000000001")])
        for i in 1...5 { XCTAssertEqual(t.next(read([n]), now: t0.addingTimeInterval(Double(i) * 60), isOn: on), []) }
        // A failed or older-CLI read changes nothing, and does not re-baseline.
        XCTAssertEqual(t.next(.unavailable, now: t0, isOn: on), [])
        XCTAssertEqual(t.next(read(nil), now: t0, isOn: on), [])
        XCTAssertEqual(t.next(read([n]), now: t0, isOn: on), [])
    }

    func testAConditionThatEndsAndComesBackIsAnotherEvent() {
        var t = NoticeTracker()
        _ = t.next(read([notice("a")]), now: t0, isOn: on)
        XCTAssertEqual(t.next(read([]), now: t0, isOn: on), [])
        XCTAssertEqual(t.next(read([notice("a")]), now: t0, isOn: on).count, 1)
    }

    func testManyThatBeginTogetherAreOneNotification() {
        var t = NoticeTracker()
        _ = t.next(read([]), now: t0, isOn: on)
        let limit = (1...5).map { notice("l\($0)", kind: "limit", loop: "l00000000000\($0)", name: "example-\($0)", text: "example-\($0) stopped: usage limit until Fri 04:00.",
                                         group: "limit.until Fri 04:00", groupText: "{n} loops stopped: usage limit until Fri 04:00.") }
        let posted = t.next(read(limit + [notice("q")]), now: t0, isOn: on)
        XCTAssertEqual(posted.count, 2)
        XCTAssertEqual(posted.first?.body, "5 loops stopped: usage limit until Fri 04:00.")
        XCTAssertNil(posted.first?.loop, "several loops open the panel, not one of them")
        XCTAssertEqual(posted.last?.loop, "l000000000001")
        XCTAssertEqual(t.recent.count, 6)
    }

    func testKindsThatAreOffAreListedButNotPushed() {
        var t = NoticeTracker()
        _ = t.next(read([]), now: t0, isOn: on)
        let lane = notice("x", kind: "lane_failed", text: "example-api has a failed lane.")
        XCTAssertEqual(t.next(read([lane]), now: t0, isOn: on), [])
        XCTAssertEqual(t.recent.map(\.text), ["example-api has a failed lane."])
        var u = NoticeTracker()
        _ = u.next(read([]), now: t0, isOn: on)
        XCTAssertEqual(u.next(read([lane]), now: t0, isOn: { _ in true }).count, 1)
    }

    func testAHoldLiftedIsListedWhenTheCleanReadSeesItEnd() {
        var t = NoticeTracker()
        let hold = notice("h", kind: "hold", text: "example-api was held.", endText: "example-api is no longer held.")
        _ = t.next(read([hold]), now: t0, isOn: on)
        XCTAssertEqual(t.next(read([], complete: false), now: t0, isOn: on), [])
        XCTAssertEqual(t.recent, [], "a cut-off read cannot say what ended")
        XCTAssertEqual(t.next(read([]), now: t0.addingTimeInterval(60), isOn: on), [])
        XCTAssertEqual(t.recent.map(\.text), ["example-api is no longer held."])
    }

    func testARecentListKeepsTheLastTenNewestFirst() {
        var t = NoticeTracker()
        _ = t.next(read([]), now: t0, isOn: { _ in false })
        for i in 0..<14 { _ = t.next(read([notice("n\(i)", kind: "lane_failed", text: "event \(i)")]), now: t0.addingTimeInterval(Double(i)), isOn: { _ in false }) }
        XCTAssertEqual(t.recent.count, 10)
        XCTAssertEqual(t.recent.first?.text, "event 13")
    }

    func testSettingsAreTheSharedOnesAndDefaultWithoutThem() {
        XCTAssertEqual(NoticeKinds.all.filter { NoticeSettings.isOn($0.kind, nil) }.map(\.kind), ["needs_you", "limit", "supervisor_gone", "host"])
        XCTAssertEqual(NoticeKinds.all.last?.label, "This computer's session or load", "host can be switched in the menu")
        let shared = PanelNotify(kinds: ["needs_you": false, "disk": true])
        XCTAssertFalse(NoticeSettings.isOn("needs_you", shared))
        XCTAssertTrue(NoticeSettings.isOn("disk", shared))
        XCTAssertTrue(NoticeSettings.isOn("limit", shared), "a kind the file does not name follows its default")
        XCTAssertEqual(NoticeSettings.map(shared)["supervisor_gone"], true)
    }

    func testTheMenuWritesTheSharedSettingsThroughTheCLIOnly() {
        XCTAssertEqual(MenuBuilder.arguments(.notifySet(kind: "disk", on: true)), ["panel", "notify", "set", "--kind", "disk", "--on"])
        XCTAssertEqual(MenuBuilder.arguments(.notifySet(kind: "limit", on: false)), ["panel", "notify", "set", "--kind", "limit", "--off"])
        XCTAssertNil(MenuBuilder.arguments(.notifySet(kind: "--on", on: true)), "only a known kind")
        XCTAssertNil(MenuBuilder.arguments(.notifySet(kind: "disk; rm", on: true)))
        XCTAssertEqual(MenuBuilder.arguments(.notifyHeartbeat(notifying: true, pid: 4242)), ["panel", "notify", "heartbeat", "--notifying=true", "--pid=4242"])
        XCTAssertEqual(MenuBuilder.arguments(.notifyHeartbeat(notifying: false, pid: 4242)), ["panel", "notify", "heartbeat", "--notifying=false", "--pid=4242"])
        XCTAssertNil(MenuBuilder.arguments(.notifyHeartbeat(notifying: false, pid: 0)), "a beat always names the menu that wrote it")
    }

    func testAGroupIsToldByItsRealTotalNotOnlyByWhatIsNew() {
        var t = NoticeTracker()
        let old = (1...4).map { notice("l\($0)", kind: "limit", loop: "l00000000000\($0)", name: "example-\($0)", text: "example-\($0) stopped.", group: "limit.x", groupText: "{n} loops stopped: usage limit.") }
        _ = t.next(read(old), now: t0, isOn: on)
        let one = notice("l5", kind: "limit", loop: "l000000000005", name: "example-5", text: "example-5 stopped.", group: "limit.x", groupText: "{n} loops stopped: usage limit.")
        let posted = t.next(read(old + [one]), now: t0, isOn: on)
        XCTAssertEqual(posted.map(\.body), ["5 loops stopped: usage limit."], "one new loop joined four: the group's total, not 1")
        XCTAssertNil(posted.first?.loop)
    }

    func testAnUrgentQuestionThatBeganWhileTheMenuWasDownIsToldOnceAtStart() {
        let down = t0.addingTimeInterval(-3600)
        let ask = PanelNotice(id: "a", kind: "needs_you", severity: "urgent", loop: "l000000000001", name: "example-api", text: "example-api is asking you a question.",
                              since: ISO8601DateFormatter().string(from: t0.addingTimeInterval(-600)), group: "needs_you.asked", groupText: "{n} loops are asking you a question.")
        let before = PanelNotice(id: "b", kind: "needs_you", severity: "urgent", loop: "l000000000002", name: "example-docs", text: "example-docs is asking you a question.",
                                 since: ISO8601DateFormatter().string(from: t0.addingTimeInterval(-7200)), group: "needs_you.asked", groupText: "{n} loops are asking you a question.")
        let held = notice("h", kind: "needs_you")
        func list(_ ns: [PanelNotice], lastRunning: Date?) -> MenuReading {
            let menu = PanelNotifyMenu(notifying: false, lastRunningAt: lastRunning.map { ISO8601DateFormatter().string(from: $0) })
            return .read(PanelList(schemaVersion: 1, registry: "available", complete: true, allClear: false, needsYou: 0, unread: 0, omitted: 0, loops: [], notices: ns, notify: PanelNotify(menu: menu)), at: t0)
        }
        var t = NoticeTracker()
        let first = t.next(list([ask, before, held], lastRunning: down), now: t0, isOn: on)
        XCTAssertEqual(first.map(\.body), ["example-api is asking you a question."], "only what began since the menu last ran, only urgent")
        XCTAssertEqual(t.next(list([ask, before, held], lastRunning: down), now: t0.addingTimeInterval(60), isOn: on), [], "once")
        // Two such questions are one notification, never a storm.
        var u = NoticeTracker()
        let two = PanelNotice(id: "c", kind: "needs_you", severity: "urgent", loop: "l000000000003", name: "example-site", text: "example-site is asking you a question.",
                              since: ISO8601DateFormatter().string(from: t0.addingTimeInterval(-300)), group: "needs_you.asked", groupText: "{n} loops are asking you a question.")
        XCTAssertEqual(u.next(list([ask, two], lastRunning: down), now: t0, isOn: on).map(\.body), ["2 loops are asking you a question."])
        // No record of an earlier run: a baseline, nothing told. A kind that is off is not told.
        var v = NoticeTracker()
        XCTAssertEqual(v.next(list([ask], lastRunning: nil), now: t0, isOn: on), [])
        var w = NoticeTracker()
        XCTAssertEqual(w.next(list([ask], lastRunning: down), now: t0, isOn: { _ in false }), [])
    }

    func testTheMenuListsRecentWithAgeAndTheSettingsAndRunsNothing() {
        let now = t0.addingTimeInterval(600)
        let view = NoticeView(recent: [RecentNotice(text: "example-api is asking you a question.", at: t0.addingTimeInterval(300))], on: ["needs_you": true, "disk": false])
        let rows = MenuBuilder.build(.unavailable, now: now, notices: view).rows
        XCTAssertTrue(rows.contains { $0.title == "Recent" })
        XCTAssertTrue(rows.contains { $0.title == "example-api is asking you a question. · 5 min ago" })
        let parent = rows.first { $0.title == "Notify me when…" }
        XCTAssertEqual(parent?.submenu.count, NoticeKinds.all.count)
        XCTAssertEqual(parent?.submenu.first { $0.title == "A loop needs you" }?.detail, "on")
        XCTAssertEqual(parent?.submenu.first { $0.title == "The disk is low" }?.detail, "off")
        XCTAssertNil(MenuBuilder.arguments(.toggleNoticeKind("disk")))
        // No events yet: no Recent section, but the settings are there.
        let empty = MenuBuilder.build(.unavailable, now: now, notices: NoticeView(recent: [], on: [:])).rows
        XCTAssertFalse(empty.contains { $0.title == "Recent" })
        XCTAssertTrue(empty.contains { $0.title == "Notify me when…" })
    }

    func testTheListDecodesAndAnOlderCLIWithoutOneIsFine() throws {
        let json = """
        {"schemaVersion":1,"readAt":"2030-01-01T12:00:00Z","registry":"available","complete":true,"allClear":true,"needsYou":0,"unread":0,"omitted":0,"loops":[],
         "notices":[{"id":"limit.l1.1","kind":"limit","severity":"high","loop":"l000000000001","name":"example-api","text":"example-api stopped: usage limit.","since":"2030-01-01T11:00:00Z","group":"limit","groupText":"{n} loops stopped: usage limit.","futureField":1}]}
        """
        let list = try JSONDecoder().decode(PanelList.self, from: Data(json.utf8))
        XCTAssertEqual(list.notices?.first?.kind, "limit")
        let old = try JSONDecoder().decode(PanelList.self, from: Data(#"{"schemaVersion":1,"readAt":"2030-01-01T12:00:00Z","registry":"available","complete":true,"allClear":true,"needsYou":0,"unread":0,"omitted":0,"loops":[]}"#.utf8))
        XCTAssertNil(old.notices)
    }

    func testTheMenuSaysItIsNotifyingOnlyWhenTheyHaveAllowedNotifications() {
        // Declined, still being asked, or not read yet: not notifying, so the panel keeps its own banners.
        for p in [NotificationPermission.denied, .notDetermined, .unknown] {
            XCTAssertFalse(HeartbeatPolicy.notifying(running: true, canPost: true, permission: p), "\(p)")
        }
        XCTAssertTrue(HeartbeatPolicy.notifying(running: true, canPost: true, permission: .authorized))
        // Not an app bundle (or turned off), or quitting: not notifying, whatever the permission says.
        XCTAssertFalse(HeartbeatPolicy.notifying(running: true, canPost: false, permission: .authorized))
        XCTAssertFalse(HeartbeatPolicy.notifying(running: false, canPost: true, permission: .authorized))
    }
}
