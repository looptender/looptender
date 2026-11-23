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
    func store() -> NoticeSettings { NoticeSettings(store: MemoryStore()) }

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

    func testSettingsDefaultsAndToggling() {
        let s = store()
        XCTAssertEqual(NoticeKinds.all.filter { s.isOn($0.kind) }.map(\.kind), ["needs_you", "limit", "supervisor_gone"])
        s.toggle("needs_you")
        s.toggle("disk")
        XCTAssertFalse(s.isOn("needs_you"))
        XCTAssertTrue(s.isOn("disk"))
        XCTAssertEqual(s.map["limit"], true)
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
}
