import XCTest
@testable import KeepgoingMenuCore

private final class FixtureUsageWorker: UsageWorkerChild {
    var isRunning = true
    var stops = 0
    func stop() { stops += 1; isRunning = false }
}
final class UsageWorkerTests: XCTestCase {
    func testOnlyOptedInRunningMenuOwnsOneChildAndBoundsRestart() {
        let now = Date(timeIntervalSince1970: 1_900_000_000)
        let first = FixtureUsageWorker(), next = FixtureUsageWorker()
        var starts = 0
        let worker = MenuUsageWorker { starts += 1; return starts == 1 ? first : next }
        worker.update(eligible: false, suspended: false, now: now)
        worker.update(eligible: true, suspended: true, now: now)
        XCTAssertEqual(starts, 0)
        worker.update(eligible: true, suspended: false, now: now)
        worker.update(eligible: true, suspended: false, now: now.addingTimeInterval(60))
        XCTAssertEqual(starts, 1)
        first.isRunning = false
        worker.update(eligible: true, suspended: false, now: now.addingTimeInterval(120))
        XCTAssertEqual(starts, 1)
        worker.update(eligible: true, suspended: false, now: now.addingTimeInterval(300))
        XCTAssertEqual(starts, 2)
        worker.update(eligible: false, suspended: false, now: now.addingTimeInterval(360))
        XCTAssertEqual(next.stops, 1)
    }
    func testSuspensionAndApplicationExitCloseTheOwnedChild() {
        let child = FixtureUsageWorker()
        let worker = MenuUsageWorker { child }
        worker.update(eligible: true, suspended: false, now: Date())
        worker.update(eligible: true, suspended: true, now: Date())
        XCTAssertEqual(child.stops, 1)
        worker.stop()
        XCTAssertEqual(child.stops, 1)
    }
}
